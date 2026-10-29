package main

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
)

type importDiskProfile struct {
	SizeGB string `json:"sizeGb"`
	Type   string `json:"type"`
}

func bindImportedCloudIdentity(environment string, imported *campaignImport, record verdict) error {
	var census struct {
		Options struct {
			Source   string `json:"source"`
			Binary   string `json:"binarySha256"`
			ID       string `json:"id"`
			Tool     string `json:"tool"`
			Mode     string `json:"mode"`
			Workload string `json:"workload"`
			Profile  string `json:"profile"`
			Image    string `json:"image"`
		} `json:"options"`
		Instances []hardwareInstance `json:"instances"`
	}
	if err := readHardwareJSON(filepath.Join(environment, "cloud", "provenance.json"), &census); err != nil {
		return err
	}
	if census.Options.Source != imported.Source || census.Options.Binary != imported.Binary {
		return fmt.Errorf("cloud driver source/binary differs from selected measured Dispat")
	}
	if census.Options.ID != record.ExecutionMeasurement.Trial.ID || census.Options.Tool != record.Tool ||
		census.Options.Mode != record.buildResult().Mode || census.Options.Workload != record.Experiment || census.Options.Profile != fullProfile {
		return fmt.Errorf("cloud driver run/tool/mode/workload identity differs from the selected trial")
	}
	if !regexp.MustCompile(`^sha256:[a-f0-9]{64}$`).MatchString(census.Options.Image) {
		return fmt.Errorf("cloud driver has no exact prepared image digest")
	}
	if imported.Images == nil {
		imported.Images = map[string]string{}
	}
	if previous := imported.Images[record.Experiment]; previous != "" && previous != census.Options.Image {
		return fmt.Errorf("cloud cells mix prepared images within %s", record.Experiment)
	}
	imported.Images[record.Experiment] = census.Options.Image
	if err := validateImportedDockerStores(environment, census.Instances); err != nil {
		return err
	}
	for _, instance := range census.Instances {
		var disk importDiskProfile
		if err := readHardwareJSON(filepath.Join(environment, "cloud", instance.Name+"-disk.json"), &disk); err != nil {
			return err
		}
		size, err := strconv.ParseUint(disk.SizeGB, 10, 64)
		if err != nil || size == 0 || disk.Type == "" {
			return fmt.Errorf("cloud disk allocation is missing or invalid for %s", instance.Name)
		}
		disk.Type = filepath.Base(disk.Type)
		if imported.Disk == nil {
			imported.Disk = &disk
		} else if *imported.Disk != disk {
			return fmt.Errorf("LLVM/kernel disk allocations differ: expected %+v, observed %+v", *imported.Disk, disk)
		}
	}
	return nil
}
