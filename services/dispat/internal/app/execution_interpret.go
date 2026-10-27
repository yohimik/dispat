// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/yohimik/dispat/services/dispat/internal/execution"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

const (
	interpretedGraphMismatchError            = "execution: independently interpreted executable graph differs from signed bootstrap"
	interpretedTaskMissingErrorFormat        = "execution: interpreted task %q has no independently derived executable template"
	interpretedCommandUnsupportedErrorFormat = "execution: pinned command %q cannot be interpreted"
	interpretedModeName                      = "interpretation"
)

type interpretedPlan struct {
	App                  *App
	Plan                 *plan.Plan
	Tasks                []execution.ChoreographyTaskSpec
	Templates            []execution.ChoreographyCompiledTemplate
	Packages             []execution.ChoreographyPackagePolicy
	SemanticGraphDigest  string
	OutcomePolicy        execution.ChoreographyOutcomePolicyx
	Contexts             map[string]execution.ChoreographyBindingOutputContext
	OutcomePackages      map[string][]string
	InputPackages        map[string][]string
	Accumulate           func(string, map[string][]plan.Output, []plan.Output) ([]plan.Output, error)
	PrepareRequest       func(string, execution.ChoreographyVerifiedBindingState) (release.StageRequest, error)
	Sweep                bool
	IncludeFailedExports bool
	Close                func() error
}

func (a *App) interpretedRunPlanner(node string, signer *execution.Signer) execution.PrepareInterpretedRun {
	return func(ctx context.Context, options execution.InterpretedRunOptions) (_ *execution.InterpretedRun, err error) {
		selection, err := decodePinnedSelection(options.Bootstrap.PinnedInputs.Selection)
		if err != nil {
			return nil, err
		}
		var private *interpretedPlan
		switch selection.Command {
		case pinnedPlanningReleaseCommandName:
			private, err = a.prepareInterpretedRelease(ctx, options)
		case pinnedPlanningRunCommandName:
			private, err = a.prepareInterpretedSweep(ctx, options)
		default:
			return nil, fmt.Errorf(interpretedCommandUnsupportedErrorFormat, selection.Command)
		}
		if err != nil {
			return nil, err
		}
		defer func() {
			if err != nil {
				err = errors.Join(err, private.Close())
			}
		}()
		return a.openInterpretedFrames(ctx, interpretedFrameOptions{
			Run: options, Plan: private, Node: node, Signer: signer})
	}
}

type interpretedFrameOptions struct {
	Run    execution.InterpretedRunOptions
	Plan   *interpretedPlan
	Node   string
	Signer *execution.Signer
}

func (a *App) openInterpretedFrames(ctx context.Context, options interpretedFrameOptions) (*execution.InterpretedRun, error) {
	run, private := options.Run, options.Plan
	bootstrap, binding := run.Bootstrap, run.Bootstrap.ChoreographyRunBinding
	digest, err := execution.DigestChoreographyLogicalGraph(execution.ChoreographyLogicalGraphInventory{
		Binding: binding, AlgorithmVersion: bootstrap.AlgorithmVersion,
		SemanticGraphDigest: private.SemanticGraphDigest, Tasks: private.Tasks,
		Templates: private.Templates, PackagePolicies: private.Packages})
	if err != nil {
		return nil, err
	}
	if digest != binding.GraphDigest || bootstrap.Mode != interpretedModeName {
		return nil, fmt.Errorf(interpretedGraphMismatchError)
	}
	if err := verifyRetainedChoreographyPolicy(retainedChoreographyPolicyOptions{
		RecomputedTasks: private.Tasks, SignedTasks: run.Carrier.Tasks,
		RecomputedPackages: private.Packages, SignedPackages: run.Carrier.PackagePolicies}); err != nil {
		return nil, err
	}
	limits := formatTransferLimits(private.App.cfg.Execution)
	store := &gitx.LocalGitx{Dir: run.ObjectStoreDir, Log: a.log}
	mailbox := execution.NewGitMailbox(bootstrap.CoordinationEndpoint, store, options.Signer, a.log)
	ledger, _, err := execution.ReadChoreographyLedgerRoot(ctx, mailbox, binding, run.BootstrapOID)
	if err != nil {
		return nil, err
	}
	feed, err := execution.OpenRetainedChoreographyFeed(ctx, execution.RetainedChoreographyFeedOptions{
		Mailbox: mailbox, Binding: binding, BootstrapOID: run.BootstrapOID,
		MaxDocumentBytes: limits.MaxManifestBytes, MaxTagBytes: limits.MaxPackBytes})
	if err != nil {
		return nil, err
	}
	cache, err := execution.NewChoreographyBindingCache(execution.ChoreographyBindingCacheOptions{
		Binding: binding, LedgerRootOID: ledger.OID, Reader: feed, Mailbox: mailbox,
		MaxDocumentBytes: limits.MaxManifestBytes, Tasks: private.Tasks,
		OutputContexts: private.Contexts, OutcomePackagesByTask: private.OutcomePackages,
		InputPackagesByTask: private.InputPackages, Accumulate: private.Accumulate,
		IncludeFailedExports: private.IncludeFailedExports})
	if err != nil {
		return nil, err
	}
	templates := make(map[string]execution.ChoreographyCompiledTemplate, len(private.Templates))
	for _, template := range private.Templates {
		templates[template.Task] = template
	}
	return &execution.InterpretedRun{
		Binding: binding, SemanticGraphDigest: private.SemanticGraphDigest,
		Tasks: private.Tasks, OutcomePolicy: private.OutcomePolicy, Close: private.Close,
		PrepareReady: func(ctx context.Context, input execution.InterpretedReadyInput) (execution.InterpretedReadyFrame, error) {
			selected := input.Task
			template, found := templates[selected.Spec.Task]
			if !found {
				return execution.InterpretedReadyFrame{}, fmt.Errorf(interpretedTaskMissingErrorFormat, selected.Spec.Task)
			}
			state, err := cache.State(ctx, selected.Spec.Task)
			if err != nil {
				return execution.InterpretedReadyFrame{}, err
			}
			request, err := private.PrepareRequest(selected.Spec.Task, state)
			if err != nil {
				return execution.InterpretedReadyFrame{}, err
			}
			bindings, err := execution.ResolveInterpretedChoreographyBindings(ctx, execution.ChoreographyInterpretedBindingOptions{
				Request: request, PreparedOID: state.PreparedInputOID, State: state,
				Sources:        private.App.resolveInputSources(private.Plan)(selected.Spec.Package),
				InputProviders: private.App.resolveOutputProviders(private.Plan)(selected.Spec.Package),
				Mailbox:        mailbox, Store: store, Binding: binding, GraphRootOID: run.GraphRootOID,
				TemplateDigest: selected.Spec.TemplateDigest, MaxBytes: limits.MaxManifestBytes, Sweep: private.Sweep})
			if err != nil {
				return execution.InterpretedReadyFrame{}, err
			}
			assignment, err := execution.BindChoreographyCompiledTemplate(execution.ChoreographyTemplateBindingOptions{
				Template: template, Bindings: bindings, BootstrapOID: run.BootstrapOID, GraphDigest: binding.GraphDigest,
				Header: execution.Header{Protocol: execution.ChoreographyProtocolVersion,
					Run: binding.Run, Generation: binding.Generation, PlanDigest: binding.PlanDigest,
					Task: selected.Spec.Task, Kind: selected.Spec.Kind, Node: options.Node, Attempt: selected.Attempt,
					Branch: execution.FormatChoreographyTaskBranch(binding.Run, selected.Spec.Task)}})
			if err != nil {
				return execution.InterpretedReadyFrame{}, err
			}
			assignment.GraphRootOID, assignment.InputsDigest = run.GraphRootOID, input.InputsDigest
			return execution.InterpretedReadyFrame{Assignment: assignment, Bindings: bindings}, nil
		},
	}, nil
}

func interpretedPlanningOptions(options execution.InterpretedRunOptions) peerReleasePlanningOptions {
	sources := make(map[string]string, len(options.Bootstrap.PinnedInputs.Repositories))
	for _, source := range options.Bootstrap.PinnedInputs.Repositories {
		sources[source.Name] = options.ObjectStoreDir
	}
	return peerReleasePlanningOptions{Inputs: options.Bootstrap.PinnedInputs,
		Binding: options.Bootstrap.ChoreographyRunBinding, SourceDirs: sources,
		SemanticGraphDigest: options.Carrier.SemanticGraphDigest}
}
