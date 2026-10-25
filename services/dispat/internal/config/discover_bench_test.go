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

// BenchmarkPackageLayers spreads the packages' entries over the root map, the
// space's map and the space folder's own file, a quarter in each and a quarter
// with none, so that every package misses most of the maps it is looked up in.
// Only the space folder admits implicit files, so the measurement is the
// lookups rather than a probe of every package folder.
func BenchmarkPackageLayers(b *testing.B) {
	for _, count := range benchPackageCounts {
		b.Run(fmt.Sprintf("packages=%d", count), func(b *testing.B) {
			root := b.TempDir()
			spaceDir := filepath.Join(root, "packages")
			rootEntries := map[string]PackageConfig{}
			spaceEntries := map[string]PackageConfig{}
			fileEntries := map[string]any{}
			names := make([]string, count)
			for i := range names {
				names[i] = fmt.Sprintf("pkg%d", i)
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
			onlySpaceFolder := func(dir string) bool { return dir == spaceDir }
			b.ReportAllocs()
			for b.Loop() {
				d, err := newDiscoveryMode(c, root, onlySpaceFolder)
				if err != nil {
					b.Fatal(err)
				}
				s, err := d.resolveSpace("libs")
				if err != nil {
					b.Fatal(err)
				}
				for _, name := range names {
					if _, err := d.packageLayers(s, name, filepath.Join(spaceDir, name), name); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}
