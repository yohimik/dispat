package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type campaignHardware struct {
	BootImage           string `json:"bootImage"`
	MachineType         string `json:"machineType"`
	CPUPlatform         string `json:"cpuPlatform"`
	CPUQuotaPeriod      string `json:"cpuQuotaPeriod"`
	MemoryBytes         uint64 `json:"memoryBytes"`
	OuterSlots          int    `json:"outerTaskSlots"`
	ConfiguredNodeSlots int    `json:"configuredNodeSlots"`
	AdmittedNodeSlots   int    `json:"admittedNodeSlots"`
	NinjaJobs           int    `json:"ninjaJobsPerTask,omitempty"`
	LinkPool            int    `json:"linkPoolPerTask"`
	MakeJobs            int    `json:"makeJobsPerTask,omitempty"`
	LinkPoolReason      string `json:"linkPoolReason,omitempty"`
}

type hardwareInstance struct {
	Name        string `json:"name"`
	ID          string `json:"id"`
	MachineType string `json:"machineType"`
	CPUPlatform string `json:"cpuPlatform"`
}

func readCampaignHardware(environment string, record verdict) (*campaignHardware, error) {
	var census struct {
		BootImage string             `json:"bootImage"`
		Instances []hardwareInstance `json:"instances"`
	}
	if err := readHardwareJSON(filepath.Join(environment, "cloud", "provenance.json"), &census); err != nil {
		return nil, err
	}
	if census.BootImage == "" || len(census.Instances) != executorCount {
		return nil, fmt.Errorf("boot image or two-instance census is missing")
	}
	resources := record.ExecutionMeasurement.Resources
	if resources.TaskSlotLimit.Value == nil || *resources.TaskSlotLimit.Value < 1 || *resources.TaskSlotLimit.Value > math.MaxInt32 || math.Trunc(*resources.TaskSlotLimit.Value) != *resources.TaskSlotLimit.Value {
		return nil, fmt.Errorf("outer task-slot limit is missing or not a positive integer")
	}
	var common *campaignHardware
	seen := map[string]bool{}
	for _, instance := range census.Instances {
		if instance.Name == "" || filepath.Base(instance.Name) != instance.Name || instance.Name == "." || instance.Name == ".." || instance.ID == "" || seen[instance.ID] {
			return nil, fmt.Errorf("instance census has missing or duplicated identity")
		}
		seen[instance.ID] = true
		actual, err := readInstanceHardware(environment, instance, census.BootImage)
		if err != nil {
			return nil, fmt.Errorf("node %s: %w", instance.Name, err)
		}
		actual.OuterSlots = int(*resources.TaskSlotLimit.Value)
		if err := bindNodeHardware(actual, record, instance.ID); err != nil {
			return nil, fmt.Errorf("node %s: %w", instance.Name, err)
		}
		if err := bindNestedHardware(actual, record); err != nil {
			return nil, err
		}
		if common == nil {
			common = actual
			continue
		}
		if err := compareCampaignHardware(*common, *actual); err != nil {
			return nil, fmt.Errorf("node %s differs from its peer: %w", instance.Name, err)
		}
	}
	return common, nil
}

func readInstanceHardware(environment string, expected hardwareInstance, boot string) (*campaignHardware, error) {
	root := filepath.Join(environment, "cloud", expected.Name)
	var actual hardwareInstance
	if err := readHardwareJSON(root+".json", &actual); err != nil {
		return nil, err
	}
	for _, field := range []struct{ Name, Expected, Actual string }{{"name", expected.Name, actual.Name}, {"id", expected.ID, actual.ID}, {"machineType", expected.MachineType, actual.MachineType}, {"cpuPlatform", expected.CPUPlatform, actual.CPUPlatform}} {
		if field.Actual == "" {
			return nil, fmt.Errorf("actual %s is missing from GCE instance evidence", field.Name)
		}
		if field.Actual != field.Expected {
			return nil, fmt.Errorf("%s: expected %q, observed %q", field.Name, field.Expected, field.Actual)
		}
	}
	var disk struct {
		SourceImage string `json:"sourceImage"`
	}
	if err := readHardwareJSON(root+"-disk.json", &disk); err != nil {
		return nil, err
	}
	if disk.SourceImage == "" || disk.SourceImage != boot {
		return nil, fmt.Errorf("bootImage: expected %q, observed %q", boot, disk.SourceImage)
	}
	content, err := os.ReadFile(root + "-resources.txt")
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	if len(lines) < 3 || strings.TrimSpace(lines[len(lines)-1]) != actual.ID {
		return nil, fmt.Errorf("container resource probe has no matching metadata instance ID")
	}
	quota := strings.Fields(lines[len(lines)-3])
	if len(quota) != 2 {
		return nil, fmt.Errorf("container cpu.max quota/period is missing")
	}
	for _, value := range quota {
		number, err := strconv.ParseUint(value, 10, 64)
		if err != nil || number == 0 {
			return nil, fmt.Errorf("container cpu.max must contain positive quota/period, observed %q", lines[len(lines)-3])
		}
	}
	memory, err := strconv.ParseUint(strings.TrimSpace(lines[len(lines)-2]), 10, 64)
	if err != nil || memory == 0 {
		return nil, fmt.Errorf("container memory.max must contain a positive byte limit, observed %q", lines[len(lines)-2])
	}
	return &campaignHardware{BootImage: disk.SourceImage, MachineType: filepath.Base(actual.MachineType), CPUPlatform: actual.CPUPlatform, CPUQuotaPeriod: strings.Join(quota, " "), MemoryBytes: memory}, nil
}

func bindNodeHardware(hardware *campaignHardware, record verdict, identity string) error {
	for _, node := range record.ExecutionMeasurement.Resources.Nodes {
		if node["hostId"] != "gce:"+identity {
			continue
		}
		quota := strings.Fields(hardware.CPUQuotaPeriod)
		numerator, _ := strconv.ParseFloat(quota[0], 64)
		denominator, _ := strconv.ParseFloat(quota[1], 64)
		for _, field := range []struct {
			Name     string
			Expected any
		}{{"machineType", hardware.MachineType}, {"cpuPlatform", hardware.CPUPlatform}, {"cpuQuota", numerator / denominator}, {"memoryBytes", float64(hardware.MemoryBytes)}} {
			if node[field.Name] != field.Expected {
				return fmt.Errorf("recorded %s: expected actual evidence %v, observed %v", field.Name, field.Expected, node[field.Name])
			}
		}
		configured, err := positiveHardwareInteger(node, "configuredTaskSlots")
		if err != nil {
			return err
		}
		admitted, err := positiveHardwareInteger(node, "admittedTaskSlots")
		if err != nil {
			return err
		}
		hardware.ConfiguredNodeSlots, hardware.AdmittedNodeSlots = configured, admitted
		return nil
	}
	return fmt.Errorf("physical instance %s has no measurement resource node", identity)
}

func bindNestedHardware(hardware *campaignHardware, record verdict) error {
	if record.Experiment == kernelWorkload {
		return bindKernelNestedHardware(hardware, record)
	}
	vertices := map[string]bool{}
	for _, vertex := range record.ExecutionMeasurement.Graph.Vertices {
		if !vertex.IsVirtual {
			vertices[vertex.ID] = false
		}
	}
	for _, nested := range record.ExecutionMeasurement.Resources.Nested {
		task, _ := nested["taskId"].(string)
		seen, exists := vertices[task]
		if !exists || seen {
			return fmt.Errorf("nested build limits have unknown or duplicate task %q", task)
		}
		jobs, err := positiveHardwareInteger(nested, "ninjaJobs")
		if err != nil {
			return err
		}
		links, err := positiveHardwareInteger(nested, "linkPool")
		if err != nil {
			return err
		}
		if hardware.NinjaJobs != 0 && (hardware.NinjaJobs != jobs || hardware.LinkPool != links) {
			return fmt.Errorf("nested build limits differ across tasks: expected Ninja/link %d/%d, observed %d/%d", hardware.NinjaJobs, hardware.LinkPool, jobs, links)
		}
		if jobs != record.LLVM.JobsPerExecutor {
			return fmt.Errorf("nested ninjaJobs %d differs from LLVM jobsPerExecutor %d", jobs, record.LLVM.JobsPerExecutor)
		}
		hardware.NinjaJobs, hardware.LinkPool = jobs, links
		vertices[task] = true
	}
	for task, seen := range vertices {
		if !seen {
			return fmt.Errorf("task %s has no nested Ninja/link limits", task)
		}
	}
	return nil
}

func positiveHardwareInteger(values map[string]any, key string) (int, error) {
	value, ok := values[key].(float64)
	if !ok || value < 1 || math.IsNaN(value) || math.IsInf(value, 0) || math.Trunc(value) != value || value > math.MaxInt32 {
		return 0, fmt.Errorf("%s is missing or not a positive integer", key)
	}
	return int(value), nil
}

func compareCampaignHardware(expected, actual campaignHardware) error {
	for _, field := range []struct {
		Name             string
		Expected, Actual any
	}{
		{"bootImage", expected.BootImage, actual.BootImage}, {"machineType", expected.MachineType, actual.MachineType},
		{"cpuPlatform", expected.CPUPlatform, actual.CPUPlatform}, {"cpu.max", expected.CPUQuotaPeriod, actual.CPUQuotaPeriod},
		{"memory.max", expected.MemoryBytes, actual.MemoryBytes}, {"outerTaskSlots", expected.OuterSlots, actual.OuterSlots},
		{"configuredNodeSlots", expected.ConfiguredNodeSlots, actual.ConfiguredNodeSlots}, {"admittedNodeSlots", expected.AdmittedNodeSlots, actual.AdmittedNodeSlots},
		{"ninjaJobs", expected.NinjaJobs, actual.NinjaJobs}, {"linkPool", expected.LinkPool, actual.LinkPool},
		{"makeJobs", expected.MakeJobs, actual.MakeJobs}, {"linkPoolReason", expected.LinkPoolReason, actual.LinkPoolReason},
	} {
		if field.Expected != field.Actual {
			return fmt.Errorf("%s: expected %v, observed %v", field.Name, field.Expected, field.Actual)
		}
	}
	return nil
}

func readHardwareJSON(path string, value any) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(content, value); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}
