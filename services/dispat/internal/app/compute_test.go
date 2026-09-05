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
	"github.com/yohimik/dispat/pkg/models/v2"

	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/filter"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
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
		require.NoError(t, os.MkdirAll(filepath.Join(root, s.Path.First()), 0o755))
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
		Scripts: map[string]config.Script{"build": {"true"}},
		Spaces: map[string]config.SpaceConfig{
			"libs": {Path: config.PathList{"packages"}, Flow: &config.SpaceFlowConfig{Build: []string{"build"}}},
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
	assert.Contains(t, listing, "+ add     svc -> core (dependencies)")
	assert.Contains(t, listing, "+ add     web -> core (dependencies)")
	assert.Contains(t, listing, "+ add     web -> tools (devDependencies)")
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

// TestComputeWriteRewritesTheRootObject: an addition to the root
// `dependencies` object is spliced into the object that is already there,
// keeping the shape the loader reads — a bare name for a plain edge, the
// consumer as the key — so the file compute writes is a file compute can read
// again.
func TestComputeWriteRewritesTheRootObject(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "packages"), 0o755))
	cfgPath := filepath.Join(root, "dispat.json")
	require.NoError(t, os.WriteFile(cfgPath, []byte(`{
  "scripts": {"build": "true"},
  "spaces": {"libs": {"path": "packages", "flow": {"build": ["build"]}}},
  "dependencies": {
    "web": ["core"]
  }
}
`), 0o644))
	loaded, err := config.Load(cfgPath, nil)
	require.NoError(t, err)
	a := New(root, loaded, zerolog.Nop())

	seedManifest(t, root, "packages/core/package.json", `{"name": "@acme/core"}`)
	seedManifest(t, root, "packages/utils/package.json", `{"name": "@acme/utils"}`)
	seedManifest(t, root, "packages/web/package.json",
		`{"name": "@acme/web", "dependencies": {"@acme/core": "^1", "@acme/utils": "^1"}}`)

	var out bytes.Buffer
	_, err = a.Compute(context.Background(), cfgPath, ComputeOptions{Write: true, Out: &out})
	require.NoError(t, err)

	written, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Contains(t, string(written), `"dependencies": {`, "still the object keyed by consumer")
	assert.NotContains(t, string(written), `"consumer"`, "the consumer is the key, never a field")
	assert.Contains(t, string(written), `"core"`)
	assert.Contains(t, string(written), `"utils"`)

	// Written, not corrupted: the config still loads and now says both edges,
	// and a second run has nothing left to suggest.
	reloaded, err := config.Load(cfgPath, nil)
	require.NoError(t, err)
	assert.Equal(t, config.Dependencies{
		{Consumer: "web", Provider: "core"},
		{Consumer: "web", Provider: "utils"},
	}, reloaded.Dependencies)

	out.Reset()
	open, err := a.Compute(context.Background(), cfgPath, ComputeOptions{Out: &out})
	require.NoError(t, err)
	assert.Zero(t, open, "converged")
}

// TestComputeWriteKeepsEdgesWhereTheConsumerDeclaresThem: a config that keeps
// a package's dependencies next to the rest of that package's configuration
// stays that way. Additions join the list the consumer already has, and a
// kind correction is applied in place now that a package list carries a kind
// as readily as the root object does.
func TestComputeWriteKeepsEdgesWhereTheConsumerDeclaresThem(t *testing.T) {
	cfg := libsConfig()
	cfg.Packages = map[string]config.PackageConfig{
		"web": {Dependencies: config.ProviderList{
			{Provider: "core", Kind: "devDependencies"}, // manifests say otherwise
			{Provider: "pinned", Keep: true},            // no manifest declares it
		}},
	}
	root, cfgPath, a := computeRepo(t, cfg, zerolog.Nop())
	require.NoError(t, os.MkdirAll(filepath.Join(root, "packages/pinned"), 0o755))
	seedManifest(t, root, "packages/core/package.json", `{"name": "@acme/core"}`)
	seedManifest(t, root, "packages/utils/package.json", `{"name": "@acme/utils"}`)
	seedManifest(t, root, "packages/web/package.json",
		`{"name": "@acme/web", "dependencies": {"@acme/core": "^1", "@acme/utils": "^1"}}`)

	var out bytes.Buffer
	_, err := a.Compute(context.Background(), cfgPath, ComputeOptions{Write: true, Out: &out})
	require.NoError(t, err)

	reloaded, err := config.Load(cfgPath, nil)
	require.NoError(t, err)
	assert.Empty(t, reloaded.Dependencies, "nothing was moved to the root object")
	assert.Equal(t, config.ProviderList{
		{Provider: "core"},               // kind corrected in place
		{Provider: "pinned", Keep: true}, // keep survives untouched
		{Provider: "utils"},              // the detected edge joined the list
	}, reloaded.Packages["web"].Dependencies)

	out.Reset()
	open, err := a.Compute(context.Background(), cfgPath, ComputeOptions{Out: &out})
	require.NoError(t, err)
	assert.Zero(t, open, "converged")
}

// TestComputeWriteDropsAnEmptiedConsumer: a consumer whose last provider is
// removed loses its key, and a config left with no edges at all keeps an
// object rather than a null the next reader cannot interpret.
func TestComputeWriteDropsAnEmptiedConsumer(t *testing.T) {
	root, cfgPath, a := computeRepo(t, libsConfig(
		config.DependencyConfig{Consumer: "web", Provider: "ghost"},
	), zerolog.Nop())
	seedManifest(t, root, "packages/web/package.json", `{"name": "@acme/web"}`)

	var out bytes.Buffer
	_, err := a.Compute(context.Background(), cfgPath, ComputeOptions{Write: true, Out: &out})
	require.NoError(t, err)

	written, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Contains(t, string(written), `"dependencies": {}`, "an empty object, never null")
	assert.NotContains(t, string(written), "ghost")

	reloaded, err := config.Load(cfgPath, nil)
	require.NoError(t, err)
	assert.Empty(t, reloaded.Dependencies)
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
	assert.Contains(t, listing, "~ kind    web -> core (dependencies)")
	assert.Contains(t, listing, "- remove  web -> ghost (dependencies)")
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
	assert.Contains(t, listing, "+ add     pyapp -> pycore (dependencies)")
	assert.Contains(t, listing, "+ add     japp -> jcore (devDependencies)", "maven test scope maps to devDependencies")
	assert.Contains(t, listing, "+ add     napp -> ncore (dependencies)", "ProjectReference matched by local path")
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
	assert.Contains(t, logBuf.String(), plan.CodeAmbiguousManifestName)
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
	assert.Contains(t, out.String(), "[dependencies]")
	before, readErr := os.ReadFile(cfgPath)
	require.NoError(t, readErr)
	assert.NotContains(t, string(before), "[dependencies]", "config untouched")
}

func TestComputeSuggestsRemovalOfStaleEndpoints(t *testing.T) {
	// The edge names a package that no longer exists on disk — the state that
	// makes every other command refuse to load. compute must run anyway and
	// offer the removal; keep: true still silences it.
	cfg := libsConfig(
		config.DependencyConfig{Consumer: "app", Provider: "ghost"},
		config.DependencyConfig{Consumer: "gone", Provider: "app", Keep: true},
	)
	root, cfgPath, a := computeRepo(t, cfg, zerolog.Nop())
	seedManifest(t, root, "packages/app/package.json", `{"name": "@acme/app"}`)

	var out bytes.Buffer
	open, err := a.Compute(context.Background(), cfgPath, ComputeOptions{Out: &out})
	require.NoError(t, err, "compute must tolerate the drift it exists to fix")
	assert.Equal(t, 1, open)
	assert.Contains(t, out.String(), `- remove  app -> ghost`)
	assert.Contains(t, out.String(), `no longer exists`)
	assert.NotContains(t, out.String(), "gone -> app", "keep: true silences even a stale edge")

	// The release path stays strict: Discover still refuses the stale edge.
	_, _, _, err = config.Discover(a.cfg, root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unknown provider package "ghost"`)
}

func TestComputeFilterScopesSuggestionsToTheSelectedConsumers(t *testing.T) {
	// Two drifted packages and one stale edge belonging to a third. A filter
	// scopes the findings to the selected consumers' own declarations —
	// nothing is proposed about a package the invocation did not name.
	cfg := libsConfig(config.DependencyConfig{Consumer: "svc", Provider: "ghost"})
	root, cfgPath, a := computeRepo(t, cfg, zerolog.Nop())
	seedManifest(t, root, "packages/core/package.json", `{"name": "@acme/core"}`)
	seedManifest(t, root, "packages/web/package.json",
		`{"name": "@acme/web", "dependencies": {"@acme/core": "workspace:*"}}`)
	seedManifest(t, root, "packages/svc/package.json",
		`{"name": "@acme/svc", "dependencies": {"@acme/core": "workspace:*"}}`)

	var out bytes.Buffer
	open, err := a.Compute(context.Background(), cfgPath,
		ComputeOptions{Filter: filter.Filter{Packages: []string{"web"}}, Out: &out})
	require.NoError(t, err)
	assert.Equal(t, 1, open)
	assert.Contains(t, out.String(), "+ add     web -> core")
	assert.NotContains(t, out.String(), "svc", "another consumer's drift is out of scope")

	// The space term reaches the same packages the space holds.
	out.Reset()
	open, err = a.Compute(context.Background(), cfgPath,
		ComputeOptions{Filter: filter.Filter{Spaces: []string{"libs"}}, Out: &out})
	require.NoError(t, err)
	assert.Equal(t, 3, open, "web -> core, svc -> core, and svc's stale edge")

	// A selection with nothing to say reports being in sync, and says for what.
	out.Reset()
	open, err = a.Compute(context.Background(), cfgPath,
		ComputeOptions{Filter: filter.Filter{Packages: []string{"core"}}, Out: &out})
	require.NoError(t, err)
	assert.Zero(t, open)
	assert.Contains(t, out.String(), "dependencies and baselines are in sync for core",
		"core's manifests declare no version, so there is no baseline left to ask git about either")

	_, err = a.Compute(context.Background(), cfgPath,
		ComputeOptions{Filter: filter.Filter{Packages: []string{"nope"}}, Out: &out})
	require.Error(t, err, "an unmatched term is a typo, not an empty scope")
}

func TestComputeFilterStillDetectsAgainstTheWholeWorkspace(t *testing.T) {
	// The fence: detection reads every package's manifests whatever the
	// filter says. Scanning only "web" would leave "@acme/core" resolving to
	// no provider, turning a perfectly good declared edge into a removal
	// suggestion — the exact opposite of what a scoped compute is for.
	cfg := libsConfig(config.DependencyConfig{Consumer: "web", Provider: "core"})
	root, cfgPath, a := computeRepo(t, cfg, zerolog.Nop())
	seedManifest(t, root, "packages/core/package.json", `{"name": "@acme/core"}`)
	seedManifest(t, root, "packages/tools/package.json", `{"name": "@acme/tools"}`)
	seedManifest(t, root, "packages/web/package.json", `{
		"name": "@acme/web",
		"dependencies": {"@acme/core": "workspace:*"},
		"devDependencies": {"@acme/tools": "workspace:*"}
	}`)

	var out bytes.Buffer
	open, err := a.Compute(context.Background(), cfgPath,
		ComputeOptions{Filter: filter.Filter{Packages: []string{"web"}}, Out: &out})
	require.NoError(t, err)
	assert.Equal(t, 1, open)
	assert.NotContains(t, out.String(), "- remove", "the declared edge is supported by a manifest")
	assert.Contains(t, out.String(), "+ add     web -> tools (devDependencies)")
}

func TestComputeInteractiveNeedsInput(t *testing.T) {
	cfg := libsConfig()
	root, cfgPath, a := computeRepo(t, cfg, zerolog.Nop())
	seedManifest(t, root, "packages/app/package.json", `{"name": "@acme/app", "dependencies": {"@acme/core": "workspace:*"}}`)
	seedManifest(t, root, "packages/core/package.json", `{"name": "@acme/core"}`)

	var out bytes.Buffer
	_, err := a.Compute(context.Background(), cfgPath, ComputeOptions{Interactive: true, Out: &out})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "input stream")
}

func TestComputeUnknownConfigFormat(t *testing.T) {
	// A config under an extension the editor does not know: the write fails
	// cleanly and the file is untouched.
	cfg := libsConfig()
	root, cfgPath, a := computeRepo(t, cfg, zerolog.Nop())
	seedManifest(t, root, "packages/app/package.json", `{"name": "@acme/app", "dependencies": {"@acme/core": "workspace:*"}}`)
	seedManifest(t, root, "packages/core/package.json", `{"name": "@acme/core"}`)
	odd := filepath.Join(root, "dispat.conf")
	data, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(odd, data, 0o644))

	var out bytes.Buffer
	_, err = a.Compute(context.Background(), odd, ComputeOptions{Write: true, Out: &out})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dispat reads json, yaml and toml config files")
	after, err := os.ReadFile(odd)
	require.NoError(t, err)
	assert.Equal(t, data, after, "a failed write must leave the file untouched")
}

func TestStrongestKindFullRanking(t *testing.T) {
	assert.Equal(t, model.KindPeerDependencies,
		strongestKind([]model.DepKind{model.KindDevDependencies, model.KindOptionalDependencies, model.KindPeerDependencies}),
		"peer outranks optional outranks dev")
	assert.Equal(t, model.KindOptionalDependencies,
		strongestKind([]model.DepKind{model.KindOptionalDependencies, model.KindDevDependencies}))
	assert.Equal(t, model.KindDependencies,
		strongestKind([]model.DepKind{model.KindDevDependencies, model.KindDependencies}))
}

// tomlComputeRepo is computeRepo for a TOML config: the same monorepo, but
// the config file the write-back has to refuse to edit in place.
func tomlComputeRepo(t *testing.T, body string) (string, string, *App) {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "packages"), 0o755))
	cfgPath := filepath.Join(root, "dispat.toml")
	require.NoError(t, os.WriteFile(cfgPath, []byte(body), 0o644))
	loaded, err := config.Load(cfgPath, nil)
	require.NoError(t, err)
	return root, cfgPath, New(root, loaded, zerolog.Nop())
}

func TestComputeWriteOnTOMLPrintsAPasteReadyBlock(t *testing.T) {
	// A TOML config has no in-place editor, so --write must not half-apply
	// anything: it prints the block to paste, fails, and leaves the file as it
	// found it. The alternative — a silent no-op — would have the command
	// report suggestions applied that were not.
	const body = `[scripts]
build = "true"

[spaces.libs]
path = "packages"

[spaces.libs.flow]
build = "build"
`
	root, cfgPath, a := tomlComputeRepo(t, body)
	seedManifest(t, root, "packages/core/package.json", `{"name": "@acme/core", "version": "1.0.0"}`)
	seedManifest(t, root, "packages/web/package.json",
		`{"name": "@acme/web", "dependencies": {"@acme/core": "^1.0.0"}}`)

	var out bytes.Buffer
	_, err := a.Compute(context.Background(), cfgPath, ComputeOptions{Write: true, Out: &out})
	require.ErrorIs(t, err, config.ErrTOMLEdit)
	assert.Contains(t, out.String(), "# paste over the [dependencies] table in dispat.toml:")
	assert.Contains(t, out.String(), "[[dependencies.web]]")
	assert.Contains(t, out.String(), "web")
	assert.Contains(t, out.String(), "core")

	data, readErr := os.ReadFile(cfgPath)
	require.NoError(t, readErr)
	assert.Equal(t, body, string(data), "a refused edit leaves the config untouched")
	assert.NoFileExists(t, cfgPath+config.BackupSuffix, "and writes no backup")
}

func TestComputeWriteOnTOMLPackageListPrintsAPasteReadyBlock(t *testing.T) {
	// The same refusal for the other kind of declaration: a list living in a
	// packages entry, whose removal would have to edit that entry in place.
	const body = `[scripts]
build = "true"

[spaces.libs]
path = "packages"

[spaces.libs.flow]
build = "build"

[packages.web]
dependencies = ["core"]
`
	root, cfgPath, a := tomlComputeRepo(t, body)
	// No manifests at all, so the declared edge has nothing supporting it and
	// core does not exist on disk either: a removal, aimed at the packages
	// entry that declares it.
	seedManifest(t, root, "packages/web/main.txt", "web\n")

	var out bytes.Buffer
	_, err := a.Compute(context.Background(), cfgPath, ComputeOptions{Write: true, Out: &out})
	require.ErrorIs(t, err, config.ErrTOMLEdit)
	assert.Contains(t, out.String(), "# paste over the dependencies in dispat.toml:")
	assert.Contains(t, out.String(), "[packages.web]")

	data, readErr := os.ReadFile(cfgPath)
	require.NoError(t, readErr)
	assert.Equal(t, body, string(data), "a refused edit leaves the config untouched")
}

func TestComputeStatedManifestNamesDeriveEdges(t *testing.T) {
	// A Gradle module declares no name any parser here can read, so nothing
	// points at it until the configuration says what it is called. With the
	// name stated, the coordinate in a consumer's manifest resolves like any
	// other.
	cfg := libsConfig()
	cfg.Packages = map[string]config.PackageConfig{
		"gradlelib": {ManifestNames: []string{"com.acme:core"}},
	}
	root, cfgPath, a := computeRepo(t, cfg, zerolog.Nop())
	require.NoError(t, os.MkdirAll(filepath.Join(root, "packages/gradlelib"), 0o755))
	seedManifest(t, root, "packages/web/pom.xml", `<project>
  <groupId>com.acme</groupId>
  <artifactId>web</artifactId>
  <version>1.0.0</version>
  <dependencies>
    <dependency>
      <groupId>com.acme</groupId>
      <artifactId>core</artifactId>
      <version>1.0.0</version>
    </dependency>
  </dependencies>
</project>`)

	var out bytes.Buffer
	open, err := a.Compute(context.Background(), cfgPath, ComputeOptions{Out: &out})
	require.NoError(t, err)
	assert.Equal(t, 1, open)
	assert.Contains(t, out.String(), "+ add     web -> gradlelib (dependencies)")
}

func TestComputeStatedNameOutranksADeclaredOne(t *testing.T) {
	// The operator's statement wins over a file that happens to say the same
	// thing, and nothing is reported ambiguous: the ranks separate the claims.
	cfg := libsConfig()
	cfg.Packages = map[string]config.PackageConfig{
		"renamed": {ManifestNames: []string{"@acme/core"}},
	}
	var logBuf bytes.Buffer
	root, cfgPath, a := computeRepo(t, cfg, zerolog.New(&logBuf))
	seedManifest(t, root, "packages/renamed/package.json", `{"name": "@acme/renamed"}`)
	seedManifest(t, root, "packages/core/package.json", `{"name": "@acme/core"}`)
	seedManifest(t, root, "packages/web/package.json",
		`{"name": "@acme/web", "dependencies": {"@acme/core": "^1"}}`)

	var out bytes.Buffer
	open, err := a.Compute(context.Background(), cfgPath, ComputeOptions{Out: &out})
	require.NoError(t, err)
	assert.Equal(t, 1, open)
	assert.Contains(t, out.String(), "+ add     web -> renamed (dependencies)")
	assert.NotContains(t, logBuf.String(), plan.CodeAmbiguousManifestName)
}

// TestComputeWritesOneBackupPerFile: one run that changes two keys of the
// root config — the top-level list gains an edge while a packages entry loses
// one — must write that file once. Writing it twice would save the first
// write's output as the "previous" copy, leaving no way back to the config as
// it was found.
func TestComputeWritesOneBackupPerFile(t *testing.T) {
	cfg := libsConfig(config.DependencyConfig{Consumer: "web", Provider: "ghost"})
	cfg.Packages = map[string]config.PackageConfig{"web": {Dependencies: models.Providers("gone")}}
	root, cfgPath, a := computeRepo(t, cfg, zerolog.Nop())
	seedManifest(t, root, "packages/core/package.json", `{"name": "@acme/core"}`)
	seedManifest(t, root, "packages/web/package.json",
		`{"name": "@acme/web", "dependencies": {"@acme/core": "workspace:*"}}`)

	before, err := os.ReadFile(cfgPath)
	require.NoError(t, err)

	var out bytes.Buffer
	open, err := a.Compute(context.Background(), cfgPath, ComputeOptions{Write: true, Out: &out})
	require.NoError(t, err)
	assert.Zero(t, open)
	assert.Contains(t, out.String(), "applied 3 change(s) to dispat.json",
		"the addition, the stale root edge and the stale packages entry")

	backup, err := os.ReadFile(cfgPath + config.BackupSuffix)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(backup), "the backup is the config from before every edit")

	after, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	// web already declares its providers in its packages entry, so the new
	// edge joins them there rather than opening a second home for web's
	// dependencies in the top-level object.
	assert.Contains(t, string(after), `"core"`)
	assert.Contains(t, string(after), `"dependencies": {}`, "the last root edge was the stale one")
	assert.NotContains(t, string(after), "ghost")
	assert.NotContains(t, string(after), "gone")

	reloaded, err := config.Load(cfgPath, nil)
	require.NoError(t, err)
	assert.Equal(t, config.Providers("core"), reloaded.Packages["web"].Dependencies)
}

// TestComputeWriteEditsASpaceObjectInPlace: a space's own `dependencies` is
// an object keyed by consumer, like the root file's, so an edge declared
// there is corrected and removed there — written back as an object, not as
// the bare provider array a package's own list is written as.
//
// Additions still go to the root object. Which edges a space may hold is a
// rule about the graph (an edge has to touch the space), and compute is not
// in a position to decide that for the author.
func TestComputeWriteEditsASpaceObjectInPlace(t *testing.T) {
	cfg := libsConfig()
	libs := cfg.Spaces["libs"]
	libs.Dependencies = config.Dependencies{
		{Consumer: "web", Provider: "core", Kind: "devDependencies"}, // manifests say otherwise
		{Consumer: "web", Provider: "ghost"},                         // no such package
	}
	cfg.Spaces["libs"] = libs
	root, cfgPath, a := computeRepo(t, cfg, zerolog.Nop())
	seedManifest(t, root, "packages/core/package.json", `{"name": "@acme/core"}`)
	seedManifest(t, root, "packages/utils/package.json", `{"name": "@acme/utils"}`)
	seedManifest(t, root, "packages/web/package.json",
		`{"name": "@acme/web", "dependencies": {"@acme/core": "^1", "@acme/utils": "^1"}}`)

	var out bytes.Buffer
	_, err := a.Compute(context.Background(), cfgPath, ComputeOptions{Write: true, Out: &out})
	require.NoError(t, err)

	written, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.NotContains(t, string(written), `"consumer"`, "the consumer stays the key")
	assert.NotContains(t, string(written), "ghost", "the edge naming no package is gone")

	reloaded, err := config.Load(cfgPath, nil)
	require.NoError(t, err)
	assert.Equal(t, config.Dependencies{{Consumer: "web", Provider: "core"}},
		reloaded.Spaces["libs"].Dependencies, "kind corrected in place, dead edge dropped")
	assert.Equal(t, config.Dependencies{{Consumer: "web", Provider: "utils"}},
		reloaded.Dependencies, "the addition went to the root object")

	out.Reset()
	open, err := a.Compute(context.Background(), cfgPath, ComputeOptions{Out: &out})
	require.NoError(t, err)
	assert.Zero(t, open, "converged")
}
