// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

const (
	packBenchmarkEvidenceEnv    = "DISPAT_BENCH_EVIDENCE"
	packBenchmarkPayloadEnv     = "DISPAT_BENCH_PAYLOAD"
	packBenchmarkInventoryEnv   = "DISPAT_BENCH_INVENTORY"
	packBenchmarkTraceEnv       = "GIT_TRACE2_EVENT"
	packBenchmarkSmall          = "small"
	packBenchmarkOpaque         = "opaque-llvm"
	packBenchmarkOperationLimit = 120 * time.Second
	packBenchmarkFileMode       = 0o600
	packBenchmarkDirectoryMode  = 0o700
)

// BenchmarkWorkerResultPackPublication measures the unchanged production entry
// point, including durable publication, bounded pack preparation, the real
// coordinator output verifier, broker admission and signed finality. An
// in-process transport adapter omits HTTP framing; this is a return-stage
// microbenchmark, not a complete coordinator lifecycle. No payload binary is
// executed. Run through testreport bench with -benchtime=1x -count=1; B.Loop
// avoids a separate calibration invocation.
// Each iteration gets fresh object stores; setup and independent verification
// are outside the timer. Custom counters remain in the raw benchmark JSON even
// though testreport's typed summary does not preserve arbitrary metric units.
// Go ns/op includes small B.Loop/timer bookkeeping spans; the separately retained
// time.Since interval is the method boundary, so these values are not identical.
// -cpu controls GOMAXPROCS only, not native Git or host CPU capacity.
func BenchmarkWorkerResultPackPublication(b *testing.B) {
	require.Equal(b, os.Getenv(packBenchmarkPayloadEnv) == "", os.Getenv(packBenchmarkInventoryEnv) == "", "payload and inventory configuration must be supplied together")
	if os.Getenv(packBenchmarkEvidenceEnv) == "" {
		require.Empty(b, os.Getenv(packBenchmarkPayloadEnv), "payload configuration requires an evidence directory")
		require.Empty(b, os.Getenv(packBenchmarkInventoryEnv), "inventory configuration requires an evidence directory")
		b.Skip("opt-in result-pack benchmark requires DISPAT_BENCH_EVIDENCE and explicit retained payload for opaque-llvm")
	}
	for _, scenario := range []string{packBenchmarkSmall, packBenchmarkOpaque} {
		b.Run(scenario, func(b *testing.B) {
			evidence := os.Getenv(packBenchmarkEvidenceEnv)
			require.True(b, filepath.IsAbs(evidence), "benchmark evidence directory must be absolute")
			require.Empty(b, os.Getenv(packBenchmarkTraceEnv), "the benchmark owns its per-operation trace")
			dir := filepath.Join(evidence, scenario)
			require.NoError(b, os.MkdirAll(filepath.Dir(dir), packBenchmarkDirectoryMode))
			require.NoError(b, os.Mkdir(dir, packBenchmarkDirectoryMode), "refuse an existing case, never overwrite an observation")
			inputs := packBenchmarkInputs(b, scenario)
			writePackBenchmarkJSON(b, filepath.Join(dir, "inputs.json"), inputs)
			b.ReportAllocs()
			var totals packBenchmarkMetrics
			calls := 0
			for b.Loop() {
				b.StopTimer()
				operation := filepath.Join(dir, fmt.Sprintf("operation-%03d", calls+1))
				require.NoError(b, os.Mkdir(operation, packBenchmarkDirectoryMode))
				fixture := newPackBenchmarkFixture(b, inputs)
				metrics := runPackBenchmarkOperation(b, fixture, operation)
				totals.add(metrics)
				calls++
				b.StartTimer()
			}
			require.Equal(b, b.N, calls, "one production return per measured iteration")
			totals.report(b, calls)
		})
	}
}

func runPackBenchmarkOperation(b *testing.B, fixture *packBenchmarkFixture, dir string) packBenchmarkMetrics {
	b.Helper()
	trace := filepath.Join(dir, "git-trace2.json")
	receipt := map[string]any{"status": "prepared", "methodCalls": 0, "caseDirectory": dir,
		"workerStore": fixture.worker.Cache.Dir, "canonicalStore": fixture.canonical.store,
		"claimOID": fixture.task.claimed, "pendingOID": fixture.pending.PendingOID,
		"outcomeBranch": fixture.outcomeBranch, "logicalOutputBytes": fixture.report.Outputs.Bytes,
		"hostOS": runtime.GOOS, "hostArch": runtime.GOARCH, "goVersion": runtime.Version(), "gomaxprocs": runtime.GOMAXPROCS(0),
		"boundary": "Worker.returnTaskResult through real signed broker admission and physical finality; setup and post-verification excluded"}
	writePackBenchmarkJSON(b, filepath.Join(dir, "observation.json"), receipt)
	defer func() { writePackBenchmarkJSON(b, filepath.Join(dir, "observation.json"), receipt) }()
	ctx, cancel := context.WithTimeout(b.Context(), packBenchmarkOperationLimit)
	defer cancel()
	fixture.attachTransport(b, ctx)
	b.Setenv(packBenchmarkTraceEnv, trace)
	before := gitx.GitInvocations()
	receipt["status"], receipt["methodCalls"] = "started", 1
	receipt["startedUTC"] = time.Now().UTC().Format(time.RFC3339Nano)
	writePackBenchmarkJSON(b, filepath.Join(dir, "observation.json"), receipt)
	b.StartTimer()
	start := time.Now()
	oid, err := fixture.worker.returnTaskResult(ctx, fixture.task, fixture.report, carriedOutputs(fixture.report.Outputs))
	elapsed := time.Since(start)
	b.StopTimer()
	invocations := gitx.GitInvocations() - before
	b.Setenv(packBenchmarkTraceEnv, "")
	receipt["elapsedSeconds"], receipt["endedUTC"] = elapsed.Seconds(), time.Now().UTC().Format(time.RFC3339Nano)
	receipt["returnedOID"], receipt["submissionCalls"] = oid, fixture.submissions
	receipt["proposal"], receipt["receipt"] = fixture.proposal, fixture.receipt
	if err != nil {
		receipt["status"], receipt["error"] = "failed", err.Error()
	}
	require.NoError(b, err)
	require.Equal(b, 1, fixture.submissions, "an iteration has one actual submission")
	require.Equal(b, fixture.receipt.OID, oid)
	receipt["status"] = "return-succeeded-verification-pending"
	fixture.verify(b)
	metrics := readPackBenchmarkTrace(b, trace, fixture.worker.Cache.Dir, fixture.outcomeBranch)
	metrics.Git = float64(invocations)
	metrics.PackBytes = float64(fixture.proposal.PayloadBytes)
	receipt["metrics"], receipt["status"] = metrics, "verified"
	writePackBenchmarkJSON(b, filepath.Join(dir, "metrics.json"), metrics)
	b.Logf("pack observation: case=%s elapsed=%s packBytes=%d git=%d submissions=%d", dir, elapsed, fixture.proposal.PayloadBytes, invocations, fixture.submissions)
	return metrics
}

func writePackBenchmarkJSON(t testing.TB, path string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, append(data, '\n'), packBenchmarkFileMode))
}
