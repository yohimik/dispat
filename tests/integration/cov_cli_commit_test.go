// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Coverage scenarios: the commit-message gate `dispat commit` installs.
//
// Goal 50 owns the ordinary authoring paths. What is here is the rest of the
// contract: the cleanup modes Git offers that a validated commit has to carry
// out itself, the command lines it refuses before Git touches the index, and
// the hook invocation Git makes — the one place the validator runs as a
// program of its own, with its configuration handed to it through the
// environment. That invocation is dispat's own interface with Git, so its
// refusals are exercised exactly as Git would produce them: a file that is
// not there, a file that is not a file, bytes that are not a configuration.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// scissorsMarker is the line Git writes above the part of a message that is
// for the author's eyes only, spelled with the repository's comment
// character.
func scissorsMarker(comment string) string {
	return comment + " ------------------------ >8 ------------------------"
}

// editorWriting returns an executable editor script that replaces the message
// Git proposes with text, and the path to install as GIT_EDITOR.
func editorWriting(t *testing.T, r *harness.Repo, name, text string) string {
	t.Helper()
	r.WriteFile(name, "#!/bin/sh\ncat > \"$1\" <<'DISPAT_EOF'\n"+text+"\nDISPAT_EOF\n")
	path := r.Path(name)
	require.NoError(t, os.Chmod(path, 0o700))
	return path
}

// TestCovCommitScissorsCleanupCutsAtTheRepositoryCommentCharacter: the
// scissors mode keeps only what is above Git's cut line, and the cut line is
// spelled with whatever comment character the repository configured. Without
// an editor there is nothing below the cut to remove, so the mode degrades to
// the whitespace cleanup rather than guessing.
func TestCovCommitScissorsCleanupCutsAtTheRepositoryCommentCharacter(t *testing.T) {
	t.Run("default comment character", func(t *testing.T) {
		r := authoringRepo(t)
		editor := editorWriting(t, r, "editor.sh",
			"feat(core): above the cut\n\n"+scissorsMarker("#")+"\nDo not put this in the commit.")
		r.WriteFile("tracked.txt", "changed\n")
		r.Git("add", "tracked.txt")

		res := r.CommandEnv([]string{"GIT_EDITOR=" + editor}, "commit", "--edit", "-m", "invalid", "--cleanup=scissors")
		require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		message := r.Git("log", "-1", "--format=%B")
		assert.Equal(t, "feat(core): above the cut", message)
	})

	t.Run("configured comment character", func(t *testing.T) {
		r := authoringRepo(t)
		r.Git("config", "core.commentChar", ";")
		editor := editorWriting(t, r, "editor.sh",
			"feat(core): above the semicolon cut\n\n"+scissorsMarker(";")+"\n; everything here is discarded")
		r.WriteFile("tracked.txt", "changed\n")
		r.Git("add", "tracked.txt")

		res := r.CommandEnv([]string{"GIT_EDITOR=" + editor}, "commit", "--edit", "-m", "invalid", "--cleanup=scissors")
		require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Equal(t, "feat(core): above the semicolon cut", r.Git("log", "-1", "--format=%B"))
	})

	t.Run("configured comment string", func(t *testing.T) {
		r := authoringRepo(t)
		r.Git("config", "core.commentString", "//")
		editor := editorWriting(t, r, "editor.sh",
			"feat(core): above the slash cut\n\n"+scissorsMarker("//")+"\n// discarded")
		r.WriteFile("tracked.txt", "changed\n")
		r.Git("add", "tracked.txt")

		res := r.CommandEnv([]string{"GIT_EDITOR=" + editor}, "commit", "--edit", "-m", "invalid", "--cleanup=scissors")
		require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Equal(t, "feat(core): above the slash cut", r.Git("log", "-1", "--format=%B"))
	})

	t.Run("no editor keeps the whole message", func(t *testing.T) {
		r := authoringRepo(t)
		r.WriteFile("tracked.txt", "changed\n")
		r.Git("add", "tracked.txt")
		body := "feat(core): no editor ran\n\n" + scissorsMarker("#") + "\nstill here"

		res := r.Command("commit", "--cleanup=scissors", "-m", body)
		require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Contains(t, r.Git("log", "-1", "--format=%B"), "still here",
			"with nothing edited there is no cut line to honor")
	})
}

// TestCovCommitRefusesCleanupItCannotCarryOut: a cleanup mode dispat cannot
// reproduce, from the command line or from the repository configuration, is
// refused before Git creates anything — and so is the comment character that
// only Git's own editor could resolve.
func TestCovCommitRefusesCleanupItCannotCarryOut(t *testing.T) {
	t.Run("unknown mode on the command line", func(t *testing.T) {
		r := authoringRepo(t)
		before := r.Git("rev-parse", "HEAD")
		r.WriteFile("tracked.txt", "changed\n")
		r.Git("add", "tracked.txt")

		res := r.Command("commit", "-m", "feat(core): refused", "--cleanup=tidy")
		assert.NotEqual(t, 0, res.Code)
		assert.Contains(t, res.Stdout+res.Stderr, "unsupported git cleanup mode")
		assert.Equal(t, before, r.Git("rev-parse", "HEAD"))
	})

	t.Run("unknown mode in the repository configuration", func(t *testing.T) {
		r := authoringRepo(t)
		r.Git("config", "commit.cleanup", "tidy")
		before := r.Git("rev-parse", "HEAD")
		r.WriteFile("tracked.txt", "changed\n")
		r.Git("add", "tracked.txt")

		res := r.Command("commit", "-m", "feat(core): refused")
		assert.NotEqual(t, 0, res.Code)
		assert.Contains(t, res.Stdout+res.Stderr, "unsupported git cleanup mode")
		assert.Equal(t, before, r.Git("rev-parse", "HEAD"))
	})

	t.Run("automatic comment character", func(t *testing.T) {
		r := authoringRepo(t)
		r.Git("config", "core.commentChar", "auto")
		before := r.Git("rev-parse", "HEAD")
		r.WriteFile("tracked.txt", "changed\n")
		r.Git("add", "tracked.txt")

		res := r.Command("commit", "-m", "feat(core): refused")
		assert.NotEqual(t, 0, res.Code)
		assert.Contains(t, res.Stdout+res.Stderr, "explicit core.commentChar")
		assert.Equal(t, before, r.Git("rev-parse", "HEAD"))
	})

	t.Run("automatic comment character with a mode that strips no comments", func(t *testing.T) {
		r := authoringRepo(t)
		r.Git("config", "core.commentChar", "auto")
		r.WriteFile("tracked.txt", "changed\n")
		r.Git("add", "tracked.txt")

		res := r.Command("commit", "-m", "feat(core): verbatim is safe", "--cleanup=verbatim")
		require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Equal(t, "feat(core): verbatim is safe", r.Git("log", "-1", "--format=%B"))
	})
}

// TestCovCommitRefusesCommandLinesItCannotForward: a Git option dispat does
// not know how to reason about, and an option missing the value it needs,
// are refused with nothing created and the staged diff untouched.
func TestCovCommitRefusesCommandLinesItCannotForward(t *testing.T) {
	for _, tc := range []struct {
		name, script, want string
	}{
		{"unknown long option", "dispat commit --squash-into HEAD -m 'feat(core): x'",
			"unsupported authoring flag --squash-into"},
		{"unknown short option in a cluster", "dispat commit -aZ -m 'feat(core): x'",
			"unsupported authoring flag -Z"},
		{"long option with no value", "dispat commit --message", "--message requires a value"},
		{"short option with no value", "dispat commit -m", "-m requires a value"},
		{"cleanup with no value", "dispat commit --amend --cleanup", "--cleanup requires a value"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := authoringRepo(t)
			before := r.Git("rev-parse", "HEAD")
			r.WriteFile("tracked.txt", "changed\n")
			r.Git("add", "tracked.txt")
			staged := r.Git("diff", "--cached")

			res := r.Shell(tc.script)
			assert.NotEqual(t, 0, res.Code)
			assert.Contains(t, res.Stdout+res.Stderr, tc.want)
			assert.Equal(t, before, r.Git("rev-parse", "HEAD"), "nothing was committed")
			assert.Equal(t, staged, r.Git("diff", "--cached"), "the index is untouched")
		})
	}
}

// validatorCall renders the hook invocation Git makes: the binary under test
// run as the commit-msg gate, with the private parser configuration and the
// cleanup mode handed over in the environment exactly as the installed hook
// hands them over.
func validatorCall(env map[string]string, args ...string) string {
	pairs := make([]string, 0, len(env)+1)
	pairs = append(pairs, "DISPAT_INTERNAL_COMMIT_VALIDATE=1")
	for _, key := range sortedKeys(env) {
		pairs = append(pairs, key+"="+harness.ShQuote(env[key]))
	}
	quoted := make([]string, 0, len(args))
	for _, a := range args {
		quoted = append(quoted, harness.ShQuote(a))
	}
	return strings.Join(pairs, " ") + " dispat " + strings.Join(quoted, " ")
}

// sortedKeys is the deterministic order the environment pairs are rendered
// in, so one scenario's command line is the same on every run.
func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// TestCovCommitMessageGateRefusesUnusableHookInput: the commit-msg gate is a
// program Git runs with one argument and a private configuration in the
// environment. Every way that contract can be broken is refused with a
// sentence naming the input, and a message it accepts is rewritten in place.
func TestCovCommitMessageGateRefusesUnusableHookInput(t *testing.T) {
	r := authoringRepo(t)
	r.WriteFile("parser.json", `{"StrictTypes":true}`)
	parser := r.Path("parser.json")
	r.WriteFile("message.txt", "feat(core): a valid message\n")
	message := r.Path("message.txt")
	require.NoError(t, os.MkdirAll(r.Path("adirectory"), 0o755))
	directory := r.Path("adirectory")

	t.Run("no message file", func(t *testing.T) {
		res := r.Shell(validatorCall(map[string]string{"DISPAT_COMMIT_PARSER": parser}))
		assert.Equal(t, 1, res.Code)
		assert.Contains(t, res.Stderr, "requires one message file")
	})

	t.Run("two message files", func(t *testing.T) {
		res := r.Shell(validatorCall(map[string]string{"DISPAT_COMMIT_PARSER": parser}, message, message))
		assert.Equal(t, 1, res.Code)
		assert.Contains(t, res.Stderr, "requires one message file")
	})

	t.Run("parser configuration missing", func(t *testing.T) {
		res := r.Shell(validatorCall(map[string]string{
			"DISPAT_COMMIT_PARSER": r.Path("absent.json"),
		}, message))
		assert.Equal(t, 1, res.Code)
		assert.Contains(t, res.Stderr, "read parser configuration")
	})

	t.Run("parser configuration is a directory", func(t *testing.T) {
		res := r.Shell(validatorCall(map[string]string{"DISPAT_COMMIT_PARSER": directory}, message))
		assert.Equal(t, 1, res.Code)
		assert.Contains(t, res.Stderr, "must be a regular file")
	})

	t.Run("parser configuration is not JSON", func(t *testing.T) {
		r.WriteFile("broken.json", "{not json")
		res := r.Shell(validatorCall(map[string]string{"DISPAT_COMMIT_PARSER": r.Path("broken.json")}, message))
		assert.Equal(t, 1, res.Code)
		assert.Contains(t, res.Stderr, "decode parser configuration")
	})

	t.Run("parser configuration exceeds the bound", func(t *testing.T) {
		r.WriteFile("huge.json", `{"Separator":"`+strings.Repeat("x", 1<<20)+`"}`)
		res := r.Shell(validatorCall(map[string]string{"DISPAT_COMMIT_PARSER": r.Path("huge.json")}, message))
		assert.Equal(t, 1, res.Code)
		assert.Contains(t, res.Stderr, "commit input exceeds")
	})

	t.Run("parser configuration the parser itself refuses", func(t *testing.T) {
		r.WriteFile("unusable.json", `{"Separator":"-"}`)
		res := r.Shell(validatorCall(map[string]string{"DISPAT_COMMIT_PARSER": r.Path("unusable.json")}, message))
		assert.Equal(t, 1, res.Code)
		assert.Contains(t, res.Stderr, "cannot configure commit-message validator")
	})

	t.Run("message file missing", func(t *testing.T) {
		res := r.Shell(validatorCall(map[string]string{"DISPAT_COMMIT_PARSER": parser}, r.Path("absent.txt")))
		assert.Equal(t, 1, res.Code)
		assert.Contains(t, res.Stderr, "clean commit message")
	})

	t.Run("message file is a directory", func(t *testing.T) {
		res := r.Shell(validatorCall(map[string]string{"DISPAT_COMMIT_PARSER": parser}, directory))
		assert.Equal(t, 1, res.Code)
		assert.Contains(t, res.Stderr, "clean commit message")
	})

	t.Run("an accepted message is rewritten in place", func(t *testing.T) {
		r.WriteFile("accepted.txt", "feat(core): accepted\n\n\n")
		accepted := r.Path("accepted.txt")
		res := r.Shell(validatorCall(map[string]string{
			"DISPAT_COMMIT_PARSER":     parser,
			"DISPAT_COMMIT_LOG_LEVEL":  "debug",
			"DISPAT_COMMIT_LOG_FORMAT": "json",
		}, accepted))
		require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Equal(t, "feat(core): accepted\n", readRepoFile(t, r, "accepted.txt"),
			"the whitespace cleanup is written back to the file Git handed over")
		assert.Contains(t, res.Stderr, "commit message validated")
	})

	t.Run("a verbatim message keeps its own bytes", func(t *testing.T) {
		r.WriteFile("verbatim.txt", "feat(core): verbatim\n\n\n")
		res := r.Shell(validatorCall(map[string]string{
			"DISPAT_COMMIT_PARSER":  parser,
			"DISPAT_COMMIT_CLEANUP": "verbatim",
		}, r.Path("verbatim.txt")))
		require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Equal(t, "feat(core): verbatim\n\n\n", readRepoFile(t, r, "verbatim.txt"))
	})

	t.Run("a refused message leaves the file alone", func(t *testing.T) {
		r.WriteFile("refused.txt", "not conventional\n")
		res := r.Shell(validatorCall(map[string]string{"DISPAT_COMMIT_PARSER": parser}, r.Path("refused.txt")))
		assert.Equal(t, 1, res.Code)
		assert.Equal(t, "not conventional\n", readRepoFile(t, r, "refused.txt"))
	})

	t.Run("the rewrite is refused when the folder cannot take a temporary file", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("a read-only folder does not stop the superuser")
		}
		locked := r.Path("locked")
		require.NoError(t, os.MkdirAll(locked, 0o755))
		target := filepath.Join(locked, "message.txt")
		require.NoError(t, os.WriteFile(target, []byte("feat(core): accepted\n\n"), 0o644))
		require.NoError(t, os.Chmod(locked, 0o555))
		t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

		res := r.Shell(validatorCall(map[string]string{"DISPAT_COMMIT_PARSER": parser}, target))
		assert.Equal(t, 1, res.Code)
		assert.Contains(t, res.Stderr, "write commit message")
	})
}

// TestCovCommitAuthoringHandlesTheRepositoryItFinds: what an authoring commit
// reads from the repository before it installs its gate — the Git
// configuration it must reproduce, and the hooks it must keep running — is
// bounded and tolerant. An oversized configuration value is refused rather
// than truncated, a repository with no hooks folder needs none, and a folder
// among the hooks is not a hook.
func TestCovCommitAuthoringHandlesTheRepositoryItFinds(t *testing.T) {
	t.Run("an oversized configuration value", func(t *testing.T) {
		r := authoringRepo(t)
		before := r.Git("rev-parse", "HEAD")
		r.Git("config", "commit.cleanup", strings.Repeat("x", 70<<10))
		r.WriteFile("tracked.txt", "changed\n")
		r.Git("add", "tracked.txt")

		res := r.Command("commit", "-m", "feat(core): refused")
		assert.NotEqual(t, 0, res.Code)
		assert.Contains(t, res.Stdout+res.Stderr, "exceeds 64 KiB")
		assert.Equal(t, before, r.Git("rev-parse", "HEAD"))
	})

	t.Run("a repository with no hooks folder", func(t *testing.T) {
		r := authoringRepo(t)
		require.NoError(t, os.RemoveAll(r.Path(".git", "hooks")))
		r.WriteFile("tracked.txt", "changed\n")
		r.Git("add", "tracked.txt")

		res := r.Command("commit", "-m", "feat(core): no hooks to copy")
		require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Equal(t, "feat(core): no hooks to copy", r.Git("log", "-1", "--format=%B"))
	})

	t.Run("a folder among the hooks", func(t *testing.T) {
		r := authoringRepo(t)
		require.NoError(t, os.MkdirAll(r.Path(".git", "hooks", "helpers"), 0o755))
		r.WriteFile(".git/hooks/helpers/shared.sh", "# not a hook\n")
		r.WriteFile("tracked.txt", "changed\n")
		r.Git("add", "tracked.txt")

		res := r.Command("commit", "-m", "feat(core): folders are not hooks")
		require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Equal(t, "feat(core): folders are not hooks", r.Git("log", "-1", "--format=%B"))
	})

	t.Run("a pathspec written without the boundary", func(t *testing.T) {
		r := authoringRepo(t)
		r.WriteFile("tracked.txt", "changed\n")
		r.WriteFile("other.txt", "not part of this commit\n")
		r.Git("add", "-A")

		res := r.Command("commit", "--message", "feat(core): one path only", "tracked.txt")
		require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Equal(t, "feat(core): one path only", r.Git("log", "-1", "--format=%B"))
		assert.Equal(t, "tracked.txt", r.Git("show", "--name-only", "--format=", "HEAD"))
	})

	t.Run("the long spelling of the bypass switch", func(t *testing.T) {
		r := authoringRepo(t)
		before := r.Git("rev-parse", "HEAD")
		r.WriteFile("tracked.txt", "changed\n")
		r.Git("add", "tracked.txt")

		res := r.Command("commit", "--no-verify", "-m", "feat(core): bypass")
		assert.NotEqual(t, 0, res.Code)
		assert.Contains(t, res.Stdout+res.Stderr, "bypasses commit-msg validation")
		assert.Equal(t, before, r.Git("rev-parse", "HEAD"))
	})
}
