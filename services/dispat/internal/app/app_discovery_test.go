package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// discoveryFixture writes a one-space workspace of count package folders and
// returns its root and loaded config.
func discoveryFixture(tb testing.TB, count int) (string, *config.File) {
	tb.Helper()
	root := tb.TempDir()
	data, err := json.Marshal(libsConfig())
	if err != nil {
		tb.Fatal(err)
	}
	cfgPath := filepath.Join(root, "dispat.json")
	if err := os.WriteFile(cfgPath, data, 0o644); err != nil {
		tb.Fatal(err)
	}
	for i := range count {
		dir := filepath.Join(root, "packages", fmt.Sprintf("pkg%03d", i))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			tb.Fatal(err)
		}
		manifest := fmt.Sprintf(`{"name": "pkg%03d", "version": "1.0.0"}`, i)
		if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(manifest), 0o644); err != nil {
			tb.Fatal(err)
		}
	}
	cfg, err := config.Load(cfgPath, nil)
	if err != nil {
		tb.Fatal(err)
	}
	return root, cfg
}

// TestPlanOptionsReuseTheDiscoveredWorkspace: the plan is computed from the
// packages the invocation already discovered, not from a second walk. The
// folder removed in between is the witness: a second walk would not see it.
func TestPlanOptionsReuseTheDiscoveredWorkspace(t *testing.T) {
	root, cfg := discoveryFixture(t, 3)
	a := New(root, cfg, zerolog.Nop())
	pkgs, err := a.packages()
	require.NoError(t, err)
	require.NoError(t, os.RemoveAll(filepath.Join(root, "packages", "pkg001")))

	opts, err := a.planOptions(t.Context())
	require.NoError(t, err)
	assert.Equal(t, pkgs, opts.Packages)
}

// TestPlanOptionsLeaveDiscoveredPackagesUnresolved: a composed workspace
// resolves each source package's record target for the plan, and the
// packages every other caller is handed stay as discovery found them.
func TestPlanOptionsLeaveDiscoveredPackagesUnresolved(t *testing.T) {
	w, rel := recordFixture(t, false, false)
	a := w.app
	recordGit(t, w.byName["source"].repo.Root, "remote", "set-url", "origin", "git@github.com:source/project.git")
	a.discoveryOnce.Do(func() {})
	a.discovered = workspaceDiscovery{packages: []*model.Package{rel.Pkg}}

	opts, err := a.planOptions(t.Context())
	require.NoError(t, err)
	require.Len(t, opts.Packages, 1)
	assert.Equal(t, "source", opts.Packages[0].GitHub.Owner)
	assert.True(t, opts.Packages[0].GitHub.Format.RepositoryResolved)

	pkgs, err := a.packages()
	require.NoError(t, err)
	assert.Same(t, rel.Pkg, pkgs[0])
	assert.Empty(t, pkgs[0].GitHub.Owner)
	assert.False(t, pkgs[0].GitHub.Format.RepositoryResolved)
}

// BenchmarkDiscoveryPerInvocation measures what one command pays to discover
// a 200-package workspace when it asks for the packages and then plans, as
// every step command does.
func BenchmarkDiscoveryPerInvocation(b *testing.B) {
	root, cfg := discoveryFixture(b, 200)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a := New(root, cfg, zerolog.Nop())
		pkgs, err := a.packages()
		if err != nil {
			b.Fatal(err)
		}
		opts, err := a.planOptions(ctx)
		if err != nil {
			b.Fatal(err)
		}
		if len(pkgs) != 200 || len(opts.Packages) != 200 {
			b.Fatalf("discovered %d and planned %d packages", len(pkgs), len(opts.Packages))
		}
	}
}
