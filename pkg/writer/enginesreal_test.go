package writer

// The same engine manifests the scanner tests read, fetched from public
// repositories on 2026-08-19 and kept here because the two packages are
// separate modules. Round-tripping a real file is where a splice that looked
// right on a hand-written fixture turns out to be wrong.
//
//	BoatAttack        github.com/Unity-Technologies/BoatAttack, master
//	Klak              github.com/keijiro/Klak, master
//	Dodge the Creeps  github.com/godotengine/godot-demo-projects, master
//	GUT               github.com/bitwes/Gut, master
//	VRExpansion       github.com/mordentral/VRExpansionPlugin, master
//	SocketIOClient    github.com/getnamo/SocketIOClient-Unreal, master
//	Defold-Input      github.com/britzl/defold-input, master
//	Atom_RPI          github.com/o3de/o3de, development

import (
	"strings"
	"testing"
)

const (
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
)

// roundTrip writes a real manifest to disk, rewrites it, and asserts the whole
// file against the source with exactly the intended substitutions applied. Any
// byte the writer touched that it was not asked to touch fails here, which a
// substring assertion would let through.
func roundTrip(t *testing.T, name, src string, run func(path string) (Result, error), swaps ...string) Result {
	t.Helper()
	path := seed(t, name, src)
	res, err := run(path)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	want := strings.NewReplacer(swaps...).Replace(src)
	if got := read(t, path); got != want {
		t.Errorf("%s: the rewrite changed bytes it was not asked to:\n got: %q\nwant: %q", name, got, want)
	}
	return res
}

func TestRealBoatAttackSettingsRoundTrip(t *testing.T) {
	res := roundTrip(t, "ProjectSettings/ProjectSettings.asset", unityBoatAttackSettings,
		func(p string) (Result, error) { return Rewrite(p, "1.4.0", nil) },
		"bundleVersion: 0.9", "bundleVersion: 1.4.0")
	if !res.VersionWritten {
		t.Error("the version was not written")
	}
	// The document header, the aspect-ratio mapping whose key holds a colon
	// (16:9) and every counter all survive untouched, which the whole-file
	// comparison above already proved. What is worth stating separately is
	// that a rewrite never moves a counter.
	if res.BuildWritten {
		t.Error("a version rewrite must not touch a build counter")
	}
}

func TestRealBoatAttackSettingsSetBuildMovesEveryPlatform(t *testing.T) {
	// The reason SetBuild writes every counter rather than the first: this
	// project ships from one settings file to Standalone, iPhone, tvOS and
	// Android, and stamping one of them leaves three stale.
	res := roundTrip(t, "ProjectSettings/ProjectSettings.asset", unityBoatAttackSettings,
		func(p string) (Result, error) { return SetBuild(p, "42") },
		"    Standalone: 0\n    iPhone: 0\n    tvOS: 0", "    Standalone: 42\n    iPhone: 42\n    tvOS: 42",
		"AndroidBundleVersionCode: 1", "AndroidBundleVersionCode: 42")
	if !res.BuildWritten {
		t.Error("the counters were not written")
	}
	if res.VersionWritten {
		t.Error("a build write must not touch the marketing version")
	}
}

func TestRealBoatAttackSettingsRefusesANonInteger(t *testing.T) {
	path := seed(t, "ProjectSettings/ProjectSettings.asset", unityBoatAttackSettings)
	if _, err := SetBuild(path, "1.2.3"); err == nil {
		t.Error("Unity parses its counters as integers and a version is not one")
	}
	if read(t, path) != unityBoatAttackSettings {
		t.Error("a refused write must leave the file alone")
	}
}

func TestRealDodgeTheCreepsRoundTrip(t *testing.T) {
	// The project declares no config/version, so there is nothing to write and
	// the file must come back byte for byte. This is the case that catches a
	// writer which creates the key it did not find.
	res := roundTrip(t, "project.godot", godotDodgeTheCreeps,
		func(p string) (Result, error) { return Rewrite(p, "2.0.0", nil) })
	if res.VersionWritten {
		t.Error("a key the project never declared must not be created")
	}
}

func TestRealDodgeTheCreepsWithAVersionRoundTrips(t *testing.T) {
	// The same file with the key present. config/features holds a
	// PackedStringArray call and config/description spans several lines; both
	// have to survive a splice two keys away from them.
	src := strings.Replace(godotDodgeTheCreeps,
		`config/icon="res://icon.webp"`,
		"config/icon=\"res://icon.webp\"\nconfig/version=\"1.0.0\"", 1)
	res := roundTrip(t, "project.godot", src,
		func(p string) (Result, error) { return Rewrite(p, "2.0.0", nil) },
		`config/version="1.0.0"`, `config/version="2.0.0"`)
	if !res.VersionWritten {
		t.Error("the version was not written")
	}
}

func TestRealGutPluginRoundTrip(t *testing.T) {
	res := roundTrip(t, "addons/gut/plugin.cfg", gutPluginCfg,
		func(p string) (Result, error) { return Rewrite(p, "10.0.0", nil) },
		`version="9.6.1"`, `version="10.0.0"`)
	if !res.VersionWritten {
		t.Error("the version was not written")
	}
}

func TestRealVRExpansionUPluginRoundTrip(t *testing.T) {
	// A real descriptor: tabs and spaces mixed inside one file, a float where
	// the schema says integer, and three versionless plugin declarations.
	res := roundTrip(t, "VRExpansionPlugin.uplugin", vrExpansionUPlugin,
		func(p string) (Result, error) {
			return Rewrite(p, "6.0.0", []Edit{
				{Name: "XRBase", Range: "1.0.0"},
				{Name: "NotThere", Range: "1.0.0"},
			})
		},
		`"VersionName": "5.8"`, `"VersionName": "6.0.0"`)
	if !res.VersionWritten {
		t.Error("VersionName was not written")
	}
	// A plugin the descriptor lists is declared but carries no version text,
	// so it is skipped. One it does not list is missing. The counter beside
	// the version is untouched, which the whole-file comparison proved.
	if len(res.Skipped) != 1 || res.Skipped[0].Name != "XRBase" {
		t.Errorf("a declared plugin must be skipped, got %+v", res.Skipped)
	}
	if len(res.Missing) != 1 || res.Missing[0].Name != "NotThere" {
		t.Errorf("an undeclared plugin must be missing, got %+v", res.Missing)
	}
	if len(res.Applied) != 0 {
		t.Errorf("no Unreal plugin declaration can be written, got %+v", res.Applied)
	}
}

func TestRealVRExpansionUPluginRefusesAFloatCounter(t *testing.T) {
	// The file already holds 5.8 where the schema says integer. SetBuild will
	// not add to the mess: it writes an integer or nothing.
	path := seed(t, "VRExpansionPlugin.uplugin", vrExpansionUPlugin)
	if _, err := SetBuild(path, "5.9"); err == nil {
		t.Error("a non-integer counter must be refused")
	}
	if read(t, path) != vrExpansionUPlugin {
		t.Error("a refused write must leave the file alone")
	}
}

func TestRealSocketIOUPluginSetBuildStaysUnquoted(t *testing.T) {
	// Version is a bare integer in every descriptor the engine writes, and
	// quoting it produces a file the build tool refuses. This is the assertion
	// that catches a writer reaching for quote() out of habit.
	res := roundTrip(t, "SocketIOClient.uplugin", socketIOUPlugin,
		func(p string) (Result, error) { return SetBuild(p, "42") },
		"\"Version\": 1,", "\"Version\": 42,")
	if !res.BuildWritten {
		t.Error("the counter was not written")
	}
	if res.VersionWritten {
		t.Error("a build write must not touch VersionName")
	}
}

func TestRealDefoldInputRoundTrip(t *testing.T) {
	// The dependency keys are spelled dependencies#0 and dependencies#1. A
	// dialect treating '#' as a comment would cut them in half, and the
	// whole-file comparison is what proves it does not.
	res := roundTrip(t, "game.project", defoldInputGameProject,
		func(p string) (Result, error) { return Rewrite(p, "0.2", nil) },
		"version = 0.1", "version = 0.2")
	if !res.VersionWritten {
		t.Error("the version was not written")
	}
}

func TestRealAtomRPIGemRoundTrip(t *testing.T) {
	res := roundTrip(t, "gem.json", o3deAtomRPIGem,
		func(p string) (Result, error) {
			return Rewrite(p, "0.1.0", []Edit{{Name: "Atom_RHI", Range: "==1.0.0"}})
		},
		`"version": "0.0.1"`, `"version": "0.1.0"`,
		`"Atom_RHI"`, `"Atom_RHI==1.0.0"`)
	if !res.VersionWritten || len(res.Applied) != 1 {
		t.Errorf("the version and the gem specifier must both be written, got %+v", res)
	}
}

func TestRealKlakPackagesRoundTrip(t *testing.T) {
	res := roundTrip(t, "Packages/manifest.json", klakPackages,
		func(p string) (Result, error) {
			return Rewrite(p, "", []Edit{{Name: "com.unity.modules.ui", Range: "2.0.0"}})
		},
		`"com.unity.modules.ui": "1.0.0"`, `"com.unity.modules.ui": "2.0.0"`)
	if len(res.Applied) != 1 {
		t.Errorf("the dependency was not written, got %+v", res)
	}
	if res.VersionWritten {
		t.Error("a Unity package manifest declares no version of its own")
	}
}

func TestRealEngineManifestsConverge(t *testing.T) {
	// A second pass with the same input writes nothing. Anything else means a
	// release run would keep reporting a file as changed for ever.
	for _, tc := range []struct{ name, src, version string }{
		{"ProjectSettings/ProjectSettings.asset", unityBoatAttackSettings, "1.4.0"},
		{"addons/gut/plugin.cfg", gutPluginCfg, "10.0.0"},
		{"SocketIOClient.uplugin", socketIOUPlugin, "3.0.0"},
		{"game.project", defoldInputGameProject, "0.2"},
		{"gem.json", o3deAtomRPIGem, "0.1.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := seed(t, tc.name, tc.src)
			if _, err := Rewrite(path, tc.version, nil); err != nil {
				t.Fatal(err)
			}
			once := read(t, path)
			res, err := Rewrite(path, tc.version, nil)
			if err != nil {
				t.Fatal(err)
			}
			if res.VersionWritten {
				t.Error("a converged rewrite must report nothing written")
			}
			if got := read(t, path); got != once {
				t.Errorf("a second pass changed the file:\n got: %q\nwant: %q", got, once)
			}
		})
	}
}
