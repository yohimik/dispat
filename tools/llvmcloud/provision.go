package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const startupScript = `#!/bin/sh
set -eu
export DEBIAN_FRONTEND=noninteractive
apt-get update -q
apt-get install -y -q docker.io git curl
touch /var/lib/dispat-llvm-ready
`

const (
	minimumCPUPlatformFlagName = "--min-cpu-platform"
	cpuPlatformMismatchError   = "instance %s has CPU platform %q; expected exact platform %q"
)

func (d *driver) provision(ctx context.Context) error {
	hardware, err := d.gcloud(ctx, "compute", "machine-types", "describe", machineType, "--zone", d.settings.Zone, "--format=json")
	if err != nil {
		return err
	}
	var spec struct {
		GuestCPUs int `json:"guestCpus"`
		MemoryMB  int `json:"memoryMb"`
	}
	if err := json.Unmarshal(hardware, &spec); err != nil {
		return err
	}
	if spec.GuestCPUs != workerCPUs || spec.MemoryMB != 16384 {
		return errors.New("selected GCE machine type has unexpected CPU or memory capacity")
	}
	if err := os.WriteFile(filepath.Join(d.settings.Root, "results/cloud/machine-type.json"), hardware, publicFileMode); err != nil {
		return err
	}
	image, err := d.resolveBootImage(ctx)
	if err != nil {
		return err
	}
	d.state.Image = image
	if err := d.save(); err != nil {
		return err
	}
	key := filepath.Join(d.settings.Root, "private/key")
	if _, err := d.runCommand(ctx, invocation{Name: "ssh-keygen", Args: []string{"-q", "-t", "ed25519", "-N", "", "-f", key}}); err != nil {
		return err
	}
	public, err := os.ReadFile(key + ".pub")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(d.settings.Root, "private/ssh-keys"), append([]byte(sshUser+":"), public...), privateFileMode); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(d.settings.Root, "private/startup.sh"), []byte(startupScript), privateFileMode); err != nil {
		return err
	}
	for index := range workerCount {
		name := "dispat-llvm-" + d.settings.ID + "-" + strconv.Itoa(index+1)
		existing, err := d.gcloud(ctx, "compute", "instances", "list", "--filter", "name="+name+" AND zone:"+d.settings.Zone, "--format=json")
		if err != nil {
			return err
		}
		var prior []instance
		if err := json.Unmarshal(existing, &prior); err != nil {
			return err
		}
		if len(prior) != 0 {
			return errors.New("cloud instance name already exists; refusing to reuse it")
		}
		// Journal intent before creation: an uncertain API result can still be
		// reconciled by exact name and the unique ownership labels during cleanup.
		item := instance{Name: name, Labels: map[string]string{"dispat-experiment": "llvm", "trial": d.settings.ID}}
		d.state.Instances = append(d.state.Instances, item)
		if err := d.save(); err != nil {
			return err
		}
		args := []string{"compute", "instances", "create", name, "--zone", d.settings.Zone, "--machine-type", machineType, "--image", d.state.Image, "--boot-disk-size", "100GB", "--boot-disk-type", "pd-balanced", "--no-service-account", "--no-scopes", "--max-run-duration", vmLifetime, "--instance-termination-action", "DELETE", "--provisioning-model", "STANDARD", "--labels", "dispat-experiment=llvm,trial=" + d.settings.ID, "--metadata", "enable-guest-attributes=TRUE,enable-oslogin=FALSE,block-project-ssh-keys=TRUE", "--metadata-from-file", "startup-script=" + filepath.Join(d.settings.Root, "private/startup.sh") + ",ssh-keys=" + filepath.Join(d.settings.Root, "private/ssh-keys"), "--format=json"}
		if d.settings.CPUPlatform != "" {
			args = append(args, minimumCPUPlatformFlagName, d.settings.CPUPlatform)
		}
		if _, err := d.gcloud(ctx, args...); err != nil {
			return fmt.Errorf("create %s (no machine or zone fallback): %w", name, err)
		}
		actual, raw, err := d.describe(ctx, name)
		if err != nil {
			return err
		}
		if err := verifyInstance(item, actual); err != nil {
			return err
		}
		if actual.ID == "" || actual.CPUPlatform == "" || actual.MachineType != "https://www.googleapis.com/compute/v1/projects/"+d.settings.Project+"/zones/"+d.settings.Zone+"/machineTypes/"+machineType || len(actual.ServiceAccounts) != 0 || len(actual.Disks) != 1 {
			return fmt.Errorf("instance %s does not have the required identity/type/no-service-account configuration", name)
		}
		if d.settings.CPUPlatform != "" && actual.CPUPlatform != d.settings.CPUPlatform {
			return fmt.Errorf(cpuPlatformMismatchError, name, actual.CPUPlatform, d.settings.CPUPlatform)
		}
		if index > 0 && actual.CPUPlatform != d.state.Instances[0].CPUPlatform {
			return errors.New("GCE CPU platforms differ; refusing an unequal worker pair")
		}
		disk, err := d.gcloud(ctx, "compute", "disks", "describe", actual.Disks[0].Source, "--zone", d.settings.Zone, "--format=json")
		if err != nil {
			return err
		}
		var diskInfo struct {
			SourceImage string `json:"sourceImage"`
		}
		if err := json.Unmarshal(disk, &diskInfo); err != nil {
			return err
		}
		if diskInfo.SourceImage != d.state.Image {
			return errors.New("worker boot disk is not the selected exact image")
		}
		d.state.Instances[index] = actual
		if err := d.save(); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(d.settings.Root, "results/cloud/"+name+".json"), raw, publicFileMode); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(d.settings.Root, "results/cloud/"+name+"-disk.json"), disk, publicFileMode); err != nil {
			return err
		}
		if err := d.awaitSSH(ctx, actual); err != nil {
			return err
		}
	}
	return nil
}

func (d *driver) resolveBootImage(ctx context.Context) (string, error) {
	if d.settings.BootImage != "" {
		return d.settings.BootImage, nil
	}
	resolved, err := d.runCommand(ctx, invocation{Name: "gcloud", Args: []string{"--quiet", "--project", imageProject, "compute", "images", "describe-from-family", imageFamily, "--format=value(selfLink)"}})
	if err != nil {
		return "", err
	}
	image := strings.TrimSpace(string(resolved))
	if !bootImageIdentity.MatchString(image) {
		return "", errors.New(bootImageIdentityError)
	}
	return image, nil
}

func (d *driver) describe(ctx context.Context, name string) (instance, []byte, error) {
	raw, err := d.gcloud(ctx, "compute", "instances", "describe", name, "--zone", d.settings.Zone, "--format=json")
	var item instance
	if err != nil {
		return item, raw, err
	}
	err = json.Unmarshal(raw, &item)
	return item, raw, err
}

func verifyInstance(expected, actual instance) error {
	if expected.Name != actual.Name || expected.ID != "" && expected.ID != actual.ID {
		return fmt.Errorf("refusing instance identity change for %s", expected.Name)
	}
	for key, value := range expected.Labels {
		if actual.Labels[key] != value {
			return fmt.Errorf("refusing instance %s with different ownership label %s", expected.Name, key)
		}
	}
	return nil
}

func instanceIP(item instance) (string, error) {
	if len(item.NetworkInterfaces) != 1 || len(item.NetworkInterfaces[0].AccessConfigs) != 1 || item.NetworkInterfaces[0].AccessConfigs[0].NATIP == "" {
		return "", fmt.Errorf("instance %s has no unique SSH address", item.Name)
	}
	return item.NetworkInterfaces[0].AccessConfigs[0].NATIP, nil
}

func (d *driver) sshArgs(item instance) ([]string, error) {
	ip, err := instanceIP(item)
	if err != nil {
		return nil, err
	}
	return []string{"-i", filepath.Join(d.settings.Root, "private/key"), "-o", "IdentitiesOnly=yes", "-o", "UserKnownHostsFile=" + filepath.Join(d.settings.Root, "private/known_hosts"), "-o", "StrictHostKeyChecking=yes", "-o", "ForwardAgent=no", "-o", "ConnectTimeout=15", "-o", "BatchMode=yes", sshUser + "@" + ip}, nil
}

func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }
func shellCommand(args []string) string {
	quoted := make([]string, len(args))
	for index, value := range args {
		quoted[index] = shellQuote(value)
	}
	return strings.Join(quoted, " ")
}
func (d *driver) ssh(ctx context.Context, item instance, request invocation) ([]byte, error) {
	args, err := d.sshArgs(item)
	if err != nil {
		return nil, err
	}
	args = append(args, shellCommand(append([]string{request.Name}, request.Args...)))
	return d.runCommand(ctx, invocation{Name: "ssh", Args: args, Input: request.Input, Output: request.Output})
}

func waitPoll(ctx context.Context) error {
	timer := time.NewTimer(pollInterval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (d *driver) awaitSSH(ctx context.Context, item instance) error {
	ip, err := instanceIP(item)
	if err != nil {
		return err
	}
	for {
		keys, keyErr := d.gcloud(ctx, "compute", "instances", "get-guest-attributes", item.Name, "--zone", d.settings.Zone, "--query-path=hostkeys/", "--format=value(key,value)")
		if keyErr == nil && len(keys) > 0 {
			var lines strings.Builder
			for _, line := range strings.Split(strings.TrimSpace(string(keys)), "\n") {
				parts := strings.Fields(line)
				if len(parts) == 2 && strings.HasPrefix(parts[0], "ssh-") {
					fmt.Fprintf(&lines, "%s %s %s\n", ip, parts[0], parts[1])
				}
			}
			if lines.Len() > 0 {
				file, err := os.OpenFile(filepath.Join(d.settings.Root, "private/known_hosts"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, privateFileMode)
				if err != nil {
					return err
				}
				_, writeErr := io.WriteString(file, lines.String())
				if err := errors.Join(writeErr, file.Close()); err != nil {
					return err
				}
				break
			}
		}
		if err := waitPoll(ctx); err != nil {
			return err
		}
	}
	for {
		if _, err := d.ssh(ctx, item, invocation{Name: "test", Args: []string{"-f", "/var/lib/dispat-llvm-ready"}}); err == nil {
			return nil
		}
		if err := waitPoll(ctx); err != nil {
			return err
		}
	}
}

func (d *driver) prepareWorkers(ctx context.Context) error {
	d.plan = remotePlan{Schema: cloudSchema, Adapter: adapterSpec{Executable: "/usr/local/bin/llvmcloud", Args: []string{"adapter", "--socket", "/cloud/control.sock"}}, MailboxURL: mailboxURL, MailboxPath: "/cloud/mailbox/origin.git", ReturnEndpoint: returnEndpoint, CoordinatorGitConfig: "/cloud/private/coordinator.gitconfig", Nodes: []remoteNode{}}
	if d.settings.Tool == "dispat" {
		d.plan.Orchestration = "native-distributed"
	} else {
		d.plan.Orchestration = "manual-gcp-split"
	}
	for index, item := range d.state.Instances {
		name := "llvm-node-" + strconv.Itoa(index+1)
		node := remoteNode{Name: name, Root: "/cloud/work/" + name + "/repo", State: "/cloud/work/" + name + "/state", EvidenceDir: "/cloud/work/" + name + "/evidence", GitConfig: "/cloud/private/" + name + ".gitconfig", HostID: "gce:" + item.ID, EvidencePath: "/cloud/results/cloud/" + item.Name + ".json", MachineType: machineType, CPUPlatform: item.CPUPlatform, CPUQuota: workerCPUs, MemoryBytes: workerMemoryBytes}
		d.plan.Nodes = append(d.plan.Nodes, node)
		if _, err := d.ssh(ctx, item, invocation{Name: "sudo", Args: []string{"install", "-d", "-m", "0700", "-o", sshUser, "-g", sshUser, "/cloud"}}); err != nil {
			return err
		}
		archive, err := os.Open(d.settings.Archive)
		if err != nil {
			return err
		}
		_, loadErr := d.ssh(ctx, item, invocation{Name: "sudo", Args: []string{"docker", "load"}, Input: archive, Output: os.Stdout})
		if err := errors.Join(loadErr, archive.Close()); err != nil {
			return err
		}
		identity, err := d.ssh(ctx, item, invocation{Name: "sudo", Args: []string{"docker", "image", "inspect", "--format", "{{.Id}}", d.settings.Image}})
		if err != nil {
			return err
		}
		if strings.TrimSpace(string(identity)) != d.settings.Image {
			return errors.New("worker loaded another OCI image")
		}
		binary, err := d.ssh(ctx, item, invocation{Name: "sudo", Args: []string{"docker", "run", "--rm", "--entrypoint", "sha256sum", d.settings.Image, "/usr/local/bin/dispat"}})
		if err != nil {
			return err
		}
		if len(strings.Fields(string(binary))) != 2 || strings.Fields(string(binary))[0] != d.settings.BinarySHA256 {
			return errors.New("worker candidate binary hash differs")
		}
		metadata, err := d.ssh(ctx, item, invocation{Name: "curl", Args: []string{"--fail", "--silent", "-H", "Metadata-Flavor: Google", "http://metadata.google.internal/computeMetadata/v1/instance/id"}})
		if err != nil {
			return err
		}
		if strings.TrimSpace(string(metadata)) != item.ID {
			return errors.New("worker metadata-service identity differs from gcloud census")
		}
		probe := []string{"docker", "run", "--rm", "--network", "host", "--cpus", "4", "--memory", strconv.FormatInt(workerMemoryBytes, 10), "--entrypoint", "sh", d.settings.Image, "-c", "set -eu; uname -a; cat /proc/meminfo; cat /sys/fs/cgroup/cpu.max; cat /sys/fs/cgroup/memory.max; test \"$(cat /sys/fs/cgroup/cpu.max)\" = \"400000 100000\"; test \"$(cat /sys/fs/cgroup/memory.max)\" = 15032385536; curl --fail --silent -H 'Metadata-Flavor: Google' http://metadata.google.internal/computeMetadata/v1/instance/id"}
		resource, err := d.ssh(ctx, item, invocation{Name: "sudo", Args: probe})
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(d.settings.Root, "results/cloud/"+item.Name+"-resources.txt"), resource, publicFileMode); err != nil {
			return err
		}
		if _, err := d.ssh(ctx, item, invocation{Name: "mkdir", Args: []string{"-p", node.Root, node.State, node.EvidenceDir, "/cloud/private"}}); err != nil {
			return err
		}
		for _, path := range []string{node.GitConfig, "/cloud/git-ca.pem"} {
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if _, err := d.ssh(ctx, item, invocation{Name: "sh", Args: []string{"-c", `umask 077; cat > "$1"`, "write-private", path}, Input: bytes.NewReader(content)}); err != nil {
				return err
			}
		}
		args, err := d.sshArgs(item)
		if err != nil {
			return err
		}
		forwards := []string{"-fN", "-M", "-S", filepath.Join(d.settings.Root, "private/"+name+".sock"), "-o", "ExitOnForwardFailure=yes", "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=3", "-R", "127.0.0.1:" + mailboxPort + ":127.0.0.1:" + mailboxPort, "-R", "127.0.0.1:" + returnPort + ":127.0.0.1:" + returnPort}
		target := args[len(args)-1]
		args = append(append(args[:len(args)-1], forwards...), target)
		if _, err := d.runCommand(ctx, invocation{Name: "ssh", Args: args}); err != nil {
			return err
		}
	}
	return nil
}

func (d *driver) cleanup(ctx context.Context) error {
	var failures []error
	failures = append(failures, d.cleanupCoordinator(ctx))
	// List is used to distinguish absence from permission/network failure. A
	// failed describe is never silently treated as successful deletion.
	for index, expected := range d.state.Instances {

		raw, err := d.gcloud(ctx, "compute", "instances", "list", "--filter", "name="+expected.Name+" AND zone:"+d.settings.Zone, "--format=json")
		if err != nil {
			failures = append(failures, err)
			continue
		}
		var found []instance
		if err := json.Unmarshal(raw, &found); err != nil {
			failures = append(failures, err)
			continue
		}
		if len(found) == 0 {
			continue
		}
		if len(found) != 1 {
			failures = append(failures, fmt.Errorf("ambiguous cleanup identity %s", expected.Name))
			continue
		}
		if err := verifyInstance(expected, found[0]); err != nil {
			failures = append(failures, err)
			continue
		}
		if expected.ID != "" {
			failures = append(failures, d.collectNode(ctx, expected))
		}
		// Persist a reconciled ID after a create with an uncertain outcome, then
		// use exactly that ID as the pre-delete identity on subsequent retries.
		if found[0].ID == "" {
			failures = append(failures, errors.New("cleanup instance has no numeric ID"))
			continue
		}
		d.state.Instances[index] = found[0]
		if err := d.save(); err != nil {
			failures = append(failures, err)
			continue
		}
		if _, err := d.gcloud(ctx, "compute", "instances", "delete", expected.Name, "--zone", d.settings.Zone); err != nil {
			failures = append(failures, err)
			continue
		}
		for {
			remaining, err := d.gcloud(ctx, "compute", "instances", "list", "--filter", "id="+found[0].ID, "--format=json")
			if err != nil {
				failures = append(failures, err)
				break
			}
			var entries []instance
			if err := json.Unmarshal(remaining, &entries); err != nil {
				failures = append(failures, err)
				break
			}
			if len(entries) == 0 {
				break
			}
			if err := waitPoll(ctx); err != nil {
				failures = append(failures, err)
				break
			}
		}
	}
	// Local forwarding processes must be settled even if collection failed,
	// the VM vanished, or the cloud cleanup exhausted its own deadline.
	masterCtx, cancelMasters := context.WithTimeout(context.Background(), 30*time.Second)
	failures = append(failures, d.closeSSHMasters(masterCtx))
	cancelMasters()
	err := errors.Join(failures...)
	record := struct {
		Complete bool   `json:"complete"`
		Error    string `json:"error,omitempty"`
	}{Complete: err == nil}
	if err != nil {
		record.Error = err.Error()
	}
	return errors.Join(err, writeJSON(filepath.Join(d.settings.Root, "results/cloud/cleanup.json"), record))
}
