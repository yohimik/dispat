// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// Where one stage frame of one package is allowed to run (CCME §28.1).
//
// A frame is its before hook, the stage's own commands and its after hook,
// and it always runs as one unit on one machine: splitting it would mean a
// hook observing a folder some other node wrote. So there is exactly one
// placement question per frame, and it is answered here.
//
// `runOnly` filters build and test placement over the pool, as
// `buildPlatforms` does. Publication stays with the orchestrator and its
// credentials regardless of available worker capacity.

import (
	public "github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// Placement is where a run may put one stage frame.
//
// Three values rather than the key's three words, because they are not the
// same question: the key says what the operator allows, and this says what
// the pool is then asked for. `both` and a publish are one placement here,
// and that is the whole reason the two vocabularies are kept apart.
type Placement string

// The three placements. AnyNode is the only one that leaves the pool a
// choice; the other two name the machine and the pool either has it free or
// waits for it.
const (
	// PlacementAnyNode is a frame that may go anywhere: a worker if one has
	// room, and the orchestrator if none has.
	PlacementAnyNode Placement = "any"
	// PlacementWorker is a frame that may only be delegated. A run with no
	// worker link cannot execute one at all and is refused before it starts.
	PlacementWorker Placement = "worker"
	// PlacementOrchestrator is a frame that may only run on the machine the
	// release was started on.
	PlacementOrchestrator Placement = "orchestrator"
)

// ResolveStagePlacement answers where one stage frame runs.
//
// Publication and release records stay on the orchestrator. Build and test
// frames may use the worker pool under the package's build placement.
func ResolveStagePlacement(stage, value string, _ bool) Placement {
	if stage == StagePublish {
		return PlacementOrchestrator
	}
	return resolveBuildPlacement(value)
}

// resolveBuildPlacement is the build stage's own reading of the key, which is
// the plain one: what it says is what the pool is asked for.
func resolveBuildPlacement(value string) Placement {
	switch value {
	case public.RunOnlyWorker:
		return PlacementWorker
	case public.RunOnlyOrchestrator:
		return PlacementOrchestrator
	default:
		return PlacementAnyNode
	}
}

// StageBuild names the placement shared by build, test and configured
// computation stages. StagePublish is retained as an assignment identity for
// refusing old worker publication requests; current releases execute it here.
const (
	StageBuild   = "build"
	StagePublish = "publish"
)

// resolveDescriptorPlacement places a publication here and uses the build
// preference only for other delegable frames.
func resolveDescriptorPlacement(descriptor release.Stage, space *model.Space) Placement {
	if descriptor.Placement == release.PlacementByPublish {
		return PlacementOrchestrator
	}
	return ResolveStagePlacement(StageBuild, space.RunOnly.ResolveBuild(), len(space.LoginScript) > 0)
}

// IsDelegable reports whether a placement may leave this machine, which is
// the one question a caller with no pool has to ask: a frame that must be
// delegated and a run with nobody to delegate to is a release that cannot be
// executed, and saying so before it starts is the whole of the check.
func (p Placement) IsDelegable() bool { return p == PlacementWorker }
