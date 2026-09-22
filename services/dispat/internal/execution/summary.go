// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// What a distributed run says about itself when it is over (CCME §28.9).
//
// The last paragraph of §28.9 is the whole specification of this file, and it
// is one sentence worth quoting: a completed task is not synonymous with a
// released package. A run that spread its work over several machines has four
// different things to report about each package and they can disagree in every
// combination. The computation may have completed and its outputs still have
// been refused. The outputs may have been admitted and the publication never
// authorized. The publication may have succeeded and the record have failed,
// which is the one state the next run cannot reason about. And the publication
// may have neither succeeded nor failed, which is a fifth thing and the reason
// the profile exists.
//
// So the summary keeps them apart, one column each, rather than folding them
// into a word like "ok". It also prints where each task ran, including the
// tasks this machine took itself, because the first question anybody asks of a
// distributed run is which machine to go and look at.
//
// The order is the plan's, never the order the answers arrived in (§17.2 by way
// of §28.9). A distributed run finishes its packages in whatever order the
// machines happened to be free, and a reader comparing two runs of the same
// plan must not have to reconstruct the plan from the log.

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

// The words a task's computation is reported with. They are the four states a
// dispatched frame can end in, and `unknown` is not one of them: a computation
// either completed, failed or was stopped, and what is unknown about a
// publication is its effect rather than its execution.
const (
	ComputationCompleted = "completed"
	ComputationFailed    = "failed"
	ComputationCancelled = "cancelled"
	// ComputationUnknown is the computation of an attempt this run cannot
	// establish anything about at all: an authorized publisher that never
	// answered ran its command for some unknown distance.
	ComputationUnknown = "unknown"
)

// The words an output set is reported with: verified and installed where its
// consumers need it, refused for breaking one of the rules of §28.5, or never
// declared at all.
const (
	OutputsAdmitted = "admitted"
	OutputsRejected = "rejected"
	OutputsNone     = "none"
)

// The words a publication is reported with. `none` is the ordinary case for a
// build task and for a prepared provider, and `unknown` is the one outcome
// that makes a run incomplete however well everything else went.
const (
	PublicationSucceeded = "succeeded"
	PublicationFailed    = "failed"
	PublicationUnknown   = "unknown"
	PublicationNone      = "none"
)

// The words a native record is reported with. A record is the orchestrator's
// own transaction, so a delegated run reports it exactly as a local one does:
// what is new is only that the thing being recorded happened elsewhere.
const (
	RecordingRecorded = "recorded"
	RecordingFailed   = "failed"
	RecordingNone     = "none"
)

// TaskRecord is what one task of a distributed run came to, in the vocabulary
// §28.9 requires the summary to distinguish.
//
// It is one flat value per task rather than a tree per package because that is
// what a reader scans: one line per task, in plan order, with the same columns
// on every line whatever kind of work it was.
type TaskRecord struct {
	// Package and Stage are the work as the plan names it, and Task is the
	// two joined, which is the name every message of the attempt carried.
	Package string
	Stage   string
	Task    string
	// Node is where the frame ran, including this machine when the run placed
	// the frame on itself: "where" is the first question a distributed run is
	// asked, and leaving this machine out would make the answer a guess.
	Node    string
	Attempt int
	// The four outcomes §28.9 requires to be told apart, plus the identities
	// of the work they belong to.
	Computation string
	Outputs     string
	Publication string
	Recording   string
	// Queued is how long the assignment waited before a node claimed it, and
	// Ran is how long the work took once one had. They are separate because
	// they are charged differently: queue time is somebody else's run, and the
	// task deadline measures only the second of them.
	Queued time.Duration
	Ran    time.Duration
	// Files and Bytes are what this task's admitted output set consists of,
	// zero for a task that declared none.
	Files int
	Bytes int64
}

// BlockedTask is one package this run did not attempt because something it
// depended on did not finish: which package, and the reason in the words the
// skip itself printed.
//
// It is in the summary because §28.9 requires blocked dependents to be
// distinguished, and because a distributed run makes them common: a node that
// went away blocks everything downstream of the package it was building, and a
// summary that listed only what ran would leave a reader counting absences.
type BlockedTask struct {
	Package string
	Reason  string
}

// RunSummary is everything one distributed run has to say about itself: what
// each task came to, what was prepared without being released, what was never
// attempted, and what the whole thing cost.
type RunSummary struct {
	Run string
	// Local is what the machine that started the run calls itself, so that a
	// line can say a frame ran here rather than naming this node as though it
	// were somebody else (gate 7b: `node` is the writer, `worker` is another).
	Local string
	// Order is the plan's own package order, which the lines are sorted by:
	// output order is the semantic order and never the arrival order.
	Order []string
	Tasks []TaskRecord
	// Prepared are the providers this run built without releasing them. They
	// are a task of the run like any other and are reported as one, which is
	// the only place a reader learns that a package's `dist` was rebuilt
	// although no version of it was published.
	Prepared []PreparedRecord
	Blocked  []BlockedTask
	// Unknown are the authorized publications whose outcome could not be
	// established, with the identities an operator needs to settle them.
	Unknown []unknownPublication
	// Wall is the time the distributed part of the run took, and Invocations
	// is how many git subprocesses it cost. Both are measured rather than
	// derived, and neither is a claim about anything: §28.7 forbids calling a
	// number a speed-up without the comparison it demands.
	Wall        time.Duration
	Invocations uint64
}

// Summarize prints one line per task in plan order, the totals that tell the
// outcomes apart, and what the run cost.
//
// Nothing is printed for a run that delegated nothing: a release with no
// worker links has one machine, one order of events and a summary of its own,
// and adding empty columns to it would be charging every user for a profile
// they did not ask for.
func (s RunSummary) Summarize(log zerolog.Logger) {
	lines := s.formatLines()
	if len(lines) == 0 {
		return
	}
	for _, line := range lines {
		event := log.Info().Str("run", s.Run).Str("package", line.Package).
			Str("stage", line.Stage).Str("task", line.Task).Int("attempt", line.Attempt).
			Str("computation", line.Computation).Str("outputs", line.Outputs).
			Str("publication", line.Publication).Str("recording", line.Recording)
		isHere := line.Node == "" || line.Node == s.Local
		if !isHere {
			event = event.Str("worker", line.Node)
		}
		if line.Queued > 0 {
			event = event.Dur("queued", line.Queued)
		}
		if line.Ran > 0 {
			event = event.Dur("took", line.Ran)
		}
		if line.Files > 0 {
			event = event.Int("files", line.Files).Int64("bytes", line.Bytes)
		}
		event.Bool("here", isHere).Msg("task outcome")
	}
	for _, blocked := range s.orderedBlocked() {
		log.Info().Str("run", s.Run).Str("package", blocked.Package).
			Str("blockedBy", blocked.Reason).Str("computation", "none").
			Str("outputs", OutputsNone).Str("publication", PublicationNone).
			Str("recording", RecordingNone).Msg("task outcome")
	}
	s.reportTotals(log, lines)
	s.reportMetrics(log, lines)
}

// summaryLine is one printed row: a task record with the ordering already
// applied.
type summaryLine = TaskRecord

// formatLines is every task of the run in the order it is printed: the plan's
// package order, and within one package the stage order a release performs.
func (s RunSummary) formatLines() []summaryLine {
	lines := make([]summaryLine, 0, len(s.Tasks)+len(s.Prepared))
	lines = append(lines, s.Tasks...)
	for _, prepared := range s.Prepared {
		// A prepared provider completed its computation and had its outputs
		// admitted, and nothing of it was published or recorded: that is the
		// whole of what a preparation is, and saying it in the same columns is
		// what makes it comparable with the rest.
		lines = append(lines, summaryLine{
			Package: prepared.Package, Stage: KindPrepare, Task: prepared.Task,
			Node: prepared.Node, Attempt: 1, Computation: prepared.Computation,
			Outputs: prepared.Outputs, Publication: prepared.Publication,
			Recording: RecordingNone,
		})
	}
	order := map[string]int{}
	for index, name := range s.Order {
		order[name] = index
	}
	sort.SliceStable(lines, func(i, j int) bool {
		if lines[i].Package != lines[j].Package {
			return resolveOrderIndex(order, lines[i].Package) < resolveOrderIndex(order, lines[j].Package)
		}
		return resolveStageRank(lines[i].Stage) < resolveStageRank(lines[j].Stage)
	})
	return lines
}

// resolveOrderIndex is where one package sits in the plan's order, and past
// the end for a package the plan does not name, which is what a prepared
// provider is.
func resolveOrderIndex(order map[string]int, packageName string) int {
	if index, isPlanned := order[packageName]; isPlanned {
		return index
	}
	return len(order)
}

// resolveStageRank is the order a release performs one package's stages in, so
// that a package's preparation, build and publication read downwards.
func resolveStageRank(stage string) int {
	switch stage {
	case KindPrepare:
		return 0
	case KindBuild:
		return 1
	default:
		return 2
	}
}

// orderedBlocked is the packages nothing was attempted for, in plan order.
func (s RunSummary) orderedBlocked() []BlockedTask {
	order := map[string]int{}
	for index, name := range s.Order {
		order[name] = index
	}
	blocked := append([]BlockedTask(nil), s.Blocked...)
	sort.SliceStable(blocked, func(i, j int) bool {
		return resolveOrderIndex(order, blocked[i].Package) < resolveOrderIndex(order, blocked[j].Package)
	})
	return blocked
}

// reportTotals is the line §28.9 asks for in so many words: completed
// computation, admitted outputs, successful publication, durable native
// recording, blocked dependents and unknown external outcomes, each counted
// separately because a completed task is not a released package.
func (s RunSummary) reportTotals(log zerolog.Logger, lines []summaryLine) {
	computed, admitted, published, recorded, unknown := 0, 0, 0, 0, 0
	for _, line := range lines {
		if line.Computation == ComputationCompleted {
			computed++
		}
		if line.Outputs == OutputsAdmitted {
			admitted++
		}
		switch line.Publication {
		case PublicationSucceeded:
			published++
		case PublicationUnknown:
			unknown++
		}
		if line.Recording == RecordingRecorded {
			recorded++
		}
	}
	log.Info().Str("run", s.Run).Int("tasks", len(lines)).Int("computed", computed).
		Int("admitted", admitted).Int("published", published).Int("recorded", recorded).
		Int("blocked", len(s.Blocked)).Int("unknown", unknown).
		Msg("distributed execution summary")
}

// reportMetrics is what the run cost, and nothing about what it saved.
//
// §28.7 is explicit that no performance claim may be made without a pinned
// fixture and a comparison, so this line carries measurements and no
// comparison at all: the phases that were actually timed, the work that was
// actually transferred, and the git invocations it took. A field nothing
// measured is left out rather than filled with a zero a reader would read as a
// measurement.
func (s RunSummary) reportMetrics(log zerolog.Logger, lines []summaryLine) {
	queued, ran := time.Duration(0), time.Duration(0)
	files, bytes, delegated := 0, int64(0), 0
	executions := map[string]int{}
	for _, line := range lines {
		queued += line.Queued
		ran += line.Ran
		files += line.Files
		bytes += line.Bytes
		executions[line.Package]++
		if line.Node != "" && line.Node != s.Local {
			delegated++
		}
	}
	event := log.Info().Str("run", s.Run).Dur("wall", s.Wall).Dur("queue", queued).
		Dur("execution", ran).Int("files", files).Int64("bytes", bytes).
		Int("delegated", delegated).Int("local", len(lines)-delegated).
		Int("packages", len(executions))
	if s.Invocations > 0 {
		event = event.Uint64("gitInvocations", s.Invocations)
	}
	event.Msg("execution metrics")
}

// taskTiming is what one attempt spent waiting and what it spent working.
type taskTiming struct {
	queued time.Duration
	ran    time.Duration
}

// rememberTiming records how long one attempt queued and how long it ran.
//
// The two are kept apart because the run charges them differently and because
// the difference is the whole of what a distributed run can be tuned by: a
// queue is a pool that is too small, and an execution is a machine that is too
// slow, and a single duration would hide which of the two a reader is looking
// at.
func (c *Coordinator) rememberTiming(task string, queued, ran time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.timings == nil {
		c.timings = map[string]taskTiming{}
	}
	c.timings[task] = taskTiming{queued: queued, ran: ran}
}

// rememberTaskOutcome records what one placed task came to, which is the one
// place every kind of work passes through: a build, a preparation and a
// publication are all placed, and all three are reported in the same columns.
func (c *Coordinator) rememberTaskOutcome(record TaskRecord) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if timing, isTimed := c.timings[record.Task]; isTimed {
		record.Queued, record.Ran = timing.queued, timing.ran
	}
	if admitted := c.outputs.find(record.Package); admitted != nil && admitted.manifest != nil {
		record.Outputs = OutputsAdmitted
		record.Files, record.Bytes = admitted.manifest.Files, admitted.manifest.Bytes
	}
	c.taskRecords = append(c.taskRecords, record)
}

// TaskRecords are what every task this run placed came to, in the order the
// run decided them. The summary sorts them; this answers them.
func (c *Coordinator) TaskRecords() []TaskRecord {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]TaskRecord(nil), c.taskRecords...)
}

// resolveTaskOutcome is what one attempt's failure, or the absence of one,
// says about the two things a task can be reported against.
//
// The order matters and it is the order of what a reader has to know first. An
// authorized publication nobody can account for is neither a success nor a
// failure and is reported as neither; a run that was interrupted cancelled its
// work rather than failing it, which is what keeps a package out of the failure
// hooks; and everything else is the ordinary pair.
func (c *Coordinator) resolveTaskOutcome(task, stage string, err error) (computation, publication string) {
	if c.isOutcomeUnknown(task) {
		return ComputationUnknown, PublicationUnknown
	}
	if err == nil {
		return ComputationCompleted, resolvePublicationOf(stage, PublicationSucceeded)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return ComputationCancelled, resolvePublicationOf(stage, PublicationFailed)
	}
	return ComputationFailed, resolvePublicationOf(stage, PublicationFailed)
}

// resolvePublicationOf is what a task of one stage says about publication: a
// build and a preparation publish nothing at all, whatever became of them.
func resolvePublicationOf(stage, outcome string) string {
	if stage != StagePublish {
		return PublicationNone
	}
	return outcome
}

// isOutcomeUnknown reports whether this run recorded an unknown publication
// for one task.
func (c *Coordinator) isOutcomeUnknown(task string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, unknown := range c.unknownPublications {
		if unknown.Task == task {
			return true
		}
	}
	return false
}

// GitInvocations is how many git subprocesses this process has started, which
// is the one cost of the transport that can be measured without instrumenting
// it.
//
// It is answered here rather than read from gitx by the caller so that the
// summary's fields all come from one place, and it is the process's total
// rather than the run's: a release is one process, and a counter scoped to the
// coordinator would be a second counter disagreeing with the first.
func (c *Coordinator) GitInvocations() uint64 { return gitx.GitInvocations() }
