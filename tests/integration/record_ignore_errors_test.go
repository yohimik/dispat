package integration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// The record commands, `dispat changelog` and `dispat github`, write what
// follows a publication. A record they could not write is logged as E222 and
// named in the command's summary, and by default the command still exits 0,
// so the stage script calling it does not turn a published package into a
// failed one and skip its consumers. `dispat commit` writes the tag, which is
// what records the version, and keeps failing.

// refusingGitHub serves the verification and the release lookup, and answers
// a creation for a tag in refusals with 422 as many times as the count says;
// every other creation is accepted and remembered, so the lookup then finds
// it. It returns the tags of the releases it created, in order.
func refusingGitHub(t *testing.T, refusals map[string]int) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var created []string
	published := map[string]bool{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if githubTagProbe(w, req, published) {
			return
		}
		if req.Method != http.MethodPost {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		var body struct {
			TagName string `json:"tag_name"`
		}
		require.NoError(t, json.NewDecoder(req.Body).Decode(&body))
		if refusals[body.TagName] > 0 {
			refusals[body.TagName]--
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"message":"Validation Failed"}`))
			return
		}
		created = append(created, body.TagName)
		published[body.TagName] = true
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"upload_url": "http://` + req.Host + `/uploads{?name,label}"}`))
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), created...)
	}
}

// githubRecordRepo is one releasing package, core, whose GitHub release is
// opted in and sent to apiURL.
func githubRecordRepo(t *testing.T, apiURL string) *harness.Repo {
	t.Helper()
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.GitHub = &models.GitHubConfig{
		Enabled: models.Bool(true), AllPackages: models.Bool(true),
		Owner: "acme", Repo: "mono", APIURL: apiURL, TokenEnv: "DISPAT_IT_TOKEN",
	}
	r.WriteConfigModel(cfg)
	t.Setenv("DISPAT_IT_TOKEN", "tkn")
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first release")
	return r
}

// assertRecordFailureReported checks the two places a failed record is
// reported whatever the exit code: the package's error line under E222, and
// the warning that closes the command naming the package.
func assertRecordFailureReported(t *testing.T, res harness.RunResult, headline, pkg string) {
	t.Helper()
	var failure, summary harness.Event
	for _, e := range res.Events {
		if e.Code() == "E222" && e.Package() == pkg {
			failure = e
		}
		if e.Str("message") == headline+" finished with failures" {
			summary = e
		}
	}
	require.NotNil(t, failure, "the record failure is logged under E222, stdout:\n%s", res.Stdout)
	assert.Equal(t, "error", failure.Str("level"))
	require.NotNil(t, summary, "the summary reports the failure, stdout:\n%s", res.Stdout)
	assert.Equal(t, "warn", summary.Str("level"))
	assert.Equal(t, []any{pkg}, summary["failedPackages"], "the summary names what is left to repair")
}

// TestRecordGitHubRefusalExitsZeroByDefault: a release GitHub refuses is an
// error the log and the summary name, and the command exits 0 unless
// --ignore-errors=false asks for the failure.
func TestRecordGitHubRefusalExitsZeroByDefault(t *testing.T) {
	srv, created := refusingGitHub(t, map[string]int{"core@0.1.0": 2})
	r := githubRecordRepo(t, srv.URL)

	res := r.Command("github", "--package", "core")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assertRecordFailureReported(t, res, "github release", "core")
	assert.Contains(t, res.Stdout, "Validation Failed", "the refusal itself is in the log")
	assert.Contains(t, res.Stdout, "exiting 0 under --ignore-errors")
	assert.Empty(t, created())

	strict := r.Command("github", "--package", "core", "--ignore-errors=false")
	assert.Equal(t, 1, strict.Code, "stdout:\n%s", strict.Stdout)
	assertRecordFailureReported(t, strict, "github release", "core")
	assert.NotContains(t, strict.Stdout, "exiting 0 under --ignore-errors")

	// The repair the summary points to: the next invocation creates it.
	repaired := r.Command("github", "--package", "core")
	require.Equal(t, 0, repaired.Code, "stdout:\n%s", repaired.Stdout)
	assert.Equal(t, []string{"core@0.1.0"}, created())
}

// TestRecordChangelogUnwritableExitsZeroByDefault: the same shape for an
// entry whose file cannot be written.
func TestRecordChangelogUnwritableExitsZeroByDefault(t *testing.T) {
	r := singlePackageRepo(t, echoBuild)
	r.Commit("feat(core): first release")
	// A folder where the changelog file belongs: the writer refuses it by name.
	require.NoError(t, os.Mkdir(r.Path("packages", "core", "CHANGELOG.md"), 0o755))

	res := r.Command("changelog", "--package", "core")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assertRecordFailureReported(t, res, "changelog", "core")
	assert.Contains(t, res.Stdout, "changelog write failed")

	strict := r.Command("changelog", "--package", "core", "--ignore-errors=false")
	assert.Equal(t, 1, strict.Code, "stdout:\n%s", strict.Stdout)
	assertRecordFailureReported(t, strict, "changelog", "core")

	// A refusal before anything is written is no record failure, and the flag
	// does not forgive it.
	refused := r.Command("changelog", "--package", "core", "--authors", "everywhere")
	assert.Equal(t, 1, refused.Code)
}

// TestRecordCommitPushRefusalStillFails: commit takes no --ignore-errors,
// because the tag it pushes is what records the version; a remote refusing
// the push keeps failing the command.
func TestRecordCommitPushRefusalStillFails(t *testing.T) {
	if os.PathSeparator != '/' {
		t.Skip("the refusing remote is a POSIX hook")
	}
	r := singlePackageRepo(t, echoBuild)
	r.Commit("feat(core): pushable feature")
	remote := r.AddBareRemote()
	r.Git("push", "-q", "origin", "HEAD:main")
	hook := filepath.Join(remote, "hooks", "pre-receive")
	require.NoError(t, os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o755))

	res := r.Command("commit", "--package", "core", "--tag", "--push")
	assert.Equal(t, 1, res.Code, "stdout:\n%s", res.Stdout)
	assert.True(t, harness.IsCodePresent(res.Events, "E224"), "the push failure is reported, stdout:\n%s", res.Stdout)
	assert.NotContains(t, r.Git("ls-remote", "--tags", remote), "core@0.1.0")

	foreign := r.Command("commit", "--package", "core", "--tag", "--push", "--ignore-errors")
	assert.Equal(t, 2, foreign.Code)
	assert.Contains(t, foreign.Stderr, "a tag commit could not create or push always fails it")
}

// TestRecordGitHubFailureInAReleaseKeepsTheConsumers: the flow this exists
// for. The publish stage tags the package and then creates its GitHub
// release; GitHub refuses it once. The package stays published, its consumer
// still releases, and the run's own recorder creates the missing release once
// the leg is done.
func TestRecordGitHubFailureInAReleaseKeepsTheConsumers(t *testing.T) {
	srv, created := refusingGitHub(t, map[string]int{"core@0.1.0": 1})
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Scripts["step-commit"] = models.Script{r.DispatCommand("commit", "--tag")}
	cfg.Scripts["step-github"] = models.Script{r.DispatCommand("github")}
	cfg.Spaces["libs"] = models.SpaceConfig{Path: models.PathList{"packages"}, Flow: &models.SpaceFlowConfig{
		Build: []string{"build"}, Publish: []string{"step-commit", "step-github"},
	}}
	cfg.Dependencies = []models.DependencyConfig{{Consumer: "app", Provider: "core"}}
	cfg.GitHub = &models.GitHubConfig{
		Enabled: models.Bool(true), AllPackages: models.Bool(true),
		Owner: "acme", Repo: "mono", APIURL: srv.URL, TokenEnv: "DISPAT_IT_TOKEN",
	}
	r.WriteConfigModel(cfg)
	t.Setenv("DISPAT_IT_TOKEN", "tkn")
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "app")
	r.Commit("feat(core,app): release both")

	res := r.ReleaseOK()
	assert.True(t, r.IsTagged("core@0.1.0"), "tags: %v", r.TagList())
	assert.True(t, r.IsTagged("app@0.1.0"), "the consumer was not skipped; tags: %v", r.TagList())
	assert.NotContains(t, res.Stdout, `"status":"failed"`)
	assert.Contains(t, res.Stdout, "E222", "the step's failure reached the run's log")
	assert.Equal(t, []string{"core@0.1.0", "app@0.1.0"}, created(),
		"each release is created once: core's by the run's recorder after its step failed, app's by its step")
}
