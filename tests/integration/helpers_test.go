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

// githubFake serves the three calls the GitHub recorder makes — the upfront
// verification GET (200), the release lookup (404, nothing published yet)
// and the create-release POST (201) — recording every POST body. Each test
// decodes the bodies into whatever shape it asserts on, so one fake serves
// tests with different views of the payload; the attachment test keeps its
// own server (it also serves the upload endpoint).
//
// Every created tag is remembered, so a second run over the same plan sees
// the release it already made and skips it, exactly as GitHub would.
func githubFake(t *testing.T) (srv *httptest.Server, bodies func() [][]byte) {
	t.Helper()
	var mu sync.Mutex
	var recorded [][]byte
	published := map[string]bool{}
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if githubTagProbe(w, req, published) {
			return
		}
		switch req.Method {
		case http.MethodGet: // upfront verification
			w.WriteHeader(http.StatusOK)
		case http.MethodPost:
			data, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			recorded = append(recorded, data)
			var created struct {
				TagName string `json:"tag_name"`
			}
			if json.Unmarshal(data, &created) == nil && created.TagName != "" {
				published[created.TagName] = true
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
