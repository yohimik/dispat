package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestKernelModulePathsNormalizeContainedKbuildEntries(t *testing.T) {
	for _, test := range []struct {
		name    string
		content string
		paths   []string
	}{
		{"observed nvdimm module", "drivers/nvdimm/../../tools/testing/nvdimm/test/iomap.ko\n", []string{"tools/testing/nvdimm/test/iomap.ko"}},
		{"object compatibility", "drivers/nvdimm/../../tools/testing/nvdimm/test/iomap.o\n", []string{"tools/testing/nvdimm/test/iomap.ko"}},
		{"preserved order", "z/../z/last.ko\n./a//first.o\n", []string{"z/last.ko", "a/first.ko"}},
		{"parent escape", "../outside.ko\n", nil},
		{"normalized parent escape", "drivers/../../outside.ko\n", nil},
		{"absolute", "/drivers/example.ko\n", nil},
		{"absolute after normalization", "/drivers/../example.ko\n", nil},
		{"invalid suffix", "drivers/example.a\n", nil},
		{"duplicate", "drivers/example.ko\ndrivers/example.ko\n", nil},
		{"normalized duplicate", "drivers/nvdimm/../../tools/testing/nvdimm/test/iomap.ko\ntools/testing/nvdimm/test/iomap.ko\n", nil},
		{"object alias duplicate", "drivers/./example.o\ndrivers/example.ko\n", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), kernelModulesOrder)
			if err := os.WriteFile(path, []byte(test.content), fileMode); err != nil {
				t.Fatal(err)
			}
			paths, err := kernelModulePaths(path)
			if test.paths == nil {
				if err == nil {
					t.Fatalf("invalid module order accepted: %q => %q", test.content, paths)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(paths, test.paths) {
				t.Fatalf("module order %q: paths=%q want=%q error=%v", test.content, paths, test.paths, err)
			}
		})
	}
}

func TestKernelModuleInventoryBindsNormalizedOrderWithoutRewritingEvidence(t *testing.T) {
	item := task{Name: "kernel-x86_64", Backend: "x86_64", Revision: kernelRevision, Profile: fullProfile}
	root, verification := kernelArtifactFixture(t, item)
	content := []byte("drivers/nvdimm/../../tools/testing/nvdimm/test/iomap.ko\n./drivers/example.o\n")
	orderPath := filepath.Join(root, kernelModulesOrder)
	if err := os.WriteFile(orderPath, content, fileMode); err != nil {
		t.Fatal(err)
	}
	before, err := describeKernelArtifact(orderPath)
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{"tools/testing/nvdimm/test/iomap.ko", "drivers/example.ko"}
	for _, path := range paths {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), directoryMode); err != nil {
			t.Fatal(err)
		}
		writeFixtureELF(t, full, item.Backend, true)
	}
	modules, err := readKernelModules(root, item)
	if err != nil {
		t.Fatal(err)
	}
	if len(modules) != len(paths) {
		t.Fatalf("inventory count=%d want=%d", len(modules), len(paths))
	}
	for index, module := range modules {
		observed, err := describeKernelArtifact(filepath.Join(root, paths[index]))
		if err != nil || module.Path != paths[index] || module.Package != item.Name || module.SHA256 != observed.SHA256 || module.Bytes != observed.Bytes {
			t.Fatalf("inventory module %d: %+v observed=%+v error=%v", index, module, observed, err)
		}
	}
	manifestPath := filepath.Join(root, kernelModulesManifest)
	manifest, err := readKernelModuleManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Modules, manifest.SampleModule = modules, paths[0]
	verification.ModuleCount, verification.SampleModule = len(modules), paths[0]
	if err := writeJSON(manifestPath, manifest); err != nil {
		t.Fatal(err)
	}
	if err := verifyKernelModuleInventory(root, item, verification); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*kernelModuleManifest)
	}{
		{"raw alias instead of normalized path", func(m *kernelModuleManifest) {
			m.Modules[0].Path = "drivers/nvdimm/../../tools/testing/nvdimm/test/iomap.ko"
		}},
		{"omitted module", func(m *kernelModuleManifest) { m.Modules = m.Modules[:1] }},
		{"duplicate module", func(m *kernelModuleManifest) { m.Modules[1] = m.Modules[0] }},
		{"reordered modules", func(m *kernelModuleManifest) { m.Modules[0], m.Modules[1] = m.Modules[1], m.Modules[0] }},
		{"wrong sample", func(m *kernelModuleManifest) { m.SampleModule = paths[1] }},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := manifest
			changed.Modules = append([]artifact(nil), manifest.Modules...)
			test.mutate(&changed)
			if err := writeJSON(manifestPath, changed); err != nil {
				t.Fatal(err)
			}
			if err := verifyKernelModuleInventory(root, item, verification); err == nil {
				t.Fatal("nonmatching module inventory accepted")
			}
		})
	}
	after, err := describeKernelArtifact(orderPath)
	if err != nil || before != after {
		t.Fatalf("module order evidence changed: before=%+v after=%+v error=%v", before, after, err)
	}
	retained, err := os.ReadFile(orderPath)
	if err != nil || string(retained) != string(content) {
		t.Fatalf("original module order bytes changed: %q error=%v", retained, err)
	}
}
