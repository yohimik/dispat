package app

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/services/dispat/internal/config"
)

// newComputeRepository commits a one-space repository of packages that each
// declare a version and have no release tag, so every one of them is both
// scanned for edges and a baseline candidate.
func newComputeRepository(t testing.TB, packages int) (string, *config.File) {
	t.Helper()
	root := t.TempDir()
	cfg := config.File{
		Scripts: map[string]config.Script{"build": {"true"}},
		Spaces: map[string]config.SpaceConfig{
			"libs": {Path: config.PathList{"packages"}, Flow: &config.SpaceFlowConfig{Build: []string{"build"}}},
		},
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(root, "dispat.json")
	if err := os.WriteFile(cfgPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	for i := range packages {
		writeComputePackage(t, root, fmt.Sprintf("pkg%03d", i))
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"add", "-A"},
		{"commit", "-q", "-m", "packages"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	loaded, err := config.Load(cfgPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	return root, loaded
}

func writeComputePackage(t testing.TB, root, name string) {
	t.Helper()
	dir := filepath.Join(root, "packages", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := fmt.Sprintf(`{"name": %q, "version": "1.0.0"}`, name)
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
}

// BenchmarkComputePreview measures one `dispat compute` preview, on a fresh
// App as each invocation gets, over 200 untagged packages.
func BenchmarkComputePreview(b *testing.B) {
	const packages = 200
	root, cfg := newComputeRepository(b, packages)
	cfgPath := filepath.Join(root, "dispat.json")
	ctx := b.Context()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a := New(root, cfg, zerolog.Nop())
		open, err := a.Compute(ctx, cfgPath, ComputeOptions{Out: io.Discard})
		if err != nil {
			b.Fatal(err)
		}
		if open != packages {
			b.Fatalf("%d suggestions, want one baseline per package", open)
		}
	}
}
