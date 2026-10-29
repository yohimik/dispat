package main

import (
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const kernelResolvedConfig = "resolved.config"
const kernelConfiguredConfig = "configured.config"
const kernelModulesManifest = "modules-manifest.json"
const kernelModulesOrder = "modules.order"
const kernelObjectFileSuffix = ".o"
const kernelModuleFileSuffix = ".ko"
const kernelParentPathPrefix = "../"

type kernelVerification struct {
	ConfigSHA256    string `json:"configSha256,omitempty"`
	KernelRelease   string `json:"kernelRelease,omitempty"`
	ModuleCount     int    `json:"moduleCount,omitempty"`
	AreModulesBuilt bool   `json:"areModulesBuilt"`
	SampleModule    string `json:"sampleModule,omitempty"`
}

type kernelModuleManifest struct {
	SourceRevision  string     `json:"sourceRevision"`
	Backend         string     `json:"backend"`
	ConfigSHA256    string     `json:"configSha256"`
	Modules         []artifact `json:"modules"`
	SampleModule    string     `json:"sampleModule"`
	OriginalVmlinux artifact   `json:"originalVmlinux"`
}

type kernelBuild struct {
	Task                task
	Root                string
	Recording           *taskRecording
	RetainConfiguration func(string, *taskRecording) error
}

func kernelCompilerPrefix(backend string) string {
	if backend == "arm64" {
		return "aarch64-linux-gnu-"
	}
	return "x86_64-linux-gnu-"
}

func kernelMakeTargets(item task) []string {
	boot := "bzImage"
	if item.Backend == "arm64" {
		boot = "Image"
	}
	if item.Profile == fullProfile {
		return []string{"vmlinux", "modules", boot}
	}
	return []string{"vmlinux", boot}
}

func kernelMakeArguments(item task, targets []string) []string {
	return append([]string{"-C", item.Source, "O=" + filepath.Join("${TASK_ROOT}", buildDirectory), "ARCH=" + item.Backend,
		"CC=" + kernelCompilerPrefix(item.Backend) + "gcc-12", "HOSTCC=gcc-12", "CROSS_COMPILE=" + kernelCompilerPrefix(item.Backend),
		"-j" + strconv.Itoa(item.Jobs)}, targets...)
}

func kernelBuildEnvironment() []string {
	return []string{"KBUILD_BUILD_USER=dispat-experiment", "KBUILD_BUILD_HOST=dispat-experiment", "KBUILD_BUILD_VERSION=1",
		"KBUILD_BUILD_TIMESTAMP=Thu Jan 1 00:00:00 UTC 1970", "SOURCE_DATE_EPOCH=0", "KCONFIG_NOTIMESTAMP=1", "LC_ALL=C"}
}

func kernelCommand(item task, root string, targets []string) command {
	args := kernelMakeArguments(item, targets)
	for index, value := range args {
		args[index] = strings.ReplaceAll(value, "${TASK_ROOT}", root)
	}
	return command{Name: "make", Args: args, Dir: root, Env: kernelBuildEnvironment(), Output: os.Stdout}
}

func runKernelBuild(ctx context.Context, request kernelBuild) (resultError error) {
	item, root, recording := request.Task, request.Root, request.Recording
	recording.Manifest.Kernel = &kernelVerification{}
	defer func() {
		if !recording.Started.IsZero() && recording.Manifest.Interval.DurationSeconds == nil {
			recording.endOperation()
		}
		retain := request.RetainConfiguration
		if retain == nil {
			retain = retainKernelConfiguration
		}
		resultError = errors.Join(resultError, retain(root, recording))
	}()
	recording.beginOperation()
	if err := runCommand(ctx, kernelCommand(item, root, []string{kernelConfiguration(item.Profile)})); err != nil {
		return err
	}
	if _, err := captureKernelConfiguration(root, recording, kernelConfiguredConfig); err != nil {
		return err
	}
	if err := runCommand(ctx, kernelCommand(item, root, kernelMakeTargets(item))); err != nil {
		return err
	}
	out := filepath.Clean(filepath.Join(root, "..", "..", artifactsDirectory, item.Name))
	if err := os.MkdirAll(out, directoryMode); err != nil {
		return err
	}
	recording.endOperation()
	if err := retainKernelConfiguration(root, recording); err != nil {
		return err
	}
	release, err := os.ReadFile(filepath.Join(root, buildDirectory, "include", "config", "kernel.release"))
	if err != nil {
		return err
	}
	recording.Manifest.Kernel.KernelRelease = strings.TrimSpace(string(release))
	if err := recording.measurePhase("artifact-staging-and-module-verification", "after build operation through debug stripping, complete module ELF inventory and hashing, metadata generation, and representative artifact copying", func() error { return stageKernelArtifacts(ctx, request, out) }); err != nil {
		return err
	}
	if err := recording.measurePhase("returned-artifact-verification", "after artifact staging through returned kernel ELF, boot-image header, configuration digest and complete module-manifest verification", func() error { return verifyKernelArtifacts(out, item, recording.Manifest.Kernel) }); err != nil {
		return err
	}
	for _, name := range workloadOutputs(kernelWorkload, item.Profile) {
		entry, err := describeKernelArtifact(filepath.Join(out, name))
		if err != nil {
			return err
		}
		entry.Package, entry.Path = item.Name, name
		recording.Manifest.Artifacts = append(recording.Manifest.Artifacts, entry)
	}
	recording.OutputPath = filepath.Join(out, manifestFileName)
	return nil
}

func retainKernelConfiguration(root string, recording *taskRecording) error {
	digest, err := captureKernelConfiguration(root, recording, kernelResolvedConfig)
	if err != nil {
		return err
	}
	if digest != "" {
		recording.Manifest.Kernel.ConfigSHA256 = digest
	}
	return nil
}

func captureKernelConfiguration(root string, recording *taskRecording, name string) (string, error) {
	destination := filepath.Join(recording.Directory, name)
	if _, err := os.Stat(destination); err == nil {
		entry, err := describeKernelArtifact(destination)
		return entry.SHA256, err
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	source := filepath.Join(root, buildDirectory, ".config")
	content, err := os.ReadFile(source)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, privateFileMode)
	if err != nil {
		return "", err
	}
	_, writeError := output.Write(content)
	if err := errors.Join(writeError, output.Close()); err != nil {
		return "", err
	}
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:]), nil
}

func kernelBootPath(item task) string {
	if item.Backend == "arm64" {
		return filepath.Join("arch", "arm64", "boot", "Image")
	}
	return filepath.Join("arch", "x86", "boot", "bzImage")
}

func stageKernelArtifacts(ctx context.Context, request kernelBuild, out string) error {
	item, root, recording := request.Task, request.Root, request.Recording
	build := filepath.Join(root, buildDirectory)
	if err := verifyConfiguredKernelELF(filepath.Join(build, "vmlinux"), item.Backend, filepath.Join(build, ".config")); err != nil {
		return err
	}
	if err := stripKernelDebug(ctx, item, filepath.Join(build, "vmlinux"), filepath.Join(out, "vmlinux.stripped")); err != nil {
		return err
	}
	for _, entry := range []struct{ Source, Name string }{{filepath.Join(build, kernelBootPath(item)), "boot-image"}, {filepath.Join(build, ".config"), ".config"}} {
		if _, err := copyArtifact(entry.Source, filepath.Join(out, entry.Name)); err != nil {
			return err
		}
	}
	if item.Profile == smokeProfile {
		return nil
	}
	modules, err := readKernelModules(build, item)
	if err != nil {
		return err
	}
	if len(modules) == 0 {
		return fmt.Errorf(kernelEmptyModulesError)
	}
	original, err := describeKernelArtifact(filepath.Join(build, "vmlinux"))
	if err != nil {
		return err
	}
	original.Path = "vmlinux"
	manifest := kernelModuleManifest{SourceRevision: item.Revision, Backend: item.Backend, ConfigSHA256: recording.Manifest.Kernel.ConfigSHA256, Modules: modules, SampleModule: modules[0].Path, OriginalVmlinux: original}
	for _, directory := range []string{out, recording.Directory} {
		if err := writeExclusiveJSON(filepath.Join(directory, kernelModulesManifest), manifest); err != nil {
			return err
		}
	}
	if _, err := copyArtifact(filepath.Join(build, kernelModulesOrder), filepath.Join(recording.Directory, kernelModulesOrder)); err != nil {
		return err
	}
	for _, name := range []string{"Module.symvers", "modules.order"} {
		if _, err := copyArtifact(filepath.Join(build, name), filepath.Join(out, name)); err != nil {
			return err
		}
	}
	if err := stripKernelDebug(ctx, item, filepath.Join(build, manifest.SampleModule), filepath.Join(out, "sample-module.ko.stripped")); err != nil {
		return err
	}
	recording.Manifest.Kernel.ModuleCount, recording.Manifest.Kernel.AreModulesBuilt, recording.Manifest.Kernel.SampleModule = len(modules), true, manifest.SampleModule
	return nil
}

func stripKernelDebug(ctx context.Context, item task, source, destination string) error {
	return runCommand(ctx, command{Name: kernelCompilerPrefix(item.Backend) + "objcopy", Args: []string{"--strip-debug", source, destination}, Output: os.Stdout})
}

func describeKernelArtifact(path string) (artifact, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return artifact{}, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 {
		return artifact{}, fmt.Errorf(kernelArtifactFileError, path)
	}
	file, err := os.Open(path)
	if err != nil {
		return artifact{}, err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return artifact{}, err
	}
	return artifact{SHA256: hex.EncodeToString(hash.Sum(nil)), Bytes: info.Size()}, nil
}

func readKernelModules(root string, item task) ([]artifact, error) {
	paths, err := kernelModulePaths(filepath.Join(root, "modules.order"))
	if err != nil {
		return nil, err
	}
	modules := []artifact{}
	for _, path := range paths {
		full := filepath.Join(root, path)
		if err := verifyKernelELF(full, item.Backend, elf.ET_REL); err != nil {
			return nil, err
		}
		entry, err := describeKernelArtifact(full)
		if err != nil {
			return nil, err
		}
		entry.Path, entry.Package = path, item.Name
		modules = append(modules, entry)
	}
	return modules, nil
}

func kernelModulePaths(path string) ([]string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	paths := []string{}
	seen := map[string]struct{}{}
	for _, entry := range strings.Fields(string(content)) {
		normalized := filepath.Clean(entry)
		if strings.HasSuffix(normalized, kernelObjectFileSuffix) {
			normalized = strings.TrimSuffix(normalized, kernelObjectFileSuffix) + kernelModuleFileSuffix
		}
		_, isRepeated := seen[normalized]
		if !strings.HasSuffix(normalized, kernelModuleFileSuffix) || filepath.IsAbs(normalized) || strings.HasPrefix(normalized, kernelParentPathPrefix) || isRepeated {
			return nil, fmt.Errorf(kernelModuleOrderError, entry)
		}
		seen[normalized] = struct{}{}
		paths = append(paths, normalized)
	}
	return paths, nil
}

func readKernelModuleManifest(path string) (kernelModuleManifest, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return kernelModuleManifest{}, err
	}
	var manifest kernelModuleManifest
	err = json.Unmarshal(content, &manifest)
	return manifest, err
}
