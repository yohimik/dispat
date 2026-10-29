package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	remotePlanSchema        = "llvm-remote-plan/v1"
	remotePrepareAction     = "prepare"
	remoteStartAction       = "start"
	remoteWaitAction        = "wait"
	remoteStopAction        = "stop"
	remoteFetchAction       = "fetch"
	remoteStopTimeout       = 45 * time.Second
	taskEvidenceEnvironment = "EXPERIMENT_LLVM_EVIDENCE_DIR"
	requireGCEEnvironment   = "EXPERIMENT_LLVM_REQUIRE_GCE"
	expectedHostEnvironment = "EXPERIMENT_LLVM_EXPECTED_HOST_ID"
)

type remoteAdapter struct {
	Executable string   `json:"executable"`
	Args       []string `json:"args"`
}

type remoteNode struct {
	Name         string   `json:"name"`
	Root         string   `json:"root"`
	State        string   `json:"state"`
	EvidenceDir  string   `json:"evidenceDir"`
	GitConfig    string   `json:"gitConfig"`
	HostID       string   `json:"hostId"`
	EvidencePath string   `json:"evidencePath"`
	MachineType  *string  `json:"machineType"`
	CPUPlatform  *string  `json:"cpuPlatform"`
	CPUQuota     *float64 `json:"cpuQuota"`
	MemoryBytes  *int64   `json:"memoryBytes"`
}

type remotePlan struct {
	Schema               string        `json:"schema"`
	Orchestration        string        `json:"orchestration"`
	Adapter              remoteAdapter `json:"adapter"`
	MailboxURL           string        `json:"mailboxURL"`
	MailboxPath          string        `json:"mailboxPath"`
	ReturnEndpoint       string        `json:"returnEndpoint"`
	CoordinatorGitConfig string        `json:"coordinatorGitConfig"`
	Nodes                []remoteNode  `json:"nodes"`
}

type remoteCommand struct {
	Dir  string   `json:"dir"`
	Name string   `json:"name"`
	Args []string `json:"args"`
	Env  []string `json:"env"`
}

type remoteRequest struct {
	Action      string         `json:"action"`
	Node        string         `json:"node"`
	TrialID     string         `json:"trialId"`
	LocalRoot   string         `json:"localRoot,omitempty"`
	Command     *remoteCommand `json:"command,omitempty"`
	Destination string         `json:"destination,omitempty"`
}

type remoteResponse struct {
	ExitCode int `json:"exitCode"`
}

func readRemotePlan(path string) (*remotePlan, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var plan remotePlan
	if err := json.Unmarshal(content, &plan); err != nil {
		return nil, err
	}
	if plan.Schema != remotePlanSchema || plan.Orchestration != "native-distributed" && plan.Orchestration != manualGCPMode || !filepath.IsAbs(plan.Adapter.Executable) || len(plan.Nodes) != executorCount ||
		!strings.HasPrefix(plan.MailboxURL, "https://127.0.0.1:") || !strings.HasPrefix(plan.ReturnEndpoint, "https://127.0.0.1:") ||
		!isCloudPath(plan.MailboxPath) || !isCloudPath(plan.CoordinatorGitConfig) {
		return nil, fmt.Errorf("LLVM remote plan %s has an invalid adapter, tunnel endpoint, mailbox or executor count", path)
	}
	names, hosts, paths := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, node := range plan.Nodes {
		if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,62}$`).MatchString(node.Name) || node.HostID == "" || names[node.Name] || hosts[node.HostID] ||
			!isCloudPath(node.Root) || !isCloudPath(node.State) || !isCloudPath(node.EvidenceDir) || !isCloudPath(node.GitConfig) || !isCloudPath(node.EvidencePath) {
			return nil, fmt.Errorf("LLVM remote node %q must have unique independently evidenced host identity and isolated cloud paths", node.Name)
		}
		for _, path := range []string{node.Root, node.State, node.EvidenceDir} {
			if paths[path] {
				return nil, fmt.Errorf("LLVM remote node %s reuses execution path %s", node.Name, path)
			}
			paths[path] = true
		}
		names[node.Name], hosts[node.HostID] = true, true
		if err := verifyRemoteHostEvidence(node); err != nil {
			return nil, err
		}
	}
	return &plan, nil
}

func verifyRemoteHostEvidence(node remoteNode) error {
	content, err := os.ReadFile(node.EvidencePath)
	if err != nil {
		return err
	}
	var described struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(content, &described); err != nil {
		return err
	}
	if !regexp.MustCompile(`^[0-9]+$`).MatchString(described.ID) || node.HostID != "gce:"+described.ID || described.Status != "RUNNING" {
		return fmt.Errorf("LLVM remote node %s has no matching independently described running GCE instance", node.Name)
	}
	return nil
}

func isCloudPath(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path && strings.HasPrefix(path, "/cloud/")
}

func callRemote(ctx context.Context, plan *remotePlan, request remoteRequest, output io.Writer) (remoteResponse, error) {
	content, err := json.Marshal(request)
	if err != nil {
		return remoteResponse{}, err
	}
	var response bytes.Buffer
	process := makeCommand(ctx, command{Name: plan.Adapter.Executable, Args: plan.Adapter.Args, Input: bytes.NewReader(content), Output: output})
	process.Stdout = &response
	if err := process.Run(); err != nil {
		return remoteResponse{}, fmt.Errorf("LLVM remote adapter %s for %s failed: %w", request.Action, request.Node, err)
	}
	var result remoteResponse
	if err := json.Unmarshal(response.Bytes(), &result); err != nil {
		return result, fmt.Errorf("LLVM remote adapter %s for %s returned invalid status: %w", request.Action, request.Node, err)
	}
	if result.ExitCode != 0 {
		return result, fmt.Errorf("LLVM remote %s for %s exited with status %d", request.Action, request.Node, result.ExitCode)
	}
	return result, nil
}

func coordinatorEnvironment(settings options) []string {
	if settings.Remote == nil {
		return nil
	}
	return []string{"GIT_CONFIG_GLOBAL=" + settings.Remote.CoordinatorGitConfig}
}

func peerEnvironment(node nodeFixture, base []string) []string {
	environment := append(append([]string{}, base...), taskEvidenceEnvironment+"="+node.EvidenceDir)
	if node.Remote != nil {
		environment = append(environment, "GIT_CONFIG_GLOBAL="+node.Remote.GitConfig, requireGCEEnvironment+"=true", expectedHostEnvironment+"="+node.Remote.HostID)
	}
	return environment
}

func requireEmptyMailbox(ctx context.Context, path string) error {
	for _, query := range []struct {
		Args []string
		Want string
	}{
		{Args: []string{"--git-dir=" + path, "rev-parse", "--is-bare-repository"}, Want: "true"},
		{Args: []string{"--git-dir=" + path, "for-each-ref", "--format=%(refname)"}},
	} {
		value, err := exec.CommandContext(ctx, "git", query.Args...).Output()
		if err != nil {
			return fmt.Errorf("LLVM cloud mailbox is not a fresh bare repository: %w", err)
		}
		if strings.TrimSpace(string(value)) != query.Want {
			return fmt.Errorf("LLVM cloud mailbox %s already has refs or is not bare", path)
		}
	}
	return nil
}
