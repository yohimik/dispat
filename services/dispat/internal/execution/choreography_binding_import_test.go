// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVerifiedBindingImportsFromRelativeControlRepository(t *testing.T) {
	source := newMailboxFixture(t)
	target := source.second(t)
	oid, err := source.mailbox.commitMessage(t.Context(), MessageResult, []byte(`{"status":"succeeded"}`), nil, nil)
	require.NoError(t, err)
	t.Chdir(source.store)
	bindings := ChoreographyTaskBindings{}
	input := AssignmentInput{Task: "provider:build", Package: "provider",
		Commit: oid, Digest: "verified-manifest", Path: "sources/provider"}
	require.NoError(t, appendVerifiedChoreographyInput(t.Context(), choreographyVerifiedInputAppend{
		Bindings: &bindings, Input: input, SourceDir: ".",
		Imported: make(map[string]bool), Seen: make(map[choreographyInputDestination]bool),
		ImportCommit: target.git.ImportCommitFrom}))
	imported, err := target.git.ResolveCommit(t.Context(), oid)
	require.NoError(t, err)
	require.Equal(t, oid, imported)
	require.Equal(t, []AssignmentInput{input}, bindings.Inputs)
}
