// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package changelog

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
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
	// The channel the planner would have resolved: what the version says it
	// is, so a release built here answers the record policies as a real one.
	channel := ccme.ChannelStable
	if next.IsPrerelease() {
		channel = next.Prerelease[0]
	}
	return &plan.Release{
		Pkg:     &model.Package{Name: "core", Dir: dir, Space: &model.Space{Name: "libs"}},
		Next:    next,
		Channel: channel,
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
	f := SpecFormat(model.RecordFormat{
		DateFormat:        "02.01.2006",
		BreakingTitle:     "Breaking",
		FeaturesTitle:     "Added",
		FixesTitle:        "Fixed",
		DependenciesTitle: "Bumped",
	})
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
	// A release carrying no cause the renderer can name still gets the plain
	// fallback line: sections never render empty, whatever admitted the
	// release to the plan.
	rel := &plan.Release{
		Pkg:  &model.Package{Name: "core", Dir: "/tmp/x", Space: &model.Space{Name: "libs"}},
		Next: ccme.Version{Major: 2},
	}
	assert.Equal(t, "No changes.\n", RenderSections(rel, Format{}))
	assert.Equal(t, "## core@2.0.0 (2026-07-26)\n\nNo changes.\n", RenderEntry(rel, testDate, Format{}))
}

func TestRenderSectionsMarksARestatement(t *testing.T) {
	// §13.10 requires corrected entries to be marked, and §7.4.2 asks for the
	// restatement to be rendered once, as the carrying unit's entry. Naming
	// what it corrects is what stops a reader chasing the line to a commit
	// message that says something else entirely.
	rel := testRelease("/tmp/x", ccme.Version{Major: 2})
	restated := rel.Units[1]
	rel.Corrects = map[*ccme.Unit][]string{restated: {"4f2a1c9abcde", "bd41f0e12345#2"}}

	out := RenderSections(rel, Format{})

	assert.Contains(t, out, "- close leak (corrects 4f2a1c9abcde, bd41f0e12345#2)")
	assert.Contains(t, out, "- add streaming\n", "an ordinary entry carries no annotation")
}

func TestRenderSectionsOmitsSuppressedEntries(t *testing.T) {
	// §7.3: a revert and the unit it reverted leave the notes together, while
	// both still count toward the bump. The renderer reads NotesUnits, so the
	// omission has to reach it without the section logic knowing about reverts.
	rel := testRelease("/tmp/x", ccme.Version{Major: 2})
	rel.SuppressedNotes = map[*ccme.Unit]bool{rel.Units[0]: true}

	out := RenderSections(rel, Format{})

	assert.NotContains(t, out, "add streaming", "the suppressed entry is gone")
	assert.NotContains(t, out, "### Features", "and so is the section it was alone in")
	assert.Contains(t, out, "- close leak", "its siblings are untouched")
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

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "the atomic replace must leave no temp file beside the changelog")
}

// TestRecordKeepsTheFilesMode: the rewrite replaces the whole file, and a
// changelog someone chmodded keeps its own permissions across it.
func TestRecordKeepsTheFilesMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CHANGELOG.md")
	w := &FileWriter{Now: func() time.Time { return testDate }}
	ctx := context.Background()

	require.NoError(t, w.Record(ctx, testRelease(dir, ccme.Version{Major: 2})))
	require.NoError(t, os.Chmod(path, 0o600))
	require.NoError(t, w.Record(ctx, testRelease(dir, ccme.Version{Major: 2, Minor: 1})))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestRecordCustomFileAndTitle(t *testing.T) {
	dir := t.TempDir()
	w := &FileWriter{
		File:      "HISTORY.md",
		FileTitle: titleLines("# History"),
		Format:    SpecFormat(model.RecordFormat{FeaturesTitle: "Added"}),
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

// TestDispatcherRecordsOnlyTheChannelsConfigured: with the file recording on
// the stable channel alone, the betas of a version leave no entry behind and
// the graduation to stable writes the one entry covering them.
func TestDispatcherRecordsOnlyTheChannelsConfigured(t *testing.T) {
	dir := t.TempDir()
	d := &Dispatcher{Now: func() time.Time { return testDate }}
	ctx := context.Background()
	spec := model.ChangelogSpec{Enabled: true, Channels: []string{"stable"}}

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

func TestRenderSectionsNeverEmpty(t *testing.T) {
	// Three release shapes reach the renderer with nothing to group and no
	// ride flag: a pin with no pending work, a channel transition with no new
	// work, and work its own reverts cancel out of the notes. Each must state
	// its cause instead of rendering an empty entry body (and, through
	// RenderBody, an empty GitHub release body).
	base := func() *plan.Release {
		return &plan.Release{
			Pkg:  &model.Package{Name: "core", Dir: "core", Space: &model.Space{Name: "libs"}},
			Next: ccme.Version{Major: 2},
		}
	}

	pinned := base()
	pinned.Pinned = true
	assert.Equal(t, "No changes: a version set by Release-As.\n",
		RenderSections(pinned, Format{}))

	moved := base()
	moved.Channel, moved.BaselineChannel = "stable", "beta"
	assert.Equal(t, "No changes: a channel transition, beta -> stable.\n",
		RenderSections(moved, Format{}))

	reverted := base()
	units := []*ccme.Unit{
		testUnit("feat", ccme.BumpMinor, "work later reverted"),
		testUnit("revert", ccme.BumpMinor, "the revert"),
	}
	reverted.Units = units
	reverted.SuppressedNotes = map[*ccme.Unit]bool{units[0]: true, units[1]: true}
	sections := RenderSections(reverted, Format{})
	assert.Equal(t, "No changes: the pending work and its reverts cancel out.\n", sections)

	for _, rel := range []*plan.Release{pinned, moved, reverted} {
		assert.NotEmpty(t, RenderBody(rel, Format{}, nil), "no body may render empty")
	}
}

func TestRideWithProviderMovementIsNotNoChanges(t *testing.T) {
	// The Updates clause of NoChanges: a ride that picks up a provider's
	// movement has a dependencies section to show, which is not "no changes".
	rel := &plan.Release{
		Pkg:       &model.Package{Name: "core", Dir: "core", Space: &model.Space{Name: "libs", Versioning: model.VersioningFixed}},
		Next:      ccme.Version{Major: 1, Minor: 1},
		FixedRide: true,
		Updates: []plan.ProviderUpdate{{
			Name: "utils",
			From: ccme.Version{Major: 1, Minor: 1},
			To:   ccme.Version{Major: 1, Minor: 2},
		}},
	}
	assert.False(t, rel.NoChanges())
	sections := RenderSections(rel, Format{})
	assert.Contains(t, sections, "### Dependencies")
	assert.Contains(t, sections, "- utils: 1.1.0 -> 1.2.0")
	assert.NotContains(t, sections, "No changes")
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
		Format: SpecFormat(model.RecordFormat{
			ReleaseName: "${DISPAT_PACKAGE} ${DISPAT_VERSION}",
			Header:      titleLines("Built by CI."),
			Footer:      titleLines("---"),
		}),
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

// TestRecordChangedFileTitleLeavesTheFilesOwnTitleInPlace documents the one
// sharp edge of a file title: the writer recognises the title it renders now,
// so a title that changed since the last release is not recognised at all —
// and an unrecognised title is a title the file brought with it. It stays
// where it is, the new one is never written, and the entry goes under it. A
// title must not carry anything that varies per release.
func TestRecordChangedFileTitleLeavesTheFilesOwnTitleInPlace(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	first := &FileWriter{FileTitle: titleLines("# Changelog"), Now: func() time.Time { return testDate }}
	require.NoError(t, first.Record(ctx, testRelease(dir, ccme.Version{Major: 2})))

	second := &FileWriter{FileTitle: titleLines("# History"), Now: func() time.Time { return testDate }}
	require.NoError(t, second.Record(ctx, testRelease(dir, ccme.Version{Major: 2, Minor: 1})))

	data, err := os.ReadFile(filepath.Join(dir, "CHANGELOG.md"))
	require.NoError(t, err)
	content := string(data)
	assert.True(t, strings.HasPrefix(content, "# Changelog\n"),
		"the file keeps the title it already had:\n%s", content)
	assert.NotContains(t, content, "# History",
		"and the unrecognised new title is never written, so the file grows no second H1")
	assertOrder(t, content, "# Changelog", "## core@2.1.0 (", "## core@2.0.0 (")
}

// --- Adoption: what a changelog written before dispat keeps.
//
// The guarantee these pin is one sentence: dispat never rewrites content it
// did not write, and never moves it. Everything above the first entry heading
// of a file dispat does not recognise is that file's preamble — front matter,
// a title in somebody else's words, a badge row, an introduction — and it
// stays at the head of the file with the new entry inserted below it.

// recordInto writes one release into dir's changelog over the given existing
// content and returns the file afterwards.
func recordInto(t *testing.T, existing string, w *FileWriter, next ccme.Version) string {
	t.Helper()
	dir := t.TempDir()
	if existing != "" {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "CHANGELOG.md"), []byte(existing), 0o644))
	}
	if w.Now == nil {
		w.Now = func() time.Time { return testDate }
	}
	require.NoError(t, w.Record(context.Background(), testRelease(dir, next)))
	data, err := os.ReadFile(filepath.Join(dir, "CHANGELOG.md"))
	require.NoError(t, err)
	return string(data)
}

func TestRecordKeepsAPreambleAboveTheEntryItInserts(t *testing.T) {
	cases := []struct {
		name     string
		existing string
		// keep is the leading run of bytes that must survive untouched, at the
		// very top of the file.
		keep string
	}{{
		name:     "a title in somebody else's words",
		existing: "# Change Log\n\n## v1.2.0 (2024-01-01)\n\n- something old\n",
		keep:     "# Change Log",
	}, {
		name: "YAML front matter",
		existing: "---\ntitle: Releases\nsidebar_position: 3\n---\n\n# Change Log\n\n" +
			"## v1.2.0 (2024-01-01)\n\n- something old\n",
		keep: "---\ntitle: Releases\nsidebar_position: 3\n---\n\n# Change Log",
	}, {
		name: "a badge row and an introduction",
		existing: "# Change Log\n\n[![build](https://img.shields.io/x)](https://example.test)\n\n" +
			"All notable changes, by hand, since 2019.\n\n## v1.2.0 (2024-01-01)\n\n- something old\n",
		keep: "# Change Log\n\n[![build](https://img.shields.io/x)](https://example.test)\n\n" +
			"All notable changes, by hand, since 2019.",
	}, {
		name:     "a file with no entry headings at all",
		existing: "# Change Log\n\nNothing has shipped yet.\n",
		keep:     "# Change Log\n\nNothing has shipped yet.",
	}, {
		name:     "a file that opens straight on an entry",
		existing: "## v1.2.0 (2024-01-01)\n\n- something old\n",
		keep:     "",
	}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := recordInto(t, c.existing, &FileWriter{}, ccme.Version{Major: 2})

			assert.True(t, strings.HasPrefix(out, c.keep),
				"the preamble must head the file byte for byte:\n%s", out)
			assert.NotContains(t, out, "# Changelog",
				"a file with a title of its own is not given dispat's as well")
			assert.Contains(t, out, "## core@2.0.0 (", "the entry was written:\n%s", out)
			if strings.Contains(c.existing, "## v1.2.0") {
				assertOrder(t, out, "## core@2.0.0 (", "## v1.2.0 (", "- something old")
			}
			// The seam above the entry is the entry seam: the preamble sits
			// where a title would, and what dispat writes around an entry is
			// the same width wherever the entry is.
			if c.keep != "" {
				assert.Contains(t, out, c.keep+"\n\n\n## core@2.0.0 (",
					"the preamble is separated from the entry by the entry seam:\n%q", out)
			} else {
				assert.True(t, strings.HasPrefix(out, "## core@2.0.0 ("), "%q", out)
			}
		})
	}
}

func TestRecordUnderAMatchingTitleKeepsWhatFollowsIt(t *testing.T) {
	// The other half of adoption: a file that already heads with the title
	// dispat renders is the file dispat has been writing, so the title is
	// re-rendered and the entry goes directly under it — which is what it has
	// always done. Content between the title and the first entry is not
	// rewritten either; it simply ends up below the entry, where the entries
	// it was already above are.
	out := recordInto(t,
		"# Changelog\n\n[![build](https://img.shields.io/x)](https://example.test)\n\n"+
			"## v1.2.0 (2024-01-01)\n\n- something old\n",
		&FileWriter{}, ccme.Version{Major: 2})

	assert.True(t, strings.HasPrefix(out, "# Changelog\n\n## core@2.0.0 ("), "%q", out)
	assert.Equal(t, 1, strings.Count(out, "# Changelog"), "the title is never duplicated")
	assertOrder(t, out, "## core@2.0.0 (", "[![build]", "## v1.2.0 (", "- something old")
}

func TestRecordKeepsTheFilesByteOrderMarkAndLineEndings(t *testing.T) {
	// A changelog checked out on Windows: a UTF-8 byte-order mark, CRLF
	// endings throughout. Neither may defeat the title match — a title the
	// match misses is a title the next release writes a second copy of — and
	// neither may be rewritten: the mark stays at the very top, before
	// anything, and the old entries keep the endings they were written with.
	existing := "\ufeff# Changelog\r\n\r\n## v1.2.0 (2024-01-01)\r\n\r\n- something old\r\n"
	out := recordInto(t, existing, &FileWriter{}, ccme.Version{Major: 2})

	require.True(t, strings.HasPrefix(out, "\ufeff"), "the mark stays at the head of the file: %q", out)
	assert.True(t, strings.HasPrefix(out, "\ufeff# Changelog\n\n## core@2.0.0 ("),
		"the title was recognised through the mark and the CRLF: %q", out)
	assert.Equal(t, 1, strings.Count(out, "# Changelog"),
		"a CRLF title must not grow a second copy of itself")
	assert.Contains(t, out, "## v1.2.0 (2024-01-01)\r\n\r\n- something old\r\n",
		"the old content keeps its own line endings")
	assert.Equal(t, 1, strings.Count(out, "\ufeff"), "and the mark is written exactly once")
}

func TestRecordKeepsTheByteOrderMarkAboveAPreamble(t *testing.T) {
	// The same mark on a file dispat did not write: it belongs before the
	// preamble, because it belongs before everything.
	out := recordInto(t, "\ufeff# Change Log\r\n\r\n## v1.2.0 (2024-01-01)\r\n\r\n- old\r\n",
		&FileWriter{}, ccme.Version{Major: 2})

	assert.True(t, strings.HasPrefix(out, "\ufeff# Change Log\n\n\n## core@2.0.0 ("), "%q", out)
	assert.Contains(t, out, "## v1.2.0 (2024-01-01)\r\n\r\n- old\r\n")
}

func TestHasEntryReadsThroughAByteOrderMark(t *testing.T) {
	// The idempotence check is what stops an entry being written twice, so it
	// has to see an entry however the file was saved. A mark sits in front of
	// the first line, which is the one line a changelog can open an entry on.
	assert.True(t, HasEntry([]byte("\ufeff## core@1.3.0 (2026-08-09)\n"), "core@1.3.0"))
	assert.True(t, HasEntry([]byte("\ufeff# Changelog\r\n\r\n## core@1.3.0 (2026-08-09)\r\n"), "core@1.3.0"))
}

func TestRecordOverAPreambleIsIdempotent(t *testing.T) {
	// Adoption converges: the second run over the file the first produced
	// recognises its own entry, skips, and changes nothing — and a third
	// release still inserts between the preamble and the entries rather than
	// above the preamble.
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "CHANGELOG.md"),
		[]byte("---\ntitle: Releases\n---\n\n# Change Log\n\n## v1.2.0 (2024-01-01)\n\n- old\n"), 0o644))
	w := &FileWriter{Now: func() time.Time { return testDate }}
	ctx := context.Background()

	require.NoError(t, w.Record(ctx, testRelease(dir, ccme.Version{Major: 2})))
	first, err := os.ReadFile(filepath.Join(dir, "CHANGELOG.md"))
	require.NoError(t, err)
	require.NoError(t, w.Record(ctx, testRelease(dir, ccme.Version{Major: 2})))
	again, err := os.ReadFile(filepath.Join(dir, "CHANGELOG.md"))
	require.NoError(t, err)
	assert.Equal(t, string(first), string(again), "a repeated write is byte-identical")

	require.NoError(t, w.Record(ctx, testRelease(dir, ccme.Version{Major: 2, Minor: 1})))
	third, err := os.ReadFile(filepath.Join(dir, "CHANGELOG.md"))
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(string(third), "---\ntitle: Releases\n---\n\n# Change Log\n\n\n## core@2.1.0 ("),
		"the front matter is still front matter:\n%s", third)
	assertOrder(t, string(third), "## core@2.1.0 (", "## core@2.0.0 (", "## v1.2.0 (")
}

func TestRecordPreambleSeamFollowsEntrySpacing(t *testing.T) {
	// The seam above the inserted entry is the configured one, like every
	// other seam the writer owns.
	out := recordInto(t, "# Change Log\n\n## v1.2.0 (2024-01-01)\n\n- old\n",
		&FileWriter{EntrySpacing: 1}, ccme.Version{Major: 2})
	assert.Contains(t, out, "# Change Log\n\n## core@2.0.0 (")
	assert.NotContains(t, out, "\n\n\n")
}

// TestRecordEmptyFileTitleKeepsTheFilesOwnHead: a fileTitle whose every line
// is filtered out for this release renders as nothing, and nothing heads every
// file there is. Read as a match it would strip no bytes, write no title in
// their place, and put the entry at byte zero — above the head the file
// brought with it. The preamble path is what an unrecognisable title has
// always meant.
func TestRecordEmptyFileTitleKeepsTheFilesOwnHead(t *testing.T) {
	w := &FileWriter{
		FileTitle: []model.EntryLine{
			{Line: []string{"# Changelog"}, Channels: []string{"beta"}},
		},
	}
	out := recordInto(t, "# Change Log\n\n## v1.2.0 (2024-01-01)\n\n- something old\n",
		w, ccme.Version{Major: 2})

	assert.True(t, strings.HasPrefix(out, "# Change Log\n"),
		"the file keeps its own head:\n%s", out)
	assertOrder(t, out, "# Change Log", "## core@2.0.0 (", "## v1.2.0 (", "- something old")
}

// TestRecordKeepsAFencedExampleInThePreambleWhole: a preamble that documents
// the file's own shape shows an entry heading as an example. Splitting the
// file at it would leave the opening fence above the new entry and the closing
// one below, which breaks both the preamble and every entry under it.
func TestRecordKeepsAFencedExampleInThePreambleWhole(t *testing.T) {
	preamble := "# Change Log\n\nEntries are written like this:\n\n" +
		"```markdown\n## v1.0.0 (2024-01-01)\n\n- what changed\n```"
	out := recordInto(t, preamble+"\n\n## v1.2.0 (2024-01-01)\n\n- something old\n",
		&FileWriter{}, ccme.Version{Major: 2})

	assert.True(t, strings.HasPrefix(out, preamble),
		"the fenced example survives byte for byte:\n%s", out)
	assertOrder(t, out, "## v1.0.0 (2024-01-01)", "## core@2.0.0 (", "## v1.2.0 (")
	assert.Equal(t, 2, strings.Count(out, "```"), "the fence is neither split nor duplicated:\n%s", out)
}

// TestRecordSaysWhenTheFileKeepsItsOwnHead: a configured title that is never
// written looks from the outside like a title dispat ignored, so the writer
// says which of the two happened, once per write.
func TestRecordSaysWhenTheFileKeepsItsOwnHead(t *testing.T) {
	var buf strings.Builder
	w := &FileWriter{Log: zerolog.New(&buf).Level(zerolog.DebugLevel)}
	recordInto(t, "# Change Log\n\n## v1.2.0 (2024-01-01)\n\n- something old\n", w, ccme.Version{Major: 2})

	out := buf.String()
	assert.Contains(t, out, "existing changelog keeps its own head")
	assert.Contains(t, out, `"package":"core"`)
	assert.Contains(t, out, `"tag":"core@2.0.0"`)
	assert.Contains(t, out, "CHANGELOG.md")
	assert.Equal(t, 1, strings.Count(out, "keeps its own head"), "said once per write:\n%s", out)

	// A file dispat has been writing is not the preamble path, and says
	// nothing about it.
	var wrote strings.Builder
	own := &FileWriter{Log: zerolog.New(&wrote).Level(zerolog.DebugLevel)}
	recordInto(t, "# Changelog\n\n## core@1.0.0 (2024-01-01)\n\n- old\n", own, ccme.Version{Major: 2})
	assert.NotContains(t, wrote.String(), "keeps its own head")
}

// TestNoteEntryAnnotatesTheEntryWithoutMovingItsHeader: the note goes inside
// the entry, after the header line, because the header is what every re-run
// recognises an existing entry by. A note that moved or split that line would
// make the next run write the entry a second time.
func TestNoteEntryAnnotatesTheEntryWithoutMovingItsHeader(t *testing.T) {
	dir := t.TempDir()
	rel := testRelease(dir, ccme.Version{Major: 1, Minor: 2})
	rel.Pkg.Changelog = model.ChangelogSpec{Enabled: true}
	w := &FileWriter{Now: func() time.Time { return testDate }}
	require.NoError(t, w.Record(context.Background(), rel))

	path, noted, err := NoteEntry(rel, "Something true about how this went out.\nOn two lines.")
	require.NoError(t, err)
	require.True(t, noted)
	assert.Equal(t, filepath.Join(dir, "CHANGELOG.md"), path)

	body, rerr := os.ReadFile(path)
	require.NoError(t, rerr)
	assert.True(t, HasEntry(body, rel.TagName()), "the header a re-run looks for is untouched")
	assert.Contains(t, string(body), "> Something true about how this went out.")
	assert.Contains(t, string(body), "> On two lines.", "every line is quoted, so none reads as a heading")
	header := strings.Index(string(body), "## "+rel.TagName()+" (")
	note := strings.Index(string(body), "> Something true")
	features := strings.Index(string(body), "### ")
	assert.Less(t, header, note, "the note follows the header")
	assert.Less(t, note, features, "and precedes the sections")

	// Nothing to annotate is silence rather than a file invented to hold the
	// note: a policy that records no entry, and a file with no entry in it.
	off := testRelease(t.TempDir(), ccme.Version{Major: 1, Minor: 2})
	off.Pkg.Changelog = model.ChangelogSpec{Enabled: false}
	_, noted, err = NoteEntry(off, "nothing to say this about")
	require.NoError(t, err)
	assert.False(t, noted)

	empty := testRelease(t.TempDir(), ccme.Version{Major: 1, Minor: 2})
	empty.Pkg.Changelog = model.ChangelogSpec{Enabled: true}
	_, noted, err = NoteEntry(empty, "nothing to say this about")
	require.NoError(t, err)
	assert.False(t, noted, "a changelog that does not exist is not created to hold a note")

	require.NoError(t, os.WriteFile(filepath.Join(empty.Pkg.Dir, "CHANGELOG.md"),
		[]byte("# Changelog\n\n## core@0.9.0 (2024-01-01)\n\n- older\n"), 0o644))
	_, noted, err = NoteEntry(empty, "nothing to say this about")
	require.NoError(t, err)
	assert.False(t, noted, "nor is an entry this release never wrote annotated")
}
