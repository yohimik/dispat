package writer

import (
	"strings"
	"testing"
)

func TestCsprojRewriteBothVersionForms(t *testing.T) {
	src := `<Project Sdk="Microsoft.NET.Sdk">

  <PropertyGroup>
    <TargetFramework>net8.0</TargetFramework>
    <PackageId>Acme.Core</PackageId>
    <Version>1.2.3</Version>
  </PropertyGroup>

  <PropertyGroup Condition="'$(Configuration)'=='Debug'">
    <Version>0.0.0-debug</Version>
  </PropertyGroup>

  <ItemGroup>
    <PackageReference Include="Newtonsoft.Json" Version="13.0.1" />
    <PackageReference Include="Serilog">
      <Version>3.1.1</Version>
    </PackageReference>
    <PackageReference Include="NoVersion" />
    <ProjectReference Include="..\Other\Other.csproj" />
  </ItemGroup>

</Project>
`
	path := seed(t, "Acme.Core.csproj", src)

	res, err := Rewrite(path, "2.0.0", []Edit{
		{Name: "Newtonsoft.Json", Range: "13.0.3"},
		{Name: "Serilog", Range: "4.0.0"},
		{Name: "NoVersion", Range: "1.0.0"},
		{Name: "Other", Range: "1.0.0"},
		{Name: "Ghost", Range: "1.0.0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.NewReplacer(
		"<Version>1.2.3</Version>", "<Version>2.0.0</Version>",
		`Include="Newtonsoft.Json" Version="13.0.1"`, `Include="Newtonsoft.Json" Version="13.0.3"`,
		"<Version>3.1.1</Version>", "<Version>4.0.0</Version>",
	).Replace(src)
	if got := read(t, path); got != want {
		t.Errorf("file mismatch:\n got: %q\nwant: %q", got, want)
	}
	// The conditioned PropertyGroup keeps its own version: only the first, the
	// one the scanner reports, is the project's own.
	if !strings.Contains(read(t, path), "<Version>0.0.0-debug</Version>") {
		t.Error("a later PropertyGroup version was rewritten")
	}
	if !res.VersionWritten || len(res.Applied) != 2 {
		t.Errorf("result mismatch: %+v", res)
	}
	// NoVersion is declared with nothing to replace; the ProjectReference and
	// the absent package are genuinely not declared as package references.
	if len(res.Skipped) != 1 || res.Skipped[0].Name != "NoVersion" || len(res.Missing) != 2 {
		t.Errorf("skipped/missing split wrong: skipped=%+v missing=%+v", res.Skipped, res.Missing)
	}
}

func TestCsprojRewriteNoChangeLeavesFileAlone(t *testing.T) {
	src := "<Project>\n  <PropertyGroup><Version>1.0.0</Version></PropertyGroup>\n  <ItemGroup><PackageReference Include=\"A\" Version=\"1.0\" /></ItemGroup>\n</Project>\n"
	path := seed(t, "A.csproj", src)
	res, err := Rewrite(path, "1.0.0", []Edit{{Name: "A", Range: "1.0"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Applied) != 0 || res.VersionWritten || read(t, path) != src {
		t.Errorf("no-op rewrite reported work: %+v", res)
	}
}

func TestCsprojRewriteEscapesAndRejectsKindedEdits(t *testing.T) {
	src := `<Project><ItemGroup><PackageReference Include="A" Version="1.0" /></ItemGroup></Project>`
	path := seed(t, "A.csproj", src)
	res, err := Rewrite(path, "", []Edit{{Name: "A", Kind: "devDependencies", Range: "2.0"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Missing) != 1 || read(t, path) != src {
		t.Errorf("a .csproj has one dependency field: %+v", res)
	}

	path = seed(t, "A.csproj", src)
	if _, err := Rewrite(path, "", []Edit{{Name: "A", Range: `2.0"><Evil/><X a="`}}); err != nil {
		t.Fatal(err)
	}
	if err := xmlWellFormed([]byte(read(t, path))); err != nil {
		t.Errorf("rewrite produced invalid XML: %v", err)
	}
	if strings.Contains(read(t, path), "<Evil/>") {
		t.Errorf("metacharacters not escaped: %q", read(t, path))
	}
}
