package writer

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yohimik/dispat/pkg/manifest"
)

// seed writes a manifest into a temp folder and returns its path.
func seed(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestRewriteReadsTheLongKindSpellingAsTheZeroKind(t *testing.T) {
	// An Edit carrying Kind "dependencies" must behave exactly like the zero
	// Kind in every format. The Podfile and pubspec writers historically
	// compared against the zero value alone, so the long spelling fell through
	// their kind tables and the edit was silently dropped.
	cases := map[string]struct{ file, content, want string }{
		"podfile": {
			"Podfile",
			"target 'Acme' do\n  pod 'Alamofire', '~> 5.6'\nend\n",
			"pod 'Alamofire', '~> 5.9'",
		},
		"pubspec": {
			"pubspec.yaml",
			"name: acme\ndependencies:\n  http: ^0.13.0\n",
			"http: ^1.0.0",
		},
	}
	ranges := map[string]string{"Alamofire": "~> 5.9", "http": "^1.0.0"}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			for _, kind := range []manifest.Kind{manifest.KindDependencies, manifest.Kind("dependencies")} {
				path := seed(t, tc.file, tc.content)
				dep := "Alamofire"
				if name == "pubspec" {
					dep = "http"
				}
				res, err := Rewrite(path, "", []Edit{{Name: dep, Kind: kind, Range: ranges[dep]}})
				if err != nil {
					t.Fatalf("kind %q: %v", kind, err)
				}
				if len(res.Applied) != 1 {
					t.Fatalf("kind %q: edit dropped: %+v", kind, res)
				}
				if !strings.Contains(read(t, path), tc.want) {
					t.Errorf("kind %q: file missing %q", kind, tc.want)
				}
			}
		})
	}
}

func TestRewriteLeavesTheCallerEditsAlone(t *testing.T) {
	// Canonicalising the long spelling must copy, never write through: the
	// edits slice belongs to the caller.
	path := seed(t, "package.json", `{"name":"a","dependencies":{"x":"^1.0.0"}}`)
	edits := []Edit{{Name: "x", Kind: manifest.Kind("dependencies"), Range: "^2.0.0"}}
	if _, err := Rewrite(path, "", edits); err != nil {
		t.Fatal(err)
	}
	if edits[0].Kind != manifest.Kind("dependencies") {
		t.Errorf("the caller's edit was mutated to %q", edits[0].Kind)
	}
}

func TestNpmRewritePreservesEveryOtherByte(t *testing.T) {
	// Deliberately odd formatting: tabs, blank lines, unsorted keys, spaces
	// inside braces, no trailing newline. Only the three targeted scalars may
	// change.
	src := "{\n\t\"private\": true,\n\n\t\"version\":   \"1.0.0\",\n" +
		"\t\"name\": \"@acme/web\",\n" +
		"\t\"dependencies\": { \"@acme/core\": \"workspace:*\",  \"left-pad\": \"^1.0.0\" },\n" +
		"\t\"devDependencies\": {\n\t\t\"@acme/tools\": \"~0.9.0\"\n\t},\n" +
		"\t\"scripts\": {\"build\": \"tsc\"}\n}"
	path := seed(t, "package.json", src)

	res, err := Rewrite(path, "2.0.0", []Edit{
		{Name: "@acme/core", Range: "2.0.0"},
		{Name: "@acme/tools", Kind: "devDependencies", Range: "^1.1.0"},
		{Name: "ghost", Kind: "peerDependencies", Range: "1.0.0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.NewReplacer(
		`"@acme/core": "workspace:*"`, `"@acme/core": "2.0.0"`,
		`"@acme/tools": "~0.9.0"`, `"@acme/tools": "^1.1.0"`,
		`"version":   "1.0.0"`, `"version":   "2.0.0"`,
	).Replace(src)
	if got := read(t, path); got != want {
		t.Errorf("file mismatch:\n got: %q\nwant: %q", got, want)
	}
	if !res.VersionWritten || len(res.Applied) != 2 {
		t.Errorf("result mismatch: %+v", res)
	}
	if len(res.Missing) != 1 || res.Missing[0].Name != "ghost" {
		t.Errorf("missing mismatch: %+v", res.Missing)
	}
}

func TestNpmRewriteNoChangeLeavesFileAlone(t *testing.T) {
	src := "{\n  \"name\": \"a\",\n  \"version\": \"1.0.0\",\n  \"dependencies\": {\"b\": \"1.0.0\"}\n}\n"
	path := seed(t, "package.json", src)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Rewrite(path, "1.0.0", []Edit{{Name: "b", Range: "1.0.0"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Applied) != 0 || len(res.Missing) != 0 || res.VersionWritten {
		t.Errorf("no-op rewrite reported work: %+v", res)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if read(t, path) != src || !after.ModTime().Equal(before.ModTime()) {
		t.Error("no-op rewrite touched the file")
	}
}

func TestNpmRewriteEscapesAndKeepsMode(t *testing.T) {
	src := `{"dependencies": {"weird\"name": "1.0.0"}}`
	path := seed(t, "package.json", src)
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := Rewrite(path, "", []Edit{{Name: `weird"name`, Range: "2.0.0"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Applied) != 1 {
		t.Fatalf("edit not applied: %+v", res)
	}
	if got, want := read(t, path), `{"dependencies": {"weird\"name": "2.0.0"}}`; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode not preserved: %v", info.Mode())
	}
}

func TestNpmRewriteMissingVersionField(t *testing.T) {
	path := seed(t, "package.json", `{"name": "a", "dependencies": {"b": "1.0.0"}}`)
	res, err := Rewrite(path, "9.9.9", []Edit{{Name: "b", Range: "2.0.0"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.VersionWritten {
		t.Error("cannot have written an absent version field")
	}
	if !strings.Contains(read(t, path), `"b": "2.0.0"`) {
		t.Error("dependency edit lost")
	}
}

func TestNpmRewriteRejectsCompositeVersionValue(t *testing.T) {
	path := seed(t, "package.json", `{"dependencies": {"b": {"version": "1.0.0"}}}`)
	if _, err := Rewrite(path, "", []Edit{{Name: "b", Range: "2.0.0"}}); err == nil {
		t.Error("composite dependency value must fail, not be clobbered")
	}
}

func TestGoModRewritePreservesLayoutAndComments(t *testing.T) {
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
	res, err := Rewrite(path, "", []Edit{
		{Name: "github.com/acme/mono/pkg/core", Range: "v1.3.0"},
		{Name: "github.com/gone/module", Range: "v1.0.0"},
		{Name: "github.com/rs/zerolog", Kind: "devDependencies", Range: "v2.0.0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(src,
		"github.com/acme/mono/pkg/core v1.2.0 // the shared core",
		"github.com/acme/mono/pkg/core v1.3.0 // the shared core", 1)
	if got := read(t, path); got != want {
		t.Errorf("file mismatch:\n got: %q\nwant: %q", got, want)
	}
	if len(res.Applied) != 1 || len(res.Missing) != 2 {
		t.Errorf("result mismatch: %+v", res)
	}
}

func TestGoModRewriteNoChangeLeavesFileAlone(t *testing.T) {
	src := "module example.com/m\n\ngo 1.25.0\n\nrequire example.com/dep v1.0.0\n"
	path := seed(t, "go.mod", src)
	res, err := Rewrite(path, "", []Edit{{Name: "example.com/dep", Range: "v1.0.0"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Applied) != 0 || read(t, path) != src {
		t.Errorf("no-op rewrite touched the file: %+v", res)
	}
}

func TestRequirementsRewritePreservesLayoutAndComments(t *testing.T) {
	src := "# runtime deps\r\n" +
		"requests>=2.0,<3   # http client\n" +
		"Acme_Core==1.0.0\n" +
		"uvicorn[standard]>=0.30\n" +
		"bare-name\n" +
		"-r requirements-dev.txt\n"
	path := seed(t, "requirements.txt", src)
	res, err := Rewrite(path, "9.9.9", []Edit{
		{Name: "acme-core", Range: "==1.0.1"}, // matched via PEP 503 against Acme_Core
		{Name: "bare-name", Range: "==2.0.0"}, // a bare line gains its first specifier
		{Name: "requests", Range: ">=2.0,<3"}, // already the wanted text: untouched
		{Name: "ghost", Range: "==1.0.0"},     // not declared: missing
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "# runtime deps\r\n" +
		"requests>=2.0,<3   # http client\n" +
		"Acme_Core==1.0.1\n" +
		"uvicorn[standard]>=0.30\n" +
		"bare-name==2.0.0\n" +
		"-r requirements-dev.txt\n"
	if got := read(t, path); got != want {
		t.Errorf("file mismatch:\n got: %q\nwant: %q", got, want)
	}
	if len(res.Applied) != 2 || len(res.Missing) != 1 || res.VersionWritten {
		t.Errorf("result mismatch: %+v", res)
	}
}

func TestRewriteDispatch(t *testing.T) {
	if !Supported("a/b/package.json") || !Supported("go.mod") || Supported("settings.gradle") {
		t.Error("Supported dispatches on the file name")
	}
	if !Supported("requirements.txt") || !Supported("dev-requirements.txt") || Supported("readme.txt") {
		t.Error("requirements files are recognised by pattern")
	}
	if _, err := Rewrite("settings.gradle", "", nil); !errors.Is(err, ErrUnsupportedManifest) {
		t.Errorf("unsupported manifests must return ErrUnsupportedManifest, got %v", err)
	}
}

func TestEveryScannedEcosystemHasAWriter(t *testing.T) {
	// The two halves are meant to cover the same ground; this is the list that
	// says so. A format the scanner learns to read should fail here until it
	// can be written too.
	for _, name := range []string{
		"package.json", "go.mod", "requirements.txt", "Cargo.toml", "pyproject.toml",
		"composer.json", "pom.xml", "App.csproj", "pubspec.yaml", "pubspec.yml",
		"Info.plist", "AndroidManifest.xml", "libs.versions.toml", "project.pbxproj",
		"Podfile", "Alamofire.podspec", "build.gradle", "build.gradle.kts",
		"Gemfile", "acme.gemspec", "Acme.nuspec", "Directory.Packages.props",
		"packages.config", "App.fsproj", "App.vbproj",
		"Dockerfile", "Dockerfile.dev", "api.Dockerfile", "Containerfile",
		"compose.yaml", "compose.yml", "docker-compose.yaml", "docker-compose.yml",
		"compose.override.yaml", "docker-compose.override.yml",
	} {
		if !Supported(name) {
			t.Errorf("%s is scanned but has no writer", name)
		}
	}
}

func TestRewriteGuards(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "package.json")
	if err := os.WriteFile(path, []byte(`{"name":"a"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// An Edit targeting a non-dependency field is refused up front: the JSON
	// descent must never be steerable into scripts or arbitrary objects.
	if _, err := Rewrite(path, "", []Edit{{Name: "build", Kind: "scripts", Range: "rm -rf /"}}); err == nil {
		t.Error("an unknown Edit.Kind must be rejected")
	}

	// Result.Path echoes the target so batching callers keep the correlation.
	res, err := Rewrite(path, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Path != path {
		t.Errorf("Result.Path = %q, want %q", res.Path, path)
	}
}

func TestAtomicWriteErrors(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("permission checks are meaningless as root")
	}
	// A missing target fails at the stat that reads its permissions.
	if err := atomicWrite(filepath.Join(t.TempDir(), "absent.json"), []byte("{}")); err == nil {
		t.Error("a missing target must error")
	}
	// An unwritable folder fails at the temp-file stage; the target survives
	// untouched and no temp file is left behind.
	dir := t.TempDir()
	path := filepath.Join(dir, "package.json")
	if err := os.WriteFile(path, []byte(`{"name":"x"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	if err := atomicWrite(path, []byte("{}")); err == nil {
		t.Error("an unwritable folder must error")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"name":"x"}` {
		t.Errorf("target modified by a failed write: %s", data)
	}
}

func TestEveryFormatHasARewriter(t *testing.T) {
	// pkg/manifest names the formats; this is the writer's half of covering
	// them. A format added there fails here until it can be written, which is
	// the drift the shared table exists to prevent.
	for _, f := range manifest.Formats {
		if _, ok := rewriters[f]; !ok {
			t.Errorf("format %q has no writer", f)
		}
	}
	for f := range rewriters {
		if !slices.Contains(manifest.Formats, f) {
			t.Errorf("writer registered for %q, which pkg/manifest does not list", f)
		}
	}
}
