package integration

// Area 27: the release edge cases that sit between two features, where each
// feature is right on its own and the interesting question is what the pair
// does. They are gathered here rather than filed under the areas they touch
// because each one is a claim about an *interaction*, and reading them
// together is what makes the interaction visible:
//
//   - an exact `Release-As` naming a prerelease, where the pin guards and the
//     channel rules meet;
//   - a release window where a provider and its consumer each changed for
//     their own reasons and no propagation syntax was written, so the two
//     auto-versioning strategies have to reconcile the manifests with no
//     `DueTo` link to work from;
//   - a package joining a versioning group with no version of its own, and
//     one joining with a version that outranks the group's;
//   - where revertOnFail stops, which is the boundary the whole failure model
//     rests on.
//
// Every one of them is a case where getting it wrong produces a *plausible*
// release rather than a failure, which is why they are worth a file.

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// tagAt writes the annotated release tag a scenario's baseline needs, in the
// shape a real release would have left behind.
func tagAt(r *harness.Repo, tag, rev string) {
	r.Git("tag", "-a", tag, "-m", "release "+tag, rev)
}

// ---------------------------------------------------------------------------
// Exact pins that name a prerelease
// ---------------------------------------------------------------------------

// TestEdgePinPrereleaseOfTheRequiredBump: cutting the next minor as an rc
// first is the ordinary reason to pin a prerelease, and the guard that stops a
// breaking change shipping as a patch (E156) must not read it as a downgrade.
// A prerelease ranks below its own core by SemVer precedence, so comparing the
// versions whole would reject the pin and fall back to releasing the stable
// 1.1.0 the operator was holding back: the guard misfiring into exactly the
// release nobody asked for, which a tag then makes permanent.
func TestEdgePinPrereleaseOfTheRequiredBump(t *testing.T) {
	r := singlePackageRepo(t, echoBuild)
	r.Commit("chore: seed")
	tagAt(r, "core@1.0.0", "HEAD")

	r.CommitEmpty("feat(core): a feature")
	r.CommitEmpty("release(core): cut an rc first\n\nRelease-As: 1.1.0-rc.0")

	res := r.ReleaseOK()

	assert.True(t, r.HasTag("core@1.1.0-rc.0"), "the pinned rc is what ships; tags: %v", r.TagList())
	assert.False(t, r.HasTag("core@1.1.0"), "the stable version must not be released instead")
	assert.False(t, harness.HasCodeForPackage(res.Events, "E156", "core"),
		"an rc of the required minor satisfies the bump; stdout:\n%s", res.Stdout)

	// The train continues from the tag that was just written, which is what
	// proves the pin really entered the rc channel rather than being read as a
	// one-off version.
	r.CommitEmpty("fix(core)%rc: another go")
	r.ReleaseOK()
	assert.True(t, r.HasTag("core@1.1.0-rc.1"), "the train continues; tags: %v", r.TagList())
}

// TestEdgePinPrereleaseBelowTheBumpIsStillRefused: the other half of the same
// rule. Measuring the guard on cores must not become a way to smuggle a
// breaking change out as an rc of a minor, so the pin is refused and the
// package falls back to the version the commits require.
func TestEdgePinPrereleaseBelowTheBumpIsStillRefused(t *testing.T) {
	r := singlePackageRepo(t, echoBuild)
	r.Commit("chore: seed")
	tagAt(r, "core@1.0.0", "HEAD")

	r.CommitEmpty("feat(core)!: a breaking change")
	r.CommitEmpty("release(core): try to soften it\n\nRelease-As: 1.1.0-rc.0")

	res := r.ReleaseOK()

	assert.True(t, harness.HasCodeForPackage(res.Events, "E156", "core"),
		"an rc of 1.1.0 does not carry a breaking change; stdout:\n%s", res.Stdout)
	assert.True(t, r.HasTag("core@2.0.0"),
		"the rejected pin contributes nothing and the computed version still ships; tags: %v", r.TagList())
}

// TestEdgePinPrereleaseMovesAWholeGroup: a versioning group runs one train, so
// a prerelease pin naming one member takes every member onto it. Without the
// core-wise guard the group's aggregate would collect the same E156 and the
// whole group would release stable instead, which is the same mistake
// multiplied by the number of members.
func TestEdgePinPrereleaseMovesAWholeGroup(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(spacesConfig(echoBuild, map[string]models.SpaceConfig{
		"libs": {Path: "packages", Versioning: models.VersioningFixed, Flow: buildPublish()},
	}))
	r.SeedPackage("packages", "a")
	r.SeedPackage("packages", "b")
	r.Commit("chore: seed both")
	tagAt(r, "a@1.0.0", "HEAD")
	tagAt(r, "b@1.0.0", "HEAD")

	r.CommitEmpty("feat(a): a feature")
	r.CommitEmpty("release(a): cut the group an rc\n\nRelease-As: 1.1.0-rc.0")

	res := r.ReleaseOK()

	assert.True(t, r.HasTag("a@1.1.0-rc.0"), "tags: %v", r.TagList())
	assert.True(t, r.HasTag("b@1.1.0-rc.0"), "the group runs one train; tags: %v", r.TagList())
	assert.False(t, r.HasTag("a@1.1.0"), "nothing graduates here")
	assert.True(t, harness.HasCodeForPackage(res.Events, "W210", "b"), "b rides the pin")
}

// ---------------------------------------------------------------------------
// A window where provider and consumer each changed, with no propagation
// ---------------------------------------------------------------------------

// TestEdgeAutoVersionSyncsWithoutPropagation drives the case a repository on
// the default settings meets constantly and nothing else in the suite states
// outright: propagation depth is 0 unless a unit or the configuration asks for
// more, so `feat(core)` moves nobody. Here core and web are changed by two
// *separate* commits, each releasing for its own reason, and web is therefore
// never `DueTo` core: there is no propagation link anywhere in the run.
//
// The manifest still declares the dependency, so it still has to be
// reconciled. The parsing strategy resolves providers from what the manifests
// declare rather than from what bumped the package, which is what makes it
// work with no link to follow; the version stage runs because the space
// auto-versions, not because anything propagated.
func TestEdgeAutoVersionSyncsWithoutPropagation(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {
			Path:        "packages",
			Flow:        buildPublish(),
			AutoVersion: &models.AutoVersionConfig{Match: []string{"^*"}},
		},
	}
	cfg.Dependencies = []models.DependencyConfig{{Consumer: "web", Provider: "core"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "web")
	r.WriteFile("packages/core/package.json", `{"name": "@acme/core", "version": "1.0.0"}`)
	r.WriteFile("packages/web/package.json", `{
  "name": "@acme/web",
  "version": "1.0.0",
  "dependencies": {"@acme/core": "^1.0.0"}
}`)
	r.Commit("chore: seed the manifests")
	tagAt(r, "core@1.0.0", "HEAD")
	tagAt(r, "web@1.0.0", "HEAD")

	// Two separate commits, no caret anywhere: each package releases for its
	// own reason and neither propagates to the other.
	r.CommitEmpty("feat(core): something in the provider")
	r.CommitEmpty("fix(web): something unrelated in the consumer")

	res := r.ReleaseOK()

	require.True(t, r.HasTag("core@1.1.0"), "tags: %v", r.TagList())
	require.True(t, r.HasTag("web@1.0.1"), "the consumer releases its own patch; tags: %v", r.TagList())

	web, err := os.ReadFile(r.Path("packages", "web", "package.json"))
	require.NoError(t, err)
	assert.Contains(t, string(web), `"@acme/core": "^1.1.0"`,
		"the declared range follows the provider with no propagation to carry it")
	assert.Contains(t, string(web), `"version": "1.0.1"`, "and the consumer's own version is its own")

	assert.False(t, harness.HasCodeForPackage(res.Events, "W221", "web"),
		"the edge is declared, so nothing is optimistic about it; stdout:\n%s", res.Stdout)
}

// TestEdgeAutoReplaceSyncsWithoutPropagation is the same window against the
// replacing strategy, which is what a space uses when nothing it ships parses
// as a manifest. Its `{provider}` patterns expand over the package's
// *configured* providers rather than over what bumped it, so it has the same
// answer for the same reason, reached by a different route.
func TestEdgeAutoReplaceSyncsWithoutPropagation(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {
			Path: "packages",
			Flow: buildPublish(),
			AutoVersion: &models.AutoVersionConfig{
				Manifests: "none",
				Replace: []models.AutoVersionReplaceConfig{{
					Files: []string{"build.gradle"},
					Find:  "com.acme:{provider}:{providerPrevious}",
					Write: "com.acme:{provider}:{providerVersion}",
				}},
			},
		},
	}
	cfg.Dependencies = []models.DependencyConfig{{Consumer: "web", Provider: "core"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "web")
	r.WriteFile("packages/web/build.gradle", "dependencies {\n  implementation 'com.acme:core:1.0.0'\n}\n")
	r.Commit("chore: seed the build script")
	tagAt(r, "core@1.0.0", "HEAD")
	tagAt(r, "web@1.0.0", "HEAD")

	r.CommitEmpty("feat(core): something in the provider")
	r.CommitEmpty("fix(web): something unrelated in the consumer")

	r.ReleaseOK()

	require.True(t, r.HasTag("core@1.1.0"), "tags: %v", r.TagList())
	require.True(t, r.HasTag("web@1.0.1"), "tags: %v", r.TagList())

	gradle, err := os.ReadFile(r.Path("packages", "web", "build.gradle"))
	require.NoError(t, err)
	assert.Contains(t, string(gradle), "com.acme:core:1.1.0",
		"the coordinate follows the provider with no propagation to carry it")
}

// TestEdgeVersionScriptWithoutPropagationRunsNoVersionStage states the
// asymmetry the two tests above are the other side of, so that nobody
// discovers it from a stale manifest.
//
// A hand-written `flow.version` script exists to consume `DISPAT_UPDATED_*`,
// and those name the providers the package was *bumped for*. With no
// propagation there are none, so the package has no version stage at all and
// the script does not run. The native strategies are unconditional precisely
// because they do not depend on that link, which is why a space syncing
// manifests by script wants a propagation depth set (or `autoVersion`).
func TestEdgeVersionScriptWithoutPropagationRunsNoVersionStage(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Scripts["stamp"] = `echo "$DISPAT_PACKAGE" >> ../../version.log`
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: "packages", Flow: &models.SpaceFlowConfig{
			Version: []string{"stamp"}, Build: []string{"build"}, Publish: []string{"publish"}}},
	}
	cfg.Dependencies = []models.DependencyConfig{{Consumer: "web", Provider: "core"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "web")
	r.Commit("chore: seed")
	tagAt(r, "core@1.0.0", "HEAD")
	tagAt(r, "web@1.0.0", "HEAD")

	r.CommitEmpty("feat(core): something in the provider")
	r.CommitEmpty("fix(web): something unrelated in the consumer")
	r.ReleaseOK()

	assert.NoFileExists(t, r.Path("version.log"),
		"no package was bumped for a provider, so nothing has a version stage")

	// The same script with propagation asked for: now web is DueTo core, and
	// the version stage it exists for runs.
	r.CommitEmpty("feat(core)^: this one reaches its consumers")
	r.ReleaseOK()

	log, err := os.ReadFile(r.Path("version.log"))
	require.NoError(t, err, "the propagated bump gives web a version stage")
	assert.Contains(t, string(log), "web")
}

// ---------------------------------------------------------------------------
// Joining a versioning group
// ---------------------------------------------------------------------------

// TestEdgeGroupNewcomerWithNoVersionJoinsAtTheGroupVersion: the ordinary way a
// package joins an established group. It has never published, so it has no
// version to reconcile against the group's and simply rides to whatever the
// group computes next. The claim worth making through the binary is the tag:
// its first release is the group's version, not the `0.0.1` a package versioned
// on its own history would have started at.
func TestEdgeGroupNewcomerWithNoVersionJoinsAtTheGroupVersion(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(spacesConfig(echoBuild, map[string]models.SpaceConfig{
		"libs": {Path: "packages", Versioning: models.VersioningFixed, Flow: buildPublish()},
	}))
	r.SeedPackage("packages", "a")
	r.Commit("chore: seed a")
	tagAt(r, "a@1.2.0", "HEAD")

	// The newcomer arrives with no tag of its own.
	r.SeedPackage("packages", "newbie")
	r.Commit("fix(a): a change in the established member")

	res := r.ReleaseOK()

	assert.True(t, r.HasTag("a@1.2.1"), "tags: %v", r.TagList())
	assert.True(t, r.HasTag("newbie@1.2.1"),
		"the newcomer joins at the group's version, not at 0.0.1; tags: %v", r.TagList())
	assert.True(t, harness.HasCodeForPackage(res.Events, "W210", "newbie"), "the ride is explained")
	assert.False(t, harness.HasCode(res.Events, "W233"),
		"a newcomer has no version to disagree about; stdout:\n%s", res.Stdout)

	// And it converges: the next run has nothing to say about either.
	r.CommitEmpty("chore: nothing to do")
	r.ReleaseOK()
	assert.Equal(t, 1, r.TagCount("newbie@"), "tags: %v", r.TagList())
}

// TestEdgeGroupMemberOnAnotherMajorIsReported: the expensive way to join.
// A group versions from its newest member, because no member may go backwards
// from what it has already published, so a package carrying a stray 9.0.0 does
// not join the group at 1.x: it takes the group to 9.x, in one run, with tags
// that §19.1 forbids moving back. Every version involved is genuinely
// published, so there is no other correct plan and the release proceeds; what
// the operator gets is W233 naming who decided it.
func TestEdgeGroupMemberOnAnotherMajorIsReported(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(spacesConfig(echoBuild, map[string]models.SpaceConfig{
		"libs": {Path: "packages", Versioning: models.VersioningFixed, Flow: buildPublish()},
	}))
	r.SeedPackage("packages", "a")
	r.SeedPackage("packages", "stray")
	r.Commit("chore: seed both")
	tagAt(r, "a@1.2.0", "HEAD")
	tagAt(r, "stray@9.0.0", "HEAD")

	r.CommitEmpty("fix(a): an ordinary fix")
	res := r.ReleaseOK()

	assert.True(t, r.HasTag("a@9.0.1"),
		"the group versions from its newest member; tags: %v", r.TagList())
	assert.True(t, r.HasTag("stray@9.0.1"), "tags: %v", r.TagList())
	assert.True(t, harness.HasCode(res.Events, "W233"),
		"the major spread must be reported; stdout:\n%s", res.Stdout)
}

// TestEdgeGroupMinorSpreadIsNotReported is the negative that keeps W233 worth
// reading. Members apart by a minor are the ordinary state a failed ride
// leaves behind, and the catch-up already has W210 to explain it: warning
// again here would put the code on almost every run of almost every group and
// train everyone to ignore it.
func TestEdgeGroupMinorSpreadIsNotReported(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(spacesConfig(echoBuild, map[string]models.SpaceConfig{
		"libs": {Path: "packages", Versioning: models.VersioningFixed, Flow: buildPublish()},
	}))
	r.SeedPackage("packages", "a")
	r.SeedPackage("packages", "laggard")
	r.Commit("chore: seed both")
	tagAt(r, "a@1.2.0", "HEAD")
	tagAt(r, "laggard@1.0.0", "HEAD")

	r.CommitEmpty("fix(a): an ordinary fix")
	res := r.ReleaseOK()

	assert.True(t, r.HasTag("a@1.2.1"), "tags: %v", r.TagList())
	assert.True(t, r.HasTag("laggard@1.2.1"), "the laggard is caught up; tags: %v", r.TagList())
	assert.True(t, harness.HasCodeForPackage(res.Events, "W210", "laggard"))
	assert.False(t, harness.HasCode(res.Events, "W233"),
		"below the major this is ordinary catch-up; stdout:\n%s", res.Stdout)
}

// ---------------------------------------------------------------------------
// Where revertOnFail stops
// ---------------------------------------------------------------------------

// TestEdgeRevertOnFailStopsAtThePublish pins the boundary the whole failure
// model rests on: revertOnFail cleans up a package that failed *before* its
// artefact went out, and nothing after that may roll a folder back.
//
// Once a publish has succeeded there is nothing left to decide and only things
// left to record, so every later failure (a release record, a tag, the release
// commit, the push) is a critical: logged with its code, collected, and
// reported in the exit status at the end. Reverting there would undo the
// build's work for a package already on its registry, and no amount of
// reverting un-publishes anything.
//
// One package fails at its build and one publishes, in the same run and the
// same revertOnFail space. The failed one's folder is clean; the published
// one's is not.
func TestEdgeRevertOnFailStopsAtThePublish(t *testing.T) {
	r := harness.New(t)
	cfg := harness.BaseFile(1)
	cfg.Scripts = map[string]string{
		"mutate-then-fail": "echo dirty > leftover.txt && exit 1",
		"mutate":           "echo dirty > leftover.txt",
		"publish":          "echo publishing",
	}
	cfg.RevertOnFail = models.Bool(true)
	cfg.Spaces = map[string]models.SpaceConfig{
		"good": {Path: "packages/good", Flow: &models.SpaceFlowConfig{
			Build: []string{"mutate"}, Publish: []string{"publish"}}},
		"bad": {Path: "packages/bad", Flow: &models.SpaceFlowConfig{
			Build: []string{"mutate-then-fail"}, Publish: []string{"publish"}}},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages/good", "good")
	r.SeedPackage("packages/bad", "bad")
	r.Commit("feat(good,bad): one publishes, one fails to build")

	res := r.Release()
	require.Equal(t, 1, res.Code, "the failed build fails the run\nstdout:\n%s", res.Stdout)

	assert.NoFileExists(t, r.Path("packages/bad/bad/leftover.txt"),
		"the package that failed before publishing is rolled back")
	assert.Zero(t, r.TagCount("bad@"), "and nothing failed is tagged")

	assert.FileExists(t, r.Path("packages/good/good/leftover.txt"),
		"the published package keeps what its build wrote: revert stops at the publish")
	assert.True(t, r.HasTag("good@0.1.0"), "tags: %v", r.TagList())
}

// TestEdgeRevertOnFailIsThreeStateAtThePackageLevel: revertOnFail is one of
// the tri-state booleans, and the distinction only pays off at the bottom of
// the ladder, where a package has to be able to say `false` against a `true`
// the root file already said. A plain bool could not carry that: unset and
// false would look the same, so the nearer layer could only ever turn the
// option on.
func TestEdgeRevertOnFailIsThreeStateAtThePackageLevel(t *testing.T) {
	r := harness.New(t)
	cfg := harness.BaseFile(1)
	cfg.Scripts = map[string]string{
		"mutate": "echo dirty > leftover.txt",
		"fail":   "exit 1",
	}
	// The root says yes; the space stays silent and inherits it.
	cfg.RevertOnFail = models.Bool(true)
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: "packages", Flow: &models.SpaceFlowConfig{
			Build: []string{"mutate"}, Publish: []string{"fail"}}},
	}
	// ...and one package says no, against the root's yes.
	cfg.Packages = map[string]models.PackageConfig{
		"keeper": {RevertOnFail: models.Bool(false)},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "keeper")
	r.SeedPackage("packages", "cleaner")
	r.Commit("feat(keeper,cleaner): both fail to publish")

	res := r.Release()
	require.Equal(t, 1, res.Code, "stdout:\n%s", res.Stdout)

	assert.FileExists(t, r.Path("packages/keeper/leftover.txt"),
		"the package's own false beats the root's true")
	assert.NoFileExists(t, r.Path("packages/cleaner/leftover.txt"),
		"the package that said nothing inherits the root's true")
}
