package writer

import (
	"strings"
	"testing"
)

// A self-closing element holds no bytes a splice can land inside. The XML
// decoder reports one as a start tag followed immediately by an end tag at the
// same offset, so a writer that only checks what follows the value mistakes it
// for an empty element whenever the next bytes are a closing tag — and writes
// the version after the tag, outside the element it belongs to. The file still
// parses, so no verify catches it; the value simply ends up somewhere no build
// reads it.

func TestMavenRewriteDeclinesASelfClosingVersion(t *testing.T) {
	src := `<project><groupId>acme</groupId><artifactId>app</artifactId><version/>` +
		`<dependencies><dependency><groupId>acme</groupId><artifactId>core</artifactId><version/></dependency></dependencies></project>`
	path := seed(t, "pom.xml", src)
	res, err := Rewrite(path, "2.0.0", []Edit{{Name: "acme:core", Range: "9.9.9"}})
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if res.VersionWritten || len(res.Applied) != 0 {
		t.Errorf("a self-closing element was written into: %+v", res)
	}
	if got := read(t, path); got != src {
		t.Errorf("the file was changed:\n%s", got)
	}
}

func TestCsprojRewriteDeclinesASelfClosingVersion(t *testing.T) {
	src := `<Project><PropertyGroup><Version/></PropertyGroup>` +
		`<ItemGroup><PackageReference Include="Core"><Version/></PackageReference></ItemGroup></Project>`
	path := seed(t, "Acme.csproj", src)
	res, err := Rewrite(path, "2.0.0", []Edit{{Name: "Core", Range: "9.9.9"}})
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if res.VersionWritten || len(res.Applied) != 0 {
		t.Errorf("a self-closing element was written into: %+v", res)
	}
	if got := read(t, path); got != src {
		t.Errorf("the file was changed:\n%s", got)
	}
}

func TestPlistRewriteDeclinesASelfClosingString(t *testing.T) {
	src := `<plist><dict><key>CFBundleShortVersionString</key><string/></dict></plist>`
	path := seed(t, "Info.plist", src)
	res, err := Rewrite(path, "2.0.0", nil)
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if res.VersionWritten {
		t.Errorf("a self-closing string was written into: %+v", res)
	}
	if got := read(t, path); got != src {
		t.Errorf("the file was changed:\n%s", got)
	}
}

func TestPlistBuildWriteDeclinesASelfClosingString(t *testing.T) {
	src := `<plist><dict><key>CFBundleVersion</key><string/></dict></plist>`
	path := seed(t, "Info.plist", src)
	res, err := SetBuild(path, "42")
	if err != nil {
		t.Fatalf("set build: %v", err)
	}
	if res.BuildWritten {
		t.Errorf("a self-closing string was written into: %+v", res)
	}
	if got := read(t, path); got != src {
		t.Errorf("the file was changed:\n%s", got)
	}
}

func TestSpacedEmptyElementsStillSplice(t *testing.T) {
	// An element that is written out in full, even with no text inside it, does
	// have bytes between its tags, and stays writable.
	src := "<project><groupId>acme</groupId><artifactId>app</artifactId><version></version></project>"
	path := seed(t, "pom.xml", src)
	res, err := Rewrite(path, "2.0.0", nil)
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if !res.VersionWritten || !strings.Contains(read(t, path), "<version>2.0.0</version>") {
		t.Errorf("an empty element was not written into: %+v\n%s", res, read(t, path))
	}
}
