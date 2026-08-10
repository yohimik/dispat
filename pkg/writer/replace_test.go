package writer

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/yohimik/dispat/pkg/manifest"
)

func TestGoModReplaceAddsRepointsAndRemoves(t *testing.T) {
	src := `// The service module.
module github.com/acme/mono/services/svc

go 1.25.0

require (
	github.com/acme/mono/pkg/core v1.2.0 // the shared core
	github.com/rs/zerolog v1.35.1
)

replace github.com/acme/mono/pkg/core => ../../pkg/core
`
	path := seed(t, "go.mod", src)
	res, err := Replace(path, []Replacement{
		{Name: "github.com/acme/mono/pkg/core", Path: "../../libs/core"}, // repoint
		{Name: "github.com/rs/zerolog", Path: "../../vendor/zerolog"},    // add
		{Name: "github.com/gone/module", Path: ""},                       // remove what is not there
	})
	if err != nil {
		t.Fatal(err)
	}
	got := read(t, path)
	if !strings.Contains(got, "replace github.com/acme/mono/pkg/core => ../../libs/core") {
		t.Errorf("existing replace not repointed:\n%s", got)
	}
	if !strings.Contains(got, "github.com/rs/zerolog => ../../vendor/zerolog") {
		t.Errorf("new replace not added:\n%s", got)
	}
	if !strings.Contains(got, "// The service module.") || !strings.Contains(got, "// the shared core") {
		t.Error("comments did not survive")
	}
	if len(res.Applied) != 2 || len(res.Missing) != 1 || res.Missing[0].Name != "github.com/gone/module" {
		t.Errorf("result mismatch: %+v", res)
	}
}

func TestGoModReplaceRemovesAndRoundTrips(t *testing.T) {
	src := "module example.com/m\n\ngo 1.25.0\n\nrequire example.com/dep v1.0.0\n\nreplace example.com/dep => ../dep\n"
	path := seed(t, "go.mod", src)
	if _, err := Replace(path, []Replacement{{Name: "example.com/dep"}}); err != nil {
		t.Fatal(err)
	}
	if got := read(t, path); strings.Contains(got, "replace") {
		t.Errorf("the replace should be gone:\n%s", got)
	}
	// Putting it back returns the file to what it was.
	if _, err := Replace(path, []Replacement{{Name: "example.com/dep", Path: "../dep"}}); err != nil {
		t.Fatal(err)
	}
	if got := read(t, path); got != src {
		t.Errorf("round trip did not return the original:\n got: %q\nwant: %q", got, src)
	}
}

func TestGoModReplaceVersionedLeftSide(t *testing.T) {
	src := "module m\n\ngo 1.25.0\n\nrequire example.com/dep v1.0.0\n"
	path := seed(t, "go.mod", src)
	if _, err := Replace(path, []Replacement{
		{Name: "example.com/dep", Version: "v1.0.0", Path: "../dep"},
	}); err != nil {
		t.Fatal(err)
	}
	if got := read(t, path); !strings.Contains(got, "example.com/dep v1.0.0 => ../dep") {
		t.Errorf("the versioned form was not written:\n%s", got)
	}
}

func TestGoModReplaceNoChangeLeavesFileAlone(t *testing.T) {
	src := "module m\n\ngo 1.25.0\n\nreplace example.com/dep => ../dep\n"
	path := seed(t, "go.mod", src)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Replace(path, []Replacement{{Name: "example.com/dep", Path: "../dep"}})
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Applied) != 0 || read(t, path) != src || !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("no-op replace touched the file: %+v", res)
	}
}

func TestCargoReplacePreservesEveryOtherByte(t *testing.T) {
	src := `[package]
name = "acme"
version = "1.0.0"   # shipped

[dependencies]
serde = "1.0"
core = { path = "../core", version = "0.3" }

[patch.crates-io]
# pinned while the fix lands upstream
serde = { path = "../forks/serde" }
`
	path := seed(t, "Cargo.toml", src)
	res, err := Replace(path, []Replacement{
		{Name: "serde", Path: "../vendor/serde"}, // repoint an existing patch
		{Name: "rand", Path: "../forks/rand"},    // add to the existing table
	})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(src,
		`serde = { path = "../forks/serde" }`,
		"serde = { path = \"../vendor/serde\" }\nrand = { path = \"../forks/rand\" }", 1)
	if got := read(t, path); got != want {
		t.Errorf("file mismatch:\n got: %q\nwant: %q", got, want)
	}
	if !strings.Contains(read(t, path), "# pinned while the fix lands upstream") {
		t.Error("the comment inside the table did not survive")
	}
	if len(res.Applied) != 2 {
		t.Errorf("result mismatch: %+v", res)
	}
}

func TestCargoReplaceCreatesAndRemovesTheTable(t *testing.T) {
	src := "[package]\nname = \"acme\"\nversion = \"1.0.0\"\n\n[dependencies]\nserde = \"1.0\"\n"
	path := seed(t, "Cargo.toml", src)
	if _, err := Replace(path, []Replacement{{Name: "serde", Path: "../serde"}}); err != nil {
		t.Fatal(err)
	}
	got := read(t, path)
	if !strings.Contains(got, "[patch.crates-io]") || !strings.Contains(got, `serde = { path = "../serde" }`) {
		t.Errorf("the table was not created:\n%s", got)
	}
	// Removing the only entry takes the header with it and returns the file.
	if _, err := Replace(path, []Replacement{{Name: "serde"}}); err != nil {
		t.Fatal(err)
	}
	if got := read(t, path); got != src {
		t.Errorf("round trip did not return the original:\n got: %q\nwant: %q", got, src)
	}
}

func TestPubspecReplaceAddsRepointsAndRemoves(t *testing.T) {
	src := `name: acme
version: 1.0.0

dependencies:
  http: ^1.0.0
  path: ^1.8.0

dependency_overrides:
  http:
    path: ../forks/http

dev_dependencies:
  test: ^1.24.0
`
	path := seed(t, "pubspec.yaml", src)
	res, err := Replace(path, []Replacement{
		{Name: "http", Path: "../vendor/http"}, // repoint
		{Name: "path", Path: "../forks/path"},  // add to the existing block
	})
	if err != nil {
		t.Fatal(err)
	}
	got := read(t, path)
	if !strings.Contains(got, "    path: ../vendor/http") {
		t.Errorf("existing override not repointed:\n%s", got)
	}
	if !strings.Contains(got, "  path:\n    path: ../forks/path") {
		t.Errorf("new override not added:\n%s", got)
	}
	// The dependency declarations and the block after it are untouched.
	if !strings.Contains(got, "  http: ^1.0.0") || !strings.Contains(got, "  test: ^1.24.0") {
		t.Errorf("surrounding blocks disturbed:\n%s", got)
	}
	if len(res.Applied) != 2 {
		t.Errorf("result mismatch: %+v", res)
	}
}

func TestPubspecReplaceCreatesAndRemovesTheBlock(t *testing.T) {
	src := "name: acme\nversion: 1.0.0\n\ndependencies:\n  http: ^1.0.0\n"
	path := seed(t, "pubspec.yaml", src)
	if _, err := Replace(path, []Replacement{{Name: "http", Path: "../http"}}); err != nil {
		t.Fatal(err)
	}
	got := read(t, path)
	if !strings.Contains(got, "dependency_overrides:") || !strings.Contains(got, "    path: ../http") {
		t.Errorf("the block was not created:\n%s", got)
	}
	if _, err := Replace(path, []Replacement{{Name: "http"}}); err != nil {
		t.Fatal(err)
	}
	if got := read(t, path); got != src {
		t.Errorf("round trip did not return the original:\n got: %q\nwant: %q", got, src)
	}
}

func TestPyprojectReplaceUsesUvSources(t *testing.T) {
	src := "[project]\nname = \"acme\"\nversion = \"1.0.0\"\ndependencies = [\n    \"requests>=2.0\",\n]\n"
	path := seed(t, "pyproject.toml", src)
	if _, err := Replace(path, []Replacement{{Name: "requests", Path: "../requests"}}); err != nil {
		t.Fatal(err)
	}
	got := read(t, path)
	if !strings.Contains(got, "[tool.uv.sources]") || !strings.Contains(got, `requests = { path = "../requests" }`) {
		t.Errorf("the uv sources table was not written:\n%s", got)
	}
	if !strings.Contains(got, `"requests>=2.0"`) {
		t.Error("the dependency array was disturbed")
	}
	if _, err := Replace(path, []Replacement{{Name: "requests"}}); err != nil {
		t.Fatal(err)
	}
	if got := read(t, path); got != src {
		t.Errorf("round trip did not return the original:\n got: %q\nwant: %q", got, src)
	}
}

func TestReplaceOnFormatsWithoutRedirects(t *testing.T) {
	// package.json is the one most likely to be mistaken for supporting this.
	// Its overrides force a version across the tree; they cannot name a folder.
	for _, tc := range []struct{ name, src string }{
		{"pom.xml", `<project><artifactId>a</artifactId></project>`},
		{"Podfile", "target 'A' do\n  pod 'B', '~> 1.0'\nend\n"},
		{"Gemfile", "gem 'rails', '~> 7.0'\n"},
		{"build.gradle", "dependencies {\n  implementation 'g:a:1.0'\n}\n"},
		{"composer.json", `{"name":"a/b","replace":{"c/d":"1.0"}}`},
	} {
		path := seed(t, tc.name, tc.src)
		if SupportsReplace(path) {
			t.Errorf("%s should not report replace support", tc.name)
		}
		res, err := Replace(path, []Replacement{{Name: "b", Path: "../b"}})
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if len(res.Skipped) != 1 || len(res.Applied) != 0 {
			t.Errorf("%s: want everything skipped, got %+v", tc.name, res)
		}
		if read(t, path) != tc.src {
			t.Errorf("%s: a no-op replace modified the file", tc.name)
		}
	}
}

func TestReplaceGuards(t *testing.T) {
	if _, err := Replace("settings.gradle", nil); !errors.Is(err, ErrUnsupportedManifest) {
		t.Errorf("an unknown manifest must give ErrUnsupportedManifest, got %v", err)
	}
	path := seed(t, "go.mod", "module m\n")
	if _, err := Replace(path, []Replacement{{Path: "../x"}}); err == nil {
		t.Error("a replacement with no name must be refused")
	}
	if !SupportsReplace(path) || SupportsReplace("pom.xml") {
		t.Error("SupportsReplace disagrees with the replacers table")
	}
	res, err := Replace(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Path != path {
		t.Errorf("Result.Path = %q, want %q", res.Path, path)
	}
}

func TestEveryFormatDeclaresReplaceSupport(t *testing.T) {
	// Every format is either implemented or an explicit no-op. A new format
	// fails here until someone decides which it is.
	noRedirect := map[manifest.Format]bool{
		manifest.FormatComposer:     true,
		manifest.FormatRequirements: true, manifest.FormatMaven: true,
		manifest.FormatMSBuildProject: true, manifest.FormatNuSpec: true,
		manifest.FormatPackagesProps: true, manifest.FormatPackagesConfig: true,
		manifest.FormatPlist: true, manifest.FormatAndroidManifest: true,
		manifest.FormatGradleCatalog: true, manifest.FormatGradleBuild: true,
		manifest.FormatXcodeProject: true, manifest.FormatPodfile: true,
		manifest.FormatPodspec: true, manifest.FormatGemfile: true,
		manifest.FormatGemspec: true,
	}
	for _, f := range manifest.Formats {
		_, implemented := replacers[f]
		if implemented == noRedirect[f] {
			t.Errorf("format %q is neither implemented nor a declared no-op", f)
		}
	}
}

func TestReplaceErrorPathsLeaveFilesAlone(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"go.mod", "module m\n\nrequire (\n"},
		{"Cargo.toml", "[package\nbroken"},
		{"pyproject.toml", "[project\nbroken"},
	} {
		path := seed(t, tc.name, tc.src)
		if _, err := Replace(path, []Replacement{{Name: "x", Path: "../x"}}); err == nil {
			t.Errorf("%s: a broken manifest should fail", tc.name)
		}
		if read(t, path) != tc.src {
			t.Errorf("%s: a failed replace modified the file", tc.name)
		}
	}
	// A missing file is an error before anything is parsed.
	if _, err := Replace("/nonexistent/go.mod", nil); err == nil {
		t.Error("a missing manifest must error")
	}
}

func TestReplaceRemovalsAndRepeatedEntries(t *testing.T) {
	// A table holding more than one entry keeps its header when only one goes.
	src := "[patch.crates-io]\na = { path = \"../a\" }\nb = { path = \"../b\" }\n"
	path := seed(t, "Cargo.toml", src)
	res, err := Replace(path, []Replacement{{Name: "a"}})
	if err != nil {
		t.Fatal(err)
	}
	got := read(t, path)
	if !strings.Contains(got, "[patch.crates-io]") || !strings.Contains(got, `b = { path = "../b" }`) {
		t.Errorf("the table should survive with one entry left:\n%s", got)
	}
	if strings.Contains(got, `a = {`) || len(res.Applied) != 1 {
		t.Errorf("the entry was not removed: %+v\n%s", res, got)
	}
	// Removing something absent is missing, not an error, and writes nothing.
	before := read(t, path)
	res, err = Replace(path, []Replacement{{Name: "gone"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Missing) != 1 || len(res.Applied) != 0 || read(t, path) != before {
		t.Errorf("removing an absent entry should be a no-op: %+v", res)
	}
}

func TestPubspecReplaceOverwritesANonPathOverride(t *testing.T) {
	// An override pointing at a git source has no path line to splice, so the
	// entry is replaced wholesale rather than edited in place.
	src := "name: a\n\ndependency_overrides:\n  http:\n    git:\n      url: https://example.com/http.git\n"
	path := seed(t, "pubspec.yaml", src)
	res, err := Replace(path, []Replacement{{Name: "http", Path: "../http"}})
	if err != nil {
		t.Fatal(err)
	}
	got := read(t, path)
	if strings.Contains(got, "git:") || !strings.Contains(got, "    path: ../http") {
		t.Errorf("the git override was not replaced by a path:\n%s", got)
	}
	if len(res.Applied) != 1 {
		t.Errorf("result mismatch: %+v", res)
	}
}

func TestReplaceOnAnUnreadableAndOversizedManifest(t *testing.T) {
	dir := t.TempDir()
	big := dir + "/go.mod"
	f, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxManifestBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Replace(big, nil); !errors.Is(err, ErrManifestTooLarge) {
		t.Errorf("an oversized manifest: got %v, want ErrManifestTooLarge", err)
	}
}

func TestNpmReplaceUsesOverridesWithAFileSpec(t *testing.T) {
	src := `{
  "name": "@acme/web",
  "version": "1.0.0",
  "dependencies": {
    "@acme/core": "^1.0.0",
    "left-pad": "^1.3.0"
  }
}`
	path := seed(t, "package.json", src)
	res, err := Replace(path, []Replacement{{Name: "@acme/core", Path: "../core"}})
	if err != nil {
		t.Fatal(err)
	}
	got := read(t, path)
	if !strings.Contains(got, `"overrides": {`) || !strings.Contains(got, `"@acme/core": "file:../core"`) {
		t.Errorf("the overrides map was not created:\n%s", got)
	}
	// The declarations are untouched: an override is a separate directive.
	if !strings.Contains(got, `"@acme/core": "^1.0.0"`) {
		t.Errorf("the dependency declaration was disturbed:\n%s", got)
	}
	if len(res.Applied) != 1 {
		t.Errorf("result mismatch: %+v", res)
	}
	// Removing it returns the file to exactly what it was.
	if _, err := Replace(path, []Replacement{{Name: "@acme/core"}}); err != nil {
		t.Fatal(err)
	}
	if got := read(t, path); got != src {
		t.Errorf("round trip did not return the original:\n got: %q\nwant: %q", got, src)
	}
}

func TestNpmReplaceFollowsTheFileToYarnAndPnpm(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{
			"an existing resolutions map wins",
			"{\n  \"name\": \"a\",\n  \"resolutions\": {\n    \"old\": \"1.0.0\"\n  }\n}",
			`"resolutions"`,
		},
		{
			"an existing pnpm.overrides map wins",
			"{\n  \"name\": \"a\",\n  \"pnpm\": {\n    \"overrides\": {\n      \"old\": \"1.0.0\"\n    }\n  }\n}",
			`"pnpm"`,
		},
		{
			"packageManager names yarn",
			"{\n  \"name\": \"a\",\n  \"packageManager\": \"yarn@4.1.0\"\n}",
			`"resolutions"`,
		},
		{
			"packageManager names pnpm",
			"{\n  \"name\": \"a\",\n  \"packageManager\": \"pnpm@9.0.0\"\n}",
			`"pnpm"`,
		},
		{
			"nothing to go on, npm's overrides",
			"{\n  \"name\": \"a\"\n}",
			`"overrides"`,
		},
	} {
		path := seed(t, "package.json", tc.src)
		if _, err := Replace(path, []Replacement{{Name: "core", Path: "../core"}}); err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		got := read(t, path)
		if !strings.Contains(got, tc.want) {
			t.Errorf("%s: wanted %s in:\n%s", tc.name, tc.want, got)
		}
		if !strings.Contains(got, `"core": "file:../core"`) {
			t.Errorf("%s: the redirect was not written:\n%s", tc.name, got)
		}
		// Whatever field it chose, removing returns the original bytes.
		if _, err := Replace(path, []Replacement{{Name: "core"}}); err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if got := read(t, path); got != tc.src {
			t.Errorf("%s: round trip did not return the original:\n got: %q\nwant: %q", tc.name, got, tc.src)
		}
	}
}

func TestNpmReplacePreservesEveryOtherByte(t *testing.T) {
	// Tabs, an existing override, a trailing entry after the map, and no
	// trailing newline. Only the map may change.
	src := "{\n\t\"name\": \"@acme/web\",\n\t\"overrides\": {\n\t\t\"old\": \"file:../old\"\n\t},\n\t\"scripts\": {\"build\": \"tsc\"}\n}"
	path := seed(t, "package.json", src)
	res, err := Replace(path, []Replacement{
		{Name: "old", Path: "../moved"}, // repoint
		{Name: "new", Path: "../new"},   // add beside it
	})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(src,
		"\t\t\"old\": \"file:../old\"\n",
		"\t\t\"old\": \"file:../moved\",\n\t\t\"new\": \"file:../new\"\n", 1)
	if got := read(t, path); got != want {
		t.Errorf("file mismatch:\n got: %q\nwant: %q", got, want)
	}
	if len(res.Applied) != 2 {
		t.Errorf("result mismatch: %+v", res)
	}
}

func TestNpmReplaceRemovalKeepsSiblingsAndDropsAnEmptyMap(t *testing.T) {
	src := "{\n  \"name\": \"a\",\n  \"overrides\": {\n    \"one\": \"file:../one\",\n    \"two\": \"file:../two\"\n  }\n}"
	path := seed(t, "package.json", src)
	if _, err := Replace(path, []Replacement{{Name: "one"}}); err != nil {
		t.Fatal(err)
	}
	got := read(t, path)
	if strings.Contains(got, `"one"`) || !strings.Contains(got, `"two": "file:../two"`) {
		t.Errorf("the wrong entry went:\n%s", got)
	}
	if !json.Valid([]byte(got)) {
		t.Errorf("removal left invalid JSON:\n%s", got)
	}
	// Taking the last one drops the map with it.
	if _, err := Replace(path, []Replacement{{Name: "two"}}); err != nil {
		t.Fatal(err)
	}
	if got := read(t, path); strings.Contains(got, "overrides") {
		t.Errorf("an emptied map should go too:\n%s", got)
	}
}

func TestNpmReplaceNoChangeLeavesFileAlone(t *testing.T) {
	src := "{\n  \"overrides\": {\n    \"core\": \"file:../core\"\n  }\n}"
	path := seed(t, "package.json", src)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Replace(path, []Replacement{{Name: "core", Path: "../core"}})
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Applied) != 0 || read(t, path) != src || !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("no-op replace touched the file: %+v", res)
	}
}
