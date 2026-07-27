package changelog

import (
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

func TestRender(t *testing.T) {
	rel := testRelease("/tmp/x", semver.Version{Major: 2})
	out := Render(rel, time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC))

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

func TestAppendCreatesAndPrepends(t *testing.T) {
	dir := t.TempDir()
	w := &FileWriter{Now: func() time.Time { return time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC) }}

	require.NoError(t, w.Append(testRelease(dir, semver.Version{Major: 2})))
	require.NoError(t, w.Append(testRelease(dir, semver.Version{Major: 2, Minor: 1})))

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
