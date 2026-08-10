package writer

// Every format's two refusal paths, tested once per format rather than once
// per writer, because the promise is the package's and not any one
// ecosystem's: a manifest that cannot be read, and a manifest that does not
// parse, must both come back as an error with the file untouched. A writer
// that half-applied an edit to a broken file would leave a manifest worse than
// it found it.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// everyWriterName is one file name per supported format, the same list
// TestEveryScannedEcosystemHasAWriter fences, minus the spellings that only
// repeat a format already named (pubspec.yml, the F# and VB project files).
var everyWriterName = []string{
	"package.json", "go.mod", "requirements.txt", "Cargo.toml", "pyproject.toml",
	"composer.json", "pom.xml", "App.csproj", "pubspec.yaml", "Info.plist",
	"AndroidManifest.xml", "libs.versions.toml", "project.pbxproj", "Podfile",
	"Alamofire.podspec", "build.gradle", "build.gradle.kts", "Gemfile",
	"acme.gemspec", "Acme.nuspec", "Directory.Packages.props", "packages.config",
}

func TestRewriteReportsAnUnreadableManifest(t *testing.T) {
	// A folder wearing a manifest's name passes the size check and then fails
	// the read, which is the one way to reach every format's read-error path
	// without breaking the filesystem. Whatever the cause, the failure must
	// surface rather than be mistaken for an empty manifest.
	for _, name := range everyWriterName {
		path := filepath.Join(t.TempDir(), name)
		if err := os.Mkdir(path, 0o755); err != nil {
			t.Fatal(err)
		}
		res, err := Rewrite(path, "2.0.0", []Edit{{Name: "acme", Range: "1.0.0"}})
		if err == nil {
			t.Errorf("%s: an unreadable manifest must be an error, got %+v", name, res)
		}
	}
}

func TestReplaceReportsAnUnreadableManifest(t *testing.T) {
	for _, name := range []string{"package.json", "go.mod", "Cargo.toml", "pyproject.toml", "pubspec.yaml"} {
		path := filepath.Join(t.TempDir(), name)
		if err := os.Mkdir(path, 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := Replace(path, []Replacement{{Name: "acme", Path: "../acme"}}); err == nil {
			t.Errorf("%s: an unreadable manifest must be an error", name)
		}
	}
}

// malformed is a file of each format that its own parser has to reject.
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

func TestRewriteRefusesAManifestThatDoesNotParse(t *testing.T) {
	// The formats with a grammar to check against refuse the file outright.
	// The rest (the Ruby manifests, the Gradle scripts, the Xcode project,
	// the line-oriented formats) have no cheap parse to fail, and their own
	// guards are tested where they live.
	for name, src := range malformed {
		path := seed(t, name, src)
		if _, err := Rewrite(path, "2.0.0", []Edit{{Name: "acme", Range: "1.0.0"}}); err == nil {
			t.Errorf("%s: a manifest that does not parse must be refused", name)
			continue
		}
		if got := read(t, path); got != src {
			t.Errorf("%s: a refused manifest must be left untouched:\n got: %q\nwant: %q", name, got, src)
		}
	}
}

func TestReplaceRefusesAManifestThatDoesNotParse(t *testing.T) {
	for _, name := range []string{"package.json", "go.mod", "Cargo.toml", "pyproject.toml"} {
		src := malformed[name]
		path := seed(t, name, src)
		if _, err := Replace(path, []Replacement{{Name: "acme", Path: "../acme"}}); err == nil {
			t.Errorf("%s: a manifest that does not parse must be refused", name)
			continue
		}
		if got := read(t, path); got != src {
			t.Errorf("%s: a refused manifest must be left untouched", name)
		}
	}
}

func TestRewriteRefusesAManifestOverTheReadCap(t *testing.T) {
	// The cap exists so a name collision on a huge generated file cannot pull
	// gigabytes into memory. It is checked before any format sees the path, so
	// one oversized file is enough to prove it for all of them.
	path := seed(t, "package.json", "")
	if err := os.Truncate(path, maxManifestBytes+1); err != nil {
		t.Skipf("cannot create a sparse file here: %v", err)
	}
	_, err := Rewrite(path, "2.0.0", nil)
	if err == nil || !strings.Contains(err.Error(), "exceeds 16 MiB") {
		t.Errorf("an oversized manifest must be refused, got %v", err)
	}
	if _, err := Replace(path, []Replacement{{Name: "acme", Path: "../acme"}}); err == nil {
		t.Error("Replace shares the cap and must refuse it too")
	}
}
