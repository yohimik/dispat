// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestFinalGitHubStepRefusesForeignRunStateBeforeTheAPI: the standalone
// GitHub command inherits a release run's decision through DISPAT_*. A value
// it cannot read, or a tag that disagrees with that decision, must stop before
// even verifying the remote: neither shape may create the plausible-looking
// release that the malformed environment asked for.
func TestFinalGitHubStepRefusesForeignRunStateBeforeTheAPI(t *testing.T) {
	for name, env := range map[string][]string{
		"a version that does not parse":               runEnvFor("core", "one point oh", "core@0.1.0"),
		"a tag that does not render from the version": runEnvFor("core", "0.2.0", "core-v0.2.0"),
	} {
		t.Run(name, func(t *testing.T) {
			var mu sync.Mutex
			requests := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				mu.Lock()
				requests++
				mu.Unlock()
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()

			r := harness.New(t)
			cfg := libsConfig(echoBuild, 1)
			cfg.GitHub = &models.GitHubConfig{
				Enabled: models.Bool(true), Owner: "acme", Repo: "mono",
				APIURL: srv.URL, TokenEnv: "DISPAT_IT_TOKEN",
			}
			r.WriteConfigModel(cfg)
			t.Setenv("DISPAT_IT_TOKEN", "tkn")
			r.SeedPackage("packages", "core")
			r.Commit("feat(core): first feature")

			res := r.CommandEnv(append(env, "DISPAT_EXPORT_GITHUB="), "github", "--log-format", "json")
			assert.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.True(t, harness.IsCodePresent(res.Events, "E219"), "stdout:\n%s", res.Stdout)

			mu.Lock()
			gotRequests := requests
			mu.Unlock()
			assert.Zero(t, gotRequests, "invalid run state reached the GitHub API")
		})
	}
}

// TestFinalPolyrepoGitHubStepChecksTheOwnerForItsTag: a package tag belongs
// to its source repository. During a polyrepo stage the control repository
// deliberately has no such tag; consulting it would emit W229 and imply that
// GitHub is about to invent the tag even though the source already carries
// the exact run decision.
func TestFinalPolyrepoGitHubStepChecksTheOwnerForItsTag(t *testing.T) {
	srv, bodies := githubFake(t)

	source := harness.New(t)
	source.SeedPackage("packages", "core")
	source.Commit("feat(core): first feature")
	source.Git("tag", "-a", "core@0.1.0", "-m", "source release tag")

	control := harness.New(t)
	addPolyrepoSource(t, control, "core-source", "sources/core", source)
	cfg := polyrepoFile()
	cfg["github"] = map[string]any{
		"enabled": true, "owner": "acme", "repo": "mono",
		"apiUrl": srv.URL, "tokenEnv": "DISPAT_IT_TOKEN",
	}
	cfg["spaces"] = centralSpaces(map[string]string{"libs": "sources/core/packages"})
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: assemble the control repository")
	t.Setenv("DISPAT_IT_TOKEN", "tkn")

	env := append(runEnvFor("core", "0.1.0", "core@0.1.0"), "DISPAT_EXPORT_GITHUB=")
	res := control.CommandEnv(env, "github", "--log-format", "json")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.False(t, harness.IsCodePresent(res.Events, "W229"),
		"the package owner's tag exists; stdout:\n%s", res.Stdout)
	assert.Empty(t, control.TagList(), "the control repository deliberately carries no package tag")
	assert.Contains(t, polyrepoTags(control, "sources/core"), "core@0.1.0")

	type createdRelease struct {
		TagName string `json:"tag_name"`
	}
	created := decodeAll[createdRelease](t, bodies())
	require.Len(t, created, 1, "one source-owned package produces one GitHub release")
	assert.Equal(t, "core@0.1.0", created[0].TagName)
}
