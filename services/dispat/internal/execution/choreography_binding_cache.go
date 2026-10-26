// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

const choreographyBindingEventPageSize = 32
const choreographyBindingMissingStateErrorFormat = "execution: task %q lacks signed predecessor state for %q: %w"
const choreographyBindingInvalidResultErrorFormat = "execution: task %q has no verified signed Result for terminal receipt %s"
const choreographyAcceptedSnapshotMismatchErrorFormat = "execution: accepted build of %s used a different pinned source snapshot"
const choreographyBindingCarryResultErrorFormat = "execution: carrying signed result %s: %w"
const choreographyBindingMissingPolicyError = "execution: signed binding cache lacks ledger, graph or accumulation policy"
const choreographyBindingDuplicateTaskError = "execution: signed binding cache repeats a logical task"
const choreographyBindingInvalidCursorError = "execution: signed binding feed has an invalid event cursor"
const choreographyBindingSkippedEpochError = "execution: signed binding feed skipped an exact event epoch"
const choreographyBindingCursorTailError = "execution: signed binding feed cursor differs from its exact event tail"
const choreographyBindingClaimHistoryErrorFormat = "execution: accepted Result for task %q lacks signed claim history: %w"
const choreographyBindingUnknownTaskErrorFormat = "execution: signed binding feed names an unknown task %q"
const choreographyBindingNoClaimPredecessorError = "execution: package settlement has no exact typed no-claim predecessor"
const choreographyBindingTaskSettlementPredecessorError = "execution: task package settlement has no exact terminal predecessor"
const choreographyBindingAbsentTaskErrorFormat = "execution: binding task %q is absent or virtual"
const choreographyBindingMissingFinalityErrorFormat = "execution: predecessor %q has no signed logical finality"
const choreographyBindingMissingOutputPolicyErrorFormat = "execution: typed output %q has no pinned package path policy"
const choreographyBindingUnknownExportTaskErrorFormat = "execution: signed export ancestry names an unknown task %q"
const choreographyBindingMissingExportPolicyError = "execution: signed typed export has no pinned task path policy"
const choreographyBindingMissingCompiledPolicyError = "execution: signed compiled frame has no pinned source or input policy"
const choreographyBindingMissingOutputIdentityError = "execution: signed provider output lacks exact Result and manifest identity"
const choreographyBindingMissingSweepPolicyError = "execution: signed sweep frame has no pinned source or input policy"
const choreographyBindingMissingCarriedResultError = "execution: inherited declared path lacks verified Result and manifest"
const choreographyBindingForeignCarriedRepositoryError = "execution: inherited declared path names a repository outside its input closure"
const choreographyOwnBuildStage = "build"

type ChoreographyBindingEventReaderx interface {
	ReadEvents(context.Context, ChoreographyEventCursor, int) (ChoreographyEventPage, error)
}

// ChoreographyInputPackages returns the pinned provider closure relevant to
// one package so independent claims do not scan all prior output manifests.
func (c *Coordinator) ChoreographyInputPackages(packageName string) []string {
	if c == nil || c.dispatch.Inputs == nil {
		return nil
	}
	inputs := c.dispatch.Inputs(packageName)
	packages := make([]string, 0, len(inputs))
	for _, input := range inputs {
		packages = append(packages, input.Package)
	}
	return packages
}

// CacheAcceptedChoreographyBuildSnapshot is only an in-process optimization
// after a durable accepted Claim. A restart must reconstruct this value from
// the signed Claim and its exact Assignment parent before publication.
func (c *Coordinator) CacheAcceptedChoreographyBuildSnapshot(spec ChoreographyTaskSpec,
	bindings ChoreographyTaskBindings, repository string) {
	if c == nil || c.snapshots == nil || spec.Package == "" ||
		(spec.Kind != string(KindBuild) && spec.Kind != string(KindPrepare)) {
		return
	}
	for _, source := range bindings.Repositories {
		if source.Name != repository || source.Snapshot == "" {
			continue
		}
		c.snapshots.mu.Lock()
		prior := c.snapshots.consumed[spec.Package]
		if prior == "" {
			c.snapshots.consumed[spec.Package] = source.Snapshot
		}
		c.snapshots.mu.Unlock()
		if prior != "" && prior != source.Snapshot {
			c.Log.Error().Str("package", spec.Package).
				Msgf(choreographyAcceptedSnapshotMismatchErrorFormat, spec.Package)
		}
		return
	}
}

// ChoreographyBindingOutputContext fixes the declared output path scope used
// to rehome a typed export. These values come from the pinned entry plan.
type ChoreographyBindingOutputContext struct {
	Repository string
	Package    string
	SourceRoot string
	Roots      []string
}

type ChoreographyBindingCacheOptions struct {
	Binding               ChoreographyRunBinding
	LedgerRootOID         string
	Reader                ChoreographyBindingEventReaderx
	Mailbox               *GitMailbox
	MaxDocumentBytes      int64
	Tasks                 []ChoreographyTaskSpec
	OutputContexts        map[string]ChoreographyBindingOutputContext
	IncludeFailedExports  bool
	OutcomePackagesByTask map[string][]string
	InputPackagesByTask   map[string][]string
	Accumulate            func(string, map[string][]plan.Output, []plan.Output) ([]plan.Output, error)
}

// ChoreographyVerifiedOutput names bytes proved by a signed Result and the
// Result's exact declared manifest. Consumers carry this Result as a direct
// assignment parent; no mutable provider ref is consulted.
type ChoreographyVerifiedOutput struct {
	Task      string
	Package   string
	ResultOID string
	Manifest  *OutputManifest
}

type ChoreographyVerifiedCarriedExport struct {
	Signed ExportedValue
	Output ChoreographyVerifiedOutput
}

type ChoreographyVerifiedBindingState struct {
	ProviderOutcomes        map[string]release.PackageOutcome
	PredecessorOutputs      map[string][]plan.Output
	PredecessorTaskOIDs     []string
	PreparedInputOID        string
	VerifiedOutputByPackage map[string]ChoreographyVerifiedOutput
	VerifiedOutputByStage   map[string]ChoreographyVerifiedOutput
	VerifiedCarriedExports  map[plan.Output]ChoreographyVerifiedCarriedExport
}

type choreographyInputDestination struct{ oid, path string }

type choreographyCarriedExportInputs struct {
	Outputs      []plan.Output
	Verified     map[plan.Output]ChoreographyVerifiedCarriedExport
	Sources      []Source
	SourceDir    string
	Imported     map[string]bool
	Seen         map[choreographyInputDestination]bool
	ImportCommit func(context.Context, string, string) error
}

func applyVerifiedChoreographyExports(ctx context.Context, bindings *ChoreographyTaskBindings,
	inputs choreographyCarriedExportInputs) error {
	for index, output := range inputs.Outputs {
		carried, found := inputs.Verified[output]
		if !found {
			continue
		}
		identity := carried.Output
		if carried.Signed.DeclaredPath == nil || identity.ResultOID == "" ||
			identity.Manifest == nil || identity.Manifest.Digest == "" ||
			identity.Task == "" || identity.Package == "" {
			return fmt.Errorf(choreographyBindingMissingCarriedResultError)
		}
		path := ""
		for _, source := range inputs.Sources {
			if source.Name == carried.Signed.DeclaredPath.Repository {
				path = source.Path
				break
			}
		}
		if path == "" {
			return fmt.Errorf(choreographyBindingForeignCarriedRepositoryError)
		}
		bindings.Exports[index] = carried.Signed
		if err := appendVerifiedChoreographyInput(ctx, bindings, AssignmentInput{
			Task: identity.Task, Package: identity.Package, Commit: identity.ResultOID,
			Digest: identity.Manifest.Digest, Path: path}, inputs.SourceDir,
			inputs.Imported, inputs.Seen, inputs.ImportCommit); err != nil {
			return err
		}
	}
	return nil
}

func appendVerifiedChoreographyInput(ctx context.Context, bindings *ChoreographyTaskBindings,
	input AssignmentInput, sourceDir string, imported map[string]bool,
	seen map[choreographyInputDestination]bool,
	importCommit func(context.Context, string, string) error) error {
	key := choreographyInputDestination{input.Commit, input.Path}
	if seen[key] {
		return nil
	}
	if !imported[input.Commit] {
		if err := importCommit(ctx, sourceDir, input.Commit); err != nil {
			return fmt.Errorf(choreographyBindingCarryResultErrorFormat, input.Commit, err)
		}
		imported[input.Commit] = true
	}
	bindings.Inputs = append(bindings.Inputs, input)
	seen[key] = true
	return nil
}

type choreographyAcceptedBindingResult struct {
	result Result
	oid    string
}

// ChoreographyBindingCache consumes only the signed first-node ledger. Its
// cursor is advanced after every verified page, so preparing independent
// ready tasks does not rescan the graph or all task refs.
type ChoreographyBindingCache struct {
	mu              sync.Mutex
	opts            ChoreographyBindingCacheOptions
	cursor          ChoreographyEventCursor
	tasks           map[string]ChoreographyTaskSpec
	accepted        map[string]choreographyAcceptedBindingResult
	terminal        map[string]ChoreographyEvent
	pendingNoClaim  map[string]bool
	pendingTask     map[string]bool
	prepared        map[string]string
	outcomes        map[string]release.PackageOutcome
	providers       map[string]ChoreographyVerifiedOutput
	stages          map[string]ChoreographyVerifiedOutput
	stagesByPackage map[string][]string
	outputMemo      map[string][]plan.Output
}

func NewChoreographyBindingCache(opts ChoreographyBindingCacheOptions) (*ChoreographyBindingCache, error) {
	if !opts.Binding.valid() || opts.LedgerRootOID == "" || opts.Reader == nil ||
		opts.Mailbox == nil || opts.MaxDocumentBytes <= 0 || len(opts.Tasks) == 0 ||
		opts.Accumulate == nil {
		return nil, fmt.Errorf(choreographyBindingMissingPolicyError)
	}
	tasks := make(map[string]ChoreographyTaskSpec, len(opts.Tasks))
	stagesByPackage := make(map[string][]string)
	for _, task := range opts.Tasks {
		if task.Task == "" || tasks[task.Task].Task != "" {
			return nil, fmt.Errorf(choreographyBindingDuplicateTaskError)
		}
		tasks[task.Task] = task
		if task.Package != "" {
			stage := task.Stage
			if stage == "" {
				stage = task.Kind
			}
			if stage != "" && !slices.Contains(stagesByPackage[task.Package], stage) {
				stagesByPackage[task.Package] = append(stagesByPackage[task.Package], stage)
			}
		}
	}
	return &ChoreographyBindingCache{opts: opts,
		cursor: ChoreographyEventCursor{OID: opts.LedgerRootOID}, tasks: tasks,
		accepted:       make(map[string]choreographyAcceptedBindingResult),
		terminal:       make(map[string]ChoreographyEvent),
		pendingNoClaim: make(map[string]bool), prepared: make(map[string]string),
		pendingTask:     make(map[string]bool),
		outcomes:        make(map[string]release.PackageOutcome),
		providers:       make(map[string]ChoreographyVerifiedOutput),
		stages:          make(map[string]ChoreographyVerifiedOutput),
		stagesByPackage: stagesByPackage, outputMemo: make(map[string][]plan.Output)}, nil
}

func (c *ChoreographyBindingCache) sync(ctx context.Context) error {
	for {
		page, err := c.opts.Reader.ReadEvents(ctx, c.cursor, choreographyBindingEventPageSize)
		if err != nil {
			return err
		}
		if page.Cursor.Sequence < c.cursor.Sequence || page.Head.Sequence < page.Cursor.Sequence ||
			len(page.Events) > choreographyBindingEventPageSize ||
			len(page.Events) == 0 && page.More {
			return fmt.Errorf(choreographyBindingInvalidCursorError)
		}
		for _, event := range page.Events {
			if event.LedgerOID == "" || event.Sequence != c.cursor.Sequence+1 {
				return fmt.Errorf(choreographyBindingSkippedEpochError)
			}
			if err := c.apply(ctx, event); err != nil {
				return err
			}
			c.cursor = ChoreographyEventCursor{OID: event.LedgerOID, Sequence: event.Sequence}
		}
		if page.Cursor != c.cursor {
			return fmt.Errorf(choreographyBindingCursorTailError)
		}
		if !page.More {
			return nil
		}
	}
}

func (c *ChoreographyBindingCache) apply(ctx context.Context, event ChoreographyEvent) error {
	if event.Package != "" && event.PackageOutcome != "" {
		c.outcomes[event.Package] = release.PackageOutcome(event.PackageOutcome)
	}
	switch event.Kind {
	case ChoreographyResultAccepted:
		result, err := c.opts.Mailbox.ReadVerifiedChoreographyAcceptedResultFromAssignment(
			ctx, c.opts.Binding, event, c.opts.MaxDocumentBytes)
		if err != nil {
			return fmt.Errorf(choreographyBindingClaimHistoryErrorFormat, event.Task, err)
		}
		c.accepted[event.TaskOID] = choreographyAcceptedBindingResult{result: result, oid: event.OutputOID}
	case ChoreographyTerminal, ChoreographyBlocked, ChoreographyNoClaimExpired:
		if c.tasks[event.Task].Task == "" {
			return fmt.Errorf(choreographyBindingUnknownTaskErrorFormat, event.Task)
		}
		if event.OutputOID != "" && (event.ResultReceiptOID == "" ||
			c.accepted[event.ResultReceiptOID].oid != event.OutputOID) {
			return fmt.Errorf(choreographyBindingInvalidResultErrorFormat, event.Task, event.ResultReceiptOID)
		}
		c.terminal[event.Task] = event
		if event.Kind == ChoreographyNoClaimExpired && event.Package != "" {
			c.pendingNoClaim[event.Task] = true
		}
		if event.Kind == ChoreographyTerminal && event.Package != "" &&
			event.PackageOutcome == "" && event.ResultReceiptOID != "" {
			c.pendingTask[event.Task] = true
		} else {
			c.installTerminalOutput(event)
		}
	case ChoreographyNoClaimPackageSettled:
		if !c.pendingNoClaim[event.Task] || c.terminal[event.Task].TaskOID != event.TaskOID {
			return fmt.Errorf(choreographyBindingNoClaimPredecessorError)
		}
		delete(c.pendingNoClaim, event.Task)
	case ChoreographyTaskPackageSettled:
		prior := c.terminal[event.Task]
		if !c.pendingTask[event.Task] || prior.Kind != ChoreographyTerminal ||
			prior.TaskOID != event.TaskOID || event.PriorTaskOID != prior.TaskOID ||
			event.Attempt != prior.Attempt || event.Package != prior.Package {
			return fmt.Errorf(choreographyBindingTaskSettlementPredecessorError)
		}
		delete(c.pendingTask, event.Task)
		clear(c.outputMemo)
		c.installTerminalOutput(prior)
	case ChoreographyFrameReady:
		c.prepared[event.Task] = event.PreparedInputOID
	case ChoreographyInputInvalidation:
		for _, task := range event.AffectedTasks {
			delete(c.prepared, task)
		}
	}
	return nil
}

func (c *ChoreographyBindingCache) installTerminalOutput(event ChoreographyEvent) {
	if event.Kind != ChoreographyTerminal || !event.Succeeded || event.OutputOID == "" {
		return
	}
	accepted := c.accepted[event.ResultReceiptOID]
	if accepted.result.Outputs == nil {
		return
	}
	output := ChoreographyVerifiedOutput{Task: event.Task,
		Package: c.tasks[event.Task].Package, ResultOID: accepted.oid,
		Manifest: accepted.result.Outputs}
	stage := c.tasks[event.Task].Stage
	if stage == "" {
		stage = c.tasks[event.Task].Kind
	}
	c.stages[stageOutputKey(output.Package, stage)] = output
	if c.tasks[event.Task].Kind == string(KindBuild) ||
		c.tasks[event.Task].Kind == string(KindPrepare) {
		c.providers[output.Package] = output
	}
}

// State returns the exact inputs of any indexed ready task, regardless of
// whether the executor's current Drain chose that task. Readiness is checked
// from signed predecessor terminals, with virtual vertices derived in place.
func (c *ChoreographyBindingCache) State(ctx context.Context, taskID string) (ChoreographyVerifiedBindingState, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.sync(ctx); err != nil {
		return ChoreographyVerifiedBindingState{}, err
	}
	task := c.tasks[taskID]
	if task.Task == "" || task.Virtual {
		return ChoreographyVerifiedBindingState{}, fmt.Errorf(choreographyBindingAbsentTaskErrorFormat, taskID)
	}
	result := ChoreographyVerifiedBindingState{
		ProviderOutcomes:        make(map[string]release.PackageOutcome, len(c.outcomes)),
		PredecessorOutputs:      make(map[string][]plan.Output, len(task.Predecessors)),
		PredecessorTaskOIDs:     make([]string, 0, len(task.Predecessors)),
		PreparedInputOID:        c.prepared[taskID],
		VerifiedOutputByPackage: make(map[string]ChoreographyVerifiedOutput, len(c.providers)),
		VerifiedOutputByStage:   make(map[string]ChoreographyVerifiedOutput),
		VerifiedCarriedExports:  make(map[plan.Output]ChoreographyVerifiedCarriedExport),
	}
	for _, pkg := range c.opts.OutcomePackagesByTask[taskID] {
		if outcome := c.outcomes[pkg]; outcome != "" {
			result.ProviderOutcomes[pkg] = outcome
		}
	}
	for _, pkg := range c.opts.InputPackagesByTask[taskID] {
		if output, found := c.providers[pkg]; found {
			result.VerifiedOutputByPackage[pkg] = output
		}
	}
	for _, stage := range c.stagesByPackage[task.Package] {
		if output, found := c.stages[stageOutputKey(task.Package, stage)]; found {
			result.VerifiedOutputByStage[stage] = output
		}
	}
	visiting := make(map[string]bool)
	for _, predecessor := range task.Predecessors {
		outputs, err := c.cumulative(predecessor, c.outputMemo, visiting)
		if err != nil {
			return ChoreographyVerifiedBindingState{}, fmt.Errorf(choreographyBindingMissingStateErrorFormat,
				taskID, predecessor, err)
		}
		result.PredecessorOutputs[predecessor] = slices.Clone(outputs)
		identity := c.terminal[predecessor].OutputOID
		if identity == "" {
			identity = c.terminal[predecessor].TaskOID
		}
		result.PredecessorTaskOIDs = append(result.PredecessorTaskOIDs, identity)
		if err := c.collectCarriedExports(predecessor, result.VerifiedCarriedExports, make(map[string]bool)); err != nil {
			return ChoreographyVerifiedBindingState{}, err
		}
	}
	return result, nil
}

func (c *ChoreographyBindingCache) cumulative(taskID string, memo map[string][]plan.Output,
	visiting map[string]bool) ([]plan.Output, error) {
	if outputs, found := memo[taskID]; found {
		return outputs, nil
	}
	task := c.tasks[taskID]
	if task.Task == "" || visiting[taskID] || !task.Virtual &&
		(c.terminal[taskID].TaskOID == "" || c.pendingNoClaim[taskID] || c.pendingTask[taskID]) {
		return nil, fmt.Errorf(choreographyBindingMissingFinalityErrorFormat, taskID)
	}
	visiting[taskID] = true
	defer delete(visiting, taskID)
	parents := make(map[string][]plan.Output, len(task.Predecessors))
	for _, predecessor := range task.Predecessors {
		outputs, err := c.cumulative(predecessor, memo, visiting)
		if err != nil {
			return nil, err
		}
		parents[predecessor] = outputs
	}
	if task.Virtual {
		memo[taskID] = nil
		return nil, nil
	}
	var own []plan.Output
	terminal := c.terminal[taskID]
	if (terminal.Succeeded || c.opts.IncludeFailedExports) && terminal.OutputOID != "" {
		result := c.accepted[terminal.ResultReceiptOID].result
		result.Exports = slices.Clone(result.Exports)
		if outputContext, found := c.opts.OutputContexts[taskID]; found {
			if err := rehomeChoreographyExports(&result, outputContext.Repository,
				outputContext.Package, outputContext.SourceRoot, outputContext.Roots); err != nil {
				return nil, err
			}
		} else {
			for _, export := range result.Exports {
				if export.DeclaredPath != nil {
					return nil, fmt.Errorf(choreographyBindingMissingOutputPolicyErrorFormat, export.Name)
				}
			}
		}
		own = formatOutputs(result.Exports)
	}
	outputs, err := c.opts.Accumulate(taskID, parents, own)
	if err != nil {
		return nil, err
	}
	memo[taskID] = slices.Clone(outputs)
	return outputs, nil
}

func (c *ChoreographyBindingCache) collectCarriedExports(taskID string,
	carried map[plan.Output]ChoreographyVerifiedCarriedExport, visited map[string]bool) error {
	if visited[taskID] {
		return nil
	}
	visited[taskID] = true
	task := c.tasks[taskID]
	if task.Task == "" {
		return fmt.Errorf(choreographyBindingUnknownExportTaskErrorFormat, taskID)
	}
	parents := slices.Clone(task.Predecessors)
	slices.Sort(parents)
	for _, parent := range parents {
		if err := c.collectCarriedExports(parent, carried, visited); err != nil {
			return err
		}
	}
	terminal := c.terminal[taskID]
	if c.pendingTask[taskID] {
		return fmt.Errorf(choreographyBindingMissingFinalityErrorFormat, taskID)
	}
	if terminal.OutputOID == "" || (!terminal.Succeeded && !c.opts.IncludeFailedExports) {
		return nil
	}
	accepted := c.accepted[terminal.ResultReceiptOID]
	context, found := c.opts.OutputContexts[taskID]
	if !found {
		return fmt.Errorf(choreographyBindingMissingExportPolicyError)
	}
	verified := ChoreographyVerifiedOutput{Task: taskID, Package: task.Package,
		ResultOID: accepted.oid, Manifest: accepted.result.Outputs}
	for _, signed := range accepted.result.Exports {
		if signed.DeclaredPath == nil {
			continue
		}
		rehomed := accepted.result
		rehomed.Exports = []ExportedValue{signed}
		if err := rehomeChoreographyExports(&rehomed, context.Repository,
			context.Package, context.SourceRoot, context.Roots); err != nil {
			return err
		}
		key := formatOutputs(rehomed.Exports)[0]
		carried[key] = ChoreographyVerifiedCarriedExport{Signed: signed, Output: verified}
	}
	return nil
}

// ResolveChoreographyBindings fills the signed template's dynamic fields from
// a frame rendered with verified predecessor state. Source snapshots are
// captured under the coordinator's native snapshot guard; output carriers
// come only from verified Result and manifest pairs in state.
func (c *Coordinator) resolvePreparedChoreographyBindings(ctx context.Context, taskID string,
	state ChoreographyVerifiedBindingState, templateDigest string,
	prepare func() (ChoreographyTaskBindings, error)) (ChoreographyTaskBindings, error) {
	if c == nil || c.canonicalMailbox == nil || taskID == "" ||
		c.GraphRootOID == "" || templateDigest == "" || prepare == nil {
		return ChoreographyTaskBindings{}, fmt.Errorf(missingChoreographyPreparedCarrierError)
	}
	binding := ChoreographyRunBinding{Run: c.Run, Generation: c.Generation,
		PlanDigest: c.PlanDigest, GraphDigest: c.GraphDigest}
	if state.PreparedInputOID != "" {
		carrier, err := c.canonicalMailbox.ReadChoreographyPreparedCarrier(ctx,
			state.PreparedInputOID, c.Limits.MaxManifestBytes)
		if err != nil {
			return ChoreographyTaskBindings{}, err
		}
		if carrier.ChoreographyRunBinding != binding || carrier.GraphRootOID != c.GraphRootOID ||
			carrier.Task != taskID || carrier.TemplateDigest != templateDigest ||
			!slices.Equal(carrier.PredecessorResultOIDs, state.PredecessorTaskOIDs) {
			return ChoreographyTaskBindings{}, fmt.Errorf(invalidChoreographyPreparedCarrierError)
		}
		bindings := carrier.Bindings
		bindings.PreparedInputOID = state.PreparedInputOID
		return bindings, nil
	}
	bindings, err := prepare()
	if err != nil {
		return ChoreographyTaskBindings{}, err
	}
	if !slices.Equal(bindings.PredecessorResultOIDs, state.PredecessorTaskOIDs) ||
		bindings.PreparedInputOID != "" {
		return ChoreographyTaskBindings{}, fmt.Errorf(invalidChoreographyPreparedCarrierError)
	}
	carrier := ChoreographyPreparedCarrier{ChoreographyRunBinding: binding,
		GraphRootOID: c.GraphRootOID, Task: taskID, TemplateDigest: templateDigest,
		PredecessorResultOIDs: slices.Clone(state.PredecessorTaskOIDs), Bindings: bindings}
	oid, err := c.canonicalMailbox.PrepareChoreographyPreparedCarrier(ctx,
		carrier, c.Limits.MaxManifestBytes)
	if err != nil {
		return ChoreographyTaskBindings{}, err
	}
	bindings.PreparedInputOID = oid
	return bindings, nil
}

func (c *Coordinator) ResolveChoreographyBindings(ctx context.Context, node string,
	request release.StageRequest, state ChoreographyVerifiedBindingState,
	templateDigest string) (ChoreographyTaskBindings, error) {
	return c.resolvePreparedChoreographyBindings(ctx, request.TaskID, state, templateDigest,
		func() (ChoreographyTaskBindings, error) {
			return c.resolveChoreographyBindingsFresh(ctx, node, request, state)
		})
}

func (c *Coordinator) resolveChoreographyBindingsFresh(ctx context.Context, node string,
	request release.StageRequest, state ChoreographyVerifiedBindingState) (ChoreographyTaskBindings, error) {
	if c == nil || c.dispatch.Sources == nil || c.dispatch.Inputs == nil ||
		c.dispatch.Store == nil || c.canonicalMailbox == nil || request.Release == nil ||
		request.Release.Pkg == nil || request.TaskID == "" || node == "" {
		return ChoreographyTaskBindings{}, fmt.Errorf(choreographyBindingMissingCompiledPolicyError)
	}
	packageName := request.Release.Pkg.Name
	sources := c.dispatch.Sources(packageName)
	dir, err := resolvePackageDir(sources, request)
	if err != nil {
		return ChoreographyTaskBindings{}, err
	}
	commits, err := c.captureInputs(ctx, sources)
	if err != nil {
		return ChoreographyTaskBindings{}, err
	}
	repositories, err := c.carrySourceInputs(ctx, sources, commits)
	if err != nil {
		return ChoreographyTaskBindings{}, err
	}
	bindings := ChoreographyTaskBindings{
		Repositories: repositories,
		Exports:      formatExports(request.Release.Outputs), Env: slices.Clone(request.Env),
		PredecessorResultOIDs: slices.Clone(state.PredecessorTaskOIDs),
		PreparedInputOID:      state.PreparedInputOID,
	}
	if request.Descriptor.Inputs == release.InputsNone {
		return bindings, nil
	}
	imported := make(map[string]bool)
	seen := make(map[choreographyInputDestination]bool)
	if err := applyVerifiedChoreographyExports(ctx, &bindings, choreographyCarriedExportInputs{
		Outputs: request.Release.Outputs, Verified: state.VerifiedCarriedExports,
		Sources: sources, SourceDir: c.canonicalMailbox.plumbing.Dir,
		Imported: imported, Seen: seen, ImportCommit: c.dispatch.Store.ImportCommitFrom,
	}); err != nil {
		return ChoreographyTaskBindings{}, err
	}
	appendOutput := func(output ChoreographyVerifiedOutput, path string) error {
		if output.ResultOID == "" || output.Manifest == nil || output.Manifest.Digest == "" ||
			output.Task == "" || output.Package == "" {
			return fmt.Errorf(choreographyBindingMissingOutputIdentityError)
		}
		return appendVerifiedChoreographyInput(ctx, &bindings, AssignmentInput{
			Task: output.Task, Package: output.Package,
			Commit: output.ResultOID, Digest: output.Manifest.Digest, Path: path,
		}, c.canonicalMailbox.plumbing.Dir, imported, seen, c.dispatch.Store.ImportCommitFrom)
	}
	for _, provider := range c.dispatch.Inputs(packageName) {
		output, found := state.VerifiedOutputByPackage[provider.Package]
		if !found {
			if provider.IsPrepared {
				return ChoreographyTaskBindings{}, fmt.Errorf(
					"execution: prepared provider %s has no signed admitted output for %s",
					provider.Package, request.TaskID)
			}
			continue
		}
		if err := appendOutput(output, provider.Path); err != nil {
			return ChoreographyTaskBindings{}, err
		}
	}
	if request.Descriptor.Inputs == release.InputsOwnOutputs {
		stages := request.Descriptor.OwnOutputStages
		if len(stages) == 0 {
			stages = []string{choreographyOwnBuildStage}
		}
		for _, stage := range stages {
			output, found := state.VerifiedOutputByStage[stage]
			if !found {
				continue
			}
			if err := appendOutput(output, resolveAnchoredPath(sources,
				request.Release.Pkg.Repository, dir)); err != nil {
				return ChoreographyTaskBindings{}, err
			}
		}
	}
	return bindings, nil
}

// ResolveChoreographySweepBindings reconstructs the sweep's inherited exports
// from signed final Results, including accepted partial exports under continue.
func (c *Coordinator) ResolveChoreographySweepBindings(ctx context.Context, node string,
	request release.StageRequest, state ChoreographyVerifiedBindingState,
	templateDigest string) (ChoreographyTaskBindings, error) {
	return c.resolvePreparedChoreographyBindings(ctx, request.TaskID, state, templateDigest,
		func() (ChoreographyTaskBindings, error) {
			return c.resolveChoreographySweepBindingsFresh(ctx, node, request, state)
		})
}

func (c *Coordinator) resolveChoreographySweepBindingsFresh(ctx context.Context, node string,
	request release.StageRequest, state ChoreographyVerifiedBindingState) (ChoreographyTaskBindings, error) {
	if c == nil || c.dispatch.Sources == nil || c.dispatch.Store == nil ||
		c.canonicalMailbox == nil || request.Release == nil || request.Release.Pkg == nil ||
		request.TaskID == "" || node == "" {
		return ChoreographyTaskBindings{}, fmt.Errorf(choreographyBindingMissingSweepPolicyError)
	}
	sources := c.dispatch.Sources(request.Release.Pkg.Name)
	if _, err := resolvePackageDir(sources, request); err != nil {
		return ChoreographyTaskBindings{}, err
	}
	commits, err := c.captureInputs(ctx, sources)
	if err != nil {
		return ChoreographyTaskBindings{}, err
	}
	repositories, err := c.carrySourceInputs(ctx, sources, commits)
	if err != nil {
		return ChoreographyTaskBindings{}, err
	}
	bindings := ChoreographyTaskBindings{Repositories: repositories,
		Exports: formatExports(request.Release.Outputs), Env: slices.Clone(request.Env),
		PredecessorResultOIDs: slices.Clone(state.PredecessorTaskOIDs),
		PreparedInputOID:      state.PreparedInputOID}
	imported := make(map[string]bool)
	seen := make(map[choreographyInputDestination]bool)
	if err := applyVerifiedChoreographyExports(ctx, &bindings, choreographyCarriedExportInputs{
		Outputs: request.Release.Outputs, Verified: state.VerifiedCarriedExports,
		Sources: sources, SourceDir: c.canonicalMailbox.plumbing.Dir,
		Imported: imported, Seen: seen, ImportCommit: c.dispatch.Store.ImportCommitFrom,
	}); err != nil {
		return ChoreographyTaskBindings{}, err
	}
	return bindings, nil
}
