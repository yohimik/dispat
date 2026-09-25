package integration

// Shared fixtures. Only shapes used by more than one test file (or more
// than one scenario within a file) live here; a config exercised by exactly
// one test stays next to that test, written out in full, because the config
// *is* the test input and hiding it behind a builder would obscure what is
// being exercised.
//
// Configs are authored as typed models from the public pkg/models module and
// marshalled to JSON by WriteConfigModel; only shapes the model cannot
// express — an unknown key — fall back to a raw map[string]any through
// WriteConfigRaw.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// buildPublish is the standard space run object: a build and a publish stage
// referencing the "build" and "publish" scripts.
func buildPublish() *models.SpaceFlowConfig {
	return &models.SpaceFlowConfig{Build: []string{"build"}, Publish: []string{"publish"}}
}

// libsConfig returns the canonical one-space config: a "libs" space at
// packages/ running the given build script (as scripts["build"]) plus an echo
// publish, on top of harness.BaseFile(concurrency...).
func libsConfig(buildScript string, concurrency ...int) models.File {
	f := harness.BaseFile(concurrency...)
	f.Scripts = map[string]models.Script{"build": {buildScript}, "publish": {"echo publishing"}}
	f.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: models.PathList{"packages"}, Flow: buildPublish()},
	}
	return f
}

// packageNames returns [prefix0, prefix1, ...] — the package set of the
// budget-style concurrency scenarios.
func packageNames(n int, prefix string) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("%s%d", prefix, i)
	}
	return out
}

// seedIndependentPackages creates every named package under packages/ and
// commits them all with one multi-scope feat, so they all release in the
// same run with no dependency edges among them.
func seedIndependentPackages(r *harness.Repo, names []string) {
	scope := ""
	for i, n := range names {
		r.SeedPackage("packages", n)
		if i > 0 {
			scope += ","
		}
		scope += n
	}
	r.Commit(fmt.Sprintf("feat(%s): bootstrap %d independent packages", scope, len(names)))
}

// singlePackageRepo returns a repository with one "core" package under a
// one-space config running the given build script (working directory:
// packages/core). Nothing is committed yet: each scenario stages its own
// history.
func singlePackageRepo(t *testing.T, buildScript string) *harness.Repo {
	t.Helper()
	r := harness.New(t)
	r.WriteConfigModel(libsConfig(buildScript, 1))
	r.SeedPackage("packages", "core")
	return r
}

// linkedRepo returns a repository with two packages in one space and a
// consumer -> provider dependency edge between them, both stages running
// the given build script. Nothing is committed yet.
func linkedRepo(t *testing.T, provider, consumer, buildScript string) *harness.Repo {
	t.Helper()
	r := harness.New(t)
	cfg := libsConfig(buildScript, 1)
	cfg.Dependencies = []models.DependencyConfig{{Consumer: consumer, Provider: provider}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", provider)
	r.SeedPackage("packages", consumer)
	return r
}

// echoBuild is the inert build script of scenarios that assert on plan
// outcomes rather than on script execution.
const echoBuild = "echo building"

// markerBuild is the build script of scenarios that assert scripts ran — or
// did not run — according to the plan: each execution appends one line to
// build.log in the monorepo root (scripts run inside packages/<name>, two
// levels down). failIfMarker instead fails whenever a FAIL file exists in
// the package folder — the untracked marker the failure scenarios plant and
// later lift, without needing a commit either way.
const (
	markerBuild  = "echo ran >> ../../build.log"
	failIfMarker = "[ ! -f FAIL ]"
)

// buildRuns returns how many times markerBuild has executed: the line count
// of build.log, zero when no build script has run at all.
func buildRuns(r *harness.Repo) int {
	data, err := os.ReadFile(r.Path("build.log"))
	if err != nil {
		return 0
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return 0
	}
	return len(strings.Split(trimmed, "\n"))
}

// githubTagProbe answers the lookup the recorder makes before creating
// anything — "does this tag already have a release?" — with the 404 that
// means no, so that a first release goes through. published names the tags
// the fake should instead report as already there; a nil map means none.
// It reports whether it handled the request.
func githubTagProbe(w http.ResponseWriter, req *http.Request, published map[string]bool) bool {
	_, tag, found := strings.Cut(req.URL.Path, "/releases/tags/")
	if req.Method != http.MethodGet || !found {
		return false
	}
	if decoded, err := url.PathUnescape(tag); err == nil {
		tag = decoded
	}
	if published[tag] {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id": 1}`))
		return true
	}
	w.WriteHeader(http.StatusNotFound)
	return true
}

// githubFake serves the four calls the GitHub recorder makes — the upfront
// verification GET (200), the release lookup (404, nothing published yet),
// the release listing the draft search reads, and the create-release POST
// (201) — recording every POST body. Each test decodes the bodies into
// whatever shape it asserts on, so one fake serves tests with different views
// of the payload; the attachment test keeps its own server (it also serves
// the upload endpoint).
//
// Every created tag is remembered, so a second run over the same plan sees
// the release it already made and skips it, exactly as GitHub would. A draft
// is remembered apart: GitHub creates no tag ref for one, so the by-tag
// lookup keeps answering 404 and only the listing knows about it.
func githubFake(t *testing.T) (srv *httptest.Server, bodies func() [][]byte) {
	t.Helper()
	var mu sync.Mutex
	var recorded [][]byte
	published := map[string]bool{}
	var drafts []string
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if githubTagProbe(w, req, published) {
			return
		}
		switch {
		case req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/releases"):
			entries := make([]map[string]any, 0, len(drafts))
			for _, tag := range drafts {
				entries = append(entries, map[string]any{"tag_name": tag, "draft": true})
			}
			data, err := json.Marshal(entries)
			require.NoError(t, err)
			_, _ = w.Write(data)
		case req.Method == http.MethodGet: // upfront verification
			w.WriteHeader(http.StatusOK)
		case req.Method == http.MethodPost:
			data, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			recorded = append(recorded, data)
			var created struct {
				TagName string `json:"tag_name"`
				Draft   bool   `json:"draft"`
			}
			if json.Unmarshal(data, &created) == nil && created.TagName != "" {
				if created.Draft {
					drafts = append(drafts, created.TagName)
				} else {
					published[created.TagName] = true
				}
			}
			w.WriteHeader(http.StatusCreated)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, func() [][]byte {
		mu.Lock()
		defer mu.Unlock()
		return append([][]byte(nil), recorded...)
	}
}

// decodeAll unmarshals every recorded body into T, in call order.
func decodeAll[T any](t *testing.T, bodies [][]byte) []T {
	t.Helper()
	out := make([]T, len(bodies))
	for i, b := range bodies {
		require.NoError(t, json.Unmarshal(b, &out[i]))
	}
	return out
}

// assertOrderedIn fails unless every marker appears in text, in the order
// given — how a test states the shape of a rendered record without pinning
// the bytes between the parts it cares about.
func assertOrderedIn(t *testing.T, text string, markers ...string) {
	t.Helper()
	at := -1
	for _, marker := range markers {
		i := strings.Index(text, marker)
		require.NotEqual(t, -1, i, "missing %q in:\n%s", marker, text)
		assert.Greater(t, i, at, "%q is out of order in:\n%s", marker, text)
		at = i
	}
}

// refusalRepo is the one repository every refusal subtest rewrites the config
// of. A refused configuration changes nothing on disk, so one fixture serves
// the whole table and each subtest still starts from the same state.
func refusalRepo(t *testing.T) *harness.Repo {
	t.Helper()
	r := harness.New(t)
	r.SeedPackage("packages", "core")
	r.WriteConfigModel(libsConfig(echoBuild, 1))
	r.Commit("feat(core): bootstrap")
	return r
}

// refusal is one row of a refusal table: what the configuration says, and the
// sentence the reader is owed for it.
type refusal struct {
	name   string
	mutate func(*models.File)
	want   string
}

// diagnosticText is everything one refused invocation said, with the
// structured fields decoded. A refusal that happens before the configured
// logger exists is written by the bootstrap logger, which renders the
// sentence as a JSON-escaped `error` field, so the decoded field is what a
// test may assert the wording against.
func diagnosticText(res harness.RunResult) string {
	var b strings.Builder
	b.WriteString(res.Stdout)
	b.WriteString(res.Stderr)
	for _, stream := range []string{res.Stdout, res.Stderr} {
		for _, e := range harness.ParseEvents(stream) {
			b.WriteString("\n" + e.Str("error"))
			b.WriteString("\n" + e.Str("message"))
		}
	}
	return b.String()
}

// runRefusals writes each row's configuration and requires that `dispat
// status` refuses it, naming the mistake and releasing nothing.
func runRefusals(t *testing.T, r *harness.Repo, rows []refusal) {
	t.Helper()
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			cfg := libsConfig(echoBuild, 1)
			row.mutate(&cfg)
			r.WriteConfigModel(cfg)
			res := r.Status("--log-format", "json")
			require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.Contains(t, diagnosticText(res), row.want)
			assert.Empty(t, r.TagList(), "a refused configuration releases nothing")
		})
	}
}

// refuseStatus requires that `dispat status` refuses this repository as it
// stands, naming want and releasing nothing.
func refuseStatus(t *testing.T, r *harness.Repo, want string) {
	t.Helper()
	res := r.Status("--log-format", "json")
	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, diagnosticText(res), want)
	assert.Empty(t, r.TagList(), "a refused configuration releases nothing")
}

// writeJSON writes any config-shaped value as a folder's own dispat.json.
func writeJSON(t *testing.T, r *harness.Repo, relPath string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	require.NoError(t, err)
	r.WriteFile(relPath, string(data))
}

// configRefused runs `dispat status` and requires a refusal whose text
// carries want.
//
// The comparison is made against the output with one level of quoting taken
// out, because a refusal reaches the reader through whichever writer is
// already standing — the JSON logger once the config loaded, the boot logger
// when it did not — and the two escape the quotes in a label such as
// spaces["libs"] differently. Neither spelling is what the scenario is about.
func configRefused(t *testing.T, r *harness.Repo, want string) {
	t.Helper()
	res := r.Status()
	assert.NotEqual(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, strings.ReplaceAll(res.Stdout+res.Stderr, `\"`, `"`), want)
}

// readRepoFile reads a file relative to the repository root, failing the test
// when it is absent — the form for reading a record a package wrote somewhere
// other than the default changelog name.
func readRepoFile(t *testing.T, r *harness.Repo, relPath string) string {
	t.Helper()
	data, err := os.ReadFile(r.Path(relPath))
	require.NoError(t, err, "reading %s", relPath)
	return string(data)
}

// skipIfSuperuser skips a scenario that relies on filesystem permissions
// actually stopping the process.
func skipIfSuperuser(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("a read-only folder does not stop the superuser")
	}
}

// jsonLine is the first logged line whose message is msg, failing the
// test when there is none.
func jsonLine(t *testing.T, res harness.RunResult, msg string) harness.Event {
	t.Helper()
	for _, e := range res.Events {
		if e.Str("message") == msg {
			return e
		}
	}
	t.Fatalf("no %q line in:\n%s\nstderr:\n%s", msg, res.Stdout, res.Stderr)
	return nil
}

// readFileString reads a file a scenario expects to exist, whole.
func readFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}
