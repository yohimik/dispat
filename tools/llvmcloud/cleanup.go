package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const coordinatorNamePrefix = "llvm-coordinator-"

type coordinatorContainer struct {
	ID     string `json:"Id"`
	Name   string `json:"Name"`
	Config struct {
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
}

func (d *driver) coordinatorIDs(ctx context.Context, filter string) ([]string, error) {
	content, err := d.runCommand(ctx, invocation{Name: "docker", Args: []string{"ps", "--all", "--no-trunc", "--filter", filter, "--format", "{{.ID}}"}})
	if err != nil {
		return nil, err
	}
	ids := strings.Fields(string(content))
	for _, id := range ids {
		if !isDigest(id, "", 64) {
			return nil, errors.New("coordinator lookup returned an invalid container identity")
		}
	}
	return ids, nil
}

func (d *driver) createCoordinator(ctx context.Context, args []string) error {
	name := coordinatorNamePrefix + d.settings.ID
	ids, err := d.coordinatorIDs(ctx, "name=^/"+name+"$")
	if err != nil {
		return err
	}
	if len(ids) != 0 {
		return errors.New("coordinator container name already exists; refusing to reuse it")
	}
	// Preserve intent before Docker can create a container whose response is
	// lost. Cleanup reconciles the exact name and trial label before adopting ID.
	d.state.CoordinatorName = name
	if err := d.save(); err != nil {
		return err
	}
	created, err := d.runCommand(ctx, invocation{Name: "docker", Args: args})
	if err != nil {
		return err
	}
	identity := strings.TrimSpace(string(created))
	if !isDigest(identity, "", 64) {
		return errors.New("Docker returned no exact coordinator container identity")
	}
	d.state.Coordinator = identity
	return d.save()
}

func (d *driver) cleanupCoordinator(ctx context.Context) error {
	name := d.state.CoordinatorName
	if name == "" && d.state.Coordinator == "" {
		return nil
	}
	if name == "" || name != coordinatorNamePrefix+d.settings.ID {
		return errors.New("cleanup coordinator has no matching journaled name")
	}
	filter := "name=^/" + name + "$"
	if d.state.Coordinator != "" {
		if !isDigest(d.state.Coordinator, "", 64) {
			return errors.New("cleanup coordinator identity is not an exact container ID")
		}
		filter = "id=" + d.state.Coordinator
	}
	ids, err := d.coordinatorIDs(ctx, filter)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		d.state.Coordinator, d.state.CoordinatorName = "", ""
		return d.save()
	}
	if len(ids) != 1 || d.state.Coordinator != "" && ids[0] != d.state.Coordinator {
		return errors.New("cleanup coordinator identity changed or is ambiguous")
	}
	content, err := d.runCommand(ctx, invocation{Name: "docker", Args: []string{"inspect", "--type", "container", ids[0]}})
	if err != nil {
		return err
	}
	var containers []coordinatorContainer
	if err := json.Unmarshal(content, &containers); err != nil {
		return err
	}
	if len(containers) != 1 || containers[0].ID != ids[0] || containers[0].Name != "/"+name || containers[0].Config.Labels["dispat-trial"] != d.settings.ID {
		return errors.New("cleanup coordinator has a different identity, name, or ownership label")
	}
	d.state.Coordinator = ids[0]
	if err := d.save(); err != nil {
		return err
	}
	if _, err := d.runCommand(ctx, invocation{Name: "docker", Args: []string{"rm", "--force", d.state.Coordinator}}); err != nil {
		return err
	}
	d.state.Coordinator, d.state.CoordinatorName = "", ""
	return d.save()
}

func (d *driver) closeSSHMasters(ctx context.Context) error {
	var failures []error
	for index := range d.state.Instances {
		name := "llvm-node-" + strconv.Itoa(index+1)
		socket := filepath.Join(d.settings.Root, "private", name+".sock")
		if _, err := os.Lstat(socket); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			failures = append(failures, err)
			continue
		}
		// A control command addresses only this local socket. Its destination
		// argument is syntactic and does not initiate another network connection.
		_, err := d.runCommand(ctx, invocation{Name: "ssh", Args: []string{"-S", socket, "-O", "exit", "localhost"}, Output: io.Discard})
		if err != nil {
			failures = append(failures, fmt.Errorf("close SSH master %s: %w", name, err))
		}
	}
	return errors.Join(failures...)
}
