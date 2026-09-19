package publicapi_test

// Shared helpers for the manifest-format scenarios: laying a realistic
// checkout down on disk, and rendering a scanned manifest as text so a case
// can state the whole canonical result in one place instead of a dozen field
// comparisons.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/yohimik/dispat/pkg/scanner"
)

// writeTree writes a set of slash-relative files under dir, creating the
// folders they need, and returns dir so a case can use it inline.
func writeTree(t *testing.T, dir string, files map[string]string) string {
	t.Helper()
	for rel, body := range files {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// writeFile writes one file under dir and returns its absolute path.
func writeFile(t *testing.T, dir, rel, body string) string {
	t.Helper()
	writeTree(t, dir, map[string]string{rel: body})
	return filepath.Join(dir, filepath.FromSlash(rel))
}

// readFile reads a file the test wrote, failing the test rather than the
// caller's control flow.
func readFile(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// renderDeps renders declarations as "kind|name|range|localPath" lines, sorted,
// so a case states the whole dependency set as one comparable block.
func renderDeps(deps []scanner.DeclaredDep) []string {
	out := make([]string, 0, len(deps))
	for _, d := range deps {
		out = append(out, strings.Join([]string{d.Kind.String(), d.Name, d.Range, d.LocalPath}, "|"))
	}
	sort.Strings(out)
	return out
}

// assertDeps compares a manifest's declarations against the expected set,
// order-insensitively.
func assertDeps(t *testing.T, m scanner.Manifest, want ...string) {
	t.Helper()
	sort.Strings(want)
	if got := strings.Join(renderDeps(m.Deps), "\n"); got != strings.Join(want, "\n") {
		t.Errorf("%s dependencies =\n%s\n\nwant:\n%s", m.Path, got, strings.Join(want, "\n"))
	}
}

// assertIdentity compares a manifest's identity fields, spelled
// "name|version|buildNumber".
func assertIdentity(t *testing.T, m scanner.Manifest, want string) {
	t.Helper()
	got := strings.Join([]string{m.Name, m.Version, m.BuildNumber}, "|")
	if got != want {
		t.Errorf("%s identity = %q; want %q", m.Path, got, want)
	}
}

// assertDropped compares the entries a manifest declared but the reader could
// not coerce into a dependency.
func assertDropped(t *testing.T, m scanner.Manifest, want ...string) {
	t.Helper()
	sort.Strings(want)
	if got := strings.Join(m.Dropped, "\n"); got != strings.Join(want, "\n") {
		t.Errorf("%s dropped =\n%s\n\nwant:\n%s", m.Path, got, strings.Join(want, "\n"))
	}
}

// scanOne scans dir and returns the single manifest it holds, failing when the
// scan finds any other number.
func scanOne(t *testing.T, dir string) scanner.Manifest {
	t.Helper()
	mans, err := scanner.Scan(t.Context(), dir)
	if err != nil {
		t.Fatalf("scan %s: %v", dir, err)
	}
	if len(mans) != 1 {
		t.Fatalf("scan %s found %d manifests, want 1: %+v", dir, len(mans), mans)
	}
	return mans[0]
}

// scanFixture writes one manifest into a fresh folder and scans it back.
func scanFixture(t *testing.T, rel, body string) scanner.Manifest {
	t.Helper()
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{rel: body})
	return scanOne(t, dir)
}
