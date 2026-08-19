package writer

import (
	"errors"
	"strings"
	"testing"

	"github.com/yohimik/dispat/pkg/manifest"
)

// The engine cases the fetched fixtures do not reach: an export_presets.cfg
// (which projects keep out of version control, because a preset can name a
// signing keystore, so there is no public one to fetch), and the refusals and
// partial states every format has to survive.

const godotExportPresets = `[preset.0]

name="Android"
platform="Android"
runnable=true
export_filter="all_resources"

[preset.0.options]

custom_template/debug=""
version/code=7
version/name="1.2.0"
package/unique_name="com.acme.game"

[preset.1]

name="iOS"
platform="iOS"
runnable=true

[preset.1.options]

application/short_version="1.2.0"
application/version="1.2.0"
application/bundle_identifier="com.acme.game"

[preset.2]

name="Web"
platform="Web"

[preset.2.options]

html/export_icon=true
`

func TestGodotExportPresetsWriteEveryPresetsVersion(t *testing.T) {
	// Three presets, two of which carry a version, spelled differently by
	// platform. All of them move, or the project ships one store a stale
	// string and says nothing about it.
	res := roundTrip(t, "export_presets.cfg", godotExportPresets,
		func(p string) (Result, error) { return Rewrite(p, "1.3.0", nil) },
		`version/name="1.2.0"`, `version/name="1.3.0"`,
		`application/short_version="1.2.0"`, `application/short_version="1.3.0"`,
		`application/version="1.2.0"`, `application/version="1.3.0"`)
	if !res.VersionWritten {
		t.Error("the versions were not written")
	}
	if res.BuildWritten {
		t.Error("a version rewrite must not touch version/code")
	}
}

func TestGodotExportPresetsSetBuildWritesEveryCounter(t *testing.T) {
	res := roundTrip(t, "export_presets.cfg", godotExportPresets,
		func(p string) (Result, error) { return SetBuild(p, "8") },
		"version/code=7", "version/code=8")
	if !res.BuildWritten {
		t.Error("the counter was not written")
	}
	if res.VersionWritten {
		t.Error("a build write must not touch a marketing version")
	}
}

func TestGodotExportPresetsRefuseANonInteger(t *testing.T) {
	path := seed(t, "export_presets.cfg", godotExportPresets)
	_, err := SetBuild(path, "1.3.0")
	if err == nil {
		t.Fatal("Godot parses version/code as an integer and a version is not one")
	}
	if !strings.Contains(err.Error(), "version/code") {
		t.Errorf("the refusal must name the key, got %v", err)
	}
	if read(t, path) != godotExportPresets {
		t.Error("a refused write must leave the file alone")
	}
}

func TestGodotRefusesAVersionThatCouldNotSurvive(t *testing.T) {
	// A quote would close the literal, a bracket could read as a section
	// header on the next parse, and a semicolon would comment out the rest of
	// the line. None of them is written, and the file is untouched.
	src := "[application]\n\nconfig/version=\"1.0.0\"\n"
	for _, bad := range []string{`1.0"`, "1.0;x", "[1.0]", "1.0\n2.0"} {
		path := seed(t, "project.godot", src)
		if _, err := Rewrite(path, bad, nil); err == nil {
			t.Errorf("%q must be refused", bad)
		}
		if read(t, path) != src {
			t.Errorf("%q: a refused write modified the file", bad)
		}
	}
}

func TestUnityRefusesAVersionThatCouldNotSurvive(t *testing.T) {
	src := "PlayerSettings:\n  bundleVersion: 1.0.0\n"
	for _, bad := range []string{"1.0:0", "1.0 #x", "1.0\n2.0", " 1.0", "1.0 "} {
		path := seed(t, "ProjectSettings/ProjectSettings.asset", src)
		if _, err := Rewrite(path, bad, nil); err == nil {
			t.Errorf("%q must be refused", bad)
		}
		if read(t, path) != src {
			t.Errorf("%q: a refused write modified the file", bad)
		}
	}
}

func TestUnityLeavesAValueWithACommentBesideIt(t *testing.T) {
	// The comment survives, and the value before it moves. A splice that took
	// the comment for part of the value would swallow it.
	roundTrip(t, "ProjectSettings/ProjectSettings.asset",
		"PlayerSettings:\n  bundleVersion: 1.0.0 # shipped\n  productName: Level #1\n",
		func(p string) (Result, error) { return Rewrite(p, "2.0.0", nil) },
		"bundleVersion: 1.0.0 #", "bundleVersion: 2.0.0 #")
}

func TestUnityNeverCreatesACounterTheProjectDoesNotKeep(t *testing.T) {
	// A project that tracks no counter has decided so, and a CI stamp does not
	// overrule it.
	src := "PlayerSettings:\n  bundleVersion: 1.0.0\n"
	path := seed(t, "ProjectSettings/ProjectSettings.asset", src)
	res, err := SetBuild(path, "42")
	if err != nil {
		t.Fatal(err)
	}
	if res.BuildWritten {
		t.Error("a counter that is not declared must not be created")
	}
	if read(t, path) != src {
		t.Error("the file must be untouched")
	}
}

func TestUnityDoesNotMistakeADeeperMappingForACounter(t *testing.T) {
	// The aspect-ratio mapping's keys are spelled 16:9, and its values are the
	// integers a counter is made of. Only the mapping under buildNumber is a
	// counter, and only scope tracking can tell them apart.
	src := `PlayerSettings:
  m_SupportedAspectRatios:
    16:9: 1
    Others: 1
  buildNumber:
    iPhone: 3
  AndroidBundleVersionCode: 3
`
	roundTrip(t, "ProjectSettings/ProjectSettings.asset", src,
		func(p string) (Result, error) { return SetBuild(p, "4") },
		"    iPhone: 3", "    iPhone: 4",
		"AndroidBundleVersionCode: 3", "AndroidBundleVersionCode: 4")
}

func TestUProjectCanWriteNothingAndSaysSoHonestly(t *testing.T) {
	// A project descriptor declares no version and its plugins carry no
	// version text. The only honest answer is which edits named something the
	// file declares.
	src := `{"FileVersion":3,"EngineAssociation":"5.4","Plugins":[{"Name":"AcmeNet"}]}`
	path := seed(t, "Server.uproject", src)
	res, err := Rewrite(path, "2.0.0", []Edit{
		{Name: "AcmeNet", Range: "1.0.0"},
		{Name: "Ghost", Range: "1.0.0"},
		{Name: "AcmeNet", Kind: manifest.KindDevDependencies, Range: "1.0.0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.VersionWritten {
		t.Error("a .uproject declares no version to write")
	}
	if len(res.Skipped) != 1 || res.Skipped[0].Name != "AcmeNet" {
		t.Errorf("the declared plugin must be skipped, got %+v", res.Skipped)
	}
	// An unknown plugin is missing, and so is a declared one named under a
	// dependency kind Unreal has no field for.
	if len(res.Missing) != 2 {
		t.Errorf("want two missing edits, got %+v", res.Missing)
	}
	if read(t, path) != src {
		t.Error("nothing was writable, so nothing may have been written")
	}
}

func TestUPluginKeepsTheShapeAQuotedCounterWasWrittenIn(t *testing.T) {
	// The counter is a bare integer in every descriptor the engine writes, and
	// this test's sibling proves that. Where a file already spells it as a
	// string, the write keeps that shape rather than reformatting somebody
	// else's file on the way past.
	roundTrip(t, "AcmeNet.uplugin", `{"Version":"4","VersionName":"1.0.0"}`,
		func(p string) (Result, error) { return SetBuild(p, "5") },
		`"Version":"4"`, `"Version":"5"`)
}

func TestUPluginWithoutACounterIsLeftAlone(t *testing.T) {
	src := `{"VersionName":"1.0.0"}`
	path := seed(t, "AcmeNet.uplugin", src)
	res, err := SetBuild(path, "5")
	if err != nil {
		t.Fatal(err)
	}
	if res.BuildWritten {
		t.Error("a counter that is not declared must not be created")
	}
	if read(t, path) != src {
		t.Error("the file must be untouched")
	}
}

func TestUnrealConfigsSplitVersionFromCounter(t *testing.T) {
	game := "[/Script/EngineSettings.GeneralProjectSettings]\nProjectName=Acme\nProjectVersion=1.0.0.0\n"
	roundTrip(t, "Config/DefaultGame.ini", game,
		func(p string) (Result, error) { return Rewrite(p, "1.1.0.0", nil) },
		"ProjectVersion=1.0.0.0", "ProjectVersion=1.1.0.0")

	engine := "[/Script/AndroidRuntimeSettings.AndroidRuntimeSettings]\nStoreVersion=3\nVersionDisplayName=1.0.0\n"
	roundTrip(t, "Config/DefaultEngine.ini", engine,
		func(p string) (Result, error) { return Rewrite(p, "1.1.0", nil) },
		"VersionDisplayName=1.0.0", "VersionDisplayName=1.1.0")
	roundTrip(t, "Config/DefaultEngine.ini", engine,
		func(p string) (Result, error) { return SetBuild(p, "4") },
		"StoreVersion=3", "StoreVersion=4")
}

func TestUnrealArrayOperationsAreADifferentKey(t *testing.T) {
	// +Key= and .Key= are Unreal's array operations, and the engine resolves
	// them differently from a plain assignment. Rewriting one would change a
	// declaration the project did not mean.
	src := `[/Script/EngineSettings.GeneralProjectSettings]
ProjectVersion=1.0.0.0
+ProjectVersion=9.9.9.9
.ProjectVersion=8.8.8.8
`
	roundTrip(t, "Config/DefaultGame.ini", src,
		func(p string) (Result, error) { return Rewrite(p, "2.0.0.0", nil) },
		"\nProjectVersion=1.0.0.0", "\nProjectVersion=2.0.0.0")
}

func TestEngineFormatsWithoutABuildCounterSaySo(t *testing.T) {
	for _, name := range []string{"project.godot", "plugin.cfg", "game.project", "gem.json", "Server.uproject", "Packages/manifest.json"} {
		path := seed(t, name, "{}\n")
		_, err := SetBuild(path, "42")
		if !errors.Is(err, ErrNoBuildCounter) {
			t.Errorf("%s: want ErrNoBuildCounter, got %v", name, err)
		}
	}
}

func TestABareManifestJSONHasNoWriter(t *testing.T) {
	// Only the one inside a Packages folder is Unity's. Everywhere else the
	// name means a web app manifest, and writing a version into one would be
	// the worst thing this feature could do.
	if Supported("manifest.json") || Supported("public/manifest.json") {
		t.Error("a web app manifest must have no writer")
	}
	if !Supported("apps/client/Packages/manifest.json") {
		t.Error("a Unity package manifest must have a writer")
	}
	path := seed(t, "public/manifest.json", `{"name":"Acme"}`)
	if _, err := Rewrite(path, "2.0.0", nil); !errors.Is(err, ErrUnsupportedManifest) {
		t.Errorf("want ErrUnsupportedManifest, got %v", err)
	}
}

func TestEngineFormatsWithOneDependencyFieldReportOtherKindsMissing(t *testing.T) {
	// Unity and O3DE each declare exactly one dependency list. An edit naming
	// a dev or peer field names something the format cannot express, which is
	// the same answer composer gives a peer dependency.
	for _, tc := range []struct{ name, src, dep string }{
		{"Packages/manifest.json", `{"dependencies":{"com.acme.core":"1.0.0"}}`, "com.acme.core"},
		{"gem.json", `{"gem_name":"Acme","dependencies":["Atom_RHI==1.0.0"]}`, "Atom_RHI"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := seed(t, tc.name, tc.src)
			res, err := Rewrite(path, "", []Edit{
				{Name: tc.dep, Kind: manifest.KindDevDependencies, Range: "2.0.0"},
				{Name: tc.dep, Kind: manifest.KindPeerDependencies, Range: "2.0.0"},
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Missing) != 2 || len(res.Applied) != 0 {
				t.Errorf("both edits must be missing, got %+v", res)
			}
			if read(t, path) != tc.src {
				t.Error("nothing was writable, so nothing may have been written")
			}
		})
	}
}

func TestO3DEWritesABareGemNameIntoASpecifier(t *testing.T) {
	// A gem declared with no version at all gains one, spelled with the
	// operator the caller's range carries.
	roundTrip(t, "gem.json", `{"gem_name":"Acme","version":"1.0.0","dependencies":["Camera","Atom_RHI==1.0.0"]}`,
		func(p string) (Result, error) {
			return Rewrite(p, "", []Edit{{Name: "Camera", Range: "==2.0.0"}})
		},
		`"Camera"`, `"Camera==2.0.0"`)
}

func TestAForeignProjectJSONIsNeverWritten(t *testing.T) {
	// project.json is not a name unique to O3DE. Several other tools keep a
	// version under one, and rewriting theirs would be the worst thing this
	// format could do. A file naming neither a project nor a gem is left
	// alone, which is the same answer the reader gives it.
	for _, src := range []string{
		`{"name":"legacy-app","version":"1.0.0","frameworks":{"net451":{}}}`,
		`{"version":"1.0.0","dependencies":["Atom_RHI==1.0.0"]}`,
		`{}`,
	} {
		path := seed(t, "project.json", src)
		res, err := Rewrite(path, "9.9.9", []Edit{{Name: "Atom_RHI", Range: "==2.0.0"}})
		if err != nil {
			t.Fatal(err)
		}
		if res.VersionWritten || len(res.Applied) != 0 {
			t.Errorf("%s: a foreign project.json was written: %+v", src, res)
		}
		if read(t, path) != src {
			t.Errorf("%s: the file must be untouched, got %s", src, read(t, path))
		}
	}
	// A real one is written, so the guard has not simply turned the format off.
	real := `{"project_name":"Acme","version":"1.0.0"}`
	path := seed(t, "project.json", real)
	res, err := Rewrite(path, "2.0.0", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.VersionWritten {
		t.Error("a real O3DE project.json must still be written")
	}
}
