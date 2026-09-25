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

// TestStepRefusesARunEnvironmentItCannotHonor: a step invoked inside a run is
// held to that run's answers, so an environment it cannot read, and a plan it
// cannot align to the environment, stop the step with E219 before anything is
// written: no commit, no tag, no changelog byte and no change to the tree.
// The rows are the environments a run cannot easily be made to produce: a
// version that does not parse, a listing naming an update whose variables are
// not there, a pin the step's own plan does not release, and a tag the aligned
// version does not render, for the changelog and the commit step alike.
func TestStepRefusesARunEnvironmentItCannotHonor(t *testing.T) {
	for _, row := range []struct {
		name string
		args []string
		env  []string
		// released publishes the pending work first, so the step's own plan
		// has nothing left to release.
		released bool
		want     string
	}{
		{name: "changelog: a version that does not parse", args: []string{"changelog"},
			env: runEnvFor("core", "one point oh", "core@0.1.0"), want: "does not parse"},
		{name: "changelog: a listing naming an update it does not describe", args: []string{"changelog"},
			env:  runEnvFor("core", "0.1.0", "core@0.1.0", "DISPAT_UPDATED_PACKAGES=UTILS"),
			want: "do not describe an update"},
		{name: "changelog: a package the step's own plan does not release", args: []string{"changelog"},
			env: runEnvFor("core", "0.2.0", "core@0.2.0"), released: true, want: "the step's own plan does not"},
		{name: "changelog: a tag the aligned version does not render", args: []string{"changelog"},
			env: runEnvFor("core", "0.2.0", "core-v0.2.0"), want: "renders tag"},
		{name: "commit: a version that does not parse", args: []string{"commit", "--tag"},
			env: runEnvFor("core", "not-a-version", "core@0.1.0"), want: "does not parse"},
		{name: "commit: a tag the aligned version does not render", args: []string{"commit", "--tag"},
			env: runEnvFor("core", "0.2.0", "other@0.2.0"), want: "renders tag"},
	} {
		t.Run(row.name, func(t *testing.T) {
			r := singlePackageRepo(t, echoBuild)
			r.Commit("feat(core): pending release")
			if row.released {
				r.ReleaseOK()
				require.True(t, r.IsTagged("core@0.1.0"), "tags: %v", r.TagList())
			}
			beforeHead := r.Git("rev-parse", "HEAD")
			beforeStatus := r.Git("status", "--porcelain=v1")
			beforeTags := r.TagList()
			beforeLog := changelogOf(t, r, "core")

			res := r.CommandEnv(row.env, append(row.args, "--log-format", "json")...)
			assert.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.True(t, harness.IsCodePresent(res.Events, "E219"), "stdout:\n%s", res.Stdout)
			assert.Contains(t, diagnosticText(res), row.want)
			assert.Equal(t, beforeHead, r.Git("rev-parse", "HEAD"), "no commit")
			assert.Equal(t, beforeStatus, r.Git("status", "--porcelain=v1"), "no change to the tree")
			assert.Equal(t, beforeTags, r.TagList(), "no tag")
			assert.Equal(t, beforeLog, changelogOf(t, r, "core"), "the record is untouched")
		})
	}
}

// TestStepAlignsItsRecordToTheRunEnvironment: where the step's own replan can
// be corrected it is corrected rather than refused, and the correction is
// reported (W228): the run's version, its prerelease channel and its provider
// movements are the authority, and the record written states them. A
// workspace listing only feeds tag masking, so an entry naming a package this
// workspace does not have, and one whose version does not parse, are skipped
// rather than refused.
func TestStepAlignsItsRecordToTheRunEnvironment(t *testing.T) {
	for _, row := range []struct {
		name string
		// linked releases app as the consumer of core instead of core alone.
		linked  bool
		env     []string
		aligned bool
		want    string
	}{
		{name: "a version the run decided", env: runEnvFor("core", "0.2.0", "core@0.2.0"),
			aligned: true, want: "## core@0.2.0 ("},
		{name: "a prerelease channel", env: runEnvFor("core", "0.2.0-beta.3", "core@0.2.0-beta.3"),
			aligned: true, want: "core@0.2.0-beta.3"},
		{name: "provider movements the run listed", env: runEnvFor("core", "0.1.0", "core@0.1.0",
			"DISPAT_UPDATED_PACKAGES=SHARED",
			"DISPAT_UPDATED_SHARED_NAME=shared",
			"DISPAT_UPDATED_SHARED_OLD_VERSION=1.0.0",
			"DISPAT_UPDATED_SHARED_NEW_VERSION=1.1.0",
			"DISPAT_UPDATED_SHARED_TAG=shared@1.1.0",
		), aligned: true, want: "shared: 1.0.0 -> 1.1.0"},
		{name: "a provider destination the run moved", linked: true, env: runEnvFor("app", "0.1.0", "app@0.1.0",
			"DISPAT_UPDATED_PACKAGES=CORE",
			"DISPAT_UPDATED_CORE_NAME=core",
			"DISPAT_UPDATED_CORE_OLD_VERSION=0.0.0",
			"DISPAT_UPDATED_CORE_NEW_VERSION=9.9.9",
			"DISPAT_UPDATED_CORE_TAG=core@9.9.9",
		), aligned: true, want: "core: 0.0.0 -> 9.9.9"},
		{name: "a workspace listing whose entries do not all resolve", env: runEnvFor("core", "0.1.0", "core@0.1.0",
			"DISPAT_WORKSPACE_PACKAGES=CORE GHOST BROKEN",
			"DISPAT_WORKSPACE_CORE_NAME=core",
			"DISPAT_WORKSPACE_CORE_VERSION=0.1.0",
			"DISPAT_WORKSPACE_CORE_RELEASING=true",
			"DISPAT_WORKSPACE_GHOST_NAME=ghost",
			"DISPAT_WORKSPACE_GHOST_VERSION=1.0.0",
			"DISPAT_WORKSPACE_GHOST_RELEASING=true",
			"DISPAT_WORKSPACE_BROKEN_NAME=broken",
			"DISPAT_WORKSPACE_BROKEN_VERSION=not a version",
			"DISPAT_WORKSPACE_BROKEN_RELEASING=true",
		), want: "## core@0.1.0 ("},
	} {
		t.Run(row.name, func(t *testing.T) {
			pkg := "core"
			var r *harness.Repo
			if row.linked {
				pkg = "app"
				r = linkedRepo(t, "core", "app", echoBuild)
				r.Commit("feat(core,app): provider and consumer move")
			} else {
				r = singlePackageRepo(t, echoBuild)
				r.Commit("feat(core): pending release")
			}

			res := r.CommandEnv(row.env, "changelog", "--log-format", "json")
			require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			if row.aligned {
				assert.True(t, harness.IsCodePresentForPackage(res.Events, "W228", pkg), "stdout:\n%s", res.Stdout)
			}
			assert.Contains(t, changelogOf(t, r, pkg), row.want, "the record states what the run decided")
		})
	}
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

// runEnvFor renders the environment a stage script of a release inherits,
// for a run releasing pkg at version under tag.
func runEnvFor(pkg, version, tag string, extra ...string) []string {
	return append([]string{
		"DISPAT_PACKAGE=" + pkg,
		"DISPAT_NEW_VERSION=" + version,
		"DISPAT_TAG=" + tag,
	}, extra...)
}
