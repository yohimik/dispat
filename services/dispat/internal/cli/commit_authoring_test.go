package cli

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSplitGitCommitArgsPreservesGitArgvAndExtractsGlobals(t *testing.T) {
	parsed, gitArgs, authoring, err := splitGitCommitArgs([]string{
		"commit", "-m", "feat(core): x", "--allow-empty", "--root", "/repo",
	})
	require.NoError(t, err)
	assert.True(t, authoring)
	assert.Equal(t, []string{"commit", "--root", "/repo"}, parsed)
	assert.Equal(t, []string{"-m", "feat(core): x", "--allow-empty"}, gitArgs)
}

func TestCommitHelpDescribesBothModesWithoutGitFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"commit", "--help"}, &stdout, &stderr)
	require.Equal(t, 0, code)
	help := stderr.String()
	assert.Contains(t, help, "-m/--message")
	assert.Contains(t, help, "-F/--file")
	assert.Contains(t, help, "--amend")
	assert.Contains(t, help, "--tag")
	assert.NotContains(t, help, "--git")
}

func TestSplitGitCommitArgsDoesNotInterpretMessageValues(t *testing.T) {
	for _, value := range []string{"-m", "-n", "--dry-run", "--tag", "--root"} {
		_, gitArgs, authoring, err := splitGitCommitArgs([]string{"commit", "-m", value})
		require.NoError(t, err, value)
		assert.True(t, authoring, value)
		assert.Equal(t, []string{"-m", value}, gitArgs, value)
	}
}

func TestSplitGitCommitArgsDoesNotInterpretOtherGitOptionValues(t *testing.T) {
	for _, option := range []string{"--author", "--date", "--trailer", "--pathspec-from-file", "--cleanup"} {
		_, gitArgs, authoring, err := splitGitCommitArgs([]string{"commit", option, "--root", "-m", "feat(core): x"})
		require.NoError(t, err, option)
		assert.True(t, authoring, option)
		assert.Equal(t, []string{option, "--root", "-m", "feat(core): x"}, gitArgs, option)
	}
	_, _, authoring, err := splitGitCommitArgs([]string{"commit", "-Ssigning-email"})
	require.NoError(t, err)
	assert.False(t, authoring)
	_, _, authoring, err = splitGitCommitArgs([]string{"commit", "-ttemplate-with-message"})
	require.NoError(t, err)
	assert.False(t, authoring)
}

func TestSplitGitCommitArgsRejectsReleaseMixture(t *testing.T) {
	_, _, _, err := splitGitCommitArgs([]string{"commit", "--tag", "-m", "feat(core): x"})
	assert.ErrorContains(t, err, "release-step flag --tag")
}

func TestSplitGitCommitArgsLeavesLegacyCommitUntouched(t *testing.T) {
	args := []string{"commit", "--tag", "--push"}
	parsed, gitArgs, authoring, err := splitGitCommitArgs(args)
	require.NoError(t, err)
	assert.False(t, authoring)
	assert.Nil(t, gitArgs)
	assert.Equal(t, args, parsed)
}

func TestSplitGitCommitArgsNeverTreatsAFlagValueAsTheCommand(t *testing.T) {
	for _, flag := range []string{"--package", "--space", "--group", "--name", "--config", "--root"} {
		args := []string{flag, "commit", "-m", "feat(core): must not run"}
		parsed, gitArgs, authoring, err := splitGitCommitArgs(args)
		require.NoError(t, err, flag)
		assert.False(t, authoring, flag)
		assert.Nil(t, gitArgs, flag)
		assert.Equal(t, args, parsed, flag)
	}
}

func TestSplitGitCommitArgsRejectsReleaseFlagsBeforeAuthoringCommand(t *testing.T) {
	for _, args := range [][]string{
		{"--package", "core", "commit", "-m", "feat(core): x"},
		{"--name", "Ada", "commit", "-m", "feat(core): x"},
	} {
		_, _, _, err := splitGitCommitArgs(args)
		assert.ErrorContains(t, err, "cannot be combined with an authoring commit")
	}
	_, gitArgs, authoring, err := splitGitCommitArgs([]string{"--log-format", "json", "commit", "-m", "feat(core): x"})
	require.NoError(t, err)
	assert.True(t, authoring)
	assert.Equal(t, []string{"-m", "feat(core): x"}, gitArgs)
}

func TestSplitGitCommitArgsRecognizesClusterAndPreservesPathBoundary(t *testing.T) {
	parsed, gitArgs, authoring, err := splitGitCommitArgs([]string{
		"commit", "-am", "feat(core): clustered", "--", "--root", "-m",
	})
	require.NoError(t, err)
	assert.True(t, authoring)
	assert.Equal(t, []string{"commit"}, parsed)
	assert.Equal(t, []string{"-am", "feat(core): clustered", "--", "--root", "-m"}, gitArgs)
	_, gitArgs, _, err = splitGitCommitArgs([]string{"commit", "-am", "--root"})
	require.NoError(t, err)
	assert.Equal(t, []string{"-am", "--root"}, gitArgs)
}
