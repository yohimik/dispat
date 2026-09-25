// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Coverage scenarios: the command lines dispat refuses to run at all.
//
// A usage mistake must cost the reader a usage message, not a configuration
// error two phases later, and it must be told apart from a run that failed:
// dispat exits 2 for "this command line does not mean anything" and 1 for
// "the work did not succeed". Every arity rule, every flag that belongs to
// another command, and every enumerated flag value is a place those two can
// be confused, so each is driven through the binary and each asserts the
// exit code as well as the sentence.

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// usageRepo is a loadable single-package repository: every refusal here is
// about the command line, so the configuration must never be the reason.
func usageRepo(t *testing.T) *harness.Repo {
	t.Helper()
	r := singlePackageRepo(t, echoBuild)
	r.Commit("feat(core): bootstrap")
	return r
}

// TestCovUsageRefusalsExitTwo: each of these command lines is refused before
// anything runs, with the sentence naming what was wrong and the usage exit
// code telling a mistyped command apart from a failed one.
func TestCovUsageRefusalsExitTwo(t *testing.T) {
	r := usageRepo(t)
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"unknown log level", []string{"diagnostics", "--log-level", "loud", "feat(core): x"},
			"unknown --log-level value"},
		{"unknown log format", []string{"diagnostics", "--log-format", "yaml", "feat(core): x"},
			"unknown --log-format value"},
		{"too many if arguments", []string{"if", "A", "B", "C", "--then", "build"},
			"if takes at most one argument"},
		{"trigger with no event", []string{"trigger"}, "trigger requires an event"},
		{"trigger event that is not one word", []string{"trigger", "9deployed"},
			"a triggered event is one word"},
		{"trigger progress with no value", []string{"trigger", "progress"},
			"trigger progress requires its value"},
		{"trigger progress out of range", []string{"trigger", "progress", "150"},
			"whole number between 0 and 100"},
		{"trigger progress that is not a number", []string{"trigger", "progress", "half"},
			"whole number between 0 and 100"},
		{"too many scanner arguments", []string{"scanner", "one", "two"},
			"scanner takes at most one argument"},
		{"run shorthand with extra words", []string{"build", "extra"}, "unexpected arguments"},
		{"rollback with a download flag", []string{"self-update", "--rollback", "--force"},
			"--rollback restores the kept binary"},
		{"rollback naming no tool", []string{"install", "--rollback"},
			"--rollback needs to know which tool"},
		{"unknown manifest format", []string{"writer", "--manifest-format", "toml", "--set-version", "1.0.0", "package.json"},
			"unknown --manifest-format value"},
		{"file condition naming no path", []string{"if", "--file", "", "--then", "build"},
			"names no path"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := r.Command(tc.args...)
			assert.Equal(t, 2, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.Contains(t, res.Stdout+res.Stderr, tc.want)
		})
	}

	t.Run("arguments after a dash-dash with no command", func(t *testing.T) {
		res := r.Shell("dispat -- something")
		assert.Equal(t, 2, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Contains(t, res.Stdout+res.Stderr, "need a command that forwards them")
	})

	t.Run("a flag half the commands share, on one that does not", func(t *testing.T) {
		res := r.Command("init", "--package", "core")
		assert.Equal(t, 2, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Contains(t, res.Stdout+res.Stderr, "is not an init flag")
		assert.Contains(t, res.Stdout+res.Stderr, "--help for its flags",
			"a flag with many owners sends the reader to the command's own help")
	})
}

// TestCovCommitAuthoringKeepsGitsOwnShortOptions: an authoring commit hands
// Git its argv untouched, so Git's short options — the ones that take an
// attached value, the ones that take a following one, and the editor request
// that selects authoring on its own — all survive the split.
func TestCovCommitAuthoringKeepsGitsOwnShortOptions(t *testing.T) {
	t.Run("the editor request alone selects authoring", func(t *testing.T) {
		r := authoringRepo(t)
		editor := editorWriting(t, r, "editor.sh", "feat(core): written by the editor")
		r.WriteFile("tracked.txt", "changed\n")
		r.Git("add", "tracked.txt")

		res := r.CommandEnv([]string{"GIT_EDITOR=" + editor}, "commit", "-e")
		require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Equal(t, "feat(core): written by the editor", r.Git("log", "-1", "--format=%B"))
	})

	t.Run("a template and an attached value are forwarded", func(t *testing.T) {
		r := authoringRepo(t)
		r.WriteFile("template.txt", "template text\n")
		editor := editorWriting(t, r, "editor.sh", "feat(core): template replaced")
		r.WriteFile("tracked.txt", "changed\n")
		r.Git("add", "tracked.txt")

		res := r.CommandEnv([]string{"GIT_EDITOR=" + editor},
			"commit", "-t", r.Path("template.txt"), "-e", "-uno")
		require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Equal(t, "feat(core): template replaced", r.Git("log", "-1", "--format=%B"))
	})

	t.Run("a release-step flag cannot ride along", func(t *testing.T) {
		r := authoringRepo(t)
		before := r.Git("rev-parse", "HEAD")
		r.WriteFile("tracked.txt", "changed\n")
		r.Git("add", "tracked.txt")

		res := r.Shell("dispat commit --tag -m 'feat(core): both at once'")
		assert.NotEqual(t, 0, res.Code)
		assert.Contains(t, res.Stdout+res.Stderr, "cannot be combined with an authoring commit")
		assert.Equal(t, before, r.Git("rev-parse", "HEAD"))
	})

	t.Run("a global flag written inline still applies", func(t *testing.T) {
		r := authoringRepo(t)
		r.WriteFile("tracked.txt", "changed\n")
		r.Git("add", "tracked.txt")

		res := r.Shell("dispat commit --log-format=json -m 'feat(core): inline global'")
		require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Equal(t, "feat(core): inline global", r.Git("log", "-1", "--format=%B"))
	})
}

// TestCovNestedWorkspaceContextIsRefusedWhenItCannotBeRead: an owner-aware
// script runner hands a nested dispat the composed invocation through the
// environment. A context that does not decode is a usage refusal rather than
// a silent fall back to this folder's own configuration, because the two
// would plan different things.
func TestCovNestedWorkspaceContextIsRefusedWhenItCannotBeRead(t *testing.T) {
	r := usageRepo(t)
	script := strings.Join([]string{
		"DISPAT_INTERNAL_WORKSPACE_ROOT=" + harness.ShQuote(r.Root),
		"DISPAT_INTERNAL_WORKSPACE_CONFIG=" + harness.ShQuote(r.Path("dispat.json")),
		"DISPAT_INTERNAL_WORKSPACE_CONFIGS='[not json'",
		"dispat status",
	}, " ")

	res := r.Shell(script)
	assert.Equal(t, 2, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stderr, "invalid nested workspace context")
}
