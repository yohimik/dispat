// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

func TestMailboxReadsSignedBlobsThroughOneProcess(t *testing.T) {
	fixture := newMailboxFixture(t)
	document := "{\"text\":\"" + strings.Repeat("payload", 10000) + "\"}\n"
	tip := mailboxBlobTip(t, fixture, document)

	before := gitx.GitInvocations()
	actual, err := fixture.mailbox.Read(t.Context(), tip, int64(len(document)))
	require.NoError(t, err)
	assert.Equal(t, document, string(actual))
	assert.Equal(t, before+1, gitx.GitInvocations(),
		"the document and its signature share one bounded object-reader process")

	actual[0] = '!'
	again, err := fixture.mailbox.Read(t.Context(), tip, int64(len(document)))
	require.NoError(t, err)
	assert.Equal(t, document, string(again), "returned bytes do not become later input")
	refused, err := fixture.mailbox.Read(t.Context(), tip, int64(len(document)-1))
	assert.Nil(t, refused)
	assert.Equal(t, ReasonOversize, RejectionReason(err), "every read applies its own ceiling")
}

func TestMailboxBatchReadRefusesInvalidObjects(t *testing.T) {
	fixture := newMailboxFixture(t)
	document := "{\"answer\":42}\n"
	tip := mailboxBlobTip(t, fixture, document)
	plumbing := gitx.NewPlumbing(fixture.git)
	invalidSignature := plumbing.HashObject(t.Context(), strings.NewReader("not-hex"))
	oversizeSignature := plumbing.HashObject(t.Context(), strings.NewReader(strings.Repeat("a", maxSignatureBytes+1)))
	tree := plumbing.MakeTree(t.Context(), nil)
	require.NoError(t, plumbing.Err())

	for _, test := range []struct {
		name      string
		document  string
		signature string
		kind      MessageKind
		limit     int64
		reason    RejectReason
	}{
		{name: "document exceeds its ceiling", document: tip.document, signature: tip.signature, kind: tip.Kind, limit: 1, reason: ReasonOversize},
		{name: "signature exceeds its ceiling", document: tip.document, signature: oversizeSignature, kind: tip.Kind, limit: 1024, reason: ReasonOversize},
		{name: "signature is malformed", document: tip.document, signature: invalidSignature, kind: tip.Kind, limit: 1024, reason: ReasonSignature},
		{name: "kind was changed", document: tip.document, signature: tip.signature, kind: MessageResult, limit: 1024, reason: ReasonSignature},
		{name: "document is a tree", document: tree, signature: tip.signature, kind: tip.Kind, limit: 1024},
		{name: "signature is a tree", document: tip.document, signature: tree, kind: tip.Kind, limit: 1024},
		{name: "document is missing", document: strings.Repeat("0", len(tip.document)), signature: tip.signature, kind: tip.Kind, limit: 1024},
		{name: "signature is missing", document: tip.document, signature: strings.Repeat("0", len(tip.signature)), kind: tip.Kind, limit: 1024},
	} {
		t.Run(test.name, func(t *testing.T) {
			invalid := tip
			invalid.document, invalid.signature, invalid.Kind = test.document, test.signature, test.kind
			actual, err := fixture.mailbox.Read(t.Context(), invalid, test.limit)
			require.Error(t, err)
			assert.Nil(t, actual, "an incomplete or unauthenticated pair returns no document")
			if test.reason != "" {
				assert.Equal(t, test.reason, RejectionReason(err))
			}
			actual, err = fixture.mailbox.Read(t.Context(), tip, 1024)
			require.NoError(t, err, "a refused read leaves no shared pipe to poison")
			assert.Equal(t, document, string(actual))
		})
	}
}

func TestMailboxBatchReadRequiresCleanProcessExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the failing Git process fixture uses a POSIX shell")
	}
	fixture := newMailboxFixture(t)
	tip := mailboxBlobTip(t, fixture, "{\"answer\":42}\n")
	realGit, err := exec.LookPath("git")
	require.NoError(t, err)
	bin := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\n\"$DISPAT_TEST_REAL_GIT\" \"$@\"\nexit 73\n"), 0o755))
	t.Setenv("DISPAT_TEST_REAL_GIT", realGit)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	actual, err := fixture.mailbox.Read(t.Context(), tip, 1024)
	require.Error(t, err)
	assert.Nil(t, actual, "valid signed bytes are not accepted from a failed process")
	assert.Contains(t, err.Error(), "exit status 73")
}

func TestMailboxBatchReadHonorsCancellation(t *testing.T) {
	fixture := newMailboxFixture(t)
	tip := mailboxBlobTip(t, fixture, "{\"answer\":42}\n")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	actual, err := fixture.mailbox.Read(ctx, tip, 1024)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Nil(t, actual)
	again, err := fixture.mailbox.Read(t.Context(), tip, 1024)
	require.NoError(t, err)
	assert.Equal(t, "{\"answer\":42}\n", string(again))
}

func TestMailboxBatchReadsAreIndependent(t *testing.T) {
	fixture := newMailboxFixture(t)
	const count = 8
	tips := make([]ChainTip, count)
	documents := make([]string, count)
	for i := range count {
		documents[i] = fmt.Sprintf("{\"index\":%d}\n", i)
		tips[i] = mailboxBlobTip(t, fixture, documents[i])
	}
	var pending sync.WaitGroup
	for i := range count {
		pending.Go(func() {
			actual, err := fixture.mailbox.Read(t.Context(), tips[i], 1024)
			assert.NoError(t, err)
			assert.Equal(t, documents[i], string(actual), "concurrent readers must not share a response stream")
		})
	}
	pending.Wait()
}

func mailboxBlobTip(t *testing.T, fixture *mailboxFixture, document string) ChainTip {
	t.Helper()
	plumbing := gitx.NewPlumbing(fixture.git)
	tip := ChainTip{Kind: MessageAssignment,
		document:  plumbing.HashObject(t.Context(), strings.NewReader(document)),
		signature: plumbing.HashObject(t.Context(), strings.NewReader(fixture.signer.Sign(MessageAssignment, []byte(document))+"\n")),
	}
	require.NoError(t, plumbing.Err())
	return tip
}
