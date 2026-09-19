// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Final production-review cases for configuration facts that become trusted
// only after the planner joins them to the composed package and ref snapshot.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestFinalPolyrepoRepositoryBaselinesMustNameTheComposedReleaseSnapshot: the
// configuration loader can prove a repository and revision, but only planning
// knows the canonical consumer set and exact reachable release tags. Neither
// an unknown consumer nor an invented tag may establish a history boundary.
func TestFinalPolyrepoRepositoryBaselinesMustNameTheComposedReleaseSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name, consumer, tag, want string
	}{
		{
			name:     "unknown consumer",
			consumer: "ghost",
			tag:      "core@0.1.0",
			want:     "unknown consumer",
		},
		{
			name:     "unreachable release tag",
			consumer: "core",
			tag:      "core@9.9.9",
			want:     "not an exact reachable release tag",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := finalPolyrepo(t)
			f.control.Git("-C", "sources/lib", "tag", "-a", "core@0.1.0", "-m", "known release")
			cfg := polyrepoFile()
			cfg["spaces"] = centralSpaces(map[string]string{"libs": "sources/lib/packages"})
			cfg["repositoryBaselines"] = []any{map[string]any{
				"consumer": tc.consumer, "releaseTag": tc.tag,
				"repository": "lib-source", "revision": "HEAD",
			}}
			writePolyrepoJSON(t, f.control, "dispat.json", cfg)

			res := f.control.Status()
			require.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			combined := res.Stdout + res.Stderr
			assert.Contains(t, combined, tc.want)
			assert.True(t, harness.IsCodePresent(res.Events, "E333"), "events: %#v", res.Events)
			assert.Equal(t, []string{"core@0.1.0"}, polyrepoTags(f.control, "sources/lib"),
				"a rejected boundary leaves the published snapshot unchanged")
			assert.Empty(t, harness.GraphLine(res.Events, "core").Str("version"),
				"a fatal boundary produces no package plan row")
		})
	}
}

// TestFinalPlanRefusesTwoRefsForOnePublishedVersion: build metadata may make
// two tag names distinct while SemVer gives them equal precedence. If those
// refs point at different commits there is no unique published baseline, so
// planning reports the conflict instead of selecting by ref order.
func TestFinalPlanRefusesTwoRefsForOnePublishedVersion(t *testing.T) {
	r := finalPlanRepo(t)
	r.Git("tag", "-a", "core@0.1.0+first", "-m", "first record")
	r.WriteFile("packages/core/fix.txt", "later\n")
	r.Commit("fix(core): later commit")
	r.Git("tag", "-a", "core@0.1.0+second", "-m", "second record")

	res := r.Status()
	require.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	combined := res.Stdout + res.Stderr
	assert.Contains(t, combined, "parse to the same version")
	assert.True(t, harness.IsCodePresent(res.Events, "E191"), "events: %#v", res.Events)
	assert.Equal(t, []string{"core@0.1.0+first", "core@0.1.0+second"}, r.TagList(),
		"ambiguous refs are preserved for operator repair")
	assert.Empty(t, harness.GraphLine(res.Events, "core").Str("version"),
		"an ambiguous baseline produces no package plan row")
}

// TestFinalPolyrepoControlHistoryToleratesATemporaryUnlink: control history is
// a sequence of fleet snapshots, and a deleted gitlink is an absent value in
// that snapshot rather than a corrupt object id. Re-adding the same source at
// HEAD restores a valid current fleet without letting the historical deletion
// erase its package or poison later planning.
func TestFinalPolyrepoControlHistoryToleratesATemporaryUnlink(t *testing.T) {
	f := finalPolyrepo(t)
	pin := f.control.Git("-C", "sources/lib", "rev-parse", "HEAD")
	f.control.Git("rm", "-q", "--cached", "sources/lib")
	f.control.Git("commit", "-q", "-m", "chore: temporarily unlink library source")
	f.control.Git("update-index", "--add", "--cacheinfo", "160000,"+pin+",sources/lib")
	f.control.Git("commit", "-q", "-m", "chore: restore library source")

	res := f.control.StatusOK()
	assert.Equal(t, "0.0.0 -> 0.1.0", harness.GraphLine(res.Events, "core").Str("version"))
	assert.False(t, harness.IsCodePresent(res.Events, "E333"), "events: %#v", res.Events)
	assert.Empty(t, polyrepoTags(f.control, "sources/lib"), "status remains read-only")
}

// TestFinalPolyrepoRefusesAmbiguousReleaseCheckpointAssociation: the same
// source release can be re-pinned by two control commits with the canonical
// release subject. Both are individually plausible checkpoints but their
// fleet snapshots differ, so neither may be selected by ordering.
func TestFinalPolyrepoRefusesAmbiguousReleaseCheckpointAssociation(t *testing.T) {
	lib := harness.New(t)
	lib.SeedPackage("packages", "lib")
	lib.Commit("feat(lib): initial library")
	lib.Git("tag", "-a", "lib@1.0.0", "-m", "initial library")
	app := harness.New(t)
	app.SeedPackage("packages", "app")
	app.Commit("feat(app): initial application")
	app.Git("tag", "-a", "app@1.0.0", "-m", "initial application")

	control := harness.New(t)
	addPolyrepoSource(t, control, "lib-source", "sources/lib", lib)
	addPolyrepoSource(t, control, "app-source", "sources/app", app)
	cfg := polyrepoFile()
	cfg["spaces"] = centralSpaces(map[string]string{
		"libs": "sources/lib/packages", "apps": "sources/app/packages",
	})
	cfg["dependencies"] = map[string]any{"app": []any{"lib"}}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: assemble fleet")

	appReleased := control.Git("-C", "sources/app", "rev-parse", "HEAD")
	control.WriteFile("sources/app/packages/app/away.txt", "away\n")
	appAway := commitPolyrepoSource(t, control, "sources/app", "chore: move app source away")
	checkpointPolyrepoSource(t, control, "sources/app")
	control.Git("-C", "sources/app", "checkout", "-q", "--detach", appReleased)
	control.Git("add", "sources/app")
	control.Git("commit", "-q", "-m", "chore(release): app@1.0.0")

	control.Git("-C", "sources/app", "checkout", "-q", "--detach", appAway)
	control.WriteFile("sources/lib/packages/lib/later.txt", "later provider\n")
	commitPolyrepoSource(t, control, "sources/lib", "fix(lib): later provider change")
	control.Git("add", "sources/app", "sources/lib")
	control.Git("commit", "-q", "-m", "chore: move fleet between checkpoints")
	control.Git("-C", "sources/app", "checkout", "-q", "--detach", appReleased)
	control.Git("add", "sources/app")
	control.Git("commit", "-q", "-m", "chore(release): app@1.0.0")

	res := control.Status()
	require.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	combined := res.Stdout + res.Stderr
	assert.Contains(t, combined, "ambiguous control checkpoint association")
	assert.True(t, harness.IsCodePresent(res.Events, "E333"), "events: %#v", res.Events)
	assert.Equal(t, []string{"app@1.0.0"}, polyrepoTags(control, "sources/app"))
	assert.Equal(t, []string{"lib@1.0.0"}, polyrepoTags(control, "sources/lib"))
	assert.Empty(t, harness.GraphLine(res.Events, "app").Str("version"),
		"ambiguous fleet evidence produces no package plan row")
}

// TestFinalPolyrepoRefusesReleaseCheckpointWithoutProviderPin: a canonical
// consumer checkpoint proves only the gitlinks in its own tree. If the
// provider was temporarily absent, restoring it later cannot retroactively
// supply the revision that consumer release incorporated.
func TestFinalPolyrepoRefusesReleaseCheckpointWithoutProviderPin(t *testing.T) {
	lib := harness.New(t)
	lib.SeedPackage("packages", "lib")
	lib.Commit("feat(lib): initial library")
	lib.Git("tag", "-a", "lib@1.0.0", "-m", "initial library")
	app := harness.New(t)
	app.SeedPackage("packages", "app")
	app.Commit("feat(app): initial application")
	app.Git("tag", "-a", "app@1.0.0", "-m", "initial application")

	control := harness.New(t)
	addPolyrepoSource(t, control, "lib-source", "sources/lib", lib)
	addPolyrepoSource(t, control, "app-source", "sources/app", app)
	cfg := polyrepoFile()
	cfg["spaces"] = centralSpaces(map[string]string{
		"libs": "sources/lib/packages", "apps": "sources/app/packages",
	})
	cfg["dependencies"] = map[string]any{"app": []any{"lib"}}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: assemble fleet")

	libPin := control.Git("-C", "sources/lib", "rev-parse", "HEAD")
	appReleased := control.Git("-C", "sources/app", "rev-parse", "HEAD")
	control.WriteFile("sources/app/packages/app/away.txt", "away\n")
	commitPolyrepoSource(t, control, "sources/app", "chore: move app source away")
	control.Git("rm", "-q", "--cached", "sources/lib")
	control.Git("add", "sources/app")
	control.Git("commit", "-q", "-m", "chore: remove provider before consumer checkpoint")
	control.Git("-C", "sources/app", "checkout", "-q", "--detach", appReleased)
	control.Git("add", "sources/app")
	control.Git("commit", "-q", "-m", "chore(release): app@1.0.0")
	control.Git("update-index", "--add", "--cacheinfo", "160000,"+libPin+",sources/lib")
	control.Git("commit", "-q", "-m", "chore: restore provider after consumer checkpoint")

	res := control.Status()
	require.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	combined := res.Stdout + res.Stderr
	assert.Contains(t, combined, "control checkpoint has no gitlink for repository lib-source")
	assert.True(t, harness.IsCodePresent(res.Events, "E333"), "events: %#v", res.Events)
	assert.Equal(t, []string{"app@1.0.0"}, polyrepoTags(control, "sources/app"))
	assert.Equal(t, []string{"lib@1.0.0"}, polyrepoTags(control, "sources/lib"))
	assert.Empty(t, harness.GraphLine(res.Events, "app").Str("version"),
		"a checkpoint without provider evidence produces no consumer plan row")
}
