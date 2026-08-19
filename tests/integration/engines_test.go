package integration

// Area 17: the game engine manifests through the compiled binary. Unity,
// Godot, Unreal, Defold and O3DE keep their versions in files no package
// manager understands, and this file is the proof that dispat now reads and
// writes them like any other manifest: through the same commands, the same
// exit codes and the same event stream, with no `replace` rule configured
// anywhere.
//
// What only a process-boundary test can witness is here: that a whole engine
// monorepo scans to the right set of files, that the folders each engine
// generates contribute nothing, that a release run reaches every engine
// manifest on its own, and that a build stamp moves every store's counter.

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// The engine fixtures of this file, each the shape its engine writes.
const (
	unityPackagesManifest = `{
  "dependencies": {
    "com.unity.textmeshpro": "3.0.6",
    "com.acme.core": "file:../../../packages/core"
  }
}
`
	unityProjectSettings = `%YAML 1.1
%TAG !u! tag:unity3d.com,2011:
--- !u!129 &1
PlayerSettings:
  productName: Acme Client
  bundleVersion: 1.2.0
  buildNumber:
    Standalone: 3
    iPhone: 3
  AndroidBundleVersionCode: 3
`
	godotProject = `[application]

config/name="Acme Editor"
config/version="1.2.0"
config/features=PackedStringArray("4.3")
`
	godotPlugin = `[plugin]

name="Acme Tools"
version="1.2.0"
script="plugin.gd"
`
	godotExportPresets = `[preset.0]

name="Android"

[preset.0.options]

version/code=3
version/name="1.2.0"

[preset.1]

name="iOS"

[preset.1.options]

application/short_version="1.2.0"
application/version="1.2.0"
`
	unrealProject = `{
	"FileVersion": 3,
	"EngineAssociation": "5.4",
	"Plugins": [
		{ "Name": "AcmeNet", "Enabled": true }
	]
}
`
	unrealPlugin = `{
	"FileVersion": 3,
	"Version": 3,
	"VersionName": "1.2.0",
	"Plugins": []
}
`
	unrealGameConfig = `[/Script/EngineSettings.GeneralProjectSettings]
ProjectName=Acme Server
ProjectVersion=1.2.0.0
`
	unrealEngineConfig = `[/Script/AndroidRuntimeSettings.AndroidRuntimeSettings]
StoreVersion=3
VersionDisplayName=1.2.0
`
	defoldProject = `[project]
title = Acme Game
version = 1.2.0
dependencies#0 = https://github.com/britzl/deftest/archive/2.6.0.zip
`
	o3deGem = `{
    "gem_name": "AcmeGem",
    "version": "1.2.0",
    "dependencies": [
        "Atom_RHI==1.0.0"
    ]
}
`
)

// writeEngineMonorepo lays out one repository holding a project of every
// engine, together with the folders each engine generates beside them.
func writeEngineMonorepo(r *harness.Repo) {
	for name, src := range map[string]string{
		"apps/client/Packages/manifest.json":                unityPackagesManifest,
		"apps/client/ProjectSettings/ProjectSettings.asset": unityProjectSettings,
		"packages/core/package.json":                        `{"name":"com.acme.core","version":"0.4.0"}`,
		"apps/server/Server.uproject":                       unrealProject,
		"apps/server/Config/DefaultGame.ini":                unrealGameConfig,
		"apps/server/Config/DefaultEngine.ini":              unrealEngineConfig,
		"apps/server/Plugins/AcmeNet/AcmeNet.uplugin":       unrealPlugin,
		"tools/editor/project.godot":                        godotProject,
		"tools/editor/addons/acme/plugin.cfg":               godotPlugin,
		"tools/editor/export_presets.cfg":                   godotExportPresets,
		"tools/pipeline/game.project":                       defoldProject,
		"gems/acme/gem.json":                                o3deGem,

		// What the engines leave behind. Unity's package cache holds a real
		// package.json per resolved package, and Unreal's build folders hold
		// copies of the descriptors beside them.
		"apps/client/Library/PackageCache/com.unity.burst@1.4.11/package.json": `{"name":"com.unity.burst","version":"1.4.11"}`,
		"apps/client/Temp/junk/package.json":                                   `{"name":"temp-junk","version":"0.0.0"}`,
		"apps/server/Intermediate/Build/Copy.uproject":                         `{"Plugins":[{"Name":"Ghost"}]}`,
		"apps/server/Binaries/Win64/Stale.uplugin":                             `{"VersionName":"0.0.1"}`,

		// The name manifest.json means nearly everywhere else.
		"apps/site/public/manifest.json": `{"name":"Acme","icons":[]}`,
	} {
		r.WriteFile(name, src)
	}
}

// TestEnginesScannerReadsEveryEngineFormat: one walk over an engine monorepo
// lists every engine manifest with its identity and ecosystem, and the folders
// the engines generate contribute nothing at all — which is the difference
// between a useful scan of a Unity project and a few hundred third-party
// packages reported as members of the workspace.
func TestEnginesScannerReadsEveryEngineFormat(t *testing.T) {
	r := harness.New(t)
	writeEngineMonorepo(r)

	res := r.Command("scanner", ".", "--log-format", "json")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)

	found := map[string]harness.Event{}
	for _, e := range res.Events {
		if e.Str("message") == "manifest" {
			found[e.Str("path")] = e
		}
	}

	for path, want := range map[string]struct{ ecosystem, name, version string }{
		"apps/client/Packages/manifest.json":                {"unity", "", ""},
		"apps/client/ProjectSettings/ProjectSettings.asset": {"unity", "Acme Client", "1.2.0"},
		"apps/server/Server.uproject":                       {"unreal", "Server", ""},
		"apps/server/Plugins/AcmeNet/AcmeNet.uplugin":       {"unreal", "AcmeNet", "1.2.0"},
		"apps/server/Config/DefaultGame.ini":                {"unreal", "Acme Server", "1.2.0.0"},
		"apps/server/Config/DefaultEngine.ini":              {"unreal", "", "1.2.0"},
		"tools/editor/project.godot":                        {"godot", "Acme Editor", "1.2.0"},
		"tools/editor/addons/acme/plugin.cfg":               {"godot", "Acme Tools", "1.2.0"},
		"tools/editor/export_presets.cfg":                   {"godot", "Android", "1.2.0"},
		"tools/pipeline/game.project":                       {"defold", "Acme Game", "1.2.0"},
		"gems/acme/gem.json":                                {"o3de", "AcmeGem", "1.2.0"},
	} {
		ev, ok := found[path]
		if !assert.True(t, ok, "%s was not scanned", path) {
			continue
		}
		assert.Equal(t, want.ecosystem, ev.Str("ecosystem"), path)
		assert.Equal(t, want.name, ev.Str("name"), path)
		assert.Equal(t, want.version, ev.Str("version"), path)
	}

	// Asserted positively, because a skip rule that stopped firing shows up as
	// a path nobody looked for rather than as a failing count.
	for path := range found {
		for _, generated := range []string{"Library/", "Temp/", "Intermediate/", "Binaries/"} {
			assert.NotContains(t, path, generated, "generated output must not be scanned")
		}
	}
	assert.NotContains(t, found, "apps/site/public/manifest.json",
		"a web app manifest is not a Unity package manifest")
}

// TestEnginesScannerReadsTheDependencyEdges: the graph an engine repository
// has. Unity declares a package by folder, Unreal declares a plugin by name
// with no version at all, and both are edges a release order depends on.
func TestEnginesScannerReadsTheDependencyEdges(t *testing.T) {
	r := harness.New(t)
	writeEngineMonorepo(r)

	res := r.Command("scanner", ".", "--log-format", "json")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)

	for _, e := range res.Events {
		if e.Str("message") != "manifest" {
			continue
		}
		switch e.Str("path") {
		case "apps/client/Packages/manifest.json":
			deps, ok := e["deps"].([]any)
			require.True(t, ok, "the Unity manifest declares dependencies: %v", e)
			require.Len(t, deps, 2)
			var core map[string]any
			for _, d := range deps {
				if m := d.(map[string]any); m["name"] == "com.acme.core" {
					core = m
				}
			}
			require.NotNil(t, core, "com.acme.core is declared")
			assert.Equal(t, "file:../../../packages/core", core["range"],
				"a Unity folder range is kept verbatim")
			assert.Equal(t, "../../../packages/core", core["localPath"],
				"and yields the local path that makes it a workspace edge")
		case "apps/server/Server.uproject":
			deps, ok := e["deps"].([]any)
			require.True(t, ok, "the descriptor declares its plugins: %v", e)
			require.Len(t, deps, 1)
			plugin := deps[0].(map[string]any)
			assert.Equal(t, "AcmeNet", plugin["name"])
			assert.Empty(t, plugin["range"], "an Unreal plugin declares no version")
		}
	}
}

// TestEnginesPathQualifiedFormatsResolve: the four formats told apart by the
// folder they sit in, over a process boundary. This is the case that would
// otherwise write a version into every web app manifest in the world.
func TestEnginesPathQualifiedFormatsResolve(t *testing.T) {
	r := harness.New(t)
	r.WriteFile("game/Packages/manifest.json", unityPackagesManifest)
	r.WriteFile("game/ProjectSettings/ProjectSettings.asset", unityProjectSettings)
	r.WriteFile("game/Config/DefaultGame.ini", unrealGameConfig)
	r.WriteFile("web/public/manifest.json", `{"name":"Acme","icons":[]}`)
	r.WriteFile("web/assets/ProjectSettings.asset", "not a Unity settings file\n")

	res := r.Command("scanner", ".")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "Packages/manifest.json")
	assert.Contains(t, res.Stdout, "ProjectSettings/ProjectSettings.asset")
	assert.Contains(t, res.Stdout, "Config/DefaultGame.ini")
	assert.NotContains(t, res.Stdout, "public/manifest.json")
	assert.NotContains(t, res.Stdout, "assets/ProjectSettings.asset")
	assert.Contains(t, res.Stdout, "3 manifest(s)")

	// The writer resolves on the same rule, and refuses the ones it cannot
	// place rather than guessing.
	assert.Equal(t, 1, r.Command("writer", "--set-version", "2.0.0", "web/public/manifest.json").Code,
		"a web app manifest has no writer")
	ok := r.Command("writer", "--set-version", "2.0.0", "game/ProjectSettings/ProjectSettings.asset")
	assert.Equal(t, 0, ok.Code, "stderr:\n%s", ok.Stderr)
}

// TestEnginesWriterWritesEachVersion: one batch spanning all five ecosystems,
// each format's own version field rewritten and every other byte left alone,
// and a second pass converging to nothing written.
func TestEnginesWriterWritesEachVersion(t *testing.T) {
	r := harness.New(t)
	writeEngineMonorepo(r)

	paths := []string{
		"apps/client/ProjectSettings/ProjectSettings.asset",
		"apps/server/Plugins/AcmeNet/AcmeNet.uplugin",
		"apps/server/Config/DefaultGame.ini",
		"tools/editor/project.godot",
		"tools/editor/addons/acme/plugin.cfg",
		"tools/editor/export_presets.cfg",
		"tools/pipeline/game.project",
		"gems/acme/gem.json",
	}
	res := r.Command(append([]string{"writer", "--set-version", "1.3.0"}, paths...)...)
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)

	for file, want := range map[string]string{
		"apps/client/ProjectSettings/ProjectSettings.asset": "bundleVersion: 1.3.0",
		"apps/server/Plugins/AcmeNet/AcmeNet.uplugin":       `"VersionName": "1.3.0"`,
		"tools/editor/project.godot":                        `config/version="1.3.0"`,
		"tools/editor/addons/acme/plugin.cfg":               `version="1.3.0"`,
		"tools/editor/export_presets.cfg":                   `version/name="1.3.0"`,
		"tools/pipeline/game.project":                       "version = 1.3.0",
		"gems/acme/gem.json":                                `"version": "1.3.0"`,
	} {
		data, err := os.ReadFile(r.Path(file))
		require.NoError(t, err)
		assert.Contains(t, string(data), want, file)
	}

	// Every build counter is where it was. A version write is not a build
	// stamp, and the two move for different reasons.
	for file, want := range map[string]string{
		"apps/client/ProjectSettings/ProjectSettings.asset": "AndroidBundleVersionCode: 3",
		"apps/server/Plugins/AcmeNet/AcmeNet.uplugin":       `"Version": 3`,
		"tools/editor/export_presets.cfg":                   "version/code=3",
	} {
		data, err := os.ReadFile(r.Path(file))
		require.NoError(t, err)
		assert.Contains(t, string(data), want, "%s: the counter must not move", file)
	}

	// The Unreal descriptor writes its ProjectVersion in the four-component
	// shape the file already used, because that text is what the engine reads.
	game, err := os.ReadFile(r.Path("apps/server/Config/DefaultGame.ini"))
	require.NoError(t, err)
	assert.Contains(t, string(game), "ProjectVersion=1.3.0")

	written := map[string][]byte{}
	for _, file := range paths {
		data, err := os.ReadFile(r.Path(file))
		require.NoError(t, err)
		written[file] = data
	}
	again := r.Command(append([]string{"writer", "--set-version", "1.3.0"}, paths...)...)
	require.Equal(t, 0, again.Code, "stderr:\n%s", again.Stderr)
	assert.Contains(t, again.Stdout, "0 applied, 0 skipped, 0 missing",
		"a second pass has nothing to do")
	for _, file := range paths {
		after, err := os.ReadFile(r.Path(file))
		require.NoError(t, err)
		assert.Equal(t, string(written[file]), string(after), "%s: a converged run rewrote it", file)
	}
}

// TestEnginesWriterSetBuildWritesEveryCounter: --set-build moves the counter
// each engine keeps and only the counter, across every Unity platform and
// every Godot preset. A project that stamps one store and leaves the others
// behind uploads builds the stores order wrongly and says nothing about it.
func TestEnginesWriterSetBuildWritesEveryCounter(t *testing.T) {
	r := harness.New(t)
	writeEngineMonorepo(r)

	res := r.Command("writer", "--set-build", "42", "--log-format", "json",
		"apps/client/ProjectSettings/ProjectSettings.asset",
		"apps/server/Plugins/AcmeNet/AcmeNet.uplugin",
		"apps/server/Config/DefaultEngine.ini",
		"tools/editor/export_presets.cfg")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)

	updated := 0
	for _, e := range res.Events {
		if e.Str("message") == "manifest updated" {
			updated++
			assert.Equal(t, true, e["buildWritten"], "the event says what moved: %v", e)
			assert.Equal(t, false, e["versionWritten"], "a build stamp is not a version write: %v", e)
		}
	}
	assert.Equal(t, 4, updated)

	settings, err := os.ReadFile(r.Path("apps/client/ProjectSettings/ProjectSettings.asset"))
	require.NoError(t, err)
	assert.Contains(t, string(settings), "AndroidBundleVersionCode: 42")
	assert.Contains(t, string(settings), "Standalone: 42")
	assert.Contains(t, string(settings), "iPhone: 42", "every platform counter moves, not the first")
	assert.Contains(t, string(settings), "bundleVersion: 1.2.0", "the marketing version stays")

	plugin, err := os.ReadFile(r.Path("apps/server/Plugins/AcmeNet/AcmeNet.uplugin"))
	require.NoError(t, err)
	assert.Contains(t, string(plugin), `"Version": 42`, "the counter is a bare integer, never quoted")
	assert.NotContains(t, string(plugin), `"Version": "42"`)
	assert.Contains(t, string(plugin), `"VersionName": "1.2.0"`)

	engine, err := os.ReadFile(r.Path("apps/server/Config/DefaultEngine.ini"))
	require.NoError(t, err)
	assert.Contains(t, string(engine), "StoreVersion=42")
	assert.Contains(t, string(engine), "VersionDisplayName=1.2.0")

	presets, err := os.ReadFile(r.Path("tools/editor/export_presets.cfg"))
	require.NoError(t, err)
	assert.Contains(t, string(presets), "version/code=42")
	assert.Contains(t, string(presets), `version/name="1.2.0"`)

	// The scanner reads every counter back, which is the pair's whole contract.
	scan := r.Command("scanner", ".", "--log-format", "json")
	require.Equal(t, 0, scan.Code)
	for _, e := range scan.Events {
		if e.Str("message") == "manifest" && e.Str("path") == "apps/client/ProjectSettings/ProjectSettings.asset" {
			assert.Equal(t, "42", e.Str("buildNumber"))
		}
	}

	// Every engine counter is an integer to the platform that reads it.
	for _, file := range []string{
		"apps/client/ProjectSettings/ProjectSettings.asset",
		"apps/server/Plugins/AcmeNet/AcmeNet.uplugin",
		"apps/server/Config/DefaultEngine.ini",
		"tools/editor/export_presets.cfg",
	} {
		assert.Equal(t, 1, r.Command("writer", "--set-build", "1.2.3", file).Code,
			"%s: a version where an integer is required is refused", file)
	}
}

// TestEnginesUnrealVersionlessPluginsAreSkipped: the Missing/Skipped split
// carried over a process boundary, and what --strict does with each. A plugin
// the descriptor lists is declared but carries no version text, so warning
// about it on every run would be noise; one it does not list is a real
// disagreement between the caller and the file.
func TestEnginesUnrealVersionlessPluginsAreSkipped(t *testing.T) {
	r := harness.New(t)
	r.WriteFile("Server.uproject", unrealProject)

	res := r.Command("writer", "--set", "AcmeNet=1.3.0", "--log-format", "json", "Server.uproject")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	summary := findEvent(t, res.Events, "write complete")
	assert.Equal(t, float64(0), summary["applied"])
	assert.Equal(t, float64(1), summary["skipped"], "a declared plugin is skipped")
	assert.Equal(t, float64(0), summary["missing"])

	strict := r.Command("writer", "--set", "AcmeNet=1.3.0", "--strict", "Server.uproject")
	assert.Equal(t, 0, strict.Code, "--strict does not fail on a skipped declaration")

	res = r.Command("writer", "--set", "Ghost=1.3.0", "--log-format", "json", "Server.uproject")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	summary = findEvent(t, res.Events, "write complete")
	assert.Equal(t, float64(1), summary["missing"], "an undeclared plugin is missing")

	assert.Equal(t, 1, r.Command("writer", "--set", "Ghost=1.3.0", "--strict", "Server.uproject").Code,
		"--strict fails on a missing declaration")
}

// TestEnginesGracefulOnPartialProjects: the states a healthy engine repository
// is routinely in. None of them is an error, and none of them writes anything.
func TestEnginesGracefulOnPartialProjects(t *testing.T) {
	r := harness.New(t)
	// A Godot project that never set config/version, which is the normal state
	// of one versioned by its git tags.
	r.WriteFile("game/project.godot", "[application]\n\nconfig/name=\"Acme\"\n")
	// A plugin descriptor with no counter.
	r.WriteFile("plugin/AcmeNet.uplugin", `{"VersionName":"1.0.0"}`)
	// A Unity settings file that tracks no counter.
	r.WriteFile("client/ProjectSettings/ProjectSettings.asset", "PlayerSettings:\n  bundleVersion: 1.0.0\n")

	res := r.Command("writer", "--set-version", "2.0.0", "game/project.godot")
	assert.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	data, err := os.ReadFile(r.Path("game/project.godot"))
	require.NoError(t, err)
	assert.NotContains(t, string(data), "config/version",
		"a key the project never declared must not be created")

	for _, file := range []string{"plugin/AcmeNet.uplugin", "client/ProjectSettings/ProjectSettings.asset"} {
		before, err := os.ReadFile(r.Path(file))
		require.NoError(t, err)
		build := r.Command("writer", "--set-build", "7", "--log-format", "json", file)
		assert.Equal(t, 0, build.Code, "%s: a counter that is not there is not an error", file)
		warned := false
		for _, e := range build.Events {
			if strings.Contains(e.Str("message"), "build counter not written") {
				warned = true
				assert.Equal(t, "warn", e.Str("level"), "%s: it is a warning", file)
			}
		}
		assert.True(t, warned, "%s: the run says so rather than passing in silence", file)
		after, err := os.ReadFile(r.Path(file))
		require.NoError(t, err)
		assert.Equal(t, string(before), string(after), "%s must be untouched", file)
	}

	// An export_presets.cfg is frequently kept out of version control, because
	// a preset can name a signing keystore. Its absence is normal.
	assert.Equal(t, 0, r.Command("scanner", "game").Code)
}

// TestEnginesAutoVersionWritesTheEngineVersion: the point of the whole
// feature. A release run computes a version from the commits and writes it
// into the engine's own file, with no `replace` rule configured anywhere.
func TestEnginesAutoVersionWritesTheEngineVersion(t *testing.T) {
	r := harness.New(t)
	cfg := harness.BaseFile()
	cfg.Scripts = map[string]models.Script{"build": {"echo built"}, "publish": {"echo published"}}
	cfg.Spaces = map[string]models.SpaceConfig{
		"games": {
			Path: models.PathList{"apps"},
			Flow: buildPublish(),
			// A wholly empty block is pruned by the loader, so the opt-in
			// is said outright.
			AutoVersion: &models.AutoVersionConfig{Enabled: models.Bool(true)},
		},
	}
	cfg.Initials = map[string]string{"client": "1.2.0"}
	r.WriteConfigModel(cfg)
	r.WriteFile("apps/client/project.godot", godotProject)
	r.Commit("feat(client): co-op lobby")

	res := r.Command()
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)

	data, err := os.ReadFile(r.Path("apps/client/project.godot"))
	require.NoError(t, err)
	assert.Contains(t, string(data), `config/version="1.3.0"`,
		"the computed version reaches the engine's own file with no replace rule")
	assert.Contains(t, string(data), `config/features=PackedStringArray("4.3")`,
		"and everything else survives")
}

// TestEnginesAutoVersionWritesAPathQualifiedManifest: the same point for the
// four formats the engine, not the author, decided the folder of. Unity keeps
// its settings under ProjectSettings/ and Unreal keeps its version under
// Config/, so neither file sits directly in the package and both are still the
// package's own. A release writes them like any other manifest of the package.
func TestEnginesAutoVersionWritesAPathQualifiedManifest(t *testing.T) {
	r := harness.New(t)
	cfg := harness.BaseFile()
	cfg.Scripts = map[string]models.Script{"build": {"echo built"}, "publish": {"echo published"}}
	cfg.Spaces = map[string]models.SpaceConfig{
		"games": {
			Path: models.PathList{"apps"},
			Flow: buildPublish(),
			// `all` is what reaches a manifest a folder down; `root` never
			// parses one, so there would be nothing to write either way.
			AutoVersion: &models.AutoVersionConfig{Enabled: models.Bool(true), Manifests: "all"},
		},
	}
	cfg.Initials = map[string]string{"client": "1.2.0", "shooter": "1.2.0"}
	r.WriteConfigModel(cfg)
	r.WriteFile("apps/client/ProjectSettings/ProjectSettings.asset", unityProjectSettings)
	r.WriteFile("apps/shooter/Config/DefaultGame.ini", unrealGameConfig)
	// One commit naming both packages: the harness commits what is pending,
	// so a second commit here would have nothing to stage.
	r.Commit("feat(client, shooter): engine versions move together")

	res := r.Command()
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)

	unity, err := os.ReadFile(r.Path("apps/client/ProjectSettings/ProjectSettings.asset"))
	require.NoError(t, err)
	assert.Contains(t, string(unity), "bundleVersion: 1.3.0",
		"the computed version reaches Unity's settings file")
	assert.Contains(t, string(unity), "AndroidBundleVersionCode: 3",
		"and the build counter beside it is left where it was")

	unreal, err := os.ReadFile(r.Path("apps/shooter/Config/DefaultGame.ini"))
	require.NoError(t, err)
	assert.Contains(t, string(unreal), "ProjectVersion=1.3.0",
		"and Unreal's project version with it")
}

// TestEnginesAutoVersionLeavesANestedCopyAlone: the other half of the rule
// above. A path-qualified manifest is the package's own where its format says
// it lives and nowhere else, so a sample project bundled inside the package
// keeps the version its author gave it.
func TestEnginesAutoVersionLeavesANestedCopyAlone(t *testing.T) {
	r := harness.New(t)
	cfg := harness.BaseFile()
	cfg.Scripts = map[string]models.Script{"build": {"echo built"}, "publish": {"echo published"}}
	cfg.Spaces = map[string]models.SpaceConfig{
		"games": {
			Path:        models.PathList{"apps"},
			Flow:        buildPublish(),
			AutoVersion: &models.AutoVersionConfig{Enabled: models.Bool(true), Manifests: "all"},
		},
	}
	cfg.Initials = map[string]string{"client": "1.2.0"}
	r.WriteConfigModel(cfg)
	r.WriteFile("apps/client/ProjectSettings/ProjectSettings.asset", unityProjectSettings)
	r.WriteFile("apps/client/Samples/Demo/ProjectSettings/ProjectSettings.asset",
		strings.Replace(unityProjectSettings, "bundleVersion: 1.2.0", "bundleVersion: 9.9.9", 1))
	r.Commit("feat(client): co-op lobby")

	res := r.Command()
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)

	own, err := os.ReadFile(r.Path("apps/client/ProjectSettings/ProjectSettings.asset"))
	require.NoError(t, err)
	assert.Contains(t, string(own), "bundleVersion: 1.3.0", "the package's own settings move")

	sample, err := os.ReadFile(r.Path("apps/client/Samples/Demo/ProjectSettings/ProjectSettings.asset"))
	require.NoError(t, err)
	assert.Contains(t, string(sample), "bundleVersion: 9.9.9",
		"a settings file bundled inside the package is somebody else's version story")
}

// TestEnginesEventsNameTheFormat: five ecosystems now cover twelve formats,
// so the ecosystem alone no longer says which reader ran. The machine contract
// carries the format beside it.
func TestEnginesEventsNameTheFormat(t *testing.T) {
	r := harness.New(t)
	r.WriteFile("game/project.godot", godotProject)
	r.WriteFile("game/export_presets.cfg", godotExportPresets)

	// The listing a terminal gets names the ecosystem, which is the short
	// answer. The machine contract carries the format beside it, which is the
	// precise one.
	quiet := r.Command("scanner", "game")
	require.Equal(t, 0, quiet.Code)
	assert.Contains(t, quiet.Stdout, "godot")

	loud := r.Command("scanner", "game", "--log-format", "json")
	require.Equal(t, 0, loud.Code, "stderr:\n%s", loud.Stderr)
	formats := map[string]bool{}
	for _, e := range loud.Events {
		if e.Str("message") == "manifest" {
			assert.Equal(t, "godot", e.Str("ecosystem"))
			formats[e.Str("format")] = true
		}
	}
	assert.True(t, formats["godot-project"], "the format is named: %v", formats)
	assert.True(t, formats["godot-export-presets"],
		"two formats of one ecosystem are told apart: %v", formats)
}

// TestEnginesUnityRangesArePinned: Unity's package manager resolves an exact
// version and nothing else, so every range keyword pins rather than writing a
// caret the project could not open.
func TestEnginesUnityRangesArePinned(t *testing.T) {
	r := harness.New(t)
	r.WriteFile("apps/client/Packages/manifest.json", unityPackagesManifest)
	r.WriteFile("packages/core/package.json", `{"name":"com.acme.core","version":"0.4.0"}`)

	res := r.Command("writer", "--set", "com.unity.textmeshpro=3.0.7",
		"apps/client/Packages/manifest.json")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	data, err := os.ReadFile(r.Path("apps/client/Packages/manifest.json"))
	require.NoError(t, err)
	assert.Contains(t, string(data), `"com.unity.textmeshpro": "3.0.7"`)
	assert.NotContains(t, string(data), "^", "UPM resolves no caret")
	assert.Contains(t, string(data), `"com.acme.core": "file:../../../packages/core"`,
		"the folder range is untouched")
}

// TestEnginesScannerStrictGatesBrokenEngineManifests: the partial-result
// contract reaching the exit code for the engine formats too.
func TestEnginesScannerStrictGatesBrokenEngineManifests(t *testing.T) {
	r := harness.New(t)
	r.WriteFile("game/Packages/manifest.json", `{"dependencies": {`)
	r.WriteFile("game/project.godot", godotProject)

	res := r.Command("scanner", "game")
	assert.Equal(t, 0, res.Code, "a broken manifest is reported, not fatal")
	assert.Contains(t, res.Stdout, "project.godot", "the healthy manifest is still listed")
	assert.Contains(t, res.Stdout, "manifest failed to parse")

	strict := r.Command("scanner", "game", "--strict")
	assert.Equal(t, 1, strict.Code, "--strict refuses the same repository")
	assert.Contains(t, strict.Stdout, "project.godot", "with the partial result still printed")
}

// TestEnginesComputeReadsTheEngineGraph: `dispat compute` derives its edges
// from the same manifests, so an Unreal plugin and the project that enables it
// come out as a dependency the release order has to respect.
func TestEnginesComputeReadsTheEngineGraph(t *testing.T) {
	r := harness.New(t)
	cfg := harness.BaseFile()
	cfg.Spaces = map[string]models.SpaceConfig{
		"apps":    {Path: models.PathList{"apps"}},
		"plugins": {Path: models.PathList{"plugins"}},
	}
	r.WriteConfigModel(cfg)
	r.WriteFile("apps/server/Server.uproject", unrealProject)
	r.WriteFile("plugins/acmenet/AcmeNet.uplugin", unrealPlugin)

	res := r.Command("compute")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.True(t,
		strings.Contains(res.Stdout, "acmenet") || strings.Contains(res.Stdout, "AcmeNet"),
		"the versionless plugin edge is still an edge:\n%s", res.Stdout)
}
