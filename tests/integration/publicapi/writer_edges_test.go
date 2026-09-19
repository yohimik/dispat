package publicapi_test

// The last edges each writer has to hold: entries a document declares in a
// shape the writer cannot splice, statements that look like declarations and
// are not, and the nesting a real file reaches.

import (
	"strings"
	"testing"

	"github.com/yohimik/dispat/pkg/writer"
)

func TestPublicAPIWriterStepsOverUnreadablePluginEntries(t *testing.T) {
	t.Run("entries that are not plugin objects", func(t *testing.T) {
		res, out, _ := rewriteFixture(t, "Acme.uplugin", `{
	"FileVersion": 3,
	"Version": 7,
	"VersionName": "1.0.0",
	"Plugins": [
		"a bare string",
		["nested", ["deeper"], {"Name": "NotCounted"}],
		{ "Name": "Networking", "Descriptor": { "Category": { "Name": "Net" } } },
		{ "Name": "" },
		{ "Enabled": true }
	]
}
`, "2.0.0", []writer.Edit{
			{Name: "Networking", Range: "1.0.0"},
			{Name: "NotCounted", Range: "1.0.0"},
			{Name: "Net", Range: "1.0.0"},
		})
		if !res.VersionWritten || !strings.Contains(out, `"VersionName": "2.0.0"`) {
			t.Fatalf("rewritten uplugin:\n%s", out)
		}
		if len(res.Skipped) != 1 || res.Skipped[0].Name != "Networking" {
			t.Errorf("uplugin outcome = %s", rewriteOutcome(res))
		}
		if len(res.Missing) != 2 {
			t.Errorf("a nested name was read as a declared plugin: %s", rewriteOutcome(res))
		}
	})

	t.Run("a plugins field that is not an array", func(t *testing.T) {
		err := assertRefusal(t, "Acme.uplugin",
			`{"Version":7,"VersionName":"1.0.0","Plugins":{"Networking":true}}`, "2.0.0",
			[]writer.Edit{{Name: "Networking", Range: "1.0.0"}})
		if !strings.Contains(err.Error(), "is not an array") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("a uproject whose plugin list nests", func(t *testing.T) {
		res, _, _ := rewriteFixture(t, "Acme.uproject", `{
	"FileVersion": 3,
	"Plugins": [
		{ "Name": "AcmeNet", "PlatformAllowList": ["Win64", "Linux"] },
		[ "stray" ]
	]
}
`, "", []writer.Edit{{Name: "AcmeNet", Range: "1.0.0"}, {Name: "Win64", Range: "1.0.0"}})
		if len(res.Skipped) != 1 || len(res.Missing) != 1 {
			t.Errorf("uproject outcome = %s", rewriteOutcome(res))
		}
	})
}

func TestPublicAPIWriterStepsOverRubyStatementsThatAreNotDeclarations(t *testing.T) {
	res, out, _ := rewriteFixture(t, "Acme.podspec", `Pod::Spec.new do |s|
  s.name                = 'Acme'
  s.summary             = 'It\'s fine'
  s.static_framework
  s.version             == '9.9.9'
  s.deployment_target   => '15.0'
  s.dependency 'AcmeEarly', '~> 1.0.0'
  s.dependency 'AcmeSpaced' , '~> 1.0.0'
  s.dependency 'AcmeBare'   # no requirement at all
  s.dependency 'AcmeEsc\'aped', '~> 1.0.0'
  s.dependency 'AcmeUnterminated
  s.dependency ''
  s.dependency acme_variable
  s.dependency 'AcmeRange', '>= 1.0', '< 2.0'
  s.version             = '1.0.0'
  s.dependency 'AcmeLate', '~> 1.0.0'
end
`, "2.0.0", []writer.Edit{
		{Name: "AcmeEarly", Range: "~> 2.0.0"},
		{Name: "AcmeSpaced", Range: "~> 2.0.0"},
		{Name: "AcmeBare", Range: "~> 2.0.0"},
		{Name: `AcmeEsc\'aped`, Range: "~> 2.0.0"},
		{Name: "AcmeRange", Range: ">= 2.0"},
		{Name: "AcmeLate", Range: "~> 2.0.0"},
	})
	if !res.VersionWritten || !strings.Contains(out, "s.version             = '2.0.0'") {
		t.Fatalf("rewritten podspec:\n%s", out)
	}
	if !strings.Contains(out, "s.version             == '9.9.9'") {
		t.Error("a comparison was rewritten as an assignment")
	}
	if !strings.Contains(out, `s.summary             = 'It\'s fine'`) {
		t.Error("an escaped literal was disturbed")
	}
	if !strings.Contains(out, "s.dependency 'AcmeRange', '>= 1.0', '< 2.0'") {
		t.Error("a constraint spread across two literals was spliced")
	}
	if !strings.Contains(out, "s.dependency 'AcmeBare'   # no requirement at all") {
		t.Errorf("a pod with no requirement gained one:\n%s", out)
	}
	if len(res.Skipped) != 2 {
		t.Errorf("podspec outcome = %s", rewriteOutcome(res))
	}
	if len(res.Applied) != 4 {
		t.Errorf("podspec outcome = %s\n%s", rewriteOutcome(res), out)
	}

	t.Run("a podfile statement whose first token merely starts alike", func(t *testing.T) {
		res, out, _ := rewriteFixture(t, "Podfile", `platform :ios, '15.0'
pods_for_everything 'NotAPod', '~> 1.0.0'
podspec
pod 'AcmeCore', '~> 1.0.0'
`, "", []writer.Edit{
			{Name: "NotAPod", Range: "~> 2.0.0"},
			{Name: "AcmeCore", Range: "~> 2.0.0"},
		})
		if len(res.Applied) != 1 || len(res.Missing) != 1 {
			t.Errorf("podfile outcome = %s", rewriteOutcome(res))
		}
		if !strings.Contains(out, "pods_for_everything 'NotAPod', '~> 1.0.0'") {
			t.Errorf("a longer identifier was read as a pod:\n%s", out)
		}
	})
}

func TestPublicAPIWriterHandlesNestedAndCompactOverrideMaps(t *testing.T) {
	t.Run("an override map beside other pnpm settings", func(t *testing.T) {
		path := writeFile(t, t.TempDir(), "package.json", `{
  "name": "acme",
  "pnpm": {
    "overrides": { "core": "file:../core" },
    "peerDependencyRules": { "ignoreMissing": ["react"] }
  },
  "dependencies": { "core": "^1.0.0" }
}
`)
		res, err := writer.DropLinks(path)
		if err != nil || len(res.Applied) != 1 {
			t.Fatalf("drop = %+v, err = %v", res, err)
		}
		out := readFile(t, path)
		if strings.Contains(out, "overrides") {
			t.Errorf("an emptied override map was left behind:\n%s", out)
		}
		if !strings.Contains(out, "peerDependencyRules") {
			t.Errorf("a sibling setting was removed with it:\n%s", out)
		}
	})

	t.Run("a compact file with an empty override map", func(t *testing.T) {
		path := writeFile(t, t.TempDir(), "package.json", `{"name":"acme","overrides":{},"dependencies":{"core":"^1.0.0"}}`)
		res, err := writer.Relink(path, []writer.Link{{Name: "core", Path: "../core"}})
		if err != nil || len(res.Applied) != 1 {
			t.Fatalf("relink = %+v, err = %v", res, err)
		}
		links, err := writer.Links(path)
		if err != nil || renderLinks(links) != "core=../core" {
			t.Fatalf("links = %+v, err = %v", links, err)
		}
	})

	t.Run("an override map holding a nested object", func(t *testing.T) {
		path := writeFile(t, t.TempDir(), "package.json", `{
  "name": "acme",
  "overrides": {
    "nested": { "inner": { "deep": "file:../deep" } },
    "core": "file:../old"
  }
}
`)
		res, err := writer.Relink(path, []writer.Link{{Name: "core", Path: "../core"}})
		if err != nil || len(res.Applied) != 1 {
			t.Fatalf("relink = %+v, err = %v", res, err)
		}
		out := readFile(t, path)
		if !strings.Contains(out, `"deep": "file:../deep"`) {
			t.Errorf("a nested override was disturbed:\n%s", out)
		}
		links, err := writer.Links(path)
		if err != nil || renderLinks(links) != "core=../core" {
			t.Fatalf("links = %+v, err = %v", links, err)
		}
	})
}

func TestPublicAPIWriterHandlesPubspecShapesItCannotSplice(t *testing.T) {
	res, out, _ := rewriteFixture(t, "pubspec.yaml", `name: "acme \" app"
version:

dependencies:
  unterminated: "^1.0.0
  bare:
  spaced:
  core: ^1.0.0

dev_dependencies:
  test_dep: ^1.0.0
`, "2.0.0", []writer.Edit{
		{Name: "unterminated", Range: "^2.0.0"},
		{Name: "bare", Range: "^2.0.0"},
		{Name: "spaced", Range: "^2.0.0"},
		{Name: "core", Range: "^2.0.0"},
	})
	if res.VersionWritten {
		t.Errorf("a version with nothing after it was written into:\n%s", out)
	}
	if !strings.Contains(out, "core: ^2.0.0") {
		t.Errorf("the writable constraint was not written:\n%s", out)
	}
	if !strings.Contains(out, `name: "acme \" app"`) {
		t.Error("an escaped literal was disturbed")
	}
	if len(res.Skipped) != 3 {
		t.Errorf("pubspec outcome = %s\n%s", rewriteOutcome(res), out)
	}
}

func TestPublicAPIWriterHandlesTOMLValuesItCannotSplice(t *testing.T) {
	res, out, _ := rewriteFixture(t, "Cargo.toml", `[package]
name = "acme"
version = "1.0.0"

[dependencies]
escaped = "1.0A"
spread = { version = "1.0", features = [
    "a",
] }

[dependencies.subtable]
version = "1.0"
features = ["a"]

[dependencies.multiline]
version = """
1.0
"""

[dependencies.numbered]
version = "1.0"
optional = true
`, "2.0.0", []writer.Edit{
		{Name: "escaped", Range: "1.1"},
		{Name: "spread", Range: "1.1"},
		{Name: "subtable", Range: "1.1"},
		{Name: "multiline", Range: "1.1"},
		{Name: "numbered", Range: "1.1"},
	})
	if !res.VersionWritten {
		t.Error("the crate version was not written")
	}
	if !strings.Contains(out, "1.0\\u0041") && !strings.Contains(out, `escaped = "1.1"`) {
		t.Errorf("an escaped literal was mishandled:\n%s", out)
	}
	if !strings.Contains(out, `"""`) {
		t.Errorf("a multi-line literal was spliced:\n%s", out)
	}
	if len(res.Applied)+len(res.Skipped) != 5 {
		t.Errorf("cargo outcome = %s\n%s", rewriteOutcome(res), out)
	}
}

func TestPublicAPIWriterHandlesComposeFlowSequences(t *testing.T) {
	cases := []struct{ name, tags string }{
		{"an empty list", `[]`},
		{"a list of spaces", `[   ]`},
		{"trailing spaces around entries", `[ ghcr.io/acme/api:1.0.0 , ghcr.io/acme/api:stable ]`},
		{"an unterminated quote", `["ghcr.io/acme/api:1.0.0`},
		{"an unclosed list", `[ghcr.io/acme/api:1.0.0`},
		{"mixed quoting", `["ghcr.io/acme/api:1.0.0", 'ghcr.io/acme/api:stable', ghcr.io/acme/api:bare]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, out, _ := rewriteFixture(t, "compose.yaml", `services:
  api:
    build:
      context: .
      tags: `+tc.tags+`
    image: ghcr.io/acme/api:1.0.0
  cache:
    image: redis:7.2
`, "2.0.0", []writer.Edit{{Name: "redis", Range: "7.4"}})
			if !res.VersionWritten {
				t.Errorf("the file's own image was not written:\n%s", out)
			}
			if !strings.Contains(out, "image: ghcr.io/acme/api:2.0.0") {
				t.Errorf("rewritten compose file:\n%s", out)
			}
		})
	}

	t.Run("service bodies that are not mappings of keys", func(t *testing.T) {
		res, out, _ := rewriteFixture(t, "compose.yaml", `services:
  api:
    build: .
    image: ghcr.io/acme/api:1.0.0
    entrypoint:
      - /bin/sh
      - -c
    healthcheck:
      test:
        - CMD
        - curl
  cache:
    image:
      # a mapping where a scalar belongs
      name: redis:7.2
`, "2.0.0", []writer.Edit{{Name: "redis", Range: "7.4"}})
		if !res.VersionWritten || !strings.Contains(out, "ghcr.io/acme/api:2.0.0") {
			t.Errorf("rewritten compose file:\n%s", out)
		}
		if !strings.Contains(out, "name: redis:7.2") {
			t.Error("a nested mapping was read as an image reference")
		}
	})
}

func TestPublicAPIWriterHandlesMalformedCatalogEntries(t *testing.T) {
	res, out, _ := rewriteFixture(t, "gradle/libs.versions.toml", `[versions]
shared = "1.0.0"

[libraries]
short = "onlyone"
long = "a:b:c:d"
nocolon = { module = "nocolon" }
onlyname = { name = "onlyname" }
onlygroup = { group = "acme" }
emptygroup = { group = "", name = "x", version = "1.0.0" }
byref = { module = "acme:byref", version.ref = "shared" }
missingref = { module = "acme:missingref", version.ref = "absent" }
richref = { module = "acme:richref", version = { require = "1.0.0" } }
shorthand = "acme:shorthand:1.0.0"
`, "", []writer.Edit{
		{Name: "acme:byref", Range: "2.0.0"},
		{Name: "acme:missingref", Range: "2.0.0"},
		{Name: "acme:richref", Range: "2.0.0"},
		{Name: "acme:shorthand", Range: "2.0.0"},
		{Name: "nocolon", Range: "2.0.0"},
		{Name: "onlyname", Range: "2.0.0"},
		{Name: "a:b", Range: "2.0.0"},
	})
	if !strings.Contains(out, `shared = "2.0.0"`) {
		t.Errorf("a shared version was not written:\n%s", out)
	}
	if !strings.Contains(out, `shorthand = "acme:shorthand:2.0.0"`) {
		t.Errorf("a shorthand coordinate was not written:\n%s", out)
	}
	if !strings.Contains(out, `short = "onlyone"`) || !strings.Contains(out, `long = "a:b:c:d"`) {
		t.Errorf("an unreadable entry was rewritten:\n%s", out)
	}
	if len(res.Missing) < 3 {
		t.Errorf("catalog outcome = %s\n%s", rewriteOutcome(res), out)
	}
}

func TestPublicAPIWriterHandlesCarriageReturnsAndNamelessLines(t *testing.T) {
	t.Run("requirements written with CRLF endings", func(t *testing.T) {
		res, out, _ := rewriteFixture(t, "requirements.txt",
			"# runtime\r\ncore>=1.0\r\n>=2.0\r\nrequests>=2.0\r\n", "",
			[]writer.Edit{{Name: "core", Range: ">=2.0"}, {Name: "requests", Range: ">=3.0"}})
		if len(res.Applied) != 2 {
			t.Errorf("requirements outcome = %s", rewriteOutcome(res))
		}
		if !strings.Contains(out, "core>=2.0\r\n") || !strings.Contains(out, "requests>=3.0\r\n") {
			t.Errorf("the line endings were not preserved: %q", out)
		}
	})

	t.Run("unity settings written with CRLF endings", func(t *testing.T) {
		res, out, _ := rewriteFixture(t, "ProjectSettings/ProjectSettings.asset",
			"PlayerSettings:\r\n  productName: Acme\r\n  bundleVersion: 1.0.0\r\n  emptyKey:\r\n", "2.0.0", nil)
		if !res.VersionWritten || !strings.Contains(out, "bundleVersion: 2.0.0\r\n") {
			t.Errorf("rewritten settings: %q", out)
		}
	})

	t.Run("an ini key another key merely starts with", func(t *testing.T) {
		res, out, _ := rewriteFixture(t, "project.godot", `[application]

config/version_note="keep me"
config/version="1.0.0"
`, "2.0.0", nil)
		if !res.VersionWritten || !strings.Contains(out, `config/version="2.0.0"`) {
			t.Errorf("rewritten project.godot:\n%s", out)
		}
		if !strings.Contains(out, `config/version_note="keep me"`) {
			t.Error("a longer key was rewritten")
		}
	})

	t.Run("an xml attribute another attribute merely starts with", func(t *testing.T) {
		res, out, _ := rewriteFixture(t, "packages.config",
			`<packages><package id="Core" version-note="a version= b" version="1.0.0" /></packages>`, "",
			[]writer.Edit{{Name: "Core", Range: "2.0.0"}})
		if len(res.Applied) != 1 || !strings.Contains(out, `version="2.0.0"`) {
			t.Errorf("rewritten packages.config:\n%s", out)
		}
		if !strings.Contains(out, `version-note="a version= b"`) {
			t.Error("a longer attribute name was rewritten")
		}
	})
}

func TestPublicAPIWriterHandlesPubspecOverrideShapes(t *testing.T) {
	t.Run("a scalar override is a constraint until explicitly relinked", func(t *testing.T) {
		path := writeFile(t, t.TempDir(), "pubspec.yaml", `name: acme
version: 1.0.0

dependencies:
  core: ^1.0.0
  other: ^1.0.0

dependency_overrides:
  core: ../core
  other:
    git:
      url: https://example.com/other.git
`)
		links, err := writer.Links(path)
		if err != nil || len(links) != 0 {
			t.Fatalf("links = %+v, err = %v", links, err)
		}
		before := readFile(t, path)
		dropped, err := writer.DropLinks(path)
		if err != nil || len(dropped.Applied) != 0 || readFile(t, path) != before {
			t.Fatalf("drop = %+v, err = %v", dropped, err)
		}
		res, err := writer.Relink(path, []writer.Link{{Name: "core", Path: "../moved"}})
		if err != nil || len(res.Applied) != 1 {
			t.Fatalf("relink = %+v, err = %v", res, err)
		}
		links, err = writer.Links(path)
		if err != nil || renderLinks(links) != "core=../moved" {
			t.Fatalf("links after relink = %+v, err = %v", links, err)
		}
		if !strings.Contains(readFile(t, path), "https://example.com/other.git") {
			t.Error("a git override was disturbed")
		}
	})

	t.Run("an override block that ends the file", func(t *testing.T) {
		path := writeFile(t, t.TempDir(), "pubspec.yaml",
			"name: acme\nversion: 1.0.0\ndependencies:\n  core: ^1.0.0\ndependency_overrides:\n  core:\n    path: ../core\n")
		res, err := writer.DropLinks(path)
		if err != nil || len(res.Applied) != 1 {
			t.Fatalf("drop = %+v, err = %v", res, err)
		}
		out := readFile(t, path)
		if strings.Contains(out, "dependency_overrides") {
			t.Fatalf("an emptied override block was left behind:\n%s", out)
		}
		if !strings.Contains(out, "core: ^1.0.0") {
			t.Fatalf("the declaration was lost:\n%s", out)
		}
	})
}

func TestPublicAPIWriterHandlesLargeFilesAndKotlinBuildScripts(t *testing.T) {
	t.Run("a text file larger than the binary sniff window", func(t *testing.T) {
		body := strings.Repeat("# a line of prose that carries no version at all\n", 400) +
			"image: ghcr.io/acme/api:1.0.0\n"
		path := writeFile(t, t.TempDir(), "notes.md", body)
		res, err := writer.Replace(path, []writer.Replacement{
			{Find: "ghcr.io/acme/api:1.0.0", Write: "ghcr.io/acme/api:2.0.0"},
		})
		if err != nil || res.Count != 1 {
			t.Fatalf("replace = %+v, err = %v", res, err)
		}
		if !strings.Contains(readFile(t, path), "ghcr.io/acme/api:2.0.0") {
			t.Fatal("the replacement did not land")
		}
	})

	t.Run("a compose file whose own version would not be a tag", func(t *testing.T) {
		assertRefusal(t, "compose.yaml",
			"services:\n  api:\n    build: .\n    image: ghcr.io/acme/api:1.0.0\n", "not a tag", nil)
	})

	t.Run("a kotlin build script assigning its properties", func(t *testing.T) {
		res, out, _ := rewriteFixture(t, "build.gradle.kts", `android {
    defaultConfig {
        versionNameSuffix = "-dev"
        versionCodeOverride = 9
        versionName = "1.0.0"
        versionCode = 7
    }
}

dependencies {
    implementation("acme:core:1.0.0")
}
`, "2.0.0", []writer.Edit{{Name: "acme:core", Range: "1.1.0"}})
		if !res.VersionWritten || !strings.Contains(out, `versionName = "2.0.0"`) {
			t.Errorf("rewritten build script:\n%s", out)
		}
		if !strings.Contains(out, `versionNameSuffix = "-dev"`) {
			t.Error("a longer property name was rewritten")
		}
		path := writeFile(t, t.TempDir(), "build.gradle.kts", out)
		build, err := writer.SetBuild(path, "42")
		if err != nil || !build.BuildWritten {
			t.Fatalf("set build = %+v, err = %v", build, err)
		}
		if !strings.Contains(readFile(t, path), "versionCode = 42") ||
			!strings.Contains(readFile(t, path), "versionCodeOverride = 9") {
			t.Errorf("rewritten counter:\n%s", readFile(t, path))
		}
	})

	t.Run("a cargo patch entry written with trailing spaces", func(t *testing.T) {
		path := writeFile(t, t.TempDir(), "Cargo.toml", "[package]\nname = \"acme\"\nversion = \"1.0.0\"\n\n"+
			"[patch.crates-io]\ncore = { path = \"../core\" }   \n")
		links, err := writer.Links(path)
		if err != nil || renderLinks(links) != "core=../core" {
			t.Fatalf("links = %+v, err = %v", links, err)
		}
		res, err := writer.Relink(path, []writer.Link{{Name: "core", Path: "../moved"}, {Name: "empty"}})
		if err != nil || len(res.Applied) != 1 || len(res.Missing) != 1 {
			t.Fatalf("relink = %+v, err = %v", res, err)
		}
	})
}
