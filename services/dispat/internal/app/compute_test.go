package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/config"
)

// Compute is a filesystem affair with no git involved, so its tests build a
// real mini-monorepo — spaces, package folders, manifests — and run the real
// scanner through the whole pipeline: detection, name mapping, diffing and
// the config write-back.

// computeRepo lays out a monorepo with one space "libs" at packages/ and the
// given package folders, writes the config as dispat.json, and returns the
// root, the config path and the loaded App.
func computeRepo(t *testing.T, cfg config.File, log zerolog.Logger) (string, string, *App) {
	t.Helper()
	root := t.TempDir()
	for _, s := range cfg.Spaces {
		require.NoError(t, os.MkdirAll(filepath.Join(root, s.Path), 0o755))
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	require.NoError(t, err)
	cfgPath := filepath.Join(root, "dispat.json")
	require.NoError(t, os.WriteFile(cfgPath, data, 0o644))
	loaded, err := config.Load(cfgPath, nil)
	require.NoError(t, err)
	return root, cfgPath, New(root, loaded, log)
}

// seedManifest writes one manifest file inside a package folder.
func seedManifest(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

// libsConfig is one space at "packages" with a build script, plus the given
// declared dependencies.
func libsConfig(deps ...config.DependencyConfig) config.File {
	return config.File{
		Scripts: map[string]string{"build": "true"},
		Spaces: map[string]config.SpaceConfig{
			"libs": {Path: "packages", Flow: &config.SpaceFlowConfig{Build: []string{"build"}}},
		},
		Dependencies: deps,
	}
}

func TestComputeSuggestsDetectedEdges(t *testing.T) {
	root, cfgPath, a := computeRepo(t, libsConfig(), zerolog.Nop())
	// web depends on core by manifest name (folder names differ from manifest
	// names) and on tools by a file: path; svc's go.mod requires core's twin
	// module via a local replace.
	seedManifest(t, root, "packages/core/package.json", `{"name": "@acme/core", "version": "1.0.0"}`)
	seedManifest(t, root, "packages/tools/package.json", `{"name": "@acme/tools"}`)
	seedManifest(t, root, "packages/web/package.json", `{
		"name": "@acme/web",
		"dependencies": {"@acme/core": "workspace:*"},
		"devDependencies": {"tools-alias": "file:../tools"}
	}`)
	seedManifest(t, root, "packages/svc/go.mod",
		"module example.com/svc\n\nrequire example.com/gocore v1.0.0\n\nreplace example.com/gocore => ../core\n")

	var out bytes.Buffer
	open, err := a.Compute(context.Background(), cfgPath, ComputeOptions{Out: &out})
	require.NoError(t, err)
	assert.Equal(t, 3, open, "three additions suggested, none applied in preview mode")
	listing := out.String()
	assert.Contains(t, listing, "+ add    svc -> core (dependencies)")
	assert.Contains(t, listing, "+ add    web -> core (dependencies)")
	assert.Contains(t, listing, "+ add    web -> tools (devDependencies)")
	assert.Contains(t, listing, `packages/web/package.json dependencies "@acme/core": "workspace:*"`)
	assert.Contains(t, listing, "apply all with --write")

	// Preview writes nothing.
	assert.Empty(t, a.cfg.Dependencies)
	_, statErr := os.Stat(cfgPath + config.BackupSuffix)
	assert.True(t, os.IsNotExist(statErr))
}

func TestComputeWriteAppliesAndBacksUp(t *testing.T) {
	root, cfgPath, a := computeRepo(t, libsConfig(), zerolog.Nop())
	seedManifest(t, root, "packages/core/package.json", `{"name": "@acme/core"}`)
	seedManifest(t, root, "packages/web/package.json",
		`{"name": "@acme/web", "dependencies": {"@acme/core": "^1.0.0"}}`)

	var out bytes.Buffer
	open, err := a.Compute(context.Background(), cfgPath, ComputeOptions{Write: true, Out: &out})
	require.NoError(t, err)
	assert.Zero(t, open)
	assert.Contains(t, out.String(), "applied 1 change(s)")

	reloaded, err := config.Load(cfgPath, nil)
	require.NoError(t, err)
	require.Len(t, reloaded.Dependencies, 1)
	assert.Equal(t, "web", reloaded.Dependencies[0].Consumer)
	assert.Equal(t, "core", reloaded.Dependencies[0].Provider)
	_, statErr := os.Stat(cfgPath + config.BackupSuffix)
	assert.NoError(t, statErr, "previous copy saved")

	// A second run is stable: nothing left to suggest.
	out.Reset()
	open, err = a.Compute(context.Background(), cfgPath, ComputeOptions{Out: &out})
	require.NoError(t, err)
	assert.Zero(t, open)
	assert.Contains(t, out.String(), "in sync")
}

func TestComputeRemovalAndKeepAndKindChange(t *testing.T) {
	root, cfgPath, a := computeRepo(t, libsConfig(
		config.DependencyConfig{Consumer: "web", Provider: "ghost"},                         // stale: suggest removal
		config.DependencyConfig{Consumer: "web", Provider: "docker", Keep: true},            // kept: silent
		config.DependencyConfig{Consumer: "web", Provider: "core", Kind: "devDependencies"}, // wrong kind
		config.DependencyConfig{Consumer: "img", Provider: "core"},                          // consumer has no manifests: silent
	), zerolog.Nop())
	seedManifest(t, root, "packages/core/package.json", `{"name": "@acme/core"}`)
	seedManifest(t, root, "packages/web/package.json",
		`{"name": "@acme/web", "dependencies": {"@acme/core": "^1"}}`)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "packages/ghost"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "packages/docker"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "packages/img"), 0o755))

	var out bytes.Buffer
	open, err := a.Compute(context.Background(), cfgPath, ComputeOptions{Out: &out})
	require.NoError(t, err)
	assert.Equal(t, 2, open)
	listing := out.String()
	assert.Contains(t, listing, "~ kind   web -> core (dependencies)")
	assert.Contains(t, listing, "- remove web -> ghost (dependencies)")
	assert.Contains(t, listing, "keep: true silences")
	assert.NotContains(t, listing, "docker", "kept edge never suggested")
	assert.NotContains(t, listing, "img", "manifest-less consumer never suggested")
}

func TestComputeInteractiveAppliesAcceptedOnly(t *testing.T) {
	root, cfgPath, a := computeRepo(t, libsConfig(), zerolog.Nop())
	seedManifest(t, root, "packages/core/package.json", `{"name": "@acme/core"}`)
	seedManifest(t, root, "packages/tools/package.json", `{"name": "@acme/tools"}`)
	seedManifest(t, root, "packages/web/package.json", `{
		"name": "@acme/web",
		"dependencies": {"@acme/core": "^1", "@acme/tools": "^1"}
	}`)

	var out bytes.Buffer
	// Suggestions arrive sorted: web->core first, web->tools second.
	open, err := a.Compute(context.Background(), cfgPath, ComputeOptions{
		Interactive: true,
		In:          strings.NewReader("y\nn\n"),
		Out:         &out,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, open, "one accepted, one declined")

	reloaded, err := config.Load(cfgPath, nil)
	require.NoError(t, err)
	require.Len(t, reloaded.Dependencies, 1)
	assert.Equal(t, "core", reloaded.Dependencies[0].Provider)
}

func TestComputeCheckNeverWrites(t *testing.T) {
	root, cfgPath, a := computeRepo(t, libsConfig(), zerolog.Nop())
	seedManifest(t, root, "packages/core/package.json", `{"name": "@acme/core"}`)
	seedManifest(t, root, "packages/web/package.json",
		`{"name": "@acme/web", "dependencies": {"@acme/core": "^1"}}`)

	var out bytes.Buffer
	open, err := a.Compute(context.Background(), cfgPath, ComputeOptions{
		Check: true, Write: true, Interactive: true, Out: &out,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, open, "check reports and overrides both apply modes")
	assert.Empty(t, a.cfg.Dependencies)
	_, statErr := os.Stat(cfgPath + config.BackupSuffix)
	assert.True(t, os.IsNotExist(statErr))
}

func TestComputeCrossEcosystemManifests(t *testing.T) {
	// The other scanner ecosystems flow through the same pipeline: a Python
	// dep matched through PEP 503 normalisation ("Acme_Core" meets
	// "acme-core"), a Maven dep by groupId:artifactId coordinate, and a .NET
	// ProjectReference by its local path.
	root, cfgPath, a := computeRepo(t, libsConfig(), zerolog.Nop())
	seedManifest(t, root, "packages/pycore/pyproject.toml",
		"[project]\nname = \"acme-core\"\nversion = \"1.0.0\"\n")
	seedManifest(t, root, "packages/pyapp/pyproject.toml",
		"[project]\nname = \"acme-app\"\ndependencies = [\"Acme_Core>=1.0\"]\n")
	seedManifest(t, root, "packages/jcore/pom.xml",
		`<project><groupId>com.acme</groupId><artifactId>core</artifactId><version>1.0.0</version></project>`)
	seedManifest(t, root, "packages/japp/pom.xml",
		`<project><groupId>com.acme</groupId><artifactId>app</artifactId>
		<dependencies><dependency><groupId>com.acme</groupId><artifactId>core</artifactId><scope>test</scope></dependency></dependencies></project>`)
	seedManifest(t, root, "packages/ncore/Acme.Core.csproj", `<Project></Project>`)
	seedManifest(t, root, "packages/napp/Acme.App.csproj",
		`<Project><ItemGroup><ProjectReference Include="..\ncore\Acme.Core.csproj" /></ItemGroup></Project>`)

	var out bytes.Buffer
	open, err := a.Compute(context.Background(), cfgPath, ComputeOptions{Out: &out})
	require.NoError(t, err)
	listing := out.String()
	assert.Contains(t, listing, "+ add    pyapp -> pycore (dependencies)")
	assert.Contains(t, listing, "+ add    japp -> jcore (devDependencies)", "maven test scope maps to devDependencies")
	assert.Contains(t, listing, "+ add    napp -> ncore (dependencies)", "ProjectReference matched by local path")
	assert.Equal(t, 3, open)
}

func TestComputeAmbiguousNameW220(t *testing.T) {
	var logBuf bytes.Buffer
	root, cfgPath, a := computeRepo(t, libsConfig(), zerolog.New(&logBuf))
	// Two packages claim the same manifest name: no edges derived from it.
	seedManifest(t, root, "packages/core/package.json", `{"name": "@acme/dup"}`)
	seedManifest(t, root, "packages/tools/package.json", `{"name": "@acme/dup"}`)
	seedManifest(t, root, "packages/web/package.json",
		`{"name": "@acme/web", "dependencies": {"@acme/dup": "^1"}}`)

	var out bytes.Buffer
	open, err := a.Compute(context.Background(), cfgPath, ComputeOptions{Out: &out})
	require.NoError(t, err)
	assert.Zero(t, open, "ambiguous name derives no edges")
	assert.Contains(t, logBuf.String(), "W220")
}

func TestComputeTOMLConfigPrintsSnippet(t *testing.T) {
	// A TOML config cannot be spliced; --write prints the paste-ready blocks
	// and fails, having changed nothing.
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "packages/core"), 0o755))
	cfgPath := filepath.Join(root, "dispat.toml")
	require.NoError(t, os.WriteFile(cfgPath, []byte(
		"[scripts]\nbuild = \"true\"\n\n[spaces.libs]\npath = \"packages\"\nflow = { build = [\"build\"] }\n"), 0o644))
	loaded, err := config.Load(cfgPath, nil)
	require.NoError(t, err)
	a := New(root, loaded, zerolog.Nop())
	seedManifest(t, root, "packages/core/package.json", `{"name": "@acme/core"}`)
	seedManifest(t, root, "packages/web/package.json",
		`{"name": "@acme/web", "dependencies": {"@acme/core": "^1"}}`)

	var out bytes.Buffer
	_, err = a.Compute(context.Background(), cfgPath, ComputeOptions{Write: true, Out: &out})
	require.ErrorIs(t, err, config.ErrTOMLEdit)
	assert.Contains(t, out.String(), "[[dependencies]]")
	before, readErr := os.ReadFile(cfgPath)
	require.NoError(t, readErr)
	assert.NotContains(t, string(before), "[[dependencies]]", "config untouched")
}
