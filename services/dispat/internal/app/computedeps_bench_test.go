package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/services/dispat/internal/config"
)

// BenchmarkCollectDepEditsPerPackageLists measures `compute --write` turning
// one removal per package into edits for 300 packages that each keep ten
// providers in their own folder file: 300 touched lists over 3,000 declared
// edges.
func BenchmarkCollectDepEditsPerPackageLists(b *testing.B) {
	const packages, providers = 300, 10
	root := b.TempDir()
	var declared []config.DeclaredDependency
	var apply []suggestion
	for i := range packages {
		consumer := fmt.Sprintf("pkg%03d", i)
		file := filepath.Join(root, consumer, "dispat.json")
		var list []map[string]string
		for j := range providers {
			provider := fmt.Sprintf("pkg%03d", (i+j+1)%packages)
			list = append(list, map[string]string{"provider": provider})
			declared = append(declared, config.DeclaredDependency{
				DependencyConfig: config.DependencyConfig{Consumer: consumer, Provider: provider},
				Source:           config.DepSource{File: file, KeyPath: []string{"dependencies"}, Index: j},
			})
		}
		data, err := json.Marshal(map[string]any{"dependencies": list})
		if err != nil {
			b.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(file, data, 0o644); err != nil {
			b.Fatal(err)
		}
		removed := declared[len(declared)-providers]
		apply = append(apply, suggestion{action: actionRemove, entry: removed.DependencyConfig, src: removed.Source})
	}
	a := New(root, &config.File{}, zerolog.Nop())
	cfgPath := filepath.Join(root, "dispat.json")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var edits fileEdits
		if err := a.collectDepEdits(&edits, cfgPath, apply, declared); err != nil {
			b.Fatal(err)
		}
		if len(edits.order) != packages {
			b.Fatalf("%d files edited", len(edits.order))
		}
	}
}
