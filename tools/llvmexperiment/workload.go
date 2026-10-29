package main

import "strings"

const (
	llvmWorkload            = "llvm"
	kernelWorkload          = "linux-kernel"
	kernelVersion           = "6.18.59"
	kernelRevision          = "1253a6aa5d104312bf0992afacc15220ae35bdf9"
	kernelSourcePath        = "/opt/linux"
	kernelOperation         = "configure-and-build-kernel-targets"
	kernelOperationBoundary = "before GNU make configuration through configured .config evidence capture, requested kernel/modules/architecture boot-image targets and output-directory creation; excludes source verification, final configuration retention, staging, stripping, module hashing, artifact verification and transport"
)

type workloadDescriptor struct {
	Name        string
	Version     string
	Revision    string
	Source      string
	Backends    []string
	Description string
	Operation   string
	Boundary    string
}

func describeWorkload(name string) workloadDescriptor {
	if name == kernelWorkload {
		return workloadDescriptor{Name: kernelWorkload, Version: kernelVersion, Revision: kernelRevision, Source: kernelSourcePath, Backends: []string{"x86_64", "arm64"}, Description: "two independent Linux kernel configurations for x86_64 and arm64", Operation: kernelOperation, Boundary: kernelOperationBoundary}
	}
	return workloadDescriptor{Name: llvmWorkload, Version: llvmVersion, Revision: llvmRevision, Source: defaultSourcePath, Backends: []string{"X86", "AArch64"}, Description: "two independent LLVM core configurations enabling X86 and AArch64", Operation: taskOperation, Boundary: taskOperationBoundary}
}

func workloadTargets(workload, profile string) []string {
	if workload != kernelWorkload {
		return buildTargets(profile)
	}
	if profile == fullProfile {
		return []string{"vmlinux", "modules", "architecture-boot-image"}
	}
	return []string{"vmlinux", "architecture-boot-image"}
}

func workloadOutputs(workload, profile string) []string {
	if workload != kernelWorkload {
		return outputTools(profile)
	}
	base := []string{"vmlinux.stripped", "boot-image", ".config"}
	if profile == fullProfile {
		return append(base, "Module.symvers", "modules.order", "modules-manifest.json", "sample-module.ko.stripped")
	}
	return base
}

func taskName(workload, backend string) string {
	if workload == kernelWorkload {
		return "kernel-" + backend
	}
	return "llvm-" + strings.ToLower(backend)
}

func kernelConfiguration(profile string) string {
	if profile == fullProfile {
		return "allmodconfig"
	}
	return "defconfig"
}

func kernelOmissions(profile string) []string {
	values := []string{"Boot/runtime tests, selftests, documentation, userspace tools and device-tree blobs are outside this workload.",
		"The GCC-only toolchain has no Rust compiler; the retained resolved .config defines enabled features.",
		"GCC plugin development headers are not installed, so Kconfig disables GCC plugins.",
		"No 32-bit ARM compatibility compiler is configured, so Kconfig disables the arm64 compatibility vDSO."}
	if profile == smokeProfile {
		return append(values, "Smoke uses defconfig and builds no modules; it is not the full allmodconfig workload.", "Returned vmlinux is stripped of debug sections after the build.")
	}
	return append(values, "Return artifacts contain stripped-debug vmlinux and one stripped-debug representative module; all selected unstripped modules are built and hashed before cleanup.")
}

func (record *verdict) buildResult() *buildResult {
	if record.LinuxKernel != nil {
		return record.LinuxKernel
	}
	return record.LLVM
}

func workloadProtocol(workload string) string {
	if workload == kernelWorkload {
		return "linux-kernel-two-architecture-requests/v1-retained-monotonic-attempts"
	}
	return llvmProtocolRevision
}
