// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Goal 36: declared version groups across spaces, beyond the fixed and
// fixedMajor lifecycles goal 14 pins. The sparse and partial-sparse modes, a
// mixed-depth member override, a shared prerelease train, the none refusal,
// group selection under a partial mode, and the defined freedom of per-member
// tag formats: one shared version, each member spelling it its own way.

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models/v2"

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

// TestVersionGroupPartialReleaseCatchesUp: a run that dies between two
// members' publishes leaves the group split — one member's tag carries the
// shared work, the other still has it pending. The retry must not re-count
// that work against the group's published baseline: the laggard catches up at
// the version that already contains it, as its own release rather than a
// ride, and the member that published is not dragged into an empty re-release
// at the next minor.
func TestVersionGroupPartialReleaseCatchesUp(t *testing.T) {
	cfg := groupConfig(models.VersioningFixedMajorMinor)
	// The svc build fails while the marker exists, which is how the first run
	// dies on app1's leg after lib1 published.
	cfg.Scripts["flaky-build"] = models.Script{`[ ! -f ../../fail-app1 ] || exit 1`, echoBuild}
	cfg.Spaces["svc"] = models.SpaceConfig{
		Path: models.PathList{"services"}, VersionGroup: "platform",
		Flow: &models.SpaceFlowConfig{Build: []string{"flaky-build"}, Publish: []string{"publish"}},
	}
	r := seedGroupRepo(t, cfg)
	r.Commit("feat(lib1, app1): the shared feature")
	require.NoError(t, os.WriteFile(r.Path("fail-app1"), nil, 0o644))

	res := r.Release()
	require.Equal(t, 1, res.Code, "app1's leg must fail the run\nstdout:\n%s", res.Stdout)
	require.True(t, r.HasTag("lib1@0.1.0"), "lib1 published before the death; tags: %v", r.TagList())
	require.Zero(t, r.TagCount("app1@"), "app1's leg died; tags: %v", r.TagList())

	require.NoError(t, os.Remove(r.Path("fail-app1")))
	res = r.ReleaseOK()
	assert.True(t, r.HasTag("app1@0.1.0"),
		"app1 catches up at the version that already carries its work; tags: %v", r.TagList())
	assert.Equal(t, 1, r.TagCount("lib1@"),
		"lib1 published this work already and must not be re-released; tags: %v", r.TagList())
	assert.False(t, harness.HasCode(res.Events, "W234"),
		"the catch-up releases app1's own commits; there is no ride to explain")

	r.ReleaseOK()
	assert.Len(t, r.TagList(), 2, "converged: nothing left once both tags exist")
}

// TestVersionGroupPartialReleaseCatchUpAcrossModes: the partial-release
// catch-up in every shared-versioning mode. One commit scoped to both
// members, the second leg killed after the first published, and the retry
// must land the laggard at the version that already carries the shared work
// — never burn the next shared prefix on a re-count, never drag the member
// that published into an empty re-release. The commit is a feat where a
// minor moves the shared part, and a breaking change for the major-only
// modes, whose groups a minor never engages.
func TestVersionGroupPartialReleaseCatchUpAcrossModes(t *testing.T) {
	cases := []struct {
		mode      string
		incident  string // the commit whose ride fails halfway
		published string // where the holder lands, and the laggard must join
	}{
		{models.VersioningFixed, "feat(lib1, app1): shared work", "0.2.0"},
		{models.VersioningFixedSparse, "feat(lib1, app1): shared work", "0.2.0"},
		{models.VersioningFixedMajorMinor, "feat(lib1, app1): shared work", "0.2.0"},
		{models.VersioningFixedMajorMinorSparse, "feat(lib1, app1): shared work", "0.2.0"},
		{models.VersioningFixedMajor, "feat(lib1, app1)!: shared break", "2.0.0"},
		{models.VersioningFixedMajorSparse, "feat(lib1, app1)!: shared break", "2.0.0"},
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			cfg := groupConfig(tc.mode)
			cfg.Scripts["flaky-build"] = models.Script{`[ ! -f ../../fail-app1 ] || exit 1`, echoBuild}
			cfg.Spaces["svc"] = models.SpaceConfig{
				Path: models.PathList{"services"}, VersionGroup: "platform",
				Flow: &models.SpaceFlowConfig{Build: []string{"flaky-build"}, Publish: []string{"publish"}},
			}
			if tc.published == "2.0.0" {
				// The major-only modes need a shared major for the break to
				// leave; initials put the group's first release onto 1.x.
				cfg.Initials = map[string]string{"lib1": "1.0.0", "app1": "1.0.0"}
			}
			r := seedGroupRepo(t, cfg)
			r.Commit("feat(lib1, app1): bootstrap")
			r.ReleaseOK()

			r.CommitEmpty(tc.incident)
			require.NoError(t, os.WriteFile(r.Path("fail-app1"), nil, 0o644))
			res := r.Release()
			require.Equal(t, 1, res.Code, "app1's leg must fail\nstdout:\n%s", res.Stdout)
			require.True(t, r.HasTag("lib1@"+tc.published), "tags: %v", r.TagList())
			require.Zero(t, r.TagCount("app1@"+tc.published), "tags: %v", r.TagList())

			require.NoError(t, os.Remove(r.Path("fail-app1")))
			res = r.ReleaseOK()
			assert.True(t, r.HasTag("app1@"+tc.published),
				"%s: the laggard joins at the published version; tags: %v", tc.mode, r.TagList())
			assert.Equal(t, 1, r.TagCount("lib1@"+tc.published),
				"%s: the holder is not re-released; tags: %v", tc.mode, r.TagList())
			assert.False(t, harness.HasCode(res.Events, "W234"),
				"%s: the laggard releases its own commits, nobody rides", tc.mode)

			before := len(r.TagList())
			r.ReleaseOK()
			assert.Len(t, r.TagList(), before, "%s: converged", tc.mode)
		})
	}
}

// TestVersionGroupCauselessLaggardRidesToThePublishedVersion: the other
// catch-up flavour, end to end. The laggard's failed leg was a *ride* — no
// commits of its own — so the retry rides it up to the group's published
// version with W234 explaining it, exactly as a member left behind by any
// failed ride has always been re-aligned.
func TestVersionGroupCauselessLaggardRidesToThePublishedVersion(t *testing.T) {
	cfg := groupConfig(models.VersioningFixedMajorMinor)
	cfg.Scripts["flaky-build"] = models.Script{`[ ! -f ../../fail-app1 ] || exit 1`, echoBuild}
	cfg.Spaces["svc"] = models.SpaceConfig{
		Path: models.PathList{"services"}, VersionGroup: "platform",
		Flow: &models.SpaceFlowConfig{Build: []string{"flaky-build"}, Publish: []string{"publish"}},
	}
	// The dependency beside the group membership is the production shape
	// (the docs site depends on the CLI it groups with), and it is what the
	// ride's entry documents its movement through.
	cfg.Dependencies = models.Dependencies{{Consumer: "app1", Provider: "lib1"}}
	r := seedGroupRepo(t, cfg)
	r.Commit("feat(lib1, app1): bootstrap")
	r.ReleaseOK()

	r.CommitEmpty("feat(lib1): lib1 moves the group; app1's ride will die")
	require.NoError(t, os.WriteFile(r.Path("fail-app1"), nil, 0o644))
	res := r.Release()
	require.Equal(t, 1, res.Code, "the ride must fail\nstdout:\n%s", res.Stdout)
	require.True(t, r.HasTag("lib1@0.2.0"), "tags: %v", r.TagList())

	require.NoError(t, os.Remove(r.Path("fail-app1")))
	res = r.ReleaseOK()
	assert.True(t, r.HasTag("app1@0.2.0"), "tags: %v", r.TagList())
	assert.True(t, harness.HasCodeForPackage(res.Events, "W234", "app1"),
		"a cause-less catch-up is a ride, and the ride is explained")
	assert.Equal(t, 1, r.TagCount("lib1@0.2.0"), "the holder is not re-released; tags: %v", r.TagList())

	entry := entryOf(t, spacedChangelog(t, r, "services", "app1"), "app1@0.2.0")
	assert.Contains(t, entry, "- lib1: 0.1.0 -> 0.2.0",
		"the ride's entry spans the movement it rode for, from app1's last release")
	assert.NotContains(t, entry, "0.2.0 -> 0.2.0")
}

// TestVersionGroupPartialReleaseTwoLaggards: one holder, two failed legs.
// Both laggards catch up at the published version in a single retry, so a
// badly interrupted run needs exactly one more, not one per member.
func TestVersionGroupPartialReleaseTwoLaggards(t *testing.T) {
	cfg := groupConfig(models.VersioningFixedMajorMinor)
	cfg.Scripts["flaky-build"] = models.Script{`[ ! -f "../../fail-$DISPAT_PACKAGE" ] || exit 1`, echoBuild}
	cfg.Spaces["svc"] = models.SpaceConfig{
		Path: models.PathList{"services"}, VersionGroup: "platform",
		Flow: &models.SpaceFlowConfig{Build: []string{"flaky-build"}, Publish: []string{"publish"}},
	}
	r := seedGroupRepo(t, cfg)
	r.SeedPackage("services", "app2")
	r.Commit("feat(lib1, app1, app2): bootstrap")
	r.ReleaseOK()

	r.CommitEmpty("feat(lib1, app1, app2): shared work, two legs will die")
	require.NoError(t, os.WriteFile(r.Path("fail-app1"), nil, 0o644))
	require.NoError(t, os.WriteFile(r.Path("fail-app2"), nil, 0o644))
	res := r.Release()
	require.Equal(t, 1, res.Code, "stdout:\n%s", res.Stdout)
	require.True(t, r.HasTag("lib1@0.2.0"), "tags: %v", r.TagList())

	require.NoError(t, os.Remove(r.Path("fail-app1")))
	require.NoError(t, os.Remove(r.Path("fail-app2")))
	res = r.ReleaseOK()
	assert.True(t, r.HasTag("app1@0.2.0"), "tags: %v", r.TagList())
	assert.True(t, r.HasTag("app2@0.2.0"), "tags: %v", r.TagList())
	assert.Equal(t, 1, r.TagCount("lib1@0.2.0"), "tags: %v", r.TagList())
	assert.False(t, harness.HasCode(res.Events, "W234"), "both laggards carry their own commits")

	before := len(r.TagList())
	r.ReleaseOK()
	assert.Len(t, r.TagList(), before, "converged")
}

// TestVersionGroupPartialReleaseNewerWorkMovesOn: the mask reaches exactly
// as far as the published tag. Work that lands after the partial release is
// new, so the retry moves the prefix for it — the laggard releases both its
// caught-up and its new work at the next minor, and the erstwhile holder
// rides up to it.
func TestVersionGroupPartialReleaseNewerWorkMovesOn(t *testing.T) {
	cfg := groupConfig(models.VersioningFixedMajorMinor)
	cfg.Scripts["flaky-build"] = models.Script{`[ ! -f ../../fail-app1 ] || exit 1`, echoBuild}
	cfg.Spaces["svc"] = models.SpaceConfig{
		Path: models.PathList{"services"}, VersionGroup: "platform",
		Flow: &models.SpaceFlowConfig{Build: []string{"flaky-build"}, Publish: []string{"publish"}},
	}
	r := seedGroupRepo(t, cfg)
	r.Commit("feat(lib1, app1): bootstrap")
	r.ReleaseOK()

	r.CommitEmpty("feat(lib1, app1): shared work, app1's leg will die")
	require.NoError(t, os.WriteFile(r.Path("fail-app1"), nil, 0o644))
	require.Equal(t, 1, r.Release().Code)
	require.True(t, r.HasTag("lib1@0.2.0"), "tags: %v", r.TagList())
	require.NoError(t, os.Remove(r.Path("fail-app1")))

	r.CommitEmpty("feat(app1): new work since the partial release")
	res := r.ReleaseOK()
	assert.True(t, r.HasTag("app1@0.3.0"),
		"fresh work owns the next minor; tags: %v", r.TagList())
	assert.True(t, r.HasTag("lib1@0.3.0"),
		"the moved prefix takes the group along; tags: %v", r.TagList())
	assert.True(t, harness.HasCodeForPackage(res.Events, "W234", "lib1"),
		"lib1's re-release is a ride this time, and it is explained")
	assert.Zero(t, r.TagCount("app1@0.2.0"),
		"the laggard never lands on the version it skipped past; tags: %v", r.TagList())
}

// TestVersionGroupTrainPartialReleaseAdvancesTheTrain: a partial release on
// a prerelease train. The catch-up masking is deliberately confined to
// stable group baselines — a train's window spans work its own prereleases
// shipped (§11.4), and re-measuring it against the holder's tag would fight
// that. So the retry does what trains do: it advances, releasing the laggard
// at the next prerelease with the holder riding beside it. A burned
// prerelease counter is the train's ordinary currency, not the burned minor
// the stable-line masking exists to prevent.
func TestVersionGroupTrainPartialReleaseAdvancesTheTrain(t *testing.T) {
	cfg := groupConfig(models.VersioningFixedMajorMinor)
	cfg.Scripts["flaky-build"] = models.Script{`[ ! -f ../../fail-app1 ] || exit 1`, echoBuild}
	cfg.Spaces["svc"] = models.SpaceConfig{
		Path: models.PathList{"services"}, VersionGroup: "platform",
		Flow: &models.SpaceFlowConfig{Build: []string{"flaky-build"}, Publish: []string{"publish"}},
	}
	r := seedGroupRepo(t, cfg)
	r.Commit("feat(lib1, app1): bootstrap")
	r.ReleaseOK()

	r.CommitEmpty("feat(lib1, app1)%beta: the group boards a train, one leg dies")
	require.NoError(t, os.WriteFile(r.Path("fail-app1"), nil, 0o644))
	res := r.Release()
	require.Equal(t, 1, res.Code, "stdout:\n%s", res.Stdout)
	require.True(t, r.HasTag("lib1@0.2.0-beta.0"), "tags: %v", r.TagList())

	require.NoError(t, os.Remove(r.Path("fail-app1")))
	r.ReleaseOK()
	assert.True(t, r.HasTag("app1@0.2.0-beta.1"),
		"the laggard boards at the train's next stop; tags: %v", r.TagList())
	assert.True(t, r.HasTag("lib1@0.2.0-beta.1"),
		"the holder rides the advanced train beside it; tags: %v", r.TagList())
	assert.Zero(t, r.TagCount("app1@0.2.0-beta.0"),
		"the published prerelease is the holder's alone; tags: %v", r.TagList())
}
