package scanner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestParseAquaLiteralPinsAndRegistryIdentity(t *testing.T) {
	m, err := parseAqua("aqua.yaml", []byte(`packages:
- name: cli/cli@v2.1.0
  version: ignored
- name: tool/name
  registry: private
  version: "1.2.3"
- name: dynamic/tool
  version_expr: env.VERSION
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Deps) != 2 || m.Deps[0].Name != "cli/cli" || m.Deps[0].Range != "v2.1.0" || m.Deps[1].Name != "private:tool/name" {
		t.Fatalf("unexpected deps: %#v", m.Deps)
	}
	if len(m.Dropped) != 1 {
		t.Fatalf("expected dynamic explanation: %#v", m.Dropped)
	}
}

func TestScanAquaImportsDotDirectoryCyclesAndContainment(t *testing.T) {
	d := t.TempDir()
	if err := os.Mkdir(filepath.Join(d, ".aqua"), 0755); err != nil {
		t.Fatal(err)
	}
	mustWrite := func(path, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(d, path), []byte(text), 0644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("aqua.yaml", "packages:\n- import: .aqua/*.yml\n- import: ../outside.yml\n")
	mustWrite(".aqua/tools.yml", "packages:\n- name: cli/cli@v2.0.0\n- import: ../aqua.yaml\n")
	mans, err := Scan(context.Background(), d)
	if err == nil {
		t.Fatal("expected containment error")
	}
	if len(mans) != 2 || mans[0].Path != ".aqua/tools.yml" || mans[1].Path != "aqua.yaml" {
		t.Fatalf("unexpected manifests: %#v", mans)
	}
}

func TestScanAquaImportWithNoMatchesIsCompatibleNoop(t *testing.T) {
	d := t.TempDir()
	if err := os.WriteFile(filepath.Join(d, "aqua.yaml"), []byte("packages:\n- import: missing/*.yaml\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mans, err := Scan(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	if len(mans) != 1 || mans[0].Path != "aqua.yaml" || len(mans[0].Deps) != 0 {
		t.Fatalf("zero-match import changed the root manifest: %#v", mans)
	}
}

func TestScanRootIncludesConventionalAquaDirectoryAndImports(t *testing.T) {
	d := t.TempDir()
	if err := os.Mkdir(filepath.Join(d, ".aqua"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, ".aqua", "aqua.yaml"), []byte("packages:\n- import: tools.inc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, ".aqua", "tools.inc"), []byte("packages:\n- name: cli/cli@v2.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mans, err := ScanRoot(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	if len(mans) != 2 || mans[0].Path != ".aqua/aqua.yaml" || mans[1].Path != ".aqua/tools.inc" {
		t.Fatalf("unexpected manifests: %#v", mans)
	}
}

func TestScanAquaConventionalSymlinksHaveContainedCanonicalOwnership(t *testing.T) {
	d := t.TempDir()
	real := filepath.Join(d, "aqua.yaml")
	if err := os.WriteFile(real, []byte("packages:\n- name: cli/cli@v1.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(d, ".aqua.yaml")); err != nil {
		t.Fatal(err)
	}
	mans, err := Scan(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	if len(mans) != 1 || mans[0].Path != "aqua.yaml" {
		t.Fatalf("canonical Aqua source does not have one deterministic owner: %#v", mans)
	}

	outside := filepath.Join(t.TempDir(), "outside.yaml")
	if err := os.WriteFile(outside, []byte("packages:\n- name: secret/tool@v9\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(d, ".aqua.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(d, ".aqua.yaml")); err != nil {
		t.Fatal(err)
	}
	mans, err = Scan(context.Background(), d)
	if err == nil {
		t.Fatal("expected an escaping conventional Aqua symlink to be refused")
	}
	for _, m := range mans {
		for _, dep := range m.Deps {
			if dep.Name == "secret/tool" {
				t.Fatalf("outside Aqua contents leaked into scan results: %#v", mans)
			}
		}
	}
}

func TestParseAquaRejectsMalformedPackages(t *testing.T) {
	if _, err := parseAqua("aqua.yaml", []byte("packages: nope\n")); err == nil {
		t.Fatal("expected error")
	}
}

func TestParseAquaOrdersDuplicateIdentitiesByVersion(t *testing.T) {
	m, err := parseAqua("aqua.yaml", []byte(`packages:
- name: cli/cli@v2.0.0
- name: cli/cli@v1.0.0
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Deps) != 2 || m.Deps[0].Range != "v1.0.0" || m.Deps[1].Range != "v2.0.0" {
		t.Fatalf("duplicate identities are not totally ordered: %#v", m.Deps)
	}
}

func FuzzParseAquaBounded(f *testing.F) {
	f.Add([]byte("packages:\n- name: cli/cli@v1.0.0\n"))
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) > 1<<20 {
			return
		}
		_, _ = parseAqua("aqua.yaml", b)
	})
}
