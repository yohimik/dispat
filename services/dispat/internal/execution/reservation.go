// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// Preallocated task attempts and fixed-size dispatch slots for protocol v2.

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// ReservationTask is one logical task that may be placed on a worker. It
// names only stable plan facts; snapshots and predecessor outcomes are filled
// into its assignment when the task becomes ready.
type ReservationTask struct {
	Task      string
	Kind      string
	Platforms []string
	Placement Placement
}

type reservedAttempt struct {
	branch string
	oid    string
}

type reservedTask struct {
	kind     string
	nodes    map[string]bool
	eligible []string
	attempts [maxPlacementAttempts]reservedAttempt
}

type dispatchSlot struct {
	branch   string
	oid      string
	sequence uint64
	inUse    bool
}

type reservationState struct {
	mu    sync.Mutex
	tasks map[string]*reservedTask
	free  map[string][]*dispatchSlot
	ready bool
}

type claimedReservation struct {
	attempt reservedAttempt
	slot    *dispatchSlot
	prior   string
	next    uint64
}

type reservationClaim struct {
	lease   *Lease
	task    string
	kind    string
	attempt int
}

func (c *Coordinator) claimReservedAttempt(input reservationClaim) (claimedReservation, error) {
	state := c.reservations
	if state == nil {
		return claimedReservation{}, fmt.Errorf("execution: task branches were not initialized")
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if !state.ready {
		return claimedReservation{}, fmt.Errorf("execution: task branches are not ready")
	}
	reserved := state.tasks[input.task]
	if reserved == nil || reserved.kind != input.kind || !reserved.nodes[input.lease.Node] ||
		input.attempt < 1 || input.attempt > maxPlacementAttempts {
		return claimedReservation{}, fmt.Errorf("execution: %s attempt %d has no reservation on %s",
			input.task, input.attempt, input.lease.Node)
	}
	free := state.free[input.lease.Node]
	if len(free) != 0 {
		slot := free[len(free)-1]
		state.free[input.lease.Node] = free[:len(free)-1]
		slot.inUse = true
		input.lease.onSettle = func(reason string) {
			if reason != "" {
				return
			}
			state.mu.Lock()
			if slot.inUse {
				slot.inUse = false
				state.free[input.lease.Node] = append(state.free[input.lease.Node], slot)
			}
			state.mu.Unlock()
		}
		return claimedReservation{attempt: reserved.attempts[input.attempt-1], slot: slot,
			prior: slot.oid, next: slot.sequence + 1}, nil
	}
	return claimedReservation{}, fmt.Errorf("execution: %s has no free dispatch slot on %s", input.task, input.lease.Node)
}

func (c *Coordinator) commitSlotNotice(ctx context.Context, node string,
	claimed claimedReservation, oid string) {
	state := c.reservations
	state.mu.Lock()
	claimed.slot.oid = oid
	claimed.slot.sequence = claimed.next
	state.mu.Unlock()
	c.recordOwnedRef(ctx, ownedRefStep{
		node: node, branch: claimed.slot.branch, oid: oid, parent: claimed.prior})
}

type endpointBatch struct {
	mailbox *GitMailbox
	nodes   []string
	refs    map[string]string
}

// InitializeStageTasks walks the compiled release DAG and the unreleasing
// providers its delegated stages may prepare. It reserves the complete
// possible worker task set before any stage frame is admitted.
func (c *Coordinator) InitializeStageTasks(ctx context.Context, stages []release.ReservationStage) error {
	specs := make([]ReservationTask, 0, len(stages))
	visitedProviders := make(map[string]bool)
	var visitProviders func(string) error
	visitProviders = func(consumer string) error {
		for _, input := range c.dispatch.Inputs(consumer) {
			if !input.IsPrepared || visitedProviders[input.Package] {
				continue
			}
			visitedProviders[input.Package] = true
			prepared, err := c.dispatch.Prepare(input.Package)
			if err != nil {
				return err
			}
			if prepared == nil || prepared.Request.Release == nil || prepared.Request.Release.Pkg == nil {
				return fmt.Errorf("execution: prepared provider %s has no package", input.Package)
			}
			space := prepared.Request.Release.Pkg.Space
			specs = append(specs, ReservationTask{Task: formatPrepareTask(input.Package), Kind: KindPrepare,
				Platforms: space.BuildPlatforms,
				Placement: ResolveStagePlacement(StageBuild, space.RunOnly.ResolveBuild(), len(space.LoginScript) > 0)})
			if err := visitProviders(input.Package); err != nil {
				return err
			}
		}
		return nil
	}
	for _, stage := range stages {
		if stage.Release == nil || stage.Release.Pkg == nil || stage.Release.Pkg.Space == nil {
			return fmt.Errorf("execution: a compiled stage has no package")
		}
		space := stage.Release.Pkg.Space
		specs = append(specs, ReservationTask{
			Task: stage.Release.Pkg.Name + ":" + stage.Descriptor.Name,
			Kind: resolveStageKind(stage.Descriptor), Platforms: space.BuildPlatforms,
			Placement: resolveDescriptorPlacement(stage.Descriptor, space),
		})
		if stage.Descriptor.Inputs == release.InputsProviderOutputs {
			if err := visitProviders(stage.Release.Pkg.Name); err != nil {
				return err
			}
		}
	}
	return c.InitializeTasks(ctx, specs)
}

// InitializeTasks creates every possible worker attempt and the worker's
// bounded dispatch slots before the first task runs. A failed batch leaves
// the state unavailable for dispatch; Close cleans every ref this run owns.
func (c *Coordinator) InitializeTasks(ctx context.Context, stated []ReservationTask) error {
	if c.reservations != nil {
		return fmt.Errorf("execution: task reservations were already initialized")
	}
	if c.Pool == nil {
		return fmt.Errorf("execution: task reservations require preflight")
	}
	state := &reservationState{tasks: make(map[string]*reservedTask),
		free: make(map[string][]*dispatchSlot)}
	byEndpoint := make(map[string]*endpointBatch)
	linksByName := make(map[string]Link, len(c.Links))
	for _, link := range c.Links {
		linksByName[link.Name] = link
		batch := byEndpoint[link.Endpoint]
		if batch == nil {
			batch = &endpointBatch{mailbox: c.mailboxes[link.Name], refs: make(map[string]string)}
			byEndpoint[link.Endpoint] = batch
		}
		batch.nodes = append(batch.nodes, link.Name)
	}
	count := make(map[string]int)
	for _, spec := range stated {
		if spec.Task == "" || spec.Kind == "" {
			return fmt.Errorf("execution: a reservation has no task or kind")
		}
		if _, exists := state.tasks[spec.Task]; exists {
			return fmt.Errorf("execution: task %s was reserved twice", spec.Task)
		}
		nodes := c.Pool.EligibleWorkers(spec.Platforms, spec.Placement)
		if spec.Placement == PlacementWorker && len(nodes) == 0 {
			return fmt.Errorf("execution: task %s has no compatible worker", spec.Task)
		}
		if len(nodes) == 0 {
			continue
		}
		task := &reservedTask{kind: spec.Kind, nodes: make(map[string]bool, len(nodes)),
			eligible: make([]string, 0, len(nodes))}
		for _, node := range nodes {
			if _, ok := linksByName[node.Name]; !ok {
				return fmt.Errorf("execution: eligible worker %s has no mailbox", node.Name)
			}
			task.nodes[node.Name] = true
			task.eligible = append(task.eligible, node.Name)
			count[node.Name]++
		}
		state.tasks[spec.Task] = task
	}
	// Preparing one signed commit per logical attempt makes its object id the
	// same on every endpoint, including different URLs for the same Git repo.
	branchOwner := make(map[string]string)
	names := make([]string, 0, len(state.tasks))
	for name := range state.tasks {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		task := state.tasks[name]
		for attempt := 1; attempt <= maxPlacementAttempts; attempt++ {
			branch := FormatReservationBranch(c.Run, name, attempt)
			if owner, exists := branchOwner[branch]; exists {
				return fmt.Errorf("execution: task branch collision between %s and %s", owner, name)
			}
			branchOwner[branch] = name
			mailbox := c.mailboxes[task.eligible[0]]
			if mailbox == nil {
				return fmt.Errorf("execution: task %s has no mailbox", name)
			}
			header := c.formatOrchestratorHeader(task.kind, name, attempt, "", branch)
			oid, err := mailbox.PrepareReservation(ctx, &Reservation{Header: header})
			if err != nil {
				return err
			}
			task.attempts[attempt-1] = reservedAttempt{branch: branch, oid: oid}
			for _, node := range task.eligible {
				link := linksByName[node]
				byEndpoint[link.Endpoint].refs[branch] = oid
			}
		}
	}
	capacities := make(map[string]int, len(c.Links))
	for _, worker := range c.Pool.EligibleWorkers(nil, PlacementWorker) {
		capacities[worker.Name] = worker.Capacity
	}
	for _, link := range c.Links {
		eligible := count[link.Name]
		if eligible == 0 {
			continue
		}
		for index := range min(capacities[link.Name], eligible) {
			branch := FormatSlotBranch(link.Name, c.Run, index)
			if owner, exists := branchOwner[branch]; exists {
				return fmt.Errorf("execution: dispatch slot on %s collides with %s", link.Name, owner)
			}
			branchOwner[branch] = link.Name
			notice := &SlotNotice{Header: c.formatOrchestratorHeader(KindSlot, "", 0, link.Name, branch)}
			oid, err := c.mailboxes[link.Name].PrepareSlotNotice(ctx, notice, "")
			if err != nil {
				return err
			}
			slot := &dispatchSlot{branch: branch, oid: oid}
			state.free[link.Name] = append(state.free[link.Name], slot)
			byEndpoint[link.Endpoint].refs[branch] = oid
		}
	}
	endpoints := make([]string, 0, len(byEndpoint))
	for endpoint := range byEndpoint {
		endpoints = append(endpoints, endpoint)
	}
	sort.Strings(endpoints)
	// Expose the state only after all refs are settled. A partial push is a
	// failed initialization and no task is allowed to use those refs.
	c.reservations = state
	for _, endpoint := range endpoints {
		if err := c.createInitialRefs(ctx, endpoint, byEndpoint[endpoint]); err != nil {
			return err
		}
	}
	state.ready = true
	return nil
}

func (c *Coordinator) createInitialRefs(ctx context.Context, endpoint string,
	batch *endpointBatch) error {
	names := make([]string, 0, len(batch.refs))
	for branch := range batch.refs {
		names = append(names, branch)
	}
	sort.Strings(names)
	for start := 0; start < len(names); start += gitx.MaxTransportBatch {
		stop := min(start+gitx.MaxTransportBatch, len(names))
		refs := make([]gitx.CreateRef, 0, stop-start)
		for _, branch := range names[start:stop] {
			refs = append(refs, gitx.CreateRef{Branch: branch, OID: batch.refs[branch]})
		}
		for _, ref := range refs {
			c.recordOwnedRef(ctx, ownedRefStep{node: batch.nodes[0], branch: ref.Branch, oid: ref.OID})
		}
		if err := batch.mailbox.remote.PushCreateAtomic(ctx, endpoint, refs); err != nil {
			if verifyErr := c.verifyInitialRefs(ctx, endpoint, batch.mailbox, refs); verifyErr != nil {
				return fmt.Errorf("execution: initializing task branches on %s: %w: %w",
					gitx.RedactURL(endpoint), err, verifyErr)
			}
		}
	}
	return nil
}

// verifyInitialRefs accepts an ambiguous or aliased create only when every
// exact branch already points at the very signed object this run prepared.
func (c *Coordinator) verifyInitialRefs(ctx context.Context, endpoint string,
	mailbox *GitMailbox, refs []gitx.CreateRef) error {
	for _, ref := range refs {
		heads, err := mailbox.remote.ListRemoteHeads(ctx, endpoint, "refs/heads/"+ref.Branch)
		if err != nil {
			return err
		}
		matched := false
		for _, head := range heads {
			if head.Name != ref.Branch {
				continue
			}
			if head.OID != ref.OID {
				return fmt.Errorf("%s has a foreign object", ref.Branch)
			}
			matched = true
		}
		if !matched {
			return fmt.Errorf("%s is absent", ref.Branch)
		}
	}
	return nil
}
