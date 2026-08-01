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

	"github.com/yohimik/dispat/internal/config"
)

// github is disabled in test configs so runs never touch the real API even
// when GITHUB_TOKEN / GITHUB_REPOSITORY are present (e.g. in CI).
const testConfig = `{
  "scripts": {
    "build": "echo building",
    "publish": "echo publishing"
  },
  "spaces": {
    "libs": {"path": "packages", "buildScript": "build", "publishScript": "publish"}
  },
  "concurrency": 1,
  "logLevel": "info",
  "logFormat": "json",
  "github": {"enabled": false}
}`

const testConfigNoChangelog = `{
  "scripts": {"build": "echo building", "publish": "echo publishing"},
  "spaces": {"libs": {"path": "packages", "buildScript": "build", "publishScript": "publish"}},
  "concurrency": 1,
  "logLevel": "info",
  "logFormat": "json",
  "changelog": {"enabled": false},
  "github": {"enabled": false}
}`

// initRepo creates a git monorepo with one package and one feat commit.
func initRepo(t *testing.T, cfg string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "packages", "core"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "dispat.json"), []byte(cfg), 0o644))
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
	git("commit", "-qm", "feat(core): first release")
	return root
}

func TestStatusCommand(t *testing.T) {
	root := initRepo(t, testConfig)
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
	root := initRepo(t, testConfig)
	var stdout, stderr bytes.Buffer

	code := Run([]string{"--root", root}, &stdout, &stderr)
	require.Equal(t, 0, code, "stderr: %s\nstdout: %s", stderr.String(), stdout.String())

	tags, err := exec.Command("git", "-C", root, "tag").Output()
	require.NoError(t, err)
	assert.Contains(t, string(tags), "core@0.1.0")
	assert.FileExists(t, filepath.Join(root, "packages", "core", "CHANGELOG.md"))
}

func TestReleaseCommandChangelogDisabled(t *testing.T) {
	root := initRepo(t, testConfigNoChangelog)
	var stdout, stderr bytes.Buffer

	code := Run([]string{"--root", root}, &stdout, &stderr)
	require.Equal(t, 0, code, "stderr: %s\nstdout: %s", stderr.String(), stdout.String())

	tags, err := exec.Command("git", "-C", root, "tag").Output()
	require.NoError(t, err)
	assert.Contains(t, string(tags), "core@0.1.0", "tagging is independent of the changelog")
	assert.NoFileExists(t, filepath.Join(root, "packages", "core", "CHANGELOG.md"))
}

const testConfigInitials = `{
  "scripts": {"build": "echo building", "publish": "echo publishing"},
  "spaces": {"libs": {"path": "packages", "buildScript": "build", "publishScript": "publish"}},
  "concurrency": 1,
  "logLevel": "info",
  "logFormat": "json",
  "initials": {"core": "1.0.0", "ghost": "5.0.0"},
  "github": {"enabled": false}
}`

func TestStatusInitialsWithUnparseableTag(t *testing.T) {
	root := initRepo(t, testConfigInitials)
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}
	// Newest (only) tag is unparseable; a fix lands after it.
	git("tag", "-a", "core@0.1.0-broken", "-m", "broken tag")
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

const testConfigRevert = `{
  "scripts": {
    "bad-build": "echo dirty > main.txt && echo junk > generated.txt && exit 1"
  },
  "spaces": {
    "libs": {"path": "packages", "revertOnFail": true, "buildScript": "bad-build"}
  },
  "concurrency": 1,
  "logLevel": "info",
  "logFormat": "json",
  "github": {"enabled": false}
}`

func TestReleaseRevertOnFail(t *testing.T) {
	root := initRepo(t, testConfigRevert)
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

const testConfigCommitPush = `{
  "scripts": {"build": "echo building", "publish": "echo publishing"},
  "spaces": {"libs": {"path": "packages", "buildScript": "build", "publishScript": "publish"}},
  "concurrency": 1,
  "logLevel": "info",
  "logFormat": "json",
  "commit": {"enabled": true, "messageFormat": "release: {packages} -> {tags}", "push": true},
  "github": {"enabled": false}
}`

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
	root := initRepo(t, testConfigCommitPush)
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
	root := initRepo(t, testConfigCommitPush)
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

	cfg := `{
  "scripts": {"build": "echo building", "publish": "echo publishing"},
  "spaces": {"libs": {"path": "packages", "buildScript": "build", "publishScript": "publish"}},
  "concurrency": 1,
  "logLevel": "info",
  "logFormat": "json",
  "commit": {"enabled": true},
  "github": {"enabled": true, "owner": "acme", "repo": "mono", "apiUrl": "` + srv.URL + `", "tokenEnv": "DISPAT_TEST_TOKEN"}
}`
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

const testConfigCatchUp = `{
  "scripts": {"build": "[ ! -f FAIL ]", "publish": "echo publishing"},
  "spaces": {"libs": {"path": "packages", "buildScript": "build", "publishScript": "publish"}},
  "dependencies": [{"consumer": "app", "provider": "core"}],
  "concurrency": 1,
  "logLevel": "info",
  "logFormat": "json",
  "github": {"enabled": false}
}`

func TestReleaseCatchUpAfterConsumerFailure(t *testing.T) {
	// The orphaned-consumer scenario end to end. Run 1: core publishes and
	// is tagged, app's build fails (FAIL marker). Run 2, with no new
	// commits: app must be caught up and released; core must not re-release.
	root := initRepo(t, testConfigCatchUp)
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

func TestRenderCommitMessage(t *testing.T) {
	pkgs := []string{"core", "utils"}
	tags := []string{"core@1.1.0", "utils@2.0.1"}
	assert.Equal(t, "chore(release): core@1.1.0, utils@2.0.1",
		renderCommitMessage("", pkgs, tags), "default format")
	assert.Equal(t, "publish core, utils as core@1.1.0, utils@2.0.1",
		renderCommitMessage("publish {packages} as {tags}", pkgs, tags))
	assert.Equal(t, "no placeholders", renderCommitMessage("no placeholders", pkgs, tags))
}

func TestUnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"bogus"}, &stdout, &stderr)
	assert.Equal(t, 2, code)
}

func TestInvalidConfig(t *testing.T) {
	root := t.TempDir() // no dispat.json
	var stdout, stderr bytes.Buffer
	code := Run([]string{"status", "--root", root}, &stdout, &stderr)
	assert.Equal(t, 1, code)
}

func TestGithubReleaserResolution(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "envowner/envrepo")
	t.Setenv("GITHUB_TOKEN", "envtoken")
	t.Setenv("CUSTOM_TOKEN", "customtoken")

	gh, err := githubReleaser(config.GitHubConfig{})
	require.NoError(t, err)
	assert.Equal(t, "envowner", gh.Owner)
	assert.Equal(t, "envrepo", gh.Repo)
	assert.Equal(t, "envtoken", gh.Token)

	gh, err = githubReleaser(config.GitHubConfig{Owner: "acme", Repo: "mono", TokenEnv: "CUSTOM_TOKEN"})
	require.NoError(t, err)
	assert.Equal(t, "acme", gh.Owner)
	assert.Equal(t, "mono", gh.Repo)
	assert.Equal(t, "customtoken", gh.Token)

	t.Setenv("GITHUB_REPOSITORY", "")
	_, err = githubReleaser(config.GitHubConfig{})
	assert.ErrorContains(t, err, "no repository")

	t.Setenv("GITHUB_TOKEN", "")
	_, err = githubReleaser(config.GitHubConfig{Owner: "acme", Repo: "mono"})
	assert.ErrorContains(t, err, "GITHUB_TOKEN")
}
