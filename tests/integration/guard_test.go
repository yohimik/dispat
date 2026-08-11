// Goal 21: the release guards. `run.allowBranch` refuses a release from any
// branch outside its globs while the read-only commands stay available, and a
// push-mode release from a checkout behind the remote is refused before any
// release work starts. Both refusals fire before builds, tags or pushes, so a
// guarded run leaves the repository exactly as it found it.
package integration

import (
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// pushClone clones the bare remote into a scratch folder and pushes one empty
// commit from it: another checkout moving the branch on, which is what leaves
// the repository under test behind.
//
// Both the branch to clone and the ref to push are named explicitly. The bare
// repository's own HEAD would otherwise follow whatever init.defaultBranch the
// host git is configured with, the clone would land on an unborn branch, and
// the push would create a second branch the original never tracks — so the
// checkout under test would never be behind and the test would prove nothing.
func pushClone(t *testing.T, bare, message string) {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}
	git("clone", "-q", "--branch", harness.DefaultBranch, bare, ".")
	git("config", "user.email", "other@dispat.test")
	git("config", "user.name", "other clone")
	git("commit", "-q", "--allow-empty", "-m", message)
	git("push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)
}

// TestGuardAllowBranch: a release on an allowlisted branch proceeds, one on a
// foreign branch is refused with nothing released, a glob reaches slashed
// branch names, and `dispat status` is never guarded.
func TestGuardAllowBranch(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Run = &models.RunConfig{AllowBranch: []string{harness.DefaultBranch, "release/*"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first")

	r.ReleaseOK()
	require.True(t, r.HasTag("core@0.1.0"), "tags: %v", r.TagList())

	r.Commit("feat(core): pending work")
	r.Git("checkout", "-q", "-b", "feature/tryout")
	res := r.Release()
	require.Equal(t, 1, res.Code, "a foreign branch must refuse\nstdout:\n%s", res.Stdout)
	assert.Contains(t, res.Stdout, `branch \"feature/tryout\" is not allowed`)
	assert.Contains(t, res.Stdout, "release/*", "the refusal names what would be allowed")
	assert.False(t, r.HasTag("core@0.2.0"), "a refused run must not tag")

	// The guard gates releasing, not reading: the dry run works anywhere.
	r.StatusOK()

	// A slashed branch matching a glob may release.
	r.Git("checkout", "-q", "-b", "release/v1")
	r.ReleaseOK()
	assert.True(t, r.HasTag("core@0.2.0"), "tags: %v", r.TagList())
}

// TestGuardAllowBranchRefusesDetachedHead: a detached HEAD has no branch name,
// so it matches nothing — including a glob as broad as "*".
func TestGuardAllowBranchRefusesDetachedHead(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Run = &models.RunConfig{AllowBranch: []string{"*"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first")
	r.Git("checkout", "-q", r.Git("rev-parse", "HEAD"))

	res := r.Release()
	require.Equal(t, 1, res.Code, "stdout:\n%s", res.Stdout)
	assert.Contains(t, res.Stdout, "HEAD is detached")
	assert.False(t, r.HasTag("core@0.1.0"), "a refused run must not tag")
}

// TestGuardBehindRemote: in push mode a checkout whose branch tip is behind
// the remote refuses before any release work — the plan was computed against
// stale tags — and releases normally once the checkout has caught up.
func TestGuardBehindRemote(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true), Push: true}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first")
	bare := r.AddBareRemote()

	r.ReleaseOK()
	require.True(t, r.HasTag("core@0.1.0"), "tags: %v", r.TagList())

	// The remote moves on without this checkout.
	pushClone(t, bare, "chore: pushed elsewhere")

	r.WriteFile("packages/core/more.txt", "stale work\n")
	r.Commit("feat(core): stale work")
	res := r.Release()
	require.Equal(t, 1, res.Code, "a stale checkout must refuse\nstdout:\n%s", res.Stdout)
	assert.Contains(t, res.Stdout, "behind origin/"+harness.DefaultBranch)
	assert.False(t, r.HasTag("core@0.2.0"), "a refused run must not tag")

	// Catching up clears the guard and the release goes through, push and all.
	r.Git("pull", "-q", "--rebase", "origin", harness.DefaultBranch)
	r.ReleaseOK()
	require.True(t, r.HasTag("core@0.2.0"), "tags: %v", r.TagList())
	assert.Contains(t, r.Git("ls-remote", "origin"), "refs/tags/core@0.2.0")
}

// TestGuardBehindRemoteHonoursCommitVerify: the behind check is another
// ls-remote, so it belongs under the same switch as the reachability check.
// commit.verify=false exists for remotes that reject ls-remote but accept
// pushes, and turning it off has to silence both or it silences neither.
func TestGuardBehindRemoteHonoursCommitVerify(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Commit = &models.CommitConfig{
		Enabled: models.Bool(true), Push: true, Verify: models.Bool(false)}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first")
	bare := r.AddBareRemote()

	r.ReleaseOK()
	require.True(t, r.HasTag("core@0.1.0"), "tags: %v", r.TagList())

	pushClone(t, bare, "chore: pushed elsewhere")

	r.WriteFile("packages/core/more.txt", "stale work\n")
	r.Commit("feat(core): stale work")
	res := r.Release()
	assert.NotContains(t, res.Stdout, "behind origin/",
		"with verification off the checkout is never compared to the remote")

	// And this is what the guard buys when it is on. The run went all the way
	// through — built, published and tagged — before git rejected the push,
	// which is the wasted release the check exists to prevent.
	assert.Contains(t, res.Stdout, "published")
	assert.True(t, r.HasTag("core@0.2.0"), "the tag was already created; tags: %v", r.TagList())
	assert.Contains(t, res.Stdout, "push failed")
	assert.Equal(t, 1, res.Code)
}

// TestGuardsAreUnsetByDefault: neither guard applies to a configuration that
// asks for it, so an ordinary repository — including one on a branch named
// anything at all, pushing to a remote — releases exactly as before.
func TestGuardsAreUnsetByDefault(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true), Push: true}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first")
	r.AddBareRemote()
	r.Git("checkout", "-q", "-b", "some/odd/branch")

	r.ReleaseOK()
	assert.True(t, r.HasTag("core@0.1.0"), "tags: %v", r.TagList())
}
