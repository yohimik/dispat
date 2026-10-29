package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	measurement "github.com/yohimik/dispat/tools/internal/executionmeasurement"
)

func defaultFixtureHardware() campaignHardware {
	return campaignHardware{BootImage: "https://example.test/images/fixture-os-v1", MachineType: "n2-standard-4", CPUPlatform: "Intel Cascade Lake", CPUQuotaPeriod: "400000 100000", MemoryBytes: 14 << 30, OuterSlots: 2, ConfiguredNodeSlots: 1, AdmittedNodeSlots: 1, NinjaJobs: 2, LinkPool: 1}
}

func writeFixtureHardware(t *testing.T, root string, record *measurement.Measurement, build *buildResult, hardware campaignHardware) {
	t.Helper()
	cloud := filepath.Join(filepath.Dir(root), "cloud")
	if err := os.MkdirAll(cloud, directoryMode); err != nil {
		t.Fatal(err)
	}
	instances := []hardwareInstance{}
	for index, node := range record.Resources.Nodes {
		identity := fmt.Sprint(index + 1)
		instance := hardwareInstance{Name: "fixture-vm-" + identity, ID: identity, MachineType: "https://example.test/zones/zone/machineTypes/" + hardware.MachineType, CPUPlatform: hardware.CPUPlatform}
		instances = append(instances, instance)
		path := filepath.Join(cloud, instance.Name)
		if err := writeJSON(path+".json", instance); err != nil {
			t.Fatal(err)
		}
		if err := writeJSON(path+"-disk.json", map[string]any{"sourceImage": hardware.BootImage}); err != nil {
			t.Fatal(err)
		}
		probe := fmt.Sprintf("synthetic resource probe\n%s\n%d\n%s", hardware.CPUQuotaPeriod, hardware.MemoryBytes, identity)
		if err := os.WriteFile(path+"-resources.txt", []byte(probe), fileMode); err != nil {
			t.Fatal(err)
		}
		node["machineType"], node["cpuPlatform"] = hardware.MachineType, hardware.CPUPlatform
		var numerator, denominator float64
		fmt.Sscanf(hardware.CPUQuotaPeriod, "%f %f", &numerator, &denominator)
		node["cpuQuota"], node["memoryBytes"] = numerator/denominator, float64(hardware.MemoryBytes)
		node["configuredTaskSlots"], node["admittedTaskSlots"] = hardware.ConfiguredNodeSlots, hardware.AdmittedNodeSlots
	}
	if err := writeJSON(filepath.Join(cloud, "provenance.json"), map[string]any{"bootImage": hardware.BootImage, "instances": instances}); err != nil {
		t.Fatal(err)
	}
	capacity := float64(hardware.OuterSlots)
	record.Resources.TaskSlotLimit.Value = &capacity
	record.Resources.Nested = []map[string]any{}
	for _, vertex := range record.Graph.Vertices {
		record.Resources.Nested = append(record.Resources.Nested, map[string]any{"taskId": vertex.ID, "ninjaJobs": hardware.NinjaJobs, "linkPool": hardware.LinkPool})
	}
	build.JobsPerExecutor = hardware.NinjaJobs
}

func TestCampaignRejectsEveryCrossCellHardwareDifference(t *testing.T) {
	for _, scenario := range []struct {
		Name, Reason string
		Change       func(*campaignHardware)
	}{
		{"boot", "bootImage", func(h *campaignHardware) { h.BootImage += "-different" }},
		{"machine", "machineType", func(h *campaignHardware) { h.MachineType = "n2-standard-8" }},
		{"cpu", "cpuPlatform", func(h *campaignHardware) { h.CPUPlatform = "Intel Ice Lake" }},
		{"quota", "cpu.max", func(h *campaignHardware) { h.CPUQuotaPeriod = "200000 100000" }},
		{"memory", "memory.max", func(h *campaignHardware) { h.MemoryBytes = 12 << 30 }},
		{"outer", "outerTaskSlots", func(h *campaignHardware) { h.OuterSlots = 3 }},
		{"configured", "configuredNodeSlots", func(h *campaignHardware) { h.ConfiguredNodeSlots = 2 }},
		{"admitted", "admittedNodeSlots", func(h *campaignHardware) { h.AdmittedNodeSlots = 2 }},
		{"ninja", "ninjaJobs", func(h *campaignHardware) { h.NinjaJobs = 1 }},
		{"links", "linkPool", func(h *campaignHardware) { h.LinkPool = 2 }},
	} {
		t.Run(scenario.Name, func(t *testing.T) {
			settings := campaignOptions{Input: t.TempDir(), Output: filepath.Join(t.TempDir(), "report"), Source: strings.Repeat("c", 40), Profile: fullProfile}
			var changed string
			for _, cell := range campaignCells() {
				path := writeCampaignFixture(t, settings, cell)
				if cell.Tool == nxTool {
					changed = path
				}
			}
			content, err := os.ReadFile(filepath.Join(changed, "verdict.json"))
			if err != nil {
				t.Fatal(err)
			}
			var record verdict
			if err := json.Unmarshal(content, &record); err != nil {
				t.Fatal(err)
			}
			hardware := defaultFixtureHardware()
			scenario.Change(&hardware)
			writeFixtureHardware(t, changed, record.ExecutionMeasurement, record.LLVM, hardware)
			if err := measurement.Derive(record.ExecutionMeasurement); err != nil {
				t.Fatal(err)
			}
			if err := writeJSON(filepath.Join(changed, "verdict.json"), record); err != nil {
				t.Fatal(err)
			}
			err = collectCampaignFiles(settings)
			if err == nil || !strings.Contains(err.Error(), scenario.Reason+": expected") || !strings.Contains(err.Error(), "observed") {
				t.Fatalf("hardware difference was not identified precisely: %v", err)
			}
			retained := filepath.Join(settings.Output, "environment", "nx-"+manualGCPMode, filepath.Base(changed), "verdict.json")
			if _, err := os.Stat(retained); err != nil {
				t.Fatal("hardware mismatch discarded actual completed result")
			}
		})
	}
}

func TestCampaignHardwareRequiresActualResourceAndDiskEvidence(t *testing.T) {
	for _, name := range []string{"fixture-vm-1-resources.txt", "fixture-vm-2-disk.json"} {
		t.Run(name, func(t *testing.T) {
			settings := campaignOptions{Input: t.TempDir(), Output: filepath.Join(t.TempDir(), "report"), Source: strings.Repeat("c", 40), Profile: fullProfile}
			for _, cell := range campaignCells() {
				writeCampaignFixture(t, settings, cell)
			}
			if err := os.Remove(filepath.Join(settings.Input, "lerna-"+manualGCPMode, "cloud", name)); err != nil {
				t.Fatal(err)
			}
			err := collectCampaignFiles(settings)
			if err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("missing actual hardware evidence was accepted: %v", err)
			}
		})
	}
}
