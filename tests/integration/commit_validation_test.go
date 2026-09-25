package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

func authoringRepo(t *testing.T) *harness.Repo {
	r := harness.New(t)
	r.WriteFile("tracked.txt", "initial\n")
	r.Commit("feat(core): initial")
	return r
}

func rawHeadMessage(t *testing.T, r *harness.Repo) string {
	t.Helper()
	out, err := exec.Command("git", "-C", r.Root, "cat-file", "commit", "HEAD").Output()
	require.NoError(t, err)
	parts := strings.SplitN(string(out), "\n\n", 2)
	require.Len(t, parts, 2)
	return parts[1]
}

func TestCommitValidationNaturalMessageAndDefaultParser(t *testing.T) {
	r := authoringRepo(t)
	r.WriteFile("tracked.txt", "changed\n")
	r.Git("add", "tracked.txt")

	res := r.Command("commit", "-m", "feat(core): accepted")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, "feat(core): accepted", r.Git("log", "-1", "--format=%B"))
}

func TestCommitValidationBlocksInvalidMessageBeforeCommit(t *testing.T) {
	r := authoringRepo(t)
	before := r.Git("rev-parse", "HEAD")
	r.WriteFile("tracked.txt", "changed\n")
	r.Git("add", "tracked.txt")
	staged := r.Git("diff", "--cached")

	res := r.Command("commit", "--message=not conventional")
	assert.NotEqual(t, 0, res.Code)
	assert.Equal(t, before, r.Git("rev-parse", "HEAD"))
	assert.Equal(t, staged, r.Git("diff", "--cached"))
	assert.Contains(t, res.Stderr, "commit message refused")
}

func TestCommitValidationUsesConfiguredParserAndFinalCleanup(t *testing.T) {
	r := authoringRepo(t)
	r.WriteFile("packages/core/main.txt", "core\n")
	r.WriteConfig(`{"packages":{"core":{"path":"packages/core"}},"parser":{"types":{"add":"minor"},"strictTypes":true}}`)
	r.WriteFile("tracked.txt", "changed\n")
	r.Git("add", "tracked.txt")
	message := r.Path("message.txt")
	r.WriteFile("message.txt", "# generated comment\nadd(core): configured\n\n# trailing comment\n")

	res := r.Command("commit", "-F", message, "--cleanup=strip")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, "add(core): configured\n", rawHeadMessage(t, r))
}

func TestCommitValidationRunsExistingCommitMessageHookOnceThenValidates(t *testing.T) {
	r := authoringRepo(t)
	hook := r.Path(".git", "hooks", "commit-msg")
	r.WriteFile(".git/hooks/hook-helper", "HOOK_MESSAGE='feat(core): repaired'\n")
	r.WriteFile(".git/hooks/commit-msg", "#!/bin/sh\n. \"$(dirname \"$0\")/hook-helper\"\nprintf 'hook\\n' >> hook-runs\nprintf '%s\\n' \"$HOOK_MESSAGE\" > \"$1\"\n")
	require.NoError(t, os.Chmod(hook, 0o700))
	r.WriteFile("tracked.txt", "changed\n")
	r.Git("add", "tracked.txt")

	res := r.Command("commit", "-m", "invalid")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	runs, err := os.ReadFile(r.Path("hook-runs"))
	require.NoError(t, err)
	assert.Equal(t, "hook\n", string(runs))
	assert.Equal(t, "feat(core): repaired", r.Git("log", "-1", "--format=%B"))
}

func TestCommitValidationRefusesNoVerifyBeforeMutation(t *testing.T) {
	r := authoringRepo(t)
	before := r.Git("rev-parse", "HEAD")
	r.WriteFile("tracked.txt", "changed\n")
	r.Git("add", "tracked.txt")

	res := r.Command("commit", "-m", "feat(core): bypass", "-n")
	assert.NotEqual(t, 0, res.Code)
	assert.Equal(t, before, r.Git("rev-parse", "HEAD"))
	assert.Contains(t, res.Stdout+res.Stderr, "bypasses commit-msg validation")
}

func TestCommitValidationWarningIsVisibleAndDoesNotBlock(t *testing.T) {
	r := authoringRepo(t)
	r.WriteFile("tracked.txt", "changed\n")
	r.Git("add", "tracked.txt")

	res := r.Command("commit", "-m", "unlisted(core): warning")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stderr, "W140")
	assert.Equal(t, "unlisted(core): warning", r.Git("log", "-1", "--format=%B"))
}

func TestCommitValidationHonorsCommitCleanupConfiguration(t *testing.T) {
	r := authoringRepo(t)
	r.Git("config", "commit.cleanup", "strip")
	r.WriteFile("tracked.txt", "changed\n")
	r.Git("add", "tracked.txt")

	res := r.Command("commit", "-m", "# comment", "-m", "feat(core): retained")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, "feat(core): retained", r.Git("log", "-1", "--format=%B"))
}

func TestCommitValidationReadsMessageFromStdin(t *testing.T) {
	r := authoringRepo(t)
	r.WriteFile("tracked.txt", "changed\n")
	r.Git("add", "tracked.txt")

	res := r.CommandInput("fix(core): from stdin\n", "commit", "-F", "-")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, "fix(core): from stdin", r.Git("log", "-1", "--format=%B"))
}

func TestCommitValidationForwardsSupportedGitOptions(t *testing.T) {
	r := authoringRepo(t)

	res := r.Command("commit", "-m", "chore(core): empty record", "--allow-empty")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, "chore(core): empty record", r.Git("log", "-1", "--format=%B"))
}

func TestCommitValidationAmendsThroughGit(t *testing.T) {
	r := authoringRepo(t)
	before := r.Git("rev-parse", "HEAD")

	res := r.Command("commit", "--amend", "--no-edit", "-m", "fix(core): amended")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.NotEqual(t, before, r.Git("rev-parse", "HEAD"))
	assert.Equal(t, "fix(core): amended", r.Git("log", "-1", "--format=%B"))
}

func TestCommitValidationUsesActualEditorForDefaultCleanup(t *testing.T) {
	r := authoringRepo(t)
	editor := r.Path("editor.sh")
	r.WriteFile("editor.sh", "#!/bin/sh\nprintf 'feat(core): edited\\n\\n# editor comment\\n' > \"$1\"\n")
	require.NoError(t, os.Chmod(editor, 0o700))
	r.WriteFile("tracked.txt", "changed\n")
	r.Git("add", "tracked.txt")

	res := r.CommandEnv([]string{"GIT_EDITOR=" + editor}, "commit", "--edit", "-m", "invalid")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, "feat(core): edited", r.Git("log", "-1", "--format=%B"))
}

func TestCommitValidationExplicitWhitespaceKeepsEditorComments(t *testing.T) {
	r := authoringRepo(t)
	editor := r.Path("editor.sh")
	r.WriteFile("editor.sh", "#!/bin/sh\nprintf 'feat(core): edited\\n\\n# retained comment\\n' > \"$1\"\n")
	require.NoError(t, os.Chmod(editor, 0o700))
	r.WriteFile("tracked.txt", "changed\n")
	r.Git("add", "tracked.txt")

	res := r.CommandEnv([]string{"GIT_EDITOR=" + editor}, "commit", "--edit", "-m", "invalid", "--cleanup=whitespace")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, r.Git("log", "-1", "--format=%B"), "# retained comment")
}

func TestCommitValidationClusteredMessageFlag(t *testing.T) {
	r := authoringRepo(t)
	r.WriteFile("tracked.txt", "changed\n")

	res := r.Command("commit", "-am", "fix(core): clustered")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, "fix(core): clustered", r.Git("log", "-1", "--format=%B"))
}

func TestCommitValidationKeepsNestedWorkingDirectorySemantics(t *testing.T) {
	r := harness.New(t)
	r.WriteConfig(`{"packages":{"core":{"path":"packages/core"}}}`)
	r.WriteFile("packages/core/main.txt", "initial\n")
	r.WriteFile("packages/core/message.txt", "fix(core): nested paths\n")
	r.Commit("feat(core): initial")
	r.WriteFile("packages/core/main.txt", "changed\n")
	r.Git("add", "packages/core/main.txt")

	res := r.CommandAt("packages/core", "commit", "-F", "message.txt", "--", "main.txt")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, "fix(core): nested paths", r.Git("log", "-1", "--format=%B"))
	assert.Equal(t, "changed", r.Git("show", "HEAD:packages/core/main.txt"))
}

func TestCommitValidationBlocksOneInvalidUnitInCompleteMessage(t *testing.T) {
	r := authoringRepo(t)
	before := r.Git("rev-parse", "HEAD")
	r.WriteFile("tracked.txt", "changed\n")
	r.Git("add", "tracked.txt")

	res := r.Command("commit", "-m", "feat(core): valid\n\n---\n\ninvalid unit")
	assert.NotEqual(t, 0, res.Code)
	assert.Equal(t, before, r.Git("rev-parse", "HEAD"))
	assert.Contains(t, res.Stderr, "commit message refused")
}

func TestCommitValidationShowsWarningsWhenParserHistoryIsQuiet(t *testing.T) {
	r := authoringRepo(t)
	r.WriteFile("packages/core/main.txt", "core\n")
	r.WriteConfig(`{"packages":{"core":{"path":"packages/core"}},"parser":{"quiet":true}}`)
	r.WriteFile("tracked.txt", "changed\n")
	r.Git("add", "tracked.txt")

	res := r.Command("commit", "-m", "unlisted(core): warning")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stderr, "W140")
}

func TestCommitValidationPreservesOriginalHookFailure(t *testing.T) {
	r := authoringRepo(t)
	before := r.Git("rev-parse", "HEAD")
	hook := r.Path(".git", "hooks", "commit-msg")
	r.WriteFile(".git/hooks/commit-msg", "#!/bin/sh\nprintf 'ran\\n' > hook-failed\nexit 42\n")
	require.NoError(t, os.Chmod(hook, 0o700))
	r.WriteFile("tracked.txt", "changed\n")
	r.Git("add", "tracked.txt")

	res := r.Command("commit", "-m", "feat(core): blocked by hook")
	assert.NotEqual(t, 0, res.Code)
	assert.Equal(t, before, r.Git("rev-parse", "HEAD"))
	data, err := os.ReadFile(r.Path("hook-failed"))
	require.NoError(t, err)
	assert.Equal(t, "ran\n", string(data))
}

func TestCommitValidationUsesCommonHooksFromLinkedWorktree(t *testing.T) {
	r := authoringRepo(t)
	worktree := r.Path("linked")
	r.Git("worktree", "add", "-q", "-b", "validation-worktree", worktree)
	hook := r.Path(".git", "hooks", "commit-msg")
	r.WriteFile(".git/hooks/commit-msg", "#!/bin/sh\nprintf 'feat(core): worktree hook\\n' > \"$1\"\n")
	require.NoError(t, os.Chmod(hook, 0o700))
	require.NoError(t, os.WriteFile(r.Path("linked", "tracked.txt"), []byte("worktree\n"), 0o644))
	cmd := exec.Command("git", "-C", worktree, "add", "tracked.txt")
	require.NoError(t, cmd.Run())

	res := r.CommandAt("linked", "commit", "-m", "invalid")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	out, err := exec.Command("git", "-C", worktree, "log", "-1", "--format=%B").Output()
	require.NoError(t, err)
	assert.Equal(t, "feat(core): worktree hook\n\n", string(out))
}

func TestCommitValidationDryRunDoesNotCommitOrClaimValidation(t *testing.T) {
	r := authoringRepo(t)
	before := r.Git("rev-parse", "HEAD")
	r.WriteFile("tracked.txt", "changed\n")
	r.Git("add", "tracked.txt")

	res := r.Command("commit", "-m", "feat(core): dry run", "--dry-run")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, before, r.Git("rev-parse", "HEAD"))
	assert.NotContains(t, res.Stdout+res.Stderr, "commit message validated")
}

func TestCommitValidationStrictTypesErrorIsVisibleAndBlocking(t *testing.T) {
	r := authoringRepo(t)
	r.WriteFile("packages/core/main.txt", "core\n")
	r.WriteConfig(`{"packages":{"core":{"path":"packages/core"}},"parser":{"strictTypes":true}}`)
	before := r.Git("rev-parse", "HEAD")
	r.WriteFile("tracked.txt", "changed\n")
	r.Git("add", "tracked.txt")

	res := r.Command("commit", "-m", "unlisted(core): rejected")
	assert.NotEqual(t, 0, res.Code)
	assert.Equal(t, before, r.Git("rev-parse", "HEAD"))
	assert.Contains(t, res.Stderr, "E140")
}

func TestCommitValidationMalformedConfigNeverFallsBackToDefaults(t *testing.T) {
	r := authoringRepo(t)
	r.WriteConfig(`{"parser":`)
	before := r.Git("rev-parse", "HEAD")
	r.WriteFile("tracked.txt", "changed\n")
	r.Git("add", "tracked.txt")

	res := r.Command("commit", "-m", "feat(core): must not commit")
	assert.NotEqual(t, 0, res.Code)
	assert.Equal(t, before, r.Git("rev-parse", "HEAD"))
	assert.Contains(t, res.Stderr, "invalid configuration")
}

func TestCommitValidationBoundsMessageFileBeforeCommit(t *testing.T) {
	r := authoringRepo(t)
	r.WriteFile("packages/core/main.txt", "core\n")
	r.WriteConfig(`{"packages":{"core":{"path":"packages/core"}},"parser":{"limits":{"messageBytes":32}}}`)
	message := r.Path("oversized-message.txt")
	r.WriteFile("oversized-message.txt", "feat(core): "+strings.Repeat("x", 128)+"\n")
	before := r.Git("rev-parse", "HEAD")
	r.WriteFile("tracked.txt", "changed\n")
	r.Git("add", "tracked.txt")

	res := r.Command("commit", "-F", message)
	assert.NotEqual(t, 0, res.Code)
	assert.Equal(t, before, r.Git("rev-parse", "HEAD"))
	assert.Contains(t, res.Stderr, "over the limit")
}

func TestCommitValidationTreatsDoubleDashAsMessageValue(t *testing.T) {
	r := authoringRepo(t)
	before := r.Git("rev-parse", "HEAD")
	r.WriteFile("tracked.txt", "changed\n")
	r.Git("add", "tracked.txt")

	res := r.Shell("dispat commit -m --")
	assert.NotEqual(t, 0, res.Code)
	assert.Equal(t, before, r.Git("rev-parse", "HEAD"))
	assert.Contains(t, res.Stderr, "commit message refused")
}

func TestCommitValidationBlocksMessageMadeInvalidByOriginalHook(t *testing.T) {
	r := authoringRepo(t)
	before := r.Git("rev-parse", "HEAD")
	hook := r.Path(".git", "hooks", "commit-msg")
	r.WriteFile(".git/hooks/commit-msg", "#!/bin/sh\nprintf 'invalid after hook\\n' > \"$1\"\n")
	require.NoError(t, os.Chmod(hook, 0o700))
	r.WriteFile("tracked.txt", "changed\n")
	r.Git("add", "tracked.txt")

	res := r.Command("commit", "-m", "feat(core): valid before hook")
	assert.NotEqual(t, 0, res.Code)
	assert.Equal(t, before, r.Git("rev-parse", "HEAD"))
	assert.Contains(t, res.Stderr, "commit message refused")
}

func TestCommitValidationCancellationStopsEditorProcessTree(t *testing.T) {
	r := authoringRepo(t)
	before := r.Git("rev-parse", "HEAD")
	r.WriteFile("tracked.txt", "changed\n")
	r.Git("add", "tracked.txt")
	r.WriteFile("editor.sh", `#!/bin/sh
printf '%s\n' "$$" > editor.pid
trap 'exit 143' TERM INT
# A child can defer or ignore graceful termination. It writes its own ready
# marker after installing the trap so cancellation cannot race its setup.
/bin/sh -c 'trap "" TERM INT; printf ready > editor-child-ready; sleep 30' &
printf '%s\n' "$!" > editor-child.pid
while [ ! -s editor-child-ready ]; do sleep 0.01; done
wait
`)
	require.NoError(t, os.Chmod(r.Path("editor.sh"), 0o700))

	res := r.Shell(`command -v ps >/dev/null || exit 95
mkdir private-tmp
TMPDIR="$PWD/private-tmp" GIT_EDITOR=./editor.sh dispat commit --edit -m invalid &
dispat_pid=$!
i=0
while { [ ! -s editor.pid ] || [ ! -s editor-child.pid ] || [ ! -s editor-child-ready ]; } && [ "$i" -lt 100 ]; do sleep 0.02; i=$((i+1)); done
[ -s editor.pid ] && [ -s editor-child.pid ] && [ -s editor-child-ready ] || exit 97
kill -TERM "$dispat_pid"
wait "$dispat_pid" >/dev/null 2>&1
editor_pid=$(cat editor.pid)
child_pid=$(cat editor-child.pid)
alive() {
  state=$(ps -o stat= -p "$1" 2>/dev/null | tr -d ' ')
  case "$state" in ''|Z*) return 1 ;; *) return 0 ;; esac
}
i=0
while { alive "$editor_pid" || alive "$child_pid"; } && [ "$i" -lt 100 ]; do
  sleep 0.02
  i=$((i+1))
done
if alive "$editor_pid"; then exit 98; fi
if alive "$child_pid"; then exit 99; fi
if find private-tmp -name 'dispat-commit-*' -print -quit | grep . >/dev/null; then exit 96; fi
exit 0`)
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, before, r.Git("rev-parse", "HEAD"))
}

func TestCommitValidationNeverUsesFlagValueAsCommand(t *testing.T) {
	r := authoringRepo(t)
	before := r.Git("rev-parse", "HEAD")
	r.WriteFile("tracked.txt", "changed\n")
	r.Git("add", "tracked.txt")
	staged := r.Git("diff", "--cached")

	res := r.Shell("dispat --package commit -m 'feat(core): must not commit'")
	assert.NotEqual(t, 0, res.Code)
	assert.Equal(t, before, r.Git("rev-parse", "HEAD"))
	assert.Equal(t, staged, r.Git("diff", "--cached"))
}

func TestCommitValidationRejectsPrefixedReleaseFlagsBeforeMutation(t *testing.T) {
	r := authoringRepo(t)
	before := r.Git("rev-parse", "HEAD")
	r.WriteFile("tracked.txt", "changed\n")
	r.Git("add", "tracked.txt")
	staged := r.Git("diff", "--cached")

	res := r.Shell("dispat --package core commit -m 'feat(core): must not commit'")
	assert.NotEqual(t, 0, res.Code)
	assert.Equal(t, before, r.Git("rev-parse", "HEAD"))
	assert.Equal(t, staged, r.Git("diff", "--cached"))
	assert.Contains(t, res.Stderr, "cannot be combined with an authoring commit")
}

// TestCommitAuthoringKeepsGitsOwnShortOptions: an authoring commit hands
// Git its argv untouched, so Git's short options — the ones that take an
// attached value, the ones that take a following one, and the editor request
// that selects authoring on its own — all survive the split.
func TestCommitAuthoringKeepsGitsOwnShortOptions(t *testing.T) {
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

// TestCommitScissorsCleanupCutsAtTheRepositoryCommentCharacter: the
// scissors mode keeps only what is above Git's cut line, and the cut line is
// spelled with whatever comment character the repository configured. Without
// an editor there is nothing below the cut to remove, so the mode degrades to
// the whitespace cleanup rather than guessing.
func TestCommitScissorsCleanupCutsAtTheRepositoryCommentCharacter(t *testing.T) {
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

// TestCommitRefusesCleanupItCannotCarryOut: a cleanup mode dispat cannot
// reproduce, from the command line or from the repository configuration, is
// refused before Git creates anything — and so is the comment character that
// only Git's own editor could resolve.
func TestCommitRefusesCleanupItCannotCarryOut(t *testing.T) {
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

// TestCommitRefusesCommandLinesItCannotForward: a Git option dispat does
// not know how to reason about, and an option missing the value it needs,
// are refused with nothing created and the staged diff untouched.
func TestCommitRefusesCommandLinesItCannotForward(t *testing.T) {
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

// TestCommitMessageGateRefusesUnusableHookInput: the commit-msg gate is a
// program Git runs with one argument and a private configuration in the
// environment. Every way that contract can be broken is refused with a
// sentence naming the input, and a message it accepts is rewritten in place.
func TestCommitMessageGateRefusesUnusableHookInput(t *testing.T) {
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

// TestCommitAuthoringHandlesTheRepositoryItFinds: what an authoring commit
// reads from the repository before it installs its gate — the Git
// configuration it must reproduce, and the hooks it must keep running — is
// bounded and tolerant. An oversized configuration value is refused rather
// than truncated, a repository with no hooks folder needs none, and a folder
// among the hooks is not a hook.
func TestCommitAuthoringHandlesTheRepositoryItFinds(t *testing.T) {
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
