package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/config"
)

// Configs are authored as typed models (config aliases the public pkg/models
// structs) and marshalled to JSON, so a config that compiles is a config that
// loads.

func boolPtr(b bool) *bool { return &b }

// testConfig returns the config every cli test starts from: one "libs" space
// at packages/ with echo build/publish scripts, serial, JSON logging. GitHub
// is disabled so runs never touch the real API even when GITHUB_TOKEN /
// GITHUB_REPOSITORY are present (e.g. in CI).
func testConfig() config.File {
	return config.File{
		Scripts: map[string]string{"build": "echo building", "publish": "echo publishing"},
		Spaces: map[string]config.SpaceConfig{
			"libs": {Path: "packages", Run: config.SpaceRunConfig{
				Build: []string{"build"}, Publish: []string{"publish"}}},
		},
		Concurrency: []int{1},
		LogLevel:    "info",
		LogFormat:   "json",
		GitHub:      config.GitHubConfig{Enabled: boolPtr(false)},
	}
}

func testConfigNoChangelog() config.File {
	cfg := testConfig()
	cfg.Changelog = config.ChangelogConfig{Enabled: boolPtr(false)}
	return cfg
}

// initRepo creates a git monorepo with one package and one feat commit.
//
// The commit carries a caret. propagation.depth defaults to 0 (§14), so a
// caret-less feat reaches no consumer at all; every end-to-end test here that
// exercises a dependency edge needs the unit to opt in to propagation.
func initRepo(t *testing.T, cfg config.File) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	cfgJSON, err := json.MarshalIndent(cfg, "", "  ")
	require.NoError(t, err)
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "packages", "core"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "dispat.json"), cfgJSON, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "packages", "core", "main.txt"), []byte("hi"), 0o644))

	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}
	git("init", "-q")
	git("config", "user.email", "test@example.com")
	git("config", "user.name", "Test")
	git("add", ".")
	git("commit", "-qm", "feat(core)^: first release")
	return root
}

func TestVersionFlag(t *testing.T) {
	// --version answers before anything else — no config file is read, so it
	// works outside a monorepo. The default "dev" marks a local build;
	// releases override Version at build time from the release tag.
	var stdout, stderr bytes.Buffer
	code := Run([]string{"--version"}, &stdout, &stderr)
	assert.Equal(t, 0, code)
	assert.Equal(t, "dispat dev\n", stdout.String())
	assert.Empty(t, stderr.String())

	old := Version
	t.Cleanup(func() { Version = old })
	Version = "1.2.3"
	stdout.Reset()
	code = Run([]string{"--version"}, &stdout, &stderr)
	assert.Equal(t, 0, code)
	assert.Equal(t, "dispat 1.2.3\n", stdout.String())
}

func TestStatusCommand(t *testing.T) {
	root := initRepo(t, testConfig())
	var stdout, stderr bytes.Buffer

	code := Run([]string{"status", "--root", root}, &stdout, &stderr)
	require.Equal(t, 0, code, "stderr: %s", stderr.String())

	out := stdout.String()
	assert.Contains(t, out, "core", "graph must list the package")
	assert.Contains(t, out, "0.1.0", "first feat release is 0.1.0")
	// Status must not build, publish or tag anything.
	assert.NotContains(t, out, "publish")
	tags, err := exec.Command("git", "-C", root, "tag").Output()
	require.NoError(t, err)
	assert.Empty(t, bytes.TrimSpace(tags), "status must not create tags")
}

func TestReleaseCommand(t *testing.T) {
	root := initRepo(t, testConfig())
	var stdout, stderr bytes.Buffer

	code := Run([]string{"--root", root}, &stdout, &stderr)
	require.Equal(t, 0, code, "stderr: %s\nstdout: %s", stderr.String(), stdout.String())

	tags, err := exec.Command("git", "-C", root, "tag").Output()
	require.NoError(t, err)
	assert.Contains(t, string(tags), "core@0.1.0")
	assert.FileExists(t, filepath.Join(root, "packages", "core", "CHANGELOG.md"))
}

func TestReleaseCommandChangelogDisabled(t *testing.T) {
	root := initRepo(t, testConfigNoChangelog())
	var stdout, stderr bytes.Buffer

	code := Run([]string{"--root", root}, &stdout, &stderr)
	require.Equal(t, 0, code, "stderr: %s\nstdout: %s", stderr.String(), stdout.String())

	tags, err := exec.Command("git", "-C", root, "tag").Output()
	require.NoError(t, err)
	assert.Contains(t, string(tags), "core@0.1.0", "tagging is independent of the changelog")
	assert.NoFileExists(t, filepath.Join(root, "packages", "core", "CHANGELOG.md"))
}

func testConfigInitials() config.File {
	cfg := testConfig()
	cfg.Initials = map[string]string{"core": "1.0.0", "ghost": "5.0.0"}
	return cfg
}

func TestStatusInitialsWithUnparseableTag(t *testing.T) {
	root := initRepo(t, testConfigInitials())
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}
	// Newest (only) tag is unparseable; a fix lands after it.
	git("tag", "-a", "core@0.1.0.broken", "-m", "broken tag")
	require.NoError(t, os.WriteFile(filepath.Join(root, "packages", "core", "fix.txt"), []byte("x"), 0o644))
	git("add", ".")
	git("commit", "-qm", "fix(core): repair")

	var stdout, stderr bytes.Buffer
	code := Run([]string{"status", "--root", root}, &stdout, &stderr)
	require.Equal(t, 0, code, "stderr: %s", stderr.String())

	out := stdout.String()
	assert.Contains(t, out, "1.0.1", "initials 1.0.0 + fix since the unparseable tag -> 1.0.1")
	assert.Contains(t, out, "baselineFromInitials")
	assert.Contains(t, out, "ghost", "unknown initials key must be warned about")
}

func testConfigRevert() config.File {
	cfg := testConfig()
	cfg.Scripts = map[string]string{
		"bad-build": "echo dirty > main.txt && echo junk > generated.txt && exit 1",
	}
	cfg.Spaces = map[string]config.SpaceConfig{
		"libs": {Path: "packages", RevertOnFail: true,
			Run: config.SpaceRunConfig{Build: []string{"bad-build"}}},
	}
	return cfg
}

func TestReleaseRevertOnFail(t *testing.T) {
	root := initRepo(t, testConfigRevert())
	var stdout, stderr bytes.Buffer

	code := Run([]string{"--root", root}, &stdout, &stderr)
	require.Equal(t, 1, code, "failed package must fail the run")

	// The failing build dirtied the folder; revertOnFail must restore it.
	data, err := os.ReadFile(filepath.Join(root, "packages", "core", "main.txt"))
	require.NoError(t, err)
	assert.Equal(t, "hi", string(data), "tracked file restored")
	assert.NoFileExists(t, filepath.Join(root, "packages", "core", "generated.txt"), "untracked file removed")

	tags, err := exec.Command("git", "-C", root, "tag").Output()
	require.NoError(t, err)
	assert.Empty(t, bytes.TrimSpace(tags), "failed package must not be tagged")
}

// testConfigCommit enables the release commit without pushing, so a run leaves
// a `chore(release): ...` commit in the history the next run must read.
func testConfigCommit() config.File {
	cfg := testConfig()
	cfg.Commit = config.CommitConfig{Enabled: boolPtr(true)}
	return cfg
}

func testConfigCommitPush() config.File {
	cfg := testConfig()
	cfg.Commit = config.CommitConfig{
		Enabled: boolPtr(true), MessageFormat: "release: {packages} -> {tags}", Push: true,
	}
	return cfg
}

// addBareRemote creates a bare repository and registers it as "origin".
func addBareRemote(t *testing.T, root string) string {
	t.Helper()
	bare := t.TempDir()
	out, err := exec.Command("git", "init", "-q", "--bare", bare).CombinedOutput()
	require.NoError(t, err, "git init --bare: %s", out)
	out, err = exec.Command("git", "-C", root, "remote", "add", "origin", bare).CombinedOutput()
	require.NoError(t, err, "git remote add: %s", out)
	return bare
}

func TestReleaseCommitAndPush(t *testing.T) {
	root := initRepo(t, testConfigCommitPush())
	bare := addBareRemote(t, root)
	var stdout, stderr bytes.Buffer

	code := Run([]string{"--root", root}, &stdout, &stderr)
	require.Equal(t, 0, code, "stderr: %s\nstdout: %s", stderr.String(), stdout.String())

	git := func(dir string, args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
		require.NoError(t, err, "git %v", args)
		return string(bytes.TrimSpace(out))
	}
	// Release commit exists with the templated message and contains the changelog.
	assert.Equal(t, "release: core -> core@0.1.0", git(root, "log", "-1", "--format=%s"))
	assert.Contains(t, git(root, "show", "--stat", "--format=", "HEAD"), "CHANGELOG.md")
	// The tag points at the release commit.
	assert.Equal(t, git(root, "rev-parse", "HEAD"),
		git(root, "rev-parse", "core@0.1.0^{commit}"), "tag must point at the release commit")
	// Commit and tag arrived on the remote.
	assert.Contains(t, git(bare, "tag"), "core@0.1.0")
	assert.Contains(t, git(bare, "log", "--format=%s", "--all"), "release: core -> core@0.1.0")
	// Worktree is clean: everything the release changed was committed.
	assert.Empty(t, git(root, "status", "--porcelain"))
}

func TestReleaseVerifyRemoteFailsFast(t *testing.T) {
	// push enabled but no remote configured: the run must fail before any
	// release work happens.
	root := initRepo(t, testConfigCommitPush())
	var stdout, stderr bytes.Buffer

	code := Run([]string{"--root", root}, &stdout, &stderr)
	require.Equal(t, 1, code)

	tags, err := exec.Command("git", "-C", root, "tag").Output()
	require.NoError(t, err)
	assert.Empty(t, bytes.TrimSpace(tags), "no tags may be created")
	assert.NoFileExists(t, filepath.Join(root, "packages", "core", "CHANGELOG.md"),
		"no release work may run before verification passes")
}

func TestReleaseCommitGithubReleaseIncludesCommitAndTag(t *testing.T) {
	// commit enabled, push DISABLED: the GitHub release must still document
	// the release commit and tag in its body, without target_commitish.
	type ghRelease struct {
		TagName         string `json:"tag_name"`
		Body            string `json:"body"`
		TargetCommitish string `json:"target_commitish"`
	}
	var mu sync.Mutex
	var releases []ghRelease
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet: // Verify
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost:
			var rel ghRelease
			require.NoError(t, json.NewDecoder(r.Body).Decode(&rel))
			mu.Lock()
			releases = append(releases, rel)
			mu.Unlock()
			w.WriteHeader(http.StatusCreated)
		}
	}))
	defer srv.Close()

	cfg := testConfigCommit()
	cfg.Scripts["publish"] = `echo "DISPAT_EXPORT_GITHUB=" >> "$DISPAT_OUTPUT"`
	cfg.GitHub = config.GitHubConfig{
		Enabled: boolPtr(true), Owner: "acme", Repo: "mono",
		APIURL: srv.URL, TokenEnv: "DISPAT_TEST_TOKEN",
	}
	t.Setenv("DISPAT_TEST_TOKEN", "tkn")
	root := initRepo(t, cfg)
	var stdout, stderr bytes.Buffer

	code := Run([]string{"--root", root}, &stdout, &stderr)
	require.Equal(t, 0, code, "stderr: %s\nstdout: %s", stderr.String(), stdout.String())

	sha, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	require.NoError(t, err)

	require.Len(t, releases, 1)
	assert.Equal(t, "core@0.1.0", releases[0].TagName)
	assert.Contains(t, releases[0].Body, "### Release")
	assert.Contains(t, releases[0].Body, "- commit: "+string(bytes.TrimSpace(sha)),
		"release body documents the release commit")
	assert.Contains(t, releases[0].Body, "- tag: core@0.1.0")
	assert.Empty(t, releases[0].TargetCommitish, "unpushed SHA must not be sent as target_commitish")
}

func testConfigCatchUp() config.File {
	cfg := testConfig()
	cfg.Scripts["build"] = "[ ! -f FAIL ]"
	cfg.Dependencies = []config.DependencyConfig{{Consumer: "app", Provider: "core"}}
	return cfg
}

func TestReleaseCatchUpAfterConsumerFailure(t *testing.T) {
	// The orphaned-consumer scenario end to end. Run 1: core publishes and
	// is tagged, app's build fails (FAIL marker). Run 2, with no new
	// commits: app must be caught up and released; core must not re-release.
	root := initRepo(t, testConfigCatchUp())
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
		return string(bytes.TrimSpace(out))
	}
	// Add the app package without any conventional commits of its own, then
	// plant the failure marker (untracked, so no commit needed to remove it).
	require.NoError(t, os.MkdirAll(filepath.Join(root, "packages", "app"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "packages", "app", "main.txt"), []byte("app"), 0o644))
	git("add", "packages/app/main.txt")
	git("commit", "-qm", "chore: add app skeleton")
	require.NoError(t, os.WriteFile(filepath.Join(root, "packages", "app", "FAIL"), []byte("x"), 0o644))

	// Run 1: core succeeds, app fails.
	var out1, err1 bytes.Buffer
	code := Run([]string{"--root", root}, &out1, &err1)
	require.Equal(t, 1, code, "app's failure must fail the run\n%s", out1.String())
	tags := git("tag")
	assert.Contains(t, tags, "core@0.1.0")
	assert.NotContains(t, tags, "app@", "failed app must not be tagged")

	// Fix the failure and run again — no new commits anywhere.
	require.NoError(t, os.Remove(filepath.Join(root, "packages", "app", "FAIL")))
	var out2, err2 bytes.Buffer
	code = Run([]string{"--root", root}, &out2, &err2)
	require.Equal(t, 0, code, "stderr: %s\nstdout: %s", err2.String(), out2.String())

	tags = git("tag")
	assert.Contains(t, tags, "app@0.0.1", "app must catch up on core's release")
	assert.Equal(t, 1, strings.Count(tags, "core@"), "core must not be re-released")

	// Run 3: everything is fresh, nothing to do.
	var out3, err3 bytes.Buffer
	code = Run([]string{"--root", root}, &out3, &err3)
	require.Equal(t, 0, code)
	tags = git("tag")
	assert.Equal(t, 1, strings.Count(tags, "app@"), "no repeat release once caught up")
}

func TestCommitErrorPolicy(t *testing.T) {
	// A commit naming a package that does not exist is a unit-scoped error
	// (§16): under the default policy the unit contributes nothing and the run
	// goes ahead, and under "error" the run refuses to release at all.
	for _, tc := range []struct {
		policy   string
		wantCode int
		wantTag  bool
	}{
		{"warn", 0, true},
		{"error", 1, false},
	} {
		cfg := testConfig()
		cfg.CommitErrors = tc.policy
		root := initRepo(t, cfg)
		git := func(args ...string) string {
			t.Helper()
			out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
			require.NoError(t, err, "git %v: %s", args, out)
			return string(bytes.TrimSpace(out))
		}
		require.NoError(t, os.WriteFile(filepath.Join(root, "packages", "core", "x.txt"), []byte("x"), 0o644))
		git("add", ".")
		git("commit", "-qm", "fix(nosuch): typo in the scope")

		var stdout, stderr bytes.Buffer
		code := Run([]string{"--root", root}, &stdout, &stderr)
		assert.Equal(t, tc.wantCode, code, "commitErrors=%s\n%s", tc.policy, stdout.String())
		assert.Contains(t, stdout.String(), "E130", "the diagnostic must be reported either way")
		assert.Equal(t, tc.wantTag, strings.Contains(git("tag"), "core@"),
			"commitErrors=%s: released?", tc.policy)
	}
}

func TestReleaseCommitScopeDoesNotPoisonTheNextRun(t *testing.T) {
	// dispat's own release commit is `chore(release): ...`, and `release` is
	// not a package. Without the nonPackageScopes exemption the tool would
	// leave an unknown-package error behind on every run for the next run to
	// trip over.
	root := initRepo(t, testConfigCommit()) // commit mode: writes chore(release)
	var out1, err1 bytes.Buffer
	require.Equal(t, 0, Run([]string{"--root", root}, &out1, &err1),
		"first run: %s", out1.String())

	subject, err := exec.Command("git", "-C", root, "log", "-1", "--format=%s").Output()
	require.NoError(t, err)
	require.Contains(t, string(subject), "chore(release)", "the release commit was created")

	var out2, err2 bytes.Buffer
	code := Run([]string{"status", "--root", root}, &out2, &err2)
	assert.Equal(t, 0, code, "the release commit must not error the next run: %s", out2.String())
	assert.NotContains(t, out2.String(), "E130")
}

func TestReleaseWithCustomTagFormat(t *testing.T) {
	// The tag name is what the *next* run reads the baseline from, so a custom
	// format has to round-trip: released under it, then read back under it.
	cfg := testConfig()
	cfg.TagFormat = "{name}@v{version}"
	root := initRepo(t, cfg)
	tags := func() string {
		out, err := exec.Command("git", "-C", root, "tag").Output()
		require.NoError(t, err)
		return string(out)
	}

	var out1, err1 bytes.Buffer
	require.Equal(t, 0, Run([]string{"--root", root}, &out1, &err1), "%s", out1.String())
	assert.Contains(t, tags(), "core@v0.1.0", "the 'v' comes from the format")

	// Second run: the tag must be recognised, so nothing is pending.
	var out2, err2 bytes.Buffer
	require.Equal(t, 0, Run([]string{"--root", root}, &out2, &err2))
	assert.Equal(t, 1, strings.Count(tags(), "core@"), "the format must round-trip")

	// A new commit bumps on top of the version read back out of the tag.
	git := exec.Command("git", "-C", root, "commit", "-q", "--allow-empty", "-m", "fix(core): repair")
	require.NoError(t, git.Run())
	var out3, err3 bytes.Buffer
	require.Equal(t, 0, Run([]string{"--root", root}, &out3, &err3), "%s", out3.String())
	assert.Contains(t, tags(), "core@v0.1.1", "0.1.0 + fix, still under the custom format")
}

func TestUnknownCommandFallsBackToRunScript(t *testing.T) {
	// A word that is not a command name is treated as `run <word>`, so
	// `dispat lint` works — and a name no space defines (a command typo
	// included) fails with exit 1 from the run script check.
	root := initRepo(t, testConfigRunScripts())
	var stdout, stderr bytes.Buffer
	code := Run([]string{"lint", "--root", root}, &stdout, &stderr)
	require.Equal(t, 0, code, "stdout: %s\nstderr: %s", stdout.String(), stderr.String())
	assert.FileExists(t, filepath.Join(root, "packages", "core", "lint.txt"))

	code = Run([]string{"bogus", "--root", root}, &stdout, &stderr)
	assert.Equal(t, 1, code, "an unknown word is an unknown run script")

	code = Run([]string{"bogus", "extra", "--root", root}, &stdout, &stderr)
	assert.Equal(t, 2, code, "more than one non-command word stays a usage error")
}

func TestUnknownFlagPrintsUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"--nope"}, &stdout, &stderr)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr.String(), "unknown flag", "the parse error must be reported, not swallowed")
	assert.Contains(t, stderr.String(), "usage: dispat")
}

func TestInvalidConfig(t *testing.T) {
	root := t.TempDir() // no config file at all
	var stdout, stderr bytes.Buffer
	code := Run([]string{"status", "--root", root}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "no dispat config file found",
		"the not-found error must be reported, naming what was tried")
}

func TestConfigFileFallbackResolution(t *testing.T) {
	// The default --config falls back through dispat.json, dispat.yaml,
	// dispat.yml, dispat.toml, so a YAML-configured monorepo needs no flag.
	// JSON is valid YAML, so renaming the file exercises the fallback and
	// viper's extension-driven format inference at once.
	root := initRepo(t, testConfig())
	require.NoError(t, os.Rename(filepath.Join(root, "dispat.json"), filepath.Join(root, "dispat.yaml")))

	var stdout, stderr bytes.Buffer
	code := Run([]string{"status", "--root", root}, &stdout, &stderr)
	require.Equal(t, 0, code, "dispat.yaml must be found without --config\nstderr: %s", stderr.String())
	assert.Contains(t, stdout.String(), "core", "the resolved config must actually be used")

	// An explicit --config is used as-is: no fallback to the yaml next to it.
	stderr.Reset()
	code = Run([]string{"status", "--root", root, "--config", "dispat.json"}, &stdout, &stderr)
	assert.Equal(t, 1, code, "an explicit --config must fail, not fall back")

	// The later names resolve too, in order.
	require.NoError(t, os.Rename(filepath.Join(root, "dispat.yaml"), filepath.Join(root, "dispat.yml")))
	require.Equal(t, 0, Run([]string{"status", "--root", root}, &stdout, &stderr))
	require.NoError(t, os.Rename(filepath.Join(root, "dispat.yml"), filepath.Join(root, "dispat.json")))
	require.Equal(t, 0, Run([]string{"status", "--root", root}, &stdout, &stderr))
}

func TestInitThenStatusComposeWithoutFlags(t *testing.T) {
	// `dispat init --format toml` followed by a plain `dispat status`: the
	// fallback finds dispat.toml with no --config anywhere, closing the loop
	// between the two commands (and exercising the TOML starter end to end —
	// the yaml fallbacks are covered by renaming, but JSON is not valid TOML).
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "packages", "core"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "packages", "core", "main.txt"), []byte("hi"), 0o644))
	git := func(args ...string) {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}
	git("init", "-q")
	git("config", "user.email", "test@example.com")
	git("config", "user.name", "Test")
	git("add", ".")
	git("commit", "-qm", "feat(core): first release")

	var stdout, stderr bytes.Buffer
	require.Equal(t, 0, Run([]string{"init", "--root", root, "--format", "toml"}, &stdout, &stderr),
		"stderr: %s", stderr.String())
	stdout.Reset()
	code := Run([]string{"status", "--root", root}, &stdout, &stderr)
	require.Equal(t, 0, code, "status must find dispat.toml without --config\nstderr: %s", stderr.String())
	assert.Contains(t, stdout.String(), "core")
}

// testConfigRunHooks wires every run-level hook to a script that records the
// stage it ran as, plus a postAll script dumping the run environment.
func testConfigRunHooks() config.File {
	cfg := testConfig()
	cfg.Scripts["hook"] = "echo $DISPAT_STAGE >> hooks.log"
	cfg.Scripts["dump"] = "env | grep '^DISPAT_' > postall.env"
	cfg.Commit = config.CommitConfig{Enabled: boolPtr(true), Push: true}
	cfg.Run = config.RunConfig{
		BeforeAll:    []string{"hook"},
		PostAll:      []string{"hook", "dump"},
		BeforeCommit: []string{"hook"},
		AfterCommit:  []string{"hook"},
		PostCommit:   []string{"hook"},
		BeforePush:   []string{"hook"},
		AfterPush:    []string{"hook"},
	}
	return cfg
}

func TestReleaseRunHooksOrderAndEnvironment(t *testing.T) {
	root := initRepo(t, testConfigRunHooks())
	addBareRemote(t, root)
	var stdout, stderr bytes.Buffer

	code := Run([]string{"--root", root}, &stdout, &stderr)
	require.Equal(t, 0, code, "stderr: %s\nstdout: %s", stderr.String(), stdout.String())

	// The hooks run in the monorepo root, bracketing their phases in order.
	data, err := os.ReadFile(filepath.Join(root, "hooks.log"))
	require.NoError(t, err)
	assert.Equal(t, "beforeAll\npostAll\nbeforeCommit\nafterCommit\npostCommit\nbeforePush\nafterPush\n",
		string(data))

	// postAll sees the run outcome and the workspace listing.
	env, err := os.ReadFile(filepath.Join(root, "postall.env"))
	require.NoError(t, err)
	assert.Contains(t, string(env), "DISPAT_PUBLISHED_PACKAGES=CORE")
	assert.Contains(t, string(env), "DISPAT_RESULT_CORE_STATUS=published")
	assert.Contains(t, string(env), "DISPAT_RESULT_CORE_NEW_VERSION=0.1.0")
	assert.Contains(t, string(env), "DISPAT_WORKSPACE_CORE_VERSION=0.1.0")
	assert.Contains(t, string(env), "DISPAT_FAILED_PACKAGES=\n")
	assert.Contains(t, string(env), "DISPAT_STAGE=postAll")

	// A second run releases nothing: postAll still reports the (empty) run,
	// but the commit and push hooks are no-ops without a publish.
	require.NoError(t, os.Remove(filepath.Join(root, "hooks.log")))
	var out2, err2 bytes.Buffer
	require.Equal(t, 0, Run([]string{"--root", root}, &out2, &err2), "%s", out2.String())
	data, err = os.ReadFile(filepath.Join(root, "hooks.log"))
	require.NoError(t, err)
	assert.Equal(t, "beforeAll\npostAll\n", string(data),
		"commit and push hooks must not run when nothing published")
	env, err = os.ReadFile(filepath.Join(root, "postall.env"))
	require.NoError(t, err)
	assert.Contains(t, string(env), "DISPAT_UNPLANNED_PACKAGES=CORE",
		"a package with nothing to release is reported as unplanned")
}

func TestReleaseRunHookFailureOnlyWarns(t *testing.T) {
	// A failing run hook warns and the rest of its sequence still runs; the
	// release itself stays successful. With the commit disabled, the commit
	// hooks never fire at all.
	cfg := testConfig()
	cfg.Scripts["boom"] = "exit 1"
	cfg.Scripts["hook"] = "echo $DISPAT_STAGE >> hooks.log"
	cfg.Run = config.RunConfig{PostAll: []string{"boom", "hook"}, BeforeCommit: []string{"hook"}}
	root := initRepo(t, cfg)
	var stdout, stderr bytes.Buffer

	code := Run([]string{"--root", root}, &stdout, &stderr)
	require.Equal(t, 0, code, "a failing run hook must not fail the run\n%s", stdout.String())
	assert.Contains(t, stdout.String(), "postAll script failed (not fatal)")

	tags, err := exec.Command("git", "-C", root, "tag").Output()
	require.NoError(t, err)
	assert.Contains(t, string(tags), "core@0.1.0", "the release went out regardless")

	data, err := os.ReadFile(filepath.Join(root, "hooks.log"))
	require.NoError(t, err)
	assert.Equal(t, "postAll\n", string(data),
		"the sequence continued past the failure; commit hooks stayed off")
}

// testConfigLogin exercises the config -> executor plumbing of run.login and
// the per-package hooks end to end. Package scripts run inside the package
// folder, so the markers land two levels up, in the monorepo root.
func testConfigLogin() config.File {
	cfg := testConfig()
	cfg.Scripts["login"] = "echo login >> ../../login.log"
	cfg.Scripts["hook"] = "echo $DISPAT_STAGE-$DISPAT_PACKAGE >> ../../pkg-hooks.log"
	cfg.Spaces["libs"] = config.SpaceConfig{Path: "packages", Run: config.SpaceRunConfig{
		Build: []string{"build"}, Publish: []string{"publish"}, Login: []string{"login"},
		BeforePublish: []string{"hook"}, Announce: []string{"hook"},
	}}
	return cfg
}

func TestReleaseLoginOncePerSpaceEndToEnd(t *testing.T) {
	root := initRepo(t, testConfigLogin())
	// A second package, so two publishes share the space's one login.
	require.NoError(t, os.MkdirAll(filepath.Join(root, "packages", "extra"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "packages", "extra", "main.txt"), []byte("x"), 0o644))
	git := exec.Command("git", "-C", root, "add", ".")
	require.NoError(t, git.Run())
	git = exec.Command("git", "-C", root, "commit", "-qm", "feat(extra): second package")
	require.NoError(t, git.Run())

	var stdout, stderr bytes.Buffer
	code := Run([]string{"--root", root}, &stdout, &stderr)
	require.Equal(t, 0, code, "stderr: %s\nstdout: %s", stderr.String(), stdout.String())

	data, err := os.ReadFile(filepath.Join(root, "login.log"))
	require.NoError(t, err)
	assert.Equal(t, "login\n", string(data), "two publishes, one space, one login")

	hooks, err := os.ReadFile(filepath.Join(root, "pkg-hooks.log"))
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(hooks)), "\n")
	assert.ElementsMatch(t, []string{
		"beforePublish-core", "beforePublish-extra",
		"announce-core", "announce-extra",
	}, lines, "the announce stage runs after each publish")
}

func TestReleaseBeforeAllHookFailureAbortsTheRun(t *testing.T) {
	// beforeAll is the one gating run hook: it fires before any release work,
	// so its failure stops the run before anything is built, published or
	// tagged.
	cfg := testConfig()
	cfg.Scripts["boom"] = "exit 1"
	cfg.Run = config.RunConfig{BeforeAll: []string{"boom"}}
	root := initRepo(t, cfg)
	var stdout, stderr bytes.Buffer

	code := Run([]string{"--root", root}, &stdout, &stderr)
	require.Equal(t, 1, code, "a failing beforeAll must abort the run\n%s", stdout.String())
	assert.Contains(t, stdout.String(), "beforeAll hook failed")

	tags, err := exec.Command("git", "-C", root, "tag").Output()
	require.NoError(t, err)
	assert.Empty(t, bytes.TrimSpace(tags), "nothing may be tagged")
	assert.NoFileExists(t, filepath.Join(root, "packages", "core", "CHANGELOG.md"),
		"no release work may run after the gate refused")
}

func TestReleasePrereleaseTrainConvergesEndToEnd(t *testing.T) {
	// The reported bug, end to end: release a beta, then run again with no
	// new commits — the second run must find nothing to release. Then a new
	// fix continues the train, and a graduation ends it.
	root := initRepo(t, testConfig())
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
		return string(bytes.TrimSpace(out))
	}
	git("commit", "-q", "--allow-empty", "-m", "feat(core)@beta!: break everything")

	// Run 1: the train starts at 1.0.0-beta.0.
	var out1, err1 bytes.Buffer
	require.Equal(t, 0, Run([]string{"--root", root}, &out1, &err1), "%s", out1.String())
	require.Contains(t, git("tag"), "core@1.0.0-beta.0")

	// Run 2, no new commits: nothing to do, no new tag, exit 0.
	var out2, err2 bytes.Buffer
	require.Equal(t, 0, Run([]string{"--root", root}, &out2, &err2), "%s", out2.String())
	assert.Equal(t, 1, strings.Count(git("tag"), "core@"),
		"a released prerelease must not re-release itself")
	assert.Contains(t, out2.String(), "unchanged")
	assert.NotContains(t, out2.String(), "W199",
		"the directive that started the train is contained, not redundant")

	// Run 3: a new fix continues the counter — and only once.
	git("commit", "-q", "--allow-empty", "-m", "fix(core): tweak")
	var out3, err3 bytes.Buffer
	require.Equal(t, 0, Run([]string{"--root", root}, &out3, &err3), "%s", out3.String())
	tags := git("tag")
	assert.Contains(t, tags, "core@1.0.0-beta.1", "new work continues the train")
	var out4, err4 bytes.Buffer
	require.Equal(t, 0, Run([]string{"--root", root}, &out4, &err4), "%s", out4.String())
	assert.Equal(t, 2, strings.Count(git("tag"), "core@"), "beta.1 converges too")

	// Run 5: graduation releases the whole train as 1.0.0.
	git("commit", "-q", "--allow-empty", "-m", "release(core)@stable: ship it")
	var out5, err5 bytes.Buffer
	require.Equal(t, 0, Run([]string{"--root", root}, &out5, &err5), "%s", out5.String())
	assert.Contains(t, git("tag"), "core@1.0.0", "the train graduates under the major it accumulated")

	// And a graduated train is converged as well.
	var out6, err6 bytes.Buffer
	require.Equal(t, 0, Run([]string{"--root", root}, &out6, &err6), "%s", out6.String())
	assert.Equal(t, 3, strings.Count(git("tag"), "core@"), "no repeat release after graduation")
}

func testConfigRunScripts() config.File {
	cfg := testConfig()
	libs := cfg.Spaces["libs"]
	libs.RunScripts = map[string]string{
		"lint": `echo "linted $DISPAT_PACKAGE at $DISPAT_NEW_VERSION" > lint.txt`,
	}
	cfg.Spaces["libs"] = libs
	return cfg
}

func TestRunCommand(t *testing.T) {
	root := initRepo(t, testConfigRunScripts())
	var stdout, stderr bytes.Buffer

	code := Run([]string{"run", "lint", "--root", root}, &stdout, &stderr)
	require.Equal(t, 0, code, "stdout: %s\nstderr: %s", stdout.String(), stderr.String())

	data, err := os.ReadFile(filepath.Join(root, "packages", "core", "lint.txt"))
	require.NoError(t, err, "the run script must execute inside the changed package")
	assert.Equal(t, "linted core at 0.1.0\n", string(data),
		"the run script receives the package's full DISPAT_* environment")
}

func TestRunCommandUnknownScript(t *testing.T) {
	root := initRepo(t, testConfigRunScripts())
	var stdout, stderr bytes.Buffer

	code := Run([]string{"run", "nope", "--root", root}, &stdout, &stderr)
	assert.Equal(t, 1, code, "a script no space defines must fail, not silently run nothing")
}

func TestRunCommandRequiresAName(t *testing.T) {
	root := initRepo(t, testConfigRunScripts())
	var stdout, stderr bytes.Buffer

	assert.Equal(t, 2, Run([]string{"run", "--root", root}, &stdout, &stderr))
	assert.Equal(t, 2, Run([]string{"run", "a", "b", "--root", root}, &stdout, &stderr))
}

func TestInitCommand(t *testing.T) {
	// dispat init writes a starter config that must itself load — a generated
	// config nobody can run is worse than none — in each supported format.
	for _, format := range []string{"json", "yaml", "toml"} {
		t.Run(format, func(t *testing.T) {
			root := t.TempDir()
			args := []string{"init", "--root", root}
			if format != "json" {
				args = append(args, "--format", format)
			}
			var stdout, stderr bytes.Buffer
			code := Run(args, &stdout, &stderr)
			require.Equal(t, 0, code, "stderr: %s", stderr.String())
			assert.Contains(t, stdout.String(), "created dispat."+format)

			file := filepath.Join(root, "dispat."+format)
			require.FileExists(t, file)
			_, err := config.Load(file, nil)
			require.NoError(t, err, "the starter config must load as written")
		})
	}
}

func TestInitCommandRefusesToOverwrite(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "dispat.json"), []byte("{}"), 0o644))
	var stdout, stderr bytes.Buffer
	code := Run([]string{"init", "--root", root}, &stdout, &stderr)
	assert.Equal(t, 1, code, "an existing config must never be overwritten")
	data, err := os.ReadFile(filepath.Join(root, "dispat.json"))
	require.NoError(t, err)
	assert.Equal(t, "{}", string(data), "the existing file must be untouched")

	code = Run([]string{"init", "--root", root, "--format", "ini"}, &stdout, &stderr)
	assert.Equal(t, 1, code, "an unknown format is an error")
}

func TestTestCommand(t *testing.T) {
	// dispat test runs one top-level script inside one package's folder with
	// the package's full DISPAT_* environment, releasing nothing.
	cfg := testConfig()
	cfg.Scripts["probe"] = `echo "$DISPAT_PACKAGE@$DISPAT_NEW_VERSION $DISPAT_STAGE" > probe.txt`
	root := initRepo(t, cfg)
	var stdout, stderr bytes.Buffer

	code := Run([]string{"test", "probe", "core", "--root", root}, &stdout, &stderr)
	require.Equal(t, 0, code, "stdout: %s\nstderr: %s", stdout.String(), stderr.String())
	data, err := os.ReadFile(filepath.Join(root, "packages", "core", "probe.txt"))
	require.NoError(t, err, "the script must run inside the package folder")
	assert.Equal(t, "core@0.1.0 test:probe\n", string(data),
		"the script receives the package's planned version and the test stage name")
	tags, err := exec.Command("git", "-C", root, "tag").Output()
	require.NoError(t, err)
	assert.Empty(t, bytes.TrimSpace(tags), "test releases nothing")

	code = Run([]string{"test", "nope", "core", "--root", root}, &stdout, &stderr)
	assert.Equal(t, 1, code, "an unknown script name is an error")
	code = Run([]string{"test", "probe", "ghost", "--root", root}, &stdout, &stderr)
	assert.Equal(t, 1, code, "an unknown package is an error")
	assert.Equal(t, 2, Run([]string{"test", "probe", "--root", root}, &stdout, &stderr),
		"test requires both the script and the package")
}

func TestTestCommandFailingScript(t *testing.T) {
	cfg := testConfig()
	cfg.Scripts["boom"] = "exit 7"
	root := initRepo(t, cfg)
	var stdout, stderr bytes.Buffer
	assert.Equal(t, 1, Run([]string{"test", "boom", "core", "--root", root}, &stdout, &stderr),
		"a failing script fails the command")
}

func TestPreviewCommand(t *testing.T) {
	root := initRepo(t, testConfig()) // one pending feat(core)
	var stdout, stderr bytes.Buffer

	code := Run([]string{"preview", "core", "--root", root}, &stdout, &stderr)
	require.Equal(t, 0, code, "stderr: %s", stderr.String())
	out := stdout.String()
	assert.Contains(t, out, "## core@0.1.0", "the header names the pending tag")
	assert.Contains(t, out, "### Features")
	assert.Contains(t, out, "- first release")

	// Release, then preview again: the window is empty.
	var relOut, relErr bytes.Buffer
	require.Equal(t, 0, Run([]string{"--root", root}, &relOut, &relErr), "%s", relOut.String())
	stdout.Reset()
	code = Run([]string{"preview", "core", "--root", root}, &stdout, &stderr)
	require.Equal(t, 0, code)
	assert.Contains(t, stdout.String(), "no pending changes for core")

	assert.Equal(t, 1, Run([]string{"preview", "ghost", "--root", root}, &stdout, &stderr),
		"an unknown package is an error")
	assert.Equal(t, 2, Run([]string{"preview", "--root", root}, &stdout, &stderr),
		"preview requires the package name")
}

func TestPrereleaseNotesWindowing(t *testing.T) {
	// The release-notes windowing across a whole train, end to end: each
	// prerelease's changelog entry documents only its own changeset, and the
	// graduation collects every prerelease's changes into one entry. The
	// version is still computed over the whole train — beta.1 stays 0.2.0-
	// based even though its entry only shows a fix.
	root := initRepo(t, testConfig())
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
		return string(bytes.TrimSpace(out))
	}
	run := func(args ...string) string {
		t.Helper()
		var stdout, stderr bytes.Buffer
		require.Equal(t, 0, Run(append(args, "--root", root), &stdout, &stderr),
			"stdout: %s\nstderr: %s", stdout.String(), stderr.String())
		return stdout.String()
	}
	// entry returns the changelog entry for the given tag: the text between
	// its "## <tag> " header and the next entry header.
	entry := func(tag string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(root, "packages", "core", "CHANGELOG.md"))
		require.NoError(t, err)
		text := string(data)
		start := strings.Index(text, "## "+tag+" ")
		require.GreaterOrEqual(t, start, 0, "no changelog entry for %s:\n%s", tag, text)
		rest := text[start+3:]
		if next := strings.Index(rest, "\n## "); next >= 0 {
			rest = rest[:next]
		}
		return rest
	}

	run() // core@0.1.0: flushes the initial feat, so the train starts clean

	git("commit", "-q", "--allow-empty", "-m", "feat(core)@beta: feature A")
	run()
	require.Contains(t, git("tag"), "core@0.2.0-beta.0")
	assert.Contains(t, entry("core@0.2.0-beta.0"), "feature A")

	git("commit", "-q", "--allow-empty", "-m", "fix(core): fix B")
	// The preview of beta.1 already narrows to the fresh changeset.
	preview := run("preview", "core")
	assert.Contains(t, preview, "fix B")
	assert.NotContains(t, preview, "feature A",
		"the preview of a prerelease must not repeat the train's published notes")
	run()
	require.Contains(t, git("tag"), "core@0.2.0-beta.1")
	beta1 := entry("core@0.2.0-beta.1")
	assert.Contains(t, beta1, "fix B")
	assert.NotContains(t, beta1, "feature A",
		"a prerelease entry contains only its own changeset")

	git("commit", "-q", "--allow-empty", "-m", "release(core)@stable: graduate")
	run()
	require.Contains(t, git("tag"), "core@0.2.0")
	graduated := entry("core@0.2.0")
	assert.Contains(t, graduated, "feature A", "the graduation collects the whole train")
	assert.Contains(t, graduated, "fix B")
}

func TestNewLoggerFallsBackOnUnknownLevel(t *testing.T) {
	// Config validation makes an unknown level unreachable through Run; the
	// constructor still degrades to info rather than panicking or silencing.
	var buf bytes.Buffer
	log := newLogger("not-a-level", "json", &buf)
	log.Info().Msg("visible")
	log.Debug().Msg("hidden")
	assert.Contains(t, buf.String(), "visible")
	assert.NotContains(t, buf.String(), "hidden", "the fallback level is info")
}
