// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

func TestCommitRefusesInvalidOrUnalignableRunEnvironmentBeforeMutation(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  []string
		want string
	}{
		{name: "invalid version", env: runEnvFor("core", "not-a-version", "core@0.1.0"), want: "does not parse"},
		{name: "foreign tag", env: runEnvFor("core", "0.2.0", "other@0.2.0"), want: "renders tag"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := singlePackageRepo(t, echoBuild)
			r.Commit("feat(core): pending release")
			beforeHead := r.Git("rev-parse", "HEAD")
			beforeStatus := r.Git("status", "--porcelain=v1")

			res := r.CommandEnv(tc.env, "commit", "--tag", "--log-format", "json")
			require.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.True(t, harness.IsCodePresent(res.Events, "E219"), "stdout:\n%s", res.Stdout)
			assert.Contains(t, diagnosticText(res), tc.want)
			assert.Equal(t, beforeHead, r.Git("rev-parse", "HEAD"))
			assert.Equal(t, beforeStatus, r.Git("status", "--porcelain=v1"))
			assert.Empty(t, r.TagList())
		})
	}
}

func TestStepAlignmentUsesPrereleaseChannelAndCorrectsProviderDestination(t *testing.T) {
	t.Run("prerelease channel", func(t *testing.T) {
		r := singlePackageRepo(t, echoBuild)
		r.Commit("feat(core): pending release")

		res := r.CommandEnv(runEnvFor("core", "0.2.0-beta.3", "core@0.2.0-beta.3"),
			"changelog", "--log-format", "json")
		require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.True(t, harness.IsCodePresentForPackage(res.Events, "W228", "core"), "stdout:\n%s", res.Stdout)
		assert.Contains(t, changelogOf(t, r, "core"), "core@0.2.0-beta.3")
	})

	t.Run("provider destination", func(t *testing.T) {
		r := linkedRepo(t, "core", "app", echoBuild)
		r.Commit("feat(core,app): provider and consumer move")
		env := runEnvFor("app", "0.1.0", "app@0.1.0",
			"DISPAT_UPDATED_PACKAGES=CORE",
			"DISPAT_UPDATED_CORE_NAME=core",
			"DISPAT_UPDATED_CORE_OLD_VERSION=0.0.0",
			"DISPAT_UPDATED_CORE_NEW_VERSION=9.9.9",
			"DISPAT_UPDATED_CORE_TAG=core@9.9.9")

		res := r.CommandEnv(env, "changelog", "--log-format", "json")
		require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.True(t, harness.IsCodePresentForPackage(res.Events, "W228", "app"), "stdout:\n%s", res.Stdout)
		assert.Contains(t, changelogOf(t, r, "app"), "core: 0.0.0 -> 9.9.9")
	})
}

func TestGitHubStepReadsLegacyOutputAndDropsForeignPackageExport(t *testing.T) {
	t.Run("legacy output file", func(t *testing.T) {
		var mu sync.Mutex
		var uploaded string
		var srv *httptest.Server
		srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if githubTagProbe(w, req, nil) {
				return
			}
			switch {
			case req.URL.Path == "/uploads":
				data, _ := io.ReadAll(req.Body)
				mu.Lock()
				uploaded = string(data)
				mu.Unlock()
				w.WriteHeader(http.StatusCreated)
			case req.Method == http.MethodGet:
				w.WriteHeader(http.StatusOK)
			default:
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"id":1,"upload_url":"` + srv.URL + `/uploads{?name,label}"}`))
			}
		}))
		defer srv.Close()

		r := harness.New(t)
		r.WriteConfigModel(githubConfig(srv.URL))
		t.Setenv("DISPAT_IT_TOKEN", "tkn")
		r.SeedPackage("packages", "core")
		r.Commit("feat(core): pending release")
		asset := r.Path("artifact.txt")
		r.WriteFile("artifact.txt", "legacy export bytes\n")
		output := r.Path("stage-output")
		require.NoError(t, os.WriteFile(output, []byte("DISPAT_EXPORT_GITHUB="+asset+"\n"), 0o644))

		res := r.CommandEnv([]string{"DISPAT_OUTPUT=" + output}, "github", "--package", "core")
		require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		mu.Lock()
		got := uploaded
		mu.Unlock()
		assert.Equal(t, "legacy export bytes\n", got)
	})

	t.Run("foreign package", func(t *testing.T) {
		srv, bodies := githubFake(t)
		r := linkedRepo(t, "core", "app", echoBuild)
		cfg := libsConfig(echoBuild, 1)
		cfg.GitHub = &models.GitHubConfig{Enabled: models.Bool(true), AllPackages: models.Bool(true),
			Owner: "acme", Repo: "mono", APIURL: srv.URL, TokenEnv: "DISPAT_IT_TOKEN"}
		r.WriteConfigModel(cfg)
		t.Setenv("DISPAT_IT_TOKEN", "tkn")
		r.Commit("feat(core,app): both release")
		asset := r.Path("foreign.txt")
		r.WriteFile("foreign.txt", "must not upload\n")
		env := append(runEnvFor("app", "0.1.0", "app@0.1.0"),
			"DISPAT_EXPORT_GITHUB="+asset)

		res := r.CommandEnv(env, "github", "--package", "core", "--log-level", "debug")
		require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Contains(t, res.Stdout+res.Stderr, "belongs to a package this invocation does not cover")
		require.Len(t, bodies(), 1)
		var created map[string]any
		require.NoError(t, json.Unmarshal(bodies()[0], &created))
		assert.Equal(t, "core@0.1.0", created["tag_name"])
		assert.NotContains(t, res.Stdout+res.Stderr, "foreign.txt uploaded")
	})
}

func TestReleaseContinuesWhenOneGitHubTargetCannotResolve(t *testing.T) {
	srv, bodies := githubFake(t)
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.UnsafeDisableLock = true
	cfg.GitHub = &models.GitHubConfig{Enabled: models.Bool(true), AllPackages: models.Bool(true),
		Owner: "acme", Repo: "mono", APIURL: srv.URL, TokenEnv: "DISPAT_IT_TOKEN"}
	cfg.Packages = map[string]models.PackageConfig{
		"app": {GitHub: &models.GitHubConfig{Repo: "unresolved", TokenEnv: "DISPAT_MISSING_TOKEN"}},
	}
	r.WriteConfigModel(cfg)
	t.Setenv("DISPAT_IT_TOKEN", "tkn")
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "app")
	r.Commit("feat(core,app): both release")

	res := r.Command("release", "--log-level", "debug")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout+res.Stderr, "github release disabled by config")
	assert.True(t, r.IsTagged("core@0.1.0"))
	assert.True(t, r.IsTagged("app@0.1.0"))
	require.Len(t, bodies(), 1, "only the resolvable package reaches GitHub")
	assert.True(t, strings.Contains(string(bodies()[0]), `"tag_name":"core@0.1.0"`))
}
