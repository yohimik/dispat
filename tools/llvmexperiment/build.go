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
	"strconv"
	"strings"
	"time"

	measurement "github.com/yohimik/dispat/tools/internal/executionmeasurement"
)

const (
	llvmDemangleTool = "llvm-cxxfilt"
	llvmSizeTool     = "llvm-size"
	llvmCodegenTool  = "llc"
	llvmOptimizeTool = "opt"
	llvmConfigTool   = "llvm-config"
	allBuildTarget   = "all"
	mangledExample   = "_Z3foov"
	demangledExample = "foo()"
)

type artifact struct {
	Package string `json:"package"`
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
	Bytes   int64  `json:"bytes"`
}

type taskManifest struct {
	Task               task                     `json:"task"`
	Node               string                   `json:"node"`
	Started            time.Time                `json:"started"`
	Seconds            float64                  `json:"seconds"`
	Artifacts          []artifact               `json:"artifacts"`
	Status             string                   `json:"status"`
	Reason             string                   `json:"reason,omitempty"`
	AttemptID          string                   `json:"attemptId"`
	StartedClockKind   string                   `json:"startedClockKind"`
	Host               hostIdentity             `json:"host"`
	Interval           measurement.TaskInterval `json:"interval"`
	ConfigureArguments []string                 `json:"configureArguments"`
	BuildArguments     []string                 `json:"buildArguments"`
}

func buildTargets(profile string) []string {
	if profile == fullProfile {
		return []string{allBuildTarget}
	}
	return []string{llvmDemangleTool, llvmSizeTool}
}

func outputTools(profile string) []string {
	if profile == fullProfile {
		return []string{llvmDemangleTool, llvmSizeTool, llvmCodegenTool, llvmOptimizeTool, llvmConfigTool}
	}
	return []string{llvmDemangleTool, llvmSizeTool}
}

func buildTask(ctx context.Context) (resultError error) {
	content, err := os.ReadFile(taskFileName)
	if err != nil {
		return err
	}
	var item task
	if err := json.Unmarshal(content, &item); err != nil {
		return err
	}
	if err := validateTask(item); err != nil {
		return err
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	recording, err := beginTaskRecording(ctx, item, root)
	if err != nil {
		return err
	}
	defer func() { resultError = errors.Join(resultError, recording.finish(resultError)) }()
	if err := verifySource(ctx, item.Source); err != nil {
		return err
	}
	if err := os.Mkdir(filepath.Join(root, buildDirectory), directoryMode); err != nil {
		return fmt.Errorf("LLVM build directory must be new and empty: %w", err)
	}
	recording.beginOperation()
	configure := configureArguments(item)
	if err := runCommand(ctx, command{Dir: root, Name: "cmake", Args: configure, Output: os.Stdout}); err != nil {
		return err
	}
	if err := runCommand(ctx, command{Dir: root, Name: "cmake", Args: buildArguments(item), Output: os.Stdout}); err != nil {
		return err
	}
	out := filepath.Clean(filepath.Join(root, "..", "..", artifactsDirectory, item.Name))
	if err := os.MkdirAll(out, directoryMode); err != nil {
		return err
	}
	recording.endOperation()
	for _, name := range outputTools(item.Profile) {
		entry, err := copyArtifact(filepath.Join(root, buildDirectory, "bin", name), filepath.Join(out, name))
		if err != nil {
			return err
		}
		entry.Package, entry.Path = item.Name, name
		recording.Manifest.Artifacts = append(recording.Manifest.Artifacts, entry)
	}
	if err := verifyBinaries(ctx, out, item); err != nil {
		return err
	}
	recording.OutputPath = filepath.Join(out, manifestFileName)
	return nil
}

func configureArguments(item task) []string {
	return []string{"-S", filepath.Join(item.Source, "llvm"), "-B", buildDirectory, "-G", "Ninja",
		"-DCMAKE_BUILD_TYPE=Release", "-DLLVM_TARGETS_TO_BUILD=" + item.Backend,
		"-DLLVM_ENABLE_PROJECTS=", "-DLLVM_ENABLE_RUNTIMES=", "-DLLVM_INCLUDE_TESTS=OFF",
		"-DLLVM_INCLUDE_EXAMPLES=OFF", "-DLLVM_INCLUDE_BENCHMARKS=OFF", "-DLLVM_INCLUDE_DOCS=OFF",
		"-DLLVM_ENABLE_ASSERTIONS=OFF", "-DLLVM_ENABLE_ZLIB=OFF", "-DLLVM_ENABLE_ZSTD=OFF",
		"-DLLVM_ENABLE_LIBXML2=OFF", "-DLLVM_ENABLE_TERMINFO=OFF", "-DLLVM_PARALLEL_LINK_JOBS=1",
		"-DCMAKE_C_COMPILER_LAUNCHER=", "-DCMAKE_CXX_COMPILER_LAUNCHER="}
}

func buildArguments(item task) []string {
	build := []string{"--build", buildDirectory, "--parallel", strconv.Itoa(item.Jobs), "--target"}
	return append(build, buildTargets(item.Profile)...)
}

func validateTask(item task) error {
	if item.Revision != llvmRevision || item.Run == "" || item.Jobs < 1 || item.Jobs > maximumJobsCount {
		return errors.New("LLVM task has an invalid pinned revision, run identity, or Ninja job count")
	}
	if item.Profile != fullProfile && item.Profile != smokeProfile {
		return fmt.Errorf("LLVM task profile %q is invalid", item.Profile)
	}
	if item.Backend != "X86" && item.Backend != "AArch64" {
		return fmt.Errorf("LLVM backend %q is invalid", item.Backend)
	}
	if item.Name != "llvm-"+strings.ToLower(item.Backend) || !filepath.IsAbs(item.Source) {
		return errors.New("LLVM task package name or absolute source path is invalid")
	}
	return nil
}

func verifySource(ctx context.Context, source string) error {
	for _, query := range []struct {
		Args []string
		Want string
	}{
		{Args: []string{"-C", source, "rev-parse", "HEAD"}, Want: llvmRevision},
		{Args: []string{"-C", source, "status", "--porcelain", "--untracked-files=normal"}},
	} {
		value, err := exec.CommandContext(ctx, "git", append([]string{"-c", "safe.directory=" + source}, query.Args...)...).Output()
		if err != nil {
			return fmt.Errorf("LLVM source checkout %s cannot be verified: %w", source, err)
		}
		if strings.TrimSpace(string(value)) != query.Want {
			return fmt.Errorf("LLVM source checkout %s is not the clean pinned revision %s", source, llvmRevision)
		}
	}
	return nil
}

func taskNode() string {
	if node := os.Getenv("DISPAT_EXECUTION_NODE"); node != "" {
		return node
	}
	return os.Getenv("NX_AGENT_NAME")
}

func copyArtifact(source, destination string) (artifact, error) {
	input, err := os.Open(source)
	if err != nil {
		return artifact{}, err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, executableFileMode)
	if err != nil {
		return artifact{}, err
	}
	hash := sha256.New()
	size, copyErr := io.Copy(io.MultiWriter(output, hash), input)
	closeErr := output.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return artifact{}, err
	}
	return artifact{SHA256: hex.EncodeToString(hash.Sum(nil)), Bytes: size}, nil
}

func verifyBinaries(ctx context.Context, root string, item task) error {
	for _, name := range outputTools(item.Profile) {
		result, err := exec.CommandContext(ctx, filepath.Join(root, name), "--version").CombinedOutput()
		if err != nil {
			return fmt.Errorf("LLVM artifact %s does not execute as version %s: %w", name, llvmVersion, err)
		}
		if !strings.Contains(string(result), llvmVersion) {
			return fmt.Errorf("LLVM artifact %s reports a different version from %s", name, llvmVersion)
		}
	}
	result, err := exec.CommandContext(ctx, filepath.Join(root, llvmDemangleTool), mangledExample).Output()
	if err != nil {
		return fmt.Errorf("LLVM demangler did not produce %s: %w", demangledExample, err)
	}
	if strings.TrimSpace(string(result)) != demangledExample {
		return fmt.Errorf("LLVM demangler result differs from %s", demangledExample)
	}
	return nil
}
