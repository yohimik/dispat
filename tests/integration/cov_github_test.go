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
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

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

// The GitHub command reads JSON after receiving a successful status line.
// A server that stops there must not keep the CLI alive past its API timeout.
func TestGitHubHeadersWithoutBodyRespectTheRequestTimeout(t *testing.T) {
	hang := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-hang
	}))
	t.Cleanup(func() { close(hang); srv.Close() })

	r := harness.New(t)
	cfg := githubConfig(srv.URL)
	cfg.GitHub.AllPackages = models.Bool(true)
	r.WriteConfigModel(cfg)
	t.Setenv("DISPAT_IT_TOKEN", "tkn")
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): bootstrap")

	start := time.Now()
	res := r.Command("github", "--package", "core")
	assert.Less(t, time.Since(start), 45*time.Second)
	assert.NotEqual(t, 0, res.Code, "a partial GitHub response cannot complete the command")
	assert.Contains(t, res.Stdout+res.Stderr, "deadline exceeded")
}
