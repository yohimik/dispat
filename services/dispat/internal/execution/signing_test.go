// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSignerRefusesAnEmptySecret: a node that cannot sign cannot take part,
// and finding that out at construction is the difference between a node that
// does not start and a node that starts and rejects everything.
func TestSignerRefusesAnEmptySecret(t *testing.T) {
	signer, err := NewSigner("")

	require.ErrorIs(t, err, ErrNoSecret)
	assert.Nil(t, signer)
}

// TestSignatureCoversTheExactBytes: the signature is over the document as it
// was written, so a single byte changed anywhere in it, a signature changed
// anywhere, a signature that is not hex at all and the same document under
// another secret are all invalid.
func TestSignatureCoversTheExactBytes(t *testing.T) {
	signer, err := NewSigner("hunter2")
	require.NoError(t, err)
	other, err := NewSigner("hunter3")
	require.NoError(t, err)
	document := []byte(`{"protocol":1,"kind":"probe"}`)
	signature := signer.Sign(MessageAssignment, document)

	assert.True(t, signer.IsSignatureValid(MessageAssignment, document, signature))
	assert.Len(t, signature, 64, "a SHA-256 code is 32 bytes as hex")

	for name, tc := range map[string]struct {
		document  []byte
		signature string
	}{
		"a changed document":          {document: []byte(`{"protocol":2,"kind":"probe"}`), signature: signature},
		"whitespace added":            {document: append(document, ' '), signature: signature},
		"a changed signature":         {document: document, signature: "0" + signature[1:]},
		"a truncated signature":       {document: document, signature: signature[:62]},
		"a signature that is not hex": {document: document, signature: "not a signature"},
		"no signature at all":         {document: document, signature: ""},
		"another node's secret":       {document: document, signature: other.Sign(MessageAssignment, document)},
	} {
		t.Run(name, func(t *testing.T) {
			assert.False(t, signer.IsSignatureValid(MessageAssignment, tc.document, tc.signature))
		})
	}
}

// TestSignatureBindsTheKindOfMessage: a signature says what a document is as
// well as who wrote it, so an authentic document republished under another
// message name is not a message.
//
// The pairs below are the dangerous ones. Anybody able to push to a mailbox
// can move bytes from one file to another without knowing the secret, and a
// withdrawal read as an authorization, or a claim read as a result, would be
// exactly the effect the secret exists to prevent.
func TestSignatureBindsTheKindOfMessage(t *testing.T) {
	signer, err := NewSigner("hunter2")
	require.NoError(t, err)
	document := []byte(`{"protocol":1,"kind":"publish","run":"run-1"}`)

	for _, pair := range []struct{ signed, read MessageKind }{
		{MessageCancel, MessageGo},
		{MessageClaim, MessageResult},
		{MessageReady, MessageGo},
		{MessageAssignment, MessageResult},
		{MessageAck, MessageClaim},
		{MessageResult, MessageAssignment},
	} {
		t.Run(string(pair.signed)+" read as "+string(pair.read), func(t *testing.T) {
			signature := signer.Sign(pair.signed, document)

			assert.True(t, signer.IsSignatureValid(pair.signed, document, signature),
				"the message is what it was signed as")
			assert.False(t, signer.IsSignatureValid(pair.read, document, signature),
				"and is nothing else, whatever file name it is found under")
		})
	}
}

// TestProtocolDocumentsRoundTrip: a message written by one node and read by
// another is the same message, field for field, including the ones this build
// leaves empty for a later one to fill.
func TestProtocolDocumentsRoundTrip(t *testing.T) {
	assignment := Assignment{
		Header: Header{
			Protocol: ProtocolVersion, Kind: KindBuild, Run: "run-1", PlanDigest: "digest",
			Task: "core", Attempt: 2, Generation: "generation", Node: "build-a",
			Branch: "dispat-worker-build-a-20260921-build-abc", IssuedAt: "2026-09-21T10:00:00Z",
		},
		Repositories: []AssignmentRepository{{Name: "api", Path: ".links/api", Snapshot: "cafe"}},
		Package:      &AssignmentPackage{Name: "core", Repository: "api", Dir: "packages/core"},
		Frame:        &AssignmentFrame{Login: []string{"login"}, Commands: []string{"build"}},
		Env:          []string{"DISPAT_PACKAGE=core"},
		StaticEnv:    []string{"REGISTRY=$DISPAT_REGISTRY"},
		Shell:        []string{"/bin/sh", "-c"},
		Platforms:    []string{"linux/amd64"},
		Inputs: []AssignmentInput{{Task: "ui:build", Package: "ui",
			Branch: "dispat-worker-build-a-20260921-relay-abc", Commit: "beef",
			Digest: "d1", Path: "packages/ui"}},
		Outputs: []string{"dist"},
		Permits: AssignmentPermits{Publish: true},
		Limits:  TransferLimits{MaxFiles: 10, MaxBytes: 20, MaxManifestBytes: 30},
	}

	document, err := json.Marshal(assignment)
	require.NoError(t, err)
	var read Assignment
	require.NoError(t, json.Unmarshal(document, &read))

	assert.Equal(t, assignment, read)

	result := Result{
		Header: assignment.Header, Assignment: "cafe", Status: StatusFailed,
		FailedPart: "build", Exit: 2,
		Platform: Platform{OS: "linux", Arch: "amd64", Dispat: "1.11.0"},
		Exports:  []ExportedValue{{Name: "IMAGE", Value: "acme/core:1", Source: "core:build"}},
		Outputs: &OutputManifest{
			Protocol: ProtocolVersion, Run: "run-1", PlanDigest: "digest", Task: "core:build",
			Attempt: 2, Generation: "generation", Node: "build-a", Package: "core", Version: "1.2.0",
			Repositories: []ManifestRepository{{Name: "api", Snapshot: "cafe"}},
			Platform:     Platform{OS: "linux", Arch: "amd64", Dispat: "1.11.0"},
			Roots:        []string{"dist"},
			Inputs:       []ManifestInput{{Package: "ui", OutputTree: "f00d", Digest: "d1"}},
			Entries: []ManifestEntry{
				{Path: "dist/a.js", Type: EntryFile, Mode: EntryModeFile, Size: 3, SHA256: "aa"},
				{Path: "dist/link", Type: EntrySymlink, Mode: EntryModeFile, Size: 4, SHA256: "bb", Target: "a.js"},
			},
			Files: 2, Bytes: 7, OutputTree: "feed", Digest: "manifest",
		},
		StrayWrites: 1,
		Report: &NodeReport{Protocol: ProtocolVersion, Dispat: "1.11.0", OS: "linux",
			Arch: "amd64", Capacity: 2, GitVersion: "git version 2.43.0"},
	}
	document, err = json.Marshal(result)
	require.NoError(t, err)
	var readResult Result
	require.NoError(t, json.Unmarshal(document, &readResult))

	assert.Equal(t, result, readResult)
}

// TestBranchNamesRouteAndDoNotRepeat: a coordination branch is addressed to
// one node, carries its date and kind as labels, and is unique.
func TestBranchNamesRouteAndDoNotRepeat(t *testing.T) {
	at := parseTestTime(t, "2026-09-21T23:30:00Z")

	branch := FormatBranch("build-a", KindProbe, at)

	assert.Contains(t, branch, "dispat-worker-build-a-20260921-probe-")
	assert.Len(t, branch, len("dispat-worker-build-a-20260921-probe-")+32)
	assert.NotEqual(t, branch, FormatBranch("build-a", KindProbe, at))
	assert.Equal(t, "refs/heads/dispat-worker-build-a-*", FormatBranchPattern("build-a"))
	// A node whose name is a prefix of another's lists the other's branches
	// too, which is harmless because the signed message decides.
	assert.Equal(t, "dispat-worker-build-", FormatBranchPrefix("build"))
	assert.NotEqual(t, FormatRunID(), FormatRunID())
	assert.Len(t, FormatRunID(), 32)
}
