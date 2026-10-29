package main

import (
	"bytes"
	"debug/elf"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func verifyKernelELF(path, backend string, wantType elf.Type) error {
	file, err := elf.Open(path)
	if err != nil {
		return fmt.Errorf(kernelELFReadError, path, err)
	}
	defer file.Close()
	machine := elf.EM_X86_64
	if backend == "arm64" {
		machine = elf.EM_AARCH64
	}
	if file.Class != elf.ELFCLASS64 || file.Machine != machine || file.Type != wantType {
		return fmt.Errorf(kernelELFIdentityError, path, file.Class, file.Machine, file.Type, machine, wantType)
	}
	return nil
}

func verifyConfiguredKernelELF(path, backend, configurationPath string) error {
	configuration, err := os.ReadFile(configurationPath)
	if err != nil {
		return err
	}
	wantType := elf.ET_EXEC
	if backend == "arm64" && strings.Contains("\n"+string(configuration), "\nCONFIG_RELOCATABLE=y\n") {
		// Pinned arch/arm64/Makefile links relocatable kernels with -shared.
		wantType = elf.ET_DYN
	}
	return verifyKernelELF(path, backend, wantType)
}

func verifyKernelBootImage(path, backend string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	if backend == "arm64" {
		magic := make([]byte, 4)
		if _, err := file.ReadAt(magic, 0x38); err != nil {
			return err
		}
		if !bytes.Equal(magic, []byte{'A', 'R', 'M', 0x64}) {
			return fmt.Errorf(kernelArmBootError)
		}
		return nil
	}
	header := make([]byte, 8)
	if _, err := file.ReadAt(header, 0x1fe); err != nil {
		return err
	}
	if header[0] != 0x55 || header[1] != 0xaa || !bytes.Equal(header[4:8], []byte("HdrS")) {
		return fmt.Errorf(kernelX86BootError)
	}
	return nil
}

func verifyKernelArtifacts(root string, item task, verification *kernelVerification) error {
	if verification == nil || !regexp.MustCompile(`^6\.18\.59(?:[-+].*)?$`).MatchString(verification.KernelRelease) {
		return fmt.Errorf(kernelReleaseError)
	}
	if err := verifyConfiguredKernelELF(filepath.Join(root, "vmlinux.stripped"), item.Backend, filepath.Join(root, ".config")); err != nil {
		return err
	}
	if err := verifyKernelBootImage(filepath.Join(root, "boot-image"), item.Backend); err != nil {
		return err
	}
	config, err := describeKernelArtifact(filepath.Join(root, ".config"))
	if err != nil {
		return err
	}
	if verification.ConfigSHA256 != config.SHA256 {
		return fmt.Errorf(kernelReturnedConfigError)
	}
	configuration, err := os.ReadFile(filepath.Join(root, ".config"))
	if err != nil {
		return err
	}
	arch := "CONFIG_X86_64=y"
	if item.Backend == "arm64" {
		arch = "CONFIG_ARM64=y"
	}
	if !strings.Contains("\n"+string(configuration), "\n"+arch+"\n") {
		return fmt.Errorf(kernelConfigArchitectureError, arch)
	}
	if item.Profile == smokeProfile {
		if verification.AreModulesBuilt || verification.ModuleCount != 0 {
			return fmt.Errorf(kernelSmokeVerificationError)
		}
		return nil
	}
	if !verification.AreModulesBuilt || verification.ModuleCount < 1 {
		return fmt.Errorf(kernelFullModulesError)
	}
	if err := verifyKernelELF(filepath.Join(root, "sample-module.ko.stripped"), item.Backend, elf.ET_REL); err != nil {
		return err
	}
	if _, err := describeKernelArtifact(filepath.Join(root, "Module.symvers")); err != nil {
		return err
	}
	return verifyKernelModuleInventory(root, item, verification)
}

func verifyKernelModuleInventory(root string, item task, verification *kernelVerification) error {
	paths, err := kernelModulePaths(filepath.Join(root, kernelModulesOrder))
	if err != nil {
		return err
	}
	manifest, err := readKernelModuleManifest(filepath.Join(root, "modules-manifest.json"))
	if err != nil {
		return err
	}
	if !verification.AreModulesBuilt || len(paths) == 0 || manifest.SourceRevision != item.Revision || manifest.Backend != item.Backend || manifest.ConfigSHA256 != verification.ConfigSHA256 || len(manifest.Modules) != verification.ModuleCount || len(paths) != len(manifest.Modules) || manifest.SampleModule != verification.SampleModule || manifest.SampleModule != paths[0] {
		return fmt.Errorf(kernelModuleManifestError)
	}
	if manifest.OriginalVmlinux.Path != "vmlinux" || manifest.OriginalVmlinux.Bytes <= 0 || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(manifest.OriginalVmlinux.SHA256) {
		return fmt.Errorf(kernelOriginalVmlinuxError)
	}
	for index, module := range manifest.Modules {
		if module.Path != paths[index] || module.Package != item.Name || module.Bytes <= 0 || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(module.SHA256) {
			return fmt.Errorf(kernelModuleEntryError, index)
		}
	}
	return nil
}
