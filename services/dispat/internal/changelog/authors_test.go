// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

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

// Unit tests of the attribution the renderer writes: the per-line suffix, the
// section block, the filters between them, and the block order the whole entry
// depends on.

var (
	ada   = plan.Author{Name: "Ada Lovelace", Email: "ada@example.com"}
	grace = plan.Author{Name: "Grace Hopper", Email: "grace@example.com"}
	alan  = plan.Author{Name: "Alan Turing", Email: "alan@example.com"}
	bot   = plan.Author{Name: "dependabot[bot]", Email: "49699333+dependabot[bot]@users.noreply.github.com"}
)

// authored builds a release whose units carry the given attribution, in the
// order the arguments are given.
func authored(units []*ccme.Unit, byUnit map[*ccme.Unit][]plan.Author, window ...plan.Author) *plan.Release {
	return &plan.Release{
		Pkg:     &model.Package{Name: "core", Dir: "/tmp/core", Space: &model.Space{Name: "libs"}},
		Next:    ccme.Version{Major: 1, Minor: 1},
		Channel: ccme.ChannelStable,
		Units:   units,
		// A stable-line release has Units == FreshUnits; setting only one of
		// them is the fixture mistake plan.go warns about.
		FreshUnits:         units,
		UnitAuthors:        byUnit,
		WindowAuthors:      window,
		FreshWindowAuthors: window,
	}
}

func TestFilterAuthors(t *testing.T) {
	all := []plan.Author{ada, grace, bot}
	for name, tc := range map[string]struct {
		include, exclude []string
		want             []plan.Author
	}{
		"no_filters":       {nil, nil, all},
		"include_one":      {[]string{"Ada Lovelace"}, nil, []plan.Author{ada}},
		"include_glob":     {[]string{"*Hopper"}, nil, []plan.Author{grace}},
		"exclude_bot":      {nil, []string{"*bot*"}, []plan.Author{ada, grace}},
		"exclude_wins":     {[]string{"*"}, []string{"*[bot]*"}, []plan.Author{ada, grace}},
		"by_email":         {[]string{"*@example.com"}, nil, []plan.Author{ada, grace}},
		"by_username":      {[]string{"grace"}, nil, []plan.Author{grace}},
		"case_insensitive": {[]string{"ADA LOVELACE"}, nil, []plan.Author{ada}},
		"pattern_case":     {nil, []string{"*BOT*"}, []plan.Author{ada, grace}},
		"include_nothing":  {[]string{"nobody"}, nil, []plan.Author{}},
		"empty_lists":      {[]string{}, []string{}, all},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, FilterAuthors(all, tc.include, tc.exclude))
		})
	}
}

func TestFilterAuthorsMatchesEachIdentityAxis(t *testing.T) {
	// An operator writing a filter is thinking of a person, not of a field, so
	// all three spellings of the person answer the same pattern.
	only := []plan.Author{ada}
	for _, pattern := range []string{"Ada*", "ada", "ada@example.com", "*@example.com", "*love*"} {
		assert.Equal(t, only, FilterAuthors(only, []string{pattern}, nil),
			"pattern %q must reach the author", pattern)
	}
	assert.Empty(t, FilterAuthors(only, []string{"grace*"}, nil))
}

func TestAuthorLabel(t *testing.T) {
	assert.Equal(t, "Ada Lovelace", authorLabel(ada, AuthorsFullName))
	assert.Equal(t, "ada", authorLabel(ada, AuthorsUsername))
	// Neither form ever renders an empty bullet: each falls back to the other.
	assert.Equal(t, "nameless", authorLabel(plan.Author{Email: "nameless@example.com"}, AuthorsFullName))
	assert.Equal(t, "Just A Name", authorLabel(plan.Author{Name: "Just A Name"}, AuthorsUsername))
}

func TestAuthorSuffixPlacements(t *testing.T) {
	u := testUnit("feat", ccme.BumpMinor, "add streaming")
	rel := authored([]*ccme.Unit{u}, map[*ccme.Unit][]plan.Author{u: {ada, grace}}, ada, grace)

	for name, tc := range map[string]struct {
		placement string
		want      string
	}{
		"off":     {AuthorsOff, ""},
		"inline":  {AuthorsInline, " (by Ada Lovelace, Grace Hopper)"},
		"section": {AuthorsSection, ""},
		"both":    {AuthorsBoth, " (by Ada Lovelace, Grace Hopper)"},
	} {
		t.Run(name, func(t *testing.T) {
			f := SpecFormat(model.RecordFormat{AuthorsPlacement: tc.placement}).withDefaults()
			assert.Equal(t, tc.want, authorSuffix(rel, u, f))
		})
	}

	f := SpecFormat(model.RecordFormat{AuthorsPlacement: AuthorsInline, AuthorsFormat: AuthorsUsername}).withDefaults()
	assert.Equal(t, " (by ada, grace)", authorSuffix(rel, u, f))

	// Filtered to nobody renders nothing rather than an empty bracket.
	f = SpecFormat(model.RecordFormat{AuthorsPlacement: AuthorsInline, AuthorsExclude: []string{"*"}}).withDefaults()
	assert.Equal(t, "", authorSuffix(rel, u, f))
}

func TestAuthorSuffixFollowsTheCorrectionNote(t *testing.T) {
	// The note is part of what the line says about the work; who did it comes
	// after what was done.
	u := testUnit("fix", ccme.BumpPatch, "close leak")
	rel := authored([]*ccme.Unit{u}, map[*ccme.Unit][]plan.Author{u: {ada}}, ada)
	rel.Corrects = map[*ccme.Unit][]string{u: {"abc1234"}}

	out := RenderSections(rel, SpecFormat(model.RecordFormat{AuthorsPlacement: AuthorsInline}))
	assert.Contains(t, out, "- close leak (corrects abc1234) (by Ada Lovelace)")
}

func TestAuthorsSectionRendersUnderItsTitle(t *testing.T) {
	u1 := testUnit("feat", ccme.BumpMinor, "add streaming")
	u2 := testUnit("fix", ccme.BumpPatch, "close leak")
	rel := authored([]*ccme.Unit{u1, u2},
		map[*ccme.Unit][]plan.Author{u1: {ada, grace}, u2: {grace, alan}}, ada, grace, alan)

	out := authorsSection(rel, SpecFormat(model.RecordFormat{AuthorsPlacement: AuthorsSection}).withDefaults())
	assert.Equal(t, "### Authors\n\n- Ada Lovelace\n- Grace Hopper\n- Alan Turing\n", out,
		"deduped across units, in the order the entry's own lines are in")

	custom := authorsSection(rel,
		SpecFormat(model.RecordFormat{AuthorsPlacement: AuthorsBoth, AuthorsTitle: "Thanks to"}).withDefaults())
	assert.True(t, strings.HasPrefix(custom, "### Thanks to\n\n"), custom)
}

func TestAuthorsSectionIsSilentWhenItWouldBeEmpty(t *testing.T) {
	u := testUnit("feat", ccme.BumpMinor, "add streaming")
	rel := authored([]*ccme.Unit{u}, map[*ccme.Unit][]plan.Author{u: {bot}}, bot)

	// A heading over no names reads as a failed write, so nothing is rendered.
	f := SpecFormat(model.RecordFormat{AuthorsPlacement: AuthorsSection, AuthorsExclude: []string{"*bot*"}}).withDefaults()
	assert.Equal(t, "", authorsSection(rel, f))

	// And the placements that do not ask for a section never render one.
	for _, p := range []string{AuthorsOff, AuthorsInline} {
		assert.Equal(t, "", authorsSection(rel, SpecFormat(model.RecordFormat{AuthorsPlacement: p}).withDefaults()))
	}
}

func TestAuthorsSectionCCMEVersusAll(t *testing.T) {
	// "ccme" lists the people behind the lines above it; "all" reaches the
	// window, which is where an author with no release record of their own
	// lives.
	u := testUnit("feat", ccme.BumpMinor, "add streaming")
	rel := authored([]*ccme.Unit{u}, map[*ccme.Unit][]plan.Author{u: {ada}}, ada, grace)

	ccmeOut := authorsSection(rel,
		SpecFormat(model.RecordFormat{AuthorsPlacement: AuthorsSection, AuthorsCommits: AuthorsCommitsCCME}).withDefaults())
	assert.Equal(t, "### Authors\n\n- Ada Lovelace\n", ccmeOut)

	allOut := authorsSection(rel,
		SpecFormat(model.RecordFormat{AuthorsPlacement: AuthorsSection, AuthorsCommits: AuthorsCommitsAll}).withDefaults())
	assert.Equal(t, "### Authors\n\n- Ada Lovelace\n- Grace Hopper\n", allOut)
}

func TestAuthorsSectionOnANoChangesRelease(t *testing.T) {
	// A ride has no units, so "ccme" has nobody to name. "all" still credits
	// whoever moved the repository, which is the honest answer for an entry
	// whose body says only that the version moved.
	rel := authored(nil, nil, ada)
	rel.FixedRide = true
	require.True(t, rel.NoChanges())

	assert.Equal(t, "", authorsSection(rel,
		SpecFormat(model.RecordFormat{AuthorsPlacement: AuthorsSection}).withDefaults()))
	assert.Equal(t, "### Authors\n\n- Ada Lovelace\n", authorsSection(rel,
		SpecFormat(model.RecordFormat{AuthorsPlacement: AuthorsSection, AuthorsCommits: AuthorsCommitsAll}).withDefaults()))
}

func TestRenderBodyBlockOrderIsHeaderSectionsAuthorsExtraFooter(t *testing.T) {
	// The order is byte-exact on purpose. Self-update reads release notes by
	// cutting at the "---" a release footer conventionally opens with, so a
	// block landing after the footer would be read as part of the cut-away
	// tail; and the GitHub recorder's "### Release" details are the last thing
	// before the footer.
	u := testUnit("feat", ccme.BumpMinor, "add streaming")
	rel := authored([]*ccme.Unit{u}, map[*ccme.Unit][]plan.Author{u: {ada}}, ada)

	f := SpecFormat(model.RecordFormat{
		AuthorsPlacement: AuthorsSection,
		Header:           []model.EntryLine{{Line: []string{"Header line."}}},
		Footer:           []model.EntryLine{{Line: []string{"---", "Footer line."}}},
	})
	got := RenderBody(rel, f, nil, "### Release\n\n- commit: abc\n")

	assert.Equal(t, "Header line.\n"+
		"\n"+
		"### Features\n"+
		"\n"+
		"- add streaming\n"+
		"\n"+
		"### Authors\n"+
		"\n"+
		"- Ada Lovelace\n"+
		"\n"+
		"### Release\n"+
		"\n"+
		"- commit: abc\n"+
		"\n"+
		"---\nFooter line.\n", got)
}

func TestRenderBodyWithAuthorsOffIsUnchanged(t *testing.T) {
	// The default must leave every existing record byte for byte what it was.
	u := testUnit("feat", ccme.BumpMinor, "add streaming")
	rel := authored([]*ccme.Unit{u}, map[*ccme.Unit][]plan.Author{u: {ada, grace}}, ada, grace)

	assert.Equal(t, RenderBody(rel, Format{}, nil),
		RenderBody(rel, SpecFormat(model.RecordFormat{AuthorsPlacement: AuthorsOff, AuthorsTitle: "Ignored"}), nil))
	assert.NotContains(t, RenderBody(rel, Format{}, nil), "Ada")
}

func TestWithDefaultsFillsTheAuthorsPolicy(t *testing.T) {
	f := Format{}.withDefaults()
	assert.Equal(t, AuthorsOff, f.AuthorsPlacement)
	assert.Equal(t, AuthorsFullName, f.AuthorsFormat)
	assert.Equal(t, AuthorsCommitsCCME, f.AuthorsCommits)
	assert.Equal(t, "Authors", f.AuthorsTitle)

	// A configured value is never overwritten by a default.
	set := SpecFormat(model.RecordFormat{AuthorsPlacement: AuthorsBoth, AuthorsFormat: AuthorsUsername,
		AuthorsCommits: AuthorsCommitsAll, AuthorsTitle: "Contributors"}).withDefaults()
	assert.Equal(t, AuthorsBoth, set.AuthorsPlacement)
	assert.Equal(t, AuthorsUsername, set.AuthorsFormat)
	assert.Equal(t, AuthorsCommitsAll, set.AuthorsCommits)
	assert.Equal(t, "Contributors", set.AuthorsTitle)
}

func TestSpecFormatCarriesTheAuthorsPolicy(t *testing.T) {
	// The renderer reads its policy from the resolved record format, so a
	// field that fails to arrive is one the configuration cannot reach. The
	// embed makes the arrival structural rather than copied; this case stands
	// as the guard against SpecFormat ever regrowing a field-by-field mapping.
	got := SpecFormat(model.RecordFormat{
		AuthorsPlacement: AuthorsBoth,
		AuthorsFormat:    AuthorsUsername,
		AuthorsCommits:   AuthorsCommitsAll,
		AuthorsInclude:   []string{"a*"},
		AuthorsExclude:   []string{"*bot*"},
		AuthorsTitle:     "Contributors",
	})
	assert.Equal(t, AuthorsBoth, got.AuthorsPlacement)
	assert.Equal(t, AuthorsUsername, got.AuthorsFormat)
	assert.Equal(t, AuthorsCommitsAll, got.AuthorsCommits)
	assert.Equal(t, []string{"a*"}, got.AuthorsInclude)
	assert.Equal(t, []string{"*bot*"}, got.AuthorsExclude)
	assert.Equal(t, "Contributors", got.AuthorsTitle)
}

func TestSectionAuthorsNarrowsWithNotesUnitsOnAPrerelease(t *testing.T) {
	// The section follows the lines: a prerelease documents its own changeset,
	// so it is attributed to whoever wrote that changeset.
	old := testUnit("feat", ccme.BumpMinor, "already shipped")
	fresh := testUnit("fix", ccme.BumpPatch, "new work")
	rel := authored([]*ccme.Unit{old, fresh},
		map[*ccme.Unit][]plan.Author{old: {ada}, fresh: {grace}}, ada, grace)
	rel.Next = ccme.Version{Major: 1, Minor: 1, Prerelease: []string{"beta", "1"}}
	rel.Channel = "beta"
	rel.FreshUnits = []*ccme.Unit{fresh}
	rel.FreshWindowAuthors = []plan.Author{grace}

	f := SpecFormat(model.RecordFormat{AuthorsPlacement: AuthorsSection}).withDefaults()
	assert.Equal(t, "### Authors\n\n- Grace Hopper\n", authorsSection(rel, f))

	f.AuthorsCommits = AuthorsCommitsAll
	assert.Equal(t, "### Authors\n\n- Grace Hopper\n", authorsSection(rel, f),
		"AllAuthors narrows to the fresh window on a prerelease too")
}

func TestSuppressedUnitLeavesTheAttributionWithTheLine(t *testing.T) {
	// A revert takes the entry out of the notes (§7.3); the attribution goes
	// with it, because there is no line left to attribute.
	kept := testUnit("feat", ccme.BumpMinor, "kept")
	gone := testUnit("feat", ccme.BumpMinor, "reverted")
	rel := authored([]*ccme.Unit{kept, gone},
		map[*ccme.Unit][]plan.Author{kept: {ada}, gone: {grace}}, ada, grace)
	rel.SuppressedNotes = map[*ccme.Unit]bool{gone: true}

	f := SpecFormat(model.RecordFormat{AuthorsPlacement: AuthorsSection}).withDefaults()
	assert.Equal(t, "### Authors\n\n- Ada Lovelace\n", authorsSection(rel, f))

	f.AuthorsCommits = AuthorsCommitsAll
	assert.Contains(t, authorsSection(rel, f), "- Grace Hopper",
		"the window still knows the reverted work was done")
}
