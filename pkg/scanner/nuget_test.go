package scanner

import (
	"reflect"
	"testing"
)

func TestNuspecIdentityAndGroupedDependencies(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "Acme.Core.nuspec", `<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://schemas.microsoft.com/packaging/2013/05/nuspec.xsd">
  <metadata>
    <id>Acme.Core</id>
    <version>1.2.3</version>
    <authors>Acme</authors>
    <dependencies>
      <dependency id="Serilog" version="3.1.1" />
      <group targetFramework="net8.0">
        <dependency id="Newtonsoft.Json" version="[13.0.1,14.0.0)" />
      </group>
    </dependencies>
  </metadata>
</package>
`)
	m := scanOne(t, dir)
	want := Manifest{
		Path: "Acme.Core.nuspec", Ecosystem: EcosystemNuGet,
		Name: "Acme.Core", Version: "1.2.3", Root: true,
		Deps: []DeclaredDep{
			// NuGet interval notation is version text like any other.
			{Name: "Newtonsoft.Json", Range: "[13.0.1,14.0.0)", Kind: KindDependencies},
			{Name: "Serilog", Range: "3.1.1", Kind: KindDependencies},
		},
	}
	if !reflect.DeepEqual(m, want) {
		t.Errorf("manifest mismatch:\n got %+v\nwant %+v", m, want)
	}
}

func TestNuspecReplacementTokens(t *testing.T) {
	// A nuspec packed from a project is a template: NuGet fills these in.
	dir := t.TempDir()
	write(t, dir, "Acme.nuspec", `<package><metadata>
  <id>$id$</id>
  <version>$version$</version>
</metadata></package>
`)
	m := scanOne(t, dir)
	if m.Name != "" {
		t.Errorf("a token identifier must not become a name, got %q", m.Name)
	}
	if m.Version != "$version$" {
		t.Errorf("version = %q, want the token kept verbatim", m.Version)
	}
}

func TestPackagesPropsCentralVersions(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "Directory.Packages.props", `<Project>
  <PropertyGroup>
    <ManagePackageVersionsCentrally>true</ManagePackageVersionsCentrally>
  </PropertyGroup>
  <ItemGroup>
    <PackageVersion Include="Newtonsoft.Json" Version="13.0.1" />
    <PackageVersion Include="Serilog" Version="3.1.1" />
  </ItemGroup>
</Project>
`)
	m := scanOne(t, dir)
	want := Manifest{
		Path: "Directory.Packages.props", Ecosystem: EcosystemNuGet, Root: true,
		Deps: []DeclaredDep{
			{Name: "Newtonsoft.Json", Range: "13.0.1", Kind: KindDependencies},
			{Name: "Serilog", Range: "3.1.1", Kind: KindDependencies},
		},
	}
	if !reflect.DeepEqual(m, want) {
		t.Errorf("manifest mismatch:\n got %+v\nwant %+v", m, want)
	}
}

func TestPackagesConfigDevelopmentFlag(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "packages.config", `<?xml version="1.0" encoding="utf-8"?>
<packages>
  <package id="Newtonsoft.Json" version="13.0.1" targetFramework="net48" />
  <package id="StyleCop.Analyzers" version="1.1.118" developmentDependency="true" />
</packages>
`)
	m := scanOne(t, dir)
	want := Manifest{
		Path: "packages.config", Ecosystem: EcosystemNuGet, Root: true,
		Deps: []DeclaredDep{
			{Name: "Newtonsoft.Json", Range: "13.0.1", Kind: KindDependencies},
			{Name: "StyleCop.Analyzers", Range: "1.1.118", Kind: KindDevDependencies},
		},
	}
	if !reflect.DeepEqual(m, want) {
		t.Errorf("manifest mismatch:\n got %+v\nwant %+v", m, want)
	}
}

func TestFsprojAndVbprojShareTheCsprojSchema(t *testing.T) {
	for _, name := range []string{"App.fsproj", "App.vbproj"} {
		dir := t.TempDir()
		write(t, dir, name, `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup><PackageId>App</PackageId><Version>1.0.0</Version></PropertyGroup>
  <ItemGroup><PackageReference Include="Serilog" Version="3.1.1" /></ItemGroup>
</Project>
`)
		m := scanOne(t, dir)
		if m.Ecosystem != EcosystemNuGet || m.Name != "App" || m.Version != "1.0.0" || len(m.Deps) != 1 {
			t.Errorf("%s: %+v", name, m)
		}
	}
}
