// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Long-tail coverage for the GitHub recorder: the answers it refuses to read
// as "nothing is published yet". Every one of them would otherwise turn a
// proxy, a permission or a truncation into a second release of a version that
// is already out, so each is a hard error naming the call that produced it.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// covTailGitHubLookupPath is the by-tag lookup every record makes before it
// creates anything. A scenario answers this one path its own way and leaves
// everything else to the ordinary fake behaviour.
const covTailGitHubLookupPath = "/releases/tags/"

// covTailGitHubServe stands up a recorder API whose by-tag lookup is the
// scenario's, answering the upfront verification, the listing and the create
// the way a healthy API would.
func covTailGitHubServe(t *testing.T, lookup http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodGet && strings.Contains(req.URL.Path, covTailGitHubLookupPath) {
			lookup(w, req)
			return
		}
		switch {
		case req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/releases"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[]`))
		case req.Method == http.MethodGet:
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id": 1}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// covTailGitHubRepo is the recorder fixture: one package, the export the
// recorder needs, and a token in the environment.
func covTailGitHubRepo(t *testing.T, apiURL string, adjust func(*models.File)) *harness.Repo {
	t.Helper()
	r := harness.New(t)
	cfg := githubConfig(apiURL)
	// Every package is recorded, so a scenario about the lookup does not have
	// to export an artefact it never attaches.
	cfg.GitHub.AllPackages = models.Bool(true)
	if adjust != nil {
		adjust(&cfg)
	}
	r.WriteConfigModel(cfg)
	t.Setenv("DISPAT_IT_TOKEN", "tkn")
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): bootstrap")
	return r
}

// TestCovTailGitHubRefusesALookupItCannotRead: "does this tag already have a
// release" is the question that decides whether anything is created, so an
// answer that is not one is a hard error rather than a shrug. A refusal, a
// body that is not JSON and a body past the bound are three ways to get a
// non-answer, and none of them may read as "nothing published yet".
func TestCovTailGitHubRefusesALookupItCannotRead(t *testing.T) {
	for name, tc := range map[string]struct {
		lookup http.HandlerFunc
		want   string
	}{
		"the endpoint refuses the lookup": {
			lookup: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"message":"Resource not accessible by personal access token"}`))
			},
			want: "looking up release",
		},
		"the lookup answers with something that is not JSON": {
			lookup: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`<html>a proxy sign-in page</html>`))
			},
			want: "parsing lookup",
		},
		"the lookup answers with more than a release can be": {
			// One release is read under a 1 MiB bound, above the 125,000
			// characters of notes GitHub accepts; an answer past that is not a
			// release at all.
			lookup: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"id":1,"body":"` + strings.Repeat("x", 1<<20+1) + `"}`))
			},
			want: "response exceeds",
		},
	} {
		t.Run(name, func(t *testing.T) {
			srv := covTailGitHubServe(t, tc.lookup)
			r := covTailGitHubRepo(t, srv.URL, nil)

			res := r.Command("github", "--package", "core")
			assert.NotEqual(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.Contains(t, res.Stdout+res.Stderr, tc.want,
				"the refusal names the call that produced it")
		})
	}
}

// TestCovTailGitHubDraftSearchReadsOnlyWhatItCanTrust: a draft has no tag ref,
// so the listing is the only thing that can see one — and a listing that does
// not parse is a permissions or proxy problem, not an answer, because reading
// it as "no draft yet" would create a second draft on every run. A listing
// that does parse and simply does not carry the draft within the pages the
// search bounds itself to says so and creates the release.
func TestCovTailGitHubDraftSearchReadsOnlyWhatItCanTrust(t *testing.T) {
	notFound := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) }
	draftConfig := func(cfg *models.File) {
		cfg.GitHub.Draft = models.Bool(true)
		cfg.GitHub.AllPackages = models.Bool(true)
	}

	t.Run("a listing that does not parse fails the record", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if githubTagProbe(w, req, nil) {
				return
			}
			if req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/releases") {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"message":"not a listing at all"}`))
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		t.Cleanup(srv.Close)
		r := covTailGitHubRepo(t, srv.URL, draftConfig)

		res := r.Command("github", "--package", "core")
		assert.NotEqual(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Contains(t, res.Stdout+res.Stderr, "parsing release listing")
	})

	t.Run("a listing with no draft in the searched pages creates the release", func(t *testing.T) {
		var mu sync.Mutex
		creates, pages := 0, 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if req.Method == http.MethodGet && strings.Contains(req.URL.Path, covTailGitHubLookupPath) {
				notFound(w, req)
				return
			}
			switch {
			case req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/releases"):
				mu.Lock()
				pages++
				mu.Unlock()
				// Every page is full, so the walk never stops early and runs
				// out at the bound instead.
				entries := make([]map[string]any, 0, 100)
				for i := range 100 {
					entries = append(entries, map[string]any{
						"tag_name": fmt.Sprintf("other@0.0.%d", i), "draft": false,
					})
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(entries)
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
		r := covTailGitHubRepo(t, srv.URL, draftConfig)

		res := r.Command("github", "--package", "core", "--log-level", "debug")
		require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Contains(t, res.Stdout, "no draft found within the searched pages")
		mu.Lock()
		defer mu.Unlock()
		assert.Equal(t, 3, pages, "the search bounds itself rather than walking the whole listing")
		assert.Equal(t, 1, creates, "and the release nobody had is created")
	})
}

// TestCovTailGitHubCannotAttachAFileItCannotRead: the export names files by
// path, and a path that passes the upfront checks can still refuse to open —
// a build artefact written with no read bit is the ordinary way. The release
// is already out by then, so the upload is what fails, named by the file.
func TestCovTailGitHubCannotAttachAFileItCannotRead(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file permissions do not gate a read on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root reads a file whatever its mode says")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if githubTagProbe(w, req, nil) {
			return
		}
		if req.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id": 7, "upload_url": "` + "http://127.0.0.1:1/uploads{?name,label}" + `"}`))
	}))
	t.Cleanup(srv.Close)

	r := covTailGitHubRepo(t, srv.URL, func(cfg *models.File) {
		cfg.Scripts["publish"] = models.Script{
			`echo artefact > locked.bin && chmod 000 locked.bin && ` +
				`echo "DISPAT_EXPORT_GITHUB=$PWD/locked.bin" >> "$DISPAT_OUTPUT"`,
		}
	})
	t.Cleanup(func() { _ = os.Chmod(r.Path("packages", "core", "locked.bin"), 0o600) })

	res := r.Release()
	out := res.Stdout + res.Stderr
	assert.Contains(t, out, "locked.bin", "the file that would not open is named")
	assert.Contains(t, out, "permission denied")
	assert.True(t, r.IsTagged("core@0.1.0"),
		"the release is out; only its attachment failed; tags: %v", r.TagList())
}

// TestCovTailGitHubCannotParseTheReleaseItCreated: the asset endpoint comes
// out of the created release's own response, so a create answering 201 with a
// body nobody can parse leaves the attachment with nowhere to go. The release
// stands and the failure names the parse rather than the upload.
func TestCovTailGitHubCannotParseTheReleaseItCreated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if githubTagProbe(w, req, nil) {
			return
		}
		if req.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`201 Created`))
	}))
	t.Cleanup(srv.Close)

	r := covTailGitHubRepo(t, srv.URL, func(cfg *models.File) {
		cfg.Scripts["publish"] = models.Script{
			`echo artefact > app.bin && echo "DISPAT_EXPORT_GITHUB=$PWD/app.bin" >> "$DISPAT_OUTPUT"`,
		}
	})

	res := r.Release()
	assert.Contains(t, res.Stdout+res.Stderr, "parsing created release")
	assert.True(t, r.IsTagged("core@0.1.0"),
		"the release was created; only its endpoint was unreadable; tags: %v", r.TagList())
}
