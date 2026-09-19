package publicapi_test

// The properties every format writer shares, checked across all of them at
// once: a rewrite that asks for what the file already says writes nothing, a
// rewrite with no version to write leaves the identity alone, and a manifest
// reached through a symbolic link is refused rather than followed.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yohimik/dispat/pkg/writer"
)

// formatFixture is one realistic manifest of a given format, with a version
// and a set of edits that all land.
type formatFixture struct {
	name, rel, body, version string
	edits                    []writer.Edit
}

// formatFixtures covers every format the writer table claims, so a property
// asserted here is asserted for all of them.
var formatFixtures = []formatFixture{
	{"npm", "package.json", `{
  "name": "acme",
  "version": "1.0.0",
  "dependencies": { "core": "^1.0.0" }
}
`, "2.0.0", []writer.Edit{{Name: "core", Range: "^2.0.0"}}},
	{"composer", "composer.json", `{
  "name": "acme/app",
  "version": "1.0.0",
  "require": { "acme/core": "^1.0" }
}
`, "2.0.0", []writer.Edit{{Name: "acme/core", Range: "^2.0"}}},
	{"go module", "go.mod", "module example.com/acme/app\n\ngo 1.26\n\nrequire example.com/acme/core v1.0.0\n",
		"", []writer.Edit{{Name: "example.com/acme/core", Range: "v1.1.0"}}},
	{"cargo", "Cargo.toml", "[package]\nname = \"acme\"\nversion = \"1.0.0\"\n\n[dependencies]\ncore = \"1.0\"\n",
		"2.0.0", []writer.Edit{{Name: "core", Range: "2.0"}}},
	{"pyproject", "pyproject.toml", "[project]\nname = \"acme\"\nversion = \"1.0.0\"\ndependencies = [\"core>=1.0\"]\n",
		"2.0.0", []writer.Edit{{Name: "core", Range: ">=2.0"}}},
	{"requirements", "requirements.txt", "# runtime\ncore>=1.0\n", "", []writer.Edit{{Name: "core", Range: ">=2.0"}}},
	{"maven", "pom.xml", `<project><groupId>acme</groupId><artifactId>app</artifactId><version>1.0.0</version><dependencies><dependency><groupId>acme</groupId><artifactId>core</artifactId><version>1.0.0</version></dependency></dependencies></project>`,
		"2.0.0", []writer.Edit{{Name: "acme:core", Range: "2.0.0"}}},
	{"msbuild", "Acme.csproj", `<Project><PropertyGroup><Version>1.0.0</Version></PropertyGroup><ItemGroup><PackageReference Include="Acme.Core" Version="1.0.0" /></ItemGroup></Project>`,
		"2.0.0", []writer.Edit{{Name: "Acme.Core", Range: "2.0.0"}}},
	{"nuspec", "Acme.nuspec", `<package><metadata><id>Acme</id><version>1.0.0</version><dependencies><dependency id="Acme.Core" version="1.0.0" /></dependencies></metadata></package>`,
		"2.0.0", []writer.Edit{{Name: "Acme.Core", Range: "2.0.0"}}},
	{"packages props", "Directory.Packages.props", `<Project><ItemGroup><PackageVersion Include="Acme.Core" Version="1.0.0" /></ItemGroup></Project>`,
		"", []writer.Edit{{Name: "Acme.Core", Range: "2.0.0"}}},
	{"packages config", "packages.config", `<packages><package id="Acme.Core" version="1.0.0" /></packages>`,
		"", []writer.Edit{{Name: "Acme.Core", Range: "2.0.0"}}},
	{"pubspec", "pubspec.yaml", "name: acme\nversion: 1.0.0\ndependencies:\n  core: ^1.0.0\n",
		"2.0.0", []writer.Edit{{Name: "core", Range: "^2.0.0"}}},
	{"plist", "Info.plist", `<plist><dict><key>CFBundleShortVersionString</key><string>1.0.0</string></dict></plist>`, "2.0.0", nil},
	{"android manifest", "AndroidManifest.xml",
		`<manifest xmlns:android="http://schemas.android.com/apk/res/android" android:versionName="1.0.0" />`, "2.0.0", nil},
	{"gradle catalog", "libs.versions.toml", "[versions]\ncore = \"1.0.0\"\n[libraries]\ncore = { module = \"acme:core\", version.ref = \"core\" }\n",
		"", []writer.Edit{{Name: "acme:core", Range: "2.0.0"}}},
	{"gradle build", "build.gradle", "android {\n  defaultConfig {\n    versionName '1.0.0'\n  }\n}\ndependencies {\n  implementation 'acme:core:1.0.0'\n}\n",
		"2.0.0", []writer.Edit{{Name: "acme:core", Range: "2.0.0"}}},
	{"xcode", "project.pbxproj", "{\n\tbuildSettings = {\n\t\tMARKETING_VERSION = 1.0.0;\n\t};\n}\n", "2.0.0", nil},
	{"podfile", "Podfile", "pod 'AcmeCore', '~> 1.0.0'\n", "", []writer.Edit{{Name: "AcmeCore", Range: "~> 2.0.0"}}},
	{"podspec", "Acme.podspec", "Pod::Spec.new do |s|\n  s.name = 'Acme'\n  s.version = '1.0.0'\n  s.dependency 'AcmeCore', '~> 1.0.0'\nend\n",
		"2.0.0", []writer.Edit{{Name: "AcmeCore", Range: "~> 2.0.0"}}},
	{"gemfile", "Gemfile", "source 'https://rubygems.org'\ngem 'acme-core', '~> 1.0.0'\n",
		"", []writer.Edit{{Name: "acme-core", Range: "~> 2.0.0"}}},
	{"gemspec", "acme.gemspec", "Gem::Specification.new do |s|\n  s.name = 'acme'\n  s.version = '1.0.0'\n  s.add_dependency 'acme-core', '~> 1.0.0'\nend\n",
		"2.0.0", []writer.Edit{{Name: "acme-core", Range: "~> 2.0.0"}}},
	{"dockerfile", "Dockerfile", "FROM ghcr.io/acme/base:1.0.0\n", "",
		[]writer.Edit{{Name: "ghcr.io/acme/base", Range: "2.0.0"}}},
	{"compose", "compose.yaml", "services:\n  app:\n    build: .\n    image: ghcr.io/acme/app:1.0.0\n  cache:\n    image: redis:7.2\n",
		"2.0.0", []writer.Edit{{Name: "redis", Range: "7.4"}}},
	{"aqua", "aqua.yaml", "packages:\n  - name: cli/cli\n    version: v2.55.0\n", "",
		[]writer.Edit{{Name: "cli/cli", Range: "v2.60.0"}}},
	{"unity packages", "Packages/manifest.json", `{"dependencies":{"com.acme.core":"1.0.0"}}`, "",
		[]writer.Edit{{Name: "com.acme.core", Range: "2.0.0"}}},
	{"unity settings", "ProjectSettings/ProjectSettings.asset", "PlayerSettings:\n  bundleVersion: 1.0.0\n", "2.0.0", nil},
	{"godot project", "project.godot", "[application]\nconfig/name=\"Acme\"\nconfig/version=\"1.0.0\"\n", "2.0.0", nil},
	{"godot plugin", "plugin.cfg", "[plugin]\nname=\"Acme\"\nversion=\"1.0.0\"\n", "2.0.0", nil},
	{"godot presets", "export_presets.cfg", "[preset.0]\nname=\"Android\"\n[preset.0.options]\nversion/name=\"1.0.0\"\n", "2.0.0", nil},
	{"unreal project", "Acme.uproject", `{"FileVersion":3,"Plugins":[{"Name":"AcmeNet"}]}`, "", nil},
	{"unreal plugin", "Acme.uplugin", `{"Version":7,"VersionName":"1.0.0"}`, "2.0.0", nil},
	{"unreal game config", "Config/DefaultGame.ini",
		"[/Script/EngineSettings.GeneralProjectSettings]\nProjectVersion=1.0.0\n", "2.0.0", nil},
	{"unreal engine config", "Config/DefaultEngine.ini",
		"[/Script/AndroidRuntimeSettings.AndroidRuntimeSettings]\nVersionDisplayName=1.0.0\n", "2.0.0", nil},
	{"defold", "game.project", "[project]\ntitle = Acme\nversion = 1.0.0\n", "2.0.0", nil},
	{"o3de project", "project.json", `{"project_name":"Acme","version":"1.0.0","dependencies":["Atom==1.0.0"]}`,
		"2.0.0", []writer.Edit{{Name: "Atom", Range: "==2.0.0"}}},
	{"o3de gem", "gem.json", `{"gem_name":"Acme","version":"1.0.0","dependencies":["Atom==1.0.0"]}`,
		"2.0.0", []writer.Edit{{Name: "Atom", Range: "==2.0.0"}}},
}

func TestPublicAPIWriterRewritingTheSameValuesTwiceWritesNothing(t *testing.T) {
	for _, tc := range formatFixtures {
		t.Run(tc.name, func(t *testing.T) {
			path := writeFile(t, t.TempDir(), tc.rel, tc.body)
			first, err := writer.Rewrite(path, tc.version, tc.edits)
			if err != nil {
				t.Fatalf("first rewrite: %v", err)
			}
			if len(first.Applied) != len(tc.edits) {
				t.Fatalf("first rewrite applied %d of %d edits: %+v", len(first.Applied), len(tc.edits), first)
			}
			after := readFile(t, path)
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}

			second, err := writer.Rewrite(path, tc.version, tc.edits)
			if err != nil {
				t.Fatalf("second rewrite: %v", err)
			}
			if len(second.Applied) != 0 || second.VersionWritten {
				t.Errorf("a repeated rewrite reported work: %+v", second)
			}
			if readFile(t, path) != after {
				t.Errorf("a repeated rewrite changed the file:\n%s", readFile(t, path))
			}
			now, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if !now.ModTime().Equal(info.ModTime()) {
				t.Error("a repeated rewrite touched the file")
			}
		})
	}
}

func TestPublicAPIWriterWithNothingToWriteLeavesTheFileAlone(t *testing.T) {
	for _, tc := range formatFixtures {
		t.Run(tc.name, func(t *testing.T) {
			path := writeFile(t, t.TempDir(), tc.rel, tc.body)
			res, err := writer.Rewrite(path, "", nil)
			if err != nil {
				t.Fatalf("empty rewrite: %v", err)
			}
			if res.VersionWritten || len(res.Applied) != 0 {
				t.Errorf("an empty rewrite reported work: %+v", res)
			}
			if got := readFile(t, path); got != tc.body {
				t.Errorf("an empty rewrite changed the file:\n%s", got)
			}
		})
	}
}

func TestPublicAPIWriterRefusesEveryFormatThroughASymbolicLink(t *testing.T) {
	for _, tc := range formatFixtures {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			target := writeFile(t, dir, "real/manifest-body", tc.body)
			link := filepath.Join(dir, filepath.FromSlash(tc.rel))
			if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			_, err := writer.Rewrite(link, tc.version, tc.edits)
			if err == nil || !strings.Contains(err.Error(), "symbolic link") {
				t.Fatalf("rewrite through a symlink = %v", err)
			}
			if got := readFile(t, target); got != tc.body {
				t.Fatalf("a symlink refusal changed the target:\n%s", got)
			}
		})
	}
}

func TestPublicAPIWriterRefusesBuildCountersThroughASymbolicLink(t *testing.T) {
	counters := []struct{ rel, body string }{
		{"Info.plist", `<plist><dict><key>CFBundleVersion</key><string>7</string></dict></plist>`},
		{"AndroidManifest.xml", `<manifest xmlns:android="http://schemas.android.com/apk/res/android" android:versionCode="7" />`},
		{"project.pbxproj", "{\n\tbuildSettings = {\n\t\tCURRENT_PROJECT_VERSION = 7;\n\t};\n}\n"},
		{"build.gradle", "android {\n  defaultConfig {\n    versionCode 7\n  }\n}\n"},
		{"pubspec.yaml", "name: acme\nversion: 1.0.0+7\n"},
		{"ProjectSettings/ProjectSettings.asset", "PlayerSettings:\n  AndroidBundleVersionCode: 7\n"},
		{"export_presets.cfg", "[preset.0]\n[preset.0.options]\nversion/code=7\n"},
		{"Acme.uplugin", `{"Version":7,"VersionName":"1.0.0"}`},
		{"Config/DefaultEngine.ini", "[/Script/AndroidRuntimeSettings.AndroidRuntimeSettings]\nStoreVersion=7\n"},
	}
	for _, tc := range counters {
		t.Run(tc.rel, func(t *testing.T) {
			dir := t.TempDir()
			target := writeFile(t, dir, "real/manifest-body", tc.body)
			link := filepath.Join(dir, filepath.FromSlash(tc.rel))
			if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			if _, err := writer.SetBuild(link, "42"); err == nil || !strings.Contains(err.Error(), "symbolic link") {
				t.Fatalf("set build through a symlink = %v", err)
			}
			if got := readFile(t, target); got != tc.body {
				t.Fatalf("a symlink refusal changed the target:\n%s", got)
			}
		})
	}
}

func TestPublicAPIWriterRefusesLinksThroughASymbolicLink(t *testing.T) {
	linkable := []struct{ rel, body, name string }{
		{"package.json", `{"name":"acme","dependencies":{"core":"^1.0.0"}}`, "core"},
		{"go.mod", "module example.com/app\n\ngo 1.26\n\nrequire example.com/core v1.0.0\n", "example.com/core"},
		{"Cargo.toml", "[package]\nname = \"acme\"\nversion = \"1.0.0\"\n\n[dependencies]\ncore = \"1.0\"\n", "core"},
		{"pyproject.toml", "[project]\nname = \"acme\"\nversion = \"1.0.0\"\ndependencies = [\"core\"]\n", "core"},
		{"pubspec.yaml", "name: acme\nversion: 1.0.0\ndependencies:\n  core: ^1.0.0\n", "core"},
	}
	for _, tc := range linkable {
		t.Run(tc.rel, func(t *testing.T) {
			dir := t.TempDir()
			target := writeFile(t, dir, "real/manifest-body", tc.body)
			link := filepath.Join(dir, tc.rel)
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			if _, err := writer.Relink(link, []writer.Link{{Name: tc.name, Path: "../core"}}); err == nil ||
				!strings.Contains(err.Error(), "symbolic link") {
				t.Errorf("relink through a symlink = %v", err)
			}
			if _, err := writer.Links(link); err == nil || !strings.Contains(err.Error(), "symbolic link") {
				t.Errorf("links through a symlink = %v", err)
			}
			if got := readFile(t, target); got != tc.body {
				t.Fatalf("a symlink refusal changed the target:\n%s", got)
			}
		})
	}
}

func TestPublicAPIWriterRefusesVersionsAFormatCannotHold(t *testing.T) {
	cases := []struct{ name, rel, body, version string }{
		{"godot project", "project.godot", "[application]\nconfig/version=\"1.0.0\"\n", `2.0.0" ; injected`},
		{"godot plugin", "plugin.cfg", "[plugin]\nversion=\"1.0.0\"\n", "2.0.0\nname=\"other\""},
		{"godot presets", "export_presets.cfg", "[preset.0]\n[preset.0.options]\nversion/name=\"1.0.0\"\n", `2.0.0"`},
		{"defold", "game.project", "[project]\nversion = 1.0.0\n", "2.0.0\n[other]"},
		{"unreal game config", "Config/DefaultGame.ini",
			"[/Script/EngineSettings.GeneralProjectSettings]\nProjectVersion=1.0.0\n", "2.0.0\nOther=1"},
		{"unreal engine config", "Config/DefaultEngine.ini",
			"[/Script/AndroidRuntimeSettings.AndroidRuntimeSettings]\nVersionDisplayName=1.0.0\n", "2.0.0 ; injected"},
		{"unity", "ProjectSettings/ProjectSettings.asset", "PlayerSettings:\n  bundleVersion: 1.0.0\n", "2.0.0: injected"},
		{"gradle", "build.gradle", "android {\n  defaultConfig {\n    versionName '1.0.0'\n  }\n}\n", "2.0.0' + evil() + '"},
		{"xcode", "project.pbxproj", "{\n\tbuildSettings = {\n\t\tMARKETING_VERSION = 1.0.0;\n\t};\n}\n", `2.0.0";`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeFile(t, t.TempDir(), tc.rel, tc.body)
			if _, err := writer.Rewrite(path, tc.version, nil); err == nil {
				t.Fatalf("%q was accepted", tc.version)
			}
			if got := readFile(t, path); got != tc.body {
				t.Fatalf("a refusal changed the file:\n%s", got)
			}
		})
	}
}
