package publicapi_test

// The byte-level edges each format writer has to survive: quoting, escapes,
// comments that are not comments, and the document shapes a splice cannot
// reach. A manifest in the wild carries all of them, and the writer's promise
// is that every byte it did not aim at comes through unchanged.

import (
	"strings"
	"testing"

	"github.com/yohimik/dispat/pkg/manifest"
	"github.com/yohimik/dispat/pkg/writer"
)

func TestPublicAPIWriterSurvivesTOMLQuoting(t *testing.T) {
	t.Run("cargo", func(t *testing.T) {
		res, out, _ := rewriteFixture(t, "Cargo.toml", `[package]
name = "acme"            # the crate
version = "1.0.0"        # bumped by the release
description = "a \" quote, a # hash and an = sign"
keywords = 'a # hash'
readme = """
a multi-line
description
"""

[dependencies]
core = "1.0"             # the core crate
literal = '1.0'
escaped = "1.0!"
inline = { version = "1.0", features = ["a", "b"] }
"quoted-key" = "1.0"
'literal-key' = "1.0"
enabled = true
`, "2.0.0", []writer.Edit{
			{Name: "core", Range: "1.1"},
			{Name: "literal", Range: "1.1"},
			{Name: "escaped", Range: "1.1"},
			{Name: "inline", Range: "1.1"},
			{Name: "quoted-key", Range: "1.1"},
			{Name: "literal-key", Range: "1.1"},
			{Name: "enabled", Range: "1.1"},
		})
		if !res.VersionWritten {
			t.Error("the crate version was not written")
		}
		if !strings.Contains(out, `version = "2.0.0"        # bumped by the release`) {
			t.Errorf("the trailing comment was disturbed:\n%s", out)
		}
		if !strings.Contains(out, `description = "a \" quote, a # hash and an = sign"`) ||
			!strings.Contains(out, `keywords = 'a # hash'`) ||
			!strings.Contains(out, "a multi-line") {
			t.Errorf("an unrelated string was disturbed:\n%s", out)
		}
		if !strings.Contains(out, `core = "1.1"             # the core crate`) {
			t.Errorf("the dependency comment was disturbed:\n%s", out)
		}
		if !strings.Contains(out, `literal = '1.1'`) {
			t.Errorf("a literal string was not written:\n%s", out)
		}
		if !strings.Contains(out, `enabled = true`) {
			t.Error("a value that is not a version was rewritten")
		}
		if len(res.Applied) < 4 {
			t.Errorf("cargo outcome = %s", rewriteOutcome(res))
		}
	})

	t.Run("gradle catalog keys that merely start alike", func(t *testing.T) {
		res, out, _ := rewriteFixture(t, "libs.versions.toml", `[versions]
core = "1.0.0"

[libraries]
by-ref = { module = "acme:by-ref", version.ref = "core" }
inline = { module = "acme:inline", version = "1.0.0" }
reversed = { version = "1.0.0", module = "acme:reversed" }
grouped = { group = "acme", name = "grouped", version = "1.0.0" }
shorthand = "acme:shorthand:1.0.0"
commented = { module = "acme:commented", version = "1.0.0" } # pinned
`, "", []writer.Edit{
			{Name: "acme:by-ref", Range: "2.0.0"},
			{Name: "acme:inline", Range: "2.0.0"},
			{Name: "acme:reversed", Range: "2.0.0"},
			{Name: "acme:grouped", Range: "2.0.0"},
			{Name: "acme:shorthand", Range: "2.0.0"},
			{Name: "acme:commented", Range: "2.0.0"},
		})
		if len(res.Applied) != 6 {
			t.Errorf("catalog outcome = %s\n%s", rewriteOutcome(res), out)
		}
		if strings.Contains(out, "1.0.0") {
			t.Errorf("a version survived the rewrite:\n%s", out)
		}
		if !strings.Contains(out, "# pinned") {
			t.Errorf("a trailing comment was lost:\n%s", out)
		}
	})

	t.Run("pyproject quoting", func(t *testing.T) {
		res, out, _ := rewriteFixture(t, "pyproject.toml", `[project]
name = "acme"
version = "1.0.0"
description = "acme # the tool, and an = sign"
dependencies = [
    "core>=1.0",     # the core package
    'literal>=1.0',
    "quoted[extra]>=1.0",
]

[tool.poetry.dependencies]
poetry-core = "^1.0"     # a poetry entry
poetry-table = { version = "^1.0", optional = true }
poetry-list = [
    { version = "^1.0", markers = "sys_platform == 'linux'" },
]
`, "2.0.0", []writer.Edit{
			{Name: "core", Range: ">=2.0"},
			{Name: "literal", Range: ">=2.0"},
			{Name: "quoted", Range: ">=2.0"},
			{Name: "poetry-core", Range: "^2.0"},
			{Name: "poetry-table", Range: "^2.0"},
			{Name: "poetry-list", Range: "^2.0"},
		})
		if !res.VersionWritten {
			t.Error("the project version was not written")
		}
		if !strings.Contains(out, `description = "acme # the tool, and an = sign"`) {
			t.Errorf("an unrelated string was disturbed:\n%s", out)
		}
		if !strings.Contains(out, "# the core package") || !strings.Contains(out, "# a poetry entry") {
			t.Errorf("a trailing comment was lost:\n%s", out)
		}
		if len(res.Applied) < 4 {
			t.Errorf("python outcome = %s\n%s", rewriteOutcome(res), out)
		}
	})
}

func TestPublicAPIWriterSurvivesRubyQuoting(t *testing.T) {
	res, out, _ := rewriteFixture(t, "Acme.podspec", `Pod::Spec.new do |s|
  s.name              = 'Acme'
  s.version           = '1.0.0'
  s.summary           = 'Acme # not a comment'
  s.description       = "Acme \"Pro\" # still not a comment"
  s.homepage          = 'https://example.com/acme#readme'
  s.static_framework  = true
  s.version           == '9.9.9'
  s.deployment_target => '15.0'
  s.dependency 'AcmeCore', '~> 1.0.0' # the core pod
  s.dependency "AcmeUI", "~> 1.0.0"
  s.dependency 'AcmeEsc', '~> 1.0.0'
  s.ios.dependency 'AcmeNet', '>= 1.0', '< 2.0'
  s.dependency 'AcmeBare'
  s.dependency 'AcmeLocal', :path => '../local'
  s.dependency 'AcmeDyn', "~> #{s.version}"
  s.dependency_placeholder 'NotADependency', '~> 1.0.0'
end
`, "2.0.0", []writer.Edit{
		{Name: "AcmeCore", Range: "~> 2.0.0"},
		{Name: "AcmeUI", Range: "~> 2.0.0"},
		{Name: "AcmeEsc", Range: "~> 2.0.0"},
		{Name: "AcmeNet", Range: ">= 2.0, < 3.0"},
		{Name: "AcmeBare", Range: "~> 2.0.0"},
		{Name: "AcmeLocal", Range: "~> 2.0.0"},
		{Name: "AcmeDyn", Range: "~> 2.0.0"},
		{Name: "NotADependency", Range: "~> 2.0.0"},
		{Name: "AcmeAbsent", Range: "~> 2.0.0"},
	})
	if !res.VersionWritten || !strings.Contains(out, "s.version           = '2.0.0'") {
		t.Errorf("rewritten podspec:\n%s", out)
	}
	if !strings.Contains(out, "s.summary           = 'Acme # not a comment'") ||
		!strings.Contains(out, `s.description       = "Acme \"Pro\" # still not a comment"`) ||
		!strings.Contains(out, "https://example.com/acme#readme") {
		t.Errorf("a quoted string was disturbed:\n%s", out)
	}
	if !strings.Contains(out, "s.version           == '9.9.9'") {
		t.Error("a comparison was read as an assignment")
	}
	if !strings.Contains(out, "s.dependency 'AcmeCore', '~> 2.0.0' # the core pod") {
		t.Errorf("a trailing comment was lost:\n%s", out)
	}
	if !strings.Contains(out, `s.dependency "AcmeUI", "~> 2.0.0"`) {
		t.Errorf("a double-quoted requirement was not written:\n%s", out)
	}
	if !strings.Contains(out, "'NotADependency', '~> 1.0.0'") {
		t.Error("a longer identifier was read as a dependency call")
	}
	if len(res.Applied) < 4 {
		t.Errorf("podspec outcome = %s", rewriteOutcome(res))
	}
}

func TestPublicAPIWriterSurvivesYAMLQuoting(t *testing.T) {
	res, out, _ := rewriteFixture(t, "pubspec.yaml", `name: acme_app
version: 1.0.0

# The dependencies this application needs.
dependencies:
  quoted: "^1.0.0"          # a double-quoted constraint
  literal: '^1.0.0'
  plain: ^1.0.0
  spaced:   ^1.0.0
  hashed: ^1.0.0#notacomment
  blocked:
    path: ../blocked
  empty:

dev_dependencies:
  test_dep: ^1.0.0
`, "2.0.0", []writer.Edit{
		{Name: "quoted", Range: "^2.0.0"},
		{Name: "literal", Range: "^2.0.0"},
		{Name: "plain", Range: "^2.0.0"},
		{Name: "spaced", Range: "^2.0.0"},
		{Name: "hashed", Range: "^2.0.0"},
		{Name: "blocked", Range: "^2.0.0"},
		{Name: "empty", Range: "^2.0.0"},
		{Name: "test_dep", Kind: manifest.KindDevDependencies, Range: "^2.0.0"},
		{Name: "absent", Range: "^2.0.0"},
	})
	if !res.VersionWritten || !strings.Contains(out, "version: 2.0.0") {
		t.Errorf("rewritten pubspec:\n%s", out)
	}
	if !strings.Contains(out, "# a double-quoted constraint") ||
		!strings.Contains(out, "# The dependencies this application needs.") {
		t.Errorf("a comment was lost:\n%s", out)
	}
	if !strings.Contains(out, "path: ../blocked") {
		t.Error("a block dependency was rewritten as a constraint")
	}
	if len(res.Applied) < 4 || len(res.Missing) != 1 {
		t.Errorf("pubspec outcome = %s\n%s", rewriteOutcome(res), out)
	}
}

func TestPublicAPIWriterRefusesAquaShapesItCannotSplice(t *testing.T) {
	cases := []struct{ name, body, wants string }{
		{"flow-style package list", "packages: [{name: cli/cli, version: v1.0.0}]\n", "flow-style packages"},
		{"flow-style entry", "packages:\n  - {name: cli/cli, version: v1.0.0}\n", "flow-style package entries"},
		{"an anchored package list", "packages: &shared\n  - name: cli/cli\n    version: v1.0.0\n", "anchors and aliases"},
		{"an aliased entry", `base: &base
  name: cli/cli
  version: v1.0.0
packages:
  - *base
`, "aliases"},
		{"a block-scalar name", "packages:\n  - name: |\n      cli/cli\n    version: v1.0.0\n", "block-scalar package names"},
		{"a block-scalar version", "packages:\n  - name: cli/cli\n    version: |\n      v1.0.0\n", "block-scalar package versions"},
		{"packages that are not a sequence", "packages:\n  name: cli/cli\n", "must be a sequence"},
		{"a document that is not a mapping", "- name: cli/cli\n", "must be a mapping"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := assertRefusal(t, "aqua.yaml", tc.body, "", []writer.Edit{{Name: "cli/cli", Range: "v2.0.0"}})
			if !strings.Contains(err.Error(), tc.wants) {
				t.Errorf("error = %v; want it to mention %q", err, tc.wants)
			}
		})
	}

	t.Run("a version a YAML scalar cannot hold", func(t *testing.T) {
		assertRefusal(t, "aqua.yaml", "packages:\n  - name: cli/cli\n    version: v1.0.0\n", "",
			[]writer.Edit{{Name: "cli/cli", Range: "v2.0.0: injected"}})
	})

	t.Run("entries a rewrite steps over", func(t *testing.T) {
		res, out, _ := rewriteFixture(t, "aqua.yaml", `registries:
  - type: standard
    ref: v4.200.0

packages:
  - import: imports/extra.yaml
  - name:
      structured: true
    version: v1.0.0
  - name: acme/registry@v1.0.0
    registry: acme
  - name: bare/tool
  - name: dynamic/tool
    version_expr: semver(">= 1.0.0")
  - name: cli/cli
    version: v2.55.0    # the GitHub CLI
`, "", []writer.Edit{
			{Name: "cli/cli", Range: "v2.60.0"},
			{Name: "acme:acme/registry", Range: "v2.0.0"},
			{Name: "bare/tool", Range: "v1.0.0"},
			{Name: "dynamic/tool", Range: "v1.0.0"},
			{Name: "absent/tool", Range: "v1.0.0"},
			{Name: "cli/cli", Kind: manifest.KindDevDependencies, Range: "v9.0.0"},
		})
		if !strings.Contains(out, "version: v2.60.0    # the GitHub CLI") {
			t.Errorf("rewritten aqua configuration:\n%s", out)
		}
		if !strings.Contains(out, "acme/registry@v2.0.0") {
			t.Errorf("a registry-qualified pin was not written:\n%s", out)
		}
		if len(res.Skipped) != 2 || len(res.Missing) != 2 {
			t.Errorf("aqua outcome = %s", rewriteOutcome(res))
		}
	})
}

func TestPublicAPIWriterSurvivesPlistAndProjectShapes(t *testing.T) {
	t.Run("only the root dictionary's own string is written", func(t *testing.T) {
		res, out, _ := rewriteFixture(t, "Info.plist", `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
	<key>LSRequiresIPhoneOS</key>
	<true/>
	<key>UIRequiredDeviceCapabilities</key>
	<array>
		<string>armv7</string>
	</array>
	<key>NestedTypes</key>
	<array>
		<dict>
			<key>CFBundleShortVersionString</key>
			<string>nested-must-not-move</string>
		</dict>
	</array>
	<key>CFBundleShortVersionString</key>
	<string>1.0.0</string>
	<key>DanglingKey</key>
</dict>
</plist>
`, "2.0.0", nil)
		if !res.VersionWritten {
			t.Error("the marketing version was not written")
		}
		if !strings.Contains(out, "<string>2.0.0</string>") || !strings.Contains(out, "nested-must-not-move") {
			t.Errorf("rewritten plist:\n%s", out)
		}
	})

	t.Run("a self-closing string has nothing to splice into", func(t *testing.T) {
		res, out, _ := rewriteFixture(t, "Info.plist",
			"<plist><dict><key>CFBundleShortVersionString</key><string/></dict></plist>", "2.0.0", nil)
		if res.VersionWritten || !strings.Contains(out, "<string/>") {
			t.Errorf("a self-closing value was written into:\n%s", out)
		}
	})

	t.Run("a plist with no root dictionary declares nothing", func(t *testing.T) {
		res, out, _ := rewriteFixture(t, "Info.plist",
			`<?xml version="1.0"?><plist version="1.0"><array><string>1.0.0</string></array></plist>`, "2.0.0", nil)
		if res.VersionWritten || !strings.Contains(out, "<string>1.0.0</string>") {
			t.Errorf("a plist with no dictionary was written into:\n%s", out)
		}
	})

	t.Run("a dictionary closing on a dangling key", func(t *testing.T) {
		res, _, _ := rewriteFixture(t, "Info.plist",
			"<plist><dict><key>CFBundleShortVersionString</key></dict></plist>", "2.0.0", nil)
		if res.VersionWritten {
			t.Error("a dangling key was written into")
		}
	})

	t.Run("a project file's quoting and conditional settings", func(t *testing.T) {
		res, out, _ := rewriteFixture(t, "project.pbxproj", `// !$*UTF8*$!
{
	objects = {
		DEBUG = {
			buildSettings = {
				MARKETING_VERSION[sdk=iphoneos*] = 9.9.9;
				MARKETING_VERSION = "1.0.0" ;
				MARKETING_VERSION = 1.0.0   ;
				INFOPLIST_KEY_CFBundleDisplayName = "Acme \"Pro\"";
				UNTERMINATED = "no closing quote;
				NO_SEMICOLON = 1
				EMPTY_SETTING =
			};
		};
	};
}
`, "2.0.0", nil)
		if !res.VersionWritten || !strings.Contains(out, `MARKETING_VERSION = "2.0.0" ;`) {
			t.Errorf("rewritten project file:\n%s", out)
		}
		if !strings.Contains(out, "MARKETING_VERSION = 2.0.0   ;") {
			t.Errorf("an unquoted build setting lost its spacing:\n%s", out)
		}
		if !strings.Contains(out, "MARKETING_VERSION[sdk=iphoneos*] = 9.9.9;") {
			t.Error("a conditional assignment was rewritten")
		}
		if !strings.Contains(out, `"Acme \"Pro\""`) {
			t.Errorf("an escaped literal was disturbed:\n%s", out)
		}
	})
}

func TestPublicAPIWriterSurvivesEngineDocumentShapes(t *testing.T) {
	t.Run("Unity package manifest cannot express a dev dependency", func(t *testing.T) {
		const body = `{"dependencies":{"com.acme.core":"1.0.0"}}`
		path := writeFile(t, t.TempDir(), "Packages/manifest.json", body)
		result, err := writer.Rewrite(path, "", []writer.Edit{{Name: "com.acme.core", Kind: manifest.KindDevDependencies, Range: "2.0.0"}})
		if err != nil || len(result.Missing) != 1 || len(result.Applied) != 0 {
			t.Fatalf("Unity dev dependency rewrite = %+v, %v", result, err)
		}
		if got := readFile(t, path); got != body {
			t.Fatalf("unsupported dev dependency changed Unity manifest: %s", got)
		}
	})
	t.Run("unity settings nested under the player block", func(t *testing.T) {
		res, out, _ := rewriteFixture(t, "ProjectSettings/ProjectSettings.asset", `%YAML 1.1
%TAG !u! tag:unity3d.com,2011:
--- !u!129 &1
PlayerSettings:
  m_ObjectHideFlags: 0
  productName: Acme
  bundleVersion: 1.0.0 # marketing
  buildNumber:
    Standalone: 7
    iPhone:
  vectorData:
    - 1
  # a comment line
  notAnEntry
  nested:
    bundleVersion: must-not-move
`, "2.0.0", nil)
		if !res.VersionWritten || !strings.Contains(out, "bundleVersion: 2.0.0 # marketing") {
			t.Errorf("rewritten unity settings:\n%s", out)
		}
		if !strings.Contains(out, "bundleVersion: must-not-move") {
			t.Error("a nested value was rewritten")
		}
	})

	t.Run("an ini value carrying the comment token inside a literal", func(t *testing.T) {
		res, out, _ := rewriteFixture(t, "project.godot", `; Engine configuration file.
config_version=5

[application]

config/name="Acme \"Pro\" ; Game"
config/version="1.0.0" ; the marketing version
config/features=PackedStringArray("4.2")
=orphan

[display]

config/version="must-not-move"
`, "2.0.0", nil)
		if !res.VersionWritten || !strings.Contains(out, `config/version="2.0.0" ; the marketing version`) {
			t.Errorf("rewritten project.godot:\n%s", out)
		}
		if !strings.Contains(out, `config/name="Acme \"Pro\" ; Game"`) {
			t.Error("a quoted name was disturbed")
		}
		if !strings.Contains(out, `config/version="must-not-move"`) {
			t.Error("another section's key was rewritten")
		}
	})

	t.Run("an unreal descriptor with nested plugin objects", func(t *testing.T) {
		res, out, _ := rewriteFixture(t, "Acme.uplugin", `{
	"FileVersion": 3,
	"Version": 7,
	"VersionName": "1.0.0",
	"Modules": [
		{ "Name": "AcmeRuntime", "Type": "Runtime" }
	],
	"Plugins": [
		{ "Name": "Networking", "Enabled": true, "PlatformAllowList": ["Win64"] },
		{ "Enabled": true },
		"not-an-object"
	]
}
`, "2.0.0", []writer.Edit{
			{Name: "Networking", Range: "1.0.0"},
			{Name: "AcmeRuntime", Range: "1.0.0"},
		})
		if !res.VersionWritten || !strings.Contains(out, `"VersionName": "2.0.0"`) {
			t.Errorf("rewritten uplugin:\n%s", out)
		}
		if len(res.Skipped) != 1 || len(res.Missing) != 1 {
			t.Errorf("uplugin outcome = %s", rewriteOutcome(res))
		}
		if !strings.Contains(out, `"Version": 7`) {
			t.Error("the build counter moved during a version write")
		}
	})

	t.Run("an o3de manifest with entries a rewrite steps over", func(t *testing.T) {
		res, out, _ := rewriteFixture(t, "gem.json", `{
    "gem_name": "AcmeGem",
    "display_name": "Acme Gem",
    "version": "1.0.0",
    "tags": ["one", "two"],
    "dependencies": [
        "Atom_RHI==1.0.0",
        "Camera",
        "Atom_RHI==1.0.0",
        42
    ],
    "external_subdirectories": ["Gem"]
}
`, "2.0.0", []writer.Edit{
			{Name: "Atom_RHI", Range: "==2.0.0"},
			{Name: "Camera", Range: ">=1.0.0"},
			{Name: "Absent", Range: "==1.0.0"},
		})
		if !res.VersionWritten || !strings.Contains(out, `"version": "2.0.0"`) {
			t.Errorf("rewritten gem.json:\n%s", out)
		}
		if !strings.Contains(out, `"tags": ["one", "two"]`) {
			t.Error("an unrelated array was disturbed")
		}
		if len(res.Applied) != 2 || len(res.Missing) != 1 {
			t.Errorf("o3de outcome = %s\n%s", rewriteOutcome(res), out)
		}
	})
}

func TestPublicAPIWriterSurvivesGradleCommentsAndClosures(t *testing.T) {
	res, out, _ := rewriteFixture(t, "build.gradle", `/*
 * Acme Android application.
 */
buildscript {
    dependencies {
        classpath 'com.android.tools.build:gradle:8.5.0'
    }
}

android {
    defaultConfig {
        versionName '1.0.0'   // the marketing version
        versionNameSuffix '-dev'
        versionCode 7
        manifestPlaceholders = [label: "Acme\"s App"]
    }
}

dependencies {
    implementation 'acme:core:1.0.0'
    implementation("acme:okhttp:1.0.0")   /* inline block comment */
    api (project(':shared')) {
        exclude group: 'org.json'
    }
    implementation 'acme:catalog'
    implementation "acme:dynamic:$acmeVersion"
    implementation 'unterminated
    somethingElse 'acme:notaconfiguration:1.0.0'
}
`, "2.0.0", []writer.Edit{
		{Name: "acme:core", Range: "1.1.0"},
		{Name: "acme:okhttp", Range: "1.1.0"},
		{Name: "acme:catalog", Range: "1.1.0"},
		{Name: "acme:dynamic", Range: "1.1.0"},
		{Name: "acme:notaconfiguration", Range: "1.1.0"},
		{Name: "com.android.tools.build:gradle", Range: "8.6.0"},
		{Name: "shared", Range: "1.1.0"},
	})
	if !res.VersionWritten || !strings.Contains(out, "versionName '2.0.0'   // the marketing version") {
		t.Errorf("rewritten build script:\n%s", out)
	}
	if !strings.Contains(out, "versionCode 7") {
		t.Error("the build counter moved during a version write")
	}
	if !strings.Contains(out, "acme:core:1.1.0") || !strings.Contains(out, "acme:okhttp:1.1.0") {
		t.Errorf("a coordinate was not written:\n%s", out)
	}
	if !strings.Contains(out, "classpath 'com.android.tools.build:gradle:8.5.0'") {
		t.Error("a buildscript classpath entry was rewritten")
	}
	if !strings.Contains(out, `"acme:dynamic:$acmeVersion"`) {
		t.Error("an interpolated coordinate was rewritten")
	}
	if !strings.Contains(out, `manifestPlaceholders = [label: "Acme\"s App"]`) {
		t.Error("an escaped literal was disturbed")
	}
	if len(res.Applied) != 2 {
		t.Errorf("gradle outcome = %s", rewriteOutcome(res))
	}
}

func TestPublicAPIWriterSurvivesComposeAndDockerShapes(t *testing.T) {
	t.Run("compose scalars that are not image references", func(t *testing.T) {
		_, out, _ := rewriteFixture(t, "compose.yaml", `# The Acme stack.
services:
  api:
    build:
      context: .
      dockerfile: Dockerfile
      tags:
        - "ghcr.io/acme/api:1.0.0"
        - 'ghcr.io/acme/api:stable'
        - ghcr.io/acme/api:bare
    image: ghcr.io/acme/api:1.0.0   # the published image
    ports:
      - "8080:80"
    command: ["run", "--image", "redis:7.2"]
    environment:
      REDIS_URL: "redis:6379"
  cache:
    image: redis:7.2
  broken:
  listed:
    - not
    - a
    - mapping
volumes:
  data:
`, "2.0.0", []writer.Edit{{Name: "redis", Range: "7.4"}})
		if !strings.Contains(out, "image: ghcr.io/acme/api:2.0.0   # the published image") {
			t.Errorf("the file's own image was not written:\n%s", out)
		}
		if strings.Count(out, "ghcr.io/acme/api:2.0.0") < 2 {
			t.Errorf("the build tags were not written:\n%s", out)
		}
		if !strings.Contains(out, `- "8080:80"`) || !strings.Contains(out, `REDIS_URL: "redis:6379"`) ||
			!strings.Contains(out, `"redis:7.2"]`) {
			t.Errorf("a scalar that is not an image reference was rewritten:\n%s", out)
		}
		if !strings.Contains(out, "image: redis:7.4") {
			t.Errorf("the dependency image was not written:\n%s", out)
		}
	})

	t.Run("a dockerfile with continuations and comments", func(t *testing.T) {
		res, out, _ := rewriteFixture(t, "api.Dockerfile", `# syntax=docker/dockerfile:1.7
ARG BASE=ghcr.io/acme/base

FROM --platform=linux/amd64 ghcr.io/acme/base:1.0.0 AS build
RUN --mount=type=bind,from=ghcr.io/acme/assets:3.0.0,source=/a,target=/a \
    --mount=type=cache,target=/root/.cache \
    make build
RUN mytool sync --from=not/an:image

FROM scratch AS empty
FROM build AS reuse
COPY --from=0 /out /out
COPY --from=build /out /out
FROM ghcr.io/acme/base:1.0.0 AS second
`, "", []writer.Edit{
			{Name: "ghcr.io/acme/base", Range: "1.1.0"},
			{Name: "ghcr.io/acme/assets", Range: "3.1.0"},
			{Name: "not/an", Range: "9.9.9"},
		})
		if strings.Count(out, "ghcr.io/acme/base:1.1.0") != 2 {
			t.Errorf("both stages must move together:\n%s", out)
		}
		if !strings.Contains(out, "from=ghcr.io/acme/assets:3.1.0") {
			t.Errorf("a mount reference was not written:\n%s", out)
		}
		if !strings.Contains(out, "mytool sync --from=not/an:image") {
			t.Error("a command flag was rewritten as an image reference")
		}
		if len(res.Applied) != 2 || len(res.Missing) != 1 {
			t.Errorf("dockerfile outcome = %s", rewriteOutcome(res))
		}
	})
}
