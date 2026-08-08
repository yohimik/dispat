package integration

// Area 5: space versioning modes. A space declares how its packages'
// versions relate — independent (the default), fixed (one shared version for
// the whole space, any change releases every member) or fixedSparse (the
// shared version is computed the same way, but unchanged members stay at
// their previous versions). These scenarios drive all three modes through
// the real binary, side by side and across multiple runs, because the
// properties worth checking are relations between runs: convergence, a
// failed ride's alignment catch-up, a shared prerelease train continuing and
// graduating, and no bleed between differently-moded spaces.

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// spacesConfig builds a config from spaces sharing one build/publish script
// pair, with the given build script text.
func spacesConfig(buildScript string, spaces map[string]models.SpaceConfig) models.File {
	cfg := harness.BaseFile(1)
	cfg.Scripts = map[string]string{"build": buildScript, "publish": "echo publishing"}
	cfg.Spaces = spaces
	return cfg
}

// TestVersioningFixedSpaceLifecycle walks a fixed space (a, b) next to an
// independent space (app) through four runs: a change to one member releases
// both at one version with the ride labelled W210 and a "no changes"
// changelog entry; a converged re-run releases nothing; a change to the
// *other* member rides the first one back; and the independent space never
// moves with any of it.
func TestVersioningFixedSpaceLifecycle(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(spacesConfig(echoBuild, map[string]models.SpaceConfig{
		"libs": {Path: "packages", Versioning: models.VersioningFixed, Flow: buildPublish()},
		"apps": {Path: "apps", Flow: buildPublish()},
	}))
	r.SeedPackage("packages", "a")
	r.SeedPackage("packages", "b")
	r.SeedPackage("apps", "app")

	// Run 1: a change scoped to a alone moves the whole fixed space.
	r.Commit("feat(a): only a changes")
	res := r.ReleaseOK()
	assert.True(t, r.HasTag("a@0.1.0"), "tags: %v", r.TagList())
	assert.True(t, r.HasTag("b@0.1.0"), "the ride shares the version; tags: %v", r.TagList())
	assert.Zero(t, r.TagCount("app@"), "the independent space must not move")
	assert.True(t, harness.HasCodeForPackage(res.Events, "W210", "b"),
		"the ride must be explained by W210")

	aLog, err := os.ReadFile(r.Path("packages", "a", "CHANGELOG.md"))
	require.NoError(t, err)
	assert.Contains(t, string(aLog), "### Features", "the changed member gets its scoped entries")
	bLog, err := os.ReadFile(r.Path("packages", "b", "CHANGELOG.md"))
	require.NoError(t, err)
	assert.Contains(t, string(bLog), "No changes — version bump", "the ride gets the bump-only entry")
	assert.NotContains(t, string(bLog), "### Features", "a's feature must not leak into b's changelog")

	// Run 2: converged — the ride must not re-release the space.
	r.ReleaseOK()
	assert.Equal(t, 1, r.TagCount("a@"))
	assert.Equal(t, 1, r.TagCount("b@"))

	// Run 3: the roles reverse — b's fix rides a to the same next version.
	r.CommitEmpty("fix(b): now only b changes")
	res = r.ReleaseOK()
	assert.True(t, r.HasTag("a@0.1.1"), "tags: %v", r.TagList())
	assert.True(t, r.HasTag("b@0.1.1"), "tags: %v", r.TagList())
	assert.True(t, harness.HasCodeForPackage(res.Events, "W210", "a"))
	aLog, err = os.ReadFile(r.Path("packages", "a", "CHANGELOG.md"))
	require.NoError(t, err)
	assert.Contains(t, string(aLog), "No changes — version bump", "this time a carries the bump-only entry")

	// Run 4: the independent space still versions alone.
	r.CommitEmpty("feat(app): the app moves by itself")
	r.ReleaseOK()
	assert.True(t, r.HasTag("app@0.1.0"), "tags: %v", r.TagList())
	assert.Equal(t, 2, r.TagCount("a@"), "the fixed space must not move for an app change")
	assert.Equal(t, 2, r.TagCount("b@"))
}

// TestVersioningFixedSparseLifecycle: the shared version is computed exactly
// like fixed, but only changed members release. An unchanged member stays at
// its previous version, then jumps to the shared version the moment it does
// change — versions align on release, never by empty releases.
func TestVersioningFixedSparseLifecycle(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(spacesConfig(echoBuild, map[string]models.SpaceConfig{
		"libs": {Path: "packages", Versioning: models.VersioningFixedSparse, Flow: buildPublish()},
	}))
	r.SeedPackage("packages", "x")
	r.SeedPackage("packages", "y")

	// Run 1: only the changed member releases; no ride, no W210.
	r.Commit("feat(x): only x changes")
	res := r.ReleaseOK()
	assert.True(t, r.HasTag("x@0.1.0"), "tags: %v", r.TagList())
	assert.Zero(t, r.TagCount("y@"), "sparse: the unchanged member stays put")
	assert.False(t, harness.HasCode(res.Events, "W210"), "no ride in sparse mode")

	// Run 2: converged.
	r.ReleaseOK()
	assert.Equal(t, 1, len(r.TagList()))

	// Run 3: y's first change jumps it to the shared version — computed over
	// the space's highest baseline (x's 0.1.0), not over y's own history.
	r.CommitEmpty("fix(y): y catches its first change")
	r.ReleaseOK()
	assert.True(t, r.HasTag("y@0.1.1"), "y aligns to the space version; tags: %v", r.TagList())
	assert.Equal(t, 1, r.TagCount("x@"), "x has no changes and must not move")

	// Run 4: both change — one shared next version for both, then converge.
	r.CommitEmpty("feat(x): x again\n---\nfix(y): y again")
	r.ReleaseOK()
	assert.True(t, r.HasTag("x@0.2.0"), "tags: %v", r.TagList())
	assert.True(t, r.HasTag("y@0.2.0"), "both land on the shared version; tags: %v", r.TagList())
	r.ReleaseOK()
	assert.Equal(t, 4, len(r.TagList()), "converged")
}

// TestVersioningThreeModesSideBySide runs one commit through three spaces of
// different modes at once: the fixed space moves as a whole, the sparse
// space moves only its changed member, and the independent space versions
// each package from its own history alone.
func TestVersioningThreeModesSideBySide(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(spacesConfig(echoBuild, map[string]models.SpaceConfig{
		"fixed":  {Path: "fixed", Versioning: models.VersioningFixed, Flow: buildPublish()},
		"sparse": {Path: "sparse", Versioning: models.VersioningFixedSparse, Flow: buildPublish()},
		"indep":  {Path: "indep", Flow: buildPublish()},
	}))
	for _, p := range []struct{ space, name string }{
		{"fixed", "f1"}, {"fixed", "f2"},
		{"sparse", "s1"}, {"sparse", "s2"},
		{"indep", "i1"}, {"indep", "i2"},
	} {
		r.SeedPackage(p.space, p.name)
	}

	// Run 1: one member of each space changes.
	r.Commit("feat(f1,s1,i1): one change per space")
	r.ReleaseOK()
	for _, tag := range []string{"f1@0.1.0", "f2@0.1.0", "s1@0.1.0", "i1@0.1.0"} {
		assert.True(t, r.HasTag(tag), "expected %s; tags: %v", tag, r.TagList())
	}
	assert.Zero(t, r.TagCount("s2@"), "sparse: the unchanged member stays")
	assert.Zero(t, r.TagCount("i2@"), "independent: the unchanged package stays")

	// Run 2: the other members change. The fixed space moves as one, the
	// sparse newcomer aligns to its space version, and the independent
	// package versions from its own empty history (0.0.1, not 0.1.1).
	r.CommitEmpty("fix(f2,s2,i2): the other members move")
	r.ReleaseOK()
	for _, tag := range []string{"f1@0.1.1", "f2@0.1.1", "s2@0.1.1", "i2@0.0.1"} {
		assert.True(t, r.HasTag(tag), "expected %s; tags: %v", tag, r.TagList())
	}
	assert.Equal(t, 1, r.TagCount("s1@"), "sparse: s1 has no changes and must not move")
	assert.Equal(t, 1, r.TagCount("i1@"), "independent: i1 must not move either")

	// Run 3: converged across all three modes at once.
	r.ReleaseOK()
	assert.Equal(t, 8, len(r.TagList()), "no mode may re-release on a quiet run; tags: %v", r.TagList())
}

// TestVersioningFixedSharedPrereleaseTrain: a fixed space runs a single
// prerelease train. One member's channel directive takes the whole space
// onto beta; later work continues the one train (beta.1 for both); one
// member's graduation directive ends it for the whole space; and the
// graduated space converges.
func TestVersioningFixedSharedPrereleaseTrain(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(spacesConfig(echoBuild, map[string]models.SpaceConfig{
		"libs": {Path: "packages", Versioning: models.VersioningFixed, Flow: buildPublish()},
	}))
	r.SeedPackage("packages", "a")
	r.SeedPackage("packages", "b")

	r.Commit("feat(a)@beta: start the train from a alone")
	r.ReleaseOK()
	assert.True(t, r.HasTag("a@0.1.0-beta.0"), "tags: %v", r.TagList())
	assert.True(t, r.HasTag("b@0.1.0-beta.0"), "one train for the space; tags: %v", r.TagList())

	r.CommitEmpty("fix(b): more work while on the train")
	r.ReleaseOK()
	assert.True(t, r.HasTag("a@0.1.0-beta.1"), "tags: %v", r.TagList())
	assert.True(t, r.HasTag("b@0.1.0-beta.1"), "the train continues as one; tags: %v", r.TagList())

	r.CommitEmpty("release(a)@beta>stable: graduate the space via one member")
	r.ReleaseOK()
	assert.True(t, r.HasTag("a@0.1.0"), "tags: %v", r.TagList())
	assert.True(t, r.HasTag("b@0.1.0"), "graduation moves the whole space; tags: %v", r.TagList())

	before := len(r.TagList())
	r.ReleaseOK()
	assert.Equal(t, before, len(r.TagList()), "the graduated space converges")
}

// TestVersioningFixedRideFailureThenAlignmentCatchUp: a ride is a real
// release and can fail like one. The changed member publishes; the rider's
// build fails; the next run must catch the rider up at exactly the space's
// published version (the alignment half of the fixed invariant), and a third
// run converges.
func TestVersioningFixedRideFailureThenAlignmentCatchUp(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(spacesConfig(failIfMarker, map[string]models.SpaceConfig{
		"libs": {Path: "packages", Versioning: models.VersioningFixed, Flow: buildPublish()},
	}))
	r.SeedPackage("packages", "a")
	r.SeedPackage("packages", "b")
	r.WriteFile("packages/b/FAIL", "x")
	r.Commit("feat(a): a changes, b's ride is about to fail")

	res := r.Release()
	require.Equal(t, 1, res.Code, "the failed ride must fail the run\nstdout:\n%s", res.Stdout)
	assert.True(t, r.HasTag("a@0.1.0"), "the changed member still publishes; tags: %v", r.TagList())
	assert.Zero(t, r.TagCount("b@"), "the failed ride must not be tagged")

	r.Remove("packages/b/FAIL")
	res = r.ReleaseOK()
	assert.True(t, r.HasTag("b@0.1.0"),
		"the laggard must align to the space's published version; tags: %v", r.TagList())
	assert.Equal(t, 1, r.TagCount("a@"), "a must not be re-released by the catch-up")
	assert.True(t, harness.HasCodeForPackage(res.Events, "W210", "b"))

	r.ReleaseOK()
	assert.Equal(t, 2, len(r.TagList()), "aligned: nothing further to do")
}

// TestVersioningCrossSpaceDependencyIntoFixedSpace: an independent provider
// propagates into one member of a fixed space; that member picks the bump up
// as an ordinary DueTo release (version task, DISPAT_UPDATED_*), and its
// space mates ride to the same shared version without any DueTo of their
// own — the dependency edge stays package-scoped even where versions are
// space-scoped.
func TestVersioningCrossSpaceDependencyIntoFixedSpace(t *testing.T) {
	const envFile = "version-env.txt"
	r := harness.New(t)
	cfg := harness.BaseFile(1)
	cfg.Scripts = map[string]string{
		"build":   "echo building",
		"publish": "echo publishing",
		"sync":    "env | grep '^DISPAT_' | sort > " + envFile,
	}
	cfg.Spaces = map[string]models.SpaceConfig{
		"core": {Path: "core", Flow: buildPublish()},
		"ui": {Path: "ui", Versioning: models.VersioningFixed, Flow: &models.SpaceFlowConfig{
			Version: []string{"sync"}, Build: []string{"build"}, Publish: []string{"publish"}}},
	}
	cfg.Dependencies = []models.DependencyConfig{{Consumer: "widgets", Provider: "core"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("core", "core")
	r.SeedPackage("ui", "widgets")
	r.SeedPackage("ui", "themes")
	r.Commit("feat(core)^: reaches widgets in the fixed ui space")

	r.ReleaseOK()
	assert.True(t, r.HasTag("core@0.1.0"), "tags: %v", r.TagList())
	assert.True(t, r.HasTag("widgets@0.0.1"), "the consumer picks up the propagated patch; tags: %v", r.TagList())
	assert.True(t, r.HasTag("themes@0.0.1"), "the space mate rides to the shared version; tags: %v", r.TagList())

	data, err := os.ReadFile(r.Path("ui", "widgets", envFile))
	require.NoError(t, err, "the DueTo member runs its version task")
	assert.Contains(t, string(data), "DISPAT_UPDATED_CORE_NEW_VERSION=0.1.0")
	assert.NoFileExists(t, r.Path("ui", "themes", envFile),
		"the ride has no provider updates, so its version task must not exist")
}

// TestVersioningFixedHoldAndResume: Release-As: none on one member keeps
// that member (and only it) out of the space's release; the resume run
// aligns it back to the space's published version.
func TestVersioningFixedHoldAndResume(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(spacesConfig(echoBuild, map[string]models.SpaceConfig{
		"libs": {Path: "packages", Versioning: models.VersioningFixed, Flow: buildPublish()},
	}))
	r.SeedPackage("packages", "a")
	r.SeedPackage("packages", "b")
	r.Commit("feat(a): work\n---\nrelease(b): keep b back\n\nRelease-As: none\n")

	r.ReleaseOK()
	assert.True(t, r.HasTag("a@0.1.0"), "tags: %v", r.TagList())
	assert.Zero(t, r.TagCount("b@"), "the held member must not release, fixed space or not")

	r.CommitEmpty("release(b): resume\n\nRelease-As: auto\n")
	res := r.ReleaseOK()
	assert.True(t, r.HasTag("b@0.1.0"),
		"the resumed member aligns to the space's published version; tags: %v", r.TagList())
	assert.Equal(t, 1, r.TagCount("a@"), "a must not move for b's resume")
	assert.True(t, harness.HasCodeForPackage(res.Events, "W210", "b"))
}

// TestVersioningFixedExactPinMovesTheSpace: an exact Release-As naming one
// member pins the space's single shared version — both members release at
// the pinned version, and the pin's guards keep applying (a second, lower
// pin later is rejected without releasing anything).
func TestVersioningFixedExactPinMovesTheSpace(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(spacesConfig(echoBuild, map[string]models.SpaceConfig{
		"libs": {Path: "packages", Versioning: models.VersioningFixed, Flow: buildPublish()},
	}))
	r.SeedPackage("packages", "a")
	r.SeedPackage("packages", "b")
	r.Commit("feat(a): work\n---\nrelease(a): ship it as 1.0.0\n\nRelease-As: 1.0.0\n")

	r.ReleaseOK()
	assert.True(t, r.HasTag("a@1.0.0"), "tags: %v", r.TagList())
	assert.True(t, r.HasTag("b@1.0.0"), "the pin moves the whole space; tags: %v", r.TagList())

	r.CommitEmpty("release(a): try to go backwards\n\nRelease-As: 0.5.0\n")
	res := r.ReleaseOK() // commitErrors: warn — the rejected pin releases nothing
	assert.True(t, harness.HasCode(res.Events, "E153"), "the not-greater guard applies to the space version")
	assert.Equal(t, 1, r.TagCount("a@"))
	assert.Equal(t, 1, r.TagCount("b@"))
}

// TestVersioningFixedSpaceExecutesEveryMemberScript: a ride is a full
// release at the execution level too — build and publish scripts run for the
// riding member, not only for the changed one. (The changelog is what
// distinguishes the two, not the pipeline.)
func TestVersioningFixedSpaceExecutesEveryMemberScript(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(spacesConfig(markerBuild, map[string]models.SpaceConfig{
		"libs": {Path: "packages", Versioning: models.VersioningFixed, Flow: buildPublish()},
	}))
	r.SeedPackage("packages", "a")
	r.SeedPackage("packages", "b")
	r.Commit("feat(a): only a changes")

	r.ReleaseOK()
	assert.Equal(t, 2, buildRuns(r), "both members build: the ride is a real release")
}

// TestVersioningFixedConflictResolutions pins the two fixed-space conflict
// warnings. Competing exact pins: the space has one shared version, so the
// newest pin wins and W211 says so. Divergent channels: the space moves as
// one, so a deterministic winner is picked and W212 says so — the warning is
// the operator's only sign that half their intent was overridden.
func TestVersioningFixedConflictResolutions(t *testing.T) {
	t.Run("W211_competing_pins", func(t *testing.T) {
		r := harness.New(t)
		r.WriteConfigModel(spacesConfig(echoBuild, map[string]models.SpaceConfig{
			"libs": {Path: "packages", Versioning: models.VersioningFixed, Flow: buildPublish()},
		}))
		r.SeedPackage("packages", "a")
		r.SeedPackage("packages", "b")
		r.Commit("feat(a,b): bootstrap")
		r.CommitEmpty("release(a): pin a\n\nRelease-As: 0.5.0\n")
		r.CommitEmpty("release(b): pin b, later\n\nRelease-As: 0.9.0\n")

		res := r.ReleaseOK()
		assert.True(t, harness.HasCode(res.Events, "W211"),
			"competing pins in one fixed space must be reported")
		assert.True(t, r.HasTag("a@0.9.0"), "the newest pin moves the space: %v", r.TagList())
		assert.True(t, r.HasTag("b@0.9.0"), "tags: %v", r.TagList())
		assert.False(t, r.HasTag("a@0.5.0"), "the losing pin must not also release")
	})

	t.Run("W212_divergent_channels", func(t *testing.T) {
		r := harness.New(t)
		r.WriteConfigModel(spacesConfig(echoBuild, map[string]models.SpaceConfig{
			"libs": {Path: "packages", Versioning: models.VersioningFixed, Flow: buildPublish()},
		}))
		r.SeedPackage("packages", "a")
		r.SeedPackage("packages", "b")
		r.Commit("feat(a)@beta: a wants beta\n---\nfeat(b)@rc: b wants rc")

		res := r.ReleaseOK()
		assert.True(t, harness.HasCode(res.Events, "W212"),
			"divergent member channels must be reported")
		tags := r.TagList()
		require.Len(t, tags, 2, "both members release once: %v", tags)
		suffix := func(tag string) string { return tag[strings.LastIndexByte(tag, '@'):] }
		assert.Equal(t, suffix(tags[0]), suffix(tags[1]),
			"the space moves as one: both members share version and channel: %v", tags)
	})
}
