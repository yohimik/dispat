package integration

// Draft GitHub releases: github.draft and the --draft flag that overrides
// it. The fake keeps drafts apart from published releases exactly as GitHub
// does — a draft has no tag ref, so the by-tag lookup never answers with one
// and only the release listing knows it exists — which is what makes the
// re-run skip and the flip below claims about behaviour rather than about
// the fixture.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// ghDraft is the view of a created release these tests assert on.
type ghDraft struct {
	TagName string `json:"tag_name"`
	Draft   bool   `json:"draft"`
}

// draftRepo returns a one-package repository releasing to the fake, with the
// given github.draft policy, and the recorded create bodies.
func draftRepo(t *testing.T, draft *bool) (*harness.Repo, func() [][]byte) {
	t.Helper()
	srv, bodies := githubFake(t)

	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.GitHub = &models.GitHubConfig{
		Enabled: models.Bool(true), AllPackages: models.Bool(true), Draft: draft,
		Owner: "acme", Repo: "mono", APIURL: srv.URL, TokenEnv: "DISPAT_IT_TOKEN",
	}
	r.WriteConfigModel(cfg)
	t.Setenv("DISPAT_IT_TOKEN", "tkn")
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first release")
	return r, bodies
}

// TestDraftReleasesWaitForAHumanToPublish: with github.draft the release is
// created as a draft, and every later pass over the same release finds it and
// skips (W224). The skip is the load-bearing half: GitHub's by-tag lookup
// cannot see a draft, so without the listing search a re-run — or the run's
// own recorder after a step already published — would leave a second draft
// behind every time.
func TestDraftReleasesWaitForAHumanToPublish(t *testing.T) {
	r, bodies := draftRepo(t, models.Bool(true))

	first := r.Command("github", "--package", "core")
	require.Equal(t, 0, first.Code, "stderr: %s", first.Stderr)
	created := decodeAll[ghDraft](t, bodies())
	require.Len(t, created, 1)
	assert.Equal(t, "core@0.1.0", created[0].TagName)
	assert.True(t, created[0].Draft, "github.draft holds the release back")

	second := r.Command("github", "--package", "core")
	assert.Equal(t, 0, second.Code, "stderr: %s", second.Stderr)
	assert.Len(t, bodies(), 1, "the draft is never created twice")
	assert.True(t, harness.IsCodePresent(second.Events, "W224"), "the skip says which code it is")

	// And the run itself converges on the draft its own step left behind.
	r.ReleaseOK()
	assert.Len(t, bodies(), 1)
	assert.True(t, r.IsTagged("core@0.1.0"), "the tag is dispat's, whoever publishes the release")
}

// TestDraftSearchReadsTheListingItCanTrust: a draft has no tag ref, so the
// release listing is the only thing that can see one, and the search walks it
// page by page within a bound. A full page is not the end of the listing, so a
// draft on the second page is found and skipped (W224) rather than created a
// second time. A listing that does not parse is a permissions or proxy
// problem, not an answer, and fails the record instead of reading as "no
// draft yet". A listing that parses and carries no draft within the searched
// pages says so and creates the release.
func TestDraftSearchReadsTheListingItCanTrust(t *testing.T) {
	fullPage := func(w http.ResponseWriter) {
		entries := make([]map[string]any, 0, 100)
		for i := range 100 {
			entries = append(entries, map[string]any{"tag_name": fmt.Sprintf("other@0.0.%d", i), "draft": false})
		}
		_ = json.NewEncoder(w).Encode(entries)
	}
	for _, row := range []struct {
		name    string
		listing func(w http.ResponseWriter, req *http.Request)
		code    int
		want    string
		skipped bool
		pages   int
		creates int
	}{
		{
			name: "a draft past the first page is found",
			listing: func(w http.ResponseWriter, req *http.Request) {
				if req.URL.Query().Get("page") == "1" {
					fullPage(w)
					return
				}
				_ = json.NewEncoder(w).Encode([]map[string]any{{
					"tag_name": "core@0.1.0", "draft": true,
					"upload_url": "http://127.0.0.1:1/uploads{?name,label}",
				}})
			},
			skipped: true, pages: 2,
		},
		{
			name: "a listing that does not parse fails the record",
			listing: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"message":"not a listing at all"}`))
			},
			code: 1, want: "parsing release listing", pages: 1,
		},
		{
			name:    "a listing with no draft in the searched pages creates the release",
			listing: func(w http.ResponseWriter, _ *http.Request) { fullPage(w) },
			want:    "no draft found within the searched pages", pages: 3, creates: 1,
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			var mu sync.Mutex
			creates, pages := 0, 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if githubTagProbe(w, req, nil) {
					return
				}
				switch {
				case req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/releases"):
					mu.Lock()
					pages++
					mu.Unlock()
					w.Header().Set("Content-Type", "application/json")
					row.listing(w, req)
				case req.Method == http.MethodGet:
					w.WriteHeader(http.StatusOK)
				default:
					mu.Lock()
					creates++
					mu.Unlock()
					w.WriteHeader(http.StatusCreated)
					_, _ = w.Write([]byte(`{"id": 1}`))
				}
			}))
			t.Cleanup(srv.Close)
			r := harness.New(t)
			cfg := githubConfig(srv.URL)
			cfg.GitHub.AllPackages = models.Bool(true)
			cfg.GitHub.Draft = models.Bool(true)
			r.WriteConfigModel(cfg)
			t.Setenv("DISPAT_IT_TOKEN", "tkn")
			r.SeedPackage("packages", "core")
			r.Commit("feat(core): bootstrap")

			res := r.Command("github", "--package", "core", "--log-level", "debug")
			assert.Equal(t, row.code, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.Contains(t, res.Stdout+res.Stderr, row.want)
			assert.Equal(t, row.skipped, harness.IsCodePresent(res.Events, "W224"), "stdout:\n%s", res.Stdout)
			mu.Lock()
			defer mu.Unlock()
			assert.Equal(t, row.pages, pages, "the search reads the pages it needs and no more")
			assert.Equal(t, row.creates, creates, "a release is created only when no draft was found")
		})
	}
}

// TestDraftFlagHoldsBackAndTheFlipAbandonsTheDraft: --draft drafts a release
// a repository configured to publish would have published. Turning drafting
// off again is deliberately not a search for the draft that is now stale: a
// releaser that does not draft makes exactly the calls it always made, so it
// sees no draft at the tag and creates the published release GitHub accepts
// alongside it. The stale draft is left for a human to delete.
func TestDraftFlagHoldsBackAndTheFlipAbandonsTheDraft(t *testing.T) {
	r, bodies := draftRepo(t, nil)

	held := r.Command("github", "--package", "core", "--draft")
	require.Equal(t, 0, held.Code, "stderr: %s", held.Stderr)
	created := decodeAll[ghDraft](t, bodies())
	require.Len(t, created, 1)
	assert.True(t, created[0].Draft, "--draft beats a configuration that says nothing")

	flipped := r.Command("github", "--package", "core")
	require.Equal(t, 0, flipped.Code, "stderr: %s", flipped.Stderr)
	created = decodeAll[ghDraft](t, bodies())
	require.Len(t, created, 2, "the stale draft is invisible to a releaser that does not draft")
	assert.False(t, created[1].Draft, "the second release is published")
	assert.Equal(t, "core@0.1.0", created[1].TagName)

	// The published release is what every later pass now finds.
	r.ReleaseOK()
	assert.Len(t, bodies(), 2)
}

// TestDraftFlagPublishesOverAConfiguredDraft: --draft=false is the other
// half of the tri-state. An invocation publishes straight away over a
// repository that drafts, and the release it created is what the run's own
// drafting recorder then finds through the ordinary by-tag lookup.
func TestDraftFlagPublishesOverAConfiguredDraft(t *testing.T) {
	r, bodies := draftRepo(t, models.Bool(true))

	published := r.Command("github", "--package", "core", "--draft=false")
	require.Equal(t, 0, published.Code, "stderr: %s", published.Stderr)
	created := decodeAll[ghDraft](t, bodies())
	require.Len(t, created, 1)
	assert.False(t, created[0].Draft, "--draft=false publishes over github.draft")

	r.ReleaseOK()
	assert.Len(t, bodies(), 1, "the published release is the run's skip, drafting or not")
}
