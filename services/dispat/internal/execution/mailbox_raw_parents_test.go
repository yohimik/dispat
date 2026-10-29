// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

func TestMailboxMissingParentCannotMasqueradeAsProbeRoot(t *testing.T) {
	fixture := newMailboxFixture(t)
	root, err := fixture.mailbox.commitMessage(t.Context(), MessageAssignment, []byte(`{"probe":"root"}`), nil, nil)
	require.NoError(t, err)
	tip, err := fixture.mailbox.readChainTip(t.Context(), "probe", root)
	require.NoError(t, err)
	require.Equal(t, MessageAssignment, tip.Kind)
	require.Empty(t, tip.PreviousOID, "a real parentless probe remains a root")
	tree, err := fixture.git.CommitTreeOID(t.Context(), root)
	require.NoError(t, err)
	missing := strings.Repeat("e", len(root))
	content := "tree " + tree + "\nparent " + missing + "\n" +
		"author Test <test@example.test> 0 +0000\ncommitter Test <test@example.test> 0 +0000\n\nmissing parent\n"
	cmd := exec.Command("git", "-C", fixture.store, "hash-object", "--literally", "-t", "commit", "-w", "--stdin")
	cmd.Stdin = strings.NewReader(content)
	result, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", result)
	broken := strings.TrimSpace(string(result))
	_, err = fixture.mailbox.readChainTip(t.Context(), "probe", broken)
	require.Error(t, err, "the signed message cannot turn an unreadable parent into a root")
	ancestors, err := fixture.mailbox.InspectChain(t.Context(), gitx.RemoteHead{Name: "probe", OID: broken})
	require.Error(t, err)
	require.Nil(t, ancestors, "a partial chain is not accepted as a complete empty chain")
}
