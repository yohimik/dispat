package main

import (
	"bytes"
	"strings"
	"testing"
)

func llvmFixture() *LLVMBuild {
	seconds := 123.456
	return &LLVMBuild{LLVMVersion: "20.1.8", DistributedBuild: DistributedBuild{
		Status: buildCompletedStatus, Mode: "hybrid", Profile: "full",
		SourceRevision: strings.Repeat("a", 40), Topology: "two executor processes, one host", Cache: "cold",
		ExecutorCount: 2, JobsPerExecutor: 2, BuildTargets: []string{"all"}, Backends: []string{"X86", "AArch64"},
		Seconds: &seconds, Nodes: []string{"node-1", "node-2"},
		Artifacts: []LLVMArtifact{
			{Package: "x86", Path: "llvm-size", SHA256: strings.Repeat("b", 64), Bytes: 123},
			{Package: "x86", Path: "llvm-cxxfilt", SHA256: strings.Repeat("b", 64), Bytes: 123},
			{Package: "arm", Path: "llvm-size", SHA256: strings.Repeat("b", 64), Bytes: 123},
			{Package: "arm", Path: "llvm-cxxfilt", SHA256: strings.Repeat("b", 64), Bytes: 123},
			{Package: "x86", Path: "llc", SHA256: strings.Repeat("b", 64), Bytes: 123},
			{Package: "x86", Path: "opt", SHA256: strings.Repeat("b", 64), Bytes: 123},
			{Package: "x86", Path: "llvm-config", SHA256: strings.Repeat("b", 64), Bytes: 123},
			{Package: "arm", Path: "llc", SHA256: strings.Repeat("b", 64), Bytes: 123},
			{Package: "arm", Path: "opt", SHA256: strings.Repeat("b", 64), Bytes: 123},
			{Package: "arm", Path: "llvm-config", SHA256: strings.Repeat("b", 64), Bytes: 123},
		},
	}}
}

func TestLLVMMeasurementRequiresDistributedEvidence(t *testing.T) {
	if err := validateLLVM(llvmFixture()); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*LLVMBuild){
		func(record *LLVMBuild) { record.Seconds = nil },
		func(record *LLVMBuild) { record.Nodes[1] = record.Nodes[0] },
		func(record *LLVMBuild) { record.Artifacts = nil },
		func(record *LLVMBuild) { record.SourceRevision = "main" },
		func(record *LLVMBuild) { record.Artifacts[0].SHA256 = "not-a-digest" },
		func(record *LLVMBuild) { record.Status = buildBlockedStatus; record.Reason = "missing credentials" },
	} {
		record := llvmFixture()
		mutate(record)
		if err := validateLLVM(record); err == nil {
			t.Fatalf("accepted unsupported measurement: %+v", record)
		}
	}
}

func TestLLVMBlockedRowsNeverRenderZeroTimeOrSuccess(t *testing.T) {
	blocked := llvmFixture()
	blocked.Status, blocked.Reason, blocked.Seconds, blocked.Nodes, blocked.Artifacts = buildBlockedStatus, "missing cloud credentials", nil, nil, nil
	if err := validateLLVM(blocked); err != nil {
		t.Fatal(err)
	}
	cell := Cell{ID: "llvm-full-distributed-nx", Tool: "nx", LLVM: blocked}
	if holding([]Cell{cell, {Tool: "dispat", Passed: true, LLVM: llvmFixture()}}) != 0 {
		t.Fatal("LLVM availability or completion was counted as a release recovery expectation")
	}
	var output bytes.Buffer
	if err := writeExperimentsMarkdown(&output, Experiments{Cells: []Cell{cell}}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "not measured") || !strings.Contains(output.String(), "blocked: missing cloud credentials") || strings.Contains(output.String(), "0.000s") {
		t.Fatalf("blocked tool was rendered as a timed result: %s", output.String())
	}
	if strings.Contains(output.String(), "| llvm-full-distributed-nx |") {
		t.Fatal("LLVM availability was included in the generic recovery expectations table")
	}
}
