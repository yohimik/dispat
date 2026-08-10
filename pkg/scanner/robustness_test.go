package scanner

// The reader's refusal paths, tested once per format rather than once per
// parser. The promise is the package's: a manifest that does not parse is
// reported by name and skipped, never guessed at and never silently read as an
// empty manifest, because a package that looks dependency-free is exactly how
// a broken file would quietly cost a repository its graph.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// malformed is a file of each format whose own parser has to reject it.
var malformed = map[string]string{
	"package.json":             `{"name": "acme", "dependencies": {`,
	"composer.json":            `{"require": [`,
	"go.mod":                   "module \x00 nonsense {\nrequire (\n",
	"Cargo.toml":               "[package\nname = \"acme\"\n",
	"pyproject.toml":           "[project\nname = \"acme\"\n",
	"libs.versions.toml":       "[versions\nacme = \"1.0\"\n",
	"pom.xml":                  "<project><version>1.0</version>",
	"App.csproj":               "<Project><PropertyGroup>",
	"Acme.nuspec":              "<package><metadata>",
	"Directory.Packages.props": "<Project><ItemGroup>",
	"packages.config":          "<packages>",
	"AndroidManifest.xml":      `<manifest android:versionName="1.0">`,
	"Info.plist":               "<plist><dict><key>CFBundleShortVersionString</key>",
}

func TestScanReportsEveryFormatThatDoesNotParse(t *testing.T) {
	for name, src := range malformed {
		dir := t.TempDir()
		write(t, dir, name, src)
		// A healthy manifest beside it, to prove the failure costs only its
		// own file.
		write(t, dir, "sibling/package.json", `{"name": "sibling"}`)

		mans, err := New().Scan(context.Background(), dir)
		if err == nil {
			t.Errorf("%s: a manifest that does not parse must be reported", name)
			continue
		}
		if !strings.Contains(err.Error(), name) {
			t.Errorf("%s: the error must name the file, got %v", name, err)
		}
		if len(mans) != 1 || mans[0].Name != "sibling" {
			t.Errorf("%s: the healthy manifest beside it was lost: %+v", name, mans)
		}
	}
}

func TestScanRootReportsEveryFormatThatDoesNotParse(t *testing.T) {
	// ScanRoot shares the contract but not the code path, so it gets the same
	// treatment: one broken file in the folder, one healthy one beside it.
	for name, src := range malformed {
		dir := t.TempDir()
		write(t, dir, name, src)
		write(t, dir, "requirements.txt", "requests==2.31.0\n")

		mans, err := New().ScanRoot(context.Background(), dir)
		if err == nil {
			t.Errorf("%s: a manifest that does not parse must be reported", name)
			continue
		}
		if len(mans) != 1 || mans[0].Path != "requirements.txt" {
			t.Errorf("%s: the healthy manifest beside it was lost: %+v", name, mans)
		}
	}
}

func TestBothMethodsReportAManifestOverTheReadCap(t *testing.T) {
	// The cap is enforced at every call site, not only inside readManifest, so
	// both methods have to report the oversized file by name and still return
	// the manifest beside it. A sparse file over the cap costs no real disk.
	dir := t.TempDir()
	big, err := os.Create(filepath.Join(dir, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := big.Truncate(maxManifestBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err := big.Close(); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "go.mod", "module github.com/acme/core\n\ngo 1.26\n")

	for _, tc := range []struct {
		method string
		scan   func() ([]Manifest, error)
	}{
		{"ScanRoot", func() ([]Manifest, error) { return New().ScanRoot(context.Background(), dir) }},
		{"Scan", func() ([]Manifest, error) { return New().Scan(context.Background(), dir) }},
	} {
		mans, err := tc.scan()
		if err == nil {
			t.Errorf("%s: an oversized manifest must be reported", tc.method)
			continue
		}
		if !strings.Contains(err.Error(), "package.json") {
			t.Errorf("%s: the error must name the file, got %v", tc.method, err)
		}
		if len(mans) != 1 || mans[0].Ecosystem != EcosystemGoMod {
			t.Errorf("%s: the readable manifest was lost: %+v", tc.method, mans)
		}
	}
}

func TestAFolderNamedLikeAManifestIsNotOne(t *testing.T) {
	// Manifests are recognised by file name, and a folder can carry any name a
	// file can. Neither method may mistake one for a manifest, and the walk
	// must still descend into it.
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "package.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "package.json/go.mod", "module github.com/acme/inside\n\ngo 1.26\n")

	mans, err := New().ScanRoot(context.Background(), dir)
	if err != nil || len(mans) != 0 {
		t.Errorf("ScanRoot: want no manifests and no error, got %+v / %v", mans, err)
	}

	mans, err = New().Scan(context.Background(), dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(mans) != 1 || mans[0].Name != "github.com/acme/inside" {
		t.Errorf("Scan must descend into it and read what is really there: %+v", mans)
	}
}
