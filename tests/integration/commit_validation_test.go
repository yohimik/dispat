package integration

import (
	"os"
	"os/exec"
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
sleep 30 &
printf '%s\n' "$!" > editor-child.pid
wait
`)
	require.NoError(t, os.Chmod(r.Path("editor.sh"), 0o700))

	res := r.Shell(`command -v ps >/dev/null || exit 95
mkdir private-tmp
TMPDIR="$PWD/private-tmp" GIT_EDITOR=./editor.sh dispat commit --edit -m invalid &
dispat_pid=$!
i=0
while { [ ! -s editor.pid ] || [ ! -s editor-child.pid ]; } && [ "$i" -lt 100 ]; do sleep 0.02; i=$((i+1)); done
[ -s editor.pid ] && [ -s editor-child.pid ] || exit 97
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
