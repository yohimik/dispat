package app

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/ccme"

	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/github"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// The end-to-end behaviour of App — status, release, hooks, finalize, exit
// semantics — is exercised by the black-box suite in tests/integration
// against the compiled binary; here live the unit tests of App's own
// helpers.

func TestGitPrerequisitesGuard(t *testing.T) {
	// The guard fires before any git command or discovery, so both cases are
	// plain unit tests: no repository, no fakes.
	cfg := &config.File{}

	t.Run("no repository root", func(t *testing.T) {
		a := New(t.TempDir(), cfg, zerolog.Nop()) // no .git
		err := a.Status(context.Background(), ReleaseOptions{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a git repository root")
	})

	t.Run("no git executable", func(t *testing.T) {
		root := t.TempDir()
		require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0o755))
		t.Setenv("PATH", t.TempDir()) // an empty PATH entry: no git anywhere
		a := New(root, cfg, zerolog.Nop())
		err := a.Status(context.Background(), ReleaseOptions{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "git executable not found")
	})
}

func TestValidOnError(t *testing.T) {
	assert.True(t, ValidOnError(OnErrorSkip))
	assert.True(t, ValidOnError(OnErrorContinue))
	assert.False(t, ValidOnError(""))
	assert.False(t, ValidOnError("explode"))
}

func TestInitialVersionsMapping(t *testing.T) {
	// Viper lowercases map keys, so matching is case-insensitive; a key that
	// names no discovered package is warned about and ignored.
	var buf bytes.Buffer
	a := New(t.TempDir(), &config.File{
		InitialVersions: map[string]ccme.Version{
			"core":  {Major: 1, Minor: 2},
			"ghost": {Major: 9},
		},
	}, zerolog.New(&buf))

	out := a.initialVersions([]*model.Package{{Name: "Core"}})
	require.Len(t, out, 1)
	assert.Equal(t, "1.2.0", out["Core"].String(), "initials key matched case-insensitively")
	assert.Contains(t, buf.String(), "initials entry matches no discovered package")
}

func TestRenderCommitMessage(t *testing.T) {
	pkgs := []string{"core", "utils"}
	tags := []string{"core@1.1.0", "utils@2.0.1"}
	assert.Equal(t, "chore(release): core@1.1.0, utils@2.0.1",
		renderCommitMessage("", pkgs, tags), "default format")
	assert.Equal(t, "publish core, utils as core@1.1.0, utils@2.0.1",
		renderCommitMessage("publish {packages} as {tags}", pkgs, tags))
	assert.Equal(t, "no placeholders", renderCommitMessage("no placeholders", pkgs, tags))
}

// TestGithubDispatchDistinctTargets: one releaser per distinct resolved
// target — packages sharing a target share the releaser, a disabled package
// has none, and an unresolvable target disables its packages without failing
// the others.
func TestGithubDispatchDistinctTargets(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "tok")
	t.Setenv("GITHUB_REPOSITORY", "")
	pkg := func(name string, spec model.GitHubSpec) *plan.Release {
		return &plan.Release{Pkg: &model.Package{Name: name, GitHub: spec}}
	}
	pl := &plan.Plan{
		Order: []string{"a", "b", "c", "d", "e"},
		Releases: map[string]*plan.Release{
			"a": pkg("a", model.GitHubSpec{Enabled: true, Owner: "acme", Repo: "mono"}),
			"b": pkg("b", model.GitHubSpec{Enabled: true, Owner: "acme", Repo: "mono"}),
			"c": pkg("c", model.GitHubSpec{Enabled: true, Owner: "acme", Repo: "other"}),
			"d": pkg("d", model.GitHubSpec{Enabled: false}),
			"e": pkg("e", model.GitHubSpec{Enabled: true}), // no repository anywhere: unresolvable
		},
	}
	a := &App{log: zerolog.Nop()}
	d := a.githubDispatch(pl)

	assert.Len(t, d.all, 2, "two distinct targets, two releasers")
	assert.False(t, d.empty())
	assert.Same(t, d.byPkg["a"], d.byPkg["b"], "one target, one releaser")
	assert.NotSame(t, d.byPkg["a"], d.byPkg["c"])
	assert.Equal(t, "other", d.byPkg["c"].Repo)
	assert.Nil(t, d.byPkg["d"], "disabled: no releaser")
	assert.Nil(t, d.byPkg["e"], "unresolvable: disabled with a warning, others unaffected")

	// Record on a package without a releaser is a silent no-op, so the
	// recorder chain never fails on a deliberately disabled package.
	require.NoError(t, d.Record(context.Background(), pl.Releases["d"]))
}

// TestRecordersAssembly: the changelog dispatcher is always present (each
// package's policy decides for itself); the GitHub dispatch joins outside
// release-commit mode and only when something resolved.
func TestRecordersAssembly(t *testing.T) {
	a := &App{log: zerolog.Nop()}
	empty := &ghDispatch{log: zerolog.Nop()}
	assert.Len(t, a.recorders(empty, false), 1, "no resolved target, changelog alone")

	one := &ghDispatch{all: []*github.Releaser{{}}, log: zerolog.Nop()}
	assert.Len(t, a.recorders(one, false), 2)
	assert.Len(t, a.recorders(one, true), 1, "commit mode: github moves to finalize")
}

func TestGithubReleaserResolution(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "envowner/envrepo")
	t.Setenv("GITHUB_TOKEN", "envtoken")
	t.Setenv("CUSTOM_TOKEN", "customtoken")

	gh, err := githubReleaser(model.GitHubSpec{}, zerolog.Nop())
	require.NoError(t, err)
	assert.Equal(t, "envowner", gh.Owner)
	assert.Equal(t, "envrepo", gh.Repo)
	assert.Equal(t, "envtoken", gh.Token)

	gh, err = githubReleaser(model.GitHubSpec{Owner: "acme", Repo: "mono", TokenEnv: "CUSTOM_TOKEN"}, zerolog.Nop())
	require.NoError(t, err)
	assert.Equal(t, "acme", gh.Owner)
	assert.Equal(t, "mono", gh.Repo)
	assert.Equal(t, "customtoken", gh.Token)

	t.Setenv("GITHUB_REPOSITORY", "")
	_, err = githubReleaser(model.GitHubSpec{}, zerolog.Nop())
	assert.ErrorContains(t, err, "no repository")

	t.Setenv("GITHUB_TOKEN", "")
	_, err = githubReleaser(model.GitHubSpec{Owner: "acme", Repo: "mono"}, zerolog.Nop())
	assert.ErrorContains(t, err, "GITHUB_TOKEN")
}

// guardRepo builds an App over a real one-commit repository on branch "main".
// The host's init.defaultBranch differs between machines and CI runners, so
// the branch is named rather than inherited.
func guardRepo(t *testing.T, cfg *config.File) (string, *App) {
	t.Helper()
	root := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	git("symbolic-ref", "HEAD", "refs/heads/main")
	git("config", "user.email", "test@example.com")
	git("config", "user.name", "Test")
	require.NoError(t, os.WriteFile(filepath.Join(root, "f.txt"), []byte("x"), 0o644))
	git("add", ".")
	git("commit", "-qm", "feat(core): initial")
	return root, New(root, cfg, zerolog.Nop())
}

func TestCheckBranchAllowed(t *testing.T) {
	ctx := context.Background()
	git := func(root string, args ...string) {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}

	t.Run("unset guard allows any branch", func(t *testing.T) {
		root, a := guardRepo(t, &config.File{Run: &config.RunConfig{}})
		git(root, "checkout", "-q", "-b", "whatever")
		require.NoError(t, a.checkBranchAllowed(ctx))
	})

	t.Run("listed branch is allowed", func(t *testing.T) {
		_, a := guardRepo(t, &config.File{Run: &config.RunConfig{AllowBranch: []string{"main"}}})
		require.NoError(t, a.checkBranchAllowed(ctx))
	})

	t.Run("glob reaches slashed names", func(t *testing.T) {
		root, a := guardRepo(t, &config.File{Run: &config.RunConfig{AllowBranch: []string{"release/*"}}})
		git(root, "checkout", "-q", "-b", "release/v2/hotfix")
		require.NoError(t, a.checkBranchAllowed(ctx),
			"a * matches separators too, so release/* reaches a nested name")
	})

	t.Run("foreign branch is refused, naming both", func(t *testing.T) {
		root, a := guardRepo(t, &config.File{
			Run: &config.RunConfig{AllowBranch: []string{"main", "release/*"}}})
		git(root, "checkout", "-q", "-b", "feature/tryout")
		err := a.checkBranchAllowed(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), `branch "feature/tryout" is not allowed`)
		assert.Contains(t, err.Error(), "main, release/*",
			"the message lists what would have been allowed")
	})

	t.Run("detached HEAD matches nothing", func(t *testing.T) {
		root, a := guardRepo(t, &config.File{Run: &config.RunConfig{AllowBranch: []string{"main"}}})
		head, err := a.git.HeadSHA(ctx)
		require.NoError(t, err)
		git(root, "checkout", "-q", head)
		err = a.checkBranchAllowed(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "HEAD is detached")
	})
}

func TestCheckNotBehind(t *testing.T) {
	ctx := context.Background()
	git := func(root string, args ...string) {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}

	t.Run("detached HEAD is skipped", func(t *testing.T) {
		// There is no branch to compare, and the push fails on its own terms
		// there, so the guard has nothing honest to say.
		root, a := guardRepo(t, &config.File{Run: &config.RunConfig{}})
		head, err := a.git.HeadSHA(ctx)
		require.NoError(t, err)
		git(root, "checkout", "-q", head)
		require.NoError(t, a.checkNotBehind(ctx, "origin"),
			"no remote is even contacted for a detached HEAD")
	})

	t.Run("behind the remote is refused", func(t *testing.T) {
		root, a := guardRepo(t, &config.File{Run: &config.RunConfig{}})
		bare := t.TempDir()
		out, err := exec.Command("git", "init", "-q", "--bare", bare).CombinedOutput()
		require.NoError(t, err, "git init --bare: %s", out)
		out, err = exec.Command("git", "-C", bare, "symbolic-ref", "HEAD", "refs/heads/main").CombinedOutput()
		require.NoError(t, err, "git symbolic-ref: %s", out)
		git(root, "remote", "add", "origin", bare)
		git(root, "push", "-q", "origin", "main")

		require.NoError(t, a.checkNotBehind(ctx, "origin"), "level with the remote")

		// Another clone moves the remote on.
		other := t.TempDir()
		out, err = exec.Command("git", "clone", "-q", "--branch", "main", bare, other).CombinedOutput()
		require.NoError(t, err, "git clone: %s", out)
		git(other, "config", "user.email", "other@example.com")
		git(other, "config", "user.name", "Other")
		git(other, "commit", "-q", "--allow-empty", "-m", "chore: elsewhere")
		git(other, "push", "-q", "origin", "HEAD:refs/heads/main")

		err = a.checkNotBehind(ctx, "origin")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "behind origin/main")
		assert.Contains(t, err.Error(), "pull before releasing")
	})
}
