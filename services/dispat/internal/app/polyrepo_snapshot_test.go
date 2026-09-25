// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/ccme"
	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// snapshotPlan is the plan the planner makes of releases with no provider in
// another repository: each release's inputs are its own repository alone.
func snapshotPlan(t *testing.T, w *workspaceRecorder, releases ...*plan.Release) *plan.Plan {
	t.Helper()
	pl := &plan.Plan{Releases: make(map[string]*plan.Release), Providers: make(map[string][]string),
		RepositoryHeads: make(map[string]string), RepositoryInputs: make(map[string][]uint64)}
	for _, record := range w.ordered {
		pl.RepositoryInputOrder = append(pl.RepositoryInputOrder, record.repo.Name)
		pl.RepositoryHeads[record.repo.Name] = recordGit(t, record.repo.Root, "rev-parse", "HEAD")
	}
	for _, rel := range releases {
		pl.Order = append(pl.Order, rel.Pkg.Name)
		pl.Releases[rel.Pkg.Name] = rel
		pl.RepositoryInputs[rel.Pkg.Name] = repositoryInputs(pl.RepositoryInputOrder, rel.Pkg.Repository)
	}
	return pl
}

// repositoryInputs is a planner input set: the named repositories, as bits of
// their places in order.
func repositoryInputs(order []string, repositories ...string) []uint64 {
	words := make([]uint64, (len(order)+63)/64)
	for index, name := range order {
		if slices.Contains(repositories, name) {
			words[index/64] |= uint64(1) << uint(index%64)
		}
	}
	return words
}

func TestSnapshotPlanAcceptsEmptyRepositoryInputs(t *testing.T) {
	w := &workspaceRecorder{snapshot: &workspaceSnapshotGuard{}}
	rel := closureRelease("lib", "")
	pl := &plan.Plan{
		Order: []string{"lib"}, Releases: map[string]*plan.Release{"lib": rel},
		RepositoryInputs: map[string][]uint64{"lib": nil},
	}
	require.NotPanics(t, func() { w.setSnapshotPlan(pl) })
	require.Contains(t, w.snapshot.byRelease, rel)
	assert.Empty(t, w.snapshot.byRelease[rel].words)
}

func TestWorkspaceSnapshotReadFailureRetainsRepositoryDiagnostic(t *testing.T) {
	w, rel := recordFixture(t, false, false)
	require.NoError(t, w.captureSnapshot(t.Context(), []*model.Package{rel.Pkg}))
	pl := snapshotPlan(t, w, rel)
	w.setSnapshotPlan(pl)
	require.NoError(t, w.prepare(t.Context(), pl))
	w.byName["source"].git.Dir = filepath.Join(t.TempDir(), "missing-worktree")
	err := w.verifySnapshot(t.Context(), rel)
	require.Error(t, err)
	assert.Equal(t, config.DiagnosticRepositoryInvalid, config.DiagnosticCode(err))
	assert.ErrorContains(t, err, "repository source: acquiring snapshot validation lock")
}

func TestWorkspaceSnapshotRejectsRelevantTagDriftButIgnoresCoordinationAndForeignRefs(t *testing.T) {
	w, rel := recordFixture(t, false, false)
	packages := []*model.Package{rel.Pkg}
	require.NoError(t, w.captureSnapshot(t.Context(), packages))
	pl := snapshotPlan(t, w, rel)
	w.setSnapshotPlan(pl)
	require.NoError(t, w.prepare(t.Context(), pl))
	source := w.byName["source"]

	recordGit(t, source.repo.Root, "tag", "foreign@1.0.0")
	recordGit(t, source.repo.Root, "tag", "dispat-release-lock-attempt-test")
	require.NoError(t, w.verifySnapshot(t.Context(), nil), "unconfigured and coordination refs do not alter release history")

	recordGit(t, source.repo.Root, "tag", "lib@9.9.9")
	err := w.verifySnapshot(t.Context(), nil)
	require.Error(t, err)
	assert.Equal(t, config.DiagnosticRepositoryInvalid, config.DiagnosticCode(err))
	assert.ErrorContains(t, err, "relevant tag lib@9.9.9 appeared")
}

func TestWorkspaceSnapshotAdmitsExactNestedHeadAndTagsForCurrentRelease(t *testing.T) {
	w, rel := recordFixture(t, false, false)
	rel.Pkg.Space.AliasTags = []model.AliasTag{{Format: "lib-v{major}", Force: true}}
	require.NoError(t, w.captureSnapshot(t.Context(), []*model.Package{rel.Pkg}))
	pl := snapshotPlan(t, w, rel)
	w.setSnapshotPlan(pl)
	require.NoError(t, w.prepare(t.Context(), pl))
	source := w.byName["source"]

	recordGit(t, source.repo.Root, "commit", "--allow-empty", "-qm", "feat(lib): nested record")
	pin := recordGit(t, source.repo.Root, "rev-parse", "HEAD")
	rel.Outputs = append(rel.Outputs, plan.Output{Name: plan.PackageCommitExportPrefix + plan.EnvKey(rel.Pkg.Name), Value: pin})
	recordGit(t, source.repo.Root, "tag", rel.TagName())
	recordGit(t, source.repo.Root, "tag", rel.AliasTags()[0].Name)

	require.NoError(t, w.verifySnapshot(t.Context(), rel))
	assert.Equal(t, pin, source.expectedHead)
	require.NoError(t, w.verifySnapshot(t.Context(), rel), "accepted exact refs become the new fixed snapshot")
}

func TestWorkspaceSnapshotRejectsPlannedTagWithoutOwnedCommitExport(t *testing.T) {
	for _, exported := range []string{"", "HEAD", "123456789abc"} {
		t.Run(exported, func(t *testing.T) {
			w, rel := recordFixture(t, false, false)
			require.NoError(t, w.captureSnapshot(t.Context(), []*model.Package{rel.Pkg}))
			pl := snapshotPlan(t, w, rel)
			w.setSnapshotPlan(pl)
			require.NoError(t, w.prepare(t.Context(), pl))
			if exported != "" {
				rel.Outputs = append(rel.Outputs, plan.Output{
					Name: plan.PackageCommitExportPrefix + plan.EnvKey(rel.Pkg.Name), Value: exported,
				})
			}

			recordGit(t, w.byName["source"].repo.Root, "tag", rel.TagName())
			err := w.verifySnapshot(t.Context(), rel)
			require.Error(t, err)
			assert.Equal(t, config.DiagnosticRepositoryInvalid, config.DiagnosticCode(err))
			assert.ErrorContains(t, err, "relevant tag "+rel.TagName()+" appeared")
		})
	}
}

func TestWorkspaceSnapshotAdvancesOnlyAfterNativeRecordSucceeds(t *testing.T) {
	w, rel := recordFixture(t, false, false)
	require.NoError(t, w.captureSnapshot(t.Context(), []*model.Package{rel.Pkg}))
	pl := snapshotPlan(t, w, rel)
	w.setSnapshotPlan(pl)
	require.NoError(t, w.prepare(t.Context(), pl))

	require.NoError(t, w.Record(t.Context(), rel))
	require.NoError(t, w.verifySnapshot(t.Context(), nil),
		"the exact tag created by the successful recorder advances the snapshot")
}

func TestWorkspaceSnapshotDoesNotConfuseSameCommitWithSameRefObject(t *testing.T) {
	w, rel := recordFixture(t, false, false)
	recordGit(t, w.byName["source"].repo.Root, "tag", "-a", "-m", "first", rel.TagName())
	require.NoError(t, w.captureSnapshot(t.Context(), []*model.Package{rel.Pkg}))
	pl := snapshotPlan(t, w, rel)
	w.setSnapshotPlan(pl)
	require.NoError(t, w.prepare(t.Context(), pl))
	source := w.byName["source"]

	recordGit(t, source.repo.Root, "tag", "-f", "-a", "-m", "replacement", rel.TagName())
	err := w.verifySnapshot(t.Context(), nil)
	require.Error(t, err)
	assert.ErrorContains(t, err, "relevant tag "+rel.TagName()+" moved")
}

func TestWorkspaceSnapshotPrepublishChecksOnlyOwnerAndProviderClosure(t *testing.T) {
	w, sourceRelease := recordFixture(t, false, false)
	controlRoot := w.byName[config.ControlRepository].repo.Root
	controlPackage := &model.Package{Name: "tool", Repository: config.ControlRepository, RepoRoot: controlRoot,
		Dir: controlRoot, Space: &model.Space{Name: "tools", Repository: config.ControlRepository}}
	controlRelease := &plan.Release{Pkg: controlPackage, Channel: "stable", Next: ccme.Version{Major: 1}, Bump: ccme.BumpPatch, NewWork: true}
	require.NoError(t, w.captureSnapshot(t.Context(), []*model.Package{sourceRelease.Pkg, controlPackage}))
	pl := snapshotPlan(t, w, sourceRelease, controlRelease)
	w.setSnapshotPlan(pl)
	require.NoError(t, w.prepare(context.Background(), pl))

	recordGit(t, w.byName["source"].repo.Root, "commit", "--allow-empty", "-qm", "feat(lib): concurrent independent work")
	require.NoError(t, w.verifySnapshot(t.Context(), controlRelease),
		"an independent repository may progress while this release publishes")

	pl.Providers[controlRelease.Pkg.Name] = []string{sourceRelease.Pkg.Name}
	pl.RepositoryInputs[controlRelease.Pkg.Name] = repositoryInputs(pl.RepositoryInputOrder,
		config.ControlRepository, sourceRelease.Pkg.Repository)
	w.setSnapshotPlan(pl)
	err := w.verifySnapshot(t.Context(), controlRelease)
	require.Error(t, err)
	assert.Equal(t, config.DiagnosticRepositoryInvalid, config.DiagnosticCode(err))
}

func TestWorkspaceSnapshotChecksApplicableControlIntentWhenCheckpointsAreDisabled(t *testing.T) {
	for _, tc := range []struct {
		name      string
		inputs    uint64
		wantError bool
	}{
		{name: "applicable control intent", inputs: 0b11, wantError: true},
		{name: "unrelated control history", inputs: 0b10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, rel := recordFixture(t, false, false)
			require.NoError(t, w.captureSnapshot(t.Context(), []*model.Package{rel.Pkg}))
			pl := snapshotPlan(t, w, rel)
			pl.RepositoryInputOrder = []string{"control", "source"}
			pl.RepositoryInputs = map[string][]uint64{rel.Pkg.Name: {tc.inputs}}
			w.setSnapshotPlan(pl)
			require.NoError(t, w.prepare(t.Context(), pl))

			control := w.byName[config.ControlRepository]
			recordGit(t, control.repo.Root, "commit", "--allow-empty", "-qm", "fix(lib): later fleet intent")
			err := w.verifySnapshot(t.Context(), rel)
			if tc.wantError {
				require.Error(t, err)
				assert.Equal(t, config.DiagnosticRepositoryInvalid, config.DiagnosticCode(err))
				assert.ErrorContains(t, err, "repository control changed after planning")
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestWorkspacePublishGateSerializesOnlySharedRepository(t *testing.T) {
	w, sourceRelease := recordFixture(t, false, false)
	sibling := *sourceRelease
	siblingPkg := *sourceRelease.Pkg
	siblingPkg.Name = "tool"
	sibling.Pkg = &siblingPkg

	releaseFirst, err := w.acquirePublish(t.Context(), sourceRelease)
	require.NoError(t, err)
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = w.acquirePublish(cancelled, &sibling)
	assert.ErrorIs(t, err, context.Canceled, "same-owner publish waits for the active record transaction")

	controlRoot := w.byName[config.ControlRepository].repo.Root
	controlRelease := &plan.Release{Pkg: &model.Package{Name: "control-tool", Repository: config.ControlRepository,
		RepoRoot: controlRoot, Dir: controlRoot, Space: &model.Space{Name: "tools", Repository: config.ControlRepository}}}
	releaseControl, err := w.acquirePublish(t.Context(), controlRelease)
	require.NoError(t, err, "another repository retains its publish concurrency")
	releaseControl()
	releaseFirst()

	releaseSibling, err := w.acquirePublish(t.Context(), &sibling)
	require.NoError(t, err)
	releaseSibling()
}

func snapshotClosureFixture(repositoryNames ...string) *workspaceRecorder {
	w := &workspaceRecorder{byName: make(map[string]*repositoryRecord)}
	for _, name := range repositoryNames {
		disabled := false
		record := &repositoryRecord{repo: &config.Repository{
			Name: name, Commit: &config.CommitConfig{Enabled: &disabled}, Config: &config.File{},
		}}
		w.byName[name] = record
		w.ordered = append(w.ordered, record)
	}
	w.snapshot = &workspaceSnapshotGuard{
		records: w.ordered, repoIndex: make(map[string]int), byRelease: make(map[*plan.Release]*repositorySet),
	}
	for index, record := range w.ordered {
		w.snapshot.repoIndex[record.repo.Name] = index
	}
	return w
}

func closureRelease(name, repository string) *plan.Release {
	return &plan.Release{Pkg: &model.Package{Name: name, Repository: repository, Space: &model.Space{Name: repository}},
		Current: ccme.Version{Major: 1}, Next: ccme.Version{Major: 1, Patch: 1}, Bump: ccme.BumpPatch, NewWork: true}
}

func TestSnapshotRepositoryClosureIncludesPlannerGroupInputs(t *testing.T) {
	w := snapshotClosureFixture(config.ControlRepository, "source-a", "source-b")
	a := closureRelease("a", "source-a")
	b := closureRelease("b", "source-b")
	pl := &plan.Plan{
		Order:                []string{"a", "b"},
		Releases:             map[string]*plan.Release{"a": a, "b": b},
		Providers:            map[string][]string{},
		RepositoryInputOrder: []string{"control", "source-a", "source-b"},
		RepositoryInputs:     map[string][]uint64{"a": {0b110}, "b": {0b110}},
	}

	w.setSnapshotPlan(pl)

	assert.True(t, w.snapshot.byRelease[b].contains(w.snapshot.repoIndex["source-a"]))
	assert.True(t, w.snapshot.byRelease[b].contains(w.snapshot.repoIndex["source-b"]))
	assert.Same(t, w.snapshot.byRelease[a], w.snapshot.byRelease[b])
}

func TestSnapshotCheckpointDoesNotMutatePlannerInputBits(t *testing.T) {
	w := snapshotClosureFixture(config.ControlRepository, "source")
	enabled := true
	w.byName[config.ControlRepository].repo.Commit.Enabled = &enabled
	rel := closureRelease("lib", "source")
	sibling := closureRelease("tool", "source")
	inputWords := []uint64{0b10}
	pl := &plan.Plan{
		Order:                []string{"lib", "tool"},
		Releases:             map[string]*plan.Release{"lib": rel, "tool": sibling},
		Providers:            map[string][]string{},
		RepositoryInputOrder: []string{"control", "source"},
		RepositoryInputs:     map[string][]uint64{"lib": inputWords, "tool": inputWords},
	}

	w.setSnapshotPlan(pl)

	assert.Equal(t, []uint64{0b10}, inputWords, "the checkpoint bit is added to a private copy")
	set := w.snapshot.byRelease[rel]
	assert.True(t, set.contains(w.snapshot.repoIndex[config.ControlRepository]))
	assert.True(t, set.contains(w.snapshot.repoIndex["source"]))
	assert.Same(t, set, w.snapshot.byRelease[sibling], "one checkpoint-augmented copy is shared by equal planner inputs")
}

func TestSnapshotRepositoryClosureTransfersMultipleWords(t *testing.T) {
	names := []string{config.ControlRepository}
	for i := range 65 {
		names = append(names, fmt.Sprintf("source-%02d", i))
	}
	w := snapshotClosureFixture(names...)
	rel := closureRelease("last", "source-64")
	words := make([]uint64, 2)
	words[0] = 1              // control
	words[1] = uint64(1) << 1 // source-64 is repository index 65
	pl := &plan.Plan{
		Order:                []string{"last"},
		Releases:             map[string]*plan.Release{"last": rel},
		Providers:            map[string][]string{},
		RepositoryInputOrder: names,
		RepositoryInputs:     map[string][]uint64{"last": words},
	}

	w.setSnapshotPlan(pl)

	set := w.snapshot.byRelease[rel]
	assert.True(t, set.contains(w.snapshot.repoIndex[config.ControlRepository]))
	assert.True(t, set.contains(w.snapshot.repoIndex["source-64"]))
	assert.False(t, set.contains(w.snapshot.repoIndex["source-00"]))
}

func TestSnapshotRepositoryClosuresShareLargeConsumerFanout(t *testing.T) {
	w := snapshotClosureFixture(config.ControlRepository, "source")
	order := []string{config.ControlRepository, "source"}
	inputs := repositoryInputs(order, "source") // interned once by the planner
	pl := &plan.Plan{Releases: make(map[string]*plan.Release), Providers: make(map[string][]string),
		RepositoryInputOrder: order, RepositoryInputs: map[string][]uint64{"base": inputs}}
	base := closureRelease("base", "source")
	pl.Order = append(pl.Order, "base")
	pl.Releases["base"] = base
	for i := range 256 {
		name := fmt.Sprintf("consumer-%03d", i)
		rel := closureRelease(name, "source")
		pl.Order = append(pl.Order, name)
		pl.Releases[name] = rel
		pl.Providers[name] = []string{"base"}
		pl.RepositoryInputs[name] = inputs
	}

	w.setSnapshotPlan(pl)

	want := w.snapshot.byRelease[base]
	for _, name := range pl.Order[1:] {
		assert.Same(t, want, w.snapshot.byRelease[pl.Releases[name]])
	}
}

func BenchmarkSetSnapshotPlanRepositoryClosures(b *testing.B) {
	w := snapshotClosureFixture(config.ControlRepository, "source-a", "source-b")
	order := []string{config.ControlRepository, "source-a", "source-b"}
	first, rest := repositoryInputs(order, "source-a"), repositoryInputs(order, "source-a", "source-b")
	pl := &plan.Plan{Releases: make(map[string]*plan.Release), Providers: make(map[string][]string),
		RepositoryInputOrder: order, RepositoryInputs: make(map[string][]uint64)}
	for i := range 1024 {
		name := fmt.Sprintf("package-%04d", i)
		repository := "source-a"
		if i%2 != 0 {
			repository = "source-b"
		}
		pl.Order = append(pl.Order, name)
		pl.Releases[name] = closureRelease(name, repository)
		pl.RepositoryInputs[name] = first
		if i > 0 {
			pl.Providers[name] = []string{pl.Order[i-1]}
			pl.RepositoryInputs[name] = rest
		}
	}
	b.ResetTimer()
	for range b.N {
		w.setSnapshotPlan(pl)
	}
}
