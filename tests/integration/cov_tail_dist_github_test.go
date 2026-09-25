// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Long-tail coverage for the GitHub recorder: the answers it refuses to read
// as "nothing is published yet". Every one of them would otherwise turn a
// proxy, a permission or a truncation into a second release of a version that
// is already out, so each is a hard error naming the call that produced it.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

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
