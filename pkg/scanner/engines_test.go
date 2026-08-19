package scanner

import (
	"context"
	"reflect"
	"sort"
	"testing"
)

// The seams this file covers live between the walk, the path-qualified format
// resolution and the local-path resolver, so no per-file parser test can see
// them. What it builds is one engine monorepo of the shape a studio actually
// has: a Unity client consuming an embedded package, an Unreal server with its
// plugin beside it, a Godot tool with an addon, and the generated folders each
// engine leaves lying around.

// engineMonorepo writes the tree and returns its root.
func engineMonorepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, src := range map[string]string{
		// The Unity client. Its package manifest points at a package that
		// lives elsewhere in the repository, which is the workspace edge
		// everything downstream depends on.
		"apps/client/Packages/manifest.json": `{
  "dependencies": {
    "com.unity.textmeshpro": "3.0.6",
    "com.acme.core": "file:../../../packages/core"
  }
}
`,
		"apps/client/ProjectSettings/ProjectSettings.asset": `%YAML 1.1
%TAG !u! tag:unity3d.com,2011:
--- !u!129 &1
PlayerSettings:
  productName: Acme Client
  bundleVersion: 1.2.0
  buildNumber:
    Standalone: 3
    iPhone: 3
  AndroidBundleVersionCode: 3
`,
		// Unity's package cache. Every one of these is a third-party
		// package.json the npm reader would happily believe, which is why the
		// walk must not enter Library at all.
		"apps/client/Library/PackageCache/com.unity.textmeshpro@3.0.6/package.json": `{"name":"com.unity.textmeshpro","version":"3.0.6"}`,
		"apps/client/Library/PackageCache/com.unity.burst@1.4.11/package.json":      `{"name":"com.unity.burst","version":"1.4.11"}`,
		"apps/client/Temp/whatever/package.json":                                    `{"name":"temp-junk","version":"0.0.0"}`,

		// The embedded package the client points at.
		"packages/core/package.json": `{"name":"com.acme.core","version":"0.4.0"}`,

		// The Unreal server and the plugin it enables.
		"apps/server/Server.uproject": `{
	"FileVersion": 3,
	"EngineAssociation": "5.4",
	"Plugins": [
		{ "Name": "AcmeNet", "Enabled": true }
	]
}
`,
		"apps/server/Config/DefaultGame.ini": `[/Script/EngineSettings.GeneralProjectSettings]
ProjectName=Acme Server
ProjectVersion=1.2.0.0
`,
		"apps/server/Config/DefaultEngine.ini": `[/Script/AndroidRuntimeSettings.AndroidRuntimeSettings]
StoreVersion=3
VersionDisplayName=1.2.0
`,
		"apps/server/Plugins/AcmeNet/AcmeNet.uplugin": `{
	"FileVersion": 3,
	"Version": 4,
	"VersionName": "1.2.0",
	"Plugins": []
}
`,
		// Unreal's generated folders, holding descriptors of their own.
		"apps/server/Intermediate/Build/Copy.uproject": `{"Plugins":[{"Name":"Ghost"}]}`,
		"apps/server/Binaries/Win64/Stale.uplugin":     `{"VersionName":"0.0.1"}`,
		"apps/server/Saved/Config/Windows/Engine.ini":  "[x]\ny=1\n",

		// The Godot tool and its addon.
		"tools/editor/project.godot": `[application]

config/name="Acme Editor"
config/version="0.9.0"
config/features=PackedStringArray("4.3")
`,
		"tools/editor/addons/acme/plugin.cfg": `[plugin]

name="Acme Tools"
version="0.9.0"
script="plugin.gd"
`,
		// A web app manifest, which is what manifest.json means nearly
		// everywhere. It must not be read as Unity's.
		"apps/site/public/manifest.json": `{"name":"Acme","icons":[]}`,
	} {
		write(t, dir, name, src)
	}
	return dir
}

func TestScanReadsAnEngineMonorepo(t *testing.T) {
	dir := engineMonorepo(t)
	mans, err := New().Scan(context.Background(), dir)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}

	// The exact set, not a count. A skip rule that stopped firing would show
	// up here as an extra path rather than as a number nobody reads.
	var got []string
	for _, m := range mans {
		got = append(got, m.Path)
	}
	sort.Strings(got)
	want := []string{
		"apps/client/Packages/manifest.json",
		"apps/client/ProjectSettings/ProjectSettings.asset",
		"apps/server/Config/DefaultEngine.ini",
		"apps/server/Config/DefaultGame.ini",
		"apps/server/Plugins/AcmeNet/AcmeNet.uplugin",
		"apps/server/Server.uproject",
		"packages/core/package.json",
		"tools/editor/addons/acme/plugin.cfg",
		"tools/editor/project.godot",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("scanned\n%q\nwant\n%q", got, want)
	}
}

func TestScanStaysOutOfTheEngineOutputFolders(t *testing.T) {
	// Asserted on its own, and positively. Unity's Library/PackageCache holds
	// one package.json per resolved package, and a scan that entered it would
	// report a few hundred third-party packages as members of the workspace.
	dir := engineMonorepo(t)
	mans, err := New().Scan(context.Background(), dir)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	for _, m := range mans {
		for _, folder := range []string{"Library/", "PackageCache/", "Temp/", "Intermediate/", "Binaries/", "Saved/"} {
			if contains(m.Path, folder) {
				t.Errorf("%s was scanned: %s is generated output", m.Path, folder)
			}
		}
		if m.Name == "com.unity.textmeshpro" || m.Name == "temp-junk" || m.Name == "Ghost" {
			t.Errorf("%s came out of a generated folder", m.Name)
		}
	}
}

func TestScanGivesEachEngineManifestItsEcosystem(t *testing.T) {
	dir := engineMonorepo(t)
	mans, err := New().Scan(context.Background(), dir)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	want := map[string]Ecosystem{
		"apps/client/Packages/manifest.json":                EcosystemUnity,
		"apps/client/ProjectSettings/ProjectSettings.asset": EcosystemUnity,
		"apps/server/Server.uproject":                       EcosystemUnreal,
		"apps/server/Config/DefaultGame.ini":                EcosystemUnreal,
		"apps/server/Config/DefaultEngine.ini":              EcosystemUnreal,
		"apps/server/Plugins/AcmeNet/AcmeNet.uplugin":       EcosystemUnreal,
		"tools/editor/project.godot":                        EcosystemGodot,
		"tools/editor/addons/acme/plugin.cfg":               EcosystemGodot,
		"packages/core/package.json":                        EcosystemNpm,
	}
	for _, m := range mans {
		if w, ok := want[m.Path]; ok && m.Ecosystem != w {
			t.Errorf("%s: ecosystem = %q, want %q", m.Path, m.Ecosystem, w)
		}
	}
}

func TestAWebAppManifestIsNotUnitys(t *testing.T) {
	// The single most likely way this feature could go wrong in a repository
	// that has never seen a game engine.
	dir := engineMonorepo(t)
	mans, err := New().Scan(context.Background(), dir)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	for _, m := range mans {
		if m.Path == "apps/site/public/manifest.json" {
			t.Error("a web app manifest was read as a Unity package manifest")
		}
	}
}

func TestEngineManifestsResolveTheirWorkspaceEdges(t *testing.T) {
	// The seam a per-file test cannot reach. The Unity client declares
	// com.acme.core as a folder three levels up, and only a walk plus the
	// local-path resolver together can say which package that folder is.
	dir := engineMonorepo(t)
	mans, err := New().Scan(context.Background(), dir)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}

	var client, server Manifest
	for _, m := range mans {
		switch m.Path {
		case "apps/client/Packages/manifest.json":
			client = m
		case "apps/server/Server.uproject":
			server = m
		}
	}

	var core DeclaredDep
	for _, d := range client.Deps {
		if d.Name == "com.acme.core" {
			core = d
		}
	}
	if core.LocalPath != "../../../packages/core" {
		t.Fatalf("the Unity file: range must yield a local path, got %+v", core)
	}
	dirs := map[string]string{
		dir + "/packages/core": "core",
		dir + "/apps/client":   "client",
		dir + "/apps/server":   "server",
	}
	// ResolveLocalDir takes the manifest's path inside its package, which is
	// what a per-package scan reports; this walk scanned the whole repository,
	// so the package prefix comes off first.
	const clientRel = "Packages/manifest.json"
	if got := ResolveLocalDir(dirs, dir+"/apps/client", clientRel, core.LocalPath); got != "core" {
		t.Errorf("the Unity local path resolved to %q, want core", got)
	}

	// The Unreal server's plugin edge is versionless, and still an edge: the
	// plugin has to be released before the project that enables it.
	want := []DeclaredDep{{Name: "AcmeNet"}}
	if !reflect.DeepEqual(server.Deps, want) {
		t.Errorf("server deps = %+v, want %+v", server.Deps, want)
	}
	names, ambiguous := NameIndex([]Owner{
		{Package: "client", Manifests: []Manifest{client}},
		{Package: "server", Manifests: mansUnder(mans, "apps/server/")},
		{Package: "core", Manifests: mansUnder(mans, "packages/core/")},
	})
	if len(ambiguous) != 0 {
		t.Errorf("nothing here is ambiguous, got %q", ambiguous)
	}
	if names["AcmeNet"] != "server" {
		t.Errorf("AcmeNet maps to %q, want server", names["AcmeNet"])
	}
}

func TestRootIsHonestForThePathQualifiedFormats(t *testing.T) {
	// A manifest one folder down is not the folder's own identity file, and
	// says so. This matches project.pbxproj inside an .xcodeproj, which has
	// always reported Root false for the same reason.
	dir := engineMonorepo(t)
	mans, err := New().Scan(context.Background(), dir)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	want := map[string]bool{
		"apps/client/Packages/manifest.json":                false,
		"apps/client/ProjectSettings/ProjectSettings.asset": false,
		"apps/server/Config/DefaultGame.ini":                false,
		"apps/server/Server.uproject":                       false,
		"tools/editor/project.godot":                        false,
	}
	for _, m := range mans {
		if w, ok := want[m.Path]; ok && m.Root != w {
			t.Errorf("%s: Root = %v, want %v", m.Path, m.Root, w)
		}
	}
	// A manifest sitting directly in the scanned folder still reports true,
	// which is what the flag is for.
	root, err := New().Scan(context.Background(), dir+"/tools/editor")
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	for _, m := range root {
		if m.Path == "project.godot" && !m.Root {
			t.Error("a project.godot in the scanned folder is a root manifest")
		}
	}
}

func TestUnityReportsAPerPlatformCounterWhereThereIsNoAndroidOne(t *testing.T) {
	// A project shipping to iOS alone sets no AndroidBundleVersionCode. The
	// counter it does use is the one to report, or a build stamp would look
	// like it had nothing to move.
	m, err := parseUnityProjectSettings("ProjectSettings/ProjectSettings.asset", []byte(`PlayerSettings:
  bundleVersion: 2.0.0
  buildNumber:
    iPhone: 17
    tvOS: 17
`))
	if err != nil {
		t.Fatal(err)
	}
	if m.BuildNumber != "17" {
		t.Errorf("build number = %q, want the iPhone counter 17", m.BuildNumber)
	}
}

// mansUnder is the manifests whose path sits under a prefix.
func mansUnder(mans []Manifest, prefix string) []Manifest {
	var out []Manifest
	for _, m := range mans {
		if len(m.Path) > len(prefix) && m.Path[:len(prefix)] == prefix {
			out = append(out, m)
		}
	}
	return out
}

// contains reports a substring, spelled out to keep the assertion above
// readable at its call site.
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
