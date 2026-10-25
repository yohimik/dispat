// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package gitx

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gitAt runs git in root with extra environment pairs and answers its trimmed
// output.
func gitAt(t *testing.T, root string, env []string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return strings.TrimSpace(string(out))
}

func TestTagDateIsTheTaggerDateOfAnAnnotatedTag(t *testing.T) {
	root, cli := initRepo(t)
	gitAt(t, root, []string{"GIT_COMMITTER_DATE=2026-10-24T23:30:00Z"},
		"commit", "-q", "--allow-empty", "-m", "chore(release): core@0.1.0")
	gitAt(t, root, []string{"GIT_COMMITTER_DATE=2026-10-25T00:00:05+04:00"},
		"tag", "-a", "core@0.1.0", "-m", "core@0.1.0")

	date, err := cli.TagDate(context.Background(), "core@0.1.0")
	require.NoError(t, err)
	assert.True(t, date.Equal(time.Date(2026, 10, 24, 20, 0, 5, 0, time.UTC)), "got %s", date)
}

func TestTagDateIsTheCommitDateOfALightweightTag(t *testing.T) {
	root, cli := initRepo(t)
	gitAt(t, root, []string{"GIT_COMMITTER_DATE=2026-10-24T23:30:00Z"},
		"commit", "-q", "--allow-empty", "-m", "chore(release): core@0.1.0")
	gitAt(t, root, []string{"GIT_COMMITTER_DATE=2026-11-01T00:00:00Z"}, "tag", "core@0.1.0")

	date, err := cli.TagDate(context.Background(), "core@0.1.0")
	require.NoError(t, err)
	assert.True(t, date.Equal(time.Date(2026, 10, 24, 23, 30, 0, 0, time.UTC)), "got %s", date)
}

func TestTagDateRefusesAMissingTag(t *testing.T) {
	root, cli := initRepo(t)
	gitAt(t, root, nil, "tag", "core@0.1.0/next")

	_, err := cli.TagDate(context.Background(), "core@0.1.0")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no tag core@0.1.0")
}

func TestFirstParentMessagesWalksTheFirstParentLine(t *testing.T) {
	root, cli := initRepo(t)
	base := gitAt(t, root, nil, "rev-parse", "HEAD")
	gitAt(t, root, nil, "checkout", "-q", "-b", "side")
	gitAt(t, root, nil, "commit", "-q", "--allow-empty", "-m", "fix(core): on the side")
	gitAt(t, root, nil, "checkout", "-q", "-")
	gitAt(t, root, nil, "commit", "-q", "--allow-empty", "-m", "feat(core): planned\n\nWith a body.")
	gitAt(t, root, nil, "merge", "-q", "--no-ff", "-m", "chore: merge side", "side")
	gitAt(t, root, nil, "commit", "-q", "--allow-empty", "-m", "chore(release): core@0.1.0")
	head := gitAt(t, root, nil, "rev-parse", "HEAD")

	commits, err := cli.FirstParentMessages(context.Background(), head, 3)
	require.NoError(t, err)
	messages := make([]string, 0, len(commits))
	for _, commit := range commits {
		messages = append(messages, commit.Message)
	}
	assert.Equal(t, []string{"chore(release): core@0.1.0\n", "chore: merge side\n", "feat(core): planned\n\nWith a body.\n"},
		messages, "the side branch is not on the line, and the walk stops at the limit")
	assert.Equal(t, head, commits[0].SHA)

	all, err := cli.FirstParentMessages(context.Background(), head, 10)
	require.NoError(t, err)
	assert.Len(t, all, 4)
	assert.Equal(t, base, all[3].SHA)
}
