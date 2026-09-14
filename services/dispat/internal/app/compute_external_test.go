package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/config"
)

// Rewriting a list for another change must retain its optional provider, even
// when no manifest can resolve that provider in the current workspace.
func TestComputeExternalProviderWriteBack(t *testing.T) {
	for _, tc := range []struct {
		name, path, content string
	}{
		{"root JSON", "dispat.json", `{"spaces":{"libs":{"path":"packages"}},"dependencies":{"web":[{"provider":"remote","external":true},{"provider":"core","kind":"devDependencies","external":true}]}}`},
		{"root YAML", "dispat.yaml", "spaces:\n  libs:\n    path: packages\ndependencies:\n  web:\n    - {provider: remote, external: true}\n    - {provider: core, kind: devDependencies, external: true}\n"},
		{"package override", "dispat.json", `{"spaces":{"libs":{"path":"packages"}},"packages":{"web":{"dependencies":[{"provider":"remote","external":true},{"provider":"core","kind":"devDependencies","external":true}]}}}`},
		{"space JSON", "packages/dispat.json", `{"dependencies":{"web":[{"provider":"remote","external":true},{"provider":"core","kind":"devDependencies","external":true}]}}`},
		{"space YAML", "packages/dispat.yaml", "dependencies:\n  web:\n    - {provider: remote, external: true}\n    - {provider: core, kind: devDependencies, external: true}\n"},
		{"package JSON", "packages/web/dispat.json", `{"dependencies":[{"provider":"remote","external":true},{"provider":"core","kind":"devDependencies","external":true}]}`},
		{"package YAML", "packages/web/dispat.yaml", "dependencies:\n  - {provider: remote, external: true}\n  - {provider: core, kind: devDependencies, external: true}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, cfgPath, _ := computeRepo(t, libsConfig(), zerolog.Nop())
			seedManifest(t, root, "packages/core/package.json", `{"name":"@acme/core"}`)
			seedManifest(t, root, "packages/utils/package.json", `{"name":"@acme/utils"}`)
			seedManifest(t, root, "packages/web/package.json", `{"name":"@acme/web","dependencies":{"@acme/core":"^1","@acme/utils":"^1"}}`)
			path := filepath.Join(root, filepath.FromSlash(tc.path))
			require.NoError(t, os.WriteFile(path, []byte(tc.content), 0o644))
			if filepath.Dir(path) == root {
				cfgPath = path
			}
			cfg, err := config.Load(cfgPath, nil)
			require.NoError(t, err)
			var out bytes.Buffer
			_, err = New(root, cfg, zerolog.Nop()).Compute(context.Background(), cfgPath, ComputeOptions{Write: true, Out: &out})
			require.NoError(t, err)
			assert.NotContains(t, out.String(), "- remove")

			cfg, err = config.Load(cfgPath, nil)
			require.NoError(t, err)
			_, declared, _, err := config.DiscoverPackages(cfg, root)
			require.NoError(t, err)
			var edges []config.DependencyConfig
			for _, d := range declared {
				edges = append(edges, d.DependencyConfig)
			}
			assert.ElementsMatch(t, []config.DependencyConfig{
				{Consumer: "web", Provider: "remote", External: true},
				{Consumer: "web", Provider: "core", External: true},
				{Consumer: "web", Provider: "utils"},
			}, edges)
			assert.FileExists(t, path+config.BackupSuffix)
			out.Reset()
			remaining, err := New(root, cfg, zerolog.Nop()).Compute(context.Background(), cfgPath, ComputeOptions{Out: &out})
			require.NoError(t, err)
			assert.Zero(t, remaining, "rewritten declarations converge")
		})
	}
}

func TestComputeExternalDoesNotProtectStaleActiveEdges(t *testing.T) {
	root, cfgPath, a := computeRepo(t, libsConfig(
		config.DependencyConfig{Consumer: "gone", Provider: "core", External: true},
		config.DependencyConfig{Consumer: "web", Provider: "core", External: true},
	), zerolog.Nop())
	seedManifest(t, root, "packages/core/package.json", `{"name":"@acme/core"}`)
	seedManifest(t, root, "packages/web/package.json", `{"name":"@acme/web"}`)
	var out bytes.Buffer
	_, err := a.Compute(context.Background(), cfgPath, ComputeOptions{Write: true, Out: &out})
	require.NoError(t, err)
	cfg, err := config.Load(cfgPath, nil)
	require.NoError(t, err)
	assert.Empty(t, cfg.Dependencies)
}

func TestComputeExternalTOMLSnippet(t *testing.T) {
	const body = `[spaces.libs]
path = "packages"
[dependencies]
web = [{provider = "remote", external = true}, {provider = "core", kind = "devDependencies", external = true}]
`
	root, cfgPath, a := tomlComputeRepo(t, body)
	seedManifest(t, root, "packages/core/package.json", `{"name":"@acme/core"}`)
	seedManifest(t, root, "packages/web/package.json", `{"name":"@acme/web","dependencies":{"@acme/core":"^1"}}`)
	var out bytes.Buffer
	_, err := a.Compute(context.Background(), cfgPath, ComputeOptions{Write: true, Out: &out})
	require.ErrorIs(t, err, config.ErrTOMLEdit)
	assert.Contains(t, out.String(), "external = true")
	assert.Contains(t, out.String(), "remote")
	assert.NotContains(t, out.String(), "- remove")
	data, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, body, string(data), "TOML retains its existing manual-edit contract")
}
