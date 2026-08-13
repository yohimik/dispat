package integration

// Area 5: space versioning modes. A space declares how much of its packages'
// versions is held in common — nothing (independent, the default), the whole
// version (fixed), the major and minor (fixedMajorMinor) or the major alone
// (fixedMajor) — and, for each shared mode, whether an unchanged member rides
// along at the shared version or stays behind until it changes (the *Sparse
// variants). These scenarios drive all seven modes through the real binary,
// side by side and across multiple runs, because the properties worth
// checking are relations between runs: convergence, a failed ride's alignment
// catch-up, a shared prerelease train continuing and graduating, versions
// diverging again below the shared part, and no bleed between spaces.

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
	cfg.Scripts = map[string]models.Script{"build": {buildScript}, "publish": {"echo publishing"}}
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

	r.Commit("feat(a)%beta: start the train from a alone")
	r.ReleaseOK()
	assert.True(t, r.HasTag("a@0.1.0-beta.0"), "tags: %v", r.TagList())
	assert.True(t, r.HasTag("b@0.1.0-beta.0"), "one train for the space; tags: %v", r.TagList())

	r.CommitEmpty("fix(b): more work while on the train")
	r.ReleaseOK()
	assert.True(t, r.HasTag("a@0.1.0-beta.1"), "tags: %v", r.TagList())
	assert.True(t, r.HasTag("b@0.1.0-beta.1"), "the train continues as one; tags: %v", r.TagList())

	r.CommitEmpty("release(a)%beta>stable: graduate the space via one member")
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

// TestVersioningFixedRideFailureMidTrainHealsOntoTheTrain: the same catch-up
// while the group is on a prerelease train, which is where the three features
// this suite covers separately actually meet — a shared version, a train, and
// a leg that failed and has to heal.
//
// The claim is what the laggard catches up *to*. The group's baseline while
// it is mid-train is a prerelease, and a prerelease ranks below its own core,
// so the member that missed a beta must join the train rather than jump past
// it to a stable version the group has never published. Jumping would put a
// package on 0.1.0 while its group mates are on 0.1.0-beta.1, and §19.1
// forbids moving tags back.
//
// The train is then graduated with the healed member in it, which is what
// proves the catch-up put it on the train rather than merely near it.
func TestVersioningFixedRideFailureMidTrainHealsOntoTheTrain(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(spacesConfig(failIfMarker, map[string]models.SpaceConfig{
		"libs": {Path: "packages", Versioning: models.VersioningFixed, Flow: buildPublish()},
	}))
	r.SeedPackage("packages", "a")
	r.SeedPackage("packages", "b")

	// Run 1: the whole group boards the train together.
	r.Commit("feat(a)%beta: start the train")
	r.ReleaseOK()
	require.True(t, r.HasTag("a@0.1.0-beta.0"), "tags: %v", r.TagList())
	require.True(t, r.HasTag("b@0.1.0-beta.0"), "tags: %v", r.TagList())

	// Run 2: the train moves on and b's ride fails, leaving it a beta behind.
	r.WriteFile("packages/b/FAIL", "x")
	r.CommitEmpty("fix(a): more work on the train")
	res := r.Release()
	require.Equal(t, 1, res.Code, "the failed ride must fail the run\nstdout:\n%s", res.Stdout)
	assert.True(t, r.HasTag("a@0.1.0-beta.1"), "the changed member continues; tags: %v", r.TagList())
	assert.Equal(t, 1, r.TagCount("b@"), "the failed ride is not tagged; tags: %v", r.TagList())

	// Run 3: b heals. It must land on the train's current position, not on the
	// stable core the group has not reached.
	r.Remove("packages/b/FAIL")
	res = r.ReleaseOK()
	assert.True(t, r.HasTag("b@0.1.0-beta.1"),
		"the laggard joins the train rather than jumping past it; tags: %v", r.TagList())
	assert.False(t, r.HasTag("b@0.1.0"), "and must not land on a stable version nobody published")
	assert.Equal(t, 2, r.TagCount("a@"), "a must not be re-released by the catch-up")
	assert.True(t, harness.HasCodeForPackage(res.Events, "W210", "b"),
		"the ride is explained, events:\n%s", res.Stdout)

	// Run 4: graduation takes both members off the train together, which only
	// holds if run 3 really put b on it.
	r.CommitEmpty("release(a)%beta>stable: graduate the group")
	r.ReleaseOK()
	assert.True(t, r.HasTag("a@0.1.0"), "tags: %v", r.TagList())
	assert.True(t, r.HasTag("b@0.1.0"), "the healed member graduates with the group; tags: %v", r.TagList())

	before := len(r.TagList())
	r.ReleaseOK()
	assert.Equal(t, before, len(r.TagList()), "the graduated group converges")
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
	cfg.Scripts = map[string]models.Script{
		"build":   {"echo building"},
		"publish": {"echo publishing"},
		"sync":    {"env | grep '^DISPAT_' | sort > " + envFile},
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
	require.NoError(t, err, "the member that picks up core's version runs its version task")
	assert.Contains(t, string(data), "DISPAT_UPDATED_CORE_NEW_VERSION=0.1.0")
	assert.NoFileExists(t, r.Path("ui", "themes", envFile),
		"the ride declares no edge to core, so it picks up no version and has no version task")
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
		r.Commit("feat(a)%beta: a wants beta\n---\nfeat(b)%rc: b wants rc")

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

// ---------------------------------------------------------------------------
// Partial sharing: the modes that hold only a prefix of the version in common
// ---------------------------------------------------------------------------

// TestVersioningFixedMajorLifecycle walks a fixedMajor space through six runs.
// Patches and minors are each package's own, so they move nobody else; a
// breaking change moves the whole group to one major, riding the unchanged
// member (W210) with a changelog entry naming what is actually shared; the
// group then converges and is free to diverge again below the major.
func TestVersioningFixedMajorLifecycle(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(spacesConfig(echoBuild, map[string]models.SpaceConfig{
		"libs": {Path: "packages", Versioning: models.VersioningFixedMajor, Flow: buildPublish()},
		"apps": {Path: "apps", Flow: buildPublish()},
	}))
	r.SeedPackage("packages", "a")
	r.SeedPackage("packages", "b")
	r.SeedPackage("apps", "app")

	// Run 1: both members start their own lines.
	r.Commit("feat(a,b): bootstrap both members")
	r.ReleaseOK()
	assert.True(t, r.HasTag("a@0.1.0"), "tags: %v", r.TagList())
	assert.True(t, r.HasTag("b@0.1.0"), "tags: %v", r.TagList())

	// Run 2: a patch is below the shared major, so it moves nobody else.
	r.CommitEmpty("fix(a): a patch of a's own")
	res := r.ReleaseOK()
	assert.True(t, r.HasTag("a@0.1.1"), "tags: %v", r.TagList())
	assert.Equal(t, 1, r.TagCount("b@"), "a patch must not move the group")
	assert.False(t, harness.HasCode(res.Events, "W210"), "no ride below the shared major")

	// Run 3: a minor is below it too — the two members diverge legitimately.
	r.CommitEmpty("feat(b): b's own minor")
	r.ReleaseOK()
	assert.True(t, r.HasTag("b@0.2.0"), "tags: %v", r.TagList())
	assert.Equal(t, 2, r.TagCount("a@"), "a minor must not move the group either")

	// Run 4: the breaking change reaches the shared part. Both members land on
	// the same next major, computed over the group's highest baseline (0.2.0).
	r.CommitEmpty("feat(a)!: a breaking change")
	res = r.ReleaseOK()
	assert.True(t, r.HasTag("a@1.0.0"), "tags: %v", r.TagList())
	assert.True(t, r.HasTag("b@1.0.0"), "the group shares one major; tags: %v", r.TagList())
	assert.True(t, harness.HasCodeForPackage(res.Events, "W210", "b"),
		"the ride must be explained by W210")
	assert.Zero(t, r.TagCount("app@"), "the independent space must not move")

	bLog, err := os.ReadFile(r.Path("packages", "b", "CHANGELOG.md"))
	require.NoError(t, err)
	assert.Contains(t, string(bLog), "No changes — version bump to keep the versioning group on one major version.",
		"the ride's entry names the part that is shared")
	assert.NotContains(t, string(bLog), "### Breaking", "a's breaking change must not leak into b's changelog")

	// Run 5: converged.
	r.ReleaseOK()
	assert.Equal(t, 3, r.TagCount("a@"))
	assert.Equal(t, 3, r.TagCount("b@"))

	// Run 6: below the shared major the members are free to diverge again.
	r.CommitEmpty("fix(b): back to b's own line")
	r.ReleaseOK()
	assert.True(t, r.HasTag("b@1.0.1"), "tags: %v", r.TagList())
	assert.Equal(t, 3, r.TagCount("a@"), "a stays where it is")
}

// TestVersioningFixedMajorSparseLifecycle: the shared major is computed the
// same way, but an unchanged member stays at its previous version instead of
// riding. Its next change is what joins it to the group's major, and it joins
// at the start of its own line rather than continuing the old one.
func TestVersioningFixedMajorSparseLifecycle(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(spacesConfig(echoBuild, map[string]models.SpaceConfig{
		"libs": {Path: "packages", Versioning: models.VersioningFixedMajorSparse, Flow: buildPublish()},
	}))
	r.SeedPackage("packages", "x")
	r.SeedPackage("packages", "y")

	// Run 1: a minor is x's own under fixedMajor, and y has nothing pending.
	r.Commit("feat(x): only x changes")
	r.ReleaseOK()
	assert.True(t, r.HasTag("x@0.1.0"), "tags: %v", r.TagList())
	assert.Zero(t, r.TagCount("y@"))

	// Run 2: the shared major moves, and sparse leaves y exactly where it is.
	r.CommitEmpty("feat(x)!: x breaks compatibility")
	res := r.ReleaseOK()
	assert.True(t, r.HasTag("x@1.0.0"), "tags: %v", r.TagList())
	assert.Zero(t, r.TagCount("y@"), "sparse: an unchanged member never rides")
	assert.False(t, harness.HasCode(res.Events, "W210"), "no ride in a sparse mode")

	// Run 3: y's first change joins it to the shared major.
	r.CommitEmpty("fix(y): y catches its first change")
	r.ReleaseOK()
	assert.True(t, r.HasTag("y@1.0.0"),
		"y joins the shared major at the start of its own line; tags: %v", r.TagList())
	assert.Equal(t, 2, r.TagCount("x@"), "x has no changes and must not move")

	// Run 4: converged.
	r.ReleaseOK()
	assert.Equal(t, 3, len(r.TagList()))
}

// TestVersioningFixedMajorMinorLifecycle: one depth further in. A patch is
// still each package's own, but a minor now moves the whole group, and so
// does a breaking change.
func TestVersioningFixedMajorMinorLifecycle(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(spacesConfig(echoBuild, map[string]models.SpaceConfig{
		"libs": {Path: "packages", Versioning: models.VersioningFixedMajorMinor, Flow: buildPublish()},
	}))
	r.SeedPackage("packages", "a")
	r.SeedPackage("packages", "b")

	// Run 1: both start their own patch lines.
	r.Commit("fix(a,b): bootstrap both members")
	r.ReleaseOK()
	assert.True(t, r.HasTag("a@0.0.1"), "tags: %v", r.TagList())
	assert.True(t, r.HasTag("b@0.0.1"), "tags: %v", r.TagList())

	// Run 2: a patch stays with its package.
	r.CommitEmpty("fix(a): a patch of a's own")
	r.ReleaseOK()
	assert.True(t, r.HasTag("a@0.0.2"), "tags: %v", r.TagList())
	assert.Equal(t, 1, r.TagCount("b@"), "a patch must not move the group")

	// Run 3: a minor reaches the shared part and moves everyone.
	r.CommitEmpty("feat(a): a new minor")
	res := r.ReleaseOK()
	assert.True(t, r.HasTag("a@0.1.0"), "tags: %v", r.TagList())
	assert.True(t, r.HasTag("b@0.1.0"), "the group shares the minor; tags: %v", r.TagList())
	assert.True(t, harness.HasCodeForPackage(res.Events, "W210", "b"))

	bLog, err := os.ReadFile(r.Path("packages", "b", "CHANGELOG.md"))
	require.NoError(t, err)
	assert.Contains(t, string(bLog), "No changes — version bump to keep the versioning group on one major and minor version.")

	// Run 4: a breaking change moves everyone too.
	r.CommitEmpty("feat(b)!: b breaks compatibility")
	r.ReleaseOK()
	assert.True(t, r.HasTag("a@1.0.0"), "tags: %v", r.TagList())
	assert.True(t, r.HasTag("b@1.0.0"), "tags: %v", r.TagList())

	// Run 5: converged.
	r.ReleaseOK()
	assert.Equal(t, 4, r.TagCount("a@"), "a also had a patch of its own; tags: %v", r.TagList())
	assert.Equal(t, 3, r.TagCount("b@"), "b never had a patch of its own; tags: %v", r.TagList())
}

// TestVersioningFixedMajorMinorSparseLifecycle: the sparse variant at depth
// two. The member that stays behind rejoins on its own next change, whatever
// the size of that change.
func TestVersioningFixedMajorMinorSparseLifecycle(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(spacesConfig(echoBuild, map[string]models.SpaceConfig{
		"libs": {Path: "packages", Versioning: models.VersioningFixedMajorMinorSparse, Flow: buildPublish()},
	}))
	r.SeedPackage("packages", "x")
	r.SeedPackage("packages", "y")

	// Run 1: the minor moves the shared part; sparse leaves y behind.
	r.Commit("feat(x): only x changes")
	res := r.ReleaseOK()
	assert.True(t, r.HasTag("x@0.1.0"), "tags: %v", r.TagList())
	assert.Zero(t, r.TagCount("y@"))
	assert.False(t, harness.HasCode(res.Events, "W210"))

	// Run 2: y's own patch joins it to the shared major and minor.
	r.CommitEmpty("fix(y): y catches its first change")
	r.ReleaseOK()
	assert.True(t, r.HasTag("y@0.1.0"), "y joins the shared prefix; tags: %v", r.TagList())
	assert.Equal(t, 1, r.TagCount("x@"))

	// Run 3: below the shared minor the two are independent again.
	r.CommitEmpty("fix(x): x's own patch")
	r.ReleaseOK()
	assert.True(t, r.HasTag("x@0.1.1"), "tags: %v", r.TagList())
	assert.Equal(t, 1, r.TagCount("y@"), "a patch moves nobody else")

	// Run 4: a minor moves the shared part again, and sparse leaves x behind.
	r.CommitEmpty("feat(y): y needs a minor")
	r.ReleaseOK()
	assert.True(t, r.HasTag("y@0.2.0"), "tags: %v", r.TagList())
	assert.Equal(t, 2, r.TagCount("x@"), "sparse: x keeps 0.1.1 until it changes")

	// Run 5: converged.
	r.ReleaseOK()
	assert.Equal(t, 4, len(r.TagList()))
}

// TestVersioningAllModesSideBySide runs the same commits through all seven
// modes at once. The first run carries a minor, which separates the depths:
// it moves the whole group under fixed and fixedMajorMinor and nobody else
// under fixedMajor. The second carries a breaking change, which every shared
// mode passes on. This is the table the documentation shows, asserted.
func TestVersioningAllModesSideBySide(t *testing.T) {
	r := harness.New(t)
	spaces := map[string]models.SpaceConfig{
		"fx":  {Path: "fx", Versioning: models.VersioningFixed, Flow: buildPublish()},
		"fxs": {Path: "fxs", Versioning: models.VersioningFixedSparse, Flow: buildPublish()},
		"mm":  {Path: "mm", Versioning: models.VersioningFixedMajorMinor, Flow: buildPublish()},
		"mms": {Path: "mms", Versioning: models.VersioningFixedMajorMinorSparse, Flow: buildPublish()},
		"mj":  {Path: "mj", Versioning: models.VersioningFixedMajor, Flow: buildPublish()},
		"mjs": {Path: "mjs", Versioning: models.VersioningFixedMajorSparse, Flow: buildPublish()},
		"ind": {Path: "ind", Flow: buildPublish()},
	}
	r.WriteConfigModel(spacesConfig(echoBuild, spaces))
	for space := range spaces {
		r.SeedPackage(space, space+"1")
		r.SeedPackage(space, space+"2")
	}

	// Run 1: a minor in the first member of every space.
	r.Commit("feat(fx1,fxs1,mm1,mms1,mj1,mjs1,ind1): one minor per space")
	r.ReleaseOK()
	for _, tag := range []string{
		"fx1@0.1.0", "fx2@0.1.0", // fixed: the whole version is shared
		"fxs1@0.1.0",             // fixedSparse: only the changed member
		"mm1@0.1.0", "mm2@0.1.0", // fixedMajorMinor: the minor is shared
		"mms1@0.1.0", // its sparse variant
		"mj1@0.1.0",  // fixedMajor: a minor is the package's own
		"mjs1@0.1.0",
		"ind1@0.1.0",
	} {
		assert.True(t, r.HasTag(tag), "expected %s; tags: %v", tag, r.TagList())
	}
	for _, pkg := range []string{"fxs2@", "mms2@", "mj2@", "mjs2@", "ind2@"} {
		assert.Zerof(t, r.TagCount(pkg), "%s must not have moved for a minor; tags: %v", pkg, r.TagList())
	}

	// Run 2: a breaking change reaches every shared mode's shared part.
	r.CommitEmpty("feat(fx1,fxs1,mm1,mms1,mj1,mjs1,ind1)!: one breaking change per space")
	r.ReleaseOK()
	for _, tag := range []string{
		"fx1@1.0.0", "fx2@1.0.0",
		"fxs1@1.0.0",
		"mm1@1.0.0", "mm2@1.0.0",
		"mms1@1.0.0",
		"mj1@1.0.0", "mj2@1.0.0", // the major is shared: mj2 finally rides
		"mjs1@1.0.0",
		"ind1@1.0.0",
	} {
		assert.True(t, r.HasTag(tag), "expected %s; tags: %v", tag, r.TagList())
	}
	for _, pkg := range []string{"fxs2@", "mms2@", "mjs2@", "ind2@"} {
		assert.Zerof(t, r.TagCount(pkg), "%s is sparse or independent and never rides; tags: %v", pkg, r.TagList())
	}

	// Run 3: the independent space's second package finally changes. It has
	// never been released, and independence means exactly that its version
	// comes from its own history: 0.0.1, the first release of a package with
	// nothing behind it, rather than a step off ind1's 1.0.0. This is the
	// claim that separates "independent" from every shared mode above, where
	// a newcomer's first release adopts the group's position instead.
	r.CommitEmpty("fix(ind2): the independent newcomer's first change")
	r.ReleaseOK()
	assert.True(t, r.HasTag("ind2@0.0.1"),
		"an independent newcomer versions from its own empty history; tags: %v", r.TagList())
	assert.Equal(t, 2, r.TagCount("ind1@"), "and takes its space mate nowhere; tags: %v", r.TagList())

	// Run 4: every mode converges on a quiet run.
	before := len(r.TagList())
	r.ReleaseOK()
	assert.Equal(t, before, len(r.TagList()), "no mode may re-release; tags: %v", r.TagList())
}

// TestVersioningFixedMajorSharedTrain: a prerelease train belongs to whatever
// it moves. A breaking change on a channel takes the whole group onto one
// train, further work continues that train for everyone, and one member's
// graduation ends it for everyone — while a train on a patch stays entirely
// within the package that started it.
func TestVersioningFixedMajorSharedTrain(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(spacesConfig(echoBuild, map[string]models.SpaceConfig{
		"libs": {Path: "packages", Versioning: models.VersioningFixedMajor, Flow: buildPublish()},
	}))
	r.SeedPackage("packages", "a")
	r.SeedPackage("packages", "b")

	r.Commit("feat(a,b): bootstrap both members")
	r.ReleaseOK()

	r.CommitEmpty("feat(a)%beta!: a breaking change on the beta line")
	r.ReleaseOK()
	assert.True(t, r.HasTag("a@1.0.0-beta.0"), "tags: %v", r.TagList())
	assert.True(t, r.HasTag("b@1.0.0-beta.0"), "the shared major runs one train; tags: %v", r.TagList())

	r.CommitEmpty("fix(b): more work while on the train")
	r.ReleaseOK()
	assert.True(t, r.HasTag("a@1.0.0-beta.1"), "tags: %v", r.TagList())
	assert.True(t, r.HasTag("b@1.0.0-beta.1"), "the train continues as one; tags: %v", r.TagList())

	r.CommitEmpty("release(a)%beta>stable: graduate the group via one member")
	r.ReleaseOK()
	assert.True(t, r.HasTag("a@1.0.0"), "tags: %v", r.TagList())
	assert.True(t, r.HasTag("b@1.0.0"), "the graduation moves the whole group; tags: %v", r.TagList())

	before := len(r.TagList())
	r.ReleaseOK()
	assert.Equal(t, before, len(r.TagList()), "the graduated group converges")

	// A train below the shared major is one package's own, exactly as its
	// versions are.
	r.CommitEmpty("fix(a)%beta: a's own train")
	r.ReleaseOK()
	assert.True(t, r.HasTag("a@1.0.1-beta.0"), "tags: %v", r.TagList())
	assert.Equal(t, before/2, r.TagCount("b@"), "b is not on a's train; tags: %v", r.TagList())
}

// TestVersioningPartialPinScope: an exact Release-As moves the group only
// when it names a different shared part. A pin that stays inside the group's
// major is the package's own business, and must not be answered against the
// group's aggregate — nor drag anyone else along.
func TestVersioningPartialPinScope(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(spacesConfig(echoBuild, map[string]models.SpaceConfig{
		"libs": {Path: "packages", Versioning: models.VersioningFixedMajor, Flow: buildPublish()},
	}))
	r.SeedPackage("packages", "a")
	r.SeedPackage("packages", "b")

	r.Commit("feat(a,b): bootstrap both members")
	r.ReleaseOK()

	// Crossing the major: the pin names the group's shared part.
	r.CommitEmpty("release(a): ship the group as 1.0.0\n\nRelease-As: 1.0.0\n")
	r.ReleaseOK()
	assert.True(t, r.HasTag("a@1.0.0"), "tags: %v", r.TagList())
	assert.True(t, r.HasTag("b@1.0.0"), "the pin moves the whole group; tags: %v", r.TagList())

	// Inside the major: the pin is a's alone.
	r.CommitEmpty("release(a): a's own next minor\n\nRelease-As: 1.5.0\n")
	res := r.ReleaseOK()
	assert.False(t, harness.HasCode(res.Events, "E153"),
		"a member's own pin must not be measured against the group: %s", res.Stdout)
	assert.True(t, r.HasTag("a@1.5.0"), "tags: %v", r.TagList())
	assert.Equal(t, 2, r.TagCount("b@"), "a pin below the shared major moves nobody else")

	r.ReleaseOK()
	assert.Equal(t, 3, r.TagCount("a@"), "converged")
	assert.Equal(t, 2, r.TagCount("b@"))
}

// TestVersioningFixedMajorRideFailureThenAlignment: a partial-mode ride is a
// real release and can fail like one. The next run must catch the laggard up
// to the group's shared major — at the start of its own line, not at the
// other member's exact version — and a further run converges.
func TestVersioningFixedMajorRideFailureThenAlignment(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(spacesConfig(failIfMarker, map[string]models.SpaceConfig{
		"libs": {Path: "packages", Versioning: models.VersioningFixedMajor, Flow: buildPublish()},
	}))
	r.SeedPackage("packages", "a")
	r.SeedPackage("packages", "b")

	r.Commit("feat(a,b): bootstrap both members")
	r.ReleaseOK()
	assert.True(t, r.HasTag("b@0.1.0"), "tags: %v", r.TagList())

	r.WriteFile("packages/b/FAIL", "x")
	r.CommitEmpty("feat(a)!: a breaks, b's ride is about to fail")
	res := r.Release()
	require.Equal(t, 1, res.Code, "the failed ride must fail the run\nstdout:\n%s", res.Stdout)
	assert.True(t, r.HasTag("a@1.0.0"), "the changed member still publishes; tags: %v", r.TagList())
	assert.Equal(t, 1, r.TagCount("b@"), "the failed ride must not be tagged")

	r.Remove("packages/b/FAIL")
	res = r.ReleaseOK()
	assert.True(t, r.HasTag("b@1.0.0"),
		"the laggard catches up to the shared major; tags: %v", r.TagList())
	assert.Equal(t, 2, r.TagCount("a@"), "a must not be re-released by the catch-up; tags: %v", r.TagList())
	assert.True(t, harness.HasCodeForPackage(res.Events, "W210", "b"))

	before := len(r.TagList())
	r.ReleaseOK()
	assert.Equal(t, before, len(r.TagList()), "aligned: nothing further to do")
}

// TestVersioningPartialRideExecutesEveryMemberScript: a ride under a partial
// mode is a full release at the execution level too, exactly as it is under
// fixed — the changelog is what distinguishes the two, not the pipeline.
func TestVersioningPartialRideExecutesEveryMemberScript(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(spacesConfig(markerBuild, map[string]models.SpaceConfig{
		"libs": {Path: "packages", Versioning: models.VersioningFixedMajor, Flow: buildPublish()},
	}))
	r.SeedPackage("packages", "a")
	r.SeedPackage("packages", "b")
	r.Commit("feat(a)!: only a changes, and it breaks")

	r.ReleaseOK()
	assert.True(t, r.HasTag("a@1.0.0"), "tags: %v", r.TagList())
	assert.True(t, r.HasTag("b@1.0.0"), "tags: %v", r.TagList())
	assert.Equal(t, 2, buildRuns(r), "both members build: the ride is a real release")
}

// TestVersioningMixedDepthGroupUsesTheDeepest: a package may override its
// space's versioning without leaving the space's group, which is how one
// group ends up with members sharing different parts of the version. The
// deepest declaration wins — it satisfies every member at once — and W213 is
// what explains the sharing the shallower member never asked for.
func TestVersioningMixedDepthGroupUsesTheDeepest(t *testing.T) {
	r := harness.New(t)
	cfg := spacesConfig(echoBuild, map[string]models.SpaceConfig{
		"libs": {Path: "packages", Versioning: models.VersioningFixedMajorMinor, Flow: buildPublish()},
	})
	cfg.Packages = map[string]models.PackageConfig{
		"b": {Versioning: models.VersioningFixedMajor},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "a")
	r.SeedPackage("packages", "b")

	r.Commit("feat(a): a minor only the deeper mode shares")
	res := r.ReleaseOK()
	assert.True(t, harness.HasCode(res.Events, "W213"),
		"the mixed depth must be reported: %s", res.Stdout)
	assert.True(t, r.HasTag("a@0.1.0"), "tags: %v", r.TagList())
	assert.True(t, r.HasTag("b@0.1.0"),
		"the group versions at the deepest depth, so the minor is shared; tags: %v", r.TagList())

	r.ReleaseOK()
	assert.Equal(t, 2, len(r.TagList()), "converged")
}
