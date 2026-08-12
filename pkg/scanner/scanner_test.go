package scanner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/yohimik/dispat/pkg/manifest"
)

// write lays a file down under dir, creating parents.
func write(t *testing.T, dir, rel, content string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func scanOne(t *testing.T, dir string) Manifest {
	t.Helper()
	mans, err := New().Scan(context.Background(), dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(mans) != 1 {
		t.Fatalf("Scan returned %d manifests, want 1: %+v", len(mans), mans)
	}
	return mans[0]
}

func TestNpmAllFieldsAndLocalPaths(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "package.json", `{
		"name": "@acme/web",
		"version": "1.2.3",
		"dependencies": {"@acme/core": "workspace:*", "left-pad": "^1.3.0"},
		"devDependencies": {"@acme/tools": "file:../tools"},
		"peerDependencies": {"react": ">=18"},
		"optionalDependencies": {"fsevents": "^2.0.0"}
	}`)
	m := scanOne(t, dir)
	want := Manifest{
		Path: "package.json", Ecosystem: EcosystemNpm,
		Name: "@acme/web", Version: "1.2.3", Root: true,
		Deps: []DeclaredDep{
			{Name: "@acme/core", Range: "workspace:*", Kind: KindDependencies},
			{Name: "left-pad", Range: "^1.3.0", Kind: KindDependencies},
			{Name: "@acme/tools", Range: "file:../tools", Kind: KindDevDependencies, LocalPath: "../tools"},
			{Name: "react", Range: ">=18", Kind: KindPeerDependencies},
			{Name: "fsevents", Range: "^2.0.0", Kind: KindOptionalDependencies},
		},
	}
	if !reflect.DeepEqual(m, want) {
		t.Errorf("manifest mismatch:\n got %+v\nwant %+v", m, want)
	}
}

func TestGoModRequiresAndReplaces(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "go.mod", `module github.com/acme/mono/services/svc

go 1.25.0

require (
	github.com/acme/mono/pkg/core v1.2.0
	github.com/rs/zerolog v1.35.1
)

require golang.org/x/sys v0.47.0 // indirect

require github.com/acme/mono/pkg/hidden v0.1.0 // indirect

replace github.com/acme/mono/pkg/core => ../../pkg/core

replace github.com/acme/mono/pkg/hidden => ../../pkg/hidden

replace github.com/rs/zerolog => github.com/fork/zerolog v1.0.0
`)
	m := scanOne(t, dir)
	want := Manifest{
		Path: "go.mod", Ecosystem: EcosystemGoMod,
		Name: "github.com/acme/mono/services/svc", Root: true,
		Deps: []DeclaredDep{
			// direct with local replace, direct registry, and the indirect
			// require kept only because a relative replace pins it locally
			{Name: "github.com/acme/mono/pkg/core", Range: "v1.2.0", Kind: KindDependencies, LocalPath: "../../pkg/core"},
			{Name: "github.com/acme/mono/pkg/hidden", Range: "v0.1.0", Kind: KindDependencies, LocalPath: "../../pkg/hidden"},
			{Name: "github.com/rs/zerolog", Range: "v1.35.1", Kind: KindDependencies},
		},
		// An indirect require with nothing wiring it locally is bookkeeping,
		// reported apart from the declarations and never in both.
		Indirect: []DeclaredDep{
			{Name: "golang.org/x/sys", Range: "v0.47.0", Kind: KindDependencies},
		},
	}
	if !reflect.DeepEqual(m, want) {
		t.Errorf("manifest mismatch:\n got %+v\nwant %+v", m, want)
	}
}

func TestCargoTablesRenamesAndPaths(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "Cargo.toml", `[package]
name = "acme-cli"
version = "0.3.1"

[dependencies]
serde = "1.0"
core = { path = "../core", version = "0.3" }
pretty = { package = "acme-pretty", version = "0.1" }

[dev-dependencies]
insta = "1.34"

[build-dependencies]
cc = "1.0"
`)
	m := scanOne(t, dir)
	want := Manifest{
		Path: "Cargo.toml", Ecosystem: EcosystemCargo,
		Name: "acme-cli", Version: "0.3.1", Root: true,
		Deps: []DeclaredDep{
			{Name: "acme-pretty", Range: "0.1", Kind: KindDependencies},
			// build-dependencies count as plain dependencies
			{Name: "cc", Range: "1.0", Kind: KindDependencies},
			{Name: "core", Range: "0.3", Kind: KindDependencies, LocalPath: "../core"},
			{Name: "serde", Range: "1.0", Kind: KindDependencies},
			{Name: "insta", Range: "1.34", Kind: KindDevDependencies},
		},
	}
	if !reflect.DeepEqual(m, want) {
		t.Errorf("manifest mismatch:\n got %+v\nwant %+v", m, want)
	}
}

func TestCargoWorkspaceVersionAndBareDeps(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "Cargo.toml", `[package]
name = "member"
version = { workspace = true }

[dependencies]
util = { workspace = true }
`)
	m := scanOne(t, dir)
	if m.Version != "" {
		t.Errorf("workspace-inherited version should read as empty, got %q", m.Version)
	}
	want := []DeclaredDep{{Name: "util", Kind: KindDependencies}}
	if !reflect.DeepEqual(m.Deps, want) {
		t.Errorf("deps mismatch:\n got %+v\nwant %+v", m.Deps, want)
	}
}

func TestScanWalksAndSkips(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "package.json", `{"name": "root"}`)
	write(t, dir, "sub/inner/package.json", `{"name": "inner"}`)
	write(t, dir, "go.mod", "module example.com/root\n")
	// All of these must be invisible.
	write(t, dir, "node_modules/dep/package.json", `{"name": "dep"}`)
	write(t, dir, "vendor/mod/go.mod", "module vendored\n")
	write(t, dir, "target/Cargo.toml", "[package]\nname = \"built\"\n")
	write(t, dir, ".git/package.json", `broken on purpose`)
	write(t, dir, "README.md", "not a manifest")

	mans, err := New().Scan(context.Background(), dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	var got []string
	for _, m := range mans {
		got = append(got, m.Path)
	}
	want := []string{"go.mod", "package.json", "sub/inner/package.json"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("paths = %v, want %v", got, want)
	}
	if !mans[0].Root || !mans[1].Root || mans[2].Root {
		t.Errorf("root flags wrong: %+v", mans)
	}
}

func TestScanReturnsPartialResultOnMalformedManifest(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "package.json", `{"name": "good"}`)
	write(t, dir, "bad/package.json", `{not json`)
	write(t, dir, "worse/go.mod", "module \x00 nonsense {\n")

	mans, err := New().Scan(context.Background(), dir)
	if err == nil {
		t.Fatal("want a joined parse error")
	}
	for _, rel := range []string{"bad/package.json", "worse/go.mod"} {
		if !strings.Contains(err.Error(), rel) {
			t.Errorf("error does not name %s: %v", rel, err)
		}
	}
	if len(mans) != 1 || mans[0].Name != "good" {
		t.Errorf("partial result lost: %+v", mans)
	}
}

func TestScanStepsOverAnUnreadableFolder(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("permission checks are meaningless as root")
	}
	// A sub-tree the walk cannot enter is one more partial result, not the end
	// of the scan: the manifests past it (sorted after it, so the walk really
	// does have to continue) must still come back, with the failure reported.
	dir := t.TempDir()
	write(t, dir, "package.json", `{"name": "root"}`)
	write(t, dir, "locked/package.json", `{"name": "hidden"}`)
	write(t, dir, "zzz/package.json", `{"name": "past-it"}`)
	locked := filepath.Join(dir, "locked")
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	mans, err := New().Scan(context.Background(), dir)
	if err == nil {
		t.Fatal("want the unreadable folder reported")
	}
	var names []string
	for _, m := range mans {
		names = append(names, m.Name)
	}
	if !slices.Contains(names, "root") || !slices.Contains(names, "past-it") {
		t.Errorf("the walk was truncated at the unreadable folder: %v", names)
	}
	if slices.Contains(names, "hidden") {
		t.Errorf("nothing may be read out of the unreadable folder: %v", names)
	}
}

func TestScanHonoursContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dir := t.TempDir()
	write(t, dir, "package.json", `{"name": "x"}`)
	if _, err := New().Scan(ctx, dir); err == nil {
		t.Error("cancelled context should fail the scan")
	}
}

func TestKindString(t *testing.T) {
	if KindDependencies.String() != "dependencies" {
		t.Error("zero kind spells out")
	}
	if KindDevDependencies.String() != "devDependencies" {
		t.Error("named kinds pass through")
	}
}

func TestNameIndex(t *testing.T) {
	owners := []Owner{
		{Package: "core", Manifests: []Manifest{
			{Name: "@acme/core", Root: true},
			{Name: "nested-example", Root: false},
		}},
		{Package: "web", Manifests: []Manifest{
			{Name: "@acme/web", Root: true},
			{Name: "", Root: true}, // unnamed manifests bind nothing
		}},
		{Package: "clone", Manifests: []Manifest{
			{Name: "@acme/web", Root: true}, // collides with web at root priority
		}},
		{Package: "vendored", Manifests: []Manifest{
			{Name: "@acme/core", Root: false}, // nested loses to core's root claim
		}},
	}
	names, ambiguous := NameIndex(owners)
	if got := names["@acme/core"]; got != "core" {
		t.Errorf("@acme/core -> %q, want core (root binds before nested)", got)
	}
	if got := names["nested-example"]; got != "core" {
		t.Errorf("nested-example -> %q, want core", got)
	}
	if _, ok := names["@acme/web"]; ok {
		t.Error("@acme/web is ambiguous at root priority and must not be mapped")
	}
	if !reflect.DeepEqual(ambiguous, []string{"@acme/web"}) {
		t.Errorf("ambiguous = %v, want [@acme/web]", ambiguous)
	}
}

func TestNameIndexStatedNamesOutrankDeclaredOnes(t *testing.T) {
	owners := []Owner{
		// A package whose manifests declare nothing a workspace can learn:
		// stating the name is the only way it becomes visible.
		{Package: "gradle-lib", Names: []string{"com.acme:core"}},
		// A stated name beats another package's root manifest, since it is
		// the operator saying so rather than a file happening to say it.
		{Package: "renamed", Names: []string{"@acme/web"}},
		{Package: "web", Manifests: []Manifest{{Name: "@acme/web", Root: true}}},
	}
	names, ambiguous := NameIndex(owners)
	if got := names["com.acme:core"]; got != "gradle-lib" {
		t.Errorf("com.acme:core -> %q, want gradle-lib", got)
	}
	if got := names["@acme/web"]; got != "renamed" {
		t.Errorf("@acme/web -> %q, want renamed (a stated name outranks a declared one)", got)
	}
	if len(ambiguous) != 0 {
		t.Errorf("ambiguous = %v, want none: the ranks separate the two claims", ambiguous)
	}
}

func TestNameIndexTwoStatedNamesCollide(t *testing.T) {
	// Nothing separates two claims of equal rank, so the name maps to
	// neither, exactly as two root manifests would.
	names, ambiguous := NameIndex([]Owner{
		{Package: "a", Names: []string{"shared"}},
		{Package: "b", Names: []string{"shared"}},
		{Package: "c", Names: []string{"shared"}},
	})
	if _, ok := names["shared"]; ok {
		t.Error("a name two packages state must not be mapped")
	}
	if !reflect.DeepEqual(ambiguous, []string{"shared"}) {
		t.Errorf("ambiguous = %v, want [shared] once", ambiguous)
	}
}

func TestResolveLocalDir(t *testing.T) {
	root := filepath.FromSlash("/repo")
	dirs := map[string]string{
		filepath.Clean(filepath.Join(root, "libs/core")): "core",
		filepath.Clean(filepath.Join(root, "apps/web")):  "web",
	}
	web := filepath.Join(root, "apps/web")
	for _, tc := range []struct {
		manifestRel, local, want string
	}{
		{"package.json", "../../libs/core", "core"},                    // exact folder
		{"package.json", "../../libs/core/dist", "core"},               // sub-folder ascends
		{"nested/inner/package.json", "../../../../libs/core", "core"}, // manifest below the root
		{"package.json", ".", "web"},                                   // self
		{"package.json", "../../elsewhere", ""},                        // outside every package
	} {
		if got := ResolveLocalDir(dirs, web, tc.manifestRel, tc.local); got != tc.want {
			t.Errorf("ResolveLocalDir(%q, %q) = %q, want %q", tc.manifestRel, tc.local, got, tc.want)
		}
	}
}

func TestReadManifestErrors(t *testing.T) {
	dir := t.TempDir()
	if _, err := readManifest(filepath.Join(dir, "absent.json")); err == nil {
		t.Error("a missing manifest must error")
	}
	big := filepath.Join(dir, "package.json")
	f, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	// A sparse file over the cap: the size guard must reject it before any
	// read, so the fixture costs no real disk.
	if err := f.Truncate(maxManifestBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = readManifest(big)
	if !errors.Is(err, ErrManifestTooLarge) {
		t.Errorf("oversized manifest: got %v, want ErrManifestTooLarge", err)
	}
}

func TestScanRootErrorPaths(t *testing.T) {
	if _, err := New().ScanRoot(context.Background(), filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("an unreadable folder must error")
	}

	dir := t.TempDir()
	write(t, dir, "package.json", `{`)
	write(t, dir, "pubspec.yaml", "name: ok\n")
	mans, err := ScanRoot(context.Background(), dir) // package-level convenience
	if err == nil {
		t.Error("a malformed manifest must surface in the joined error")
	}
	if len(mans) != 1 || mans[0].Name != "ok" {
		t.Errorf("the parseable manifest is still returned: %+v", mans)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := New().ScanRoot(ctx, dir); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled scan: got %v, want context.Canceled", err)
	}
}

func TestEveryFormatHasAParser(t *testing.T) {
	// pkg/manifest names the formats; this is the reader's half of covering
	// them. The writer keeps the matching list, so the two cannot drift.
	for _, f := range manifest.Formats {
		if _, ok := parsers[f]; !ok {
			t.Errorf("format %q has no parser", f)
		}
	}
	for f := range parsers {
		if !slices.Contains(manifest.Formats, f) {
			t.Errorf("parser registered for %q, which pkg/manifest does not list", f)
		}
	}
}
