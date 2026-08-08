package scanner

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
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
