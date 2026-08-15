package changelog

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/ccme"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// titleLines is the unfiltered list a bare string in the config decodes into.
func titleLines(text ...string) []model.EntryLine {
	return []model.EntryLine{{Line: text}}
}

// lineFor builds one filtered line; empty filters are left unset.
func lineFor(text string, pkgs, spaces, groups []string) model.EntryLine {
	return model.EntryLine{Line: []string{text}, Package: pkgs, Space: spaces, Group: groups}
}

func TestRenderLinesUnfiltered(t *testing.T) {
	rel := testRelease("/tmp/x", ccme.Version{Major: 2})
	out := RenderLines([]model.EntryLine{
		{Line: []string{"first"}},
		{Line: []string{"second", "third"}},
	}, rel, nil)
	assert.Equal(t, "first\nsecond\nthird\n", out, "every line, in order, one newline each")
}

func TestRenderLinesEmptyIsEmpty(t *testing.T) {
	rel := testRelease("/tmp/x", ccme.Version{Major: 2})
	assert.Empty(t, RenderLines(nil, rel, nil))
	assert.Empty(t, RenderLines([]model.EntryLine{}, rel, nil))
}

func TestRenderLinesBlankLineIsWritten(t *testing.T) {
	rel := testRelease("/tmp/x", ccme.Version{Major: 2})
	assert.Equal(t, "\ntext\n", RenderLines(titleLines("", "text"), rel, nil),
		"an empty string is a deliberate blank line, not an absent one")
}

// TestRenderLinesFilters covers the three filters against package core, in
// space libs, versioning as one group so that it has a group to match.
func TestRenderLinesFilters(t *testing.T) {
	rel := testRelease("/tmp/x", ccme.Version{Major: 2})
	rel.Pkg.Space.Versioning = model.VersioningFixed
	cases := []struct {
		name  string
		line  model.EntryLine
		wants bool
	}{
		{"package matches", lineFor("x", []string{"core"}, nil, nil), true},
		{"package misses", lineFor("x", []string{"other"}, nil, nil), false},
		{"space matches", lineFor("x", nil, []string{"libs"}, nil), true},
		{"space misses", lineFor("x", nil, []string{"apps"}, nil), false},
		{"group matches", lineFor("x", nil, nil, []string{"libs"}), true},
		{"group misses", lineFor("x", nil, nil, []string{"apps"}), false},
		{"glob matches", lineFor("x", []string{"co*"}, nil, nil), true},
		{"glob crosses separators", lineFor("x", []string{"*e"}, nil, nil), true},
		{"case-insensitive", lineFor("x", []string{"CORE"}, nil, nil), true},
		{"one of several values", lineFor("x", []string{"a", "core", "b"}, nil, nil), true},
		{"none of several values", lineFor("x", []string{"a", "b"}, nil, nil), false},
		{"every filter must match", lineFor("x", []string{"core"}, []string{"apps"}, nil), false},
		{"all filters match", lineFor("x", []string{"core"}, []string{"libs"}, []string{"libs"}), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := RenderLines([]model.EntryLine{tc.line}, rel, nil)
			if tc.wants {
				assert.Equal(t, "x\n", out)
			} else {
				assert.Empty(t, out)
			}
		})
	}
}

// TestRenderLinesChannelFilter: the one filter that asks about the release
// rather than the package. A line reaches the prereleases alone, the stables
// alone, or one named channel, and no filter still reaches everything.
func TestRenderLinesChannelFilter(t *testing.T) {
	cases := []struct {
		name     string
		next     ccme.Version
		channels []string
		wants    bool
	}{
		{"no filter on a stable release", ccme.Version{Major: 2}, nil, true},
		{"no filter on a prerelease", ccme.Version{Major: 2, Prerelease: []string{"beta", "1"}}, nil, true},
		{"stable on a stable release", ccme.Version{Major: 2}, []string{"stable"}, true},
		{"stable on a prerelease", ccme.Version{Major: 2, Prerelease: []string{"beta", "1"}},
			[]string{"stable"}, false},
		{"any prerelease on a beta", ccme.Version{Major: 2, Prerelease: []string{"beta", "1"}},
			[]string{"*"}, true},
		{"any prerelease on a stable release", ccme.Version{Major: 2}, []string{"*"}, false},
		{"a named channel on its own", ccme.Version{Major: 2, Prerelease: []string{"beta", "1"}},
			[]string{"beta"}, true},
		{"a named channel on another", ccme.Version{Major: 2, Prerelease: []string{"rc", "1"}},
			[]string{"beta"}, false},
		{"stable and a name together", ccme.Version{Major: 2}, []string{"stable", "beta"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rel := testRelease("/tmp/x", tc.next)
			out := RenderLines([]model.EntryLine{{Line: []string{"x"}, Channels: tc.channels}}, rel, nil)
			if tc.wants {
				assert.Equal(t, "x\n", out)
			} else {
				assert.Empty(t, out)
			}
		})
	}
}

// TestRenderLinesChannelCombinesWithPackageFilters: channels is one filter
// among the others, so a line carrying both is written only where both hold.
func TestRenderLinesChannelCombinesWithPackageFilters(t *testing.T) {
	beta := testRelease("/tmp/x", ccme.Version{Major: 2, Prerelease: []string{"beta", "1"}})
	stable := testRelease("/tmp/x", ccme.Version{Major: 2})
	line := model.EntryLine{Line: []string{"x"}, Package: []string{"core"}, Channels: []string{"*"}}

	assert.Equal(t, "x\n", RenderLines([]model.EntryLine{line}, beta, nil),
		"the package and the channel both match")
	assert.Empty(t, RenderLines([]model.EntryLine{line}, stable, nil),
		"the package matches and the channel does not")

	beta.Pkg.Name = "other"
	assert.Empty(t, RenderLines([]model.EntryLine{line}, beta, nil),
		"the channel matches and the package does not")
}

// TestRenderLinesIndependentPackageHasNoGroup: a package whose space versions
// independently shares its version with nothing, so it belongs to no group and
// a group filter cannot select it. Its space still can.
func TestRenderLinesIndependentPackageHasNoGroup(t *testing.T) {
	rel := testRelease("/tmp/x", ccme.Version{Major: 2})
	require.Equal(t, model.Versioning(""), rel.Pkg.Space.Versioning, "the fixture versions independently")

	assert.Empty(t, RenderLines([]model.EntryLine{lineFor("x", nil, nil, []string{"libs"})}, rel, nil))
	assert.Empty(t, RenderLines([]model.EntryLine{lineFor("x", nil, nil, []string{"*"})}, rel, nil))
	assert.Equal(t, "x\n", RenderLines([]model.EntryLine{lineFor("x", nil, []string{"libs"}, nil)}, rel, nil))
}

// TestRenderLinesSpacelessPackageMatchesNoSpaceFilter: a standalone package
// belongs to no space, so a space or group filter cannot select it — but an
// unfiltered line still reaches it.
func TestRenderLinesSpacelessPackageMatchesNoSpaceFilter(t *testing.T) {
	rel := testRelease("/tmp/x", ccme.Version{Major: 2})
	rel.Pkg.Space = nil

	assert.Empty(t, RenderLines([]model.EntryLine{lineFor("x", nil, []string{"libs"}, nil)}, rel, nil))
	assert.Empty(t, RenderLines([]model.EntryLine{lineFor("x", nil, nil, []string{"libs"})}, rel, nil))
	assert.Equal(t, "x\n", RenderLines(titleLines("x"), rel, nil))
	assert.Equal(t, "x\n", RenderLines([]model.EntryLine{lineFor("x", []string{"core"}, nil, nil)}, rel, nil))
}

func TestExpandVariables(t *testing.T) {
	look := func(name string) (string, bool) {
		v, ok := map[string]string{"NAME": "core", "EMPTY": ""}[name]
		return v, ok
	}
	cases := []struct{ in, want string }{
		{"$NAME", "core"},
		{"${NAME}", "core"},
		{"a-${NAME}-b", "a-core-b"},
		{"${NAME}${NAME}", "corecore"},
		{"$MISSING", ""},
		{"before $MISSING after", "before  after"},
		{"$EMPTY!", "!"},
		{"no variables", "no variables"},
		{"", ""},
		{"100% $ pure", "100% $ pure"},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, Expand(tc.in, look), "expanding %q", tc.in)
	}
}

func TestExpandWithoutLookupIsLiteral(t *testing.T) {
	assert.Equal(t, "$NAME", Expand("$NAME", nil))
}

// TestReleaseLookupPrefersTheRelease: the release's own variables and its
// script outputs answer first, the process environment only fills the gaps.
func TestReleaseLookupPrefersTheRelease(t *testing.T) {
	t.Setenv("DISPAT_PACKAGE", "from-the-environment")
	t.Setenv("ONLY_IN_ENV", "env-value")

	rel := testRelease("/tmp/x", ccme.Version{Major: 2})
	rel.Outputs = []plan.Output{{Name: "IMAGE", Value: "acme/core:2.0.0", Source: "core:build"}}
	look := ReleaseLookup(rel)

	got := func(name string) string { v, _ := look(name); return v }
	assert.Equal(t, "core", got("DISPAT_PACKAGE"), "the release wins over the environment")
	assert.Equal(t, "libs", got("DISPAT_SPACE"))
	assert.Equal(t, "core@2.0.0", got("DISPAT_TAG"))
	assert.Equal(t, "2.0.0", got("DISPAT_VERSION"))
	assert.Equal(t, "acme/core:2.0.0", got("DISPAT_OUTPUT_IMAGE"), "script outputs resolve too")
	assert.Equal(t, "core:build", got("DISPAT_OUTPUT_SOURCE_IMAGE"))
	assert.Equal(t, "env-value", got("ONLY_IN_ENV"), "and the environment fills the gaps")

	_, ok := look("NOTHING_DEFINES_THIS")
	assert.False(t, ok)
}

func TestReleaseLookupWithoutAPackage(t *testing.T) {
	t.Setenv("ONLY_IN_ENV", "env-value")
	look := ReleaseLookup(&plan.Release{})
	v, ok := look("ONLY_IN_ENV")
	assert.True(t, ok)
	assert.Equal(t, "env-value", v)
}

// assertOrder fails unless every marker appears in out, in the order given.
func assertOrder(t *testing.T, out string, markers ...string) {
	t.Helper()
	at := -1
	for _, marker := range markers {
		i := strings.Index(out, marker)
		require.NotEqual(t, -1, i, "missing %q in:\n%s", marker, out)
		assert.Greater(t, i, at, "%q is out of order in:\n%s", marker, out)
		at = i
	}
}

var placementFormat = Format{
	ReleaseName: "Winter release",
	Header:      titleLines("header line"),
	Footer:      titleLines("footer line"),
}

// TestRenderEntryBodyPlacement: in a changelog entry the release name opens
// the body as a sub-header, then the header lines, the sections, the footer.
func TestRenderEntryBodyPlacement(t *testing.T) {
	rel := testRelease("/tmp/x", ccme.Version{Major: 2})
	assertOrder(t, RenderEntryBody(rel, placementFormat, nil),
		"### Winter release", "header line",
		"### Breaking Changes", "### Features", "### Fixes", "### Dependencies",
		"footer line")
}

// TestRenderBodyPlacement: the shared body carries no release name — GitHub
// puts it in the release's own name field — and the caller's extra section
// sits between the sections and the footer.
func TestRenderBodyPlacement(t *testing.T) {
	rel := testRelease("/tmp/x", ccme.Version{Major: 2})
	out := RenderBody(rel, placementFormat, nil, "### Release\n\n- commit: abc\n")

	assert.NotContains(t, out, "Winter release", "the name belongs to the destination, not the body")
	assertOrder(t, out, "header line",
		"### Breaking Changes", "### Features", "### Fixes", "### Dependencies",
		"### Release", "footer line")
}

// TestRenderBodyBlockSpacing: whatever combination of blocks is configured,
// blocks are separated by exactly one blank line and none of them run
// together.
func TestRenderBodyBlockSpacing(t *testing.T) {
	rel := testRelease("/tmp/x", ccme.Version{Major: 2})
	rel.Units = []*ccme.Unit{testUnit("feat", ccme.BumpMinor, "add streaming")}
	rel.DueTo, rel.Updates = nil, nil

	out := RenderEntryBody(rel, Format{
		ReleaseName: "Name",
		Header:      titleLines("header"),
		Footer:      titleLines("footer"),
	}, nil)
	assert.Equal(t, "### Name\n\nheader\n\n### Features\n\n- add streaming\n\nfooter\n", out)
	assert.NotContains(t, out, "\n\n\n", "no block may leave a double blank line")
}

// TestRenderBodyWithoutOptionalBlocks: an unconfigured format renders exactly
// the sections, byte for byte — the guard that this feature costs nothing to
// a repository that does not use it.
func TestRenderBodyWithoutOptionalBlocks(t *testing.T) {
	rel := testRelease("/tmp/x", ccme.Version{Major: 2})
	f := Format{}
	assert.Equal(t, RenderSections(rel, f), RenderBody(rel, f, nil))
}

// TestRenderBodyWithoutSections: a release with nothing to group still writes
// its configured blocks, and they are the whole body.
func TestRenderBodyWithoutSections(t *testing.T) {
	rel := &plan.Release{
		Pkg:  &model.Package{Name: "core", Space: &model.Space{Name: "libs"}},
		Next: ccme.Version{Major: 2},
	}
	out := RenderBody(rel, Format{Header: titleLines("header"), Footer: titleLines("footer")}, nil)
	assert.Equal(t, "header\n\nfooter\n", out)
}

// TestRenderBodyAroundASharedVersioningRide: the one-line "no changes" body a
// fixed-group member renders still gets its header and footer.
func TestRenderBodyAroundASharedVersioningRide(t *testing.T) {
	rel := testRelease("/tmp/x", ccme.Version{Major: 2})
	rel.Units, rel.DueTo, rel.Updates = nil, nil, nil
	rel.FixedRide = true
	rel.Pkg.Space.Versioning = model.VersioningFixed
	require.True(t, rel.NoChanges(), "fixture must be a shared-versioning ride")

	out := RenderBody(rel, Format{Header: titleLines("header"), Footer: titleLines("footer")}, nil)
	assert.Equal(t, "header\n\nNo changes: a version bump to keep the versioning group on one version.\n\nfooter\n", out)
}

// TestRenderBodyExpandsEveryBlock: the release name, the header and the footer
// all interpolate against the same release.
func TestRenderBodyExpandsEveryBlock(t *testing.T) {
	rel := testRelease("/tmp/x", ccme.Version{Major: 2})
	out := RenderEntryBody(rel, Format{
		ReleaseName: "${DISPAT_PACKAGE} ${DISPAT_VERSION}",
		Header:      titleLines("space: ${DISPAT_SPACE}"),
		Footer:      titleLines("tag: ${DISPAT_TAG}"),
	}, nil)

	assert.Contains(t, out, "### core 2.0.0")
	assert.Contains(t, out, "space: libs")
	assert.Contains(t, out, "tag: core@2.0.0")
}

// TestRenderEntryCarriesTheBlocks: the dated entry header stays first, with
// the release name beneath it.
func TestRenderEntryCarriesTheBlocks(t *testing.T) {
	rel := testRelease("/tmp/x", ccme.Version{Major: 2})
	out := RenderEntry(rel, testDate, Format{
		ReleaseName: "Winter release",
		Header:      titleLines("header line"),
		Footer:      titleLines("footer line"),
	})

	assert.True(t, strings.HasPrefix(out, "## core@2.0.0 (2026-07-26)\n\n### Winter release\n"), out)
	assert.True(t, strings.HasSuffix(out, "footer line\n"), out)
	assert.True(t, HasEntry([]byte(out), "core@2.0.0"), "the tag line must stay findable")
}

// TestRenderEntryFilteredOutBlocksLeaveNothing: a header aimed at another
// package leaves the entry exactly as it would have been.
func TestRenderEntryFilteredOutBlocksLeaveNothing(t *testing.T) {
	rel := testRelease("/tmp/x", ccme.Version{Major: 2})
	plain := RenderEntry(rel, testDate, Format{})
	filtered := RenderEntry(rel, testDate, Format{
		Header: []model.EntryLine{lineFor("not for core", []string{"other"}, nil, nil)},
	})
	assert.Equal(t, plain, filtered)
}
