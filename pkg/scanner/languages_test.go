package scanner

import (
	"context"
	"reflect"
	"testing"
)

// The parsers added after the first three ecosystems, each tested on one
// realistic manifest exercising its kind mapping and local-path signal.

func TestPythonPEP621AndGroups(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "pyproject.toml", `[project]
name = "Acme_Web.App"
version = "1.2.3"
dependencies = [
    "requests>=2.0,<3",
    "acme-core (>=1.0)",
    "acme_utils[cli]>=0.5 ; python_version > '3.10'",
    "local-lib @ file:../local-lib",
]

[project.optional-dependencies]
socks = ["PySocks>=1.5"]

[dependency-groups]
dev = ["pytest>=8", {include-group = "lint"}]
lint = ["ruff"]
`)
	m := scanOne(t, dir)
	want := Manifest{
		Path: "pyproject.toml", Ecosystem: EcosystemPython,
		Name: "acme-web-app", Version: "1.2.3", Root: true,
		Deps: []DeclaredDep{
			{Name: "acme-core", Range: ">=1.0", Kind: KindDependencies},
			{Name: "acme-utils", Range: ">=0.5", Kind: KindDependencies},
			{Name: "local-lib", Range: "@ file:../local-lib", Kind: KindDependencies, LocalPath: "../local-lib"},
			{Name: "requests", Range: ">=2.0,<3", Kind: KindDependencies},
			{Name: "pytest", Range: ">=8", Kind: KindDevDependencies},
			{Name: "ruff", Kind: KindDevDependencies},
			{Name: "pysocks", Range: ">=1.5", Kind: KindOptionalDependencies},
		},
	}
	if !reflect.DeepEqual(m, want) {
		t.Errorf("manifest mismatch:\n got %+v\nwant %+v", m, want)
	}
}

func TestPythonPoetry(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "pyproject.toml", `[tool.poetry]
name = "acme-svc"
version = "0.9.0"

[tool.poetry.dependencies]
python = "^3.11"
acme-core = { path = "../core", develop = true }
httpx = "^0.27"

[tool.poetry.group.dev.dependencies]
pytest = "^8.0"
`)
	m := scanOne(t, dir)
	want := Manifest{
		Path: "pyproject.toml", Ecosystem: EcosystemPython,
		Name: "acme-svc", Version: "0.9.0", Root: true,
		Deps: []DeclaredDep{
			{Name: "acme-core", Kind: KindDependencies, LocalPath: "../core"},
			{Name: "httpx", Range: "^0.27", Kind: KindDependencies},
			{Name: "pytest", Range: "^8.0", Kind: KindDevDependencies},
		},
	}
	if !reflect.DeepEqual(m, want) {
		t.Errorf("manifest mismatch:\n got %+v\nwant %+v", m, want)
	}
}

func TestComposer(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "composer.json", `{
		"name": "acme/web",
		"require": {
			"php": ">=8.2",
			"ext-json": "*",
			"acme/core": "^1.0",
			"monolog/monolog": "^3.0"
		},
		"require-dev": {"phpunit/phpunit": "^11"}
	}`)
	m := scanOne(t, dir)
	want := Manifest{
		Path: "composer.json", Ecosystem: EcosystemComposer,
		Name: "acme/web", Root: true,
		Deps: []DeclaredDep{
			{Name: "acme/core", Range: "^1.0", Kind: KindDependencies},
			{Name: "monolog/monolog", Range: "^3.0", Kind: KindDependencies},
			{Name: "phpunit/phpunit", Range: "^11", Kind: KindDevDependencies},
		},
	}
	if !reflect.DeepEqual(m, want) {
		t.Errorf("manifest mismatch:\n got %+v\nwant %+v", m, want)
	}
}

func TestMaven(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "pom.xml", `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <parent>
    <groupId>com.acme</groupId>
    <artifactId>parent</artifactId>
    <version>2.0.0</version>
  </parent>
  <artifactId>web</artifactId>
  <dependencies>
    <dependency>
      <groupId>com.acme</groupId>
      <artifactId>core</artifactId>
      <version>${project.version}</version>
    </dependency>
    <dependency>
      <groupId>org.junit.jupiter</groupId>
      <artifactId>junit-jupiter</artifactId>
      <version>5.10.0</version>
      <scope>test</scope>
    </dependency>
    <dependency>
      <groupId>com.acme</groupId>
      <artifactId>extras</artifactId>
      <version>1.0.0</version>
      <optional>true</optional>
    </dependency>
  </dependencies>
</project>
`)
	m := scanOne(t, dir)
	want := Manifest{
		Path: "pom.xml", Ecosystem: EcosystemMaven,
		Name: "com.acme:web", Version: "2.0.0", Root: true,
		Deps: []DeclaredDep{
			{Name: "com.acme:core", Range: "${project.version}", Kind: KindDependencies},
			{Name: "org.junit.jupiter:junit-jupiter", Range: "5.10.0", Kind: KindDevDependencies},
			{Name: "com.acme:extras", Range: "1.0.0", Kind: KindOptionalDependencies},
		},
	}
	if !reflect.DeepEqual(m, want) {
		t.Errorf("manifest mismatch:\n got %+v\nwant %+v", m, want)
	}
}

func TestCsproj(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "Acme.Web.csproj", `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <TargetFramework>net8.0</TargetFramework>
    <Version>1.4.0</Version>
  </PropertyGroup>
  <ItemGroup>
    <PackageReference Include="Newtonsoft.Json" Version="13.0.1" />
    <PackageReference Include="Serilog">
      <Version>3.1.1</Version>
    </PackageReference>
    <ProjectReference Include="..\Acme.Core\Acme.Core.csproj" />
  </ItemGroup>
</Project>
`)
	m := scanOne(t, dir)
	want := Manifest{
		Path: "Acme.Web.csproj", Ecosystem: EcosystemNuGet,
		Name: "Acme.Web", Version: "1.4.0", Root: true,
		Deps: []DeclaredDep{
			{Name: "Acme.Core", Kind: KindDependencies, LocalPath: "../Acme.Core/Acme.Core.csproj"},
			{Name: "Newtonsoft.Json", Range: "13.0.1", Kind: KindDependencies},
			{Name: "Serilog", Range: "3.1.1", Kind: KindDependencies},
		},
	}
	if !reflect.DeepEqual(m, want) {
		t.Errorf("manifest mismatch:\n got %+v\nwant %+v", m, want)
	}
}

func TestPubspec(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "pubspec.yaml", `name: acme_app
version: 2.1.0
dependencies:
  flutter:
    sdk: flutter
  acme_core:
    path: ../core
  http: ^1.2.0
  args:
dev_dependencies:
  test: ^1.25.0
dependency_overrides:
  acme_utils:
    path: ../utils
`)
	m := scanOne(t, dir)
	want := Manifest{
		Path: "pubspec.yaml", Ecosystem: EcosystemPub,
		Name: "acme_app", Version: "2.1.0", Root: true,
		Deps: []DeclaredDep{
			{Name: "acme_core", Kind: KindDependencies, LocalPath: "../core"},
			{Name: "acme_utils", Kind: KindDependencies, LocalPath: "../utils"},
			{Name: "args", Kind: KindDependencies},
			{Name: "flutter", Kind: KindDependencies},
			{Name: "http", Range: "^1.2.0", Kind: KindDependencies},
			{Name: "test", Range: "^1.25.0", Kind: KindDevDependencies},
		},
	}
	if !reflect.DeepEqual(m, want) {
		t.Errorf("manifest mismatch:\n got %+v\nwant %+v", m, want)
	}
}

func TestRequirementsFile(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "requirements.txt", `# runtime deps
requests>=2.0,<3   # http client
Acme_Core==1.0.0
uvicorn[standard] \
    >=0.30
-r requirements-dev.txt
-e ./editable
./local-folder
https://example.com/pkg.tar.gz
bare-name
`)
	write(t, dir, "requirements-dev.txt", "pytest>=8\n")
	mans, err := New().Scan(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(mans) != 2 {
		t.Fatalf("want both requirements files, got %+v", mans)
	}
	dev, main := mans[0], mans[1] // path-sorted: requirements-dev.txt first
	if main.Ecosystem != EcosystemPython || main.Name != "" || !main.Root {
		t.Errorf("line manifests have no identity: %+v", main)
	}
	wantMain := []DeclaredDep{
		{Name: "acme-core", Range: "==1.0.0", Kind: KindDependencies},
		{Name: "bare-name", Kind: KindDependencies},
		{Name: "requests", Range: ">=2.0,<3", Kind: KindDependencies},
		{Name: "uvicorn", Range: ">=0.30", Kind: KindDependencies},
	}
	if !reflect.DeepEqual(main.Deps, wantMain) {
		t.Errorf("deps mismatch:\n got %+v\nwant %+v", main.Deps, wantMain)
	}
	wantDev := []DeclaredDep{{Name: "pytest", Range: ">=8", Kind: KindDevDependencies}}
	if !reflect.DeepEqual(dev.Deps, wantDev) {
		t.Errorf("a dev requirements file declares devDependencies: %+v", dev.Deps)
	}
}

func TestScanRootMatchesSuffixManifests(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "App.csproj", `<Project><PropertyGroup><Version>1.0.0</Version></PropertyGroup></Project>`)
	write(t, dir, "pubspec.yaml", "name: app\n")
	write(t, dir, "nested/Other.csproj", `<Project></Project>`)
	mans, err := ScanRoot(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, m := range mans {
		got = append(got, m.Path)
	}
	want := []string{"App.csproj", "pubspec.yaml"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ScanRoot paths = %v, want %v (root files only, suffix match included)", got, want)
	}
	if mans[0].Name != "App" {
		t.Errorf("csproj name falls back to the file base name, got %q", mans[0].Name)
	}
}

func TestPep508DepForms(t *testing.T) {
	cases := []struct {
		in   string
		want DeclaredDep
	}{
		{"requests", DeclaredDep{Name: "requests"}},
		{"requests >= 2.0", DeclaredDep{Name: "requests", Range: ">= 2.0"}},
		{"Foo.Bar_baz[extra1,extra2]==1.0", DeclaredDep{Name: "foo-bar-baz", Range: "==1.0"}},
		{"pkg; sys_platform == 'darwin'", DeclaredDep{Name: "pkg"}},
		{"core @ file:./core", DeclaredDep{Name: "core", Range: "@ file:./core", LocalPath: "./core"}},
		{"web @ https://example.com/web.tar.gz", DeclaredDep{Name: "web", Range: "@ https://example.com/web.tar.gz"}},
	}
	for _, c := range cases {
		if got := pep508Dep(c.in, KindDependencies); got != c.want {
			t.Errorf("pep508Dep(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}
}
