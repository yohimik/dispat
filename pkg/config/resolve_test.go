package config

// The ascent: what a directory offers, what a found file turns out to be, and
// which of the files found on the way up is the one to load.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// appResolver is the test language's ascent: a root declares `areas`, a
// candidate declares `hooks` — which a folder's own file declares too — and a
// root owns the folder one of its areas names.
func appResolver() Resolver {
	return Resolver{
		Names:    []string{"app.json", "app.yaml", "app.toml"},
		Classify: MarkerClassify([]string{"areas"}, []string{"hooks"}),
		Owns:     FolderOwner("areas", "path"),
	}
}

func resolve(t *testing.T, dir string, r Resolver) (string, string, error) {
	t.Helper()
	return NewLoader(Options{}).Resolve(t.Context(), dir, r)
}

// TestResolveFindsTheFileInTheDirectoryItself: the common case, and the name
// order within one folder is what decides between two files sitting in it.
func TestResolveFindsTheFileInTheDirectoryItself(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "app.yaml", "areas:\n  libs:\n    path: pkgs\n")
	want := writeFile(t, dir, "app.json", `{"areas": {"libs": {"path": "pkgs"}}}`)

	path, root, err := resolve(t, dir, appResolver())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if path != want || root != dir {
		t.Errorf("resolve = %q, %q; want %q, %q", path, root, want, dir)
	}
}

// TestResolveAscendsToTheRoot: the ascent is what lets a command run from
// inside a sub-folder, with the config's own directory becoming the root.
func TestResolveAscendsToTheRoot(t *testing.T) {
	dir := t.TempDir()
	want := writeFile(t, dir, "app.json", `{"areas": {"libs": {"path": "pkgs"}}}`)
	deep := filepath.Join(dir, "pkgs", "core", "src")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}

	path, root, err := resolve(t, deep, appResolver())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if path != want || root != dir {
		t.Errorf("resolve = %q, %q; want %q, %q", path, root, want, dir)
	}
}

// TestResolveStopsAtABrokenFile: a file that cannot be read is broken rather
// than skippable — loading is where a broken config fails loudly, and stepping
// over it to use a parent's file would hide the breakage.
func TestResolveStopsAtABrokenFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "app.json", `{"areas": {"libs": {"path": "pkgs"}}}`)
	sub := filepath.Join(dir, "pkgs")
	broken := writeFile(t, sub, "app.json", "{not json")

	path, root, err := resolve(t, sub, appResolver())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if path != broken || root != sub {
		t.Errorf("resolve = %q, %q; want the broken file %q", path, root, broken)
	}
}

// TestResolveRemembersACandidateUntilARootClaimsIt: a folder's own file
// declares what a root of standalone entries declares, so it is remembered and
// the ascent goes on; a root above it wins only when it owns the folder.
func TestResolveRemembersACandidateUntilARootClaimsIt(t *testing.T) {
	t.Run("the root owns the folder", func(t *testing.T) {
		dir := t.TempDir()
		root := writeFile(t, dir, "app.json", `{"areas": {"libs": {"path": "pkgs"}}}`)
		sub := filepath.Join(dir, "pkgs")
		writeFile(t, sub, "app.json", `{"hooks": [{"url": "u"}]}`)

		path, at, err := resolve(t, sub, appResolver())
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if path != root || at != dir {
			t.Errorf("resolve = %q, %q; want the owning root %q", path, at, root)
		}
	})

	t.Run("the root names some other folder", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "app.json", `{"areas": {"libs": {"path": "elsewhere"}}}`)
		sub := filepath.Join(dir, "pkgs")
		own := writeFile(t, sub, "app.json", `{"hooks": [{"url": "u"}]}`)

		path, at, err := resolve(t, sub, appResolver())
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if path != own || at != sub {
			t.Errorf("resolve = %q, %q; want the folder's own %q", path, at, own)
		}
	})

	t.Run("the root claims a symlinked folder", func(t *testing.T) {
		dir := t.TempDir()
		real := filepath.Join(dir, "real")
		if err := os.MkdirAll(real, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(real, filepath.Join(dir, "linked")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		root := writeFile(t, dir, "app.json", `{"areas": {"libs": {"path": ["other", "linked"]}}}`)
		writeFile(t, real, "app.json", `{"hooks": [{"url": "u"}]}`)

		// Folders are compared by identity, so a space reached by one name owns
		// the file found under the other.
		path, _, err := resolve(t, real, appResolver())
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if path != root {
			t.Errorf("resolve = %q, want the owning root %q", path, root)
		}
	})
}

// TestResolveFallsBackToTheNearestFile: when no file on the way up declares
// anything, the nearest one found is returned anyway — whatever the caller's
// own validation says about it is the real mistake, and a better message than
// this one could be.
func TestResolveFallsBackToTheNearestFile(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "pkgs", "core")
	near := writeFile(t, sub, "app.json", `{"name": "core"}`)
	writeFile(t, dir, "app.json", `{"name": "root"}`)

	path, at, err := resolve(t, sub, appResolver())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if path != near || at != sub {
		t.Errorf("resolve = %q, %q; want the nearest %q", path, at, near)
	}
}

// TestResolveCandidateOutranksAFallback: a file that could be a root beats one
// that declares nothing, however much nearer the second is.
func TestResolveCandidateOutranksAFallback(t *testing.T) {
	dir := t.TempDir()
	mid := filepath.Join(dir, "pkgs")
	deep := filepath.Join(mid, "core")
	writeFile(t, deep, "app.json", `{"name": "core"}`)
	candidate := writeFile(t, mid, "app.json", `{"hooks": [{"url": "u"}]}`)

	path, at, err := resolve(t, deep, appResolver())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if path != candidate || at != mid {
		t.Errorf("resolve = %q, %q; want %q", path, at, candidate)
	}
}

// TestResolveFindsNothing: the error names the directory it started in and
// every candidate tried, because that is what the reader has to write.
func TestResolveFindsNothing(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "empty")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	_, _, err := resolve(t, sub, appResolver())
	if !errors.Is(err, ErrNoConfig) {
		t.Fatalf("err = %v, want ErrNoConfig", err)
	}
	var none *NoConfigError
	if !errors.As(err, &none) || none.Dir != sub {
		t.Fatalf("err = %#v", none)
	}
	want := "no config file found in " + sub +
		" or any parent directory (tried app.json, app.yaml, app.toml)"
	if err.Error() != want {
		t.Errorf("err = %q, want %q", err, want)
	}
}

// TestResolveCandidatesErrorsComeBackUnchanged: the probe is the caller's, and
// so is the wording of what went wrong inside it.
func TestResolveCandidatesErrorsComeBackUnchanged(t *testing.T) {
	mine := errors.New("the folder's exclude file is unreadable")
	r := appResolver()
	r.Candidates = func(string) ([]string, error) { return nil, mine }

	_, _, err := resolve(t, t.TempDir(), r)
	if !errors.Is(err, mine) {
		t.Fatalf("err = %v, want the caller's own", err)
	}
}

// TestResolveCandidatesMayExcludeAName: the probe is a function rather than a
// directory listing so that a folder holding two config files can say which
// one is real.
func TestResolveCandidatesMayExcludeAName(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "app.json", `{"areas": {"libs": {"path": "pkgs"}}}`)
	want := writeFile(t, dir, "app.yaml", "areas:\n  libs:\n    path: pkgs\n")

	r := appResolver()
	r.Candidates = func(at string) ([]string, error) {
		var present []string
		for _, name := range r.Names {
			if name == "app.json" {
				continue
			}
			if _, err := os.Stat(filepath.Join(at, name)); err == nil {
				present = append(present, name)
			}
		}
		return present, nil
	}

	path, _, err := resolve(t, dir, r)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if path != want {
		t.Errorf("resolve = %q, want %q", path, want)
	}
}

// TestResolveWithoutAClassifierTakesTheFirstFile: a language with no override
// layers has nothing to classify, and the ascent stops at the first file it
// finds.
func TestResolveWithoutAClassifierTakesTheFirstFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "app.json", `{"name": "root"}`)
	sub := filepath.Join(dir, "pkgs")
	want := writeFile(t, sub, "app.json", `{"name": "sub"}`)

	path, at, err := resolve(t, sub, Resolver{Names: []string{"app.json"}})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if path != want || at != sub {
		t.Errorf("resolve = %q, %q; want %q", path, at, want)
	}
}

// TestResolveWithoutAnOwnerKeepsTheCandidate: a nil Owns claims nothing, so a
// remembered candidate always wins over an ancestor root.
func TestResolveWithoutAnOwnerKeepsTheCandidate(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "app.json", `{"areas": {"libs": {"path": "pkgs"}}}`)
	sub := filepath.Join(dir, "pkgs")
	want := writeFile(t, sub, "app.json", `{"hooks": [{"url": "u"}]}`)

	r := appResolver()
	r.Owns = nil
	path, _, err := resolve(t, sub, r)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if path != want {
		t.Errorf("resolve = %q, want the candidate %q", path, want)
	}
}

// TestMarkerClassify: a key holding null is not a declaration, which is the
// rule the rest of the language follows too.
func TestMarkerClassify(t *testing.T) {
	classify := MarkerClassify([]string{"areas"}, []string{"hooks"})
	for _, tc := range []struct {
		name string
		root map[string]any
		want Class
	}{
		{"a root", map[string]any{"AREAS": map[string]any{"libs": nil}}, ClassRoot},
		{"a candidate", map[string]any{"Hooks": []any{}}, ClassCandidate},
		{"neither", map[string]any{"name": "n"}, ClassFallback},
		{"an empty declaration", map[string]any{"areas": nil, "hooks": nil}, ClassFallback},
		{"both", map[string]any{"areas": map[string]any{}, "hooks": []any{}}, ClassRoot},
	} {
		if got := classify(tc.root); got != tc.want {
			t.Errorf("%s: classify = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestFolderOwnerReadsTheRawShape: the collection is read before any decode
// has typed it, so it takes the same "one folder or a list of them" shape the
// setter would.
func TestFolderOwnerReadsTheRawShape(t *testing.T) {
	dir := t.TempDir()
	owned := filepath.Join(dir, "pkgs")
	other := filepath.Join(dir, "other")
	for _, d := range []string{owned, other} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	owns := FolderOwner("areas", "path")

	for _, tc := range []struct {
		name string
		root map[string]any
		dir  string
		want bool
	}{
		{"a scalar path", map[string]any{"areas": map[string]any{"libs": map[string]any{"path": "pkgs"}}}, owned, true},
		{"a list of paths", map[string]any{"areas": map[string]any{
			"libs": map[string]any{"Path": []any{"", "pkgs"}}}}, owned, true},
		{"some other folder", map[string]any{"areas": map[string]any{
			"libs": map[string]any{"path": "pkgs"}}}, other, false},
		{"no collection", map[string]any{"name": "n"}, owned, false},
		{"the collection is not an object", map[string]any{"areas": "n"}, owned, false},
		{"an entry that is not an object", map[string]any{"areas": map[string]any{"libs": "n"}}, owned, false},
		{"an entry with no path", map[string]any{"areas": map[string]any{"libs": map[string]any{}}}, owned, false},
		{"a path of the wrong shape", map[string]any{"areas": map[string]any{
			"libs": map[string]any{"path": []any{7}}}}, owned, false},
		{"a path that is a number", map[string]any{"areas": map[string]any{
			"libs": map[string]any{"path": 7}}}, owned, false},
		{"a folder that is not there", map[string]any{"areas": map[string]any{
			"libs": map[string]any{"path": "pkgs"}}}, filepath.Join(dir, "absent"), false},
	} {
		if got := owns(tc.root, dir, tc.dir); got != tc.want {
			t.Errorf("%s: owns = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestAscentEndsAtTheFilesystemRoot: the walk beyond the starting directory is
// absolute, which makes it well-defined wherever a relative start pointed.
func TestAscentEndsAtTheFilesystemRoot(t *testing.T) {
	dirs := ascent(filepath.Join(t.TempDir(), "a", "b"))
	if len(dirs) < 3 {
		t.Fatalf("ascent = %#v", dirs)
	}
	last := dirs[len(dirs)-1]
	if last != filepath.Dir(last) {
		t.Errorf("the walk ended at %q, which is not the filesystem root", last)
	}
	if !strings.HasSuffix(dirs[0], filepath.Join("a", "b")) {
		t.Errorf("the walk started at %q", dirs[0])
	}
}
