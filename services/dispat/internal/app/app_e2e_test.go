package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/config"
)

// App-level end-to-end tests against real git repositories. The cli package
// covers the same flows through the command line; these drive App directly,
// which is the contract any other front end would use.
//
// Configs are authored as typed models (config aliases the public pkg/models
// structs) and marshalled to JSON, so a config that compiles is a config that
// loads.

func boolPtr(b bool) *bool { return &b }

// appBaseConfig returns the config every test starts from: one "libs" space
// at packages/ with echo build/publish scripts, serial, JSON logging, and
// GitHub disabled so a run never reaches the real API even when GITHUB_TOKEN
// / GITHUB_REPOSITORY happen to be set (e.g. in CI).
func appBaseConfig() config.File {
	return config.File{
		Scripts: map[string]string{"build": "echo building", "publish": "echo publishing"},
		Spaces: map[string]config.SpaceConfig{
			"libs": {Path: "packages", Run: config.SpaceRunConfig{
				Build: []string{"build"}, Publish: []string{"publish"}}},
		},
		Concurrency: []int{1},
		LogFormat:   "json",
		GitHub:      config.GitHubConfig{Enabled: boolPtr(false)},
	}
}

// appRepo creates a git monorepo with one "core" package, writes the config
// model as dispat.json, and returns an App logging into the returned buffer.
func appRepo(t *testing.T, cfgModel config.File) (*App, string, *bytes.Buffer) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	cfgJSON, err := json.MarshalIndent(cfgModel, "", "  ")
	require.NoError(t, err)
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "packages", "core"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "dispat.json"), cfgJSON, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "packages", "core", "main.txt"), []byte("hi"), 0o644))
	appGit(t, root, "init", "-q")
	appGit(t, root, "config", "user.email", "app@test")
	appGit(t, root, "config", "user.name", "app test")

	cfg, err := config.Load(filepath.Join(root, "dispat.json"), nil)
	require.NoError(t, err)
	var buf bytes.Buffer
	return New(root, cfg, zerolog.New(&buf)), root, &buf
}

func appGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return strings.TrimSpace(string(out))
}

func appTags(t *testing.T, root string) []string {
	t.Helper()
	out := appGit(t, root, "tag")
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

func TestAppStatusAndReleaseEndToEnd(t *testing.T) {
	a, root, _ := appRepo(t, appBaseConfig())
	appGit(t, root, "add", "-A")
	appGit(t, root, "commit", "-qm", "feat(core): first release")

	ctx := context.Background()
	require.NoError(t, a.Status(ctx), "status only reports")
	assert.Empty(t, appTags(t, root), "status must not tag")

	require.NoError(t, a.Release(ctx))
	assert.Equal(t, []string{"core@0.1.0"}, appTags(t, root))
	assert.FileExists(t, filepath.Join(root, "packages", "core", "CHANGELOG.md"),
		"the default changelog recorder ran")

	require.NoError(t, a.Release(ctx), "a converged release is a clean no-op")
	assert.Equal(t, []string{"core@0.1.0"}, appTags(t, root))
}

func TestAppReleaseRefusedUnderCommitErrorsError(t *testing.T) {
	cfg := appBaseConfig()
	cfg.CommitErrors = config.CommitErrorsError
	a, root, _ := appRepo(t, cfg)
	appGit(t, root, "add", "-A")
	appGit(t, root, "commit", "-qm", "feat(ghost): scope names no package")

	ctx := context.Background()
	require.NoError(t, a.Status(ctx), "a unit-scoped error still lets status print the plan")
	require.Error(t, a.Release(ctx), "commitErrors=error refuses the release")
	assert.Empty(t, appTags(t, root))
}

func TestAppInitialVersionsBaseline(t *testing.T) {
	cfg := appBaseConfig()
	cfg.Initials = map[string]string{"core": "1.2.0", "ghost": "9.9.9"}
	a, root, buf := appRepo(t, cfg)
	appGit(t, root, "add", "-A")
	appGit(t, root, "commit", "-qm", "fix(core): first release from the configured baseline")

	require.NoError(t, a.Release(context.Background()))
	assert.Equal(t, []string{"core@1.2.1"}, appTags(t, root), "initials seed the baseline the bump applies to")
	assert.Contains(t, buf.String(), "initials entry matches no discovered package",
		"an initials key naming nothing is warned about, not fatal")
}

func TestAppChangelogDisabled(t *testing.T) {
	cfg := appBaseConfig()
	cfg.Changelog = config.ChangelogConfig{Enabled: boolPtr(false)}
	a, root, _ := appRepo(t, cfg)
	appGit(t, root, "add", "-A")
	appGit(t, root, "commit", "-qm", "feat(core): no changelog wanted")

	require.NoError(t, a.Release(context.Background()))
	assert.Equal(t, []string{"core@0.1.0"}, appTags(t, root))
	assert.NoFileExists(t, filepath.Join(root, "packages", "core", "CHANGELOG.md"))
}

func TestAppGatingBeforeAllHookAbortsTheRelease(t *testing.T) {
	cfg := appBaseConfig()
	cfg.Scripts["no"] = "exit 1"
	cfg.Run = config.RunConfig{BeforeAll: []string{"no"}}
	a, root, _ := appRepo(t, cfg)
	appGit(t, root, "add", "-A")
	appGit(t, root, "commit", "-qm", "feat(core): never released")

	require.Error(t, a.Release(context.Background()), "the gating hook's failure aborts the run")
	assert.Empty(t, appTags(t, root), "nothing may be tagged after a failed beforeAll")
}

func TestAppCommitModeFinalizeWithGithub(t *testing.T) {
	// Release-commit mode end to end at the App level: tags deferred to the
	// finalize phase and placed on the release commit, the run-level commit
	// hooks firing around it, and the GitHub releases created last with the
	// release commit documented in their body.
	type ghRelease struct {
		TagName string `json:"tag_name"`
		Body    string `json:"body"`
	}
	var releases []ghRelease
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet { // upfront verification
			w.WriteHeader(http.StatusOK)
			return
		}
		var rel ghRelease
		require.NoError(t, json.NewDecoder(r.Body).Decode(&rel))
		releases = append(releases, rel)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	t.Setenv("DISPAT_APP_TOKEN", "tkn")

	cfg := appBaseConfig()
	cfg.Scripts["publish"] = `echo "DISPAT_EXPORT_GITHUB=" >> "$DISPAT_OUTPUT"`
	cfg.Scripts["hook"] = "echo $DISPAT_STAGE >> hooks.log"
	cfg.Run = config.RunConfig{
		BeforeAll: []string{"hook"}, PostAll: []string{"hook"},
		BeforeCommit: []string{"hook"}, AfterCommit: []string{"hook"}, PostCommit: []string{"hook"},
	}
	cfg.Commit = config.CommitConfig{Enabled: boolPtr(true)}
	cfg.GitHub = config.GitHubConfig{
		Enabled: boolPtr(true), Owner: "acme", Repo: "mono",
		APIURL: srv.URL, TokenEnv: "DISPAT_APP_TOKEN",
	}
	a, root, _ := appRepo(t, cfg)
	appGit(t, root, "add", "-A")
	appGit(t, root, "commit", "-qm", "feat(core): first release")

	require.NoError(t, a.Release(context.Background()))
	assert.Equal(t, []string{"core@0.1.0"}, appTags(t, root))

	subject := appGit(t, root, "log", "-1", "--format=%s")
	assert.Equal(t, "chore(release): core@0.1.0", subject, "the finalize phase created the release commit")
	tagTarget := appGit(t, root, "rev-list", "-n1", "core@0.1.0")
	head := appGit(t, root, "rev-parse", "HEAD")
	assert.Equal(t, head, tagTarget, "the tag points at the release commit")

	hooks, err := os.ReadFile(filepath.Join(root, "hooks.log"))
	require.NoError(t, err)
	assert.Equal(t, []string{"beforeAll", "postAll", "beforeCommit", "afterCommit", "postCommit"},
		strings.Fields(string(hooks)), "the run-level hooks fire in phase order")

	require.Len(t, releases, 1)
	assert.Equal(t, "core@0.1.0", releases[0].TagName)
	assert.Contains(t, releases[0].Body, "- commit: "+head, "the release documents the release commit")
}

func TestAppGithubReleaseSkippedWithoutExport(t *testing.T) {
	// The GitHub recorder is opt-in per package: a publish that exports no
	// DISPAT_EXPORT_GITHUB releases everything else — tag, changelog — but
	// creates no GitHub release.
	var posts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet { // upfront verification
			w.WriteHeader(http.StatusOK)
			return
		}
		posts++
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	t.Setenv("DISPAT_APP_TOKEN", "tkn")

	cfg := appBaseConfig()
	cfg.GitHub = config.GitHubConfig{
		Enabled: boolPtr(true), Owner: "acme", Repo: "mono",
		APIURL: srv.URL, TokenEnv: "DISPAT_APP_TOKEN",
	}
	a, root, buf := appRepo(t, cfg)
	appGit(t, root, "add", "-A")
	appGit(t, root, "commit", "-qm", "feat(core): released without a github release")

	require.NoError(t, a.Release(context.Background()))
	assert.Equal(t, []string{"core@0.1.0"}, appTags(t, root), "the release itself goes through")
	assert.Zero(t, posts, "no GitHub release may be created without the export")
	assert.Contains(t, buf.String(), "github release skipped")
}

func TestAppPushModeFailsFastWithoutARemote(t *testing.T) {
	cfg := appBaseConfig()
	cfg.Commit = config.CommitConfig{Enabled: boolPtr(true), Push: true}
	a, root, _ := appRepo(t, cfg)
	appGit(t, root, "add", "-A")
	appGit(t, root, "commit", "-qm", "feat(core): never released")

	require.Error(t, a.Release(context.Background()), "remote verification fails before any work")
	assert.Empty(t, appTags(t, root))
}

func TestAppRunScriptDirect(t *testing.T) {
	cfg := appBaseConfig()
	libs := cfg.Spaces["libs"]
	libs.RunScripts = map[string]string{"lint": "echo linted > lint.txt", "boom": "exit 1"}
	cfg.Spaces["libs"] = libs
	a, root, _ := appRepo(t, cfg)
	appGit(t, root, "add", "-A")
	appGit(t, root, "commit", "-qm", "feat(core): a change to run over")

	ctx := context.Background()
	require.NoError(t, a.RunScript(ctx, "lint", ""), "an empty policy defaults to skip")
	assert.FileExists(t, filepath.Join(root, "packages", "core", "lint.txt"),
		"the script ran inside the package folder")
	assert.Empty(t, appTags(t, root), "run releases nothing")

	require.Error(t, a.RunScript(ctx, "nope", OnErrorSkip), "an unknown script name is an error")
	require.Error(t, a.RunScript(ctx, "boom", OnErrorContinue), "a failing script fails the command")
}

func TestValidOnError(t *testing.T) {
	assert.True(t, ValidOnError(OnErrorSkip))
	assert.True(t, ValidOnError(OnErrorContinue))
	assert.False(t, ValidOnError(""))
	assert.False(t, ValidOnError("explode"))
}

func TestAppRunScriptCarriesProviderOutputs(t *testing.T) {
	// A provider's run script exports through DISPAT_OUTPUT; its consumer's
	// script receives the export as DISPAT_OUTPUT_<NAME> — the run command's
	// counterpart of the pipeline's per-package accumulation.
	cfg := appBaseConfig()
	libs := cfg.Spaces["libs"]
	libs.RunScripts = map[string]string{
		"carry": `if [ "$DISPAT_PACKAGE" = "base" ]; then echo "MARK=carried" >> "$DISPAT_OUTPUT"; else echo "$DISPAT_OUTPUT_MARK" > got.txt; fi`,
	}
	cfg.Spaces["libs"] = libs
	cfg.Dependencies = []config.DependencyConfig{{Consumer: "core", Provider: "base"}}
	a, root, _ := appRepo(t, cfg)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "packages", "base"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "packages", "base", "main.txt"), []byte("base"), 0o644))
	appGit(t, root, "add", "-A")
	appGit(t, root, "commit", "-qm", "feat(base,core): both change")

	require.NoError(t, a.RunScript(context.Background(), "carry", OnErrorSkip))
	data, err := os.ReadFile(filepath.Join(root, "packages", "core", "got.txt"))
	require.NoError(t, err)
	assert.Equal(t, "carried\n", string(data))
}
