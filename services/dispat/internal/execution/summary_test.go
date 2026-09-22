// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// What the summary prints, and in what order.
//
// The two properties worth pinning are the ones §28.9 states rather than
// implies: the four outcomes are told apart, and the order is the plan's
// rather than the order the machines happened to finish in. Both are asserted
// against the lines the summary would print, so a change that folded two
// columns together or sorted by arrival fails here rather than in a log
// somebody reads later.

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTheSummaryFollowsThePlanAndNotTheClock: the lines are sorted by the
// plan's package order and, inside one package, by the order a release
// performs its stages. The records are handed in backwards on purpose.
func TestTheSummaryFollowsThePlanAndNotTheClock(t *testing.T) {
	summary := RunSummary{
		Run:   "run-1",
		Local: "here",
		Order: []string{"assets", "ui", "app"},
		Tasks: []TaskRecord{
			{Package: "app", Stage: StagePublish, Task: "app:publish", Node: "build-b"},
			{Package: "assets", Stage: StageBuild, Task: "assets:build", Node: "build-a"},
			{Package: "app", Stage: StageBuild, Task: "app:build", Node: "here"},
			{Package: "ui", Stage: StageBuild, Task: "ui:build", Node: "build-a"},
		},
		Prepared: []PreparedRecord{{Package: "tooling", Task: "tooling:prepare", Node: "build-a",
			Computation: PreparationCompleted, Outputs: PreparationAdmitted,
			Publication: PreparationNone}},
	}

	lines := summary.formatLines()

	printed := make([]string, 0, len(lines))
	for _, line := range lines {
		printed = append(printed, line.Task)
	}
	assert.Equal(t, []string{"assets:build", "ui:build", "app:build", "app:publish",
		"tooling:prepare"}, printed,
		"plan order, then stage order, with a package the plan does not name last")
}

// TestTheSummaryTellsTheFiveOutcomesApart: one run holding a published
// package, a build that failed, a blocked dependent, a prepared provider and a
// publication nobody can account for. Every column is read back from the
// printed lines, because the point of the summary is that none of them can be
// derived from another.
func TestTheSummaryTellsTheFiveOutcomesApart(t *testing.T) {
	var out bytes.Buffer
	log := zerolog.New(&out)
	summary := RunSummary{
		Run:   "run-1",
		Local: "here",
		Order: []string{"assets", "ui", "app", "docs"},
		Tasks: []TaskRecord{
			{Package: "assets", Stage: StageBuild, Task: "assets:build", Node: "build-a",
				Attempt: 1, Computation: ComputationCompleted, Outputs: OutputsAdmitted,
				Publication: PublicationNone, Recording: RecordingNone,
				Queued: 2 * time.Second, Ran: 5 * time.Second, Files: 3, Bytes: 4096},
			{Package: "assets", Stage: StagePublish, Task: "assets:publish", Node: "build-a",
				Attempt: 1, Computation: ComputationCompleted, Outputs: OutputsAdmitted,
				Publication: PublicationSucceeded, Recording: RecordingRecorded},
			{Package: "ui", Stage: StageBuild, Task: "ui:build", Node: "build-b",
				Attempt: 2, Computation: ComputationFailed, Outputs: OutputsNone,
				Publication: PublicationNone, Recording: RecordingNone},
			{Package: "app", Stage: StagePublish, Task: "app:publish", Node: "build-b",
				Attempt: 1, Computation: ComputationUnknown, Outputs: OutputsAdmitted,
				Publication: PublicationUnknown, Recording: RecordingNone},
		},
		Prepared: []PreparedRecord{{Package: "tooling", Task: "tooling:prepare", Node: "here",
			Computation: PreparationCompleted, Outputs: PreparationAdmitted,
			Publication: PreparationNone}},
		Blocked: []BlockedTask{{Package: "docs", Reason: "ui"}},
		Unknown: []unknownPublication{{Task: "app:publish", Attempt: 1, Node: "build-b",
			Repository: "web"}},
		Wall:        time.Minute,
		Invocations: 412,
	}

	summary.Summarize(log)

	lines := decodeSummaryLines(t, out.String())
	require.Len(t, lines, 8, "five tasks, one blocked package, the totals and the metrics")
	assert.Equal(t, "admitted", lines[0]["outputs"])
	assert.Equal(t, "build-a", lines[0]["worker"])
	assert.Equal(t, false, lines[0]["here"])
	assert.Equal(t, float64(4096), lines[0]["bytes"])
	assert.Equal(t, "recorded", lines[1]["recording"])
	assert.Equal(t, float64(2), lines[2]["attempt"], "the attempt that failed names itself")
	assert.Equal(t, "unknown", lines[3]["publication"])
	assert.Equal(t, "unknown", lines[3]["computation"])
	assert.Equal(t, true, lines[4]["here"], "a frame the run placed on itself says so")
	assert.Equal(t, "ui", lines[5]["blockedBy"])

	totals := lines[len(lines)-2]
	assert.Equal(t, "distributed execution summary", totals["message"])
	assert.Equal(t, float64(3), totals["computed"])
	assert.Equal(t, float64(1), totals["published"])
	assert.Equal(t, float64(1), totals["recorded"])
	assert.Equal(t, float64(1), totals["blocked"])
	assert.Equal(t, float64(1), totals["unknown"],
		"a publication nobody can account for is counted as neither published nor failed")

	metrics := lines[len(lines)-1]
	assert.Equal(t, "execution metrics", metrics["message"])
	assert.Equal(t, float64(412), metrics["gitInvocations"])
	assert.Equal(t, float64(3), metrics["files"])
	assert.Equal(t, float64(4), metrics["delegated"])
	assert.Equal(t, float64(1), metrics["local"])
	assert.NotContains(t, metrics, "speedup", "no measurement here is a claim about one")
}

// TestNothingIsPrintedWithoutDistributedWork: a run that delegated nothing has
// one machine and a summary of its own, and pays nothing for a profile it did
// not ask for.
func TestNothingIsPrintedWithoutDistributedWork(t *testing.T) {
	var out bytes.Buffer
	RunSummary{Run: "run-1", Local: "here"}.Summarize(zerolog.New(&out))
	assert.Empty(t, out.String())
}

// decodeSummaryLines reads the printed JSON lines back, which is what a CI job
// filtering on these fields does.
func decodeSummaryLines(t *testing.T, printed string) []map[string]any {
	t.Helper()
	var lines []map[string]any
	for _, raw := range strings.Split(strings.TrimSpace(printed), "\n") {
		if raw == "" {
			continue
		}
		var line map[string]any
		require.NoError(t, json.Unmarshal([]byte(raw), &line), "line: %s", raw)
		lines = append(lines, line)
	}
	return lines
}
