package app

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/ccme"
	"github.com/yohimik/dispat/pkg/scanner"

	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/filter"
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// The baseline half of compute, taken apart. Which version a package's
// manifests agree it is at, and which packages are still missing an entry,
// are decided before any git runs, so they are testable on their own; the
// tag reading itself is exercised through the compiled binary in the
// integration suite, against real tags.

// versioned is one manifest with a declared version, at the rank the caller
// asks for.
func versioned(path, version string, root bool) scanner.Manifest {
	return scanner.Manifest{Path: path, Ecosystem: scanner.EcosystemNpm, Version: version, Root: root}
}

// baselineOf runs pickManifestVersion over one package's manifests.
func baselineOf(t *testing.T, a *App, root string, mans ...scanner.Manifest) (manifestBaseline, bool) {
	t.Helper()
	pkg := &model.Package{Name: "core", Dir: filepath.Join(root, "packages", "core")}
	return a.pickManifestVersion(scannedPackage{pkg: pkg, mans: mans})
}

func TestPickManifestVersionRanksRootAboveNested(t *testing.T) {
	// The rank scanner.NameIndex applies to names applies to versions too: a
	// package's own manifest beats an example or vendored one deeper inside
	// it, and a nested manifest is read only when no root one has an answer.
	root, _, a := computeRepo(t, libsConfig(), zerolog.Nop())

	got, ok := baselineOf(t, a, root,
		versioned("examples/demo/package.json", "9.9.9", false),
		versioned("package.json", "1.4.2", true))
	require.True(t, ok)
	assert.Equal(t, "1.4.2", got.version.String())
	assert.Equal(t, "packages/core/package.json", got.manifest, "the evidence names the declaring file")

	got, ok = baselineOf(t, a, root, versioned("ios/Runner/Info.plist", "3.1.0", false))
	require.True(t, ok, "a package whose only versioned manifest is nested still has an answer")
	assert.Equal(t, "3.1.0", got.version.String())
}

func TestPickManifestVersionSkipsWhatCannotSeedABaseline(t *testing.T) {
	root, _, a := computeRepo(t, libsConfig(), zerolog.Nop())

	t.Run("no version anywhere", func(t *testing.T) {
		_, ok := baselineOf(t, a, root, versioned("package.json", "", true), versioned("go.mod", "", true))
		assert.False(t, ok)
	})

	t.Run("not a semantic version", func(t *testing.T) {
		_, ok := baselineOf(t, a, root, versioned("pom.xml", "2023.04", true))
		assert.False(t, ok)
	})

	t.Run("prerelease", func(t *testing.T) {
		// A version being worked toward, not one released: the baseline stays
		// where it is.
		_, ok := baselineOf(t, a, root, versioned("pom.xml", "1.0.0-SNAPSHOT", true))
		assert.False(t, ok)
	})

	t.Run("zero", func(t *testing.T) {
		// 0.0.0 is what a package with no entry already starts from.
		_, ok := baselineOf(t, a, root, versioned("package.json", "0.0.0", true))
		assert.False(t, ok)
	})

	t.Run("unparseable beside a good one", func(t *testing.T) {
		// The bad one is passed over rather than making the pair ambiguous.
		got, ok := baselineOf(t, a, root,
			versioned("Cargo.toml", "not-a-version", true),
			versioned("package.json", "1.4.2", true))
		require.True(t, ok)
		assert.Equal(t, "1.4.2", got.version.String())
	})

	t.Run("build metadata is not a disagreement", func(t *testing.T) {
		got, ok := baselineOf(t, a, root,
			versioned("Cargo.toml", "1.4.2+build.7", true),
			versioned("package.json", "1.4.2", true))
		require.True(t, ok)
		assert.Equal(t, "1.4.2", got.version.String(), "build metadata is dropped from a baseline")
	})
}

func TestPickManifestVersionReportsDisagreement(t *testing.T) {
	// Two root manifests, two versions: which one the package is at is a
	// question the files disagree about, so compute derives nothing and says
	// so as W225.
	var logs bytes.Buffer
	root, _, a := computeRepo(t, libsConfig(), zerolog.New(&logs))

	_, ok := baselineOf(t, a, root,
		versioned("Cargo.toml", "1.2.4", true),
		versioned("package.json", "1.2.3", true))
	assert.False(t, ok)
	assert.Contains(t, logs.String(), `"code":"W225"`)
	assert.Contains(t, logs.String(), "1.2.3")
	assert.Contains(t, logs.String(), "1.2.4")
}

func TestManifestBaselinesLeaveDecidedPackagesAlone(t *testing.T) {
	// An entry already in the config is the operator's own statement and the
	// way to silence the suggestion for good. Matching is case-insensitive,
	// because viper lowercases map keys.
	cfg := libsConfig()
	cfg.Initials = map[string]string{"core": "0.0.0"}
	root, _, a := computeRepo(t, cfg, zerolog.Nop())

	pkg := func(name string) *model.Package {
		return &model.Package{Name: name, Dir: filepath.Join(root, "packages", name)}
	}
	scanned := []scannedPackage{
		{pkg: pkg("core"), mans: []scanner.Manifest{versioned("package.json", "1.4.2", true)}},
		{pkg: pkg("web"), mans: []scanner.Manifest{versioned("package.json", "2.1.0", true)}},
	}

	got := a.manifestBaselines(scanned, filter.Result{})
	require.Len(t, got, 1)
	assert.Equal(t, "web", got[0].pkg.Name)

	// A filter scopes the baselines the way it scopes the edges.
	sel, err := a.selectPackages(a.discoveredWorkspace([]*model.Package{pkg("core"), pkg("web")}),
		filter.Filter{Packages: []string{"core"}})
	require.NoError(t, err)
	assert.Empty(t, a.manifestBaselines(scanned, sel), "core is selected, and core is already decided")
}

func TestSuggestInitialsWithoutGitStepsAside(t *testing.T) {
	// compute is a filesystem command for everything but this: without a
	// repository there is no way to tell a package that never released from
	// one released long ago, and guessing would propose a baseline for
	// everything in sight. The dependency half is unaffected.
	var logs bytes.Buffer
	root, cfgPath, a := computeRepo(t, libsConfig(), zerolog.New(&logs))
	seedManifest(t, root, "packages/core/package.json", `{"name": "@acme/core", "version": "1.4.2"}`)
	seedManifest(t, root, "packages/web/package.json",
		`{"name": "@acme/web", "dependencies": {"@acme/core": "workspace:*"}}`)

	var out bytes.Buffer
	open, err := a.Compute(t.Context(), cfgPath, ComputeOptions{Out: &out})
	require.NoError(t, err)
	assert.Equal(t, 1, open, "the edge is still suggested")
	assert.NotContains(t, out.String(), "+ initial")
	assert.Contains(t, logs.String(), "skipping version baselines")
}

func TestInitialSuggestionRendersItsEvidence(t *testing.T) {
	s := initialSuggestion{
		pkg:     "core",
		version: ccme.Version{Major: 1, Minor: 4, Patch: 2},
		detail:  "packages/core/package.json declares 1.4.2; no release tag yet",
	}
	assert.Equal(t, "+ initial core 1.4.2  packages/core/package.json declares 1.4.2; no release tag yet", s.render())
}

func TestCollectInitialEditsMergeWithoutRenaming(t *testing.T) {
	// The map is re-read from the file, not taken from the loaded config:
	// viper lowercases map keys, so writing the parsed map back would rename
	// an entry its author spelled otherwise.
	cfg := libsConfig()
	cfg.Initials = map[string]string{"@acme/Core": "2.0.0"}
	_, cfgPath, a := computeRepo(t, cfg, zerolog.Nop())

	var edits fileEdits
	require.NoError(t, a.collectInitialEdits(&edits, cfgPath, []initialSuggestion{
		{pkg: "web", version: ccme.Version{Major: 2, Minor: 1}},
	}))
	require.Equal(t, []string{cfgPath}, edits.order)
	require.Len(t, edits.byFile[cfgPath], 1)
	assert.Equal(t, []string{"initials"}, edits.byFile[cfgPath][0].KeyPath)
	assert.Equal(t, map[string]string{"@acme/Core": "2.0.0", "web": "2.1.0"}, edits.byFile[cfgPath][0].Value)

	// The in-memory view keys the addition the way a reload would.
	assert.Equal(t, "2.1.0", a.cfg.Initials["web"])
	assert.Equal(t, "2.1.0", a.cfg.InitialVersions["web"].String())

	// Nothing accepted is nothing to edit, and an unreadable config is an
	// error rather than a silently dropped entry.
	var none fileEdits
	require.NoError(t, a.collectInitialEdits(&none, cfgPath, nil))
	assert.Empty(t, none.order)
	assert.Error(t, a.collectInitialEdits(&none, filepath.Join(t.TempDir(), "gone.json"),
		[]initialSuggestion{{pkg: "web"}}))
}

func TestTOMLFallbackNamesWhatItReplaces(t *testing.T) {
	what, block, err := tomlFallback(config.Edit{
		KeyPath: []string{"dependencies"},
		Value:   config.Dependencies{{Consumer: "web", Provider: "core"}},
	})
	require.NoError(t, err)
	assert.Equal(t, "[dependencies] table", what)
	assert.Contains(t, block, "[[dependencies.web]]")

	what, block, err = tomlFallback(config.Edit{
		KeyPath: []string{"initials"},
		Value:   map[string]string{"core": "1.4.2"},
	})
	require.NoError(t, err)
	assert.Equal(t, "initials", what)
	assert.Contains(t, block, "[initials]")
	assert.Contains(t, block, "core = '1.4.2'")
}
