package integration

// Area 10: the compute command through the compiled binary. compute derives
// the dependency graph from real manifests on disk and edits the config
// file; what must hold over the process boundary is the full loop — scan,
// suggest, gate CI, apply, back up — and that the very next status consumes
// the edges compute wrote.

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestComputeDetectApplyStatus is the whole loop: two npm packages whose
// manifests declare a workspace dependency, `compute` previews the addition
// without writing, `--check` fails the CI gate, `--write` applies it (saving
// the previous config), the next `status` orders the graph by the new edge,
// and the gate passes.
func TestComputeDetectApplyStatus(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(libsConfig(echoBuild, 1))
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "web")
	r.WriteFile("packages/core/package.json", `{"name": "@acme/core", "version": "0.0.0"}`)
	r.WriteFile("packages/web/package.json",
		`{"name": "@acme/web", "version": "0.0.0", "dependencies": {"@acme/core": "workspace:*"}}`)
	r.Commit("feat(core,web): bootstrap")

	// Preview: the suggestion is printed, nothing changes.
	res := r.Command("compute")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "+ add     web -> core (dependencies)")
	assert.Contains(t, res.Stdout, `packages/web/package.json dependencies "@acme/core": "workspace:*"`)
	configBefore, err := os.ReadFile(r.Path("dispat.json"))
	require.NoError(t, err)
	assert.NotContains(t, string(configBefore), `"consumer"`, "preview writes nothing")

	// The CI gate fails while the config lags the manifests.
	assert.Equal(t, 1, r.Command("compute", "--check").Code)

	// Apply: the edge lands in the config, the previous copy is saved.
	res = r.Command("compute", "--write")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "applied 1 change(s)")
	configAfter, err := os.ReadFile(r.Path("dispat.json"))
	require.NoError(t, err)
	assert.Contains(t, string(configAfter), `"web": [`, "written as the consumer-keyed object")
	assert.Contains(t, string(configAfter), `"core"`)
	backup, err := os.ReadFile(r.Path("dispat.json.backup"))
	require.NoError(t, err)
	assert.Equal(t, string(configBefore), string(backup), "backup is the pre-edit config")

	// The next status consumes the edge; the gate is green.
	status := r.StatusOK()
	dependsOn := ""
	for _, e := range status.Events {
		if e.Package() == "web" {
			if list, ok := e["dependsOn"].([]any); ok {
				parts := make([]string, len(list))
				for i, v := range list {
					parts[i], _ = v.(string)
				}
				dependsOn = strings.Join(parts, ",")
			}
		}
	}
	assert.Contains(t, dependsOn, "core", "the computed edge orders the graph")
	assert.Equal(t, 0, r.Command("compute", "--check").Code)
}

// TestComputeKeepAndRemoval: a declared edge no manifest supports is
// suggested for removal — unless it carries keep: true, the escape hatch for
// deliberate non-manifest coupling (a Docker chain). The kept edge survives
// --write byte-identically.
func TestComputeKeepAndRemoval(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Dependencies = []models.DependencyConfig{
		{Consumer: "web", Provider: "ghost"},
		{Consumer: "web", Provider: "core", Keep: true},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "ghost")
	r.SeedPackage("packages", "web")
	r.WriteFile("packages/web/package.json", `{"name": "@acme/web"}`)
	r.Commit("feat(core,ghost,web): bootstrap")

	res := r.Command("compute")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "- remove  web -> ghost")
	assert.NotContains(t, res.Stdout, "web -> core", "keep: true silences the removal")

	require.Equal(t, 0, r.Command("compute", "--write").Code)
	configAfter, err := os.ReadFile(r.Path("dispat.json"))
	require.NoError(t, err)
	assert.NotContains(t, string(configAfter), "ghost")
	assert.Contains(t, string(configAfter), `"keep": true`)

	// With the stale edge gone the config loads and status still runs.
	r.StatusOK()
}

// TestComputeAmbiguousNameReportsW220 through the binary: two packages
// declaring the same manifest name derive no edges, and the ambiguity reaches
// the JSON events as W220.
func TestComputeAmbiguousNameReportsW220(t *testing.T) {
	r := harness.New(t)
	cfg := harness.BaseFile(1)
	cfg.Scripts = map[string]models.Script{"build": {echoBuild}}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: models.PathList{"packages"}, Flow: &models.SpaceFlowConfig{Build: []string{"build"}}},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "a")
	r.SeedPackage("packages", "b")
	r.WriteFile("packages/a/package.json", `{"name": "@acme/same"}`)
	r.WriteFile("packages/b/package.json", `{"name": "@acme/same"}`)
	r.Commit("feat(a,b): two packages, one manifest name")

	res := r.Command("compute", "--check")
	assert.Zero(t, res.Code, "an ambiguous name derives nothing: no drift, exit 0; stdout:\n%s", res.Stdout)
	assert.True(t, harness.HasCode(res.Events, "W220"),
		"the ambiguity must reach the events: %s", res.Stdout)
}

// TestComputeEditsSpaceLayerDeclarations: an edge declared in a space's
// packages entry lives in the root config, and one declared in a space
// folder's file lives in that file. `compute --write` removes each stale
// edge where it was written, names the source in the listing, and backs up
// only the files it touched.
func TestComputeEditsSpaceLayerDeclarations(t *testing.T) {
	r := harness.New(t)
	cfg := harness.BaseFile(1)
	cfg.Scripts = map[string]models.Script{"build": {echoBuild}, "publish": {"echo publishing"}}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: models.PathList{"packages"}, Flow: buildPublish(), Packages: map[string]models.PackageConfig{
			"web": {Dependencies: models.Providers("ghost")},
		}},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "ghost")
	r.SeedPackage("packages", "web")
	spaceFile(t, r, "packages", models.SpaceFile{
		Packages: map[string]models.PackageConfig{"core": {Dependencies: models.Providers("ghost")}},
	})
	// Both consumers carry a manifest, so their declarations are judged
	// against something: an edge no manifest declares is stale.
	r.WriteFile("packages/web/package.json", `{"name": "@acme/web"}`)
	r.WriteFile("packages/core/package.json", `{"name": "@acme/core"}`)
	r.Commit("feat(core,ghost,web): bootstrap")

	res := r.Command("compute")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, `spaces["libs"]: packages["web"]: dependencies[0]`,
		"the listing names the space entry that holds the edge")
	assert.Contains(t, res.Stdout, `packages["core"]: dependencies[0]`,
		"and the space file that holds the other")

	require.Equal(t, 0, r.Command("compute", "--write").Code)

	rootAfter, err := os.ReadFile(r.Path("dispat.json"))
	require.NoError(t, err)
	assert.NotContains(t, string(rootAfter), "ghost", "the space entry's edge is gone from the root config")
	spaceAfter, err := os.ReadFile(r.Path("packages", "dispat.json"))
	require.NoError(t, err)
	assert.NotContains(t, string(spaceAfter), "ghost", "and the space file's edge from the space file")
	assert.FileExists(t, r.Path("packages", "dispat.json.backup"), "each edited file keeps its own backup")

	// Both edits together still load, and the packages survive them.
	r.StatusOK()
}

// TestComputeSeedsInitialsFromManifests: a repository adopting dispat already
// carries its versions in its manifests. compute proposes them as initials
// entries, applies them in the same write as the edges it found, and the very
// next status plans from those baselines instead of starting over at 0.0.0.
func TestComputeSeedsInitialsFromManifests(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(libsConfig(echoBuild, 1))
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "web")
	r.WriteFile("packages/core/package.json", `{"name": "@acme/core", "version": "1.4.2"}`)
	r.WriteFile("packages/web/package.json",
		`{"name": "@acme/web", "version": "2.1.0", "dependencies": {"@acme/core": "workspace:*"}}`)
	r.Commit("feat(core,web): bootstrap")

	// Preview: both kinds of suggestion, each with the manifest behind it.
	res := r.Command("compute")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "+ add     web -> core (dependencies)")
	assert.Contains(t, res.Stdout, "+ initial core 1.4.2  packages/core/package.json declares 1.4.2; no release tag yet")
	assert.Contains(t, res.Stdout, "+ initial web 2.1.0")
	configBefore, err := os.ReadFile(r.Path("dispat.json"))
	require.NoError(t, err)
	assert.NotContains(t, string(configBefore), "initials", "preview writes nothing")

	// The gate is red while the config lags the manifests, baselines included.
	assert.Equal(t, 1, r.Command("compute", "--check").Code)

	// Apply: the edge and both baselines land in one write, so the single
	// backup is the config as it stood before any of them.
	res = r.Command("compute", "--write")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "applied 3 change(s)")
	configAfter, err := os.ReadFile(r.Path("dispat.json"))
	require.NoError(t, err)
	assert.Contains(t, string(configAfter), `"core": "1.4.2"`)
	assert.Contains(t, string(configAfter), `"web": "2.1.0"`)
	assert.Contains(t, string(configAfter), `"web": [`, "the edge is keyed by consumer")
	backup, err := os.ReadFile(r.Path("dispat.json.backup"))
	require.NoError(t, err)
	assert.Equal(t, string(configBefore), string(backup), "one backup, from before both edits")

	// The plan now continues the manifests' history rather than starting over.
	status := r.StatusOK()
	versions := map[string]string{}
	for _, e := range status.Events {
		if v := e.Str("version"); v != "" && e.Package() != "" {
			versions[e.Package()] = v
			assert.Equal(t, true, e["baselineFromInitials"], "%s: the baseline is the seeded one", e.Package())
		}
	}
	assert.Equal(t, "1.4.2 -> 1.5.0", versions["core"])
	assert.Equal(t, "2.1.0 -> 2.2.0", versions["web"])

	// And the run converges: a second compute has nothing left to say.
	res = r.Command("compute")
	require.Equal(t, 0, res.Code)
	assert.Contains(t, res.Stdout, "dependencies and baselines are in sync")
	assert.Equal(t, 0, r.Command("compute", "--check").Code)
}

// TestComputeKeepsExistingInitials: an entry already in the config is the
// operator's own statement. compute never rewrites one, whatever the manifest
// says and however the entry is spelled, which is what makes writing the
// entry yourself the way to silence the suggestion for good.
func TestComputeKeepsExistingInitials(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	// Mixed case on purpose: dispat lowercases map keys when it loads them, so
	// a config written back from the parsed map would rename this entry.
	cfg.Initials = map[string]string{"Core": "3.0.0", "web": "0.0.0"}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "web")
	r.WriteFile("packages/core/package.json", `{"name": "@acme/core", "version": "1.4.2"}`)
	r.WriteFile("packages/web/package.json",
		`{"name": "@acme/web", "version": "2.1.0", "dependencies": {"@acme/core": "workspace:*"}}`)
	r.Commit("feat(core,web): bootstrap")

	res := r.Command("compute")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.NotContains(t, res.Stdout, "+ initial", "both packages are already decided")

	// The edge is still applied, and the entries come through it untouched.
	require.Equal(t, 0, r.Command("compute", "--write").Code)
	configAfter, err := os.ReadFile(r.Path("dispat.json"))
	require.NoError(t, err)
	assert.Contains(t, string(configAfter), `"Core": "3.0.0"`, "the author's spelling survives")
	assert.Contains(t, string(configAfter), `"web": "0.0.0"`, "0.0.0 is a decision like any other")

	status := r.StatusOK()
	for _, e := range status.Events {
		if e.Package() == "core" && e.Str("version") != "" {
			assert.Equal(t, "3.0.0 -> 3.1.0", e.Str("version"), "the entry beats the manifest")
		}
	}
}

// TestComputeSkipsPackagesWithReleaseTags: an initials entry is only ever read
// when the tags cannot answer, so that is the only case compute proposes one.
// A package with a parseable release tag is left alone; one whose newest tag
// matches the format but is not a version still needs the entry, and the
// listing says which tag made it necessary.
func TestComputeSkipsPackagesWithReleaseTags(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(libsConfig(echoBuild, 1))
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "web")
	r.SeedPackage("packages", "docs")
	r.WriteFile("packages/core/package.json", `{"name": "@acme/core", "version": "1.4.2"}`)
	r.WriteFile("packages/web/package.json", `{"name": "@acme/web", "version": "2.1.0"}`)
	r.WriteFile("packages/docs/package.json", `{"name": "@acme/docs", "version": "3.0.0"}`)
	r.Commit("feat(core,web,docs): bootstrap")
	r.Git("tag", "-a", "core@1.5.0", "-m", "released")
	r.Git("tag", "-a", "web@1.0.1.0", "-m", "an old convention")

	res := r.Command("compute")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.NotContains(t, res.Stdout, "+ initial core", "a parseable tag is the baseline; initials would never be read")
	assert.Contains(t, res.Stdout, "+ initial web 2.1.0  packages/web/package.json declares 2.1.0; newest tag web@1.0.1.0 is not a version")
	assert.Contains(t, res.Stdout, "+ initial docs 3.0.0")

	require.Equal(t, 0, r.Command("compute", "--write").Code)
	configAfter, err := os.ReadFile(r.Path("dispat.json"))
	require.NoError(t, err)
	assert.NotContains(t, string(configAfter), `"core"`)
	assert.Contains(t, string(configAfter), `"web": "2.1.0"`)
	assert.Contains(t, string(configAfter), `"docs": "3.0.0"`)
	r.StatusOK()
}

// TestComputeVersionsItCannotUse: manifests that disagree about one package's
// version make the answer a guess, reported as W225 and left to the operator.
// A prerelease and a version that is not semver are passed over in silence:
// neither states a released version.
func TestComputeVersionsItCannotUse(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(libsConfig(echoBuild, 1))
	r.SeedPackage("packages", "split")
	r.SeedPackage("packages", "snap")
	r.SeedPackage("packages", "odd")
	r.WriteFile("packages/split/package.json", `{"name": "@acme/split", "version": "1.2.3"}`)
	r.WriteFile("packages/split/Cargo.toml", "[package]\nname = \"split\"\nversion = \"1.2.4\"\n")
	r.WriteFile("packages/snap/pom.xml",
		"<project><groupId>com.acme</groupId><artifactId>snap</artifactId><version>1.0.0-SNAPSHOT</version></project>")
	r.WriteFile("packages/odd/package.json", `{"name": "@acme/odd", "version": "2023.04"}`)
	r.Commit("feat(split,snap,odd): bootstrap")

	res := r.Command("compute", "--check")
	assert.Zero(t, res.Code, "nothing derivable is no drift; stdout:\n%s", res.Stdout)
	assert.True(t, harness.HasCodeForPackage(res.Events, "W225", "split"),
		"the disagreement must reach the events: %s", res.Stdout)
	assert.NotContains(t, res.Stdout, "+ initial")
}

// TestComputeInitialsInYAMLAndTOMLConfigs: the baselines are written the way
// every other config edit is. A YAML config gains the key and keeps its
// comments; a TOML config cannot be rewritten in place, so it gets the block
// to paste and an error instead of a half-applied edit.
func TestComputeInitialsInYAMLAndTOMLConfigs(t *testing.T) {
	t.Run("yaml", func(t *testing.T) {
		r := harness.New(t)
		r.WriteFile("dispat.yaml", `# the repository's release configuration
scripts:
  build: echo building
spaces:
  libs: # every package under packages/
    path: packages
    flow:
      build: [build]
`)
		r.SeedPackage("packages", "core")
		r.WriteFile("packages/core/package.json", `{"name": "@acme/core", "version": "1.4.2"}`)
		r.Commit("feat(core): bootstrap")

		require.Equal(t, 0, r.Command("compute", "--write").Code)
		after, err := os.ReadFile(r.Path("dispat.yaml"))
		require.NoError(t, err)
		assert.Contains(t, string(after), "core: 1.4.2")
		assert.Contains(t, string(after), "# the repository's release configuration")
		assert.Contains(t, string(after), "# every package under packages/")
		r.StatusOK()
	})

	t.Run("toml", func(t *testing.T) {
		r := harness.New(t)
		const body = `[scripts]
build = "echo building"

[spaces.libs]
path = "packages"

[spaces.libs.flow]
build = ["build"]
`
		r.WriteFile("dispat.toml", body)
		r.SeedPackage("packages", "core")
		r.WriteFile("packages/core/package.json", `{"name": "@acme/core", "version": "1.4.2"}`)
		r.Commit("feat(core): bootstrap")

		res := r.Command("compute", "--write")
		assert.Equal(t, 1, res.Code, "a refused edit fails rather than reporting changes it did not make")
		assert.Contains(t, res.Stdout, "# paste over the initials in dispat.toml:")
		assert.Contains(t, res.Stdout, "[initials]")
		assert.Contains(t, res.Stdout, "core = '1.4.2'")
		after, err := os.ReadFile(r.Path("dispat.toml"))
		require.NoError(t, err)
		assert.Equal(t, body, string(after), "the config is left as it was found")
		assert.NoFileExists(t, r.Path("dispat.toml.backup"))
	})
}

// TestComputeSeedsInitialsBeforeTheFirstCommit: adopting dispat often starts
// in a repository whose first commit has not happened yet. Nothing can be
// reachable from a HEAD that does not exist, so every package is a first
// release and the baselines are proposed without a single tag query.
func TestComputeSeedsInitialsBeforeTheFirstCommit(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(libsConfig(echoBuild, 1))
	r.SeedPackage("packages", "core")
	r.WriteFile("packages/core/package.json", `{"name": "@acme/core", "version": "1.4.2"}`)

	res := r.Command("compute")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "+ initial core 1.4.2  packages/core/package.json declares 1.4.2; the repository has no commits yet")

	require.Equal(t, 0, r.Command("compute", "--write").Code)
	configAfter, err := os.ReadFile(r.Path("dispat.json"))
	require.NoError(t, err)
	assert.Contains(t, string(configAfter), `"core": "1.4.2"`)

	// Committing the seeded repository, the first release continues from the
	// manifest's version.
	r.Commit("feat(core): bootstrap")
	status := r.StatusOK()
	for _, e := range status.Events {
		if e.Package() == "core" && e.Str("version") != "" {
			assert.Equal(t, "1.4.2 -> 1.5.0", e.Str("version"))
		}
	}
}

// TestComputeInteractiveChoosesAmongBothKinds: the prompt walks the edges and
// the baselines in one pass, and each answer stands on its own — taking the
// graph now and leaving a version to think about is a supported way to adopt
// dispat, so it has to survive the process boundary.
func TestComputeInteractiveChoosesAmongBothKinds(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(libsConfig(echoBuild, 1))
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "web")
	r.WriteFile("packages/core/package.json", `{"name": "@acme/core", "version": "1.4.2"}`)
	r.WriteFile("packages/web/package.json",
		`{"name": "@acme/web", "version": "2.1.0", "dependencies": {"@acme/core": "workspace:*"}}`)
	r.Commit("feat(core,web): bootstrap")

	// The edge, then core's baseline, then web's: take the first two.
	res := r.CommandInput("y\ny\nn\n", "compute", "--interactive")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "applied 2 change(s)")

	configAfter, err := os.ReadFile(r.Path("dispat.json"))
	require.NoError(t, err)
	assert.Contains(t, string(configAfter), `"web": [`, "the edge is keyed by consumer")
	assert.Contains(t, string(configAfter), `"core": "1.4.2"`)
	assert.NotContains(t, string(configAfter), `"web": "2.1.0"`, "the declined baseline was not written")

	// What was declined is still offered next time, and nothing else is.
	res = r.Command("compute")
	require.Equal(t, 0, res.Code)
	assert.Contains(t, res.Stdout, "+ initial web 2.1.0")
	assert.NotContains(t, res.Stdout, "+ add")
	assert.NotContains(t, res.Stdout, "+ initial core")
}

// TestComputeWritesThroughARef: a configuration that keeps its packages in a
// referenced fragment is still writable. compute edits the fragment, at the
// key the fragment holds, and the reference in the root config survives — the
// alternative, flattening the fragment into the file that named it, would undo
// the split on the first write.
func TestComputeWritesThroughARef(t *testing.T) {
	r := harness.New(t)
	r.WriteFile("cfg/packages.json", `{"web": {"dependencies": ["ghost"]}}`)
	r.WriteConfigRaw(map[string]any{
		"logLevel":    "info",
		"logFormat":   "json",
		"github":      map[string]any{"enabled": false},
		"updateCheck": false,
		"scripts":     map[string]any{"build": echoBuild, "publish": "echo publishing"},
		"spaces": map[string]any{
			"libs": map[string]any{"path": "packages",
				"flow": map[string]any{"build": []string{"build"}, "publish": []string{"publish"}}},
		},
		"packages": map[string]any{"$ref": "./cfg/packages.json"},
	})
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "ghost")
	r.SeedPackage("packages", "web")
	r.WriteFile("packages/web/package.json", `{"name": "@acme/web", "dependencies": {"@acme/core": "workspace:*"}}`)
	r.WriteFile("packages/core/package.json", `{"name": "@acme/core"}`)
	r.Commit("feat(core,ghost,web): bootstrap")

	res := r.Command("compute", "--write")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)

	fragment, err := os.ReadFile(r.Path("cfg", "packages.json"))
	require.NoError(t, err)
	assert.Contains(t, string(fragment), "core", "the detected edge landed in the fragment")
	assert.NotContains(t, string(fragment), "ghost", "and the stale one was removed there")
	assert.FileExists(t, r.Path("cfg", "packages.json.backup"), "the file it wrote keeps the backup")

	root, err := os.ReadFile(r.Path("dispat.json"))
	require.NoError(t, err)
	assert.Contains(t, string(root), `"$ref": "./cfg/packages.json"`, "the reference survived the write")
	assert.NoFileExists(t, r.Path("dispat.json.backup"), "the file that was not edited was not backed up")

	// The rewritten split configuration still loads, and status reads the
	// edge compute just wrote.
	next := r.StatusOK()
	assert.Contains(t, next.Stdout, "web")
}

// TestComputeRefusesAComposedKey: a key made of a fragment *and* the keys
// written beside the reference comes from two files at once, so compute
// refuses rather than guessing which file to write, and says what to change.
func TestComputeRefusesAComposedKey(t *testing.T) {
	r := harness.New(t)
	r.WriteFile("cfg/deps.json", `{"web": ["ghost"]}`)
	r.WriteConfigRaw(map[string]any{
		"logLevel":    "info",
		"logFormat":   "json",
		"github":      map[string]any{"enabled": false},
		"updateCheck": false,
		"scripts":     map[string]any{"build": echoBuild, "publish": "echo publishing"},
		"spaces": map[string]any{
			"libs": map[string]any{"path": "packages",
				"flow": map[string]any{"build": []string{"build"}, "publish": []string{"publish"}}},
		},
		"dependencies": map[string]any{"$ref": "./cfg/deps.json", "core": []string{"ghost"}},
	})
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "ghost")
	r.SeedPackage("packages", "web")
	r.WriteFile("packages/web/package.json", `{"name": "@acme/web"}`)
	r.WriteFile("packages/core/package.json", `{"name": "@acme/core"}`)
	r.Commit("feat(core,ghost,web): bootstrap")

	before, err := os.ReadFile(r.Path("dispat.json"))
	require.NoError(t, err)

	res := r.Command("compute", "--write")
	assert.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout+res.Stderr, "cannot be rewritten in place")

	after, err := os.ReadFile(r.Path("dispat.json"))
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "a refused write leaves every file as it was")
	assert.NoFileExists(t, r.Path("dispat.json.backup"))
	assert.NoFileExists(t, r.Path("cfg", "deps.json.backup"))
}
