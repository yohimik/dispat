package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

const (
	llvmRevision              = "87f0227cb60147a26a1eeb4fb06e3b505e9c7261"
	llvmVersion               = "20.1.8"
	fullProfile               = "full"
	smokeProfile              = "smoke"
	dispatTool                = "dispat"
	nxTool                    = "nx"
	lernaTool                 = "lerna"
	changesetsTool            = "changesets"
	distributedMode           = "distributed"
	compilationMode           = "compilation"
	interpretationMode        = "interpretation"
	hybridMode                = "hybrid"
	completedStatus           = "completed"
	failedStatus              = "failed"
	blockedStatus             = "blocked"
	unsupportedStatus         = "unsupported"
	experimentName            = "llvm"
	executorCount             = 2
	defaultJobsCount          = 2
	maximumJobsCount          = 64
	defaultTimeout            = 4 * time.Hour
	cleanupTimeout            = 15 * time.Second
	commandWaitDelay          = 5 * time.Second
	runIdentityBytes          = 16
	directoryMode             = 0755
	fileMode                  = 0644
	privateFileMode           = 0600
	executableFileMode        = 0755
	defaultSourcePath         = "/opt/llvm-project"
	defaultResultsPath        = "/results"
	defaultToolsPath          = "/opt/tools/node_modules"
	taskFileName              = "llvm-task.json"
	manifestFileName          = "manifest.json"
	artifactsDirectory        = "artifacts"
	buildDirectory            = "build"
	nodeNamePrefix            = "llvm-node-"
	secretEnvironmentName     = "EXPERIMENT_LLVM_SECRET"
	cloudTokenEnvironmentName = "NX_CLOUD_ACCESS_TOKEN"
	cloudIDEnvironmentName    = "EXPERIMENTS_NX_CLOUD_ID"
	coordinatorName           = "llvm-coordinator"
)

type options struct {
	Tool    string
	Mode    string
	Profile string
	Source  string
	Results string
	Tools   string
	Work    string
	Jobs    int
	Timeout time.Duration
}

type buildResult struct {
	Status          string            `json:"status"`
	Reason          string            `json:"reason,omitempty"`
	Mode            string            `json:"mode"`
	Profile         string            `json:"profile"`
	LLVMVersion     string            `json:"llvmVersion"`
	SourceRevision  string            `json:"sourceRevision"`
	Topology        string            `json:"topology"`
	ExecutorCount   int               `json:"executorCount"`
	JobsPerExecutor int               `json:"jobsPerExecutor"`
	TimeoutSeconds  float64           `json:"timeoutSeconds"`
	BuildTargets    []string          `json:"buildTargets"`
	Backends        []string          `json:"backends"`
	Cache           string            `json:"cache"`
	Seconds         *float64          `json:"seconds,omitempty"`
	Nodes           []string          `json:"nodes"`
	Artifacts       []artifact        `json:"artifacts"`
	Platform        string            `json:"platform"`
	ToolVersions    map[string]string `json:"toolVersions"`
	BinaryID        string            `json:"binaryId"`
	BinarySHA256    string            `json:"binarySha256,omitempty"`
}

type verdict struct {
	Experiment string       `json:"experiment"`
	Tool       string       `json:"tool"`
	Scenario   string       `json:"scenario"`
	Dispat     string       `json:"dispat"`
	Steps      []step       `json:"steps"`
	Checks     []check      `json:"checks"`
	Passed     bool         `json:"passed"`
	LLVM       *buildResult `json:"llvm"`
}

type step struct {
	Name    string  `json:"step"`
	Exit    int     `json:"exit"`
	Seconds float64 `json:"seconds"`
}

type check struct {
	Name string `json:"check"`
	OK   bool   `json:"ok"`
}

type command struct {
	Dir    string
	Name   string
	Args   []string
	Env    []string
	Output io.Writer
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("LLVM experiment requires run or build")
	}
	if args[0] == "build" {
		return buildTask(ctx)
	}
	if args[0] != "run" {
		return fmt.Errorf("LLVM experiment command %q is invalid; expected run or build", args[0])
	}
	settings, err := parseOptions(args[1:])
	if err != nil {
		return err
	}
	return runExperiment(ctx, settings)
}

func parseOptions(args []string) (options, error) {
	var settings options
	flags := flag.NewFlagSet("llvmexperiment run", flag.ContinueOnError)
	flags.StringVar(&settings.Tool, "tool", dispatTool, "dispat, nx, lerna, or changesets")
	flags.StringVar(&settings.Mode, "mode", compilationMode, "compilation, interpretation, hybrid, or distributed")
	flags.StringVar(&settings.Profile, "profile", fullProfile, "full LLVM build, or explicitly partial smoke build")
	flags.StringVar(&settings.Source, "source", defaultSourcePath, "prepared immutable LLVM source checkout")
	flags.StringVar(&settings.Results, "results", defaultResultsPath, "campaign results directory")
	flags.StringVar(&settings.Tools, "tools", defaultToolsPath, "installed pinned competitor node_modules directory")
	flags.StringVar(&settings.Work, "work", os.TempDir(), "parent for a fresh disposable workspace")
	flags.IntVar(&settings.Jobs, "jobs", defaultJobsCount, "Ninja parallelism on each of two executor processes")
	flags.DurationVar(&settings.Timeout, "timeout", defaultTimeout, "maximum elapsed time for one cell")
	if err := flags.Parse(args); err != nil {
		return settings, err
	}
	if flags.NArg() != 0 || settings.Jobs < 1 || settings.Jobs > maximumJobsCount || settings.Timeout <= 0 {
		return settings, errors.New("LLVM arguments require no positional values, 1..64 Ninja jobs, and a positive timeout")
	}
	if settings.Profile != fullProfile && settings.Profile != smokeProfile {
		return settings, fmt.Errorf("LLVM profile %q is invalid; expected full or smoke", settings.Profile)
	}
	switch settings.Tool {
	case dispatTool:
		if settings.Mode != compilationMode && settings.Mode != interpretationMode && settings.Mode != hybridMode {
			return settings, fmt.Errorf("Dispat distribution mode %q is invalid", settings.Mode)
		}
	case nxTool, lernaTool, changesetsTool:
		if settings.Mode != distributedMode {
			return settings, fmt.Errorf("%s requires mode distributed, got %q", settings.Tool, settings.Mode)
		}
	default:
		return settings, fmt.Errorf("LLVM experiment tool %q is invalid", settings.Tool)
	}
	return settings, nil
}

func initialResult(settings options) buildResult {
	return buildResult{
		Status: failedStatus, Mode: settings.Mode, Profile: settings.Profile,
		LLVMVersion: llvmVersion, SourceRevision: llvmRevision,
		Topology:      "two executor processes, isolated checkouts, one host",
		ExecutorCount: executorCount, JobsPerExecutor: settings.Jobs,
		TimeoutSeconds: settings.Timeout.Seconds(),
		BuildTargets:   buildTargets(settings.Profile), Backends: []string{"X86", "AArch64"},
		Cache: "cold build directories; unique committed run input; source and toolchains prepared before timing",
		Nodes: []string{}, Artifacts: []artifact{},
		Platform:     runtime.GOOS + "/" + runtime.GOARCH,
		ToolVersions: map[string]string{}, BinaryID: os.Getenv("EXPERIMENTS_DISPAT_BINARY_ID"),
	}
}

func availability(settings options) (string, string) {
	if settings.Tool == changesetsTool {
		return unsupportedStatus, "Changesets has no build task executor or distributed build mode"
	}
	if settings.Tool != dispatTool && (os.Getenv(cloudTokenEnvironmentName) == "" || os.Getenv(cloudIDEnvironmentName) == "") {
		return blockedStatus, "Nx Cloud distribution requires NX_CLOUD_ACCESS_TOKEN and EXPERIMENTS_NX_CLOUD_ID; no local fallback was run"
	}
	return "", ""
}

func runExperiment(parent context.Context, settings options) error {
	ctx, cancel := context.WithTimeout(parent, settings.Timeout)
	defer cancel()
	cell := strings.Join([]string{experimentName, settings.Profile, settings.Mode, settings.Tool}, "-")
	output := filepath.Join(settings.Results, cell)
	if err := os.MkdirAll(output, directoryMode); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(output, "verdict.json")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	log, err := os.Create(filepath.Join(output, "transcript.txt"))
	if err != nil {
		return err
	}
	defer log.Close()
	result := initialResult(settings)
	record := verdict{Experiment: experimentName, Tool: settings.Tool, Scenario: settings.Profile + "-" + settings.Mode,
		LLVM: &result, Steps: []step{}, Checks: []check{}}
	version, versionErr := exec.CommandContext(ctx, dispatTool, "--version").Output()
	if versionErr == nil {
		record.Dispat = dispatVersionLine(string(version))
	}
	result.BinarySHA256 = binaryDigest(dispatTool)
	status, reason := availability(settings)
	if status != "" {
		result.Status, result.Reason = status, reason
		fmt.Fprintln(log, reason)
		fmt.Fprintf(os.Stdout, "%s: %s: %s\n", cell, status, reason)
		return writeResult(output, record)
	}
	work, err := os.MkdirTemp(settings.Work, "dispat-llvm-")
	if err == nil {
		defer os.RemoveAll(work)
		err = executeExperiment(ctx, settings, work, log, &record)
	}
	if err != nil {
		result.Reason = err.Error()
		fmt.Fprintln(log, err)
		record.Checks = append(record.Checks, check{Name: result.Reason})
	}
	if err := writeResult(output, record); err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "%s: %s\n", cell, result.Status)
	if !record.Passed {
		return fmt.Errorf("LLVM experiment %s failed: %s", cell, result.Reason)
	}
	return nil
}

func dispatVersionLine(output string) string {
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "dispat ") {
			return strings.TrimSpace(line)
		}
	}
	return strings.TrimSpace(output)
}

func binaryDigest(name string) string {
	path, err := exec.LookPath(name)
	if err != nil {
		return ""
	}
	input, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer input.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, input); err != nil {
		return ""
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func writeResult(output string, record verdict) error {
	if err := writeJSON(filepath.Join(output, "verdict.json"), record); err != nil {
		return err
	}
	return writeJSON(filepath.Join(output, "observations.jsonl"), struct {
		Label    string         `json:"label"`
		Packages map[string]any `json:"packages"`
	}{Label: record.LLVM.Status, Packages: map[string]any{}})
}

func writeJSON(path string, value any) error {
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(content, '\n'), fileMode)
}

func newIdentity() (string, error) {
	identity := make([]byte, runIdentityBytes)
	if _, err := rand.Read(identity); err != nil {
		return "", err
	}
	return hex.EncodeToString(identity), nil
}

func runCommand(ctx context.Context, request command) error {
	process := makeCommand(ctx, request)
	if err := process.Run(); err != nil {
		return fmt.Errorf("%s in %s did not succeed: %w", request.Name, request.Dir, err)
	}
	return nil
}

func makeCommand(ctx context.Context, request command) *exec.Cmd {
	process := exec.CommandContext(ctx, request.Name, request.Args...)
	process.Dir = request.Dir
	process.Env = append(os.Environ(), request.Env...)
	process.Stdout, process.Stderr = request.Output, request.Output
	process.WaitDelay = commandWaitDelay
	configureProcess(process)
	return process
}
