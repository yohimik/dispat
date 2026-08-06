package graph

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
	handed     map[N]bool // already returned by Ready or Done
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

// AddEdge records that after may only run once before has completed. Both
// nodes are registered as a side effect.
func (s *Scheduler[N]) AddEdge(before, after N) {
	s.Add(before)
	s.Add(after)
	s.indeg[after]++
	s.dependents[before] = append(s.dependents[before], after)
}

// Len is the total number of nodes.
func (s *Scheduler[N]) Len() int { return len(s.nodes) }

// Ready returns the nodes with no pending dependencies that have not been
// handed out yet, in insertion order. The initial frontier, on a fresh
// scheduler.
func (s *Scheduler[N]) Ready() []N {
	var out []N
	for _, n := range s.nodes {
		if s.indeg[n] == 0 && !s.handed[n] {
			s.handed[n] = true
			out = append(out, n)
		}
	}
	return out
}

// Done marks a node complete and returns the nodes that just became ready, in
// edge insertion order.
func (s *Scheduler[N]) Done(n N) []N {
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
// budget(class(n)) nodes of one class in flight at a time (a budget below 1
// is treated as 1). Classes exist because budgets can be independent — the
// executor caps its build and publish stages separately — and each class
// keeps its own ready queue, so a stalled class never blocks another class's
// budget. Drain returns when every node has completed.
//
// This is the one scheduling pump in the program: the executor drains
// (package, stage) tasks over two budgets, the run command drains package
// names over one. run is responsible for its own synchronisation; Drain
// guarantees only the ordering the edges state, plus the happens-before
// between a node's completion and its dependents' starts that the completion
// channel provides.
func Drain[N comparable, C comparable](s *Scheduler[N], class func(N) C, budget func(C) int, run func(N)) {
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

	inFlight := make(map[C]int)
	done := make(chan N)
	finished, total := 0, s.Len()
	for finished < total {
		for _, c := range classes {
			queue := ready[c]
			limit := max(1, budget(c))
			for len(queue) > 0 && inFlight[c] < limit {
				n := queue[len(queue)-1]
				queue = queue[:len(queue)-1]
				inFlight[c]++
				go func(n N) {
					run(n)
					done <- n
				}(n)
			}
			ready[c] = queue
		}
		n := <-done
		inFlight[class(n)]--
		finished++
		for _, next := range s.Done(n) {
			push(next)
		}
	}
}
