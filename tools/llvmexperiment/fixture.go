package main

import (
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	packageVersion           = "1.0.0"
	fixtureName              = "llvm-distributed-experiment"
	buildScript              = "llvmexperiment build"
	buildScriptName          = "build"
	choreographerPlacement   = "choreographer"
	orchestratorPlacement    = "orchestrator"
	versioningNone           = "none"
	fixtureCommitMessage     = "chore: prepare LLVM distributed build"
	returnEndpoint           = "https://127.0.0.1:18443"
	preflightTimeoutSeconds  = 120
	transferTimeoutSeconds   = 600
	transferMaximumBytes     = 1 << 30
	transferMaximumFiles     = 1000
	workerIdleTimeoutSeconds = 14400
)

type task struct {
	Name     string `json:"name"`
	Backend  string `json:"backend"`
	Profile  string `json:"profile"`
	Source   string `json:"source"`
	Revision string `json:"revision"`
	Run      string `json:"run"`
	Jobs     int    `json:"jobs"`
}

type packageManifest struct {
	Name       string                `json:"name"`
	Version    string                `json:"version"`
	Private    bool                  `json:"private"`
	Workspaces []string              `json:"workspaces,omitempty"`
	Scripts    map[string]string     `json:"scripts,omitempty"`
	NX         *projectConfiguration `json:"nx,omitempty"`
}

type projectConfiguration struct {
	Targets map[string]targetConfiguration `json:"targets"`
}

type targetConfiguration struct {
	Cache   bool     `json:"cache"`
	Inputs  []string `json:"inputs"`
	Outputs []string `json:"outputs"`
}

type nxConfiguration struct {
	CloudID        string                         `json:"nxCloudId,omitempty"`
	Parallel       int                            `json:"parallel"`
	TargetDefaults map[string]targetConfiguration `json:"targetDefaults"`
}

type dispatConfiguration struct {
	Concurrency int                      `yaml:"concurrency"`
	Versioning  string                   `yaml:"versioning,omitempty"`
	RunOnly     []string                 `yaml:"runOnly,omitempty"`
	Packages    map[string]dispatPackage `yaml:"packages,omitempty"`
	RunOutputs  map[string][]string      `yaml:"runOutputs,omitempty"`
	Execution   executionConfiguration   `yaml:"execution"`
	LogFormat   string                   `yaml:"logFormat"`
}

type dispatPackage struct {
	Path    string            `yaml:"path"`
	Scripts map[string]string `yaml:"scripts"`
}

type executionConfiguration struct {
	Name           string                 `yaml:"name"`
	Endpoint       string                 `yaml:"endpoint,omitempty"`
	Concurrency    int                    `yaml:"concurrency"`
	SecretEnv      string                 `yaml:"secretEnv"`
	Choreographers []nodeConfiguration    `yaml:"choreographers,omitempty"`
	Timeouts       *timeoutConfiguration  `yaml:"timeouts,omitempty"`
	Transfer       *transferConfiguration `yaml:"transfer,omitempty"`
}

type nodeConfiguration struct {
	Name     string `yaml:"name"`
	Endpoint string `yaml:"endpoint"`
}

type timeoutConfiguration struct {
	Preflight int `yaml:"preflight"`
	Task      int `yaml:"task"`
}

type transferConfiguration struct {
	Timeout  int   `yaml:"timeout"`
	MaxBytes int64 `yaml:"maxBytes"`
	MaxFiles int   `yaml:"maxFiles"`
}

type fixture struct {
	Root   string
	Origin string
	Run    string
	Tasks  []task
	Nodes  []nodeFixture
}

type nodeFixture struct {
	Name  string
	Root  string
	State string
}

func prepareFixture(ctx context.Context, settings options, work string, output io.Writer) (fixture, error) {
	identity, err := newIdentity()
	if err != nil {
		return fixture{}, err
	}
	result := fixture{Root: filepath.Join(work, "coordinator"), Origin: filepath.Join(work, "origin.git"), Run: identity}
	if err := os.MkdirAll(result.Root, directoryMode); err != nil {
		return result, err
	}
	for _, backend := range []string{"X86", "AArch64"} {
		name := "llvm-" + strings.ToLower(backend)
		item := task{Name: name, Backend: backend, Profile: settings.Profile, Source: settings.Source,
			Revision: llvmRevision, Run: identity, Jobs: settings.Jobs}
		if err := writePackage(result.Root, item); err != nil {
			return result, err
		}
		result.Tasks = append(result.Tasks, item)
	}
	for i := range executorCount {
		name := nodeNamePrefix + strconv.Itoa(i+1)
		result.Nodes = append(result.Nodes, nodeFixture{Name: name, Root: filepath.Join(work, name), State: filepath.Join(work, name+"-state")})
	}
	if err := writeFixtureConfiguration(result, settings); err != nil {
		return result, err
	}
	for _, args := range [][]string{
		{"init", "-q", "--initial-branch=main"},
		{"config", "user.name", "LLVM experiment"},
		{"config", "user.email", "experiments@dispat.dev"},
		{"add", "."},
		{"commit", "-qm", fixtureCommitMessage},
		{"clone", "--bare", result.Root, result.Origin},
		{"remote", "add", "origin", result.Origin},
		{"fetch", "-q", "origin"},
	} {
		if err := runCommand(ctx, command{Dir: result.Root, Name: "git", Args: args, Output: output}); err != nil {
			return result, err
		}
	}
	if settings.Tool == dispatTool {
		return result, prepareDispatNodes(result)
	}
	for _, node := range result.Nodes {
		if err := runCommand(ctx, command{Dir: work, Name: "git", Args: []string{"clone", "-q", result.Origin, node.Root}, Output: output}); err != nil {
			return result, err
		}
		if err := os.Symlink(settings.Tools, filepath.Join(node.Root, "node_modules")); err != nil {
			return result, err
		}
	}
	return result, os.Symlink(settings.Tools, filepath.Join(result.Root, "node_modules"))
}

func writePackage(root string, item task) error {
	path := filepath.Join(root, "packages", item.Name)
	if err := os.MkdirAll(path, directoryMode); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(path, taskFileName), item); err != nil {
		return err
	}
	return writeJSON(filepath.Join(path, "package.json"), packageManifest{
		Name: item.Name, Version: packageVersion, Private: true,
		Scripts: map[string]string{buildScriptName: buildScript},
		NX: &projectConfiguration{Targets: map[string]targetConfiguration{buildScriptName: {
			Cache: true, Inputs: []string{"{projectRoot}/**/*"},
			Outputs: []string{"{workspaceRoot}/artifacts/" + item.Name},
		}}},
	})
}

func writeFixtureConfiguration(data fixture, settings options) error {
	if err := os.WriteFile(filepath.Join(data.Root, ".gitignore"), []byte("node_modules/\n.nx/\nartifacts/\n**/build/\n"), fileMode); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(data.Root, "package.json"), packageManifest{
		Name: fixtureName, Version: packageVersion, Private: true, Workspaces: []string{"packages/*"},
	}); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(data.Root, "nx.json"), nxConfiguration{
		CloudID: os.Getenv(cloudIDEnvironmentName), Parallel: 1,
		TargetDefaults: map[string]targetConfiguration{buildScriptName: {Cache: true, Inputs: []string{"{projectRoot}/**/*"}, Outputs: []string{"{workspaceRoot}/artifacts/{projectName}"}}},
	}); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(data.Root, "lerna.json"), struct {
		Version string `json:"version"`
		UseNx   bool   `json:"useNx"`
	}{Version: "independent", UseNx: true}); err != nil {
		return err
	}
	config := dispatConfiguration{Concurrency: executorCount, Versioning: versioningNone,
		RunOnly:  []string{choreographerPlacement, orchestratorPlacement},
		Packages: map[string]dispatPackage{}, RunOutputs: map[string][]string{buildScriptName: {artifactsDirectory}},
		LogFormat: "json", Execution: executionConfiguration{Name: coordinatorName, Concurrency: 1,
			SecretEnv: secretEnvironmentName, Timeouts: &timeoutConfiguration{
				Preflight: preflightTimeoutSeconds, Task: int(math.Ceil(float64(settings.Timeout) / float64(time.Second)))},
			Transfer: &transferConfiguration{Timeout: transferTimeoutSeconds, MaxBytes: transferMaximumBytes, MaxFiles: transferMaximumFiles}},
	}
	for _, item := range data.Tasks {
		config.Packages[item.Name] = dispatPackage{Path: "packages/" + item.Name, Scripts: map[string]string{buildScriptName: buildScript}}
	}
	for _, node := range data.Nodes {
		config.Execution.Choreographers = append(config.Execution.Choreographers, nodeConfiguration{Name: node.Name, Endpoint: "file://" + data.Origin})
	}
	return writeYAML(filepath.Join(data.Root, "dispat.yaml"), config)
}

func prepareDispatNodes(data fixture) error {
	for _, node := range data.Nodes {
		if err := os.MkdirAll(node.Root, directoryMode); err != nil {
			return err
		}
		config := dispatConfiguration{Concurrency: 1, LogFormat: "json", Execution: executionConfiguration{
			Name: node.Name, Endpoint: "file://" + data.Origin, Concurrency: 1, SecretEnv: secretEnvironmentName,
		}}
		if err := writeYAML(filepath.Join(node.Root, "dispat.yaml"), config); err != nil {
			return err
		}
	}
	return nil
}

func writeYAML(path string, value any) error {
	content, err := yaml.Marshal(value)
	if err != nil {
		return fmt.Errorf("LLVM fixture configuration is invalid: %w", err)
	}
	return os.WriteFile(path, content, fileMode)
}
