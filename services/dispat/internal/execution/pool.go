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
	"sort"
	"strings"
	"sync"

	"github.com/rs/zerolog"
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
	name      string
	platform  string
	capacity  int
	inFlight  int
	isHealthy bool
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

	pool *Pool
	// isSettled keeps a slot from being returned twice, which would hand out
	// capacity the node never got back.
	isSettled bool
}

// NewPool builds the pool from the reports preflight collected, one entry per
// configured link.
//
// The capacity is the node's own, because the node is the only party that
// knows what it can run: an orchestrator that decided would be deciding about
// a machine it cannot see.
func NewPool(links []Link, reports []*NodeReport, log zerolog.Logger) *Pool {
	nodes := make([]*poolNode, 0, len(links))
	for index, link := range links {
		report := reports[index]
		nodes = append(nodes, &poolNode{
			name:      link.Name,
			platform:  report.OS + "/" + report.Arch,
			capacity:  report.Capacity,
			isHealthy: true,
		})
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].name < nodes[j].name })
	return &Pool{log: log, nodes: nodes, changed: make(chan struct{})}
}

// Acquire takes one slot on a node this task may run on, waiting for one when
// every compatible node is busy.
//
// The wait is bounded by two things and never by hope. The caller's context
// ends it, and so does the pool running out of healthy compatible nodes: a
// task waiting for a machine that no longer exists is a task that will wait
// for ever, so it is failed at once with the platform it needed and the nodes
// that could have run it named.
func (p *Pool) Acquire(ctx context.Context, platforms []string) (*Lease, error) {
	for {
		p.mu.Lock()
		lease, isPlacementPossible := p.takeSlot(platforms)
		changed := p.changed
		p.mu.Unlock()
		if lease != nil {
			p.log.Debug().Str("worker", lease.Node).Msg("node slot acquired")
			return lease, nil
		}
		if !isPlacementPossible {
			return nil, p.refusePlacement(platforms)
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("waiting for a node to run this task on: %w", ctx.Err())
		case <-changed:
		}
	}
}

// takeSlot answers the slot this task gets, and whether waiting for one could
// ever help. It runs under the lock.
//
// The preference is deterministic on purpose: the least loaded compatible
// node, and among equals the first by name. A run placing the same plan on the
// same pool twice places it the same way, which is what makes a distributed
// run's behaviour reproducible enough to debug.
func (p *Pool) takeSlot(platforms []string) (*Lease, bool) {
	var chosen *poolNode
	isPlacementPossible := false
	for _, node := range p.nodes {
		if !node.isHealthy || !isPlatformCompatible(node.platform, platforms) {
			continue
		}
		isPlacementPossible = true
		if node.inFlight >= node.capacity {
			continue
		}
		if chosen == nil || node.inFlight < chosen.inFlight {
			chosen = node
		}
	}
	if chosen == nil {
		return nil, isPlacementPossible
	}
	chosen.inFlight++
	return &Lease{Node: chosen.name, pool: p}, isPlacementPossible
}

// refusePlacement is the failure a task gets when nothing in the pool could
// ever run it: every node that satisfied its platforms has stopped answering.
func (p *Pool) refusePlacement(platforms []string) error {
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
		"no healthy worker node runs %s: %d of %d nodes stopped answering (%s), so this task has nowhere to run",
		wanted, len(unhealthy), len(p.nodes), strings.Join(unhealthy, ", "))
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
	l.settle(false)
}

// Leak keeps one slot and takes its node out of the pool.
//
// It is what an attempt that stopped answering does. The work may still be
// running on that machine, so the slot is not free, and a node that lost one
// attempt is a node this run will not place anything else on: both halves are
// the same statement, which is why they are one call.
func (l *Lease) Leak() {
	l.settle(true)
}

// settle is the one place a lease ends, so that a lease returned twice cannot
// hand out capacity nobody released.
func (l *Lease) settle(isLeaked bool) {
	if l.isSettled {
		return
	}
	l.isSettled = true
	pool := l.pool
	pool.mu.Lock()
	for _, node := range pool.nodes {
		if node.name != l.Node {
			continue
		}
		if isLeaked {
			node.isHealthy = false
			break
		}
		node.inFlight--
		break
	}
	// Every waiter re-evaluates: a returned slot may be theirs, and a node
	// that has gone is what lets a waiter for that node stop waiting.
	close(pool.changed)
	pool.changed = make(chan struct{})
	pool.mu.Unlock()
	if isLeaked {
		pool.log.Warn().Str("worker", l.Node).Str("code", CodeIntegrity).Str("category", CategoryIntegrity).
			Msg("the node stopped answering and its capacity is held")
		return
	}
	pool.log.Debug().Str("worker", l.Node).Msg("node slot returned")
}
