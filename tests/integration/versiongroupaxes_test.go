// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Goal 56: a versioning group's sharing axes. A group holds a version prefix
// in common; whether its members also hold one prerelease counter and one
// channel is two further choices, and the three of them together decide what
// a release, a retry and a graduation do to the members nobody named.
//
// The defaults are the behaviour goals 14 and 36 already pin, so what lives
// here is what an opted-in group does differently, plus the two shapes the
// defaults themselves got wrong: a member resting off the train deciding the
// group's channel, and a half-finished graduation that could not be retried.

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// axesConfig is this goal's fixture: three packages in three spaces, all
// joined to one declared group under the stated rule, with app1 depending on
// lib1 so publication order and manifest reconciliation are exercised beside
// the versioning. app1's build reads a marker file, which is how a leg is
// made to fail on demand.
func axesConfig(rule models.VersionGroupConfig) models.File {
	cfg := harness.BaseFile(1)
	cfg.Scripts = map[string]models.Script{
		"build":       {echoBuild},
		"publish":     {"echo publishing"},
		"flaky-build": {`[ ! -f ../../fail-app1 ] || exit 1`, echoBuild},
	}
	cfg.VersionGroups = map[string]models.VersionGroupConfig{"platform": rule}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: models.PathList{"packages"}, Flow: buildPublish(), VersionGroup: "platform"},
		"svc": {Path: models.PathList{"services"}, VersionGroup: "platform",
			Flow: &models.SpaceFlowConfig{Build: []string{"flaky-build"}, Publish: []string{"publish"}}},
		"docs": {Path: models.PathList{"documentation"}, Flow: buildPublish(), VersionGroup: "platform"},
	}
	cfg.Dependencies = []models.DependencyConfig{{Consumer: "app1", Provider: "lib1"}}
	cfg.Initials = map[string]string{"lib1": "1.0.0", "app1": "1.0.0", "docs1": "1.0.0"}
	return cfg
}

// seedAxesRepo writes the config and one package per space.
func seedAxesRepo(t *testing.T, rule models.VersionGroupConfig) *harness.Repo {
	t.Helper()
	r := harness.New(t)
	r.WriteConfigModel(axesConfig(rule))
	r.SeedPackage("packages", "lib1")
	r.SeedPackage("services", "app1")
	r.SeedPackage("documentation", "docs1")
	return r
}

// libChangelog reads lib1's changelog, empty when it has none yet.
func libChangelog(t *testing.T, r *harness.Repo) string {
	t.Helper()
	body, err := os.ReadFile(r.Path("packages", "lib1", "CHANGELOG.md"))
	if os.IsNotExist(err) {
		return ""
	}
	require.NoError(t, err)
	return string(body)
}

// TestVersionGroupIndependentCounterRetriesOnlyTheFailedLeg walks the whole
// release cycle of an opted-in group: onto a train together, a leg that dies,
// a retry that owes only that leg, a fix that moves one package, the next
// train that moves all of them, and convergence.
func TestVersionGroupIndependentCounterRetriesOnlyTheFailedLeg(t *testing.T) {
	r := seedAxesRepo(t, models.VersionGroupConfig{
		Versioning: models.VersioningFixedMajorMinor,
		Counter:    models.SharingIndependent,
		Channels:   models.SharingIndependent,
	})

	// Run 1: the shared prefix moves, so every member moves with it.
	r.Commit("feat(lib1, app1, docs1): bootstrap the platform")
	r.ReleaseOK()
	for _, tag := range []string{"lib1@1.1.0", "app1@1.1.0", "docs1@1.1.0"} {
		assert.True(t, r.IsTagged(tag), "tags: %v", r.TagList())
	}

	// Run 2: the group boards a train together and app1's leg dies.
	r.CommitEmpty("feat(lib1, app1, docs1)%beta: board the train")
	require.NoError(t, os.WriteFile(r.Path("fail-app1"), nil, 0o644))
	res := r.Release()
	require.Equal(t, 1, res.Code, "stdout:\n%s", res.Stdout)
	assert.True(t, r.IsTagged("lib1@1.2.0-beta.0"), "tags: %v", r.TagList())
	assert.True(t, r.IsTagged("docs1@1.2.0-beta.0"), "tags: %v", r.TagList())
	assert.Zero(t, r.TagCount("app1@1.2.0"), "app1's leg failed; tags: %v", r.TagList())
	publishedEntry := libChangelog(t, r)

	// Run 3: the retry owes the failed leg the version the failed run planned
	// for it, and owes the members that published nothing at all.
	require.NoError(t, os.Remove(r.Path("fail-app1")))
	r.ReleaseOK()
	assert.True(t, r.IsTagged("app1@1.2.0-beta.0"),
		"the failed leg releases at the version it was planned at; tags: %v", r.TagList())
	assert.Equal(t, 2, r.TagCount("lib1@"), "the published member is untouched; tags: %v", r.TagList())
	assert.Equal(t, 2, r.TagCount("docs1@"), "tags: %v", r.TagList())
	assert.Equal(t, publishedEntry, libChangelog(t, r),
		"a member with nothing to add writes no entry, least of all a no-changes one")
	assert.NotContains(t, libChangelog(t, r), "No changes")

	// Run 4: work inside the train belongs to the package that wrote it, and
	// its counter is its own.
	r.CommitEmpty("fix(lib1): repair something on the train")
	r.ReleaseOK()
	assert.True(t, r.IsTagged("lib1@1.2.0-beta.1"), "tags: %v", r.TagList())
	assert.Equal(t, 2, r.TagCount("app1@"), "nobody else moves; tags: %v", r.TagList())
	assert.Equal(t, 2, r.TagCount("docs1@"), "tags: %v", r.TagList())

	// Run 5: the shared prefix moves onto the next train, which is the whole
	// group's, and every counter starts again.
	r.CommitEmpty("feat(app1)!: the next generation")
	res = r.ReleaseOK()
	for _, tag := range []string{"lib1@2.0.0-beta.0", "app1@2.0.0-beta.0", "docs1@2.0.0-beta.0"} {
		assert.True(t, r.IsTagged(tag), "tags: %v", r.TagList())
	}
	assert.True(t, harness.IsCodePresentForPackage(res.Events, "W234", "docs1"),
		"a member with nothing of its own rides, and the ride is explained")

	// Run 6: nothing left to do.
	before := len(r.TagList())
	r.ReleaseOK()
	assert.Len(t, r.TagList(), before, "converged")
}

// TestVersionGroupIndependentChannelsGraduateOnlyNamedMembers: with a channel
// of its own, ending one member's train says nothing about the others', and a
// later movement of the shared prefix still takes all of them, each on the
// line it is on.
func TestVersionGroupIndependentChannelsGraduateOnlyNamedMembers(t *testing.T) {
	r := seedAxesRepo(t, models.VersionGroupConfig{
		Versioning: models.VersioningFixedMajorMinor,
		Counter:    models.SharingIndependent,
		Channels:   models.SharingIndependent,
	})
	r.Commit("feat(lib1, app1, docs1): bootstrap the platform")
	r.ReleaseOK()
	r.CommitEmpty("feat(lib1, app1, docs1)%beta: board the train")
	r.ReleaseOK()

	r.CommitEmpty("release(docs1)%beta>stable: the documentation is ready first")
	res := r.ReleaseOK()
	assert.True(t, r.IsTagged("docs1@1.2.0"), "the named member graduates; tags: %v", r.TagList())
	assert.False(t, r.IsTagged("lib1@1.2.0"), "nobody else leaves the train; tags: %v", r.TagList())
	assert.Equal(t, 2, r.TagCount("lib1@"), "tags: %v", r.TagList())
	assert.Equal(t, 2, r.TagCount("app1@"), "tags: %v", r.TagList())
	assert.False(t, harness.IsCodePresent(res.Events, "W236"),
		"no channel is forced on anybody, so there is no conflict to report")

	// The next feature moves the shared minor, so everybody moves: docs1 on
	// the stable line it graduated onto, the others on the beta line they
	// never left. A ride does not graduate a member.
	r.CommitEmpty("feat(app1): the next feature")
	r.ReleaseOK()
	assert.True(t, r.IsTagged("docs1@1.3.0"), "tags: %v", r.TagList())
	assert.True(t, r.IsTagged("lib1@1.3.0-beta.0"), "tags: %v", r.TagList())
	assert.True(t, r.IsTagged("app1@1.3.0-beta.0"), "tags: %v", r.TagList())
}

// TestVersionGroupIndependentCounterCatchesUpAtItsOwnCounter: falling behind
// the shared prefix is not something any axis excuses, but where the counter
// is each member's own the laggard joins the prefix at the start of its own
// line rather than adopting the group's published prerelease.
func TestVersionGroupIndependentCounterCatchesUpAtItsOwnCounter(t *testing.T) {
	r := seedAxesRepo(t, models.VersionGroupConfig{
		Versioning: models.VersioningFixedMajorMinorSparse,
		Counter:    models.SharingIndependent,
		Channels:   models.SharingIndependent,
	})
	r.Commit("feat(lib1, app1, docs1): bootstrap the platform")
	r.ReleaseOK()

	// lib1 and app1 board a train and run it two prereleases deep. docs1 is
	// sparse with nothing of its own, so it stays behind on the stable line.
	r.CommitEmpty("feat(lib1, app1)%beta: board the train")
	r.ReleaseOK()
	r.CommitEmpty("fix(lib1, app1): a second prerelease")
	r.ReleaseOK()
	require.True(t, r.IsTagged("lib1@1.2.0-beta.1"), "tags: %v", r.TagList())
	assert.Equal(t, 1, r.TagCount("docs1@"), "the sparse member stayed behind; tags: %v", r.TagList())

	// docs1's own first change joins the shared prefix. Its counter is its
	// own, so it starts the line rather than landing on beta.1.
	r.CommitEmpty("fix(docs1): the sparse member finally changes")
	r.ReleaseOK()
	assert.True(t, r.IsTagged("docs1@1.2.0-beta.0"),
		"the laggard joins the prefix at the start of its own line; tags: %v", r.TagList())
	assert.Equal(t, 3, r.TagCount("lib1@"), "nobody else moves; tags: %v", r.TagList())
}

// TestVersionGroupRefusesASharedCounterWithIndependentChannels: one counter
// counts one train and a train runs on one channel, so the combination has no
// meaning. The refusal reaches the operator through the binary.
func TestVersionGroupRefusesASharedCounterWithIndependentChannels(t *testing.T) {
	r := seedAxesRepo(t, models.VersionGroupConfig{
		Versioning: models.VersioningFixedMajorMinor,
		Channels:   models.SharingIndependent,
	})
	r.Commit("feat(lib1): anything at all")

	res := r.Status()
	require.NotEqual(t, 0, res.Code, "stdout:\n%s", res.Stdout)
	assert.Contains(t, loadError(res), "one shared counter cannot span two channels")
}

// TestVersionGroupRestingMemberDoesNotDecideTheGroupsChannel: under the
// defaults, a member that never joined the train rests on stable, and a
// resting channel is not a request to end anybody's train.
func TestVersionGroupRestingMemberDoesNotDecideTheGroupsChannel(t *testing.T) {
	r := seedAxesRepo(t, models.VersionGroupConfig{Versioning: models.VersioningFixedMajorMinorSparse})
	r.Commit("feat(lib1, app1, docs1): bootstrap the platform")
	r.ReleaseOK()

	// lib1 and app1 board a train; docs1 is sparse and has nothing of its own.
	r.CommitEmpty("feat(lib1, app1)%beta: board the train")
	r.ReleaseOK()
	assert.True(t, r.IsTagged("lib1@1.2.0-beta.0"), "tags: %v", r.TagList())
	assert.Equal(t, 1, r.TagCount("docs1@"), "the sparse member stays where it is; tags: %v", r.TagList())

	// A fix on the train continues it. docs1 sitting on stable must not
	// graduate the group.
	r.CommitEmpty("fix(lib1): repair something on the train")
	r.ReleaseOK()
	assert.True(t, r.IsTagged("lib1@1.2.0-beta.1"),
		"the train continues rather than graduating; tags: %v", r.TagList())
	assert.False(t, r.IsTagged("lib1@1.2.0"), "tags: %v", r.TagList())
}

// TestVersionGroupGraduationRetryFinishesTheTrain: under the defaults, a
// graduation whose second leg failed is retried rather than reported as a
// version going backwards. The members that never published the feature that
// set the train's core cannot compute that core from their own windows.
func TestVersionGroupGraduationRetryFinishesTheTrain(t *testing.T) {
	r := seedAxesRepo(t, models.VersionGroupConfig{Versioning: models.VersioningFixedMajorMinor})
	r.Commit("feat(lib1, app1, docs1): bootstrap the platform")
	r.ReleaseOK()

	// lib1 alone writes the feature that sets the train's core; the other two
	// ride onto it and carry none of that work in their own windows.
	r.CommitEmpty("feat(lib1)%beta: board the train")
	r.ReleaseOK()
	require.True(t, r.IsTagged("app1@1.2.0-beta.0"), "tags: %v", r.TagList())

	// Graduate, and let app1's leg die.
	r.CommitEmpty("release(lib1, app1, docs1)%beta>stable: end the train")
	require.NoError(t, os.WriteFile(r.Path("fail-app1"), nil, 0o644))
	res := r.Release()
	require.Equal(t, 1, res.Code, "stdout:\n%s", res.Stdout)
	assert.True(t, r.IsTagged("lib1@1.2.0"), "tags: %v", r.TagList())

	require.NoError(t, os.Remove(r.Path("fail-app1")))
	res = r.ReleaseOK()
	assert.True(t, r.IsTagged("app1@1.2.0"),
		"the retry finishes the graduation; tags: %v", r.TagList())
	assert.False(t, strings.Contains(res.Stdout, "E185"),
		"nothing goes backwards: %s", res.Stdout)
}
