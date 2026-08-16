package integration

// Goal 36: declared version groups across spaces, beyond the fixed and
// fixedMajor lifecycles goal 14 pins. The sparse and partial-sparse modes, a
// mixed-depth member override, a shared prerelease train, the none refusal,
// group selection under a partial mode, and the defined freedom of per-member
// tag formats: one shared version, each member spelling it its own way.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// groupConfig is the two-space fixture every test here starts from: packages/
// and services/, both joined to a declared group "platform" of the given
// mode, with the plain build/publish flow.
func groupConfig(mode string) models.File {
	cfg := harness.BaseFile(1)
	cfg.Scripts = map[string]models.Script{"build": {echoBuild}, "publish": {"echo publishing"}}
	cfg.VersionGroups = map[string]models.VersionGroupConfig{"platform": {Versioning: mode}}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: models.PathList{"packages"}, Flow: buildPublish(), VersionGroup: "platform"},
		"svc":  {Path: models.PathList{"services"}, Flow: buildPublish(), VersionGroup: "platform"},
	}
	return cfg
}

// seedGroupRepo writes the config and one package per space.
func seedGroupRepo(t *testing.T, cfg models.File) *harness.Repo {
	t.Helper()
	r := harness.New(t)
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "lib1")
	r.SeedPackage("services", "app1")
	return r
}

// TestVersionGroupSparseAcrossSpaces: a declared fixedSparse group shares the
// version without back-filling it — an untouched member in the other space
// does not ride, and when it finally changes it joins at the group's version,
// skipping the ones it sat out.
func TestVersionGroupSparseAcrossSpaces(t *testing.T) {
	r := seedGroupRepo(t, groupConfig(models.VersioningFixedSparse))
	r.Commit("feat(lib1): moves only itself")

	res := r.ReleaseOK()
	assert.True(t, r.HasTag("lib1@0.1.0"), "tags: %v", r.TagList())
	assert.Zero(t, r.TagCount("app1@"), "sparse: an untouched member does not ride; tags: %v", r.TagList())
	assert.False(t, harness.HasCode(res.Events, "W234"), "no ride, so nothing to explain")

	r.CommitEmpty("feat(app1): now app1 moves")
	r.ReleaseOK()
	assert.True(t, r.HasTag("app1@0.2.0"),
		"the member joins at the group's next version, skipping 0.1.0; tags: %v", r.TagList())
	assert.Equal(t, 1, r.TagCount("lib1@"), "the other member sat this one out")

	r.ReleaseOK()
	assert.Len(t, r.TagList(), 2, "converged")
}

// TestVersionGroupPartialSparseAcrossSpaces: fixedMajorMinorSparse shares only
// major.minor, and sparsely. A patch stays inside its member; a minor moves
// the shared part without dragging the other space along; the laggard joins
// at the shared part when it next changes.
func TestVersionGroupPartialSparseAcrossSpaces(t *testing.T) {
	r := seedGroupRepo(t, groupConfig(models.VersioningFixedMajorMinorSparse))
	r.Commit("feat(lib1): begin")
	r.CommitEmpty("feat(app1): begin")
	r.ReleaseOK()
	require.True(t, r.HasTag("lib1@0.1.0"), "tags: %v", r.TagList())
	require.True(t, r.HasTag("app1@0.1.0"), "tags: %v", r.TagList())

	r.CommitEmpty("fix(lib1): a patch below the shared part")
	r.ReleaseOK()
	assert.True(t, r.HasTag("lib1@0.1.1"), "tags: %v", r.TagList())
	assert.Equal(t, 1, r.TagCount("app1@"), "a patch never crosses a major.minor group")

	r.CommitEmpty("feat(lib1): a minor moves the shared part")
	res := r.ReleaseOK()
	assert.True(t, r.HasTag("lib1@0.2.0"), "tags: %v", r.TagList())
	assert.Equal(t, 1, r.TagCount("app1@"), "sparse: the other space still does not ride")
	assert.False(t, harness.HasCode(res.Events, "W234"))

	r.CommitEmpty("fix(app1): the laggard changes at last")
	r.ReleaseOK()
	assert.True(t, r.HasTag("app1@0.2.0"),
		"the member joins at the group's shared part; tags: %v", r.TagList())
}

// TestVersionGroupMemberOverrideLeavesTheGroup: versioning and versionGroup
// are one axis of the override ladder, so a package-level versioning on a
// declared group's member supersedes the membership its space joined — the
// package leaves the group rather than dragging a depth conflict into it.
// (Mixed depth inside one group is the implicit space group's shape, fenced
// by TestVersioningMixedDepthGroupUsesTheDeepest.)
func TestVersionGroupMemberOverrideLeavesTheGroup(t *testing.T) {
	cfg := groupConfig(models.VersioningFixed)
	cfg.Packages = map[string]models.PackageConfig{
		"app1": {Versioning: models.VersioningIndependent},
	}
	r := seedGroupRepo(t, cfg)
	r.Commit("feat(lib1): moves the group, not the detached member")

	res := r.ReleaseOK()
	assert.True(t, r.HasTag("lib1@0.1.0"), "tags: %v", r.TagList())
	assert.Zero(t, r.TagCount("app1@"), "the overridden member no longer rides; tags: %v", r.TagList())
	assert.False(t, harness.HasCode(res.Events, "W234"))

	// Its own change versions it on its own line, not at the group's next.
	r.CommitEmpty("feat(app1): its own first release")
	r.ReleaseOK()
	assert.True(t, r.HasTag("app1@0.1.0"),
		"independent: app1 starts its own line instead of joining the group at 0.2.0; tags: %v", r.TagList())
}

// TestVersionGroupPrereleaseTrain: a prerelease train runs across the whole
// declared group — one shared counter, not one per member — a member asking
// for a different channel while the group moves is W236, and graduation lands
// every member on the same stable version.
func TestVersionGroupPrereleaseTrain(t *testing.T) {
	r := seedGroupRepo(t, groupConfig(models.VersioningFixed))
	r.Commit("feat(lib1)%beta: begin the train")

	r.ReleaseOK()
	assert.True(t, r.HasTag("lib1@0.1.0-beta.0"), "tags: %v", r.TagList())
	assert.True(t, r.HasTag("app1@0.1.0-beta.0"), "the ride shares the train; tags: %v", r.TagList())

	r.CommitEmpty("feat(lib1)%beta: more on the train")
	r.ReleaseOK()
	assert.True(t, r.HasTag("lib1@0.1.0-beta.1"), "one shared counter; tags: %v", r.TagList())
	assert.True(t, r.HasTag("app1@0.1.0-beta.1"), "tags: %v", r.TagList())

	r.CommitEmpty("fix(lib1)%beta>stable: graduate the train")
	r.ReleaseOK()
	assert.True(t, r.HasTag("lib1@0.1.0"), "graduated; tags: %v", r.TagList())
	assert.True(t, r.HasTag("app1@0.1.0"), "the whole group graduates together; tags: %v", r.TagList())

	// One window, two channels: the group moves as one, and the member whose
	// channel lost is told so. rc rather than alpha, because a train can only
	// move up its own prerelease ordering.
	r.CommitEmpty("feat(lib1)%beta: the next train")
	r.CommitEmpty("feat(app1)%rc: asks for another channel")
	res := r.ReleaseOK()
	assert.True(t, harness.HasCode(res.Events, "W236"),
		"divergent member channels while the group moves are said out loud: %s", res.Stdout)
}

// TestVersionGroupRefusesNone: a declared group with versioning "none" is a
// config error through the binary, not just the loader's unit tests — a group
// exists to share versions, and none shares nothing.
func TestVersionGroupRefusesNone(t *testing.T) {
	cfg := groupConfig(models.VersioningNone)
	r := seedGroupRepo(t, cfg)
	r.Commit("chore: scaffolding")

	res := r.Command("status")
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stdout+res.Stderr, "a group exists to share versions")
}

// TestGroupFilterPartialMode: --group under a partial mode selects the whole
// group, and the partial mode keeps meaning what it means inside the
// selection — a breaking change moves every member (a ride, W234), a minor of
// one member releases that member alone with no ride to explain, and the
// aligned group converges.
func TestGroupFilterPartialMode(t *testing.T) {
	r := seedGroupRepo(t, groupConfig(models.VersioningFixedMajor))
	r.Commit("feat(lib1)!: a breaking change")

	res := r.ReleaseOK("--group", "platform")
	assert.True(t, r.HasTag("lib1@1.0.0"), "tags: %v", r.TagList())
	assert.True(t, r.HasTag("app1@1.0.0"), "the shared major moves both spaces; tags: %v", r.TagList())
	assert.True(t, harness.HasCodeForPackage(res.Events, "W234", "app1"),
		"the ride is explained: %s", res.Stdout)

	r.CommitEmpty("feat(lib1): a minor of lib1's own")
	res = r.ReleaseOK("--group", "platform")
	assert.True(t, r.HasTag("lib1@1.1.0"), "tags: %v", r.TagList())
	assert.Equal(t, 1, r.TagCount("app1@"),
		"below the shared major the members stay independent, --group or not")
	assert.False(t, harness.HasCode(res.Events, "W234"), "no ride below the shared major")

	r.ReleaseOK("--group", "platform")
	assert.Len(t, r.TagList(), 3, "converged")
}

// TestVersionGroupDivergentTagFormats: a version group shares the version,
// not its spelling. Each member renders the shared version through its own
// space's tagFormat, and that is defined behavior, not a conflict: no
// diagnostic beyond the ride's own W234 — on the stable line and along a
// whole prerelease train, whose baselines each member reads back out of its
// own spelling.
func TestVersionGroupDivergentTagFormats(t *testing.T) {
	cfg := groupConfig(models.VersioningFixed)
	libs := cfg.Spaces["libs"]
	libs.TagFormat = "{name}-v{version}"
	cfg.Spaces["libs"] = libs
	r := seedGroupRepo(t, cfg)
	r.Commit("feat(lib1): moves the whole group")

	res := r.ReleaseOK()
	assert.True(t, r.HasTag("lib1-v0.1.0"), "libs spells its tags its own way; tags: %v", r.TagList())
	assert.True(t, r.HasTag("app1@0.1.0"), "svc keeps the default spelling; tags: %v", r.TagList())
	for _, e := range res.Events {
		assert.NotEqual(t, "error", e["level"], "one version, two spellings, no error: %+v", e)
	}

	r.ReleaseOK()
	assert.Len(t, r.TagList(), 2, "converged")

	// The same freedom holds on a train: two spellings of every prerelease,
	// one shared counter, one graduation.
	r.CommitEmpty("feat(lib1)%rc: board the train")
	r.ReleaseOK()
	assert.True(t, r.HasTag("lib1-v0.2.0-rc.0"), "tags: %v", r.TagList())
	assert.True(t, r.HasTag("app1@0.2.0-rc.0"), "tags: %v", r.TagList())

	r.CommitEmpty("fix(lib1)%rc: more on the train")
	r.ReleaseOK()
	assert.True(t, r.HasTag("lib1-v0.2.0-rc.1"), "each member reads its own spelling back; tags: %v", r.TagList())
	assert.True(t, r.HasTag("app1@0.2.0-rc.1"), "tags: %v", r.TagList())

	r.CommitEmpty("fix(lib1)%rc>stable: graduate")
	r.ReleaseOK()
	assert.True(t, r.HasTag("lib1-v0.2.0"), "tags: %v", r.TagList())
	assert.True(t, r.HasTag("app1@0.2.0"), "tags: %v", r.TagList())
	r.ReleaseOK()
	assert.Len(t, r.TagList(), 8, "converged after the train")
}

// TestVersionGroupExactPinMidTrain: an exact Release-As naming one member
// while the group is mid-train moves the whole group onto the pinned version
// — one shared version admits no private jump. Graduating such a train then
// takes two facts of the spec together: §11.5 computes the graduation from
// the stable baseline and the window alone, so after a raising pin the naive
// graduation is refused as backwards (E185, repository-scoped), and the
// operator's way out is pinning the graduation itself — Release-As: auto
// resumes holds, not this.
func TestVersionGroupExactPinMidTrain(t *testing.T) {
	r := seedGroupRepo(t, groupConfig(models.VersioningFixed))
	r.Commit("feat(lib1)%rc: board the train")
	r.ReleaseOK()
	require.True(t, r.HasTag("lib1@0.1.0-rc.0"), "tags: %v", r.TagList())
	require.True(t, r.HasTag("app1@0.1.0-rc.0"), "tags: %v", r.TagList())

	r.CommitEmpty("release(lib1): jump the train\n\nRelease-As: 1.0.0-rc.0")
	res := r.ReleaseOK()
	assert.True(t, r.HasTag("lib1@1.0.0-rc.0"), "the pin moves the train; tags: %v", r.TagList())
	assert.True(t, r.HasTag("app1@1.0.0-rc.0"), "and the whole group with it; tags: %v", r.TagList())
	assert.True(t, harness.HasCodeForPackage(res.Events, "W234", "app1"),
		"the ride is explained: %s", res.Stdout)

	// A plain graduation computes 0.1.0 off the stable baseline, which sits
	// below the train the pin raised: E185, and nothing releases.
	r.CommitEmpty("fix(lib1)%rc>stable: graduate")
	blocked := r.Release()
	assert.NotEqual(t, 0, blocked.Code, "a backwards graduation is repository-scoped")
	assert.True(t, harness.HasCode(blocked.Events, "E185"), "out: %s", blocked.Stdout)
	assert.Equal(t, 2, r.TagCount("lib1@"), "nothing released; tags: %v", r.TagList())

	// The remedy the diagnostic reference names: pin the graduation.
	r.CommitEmpty("release(lib1)%rc>stable: graduate at the pin\n\nRelease-As: 1.0.0")
	r.ReleaseOK()
	assert.True(t, r.HasTag("lib1@1.0.0"), "the train graduates where the pin put it; tags: %v", r.TagList())
	assert.True(t, r.HasTag("app1@1.0.0"), "tags: %v", r.TagList())

	r.ReleaseOK()
	assert.Equal(t, 3, r.TagCount("lib1@"), "converged: %v", r.TagList())
}

// TestVersionGroupSparseMemberPin: under a sparse mode a pin moves the shared
// version without back-filling it — the untouched member stays where it is,
// and when it finally changes it joins at the group's next version above the
// pin, skipping everything it sat out.
func TestVersionGroupSparseMemberPin(t *testing.T) {
	r := seedGroupRepo(t, groupConfig(models.VersioningFixedSparse))
	r.Commit("feat(lib1): begin")
	r.ReleaseOK()
	require.True(t, r.HasTag("lib1@0.1.0"), "tags: %v", r.TagList())
	require.Zero(t, r.TagCount("app1@"), "sparse: nothing rides")

	r.CommitEmpty("release(lib1): jump\n\nRelease-As: 1.0.0")
	r.ReleaseOK()
	assert.True(t, r.HasTag("lib1@1.0.0"), "tags: %v", r.TagList())
	assert.Zero(t, r.TagCount("app1@"), "a pin does not back-fill a sparse member; tags: %v", r.TagList())

	r.CommitEmpty("feat(app1): the laggard changes at last")
	r.ReleaseOK()
	assert.True(t, r.HasTag("app1@1.1.0"),
		"the member joins above the pin, skipping what it sat out; tags: %v", r.TagList())
	assert.Equal(t, 2, r.TagCount("lib1@"), "the pinned member sat this one out")
}

// TestVersionGroupNoneMemberIsScriptOnly: the polyglot-monorepo shape — a
// package whose folder sits in a group-joined space but which itself does not
// version (versioning "none"). The override supersedes the membership, so the
// package leaves the group as script-only: the group moves without it, it is
// never tagged, and its scripts still run.
func TestVersionGroupNoneMemberIsScriptOnly(t *testing.T) {
	cfg := groupConfig(models.VersioningFixed)
	cfg.Scripts["build"] = models.Script{markerBuild}
	cfg.Packages = map[string]models.PackageConfig{
		"app1": {Versioning: models.VersioningNone},
	}
	r := seedGroupRepo(t, cfg)
	r.Commit("feat(lib1): moves the group\n---\nfeat(app1): script-only work")

	res := r.ReleaseOK()
	assert.True(t, r.HasTag("lib1@0.1.0"), "tags: %v", r.TagList())
	assert.Zero(t, r.TagCount("app1@"), "a none package is never tagged; tags: %v", r.TagList())
	line := harness.GraphLine(res.Events, "app1")
	assert.Contains(t, line.Str("message"), "script-only",
		"the graph names what the package is: %s", res.Stdout)
	assert.Equal(t, 1, buildRuns(r), "a release builds only the versioned member")

	// The none package's work still runs — through the run window, which
	// carries a changed none package whether or not anything is releasing.
	r.RunScriptOK("build")
	assert.Equal(t, 2, buildRuns(r), "the run window carries the script-only member")

	r.ReleaseOK()
	assert.Len(t, r.TagList(), 1, "converged")
}

// TestVersionGroupMixedDepthTrain: mixed shared depth is the implicit space
// group's shape (a package override on a declared group's member leaves the
// group instead — see TestVersionGroupMemberOverrideLeavesTheGroup). A member
// declaring fixedMajorMinor inside a fixedMajor space is resolved to the
// deepest declaration with W237 — and the resolution holds while a prerelease
// train runs, which is when a depth disagreement would otherwise split the
// shared counter.
func TestVersionGroupMixedDepthTrain(t *testing.T) {
	r := harness.New(t)
	cfg := spacesConfig(echoBuild, map[string]models.SpaceConfig{
		"libs": {Path: models.PathList{"packages"}, Versioning: models.VersioningFixedMajor, Flow: buildPublish()},
	})
	cfg.Packages = map[string]models.PackageConfig{
		"app1": {Versioning: models.VersioningFixedMajorMinor},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "lib1")
	r.SeedPackage("packages", "app1")
	r.Commit("feat(lib1)%rc: a minor only the deeper mode shares")

	res := r.ReleaseOK()
	assert.True(t, harness.HasCode(res.Events, "W237"),
		"the mixed depth must be reported: %s", res.Stdout)
	assert.True(t, r.HasTag("lib1@0.1.0-rc.0"), "tags: %v", r.TagList())
	assert.True(t, r.HasTag("app1@0.1.0-rc.0"),
		"the deepest depth shares the minor, so the train carries both; tags: %v", r.TagList())

	r.CommitEmpty("fix(lib1)%rc: more on the train")
	r.ReleaseOK()
	assert.True(t, r.HasTag("lib1@0.1.0-rc.1"), "one shared counter; tags: %v", r.TagList())
	assert.True(t, r.HasTag("app1@0.1.0-rc.1"), "tags: %v", r.TagList())

	r.CommitEmpty("fix(lib1)%rc>stable: graduate")
	r.ReleaseOK()
	assert.True(t, r.HasTag("lib1@0.1.0"), "tags: %v", r.TagList())
	assert.True(t, r.HasTag("app1@0.1.0"), "the whole group graduates; tags: %v", r.TagList())
}
