// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Coverage scenarios for the GitHub recorder's failure shapes: the transient
// answers a read-only call is re-issued for, the Retry-After the server may
// name, and the export entries and uploads a release cannot attach. A release
// is already out by the time any of the attachment work runs, so none of it
// may take the release back — what each case owes the reader is a line saying
// what was skipped and why.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// covGitHubVerifyPath is the endpoint the upfront verification asks for, and
// the only one these scenarios answer transiently: it is a read-only call, so
// it is the one marked safe to re-issue.
const covGitHubVerifyPath = "/repos/acme/mono"

// covRetryingGitHub is a fake whose verification endpoint answers the given
// statuses in order, the last one repeating, and carries the matching
// Retry-After header where one is given. Everything else it answers the way
// githubFake does.
func covRetryingGitHub(t *testing.T, answers []covGitHubAnswer) (*httptest.Server, func() int) {
	t.Helper()
	var mu sync.Mutex
	verifications := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == covGitHubVerifyPath && req.Method == http.MethodGet {
			mu.Lock()
			at := verifications
			verifications++
			mu.Unlock()
			if at >= len(answers) {
				at = len(answers) - 1
			}
			answer := answers[at]
			if answer.retryAfter != "" {
				w.Header().Set("Retry-After", answer.retryAfter)
			}
			w.WriteHeader(answer.status)
			_, _ = w.Write([]byte(`{"message":"try again"}`))
			return
		}
		if githubTagProbe(w, req, nil) {
			return
		}
		switch req.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[]`))
		default:
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id": 1}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, func() int {
		mu.Lock()
		defer mu.Unlock()
		return verifications
	}
}

// covGitHubAnswer is one scripted answer of the verification endpoint.
type covGitHubAnswer struct {
	status     int
	retryAfter string
}

// TestGitHubReissuesAReadOnlyCallThatFailedTransiently: a 5xx and a rate limit
// are answers a later attempt can outlive, so the verification is re-issued
// with backoff — honouring a Retry-After the server names in whole seconds and
// ignoring one it does not. The ladder is finite: a repository that never
// answers refuses the run rather than retrying forever.
func TestGitHubReissuesAReadOnlyCallThatFailedTransiently(t *testing.T) {
	seed := func(t *testing.T, srv *httptest.Server) *harness.Repo {
		t.Helper()
		r := harness.New(t)
		r.WriteConfigModel(githubConfig(srv.URL))
		t.Setenv("DISPAT_IT_TOKEN", "tkn")
		r.SeedPackage("packages", "core")
		r.Commit("feat(core): bootstrap")
		return r
	}

	t.Run("the third attempt succeeds", func(t *testing.T) {
		srv, verifications := covRetryingGitHub(t, []covGitHubAnswer{
			{status: http.StatusServiceUnavailable, retryAfter: "1"},
			{status: http.StatusTooManyRequests, retryAfter: "in a little while"},
			{status: http.StatusOK},
		})
		r := seed(t, srv)

		start := time.Now()
		r.ReleaseOK()
		assert.Equal(t, 3, verifications(), "the ladder is three attempts, not one")
		assert.True(t, r.IsTagged("core@0.1.0"), "tags: %v", r.TagList())
		assert.GreaterOrEqual(t, time.Since(start), time.Second,
			"a Retry-After of one second is waited out rather than ignored")
	})

	t.Run("a repository that never answers refuses the run", func(t *testing.T) {
		srv, verifications := covRetryingGitHub(t, []covGitHubAnswer{
			{status: http.StatusBadGateway, retryAfter: "-5"},
		})
		r := seed(t, srv)

		res := r.Release()
		assert.NotEqual(t, 0, res.Code, "stdout:\n%s", res.Stdout)
		assert.Contains(t, res.Stdout+res.Stderr, "502")
		assert.Equal(t, 3, verifications(), "and it gave up after the ladder rather than at the first refusal")
		assert.Equal(t, 0, r.TagCount("core@"), "tags: %v", r.TagList())
	})
}

// TestGitHubSkipsTheAttachmentsItCannotMake: the export names files, and every
// way of naming one that dispat cannot attach is a line rather than a failed
// release — the release is already out, and the sound files still deserve to
// be attached. A name repeated in the export is uploaded once, because the API
// would refuse the second copy anyway and one warning reads better than a 422.
func TestGitHubSkipsTheAttachmentsItCannotMake(t *testing.T) {
	type upload struct{ name, body string }
	var mu sync.Mutex
	var uploads []upload
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if githubTagProbe(w, req, nil) {
			return
		}
		switch {
		case req.Method == http.MethodGet:
			w.WriteHeader(http.StatusOK)
		case req.URL.Path == "/uploads":
			name := req.URL.Query().Get("name")
			body, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			mu.Lock()
			uploads = append(uploads, upload{name: name, body: string(body)})
			mu.Unlock()
			if name == "refused.txt" {
				// An upload is not re-issued: a half-done one is the next
				// run's reconcile job rather than this one's retry.
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"message":"no room"}`))
				return
			}
			w.WriteHeader(http.StatusCreated)
		default:
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"upload_url": "` + srv.URL + `/uploads{?name,label}"}`))
		}
	}))
	defer srv.Close()

	r := harness.New(t)
	cfg := githubConfig(srv.URL)
	cfg.Scripts = map[string]models.Script{
		"build": {"echo building"},
		"publish": {`mkdir -p dist/second && echo good > dist/app.bin && echo other > dist/second/app.bin` +
			` && echo refused > dist/refused.txt` +
			` && echo "DISPAT_EXPORT_GITHUB=$PWD/dist/app.bin $PWD/dist/second/app.bin` +
			` $PWD/dist/refused.txt dist/relative.bin $PWD/dist/nothing-here.bin $PWD/dist" >> "$DISPAT_OUTPUT"`},
	}
	r.WriteConfigModel(cfg)
	t.Setenv("DISPAT_IT_TOKEN", "tkn")
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): bootstrap with artefacts")

	res := r.Release()
	assert.True(t, r.IsTagged("core@0.1.0"),
		"the release is out whatever the attachments did; tags: %v", r.TagList())

	out := res.Stdout
	assert.Contains(t, out, "not an absolute path")
	assert.Contains(t, out, "names a directory, want a file")
	assert.Contains(t, out, "nothing-here.bin", "a path naming no file is named back")
	assert.Contains(t, out, "name repeats in the export")
	assert.Contains(t, out, "github release asset upload failed")

	mu.Lock()
	defer mu.Unlock()
	var names []string
	for _, u := range uploads {
		names = append(names, u.name)
	}
	assert.Equal(t, []string{"app.bin", "refused.txt"}, names,
		"the second app.bin is the repeat, and the invalid entries were never attempted")
	assert.Equal(t, "good\n", uploads[0].body,
		"the first path under a name is the one that is sent")
	assert.False(t, strings.Contains(out, "dist/second/app.bin uploaded"))
}

// covDraftListing serves a release listing whose first page is full of other
// releases and whose second page carries the draft, which is the shape a
// repository that has cut a hundred releases since has. The by-tag lookup
// answers 404 throughout, as GitHub's does for a draft: a draft creates no tag
// ref, so the listing is the only thing that knows it exists.
func covDraftListing(t *testing.T, tag string) (*httptest.Server, func() int) {
	t.Helper()
	const perPage = 100
	var mu sync.Mutex
	creates := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if githubTagProbe(w, req, nil) {
			return
		}
		switch {
		case req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/releases"):
			w.Header().Set("Content-Type", "application/json")
			if req.URL.Query().Get("page") == "1" {
				entries := make([]map[string]any, 0, perPage)
				for i := range perPage {
					entries = append(entries, map[string]any{
						"tag_name": fmt.Sprintf("other@0.0.%d", i), "draft": false,
					})
				}
				_ = json.NewEncoder(w).Encode(entries)
				return
			}
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"tag_name": tag, "draft": true, "upload_url": "http://127.0.0.1:1/uploads{?name,label}"},
			})
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
	return srv, func() int {
		mu.Lock()
		defer mu.Unlock()
		return creates
	}
}

// TestGitHubFindsADraftPastTheFirstPageOfTheListing: a draft has no tag ref,
// so the by-tag lookup can never see one and the listing is the only thing
// that can. A full page is not the end of the listing, and stopping there
// would leave a second draft behind on every run of a repository that has
// released a hundred times since.
func TestGitHubFindsADraftPastTheFirstPageOfTheListing(t *testing.T) {
	srv, creates := covDraftListing(t, "core@0.1.0")
	r := harness.New(t)
	cfg := githubConfig(srv.URL)
	cfg.GitHub.AllPackages = models.Bool(true)
	cfg.GitHub.Draft = models.Bool(true)
	r.WriteConfigModel(cfg)
	t.Setenv("DISPAT_IT_TOKEN", "tkn")
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): bootstrap")

	res := r.Command("github", "--package", "core")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.True(t, harness.IsCodePresent(res.Events, "W224"),
		"the draft on the second page is found and skipped: %s", res.Stdout)
	assert.Equal(t, 0, creates(), "and nothing was created a second time")
}

// TestGitHubRefusesToAttachWithoutAnUploadURL: the asset endpoint comes from
// the created release itself, because it is the release that knows where its
// own uploads go. A response that carries none is not an endpoint to guess at,
// and a release with files to attach and nowhere to put them says so.
func TestGitHubRefusesToAttachWithoutAnUploadURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if githubTagProbe(w, req, nil) {
			return
		}
		if req.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			return
		}
		// A created release with nothing in it: what an API proxy that trims
		// the response body leaves behind.
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id": 7}`))
	}))
	t.Cleanup(srv.Close)

	r := harness.New(t)
	cfg := githubConfig(srv.URL)
	cfg.Scripts["publish"] = models.Script{
		`echo artefact > app.bin && echo "DISPAT_EXPORT_GITHUB=$PWD/app.bin" >> "$DISPAT_OUTPUT"`,
	}
	r.WriteConfigModel(cfg)
	t.Setenv("DISPAT_IT_TOKEN", "tkn")
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): bootstrap with an artefact")

	res := r.Release()
	assert.Contains(t, res.Stdout+res.Stderr, "upload_url")
	assert.True(t, r.IsTagged("core@0.1.0"),
		"the release is out; only its attachment failed; tags: %v", r.TagList())
}
