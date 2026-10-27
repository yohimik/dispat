// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/yohimik/dispat/services/dispat/internal/execution"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
	"github.com/yohimik/dispat/services/dispat/internal/script"
)

const (
	interpretedSweepArgumentsMismatch = "execution: interpreted sweep arguments differ from signed pinned selection or private script"
	interpretedSweepNoArguments       = 0
	interpretedSweepFirstByte         = 0
	interpretedSweepNextByte          = 1
	interpretedSweepSpace             = ' '
	interpretedSweepQuote             = '\''
	interpretedSweepEscape            = '\\'
	interpretedSweepMissingTemplate   = "execution: interpreted sweep has no signed executable command for its pinned arguments"
	interpretedSweepNoCommands        = 0
	interpretedSweepPriorCommand      = 1
)

func (a *App) prepareInterpretedSweep(ctx context.Context,
	options execution.InterpretedRunOptions) (_ *interpretedPlan, err error) {
	planning := interpretedPlanningOptions(options)
	planning.Log, planning.MaxPackBytes = a.log, formatTransferLimits(a.cfg.Execution).MaxPackBytes
	private, err := recomputePeerSweep(ctx, planning)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, private.close())
		}
	}()
	settings, policy := private.app.cfg.Execution, options.Bootstrap.JoinPolicy
	if settings.ResolveMaxAttempts() != policy.MaxAttempts ||
		settings.RetryAfterUncertainAttempt != policy.RetryAfterUncertainAttempt {
		return nil, fmt.Errorf(interpretedRetryPolicyMismatchError)
	}
	specs, err := private.interpretedSweepSpecs(options.Roster)
	if err != nil {
		return nil, err
	}
	applyChoreographyRetrySafety(specs, policy.RetryAfterUncertainAttempt)
	applyChoreographyAttemptLimit(specs, policy)
	selection, err := decodePinnedSweepSelection(options.Bootstrap.PinnedInputs)
	if err != nil {
		return nil, err
	}
	args, err := private.interpretedSweepArgs(options.Carrier.Templates, specs, selection.ArgsSHA256)
	if err != nil {
		return nil, err
	}
	work := &scriptWork{app: private.app, pl: private.plan, name: private.name,
		args: args, onError: private.onError,
		wsVars:  release.WorkspaceEnv(private.plan, private.app.log),
		covered: coveredReleases(private.plan, private.covered)}
	work.roots, _ = private.app.cfg.FindRunOutputs(work.name)
	templates, err := compileSweepChoreographyTemplatesWithInputs(sweepChoreographyTemplateOptions{
		Work: work, Tasks: specs, Sources: private.app.resolveInputSources(private.plan),
		Shell:       private.app.resolveShell,
		TaskTimeout: time.Duration(settings.ResolveTimeouts().Task) * time.Second,
		Limits:      formatTransferLimits(settings)})
	if err != nil {
		return nil, err
	}
	policies := sweepChoreographyPackagePolicies(private.plan, private.covered, private.onError)
	outcomes, err := execution.NewChoreographyPackageOutcomeEvaluator(policies)
	if err != nil {
		return nil, err
	}
	semanticDigest, err := execution.DigestSweepTasks(options.Bootstrap.PlanDigest,
		private.name, private.tasks)
	if err != nil || semanticDigest != options.Carrier.SemanticGraphDigest {
		return nil, fmt.Errorf(pinnedPlanningPeerSweepGraphMismatchErrorText)
	}
	frozen := freezeChoreographySweepPlan(private.plan)
	result := &interpretedPlan{App: private.app, Plan: private.plan, Tasks: specs,
		Templates: templates, Packages: policies, SemanticGraphDigest: semanticDigest,
		OutcomePolicy: outcomes, Sweep: true, IncludeFailedExports: private.onError == OnErrorContinue,
		Close:    private.close,
		Contexts: make(map[string]execution.ChoreographyBindingOutputContext, len(specs))}
	byTask := make(map[string]execution.ChoreographyTaskSpec, len(specs))
	for _, spec := range specs {
		byTask[spec.Task] = spec
		rel := frozen.Releases[spec.Package]
		if rel == nil || rel.Pkg == nil {
			return nil, fmt.Errorf(missingSweepPinnedPackageErrorFormat, spec.Task)
		}
		result.Contexts[spec.Task] = execution.ChoreographyBindingOutputContext{
			Repository: rel.Pkg.Repository, Package: rel.Pkg.Name,
			SourceRoot: rel.Pkg.Dir, Roots: slices.Clone(work.roots)}
	}
	result.Accumulate = func(taskID string, predecessors map[string][]plan.Output,
		own []plan.Output) ([]plan.Output, error) {
		visible, err := sweepSignedVisibleRelease(sweepSignedVisibleReleaseOptions{
			Plan: frozen, Task: byTask[taskID], Predecessors: predecessors, OwnOutputs: own})
		if err != nil {
			return nil, err
		}
		return visible.Outputs, nil
	}
	result.PrepareRequest = func(taskID string,
		state execution.ChoreographyVerifiedBindingState) (release.StageRequest, error) {
		spec := byTask[taskID]
		rel := frozen.Releases[spec.Package]
		if rel == nil || rel.Pkg == nil || rel.Pkg.Space == nil {
			return release.StageRequest{}, fmt.Errorf(missingSweepPinnedPackageErrorFormat, taskID)
		}
		visible, err := sweepSignedVisibleRelease(sweepSignedVisibleReleaseOptions{
			Plan: frozen, Task: spec, Predecessors: state.PredecessorOutputs})
		if err != nil {
			return release.StageRequest{}, err
		}
		commands, found := rel.Pkg.Space.Script(work.name)
		if !found {
			return release.StageRequest{}, fmt.Errorf(choreographyMissingSweepTaskScriptErrorFormat, taskID)
		}
		return release.StageRequest{TaskID: taskID, Release: visible,
			Stage: work.stage(), Dir: rel.Pkg.Dir, StaticEnv: rel.Pkg.Space.Env,
			Env: release.ComputedSweepFrameEnv(release.ComputedSweepFrameEnvOptions{
				Plan: frozen, Visible: visible, Package: spec.Package,
				Stage: work.stage(), WorkspaceVars: work.wsVars}),
			Frame: release.StageFrame{Commands: script.AppendArgsToLast(commands, work.args)}}, nil
	}
	return result, nil
}

func (p *peerSweepPlan) interpretedSweepArgs(templates []execution.ChoreographyCompiledTemplate,
	specs []execution.ChoreographyTaskSpec, signedHash string) ([]string, error) {
	byTask := make(map[string]execution.ChoreographyTaskSpec, len(specs))
	for _, spec := range specs {
		byTask[spec.Task] = spec
	}
	for _, template := range templates {
		spec := byTask[template.Task]
		rel := p.plan.Releases[spec.Package]
		if rel == nil || rel.Pkg == nil || rel.Pkg.Space == nil ||
			template.Frame == nil || len(template.Frame.Commands) == interpretedSweepNoCommands {
			continue
		}
		commands, found := rel.Pkg.Space.Script(p.name)
		if !found || len(commands) != len(template.Frame.Commands) ||
			len(commands) == interpretedSweepNoCommands ||
			!slices.Equal(commands[:len(commands)-interpretedSweepPriorCommand],
				template.Frame.Commands[:len(commands)-interpretedSweepPriorCommand]) {
			return nil, fmt.Errorf(interpretedSweepArgumentsMismatch)
		}
		return recoverInterpretedSweepArgs(commands[len(commands)-interpretedSweepPriorCommand],
			template.Frame.Commands[len(commands)-interpretedSweepPriorCommand], signedHash)
	}
	return nil, fmt.Errorf(interpretedSweepMissingTemplate)
}

// recoverInterpretedSweepArgs decodes only script.AppendArgs' canonical
// suffix. The signed template already carries these command bytes; this
// restores the invocation without adding a second raw-argument carrier.
func recoverInterpretedSweepArgs(base, rendered, signedHash string) ([]string, error) {
	var args []string
	if rendered != base {
		args = make([]string, interpretedSweepNoArguments)
		if !strings.HasPrefix(rendered, base+string(interpretedSweepSpace)) {
			return nil, fmt.Errorf(interpretedSweepArgumentsMismatch)
		}
		suffix := rendered[len(base)+interpretedSweepNextByte:]
		for len(suffix) != interpretedSweepNoArguments {
			var word strings.Builder
			quoted, escaped := false, false
			index := interpretedSweepFirstByte
			for ; index < len(suffix); index++ {
				character := suffix[index]
				if escaped {
					word.WriteByte(character)
					escaped = false
					continue
				}
				if character == interpretedSweepQuote {
					quoted = !quoted
					continue
				}
				if !quoted && character == interpretedSweepEscape {
					escaped = true
					continue
				}
				if !quoted && character == interpretedSweepSpace {
					break
				}
				word.WriteByte(character)
			}
			if quoted || escaped {
				return nil, fmt.Errorf(interpretedSweepArgumentsMismatch)
			}
			args = append(args, word.String())
			if index == len(suffix) {
				break
			}
			suffix = suffix[index+interpretedSweepNextByte:]
		}
	}
	if script.AppendArgs(base, args) != rendered {
		return nil, fmt.Errorf(interpretedSweepArgumentsMismatch)
	}
	encoded, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(encoded)
	if hex.EncodeToString(hash[:]) != signedHash {
		if rendered == base {
			empty := make([]string, interpretedSweepNoArguments)
			encoded, err = json.Marshal(empty)
			if err != nil {
				return nil, err
			}
			hash = sha256.Sum256(encoded)
			if hex.EncodeToString(hash[:]) == signedHash {
				return empty, nil
			}
		}
		return nil, fmt.Errorf(interpretedSweepArgumentsMismatch)
	}
	return args, nil
}
