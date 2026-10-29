// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

func TestMailboxDocumentCacheKeepsVerificationPerRead(t *testing.T) {
	f := newMailboxFixture(t)
	const body = "signed immutable bytes"
	tip := mailboxBlobTip(t, f, body)
	first, err := f.mailbox.Read(t.Context(), tip, 1024)
	require.NoError(t, err)
	first[0] = '!'
	before := gitx.GitInvocations()
	second, err := f.mailbox.Read(t.Context(), tip, 1024)
	require.NoError(t, err)
	assert.Equal(t, body, string(second))
	second[0] = '?'
	third, err := f.mailbox.Read(t.Context(), tip, 1024)
	require.NoError(t, err)
	assert.Equal(t, body, string(third), "both insertion and hits isolate returned buffers")
	_, err = f.mailbox.Read(t.Context(), tip, 1)
	assert.Equal(t, ReasonOversize, RejectionReason(err))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = f.mailbox.Read(ctx, tip, 1024)
	assert.ErrorIs(t, err, context.Canceled)
	other, err := NewSigner("another signing context")
	require.NoError(t, err)
	f.mailbox.signer = other
	_, err = f.mailbox.Read(t.Context(), tip, 1024)
	assert.Equal(t, ReasonSignature, RejectionReason(err), "a cached document is authenticated by the current signer")
	f.mailbox.signer = f.signer
	assert.Equal(t, before, gitx.GitInvocations(), "verified immutable bytes need no new Git process")

	for _, mutate := range []func(*ChainTip){
		func(tip *ChainTip) { tip.Kind = MessageResult },
		func(tip *ChainTip) { tip.signature = mailboxBlobTip(t, f, "other bytes").signature },
	} {
		changed := tip
		mutate(&changed)
		before = gitx.GitInvocations()
		for range 2 {
			got, err := f.mailbox.Read(t.Context(), changed, 1024)
			assert.Nil(t, got)
			assert.Equal(t, ReasonSignature, RejectionReason(err))
		}
		assert.Equal(t, before+2, gitx.GitInvocations(), "a changed binding misses, and failures are never cached")
	}
}

func TestMailboxDocumentCacheEvictsAndSkipsOversizedEntries(t *testing.T) {
	f := newMailboxFixture(t)
	tip := mailboxBlobTip(t, f, "first")
	_, err := f.mailbox.Read(t.Context(), tip, 1024)
	require.NoError(t, err)
	for i := range mailboxCacheMaximumEntries {
		key := mailboxDocumentKey{kind: MessageAssignment,
			document: fmt.Sprintf("%040x", i+1), signature: strings.Repeat("f", 40)}
		f.mailbox.rememberDocument(key, []byte("small"), "signature", 0)
	}
	assert.Len(t, f.mailbox.documents.entries, mailboxCacheMaximumEntries)
	assert.LessOrEqual(t, f.mailbox.documents.bytes+mailboxCacheBaseMetadataBytes, mailboxDocumentCacheMaximumBytes)
	before := gitx.GitInvocations()
	_, err = f.mailbox.Read(t.Context(), tip, 1024)
	require.NoError(t, err)
	assert.Equal(t, before+1, gitx.GitInvocations(), "the oldest object must be read again after eviction")

	f.mailbox.Forget()
	for i := range 50 {
		key := mailboxDocumentKey{kind: MessageAssignment,
			document: fmt.Sprintf("%040x", i+1), signature: strings.Repeat("f", 40)}
		f.mailbox.rememberDocument(key, bytes.Repeat([]byte("x"), 20<<10), "signature", 1)
	}
	assert.Less(t, len(f.mailbox.documents.entries), 50, "the byte budget evicts before the entry limit")
	assert.LessOrEqual(t, f.mailbox.documents.bytes+mailboxCacheBaseMetadataBytes, mailboxDocumentCacheMaximumBytes)

	large := strings.Repeat("x", mailboxDocumentCacheMaximumBytes/mailboxCacheAllocationFactor)
	largeTip := mailboxBlobTip(t, f, large)
	before = gitx.GitInvocations()
	for range 2 {
		actual, err := f.mailbox.Read(t.Context(), largeTip, int64(len(large)))
		require.NoError(t, err)
		assert.Equal(t, large, string(actual))
	}
	assert.Equal(t, before+2, gitx.GitInvocations(), "valid documents larger than the cache budget still stream normally")
}

func TestMailboxCachesForgetInFlightGenerations(t *testing.T) {
	f := newMailboxFixture(t)
	tip := mailboxBlobTip(t, f, "signed")
	key := mailboxDocumentKey{kind: tip.Kind, document: tip.document, signature: tip.signature}
	_, documentGeneration, _, err := f.mailbox.documents.find(t.Context(), key)
	require.NoError(t, err)
	oid := strings.Repeat("a", 40)
	_, treeGeneration, _, err := f.mailbox.trees.find(t.Context(), oid)
	require.NoError(t, err)
	f.mailbox.Forget()
	f.mailbox.rememberDocument(key, []byte("signed"), f.signer.Sign(tip.Kind, []byte("signed")), documentGeneration)
	f.mailbox.rememberTree(oid, transportTree{kind: tip.Kind, document: tip.document, signature: tip.signature}, treeGeneration)
	assert.Empty(t, f.mailbox.documents.entries, "a read begun before Forget cannot refill the new cache")
	assert.Empty(t, f.mailbox.trees.entries)
	_, err = f.mailbox.Read(t.Context(), tip, 1024)
	require.NoError(t, err)
	assert.Len(t, f.mailbox.documents.entries, 1)
	f.mailbox.Forget()
	before := gitx.GitInvocations()
	_, err = f.mailbox.Read(t.Context(), tip, 1024)
	require.NoError(t, err)
	assert.Equal(t, before+1, gitx.GitInvocations(), "a rebuilt store is read afresh")
}

func TestMailboxTreeCacheOnlyMemoizesCompleteImmutableTrees(t *testing.T) {
	f := newMailboxFixture(t)
	first := mailboxCacheCommit(t, f, "first")
	second := mailboxCacheCommit(t, f, "second")
	before := gitx.GitInvocations()
	tree, err := f.mailbox.readTree(t.Context(), first)
	require.NoError(t, err)
	again, err := f.mailbox.readTree(t.Context(), first)
	require.NoError(t, err)
	assert.Equal(t, tree, again)
	assert.Equal(t, before+1, gitx.GitInvocations())

	const ref = "refs/heads/moving"
	mailboxCacheGit(t, f, "update-ref", ref, first)
	fromRef, err := f.mailbox.readTree(t.Context(), ref)
	require.NoError(t, err)
	assert.Equal(t, tree, fromRef)
	mailboxCacheGit(t, f, "update-ref", ref, second)
	moved, err := f.mailbox.readTree(t.Context(), ref)
	require.NoError(t, err)
	assert.NotEqual(t, fromRef.document, moved.document, "a mutable ref is resolved on every read")
	for _, expression := range []string{first[:12], first + "^{tree}"} {
		before = gitx.GitInvocations()
		for range 2 {
			_, err := f.mailbox.readTree(t.Context(), expression)
			require.NoError(t, err)
		}
		assert.Equal(t, before+2, gitx.GitInvocations(), "revision expressions and abbreviations bypass the cache")
	}
	plumbing := gitx.NewPlumbing(f.git)
	empty := plumbing.MakeTree(t.Context(), nil)
	documentOnly := plumbing.MakeTree(t.Context(), []gitx.TreeEntry{{Name: "assignment.json", Mode: gitx.TreeModeFile, Type: "blob", OID: tree.document}})
	malformed := plumbing.MakeTree(t.Context(), []gitx.TreeEntry{{Name: messageDir, Mode: gitx.TreeModeDir, Type: "tree", OID: documentOnly}})
	require.NoError(t, plumbing.Err())
	for _, oid := range []string{empty, malformed, strings.Repeat("0", 40)} {
		before = gitx.GitInvocations()
		for range 2 {
			_, _ = f.mailbox.readTree(t.Context(), oid)
		}
		assert.Equal(t, before+2, gitx.GitInvocations(), "empty, malformed and failed listings are not memoized")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = f.mailbox.readTree(ctx, first)
	assert.ErrorIs(t, err, context.Canceled)

	for i := range mailboxCacheMaximumEntries {
		f.mailbox.rememberTree(fmt.Sprintf("%040x", i+1), tree, 0)
	}
	assert.LessOrEqual(t, len(f.mailbox.trees.entries), mailboxCacheMaximumEntries)
	assert.LessOrEqual(t, f.mailbox.trees.bytes+mailboxCacheBaseMetadataBytes, mailboxTreeCacheMaximumBytes)
}

func TestMailboxCacheReadsIgnoreReplacementRefs(t *testing.T) {
	f := newMailboxFixture(t)
	first := mailboxCacheCommit(t, f, "original signed document")
	second := mailboxCacheCommit(t, f, "replacement signed document")
	original, err := f.mailbox.readTree(t.Context(), first)
	require.NoError(t, err)
	replacement, err := f.mailbox.readTree(t.Context(), second)
	require.NoError(t, err)
	firstTree, err := f.git.CommitTreeOID(t.Context(), first)
	require.NoError(t, err)
	secondTree, err := f.git.CommitTreeOID(t.Context(), second)
	require.NoError(t, err)
	for _, pair := range [][2]string{{first, second}, {firstTree, secondTree}, {original.document, replacement.document}, {original.signature, replacement.signature}} {
		mailboxCacheGit(t, f, "replace", pair[0], pair[1])
	}
	assert.Equal(t, "replacement signed document", strings.TrimSpace(mailboxCacheGit(t, f, "cat-file", "blob", original.document)),
		"ordinary source Git still observes replacement refs")
	f.mailbox.Forget()
	for range 2 {
		tree, err := f.mailbox.readTree(t.Context(), first)
		require.NoError(t, err)
		assert.Equal(t, original, tree)
		fromTree, err := f.mailbox.readTree(t.Context(), firstTree)
		require.NoError(t, err)
		assert.Equal(t, original, fromTree)
		actual, err := f.mailbox.Read(t.Context(), ChainTip{Kind: tree.kind, document: tree.document, signature: tree.signature}, 1024)
		require.NoError(t, err)
		assert.Equal(t, "original signed document", string(actual), "both cold and cached reads use raw immutable object identities")
	}
}

func TestMailboxCachesPermitConcurrentReadAndForget(t *testing.T) {
	f := newMailboxFixture(t)
	oid := mailboxCacheCommit(t, f, "concurrent signed document")
	var pending sync.WaitGroup
	for range 8 {
		pending.Go(func() {
			for range 4 {
				tree, err := f.mailbox.readTree(t.Context(), oid)
				if !assert.NoError(t, err) {
					return
				}
				actual, err := f.mailbox.Read(t.Context(), ChainTip{Kind: tree.kind, document: tree.document, signature: tree.signature}, 1024)
				assert.NoError(t, err)
				assert.Equal(t, "concurrent signed document", string(actual))
				f.mailbox.Forget()
			}
		})
	}
	pending.Wait()
}

func mailboxCacheCommit(t testing.TB, f *mailboxFixture, body string) string {
	t.Helper()
	oid, err := f.mailbox.commitMessage(t.Context(), MessageAssignment, []byte(body), nil, nil)
	require.NoError(t, err)
	return oid
}

func mailboxCacheGit(t *testing.T, f *mailboxFixture, args ...string) string {
	t.Helper()
	result, err := exec.Command("git", append([]string{"-C", f.store}, args...)...).CombinedOutput()
	require.NoError(t, err, "%s", result)
	return string(result)
}
