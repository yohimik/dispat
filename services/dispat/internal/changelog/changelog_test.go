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

	"github.com/yohimik/dispat/pkg/ccme"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// testUnit builds the minimum of a parsed unit that the renderer reads: the
// bump it was classified as, and the description it renders.
func testUnit(typ string, bump ccme.Bump, description string) *ccme.Unit {
	return &ccme.Unit{
		Header: ccme.Header{Type: typ, Description: description},
		Bump:   bump,
		Valid:  true,
	}
}

func testRelease(dir string, next ccme.Version) *plan.Release {
	return &plan.Release{
		Pkg:  &model.Package{Name: "core", Dir: dir, Space: &model.Space{Name: "libs"}},
		Next: next,
		Units: []*ccme.Unit{
			testUnit("feat", ccme.BumpMinor, "add streaming"),
			testUnit("fix", ccme.BumpPatch, "close leak"),
			testUnit("feat", ccme.BumpMajor, "drop old API"),
		},
		DueTo: []string{"utils"},
		Updates: []plan.ProviderUpdate{{
			Name: "utils",
			From: ccme.Version{Major: 1, Minor: 1},
			To:   ccme.Version{Major: 1, Minor: 2},
		}},
	}
}

var testDate = time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC)

func TestRenderEntryDefaults(t *testing.T) {
	out := RenderEntry(testRelease("/tmp/x", ccme.Version{Major: 2}), testDate, Format{})

	for _, want := range []string{
		"## core@2.0.0 (2026-07-26)",
		"### Breaking Changes",
		"- drop old API",
		"### Features",
		"- add streaming",
		"### Fixes",
		"- close leak",
		"### Dependencies",
		"- utils: 1.1.0 -> 1.2.0",
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
	out := RenderEntry(testRelease("/tmp/x", ccme.Version{Major: 2}), testDate, f)

	assert.Contains(t, out, "## core@2.0.0 (26.07.2026)")
	assert.Contains(t, out, "### Breaking")
	assert.Contains(t, out, "### Added")
	assert.Contains(t, out, "### Fixed")
	assert.Contains(t, out, "### Bumped")
	assert.NotContains(t, out, "### Features")
}

func TestRenderSections(t *testing.T) {
	out := RenderSections(testRelease("/tmp/x", ccme.Version{Major: 2}), Format{})
	assert.True(t, strings.HasPrefix(out, "### Breaking Changes"), "no entry header in sections: %q", out)
	assert.NotContains(t, out, "## core@")
}

func TestRenderSectionsEmpty(t *testing.T) {
	rel := &plan.Release{
		Pkg:  &model.Package{Name: "core", Dir: "/tmp/x", Space: &model.Space{Name: "libs"}},
		Next: ccme.Version{Major: 2},
	}
	assert.Empty(t, RenderSections(rel, Format{}))
	assert.Equal(t, "## core@2.0.0 (2026-07-26)\n", RenderEntry(rel, testDate, Format{}))
}

func TestRecordCreatesAndPrepends(t *testing.T) {
	dir := t.TempDir()
	w := &FileWriter{Now: func() time.Time { return testDate }}
	ctx := context.Background()

	require.NoError(t, w.Record(ctx, testRelease(dir, ccme.Version{Major: 2})))
	require.NoError(t, w.Record(ctx, testRelease(dir, ccme.Version{Major: 2, Minor: 1})))

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
		File:      "HISTORY.md",
		FileTitle: titleLines("# History"),
		Format:    Format{FeaturesTitle: "Added"},
		Now:       func() time.Time { return testDate },
	}
	require.NoError(t, w.Record(context.Background(), testRelease(dir, ccme.Version{Major: 2})))

	data, err := os.ReadFile(filepath.Join(dir, "HISTORY.md"))
	require.NoError(t, err)
	content := string(data)
	assert.True(t, strings.HasPrefix(content, "# History\n"))
	assert.Contains(t, content, "### Added")
	assert.NoFileExists(t, filepath.Join(dir, "CHANGELOG.md"))
}

// TestDispatcherRoutesPerPackagePolicy: the dispatcher reads each package's
// resolved changelog policy — a disabled package writes nothing at all, an
// enabled one writes through its own file, title and format.
func TestDispatcherRoutesPerPackagePolicy(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	d := &Dispatcher{Now: func() time.Time { return testDate }}
	ctx := context.Background()

	enabled := testRelease(dirA, ccme.Version{Major: 2})
	enabled.Pkg.Changelog = model.ChangelogSpec{
		Enabled: true, File: "HISTORY.md", FileTitle: titleLines("# History"),
		Format: model.RecordFormat{FeaturesTitle: "Added"},
	}
	disabled := testRelease(dirB, ccme.Version{Major: 2})
	disabled.Pkg.Changelog = model.ChangelogSpec{Enabled: false, File: "HISTORY.md"}

	require.NoError(t, d.Record(ctx, enabled))
	require.NoError(t, d.Record(ctx, disabled))

	data, err := os.ReadFile(filepath.Join(dirA, "HISTORY.md"))
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(string(data), "# History\n"))
	assert.Contains(t, string(data), "### Added")
	entries, err := os.ReadDir(dirB)
	require.NoError(t, err)
	assert.Empty(t, entries, "a disabled package records nothing")
}

// TestDispatcherHoldsPrereleasesBack: with changelog.prerelease off, the
// betas of a version leave no entry behind and the graduation to stable
// writes the one entry covering them.
func TestDispatcherHoldsPrereleasesBack(t *testing.T) {
	dir := t.TempDir()
	d := &Dispatcher{Now: func() time.Time { return testDate }}
	ctx := context.Background()
	spec := model.ChangelogSpec{Enabled: true, Prerelease: false}

	beta := testRelease(dir, ccme.Version{Major: 2, Prerelease: []string{"beta", "1"}})
	beta.Pkg.Changelog = spec
	require.NoError(t, d.Record(ctx, beta))
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, entries, "a held-back prerelease writes no file at all")

	stable := testRelease(dir, ccme.Version{Major: 2})
	stable.Pkg.Changelog = spec
	require.NoError(t, d.Record(ctx, stable))
	data, err := os.ReadFile(filepath.Join(dir, "CHANGELOG.md"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "## core@2.0.0 (")
	assert.NotContains(t, string(data), "beta")
}

func TestRenderSectionsDependenciesFollowUpdates(t *testing.T) {
	// Updates is the whole answer to "which providers does this release pick
	// up", so it is also the section's gate. Nothing to report, no section —
	// there is no second source of provider names to fall back to, and a
	// heading over an empty list would be worse than no heading.
	rel := testRelease("/tmp/x", ccme.Version{Major: 2})
	assert.Contains(t, RenderSections(rel, Format{}), "### Dependencies")

	rel.Updates = nil
	out := RenderSections(rel, Format{})
	assert.NotContains(t, out, "### Dependencies")

	// DueTo alone does not bring it back: DueTo says why the package is
	// releasing, Updates says whose version it carries, and only the second
	// question has an answer to print.
	rel.DueTo = []string{"utils"}
	assert.NotContains(t, RenderSections(rel, Format{}), "### Dependencies")
}

func TestRenderFixedRideNoChangesEntry(t *testing.T) {
	// A shared-versioning ride has no units and no provider updates: its
	// entry states the bump-only nature instead of rendering empty sections,
	// and it names the part of the version the group actually holds in common
	// so a fixedMajor reader is not told the whole version is shared.
	cases := []struct {
		mode model.Versioning
		want string
	}{
		{model.VersioningFixed, "one version"},
		{model.VersioningFixedSparse, "one version"},
		{model.VersioningFixedMajorMinor, "one major and minor version"},
		{model.VersioningFixedMajorMinorSparse, "one major and minor version"},
		{model.VersioningFixedMajor, "one major version"},
		{model.VersioningFixedMajorSparse, "one major version"},
	}
	for _, c := range cases {
		t.Run(string(c.mode), func(t *testing.T) {
			rel := &plan.Release{
				Pkg:       &model.Package{Name: "core", Dir: "core", Space: &model.Space{Name: "libs", Versioning: c.mode}},
				Next:      ccme.Version{Major: 1, Minor: 1},
				FixedRide: true,
			}
			line := "No changes: a version bump to keep the versioning group on " + c.want + ".\n"
			assert.Equal(t, line, RenderSections(rel, Format{}))
			assert.Equal(t, "## core@1.1.0 (2026-07-26)\n\n"+line, RenderEntry(rel, testDate, Format{}))
		})
	}
}

func TestRenderFixedMemberWithOwnUnitsIsOrdinary(t *testing.T) {
	// FixedRide is only set for members with no cause of their own, but even
	// a defensive combination of the flag with real units must render the
	// units: content always beats the placeholder.
	rel := testRelease("core", ccme.Version{Major: 1, Minor: 1})
	rel.FixedRide = true
	sections := RenderSections(rel, Format{})
	assert.Contains(t, sections, "### Features")
	assert.NotContains(t, sections, "No changes")
}

func TestRenderSectionsPrereleaseUsesOnlyItsChangeset(t *testing.T) {
	// On a prerelease train the window spans the whole train (Units), but the
	// entry of the *next* prerelease documents only what its baseline has not
	// published (FreshUnits) — beta.1 does not repeat beta.0's notes. A stable
	// version at the same window — the graduation — collects everything.
	units := []*ccme.Unit{
		testUnit("feat", ccme.BumpMinor, "feature shipped in beta.0"),
		testUnit("fix", ccme.BumpPatch, "fix new in beta.1"),
	}
	rel := &plan.Release{
		Pkg:        &model.Package{Name: "core", Dir: "core", Space: &model.Space{Name: "libs"}},
		Next:       ccme.Version{Minor: 2, Prerelease: []string{"beta", "1"}},
		Units:      units,
		FreshUnits: units[1:],
	}
	sections := RenderSections(rel, Format{})
	assert.Contains(t, sections, "fix new in beta.1")
	assert.NotContains(t, sections, "feature shipped in beta.0",
		"a prerelease entry must not repeat what the train already published")

	rel.Next = ccme.Version{Minor: 2} // the graduation of the same window
	sections = RenderSections(rel, Format{})
	assert.Contains(t, sections, "feature shipped in beta.0",
		"a graduation collects every prerelease's changes")
	assert.Contains(t, sections, "fix new in beta.1")
}

func TestFileWriterSkipsExistingEntry(t *testing.T) {
	// The entry-exists check is the idempotence of the whole record path: a
	// second write of the same tag changes nothing (W226), while a different
	// tag still prepends above the existing entry.
	dir := t.TempDir()
	rel := testRelease(dir, ccme.Version{Major: 1, Minor: 3})
	w := &FileWriter{Now: func() time.Time { return time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC) }}
	require.NoError(t, w.Record(context.Background(), rel))
	first, err := os.ReadFile(filepath.Join(dir, "CHANGELOG.md"))
	require.NoError(t, err)

	already, err := w.HasEntryFor(rel)
	require.NoError(t, err)
	assert.True(t, already)
	require.NoError(t, w.Record(context.Background(), rel))
	second, err := os.ReadFile(filepath.Join(dir, "CHANGELOG.md"))
	require.NoError(t, err)
	assert.Equal(t, string(first), string(second), "a repeated write is byte-identical")
	assert.Equal(t, 1, strings.Count(string(second), "## core@1.3.0 ("), "exactly one header")

	next := testRelease(dir, ccme.Version{Major: 1, Minor: 4})
	require.NoError(t, w.Record(context.Background(), next))
	third, err := os.ReadFile(filepath.Join(dir, "CHANGELOG.md"))
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(string(third), "## core@1.4.0 ("))
	assert.Equal(t, 1, strings.Count(string(third), "## core@1.3.0 ("), "the older entry survives below")
	assert.Less(t, strings.Index(string(third), "core@1.4.0"), strings.Index(string(third), "core@1.3.0"))
}

func TestHasEntryIsLineAnchored(t *testing.T) {
	// Quoted or indented header text in a body must not suppress a write, and
	// a tag that extends another must not match its prefix.
	body := []byte("# Changelog\n\n## core@1.3.0-beta.1 (2026-08-09)\n\n- see `## core@9.9.9 (` in docs\n  ## core@2.0.0 (indented)\n")
	assert.True(t, HasEntry(body, "core@1.3.0-beta.1"))
	assert.False(t, HasEntry(body, "core@1.3.0"), "a prefix of an existing tag does not match")
	assert.False(t, HasEntry(body, "core@9.9.9"), "mid-line mention does not count")
	assert.False(t, HasEntry(body, "core@2.0.0"), "indented text does not count")
	assert.False(t, HasEntry(nil, "core@1.0.0"), "an absent file has no entries")
}

// TestRecordMultiLineFileTitle: a file title may be several lines, written
// once at the top and not repeated by the next release.
func TestRecordMultiLineFileTitle(t *testing.T) {
	dir := t.TempDir()
	w := &FileWriter{
		FileTitle: titleLines("# Changelog", "", "All notable changes to core."),
		Now:       func() time.Time { return testDate },
	}
	ctx := context.Background()
	require.NoError(t, w.Record(ctx, testRelease(dir, ccme.Version{Major: 2})))
	require.NoError(t, w.Record(ctx, testRelease(dir, ccme.Version{Major: 2, Minor: 1})))

	data, err := os.ReadFile(filepath.Join(dir, "CHANGELOG.md"))
	require.NoError(t, err)
	content := string(data)

	assert.True(t, strings.HasPrefix(content, "# Changelog\n\nAll notable changes to core.\n"), content)
	assert.Equal(t, 1, strings.Count(content, "All notable changes to core."),
		"the title heads the file once, however many entries it holds")
}

// TestRecordFileTitleIsFiltered: a file title can differ per package, like
// any other record line.
func TestRecordFileTitleIsFiltered(t *testing.T) {
	dir := t.TempDir()
	w := &FileWriter{
		FileTitle: []model.EntryLine{
			{Line: []string{"# Core"}, Package: []string{"core"}},
			{Line: []string{"# Everything else"}, Package: []string{"other"}},
		},
		Now: func() time.Time { return testDate },
	}
	require.NoError(t, w.Record(context.Background(), testRelease(dir, ccme.Version{Major: 2})))

	data, err := os.ReadFile(filepath.Join(dir, "CHANGELOG.md"))
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(string(data), "# Core\n"), string(data))
	assert.NotContains(t, string(data), "Everything else")
}

// TestRecordEveryEntryCarriesItsOwnBlocks: the header and footer belong to
// the entry, so a second release writes its own copy above the first and
// leaves the first untouched.
func TestRecordEveryEntryCarriesItsOwnBlocks(t *testing.T) {
	dir := t.TempDir()
	w := &FileWriter{
		Format: Format{
			ReleaseName: "${DISPAT_PACKAGE} ${DISPAT_VERSION}",
			Header:      titleLines("Built by CI."),
			Footer:      titleLines("---"),
		},
		Now: func() time.Time { return testDate },
	}
	ctx := context.Background()
	require.NoError(t, w.Record(ctx, testRelease(dir, ccme.Version{Major: 2})))
	require.NoError(t, w.Record(ctx, testRelease(dir, ccme.Version{Major: 2, Minor: 1})))

	data, err := os.ReadFile(filepath.Join(dir, "CHANGELOG.md"))
	require.NoError(t, err)
	content := string(data)

	assert.Equal(t, 2, strings.Count(content, "Built by CI."), "one header per entry")
	assert.Equal(t, 2, strings.Count(content, "\n---\n"), "one footer per entry")
	assert.Contains(t, content, "### core 2.1.0", "each entry names its own release")
	assert.Contains(t, content, "### core 2.0.0")
	assert.True(t, HasEntry([]byte(content), "core@2.1.0"), "the entry stays findable under its sub-header")
}

// TestRecordChangedFileTitleIsNotStripped documents the one sharp edge of a
// file title: the writer strips the title it renders now, so a title that
// changed since the last release is not recognised and the old one stays in
// the file. A title must not carry anything that varies per release.
func TestRecordChangedFileTitleIsNotStripped(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	first := &FileWriter{FileTitle: titleLines("# Changelog"), Now: func() time.Time { return testDate }}
	require.NoError(t, first.Record(ctx, testRelease(dir, ccme.Version{Major: 2})))

	second := &FileWriter{FileTitle: titleLines("# History"), Now: func() time.Time { return testDate }}
	require.NoError(t, second.Record(ctx, testRelease(dir, ccme.Version{Major: 2, Minor: 1})))

	data, err := os.ReadFile(filepath.Join(dir, "CHANGELOG.md"))
	require.NoError(t, err)
	content := string(data)
	assert.True(t, strings.HasPrefix(content, "# History\n"), "the new title heads the file")
	assert.Contains(t, content, "# Changelog", "and the old one survives below, unrecognised")
}
