package config

// Unit tests of the record entry-format keys added beside `authors`: the
// section order, the dependency and commit links, the no-changes sentence and
// the entry spacing. The ladder that folds them, the mapping onto the resolved
// policy, and the values they refuse.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/ccme/v2"
	lib "github.com/yohimik/dispat/pkg/config"
	"github.com/yohimik/dispat/pkg/models/v2"

	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// --- decode ----------------------------------------------------------------

func TestSectionsTakeTheBareStringShorthand(t *testing.T) {
	// Reordering the built-ins is the common case, and it needs no objects at
	// all: a bare string in the list names a built-in by its key.
	cfg, err := decodeRoot(t, map[string]any{
		"changelog": map[string]any{"sections": []any{"fixes", "features"}},
	})
	require.NoError(t, err)
	assert.Equal(t, []SectionConfig{{Title: "fixes"}, {Title: "features"}}, cfg.Changelog.Sections)

	// A whole list written as one string is the one-element list it stands
	// for, exactly as a record line is.
	cfg, err = decodeRoot(t, map[string]any{"changelog": map[string]any{"sections": "fixes"}})
	require.NoError(t, err)
	assert.Equal(t, []SectionConfig{{Title: "fixes"}}, cfg.Changelog.Sections)
}

func TestSectionsTakeTheObjectForm(t *testing.T) {
	cfg, err := decodeRoot(t, map[string]any{
		"github": map[string]any{"sections": []any{
			map[string]any{"title": "Added", "types": []any{"add", "new"}, "bump": "minor"},
			"features",
		}},
	})
	require.NoError(t, err)
	assert.Equal(t, []SectionConfig{
		{Title: "Added", Types: []string{"add", "new"}, Bump: "minor"},
		{Title: "features"},
	}, cfg.GitHub.Sections)
}

func TestSectionsRefuseAnElementThatIsNeitherShape(t *testing.T) {
	_, err := decodeRoot(t, map[string]any{
		"changelog": map[string]any{"sections": []any{[]any{"fixes"}}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sections[0]")
}

func TestEntrySpacingIsTriState(t *testing.T) {
	// nil is "the layer says nothing", which is what lets a broader layer's
	// value survive. A written 1 is a value, and must not read as absence.
	cfg, err := decodeRoot(t, map[string]any{"changelog": map[string]any{"file": "HISTORY.md"}})
	require.NoError(t, err)
	require.NotNil(t, cfg.Changelog)
	assert.Nil(t, cfg.Changelog.EntrySpacing)
	assert.Equal(t, models.DefaultEntrySpacing, cfg.Changelog.EntrySpacingOrDefault())

	cfg, err = decodeRoot(t, map[string]any{"changelog": map[string]any{"entrySpacing": 1}})
	require.NoError(t, err)
	require.NotNil(t, cfg.Changelog.EntrySpacing)
	assert.Equal(t, 1, *cfg.Changelog.EntrySpacing)
	assert.Equal(t, 1, cfg.Changelog.EntrySpacingOrDefault())
}

func TestCommitRefsDecodeAsAnObject(t *testing.T) {
	cfg, err := decodeRoot(t, map[string]any{
		"changelog": map[string]any{"commitRefs": map[string]any{
			"placement": "suffix", "link": "auto", "format": "$DISPAT_COMMIT",
		}},
	})
	require.NoError(t, err)
	require.NotNil(t, cfg.Changelog.CommitRefs)
	assert.Equal(t, "suffix", cfg.Changelog.CommitRefs.Placement)
	assert.Equal(t, "auto", cfg.Changelog.CommitRefs.Link)
	assert.Equal(t, "$DISPAT_COMMIT", cfg.Changelog.CommitRefs.Format)
}

// --- overlay ---------------------------------------------------------------

func TestOverlayCommitRefsFieldByField(t *testing.T) {
	base := &CommitRefsConfig{Placement: "suffix", Format: "$DISPAT_COMMIT_SHORT", Link: "auto"}
	assert.Equal(t, base, overlayCommitRefs(base, nil), "a layer that says nothing changes nothing")

	got := overlayCommitRefs(base, &CommitRefsConfig{Link: "https://example/$DISPAT_COMMIT"})
	assert.Equal(t, "https://example/$DISPAT_COMMIT", got.Link)
	assert.Equal(t, "suffix", got.Placement, "the other two inherit")
	assert.Equal(t, "auto", base.Link, "the base is not mutated")

	// "off" is spelled out as a placement precisely so a package can switch
	// off what its space turned on.
	assert.Equal(t, "off", overlayCommitRefs(base, &CommitRefsConfig{Placement: "off"}).Placement)
}

func TestOverlayFormatCarriesTheRecordLinkKeys(t *testing.T) {
	base := EntryFormatConfig{
		DependencyLink: "auto",
		NoChangesText:  "nothing changed",
		Sections:       []SectionConfig{{Title: "fixes"}},
		CommitRefs:     &CommitRefsConfig{Placement: "suffix"},
	}
	same := overlayFormat(base, EntryFormatConfig{})
	assert.Equal(t, "auto", same.DependencyLink)
	assert.Equal(t, "nothing changed", same.NoChangesText)
	assert.Equal(t, []SectionConfig{{Title: "fixes"}}, same.Sections)
	assert.Equal(t, "suffix", same.CommitRefs.Placement)

	over := overlayFormat(base, EntryFormatConfig{
		DependencyLink: "https://example/$DISPAT_DEP_TAG",
		Sections:       []SectionConfig{{Title: "features"}},
	})
	assert.Equal(t, "https://example/$DISPAT_DEP_TAG", over.DependencyLink)
	assert.Equal(t, []SectionConfig{{Title: "features"}}, over.Sections,
		"a section order replaces whole: appending could never move a section earlier")
	assert.Equal(t, "nothing changed", over.NoChangesText, "what the layer did not state is inherited")
}

func TestOverlayChangelogCarriesEntrySpacing(t *testing.T) {
	base := &ChangelogConfig{EntrySpacing: models.Int(3)}
	assert.Equal(t, 3, overlayChangelog(base, &ChangelogConfig{}).EntrySpacingOrDefault(),
		"a layer that says nothing about spacing inherits it")
	assert.Equal(t, 1, overlayChangelog(base, &ChangelogConfig{EntrySpacing: models.Int(1)}).EntrySpacingOrDefault())
}

// --- resolution ------------------------------------------------------------

func TestRecordFormatFlattensTheCommitRefsObject(t *testing.T) {
	got := recordFormat(EntryFormatConfig{
		DependencyLink: "auto",
		NoChangesText:  "see the changelog",
		CommitRefs:     &CommitRefsConfig{Placement: "suffix", Format: "$DISPAT_COMMIT", Link: "auto"},
	}, &GitHubConfig{Owner: "acme", Repo: "tools", APIURL: "https://api.github.com"})

	assert.Equal(t, "auto", got.DependencyLink)
	assert.Equal(t, "see the changelog", got.NoChangesText)
	assert.Equal(t, "suffix", got.CommitRefsPlacement)
	assert.Equal(t, "$DISPAT_COMMIT", got.CommitRefsFormat)
	assert.Equal(t, "auto", got.CommitRefsLink)

	// The forge coordinates travel onto the format so the renderer needs no
	// configuration of its own, and the changelog destination borrows the
	// package's github ones because a file has none.
	assert.Equal(t, "acme", got.LinkOwner)
	assert.Equal(t, "tools", got.LinkRepo)
	assert.Equal(t, "https://api.github.com", got.LinkAPIURL)

	empty := recordFormat(EntryFormatConfig{}, nil)
	assert.Empty(t, empty.CommitRefsPlacement)
	assert.Empty(t, empty.Sections)
	assert.Empty(t, empty.LinkOwner)
}

func TestResolveSectionsAppendsTheBuiltinsTheListOmitted(t *testing.T) {
	// A `sections` list orders sections; it never drops one. A built-in left
	// out is appended after the listed ones, in the default relative order, so
	// released work cannot fall out of the record by omission.
	got, err := resolveSections("changelog", []SectionConfig{
		{Title: "Added", Types: []string{"Add"}, Bump: "minor"},
		{Title: "Fixes"},
	})
	require.NoError(t, err)
	assert.Equal(t, []model.RecordSection{
		{Title: "Added", Types: []string{"add"}, Bump: "minor"},
		{Builtin: model.SectionFixes},
		{Builtin: model.SectionBreaking},
		{Builtin: model.SectionFeatures},
		{Builtin: model.SectionDependencies},
	}, got)

	none, err := resolveSections("changelog", nil)
	require.NoError(t, err)
	assert.Nil(t, none, "an unconfigured order stays unconfigured, which is the renderer's own default")
}

func TestResolveSectionsRefusesWhatCannotRender(t *testing.T) {
	for name, tc := range map[string]struct {
		list []SectionConfig
		want string
	}{
		"an unknown built-in": {
			[]SectionConfig{{Title: "changes"}}, "is not a built-in section"},
		"a built-in listed twice": {
			[]SectionConfig{{Title: "fixes"}, {Title: "Fixes"}}, "listed twice"},
		"a custom section with no title": {
			[]SectionConfig{{Types: []string{"add"}}}, "title is required"},
		"a type claimed twice in one destination": {
			[]SectionConfig{
				{Title: "Added", Types: []string{"add"}},
				{Title: "New", Types: []string{"Add"}},
			}, "already claimed by sections[0]"},
		"an empty type": {
			[]SectionConfig{{Title: "Added", Types: []string{" "}}}, "must not be empty"},
		"an unknown bump": {
			[]SectionConfig{{Title: "Added", Types: []string{"add"}, Bump: "huge"}}, "unknown value"},
		"a bump on a built-in": {
			[]SectionConfig{{Title: "fixes", Bump: "minor"}}, "bump belongs to a custom section"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := resolveSections("changelog", tc.list)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

// --- the parser fold -------------------------------------------------------

// sectionsConfig is a minimal config whose root changelog declares one custom
// section, which is what the parser-fold tests vary.
func sectionsConfig(sections []SectionConfig, types map[string]string) File {
	cfg := minimalConfig()
	cfg.Changelog = &ChangelogConfig{EntryFormatConfig: EntryFormatConfig{Sections: sections}}
	if types != nil {
		cfg.Parser = &ParserConfig{Types: types}
	}
	return cfg
}

func TestSectionBumpMergesIntoTheParserTypeTable(t *testing.T) {
	// Declaring the section that renders a type is enough to make commits of
	// that type release at all, and the specification's own table survives:
	// a non-nil Types replaces ccme's wholesale, so the fold starts from a
	// copy of the defaults rather than from nothing.
	loaded, err := loadModel(t, sectionsConfig(
		[]SectionConfig{{Title: "Added", Types: []string{"Add"}, Bump: "minor"}}, nil), "pkgs/core")
	require.NoError(t, err)
	assert.Equal(t, ccme.BumpMinor, loaded.ResolvedParser.Types["add"])
	assert.NotContains(t, loaded.ResolvedParser.Types, "Add", "the parser matches a type in lower case")
	assert.Equal(t, ccme.BumpMinor, loaded.ResolvedParser.Types["feat"], "the built-in table survives the fold")
	assert.Equal(t, ccme.BumpPatch, loaded.ResolvedParser.Types["fix"])
}

func TestSectionBumpAgreesWithParserTypes(t *testing.T) {
	// Restating the same bump is how one package's two destinations are kept
	// in step, so agreement is fine and only disagreement is refused.
	_, err := loadModel(t, sectionsConfig(
		[]SectionConfig{{Title: "Added", Types: []string{"add"}, Bump: "minor"}},
		map[string]string{"add": "minor"}), "pkgs/core")
	require.NoError(t, err)

	_, err = loadModel(t, sectionsConfig(
		[]SectionConfig{{Title: "Added", Types: []string{"add"}, Bump: "major"}},
		map[string]string{"add": "minor"}), "pkgs/core")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `the commit type "add"`)
	assert.Contains(t, err.Error(), "parser.types")
}

func TestSectionBumpConflictsAcrossPackages(t *testing.T) {
	// The parser is one table for the whole repository while sections are per
	// package and per destination, so two layers giving one type different
	// bumps is a repository-wide contradiction rather than a local override.
	cfg := minimalConfig()
	cfg.Changelog = &ChangelogConfig{EntryFormatConfig: EntryFormatConfig{
		Sections: []SectionConfig{{Title: "Added", Types: []string{"add"}, Bump: "minor"}}}}
	cfg.Packages = map[string]PackageConfig{"core": {GitHub: &GitHubConfig{
		EntryFormatConfig: EntryFormatConfig{
			Sections: []SectionConfig{{Title: "Added", Types: []string{"add"}, Bump: "patch"}}}}}}

	_, err := loadModel(t, cfg, "pkgs/core")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `packages["core"]`)
	assert.Contains(t, err.Error(), "one bump for the whole repository")
}

func TestSectionBumpOverridesASpecificationDefaultWithoutComplaint(t *testing.T) {
	// `docs` is `none` in the specification's table. A section that renders it
	// as a patch is a deliberate override rather than a contradiction: only
	// what `parser.types` and other sections explicitly stated is held to
	// agreement.
	loaded, err := loadModel(t, sectionsConfig(
		[]SectionConfig{{Title: "Documentation", Types: []string{"docs"}, Bump: "patch"}}, nil), "pkgs/core")
	require.NoError(t, err)
	assert.Equal(t, ccme.BumpPatch, loaded.ResolvedParser.Types["docs"])
}

func TestFolderConfigRefusesASectionBump(t *testing.T) {
	// The parser is built once, from the root file, before any in-folder file
	// is read. A bump declared in one would render its section and never make
	// the type releasable, so it is refused where it says what to do instead.
	cfg := minimalConfig()
	root := writeModelRepo(t, cfg, "pkgs/core")
	writeFolderConfig(t, root, filepath.Join("pkgs", "core"), "dispat.json", map[string]any{
		"changelog": map[string]any{"sections": []any{
			map[string]any{"title": "Added", "types": []any{"add"}, "bump": "minor"},
		}},
	})
	loaded, err := Load(filepath.Join(root, "dispat.json"), nil)
	require.NoError(t, err)
	_, _, _, err = Discover(loaded, root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bump cannot be set in a folder's own config file")

	// The section itself still works from a folder file; only the bump is
	// refused.
	writeFolderConfig(t, root, filepath.Join("pkgs", "core"), "dispat.json", map[string]any{
		"changelog": map[string]any{"sections": []any{
			map[string]any{"title": "Added", "types": []any{"add"}},
		}},
	})
	pkgs, _, _, err := Discover(loaded, root)
	require.NoError(t, err)
	require.Len(t, pkgs, 1)
	assert.Equal(t, "Added", pkgs[0].Changelog.Format.Sections[0].Title)
}

// --- validation ------------------------------------------------------------

func TestEntrySpacingBounds(t *testing.T) {
	for _, n := range []int{0, 11, -1} {
		cfg := minimalConfig()
		cfg.Changelog = &ChangelogConfig{EntrySpacing: models.Int(n)}
		_, err := loadModel(t, cfg, "pkgs/core")
		require.Error(t, err, "entrySpacing %d", n)
		assert.Contains(t, err.Error(), "entrySpacing must be between 1 and 10")
	}
	cfg := minimalConfig()
	cfg.Changelog = &ChangelogConfig{EntrySpacing: models.Int(10)}
	_, err := loadModel(t, cfg, "pkgs/core")
	require.NoError(t, err)
}

func TestCommitRefsPlacementIsAClosedSet(t *testing.T) {
	cfg := minimalConfig()
	cfg.Changelog = &ChangelogConfig{EntryFormatConfig: EntryFormatConfig{
		CommitRefs: &CommitRefsConfig{Placement: "everywhere"}}}
	_, err := loadModel(t, cfg, "pkgs/core")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "commitRefs.placement")
	assert.Contains(t, err.Error(), "off, suffix")
}

func TestNoChangesTextMustNotOpenTheSelfUpdateCut(t *testing.T) {
	// `dispat self-update` reads a release's notes by cutting at the "---" a
	// footer conventionally opens with. A sentence beginning there would never
	// be shown, which is a failure nobody would look for in a changelog key.
	cfg := minimalConfig()
	cfg.GitHub = &GitHubConfig{EntryFormatConfig: EntryFormatConfig{NoChangesText: "--- nothing changed"}}
	_, err := loadModel(t, cfg, "pkgs/core")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "noChangesText must not begin")
}

// TestNoChangesTextMustNotCarryAnInteriorRule: the cut takes everything from
// the first rule down, not only a text that opens on one. A sentence with a
// rule in the middle of it loses its second half after an update, and a
// multi-line sentence — a line and the link under it — is an ordinary thing to
// write.
func TestNoChangesTextMustNotCarryAnInteriorRule(t *testing.T) {
	for name, text := range map[string]string{
		"a rule after a blank line": "Released with the group.\n\n---\nSee the monorepo changelog.",
		"a rule of asterisks":       "Released with the group.\n***\nSee the monorepo changelog.",
		"a spaced rule":             "Released with the group.\n- - -\nSee the monorepo changelog.",
		"a rule of underscores":     "Released with the group.\n____\nSee the monorepo changelog.",
	} {
		t.Run(name, func(t *testing.T) {
			cfg := minimalConfig()
			cfg.GitHub = &GitHubConfig{EntryFormatConfig: EntryFormatConfig{NoChangesText: text}}
			_, err := loadModel(t, cfg, "pkgs/core")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "noChangesText must not contain the horizontal rule")
		})
	}

	// A sentence that merely mentions the characters is not a rule: the line
	// has to be nothing else.
	cfg := minimalConfig()
	cfg.GitHub = &GitHubConfig{EntryFormatConfig: EntryFormatConfig{
		NoChangesText: "Released with the group.\nSee core -> utils in the monorepo changelog.",
	}}
	_, err := loadModel(t, cfg, "pkgs/core")
	assert.NoError(t, err)
}

// TestOverlayFormatTurnsAnInheritedLinkOff: a nearer layer states "off" to
// render the plain line a repository configuring nothing renders. An omitted
// key inherits, so without the written value a package under a space that
// turned linking on could never turn it off again.
func TestOverlayFormatTurnsAnInheritedLinkOff(t *testing.T) {
	space := EntryFormatConfig{
		DependencyLink: "auto",
		CommitRefs:     &CommitRefsConfig{Placement: "suffix", Link: "auto"},
	}
	pkg := overlayFormat(space, EntryFormatConfig{
		DependencyLink: "off",
		CommitRefs:     &CommitRefsConfig{Link: "off"},
	})
	assert.Equal(t, "off", pkg.DependencyLink)
	assert.Equal(t, "off", pkg.CommitRefs.Link)
	assert.Equal(t, "suffix", pkg.CommitRefs.Placement,
		"the reference itself stays: placement is what removes it")
	assert.Equal(t, "auto", space.DependencyLink, "the space is not mutated")

	got := recordFormat(pkg, &GitHubConfig{Owner: "acme", Repo: "tools"})
	assert.Equal(t, model.LinkOff, got.DependencyLink, "the value reaches the renderer as written")
	assert.Equal(t, model.LinkOff, got.CommitRefsLink)
}

// --- the resolved spec -----------------------------------------------------

func TestChangelogSpecResolvesSpacingAndForgeCoordinates(t *testing.T) {
	cfg := minimalConfig()
	cfg.GitHub = &GitHubConfig{Owner: "acme", Repo: "tools"}
	cfg.Changelog = &ChangelogConfig{
		EntrySpacing:      models.Int(1),
		EntryFormatConfig: EntryFormatConfig{DependencyLink: "auto"},
	}
	loaded, err := loadModel(t, cfg, "pkgs/core")
	require.NoError(t, err)
	root := filepath.Dir(loaded.SourceFiles[0])
	pkgs, _, _, err := Discover(loaded, root)
	require.NoError(t, err)
	require.Len(t, pkgs, 1)

	assert.Equal(t, 1, pkgs[0].Changelog.EntrySpacing)
	assert.Equal(t, "acme", pkgs[0].Changelog.Format.LinkOwner,
		"the changelog borrows the package's github coordinates: a file has none of its own")
	assert.Equal(t, "tools", pkgs[0].Changelog.Format.LinkRepo)
	assert.Equal(t, "acme", pkgs[0].GitHub.Format.LinkOwner)
}

func TestChangelogSpecDefaultsTheSpacing(t *testing.T) {
	loaded, err := loadModel(t, minimalConfig(), "pkgs/core")
	require.NoError(t, err)
	root := filepath.Dir(loaded.SourceFiles[0])
	pkgs, _, _, err := Discover(loaded, root)
	require.NoError(t, err)
	assert.Equal(t, models.DefaultEntrySpacing, pkgs[0].Changelog.EntrySpacing)
}

// --- the unknown-key hint --------------------------------------------------

func TestUnknownKeyPointsAtSelfUpdate(t *testing.T) {
	// A key with no field is usually a typo, and the loader says so. The other
	// cause it cannot know about is a config written for a newer dispat than
	// the one reading it, so dispat's own wrapper names the command that
	// checks.
	root := writeRawRepo(t, map[string]any{
		"spaces":        map[string]any{"libs": map[string]any{"path": "pkgs"}},
		"aKeyNobodyHas": true,
	}, "pkgs/core")
	_, err := Load(filepath.Join(root, "dispat.json"), nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, lib.ErrUnknownKey, "the wrapper must not hide what it wraps")
	assert.Contains(t, err.Error(), "newer config schema than this dispat")
	assert.Contains(t, err.Error(), "dispat self-update --check")
}

func TestOtherConfigErrorsCarryNoSchemaHint(t *testing.T) {
	root := writeRawRepo(t, map[string]any{
		"spaces":      map[string]any{"libs": map[string]any{"path": "pkgs"}},
		"concurrency": "not a number",
	}, "pkgs/core")
	_, err := Load(filepath.Join(root, "dispat.json"), nil)
	require.Error(t, err)
	assert.False(t, strings.Contains(err.Error(), "self-update"),
		"only an unknown key is a candidate for a newer schema: %v", err)
}
