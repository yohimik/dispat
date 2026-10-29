package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const (
	classicDockerDriverName       = "overlay2"
	containerdDriverStatusKey     = "driver-type"
	containerdDriverStatusValue   = "io.containerd.snapshotter.v1"
	dockerInfoFormat              = `{"ServerVersion":{{json .ServerVersion}},"Driver":{{json .Driver}},"DriverStatus":{{json .DriverStatus}},"Architecture":{{json .Architecture}},"OSType":{{json .OSType}},"CgroupDriver":{{json .CgroupDriver}},"CgroupVersion":{{json .CgroupVersion}}}`
	coordinatorDockerInfoFileName = "coordinator-docker-info.json"
	workerDockerInfoFileSuffix    = "-docker-info.json"
	dockerInfoArgumentsName       = "info"
	dockerInfoFormatFlag          = "--format"
	dockerInfoParseError          = "Docker store evidence %s is invalid: %w"
	dockerStoreMismatchError      = "Docker store %s must use classic overlay2 with containerd-snapshotter disabled; observed driver %q, version %q"
)

type dockerStoreInfo struct {
	ServerVersion string     `json:"ServerVersion"`
	Driver        string     `json:"Driver"`
	DriverStatus  [][]string `json:"DriverStatus"`
}

func (d *driver) verifyCoordinatorDockerStore(ctx context.Context) error {
	content, err := d.runCommand(ctx, invocation{Name: "docker", Args: []string{dockerInfoArgumentsName, dockerInfoFormatFlag, dockerInfoFormat}})
	if err != nil {
		return err
	}
	return d.retainDockerStore(content, coordinatorDockerInfoFileName)
}

func (d *driver) verifyWorkerDockerStore(ctx context.Context, worker instance) error {
	content, err := d.ssh(ctx, worker, invocation{Name: "sudo", Args: []string{"docker", dockerInfoArgumentsName, dockerInfoFormatFlag, dockerInfoFormat}})
	if err != nil {
		return err
	}
	return d.retainDockerStore(content, worker.Name+workerDockerInfoFileSuffix)
}

func (d *driver) retainDockerStore(content []byte, name string) error {
	if err := os.WriteFile(filepath.Join(d.settings.Root, "results/cloud", name), content, publicFileMode); err != nil {
		return err
	}
	var observed dockerStoreInfo
	if err := json.Unmarshal(content, &observed); err != nil {
		return fmt.Errorf(dockerInfoParseError, name, err)
	}
	if observed.Driver != classicDockerDriverName || observed.ServerVersion == "" {
		return fmt.Errorf(dockerStoreMismatchError, name, observed.Driver, observed.ServerVersion)
	}
	for _, status := range observed.DriverStatus {
		if len(status) == 2 && status[0] == containerdDriverStatusKey && status[1] == containerdDriverStatusValue {
			return fmt.Errorf(dockerStoreMismatchError, name, observed.Driver, observed.ServerVersion)
		}
	}
	return nil
}
