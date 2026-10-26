// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// Where one task runs, and what holding a node's capacity means.
//
// The pool is the run's whole placement policy, and it is deliberately small.
// A task asks for a node that satisfies its platforms; it is given the free
// slot of the healthiest, least loaded compatible node; and it holds that slot
// until the attempt is terminal. Nothing here schedules: the run's own stage
// budgets already decide how many builds may be in flight, and a pool that
// also decided would be a second scheduler disagreeing with the first.
//
// One rule is worth stating because it is the reason a lost node cannot cost a
// run its capacity twice. A slot returns when the attempt ended in a way both
// parties agree on: a result, or an acknowledged withdrawal. An attempt that
// simply stopped answering leaves its slot where it is and takes its node out
// of the pool, because the work may still be running on that machine and
// placing something else there would be placing it beside an unknown process.

import (
	"context"
	"fmt"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/rs/zerolog"
)

const (
	poolEmptyRosterSize                = 0
	poolPlatformSeparator              = "/"
	poolMinimumTaskWeight              = 1
	poolNodeInitiallyHealthy           = true
	poolFirstNodePresent               = true
	poolFirstNodeAbsent                = false
	poolPlacementInitiallyPossible     = false
	poolInvalidWeightedPlacementReason = "execution: invalid weighted node placement"
	poolInvalidLocalWeightReason       = "execution: local task weight is invalid"
	poolLocalTaskIncompatibleReason    = "execution: first node cannot execute this task's platform or weight"
	poolFirstNodeMissingReason         = "execution: first node is absent from its execution pool"
	poolWorkerLogKey                   = "worker"
	poolPlacementLogKey                = "placement"
)

// Pool places tasks on the nodes preflight reported, and bounds how many each
// of them runs at once.
type Pool struct {
	log zerolog.Logger

	// mu guards every field below it: one owner for the whole placement state,
	// because "is there a slot" and "take it" have to be one decision.
	mu    sync.Mutex
	nodes []*poolNode
	// changed is closed and replaced whenever a slot is returned or a node's
	// health changes. Waiting on a channel rather than on a condition variable
	// is what lets a waiting task be interrupted by its own context.
	changed chan struct{}
}

// poolNode is one node as the pool sees it: what it is, what it can take, and
// how much of that is in use.
type poolNode struct {
	name        string
	authorityID string
	platform    string
	capacity    int
	inFlight    int
	isHealthy   bool
	// isLocal marks the orchestrator's own entry. It is what a placement
	// filters on, and it is also why the entry is a node here rather than a
	// mailbox somewhere: this machine runs a frame by running it, so giving
	// it an endpoint would be giving it a way to talk to itself.
	isLocal bool
}

// LocalNode is the orchestrator's own place in the pool: what this machine
// calls itself, and how much of its own run it may execute (§28.1, the
// orchestrator "MAY execute tasks locally under the same rules").
//
// The capacity is this node's `execution.concurrency`, the same number that
// bounds the frames it keeps for itself, because it is one machine and the
// work does not become cheaper for having been placed rather than delegated.
type LocalNode struct {
	Name        string
	Capacity    int
	AuthorityID string
}

// EligibleWorker is a preflighted node that could run one declared task.
// Capacity is the maximum number of its simultaneous one-slot tasks.
type EligibleWorker struct {
	Name     string
	Capacity int
}

// WeightedPlacementRequest identifies one eligible placement and its exact
// signed weight. A waiting request does not reserve any node capacity.
type WeightedPlacementRequest struct {
	Platforms []string
	Placement Placement
	Preferred string
	Weight    int
}

// EligibleWorkers lists every compatible worker before task execution starts.
// The list is a superset of later placements; capacity and health may shrink.
func (p *Pool) EligibleWorkers(platforms []string, placement Placement) []EligibleWorker {
	p.mu.Lock()
	defer p.mu.Unlock()
	var eligible []EligibleWorker
	for _, node := range p.nodes {
		if node.isLocal || !node.isHealthy || !isNodeAllowed(node, placement) ||
			!isPlatformCompatible(node.platform, platforms) {
			continue
		}
		eligible = append(eligible, EligibleWorker{Name: node.name, Capacity: node.capacity})
	}
	return eligible
}

// choreographyRosterNodes snapshots the exact pool accepted at preflight.
// Node availability may later shrink; the signed root fixes compatibility
// and capacity, while claims still need the current broker budget.
func (p *Pool) choreographyRosterNodes() []ChoreographyRuntimeNode {
	p.mu.Lock()
	defer p.mu.Unlock()
	nodes := make([]ChoreographyRuntimeNode, poolEmptyRosterSize, len(p.nodes))
	for _, node := range p.nodes {
		nodes = append(nodes, ChoreographyRuntimeNode{
			Name: node.name, Platform: node.platform,
			Capacity: node.capacity, First: node.isLocal, AuthorityID: node.authorityID,
		})
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Name < nodes[j].Name })
	return nodes
}

// ChoreographyNodes exposes the post-preflight pool identity that the ledger
// root retains. It never reports a node from ambient peer configuration.
func (c *Coordinator) ChoreographyNodes() []ChoreographyRuntimeNode {
	if c == nil || c.Pool == nil {
		return nil
	}
	return c.Pool.choreographyRosterNodes()
}

// Lease is one node slot held by one attempt.
//
// It is a value the holder gives back rather than a counter the holder
// decrements, so that the two ways an attempt can end are two named calls at
// the call site: Release for an attempt both parties saw finish, and Leak for
// one whose node stopped answering.
type Lease struct {
	// Node is the node the slot belongs to.
	Node string
	// IsLocal says the slot is this machine's, which is what decides whether
	// the frame travels or is simply run. It is on the lease rather than
	// asked of the pool afterwards because it is a property of the placement
	// that was made, and the pool has moved on by then.
	IsLocal bool

	pool   *Pool
	weight int
	// onSettle releases the matching dispatch-index slot only when a remote
	// attempt is known to have stopped. A leaked node keeps that slot fenced.
	onSettle func(reason string)
	// isSettled keeps a slot from being returned twice, which would hand out
	// capacity the node never got back.
	isSettled bool
}

// NewPool builds the pool from the reports preflight collected, one entry per
// configured link, plus this machine's own.
//
// A worker's capacity is the node's own, because the node is the only party
// that knows what it can run: an orchestrator that decided would be deciding
// about a machine it cannot see. This machine's is its configuration's, and
// its platform is what this binary was built for, which is the same question
// a probe answers about a worker.
func NewPool(links []Link, reports []*NodeReport, local LocalNode, log zerolog.Logger) *Pool {
	nodes := make([]*poolNode, 0, len(links)+1)
	for index, link := range links {
		report := reports[index]
		nodes = append(nodes, &poolNode{
			name:        link.Name,
			authorityID: report.AuthorityID,
			platform:    report.OS + poolPlatformSeparator + report.Arch,
			capacity:    report.Capacity,
			isHealthy:   poolNodeInitiallyHealthy,
		})
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].name < nodes[j].name })
	return &Pool{log: log, changed: make(chan struct{}), nodes: append(nodes, &poolNode{
		name:        local.Name,
		authorityID: local.AuthorityID,
		platform:    runtime.GOOS + poolPlatformSeparator + runtime.GOARCH,
		capacity:    max(local.Capacity, poolMinimumTaskWeight),
		isHealthy:   poolNodeInitiallyHealthy,
		isLocal:     poolFirstNodePresent,
	})}
}

// AcquireNear is Acquire with a node this task would rather run on.
//
// The preference is a preference and never a requirement, which is why it is
// a separate entry point rather than a fourth placement: a publication would
// rather run where the package was built, because that machine's checkout and
// object store already hold the bytes it is about to install, but waiting for
// that machine when another is free would be trading a certain delay for a
// possible copy. An empty name matches no node and is the plain Acquire.
func (p *Pool) AcquireNear(ctx context.Context, platforms []string, placement Placement,
	preferred string) (*Lease, error) {
	return p.AcquireNearWeight(ctx, WeightedPlacementRequest{
		Platforms: platforms, Placement: placement, Preferred: preferred, Weight: poolMinimumTaskWeight,
	})
}

// AcquireNearWeight reserves the complete signed v3 task weight before any
// local physical slot is taken or remote assignment is offered. Legacy
// callers continue to reserve one unit through AcquireNear.
func (p *Pool) AcquireNearWeight(ctx context.Context, request WeightedPlacementRequest) (*Lease, error) {
	if p == nil || request.Weight < poolMinimumTaskWeight {
		return nil, fmt.Errorf(poolInvalidWeightedPlacementReason)
	}
	for {
		p.mu.Lock()
		lease, isPlacementPossible := p.takeSlotWeight(request)
		changed := p.changed
		p.mu.Unlock()
		if lease != nil {
			p.log.Debug().Str(poolWorkerLogKey, lease.Node).Str(poolPlacementLogKey, string(request.Placement)).
				Bool("isLocal", lease.IsLocal).Msg("node slot acquired")
			return lease, nil
		}
		if !isPlacementPossible {
			return nil, p.refusePlacement(request.Platforms, request.Placement)
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("waiting for a node to run this task on: %w", ctx.Err())
		case <-changed:
		}
	}
}

// TryAcquireLocalWeight reserves only the first node's free run-local weight.
// It never selects a peer or waits while another node may claim the task.
func (p *Pool) TryAcquireLocalWeight(platforms []string, weight int) (*Lease, bool, error) {
	if p == nil || weight < poolMinimumTaskWeight {
		return nil, poolFirstNodeAbsent, fmt.Errorf(poolInvalidLocalWeightReason)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, node := range p.nodes {
		if !node.isLocal {
			continue
		}
		if !node.isHealthy || node.capacity < weight ||
			!isPlatformCompatible(node.platform, platforms) {
			return nil, poolFirstNodeAbsent, fmt.Errorf(poolLocalTaskIncompatibleReason)
		}
		if node.inFlight+weight > node.capacity {
			return nil, poolFirstNodeAbsent, nil
		}
		node.inFlight += weight
		return &Lease{Node: node.name, IsLocal: poolFirstNodePresent, pool: p, weight: weight}, poolFirstNodePresent, nil
	}
	return nil, poolFirstNodeAbsent, fmt.Errorf(poolFirstNodeMissingReason)
}

// takeSlot answers the slot this task gets, and whether waiting for one could
// ever help. It runs under the lock.
//
// The preference is deterministic on purpose: the least loaded eligible node,
// and among equals the first by name. A run placing the same plan on the same
// pool twice places it the same way, which is what makes a distributed run's
// behaviour reproducible enough to debug.
//
// This machine comes last and only when nothing else is free. It is the one
// node that also owns the version stages, the lock-file preparation, the
// publications and the records, so a run that spent it on a build that some
// worker could have taken would be a run that queued its own work behind
// somebody else's.
func (p *Pool) takeSlot(platforms []string, placement Placement, preferred string) (*Lease, bool) {
	return p.takeSlotWeight(WeightedPlacementRequest{
		Platforms: platforms, Placement: placement, Preferred: preferred, Weight: poolMinimumTaskWeight,
	})
}

func (p *Pool) takeSlotWeight(request WeightedPlacementRequest) (*Lease, bool) {
	var chosen *poolNode
	isPlacementPossible := poolPlacementInitiallyPossible
	for _, node := range p.nodes {
		if !node.isHealthy || !isNodeAllowed(node, request.Placement) ||
			!isPlatformCompatible(node.platform, request.Platforms) || node.capacity < request.Weight {
			continue
		}
		isPlacementPossible = true
		if node.inFlight+request.Weight > node.capacity {
			continue
		}
		if isNodePreferred(chosen, node, request.Preferred) {
			chosen = node
		}
	}
	if chosen == nil {
		return nil, isPlacementPossible
	}
	chosen.inFlight += request.Weight
	return &Lease{Node: chosen.name, IsLocal: chosen.isLocal, pool: p, weight: request.Weight}, isPlacementPossible
}

// isNodeAllowed reports whether one node is a node this placement may use.
func isNodeAllowed(node *poolNode, placement Placement) bool {
	switch placement {
	case PlacementWorker:
		return !node.isLocal
	case PlacementOrchestrator:
		return node.isLocal
	default:
		return true
	}
}

// isNodePreferred reports whether offered is a better home for this frame
// than whatever was chosen before it.
//
// The caller's own preference comes first: a node named by the task already
// holds what the task is about, and an empty name matches nothing, so the
// order below it is exactly the order every other placement uses.
func isNodePreferred(chosen, offered *poolNode, preferred string) bool {
	if chosen == nil {
		return true
	}
	if (chosen.name == preferred) != (offered.name == preferred) {
		return offered.name == preferred
	}
	if chosen.isLocal != offered.isLocal {
		return chosen.isLocal
	}
	return offered.inFlight < chosen.inFlight
}

// refusePlacement is the failure a task gets when nothing in the pool could
// ever run it: every node it was allowed to run on has stopped answering, or
// none of them satisfied its platforms.
func (p *Pool) refusePlacement(platforms []string, placement Placement) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	unhealthy := make([]string, 0, len(p.nodes))
	for _, node := range p.nodes {
		if !node.isHealthy {
			unhealthy = append(unhealthy, node.name)
		}
	}
	wanted := "any platform"
	if len(platforms) > 0 {
		wanted = strings.Join(platforms, ", ")
	}
	return NewDiagnostic(CodeIntegrity, CategoryIntegrity,
		"no healthy node runs %s and takes a %s placement: %d of %d nodes stopped answering (%s), so this task has nowhere to run",
		wanted, placement, len(unhealthy), len(p.nodes), strings.Join(unhealthy, ", "))
}

// isPlatformCompatible reports whether a node's platform is one a task may run
// on. An empty list is every platform, so a workspace that never says
// otherwise places its work anywhere.
func isPlatformCompatible(platform string, platforms []string) bool {
	if len(platforms) == 0 {
		return true
	}
	for _, wanted := range platforms {
		if wanted == platform {
			return true
		}
	}
	return false
}

// Release gives one slot back, which is what an attempt both parties saw end
// does.
func (l *Lease) Release() {
	l.settle("")
}

// Leak keeps one slot and takes its node out of the pool.
//
// It is what an attempt that stopped answering does. The work may still be
// running on that machine, so the slot is not free, and a node that lost one
// attempt is a node this run will not place anything else on: both halves are
// the same statement, which is why they are one call.
//
// This machine is the one node it can never be said of. A frame placed here
// runs in this process, so a frame that ended is a frame whose goroutine
// returned: there is no unknown process left holding the slot and nothing to
// take out of the pool. A local lease therefore returns its slot however the
// attempt ended, which is what keeps a run that helps its own pool from losing
// the capacity it lent itself, and what keeps the orchestrator available for
// the frames only it may run.
func (l *Lease) Leak(reason string) {
	if l.IsLocal {
		l.settle("")
		return
	}
	l.settle(reason)
}

// settle is the one place a lease ends, so that a lease returned twice cannot
// hand out capacity nobody released.
func (l *Lease) settle(reason string) {
	if l.isSettled {
		return
	}
	l.isSettled = true
	// Return the matching dispatch slot before waking a placement waiter.
	// Otherwise a new lease can be acquired while its signed slot is still
	// marked busy and a ready task would fail despite free node capacity.
	if l.onSettle != nil {
		l.onSettle(reason)
	}
	pool := l.pool
	pool.mu.Lock()
	for _, node := range pool.nodes {
		if node.name != l.Node {
			continue
		}
		if reason != "" {
			node.isHealthy = false
			break
		}
		node.inFlight -= max(l.weight, poolMinimumTaskWeight)
		break
	}
	// Every waiter re-evaluates: a returned slot may be theirs, and a node
	// that has gone is what lets a waiter for that node stop waiting.
	close(pool.changed)
	pool.changed = make(chan struct{})
	pool.mu.Unlock()
	if reason != "" {
		pool.log.Warn().Str("worker", l.Node).Str("reason", reason).
			Str("code", CodeIntegrity).Str("category", CategoryIntegrity).
			Msg("worker marked unhealthy")
		return
	}
	pool.log.Debug().Str("worker", l.Node).Msg("node slot returned")
}
