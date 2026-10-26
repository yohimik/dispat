// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"fmt"
	"slices"
)

type choreographyFrontierState uint8

const (
	frontierPickupContactLossRecorded = true
	frontierPickupSettlementRecorded  = true
)

const (
	frontierReady choreographyFrontierState = iota + frontierFirstStateOrdinal
	frontierPending
	frontierOffered
	frontierQueueFenced
	frontierBusy
	frontierBlocked
	frontierTerminal
)

// ChoreographyOutcomePolicyx is implemented by the independently compiled
// release graph. The execution package cannot infer §19.3 from edge presence:
// optional providers, propagated causes and record failures differ.
type ChoreographyOutcomePolicyx interface {
	ValidateOutcome(packageName, status string) error
	ApplyOutcome(packageName, status string) error
	ShouldSkip(packageName string) (bool, string, error)
}

// ChoreographyBlockedTask is a locally proven no-claim candidate. It remains
// pending until the first node signs and persists its Blocked event; a peer
// never treats this local conclusion as durable finality.
type ChoreographyBlockedTask struct {
	Task      string
	Package   string
	BlockedBy string
}

type choreographyFrontierTask struct {
	spec                    ChoreographyTaskSpec
	remaining               int
	children                []string
	attempt                 int
	state                   choreographyFrontierState
	outputOID               string
	terminal                ChoreographyEvent
	packageSettled          bool
	outputAdmissionRejected bool
	virtualSucceeded        bool
	refusedNotReady         bool
	preparedSinceSelection  bool
	preparedInputOID        string
	// The signed task tip and offered Assignment bind an unclaimed queue
	// retry to the exact offer; a newer ledger epoch alone is insufficient.
	tipOID      string
	offeredOID  string
	offeredNode string
	// A prepared-provider edge carries required build input. Its failure
	// blocks this task independently of release-package cause policy.
	failedPrepared  string
	pickups         map[string]*choreographyFrontierPickup
	currentPickupID string
}

// A pickup remains indexed after contact loss or logical completion so a
// later signed result can be identified without treating it as the current
// canonical task attempt.
type choreographyFrontierPickup struct {
	claimOID, node, cancelOID, cancelReplyDeadlineUTC string
	resultReceiptOID, outputOID                       string
	attempt                                           int
	contactLost, settled                              bool
}

func (t *choreographyFrontierTask) noteFailedPrepared(packageName string) {
	if packageName != frontierNoPackage &&
		(t.failedPrepared == frontierNoBlocker || packageName < t.failedPrepared) {
		t.failedPrepared = packageName
	}
}

// ChoreographyReadyTask is a locally chosen candidate. Selecting it is not
// authority to execute; only an accepted exact claim receipt grants that.
type ChoreographyReadyTask struct {
	Spec    ChoreographyTaskSpec
	Attempt int
}

// ChoreographyFrontier applies signed event pages to indexed adjacency and
// ready queues. Each vertex/edge is touched once per logical finality; it
// does not rescan the whole graph at every poll or proposal.
type ChoreographyFrontier struct {
	binding       ChoreographyRunBinding
	graphRootOID  string
	initialized   bool
	started       bool
	node          string
	nodePolicy    *ChoreographyRuntimeNode
	tasks         map[string]*choreographyFrontierTask
	order         []string
	ready         []string
	deferred      []string
	head          int
	cursor        ChoreographyEventCursor
	failed        bool
	draining      bool
	stopped       bool
	invalidated   bool
	sealed        bool
	hasPackages   bool
	outcomes      ChoreographyOutcomePolicyx
	packageStatus map[string]string
	blocked       []ChoreographyBlockedTask
	blockedHead   int
}

type CompiledChoreographyFrontierOptions struct {
	Binding      ChoreographyRunBinding
	GraphRootOID string
	Node         ChoreographyRuntimeNode
	Tasks        []ChoreographyTaskSpec
}

// NewCompiledChoreographyFrontier requires the exact signed complete graph
// root and admitted node policy before any compiled task can be selected.
func NewCompiledChoreographyFrontier(options CompiledChoreographyFrontierOptions) (*ChoreographyFrontier, error) {
	binding, graphRootOID, node, specs := options.Binding, options.GraphRootOID, options.Node, options.Tasks

	if graphRootOID == frontierNoGraphRootOID {
		return nil, fmt.Errorf(frontierMissingSignedRootError)
	}
	frontier, err := NewChoreographyFrontier(ChoreographyFrontierOptions{
		Binding:    binding,
		Node:       node.Name,
		Tasks:      specs,
		NodePolicy: []ChoreographyRuntimeNode{node},
	})
	if err != nil {
		return nil, err
	}
	frontier.graphRootOID = graphRootOID
	return frontier, nil
}

type ChoreographyFrontierOptions struct {
	Binding    ChoreographyRunBinding
	Node       string
	Tasks      []ChoreographyTaskSpec
	NodePolicy []ChoreographyRuntimeNode
}

func NewChoreographyFrontier(options ChoreographyFrontierOptions) (*ChoreographyFrontier, error) {
	binding, node, specs, nodePolicy := options.Binding, options.Node, options.Tasks, options.NodePolicy

	if !binding.valid() || node == frontierNoNode {
		return nil, fmt.Errorf(frontierIncompleteIdentityError)
	}
	f := &ChoreographyFrontier{binding: binding, node: node,
		tasks:         make(map[string]*choreographyFrontierTask, len(specs)),
		packageStatus: make(map[string]string)}
	if len(nodePolicy) > frontierSingleNodePolicy || len(nodePolicy) == frontierSingleNodePolicy &&
		(nodePolicy[frontierFirstNodePolicyIndex].Name != node || nodePolicy[frontierFirstNodePolicyIndex].Capacity <= frontierMinimumNodeCapacity) {
		return nil, fmt.Errorf(frontierInvalidNodePolicyError)
	}
	if len(nodePolicy) == frontierSingleNodePolicy {
		policy := nodePolicy[frontierFirstNodePolicyIndex]
		f.nodePolicy = &policy
	}
	for _, spec := range specs {
		if spec.Task == frontierNoTaskID || f.tasks[spec.Task] != nil || (spec.Virtual && spec.LocalAuthority) {
			return nil, fmt.Errorf(frontierInvalidTaskIdentityError)
		}
		f.tasks[spec.Task] = &choreographyFrontierTask{spec: spec,
			remaining: len(spec.Predecessors), attempt: frontierFirstAttemptNumber, state: frontierReady,
			tipOID: spec.ReservationOID, pickups: make(map[string]*choreographyFrontierPickup)}
		f.hasPackages = f.hasPackages || spec.Package != frontierNoPackage && spec.Kind != KindPrepare
		f.order = append(f.order, spec.Task)
	}
	indegree := make(map[string]int, len(f.tasks))
	for _, name := range f.order {
		task := f.tasks[name]
		for _, predecessor := range task.spec.Predecessors {
			parent := f.tasks[predecessor]
			if parent == nil {
				return nil, fmt.Errorf(frontierInvalidPredecessorErrorFormat, name)
			}
			// Distinct graph edges may have the same two endpoint tasks. Keep
			// one adjacency entry per edge so the signed terminal discharges
			// exactly the multiplicity counted in remaining.
			parent.children = append(parent.children, name)
		}
		indegree[name] = task.remaining
	}
	queue := make([]string, frontierInitialQueueLength, len(f.tasks))
	for _, name := range f.order {
		count := indegree[name]
		if count == frontierNoPredecessors {
			queue = append(queue, name)
		}
	}
	visited := frontierInitialVisitedCount
	for len(queue) > frontierInitialQueueLength {
		name := queue[frontierFirstQueueIndex]
		queue = queue[frontierQueueAdvanceStep:]
		visited++
		for _, child := range f.tasks[name].children {
			indegree[child]--
			if indegree[child] == frontierNoPredecessors {
				queue = append(queue, child)
			}
		}
	}
	if visited != len(f.tasks) {
		return nil, fmt.Errorf(frontierCycleError)
	}
	virtualRoots := make([]*choreographyFrontierTask, frontierInitialVirtualRootCount)
	for _, name := range f.order {
		task := f.tasks[name]
		if task.remaining == frontierNoPredecessors {
			if task.spec.Virtual {
				virtualRoots = append(virtualRoots, task)
			} else if !task.spec.ExecutorOwned {
				f.ready = append(f.ready, name)
			}
		}
	}
	for _, root := range virtualRoots {
		if root.spec.Package == frontierNoPackage {
			if err := f.completeVirtual(root); err != nil {
				return nil, err
			}
		}
	}
	return f, nil
}

// AttachOutcomePolicy must precede the first signed page or selection. A
// release task carrying a package refuses selection without this policy.
func (f *ChoreographyFrontier) AttachOutcomePolicy(policy ChoreographyOutcomePolicyx) error {
	if f == nil || policy == nil || f.outcomes != nil || f.cursor.Sequence != frontierInitialCursorSequence || f.head != frontierInitialReadyHead {
		return fmt.Errorf(frontierLateOutcomePolicyError)
	}
	f.outcomes = policy
	for _, name := range f.order {
		task := f.tasks[name]
		if task.spec.Virtual && task.remaining == frontierNoPredecessors && task.state != frontierTerminal {
			if err := f.completeVirtual(task); err != nil {
				f.failed = frontierQuarantined
				return err
			}
		}
	}
	return nil
}

func (f *ChoreographyFrontier) completeVirtual(first *choreographyFrontierTask) error {
	queue := []*choreographyFrontierTask{first}
	for len(queue) > frontierInitialQueueLength {
		task := queue[frontierFirstQueueIndex]
		queue = queue[frontierQueueAdvanceStep:]
		if task.state == frontierTerminal {
			continue
		}
		if task.spec.Package != frontierNoPackage && task.spec.Kind != KindPrepare &&
			task.failedPrepared == frontierNoBlocker {
			if f.outcomes == nil {
				continue
			}
			skip, _, err := f.outcomes.ShouldSkip(task.spec.Package)
			if err != nil {
				return err
			}
			if skip && f.packageStatus[task.spec.Package] == frontierNoPackageOutcome {
				if err := f.outcomes.ApplyOutcome(task.spec.Package, frontierPackageOutcomeSkipped); err != nil {
					return err
				}
				f.packageStatus[task.spec.Package] = frontierPackageOutcomeSkipped
			}
		}
		task.state = frontierTerminal
		task.virtualSucceeded = task.failedPrepared == frontierNoBlocker &&
			(task.spec.Package == frontierNoPackage || f.packageStatus[task.spec.Package] != frontierPackageOutcomeSkipped &&
				f.packageStatus[task.spec.Package] != frontierPackageOutcomeFailed)
		for _, child := range task.children {
			next := f.tasks[child]
			if choreographyPreparedProviderFor(next.spec, task.failedPrepared) {
				next.noteFailedPrepared(task.failedPrepared)
			}
			next.remaining--
			if next.remaining == frontierNoPredecessors {
				if next.spec.Virtual {
					queue = append(queue, next)
				} else if !next.spec.ExecutorOwned {
					f.ready = append(f.ready, child)
				}
			}
		}
	}
	return nil
}

// ReadyClaimInputs names the exact direct predecessor finality in the
// compiled graph's edge order. Virtual gates have no Git object; their
// outcome is still included in the signed ready-input digest.
func (f *ChoreographyFrontier) ReadyClaimInputs(taskID string) (string, []string, string, error) {
	if f == nil || f.graphRootOID == frontierNoGraphRootOID {
		return frontierNoInputsDigest, nil, frontierNoTaskOID, fmt.Errorf(frontierReadyMissingRootError)
	}
	task := f.tasks[taskID]
	if task == nil || task.remaining != frontierNoPredecessors || task.state != frontierPending || task.tipOID == frontierNoTaskOID {
		return frontierNoInputsDigest, nil, frontierNoTaskOID, fmt.Errorf(frontierReadyInputStateError)
	}
	finality := make([]ChoreographyInputFinality, frontierInitialInputCount, len(task.spec.Predecessors))
	ids := make([]string, frontierInitialInputCount, len(task.spec.Predecessors))
	for _, name := range task.spec.Predecessors {
		predecessor := f.tasks[name]
		if predecessor == nil || predecessor.state != frontierTerminal {
			return frontierNoInputsDigest, nil, frontierNoTaskOID, fmt.Errorf(frontierUnsettledPredecessorError)
		}
		if predecessor.spec.Virtual {
			finality = append(finality, ChoreographyInputFinality{
				Task: name, Succeeded: predecessor.virtualSucceeded})
			ids = append(ids, frontierVirtualPredecessorOID)
			continue
		}
		if predecessor.terminal.TaskOID == frontierNoTaskOID {
			return frontierNoInputsDigest, nil, frontierNoTaskOID, fmt.Errorf(frontierUnsignedPredecessorError)
		}
		finality = append(finality, ChoreographyInputFinality{Task: name,
			OID: predecessor.terminal.TaskOID, Succeeded: predecessor.terminal.Succeeded})
		ids = append(ids, choreographyPredecessorIdentity(
			predecessor.terminal.OutputOID, predecessor.terminal.TaskOID))
	}
	digest, err := ChoreographyReadyInputsDigest(f.binding, taskID, finality)
	return digest, ids, task.tipOID, err
}

// SelectReady marks one eligible task locally pending. The caller retains
// the exact proposal while its response is unknown; a 404 lookup is not a
// reason to put the task back in this queue.
func (f *ChoreographyFrontier) SelectReady() (ChoreographyReadyTask, bool, error) {
	if f.failed {
		return ChoreographyReadyTask{}, frontierSelectionMiss, fmt.Errorf(frontierQuarantinedError)
	}
	if f.hasPackages && f.outcomes == nil {
		f.failed = frontierQuarantined
		return ChoreographyReadyTask{}, frontierSelectionMiss, fmt.Errorf(frontierMissingPackagePolicyError)
	}
	if f.stopped || f.sealed || f.draining ||
		f.graphRootOID != frontierNoGraphRootOID && (!f.initialized || !f.started) {
		return ChoreographyReadyTask{}, frontierSelectionMiss, nil
	}
	for f.head < len(f.ready) {
		name := f.ready[f.head]
		f.head++
		task := f.tasks[name]
		if task == nil || task.remaining != frontierNoPredecessors || task.state != frontierReady {
			continue
		}
		if !f.isEligible(task.spec) {
			f.deferred = append(f.deferred, name)
			continue
		}
		if task.spec.Package != frontierNoPackage && task.spec.Kind != KindPrepare {
			if f.outcomes == nil {
				f.failed = frontierQuarantined
				return ChoreographyReadyTask{}, frontierSelectionMiss, fmt.Errorf(frontierMissingReleasePolicyError)
			}
			status := f.packageStatus[task.spec.Package]
			if status == frontierPackageOutcomeFailed || status == frontierPackageOutcomeSkipped || status == frontierPackageOutcomeRecordBlocked || status == frontierPackageOutcomeCancelled {
				f.parkBlocked(task, task.spec.Package)
				continue
			}
		}
		if task.failedPrepared != frontierNoBlocker {
			f.parkBlocked(task, task.failedPrepared)
			continue
		}
		if task.spec.Package != frontierNoPackage && task.spec.Kind != KindPrepare {
			skip, blocker, err := f.outcomes.ShouldSkip(task.spec.Package)
			if err != nil {
				f.failed = frontierQuarantined
				return ChoreographyReadyTask{}, frontierSelectionMiss, err
			}
			if skip {
				f.parkBlocked(task, blocker)
				continue
			}
		}
		task.state = frontierPending
		task.preparedSinceSelection = frontierFrameNotPrepared
		return ChoreographyReadyTask{Spec: task.spec, Attempt: task.attempt}, frontierSelectionHit, nil
	}
	return ChoreographyReadyTask{}, frontierSelectionMiss, nil
}

// ReturnUnproposed restores a local selection only when no signed proposal
// was sent. Losing the local physical slot cannot consume a graph task.
func (f *ChoreographyFrontier) ReturnUnproposed(taskID string) error {
	task := f.tasks[taskID]
	if task == nil || task.state != frontierPending {
		return fmt.Errorf(frontierUnproposedStateError)
	}
	task.state = frontierReady
	f.ready = append(f.ready, taskID)
	return nil
}

// RequeueFenced follows an exact signed refusal and a relevant signed
// capacity release. The refused task remains the same logical vertex.
func (f *ChoreographyFrontier) RequeueFenced(taskID string) error {
	task := f.tasks[taskID]
	if task == nil || task.state != frontierPending || task.refusedNotReady {
		return fmt.Errorf(frontierRefusedRequeueError)
	}
	task.state = frontierReady
	f.ready = append(f.ready, taskID)
	return nil
}

func (f *ChoreographyFrontier) PreparedInputOID(taskID string) string {
	if task := f.tasks[taskID]; task != nil {
		return task.preparedInputOID
	}
	return frontierAbsentPreparedInputOID
}

func (f *ChoreographyFrontier) isEligible(spec ChoreographyTaskSpec) bool {
	if f.nodePolicy == nil {
		return slices.Contains(spec.EligibleNodes, f.node)
	}
	if spec.Weight > f.nodePolicy.Capacity ||
		!isPlatformCompatible(f.nodePolicy.Platform, spec.Platforms) {
		return frontierNodeIneligible
	}
	switch spec.Placement {
	case PlacementOrchestrator:
		return f.nodePolicy.First
	case PlacementWorker:
		return !f.nodePolicy.First
	case PlacementAnyNode:
		return frontierNodeEligible
	default:
		return frontierNodeIneligible
	}
}

// UpdateNodePolicy applies a signed membership change for this node. Only
// tasks previously deferred by eligibility are reconsidered; unrelated
// terminal events still update only their outgoing edges.
func (f *ChoreographyFrontier) UpdateNodePolicy(node ChoreographyRuntimeNode) error {
	if f == nil || node.Name != f.node || node.Capacity <= frontierMinimumNodeCapacity {
		return fmt.Errorf(frontierInvalidMembershipError)
	}
	f.nodePolicy = &node
	stillDeferred := f.deferred[:frontierInitialQueueLength]
	for _, name := range f.deferred {
		task := f.tasks[name]
		if task == nil || task.state != frontierReady || task.remaining != frontierNoPredecessors {
			continue
		}
		if f.isEligible(task.spec) {
			f.ready = append(f.ready, name)
		} else {
			stillDeferred = append(stillDeferred, name)
		}
	}
	f.deferred = stillDeferred
	return nil
}

func (f *ChoreographyFrontier) parkBlocked(task *choreographyFrontierTask, blocker string) {
	task.state = frontierBlocked
	f.blocked = append(f.blocked, ChoreographyBlockedTask{
		Task: task.spec.Task, Package: task.spec.Package, BlockedBy: blocker})
}

// PeekBlocked returns a no-claim candidate without consuming it. Only a
// signed Blocked event removes it, so an ambiguous persistence reply cannot
// silently discard a required skip record.
func (f *ChoreographyFrontier) PeekBlocked() (ChoreographyBlockedTask, bool) {
	for f.blockedHead < len(f.blocked) {
		candidate := f.blocked[f.blockedHead]
		if task := f.tasks[candidate.Task]; task != nil && task.state == frontierBlocked {
			return candidate, frontierSelectionHit
		}
		f.blockedHead++
	}
	return ChoreographyBlockedTask{}, frontierSelectionMiss
}

// ApplyEventPage accepts only contiguous events from the signed return
// client. Claim events mark work busy; logical finality releases outgoing
// precedence edges even when the task failed. The shared outcome policy then
// decides whether a now-ready consumer may be claimed or must be blocked.
// A failed attempt becomes selectable only after an explicit retry-ready event.
func (f *ChoreographyFrontier) ApplyEventPage(page ChoreographyEventPage) error {
	if f.failed {
		return fmt.Errorf(frontierQuarantinedError)
	}
	if f.hasPackages && f.outcomes == nil {
		f.failed = frontierQuarantined
		return fmt.Errorf(frontierMissingPackagePolicyError)
	}
	if page.Cursor.OID == frontierNoLedgerOID || page.Cursor.Sequence != f.cursor.Sequence+uint64(len(page.Events)) ||
		(len(page.Events) == frontierNoSignedEvents && f.cursor.OID != frontierNoLedgerOID && page.Cursor.OID != f.cursor.OID) {
		f.failed = frontierQuarantined
		return fmt.Errorf(frontierNoncontiguousPageError)
	}
	for index, event := range page.Events {
		if event.Sequence != f.cursor.Sequence+uint64(index)+frontierFirstEventSequence || event.TaskOID == frontierNoTaskOID {
			f.failed = frontierQuarantined
			return fmt.Errorf(frontierEventSequenceGapError)
		}
		if event.OutputAdmissionRejected && event.Kind != ChoreographyTaskPackageSettled {
			f.failed = frontierQuarantined
			return fmt.Errorf(frontierTaskPackageSettlementError)
		}
		if event.Kind == ChoreographyInitializationComplete {
			if f.graphRootOID == frontierNoGraphRootOID || f.initialized || event.Sequence != frontierFirstEventSequence ||
				event.Task != choreographyRunTaskID || event.TaskOID != f.graphRootOID ||
				event.GraphRootOID != f.graphRootOID {
				f.failed = frontierQuarantined
				return fmt.Errorf(frontierInitializationMismatchError)
			}
			f.initialized = frontierInitializationVerified
			continue
		}
		if event.Kind == ChoreographyExecutionStarted {
			if f.graphRootOID == frontierNoGraphRootOID || !f.initialized || f.started || f.stopped ||
				event.Task != choreographyRunTaskID || event.TaskOID != f.graphRootOID ||
				event.GraphRootOID != f.graphRootOID || event.Attempt != frontierNoTaskAttempt ||
				event.AdmissionDeadlineUTC == frontierNoAdmissionDeadlineUTC {
				f.failed = frontierQuarantined
				return fmt.Errorf(frontierExecutionStartMismatchError)
			}
			f.started = frontierExecutionAuthorized
			continue
		}
		if f.graphRootOID != frontierNoGraphRootOID && !f.initialized {
			f.failed = frontierQuarantined
			return fmt.Errorf(frontierBeforeInitializationError)
		}
		if event.Kind == ChoreographyMemberJoined ||
			event.Kind == ChoreographyMemberDraining ||
			event.Kind == ChoreographyMemberCapacityChanged {
			if event.Task != choreographyMembershipTaskID || event.Node == frontierNoNode || event.Member == nil ||
				event.Member.Name != event.Node || f.graphRootOID == frontierNoGraphRootOID ||
				event.GraphRootOID != f.graphRootOID {
				f.failed = frontierQuarantined
				return fmt.Errorf(frontierMembershipMismatchError)
			}
			if event.Node == f.node {
				if event.Kind == ChoreographyMemberDraining {
					f.draining = frontierMemberDraining
				} else {
					if err := f.UpdateNodePolicy(*event.Member); err != nil {
						f.failed = frontierQuarantined
						return err
					}
					if event.Kind == ChoreographyMemberJoined {
						f.draining = frontierMemberSelecting
					}
				}
			}
			continue
		}
		if event.Kind == ChoreographyRunSealed {
			if !f.stopped || f.sealed || event.Task != choreographyRunTaskID || event.Attempt != frontierNoTaskAttempt {
				f.failed = frontierQuarantined
				return fmt.Errorf(frontierInvalidRunSealError)
			}
			f.sealed = frontierRunSealed
			continue
		}
		if event.Kind == ChoreographyInputInvalidation {
			if f.graphRootOID == frontierNoGraphRootOID || f.invalidated || f.stopped ||
				event.Task != choreographyRunTaskID || event.TaskOID != f.graphRootOID ||
				event.GraphRootOID != f.graphRootOID ||
				event.OldPreparedOID == frontierAbsentPreparedInputOID || event.NewPreparedOID == frontierAbsentPreparedInputOID ||
				event.OldPreparedOID == event.NewPreparedOID || len(event.AffectedTasks) == frontierNoAffectedTasks ||
				!slices.IsSorted(event.AffectedTasks) {
				f.failed = frontierQuarantined
				return fmt.Errorf(frontierInvalidInputInvalidationError)
			}
			for index, taskID := range event.AffectedTasks {
				if taskID == frontierNoTaskID || f.tasks[taskID] == nil ||
					index > frontierFirstAffectedTaskIndex && taskID == event.AffectedTasks[index-frontierPreviousAffectedTaskStep] {
					f.failed = frontierQuarantined
					return fmt.Errorf(frontierInputInvalidationTaskError)
				}
			}
			f.invalidated, f.stopped = frontierInputInvalidated, frontierRunStopped
			continue
		}
		if event.Kind == ChoreographyRunStopping {
			if f.stopped && !f.invalidated || event.Task != choreographyRunTaskID || event.Attempt != frontierNoTaskAttempt ||
				event.Node != frontierNoNode || event.Package != frontierNoPackage || event.PackageOutcome != frontierNoPackageOutcome {
				f.failed = frontierQuarantined
				return fmt.Errorf(frontierInvalidRunStoppingError)
			}
			f.stopped = frontierRunStopped
			f.invalidated = frontierInputCurrent
			continue
		}
		task := f.tasks[event.Task]
		var pickup *choreographyFrontierPickup
		if task != nil && event.PickupID != frontierNoProposalID {
			pickup = task.pickups[event.PickupID]
		}
		priorPickupEvent := pickup != nil &&
			(event.Kind == ChoreographyResultAccepted || event.Kind == ChoreographyAckAccepted ||
				event.Kind == ChoreographyPickupSettled || event.Kind == ChoreographyTerminal)
		settledWinnerEvent := task != nil && event.Kind == ChoreographyTaskPackageSettled &&
			task.terminal.Kind == ChoreographyTerminal && event.Attempt == task.terminal.Attempt
		if task == nil || task.spec.Virtual ||
			(event.Attempt != task.attempt && !settledWinnerEvent &&
				(!priorPickupEvent || event.Attempt != pickup.attempt)) ||
			event.Kind != ChoreographyClaimRefused &&
				event.Kind != ChoreographyInertCancelled &&
				event.Kind != ChoreographyInertCancelSettled &&
				event.Kind != ChoreographyPickupSettled && task.remaining != frontierNoPredecessors {
			f.failed = frontierQuarantined
			return fmt.Errorf(frontierUnreadyTaskEventError)
		}
		priorPackageStatus := f.packageStatus[event.Package]
		if event.PackageOutcome != frontierNoPackageOutcome {
			if (event.Kind != ChoreographyTerminal && event.Kind != ChoreographyBlocked &&
				event.Kind != ChoreographyNoClaimExpired && event.Kind != ChoreographyNoClaimPackageSettled &&
				event.Kind != ChoreographyInertCancelSettled && event.Kind != ChoreographyTaskPackageSettled) ||
				event.Package == frontierNoPackage || event.Package != task.spec.Package || f.outcomes == nil {
				f.failed = frontierQuarantined
				return fmt.Errorf(frontierUnboundPackageOutcomeError)
			}
			if err := f.outcomes.ApplyOutcome(event.Package, event.PackageOutcome); err != nil {
				f.failed = frontierQuarantined
				return err
			}
			f.packageStatus[event.Package] = event.PackageOutcome
		} else if event.Package != frontierNoPackage && event.Package != task.spec.Package {
			f.failed = frontierQuarantined
			return fmt.Errorf(frontierWrongEventPackageError)
		}
		switch event.Kind {
		case ChoreographyFrameReady:
			if f.graphRootOID == frontierNoGraphRootOID || event.GraphRootOID != f.graphRootOID ||
				event.PriorTaskOID != task.tipOID || event.TaskOID != task.tipOID ||
				task.state == frontierTerminal || task.state == frontierBusy {
				f.failed = frontierQuarantined
				return fmt.Errorf(frontierFrameReadyTipError)
			}
			task.preparedInputOID = event.PreparedInputOID
			if task.state == frontierPending {
				task.preparedSinceSelection = frontierFramePrepared
			}
			if task.refusedNotReady && !task.spec.ExecutorOwned {
				task.refusedNotReady = frontierNotReadyCleared
				task.state = frontierReady
				f.ready = append(f.ready, event.Task)
			}
		case ChoreographyAssignmentOffered:
			if task.spec.LocalAuthority || task.state != frontierReady ||
				event.PriorTaskOID != task.tipOID || event.Node == frontierNoNode ||
				!slices.Contains(task.spec.EligibleNodes, event.Node) ||
				event.ResultReceiptOID != frontierNoResultReceiptOID || event.OutputOID != frontierNoOutputOID {
				f.failed = frontierQuarantined
				return fmt.Errorf(frontierOfferTipError)
			}
			task.offeredOID, task.offeredNode = event.TaskOID, event.Node
			task.tipOID = event.TaskOID
			task.state = frontierOffered
		case ChoreographyQueueFenced:
			if task.state != frontierOffered || event.PriorTaskOID != task.offeredOID ||
				event.Node != task.offeredNode || event.ResultReceiptOID != frontierNoResultReceiptOID ||
				event.OutputOID != frontierNoOutputOID || event.Weight != frontierNoTransferWeight || event.Budget != frontierNoBudget ||
				event.PackageOutcome != frontierNoPackageOutcome {
				f.failed = frontierQuarantined
				return fmt.Errorf(frontierQueueFenceError)
			}
			task.tipOID = event.TaskOID
			task.state = frontierQueueFenced
		case ChoreographyClaimRefused:
			if event.ProposalDigest == frontierNoProposalDigest || event.ProposalID == frontierNoProposalID ||
				event.OutputOID != frontierNoOutputOID || event.RefusalReason == frontierNoRefusalReason || event.Node == frontierNoNode ||
				(f.graphRootOID != frontierNoGraphRootOID && event.GraphRootOID != f.graphRootOID) {
				f.failed = frontierQuarantined
				return fmt.Errorf(frontierInvalidClaimRefusalError)
			}
			// A refusal fences this exact proposal, but says nothing about
			// whether the logical task is ready for a later capacity epoch.
			// The pending selector remains parked until the caller observes a
			// capacity release and explicitly requeues it.
			if event.Node == f.node && event.RefusalReason == ChoreographyRefusalNotReady {
				if task.preparedSinceSelection && !task.spec.ExecutorOwned {
					task.refusedNotReady = frontierNotReadyCleared
					task.preparedSinceSelection = frontierFrameNotPrepared
					task.state = frontierReady
					f.ready = append(f.ready, event.Task)
				} else {
					task.refusedNotReady = frontierNotReadyRefused
				}
			}
		case ChoreographyClaimAccepted:
			if (task.state != frontierReady && task.state != frontierPending && task.state != frontierOffered) ||
				event.Node == frontierNoNode || (f.graphRootOID != frontierNoGraphRootOID &&
				(event.GraphRootOID != f.graphRootOID || event.AssignmentOID == frontierNoAssignmentOID)) {
				f.failed = frontierQuarantined
				return fmt.Errorf(frontierClaimStateConflictError)
			}
			if f.graphRootOID != frontierNoGraphRootOID && event.PriorTaskOID != task.tipOID {
				f.failed = frontierQuarantined
				return fmt.Errorf(frontierWrongClaimRootError)
			}
			if task.state == frontierOffered &&
				(event.PriorTaskOID != task.offeredOID || event.Node != task.offeredNode) {
				f.failed = frontierQuarantined
				return fmt.Errorf(frontierWrongOfferedClaimError)
			}
			task.tipOID = event.TaskOID
			if event.PickupID != frontierNoProposalID {
				if task.spec.ExecutorOwned ||
					event.GraphRootOID != f.graphRootOID || task.pickups[event.PickupID] != nil {
					f.failed = frontierQuarantined
					return fmt.Errorf(frontierPickupIdentityError)
				}
				task.pickups[event.PickupID] = &choreographyFrontierPickup{
					claimOID: event.TaskOID, node: event.Node, attempt: event.Attempt}
				task.currentPickupID = event.PickupID
			}
			task.offeredOID, task.offeredNode = frontierNoOfferOID, frontierNoNode
			task.state = frontierBusy
		case ChoreographyResultAccepted, ChoreographyAckAccepted:
			if event.PickupID != frontierNoProposalID {
				if pickup == nil || pickup.settled || pickup.claimOID != event.ClaimOID ||
					pickup.node != event.Node || pickup.attempt != event.Attempt ||
					pickup.resultReceiptOID != frontierNoResultReceiptOID ||
					event.GraphRootOID != f.graphRootOID ||
					event.ProposalDigest == frontierNoProposalDigest || event.ProposalID == frontierNoProposalID ||
					event.Kind == ChoreographyResultAccepted &&
						(event.TaskOID != task.tipOID || event.PriorTaskOID != task.tipOID ||
							event.ResultReceiptOID == frontierNoResultReceiptOID || event.OutputOID == frontierNoOutputOID) ||
					event.Kind == ChoreographyAckAccepted &&
						(event.OutputOID != frontierNoOutputOID || pickup.cancelOID == frontierNoTaskOID ||
							event.ResultReceiptOID != frontierNoResultReceiptOID &&
								(event.TaskOID != task.tipOID || event.PriorTaskOID != task.tipOID) ||
							event.ResultReceiptOID == frontierNoResultReceiptOID &&
								(task.currentPickupID != event.PickupID || task.state != frontierBusy ||
									event.PriorTaskOID != task.tipOID)) {
					f.failed = frontierQuarantined
					return fmt.Errorf(frontierPickupIdentityError)
				}
				if event.Kind == ChoreographyResultAccepted || event.ResultReceiptOID != frontierNoResultReceiptOID {
					pickup.resultReceiptOID = event.ResultReceiptOID
				} else {
					pickup.resultReceiptOID = event.TaskOID
					task.tipOID = event.TaskOID
				}
				pickup.outputOID = event.OutputOID
				continue
			}
			if task.state != frontierBusy || event.ProposalDigest == frontierNoProposalDigest || event.ProposalID == frontierNoProposalID ||
				event.Kind == ChoreographyResultAccepted && event.OutputOID == frontierNoOutputOID ||
				event.Kind == ChoreographyAckAccepted && event.OutputOID != frontierNoOutputOID {
				f.failed = frontierQuarantined
				return fmt.Errorf(frontierResultWithoutClaimError)
			}
		case ChoreographyCancelIssued:
			if task.state != frontierBusy || event.PriorTaskOID != task.tipOID ||
				event.PickupID != frontierNoProposalID &&
					(pickup == nil || task.currentPickupID != event.PickupID ||
						pickup.claimOID != event.ClaimOID || pickup.cancelOID != frontierNoTaskOID ||
						event.CancelReplyDeadlineUTC == frontierNoAdmissionDeadlineUTC ||
						event.GraphRootOID != f.graphRootOID) ||
				event.Node == frontierNoNode ||
				event.ResultReceiptOID != frontierNoResultReceiptOID || event.OutputOID != frontierNoOutputOID ||
				event.Weight != frontierNoTransferWeight || event.Budget != frontierNoBudget {
				f.failed = frontierQuarantined
				return fmt.Errorf(frontierCancelWithoutClaimError)
			}
			task.tipOID = event.TaskOID
			if pickup != nil {
				pickup.cancelOID = event.TaskOID
				pickup.cancelReplyDeadlineUTC = event.CancelReplyDeadlineUTC
			}
		case ChoreographyPickupContactLost:
			if pickup == nil || pickup.settled || pickup.contactLost ||
				task.state != frontierBusy || task.spec.ExecutorOwned ||
				task.currentPickupID != event.PickupID ||
				pickup.claimOID != event.ClaimOID || pickup.node != event.Node ||
				pickup.attempt != event.Attempt || event.TaskOID != task.tipOID ||
				event.PriorTaskOID != task.tipOID || event.GraphRootOID != f.graphRootOID ||
				pickup.cancelOID == frontierNoTaskOID ||
				event.CancelReplyDeadlineUTC != pickup.cancelReplyDeadlineUTC {
				f.failed = frontierQuarantined
				return fmt.Errorf(frontierPickupIdentityError)
			}
			pickup.contactLost = frontierPickupContactLossRecorded
			task.currentPickupID = frontierNoProposalID
			task.attempt++
			task.state = frontierReady
			f.ready = append(f.ready, event.Task)
		case ChoreographyPickupSettled:
			if pickup == nil || pickup.settled || pickup.claimOID != event.ClaimOID ||
				pickup.node != event.Node || pickup.attempt != event.Attempt ||
				pickup.resultReceiptOID == frontierNoResultReceiptOID ||
				pickup.resultReceiptOID != event.ResultReceiptOID ||
				pickup.outputOID != event.OutputOID ||
				event.TaskOID != task.tipOID || event.PriorTaskOID != task.tipOID ||
				event.GraphRootOID != f.graphRootOID {
				f.failed = frontierQuarantined
				return fmt.Errorf(frontierPickupIdentityError)
			}
			pickup.settled = frontierPickupSettlementRecorded
		case ChoreographyRetryReady:
			if event.PickupID != frontierNoProposalID {
				if pickup == nil || pickup.settled || task.state != frontierBusy ||
					task.currentPickupID != event.PickupID ||
					pickup.claimOID != event.ClaimOID || pickup.node != event.Node ||
					pickup.attempt != event.Attempt || pickup.resultReceiptOID == frontierNoResultReceiptOID ||
					pickup.resultReceiptOID != event.ResultReceiptOID ||
					pickup.outputOID != event.OutputOID ||
					event.PriorTaskOID != task.tipOID || event.GraphRootOID != f.graphRootOID ||
					event.Succeeded || event.PackageOutcome != frontierNoPackageOutcome {
					f.failed = frontierQuarantined
					return fmt.Errorf(frontierPickupIdentityError)
				}
				pickup.settled = frontierPickupSettlementRecorded
				task.currentPickupID = frontierNoProposalID
				task.tipOID = event.TaskOID
				task.attempt++
				task.state = frontierReady
				f.ready = append(f.ready, event.Task)
				continue
			}
			if task.state != frontierBusy && task.state != frontierOffered {
				f.failed = frontierQuarantined
				return fmt.Errorf(frontierRetryWithoutClaimError)
			}
			if task.state == frontierOffered &&
				(event.PriorTaskOID != task.offeredOID || event.Node != task.offeredNode ||
					event.ResultReceiptOID != frontierNoResultReceiptOID || event.OutputOID != frontierNoOutputOID ||
					event.Weight != frontierNoTransferWeight || event.Budget != frontierNoBudget) {
				f.failed = frontierQuarantined
				return fmt.Errorf(frontierUnclaimedRetryFenceError)
			}
			if task.state == frontierBusy && event.ResultReceiptOID == frontierNoResultReceiptOID {
				f.failed = frontierQuarantined
				return fmt.Errorf(frontierClaimedRetryResultError)
			}
			task.tipOID = event.TaskOID
			task.offeredOID, task.offeredNode = frontierNoOfferOID, frontierNoNode
			task.attempt++
			task.state = frontierReady
			if !task.spec.ExecutorOwned {
				f.ready = append(f.ready, event.Task)
			}
		case ChoreographyTerminal:
			if event.PickupID != frontierNoProposalID {
				if pickup == nil || pickup.settled || task.state == frontierTerminal ||
					pickup.claimOID != event.ClaimOID || pickup.node != event.Node ||
					pickup.attempt != event.Attempt || pickup.resultReceiptOID == frontierNoResultReceiptOID ||
					pickup.resultReceiptOID != event.ResultReceiptOID ||
					pickup.outputOID != event.OutputOID ||
					event.PriorTaskOID != task.tipOID || event.GraphRootOID != f.graphRootOID ||
					event.Succeeded && event.OutputOID == frontierNoOutputOID {
					f.failed = frontierQuarantined
					return fmt.Errorf(frontierPickupIdentityError)
				}
				pickup.settled = frontierPickupSettlementRecorded
				task.currentPickupID = frontierNoProposalID
				task.tipOID = event.TaskOID
				task.state = frontierTerminal
				task.terminal = event
				task.outputOID = event.OutputOID
				pendingPackageSettlement := task.spec.Package != frontierNoPackage &&
					event.PackageOutcome == frontierNoPackageOutcome
				task.packageSettled = !pendingPackageSettlement
				if !pendingPackageSettlement {
					if err := f.releaseChildren(task); err != nil {
						f.failed = frontierQuarantined
						return err
					}
				}
				continue
			}
			unclaimed := task.state == frontierQueueFenced
			if !task.spec.LocalAuthority && task.state != frontierBusy && !unclaimed ||
				task.state == frontierTerminal {
				f.failed = frontierQuarantined
				return fmt.Errorf(frontierTerminalWithoutAttemptError)
			}
			if unclaimed && (event.PriorTaskOID != task.tipOID ||
				event.Node != task.offeredNode || event.ResultReceiptOID != frontierNoResultReceiptOID ||
				event.OutputOID != frontierNoOutputOID || event.Succeeded || event.Weight != frontierNoTransferWeight ||
				event.Budget != frontierNoBudget) {
				f.failed = frontierQuarantined
				return fmt.Errorf(frontierUnclaimedTerminalFenceError)
			}
			task.state = frontierTerminal
			task.terminal = event
			pendingPackageSettlement := f.graphRootOID != frontierNoGraphRootOID &&
				!task.spec.ExecutorOwned && task.spec.Package != frontierNoPackage &&
				event.PackageOutcome == frontierNoPackageOutcome &&
				event.ResultReceiptOID != frontierNoResultReceiptOID
			task.packageSettled = !pendingPackageSettlement
			if event.Succeeded && !task.spec.LocalAuthority && event.OutputOID == frontierNoOutputOID {
				f.failed = frontierQuarantined
				return fmt.Errorf(frontierSuccessfulTerminalResultError)
			}
			// A failed final result can still carry verified partial exports for
			// a sweep configured to continue after errors.
			task.outputOID = event.OutputOID
			if !pendingPackageSettlement {
				if err := f.releaseChildren(task); err != nil {
					f.failed = frontierQuarantined
					return err
				}
			}
		case ChoreographyTaskPackageSettled:
			if f.graphRootOID == frontierNoGraphRootOID || event.GraphRootOID != f.graphRootOID ||
				task.state != frontierTerminal || task.terminal.Kind != ChoreographyTerminal ||
				task.terminal.ResultReceiptOID == frontierNoResultReceiptOID || task.packageSettled ||
				event.TaskOID != task.terminal.TaskOID || event.PriorTaskOID != task.terminal.TaskOID ||
				event.Attempt != task.terminal.Attempt || event.Package != task.spec.Package ||
				event.Node != frontierNoNode || event.ResultReceiptOID != frontierNoResultReceiptOID || event.OutputOID != frontierNoOutputOID ||
				event.Weight != frontierNoTransferWeight || event.Budget != frontierNoBudget ||
				event.PackageOutcome == frontierNoPackageOutcome && !task.terminal.Succeeded &&
					task.spec.Kind != KindPrepare &&
					priorPackageStatus == frontierNoPackageOutcome {
				f.failed = frontierQuarantined
				return fmt.Errorf(frontierTaskPackageSettlementError)
			}
			task.packageSettled = frontierPackageSettled
			task.outputAdmissionRejected = event.OutputAdmissionRejected
			if err := f.releaseChildren(task); err != nil {
				f.failed = frontierQuarantined
				return err
			}
		case ChoreographyBlocked:
			if task.state == frontierBusy || task.state == frontierQueueFenced ||
				task.state == frontierTerminal || event.Node != frontierNoNode ||
				event.OutputOID != frontierNoOutputOID || event.Succeeded ||
				(event.BlockedBy == frontierNoBlocker && f.packageStatus[task.spec.Package] == frontierNoPackageOutcome) {
				f.failed = frontierQuarantined
				return fmt.Errorf(frontierBlockedWithoutCauseError)
			}
			task.state = frontierTerminal
			task.terminal = event
			if err := f.releaseChildren(task); err != nil {
				f.failed = frontierQuarantined
				return err
			}
		case ChoreographyNoClaimExpired:
			if f.graphRootOID == frontierNoGraphRootOID || event.GraphRootOID != f.graphRootOID ||
				task.state == frontierBusy || task.state == frontierTerminal ||
				event.PriorTaskOID != task.tipOID || event.Node != frontierNoNode ||
				event.Succeeded || event.OutputOID != frontierNoOutputOID || event.BlockedBy == frontierNoBlocker ||
				task.spec.Package != frontierNoPackage && event.PackageOutcome != frontierNoPackageOutcome {
				f.failed = frontierQuarantined
				return fmt.Errorf(frontierNoClaimWithoutCauseError)
			}
			task.state = frontierTerminal
			task.tipOID = event.TaskOID
			task.terminal = event
			if task.spec.Package == frontierNoPackage || task.spec.Kind == string(KindPrepare) {
				if err := f.releaseChildren(task); err != nil {
					f.failed = frontierQuarantined
					return err
				}
			}
		case ChoreographyNoClaimPackageSettled:
			if f.graphRootOID == frontierNoGraphRootOID || event.GraphRootOID != f.graphRootOID ||
				task.state != frontierTerminal || task.terminal.Kind != ChoreographyNoClaimExpired ||
				event.PriorTaskOID != task.tipOID || event.TaskOID != task.tipOID ||
				(event.PackageOutcome != frontierPackageOutcomeFailed &&
					(event.PackageOutcome != frontierNoPackageOutcome || f.packageStatus[task.spec.Package] != frontierPackageOutcomeFailed)) ||
				event.Package == frontierNoPackage || event.Package != task.spec.Package {
				f.failed = frontierQuarantined
				return fmt.Errorf(frontierNoClaimSettlementError)
			}
			task.terminal = event
			if err := f.releaseChildren(task); err != nil {
				f.failed = frontierQuarantined
				return err
			}
		case ChoreographyInertCancelled:
			if f.graphRootOID == frontierNoGraphRootOID || !f.stopped || event.GraphRootOID != f.graphRootOID ||
				task.state == frontierBusy || task.state == frontierTerminal ||
				event.PriorTaskOID != task.tipOID || event.Node != frontierNoNode ||
				event.Succeeded || event.OutputOID != frontierNoOutputOID || event.BlockedBy != choreographyInertCancelCause ||
				event.PackageOutcome != frontierNoPackageOutcome {
				f.failed = frontierQuarantined
				return fmt.Errorf(frontierInertCancelStateError)
			}
			task.state = frontierTerminal
			task.tipOID = event.TaskOID
			task.terminal = event
			if task.spec.Package == frontierNoPackage {
				if err := f.releaseChildren(task); err != nil {
					f.failed = frontierQuarantined
					return err
				}
			}
		case ChoreographyInertCancelSettled:
			if f.graphRootOID == frontierNoGraphRootOID || event.GraphRootOID != f.graphRootOID ||
				task.state != frontierTerminal || task.terminal.Kind != ChoreographyInertCancelled ||
				event.PriorTaskOID != task.tipOID || event.TaskOID != task.tipOID ||
				!isValidChoreographyInertCancelOutcome(event.PackageOutcome,
					priorPackageStatus) ||
				event.Package == frontierNoPackage || event.Package != task.spec.Package {
				f.failed = frontierQuarantined
				return fmt.Errorf(frontierInertCancelSettlementError)
			}
			task.terminal = event
			if err := f.releaseChildren(task); err != nil {
				f.failed = frontierQuarantined
				return err
			}
		default:
			f.failed = frontierQuarantined
			return fmt.Errorf(frontierUnknownEventKindError)
		}
	}
	f.cursor = page.Cursor
	return nil
}

func (f *ChoreographyFrontier) releaseChildren(task *choreographyFrontierTask) error {
	for _, child := range task.children {
		next := f.tasks[child]
		if task.spec.Kind == KindPrepare && (!task.terminal.Succeeded || task.outputAdmissionRejected) &&
			choreographyPreparedProviderFor(next.spec, task.spec.Package) {
			next.noteFailedPrepared(task.spec.Package)
		}
		next.remaining--
		if next.remaining == frontierNoPredecessors {
			if next.spec.Virtual {
				if err := f.completeVirtual(next); err != nil {
					return err
				}
			} else if !next.spec.ExecutorOwned {
				f.ready = append(f.ready, child)
			}
		}
	}
	return nil
}

func (f *ChoreographyFrontier) Cursor() ChoreographyEventCursor { return f.cursor }

// CompletedOutput resolves one declared provider task's exact signed Result
// commit after durable logical finality in O(1), including a failed sweep
// task's verified partial exports. The app supplies IDs from compiled input
// edges; no transitive graph scan or mutable ref lookup occurs at selection.
func (f *ChoreographyFrontier) CompletedOutput(taskName string) (string, bool) {
	task := f.tasks[taskName]
	if task == nil || task.state != frontierTerminal || !task.packageSettled ||
		task.outputAdmissionRejected || task.outputOID == frontierNoOutputOID {
		return frontierNoOutputOID, frontierOutputUnavailable
	}
	return task.outputOID, frontierOutputAvailable
}

// CompletedResultEvent returns the exact signed terminal identity for one
// direct predecessor whose final Result was admitted. A peer must verify this
// decision before reading its exports; a raw ResultAccepted is insufficient.
func (f *ChoreographyFrontier) CompletedResultEvent(taskName string) (ChoreographyEvent, bool) {
	task := f.tasks[taskName]
	if task == nil || task.state != frontierTerminal || !task.packageSettled || task.outputAdmissionRejected ||
		task.terminal.Kind != ChoreographyTerminal || task.terminal.OutputOID == frontierNoOutputOID {
		return ChoreographyEvent{}, frontierOutputUnavailable
	}
	return task.terminal, frontierOutputAvailable
}

// IsClaimProposalFencedByEvent is true only when a durable task transition
// proves a pending claim's expected ref can no longer be its CAS predecessor.
// It does not turn a bare receipt 404 into a refusal; the caller still checks
// the exact receipt before releasing local capacity.
func IsClaimProposalFencedByEvent(proposal ChoreographyClaimProposal, event ChoreographyEvent) bool {
	if event.Kind == ChoreographyClaimRefused && event.Task == proposal.Task &&
		event.Attempt == proposal.Attempt && event.Node == proposal.Node {
		digest, err := ChoreographyProposalDigest(proposal)
		return err == nil && event.ProposalDigest == digest && event.ProposalID == proposal.ProposalID
	}
	if event.Task != proposal.Task || event.Attempt < proposal.Attempt || event.TaskOID == proposal.ExpectedOID {
		return frontierClaimNotFenced
	}
	if event.Kind == ChoreographyClaimAccepted {
		return event.Node != proposal.Node
	}
	if event.Kind == ChoreographyBlocked {
		// A branch-bearing Blocked commit advances exactly the reservation
		// named by PriorTaskOID. Branchless local effects leave it empty.
		return event.Attempt == proposal.Attempt && event.PriorTaskOID == proposal.ExpectedOID
	}
	if event.Kind == ChoreographyQueueFenced {
		return event.Attempt == proposal.Attempt && event.PriorTaskOID == proposal.ExpectedOID
	}
	return event.Kind == ChoreographyRetryReady || event.Kind == ChoreographyTerminal
}
