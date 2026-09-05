package integration

// Test-plan goals 7, 18 and 29 meet at the ambiguous external-write
// boundary: GitHub persists a release, then the response disappears.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

func TestReleaseReconcilesGithubCreateWhoseResponseWasLost(t *testing.T) {
	var mu sync.Mutex
	persisted := map[string]bool{}
	posts := map[string]int{}
	dropped := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if githubTagProbe(w, req, persisted) {
			return
		}
		if req.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			return
		}
		var body struct {
			Tag string `json:"tag_name"`
		}
		require.NoError(t, json.NewDecoder(req.Body).Decode(&body))
		persisted[body.Tag] = true
		posts[body.Tag]++
		if !dropped {
			dropped = true
			hijacker, ok := w.(http.Hijacker)
			require.True(t, ok)
			conn, _, err := hijacker.Hijack()
			require.NoError(t, err)
			require.NoError(t, conn.Close())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":2,"upload_url":"https://example.invalid/{?name}"}`))
	}))
	defer srv.Close()

	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.GitHub = &models.GitHubConfig{Enabled: models.Bool(true), AllPackages: models.Bool(true),
		Owner: "acme", Repo: "mono", APIURL: srv.URL, TokenEnv: "DISPAT_IT_TOKEN"}
	r.WriteConfigModel(cfg)
	t.Setenv("DISPAT_IT_TOKEN", "tkn")
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "utils")
	r.Commit("feat(core,utils): publish independent packages")

	first := r.Release()
	require.NotEqual(t, 0, first.Code, "a lost response leaves the run incomplete")
	assert.Contains(t, first.Stdout+first.Stderr, "release recording failed")
	assert.True(t, harness.HasCode(first.Events, "E222"), "the ambiguous record is a post-publish critical")
	assert.True(t, r.HasTag("core@0.1.0"), "the published package keeps its durable tag")
	assert.True(t, r.HasTag("utils@0.1.0"), "independent work continues after the ambiguous response")
	mu.Lock()
	firstPosts := posts["core@0.1.0"] + posts["utils@0.1.0"]
	mu.Unlock()
	assert.Equal(t, 2, firstPosts, "both independent GitHub records were attempted")

	second := r.Release()
	require.Equal(t, 0, second.Code, "stdout:\n%s\nstderr:\n%s", second.Stdout, second.Stderr)
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, firstPosts, posts["core@0.1.0"]+posts["utils@0.1.0"],
		"the tag baselines make the rerun a no-op instead of duplicating a persisted release")
}
