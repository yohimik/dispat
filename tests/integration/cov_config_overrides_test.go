// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Coverage scenarios: the per-package overlay of the record configuration.
//
// A record object is written at the root, refined by a space and refined
// again by a package, and every field of it overlays independently: a package
// that renames one section keeps every other title the root gave it. The
// overlay is a long sequence of single-field decisions, and the only way to
// see that each one is wired to the field it names is a package that
// overrides all of them at once beside a package that overrides none.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// recordedCall is one request the coverage fake was handed.
type recordedCall struct {
	Method string
	Path   string
	Body   []byte
}

// pathRecordingGitHub is githubFake with the request path kept as well as the
// body: which repository a release was created in is exactly what the
// per-package owner and repo overrides decide, and the body alone cannot say.
func pathRecordingGitHub(t *testing.T) (*httptest.Server, func() []recordedCall) {
	t.Helper()
	var mu sync.Mutex
	var calls []recordedCall
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case req.Method == http.MethodGet && strings.Contains(req.URL.Path, "/releases/tags/"):
			w.WriteHeader(http.StatusNotFound)
		case req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/releases"):
			_, _ = w.Write([]byte(`[]`))
		case req.Method == http.MethodGet:
			w.WriteHeader(http.StatusOK)
		case req.Method == http.MethodPost:
			data, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			calls = append(calls, recordedCall{Method: req.Method, Path: req.URL.Path, Body: data})
			w.WriteHeader(http.StatusCreated)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, func() []recordedCall {
		mu.Lock()
		defer mu.Unlock()
		return append([]recordedCall(nil), calls...)
	}
}

// TestCovConfigPackageOverridesReplaceEveryInheritedRecordField: one package
// restates every field of the root's changelog and GitHub objects while a
// second package restates none, so each overlay decision is visible in what
// the two packages recorded.
func TestCovConfigPackageOverridesReplaceEveryInheritedRecordField(t *testing.T) {
	srv, calls := pathRecordingGitHub(t)
	t.Setenv("DISPAT_IT_TOKEN", "root-token")
	t.Setenv("DISPAT_IT_CORE_TOKEN", "core-token")

	rootFormat := models.EntryFormatConfig{
		DateFormat:        "2006-01-02",
		BreakingTitle:     "Root Breaking",
		FeaturesTitle:     "Root Features",
		FixesTitle:        "Root Fixes",
		DependenciesTitle: "Root Dependencies",
		ReleaseName:       "root ${DISPAT_PACKAGE}",
		Header:            []models.EntryLine{{Line: []string{"root header line"}}},
		Footer:            []models.EntryLine{{Line: []string{"root footer line"}}},
		DependencyLink:    "https://root.test/${DISPAT_DEP_NAME}",
		NoChangesText:     "root says nothing changed",
		Sections:          []models.SectionConfig{{Title: "Root Performance", Types: []string{"perf"}}},
		CommitRefs:        &models.CommitRefsConfig{Placement: "off", Format: "$DISPAT_COMMIT_SHORT"},
		Authors:           &models.AuthorsConfig{Placement: "off", Format: "fullname", Commits: "ccme", Title: "Root Authors"},
	}
	coreFormat := models.EntryFormatConfig{
		DateFormat:        "02.01.2006",
		BreakingTitle:     "Core Breaking",
		FeaturesTitle:     "Core Features",
		FixesTitle:        "Core Fixes",
		DependenciesTitle: "Core Dependencies",
		ReleaseName:       "core ${DISPAT_PACKAGE}",
		Header:            []models.EntryLine{{Line: []string{"core header line"}}},
		Footer:            []models.EntryLine{{Line: []string{"core footer line"}}},
		DependencyLink:    "https://core.test/${DISPAT_DEP_NAME}",
		NoChangesText:     "core says nothing changed",
		Sections:          []models.SectionConfig{{Title: "Core Performance", Types: []string{"perf"}}},
		CommitRefs:        &models.CommitRefsConfig{Placement: "suffix", Format: "[$DISPAT_COMMIT_SHORT]", Link: ""},
		Authors: &models.AuthorsConfig{
			Placement: "section", Format: "username", Commits: "all", Title: "Core Authors",
			Include: []string{"*"}, Exclude: []string{"nobody@example.test"},
		},
	}

	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Changelog = &models.ChangelogConfig{
		FileTitle:         []models.EntryLine{{Line: []string{"# Root Changelog"}}},
		EntrySpacing:      models.Int(2),
		EntryFormatConfig: rootFormat,
	}
	cfg.GitHub = &models.GitHubConfig{
		Enabled: models.Bool(true), AllPackages: models.Bool(true), Draft: models.Bool(false),
		Owner: "acme", Repo: "mono", APIURL: srv.URL, TokenEnv: "DISPAT_IT_TOKEN",
		EntryFormatConfig: rootFormat,
	}
	cfg.Packages = map[string]models.PackageConfig{"core": {
		Changelog: &models.ChangelogConfig{
			File:              "NOTES.md",
			FileTitle:         []models.EntryLine{{Line: []string{"# Core Notes"}}},
			EntrySpacing:      models.Int(3),
			EntryFormatConfig: coreFormat,
		},
		GitHub: &models.GitHubConfig{
			AllPackages: models.Bool(true), Draft: models.Bool(true),
			Owner: "core-owner", Repo: "core-repo", APIURL: srv.URL, TokenEnv: "DISPAT_IT_CORE_TOKEN",
			EntryFormatConfig: coreFormat,
		},
	}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "plain")
	r.Commit("feat(core,plain): bootstrap both packages")
	r.ReleaseOK()
	require.True(t, r.IsTagged("core@0.1.0"), "tags: %v", r.TagList())
	require.True(t, r.IsTagged("plain@0.1.0"), "tags: %v", r.TagList())

	t.Run("the overriding package writes its own file, title and format", func(t *testing.T) {
		notes := readRepoFile(t, r, "packages/core/NOTES.md")
		assert.Contains(t, notes, "# Core Notes", "the package names its own file title")
		assert.Contains(t, notes, "core header line")
		assert.Contains(t, notes, "core footer line")
		assert.Contains(t, notes, "Core Features")
		assert.NotContains(t, notes, "Root Features", "an overridden title is replaced, not joined")
		assert.Contains(t, notes, time.Now().UTC().Format("02.01.2006"),
			"the entry heading carries the package's date format:\n%s", notes)
		assert.Contains(t, notes, "Core Authors", "an authors section placement is the package's")
		assert.Equal(t, "", changelogOf(t, r, "core"),
			"nothing was written to the inherited file name")
	})

	t.Run("the inheriting package keeps every root value", func(t *testing.T) {
		log := changelogOf(t, r, "plain")
		assert.Contains(t, log, "# Root Changelog")
		assert.Contains(t, log, "root header line")
		assert.Contains(t, log, "root footer line")
		assert.Contains(t, log, "Root Features")
		assert.NotContains(t, log, "Core Features")
		assert.NotContains(t, log, "Root Authors", "an off placement writes no section")
	})

	t.Run("the overriding package retargets its GitHub release", func(t *testing.T) {
		var corePath, plainPath string
		var coreBody, plainBody struct {
			TagName string `json:"tag_name"`
			Name    string `json:"name"`
			Draft   bool   `json:"draft"`
		}
		for _, call := range calls() {
			var decoded struct {
				TagName string `json:"tag_name"`
				Name    string `json:"name"`
				Draft   bool   `json:"draft"`
			}
			require.NoError(t, json.Unmarshal(call.Body, &decoded))
			switch decoded.TagName {
			case "core@0.1.0":
				corePath, coreBody = call.Path, decoded
			case "plain@0.1.0":
				plainPath, plainBody = call.Path, decoded
			}
		}
		require.NotEmpty(t, corePath, "no release was created for core; calls: %+v", calls())
		assert.Contains(t, corePath, "/repos/core-owner/core-repo/releases")
		assert.True(t, coreBody.Draft, "the package's own draft policy applies")
		assert.Equal(t, "core core", coreBody.Name, "the package's releaseName template rendered")

		require.NotEmpty(t, plainPath, "no release was created for plain")
		assert.Contains(t, plainPath, "/repos/acme/mono/releases")
		assert.False(t, plainBody.Draft)
		assert.Equal(t, "root plain", plainBody.Name)
	})
}

// readRepoFile reads a file relative to the repository root, failing the test
// when it is absent — the form for reading a record a package wrote somewhere
// other than the default changelog name.
func readRepoFile(t *testing.T, r *harness.Repo, relPath string) string {
	t.Helper()
	data, err := os.ReadFile(r.Path(relPath))
	require.NoError(t, err, "reading %s", relPath)
	return string(data)
}
