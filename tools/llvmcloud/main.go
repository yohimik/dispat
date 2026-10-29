// llvmcloud runs one isolated, two-VM LLVM experiment. Cloud credentials never
// enter the coordinator container or either executor.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	cloudRoot                  = "/cloud"
	cloudSchema                = "llvm-remote-plan/v1"
	machineType                = "n2-standard-4"
	defaultZone                = "us-central1-a"
	imageProject               = "ubuntu-os-cloud"
	imageFamily                = "ubuntu-2404-lts-amd64"
	workerCount                = 2
	workerCPUs                 = 4
	workerMemoryBytes    int64 = 14 << 30
	privateDirectoryMode       = 0700
	privateFileMode            = 0600
	publicFileMode             = 0644
	vmLifetime                 = "5h"
	cellTimeout                = 4 * time.Hour
	setupTimeout               = 35 * time.Minute
	cleanupTimeout             = 15 * time.Minute
	pollInterval               = 5 * time.Second
	mailboxPort                = "18444"
	returnPort                 = "18443"
	mailboxURL                 = "https://127.0.0.1:" + mailboxPort + "/origin.git"
	returnEndpoint             = "https://127.0.0.1:" + returnPort
	sshUser                    = "dispat"
	stateFile                  = "state.json"
	planFile                   = "plan.json"
	maximumRequestBytes        = 1 << 20
)

var safeName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,47}$`)

type options struct {
	Project      string `json:"project"`
	Zone         string `json:"zone"`
	ID           string `json:"id"`
	Tool         string `json:"tool"`
	Mode         string `json:"mode"`
	Profile      string `json:"profile"`
	Image        string `json:"image"`
	Archive      string `json:"archive"`
	Source       string `json:"source"`
	BinarySHA256 string `json:"binarySha256"`
	Root         string `json:"root"`
}

type adapterSpec struct {
	Executable string   `json:"executable"`
	Args       []string `json:"args"`
}

type remotePlan struct {
	Schema               string       `json:"schema"`
	Orchestration        string       `json:"orchestration"`
	Adapter              adapterSpec  `json:"adapter"`
	MailboxURL           string       `json:"mailboxURL"`
	MailboxPath          string       `json:"mailboxPath"`
	ReturnEndpoint       string       `json:"returnEndpoint"`
	CoordinatorGitConfig string       `json:"coordinatorGitConfig"`
	Nodes                []remoteNode `json:"nodes"`
}

type remoteNode struct {
	Name         string `json:"name"`
	Root         string `json:"root"`
	State        string `json:"state"`
	EvidenceDir  string `json:"evidenceDir"`
	GitConfig    string `json:"gitConfig"`
	HostID       string `json:"hostId"`
	EvidencePath string `json:"evidencePath"`
	MachineType  string `json:"machineType"`
	CPUPlatform  string `json:"cpuPlatform"`
	CPUQuota     int    `json:"cpuQuota"`
	MemoryBytes  int64  `json:"memoryBytes"`
}

type instance struct {
	Name            string            `json:"name"`
	ID              string            `json:"id"`
	Labels          map[string]string `json:"labels"`
	MachineType     string            `json:"machineType"`
	CPUPlatform     string            `json:"cpuPlatform"`
	Status          string            `json:"status"`
	ServiceAccounts []json.RawMessage `json:"serviceAccounts"`
	Disks           []struct {
		Source string `json:"source"`
	} `json:"disks"`
	NetworkInterfaces []struct {
		AccessConfigs []struct {
			NATIP string `json:"natIP"`
		} `json:"accessConfigs"`
	} `json:"networkInterfaces"`
}

type journal struct {
	Options     options           `json:"options"`
	Image       string            `json:"bootImage"`
	Instances   []instance        `json:"instances"`
	Processes   map[string]string `json:"processes,omitempty"`
	Coordinator string            `json:"coordinator,omitempty"`
}

type driver struct {
	mutex      sync.Mutex
	settings   options
	state      journal
	plan       remotePlan
	runCommand func(context.Context, invocation) ([]byte, error)
}

type invocation struct {
	Name   string
	Args   []string
	Input  io.Reader
	Output io.Writer
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("llvmcloud requires run, cleanup, adapter, or authorize-receive")
	}
	switch args[0] {
	case "adapter":
		return runAdapter(ctx, args[1:])
	case "authorize-receive":
		return authorizeReceive(os.Getenv("REMOTE_USER"), os.Stdin)
	case "cleanup":
		if len(args) != 2 {
			return errors.New("cleanup requires the private state directory")
		}
		var state journal
		if err := readJSON(filepath.Join(args[1], stateFile), &state); err != nil {
			return err
		}
		return (&driver{settings: state.Options, state: state, runCommand: execute}).cleanup(ctx)
	case "run":
		settings, err := parseOptions(args[1:])
		if err != nil {
			return err
		}
		d := &driver{settings: settings, state: journal{Options: settings}, runCommand: execute}
		return d.run(ctx)
	default:
		return fmt.Errorf("unknown llvmcloud command %q", args[0])
	}
}

func parseOptions(args []string) (options, error) {
	var value options
	flags := flag.NewFlagSet("llvmcloud run", flag.ContinueOnError)
	flags.StringVar(&value.Project, "project", "", "GCP project")
	flags.StringVar(&value.Zone, "zone", defaultZone, "one fixed GCE zone; no fallback")
	flags.StringVar(&value.ID, "id", "", "unique run/attempt/cell identifier")
	flags.StringVar(&value.Tool, "tool", "dispat", "executor tool")
	flags.StringVar(&value.Mode, "mode", "compilation", "distribution mode")
	flags.StringVar(&value.Profile, "profile", "full", "full or smoke")
	flags.StringVar(&value.Image, "image", "", "exact loaded OCI image ID")
	flags.StringVar(&value.Archive, "archive", "", "prepared docker image archive")
	flags.StringVar(&value.Source, "source", "", "exact source commit")
	flags.StringVar(&value.BinarySHA256, "binary-sha256", "", "candidate binary hash")
	flags.StringVar(&value.Root, "root", cloudRoot, "private state directory (must be /cloud)")
	if err := flags.Parse(args); err != nil {
		return value, err
	}
	if flags.NArg() != 0 || value.Root != cloudRoot || !safeName.MatchString(value.ID) || !safeName.MatchString(value.Project) || !safeName.MatchString(value.Zone) || value.Archive == "" || !isDigest(value.Image, "sha256:", 64) || !isDigest(value.Source, "", 40) || !isDigest(value.BinarySHA256, "", 64) {
		return value, errors.New("cloud options require /cloud, safe project/zone/run names, exact image/source/binary identities and an archive")
	}
	switch value.Tool {
	case "dispat":
		if value.Mode != "compilation" && value.Mode != "interpretation" && value.Mode != "hybrid" {
			return value, errors.New("cloud Dispat mode must be compilation, interpretation, or hybrid")
		}
	case "nx", "lerna":
		if value.Mode != "manual-gcp-split" {
			return value, errors.New("cloud competitors require explicit manual-gcp-split mode; no native cloud fallback")
		}
	default:
		return value, errors.New("cloud tool must be dispat, nx, or lerna; Changesets has no build scheduler")
	}
	if value.Profile != "full" && value.Profile != "smoke" {
		return value, errors.New("cloud profile must be full or smoke")
	}
	return value, nil
}

func isDigest(value, prefix string, length int) bool {
	if !strings.HasPrefix(value, prefix) || len(value) != len(prefix)+length {
		return false
	}
	for _, letter := range strings.TrimPrefix(value, prefix) {
		if !(letter >= '0' && letter <= '9' || letter >= 'a' && letter <= 'f') {
			return false
		}
	}
	return true
}

func execute(ctx context.Context, request invocation) ([]byte, error) {
	process := exec.CommandContext(ctx, request.Name, request.Args...)
	process.Stdin = request.Input
	process.Stderr = os.Stderr
	if request.Output != nil {
		process.Stdout = request.Output
		return nil, process.Run()
	}
	return process.Output()
}

func (d *driver) gcloud(ctx context.Context, args ...string) ([]byte, error) {
	return d.runCommand(ctx, invocation{Name: "gcloud", Args: append([]string{"--quiet", "--project", d.settings.Project}, args...)})
}
func writeJSON(path string, value any) error {
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(content, '\n'), privateFileMode)
}
func readJSON(path string, value any) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(content, value)
}
func (d *driver) save() error {
	path := filepath.Join(d.settings.Root, stateFile)
	if err := writeJSON(path+".next", d.state); err != nil {
		return err
	}
	return os.Rename(path+".next", path)
}

func (d *driver) run(ctx context.Context) (result error) {
	if entries, err := os.ReadDir(d.settings.Root); err == nil && len(entries) != 0 {
		return errors.New("cloud state directory is not empty; refusing to reuse a trial")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(d.settings.Root, privateDirectoryMode); err != nil {
		return err
	}
	for _, name := range []string{"results/cloud", "work/home", "mailbox", "private"} {
		if err := os.MkdirAll(filepath.Join(d.settings.Root, name), privateDirectoryMode); err != nil {
			return err
		}
	}
	started := time.Now()
	defer func() {
		status := "completed"
		reason := ""
		if result != nil {
			status = "failed"
			reason = result.Error()
		}
		if errors.Is(result, context.DeadlineExceeded) {
			status = "timeout"
		}
		record := struct {
			Trial    options `json:"trial"`
			Status   string  `json:"status"`
			Reason   string  `json:"reason,omitempty"`
			Seconds  float64 `json:"seconds"`
			Boundary string  `json:"boundary"`
		}{Trial: d.settings, Status: status, Reason: reason, Seconds: time.Since(started).Seconds(), Boundary: "driver provisioning start through evidence collection and cleanup; coordinator monotonic elapsed"}
		result = errors.Join(result, writeJSON(filepath.Join(d.settings.Root, "results/cloud/trial.json"), record))
	}()
	if err := d.save(); err != nil {
		return err
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()
		result = errors.Join(result, d.cleanup(cleanupCtx))
	}()
	setupCtx, cancel := context.WithTimeout(ctx, setupTimeout)
	defer cancel()
	if err := d.provision(setupCtx); err != nil {
		return err
	}
	closeServers, err := d.startServers()
	if err != nil {
		return err
	}
	defer closeServers()
	if err := d.prepareWorkers(setupCtx); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(d.settings.Root, planFile), d.plan); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(d.settings.Root, "results/cloud/provenance.json"), d.state); err != nil {
		return err
	}
	cellCtx, stop := context.WithTimeout(ctx, cellTimeout)
	defer stop()
	self, err := os.Executable()
	if err != nil {
		return err
	}
	args := []string{"create", "--name", "llvm-coordinator-" + d.settings.ID, "--label", "dispat-trial=" + d.settings.ID, "--network", "host", "--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()), "--env", "HOME=/cloud/work/home", "--mount", "type=bind,src=" + self + ",dst=/usr/local/bin/llvmcloud,readonly", "--env", "GIT_CONFIG_GLOBAL=" + d.plan.CoordinatorGitConfig, "--env", "GIT_CONFIG_NOSYSTEM=1", "--entrypoint", "llvmexperiment", d.settings.Image, "run", "--tool", d.settings.Tool, "--mode", d.settings.Mode, "--profile", d.settings.Profile, "--trial-id", d.settings.ID, "--repetition", "1", "--timeout", "4h", "--jobs", "2", "--results", "/cloud/results", "--work", "/cloud/work", "--remote-plan", "/cloud/plan.json"}
	mounts := []string{}
	for _, entry := range []struct {
		path     string
		readOnly bool
	}{{"work", false}, {"results", false}, {"mailbox", true}, {"private/coordinator.gitconfig", true}, {"git-ca.pem", true}, {"control.sock", false}, {"plan.json", true}} {
		path := filepath.Join(d.settings.Root, entry.path)
		value := "type=bind,src=" + path + ",dst=" + path
		if entry.readOnly {
			value += ",readonly"
		}
		mounts = append(mounts, "--mount", value)
	}
	args = append(append([]string{args[0]}, mounts...), args[1:]...)
	created, err := d.runCommand(cellCtx, invocation{Name: "docker", Args: args})
	if err != nil {
		return err
	}
	d.state.Coordinator = strings.TrimSpace(string(created))
	if !isDigest(d.state.Coordinator, "", 64) {
		return errors.New("Docker returned no exact coordinator container identity")
	}
	if err := d.save(); err != nil {
		return err
	}
	_, err = d.runCommand(cellCtx, invocation{Name: "docker", Args: []string{"start", "--attach", d.state.Coordinator}, Output: os.Stdout})
	return err
}
