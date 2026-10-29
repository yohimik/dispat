package main

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func kernelFixture(profile string) *LinuxKernelBuild {
	common := llvmFixture().DistributedBuild
	common.SourceRevision = "1253a6aa5d104312bf0992afacc15220ae35bdf9"
	common.Profile, common.Backends, common.Artifacts = profile, []string{"x86_64", "arm64"}, nil
	common.Topology = "two GCE worker VMs; coordinator on GitHub runner"
	common.BuildTargets = []string{"vmlinux", "architecture-boot-image"}
	result := &LinuxKernelBuild{DistributedBuild: common, KernelVersion: "6.18.59", Configuration: "defconfig", Omissions: []string{"partial smoke profile excludes all kernel modules"}, ConfigDigests: map[string]string{}, ModuleCounts: map[string]int64{}}
	artifacts := []string{"vmlinux.stripped", "boot-image", ".config"}
	moduleCount := int64(0)
	if profile == "full" {
		result.Configuration, result.Omissions = "allmodconfig", []string{}
		result.BuildTargets = []string{"vmlinux", "modules", "architecture-boot-image"}
		artifacts = append(artifacts, "Module.symvers", "modules.order", "modules-manifest.json", "sample-module.ko.stripped")
		moduleCount = 3000
	}
	for _, backend := range result.Backends {
		result.ConfigDigests[backend], result.ModuleCounts[backend] = strings.Repeat("c", 64), moduleCount
		for _, name := range artifacts {
			result.Artifacts = append(result.Artifacts, BuildArtifact{Package: "kernel-" + backend, Path: name, SHA256: strings.Repeat("c", 64), Bytes: 123})
		}
	}
	return result
}

func TestLinuxKernelProfilesRequireTheirOwnConfigurationsAndArtifacts(t *testing.T) {
	for _, profile := range []string{"full", "smoke"} {
		if err := validateLinuxKernel(kernelFixture(profile)); err != nil {
			t.Fatalf("valid %s: %v", profile, err)
		}
	}
	future := kernelFixture("full")
	future.KernelVersion = "6.18.60"
	if err := validateLinuxKernel(future); err != nil {
		t.Fatalf("another pinned kernel version became unreadable: %v", err)
	}
	for _, scenario := range []struct {
		name, profile string
		mutate        func(*LinuxKernelBuild)
	}{
		{"missing version", "full", func(r *LinuxKernelBuild) { r.KernelVersion = "" }},
		{"mutable revision", "full", func(r *LinuxKernelBuild) { r.SourceRevision = "linux-6.18.y" }},
		{"wrong backend", "full", func(r *LinuxKernelBuild) { r.Backends[1] = "AArch64" }},
		{"repeated backend", "full", func(r *LinuxKernelBuild) { r.Backends[1] = r.Backends[0] }},
		{"reversed backends", "full", func(r *LinuxKernelBuild) { r.Backends = []string{"arm64", "x86_64"} }},
		{"missing modules target", "full", func(r *LinuxKernelBuild) { r.BuildTargets = []string{"vmlinux", "architecture-boot-image"} }},
		{"wrong full configuration", "full", func(r *LinuxKernelBuild) { r.Configuration = "defconfig" }},
		{"wrong smoke configuration", "smoke", func(r *LinuxKernelBuild) { r.Configuration = "allmodconfig" }},
		{"undeclared omissions", "full", func(r *LinuxKernelBuild) { r.Omissions = nil }},
		{"empty omission", "full", func(r *LinuxKernelBuild) { r.Omissions = []string{" "} }},
		{"hidden smoke omissions", "smoke", func(r *LinuxKernelBuild) { r.Omissions = []string{} }},
		{"missing configuration", "full", func(r *LinuxKernelBuild) { delete(r.ConfigDigests, "arm64") }},
		{"invalid configuration digest", "full", func(r *LinuxKernelBuild) { r.ConfigDigests["arm64"] = "digest" }},
		{"mismatched configuration", "full", func(r *LinuxKernelBuild) { r.ConfigDigests["arm64"] = strings.Repeat("d", 64) }},
		{"extra configuration", "full", func(r *LinuxKernelBuild) { r.ConfigDigests["other"] = strings.Repeat("c", 64) }},
		{"missing module count", "full", func(r *LinuxKernelBuild) { delete(r.ModuleCounts, "arm64") }},
		{"zero full modules", "full", func(r *LinuxKernelBuild) { r.ModuleCounts["arm64"] = 0 }},
		{"negative modules", "full", func(r *LinuxKernelBuild) { r.ModuleCounts["arm64"] = -1 }},
		{"invented smoke modules", "smoke", func(r *LinuxKernelBuild) { r.ModuleCounts["arm64"] = 1 }},
		{"missing artifact", "full", func(r *LinuxKernelBuild) { r.Artifacts = r.Artifacts[1:] }},
		{"repeated artifact", "full", func(r *LinuxKernelBuild) { r.Artifacts[1] = r.Artifacts[0] }},
		{"wrong artifact package", "full", func(r *LinuxKernelBuild) { r.Artifacts[0].Package = "llvm-x86" }},
		{"wrong artifact path", "full", func(r *LinuxKernelBuild) { r.Artifacts[0].Path = "vmlinux" }},
		{"invalid artifact digest", "full", func(r *LinuxKernelBuild) { r.Artifacts[0].SHA256 = "invalid" }},
		{"empty artifact", "full", func(r *LinuxKernelBuild) { r.Artifacts[0].Bytes = 0 }},
		{"same executor", "full", func(r *LinuxKernelBuild) { r.Nodes[1] = r.Nodes[0] }},
		{"missing completion", "full", func(r *LinuxKernelBuild) { r.Seconds = nil }},
		{"infinite completion", "full", func(r *LinuxKernelBuild) { seconds := math.Inf(1); r.Seconds = &seconds }},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			record := kernelFixture(scenario.profile)
			scenario.mutate(record)
			if err := validateLinuxKernel(record); err == nil {
				t.Fatalf("invalid kernel measurement accepted: %+v", record)
			}
		})
	}
}

func TestLinuxKernelUnmeasuredOutcomesNeverClaimCompletion(t *testing.T) {
	for _, status := range []string{"failed", "blocked", "unsupported"} {
		t.Run(status, func(t *testing.T) {
			record := kernelFixture("full")
			record.Status, record.Reason, record.Seconds = status, "no completed build", nil
			record.ConfigDigests, record.ModuleCounts, record.Artifacts, record.Nodes = nil, nil, nil, nil
			if err := validateLinuxKernel(record); err != nil {
				t.Fatal(err)
			}
			campaign := Experiments{Cells: []Cell{{ID: "kernel-unmeasured", Tool: "changesets", LinuxKernel: record}}}
			var output bytes.Buffer
			if err := writeExperimentsMarkdown(&output, campaign); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(output.String(), status+": no completed build") || !strings.Contains(output.String(), "not measured") || strings.Contains(output.String(), "0.000s") || strings.Contains(output.String(), "LLVM") || strings.Contains(output.String(), "| kernel-unmeasured |") {
				t.Fatalf("unmeasured kernel result mislabeled: %s", output.String())
			}
			zero := 0.0
			record.Seconds = &zero
			if err := validateLinuxKernel(record); err == nil {
				t.Fatal("unmeasured zero duration accepted")
			}
			record.Seconds, record.ModuleCounts = nil, map[string]int64{"x86_64": 0}
			record.ConfigDigests = map[string]string{"x86_64": strings.Repeat("c", 64)}
			if err := validateLinuxKernel(record); err != nil {
				t.Fatalf("partial observed backend evidence was discarded: %v", err)
			}
			record.ModuleCounts["x86_64"] = -1
			if err := validateLinuxKernel(record); err == nil {
				t.Fatal("negative partial module count accepted")
			}
			record.ModuleCounts["x86_64"] = 0
			record.ConfigDigests["x86_64"] = "invalid"
			if err := validateLinuxKernel(record); err == nil {
				t.Fatal("invalid partial configuration digest accepted")
			}
			record.ConfigDigests = map[string]string{"other": strings.Repeat("c", 64)}
			if err := validateLinuxKernel(record); err == nil {
				t.Fatal("partial configuration for an unknown architecture accepted")
			}
			record.ConfigDigests, record.ModuleCounts = nil, map[string]int64{"other": 0}
			if err := validateLinuxKernel(record); err == nil {
				t.Fatal("partial module count for an unknown architecture accepted")
			}
		})
	}
}

func TestKernelReaderPreservesSeparateLLVMRecordsAndRendering(t *testing.T) {
	directory := t.TempDir()
	for _, item := range []struct {
		id, experiment, field string
		result                any
	}{{"llvm-full-hybrid-dispat", "llvm", "llvm", llvmFixture()}, {"linux-kernel-full-hybrid-dispat", "linux-kernel", "linuxKernel", kernelFixture("full")}} {
		content, err := json.Marshal(map[string]any{"experiment": item.experiment, "tool": "dispat", "passed": true, item.field: item.result})
		if err != nil {
			t.Fatal(err)
		}
		writeCell(t, directory, item.id, string(content), "")
	}
	campaign, err := readExperiments(directory)
	if err != nil || len(campaign.Cells) != 2 || holding(campaign.Cells) != 0 {
		t.Fatalf("campaign %+v: %v", campaign, err)
	}
	content, err := json.Marshal(campaign)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{`"llvmVersion":"20.1.8"`, `"kernelVersion":"6.18.59"`, `"configDigests"`, `"moduleCounts"`, `"omissions":[]`} {
		if !strings.Contains(string(content), fragment) {
			t.Fatalf("report omitted %s: %s", fragment, content)
		}
	}
	var markdown, plain bytes.Buffer
	if err := writeExperimentsMarkdown(&markdown, campaign); err != nil {
		t.Fatal(err)
	}
	if err := writeExperimentsPlain(&plain, campaign); err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{"LLVM distributed build", "Linux kernel distributed build", "executors × make jobs", "full / allmodconfig", "architecture-boot-image"} {
		if !strings.Contains(markdown.String(), fragment) {
			t.Fatalf("Markdown omitted %s: %s", fragment, markdown.String())
		}
	}
	if !strings.Contains(plain.String(), "Linux kernel 6.18.59: full / allmodconfig") || !strings.Contains(plain.String(), "LLVM: full") {
		t.Fatalf("plain output conflated build workloads: %s", plain.String())
	}
}

func TestKernelReaderRefusesNamespaceStatusAndJSONShapeContradictions(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"missing kernel payload", func(v map[string]any) { delete(v, "linuxKernel") }},
		{"kernel under LLVM", func(v map[string]any) { v["experiment"] = "llvm" }},
		{"mixed payloads", func(v map[string]any) { v["llvm"] = llvmFixture() }},
		{"completion contradicts verdict", func(v map[string]any) { v["passed"] = false }},
		{"LLVM version field", func(v map[string]any) { v["linuxKernel"].(map[string]any)["llvmVersion"] = nil }},
		{"fractional module count", func(v map[string]any) {
			v["linuxKernel"].(map[string]any)["moduleCounts"] = map[string]any{"x86_64": 1.5, "arm64": 3}
		}},
		{"missing omissions", func(v map[string]any) { delete(v["linuxKernel"].(map[string]any), "omissions") }},
		{"non-string omissions", func(v map[string]any) { v["linuxKernel"].(map[string]any)["omissions"] = []any{false} }},
		{"invalid shared measurement", func(v map[string]any) { v["executionMeasurement"] = map[string]any{} }},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			content, err := json.Marshal(map[string]any{"experiment": "linux-kernel", "tool": "dispat", "passed": true, "linuxKernel": kernelFixture("full")})
			if err != nil {
				t.Fatal(err)
			}
			var value map[string]any
			if err := json.Unmarshal(content, &value); err != nil {
				t.Fatal(err)
			}
			scenario.mutate(value)
			content, err = json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			directory := t.TempDir()
			writeCell(t, directory, "linux-kernel-full-hybrid-dispat", string(content), "")
			if _, err := readExperiments(directory); err == nil {
				t.Fatalf("invalid kernel verdict accepted: %s", content)
			}
		})
	}
}
