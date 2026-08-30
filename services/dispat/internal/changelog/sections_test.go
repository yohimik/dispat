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

// sectionsRelease is a release with one unit per built-in section and one
// dependency movement, which is what the ordering and grouping tests read.
func sectionsRelease() *plan.Release {
	rel := testRelease("/tmp/x", ccme.Version{Major: 2})
	rel.Updates[0].Tag = "utils@1.2.0"
	return rel
}

func TestIndentBodyMakesTheBodyPartOfItsBullet(t *testing.T) {
	// Two spaces on every non-blank line and nothing at all on the blank ones:
	// a flush-left paragraph after the first leaves the list item entirely,
	// and trailing whitespace on an empty line is the next thing a linter
	// complains about.
	got := indentBody("first\n\nsecond\n- inner\n")
	assert.Equal(t, "  first\n\n  second\n  - inner\n", got)

	assert.Equal(t, "", indentBody(""))
	assert.Equal(t, "  one", indentBody("one"))
}

func TestSectionItemsAreSeparatedByExactlyOneBlankLine(t *testing.T) {
	// The seam between two items and the seam below the section are the same
	// whether the items carry bodies or not. Before the normalisation a
	// section of bodiless bullets left a second blank line behind it while one
	// whose last bullet carried a body did not, so a file's spacing recorded
	// the shape of each release rather than one rule.
	rel := &plan.Release{
		Pkg:  &model.Package{Name: "core", Dir: "/tmp/x", Space: &model.Space{Name: "libs"}},
		Next: ccme.Version{Major: 1, Minor: 1},
		Units: []*ccme.Unit{
			testUnit("feat", ccme.BumpMinor, "one"),
			testUnit("feat", ccme.BumpMinor, "two"),
			testUnit("fix", ccme.BumpPatch, "three"),
		},
	}
	assert.Equal(t, "### Features\n\n- one\n\n- two\n\n### Fixes\n\n- three\n",
		RenderSections(rel, Format{}))

	rel.Units[1].Body = "why it was done"
	assert.Equal(t, "### Features\n\n- one\n\n- two\n  why it was done\n\n### Fixes\n\n- three\n",
		RenderSections(rel, Format{}))
}

func TestSectionsRenderInTheConfiguredOrder(t *testing.T) {
	f := Format{Sections: []model.RecordSection{
		{Builtin: model.SectionDependencies},
		{Builtin: model.SectionFixes},
		{Builtin: model.SectionFeatures},
		{Builtin: model.SectionBreaking},
	}}
	out := RenderSections(sectionsRelease(), f)
	order := []string{"### Dependencies", "### Fixes", "### Features", "### Breaking Changes"}
	at := -1
	for _, title := range order {
		i := strings.Index(out, title)
		require.Greater(t, i, at, "%s is out of order in:\n%s", title, out)
		at = i
	}
}

func TestOmittedBuiltinSectionsStillRender(t *testing.T) {
	// A `sections` list is how sections are ordered, never how one is dropped:
	// a built-in the list leaves out is appended after the listed ones, so no
	// released work can fall out of the record by omission. The resolution in
	// internal/config is what appends them; this pins that the renderer honours
	// exactly the order it is handed.
	f := Format{Sections: []model.RecordSection{
		{Builtin: model.SectionFixes},
		{Builtin: model.SectionBreaking},
		{Builtin: model.SectionFeatures},
		{Builtin: model.SectionDependencies},
	}}
	out := RenderSections(sectionsRelease(), f)
	for _, want := range []string{"- close leak", "- drop old API", "- add streaming", "- utils: 1.1.0 -> 1.2.0"} {
		assert.Contains(t, out, want)
	}
}

func TestCustomSectionClaimsItsTypes(t *testing.T) {
	rel := &plan.Release{
		Pkg:  &model.Package{Name: "core", Dir: "/tmp/x", Space: &model.Space{Name: "libs"}},
		Next: ccme.Version{Major: 1, Minor: 1},
		Units: []*ccme.Unit{
			testUnit("add", ccme.BumpMinor, "claimed"),
			testUnit("feat", ccme.BumpMinor, "unclaimed"),
		},
	}
	f := Format{Sections: []model.RecordSection{
		{Title: "Added", Types: []string{"add"}},
		{Builtin: model.SectionBreaking},
		{Builtin: model.SectionFeatures},
		{Builtin: model.SectionFixes},
		{Builtin: model.SectionDependencies},
	}}
	assert.Equal(t, "### Added\n\n- claimed\n\n### Features\n\n- unclaimed\n", RenderSections(rel, f))
}

func TestBreakingWinsOverACustomClaim(t *testing.T) {
	// A change that breaks its consumers is the thing a reader scans an entry
	// for. Letting `add(x)!: ...` render under the word its author chose for
	// ordinary work would hide it.
	rel := &plan.Release{
		Pkg:  &model.Package{Name: "core", Dir: "/tmp/x", Space: &model.Space{Name: "libs"}},
		Next: ccme.Version{Major: 2},
		Units: []*ccme.Unit{
			testUnit("add", ccme.BumpMajor, "breaking addition"),
			testUnit("add", ccme.BumpMinor, "ordinary addition"),
		},
	}
	f := Format{Sections: []model.RecordSection{
		{Title: "Added", Types: []string{"add"}},
		{Builtin: model.SectionBreaking},
		{Builtin: model.SectionFeatures},
		{Builtin: model.SectionFixes},
		{Builtin: model.SectionDependencies},
	}}
	assert.Equal(t, "### Added\n\n- ordinary addition\n\n### Breaking Changes\n\n- breaking addition\n",
		RenderSections(rel, f))
}

func TestCommitRefSuffix(t *testing.T) {
	sha := "0123456789abcdef0123456789abcdef01234567"
	unit := testUnit("feat", ccme.BumpMinor, "add streaming")
	rel := &plan.Release{
		Pkg:         &model.Package{Name: "core", Dir: "/tmp/x", Space: &model.Space{Name: "libs"}},
		Next:        ccme.Version{Major: 1, Minor: 1},
		Units:       []*ccme.Unit{unit},
		UnitCommits: map[*ccme.Unit]string{unit: sha},
	}
	for name, tc := range map[string]struct {
		f    Format
		want string
	}{
		"off by default": {
			Format{}, "### Features\n\n- add streaming\n"},
		"suffix, unlinked": {
			Format{CommitRefsPlacement: RefsSuffix},
			"### Features\n\n- add streaming (0123456)\n"},
		"suffix, auto": {
			Format{CommitRefsPlacement: RefsSuffix, CommitRefsLink: model.LinkAuto,
				LinkOwner: "acme", LinkRepo: "tools"},
			"### Features\n\n- add streaming ([0123456](https://github.com/acme/tools/commit/" + sha + "))\n"},
		"auto with no coordinates falls back to plain text": {
			Format{CommitRefsPlacement: RefsSuffix, CommitRefsLink: model.LinkAuto},
			"### Features\n\n- add streaming (0123456)\n"},
		"auto declines a github enterprise endpoint": {
			Format{CommitRefsPlacement: RefsSuffix, CommitRefsLink: model.LinkAuto,
				LinkOwner: "acme", LinkRepo: "tools", LinkAPIURL: "https://git.acme.com/api/v3"},
			"### Features\n\n- add streaming (0123456)\n"},
		"a template wins over auto and sees the full sha": {
			Format{CommitRefsPlacement: RefsSuffix, CommitRefsFormat: "$DISPAT_COMMIT",
				CommitRefsLink: "https://git.acme.com/c/$DISPAT_COMMIT",
				LinkOwner:      "acme", LinkRepo: "tools"},
			"### Features\n\n- add streaming ([" + sha + "](https://git.acme.com/c/" + sha + "))\n"},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, RenderSections(rel, tc.f))
		})
	}

	t.Run("a synthetic window key is never presented as a commit", func(t *testing.T) {
		// The key stands in for a sha a Git implementation did not report. It
		// names an entry in this run's window and nothing a reader could open,
		// so the line renders unreferenced rather than with a dead reference.
		rel.UnitCommits = map[*ccme.Unit]string{unit: "msg:feat: add streaming"}
		assert.Equal(t, "### Features\n\n- add streaming\n",
			RenderSections(rel, Format{CommitRefsPlacement: RefsSuffix}))
	})
}

func TestDependencyLink(t *testing.T) {
	rel := &plan.Release{
		Pkg:  &model.Package{Name: "core", Dir: "/tmp/x", Space: &model.Space{Name: "libs"}},
		Next: ccme.Version{Major: 1, Minor: 1},
		Updates: []plan.ProviderUpdate{{
			Name: "utils",
			From: ccme.Version{Major: 1, Minor: 1},
			To:   ccme.Version{Major: 1, Minor: 2},
			Tag:  "utils@1.2.0",
		}},
	}
	for name, tc := range map[string]struct {
		f    Format
		want string
	}{
		"plain by default": {
			Format{}, "### Dependencies\n\n- utils: 1.1.0 -> 1.2.0\n"},
		"auto": {
			Format{DependencyLink: model.LinkAuto, LinkOwner: "acme", LinkRepo: "tools"},
			"### Dependencies\n\n- [utils](https://github.com/acme/tools/releases/tag/utils@1.2.0): 1.1.0 -> 1.2.0\n"},
		"auto with no coordinates falls back to the plain line": {
			Format{DependencyLink: model.LinkAuto},
			"### Dependencies\n\n- utils: 1.1.0 -> 1.2.0\n"},
		"a template wins over auto": {
			Format{DependencyLink: "https://pkg.example/$DISPAT_DEP_NAME/$DISPAT_DEP_TO",
				LinkOwner: "acme", LinkRepo: "tools"},
			"### Dependencies\n\n- [utils](https://pkg.example/utils/1.2.0): 1.1.0 -> 1.2.0\n"},
		"a template expanding to nothing falls back too": {
			Format{DependencyLink: "$NOTHING_DEFINES_THIS_NAME"},
			"### Dependencies\n\n- utils: 1.1.0 -> 1.2.0\n"},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, RenderSections(rel, tc.f))
		})
	}
}

func TestDependenciesStayATightList(t *testing.T) {
	// The dependencies section is a table of movements: its lines never carry
	// bodies, and every changelog written so far renders them without blank
	// lines between. The loose joining the other sections use for their
	// bullet-and-body items does not apply here.
	rel := &plan.Release{
		Pkg:  &model.Package{Name: "core", Dir: "/tmp/x", Space: &model.Space{Name: "libs"}},
		Next: ccme.Version{Major: 1, Minor: 1},
		Updates: []plan.ProviderUpdate{
			{Name: "utils", From: ccme.Version{Major: 1}, To: ccme.Version{Major: 1, Minor: 1}, Tag: "utils@1.1.0"},
			{Name: "models", From: ccme.Version{Major: 2}, To: ccme.Version{Major: 2, Minor: 1}, Tag: "models@2.1.0"},
		},
	}
	assert.Equal(t,
		"### Dependencies\n\n- utils: 1.0.0 -> 1.1.0\n- models: 2.0.0 -> 2.1.0\n",
		RenderSections(rel, Format{}))
}

func TestAutoLinksReadTheRepositoryFromTheEnvironment(t *testing.T) {
	// The ordinary CI setup states the repository nowhere in the config file:
	// the workflow runs in it, and the GitHub releaser resolves it from
	// $GITHUB_REPOSITORY. The record's own links resolve the same way, or
	// "auto" would silently do nothing in exactly the setup it is meant for.
	t.Setenv("GITHUB_REPOSITORY", "acme/tools")
	rel := &plan.Release{
		Pkg:     &model.Package{Name: "core", Dir: "/tmp/x", Space: &model.Space{Name: "libs"}},
		Next:    ccme.Version{Major: 1, Minor: 1},
		Updates: []plan.ProviderUpdate{{Name: "utils", To: ccme.Version{Major: 1}, Tag: "utils@1.0.0"}},
	}
	assert.Contains(t, RenderSections(rel, Format{DependencyLink: model.LinkAuto}),
		"https://github.com/acme/tools/releases/tag/utils@1.0.0")

	// Configuration wins: a package that names its repository is not
	// overruled by the workflow it happens to run in.
	assert.Contains(t,
		RenderSections(rel, Format{DependencyLink: model.LinkAuto, LinkOwner: "other", LinkRepo: "repo"}),
		"https://github.com/other/repo/releases/tag/utils@1.0.0")
}

func TestNoChangesTextReplacesTheBuiltinSentence(t *testing.T) {
	rel := &plan.Release{
		Pkg:    &model.Package{Name: "core", Dir: "/tmp/x", Space: &model.Space{Name: "libs"}},
		Next:   ccme.Version{Major: 2},
		Pinned: true,
	}
	assert.Equal(t, "see the dispat changelog for core@2.0.0.\n",
		RenderSections(rel, Format{NoChangesText: "see the dispat changelog for $DISPAT_TAG."}))

	// An expansion that comes out empty is a mistake in the template rather
	// than an instruction to publish an empty entry, so the built-in stands.
	assert.Equal(t, "No changes: a version set by Release-As.\n",
		RenderSections(rel, Format{NoChangesText: "$NOTHING_DEFINES_THIS_NAME"}))
}

func TestLogRecordPolicyWarnsAboutUnavailableCommitRefs(t *testing.T) {
	unit := testUnit("feat", ccme.BumpMinor, "add streaming")
	rel := &plan.Release{
		Pkg:         &model.Package{Name: "core", Dir: "/tmp/x", Space: &model.Space{Name: "libs"}},
		Next:        ccme.Version{Major: 1, Minor: 1},
		Units:       []*ccme.Unit{unit},
		UnitCommits: map[*ccme.Unit]string{unit: "msg:feat: add streaming"},
	}
	var buf strings.Builder
	log := zerolog.New(&buf).Level(zerolog.DebugLevel)
	LogRecordPolicy(log, rel, Format{CommitRefsPlacement: RefsSuffix, CommitRefsLink: model.LinkAuto})

	out := buf.String()
	assert.Contains(t, out, plan.CodeCommitRefUnavailable)
	assert.Contains(t, out, `"units":1`)
	assert.Contains(t, out, "record links fall back to plain text")
}

func TestFileWriterEntrySpacing(t *testing.T) {
	// The seam between two entries is exactly the configured number of blank
	// lines, whatever the entry above it ended with. It used to vary: an entry
	// closing on a dependencies list left one blank line and one closing on a
	// section of bodiless bullets left two, so a file recorded the shape of
	// each release rather than one rule.
	write := func(t *testing.T, spacing int) string {
		t.Helper()
		dir := t.TempDir()
		w := &FileWriter{EntrySpacing: spacing, Now: func() time.Time { return testDate }}
		ctx := context.Background()
		require.NoError(t, w.Record(ctx, testRelease(dir, ccme.Version{Major: 2})))
		require.NoError(t, w.Record(ctx, testRelease(dir, ccme.Version{Major: 2, Minor: 1})))
		data, err := os.ReadFile(filepath.Join(dir, "CHANGELOG.md"))
		require.NoError(t, err)
		return string(data)
	}

	def := write(t, 0)
	assert.Contains(t, def, "- utils: 1.1.0 -> 1.2.0\n\n\n## core@2.0.0 (",
		"the default seam is two blank lines")
	assert.True(t, strings.HasPrefix(def, "# Changelog\n\n## core@2.1.0 ("),
		"the title's own seam is unchanged: %q", def)

	one := write(t, 1)
	assert.Contains(t, one, "- utils: 1.1.0 -> 1.2.0\n\n## core@2.0.0 (")
	assert.NotContains(t, one, "\n\n\n")

	assert.Contains(t, write(t, 4), "- utils: 1.1.0 -> 1.2.0\n\n\n\n\n## core@2.0.0 (")
}

func TestFileWriterRewriteIsByteIdentical(t *testing.T) {
	// The normalised seam must not make a re-run rewrite what it already
	// wrote: the entry-exists skip is what guarantees it, and the spacing
	// change is exactly the kind of edit that would slip past it.
	dir := t.TempDir()
	w := &FileWriter{Now: func() time.Time { return testDate }}
	ctx := context.Background()
	require.NoError(t, w.Record(ctx, testRelease(dir, ccme.Version{Major: 2})))
	require.NoError(t, w.Record(ctx, testRelease(dir, ccme.Version{Major: 2, Minor: 1})))
	first, err := os.ReadFile(filepath.Join(dir, "CHANGELOG.md"))
	require.NoError(t, err)

	require.NoError(t, w.Record(ctx, testRelease(dir, ccme.Version{Major: 2, Minor: 1})))
	second, err := os.ReadFile(filepath.Join(dir, "CHANGELOG.md"))
	require.NoError(t, err)
	assert.Equal(t, string(first), string(second))
}

func TestRenderEntryCarriesNoEntrySeam(t *testing.T) {
	// The seam belongs to the file, not to the entry: a GitHub release body
	// and `dispat changelog --preview` render the same entry with nothing
	// after it.
	out := RenderEntry(testRelease("/tmp/x", ccme.Version{Major: 2}), testDate, Format{})
	assert.True(t, strings.HasSuffix(out, "- utils: 1.1.0 -> 1.2.0\n"), "%q", out)
}
