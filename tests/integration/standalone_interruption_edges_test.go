//go:build !windows

// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestStandaloneGitHubInterruptDrainsTheInFlightRequest proves cancellation of
// a standalone step reaches its HTTP work and is returned by the sweep drain.
// The server signals only after the release lookup is in flight, removing any
// timing guess from the interrupt; it then observes the request context close.
func TestStandaloneGitHubInterruptDrainsTheInFlightRequest(t *testing.T) {
	started := make(chan struct{}, 1)
	cancelled := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodGet && strings.Contains(req.URL.Path, "/releases/tags/") {
			started <- struct{}{}
			<-req.Context().Done()
			cancelled <- struct{}{}
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.GitHub = &models.GitHubConfig{Enabled: models.Bool(true), AllPackages: models.Bool(true),
		Owner: "acme", Repo: "mono", APIURL: srv.URL, TokenEnv: "DISPAT_IT_TOKEN"}
	cfg.Dependencies = []models.DependencyConfig{{Consumer: "app", Provider: "core"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "app")
	r.Commit("feat(core,app): pending release metadata")

	proc := r.StartCommandEnv([]string{"DISPAT_IT_TOKEN=token"}, "github", "--package", "*")
	select {
	case <-started:
	case <-time.After(15 * time.Second):
		t.Fatal("github release lookup never reached the server")
	}
	proc.Signal(os.Interrupt)
	res := proc.Wait()

	require.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout+res.Stderr, "github release interrupted")
	assert.Empty(t, r.TagList())
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("the interrupted command left its HTTP request running")
	}
}
