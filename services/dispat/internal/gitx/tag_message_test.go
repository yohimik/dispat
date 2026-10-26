// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package gitx

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTagMessageRequiresExactLocalAnnotatedTag(t *testing.T) {
	root, git := initRepo(t)
	const releaseTag = "api-v1.2.3"
	const releaseMessage = "release api-v1.2.3"
	require.NoError(t, git.CreateTag(t.Context(), releaseTag, releaseMessage, ""))
	message, err := git.TagMessage(t.Context(), releaseTag)
	require.NoError(t, err)
	require.Equal(t, releaseMessage+"\n", message)
	runGit(t, root, "tag", "lightweight", "HEAD")
	_, err = git.TagMessage(t.Context(), "lightweight")
	require.ErrorContains(t, err, "not an annotated tag")
	head, err := git.HeadSHA(t.Context())
	require.NoError(t, err)
	wrongName := "object " + head + "\ntype commit\ntag another-release\n" +
		"tagger Test <test@example.com> 0 +0000\n\nrelease forged\n"
	cmd := exec.Command("git", "-C", root, "hash-object", "-t", "tag", "-w", "--stdin")
	cmd.Stdin = strings.NewReader(wrongName)
	encoded, err := cmd.Output()
	require.NoError(t, err)
	runGit(t, root, "update-ref", "refs/tags/forged", strings.TrimSpace(string(encoded)))
	_, err = git.TagMessage(t.Context(), "forged")
	require.ErrorContains(t, err, "another tag identity")
}
