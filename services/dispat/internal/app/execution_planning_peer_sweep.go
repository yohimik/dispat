// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/yohimik/dispat/services/dispat/internal/execution"
	"github.com/yohimik/dispat/services/dispat/internal/filter"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

type peerSweepPlan struct {
	app     *App
	inputs  *planningMaterialization
	plan    *plan.Plan
	covered []string
	tasks   []execution.ReservationTask
	name    string
	onError string
}

func (p *peerSweepPlan) close() error {
	if p == nil {
		return nil
	}
	return p.inputs.close()
}

// recomputePeerSweep independently derives the same covered packages and task
// graph. The signed arguments hash is verified against the eventual task
// frame; raw argument values are not put into a Git carrier.
func recomputePeerSweep(ctx context.Context, options peerReleasePlanningOptions) (_ *peerSweepPlan, err error) {
	inputs := options.Inputs
	if options.Binding.Run == pinnedPlanningAbsentText || options.Binding.Generation == pinnedPlanningAbsentText ||
		options.Binding.PlanDigest == pinnedPlanningAbsentText || options.Binding.GraphDigest == pinnedPlanningAbsentText ||
		options.SemanticGraphDigest == pinnedPlanningAbsentText {
		return nil, fmt.Errorf(pinnedPlanningPeerSweepIdentityIncompleteErrorText)
	}
	selection, err := decodePinnedSweepSelection(inputs)
	if err != nil {
		return nil, err
	}
	environment, err := openPeerPlanningEnvironment(ctx, options)
	if err != nil {
		return nil, err
	}
	materialized, private := environment.inputs, environment.app
	defer func() {
		if err != nil {
			_ = materialized.close()
		}
	}()
	computed, err := private.computePlan(ctx)
	if err != nil {
		return nil, err
	}
	if computed.IsFatal() {
		return nil, fmt.Errorf(pinnedPlanningSweepPlanFatalErrorText)
	}
	digest, err := private.calculatePlanDigest(ctx, computed, ReleaseOptions{})
	if err != nil {
		return nil, err
	}
	if digest != options.Binding.PlanDigest {
		return nil, fmt.Errorf(pinnedPlanningPeerSweepPlanMismatchErrorText)
	}
	_, policy, err := private.marshalPinnedDocuments(selection)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(policy, inputs.Options) {
		return nil, fmt.Errorf(pinnedPlanningPeerSweepPolicyMismatchErrorText)
	}
	if !private.scriptDefinedAnywhere(selection.Script) {
		return nil, fmt.Errorf(pinnedPlanningSweepScriptUndefinedErrorFormat, selection.Script)
	}
	window := WindowOptions{Since: selection.Since, Consumers: selection.Consumers,
		Filter: filter.Filter{Packages: selection.Packages, Spaces: selection.Spaces, Groups: selection.Groups}}
	if selection.Dir != pinnedPlanningAbsentText {
		window.Filter.Dir, err = rebasePlanningPath(
			filepath.Join(private.root, filepath.FromSlash(selection.Dir)), private.root, private.root)
		if err != nil {
			return nil, err
		}
	}
	_, covered, err := private.coveredSelection(ctx, computed, window)
	if err != nil {
		return nil, err
	}
	selected := slices.Clone(covered)
	slices.Sort(selected)
	if !slices.Equal(selected, selection.Covered) {
		return nil, fmt.Errorf(pinnedPlanningPeerSweepCoverageMismatchErrorText)
	}
	work := &scriptWork{app: private, pl: computed, name: selection.Script,
		covered: coveredReleases(computed, covered)}
	tasks := make([]execution.ReservationTask, pinnedPlanningInitialEntryCount, len(covered))
	for _, rel := range work.resolveReachedReleases(covered) {
		space := rel.Pkg.Space
		tasks = append(tasks, execution.ReservationTask{
			Task: rel.Pkg.Name + pinnedPlanningTaskSeparatorText + execution.KindRun, Kind: execution.KindRun,
			Platforms: space.BuildPlatforms,
			Placement: execution.ResolveStagePlacement(execution.StageBuild,
				space.RunOnly.ResolveBuild(), len(space.LoginScript) > pinnedPlanningEmptyEntryCount),
		})
	}
	graphDigest, err := execution.DigestSweepTasks(digest, selection.Script, tasks)
	if err != nil {
		return nil, err
	}
	if graphDigest != options.SemanticGraphDigest {
		return nil, fmt.Errorf(pinnedPlanningPeerSweepGraphMismatchErrorText)
	}
	return &peerSweepPlan{app: private, inputs: materialized, plan: computed,
		covered: covered, tasks: tasks, name: selection.Script, onError: selection.OnError}, nil
}

func decodePinnedSweepSelection(inputs execution.PinnedRunInputs) (pinnedPlanningSelection, error) {
	selection, err := decodePinnedSelection(inputs.Selection)
	if err != nil ||
		selection.Command != pinnedPlanningRunCommandName || selection.Script == pinnedPlanningAbsentText || !IsValidOnError(selection.OnError) ||
		selection.Strict || selection.RequireRelease ||
		filepath.IsAbs(selection.Dir) || selection.Dir == pinnedPlanningParentPath ||
		strings.HasPrefix(filepath.Clean(selection.Dir), pinnedPlanningParentPath+string(filepath.Separator)) {
		return pinnedPlanningSelection{}, fmt.Errorf(pinnedPlanningSweepSelectionInvalidErrorText)
	}
	encodedHash, err := hex.DecodeString(selection.ArgsSHA256)
	if err != nil || len(encodedHash) != pinnedPlanningArgumentDigestBytes || selection.ArgsSHA256 != hex.EncodeToString(encodedHash) {
		return pinnedPlanningSelection{}, fmt.Errorf(pinnedPlanningSweepArgumentsDigestAbsentErrorText)
	}
	anchor := inputs.SinceAnchor
	if anchor == nil {
		if selection.Since != pinnedPlanningAbsentText && selection.Since != SinceAll {
			return pinnedPlanningSelection{}, fmt.Errorf(pinnedPlanningSweepRevisionCarrierAbsentErrorText)
		}
	} else if anchor.Repository != inputs.RootRepository || anchor.OID == pinnedPlanningAbsentText || selection.Since != anchor.OID {
		return pinnedPlanningSelection{}, fmt.Errorf(pinnedPlanningSweepRevisionMismatchErrorText)
	}
	if !slices.IsSorted(selection.Covered) {
		return pinnedPlanningSelection{}, fmt.Errorf(pinnedPlanningSweepCoverageNoncanonicalErrorText)
	}
	return selection, nil
}
