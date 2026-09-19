package publicapi_test

// Truncation is the shape a half-written or half-transferred manifest takes,
// and it is the cheapest way to reach every point at which a reader can fail
// mid-document. The property asserted here is the package's own: a write that
// cannot be completed leaves the file exactly as it was, and no prefix of a
// manifest makes a writer panic or produce an empty file.

import (
	"strings"
	"testing"

	"github.com/yohimik/dispat/pkg/writer"
)

// truncationCases are manifests whose readers walk a token stream, where a cut
// at a different byte fails at a different point.
var truncationCases = []formatFixture{
	{"npm", "package.json", `{
  "name": "acme",
  "version": "1.0.0",
  "keywords": ["a", "b"],
  "scripts": { "build": "tsc" },
  "dependencies": { "core": "^1.0.0", "other": "^1.0.0" },
  "devDependencies": { "vitest": "^2.0.0" }
}
`, "2.0.0", []writer.Edit{{Name: "core", Range: "^2.0.0"}}},
	{"composer", "composer.json", `{
  "name": "acme/app",
  "version": "1.0.0",
  "require": { "acme/core": "^1.0" }
}
`, "2.0.0", []writer.Edit{{Name: "acme/core", Range: "^2.0"}}},
	{"o3de gem", "gem.json", `{
    "gem_name": "AcmeGem",
    "tags": ["one"],
    "version": "1.0.0",
    "dependencies": ["Atom_RHI==1.0.0", "Camera"]
}
`, "2.0.0", []writer.Edit{{Name: "Atom_RHI", Range: "==2.0.0"}}},
	{"unreal plugin", "Acme.uplugin", `{
	"FileVersion": 3,
	"Version": 7,
	"VersionName": "1.0.0",
	"Modules": [ { "Name": "AcmeRuntime" } ],
	"Plugins": [ { "Name": "Networking", "Enabled": true } ]
}
`, "2.0.0", []writer.Edit{{Name: "Networking", Range: "1.0.0"}}},
	{"unreal project", "Acme.uproject", `{
	"FileVersion": 3,
	"EngineAssociation": "5.4",
	"Plugins": [ { "Name": "AcmeNet", "Enabled": true } ]
}
`, "", []writer.Edit{{Name: "AcmeNet", Range: "1.0.0"}}},
	{"unity packages", "Packages/manifest.json", `{
  "dependencies": {
    "com.acme.core": "1.0.0"
  }
}
`, "", []writer.Edit{{Name: "com.acme.core", Range: "2.0.0"}}},
	{"plist", "Info.plist", `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
	<key>LSRequiresIPhoneOS</key>
	<true/>
	<key>CFBundleShortVersionString</key>
	<string>1.0.0</string>
	<key>CFBundleVersion</key>
	<string>7</string>
</dict>
</plist>
`, "2.0.0", nil},
	{"maven", "pom.xml", `<project>
  <groupId>acme</groupId>
  <artifactId>app</artifactId>
  <version>1.0.0</version>
  <dependencies>
    <dependency>
      <groupId>acme</groupId>
      <artifactId>core</artifactId>
      <version>1.0.0</version>
      <scope>test</scope>
    </dependency>
  </dependencies>
</project>
`, "2.0.0", []writer.Edit{{Name: "acme:core", Range: "2.0.0"}}},
	{"msbuild", "Acme.csproj", `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <Version>1.0.0</Version>
  </PropertyGroup>
  <ItemGroup>
    <PackageReference Include="Core">
      <Version>1.0.0</Version>
    </PackageReference>
  </ItemGroup>
</Project>
`, "2.0.0", []writer.Edit{{Name: "Core", Range: "2.0.0"}}},
	{"nuspec", "Acme.nuspec", `<package>
  <metadata>
    <id>Acme</id>
    <version>1.0.0</version>
    <dependencies>
      <dependency id="Core" version="1.0.0" />
    </dependencies>
  </metadata>
</package>
`, "2.0.0", []writer.Edit{{Name: "Core", Range: "2.0.0"}}},
	{"android manifest", "AndroidManifest.xml", `<?xml version="1.0" encoding="utf-8"?>
<manifest xmlns:android="http://schemas.android.com/apk/res/android"
    package="com.acme.app"
    android:versionName="1.0.0"
    android:versionCode="7">
  <application android:label="Acme" />
</manifest>
`, "2.0.0", nil},
	{"aqua", "aqua.yaml", `registries:
  - type: standard
    ref: v4.200.0
packages:
  - name: cli/cli
    version: v2.55.0
`, "", []writer.Edit{{Name: "cli/cli", Range: "v2.60.0"}}},
	{"cargo", "Cargo.toml", `[package]
name = "acme"
version = "1.0.0"

[dependencies]
core = { version = "1.0", features = ["a"] }
`, "2.0.0", []writer.Edit{{Name: "core", Range: "1.1"}}},
	{"gradle catalog", "libs.versions.toml", `[versions]
core = "1.0.0"

[libraries]
core = { module = "acme:core", version.ref = "core" }
`, "", []writer.Edit{{Name: "acme:core", Range: "2.0.0"}}},
}

func TestPublicAPIWriterLeavesEveryTruncatedManifestIntact(t *testing.T) {
	for _, tc := range truncationCases {
		t.Run(tc.name, func(t *testing.T) {
			for cut := 1; cut < len(tc.body); cut++ {
				body := tc.body[:cut]
				path := writeFile(t, t.TempDir(), tc.rel, body)
				res, err := writer.Rewrite(path, tc.version, tc.edits)
				got := readFile(t, path)
				if err != nil {
					if got != body {
						t.Fatalf("cut %d: a refused rewrite changed the file:\n%s", cut, got)
					}
					continue
				}
				if got == "" && body != "" {
					t.Fatalf("cut %d: the rewrite emptied the file (result %+v)", cut, res)
				}
			}
		})
	}
}

func TestPublicAPIWriterLeavesEveryTruncatedLinkableManifestIntact(t *testing.T) {
	cases := []struct{ name, rel, body, dependency string }{
		{"npm", "package.json", `{
  "name": "acme",
  "packageManager": "pnpm@9.1.0",
  "pnpm": { "overrides": { "core": "file:../core", "other": "file:../other" } },
  "dependencies": { "core": "^1.0.0" }
}
`, "core"},
		{"npm resolutions", "package.json", `{
  "name": "acme",
  "resolutions": { "core": "file:../core" },
  "dependencies": { "core": "^1.0.0" }
}
`, "core"},
		{"cargo", "Cargo.toml", `[package]
name = "acme"
version = "1.0.0"

[dependencies]
core = "1.0"

[patch.crates-io]
core = { path = "../core" }
`, "core"},
		{"pyproject", "pyproject.toml", `[project]
name = "acme"
version = "1.0.0"

[tool.uv.sources]
core = { path = "../core" }
`, "core"},
		{"go module", "go.mod", `module example.com/app

go 1.26

require example.com/core v1.0.0

replace example.com/core => ../core
`, "example.com/core"},
		{"pubspec", "pubspec.yaml", `name: acme
version: 1.0.0

dependencies:
  core: ^1.0.0

dependency_overrides:
  core:
    path: ../core
`, "core"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for cut := 1; cut < len(tc.body); cut++ {
				body := tc.body[:cut]
				path := writeFile(t, t.TempDir(), tc.rel, body)

				if _, err := writer.Links(path); err != nil {
					if got := readFile(t, path); got != body {
						t.Fatalf("cut %d: a failed listing changed the file:\n%s", cut, got)
					}
				}
				if _, err := writer.Relink(path, []writer.Link{{Name: tc.dependency, Path: "../moved"}}); err != nil {
					if got := readFile(t, path); got != body {
						t.Fatalf("cut %d: a refused relink changed the file:\n%s", cut, got)
					}
					continue
				}
				if readFile(t, path) == "" && body != "" {
					t.Fatalf("cut %d: the relink emptied the file", cut)
				}
			}
		})
	}
}

func TestPublicAPIWriterLeavesEveryTruncatedCounterManifestIntact(t *testing.T) {
	cases := []struct{ rel, body string }{
		{"Info.plist", `<plist version="1.0">
<dict>
	<key>CFBundleShortVersionString</key>
	<string>1.0.0</string>
	<key>CFBundleVersion</key>
	<string>7</string>
</dict>
</plist>
`},
		{"AndroidManifest.xml", `<manifest xmlns:android="http://schemas.android.com/apk/res/android"
    android:versionName="1.0.0"
    android:versionCode="7">
</manifest>
`},
		{"Acme.uplugin", `{
	"Version": 7,
	"VersionName": "1.0.0",
	"Plugins": [ { "Name": "Networking" } ]
}
`},
		{"build.gradle", `android {
  defaultConfig {
    versionName '1.0.0'
    versionCode 7
  }
}
`},
		{"ProjectSettings/ProjectSettings.asset", `PlayerSettings:
  bundleVersion: 1.0.0
  AndroidBundleVersionCode: 7
`},
		{"export_presets.cfg", `[preset.0]

name="Android"

[preset.0.options]

version/code=7
version/name="1.0.0"
`},
	}
	for _, tc := range cases {
		t.Run(tc.rel, func(t *testing.T) {
			for cut := 1; cut < len(tc.body); cut++ {
				body := tc.body[:cut]
				path := writeFile(t, t.TempDir(), tc.rel, body)
				if _, err := writer.SetBuild(path, "42"); err != nil {
					if got := readFile(t, path); got != body {
						t.Fatalf("cut %d: a refused counter write changed the file:\n%s", cut, got)
					}
					continue
				}
				if readFile(t, path) == "" && body != "" {
					t.Fatalf("cut %d: the counter write emptied the file", cut)
				}
			}
		})
	}
}

func TestPublicAPIWriterSurvivesAttributeSpelling(t *testing.T) {
	cases := []struct {
		name, body string
		written    bool
	}{
		{"spaced around the equals", `<manifest xmlns:android="http://schemas.android.com/apk/res/android" android:versionName = "1.0.0" />`, true},
		{"single quoted", `<manifest xmlns:android="http://schemas.android.com/apk/res/android" android:versionName='1.0.0' />`, true},
		{"no namespace prefix", `<manifest versionName="1.0.0" />`, true},
		{"a longer attribute name first", `<manifest xmlns:android="http://schemas.android.com/apk/res/android" android:myversionName="x" android:versionName="1.0.0" />`, true},
		{"the name inside another value", `<manifest xmlns:android="http://schemas.android.com/apk/res/android" android:label="versionName" android:versionName="1.0.0" />`, true},
		{"no such attribute", `<manifest package="com.acme.app" />`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, out, _ := rewriteFixture(t, "AndroidManifest.xml", tc.body, "2.0.0", nil)
			if res.VersionWritten != tc.written {
				t.Fatalf("version written = %v; want %v\n%s", res.VersionWritten, tc.written, out)
			}
			if tc.written && !strings.Contains(out, "2.0.0") {
				t.Fatalf("rewritten manifest:\n%s", out)
			}
			if !tc.written && out != tc.body {
				t.Fatalf("an untouched manifest changed:\n%s", out)
			}
		})
	}

	t.Run("a document whose root is not a manifest", func(t *testing.T) {
		res, out, _ := rewriteFixture(t, "AndroidManifest.xml",
			`<?xml version="1.0"?><resources><string name="app_name">Acme</string></resources>`, "2.0.0", nil)
		if res.VersionWritten || !strings.Contains(out, "Acme") {
			t.Fatalf("a foreign document was written into:\n%s", out)
		}
	})
}
