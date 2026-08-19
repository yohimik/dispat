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

	"github.com/yohimik/dispat/pkg/manifest"
)

// everyWriterName is one file name per supported format, minus the spellings
// that only repeat a format already named (pubspec.yml, the F# and VB project
// files). TestEveryWriterNameCoversEveryFormat fences it against
// manifest.Formats, so a format gaining a writer without joining this suite
// fails loudly instead of silently going untested.
var everyWriterName = []string{
	"package.json", "go.mod", "requirements.txt", "Cargo.toml", "pyproject.toml",
	"composer.json", "pom.xml", "App.csproj", "pubspec.yaml", "Info.plist",
	"AndroidManifest.xml", "libs.versions.toml", "project.pbxproj", "Podfile",
	"Alamofire.podspec", "build.gradle", "build.gradle.kts", "Gemfile",
	"acme.gemspec", "Acme.nuspec", "Directory.Packages.props", "packages.config",
	"Dockerfile", "compose.yaml",
	// The engine formats. Four of them are recognised by the folder they sit
	// in rather than by their base name, so the names here are paths and the
	// fence resolves them as paths.
	"Packages/manifest.json", "ProjectSettings/ProjectSettings.asset",
	"project.godot", "plugin.cfg", "export_presets.cfg",
	"MyGame.uproject", "AcmeNet.uplugin",
	"Config/DefaultGame.ini", "Config/DefaultEngine.ini",
	"game.project", "project.json", "gem.json",
}

func TestEveryWriterNameCoversEveryFormat(t *testing.T) {
	covered := map[manifest.Format]bool{}
	for _, name := range everyWriterName {
		f, ok := manifest.FormatOfPath(name)
		if !ok {
			t.Errorf("%s: names no format", name)
			continue
		}
		covered[f] = true
	}
	for _, f := range manifest.Formats {
		if !covered[f] {
			t.Errorf("format %q is missing from the robustness suite", f)
		}
	}
}

func TestRewriteReportsAnUnreadableManifest(t *testing.T) {
	// A folder wearing a manifest's name passes the size check and then fails
	// the read, which is the one way to reach every format's read-error path
	// without breaking the filesystem. Whatever the cause, the failure must
	// surface rather than be mistaken for an empty manifest.
	for _, name := range everyWriterName {
		// MkdirAll, because the formats recognised by their folder are named
		// here as paths and their parent has to exist first.
		path := filepath.Join(t.TempDir(), name)
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		res, err := Rewrite(path, "2.0.0", []Edit{{Name: "acme", Range: "1.0.0"}})
		if err == nil {
			t.Errorf("%s: an unreadable manifest must be an error, got %+v", name, res)
		}
	}
}

func TestLinkReportsAnUnreadableManifest(t *testing.T) {
	for _, name := range []string{"package.json", "go.mod", "Cargo.toml", "pyproject.toml", "pubspec.yaml"} {
		path := filepath.Join(t.TempDir(), name)
		if err := os.Mkdir(path, 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := Relink(path, []Link{{Name: "acme", Path: "../acme"}}); err == nil {
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
	// The engine formats with a grammar to fail. The INI-family ones
	// (project.godot, plugin.cfg, export_presets.cfg, the Unreal configs,
	// game.project) and the Unity settings file have no parse to fail by
	// design: an unrecognised line is stepped over, the same way the Gradle
	// and Podfile readers step over one, which is why those are absent here
	// too.
	"Packages/manifest.json": `{"dependencies": {`,
	"MyGame.uproject":        `{"Plugins": [`,
	"AcmeNet.uplugin":        `{"VersionName":`,
	"project.json":           `{"project_name": "acme", "dependencies": [`,
	"gem.json":               `{"gem_name": "acme", "version":`,
	// The Docker formats have no entry here: the Dockerfile and compose
	// writers read any byte sequence line by line and re-run their own reader
	// over the result instead of parsing up front, so there is no cheap
	// "does not parse" refusal to trigger. Their unreadable-file path is
	// covered through everyWriterName above.
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

func TestLinkRefusesAManifestThatDoesNotParse(t *testing.T) {
	for _, name := range []string{"package.json", "go.mod", "Cargo.toml", "pyproject.toml"} {
		src := malformed[name]
		path := seed(t, name, src)
		if _, err := Relink(path, []Link{{Name: "acme", Path: "../acme"}}); err == nil {
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
	if _, err := Relink(path, []Link{{Name: "acme", Path: "../acme"}}); err == nil {
		t.Error("Link shares the cap and must refuse it too")
	}
}
