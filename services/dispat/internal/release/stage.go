// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package release

// The stages of one package's release, as data.
//
// A stage is a descriptor rather than a code path: what it is called, which
// budget its tasks hold, where it may run, what a node running it must be
// given and whether it produces an output set. The executor reads every one
// of those questions off the descriptor, so a stage added to the table, or
// defined by a configuration, runs, reports, gates, fails, reverts and travels
// to a worker the way the stages beside it do. What a stage's commands are,
// which stages a package has and in which order they run are the answers
// read from the package's own configuration (see stageplan.go).

import (
	public "github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// StageBudget is the run-wide budget a stage's tasks hold a slot of: the
// build budget, shared by everything that prepares, builds or checks a
// package; the publications' own, shared by what records and observes a
// release; or the one serializing lock-file writers.
type StageBudget uint8

const (
	BudgetBuild StageBudget = iota
	BudgetPublish
	BudgetSyncLock
)

// StagePlacement is where a stage's frame may run: on the orchestrator alone,
// under the snapshot guard, because it writes the working tree delegated
// frames are snapshotted from or observes a release the orchestrator
// recorded; or where the package's build placement allows computation.
// Publication is always an orchestrator frame.
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
	// Label is the display name the configuration gave the stage, shown
	// beside Name in the log, the events and the summary; empty for a stage
	// that was given none.
	Label string
	// Budget is the run-wide budget its tasks hold a slot of.
	Budget StageBudget
	// Placement is where its frame may run.
	Placement StagePlacement
	// Inputs is what a node is given before the frame runs.
	Inputs StageInputs
	// OwnOutputStages names graph ancestors whose admitted output sets this
	// stage consumes. Their order is the package's stable stage order.
	OwnOutputStages []string
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
	// IsObserving says the stage runs after the release records: it observes
	// a release that is already out, so its whole frame only warns.
	IsObserving bool

	kind taskKind
	// isPresent answers whether a space's packages run the stage at all, as
	// far as the configuration alone decides it.
	isPresent func(*model.Space) bool
	// frame is the stage's hooks and commands as the package's space
	// configures them.
	frame func(*model.Space) stage
}

// userName is the stage name a task carries for a stage the configuration
// defines, which its kind alone cannot name; empty for every built-in stage.
func (s Stage) userName() string {
	if s.kind == taskUser {
		return s.Name
	}
	return ""
}

// stageTable is every built-in stage in the order a package's release runs
// them by default. The preparing stages always come first, in this order; the
// others take the places the package's flow gives them.
var stageTable = []Stage{
	{Name: public.FlowStageSign, Title: "Sign", Budget: BudgetBuild, IsPreparing: true, kind: taskSign,
		isPresent: hasSignStage,
		frame: func(s *model.Space) stage {
			return stage{before: s.BeforeSignScript, commands: s.SignScript, after: s.PostSignScript}
		}},
	{Name: string(model.StageVersion), Title: model.StageVersion.Title(), Budget: BudgetBuild,
		IsPreparing: true, kind: taskVersion, isPresent: isConfigured,
		frame: func(s *model.Space) stage {
			return stage{before: s.BeforeVersionScript, commands: s.VersionScript, after: s.PostVersionScript}
		}},
	{Name: public.FlowStageSyncLock, Title: "SyncLock", Budget: BudgetSyncLock, IsPreparing: true, kind: taskSyncLock,
		isPresent: hasSyncLockStage,
		frame:     func(s *model.Space) stage { return stage{commands: s.AutoVersion.SyncLock} }},
	{Name: public.FlowStageBuild, Title: "Build", Budget: BudgetBuild, Placement: PlacementByBuild,
		Inputs: InputsProviderOutputs, IsProducingOutputs: true, kind: taskBuild,
		isPresent: hasBuildStage,
		frame: func(s *model.Space) stage {
			return stage{before: s.BeforeBuildScript, commands: s.BuildScript, after: s.PostBuildScript}
		}},
	{Name: public.FlowStageTest, Title: "Test", Budget: BudgetBuild, Placement: PlacementByBuild,
		Inputs: InputsOwnOutputs, kind: taskTest, isPresent: hasTestStage,
		frame: func(s *model.Space) stage {
			return stage{before: s.BeforeTestScript, commands: s.TestScript, after: s.PostTestScript}
		}},
	// postPublish is not part of the publish frame: it only runs after the
	// package is fully published, in the publish tail.
	{Name: public.FlowStagePublish, Title: "Publish", Budget: BudgetPublish, Placement: PlacementByPublish,
		Inputs: InputsOwnOutputs, IsAuthorized: true, kind: taskPublish, isPresent: hasPublishStage,
		frame: func(s *model.Space) stage {
			return stage{before: s.BeforePublishScript, commands: s.PublishScript}
		}},
	// The announce stage observes a release that is out, by default: it runs
	// after the records, on the machine that wrote them, and only warns. A
	// flow that places it before the publish makes it gate like the stages
	// around it.
	{Name: public.FlowStageAnnounce, Title: "Announce", Budget: BudgetPublish, kind: taskAnnounce,
		isPresent: hasAnnounceStage,
		frame: func(s *model.Space) stage {
			return stage{before: s.BeforeAnnounceScript, commands: s.AnnounceScript, after: s.PostAnnounceScript}
		}},
}

// BuildStage is the build stage's descriptor, for a frame that is a build
// although no release task runs it: the preparation of a provider this run
// does not release.
func BuildStage() Stage { return stageOf(taskBuild) }

// stageOf is the descriptor of one built-in task kind, as stageTable states
// it.
func stageOf(kind taskKind) Stage {
	for _, descriptor := range stageTable {
		if descriptor.kind == kind {
			return descriptor
		}
	}
	panic("release: no stage descriptor for a task kind")
}

// isConfigured is the version stage's presence as far as the configuration
// decides it: every space may have one, and the plan says which package does
// (see hasVersionTask).
func isConfigured(*model.Space) bool { return true }

// hasSignStage reports whether a space's packages run a sign stage: the space
// enables autoSign, or configures any of the sign stage's three sequences, so
// a hook somebody configured never goes silent.
func hasSignStage(space *model.Space) bool {
	return space.AutoSign != nil ||
		len(space.SignScript) > 0 || len(space.BeforeSignScript) > 0 || len(space.PostSignScript) > 0
}

// hasSyncLockStage reports whether a space regenerates lock files between
// its version stage and its build: its autoVersion names syncLock scripts.
// Whether a package runs it this run follows its version stage.
func hasSyncLockStage(space *model.Space) bool {
	return space.AutoVersion != nil && len(space.AutoVersion.SyncLock) > 0
}

// hasBuildStage reports whether a space's packages run a build stage: any of
// its three sequences is configured. A package with none of them has no build
// task, no build hook and no build event.
func hasBuildStage(space *model.Space) bool {
	return len(space.BuildScript) > 0 || len(space.BeforeBuildScript) > 0 || len(space.PostBuildScript) > 0
}

// hasTestStage reports whether a space's packages run a test stage, on the
// same terms as the build.
func hasTestStage(space *model.Space) bool {
	return len(space.TestScript) > 0 || len(space.BeforeTestScript) > 0 || len(space.PostTestScript) > 0
}

// hasPublishStage reports whether a space's packages run a publish stage: its
// commands or either of its hooks are configured. A package without one is
// recorded after its last stage instead.
func hasPublishStage(space *model.Space) bool {
	return len(space.PublishScript) > 0 || len(space.BeforePublishScript) > 0 || len(space.PostPublishScript) > 0
}

// hasAnnounceStage reports whether a space's packages run an announce stage,
// on the same terms as the build.
func hasAnnounceStage(space *model.Space) bool {
	return len(space.AnnounceScript) > 0 || len(space.BeforeAnnounceScript) > 0 || len(space.PostAnnounceScript) > 0
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
	return hasVersionTask(rel) && hasSyncLockStage(rel.Pkg.Space)
}
