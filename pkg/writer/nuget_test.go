package writer

import (
	"strings"
	"testing"
)

func TestNuspecRewriteVersionAndDependencies(t *testing.T) {
	src := `<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://schemas.microsoft.com/packaging/2013/05/nuspec.xsd">
  <metadata>
    <id>Acme.Core</id>
    <version>1.2.3</version>
    <dependencies>
      <dependency id="Serilog" version="3.1.1" />
      <group targetFramework="net8.0">
        <dependency id="Newtonsoft.Json" version="13.0.1" />
      </group>
      <dependency id="NoVersion" />
    </dependencies>
  </metadata>
</package>
`
	path := seed(t, "Acme.Core.nuspec", src)
	res, err := Rewrite(path, "2.0.0", []Edit{
		{Name: "Serilog", Range: "4.0.0"},
		{Name: "Newtonsoft.Json", Range: "13.0.3"},
		{Name: "NoVersion", Range: "1.0.0"},
		{Name: "Ghost", Range: "1.0.0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.NewReplacer(
		"<version>1.2.3</version>", "<version>2.0.0</version>",
		`id="Serilog" version="3.1.1"`, `id="Serilog" version="4.0.0"`,
		`id="Newtonsoft.Json" version="13.0.1"`, `id="Newtonsoft.Json" version="13.0.3"`,
	).Replace(src)
	if got := read(t, path); got != want {
		t.Errorf("file mismatch:\n got: %q\nwant: %q", got, want)
	}
	if !res.VersionWritten || len(res.Applied) != 2 {
		t.Errorf("result mismatch: %+v", res)
	}
	if len(res.Skipped) != 1 || res.Skipped[0].Name != "NoVersion" ||
		len(res.Missing) != 1 || res.Missing[0].Name != "Ghost" {
		t.Errorf("skipped/missing split wrong: skipped=%+v missing=%+v", res.Skipped, res.Missing)
	}
}

func TestNuspecRewriteRefusesReplacementTokens(t *testing.T) {
	// Writing a literal over $version$ would sever the link to the project and
	// freeze the package at whatever was written.
	src := "<package><metadata>\n  <id>$id$</id>\n  <version>$version$</version>\n  <dependencies>\n    <dependency id=\"A\" version=\"$v$\" />\n  </dependencies>\n</metadata></package>\n"
	path := seed(t, "A.nuspec", src)
	res, err := Rewrite(path, "2.0.0", []Edit{{Name: "A", Range: "9.9"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.VersionWritten || len(res.Applied) != 0 || read(t, path) != src {
		t.Errorf("a replacement token must not be overwritten: %+v", res)
	}
}

func TestPackagesPropsRewriteCentralVersions(t *testing.T) {
	src := `<Project>
  <ItemGroup>
    <PackageVersion Include="Newtonsoft.Json" Version="13.0.1" />
    <PackageVersion Include="Serilog" Version="3.1.1" />
  </ItemGroup>
  <ItemGroup Condition="'$(TargetFramework)'=='net48'">
    <PackageVersion Include="Serilog" Version="3.1.1" />
  </ItemGroup>
</Project>
`
	path := seed(t, "Directory.Packages.props", src)
	res, err := Rewrite(path, "9.9.9", []Edit{
		{Name: "Serilog", Range: "4.0.0"},
		{Name: "Absent", Range: "1.0.0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Declared twice, spliced twice, reported once — and this file has no own
	// version, so the version argument has no target.
	if strings.Count(read(t, path), `Version="4.0.0"`) != 2 {
		t.Errorf("both declarations must be spliced: %q", read(t, path))
	}
	if len(res.Applied) != 1 || len(res.Missing) != 1 || res.VersionWritten {
		t.Errorf("result mismatch: %+v", res)
	}
	if !strings.Contains(read(t, path), `Include="Newtonsoft.Json" Version="13.0.1"`) {
		t.Error("an untargeted entry was rewritten")
	}
}

func TestPackagesConfigRewriteLowercaseAttributes(t *testing.T) {
	src := `<?xml version="1.0" encoding="utf-8"?>
<packages>
  <package id="Newtonsoft.Json" version="13.0.1" targetFramework="net48" />
  <package id="StyleCop.Analyzers" version="1.1.118" developmentDependency="true" />
</packages>
`
	path := seed(t, "packages.config", src)
	res, err := Rewrite(path, "", []Edit{
		{Name: "Newtonsoft.Json", Range: "13.0.3"},
		{Name: "StyleCop.Analyzers", Kind: "devDependencies", Range: "1.2.0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(src, `version="13.0.1"`, `version="13.0.3"`, 1)
	if got := read(t, path); got != want {
		t.Errorf("file mismatch:\n got: %q\nwant: %q", got, want)
	}
	// packages.config has one dependency field, so a kinded edit names nothing
	// it can express.
	if len(res.Applied) != 1 || len(res.Missing) != 1 {
		t.Errorf("result mismatch: %+v", res)
	}
}

func TestNuGetWriterDispatch(t *testing.T) {
	for _, name := range []string{
		"App.csproj", "App.fsproj", "App.vbproj", "Acme.nuspec",
		"Directory.Packages.props", "packages.config",
	} {
		if !Supported(name) {
			t.Errorf("%s should have a writer", name)
		}
	}
	for _, name := range []string{"Directory.Build.props", "packages.lock.json", "notes.nuspec.txt"} {
		if Supported(name) {
			t.Errorf("%s should not have a writer", name)
		}
	}
}

func TestNetWritersRefuseMSBuildPropertyReferences(t *testing.T) {
	// The value lives in a Directory.Build.props or on the command line.
	// Freezing it to a literal stops the property working, so all three .NET
	// writers decline, matching the plist, pom and nuspec writers.
	csproj := `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <Version>$(VersionPrefix)</Version>
  </PropertyGroup>
  <ItemGroup>
    <PackageReference Include="Serilog" Version="$(SerilogVersion)" />
    <PackageReference Include="Newtonsoft.Json">
      <Version>$(JsonVersion)</Version>
    </PackageReference>
  </ItemGroup>
</Project>
`
	path := seed(t, "A.csproj", csproj)
	res, err := Rewrite(path, "2.0.0", []Edit{
		{Name: "Serilog", Range: "4.0.0"},
		{Name: "Newtonsoft.Json", Range: "13.0.3"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.VersionWritten || len(res.Applied) != 0 || read(t, path) != csproj {
		t.Errorf("csproj property references must survive: %+v", res)
	}

	props := "<Project>\n  <ItemGroup>\n    <PackageVersion Include=\"Serilog\" Version=\"$(SerilogVersion)\" />\n  </ItemGroup>\n</Project>\n"
	path = seed(t, "Directory.Packages.props", props)
	if res, err = Rewrite(path, "", []Edit{{Name: "Serilog", Range: "4.0.0"}}); err != nil {
		t.Fatal(err)
	}
	if len(res.Applied) != 0 || read(t, path) != props {
		t.Errorf("central property references must survive: %+v", res)
	}

	config := "<packages>\n  <package id=\"Serilog\" version=\"$(SerilogVersion)\" />\n</packages>\n"
	path = seed(t, "packages.config", config)
	if res, err = Rewrite(path, "", []Edit{{Name: "Serilog", Range: "4.0.0"}}); err != nil {
		t.Fatal(err)
	}
	if len(res.Applied) != 0 || read(t, path) != config {
		t.Errorf("packages.config property references must survive: %+v", res)
	}
}
