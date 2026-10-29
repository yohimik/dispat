// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"bufio"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const packBenchmarkTraceLineBytes = 1 << 20

type packBenchmarkMetrics struct {
	Git           float64 `json:"gitInvocations"`
	AllPacks      float64 `json:"allPackObjects"`
	ArtifactPacks float64 `json:"artifactPackObjects"`
	Written       float64 `json:"artifactWrittenObjects"`
	Reused        float64 `json:"artifactReusedObjects"`
	ReusedDeltas  float64 `json:"artifactReusedDeltas"`
	LocalIndexes  float64 `json:"workerLocalIndexPacks"`
	PackBytes     float64 `json:"preparedResultPackBytes"`
}

func (m *packBenchmarkMetrics) add(other packBenchmarkMetrics) {
	m.Git += other.Git
	m.AllPacks += other.AllPacks
	m.ArtifactPacks += other.ArtifactPacks
	m.Written += other.Written
	m.Reused += other.Reused
	m.ReusedDeltas += other.ReusedDeltas
	m.LocalIndexes += other.LocalIndexes
	m.PackBytes += other.PackBytes
}

func (m packBenchmarkMetrics) report(b *testing.B, calls int) {
	b.Helper()
	require.Positive(b, calls)
	for unit, value := range map[string]float64{"git/op": m.Git, "all-pack-objects/op": m.AllPacks,
		"artifact-pack-objects/op": m.ArtifactPacks, "artifact-written-objects/op": m.Written,
		"artifact-reused-objects/op": m.Reused, "artifact-reused-deltas/op": m.ReusedDeltas,
		"local-index-pack/op": m.LocalIndexes, "result-pack-bytes/op": m.PackBytes} {
		b.ReportMetric(value/float64(calls), unit)
	}
}

type packBenchmarkTraceEvent struct {
	Event    string          `json:"event"`
	SID      string          `json:"sid"`
	Name     string          `json:"name"`
	Argv     []string        `json:"argv"`
	Category string          `json:"category"`
	Key      string          `json:"key"`
	Value    json.RawMessage `json:"value"`
}

// Trace2's written/reused counts are object counts, never byte counts. Identify
// the artifact push by its exact immutable outcome ref; metadata push children
// remain counted separately. Parent invocation counts are the production Go
// counter, while this parser includes native Git's pack-objects children.
func readPackBenchmarkTrace(t testing.TB, path, workerStore, outcomeBranch string) packBenchmarkMetrics {
	t.Helper()
	file, err := os.Open(path)
	require.NoError(t, err)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), packBenchmarkTraceLineBytes)
	var events []packBenchmarkTraceEvent
	for scanner.Scan() {
		var event packBenchmarkTraceEvent
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &event))
		events = append(events, event)
	}
	require.NoError(t, scanner.Err())
	require.NoError(t, file.Close())
	args := map[string][]string{}
	names := map[string]string{}
	for _, event := range events {
		if event.Event == "start" {
			args[event.SID] = event.Argv
		}
		if event.Event == "cmd_name" {
			names[event.SID] = event.Name
		}
	}
	pushes := map[string]bool{}
	for sid, argv := range args {
		if names[sid] != "push" || packBenchmarkGitDirectory(argv) != workerStore {
			continue
		}
		for _, arg := range argv {
			if strings.HasSuffix(arg, ":refs/heads/"+outcomeBranch) && !strings.HasPrefix(arg, "--") {
				pushes[sid] = true
			}
		}
	}
	require.Len(t, pushes, 1, "the actual artifact outcome is published exactly once")
	artifact := map[string]bool{}
	metrics := packBenchmarkMetrics{}
	for sid, name := range names {
		if name == "index-pack" && packBenchmarkGitDirectory(args[sid]) == workerStore {
			metrics.LocalIndexes++
		}
		if name != "pack-objects" {
			continue
		}
		metrics.AllPacks++
		selected := packBenchmarkGitDirectory(args[sid]) == workerStore
		for parent := range pushes {
			selected = selected || strings.HasPrefix(sid, parent+"/")
		}
		if selected {
			artifact[sid] = true
			metrics.ArtifactPacks++
		}
	}
	require.NotEmpty(t, artifact, "artifact packing counters must be observed, never inferred")
	counters := map[string]map[string]float64{}
	for _, event := range events {
		if !artifact[event.SID] || event.Event != "data" || event.Category != "pack-objects" {
			continue
		}
		if event.Key != "written" && event.Key != "reused" && event.Key != "reused/delta" {
			continue
		}
		var text string
		require.NoError(t, json.Unmarshal(event.Value, &text))
		value, err := strconv.ParseFloat(text, 64)
		require.NoError(t, err)
		require.GreaterOrEqual(t, value, float64(0))
		if counters[event.SID] == nil {
			counters[event.SID] = map[string]float64{}
		}
		_, duplicate := counters[event.SID][event.Key]
		require.False(t, duplicate, "one terminal counter per packing process")
		counters[event.SID][event.Key] = value
	}
	for sid := range artifact {
		for _, key := range []string{"written", "reused", "reused/delta"} {
			require.Contains(t, counters[sid], key, "missing counter is unavailable, not zero")
		}
		metrics.Written += counters[sid]["written"]
		metrics.Reused += counters[sid]["reused"]
		metrics.ReusedDeltas += counters[sid]["reused/delta"]
	}
	return metrics
}

func packBenchmarkGitDirectory(args []string) string {
	directory := ""
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-C" {
			directory = args[i+1]
			i++
		}
	}
	return directory
}
