package publicapi_test

// Format-by-format writing through the public writer, on the same realistic
// files the scanner reads. Every case checks the three outcomes a rewrite
// reports (applied, missing, skipped), that the bytes around the change are
// untouched, and that reading the file back yields what was asked for.

import (
	"strings"
	"testing"

	"github.com/yohimik/dispat/pkg/manifest"
	"github.com/yohimik/dispat/pkg/scanner"
	"github.com/yohimik/dispat/pkg/writer"
)

// rewriteOutcome renders a Result's three buckets as comparable text.
func rewriteOutcome(res writer.Result) string {
	render := func(edits []writer.Edit) string {
		out := make([]string, 0, len(edits))
		for _, e := range edits {
			out = append(out, e.Kind.String()+":"+e.Name)
		}
		return strings.Join(out, ",")
	}
	return strings.Join([]string{
		"applied=" + render(res.Applied),
		"missing=" + render(res.Missing),
		"skipped=" + render(res.Skipped),
	}, " ")
}

// assertOutcome compares a rewrite result's buckets against the expected text.
func assertOutcome(t *testing.T, res writer.Result, want string) {
	t.Helper()
	if got := rewriteOutcome(res); got != want {
		t.Errorf("rewrite outcome = %q; want %q", got, want)
	}
}

// rewriteFixture writes one manifest, rewrites it, and returns the result and
// the file's new contents.
func rewriteFixture(t *testing.T, rel, body, version string, edits []writer.Edit) (writer.Result, string, string) {
	t.Helper()
	dir := t.TempDir()
	path := writeFile(t, dir, rel, body)
	res, err := writer.Rewrite(path, version, edits)
	if err != nil {
		t.Fatalf("rewrite %s: %v", rel, err)
	}
	return res, readFile(t, path), path
}

// assertRefusal rewrites a manifest expected to be refused and proves the file
// on disk did not move.
func assertRefusal(t *testing.T, rel, body, version string, edits []writer.Edit) error {
	t.Helper()
	dir := t.TempDir()
	path := writeFile(t, dir, rel, body)
	_, err := writer.Rewrite(path, version, edits)
	if err == nil {
		t.Fatalf("%s: the rewrite was not refused", rel)
	}
	if got := readFile(t, path); got != body {
		t.Fatalf("%s: a refusal changed the file:\n%s", rel, got)
	}
	return err
}

func TestPublicAPIWriterRewritesJSONManifests(t *testing.T) {
	t.Run("package.json", func(t *testing.T) {
		res, out, path := rewriteFixture(t, "package.json", `{
  "name": "@acme/app",
  "version": "1.0.0",
  "private": true,
  "workspaces": ["packages/*"],
  "scripts": { "build": "tsc -b" },
  "dependencies": {
    "@acme/core": "^1.0.0",
    "left-pad": "^1.3.0"
  },
  "devDependencies": { "vitest": "^2.0.0" },
  "peerDependencies": { "react": ">=18" },
  "optionalDependencies": { "fsevents": "^2.3.0" }
}
`, "2.0.0", []writer.Edit{
			{Name: "@acme/core", Kind: manifest.KindDependencies, Range: "^2.0.0"},
			{Name: "left-pad", Kind: manifest.KindDependencies, Range: "^1.3.0"},
			{Name: "vitest", Kind: manifest.KindDevDependencies, Range: "^3.0.0"},
			{Name: "react", Kind: manifest.KindPeerDependencies, Range: ">=19"},
			{Name: "fsevents", Kind: manifest.KindOptionalDependencies, Range: "^2.4.0"},
			{Name: "missing-dep", Kind: manifest.KindDependencies, Range: "^1.0.0"},
		})
		assertOutcome(t, res,
			"applied=dependencies:@acme/core,devDependencies:vitest,peerDependencies:react,optionalDependencies:fsevents "+
				"missing=dependencies:missing-dep skipped=")
		if !res.VersionWritten {
			t.Error("the manifest's own version was not written")
		}
		for _, want := range []string{`"version": "2.0.0"`, `"@acme/core": "^2.0.0"`,
			`"left-pad": "^1.3.0"`, `"vitest": "^3.0.0"`, `"\u003e=19"`, `"^2.4.0"`,
			`"workspaces": ["packages/*"]`, `"build": "tsc -b"`} {
			if !strings.Contains(out, want) {
				t.Errorf("rewritten package.json lacks %s:\n%s", want, out)
			}
		}
		m := scanOne(t, strings.TrimSuffix(path, "/package.json"))
		assertIdentity(t, m, "@acme/app|2.0.0|")
	})

	t.Run("a package.json already carrying the wanted text is left alone", func(t *testing.T) {
		res, out, _ := rewriteFixture(t, "package.json",
			`{"name":"acme","version":"1.0.0","dependencies":{"core":"^1.0.0"}}`,
			"1.0.0", []writer.Edit{{Name: "core", Kind: manifest.KindDependencies, Range: "^1.0.0"}})
		assertOutcome(t, res, "applied= missing= skipped=")
		if res.VersionWritten {
			t.Error("an unchanged version was reported written")
		}
		if out != `{"name":"acme","version":"1.0.0","dependencies":{"core":"^1.0.0"}}` {
			t.Errorf("a no-op rewrite changed the file: %q", out)
		}
	})

	t.Run("composer.json has only two dependency fields", func(t *testing.T) {
		res, out, _ := rewriteFixture(t, "composer.json", `{
  "name": "acme/app",
  "version": "1.0.0",
  "require": { "php": ">=8.2", "acme/core": "^1.0" },
  "require-dev": { "phpunit/phpunit": "^11.0" }
}
`, "2.0.0", []writer.Edit{
			{Name: "acme/core", Kind: manifest.KindDependencies, Range: "^2.0"},
			{Name: "phpunit/phpunit", Kind: manifest.KindDevDependencies, Range: "^12.0"},
			{Name: "acme/peer", Kind: manifest.KindPeerDependencies, Range: "^1.0"},
		})
		assertOutcome(t, res,
			"applied=dependencies:acme/core,devDependencies:phpunit/phpunit missing=peerDependencies:acme/peer skipped=")
		if !strings.Contains(out, `"acme/core": "^2.0"`) || !strings.Contains(out, `"php": ">=8.2"`) {
			t.Errorf("rewritten composer.json:\n%s", out)
		}
	})

	t.Run("malformed and unwritable json is refused", func(t *testing.T) {
		cases := []struct{ name, rel, body string }{
			{"truncated", "package.json", `{"name":"acme","dependencies":{"core":`},
			{"top level array", "package.json", `["acme"]`},
			{"field is not an object", "package.json", `{"dependencies": 5}`},
			{"dependency is an object", "package.json", `{"dependencies":{"core":{"version":"1.0.0"}}}`},
			{"version is an object", "package.json", `{"version":{"major":1},"dependencies":{"core":"^1.0.0"}}`},
			{"trailing garbage", "package.json", `{"name":"acme","dependencies":{"core":"^1.0.0"}} oops`},
			{"o3de top level", "gem.json", `["acme"]`},
			{"o3de truncated", "gem.json", `{"gem_name":"Acme","dependencies":[`},
			{"uplugin top level", "Acme.uplugin", `["acme"]`},
			{"uproject truncated", "Acme.uproject", `{"Plugins":[{"Name":`},
			{"unity packages", "Packages/manifest.json", `{"dependencies":{"com.acme.core":`},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				assertRefusal(t, tc.rel, tc.body, "2.0.0", []writer.Edit{
					{Name: "core", Kind: manifest.KindDependencies, Range: "^2.0.0"},
				})
			})
		}
	})
}

func TestPublicAPIWriterRewritesXMLManifests(t *testing.T) {
	t.Run("pom.xml writes its own version and never the parent's", func(t *testing.T) {
		res, out, _ := rewriteFixture(t, "pom.xml", `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <parent>
    <groupId>com.acme</groupId>
    <artifactId>acme-parent</artifactId>
    <version>9.9.9</version>
  </parent>
  <groupId>com.acme</groupId>
  <artifactId>acme-api</artifactId>
  <version>1.0.0</version>
  <properties>
    <acme.version>1.0.0</acme.version>
  </properties>
  <dependencies>
    <dependency>
      <groupId>com.acme</groupId>
      <artifactId>acme-core</artifactId>
      <version>${acme.version}</version>
    </dependency>
    <dependency>
      <groupId>org.junit.jupiter</groupId>
      <artifactId>junit-jupiter</artifactId>
      <version>5.10.2</version>
      <scope>test</scope>
      <exclusions>
        <exclusion>
          <groupId>org.opentest4j</groupId>
          <artifactId>opentest4j</artifactId>
        </exclusion>
      </exclusions>
    </dependency>
    <dependency>
      <groupId>com.acme</groupId>
      <artifactId>acme-bom</artifactId>
    </dependency>
    <dependency>
      <groupId>com.acme</groupId>
      <artifactId>acme-empty</artifactId>
      <version/>
    </dependency>
    <dependency>
      <groupId>com.acme</groupId>
      <artifactId>acme-marked</artifactId>
      <version>1.0.0<qualifier/></version>
    </dependency>
    <dependency>
      <groupId>com.acme</groupId>
    </dependency>
    <dependency>
      <artifactId>groupless</artifactId>
      <version>1.0.0</version>
    </dependency>
  </dependencies>
</project>
`, "2.0.0", []writer.Edit{
			{Name: "com.acme:acme-core", Range: "2.0.0"},
			{Name: "org.junit.jupiter:junit-jupiter", Kind: manifest.KindDevDependencies, Range: "5.11.0"},
			{Name: "com.acme:acme-bom", Range: "2.0.0"},
			{Name: "com.acme:acme-empty", Range: "2.0.0"},
			{Name: "com.acme:acme-marked", Range: "2.0.0"},
			{Name: "groupless", Range: "2.0.0"},
			{Name: "com.acme:absent", Range: "2.0.0"},
		})
		assertOutcome(t, res,
			"applied=devDependencies:org.junit.jupiter:junit-jupiter,dependencies:groupless "+
				"missing=dependencies:com.acme:absent "+
				"skipped=dependencies:com.acme:acme-core,dependencies:com.acme:acme-bom,"+
				"dependencies:com.acme:acme-empty,dependencies:com.acme:acme-marked")
		if !res.VersionWritten {
			t.Error("the project version was not written")
		}
		if !strings.Contains(out, "<version>9.9.9</version>") {
			t.Error("the parent version was rewritten")
		}
		if !strings.Contains(out, "<artifactId>acme-api</artifactId>\n  <version>2.0.0</version>") {
			t.Errorf("rewritten pom:\n%s", out)
		}
		if !strings.Contains(out, "<version>${acme.version}</version>") {
			t.Error("a property reference was overwritten")
		}
		if !strings.Contains(out, "<version>5.11.0</version>") {
			t.Errorf("the test dependency was not written:\n%s", out)
		}
	})

	t.Run("csproj writes both spellings of a package version", func(t *testing.T) {
		res, out, _ := rewriteFixture(t, "Acme.Api.csproj", `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup Condition="'$(Configuration)'=='Debug'">
    <Version>$(VersionPrefix)</Version>
  </PropertyGroup>
  <PropertyGroup>
    <PackageId>Acme.Api</PackageId>
    <Version>1.0.0</Version>
  </PropertyGroup>
  <ItemGroup>
    <PackageReference Include="Serilog" Version="3.1.1" />
    <PackageReference Include="Newtonsoft.Json">
      <Version>13.0.3</Version>
    </PackageReference>
    <PackageReference Include="Acme.Deferred" Version="$(AcmeVersion)" />
    <PackageReference Include="Acme.Central" />
    <PackageReference Include="Acme.Marked">
      <Version>1.0.0<qualifier/></Version>
    </PackageReference>
    <ProjectReference Include="..\Acme.Core\Acme.Core.csproj" />
  </ItemGroup>
</Project>
`, "2.0.0", []writer.Edit{
			{Name: "Serilog", Range: "3.2.0"},
			{Name: "Newtonsoft.Json", Range: "13.1.0"},
			{Name: "Acme.Deferred", Range: "2.0.0"},
			{Name: "Acme.Central", Range: "2.0.0"},
			{Name: "Acme.Marked", Range: "2.0.0"},
			{Name: "Acme.Core", Range: "2.0.0"},
			{Name: "Acme.Dev", Kind: manifest.KindDevDependencies, Range: "2.0.0"},
		})
		assertOutcome(t, res,
			"applied=dependencies:Serilog,dependencies:Newtonsoft.Json "+
				"missing=dependencies:Acme.Core,devDependencies:Acme.Dev "+
				"skipped=dependencies:Acme.Deferred,dependencies:Acme.Central,dependencies:Acme.Marked")
		if !res.VersionWritten {
			t.Error("the project version was not written")
		}
		if !strings.Contains(out, "<Version>$(VersionPrefix)</Version>") {
			t.Error("an MSBuild property reference was overwritten")
		}
		if !strings.Contains(out, `Version="3.2.0"`) || !strings.Contains(out, "<Version>13.1.0</Version>") {
			t.Errorf("rewritten csproj:\n%s", out)
		}
		if !strings.Contains(out, "<PackageId>Acme.Api</PackageId>\n    <Version>2.0.0</Version>") {
			t.Errorf("the project version landed in the wrong group:\n%s", out)
		}
	})

	t.Run("nuspec and the flat nuget lists", func(t *testing.T) {
		res, out, _ := rewriteFixture(t, "Acme.nuspec", `<?xml version="1.0" encoding="utf-8"?>
<package>
  <metadata>
    <id>Acme</id>
    <version>1.0.0</version>
    <dependencies>
      <dependency id="Serilog" version="3.1.1" />
      <dependency id="Acme.Deferred" version="$version$" />
      <dependency id="Acme.Bare" />
      <group targetFramework="net8.0">
        <dependency id="Acme.Core" version="1.0.0" />
      </group>
    </dependencies>
  </metadata>
</package>
`, "2.0.0", []writer.Edit{
			{Name: "Serilog", Range: "3.2.0"},
			{Name: "Acme.Core", Range: "2.0.0"},
			{Name: "Acme.Deferred", Range: "2.0.0"},
			{Name: "Acme.Bare", Range: "2.0.0"},
			{Name: "Acme.Absent", Range: "2.0.0"},
			{Name: "Acme.Dev", Kind: manifest.KindDevDependencies, Range: "2.0.0"},
		})
		assertOutcome(t, res,
			"applied=dependencies:Serilog,dependencies:Acme.Core "+
				"missing=dependencies:Acme.Absent,devDependencies:Acme.Dev "+
				"skipped=dependencies:Acme.Deferred,dependencies:Acme.Bare")
		if !res.VersionWritten || !strings.Contains(out, "<version>2.0.0</version>") {
			t.Errorf("rewritten nuspec:\n%s", out)
		}
		if !strings.Contains(out, `id="Acme.Deferred" version="$version$"`) {
			t.Error("a pack-time token was overwritten")
		}

		props, propsOut, _ := rewriteFixture(t, "Directory.Packages.props", `<Project>
  <ItemGroup>
    <PackageVersion Include="Serilog" Version="3.1.1" />
    <PackageVersion Include="Acme.Deferred" Version="$(AcmeVersion)" />
    <PackageVersion Include="Acme.Bare" />
  </ItemGroup>
</Project>
`, "9.9.9", []writer.Edit{
			{Name: "Serilog", Range: "3.2.0"},
			{Name: "Acme.Deferred", Range: "2.0.0"},
			{Name: "Acme.Bare", Range: "2.0.0"},
			{Name: "Acme.Absent", Range: "2.0.0"},
		})
		assertOutcome(t, props,
			"applied=dependencies:Serilog missing=dependencies:Acme.Absent "+
				"skipped=dependencies:Acme.Deferred,dependencies:Acme.Bare")
		if props.VersionWritten {
			t.Error("a flat package list has no version of its own")
		}
		if !strings.Contains(propsOut, `Include="Serilog" Version="3.2.0"`) {
			t.Errorf("rewritten props:\n%s", propsOut)
		}

		config, configOut, _ := rewriteFixture(t, "packages.config", `<?xml version="1.0" encoding="utf-8"?>
<packages>
  <package id="Serilog" version="3.1.1" targetFramework="net48" />
  <package id="StyleCop.Analyzers" version="1.1.118" developmentDependency="true" />
</packages>
`, "", []writer.Edit{
			{Name: "Serilog", Range: "3.2.0"},
			{Name: "StyleCop.Analyzers", Range: "1.2.0"},
		})
		assertOutcome(t, config, "applied=dependencies:Serilog,dependencies:StyleCop.Analyzers missing= skipped=")
		if !strings.Contains(configOut, `version="3.2.0"`) || !strings.Contains(configOut, `version="1.2.0"`) {
			t.Errorf("rewritten packages.config:\n%s", configOut)
		}
	})

	t.Run("plist and android identity", func(t *testing.T) {
		res, out, _ := rewriteFixture(t, "Info.plist", `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
	<key>CFBundleIdentifier</key>
	<string>com.acme.app</string>
	<key>CFBundleShortVersionString</key>
	<string>1.0.0</string>
	<key>CFBundleVersion</key>
	<string>7</string>
	<key>LSRequiresIPhoneOS</key>
	<true/>
	<key>CFBundleURLTypes</key>
	<array>
		<dict>
			<key>CFBundleShortVersionString</key>
			<string>nested-must-not-move</string>
		</dict>
	</array>
</dict>
</plist>
`, "2.0.0", nil)
		if !res.VersionWritten {
			t.Error("the plist version was not written")
		}
		if !strings.Contains(out, "<string>2.0.0</string>") || !strings.Contains(out, "nested-must-not-move") {
			t.Errorf("rewritten plist:\n%s", out)
		}
		if !strings.Contains(out, "<key>CFBundleVersion</key>\n\t<string>7</string>") {
			t.Error("the build counter moved during a version write")
		}

		android, androidOut, _ := rewriteFixture(t, "AndroidManifest.xml",
			`<?xml version="1.0" encoding="utf-8"?>
<manifest xmlns:android="http://schemas.android.com/apk/res/android"
    package="com.acme.app"
    android:versionName="1.0.0"
    android:versionCode="7">
  <application android:label="Acme" />
</manifest>
`, "2.0.0", nil)
		if !android.VersionWritten || !strings.Contains(androidOut, `android:versionName="2.0.0"`) {
			t.Errorf("rewritten android manifest:\n%s", androidOut)
		}
		if !strings.Contains(androidOut, `android:versionCode="7"`) {
			t.Error("the build counter moved during a version write")
		}
	})

	t.Run("a deferred plist version is left alone", func(t *testing.T) {
		res, out, _ := rewriteFixture(t, "Info.plist", `<plist version="1.0">
<dict>
	<key>CFBundleShortVersionString</key>
	<string>$(MARKETING_VERSION)</string>
</dict>
</plist>
`, "2.0.0", nil)
		if res.VersionWritten || !strings.Contains(out, "$(MARKETING_VERSION)") {
			t.Errorf("a build-setting reference was overwritten:\n%s", out)
		}
	})

	t.Run("malformed xml is refused", func(t *testing.T) {
		cases := []struct{ name, rel, body string }{
			{"pom", "pom.xml", "<project><version>1.0.0</project>"},
			{"pom dependency", "pom.xml", "<project><dependencies><dependency><groupId><x></groupId></dependency></dependencies></project>"},
			{"pom artifact", "pom.xml", "<project><dependencies><dependency><artifactId><x></artifactId></dependency></dependencies></project>"},
			{"pom version text", "pom.xml", "<project><version>1.0<x></version></project>"},
			{"pom skip", "pom.xml", "<project><dependencies><dependency><scope><x></scope></dependency></dependencies></project>"},
			{"csproj", "Acme.csproj", "<Project><ItemGroup></Project>"},
			{"csproj version", "Acme.csproj", "<Project><PropertyGroup><Version>1.0<x></Version></PropertyGroup></Project>"},
			{"csproj reference", "Acme.csproj", `<Project><ItemGroup><PackageReference Include="core"><Version>1.0<x></Version></PackageReference></ItemGroup></Project>`},
			{"nuspec", "Acme.nuspec", "<package><metadata></package>"},
			{"props", "Directory.Packages.props", "<Project><ItemGroup></Project>"},
			{"packages config", "packages.config", "<packages><package></packages>"},
			{"plist", "Info.plist", "<plist><dict><key>a</key></plist>"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				assertRefusal(t, tc.rel, tc.body, "2.0.0", []writer.Edit{{Name: "core", Range: "2.0.0"}})
			})
		}
	})
}

func TestPublicAPIWriterRewritesTOMLManifests(t *testing.T) {
	t.Run("cargo", func(t *testing.T) {
		res, out, _ := rewriteFixture(t, "Cargo.toml", `# Acme application crate.
[package]
name = "acme-app"
version = "1.0.0"
edition = "2021"

[dependencies]
serde = "1.0"
serde_json = { version = "1.0", features = ["preserve_order"] }
acme-core = { path = "../core", version = "1.0" }
acme-workspace = { workspace = true }
renamed = { package = "real-crate", version = "2.0" }

[dev-dependencies]
criterion = "0.5"

[build-dependencies]
cc = "1.0"
`, "2.0.0", []writer.Edit{
			{Name: "serde", Range: "1.1"},
			{Name: "serde_json", Range: "1.1"},
			{Name: "acme-core", Range: "1.1"},
			{Name: "acme-workspace", Range: "1.1"},
			{Name: "real-crate", Range: "2.1"},
			{Name: "cc", Range: "1.1"},
			{Name: "criterion", Kind: manifest.KindDevDependencies, Range: "0.6"},
			{Name: "absent", Range: "1.0"},
		})
		if !res.VersionWritten {
			t.Error("the crate version was not written")
		}
		if !strings.Contains(out, "# Acme application crate.") {
			t.Error("the leading comment was lost")
		}
		for _, want := range []string{`version = "2.0.0"`, `serde = "1.1"`, `criterion = "0.6"`, `cc = "1.1"`} {
			if !strings.Contains(out, want) {
				t.Errorf("rewritten Cargo.toml lacks %s:\n%s", want, out)
			}
		}
		if !strings.Contains(out, `acme-workspace = { workspace = true }`) {
			t.Error("a workspace-inherited dependency was overwritten")
		}
	})

	t.Run("pyproject in both dialects", func(t *testing.T) {
		res, out, _ := rewriteFixture(t, "pyproject.toml", `[build-system]
requires = ["hatchling"]

[project]
name = "acme-core"
version = "1.0.0"
dependencies = [
    "requests[socks]>=2.0,<3; python_version > '3.8'",
    "acme-io @ file:../io",
    'tomli >= 2.0',
]

[project.optional-dependencies]
cli = ["click>=8.0"]

[dependency-groups]
dev = ["pytest>=8.0"]
`, "2.0.0", []writer.Edit{
			{Name: "requests", Range: ">=3.0"},
			{Name: "tomli", Range: ">=2.1"},
			{Name: "click", Kind: manifest.KindOptionalDependencies, Range: ">=8.1"},
			{Name: "pytest", Kind: manifest.KindDevDependencies, Range: ">=8.1"},
			{Name: "absent", Range: ">=1.0"},
		})
		if !res.VersionWritten || !strings.Contains(out, `version = "2.0.0"`) {
			t.Errorf("rewritten pyproject:\n%s", out)
		}
		if len(res.Applied) == 0 {
			t.Fatalf("no python dependency was written:\n%s", out)
		}
		if !strings.Contains(out, `requires = ["hatchling"]`) {
			t.Error("the build system table was disturbed")
		}

		poetry, poetryOut, _ := rewriteFixture(t, "pyproject.toml", `[tool.poetry]
name = "acme-app"
version = "1.0.0"

[tool.poetry.dependencies]
python = "^3.11"
requests = "^2.31"
acme-core = { path = "../core", develop = true }
acme-db = { version = "^1.0", optional = true }

[tool.poetry.dev-dependencies]
pytest = "^8.0"

[tool.poetry.group.docs.dependencies]
sphinx = "^7.0"
`, "2.0.0", []writer.Edit{
			{Name: "requests", Range: "^2.32"},
			{Name: "acme-db", Range: "^1.1"},
			{Name: "acme-core", Range: "^1.1"},
			{Name: "pytest", Kind: manifest.KindDevDependencies, Range: "^8.1"},
			{Name: "sphinx", Kind: manifest.KindDevDependencies, Range: "^7.1"},
		})
		if !poetry.VersionWritten {
			t.Error("the poetry version was not written")
		}
		for _, want := range []string{`requests = "^2.32"`, `version = "^1.1"`, `pytest = "^8.1"`, `sphinx = "^7.1"`} {
			if !strings.Contains(poetryOut, want) {
				t.Errorf("rewritten poetry manifest lacks %s:\n%s", want, poetryOut)
			}
		}
		if !strings.Contains(poetryOut, `python = "^3.11"`) {
			t.Error("the python constraint was rewritten as a dependency")
		}
	})

	t.Run("gradle version catalog", func(t *testing.T) {
		res, out, _ := rewriteFixture(t, "gradle/libs.versions.toml", `[versions]
kotlin = "1.9.24"
okhttp = "4.12.0"
shared = "1.0.0"

[libraries]
kotlin-stdlib = { module = "org.jetbrains.kotlin:kotlin-stdlib", version.ref = "kotlin" }
okhttp = { group = "com.squareup.okhttp3", name = "okhttp", version.ref = "okhttp" }
shorthand = "androidx.core:core-ktx:1.12.0"
inline = { module = "com.acme:inline", version = "1.0.0" }
shared-a = { module = "com.acme:shared-a", version.ref = "shared" }
shared-b = { module = "com.acme:shared-b", version.ref = "shared" }

[plugins]
android = { id = "com.android.application", version = "8.5.0" }
`, "", []writer.Edit{
			{Name: "org.jetbrains.kotlin:kotlin-stdlib", Range: "2.0.0"},
			{Name: "androidx.core:core-ktx", Range: "1.13.0"},
			{Name: "com.acme:inline", Range: "1.1.0"},
			{Name: "com.acme:shared-a", Range: "1.1.0"},
			{Name: "com.acme:shared-b", Range: "1.1.0"},
			{Name: "com.acme:absent", Range: "1.0.0"},
			{Name: "com.acme:dev", Kind: manifest.KindDevDependencies, Range: "1.0.0"},
		})
		if len(res.Missing) != 2 {
			t.Errorf("catalog outcome = %s", rewriteOutcome(res))
		}
		for _, want := range []string{`kotlin = "2.0.0"`, `"androidx.core:core-ktx:1.13.0"`,
			`version = "1.1.0"`, `shared = "1.1.0"`} {
			if !strings.Contains(out, want) {
				t.Errorf("rewritten catalog lacks %s:\n%s", want, out)
			}
		}
		if !strings.Contains(out, `version = "8.5.0"`) {
			t.Error("a plugin version was rewritten")
		}
	})

	t.Run("malformed toml is refused", func(t *testing.T) {
		cases := []struct{ name, rel, body string }{
			{"cargo", "Cargo.toml", "[package\nname = \"acme\"\n"},
			{"pyproject", "pyproject.toml", "[project\n"},
			{"catalog", "gradle/libs.versions.toml", "[versions\n"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				assertRefusal(t, tc.rel, tc.body, "2.0.0", []writer.Edit{{Name: "core", Range: "2.0.0"}})
			})
		}
	})
}

func TestPublicAPIWriterRewritesLineManifests(t *testing.T) {
	t.Run("requirements", func(t *testing.T) {
		res, out, _ := rewriteFixture(t, "requirements.txt", `# Runtime requirements.
--index-url https://pypi.example.com/simple
acme-core==1.0.0
requests>=2.0  # the HTTP client
flask ==3.0.0
acme-io @ file:../io
-e ./packages/local
`, "9.9.9", []writer.Edit{
			{Name: "acme-core", Range: "==2.0.0"},
			{Name: "requests", Range: ">=3.0"},
			{Name: "flask", Range: "==4.0.0"},
			{Name: "absent", Range: "==1.0.0"},
		})
		if res.VersionWritten {
			t.Error("a requirements file has no version of its own")
		}
		if len(res.Applied) != 3 || len(res.Missing) != 1 {
			t.Errorf("requirements outcome = %s", rewriteOutcome(res))
		}
		for _, want := range []string{"acme-core==2.0.0", "requests>=3.0  # the HTTP client",
			"flask==4.0.0", "# Runtime requirements.", "--index-url https://pypi.example.com/simple"} {
			if !strings.Contains(out, want) {
				t.Errorf("rewritten requirements lack %q:\n%s", want, out)
			}
		}
	})

	t.Run("podfile and gemfile", func(t *testing.T) {
		podfile, podOut, _ := rewriteFixture(t, "Podfile", `platform :ios, '15.0'

target 'Acme' do
  pod 'Alamofire', '~> 5.8'
  pod 'AcmeCore', :path => '../core'
  pod 'AcmeNet', :git => 'https://example.com/net.git'
  target 'AcmeTests' do
    pod 'Quick', '~> 7.0'
  end
end
`, "", []writer.Edit{
			{Name: "Alamofire", Range: "~> 5.9"},
			{Name: "Quick", Range: "~> 7.1"},
			{Name: "AcmeNet", Range: "~> 1.0"},
			{Name: "Absent", Range: "~> 1.0"},
		})
		if len(podfile.Applied) != 2 {
			t.Errorf("podfile outcome = %s", rewriteOutcome(podfile))
		}
		if !strings.Contains(podOut, "pod 'Alamofire', '~> 5.9'") || !strings.Contains(podOut, "pod 'Quick', '~> 7.1'") {
			t.Errorf("rewritten Podfile:\n%s", podOut)
		}
		if !strings.Contains(podOut, "pod 'AcmeCore', :path => '../core'") {
			t.Error("a local pod was rewritten")
		}

		gemfile, gemOut, _ := rewriteFixture(t, "Gemfile", `source 'https://rubygems.org'

gem 'rails', '~> 7.1'
gem 'pg', '>= 0.18', '< 2.0'
gem 'acme-core', path: '../core'

group :development, :test do
  gem 'rspec-rails', '~> 6.1'
end
`, "", []writer.Edit{
			{Name: "rails", Range: "~> 7.2"},
			{Name: "rspec-rails", Range: "~> 6.2"},
			{Name: "absent", Range: "~> 1.0"},
		})
		if len(gemfile.Applied) != 2 || len(gemfile.Missing) != 1 {
			t.Errorf("gemfile outcome = %s", rewriteOutcome(gemfile))
		}
		if !strings.Contains(gemOut, "gem 'rails', '~> 7.2'") {
			t.Errorf("rewritten Gemfile:\n%s", gemOut)
		}
	})

	t.Run("podspec and gemspec carry their own version", func(t *testing.T) {
		podspec, podOut, _ := rewriteFixture(t, "AcmeKit.podspec", `Pod::Spec.new do |s|
  s.name              = 'AcmeKit'
  s.version           = '1.0.0'
  s.dependency 'Alamofire', '~> 5.8'
  s.ios.dependency 'AcmeUI'
end
`, "2.0.0", []writer.Edit{
			{Name: "Alamofire", Range: "~> 5.9"},
			{Name: "AcmeUI", Range: "~> 1.0"},
		})
		if !podspec.VersionWritten || !strings.Contains(podOut, "s.version           = '2.0.0'") {
			t.Errorf("rewritten podspec:\n%s", podOut)
		}

		gemspec, gemOut, _ := rewriteFixture(t, "acme.gemspec", `Gem::Specification.new do |spec|
  spec.name          = 'acme'
  spec.version       = '1.0.0'
  spec.add_dependency 'rack', '>= 2.0', '< 4.0'
  spec.add_development_dependency 'rspec', '~> 3.13'
end
`, "2.0.0", []writer.Edit{
			{Name: "rspec", Kind: manifest.KindDevDependencies, Range: "~> 3.14"},
		})
		if !gemspec.VersionWritten || !strings.Contains(gemOut, "spec.version       = '2.0.0'") {
			t.Errorf("rewritten gemspec:\n%s", gemOut)
		}
	})

	t.Run("a ruby manifest refuses a version that could end a literal", func(t *testing.T) {
		assertRefusal(t, "AcmeKit.podspec", `Pod::Spec.new do |s|
  s.version = '1.0.0'
end
`, "2.0.0'; system('rm -rf /')", nil)
		assertRefusal(t, "Podfile", "pod 'Alamofire', '~> 5.8'\n", "", []writer.Edit{
			{Name: "Alamofire", Range: "~> 5.9'; evil"},
		})
	})
}

func TestPublicAPIWriterRewritesYAMLManifests(t *testing.T) {
	t.Run("pubspec", func(t *testing.T) {
		res, out, _ := rewriteFixture(t, "pubspec.yaml", `name: acme_app
version: 1.2.3+45

environment:
  sdk: '>=3.0.0 <4.0.0'

dependencies:
  flutter:
    sdk: flutter
  acme_core: ^1.0.0
  acme_net:
    version: ^2.0.0
    hosted: https://pub.example.com

dev_dependencies:
  mocktail: ^1.0.0

dependency_overrides:
  acme_core:
    path: ../core
`, "2.0.0", []writer.Edit{
			{Name: "acme_core", Range: "^2.0.0"},
			{Name: "flutter", Range: "^1.0.0"},
			{Name: "acme_net", Range: "^3.0.0"},
			{Name: "mocktail", Kind: manifest.KindDevDependencies, Range: "^1.1.0"},
			{Name: "absent", Range: "^1.0.0"},
			{Name: "acme_peer", Kind: manifest.KindPeerDependencies, Range: "^1.0.0"},
		})
		if !res.VersionWritten {
			t.Error("the pubspec version was not written")
		}
		if !strings.Contains(out, "version: 2.0.0+45") {
			t.Errorf("the build counter was lost from the version:\n%s", out)
		}
		if !strings.Contains(out, "acme_core: ^2.0.0") || !strings.Contains(out, "mocktail: ^1.1.0") {
			t.Errorf("rewritten pubspec:\n%s", out)
		}
		if !strings.Contains(out, "path: ../core") {
			t.Error("an override path was rewritten as a version")
		}
		if len(res.Skipped) != 2 || len(res.Missing) != 2 {
			t.Errorf("pubspec outcome = %s", rewriteOutcome(res))
		}
	})

	t.Run("a pubspec refuses a version a YAML scalar cannot hold", func(t *testing.T) {
		assertRefusal(t, "pubspec.yaml", "name: acme_app\nversion: 1.0.0\n", "2.0.0: bad", nil)
		assertRefusal(t, "pubspec.yaml", "name: acme_app\ndependencies:\n  core: ^1.0.0\n", "",
			[]writer.Edit{{Name: "core", Range: "^2.0.0 # comment"}})
	})

	t.Run("aqua", func(t *testing.T) {
		res, out, _ := rewriteFixture(t, "aqua.yaml", `---
registries:
  - type: standard
    ref: v4.200.0

packages:
  - name: cli/cli@v2.55.0
  - name: junegunn/fzf
    version: v0.54.0
  - name: golang/go
    go_version_file: .go-version
`, "", []writer.Edit{
			{Name: "cli/cli", Range: "v2.60.0"},
			{Name: "junegunn/fzf", Range: "v0.55.0"},
			{Name: "golang/go", Range: "v1.24.0"},
			{Name: "absent/tool", Range: "v1.0.0"},
		})
		if len(res.Applied) != 2 {
			t.Errorf("aqua outcome = %s", rewriteOutcome(res))
		}
		if !strings.Contains(out, "cli/cli@v2.60.0") || !strings.Contains(out, "version: v0.55.0") {
			t.Errorf("rewritten aqua configuration:\n%s", out)
		}
		if !strings.Contains(out, "ref: v4.200.0") {
			t.Error("the registry pin was rewritten")
		}
	})

	t.Run("compose", func(t *testing.T) {
		res, out, _ := rewriteFixture(t, "compose.yaml", `services:
  api:
    build:
      context: .
      tags:
        - ghcr.io/acme/api:1.0.0
        - ghcr.io/acme/api:latest
    image: ghcr.io/acme/api:1.0.0
    ports:
      - "8080:80"
    environment:
      REDIS_URL: "redis:6379"
  cache:
    image: redis:7.2
  pinned:
    image: gcr.io/distroless/static@sha256:abc
  templated:
    image: ${BASE}:${TAG}
`, "2.0.0", []writer.Edit{
			{Name: "redis", Range: "7.4"},
			{Name: "gcr.io/distroless/static", Range: "2.0.0"},
			{Name: "absent", Range: "1.0.0"},
		})
		if !strings.Contains(out, "ghcr.io/acme/api:2.0.0") {
			t.Errorf("the file's own image was not rewritten:\n%s", out)
		}
		if !strings.Contains(out, "image: redis:7.4") {
			t.Errorf("a dependency image was not rewritten:\n%s", out)
		}
		if !strings.Contains(out, `- "8080:80"`) || !strings.Contains(out, `REDIS_URL: "redis:6379"`) {
			t.Error("a scalar that merely looks like an image reference was rewritten")
		}
		if !strings.Contains(out, "sha256:abc") || !strings.Contains(out, "${BASE}:${TAG}") {
			t.Error("a pinned or interpolated reference was rewritten")
		}
		if len(res.Missing) != 1 {
			t.Errorf("compose outcome = %s", rewriteOutcome(res))
		}
	})

	t.Run("compose with a flow-sequence tag list", func(t *testing.T) {
		_, out, _ := rewriteFixture(t, "docker-compose.yml", `services:
  api:
    build:
      context: .
      tags: ["ghcr.io/acme/api:1.0.0", 'ghcr.io/acme/api:stable', ghcr.io/acme/api:bare]
    image: ghcr.io/acme/api:1.0.0
`, "2.0.0", nil)
		if strings.Count(out, "ghcr.io/acme/api:2.0.0") < 2 {
			t.Errorf("flow-sequence tags were not rewritten:\n%s", out)
		}
	})

	t.Run("dockerfile", func(t *testing.T) {
		res, out, _ := rewriteFixture(t, "Dockerfile", `# syntax=docker/dockerfile:1.7
FROM ghcr.io/acme/base:1.0.0 AS build
COPY --from=ghcr.io/acme/tools:2.0.0 /usr/bin/tool /usr/bin/tool
RUN --mount=type=bind,from=ghcr.io/acme/assets:3.0.0,source=/a,target=/a make build

FROM gcr.io/distroless/static@sha256:abc AS runtime
COPY --from=build /out /out
`, "", []writer.Edit{
			{Name: "ghcr.io/acme/base", Range: "1.1.0"},
			{Name: "ghcr.io/acme/tools", Range: "2.1.0"},
			{Name: "ghcr.io/acme/assets", Range: "3.1.0"},
			{Name: "gcr.io/distroless/static", Range: "1.0.0"},
			{Name: "absent", Range: "1.0.0"},
		})
		if len(res.Applied) != 3 {
			t.Errorf("dockerfile outcome = %s", rewriteOutcome(res))
		}
		for _, want := range []string{"ghcr.io/acme/base:1.1.0", "--from=ghcr.io/acme/tools:2.1.0",
			"from=ghcr.io/acme/assets:3.1.0", "# syntax=docker/dockerfile:1.7"} {
			if !strings.Contains(out, want) {
				t.Errorf("rewritten Dockerfile lacks %q:\n%s", want, out)
			}
		}
		if !strings.Contains(out, "sha256:abc") {
			t.Error("a digest-pinned base was rewritten")
		}
	})

	t.Run("an image tag a registry would refuse is not written", func(t *testing.T) {
		assertRefusal(t, "Dockerfile", "FROM ghcr.io/acme/base:1.0.0\n", "",
			[]writer.Edit{{Name: "ghcr.io/acme/base", Range: "1.0.0+build"}})
		assertRefusal(t, "compose.yaml",
			"services:\n  api:\n    build: .\n    image: ghcr.io/acme/api:1.0.0\n  cache:\n    image: redis:7.2\n", "",
			[]writer.Edit{{Name: "redis", Range: "not a tag"}})
	})

	t.Run("malformed yaml is refused", func(t *testing.T) {
		if err := assertRefusal(t, "aqua.yaml", "packages: [unclosed\n", "",
			[]writer.Edit{{Name: "cli/cli", Range: "v1.0.0"}}); err == nil {
			t.Fatal("no error")
		}
	})
}

func TestPublicAPIWriterRewritesGradleBuildScripts(t *testing.T) {
	res, out, _ := rewriteFixture(t, "build.gradle", `/*
 * Acme Android application.
 */
buildscript {
    dependencies {
        classpath 'com.android.tools.build:gradle:8.5.0'
    }
}

android {
    namespace 'com.acme.app'
    defaultConfig {
        applicationId "com.acme.app"
        versionName '1.0.0' // the marketing version
        versionCode 7
    }
}

dependencies {
    implementation 'androidx.core:core-ktx:1.12.0'
    implementation("com.squareup.okhttp3:okhttp:4.12.0")
    api project(':core')
    testImplementation 'junit:junit:4.13.2'
    compileOnly 'javax.annotation:jsr250-api:1.0'
    implementation libs.retrofit
    implementation "com.acme:dynamic:$acmeVersion"
}
`, "2.0.0", []writer.Edit{
		{Name: "androidx.core:core-ktx", Range: "1.13.0"},
		{Name: "com.squareup.okhttp3:okhttp", Range: "4.13.0"},
		{Name: "junit:junit", Kind: manifest.KindDevDependencies, Range: "4.13.3"},
		{Name: "javax.annotation:jsr250-api", Kind: manifest.KindPeerDependencies, Range: "1.1"},
		{Name: "core", Range: "2.0.0"},
		{Name: "com.android.tools.build:gradle", Range: "8.6.0"},
		{Name: "com.acme:absent", Range: "1.0.0"},
	})
	if !res.VersionWritten || !strings.Contains(out, "versionName '2.0.0' // the marketing version") {
		t.Errorf("rewritten build script:\n%s", out)
	}
	if !strings.Contains(out, "versionCode 7") {
		t.Error("the build counter moved during a version write")
	}
	for _, want := range []string{"androidx.core:core-ktx:1.13.0", "com.squareup.okhttp3:okhttp:4.13.0",
		"junit:junit:4.13.3", "javax.annotation:jsr250-api:1.1"} {
		if !strings.Contains(out, want) {
			t.Errorf("rewritten build script lacks %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "classpath 'com.android.tools.build:gradle:8.5.0'") {
		t.Error("a buildscript classpath entry was rewritten")
	}
	if !strings.Contains(out, `"com.acme:dynamic:$acmeVersion"`) {
		t.Error("an interpolated coordinate was rewritten")
	}

	t.Run("a coordinate that could end a literal is refused", func(t *testing.T) {
		assertRefusal(t, "build.gradle", `dependencies {
    implementation 'androidx.core:core-ktx:1.12.0'
}
`, "", []writer.Edit{{Name: "androidx.core:core-ktx", Range: "1.13.0' + evil() + '"}})
	})
}

func TestPublicAPIWriterRewritesEngineManifests(t *testing.T) {
	t.Run("unity", func(t *testing.T) {
		packages, packagesOut, _ := rewriteFixture(t, "Packages/manifest.json", `{
  "dependencies": {
    "com.unity.render-pipelines.universal": "14.0.9",
    "com.acme.core": "file:../../packages/core"
  }
}
`, "9.9.9", []writer.Edit{
			{Name: "com.unity.render-pipelines.universal", Range: "14.1.0"},
			{Name: "com.acme.core", Range: "1.0.0"},
			{Name: "com.acme.absent", Range: "1.0.0"},
		})
		if packages.VersionWritten {
			t.Error("a unity package manifest has no version of its own")
		}
		if !strings.Contains(packagesOut, `"com.unity.render-pipelines.universal": "14.1.0"`) {
			t.Errorf("rewritten unity packages:\n%s", packagesOut)
		}

		settings, settingsOut, _ := rewriteFixture(t, "ProjectSettings/ProjectSettings.asset", `%YAML 1.1
%TAG !u! tag:unity3d.com,2011:
--- !u!129 &1
PlayerSettings:
  productName: Acme
  bundleVersion: 1.0.0 # marketing
  AndroidBundleVersionCode: 42
  buildNumber:
    Standalone: 7
`, "2.0.0", nil)
		if !settings.VersionWritten || !strings.Contains(settingsOut, "bundleVersion: 2.0.0 # marketing") {
			t.Errorf("rewritten unity settings:\n%s", settingsOut)
		}
		if !strings.Contains(settingsOut, "AndroidBundleVersionCode: 42") {
			t.Error("the build counter moved during a version write")
		}
	})

	t.Run("godot", func(t *testing.T) {
		project, projectOut, _ := rewriteFixture(t, "project.godot", `; Engine configuration file.
config_version=5

[application]

config/name="Acme"
config/version="1.0.0"
config/features=PackedStringArray("4.2")
`, "2.0.0", nil)
		if !project.VersionWritten || !strings.Contains(projectOut, `config/version="2.0.0"`) {
			t.Errorf("rewritten project.godot:\n%s", projectOut)
		}
		if !strings.Contains(projectOut, `PackedStringArray("4.2")`) {
			t.Error("a container value was rewritten")
		}

		plugin, pluginOut, _ := rewriteFixture(t, "addons/acme/plugin.cfg", `[plugin]

name="Acme Addon"
version="1.2.3"
`, "2.0.0", nil)
		if !plugin.VersionWritten || !strings.Contains(pluginOut, `version="2.0.0"`) {
			t.Errorf("rewritten plugin.cfg:\n%s", pluginOut)
		}

		presets, presetsOut, _ := rewriteFixture(t, "export_presets.cfg", `[preset.0]

name="Android"

[preset.0.options]

version/code=42
version/name="1.0.0"

[preset.1]

name="iOS"

[preset.1.options]

application/short_version="1.0.0"
application/version="1.0.0"
`, "2.0.0", nil)
		if !presets.VersionWritten {
			t.Error("the export presets version was not written")
		}
		if strings.Count(presetsOut, `"2.0.0"`) != 3 {
			t.Errorf("every preset must move together:\n%s", presetsOut)
		}
		if !strings.Contains(presetsOut, "version/code=42") {
			t.Error("the build counter moved during a version write")
		}
	})

	t.Run("unreal", func(t *testing.T) {
		project, _, _ := rewriteFixture(t, "Acme.uproject", `{
	"FileVersion": 3,
	"EngineAssociation": "5.4",
	"Plugins": [ { "Name": "AcmeNet", "Enabled": true } ]
}
`, "2.0.0", []writer.Edit{
			{Name: "AcmeNet", Range: "2.0.0"},
			{Name: "Absent", Range: "2.0.0"},
			{Name: "AcmeNet", Kind: manifest.KindDevDependencies, Range: "2.0.0"},
		})
		assertOutcome(t, project,
			"applied= missing=dependencies:Absent,devDependencies:AcmeNet skipped=dependencies:AcmeNet")
		if project.VersionWritten {
			t.Error("a uproject declares no version of its own")
		}

		plugin, pluginOut, _ := rewriteFixture(t, "AcmeNet.uplugin", `{
	"FileVersion": 3,
	"Version": 7,
	"VersionName": "1.0.0",
	"Plugins": [ { "Name": "Networking" } ]
}
`, "2.0.0", []writer.Edit{{Name: "Networking", Range: "1.0.0"}})
		if !plugin.VersionWritten || !strings.Contains(pluginOut, `"VersionName": "2.0.0"`) {
			t.Errorf("rewritten uplugin:\n%s", pluginOut)
		}
		if !strings.Contains(pluginOut, `"Version": 7`) {
			t.Error("the build counter moved during a version write")
		}
		if len(plugin.Skipped) != 1 {
			t.Errorf("uplugin outcome = %s", rewriteOutcome(plugin))
		}

		game, gameOut, _ := rewriteFixture(t, "Config/DefaultGame.ini", `[/Script/EngineSettings.GeneralProjectSettings]
ProjectName=Acme
ProjectVersion=1.0.0 ; the marketing version
`, "2.0.0", nil)
		if !game.VersionWritten || !strings.Contains(gameOut, "ProjectVersion=2.0.0 ; the marketing version") {
			t.Errorf("rewritten DefaultGame.ini:\n%s", gameOut)
		}

		engine, engineOut, _ := rewriteFixture(t, "Config/DefaultEngine.ini", `[/Script/AndroidRuntimeSettings.AndroidRuntimeSettings]
StoreVersion=42
VersionDisplayName=1.0.0
`, "2.0.0", nil)
		if !engine.VersionWritten || !strings.Contains(engineOut, "VersionDisplayName=2.0.0") {
			t.Errorf("rewritten DefaultEngine.ini:\n%s", engineOut)
		}
		if !strings.Contains(engineOut, "StoreVersion=42") {
			t.Error("the build counter moved during a version write")
		}
	})

	t.Run("defold and o3de", func(t *testing.T) {
		defold, defoldOut, _ := rewriteFixture(t, "game.project", `[project]
title = Acme
version = 1.0.0
dependencies#0 = https://example.com/dep.zip
`, "2.0.0", nil)
		if !defold.VersionWritten || !strings.Contains(defoldOut, "version = 2.0.0") {
			t.Errorf("rewritten game.project:\n%s", defoldOut)
		}

		gem, gemOut, _ := rewriteFixture(t, "gem.json", `{
    "gem_name": "AcmeGem",
    "version": "1.0.0",
    "dependencies": [
        "Atom_RHI==1.0.0",
        "Camera"
    ]
}
`, "2.0.0", []writer.Edit{
			{Name: "Atom_RHI", Range: "==2.0.0"},
			{Name: "Camera", Range: ">=1.0.0"},
			{Name: "Absent", Range: "==1.0.0"},
			{Name: "Atom_RHI", Kind: manifest.KindDevDependencies, Range: "==2.0.0"},
		})
		if !gem.VersionWritten {
			t.Error("the gem version was not written")
		}
		if !strings.Contains(gemOut, `"Atom_RHI==2.0.0"`) || !strings.Contains(gemOut, `"Camera\u003e=1.0.0"`) {
			t.Errorf("rewritten gem.json:\n%s", gemOut)
		}
		if len(gem.Missing) != 2 {
			t.Errorf("o3de outcome = %s", rewriteOutcome(gem))
		}

		unnamed, unnamedOut, _ := rewriteFixture(t, "project.json", `{"someOtherTool": true, "version": "1.0.0"}`,
			"2.0.0", []writer.Edit{{Name: "Atom", Range: "==2.0.0"}})
		if unnamed.VersionWritten || len(unnamed.Missing) != 1 {
			t.Errorf("an unnamed project.json was written: %s", rewriteOutcome(unnamed))
		}
		if !strings.Contains(unnamedOut, `"version": "1.0.0"`) {
			t.Errorf("an unnamed project.json was rewritten: %s", unnamedOut)
		}
	})

	t.Run("xcode", func(t *testing.T) {
		res, out, _ := rewriteFixture(t, "Acme.xcodeproj/project.pbxproj", `// !$*UTF8*$!
{
	objects = {
		1A2B /* Debug */ = {
			buildSettings = {
				CURRENT_PROJECT_VERSION = 42;
				MARKETING_VERSION = 1.0.0;
				PRODUCT_BUNDLE_IDENTIFIER = "com.acme.app";
			};
		};
		1A2C /* Release */ = {
			buildSettings = {
				MARKETING_VERSION = 1.0.0;
			};
		};
		1A2D /* Deferred */ = {
			buildSettings = {
				MARKETING_VERSION = "$(INHERITED)";
			};
		};
	};
}
`, "2.0.0", nil)
		if !res.VersionWritten {
			t.Error("the marketing version was not written")
		}
		if strings.Count(out, "MARKETING_VERSION = 2.0.0;") != 2 {
			t.Errorf("every configuration must move together:\n%s", out)
		}
		if !strings.Contains(out, `MARKETING_VERSION = "$(INHERITED)"`) {
			t.Error("an inherited build setting was overwritten")
		}
		if !strings.Contains(out, "CURRENT_PROJECT_VERSION = 42;") {
			t.Error("the build counter moved during a version write")
		}
	})
}

func TestPublicAPIWriterRewritesGoModules(t *testing.T) {
	res, out, _ := rewriteFixture(t, "go.mod", `module example.com/acme/app

go 1.26

require (
	example.com/acme/core v1.0.0
	example.com/acme/tools v0.1.0 // indirect
	golang.org/x/mod v0.29.0
)

replace example.com/acme/core => ../core
`, "9.9.9", []writer.Edit{
		{Name: "example.com/acme/core", Range: "v1.1.0"},
		{Name: "golang.org/x/mod", Range: "v0.30.0"},
		{Name: "example.com/acme/absent", Range: "v1.0.0"},
	})
	if res.VersionWritten {
		t.Error("a go.mod declares no version of its own")
	}
	if len(res.Applied) != 2 || len(res.Missing) != 1 {
		t.Errorf("go.mod outcome = %s", rewriteOutcome(res))
	}
	for _, want := range []string{"example.com/acme/core v1.1.0", "golang.org/x/mod v0.30.0",
		"replace example.com/acme/core => ../core", "// indirect"} {
		if !strings.Contains(out, want) {
			t.Errorf("rewritten go.mod lacks %q:\n%s", want, out)
		}
	}

	t.Run("an unparseable go.mod is refused", func(t *testing.T) {
		assertRefusal(t, "go.mod", "module example.com/x\n\nrequire (\n", "",
			[]writer.Edit{{Name: "example.com/core", Range: "v1.0.0"}})
	})
}

func TestPublicAPIWriterLifecycleAcrossEveryFormat(t *testing.T) {
	// The cross-component contract: what the writer puts in, the scanner reads
	// back, for every format that declares both a version and a dependency.
	cases := []struct {
		name, rel, body, dependency, next string
	}{
		{"npm", "package.json", `{"name":"acme","version":"1.0.0","dependencies":{"core":"^1.0.0"}}`, "core", "^2.0.0"},
		{"composer", "composer.json", `{"name":"acme/app","version":"1.0.0","require":{"acme/core":"^1.0"}}`, "acme/core", "^2.0"},
		{"cargo", "Cargo.toml", "[package]\nname = \"acme\"\nversion = \"1.0.0\"\n\n[dependencies]\ncore = \"1.0\"\n", "core", "2.0"},
		{"pyproject", "pyproject.toml", "[project]\nname = \"acme\"\nversion = \"1.0.0\"\ndependencies = [\"core>=1.0\"]\n", "core", ">=2.0"},
		{"maven", "pom.xml", `<project><groupId>acme</groupId><artifactId>app</artifactId><version>1.0.0</version><dependencies><dependency><groupId>acme</groupId><artifactId>core</artifactId><version>1.0.0</version></dependency></dependencies></project>`, "acme:core", "2.0.0"},
		{"pubspec", "pubspec.yaml", "name: acme\nversion: 1.0.0\ndependencies:\n  core: ^1.0.0\n", "core", "^2.0.0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := writeFile(t, dir, tc.rel, tc.body)
			res, err := writer.Rewrite(path, "2.0.0", []writer.Edit{
				{Name: tc.dependency, Kind: manifest.KindDependencies, Range: tc.next},
			})
			if err != nil || !res.VersionWritten || len(res.Applied) != 1 {
				t.Fatalf("rewrite = %+v, err = %v", res, err)
			}
			mans, err := scanner.ScanRoot(t.Context(), dir)
			if err != nil || len(mans) != 1 {
				t.Fatalf("rescan = %+v, err = %v", mans, err)
			}
			if mans[0].Version != "2.0.0" {
				t.Errorf("version after rewrite = %q", mans[0].Version)
			}
			found := false
			for _, d := range mans[0].Deps {
				if d.Name == tc.dependency && d.Range == tc.next {
					found = true
				}
			}
			if !found {
				t.Errorf("dependency after rewrite = %+v", mans[0].Deps)
			}
		})
	}
}
