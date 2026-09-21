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
// Two things decide it. `runOnly` is what the operator said, and it is a
// filter over the pool exactly as `buildPlatforms` is one. The space's login
// is what the operator implied: a login is authentication, authentication
// must not travel, so a space that logs in publishes on the orchestrator
// whatever else is stated. The configuration refuses the pair that would
// contradict that, so the rule here never overrules a statement the operator
// was allowed to make.

import (
	public "github.com/yohimik/dispat/pkg/models"
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
// The publish stage is not the build stage and the difference is deliberate.
// A publication is serialized per repository anyway, so delegating one buys a
// run nothing and spreads the registry credentials over one more machine;
// therefore publication is delegated only where the operator asked for it in
// so many words, and `both` keeps it here. A build is the opposite: it is the
// work a pool exists for, so `both` offers it to the pool.
func ResolveStagePlacement(stage, value string, isSpaceLoggingIn bool) Placement {
	if stage != StagePublish {
		return resolveBuildPlacement(value)
	}
	if isSpaceLoggingIn {
		// The configuration refused `publish: worker` beside a login, so this
		// overrules nothing an operator was allowed to state: it is what
		// `both` means for a space whose publish needs credentials.
		return PlacementOrchestrator
	}
	if value == public.RunOnlyWorker {
		return PlacementWorker
	}
	return PlacementOrchestrator
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

// The two stages a run may delegate, under the names the executor gives them.
// Everything else a release does (the version stage, the lock-file
// preparation, the login, the records, the tags and every run-level hook) is
// the orchestrator's and is not configurable, so it is not named here.
const (
	StageBuild   = "build"
	StagePublish = "publish"
)

// IsDelegable reports whether a placement may leave this machine, which is
// the one question a caller with no pool has to ask: a frame that must be
// delegated and a run with nobody to delegate to is a release that cannot be
// executed, and saying so before it starts is the whole of the check.
func (p Placement) IsDelegable() bool { return p == PlacementWorker }
