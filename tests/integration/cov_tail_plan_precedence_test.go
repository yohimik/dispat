// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Coverage scenarios: two repositories deciding one package.
//
// In a composed workspace a package can be addressed both from the repository
// that holds it and from the control repository that composes the fleet, and
// those two histories have no shared clock. "The newest wins" is therefore not
// a question a timestamp may answer: it is answered by causality, and the only
// thing that establishes it across repositories is a control revision whose
// gitlink snapshot observes the source revision it is ranked against. When
// nothing does, the run refuses rather than picking, because either choice
// would be a different release depending on which repository was read first.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// covTailComposedOwner builds a control repository composing one source that
// owns the package named. Both repositories can address the package, which is
// what every scenario in this file needs.
func covTailComposedOwner(t *testing.T, pkg string) *harness.Repo {
	t.Helper()
	source := harness.New(t)
	source.SeedPackage("packages", pkg)
	source.Commit("feat(" + pkg + "): initial package")
	source.Git("tag", "-a", pkg+"@1.0.0", "-m", "initial release")

	control := harness.New(t)
	addPolyrepoSource(t, control, pkg+"-source", "sources/"+pkg, source)
	cfg := polyrepoFile()
	cfg["spaces"] = centralSpaces(map[string]string{pkg: "sources/" + pkg + "/packages"})
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: compose the fleet")

	// The package's existing release predates the composition, so the control
	// revision it was published from has to be stated rather than discovered.
	cfg["repositoryBaselines"] = []any{map[string]any{
		"consumer": pkg, "releaseTag": pkg + "@1.0.0",
		"repository": "control", "revision": control.Git("rev-parse", "HEAD"),
	}}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: state the control checkpoint of the existing release")
	return control
}

// TestCovTailIncomparableExactPinsNeedCausalControlResolution: an exact
// version written in the package's own source and another written in the
// control repository are two answers to the same question. Neither history
// observes the other, so neither is newer, and the run says so instead of
// releasing whichever it happened to read last. A later control directive,
// written once the source revision is in its gitlink snapshot, does observe
// both and settles it.
func TestCovTailIncomparableExactPinsNeedCausalControlResolution(t *testing.T) {
	control := covTailComposedOwner(t, "a")

	control.WriteFile("sources/a/packages/a/pin.txt", "source\n")
	commitPolyrepoSource(t, control, "sources/a", "release(a): the source names a version\n\nRelease-As: 1.5.0")
	// Written before the source revision above is checkpointed, so this
	// control revision cannot observe it.
	control.CommitEmpty("release(a): the control names another\n\nRelease-As: 1.8.0")
	checkpointPolyrepoSource(t, control, "sources/a")

	conflict := control.Status()
	require.NotZero(t, conflict.Code, "stdout:\n%s\nstderr:\n%s", conflict.Stdout, conflict.Stderr)
	assert.True(t, harness.IsCodePresent(conflict.Events, "E334"),
		"neither pin is newer than the other: %s", conflict.Stdout)
	assert.Contains(t, conflict.Stdout, "conflicting Release-As directives",
		"and the run names the axis the conflict is on")

	// Now that the source revision is in the control's snapshot, a control
	// directive observes both candidates and is the causal winner.
	control.CommitEmpty("release(a): the control settles it\n\nRelease-As: 2.0.0")
	resolved := control.StatusOK()
	assert.Equal(t, "1.0.0 -> 2.0.0", harness.GraphLine(resolved.Events, "a").Str("version"),
		"the resolving control directive decides the version: %s", resolved.Stdout)
}

// TestCovTailIncomparableDirectChannelsNeedCausalControlResolution: the same
// rule on the channel axis, and for the directives a package writes about
// itself rather than the ones propagated to it. Two direct channel choices
// from two repositories conflict exactly when no control revision observes
// both.
func TestCovTailIncomparableDirectChannelsNeedCausalControlResolution(t *testing.T) {
	control := covTailComposedOwner(t, "a")

	control.WriteFile("sources/a/packages/a/channel.txt", "rc\n")
	commitPolyrepoSource(t, control, "sources/a", "release(a)%rc: the source chooses rc")
	control.CommitEmpty("release(a)%beta: the control chooses beta")
	checkpointPolyrepoSource(t, control, "sources/a")

	conflict := control.Status()
	require.NotZero(t, conflict.Code, "stdout:\n%s\nstderr:\n%s", conflict.Stdout, conflict.Stderr)
	assert.True(t, harness.IsCodePresent(conflict.Events, "E334"),
		"neither channel directive is newer than the other: %s", conflict.Stdout)
	assert.Contains(t, conflict.Stdout, "conflicting direct channel directives",
		"and the run names the axis the conflict is on")

	control.CommitEmpty("release(a)%canary: the control settles it")
	resolved := control.StatusOK()
	assert.Equal(t, "stable -> canary", harness.GraphLine(resolved.Events, "a").Str("channel"),
		"the resolving control directive decides the channel: %s", resolved.Stdout)
}

// TestCovTailIncomparableFixedGroupPinsNeedCausalControlResolution: a fixed
// version group holds one shared version, so an exact pin written for any
// member is a pin on the group. Two members pinned to different versions from
// two repositories are the same standoff as above, one level up: the group
// cannot take both, and nothing ranks the two revisions until a control
// revision observes them.
func TestCovTailIncomparableFixedGroupPinsNeedCausalControlResolution(t *testing.T) {
	newSource := func(pkg string) *harness.Repo {
		source := harness.New(t)
		source.SeedPackage("packages", pkg)
		source.Commit("feat(" + pkg + "): initial package")
		source.Git("tag", "-a", pkg+"@1.0.0", "-m", "initial release")
		return source
	}
	control := harness.New(t)
	addPolyrepoSource(t, control, "a-source", "sources/a", newSource("a"))
	addPolyrepoSource(t, control, "b-source", "sources/b", newSource("b"))
	cfg := polyrepoFile()
	cfg["versionGroups"] = map[string]any{"platform": map[string]any{"versioning": "fixed"}}
	cfg["spaces"] = map[string]any{
		"a": map[string]any{"path": []string{"sources/a/packages"}, "versionGroup": "platform"},
		"b": map[string]any{"path": []string{"sources/b/packages"}, "versionGroup": "platform"},
	}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: compose the fleet")

	baseline := control.Git("rev-parse", "HEAD")
	cfg["repositoryBaselines"] = []any{
		map[string]any{"consumer": "a", "releaseTag": "a@1.0.0", "repository": "control", "revision": baseline},
		map[string]any{"consumer": "b", "releaseTag": "b@1.0.0", "repository": "control", "revision": baseline},
	}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: state the control checkpoints of the existing releases")

	control.WriteFile("sources/a/packages/a/pin.txt", "source\n")
	commitPolyrepoSource(t, control, "sources/a", "release(a): one member names a version\n\nRelease-As: 1.5.0")
	// Written before the source revision above is checkpointed.
	control.CommitEmpty("release(b): the other member names another\n\nRelease-As: 1.8.0")
	checkpointPolyrepoSource(t, control, "sources/a")

	conflict := control.Status()
	require.NotZero(t, conflict.Code, "stdout:\n%s\nstderr:\n%s", conflict.Stdout, conflict.Stderr)
	assert.True(t, harness.IsCodePresent(conflict.Events, "E334"),
		"neither pin on the group is newer than the other: %s", conflict.Stdout)
	assert.Contains(t, conflict.Stdout, "conflicting fixed-group pins",
		"and the run names the axis the conflict is on")

	control.CommitEmpty("release(b): the control settles the group\n\nRelease-As: 2.0.0")
	resolved := control.StatusOK()
	assert.True(t, harness.IsCodePresent(resolved.Events, "W235"),
		"the competing pins are still reported: %s", resolved.Stdout)
	assert.Equal(t, "1.0.0 -> 2.0.0", harness.GraphLine(resolved.Events, "a").Str("version"),
		"and the whole group takes the resolving version: %s", resolved.Stdout)
	assert.Equal(t, "1.0.0 -> 2.0.0", harness.GraphLine(resolved.Events, "b").Str("version"),
		"both members alike: %s", resolved.Stdout)
}

// TestCovTailControlDirectivesProjectPropagationOntoSources: a directive
// written in the control repository can reach a package it does not hold, and
// through that package's dependents. The guard that keeps such a directive
// honest has to account for every package it reaches — the ones its scope
// names and the ones its propagation walks to — because a control revision
// that pins a source older than the work it is addressing would otherwise
// publish something nobody looked at.
func TestCovTailControlDirectivesProjectPropagationOntoSources(t *testing.T) {
	newSource := func(pkg string) *harness.Repo {
		source := harness.New(t)
		source.SeedPackage("packages", pkg)
		source.Commit("feat(" + pkg + "): initial package")
		source.Git("tag", "-a", pkg+"@1.0.0", "-m", "initial release")
		return source
	}
	control := harness.New(t)
	addPolyrepoSource(t, control, "a-source", "sources/a", newSource("a"))
	addPolyrepoSource(t, control, "app-source", "sources/app", newSource("app"))
	cfg := polyrepoFile()
	cfg["spaces"] = centralSpaces(map[string]string{
		"a":   "sources/a/packages",
		"app": "sources/app/packages",
	})
	cfg["dependencies"] = map[string]any{"app": []any{"a"}}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: compose the fleet")

	baseline := control.Git("rev-parse", "HEAD")
	cfg["repositoryBaselines"] = []any{
		map[string]any{"consumer": "a", "releaseTag": "a@1.0.0", "repository": "control", "revision": baseline},
		map[string]any{"consumer": "app", "releaseTag": "app@1.0.0", "repository": "control", "revision": baseline},
		map[string]any{"consumer": "app", "releaseTag": "app@1.0.0", "repository": "a-source", "revision": "a@1.0.0"},
	}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: state the control checkpoints of the existing releases")

	control.CommitEmpty("feat(a)^: the control records work that reaches the dependent")
	control.CommitEmpty("release(a)%beta%%beta++1: and a channel the dependent inherits")

	res := control.StatusOK()
	app := harness.GraphLine(res.Events, "app")
	assert.Equal(t, "patch", app.Str("bump"),
		"the dependent was reached by the control directive's propagation: %s", res.Stdout)
	assert.Equal(t, "stable -> beta", app.Str("channel"),
		"and by its channel: %s", res.Stdout)
}
