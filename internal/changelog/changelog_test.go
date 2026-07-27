package changelog

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/monorel/internal/conventional"
	"github.com/yohimik/monorel/internal/model"
	"github.com/yohimik/monorel/internal/plan"
	"github.com/yohimik/monorel/internal/semver"
)

func testRelease(dir string, next semver.Version) *plan.Release {
	return &plan.Release{
		Pkg:  &model.Package{Name: "core", Dir: dir, Space: &model.Space{Name: "libs"}},
		Next: next,
		Commits: []conventional.Commit{
			{Kind: conventional.KindFeat, Scope: "core", Description: "add streaming"},
			{Kind: conventional.KindFix, Scope: "core", Description: "close leak"},
			{Kind: conventional.KindBreaking, Scope: "core", Description: "drop old API"},
		},
		DueTo: []string{"utils"},
	}
}

var testDate = time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC)

func TestRenderEntryDefaults(t *testing.T) {
	out := RenderEntry(testRelease("/tmp/x", semver.Version{Major: 2}), testDate, Format{})

	for _, want := range []string{
		"## core@2.0.0 (2026-07-26)",
		"### Breaking Changes",
		"- drop old API",
		"### Features",
		"- add streaming",
		"### Fixes",
		"- close leak",
		"### Dependencies",
		"- updated providers: utils",
	} {
		assert.Contains(t, out, want)
	}
	assert.Less(t, strings.Index(out, "Breaking"), strings.Index(out, "Features"),
		"Breaking Changes must be rendered before Features")
}

func TestRenderEntryCustomFormat(t *testing.T) {
	f := Format{
		DateFormat:        "02.01.2006",
		BreakingTitle:     "Breaking",
		FeaturesTitle:     "Added",
		FixesTitle:        "Fixed",
		DependenciesTitle: "Bumped",
	}
	out := RenderEntry(testRelease("/tmp/x", semver.Version{Major: 2}), testDate, f)

	assert.Contains(t, out, "## core@2.0.0 (26.07.2026)")
	assert.Contains(t, out, "### Breaking")
	assert.Contains(t, out, "### Added")
	assert.Contains(t, out, "### Fixed")
	assert.Contains(t, out, "### Bumped")
	assert.NotContains(t, out, "### Features")
}

func TestRenderSections(t *testing.T) {
	out := RenderSections(testRelease("/tmp/x", semver.Version{Major: 2}), Format{})
	assert.True(t, strings.HasPrefix(out, "### Breaking Changes"), "no entry header in sections: %q", out)
	assert.NotContains(t, out, "## core@")
}

func TestRenderSectionsEmpty(t *testing.T) {
	rel := &plan.Release{
		Pkg:  &model.Package{Name: "core", Dir: "/tmp/x", Space: &model.Space{Name: "libs"}},
		Next: semver.Version{Major: 2},
	}
	assert.Empty(t, RenderSections(rel, Format{}))
	assert.Equal(t, "## core@2.0.0 (2026-07-26)\n", RenderEntry(rel, testDate, Format{}))
}

func TestRecordCreatesAndPrepends(t *testing.T) {
	dir := t.TempDir()
	w := &FileWriter{Now: func() time.Time { return testDate }}
	ctx := context.Background()

	require.NoError(t, w.Record(ctx, testRelease(dir, semver.Version{Major: 2})))
	require.NoError(t, w.Record(ctx, testRelease(dir, semver.Version{Major: 2, Minor: 1})))

	data, err := os.ReadFile(filepath.Join(dir, "CHANGELOG.md"))
	require.NoError(t, err)
	content := string(data)

	assert.True(t, strings.HasPrefix(content, "# Changelog\n"), "must start with the header")
	first := strings.Index(content, "## core@2.1.0")
	second := strings.Index(content, "## core@2.0.0")
	require.NotEqual(t, -1, first, "missing 2.1.0 entry:\n%s", content)
	require.NotEqual(t, -1, second, "missing 2.0.0 entry:\n%s", content)
	assert.Less(t, first, second, "newest release must be at the top")
	assert.Equal(t, 1, strings.Count(content, "# Changelog"), "header must not be duplicated")
}

func TestRecordCustomFileAndTitle(t *testing.T) {
	dir := t.TempDir()
	w := &FileWriter{
		File:   "HISTORY.md",
		Title:  "# History",
		Format: Format{FeaturesTitle: "Added"},
		Now:    func() time.Time { return testDate },
	}
	require.NoError(t, w.Record(context.Background(), testRelease(dir, semver.Version{Major: 2})))

	data, err := os.ReadFile(filepath.Join(dir, "HISTORY.md"))
	require.NoError(t, err)
	content := string(data)
	assert.True(t, strings.HasPrefix(content, "# History\n"))
	assert.Contains(t, content, "### Added")
	assert.NoFileExists(t, filepath.Join(dir, "CHANGELOG.md"))
}
