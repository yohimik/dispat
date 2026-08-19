package scanner

// Engine manifests fetched from public repositories on 2026-08-19, trimmed to
// the parts that matter and otherwise byte-for-byte. They exist for the same
// reason the fixtures in realworld_test.go do: a hand-written fixture agrees
// with the parser by construction, and these do not.
//
// Two of them broke an assumption on first contact. VRExpansionPlugin writes
// "Version": 5.8, a float where Unreal's own schema says integer, so the
// counter cannot be read as a number. And Dodge the Creeps declares no
// config/version at all, which is the normal state of a Godot project that
// versions itself by git tag.
//
//	BoatAttack        github.com/Unity-Technologies/BoatAttack, master
//	Klak              github.com/keijiro/Klak, master
//	Dodge the Creeps  github.com/godotengine/godot-demo-projects, master
//	GUT               github.com/bitwes/Gut, master
//	HTerrain          github.com/Zylann/godot_heightmap_plugin, master
//	VRExpansion       github.com/mordentral/VRExpansionPlugin, master
//	SocketIOClient    github.com/getnamo/SocketIOClient-Unreal, master
//	Defold-Input      github.com/britzl/defold-input, master
//	Atom_RPI          github.com/o3de/o3de, development
//	DefaultProject    github.com/o3de/o3de, development

import (
	"reflect"
	"testing"
)

const (
	unityBoatAttackPackages = `{
  "dependencies": {
    "com.unity.2d.sprite": "1.0.0",
    "com.unity.addressables": "1.19.11",
    "com.unity.burst": "1.4.11",
    "com.unity.cinemachine": "2.9.1",
    "com.unity.ide.rider": "2.0.7",
    "com.unity.inputsystem": "1.1.1",
    "com.unity.mathematics": "1.2.5",
    "com.unity.memoryprofiler": "0.4.2-preview.1",
    "com.unity.render-pipelines.universal": "10.7.0",
    "com.unity.test-framework": "1.1.30",
    "com.unity.testframework.graphics": "7.8.17-exp.1",
    "com.unity.textmeshpro": "3.0.6",
    "com.unity.timeline": "1.4.8",
    "com.unity.ugui": "1.0.0",
    "net.peeweek.gameplay-ingredients": "2020.2.10",
    "com.unity.modules.ai": "1.0.0",
    "com.unity.modules.androidjni": "1.0.0",
    "com.unity.modules.animation": "1.0.0",
    "com.unity.modules.assetbundle": "1.0.0",
    "com.unity.modules.audio": "1.0.0",
    "com.unity.modules.cloth": "1.0.0",
    "com.unity.modules.director": "1.0.0",
    "com.unity.modules.imageconversion": "1.0.0",
    "com.unity.modules.imgui": "1.0.0",
    "com.unity.modules.jsonserialize": "1.0.0",
    "com.unity.modules.particlesystem": "1.0.0",
    "com.unity.modules.physics": "1.0.0",
    "com.unity.modules.physics2d": "1.0.0",
    "com.unity.modules.screencapture": "1.0.0",
    "com.unity.modules.terrain": "1.0.0",
    "com.unity.modules.terrainphysics": "1.0.0",
    "com.unity.modules.ui": "1.0.0",
    "com.unity.modules.uielements": "1.0.0",
    "com.unity.modules.umbra": "1.0.0",
    "com.unity.modules.unityanalytics": "1.0.0",
    "com.unity.modules.unitywebrequest": "1.0.0",
    "com.unity.modules.unitywebrequestassetbundle": "1.0.0",
    "com.unity.modules.unitywebrequestaudio": "1.0.0",
    "com.unity.modules.unitywebrequesttexture": "1.0.0",
    "com.unity.modules.unitywebrequestwww": "1.0.0",
    "com.unity.modules.video": "1.0.0",
    "com.unity.modules.vr": "1.0.0",
    "com.unity.modules.wind": "1.0.0",
    "com.unity.modules.xr": "1.0.0"
  },
  "testables": [
    "com.unity.testframework.graphics"
  ],
  "scopedRegistries": [
    {
      "name": "OpenUPM",
      "url": "https://package.openupm.com",
      "scopes": [
        "com.dbrizov",
        "net.peeweek"
      ]
    }
  ]
}
`

	klakPackages = `{
  "dependencies": {
    "com.unity.package-manager-ui": "2.0.3",
    "com.unity.modules.animation": "1.0.0",
    "com.unity.modules.particlesystem": "1.0.0",
    "com.unity.modules.physics": "1.0.0",
    "com.unity.modules.ui": "1.0.0"
  }
}
`

	unityBoatAttackSettings = `%YAML 1.1
%TAG !u! tag:unity3d.com,2011:
--- !u!129 &1
PlayerSettings:
  m_ObjectHideFlags: 0
  serializedVersion: 22
  # ... trimmed ...
  accelerometerFrequency: 0
  companyName: UnityTechnologies
  productName: BoatAttack
  defaultCursor: {fileID: 0}
  # ... trimmed ...
    16:9: 1
    Others: 1
  bundleVersion: 0.9
  preloadedAssets:
  # ... trimmed ...
    Standalone: com.UnityTechnologies.BoatAttack
    iPhone: com.Unity3D.BoatAttack
  buildNumber:
    Standalone: 0
    iPhone: 0
    tvOS: 0
  overrideDefaultApplicationIdentifier: 0
  AndroidBundleVersionCode: 1
  AndroidMinSdkVersion: 21
  AndroidTargetSdkVersion: 0
`

	godotDodgeTheCreeps = `; Engine configuration file.
; It's best edited using the editor UI and not directly,
; since the parameters that go here are not all obvious.
;
; Format:
;   [section] ; section goes between []
;   param=value ; assign values to parameters

config_version=5

[application]

config/name="Dodge the Creeps"
config/description="This is a simple game where your character must move
and avoid the enemies for as long as possible.

This is a finished version of the game featured in the 'Your first 2D game'
tutorial in the documentation. For more details, consider
following the tutorial in the documentation."
config/tags=PackedStringArray("2d", "demo", "official")
run/main_scene="res://main.tscn"
config/features=PackedStringArray("4.7")
config/icon="res://icon.webp"

[display]

window/size/viewport_width=480
window/size/viewport_height=720
`

	gutPluginCfg = `[plugin]

name="Gut"
description="Unit Testing tool for Godot."
author="Butch Wesley"
version="9.6.1"
script="gut_plugin.gd"
`

	hterrainPluginCfg = `[plugin]

name="Heightmap Terrain"
description="Heightmap-based terrain"
author="Marc Gilleron"
version="1.8.1 dev"
script="tools/plugin.gd"
`

	vrExpansionUPlugin = `{
  "FileVersion": 3,
  "Version": 5.8,
  "VersionName": "5.8",
  "FriendlyName": "VRExpansionPlugin",
  "Description": "Adds several new VR features & components to UE4",
  "Category": "VRExpansion",
  "CreatedBy": "Joshua (MordenTral) Statzer",
  "CreatedByURL": "http://www.vreue4.com",
  "DocsURL": "http://www.vreue4.com",
  "MarketplaceURL": "",
  "SupportURL": "",
  "EnabledByDefault": true,
  "CanContainContent": false,
  "IsBetaVersion": false,
  "Installed": true,
  "SupportedTargetPlatforms": [
		"Win64",
		"Linux",
		"Android",
		"Mac",
		"IOS"
	],
  "Modules": [
    {
      "Name": "VRExpansionPlugin",
      "Type": "RunTime",
      "LoadingPhase": "Default"
    },
	{
      "Name": "VRExpansionEditor",
      "Type": "UnCookedOnly",
	  "LoadingPhase": "PostEngineInit"
    }
  ],
  "Plugins": [
    {
      "Name": "ChaosVehiclesPlugin",
      "Enabled": true
    },
    {
      "Name": "XRBase",
      "Enabled": true
    },
    {
      "Name": "Mover",
      "Enabled": true
    }
  ]
}`

	socketIOUPlugin = `{
	"FileVersion": 3,
	"Version": 1,
	"VersionName": "2.12.0",
	"EngineVersion": "5.8",
	"FriendlyName": "Socket.IO Client",
	"Description": "Real-time WebSocket networking via Socket.IO protocol usable from blueprints and c++.",
	"Category": "Networking",
	"CreatedBy": "Getnamo",
	"CreatedByURL": "http://getnamo.com",
	"DocsURL": "https://github.com/getnamo/SocketIOClient-Unreal",
	"MarketplaceURL": "com.epicgames.launcher://ue/marketplace/slug/socket-io-client",
	"SupportURL": "https://github.com/getnamo/SocketIOClient-Unreal/issues",
	"EnabledByDefault": true,
	"CanContainContent": false,
	"IsBetaVersion": false,
	"Installed": false,
	"Modules": [
		{
			"Name": "SocketIOClient",
			"Type": "Runtime",
			"LoadingPhase": "PreDefault",
			"WhitelistPlatforms": [
				"Win64",
				"Linux",
				"Mac",
				"Android",
				"IOS"
			]
		},
		{
			"Name": "SIOJson",
			"Type": "Runtime",
			"LoadingPhase": "PreDefault",
			"WhitelistPlatforms": [
				"Win64",
				"Linux",
				"Mac",
				"Android",
				"IOS"
			]
		},
		{
			"Name": "SocketIOLib",
			"Type": "Runtime",
			"LoadingPhase": "PreDefault",
			"WhitelistPlatforms": [
				"Win64",
				"Linux",
				"Mac",
				"Android",
				"IOS"
			]
		},
		{
			"Name": "CoreUtility",
			"Type": "Runtime",
			"LoadingPhase": "PreDefault",
			"WhitelistPlatforms": [
				"Win64",
				"Linux",
				"Mac",
				"Android",
				"IOS"
			]
		},
		{
			"Name": "SIOJEditorPlugin",
			"Type": "Editor",
			"LoadingPhase": "Default",
			"WhitelistPlatforms": [
				"Win64",
				"Linux",
				"Mac"
			]
		}
	]
}
`

	defoldInputGameProject = `[project]
title = Defold-Input
version = 0.1
dependencies#0 = https://github.com/britzl/deftest/archive/2.6.0.zip
dependencies#1 = https://github.com/britzl/defold-orthographic/archive/2.10.0.zip

[bootstrap]
main_collection = /examples/examples.collectionc
render = /orthographic/render/orthographic.renderc

[display]
width = 640
height = 1136
dynamic_orientation = 1

[physics]
scale = 0.02
debug = 0
gravity_y = 0
max_collision_object_count = 512

[script]
shared_state = 1

[library]
include_dirs = in

[graphics]
max_debug_vertices = 50000

[collection_proxy]
max_count = 16

[sprite]
max_count = 2000

[android]
package = com.britzl.defold.input
input_method = HiddenInputField

[osx]
bundle_identifier = com.britzl.defold.input

[ios]
bundle_identifier = com.britzl.defold.input

[html5]
show_fullscreen_button = 0
show_made_with_defold = 0
cssfile = /builtins/manifests/web/dark_theme.css

[input]
game_binding = /builtins/input/all.input_bindingc

`

	o3deAtomRPIGem = `{
    "gem_name": "Atom_RPI",
    "version": "0.0.1",
    "display_name": "Atom API",
    "license": "Apache-2.0 Or MIT",
    "license_url": "https://github.com/o3de/o3de/blob/development/LICENSE.txt",
    "origin": "Open 3D Engine - o3de.org",
    "origin_url": "https://github.com/o3de/o3de",
    "type": "Code",
    "summary": "RPI for Atom",
    "canonical_tags": [
        "Gem"
    ],
    "user_tags": [],
    "requirements": "",
    "documentation_url": "",
    "dependencies": [
        "Atom_RHI"
    ]
}
`

	o3deDefaultProjectJSON = `{
    "project_name": "${Name}",
    "version": "${Version}",
    "project_id": "${ProjectId}",
    "origin": "The primary repo for ${Name} goes here: i.e. http://www.mydomain.com",
    "license": "What license ${Name} uses goes here: i.e. https://opensource.org/licenses/Apache-2.0 Or https://opensource.org/licenses/MIT etc.",
    "display_name": "${Name}",
    "summary": "A short description of ${Name}.",
    "canonical_tags": [
        "Project"
    ],
    "user_tags": [
        "${Name}"
    ],
    "icon_path": "preview.png",
    "engine": "o3de",
    "external_subdirectories": [
        "Gem"
    ],
    "restricted": "${Name}",
    "gem_names": [
        "${Name}",
        "Atom",
        "AudioSystem",
        "CameraFramework",
        "DebugDraw",
        "DiffuseProbeGrid",
        "EditorPythonBindings",
        "EMotionFX",
        "GameState",
        "ImGui",
        "LandscapeCanvas",
        "LyShine",
        "MiniAudio",
        "PhysX5",
        "PrimitiveAssets",
        "PrefabBuilder",
        "SaveData",
        "ScriptCanvasPhysics",
        "ScriptEvents",
        "StartingPointInput",
        "TextureAtlas",
        "WhiteBox",
        "RemoteTools"
    ]
}
`
)

func TestRealKlakUnityPackages(t *testing.T) {
	m := parse(t, parseUnityPackages, "Packages/manifest.json", klakPackages)
	if m.Ecosystem != EcosystemUnity {
		t.Errorf("ecosystem = %q, want unity", m.Ecosystem)
	}
	// The manifest names no package and declares no version. It says what the
	// project consumes, not what it is.
	if m.Name != "" || m.Version != "" {
		t.Errorf("a package manifest declares no identity, got %q %q", m.Name, m.Version)
	}
	want := []DeclaredDep{
		{Name: "com.unity.modules.animation", Range: "1.0.0"},
		{Name: "com.unity.modules.particlesystem", Range: "1.0.0"},
		{Name: "com.unity.modules.physics", Range: "1.0.0"},
		{Name: "com.unity.modules.ui", Range: "1.0.0"},
		{Name: "com.unity.package-manager-ui", Range: "2.0.3"},
	}
	if !reflect.DeepEqual(m.Deps, want) {
		t.Errorf("deps =\n%+v\nwant\n%+v", m.Deps, want)
	}
}

func TestRealBoatAttackUnityPackages(t *testing.T) {
	m := parse(t, parseUnityPackages, "Packages/manifest.json", unityBoatAttackPackages)
	byName := map[string]DeclaredDep{}
	for _, d := range m.Deps {
		byName[d.Name] = d
	}
	// A preview and an experimental version are ordinary version text, not
	// something to normalise away.
	for name, want := range map[string]string{
		"com.unity.render-pipelines.universal": "10.7.0",
		"com.unity.memoryprofiler":             "0.4.2-preview.1",
		"com.unity.testframework.graphics":     "7.8.17-exp.1",
		"net.peeweek.gameplay-ingredients":     "2020.2.10",
	} {
		if got := byName[name].Range; got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	// A real project's manifest carries scopedRegistries and testables beside
	// its dependencies. Neither is a dependency, and neither may leak in.
	for _, d := range m.Deps {
		if d.Name == "scopedRegistries" || d.Name == "testables" {
			t.Errorf("%q is not a dependency", d.Name)
		}
	}
	if len(m.Deps) < 20 {
		t.Errorf("a real Unity manifest declares more than %d dependencies", len(m.Deps))
	}
}

func TestRealBoatAttackUnityProjectSettings(t *testing.T) {
	// The file yaml.v3 refuses: %TAG !u! and a !u!129 class tag on the
	// document. Reading it by line is the whole point.
	m := parse(t, parseUnityProjectSettings, "ProjectSettings/ProjectSettings.asset", unityBoatAttackSettings)
	if m.Ecosystem != EcosystemUnity {
		t.Errorf("ecosystem = %q, want unity", m.Ecosystem)
	}
	if m.Name != "BoatAttack" {
		t.Errorf("name = %q, want BoatAttack", m.Name)
	}
	if m.Version != "0.9" {
		t.Errorf("version = %q, want 0.9", m.Version)
	}
	if m.BuildNumber != "1" {
		t.Errorf("build number = %q, want the AndroidBundleVersionCode 1", m.BuildNumber)
	}
	if len(m.Deps) != 0 {
		t.Errorf("a settings file declares no dependencies, got %+v", m.Deps)
	}
}

func TestRealDodgeTheCreepsGodotProject(t *testing.T) {
	m := parse(t, parseGodotProject, "project.godot", godotDodgeTheCreeps)
	if m.Ecosystem != EcosystemGodot {
		t.Errorf("ecosystem = %q, want godot", m.Ecosystem)
	}
	if m.Name != "Dodge the Creeps" {
		t.Errorf("name = %q, want Dodge the Creeps", m.Name)
	}
	// The project sets no config/version, which is the normal state of a Godot
	// project versioned by its git tags. An empty version is the honest answer.
	if m.Version != "" {
		t.Errorf("version = %q, want empty: the project declares none", m.Version)
	}
}

func TestRealGodotPluginCfgs(t *testing.T) {
	for _, tc := range []struct{ name, src, wantName, wantVersion string }{
		{"GUT", gutPluginCfg, "Gut", "9.6.1"},
		// The addon's name is its display name, and its version carries a
		// word. Both are kept verbatim: what the file declares is what the
		// editor shows, and normalising either would make the two disagree.
		{"HTerrain", hterrainPluginCfg, "Heightmap Terrain", "1.8.1 dev"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := parse(t, parseGodotPlugin, "addons/x/plugin.cfg", tc.src)
			if m.Name != tc.wantName || m.Version != tc.wantVersion {
				t.Errorf("= %q %q, want %q %q", m.Name, m.Version, tc.wantName, tc.wantVersion)
			}
		})
	}
}

func TestRealVRExpansionUPlugin(t *testing.T) {
	m := parse(t, parseUPlugin, "VRExpansionPlugin/VRExpansionPlugin.uplugin", vrExpansionUPlugin)
	if m.Ecosystem != EcosystemUnreal {
		t.Errorf("ecosystem = %q, want unreal", m.Ecosystem)
	}
	// The name is the file's, which is what other descriptors reference it by.
	// FriendlyName in the file is "VRExpansionPlugin" too, but Modules and
	// Plugins entries name the file, so the file is what a workspace resolves.
	if m.Name != "VRExpansionPlugin" {
		t.Errorf("name = %q, want VRExpansionPlugin", m.Name)
	}
	if m.Version != "5.8" {
		t.Errorf("version = %q, want 5.8", m.Version)
	}
	// This one broke an assumption. Unreal's schema calls Version an integer
	// and this plugin writes 5.8. The counter is reported as the file spells
	// it rather than coerced, and SetBuild will refuse to write a non-integer
	// over it, which is the right way round.
	if m.BuildNumber != "5.8" {
		t.Errorf("build number = %q, want the float 5.8 verbatim", m.BuildNumber)
	}
	want := []DeclaredDep{
		{Name: "ChaosVehiclesPlugin"},
		{Name: "Mover"},
		{Name: "XRBase"},
	}
	if !reflect.DeepEqual(m.Deps, want) {
		t.Errorf("deps =\n%+v\nwant\n%+v", m.Deps, want)
	}
	// Modules are the plugin's own compilation units, not dependencies.
	for _, d := range m.Deps {
		if d.Name == "VRExpansionEditor" {
			t.Error("a module leaked in as a dependency")
		}
	}
}

func TestRealSocketIOUPlugin(t *testing.T) {
	m := parse(t, parseUPlugin, "SocketIOClient.uplugin", socketIOUPlugin)
	if m.Name != "SocketIOClient" {
		t.Errorf("name = %q, want SocketIOClient", m.Name)
	}
	if m.Version != "2.12.0" {
		t.Errorf("version = %q, want 2.12.0", m.Version)
	}
	if m.BuildNumber != "1" {
		t.Errorf("build number = %q, want 1", m.BuildNumber)
	}
	// EngineVersion pins what the plugin builds against. It is not a
	// dependency and must not be read as the plugin's own version.
	if m.Version == "5.8" {
		t.Error("EngineVersion was read as the plugin's version")
	}
}

func TestRealDefoldInputGameProject(t *testing.T) {
	m := parse(t, parseDefoldProject, "game.project", defoldInputGameProject)
	if m.Ecosystem != EcosystemDefold {
		t.Errorf("ecosystem = %q, want defold", m.Ecosystem)
	}
	if m.Name != "Defold-Input" {
		t.Errorf("name = %q, want Defold-Input", m.Name)
	}
	if m.Version != "0.1" {
		t.Errorf("version = %q, want 0.1", m.Version)
	}
	// The dependency keys are spelled dependencies#0 and dependencies#1, which
	// is why the Defold dialect has no comment token: a '#' rule would cut the
	// key in half. Their versions live inside archive URLs and are documented
	// as not read.
	if len(m.Deps) != 0 {
		t.Errorf("Defold archive URLs are not read as dependencies, got %+v", m.Deps)
	}
}

func TestRealO3DEManifests(t *testing.T) {
	gem := parse(t, parseO3DEGem, "Gems/Atom/RPI/gem.json", o3deAtomRPIGem)
	if gem.Ecosystem != EcosystemO3DE {
		t.Errorf("ecosystem = %q, want o3de", gem.Ecosystem)
	}
	if gem.Name != "Atom_RPI" || gem.Version != "0.0.1" {
		t.Errorf("gem = %q %q, want Atom_RPI 0.0.1", gem.Name, gem.Version)
	}
	// A bare gem name with no specifier is a real declaration with no range,
	// and the capitalisation is part of the name: folding it the way a Python
	// distribution name is folded would stop it matching the gem.
	want := []DeclaredDep{{Name: "Atom_RHI"}}
	if !reflect.DeepEqual(gem.Deps, want) {
		t.Errorf("deps =\n%+v\nwant\n%+v", gem.Deps, want)
	}

	proj := parse(t, parseO3DEProject, "project.json", o3deDefaultProjectJSON)
	if proj.Ecosystem != EcosystemO3DE {
		t.Errorf("ecosystem = %q, want o3de", proj.Ecosystem)
	}
	if proj.Name == "" {
		t.Error("the project declares a project_name and it must be read")
	}
}

func TestAForeignProjectJSONReadsAsNothing(t *testing.T) {
	// project.json is not a name unique to O3DE. A file of that name belonging
	// to something else parses to an empty manifest rather than a wrong one:
	// no name, no version, no dependencies and no drops.
	m := parse(t, parseO3DEProject, "project.json", `{"name": "webthing", "scripts": {"build": "tsc"}}`)
	if m.Name != "" || m.Version != "" || len(m.Deps) != 0 || len(m.Dropped) != 0 {
		t.Errorf("a foreign project.json must read as nothing, got %+v", m)
	}
}

// parse runs one parser and fails the test if the manifest does not parse.
func parse(t *testing.T, fn parseFunc, rel, src string) Manifest {
	t.Helper()
	m, err := fn(rel, []byte(src))
	if err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	return m
}
