package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/yohimik/dispat/services/dispat/internal/model"
)

var benchPackageCounts = []int{100, 500}

// benchWorkspacePackages is a workspace whose packages share one space, as a
// root-level setting makes them: every package inherits the same aliasTags and
// buildOutputs. The folders carry upper-case bytes, as every macOS home
// directory does, because that is the input folding allocates for.
func benchWorkspacePackages(count int, space *model.Space) []*model.Package {
	pkgs := make([]*model.Package, count)
	for i := range pkgs {
		name := fmt.Sprintf("pkg%d", i)
		pkgs[i] = &model.Package{
			Name:  name,
			Dir:   filepath.Join("/Users/Bench/Repo/packages", name),
			Space: space,
		}
	}
	return pkgs
}

func BenchmarkCheckAliasTagsAreWriteOnly(b *testing.B) {
	space := &model.Space{AliasTags: []model.AliasTag{{Format: "{name}-v{major}", Moving: true}}}
	for _, count := range benchPackageCounts {
		b.Run(fmt.Sprintf("packages=%d", count), func(b *testing.B) {
			pkgs := benchWorkspacePackages(count, space)
			b.ReportAllocs()
			for b.Loop() {
				if err := checkAliasTagsAreWriteOnly(pkgs); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkCheckBuildOutputRoots(b *testing.B) {
	space := &model.Space{BuildOutputs: []string{"dist"}}
	for _, count := range benchPackageCounts {
		b.Run(fmt.Sprintf("packages=%d", count), func(b *testing.B) {
			d := &discovery{pkgs: benchWorkspacePackages(count, space)}
			b.ReportAllocs()
			for b.Loop() {
				if err := d.checkBuildOutputRoots(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkCheckRunOutputRoots(b *testing.B) {
	space := &model.Space{BuildOutputs: []string{"dist"}}
	runOutputs := map[string][]string{"test": {"coverage", "reports/junit"}}
	for _, count := range benchPackageCounts {
		b.Run(fmt.Sprintf("packages=%d", count), func(b *testing.B) {
			pkgs := benchWorkspacePackages(count, space)
			b.ReportAllocs()
			for b.Loop() {
				if err := checkRunOutputRoots(runOutputs, pkgs, "/Users/Bench/Repo"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkCheckOutputRootsUndeclared is the workspace that declares no
// output roots, the common one, for which neither check has a root to hold
// against the packages.
func BenchmarkCheckOutputRootsUndeclared(b *testing.B) {
	for _, count := range benchPackageCounts {
		pkgs := benchWorkspacePackages(count, &model.Space{})
		d := &discovery{pkgs: pkgs}
		b.Run(fmt.Sprintf("build/packages=%d", count), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if err := d.checkBuildOutputRoots(); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(fmt.Sprintf("run/packages=%d", count), func(b *testing.B) {
			runOutputs := map[string][]string{"test": {}}
			b.ReportAllocs()
			for b.Loop() {
				if err := checkRunOutputRoots(runOutputs, pkgs, "/Users/Bench/Repo"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// packageLayersFixture is a space of count packages under one folder. With
// entries, the packages' entries are spread over the root map, the space's map
// and the space folder's own file, a quarter in each and a quarter with none,
// so that every package misses most of the maps it is looked up in; without,
// every map is empty, as in a workspace that overrides nothing per package.
// Only the space folder admits implicit files, so the measurement is the
// lookups rather than a probe of every package folder.
type packageLayersFixture struct {
	c        *File
	root     string
	spaceDir string
	names    []string
}

func newPackageLayersFixture(b *testing.B, count int, withEntries bool) packageLayersFixture {
	b.Helper()
	root := b.TempDir()
	spaceDir := filepath.Join(root, "packages")
	rootEntries := map[string]PackageConfig{}
	spaceEntries := map[string]PackageConfig{}
	fileEntries := map[string]any{}
	names := make([]string, count)
	for i := range names {
		names[i] = fmt.Sprintf("pkg%d", i)
		if !withEntries {
			continue
		}
		switch i % 4 {
		case 0:
			rootEntries[names[i]] = PackageConfig{}
		case 1:
			spaceEntries[names[i]] = PackageConfig{}
		case 2:
			fileEntries[names[i]] = map[string]any{"tagFormat": "{name}/v{version}"}
		}
	}
	spaceFile, err := json.Marshal(map[string]any{"packages": fileEntries})
	if err != nil {
		b.Fatal(err)
	}
	if err := os.MkdirAll(spaceDir, 0o755); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(spaceDir, "dispat.json"), spaceFile, 0o644); err != nil {
		b.Fatal(err)
	}
	c := &File{
		Scripts:  map[string]Script{"build": {"echo build"}, "publish": {"echo publish"}},
		Packages: rootEntries,
		Spaces: map[string]SpaceConfig{"libs": {
			Path:     PathList{"packages"},
			Packages: spaceEntries,
			Flow:     &SpaceFlowConfig{Build: []string{"build"}, Publish: []string{"publish"}},
		}},
	}
	return packageLayersFixture{c: c, root: root, spaceDir: spaceDir, names: names}
}

func (f packageLayersFixture) layerEveryPackage(b *testing.B) {
	onlySpaceFolder := func(dir string) bool { return dir == f.spaceDir }
	d, err := newDiscoveryMode(f.c, f.root, onlySpaceFolder)
	if err != nil {
		b.Fatal(err)
	}
	s, err := d.resolveSpace("libs")
	if err != nil {
		b.Fatal(err)
	}
	for _, name := range f.names {
		if _, err := d.packageLayers(s, name, filepath.Join(f.spaceDir, name), name); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPackageLayers(b *testing.B) {
	for _, count := range benchPackageCounts {
		b.Run(fmt.Sprintf("packages=%d", count), func(b *testing.B) {
			f := newPackageLayersFixture(b, count, true)
			b.ReportAllocs()
			for b.Loop() {
				f.layerEveryPackage(b)
			}
		})
	}
}

func BenchmarkPackageLayersWithoutEntries(b *testing.B) {
	for _, count := range benchPackageCounts {
		b.Run(fmt.Sprintf("packages=%d", count), func(b *testing.B) {
			f := newPackageLayersFixture(b, count, false)
			b.ReportAllocs()
			for b.Loop() {
				f.layerEveryPackage(b)
			}
		})
	}
}

// BenchmarkResolvedSpaceConfigs settles the spaces of a root file whose
// packages map holds an entry for every package: settling a space's own
// configuration never asks that map about a package.
func BenchmarkResolvedSpaceConfigs(b *testing.B) {
	for _, count := range benchPackageCounts {
		b.Run(fmt.Sprintf("packages=%d", count), func(b *testing.B) {
			f := newPackageLayersFixture(b, count, false)
			for _, name := range f.names {
				f.c.Packages[name] = PackageConfig{}
			}
			b.ReportAllocs()
			for b.Loop() {
				if _, err := ResolvedSpaceConfigs(f.c, f.root); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
