package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type peerProcesses struct {
	Cancel context.CancelFunc
	Wait   sync.WaitGroup
}

func executeExperiment(ctx context.Context, settings options, work string, output io.Writer, record *verdict) error {
	record.LLVM.ToolVersions = captureToolVersions(ctx, settings)
	if err := verifySource(ctx, settings.Source); err != nil {
		return err
	}
	data, err := prepareFixture(ctx, settings, work, output)
	if err != nil {
		return err
	}
	secret, err := newIdentity()
	if err != nil {
		return err
	}
	environment := []string{secretEnvironmentName + "=" + secret, "DISPAT_UPDATE_CHECK=0", "NX_NO_CLOUD=false",
		"NX_DAEMON=false", "NX_TUI=false", "NX_AGENT_NAME=", "CI=true", "NX_CI_EXECUTION_ID=" + data.Run,
		"NX_BRANCH=llvm-" + data.Run, "NX_CLOUD_DISTRIBUTED_EXECUTION=true", "NX_CACHE_DIRECTORY=.nx/cache",
		"NX_CLOUD_FORCE_REVALIDATE=false"}
	if settings.Tool != dispatTool {
		if err := prepareCloudClients(ctx, data, environment, output, record.LLVM.ToolVersions); err != nil {
			return err
		}
	}
	started := time.Now()
	if settings.Tool != dispatTool {
		defer stopCloudAgents(data.Root, environment, output)
	}
	peers, err := startPeers(ctx, settings, data, environment, output)
	if peers != nil {
		defer peers.stop()
	}
	if err != nil {
		return err
	}
	request := coordinatorCommand(settings, data, environment, output)
	err = runCommand(ctx, request)
	exitCode := 0
	if err != nil {
		exitCode = 1
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			exitCode = exit.ExitCode()
		}
	}
	record.Steps = append(record.Steps, step{Name: buildScriptName, Exit: exitCode, Seconds: time.Since(started).Seconds()})
	if err != nil {
		return err
	}
	if err := collectArtifacts(ctx, data, record.LLVM); err != nil {
		return err
	}
	seconds := time.Since(started).Seconds()
	record.LLVM.Status, record.LLVM.Seconds = completedStatus, &seconds
	record.Passed = true
	record.Checks = append(record.Checks, check{Name: "both cold LLVM builds completed on distinct executors and returned verified runnable artifacts", OK: true})
	return nil
}

func captureToolVersions(ctx context.Context, settings options) map[string]string {
	versions := map[string]string{}
	for _, name := range []string{dispatTool, "cmake", "ninja", "c++", "node"} {
		content, err := exec.CommandContext(ctx, name, "--version").Output()
		if err == nil {
			first, _, _ := strings.Cut(strings.TrimSpace(string(content)), "\n")
			if name == dispatTool {
				first = dispatVersionLine(string(content))
			}
			versions[name] = first
		}
	}
	for _, name := range []string{nxTool, lernaTool, "@changesets/cli"} {
		content, err := os.ReadFile(filepath.Join(settings.Tools, name, "package.json"))
		if err != nil {
			continue
		}
		var manifest struct {
			Version string `json:"version"`
		}
		if json.Unmarshal(content, &manifest) == nil {
			versions[name] = manifest.Version
		}
	}
	return versions
}

func coordinatorCommand(settings options, data fixture, environment []string, output io.Writer) command {
	request := command{Dir: data.Root, Env: environment, Output: output, Name: settings.Tool}
	switch settings.Tool {
	case dispatTool:
		request.Args = []string{"run", buildScriptName, "--since", "all", "--distribution-mode", settings.Mode,
			"--return-endpoint", returnEndpoint, "--log-format", "json"}
	case nxTool:
		request.Args = []string{"run-many", "-t", buildScriptName, "--parallel=1", "--agents", "--output-style=stream"}
	case lernaTool:
		request.Args = []string{"run", buildScriptName, "--concurrency=1", "--stream"}
	}
	return request
}

func startPeers(ctx context.Context, settings options, data fixture, environment []string, output io.Writer) (*peerProcesses, error) {
	peerContext, cancel := context.WithCancel(ctx)
	peers := &peerProcesses{Cancel: cancel}
	if settings.Tool != dispatTool {
		if err := runCommand(ctx, command{Dir: data.Root, Name: nxTool,
			Args: []string{"start-ci-run", "--distribute-on=manual", "--use-dte-by-default=true", "--stop-agents-on-failure=true"},
			Env:  environment, Output: output}); err != nil {
			return peers, err
		}
	}
	for _, node := range data.Nodes {
		request := command{Dir: node.Root, Env: environment, Output: output, Name: dispatTool,
			Args: []string{"choreographer", "--state-dir", node.State, "--idle-timeout", strconv.Itoa(workerIdleTimeoutSeconds)},
		}
		if settings.Tool != dispatTool {
			request.Name, request.Args = nxTool, []string{"start-agent"}
			request.Env = append(append([]string{}, environment...), "NX_AGENT_NAME="+node.Name)
		}
		process := makeCommand(peerContext, request)
		if err := process.Start(); err != nil {
			return peers, fmt.Errorf("LLVM executor %s could not start: %w", node.Name, err)
		}
		peers.Wait.Go(func() { _ = process.Wait() })
	}
	return peers, nil
}

func (peers *peerProcesses) stop() {
	peers.Cancel()
	peers.Wait.Wait()
}

func stopCloudAgents(root string, environment []string, output io.Writer) {
	ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	if err := runCommand(ctx, command{Dir: root, Name: nxTool, Args: []string{"stop-all-agents"}, Env: environment, Output: output}); err != nil {
		fmt.Fprintln(output, err)
	}
}

func collectArtifacts(ctx context.Context, data fixture, result *buildResult) error {
	nodes := map[string]bool{}
	for _, expected := range data.Tasks {
		root := filepath.Join(data.Root, artifactsDirectory, expected.Name)
		content, err := os.ReadFile(filepath.Join(root, manifestFileName))
		if err != nil {
			return err
		}
		var manifest taskManifest
		if err := json.Unmarshal(content, &manifest); err != nil {
			return err
		}
		if !reflect.DeepEqual(manifest.Task, expected) {
			return fmt.Errorf("LLVM artifact manifest for %s does not match this cold run's pinned task", expected.Name)
		}
		if !isKnownNode(data, manifest.Node) {
			return fmt.Errorf("LLVM task %s executed outside the named peer pool: %q", expected.Name, manifest.Node)
		}
		if len(manifest.Artifacts) != len(outputTools(expected.Profile)) {
			return fmt.Errorf("LLVM task %s returned an incomplete artifact manifest", expected.Name)
		}
		seen := map[string]bool{}
		for _, entry := range manifest.Artifacts {
			if entry.Package != expected.Name || !isExpectedTool(expected.Profile, entry.Path) || seen[entry.Path] {
				return fmt.Errorf("LLVM artifact %q in %s is unexpected or duplicated", entry.Path, expected.Name)
			}
			seen[entry.Path] = true
			if err := verifyArtifact(root, entry); err != nil {
				return err
			}
			result.Artifacts = append(result.Artifacts, entry)
		}
		if err := verifyBinaries(ctx, root, expected); err != nil {
			return err
		}
		nodes[manifest.Node] = true
	}
	if len(nodes) != executorCount {
		return fmt.Errorf("LLVM builds used %d distinct executors, expected %d; result is not a two-node distributed measurement", len(nodes), executorCount)
	}
	for node := range nodes {
		result.Nodes = append(result.Nodes, node)
	}
	sort.Strings(result.Nodes)
	return nil
}

func isKnownNode(data fixture, name string) bool {
	for _, node := range data.Nodes {
		if node.Name == name {
			return true
		}
	}
	return false
}

func isExpectedTool(profile, name string) bool {
	for _, tool := range outputTools(profile) {
		if name == tool {
			return true
		}
	}
	return false
}

func verifyArtifact(root string, entry artifact) error {
	path := filepath.Join(root, entry.Path)
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() != entry.Bytes || info.Mode().Perm()&0111 == 0 {
		return fmt.Errorf("LLVM artifact %s has an invalid type, size or executable mode", path)
	}
	input, err := os.Open(path)
	if err != nil {
		return err
	}
	defer input.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, input); err != nil {
		return err
	}
	if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), entry.SHA256) {
		return fmt.Errorf("LLVM artifact %s has a different SHA-256 digest from its executor's manifest", path)
	}
	return nil
}
