package graph

import (
	"cmp"
	"context"
	"fmt"
	"slices"
)

// Scheduler is the incremental core of Kahn's algorithm: a dependency graph
// consumed node by node while something else — a worker pool, a sort loop —
// decides when and in what order the ready nodes actually run.
//
// It is generic because its two users track different node types over the same
// bookkeeping: the planner sorts package names, the executor schedules
// (package, stage) task nodes. Both need exactly this and nothing more —
// nodes, edges, the ready frontier, and "this one finished, what became
// ready?" — so this is the whole API.
//
// Ready and Done return nodes in insertion order (of nodes and edges
// respectively), so a caller that wants a specific order — the sorter's
// alphabetical tie-break, say — imposes it on top, and a caller that does not
// still gets deterministic behaviour for free.
type Scheduler[N comparable] struct {
	nodes      []N
	indeg      map[N]int
	dependents map[N][]N
	handed     map[N]bool // already returned by Ready or Done, or completed in place
	// passThroughs are the nodes nothing runs, in registration order; see
	// AddPassThrough.
	passThroughs  []N
	isPassThrough map[N]bool
	// edgeSequences numbers each node's dependent edges in the order they
	// were stated, and is kept only by a scheduler with pass-through nodes:
	// it is how Done hands out what a pass-through node readied in the
	// place of the edge that readied it.
	edgeSequences map[N][]int
	edgeCount     int
}

// NewScheduler returns an empty scheduler.
func NewScheduler[N comparable]() *Scheduler[N] {
	return &Scheduler[N]{
		indeg:      make(map[N]int),
		dependents: make(map[N][]N),
		handed:     make(map[N]bool),
	}
}

// Add registers a node; adding one twice is a no-op. Nodes named by AddEdge
// are registered implicitly, so Add is only needed for isolated nodes.
func (s *Scheduler[N]) Add(n N) {
	if _, ok := s.indeg[n]; ok {
		return
	}
	s.indeg[n] = 0
	s.nodes = append(s.nodes, n)
}

// AddPassThrough registers a node that stands for an ordering rather than for
// work. It is never handed out and never counted: it completes in place the
// moment its last dependency does, so its dependents wait for everything it
// waits for. One between j dependencies and k dependents states with j + k
// edges what j * k edges would state directly.
//
// Pass-through nodes are added before any edge, because only then can every
// edge be numbered for Done's order, a cost a graph without them never pays.
func (s *Scheduler[N]) AddPassThrough(n N) {
	if s.edgeCount > 0 {
		panic("graph: a pass-through node is added before the edges")
	}
	if _, ok := s.indeg[n]; ok {
		return
	}
	if s.isPassThrough == nil {
		s.isPassThrough = make(map[N]bool)
		s.edgeSequences = make(map[N][]int)
	}
	s.indeg[n] = 0
	s.isPassThrough[n] = true
	s.passThroughs = append(s.passThroughs, n)
}

// AddEdge records that after may only run once before has completed. Both
// nodes are registered as a side effect.
func (s *Scheduler[N]) AddEdge(before, after N) {
	s.Add(before)
	s.Add(after)
	s.indeg[after]++
	s.dependents[before] = append(s.dependents[before], after)
	if s.edgeSequences != nil {
		s.edgeSequences[before] = append(s.edgeSequences[before], s.edgeCount)
	}
	s.edgeCount++
}

// Len is the total number of nodes, pass-through nodes not counted.
func (s *Scheduler[N]) Len() int { return len(s.nodes) }

// EdgeLen is the number of edges stated, pass-through nodes' own included,
// which is what the graph costs to hold and to drain.
func (s *Scheduler[N]) EdgeLen() int { return s.edgeCount }

// CycleNodes names exactly the vertices on directed cycles without consuming
// the scheduler. Pass-through vertices are included because a cycle through
// a zero-duration relay still prevents its dependent tasks from starting.
func (s *Scheduler[N]) CycleNodes() []N {
	all := append(slices.Clone(s.passThroughs), s.nodes...)
	reverse := make(map[N][]N, len(all))
	for from, next := range s.dependents {
		for _, to := range next {
			reverse[to] = append(reverse[to], from)
		}
	}
	type frame struct {
		node  N
		index int
	}
	seen := make(map[N]bool, len(all))
	order := make([]N, 0, len(all))
	for _, root := range all {
		if seen[root] {
			continue
		}
		seen[root] = true
		stack := []frame{{node: root}}
		for len(stack) > 0 {
			top := &stack[len(stack)-1]
			if top.index < len(s.dependents[top.node]) {
				to := s.dependents[top.node][top.index]
				top.index++
				if !seen[to] {
					seen[to] = true
					stack = append(stack, frame{node: to})
				}
				continue
			}
			order = append(order, top.node)
			stack = stack[:len(stack)-1]
		}
	}
	seen = make(map[N]bool, len(all))
	onCycle := make(map[N]bool)
	for i := len(order) - 1; i >= 0; i-- {
		root := order[i]
		if seen[root] {
			continue
		}
		seen[root] = true
		component := []N{root}
		stack := []N{root}
		for len(stack) > 0 {
			n := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			for _, from := range reverse[n] {
				if !seen[from] {
					seen[from] = true
					component = append(component, from)
					stack = append(stack, from)
				}
			}
		}
		if len(component) > 1 {
			for _, n := range component {
				onCycle[n] = true
			}
			continue
		}
		for _, to := range s.dependents[root] {
			if to == root {
				onCycle[root] = true
				break
			}
		}
	}
	var cyclic []N
	for _, n := range all {
		if onCycle[n] {
			cyclic = append(cyclic, n)
		}
	}
	return cyclic
}

// Ready returns the nodes with no pending dependencies that have not been
// handed out yet, in insertion order. The initial frontier, on a fresh
// scheduler. Pass-through nodes with nothing to wait for complete first, so
// what they held back is part of the frontier at its own insertion place.
func (s *Scheduler[N]) Ready() []N {
	for _, n := range s.passThroughs {
		if s.indeg[n] == 0 && !s.handed[n] {
			s.handed[n] = true
			s.completeOnFrontier(n)
		}
	}
	var out []N
	for _, n := range s.nodes {
		if s.indeg[n] == 0 && !s.handed[n] {
			s.handed[n] = true
			out = append(out, n)
		}
	}
	return out
}

// completeOnFrontier completes a pass-through node before anything ran. The
// pass-through nodes it readies complete with it; the nodes that run are left
// for Ready's scan, which keeps them in insertion order.
func (s *Scheduler[N]) completeOnFrontier(n N) {
	for _, dep := range s.dependents[n] {
		s.indeg[dep]--
		if s.indeg[dep] == 0 && s.isPassThrough[dep] && !s.handed[dep] {
			s.handed[dep] = true
			s.completeOnFrontier(dep)
		}
	}
}

// Done marks a node complete and returns the nodes that just became ready, in
// edge insertion order. A pass-through node it readies completes in place,
// and each node that readies hands out at the place of the edge that readied
// it. A caller that states a pass-through node's edges to its dependents where
// the direct edges would have stood gets the direct graph's order.
func (s *Scheduler[N]) Done(n N) []N {
	if s.isPassThrough == nil {
		return s.completeDirect(n)
	}
	readied := s.completeThrough(n, nil)
	if len(readied) == 0 {
		return nil
	}
	slices.SortFunc(readied, func(a, b readiedNode[N]) int { return cmp.Compare(a.sequence, b.sequence) })
	out := make([]N, len(readied))
	for i, r := range readied {
		out[i] = r.node
	}
	return out
}

// completeDirect is Done on a graph with no pass-through node, whose edges
// already come in insertion order.
func (s *Scheduler[N]) completeDirect(n N) []N {
	var out []N
	for _, dep := range s.dependents[n] {
		s.indeg[dep]--
		if s.indeg[dep] == 0 && !s.handed[dep] {
			s.handed[dep] = true
			out = append(out, dep)
		}
	}
	return out
}

// readiedNode is a node Done hands out with the number of the edge that
// readied it.
type readiedNode[N comparable] struct {
	node     N
	sequence int
}

// completeThrough completes n and appends what it readied, completing in
// place every pass-through node it readies. In place, rather than after n's
// own edges, so that a node readied both through a pass-through node and by a
// later edge of n's is readied by that later edge, as it is when every
// ordering is stated directly.
func (s *Scheduler[N]) completeThrough(n N, readied []readiedNode[N]) []readiedNode[N] {
	sequences := s.edgeSequences[n]
	for i, dep := range s.dependents[n] {
		s.indeg[dep]--
		if s.indeg[dep] != 0 || s.handed[dep] {
			continue
		}
		s.handed[dep] = true
		if s.isPassThrough[dep] {
			readied = s.completeThrough(dep, readied)
			continue
		}
		readied = append(readied, readiedNode[N]{node: dep, sequence: sequences[i]})
	}
	return readied
}

// Blocked returns the nodes that still have pending dependencies, in insertion
// order. After every handed-out node is Done, a non-empty Blocked is exactly
// the members of dependency cycles — which is how TopoSort names them.
func (s *Scheduler[N]) Blocked() []N {
	var out []N
	for _, n := range s.nodes {
		if s.indeg[n] > 0 {
			out = append(out, n)
		}
	}
	return out
}

// Drain consumes the scheduler with bounded parallelism: every node runs
// exactly once in a goroutine of its own, edges respected, with at most
// budget(class(n)) slots of one class occupied at a time (a budget below 1
// is treated as 1). Classes exist because budgets can be independent — the
// executor caps its build and publish stages separately — and each class
// keeps its own ready queue, so a stalled class never blocks another class's
// budget. Drain returns nil once every node has completed.
//
// weight is how many of its class's slots a node occupies, clamped to
// [1, budget] so a node can always launch on an idle class — a package
// weighted past the budget simply runs alone. It must be pure: it is read
// again on completion to release the slots. A node that does not fit waits
// at the head of its queue and nothing behind it overtakes; the head-of-line
// discipline is what makes a heavy node's wait finite instead of starvable.
//
// Two things end it early, both only after every in-flight node has been
// awaited, so no goroutine outlives the call. A cancelled ctx stops new
// launches: nodes not yet launched never run at all and ctx.Err() is
// returned — the caller decides what a never-ran node means. And a graph
// whose remaining nodes can never become ready (a dependency cycle) is
// reported as an error instead of the coordinator blocking forever; callers
// validate their graphs first (the planner rejects cycles as E200), so
// hitting it is a programming error, but a diagnosed one.
//
// This is the one scheduling pump in the program: the executor drains
// (package, stage) tasks over two budgets, the run command drains package
// names over one. run is responsible for its own synchronisation; Drain
// guarantees only the ordering the edges state, plus the happens-before
// between a node's completion and its dependents' starts that the completion
// channel provides.
func Drain[N comparable, C comparable](ctx context.Context, s *Scheduler[N], class func(N) C, budget func(C) int, weight func(N) int, run func(N)) error {
	ready := make(map[C][]N)
	var classes []C // first-seen order, so launch scanning is deterministic
	push := func(n N) {
		c := class(n)
		if _, ok := ready[c]; !ok {
			classes = append(classes, c)
		}
		ready[c] = append(ready[c], n)
	}
	for _, n := range s.Ready() {
		push(n)
	}
	cost := func(n N, limit int) int { return min(max(1, weight(n)), limit) }

	inFlight := make(map[C]int)
	inFlightTotal := 0
	done := make(chan N)
	finished, total := 0, s.Len()
	for finished < total {
		if ctx.Err() == nil {
			for _, c := range classes {
				queue := ready[c]
				limit := max(1, budget(c))
				for len(queue) > 0 {
					n := queue[0]
					w := cost(n, limit)
					if inFlight[c]+w > limit {
						break // head of line waits; nothing overtakes it
					}
					queue = queue[1:]
					inFlight[c] += w
					inFlightTotal++
					go func(n N) {
						run(n)
						done <- n
					}(n)
				}
				ready[c] = queue
			}
		}
		if inFlightTotal == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
			// Wrapped in the sentinel TopoSort's CycleError also carries, so
			// a caller can recognise "the graph has no order" with errors.Is
			// rather than by reading the sentence. Not CycleError itself:
			// Drain is generic over the node type and that error names
			// strings.
			return fmt.Errorf("graph: %d node(s) can never become ready: %w", len(s.Blocked()), ErrCycle)
		}
		n := <-done
		c := class(n)
		inFlight[c] -= cost(n, max(1, budget(c)))
		inFlightTotal--
		finished++
		for _, next := range s.Done(n) {
			push(next)
		}
	}
	return nil
}
