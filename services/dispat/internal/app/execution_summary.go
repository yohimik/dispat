// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

// Assembling what a distributed run says about itself (CCME §28.9).
//
// The transport knows where each task ran and what became of it; the release
// path knows what was recorded and what was never attempted. Neither half is
// the summary on its own, and neither should learn the other's vocabulary, so
// this is the one function that holds both: it reads the run's own results for
// the recording column and the blocked packages, asks the coordinator for
// everything about the tasks, and hands the pair to the summary that prints
// them.
//
// It runs before the existing run summary rather than instead of it. A
// distributed release is still a release, and the line an operator has read
// for years is the one they will look for first; what comes before it is the
// answer to the question only a distributed run raises, which is which machine
// did what and whether anything is still in doubt.

import (
	"time"

	"github.com/yohimik/dispat/services/dispat/internal/execution"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// summarizeExecution prints the per-task outcomes of a distributed run, its
// totals and what it cost, and prints nothing at all for a run that delegated
// nothing.
func (a *App) summarizeExecution(coordinator *execution.Coordinator, pl *plan.Plan,
	results map[string]*release.Result, took time.Duration) {
	if coordinator == nil {
		return
	}
	execution.RunSummary{
		Run:         a.runID,
		Local:       a.sender.Node,
		Order:       pl.Order,
		Tasks:       a.formatRecordedTasks(coordinator, results),
		Prepared:    coordinator.PreparedRecords(),
		Blocked:     formatBlockedTasks(pl, results),
		Unknown:     coordinator.UnknownPublications(),
		Wall:        took,
		Invocations: coordinator.GitInvocations(),
	}.Summarize(a.log)
}

// formatRecordedTasks is the coordinator's own records with the one column it
// cannot know filled in: whether the run wrote the durable record of what was
// published.
//
// The recording is the orchestrator's transaction and the coordinator never
// sees it, which is exactly why §28.9 asks for it separately. A published
// package with no record is the one state the next run cannot reason about, so
// the summary says so on the package's own line rather than leaving it to be
// inferred from a critical error somewhere else in the log.
func (a *App) formatRecordedTasks(coordinator *execution.Coordinator,
	results map[string]*release.Result) []execution.TaskRecord {
	records := coordinator.TaskRecords()
	for index := range records {
		record := &records[index]
		if record.Stage != execution.StagePublish {
			continue
		}
		record.Recording = resolveRecordingOutcome(results[record.Package])
	}
	return records
}

// resolveRecordingOutcome is what the run's own result says about one
// package's durable record.
func resolveRecordingOutcome(result *release.Result) string {
	if result == nil || result.Status != release.StatusPublished {
		return execution.RecordingNone
	}
	if len(result.Critical) > 0 {
		return execution.RecordingFailed
	}
	return execution.RecordingRecorded
}

// formatBlockedTasks are the packages this run planned and never attempted,
// with the reason in the words the skip itself printed.
func formatBlockedTasks(pl *plan.Plan, results map[string]*release.Result) []execution.BlockedTask {
	var blocked []execution.BlockedTask
	for _, name := range pl.Order {
		result := results[name]
		if result == nil || !result.Blocked {
			continue
		}
		blocked = append(blocked, execution.BlockedTask{Package: name, Reason: result.BlockedBy})
	}
	return blocked
}
