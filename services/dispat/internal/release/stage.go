// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package release

// The stages of one package's release, as data.
//
// A stage is a descriptor rather than a code path: what it is called, which
// budget its tasks hold, where it may run, what a node running it must be
// given and whether it produces an output set. The executor reads every one
// of those questions off the descriptor, so a stage added to the table runs,
// reports, gates, fails, reverts and travels to a worker the way the stages
// beside it do. What a stage's commands are, and when a package has the stage
// at all, are the two answers read from the package's own configuration.

import (
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// StageBudget is the run-wide budget a stage's tasks hold a slot of: the
// build budget, shared by everything that prepares, builds or checks a
// package; the publications' own; or the one serializing lock-file writers.
type StageBudget uint8

const (
	BudgetBuild StageBudget = iota
	BudgetPublish
	BudgetSyncLock
)

// StagePlacement is where a stage's frame may run: on the orchestrator alone,
// under the snapshot guard, because it writes the working tree delegated
// frames are snapshotted from; or where the package's runOnly value for the
// build or for the publish places it.
type StagePlacement uint8

const (
	PlacementOrchestratorOnly StagePlacement = iota
	PlacementByBuild
	PlacementByPublish
)

// StageInputs is what a node running a stage's frame installs first, beyond
// the package's sources: nothing, the providers' admitted outputs (a build),
// or the package's own admitted outputs beside them (a stage that checks or
// ships what the build produced).
type StageInputs uint8

const (
	InputsNone StageInputs = iota
	InputsProviderOutputs
	InputsOwnOutputs
)

// Stage describes one stage of a package's release.
type Stage struct {
	// Name is what DISPAT_STAGE, the failed stage, the events and the log
	// call the stage, and Title is the spelling inside its hook names:
	// beforeTitle and postTitle.
	Name, Title string
	// Budget is the run-wide budget its tasks hold a slot of.
	Budget StageBudget
	// Placement is where its frame may run.
	Placement StagePlacement
	// Inputs is what a node is given before the frame runs.
	Inputs StageInputs
	// IsProducingOutputs says the frame's declared outputs are captured and
	// admitted as the package's output set.
	IsProducingOutputs bool
	// IsAuthorized says the frame's commands wait for the run's authorization
	// after its before hook: the irreversible stage, whose revalidation is
	// made on the machine that owns the locks.
	IsAuthorized bool
	// IsPreparing says the stage writes release files ahead of the build,
	// which the release commit may share with other packages.
	IsPreparing bool

	kind taskKind
	// isPresent answers whether a package's release runs the stage at all.
	isPresent func(*plan.Release) bool
	// frame is the stage's hooks and commands as the package's space
	// configures them.
	frame func(*model.Space) stage
}

// stageTable is every stage in the order a package's release runs them. The
// task graph chains the stages a package has in this order.
var stageTable = []Stage{
	{Name: "sign", Title: "Sign", Budget: BudgetBuild, IsPreparing: true, kind: taskSign,
		isPresent: hasSignTask,
		frame: func(s *model.Space) stage {
			return stage{before: s.BeforeSignScript, commands: s.SignScript, after: s.PostSignScript}
		}},
	{Name: string(model.StageVersion), Title: model.StageVersion.Title(), Budget: BudgetBuild,
		IsPreparing: true, kind: taskVersion, isPresent: hasVersionTask,
		frame: func(s *model.Space) stage {
			return stage{before: s.BeforeVersionScript, commands: s.VersionScript, after: s.PostVersionScript}
		}},
	{Name: "syncLock", Title: "SyncLock", Budget: BudgetSyncLock, IsPreparing: true, kind: taskSyncLock,
		isPresent: hasSyncLockTask,
		frame:     func(s *model.Space) stage { return stage{commands: s.AutoVersion.SyncLock} }},
	{Name: "build", Title: "Build", Budget: BudgetBuild, Placement: PlacementByBuild,
		Inputs: InputsProviderOutputs, IsProducingOutputs: true, kind: taskBuild,
		isPresent: isAlwaysPresent,
		frame: func(s *model.Space) stage {
			return stage{before: s.BeforeBuildScript, commands: s.BuildScript, after: s.PostBuildScript}
		}},
	{Name: "tests", Title: "Tests", Budget: BudgetBuild, Placement: PlacementByBuild,
		Inputs: InputsOwnOutputs, kind: taskTests, isPresent: hasTestsTask,
		frame: func(s *model.Space) stage {
			return stage{before: s.BeforeTestsScript, commands: s.TestsScript, after: s.PostTestsScript}
		}},
	// postPublish is not part of the publish frame: it only runs after the
	// package is fully published, in the publish tail.
	{Name: "publish", Title: "Publish", Budget: BudgetPublish, Placement: PlacementByPublish,
		Inputs: InputsOwnOutputs, IsAuthorized: true, kind: taskPublish, isPresent: isAlwaysPresent,
		frame: func(s *model.Space) stage {
			return stage{before: s.BeforePublishScript, commands: s.PublishScript}
		}},
}

// BuildStage is the build stage's descriptor, for a frame that is a build
// although no release task runs it: the preparation of a provider this run
// does not release.
func BuildStage() Stage { return stageOf(taskBuild) }

// stageOf is the descriptor of one task kind, as stageTable states it.
func stageOf(kind taskKind) Stage {
	for _, descriptor := range stageTable {
		if descriptor.kind == kind {
			return descriptor
		}
	}
	panic("release: no stage descriptor for a task kind")
}

// resolveStage is the descriptor a task of this kind of this release runs
// under. The version stage is the one stage with two names, and the package's
// space says which one it was configured under.
func resolveStage(rel *plan.Release, kind taskKind) Stage {
	descriptor := stageOf(kind)
	if kind == taskVersion {
		descriptor.Name, descriptor.Title = rel.Pkg.Space.VersionStage.String(), rel.Pkg.Space.VersionStage.Title()
	}
	return descriptor
}

// presentStages are the stages one package's release runs, in table order.
func presentStages(rel *plan.Release) []Stage {
	present := make([]Stage, 0, len(stageTable))
	for _, descriptor := range stageTable {
		if descriptor.isPresent(rel) {
			present = append(present, descriptor)
		}
	}
	return present
}

// isAlwaysPresent is the build's and the publish's presence: every releasing
// package runs them, with or without a command.
func isAlwaysPresent(*plan.Release) bool { return true }

// hasSignTask reports whether the package's release runs a sign task: its
// space enables autoSign, or configures any of the sign stage's three
// sequences, so a hook somebody configured never goes silent. A package with
// none of them has no sign task at all, and nothing about its run changes.
func hasSignTask(rel *plan.Release) bool {
	space := rel.Pkg.Space
	return space.AutoSign != nil ||
		len(space.SignScript) > 0 || len(space.BeforeSignScript) > 0 || len(space.PostSignScript) > 0
}

// hasVersionTask reports whether the package's release runs a version task:
// any provider of it moved, or its space auto-versions (whose reconciliation
// is unconditional per §9.4).
//
// Updates rather than DueTo, so that a hand-written flow.version script and a
// native autoVersion block answer the same question. Propagation depth is 0 by
// default, so gating on DueTo meant a consumer whose provider released beside
// it — without a caret between them — got no version stage at all, while the
// same space under autoVersion reconciled normally.
func hasVersionTask(rel *plan.Release) bool {
	return len(rel.Updates) > 0 || rel.Pkg.Space.AutoVersion != nil
}

// hasSyncLockTask reports whether the package's release regenerates lock
// files between its version stage and its build: it has a version stage and
// its space's autoVersion names syncLock scripts.
func hasSyncLockTask(rel *plan.Release) bool {
	av := rel.Pkg.Space.AutoVersion
	return hasVersionTask(rel) && av != nil && len(av.SyncLock) > 0
}

// hasTestsTask reports whether the package's release runs a tests task: its
// space configures any of the tests stage's three sequences, so a hook
// somebody configured never goes silent. A package with none of them has no
// tests task at all, and nothing about its run changes.
func hasTestsTask(rel *plan.Release) bool {
	space := rel.Pkg.Space
	return len(space.TestsScript) > 0 || len(space.BeforeTestsScript) > 0 || len(space.PostTestsScript) > 0
}
