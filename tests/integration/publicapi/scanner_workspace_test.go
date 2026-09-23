package publicapi_test

// The scanner's workspace behaviour: which folders a walk enters, what it does
// with a file it cannot read, how an Aqua configuration's local imports are
// followed without ever leaving the scanned tree, and the two index helpers a
// caller builds a dependency graph with.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yohimik/dispat/pkg/scanner"
)

func TestPublicAPIScannerWalkSkipsAndReportsWithoutStopping(t *testing.T) {
	dir := writeTree(t, t.TempDir(), map[string]string{
		"README.md":                          "# Acme\n",
		"package.json":                       `{"name":"acme","version":"1.0.0"}`,
		"packages/core/package.json":         `{"name":"@acme/core","version":"1.0.0"}`,
		"node_modules/left-pad/package.json": `{"name":"left-pad","version":"1.3.0"}`,
		"vendor/acme/composer.json":          `{"name":"vendored/acme"}`,
		"dist/package.json":                  `{"name":"built","version":"1.0.0"}`,
		".git/package.json":                  `{"name":"git","version":"1.0.0"}`,
		"Binaries/Acme.uproject":             `{"FileVersion":3}`,
		"Saved/Acme.uproject":                `{"FileVersion":3}`,
	})
	// A folder wearing a manifest's name is not a manifest, and neither is a
	// link to one: the walk sees a link as a file and the reader has to refuse
	// it on its own.
	if err := os.MkdirAll(filepath.Join(dir, "tools", "Podfile"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "tools", "Podfile"), filepath.Join(dir, "Podfile")); err != nil {
		t.Fatal(err)
	}

	mans, err := scanner.New().Scan(t.Context(), dir)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	var paths []string
	for _, m := range mans {
		paths = append(paths, m.Path)
	}
	want := "package.json,packages/core/package.json"
	if strings.Join(paths, ",") != want {
		t.Fatalf("scan found %q; want %q", strings.Join(paths, ","), want)
	}

	for _, name := range []string{"node_modules", "vendor", "dist", ".git", ".idea"} {
		if !scanner.IsSkippedDir(name) || !scanner.SkipDir(name) {
			t.Errorf("%s is not skipped", name)
		}
		if !scanner.IsSkippedWorkspaceDir(name) || !scanner.SkipWorkspaceDir(name) {
			t.Errorf("%s is not skipped by a workspace walk", name)
		}
	}
	for _, name := range []string{"Binaries", "Saved", "Intermediate"} {
		if scanner.IsSkippedDir(name) {
			t.Errorf("%s must stay visible to a literal-text walk", name)
		}
		if !scanner.IsSkippedWorkspaceDir(name) {
			t.Errorf("%s must be skipped by a manifest walk", name)
		}
	}
	for _, name := range []string{"packages", "src", "app"} {
		if scanner.IsSkippedDir(name) || scanner.IsSkippedWorkspaceDir(name) {
			t.Errorf("%s must be walked", name)
		}
	}
}

func TestPublicAPIScannerReportsUnreadableEntriesAndKeepsGoing(t *testing.T) {
	dir := writeTree(t, t.TempDir(), map[string]string{
		"package.json":             `{"name":"acme","version":"1.0.0"}`,
		"blocked/package.json":     `{"name":"blocked","version":"1.0.0"}`,
		"locked/package.json":      `{"name":"locked","version":"1.0.0"}`,
		"packages/ok/package.json": `{"name":"@acme/ok","version":"1.0.0"}`,
	})
	if err := os.Chmod(filepath.Join(dir, "blocked"), 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(filepath.Join(dir, "blocked"), 0o755)
	if err := os.Chmod(filepath.Join(dir, "locked", "package.json"), 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(filepath.Join(dir, "locked", "package.json"), 0o600)

	mans, err := scanner.Scan(t.Context(), dir)
	if err == nil {
		t.Fatal("an unreadable folder and file were not reported")
	}
	var paths []string
	for _, m := range mans {
		paths = append(paths, m.Path)
	}
	if strings.Join(paths, ",") != "package.json,packages/ok/package.json" {
		t.Fatalf("partial result = %q (err %v)", paths, err)
	}
}

func TestPublicAPIScannerRefusesAnOversizedManifestInAWalk(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"package.json": `{"name":"acme","version":"1.0.0"}`})
	big := filepath.Join(dir, "huge", "package.json")
	if err := os.MkdirAll(filepath.Dir(big), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(big, make([]byte, (16<<20)+1), 0o644); err != nil {
		t.Fatal(err)
	}
	mans, err := scanner.Scan(t.Context(), dir)
	if !errors.Is(err, scanner.ErrManifestTooLarge) {
		t.Fatalf("oversized manifest error = %v", err)
	}
	if len(mans) != 1 || mans[0].Path != "package.json" {
		t.Fatalf("partial result = %+v", mans)
	}
}

func TestPublicAPIScannerHonoursACancelledContext(t *testing.T) {
	dir := writeTree(t, t.TempDir(), map[string]string{
		"package.json":        `{"name":"acme","version":"1.0.0"}`,
		"aqua/aqua.yaml":      "packages:\n  - name: cli/cli@v2.55.0\n",
		"nested/package.json": `{"name":"nested","version":"1.0.0"}`,
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	mans, err := scanner.Scan(ctx, dir)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled walk error = %v", err)
	}
	if len(mans) != 0 {
		t.Fatalf("cancelled walk returned %+v", mans)
	}
	mans, err = scanner.ScanRoot(ctx, dir)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled root scan error = %v", err)
	}
	if len(mans) != 0 {
		t.Fatalf("cancelled root scan returned %+v", mans)
	}
}

func TestPublicAPIScannerRootScanReadsOnlyTheFolderItself(t *testing.T) {
	dir := writeTree(t, t.TempDir(), map[string]string{
		"package.json":        `{"name":"acme","version":"1.0.0"}`,
		"README.md":           "# Acme\n",
		"nested/package.json": `{"name":"nested","version":"1.0.0"}`,
		"aqua/aqua.yaml":      "packages:\n  - name: cli/cli@v2.55.0\n",
	})
	// A folder wearing a manifest's name is stepped over, not read.
	if err := os.Mkdir(filepath.Join(dir, "Podfile"), 0o755); err != nil {
		t.Fatal(err)
	}
	mans, err := scanner.New().ScanRoot(t.Context(), dir)
	if err != nil {
		t.Fatalf("root scan: %v", err)
	}
	var paths []string
	for _, m := range mans {
		paths = append(paths, m.Path)
	}
	if strings.Join(paths, ",") != "aqua/aqua.yaml,package.json" {
		t.Fatalf("root scan found %q", paths)
	}

	t.Run("a missing folder is an error rather than an empty result", func(t *testing.T) {
		if _, err := scanner.ScanRoot(t.Context(), filepath.Join(dir, "absent")); err == nil {
			t.Fatal("a missing folder scanned successfully")
		}
		mans, err := scanner.Scan(t.Context(), filepath.Join(dir, "absent"))
		if err == nil {
			t.Fatal("a missing folder walked successfully")
		}
		if len(mans) != 0 {
			t.Fatalf("a missing folder yielded %+v", mans)
		}
	})

	t.Run("a link to a folder wearing a manifest name is stepped over", func(t *testing.T) {
		linked := writeTree(t, t.TempDir(), map[string]string{
			"package.json":  `{"name":"acme","version":"1.0.0"}`,
			"real/keep.txt": "kept\n",
		})
		if err := os.Symlink(filepath.Join(linked, "real"), filepath.Join(linked, "Podfile")); err != nil {
			t.Fatal(err)
		}
		mans, err := scanner.ScanRoot(t.Context(), linked)
		if err != nil {
			t.Fatalf("root scan: %v", err)
		}
		if len(mans) != 1 || mans[0].Path != "package.json" {
			t.Fatalf("linked folder read as a manifest: %+v", mans)
		}
	})

	t.Run("an unreadable root manifest is reported beside the readable ones", func(t *testing.T) {
		locked := writeTree(t, t.TempDir(), map[string]string{
			"package.json":  `{"name":"acme","version":"1.0.0"}`,
			"composer.json": `{"name":"acme/app"}`,
		})
		path := filepath.Join(locked, "composer.json")
		if err := os.Chmod(path, 0); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(path, 0o600)
		mans, err := scanner.ScanRoot(t.Context(), locked)
		if err == nil || !strings.Contains(err.Error(), "composer.json") {
			t.Fatalf("root scan error = %v", err)
		}
		if len(mans) != 1 || mans[0].Path != "package.json" {
			t.Fatalf("partial root result = %+v", mans)
		}
	})

	t.Run("an unparseable root manifest is reported beside the readable ones", func(t *testing.T) {
		broken := writeTree(t, t.TempDir(), map[string]string{
			"package.json":  `{"name":"acme","version":"1.0.0"}`,
			"composer.json": `{"require":[`,
		})
		mans, err := scanner.ScanRoot(t.Context(), broken)
		if err == nil || !strings.Contains(err.Error(), "composer.json") {
			t.Fatalf("root scan error = %v", err)
		}
		if len(mans) != 1 || mans[0].Path != "package.json" {
			t.Fatalf("partial root result = %+v", mans)
		}
	})

	t.Run("an unreadable nested aqua configuration is reported", func(t *testing.T) {
		locked := writeTree(t, t.TempDir(), map[string]string{
			".aqua/aqua.yaml": "packages:\n  - name: cli/cli@v2.55.0\n",
		})
		path := filepath.Join(locked, ".aqua", "aqua.yaml")
		if err := os.Chmod(path, 0); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(path, 0o600)
		if _, err := scanner.ScanRoot(t.Context(), locked); err == nil {
			t.Fatal("an unreadable nested aqua configuration was not reported")
		}
	})

	t.Run("an unparseable nested aqua configuration is reported", func(t *testing.T) {
		broken := writeTree(t, t.TempDir(), map[string]string{
			"aqua/aqua.yaml": "- not a mapping\n",
		})
		if _, err := scanner.ScanRoot(t.Context(), broken); err == nil {
			t.Fatal("an unparseable nested aqua configuration was not reported")
		}
	})
}

func TestPublicAPIScannerFollowsLocalAquaImports(t *testing.T) {
	dir := writeTree(t, t.TempDir(), map[string]string{
		"aqua.yaml": `import_dir: imports
registries:
  - type: standard
    ref: v4.200.0
packages:
  - name: cli/cli@v2.55.0
  - import: extra/fzf.yaml
  - import: imports/ktlint.yaml
`,
		"extra/fzf.yaml":      "packages:\n  - name: junegunn/fzf@v0.54.0\n",
		"imports/ktlint.yaml": "packages:\n  - name: pinterest/ktlint@1.3.1\n",
		"imports/alias.yaml":  "packages:\n  - name: acme/alias@v1.0.0\n",
	})
	// A second name for one file: the import walk must read it once.
	if err := os.Symlink("alias.yaml", filepath.Join(dir, "imports", "mirror.yaml")); err != nil {
		t.Fatal(err)
	}
	// A folder matching the import glob is not a configuration file.
	if err := os.Mkdir(filepath.Join(dir, "imports", "folder.yaml"), 0o755); err != nil {
		t.Fatal(err)
	}

	mans, err := scanner.Scan(t.Context(), dir)
	if err != nil {
		t.Fatalf("aqua import scan: %v", err)
	}
	var paths []string
	for _, m := range mans {
		paths = append(paths, m.Path)
	}
	want := "aqua.yaml,extra/fzf.yaml,imports/alias.yaml,imports/ktlint.yaml"
	if strings.Join(paths, ",") != want {
		t.Fatalf("aqua import scan found %q; want %q", strings.Join(paths, ","), want)
	}
}

// One bad imported package list must not hide its healthy siblings. The
// ordinary walk does not recognize these arbitrary YAML file names; they are
// reached only through Aqua's import_dir expansion.
func TestPublicAPIScannerKeepsValidAquaImportsBesideRejectedSiblings(t *testing.T) {
	dir := writeTree(t, t.TempDir(), map[string]string{
		"aqua.yaml":         "import_dir: imports\npackages:\n  - name: cli/cli@v2.55.0\n",
		"imports/good.yaml": "packages:\n  - name: junegunn/fzf@v0.54.0\n",
		"imports/bad.yaml":  "- not a mapping\n",
	})
	huge := filepath.Join(dir, "imports", "huge.yaml")
	if err := os.WriteFile(huge, make([]byte, (16<<20)+1), 0o644); err != nil {
		t.Fatal(err)
	}

	mans, err := scanner.Scan(t.Context(), dir)
	if !errors.Is(err, scanner.ErrManifestTooLarge) || !strings.Contains(err.Error(), "imports/bad.yaml") {
		t.Fatalf("import errors = %v; want both the oversized and malformed siblings", err)
	}
	var paths []string
	for _, m := range mans {
		paths = append(paths, m.Path)
	}
	if got, want := strings.Join(paths, ","), "aqua.yaml,imports/good.yaml"; got != want {
		t.Fatalf("partial import result = %q; want %q", got, want)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "imports", "bad.yaml")); err != nil || string(got) != "- not a mapping\n" {
		t.Fatalf("rejected malformed import changed: %q, %v", got, err)
	}
	if info, err := os.Stat(huge); err != nil || info.Size() != (16<<20)+1 {
		t.Fatalf("rejected oversized import changed: %v, %v", info, err)
	}
}

func TestPublicAPIScannerRefusesAquaImportsThatLeaveTheTree(t *testing.T) {
	parent := t.TempDir()
	outside := writeTree(t, parent, map[string]string{
		"outside/aqua-extra.yaml": "packages:\n  - name: acme/outside@v1.0.0\n",
	})
	dir := writeTree(t, filepath.Join(parent, "repo"), map[string]string{
		"aqua.yaml": `packages:
  - name: cli/cli@v2.55.0
  - import: ../outside/aqua-extra.yaml
  - import: imports/bad[pattern.yaml
`,
		"imports/broken.yaml":    "packages: []\n",
		"imports/malformed.yaml": "- not a mapping\n",
	})
	if err := os.Remove(filepath.Join(dir, "imports", "broken.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("nowhere.yaml", filepath.Join(dir, "imports", "broken.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "outside", "aqua-extra.yaml"),
		filepath.Join(dir, "imports", "escape.yaml")); err != nil {
		t.Fatal(err)
	}
	locked := filepath.Join(dir, "imports", "locked.yaml")
	if err := os.WriteFile(locked, []byte("packages: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(locked, 0o600)
	// A second import list naming the whole folder, so the glob meets every
	// entry above.
	if err := os.WriteFile(filepath.Join(dir, "aqua.yml"),
		[]byte("import_dir: imports\npackages: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	mans, err := scanner.Scan(t.Context(), dir)
	if err == nil {
		t.Fatal("escaping and malformed aqua imports were not reported")
	}
	for _, want := range []string{
		"escapes scanned directory",
		"malformed aqua import",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q: %v", want, err)
		}
	}
	for _, m := range mans {
		if strings.Contains(m.Path, "..") || strings.Contains(m.Path, "escape") {
			t.Errorf("a manifest outside the tree was returned: %s", m.Path)
		}
	}
}

func TestPublicAPIScannerPrefersTheRealAquaFileOverItsAlias(t *testing.T) {
	parent := t.TempDir()
	writeTree(t, parent, map[string]string{
		"outside/aqua.yaml": "packages:\n  - name: acme/outside@v1.0.0\n",
	})
	dir := writeTree(t, filepath.Join(parent, "repo"), map[string]string{
		"aqua.yaml": "packages:\n  - name: cli/cli@v2.55.0\n",
	})
	if err := os.Symlink("aqua.yaml", filepath.Join(dir, ".aqua.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(parent, "outside", "aqua.yaml"),
		filepath.Join(dir, "aqua.yml")); err != nil {
		t.Fatal(err)
	}

	mans, err := scanner.Scan(t.Context(), dir)
	if err == nil || !strings.Contains(err.Error(), "escapes scanned directory through symlink") {
		t.Fatalf("symlinked aqua manifest error = %v", err)
	}
	var paths []string
	for _, m := range mans {
		paths = append(paths, m.Path)
	}
	if strings.Join(paths, ",") != "aqua.yaml" {
		t.Fatalf("aliased aqua scan found %q; want the real file alone", paths)
	}
}

func TestPublicAPIScannerPackageRootRule(t *testing.T) {
	cases := []struct {
		path string
		root bool
		want bool
	}{
		{"package.json", true, true},
		{"packages/core/package.json", false, false},
		{"ProjectSettings/ProjectSettings.asset", false, true},
		{"Config/DefaultEngine.ini", false, true},
		{"Packages/manifest.json", false, true},
		{"nested/ProjectSettings/ProjectSettings.asset", false, false},
		{"aqua/aqua.yaml", false, true},
		{".aqua/aqua.yml", false, true},
		{"tools/aqua/aqua.yaml", false, false},
		{"docs/notes.md", false, false},
	}
	for _, tc := range cases {
		m := scanner.Manifest{Path: tc.path, Root: tc.root}
		if got := m.IsAtPackageRoot(); got != tc.want {
			t.Errorf("Manifest{%q}.IsAtPackageRoot() = %v; want %v", tc.path, got, tc.want)
		}
		if got := m.AtPackageRoot(); got != tc.want {
			t.Errorf("Manifest{%q}.AtPackageRoot() = %v; want %v", tc.path, got, tc.want)
		}
	}
}

func TestPublicAPIScannerNameIndexBindsByRank(t *testing.T) {
	owners := []scanner.Owner{
		{
			Package: "api",
			Names:   []string{"acme-api"},
			Manifests: []scanner.Manifest{
				{Path: "package.json", Name: "@acme/api", Root: true},
				{Path: "examples/demo/package.json", Name: "@acme/shared-example"},
			},
		},
		{
			Package: "core",
			Manifests: []scanner.Manifest{
				{Path: "package.json", Name: "@acme/core", Root: true},
				{Path: "fixtures/package.json", Name: "@acme/shared-example"},
				{Path: "unnamed/package.json"},
			},
		},
		{
			Package: "clash",
			Names:   []string{"acme-api"},
			Manifests: []scanner.Manifest{
				{Path: "package.json", Name: "@acme/api", Root: true},
			},
		},
	}
	names, ambiguous := scanner.NameIndex(owners)
	if strings.Join(ambiguous, ",") != "@acme/api,@acme/shared-example,acme-api" {
		t.Fatalf("ambiguous = %q", ambiguous)
	}
	if names["@acme/core"] != "core" {
		t.Errorf("names = %v", names)
	}
	for _, name := range ambiguous {
		if owner, ok := names[name]; ok {
			t.Errorf("ambiguous name %q still bound to %q", name, owner)
		}
	}

	t.Run("a root claim beats a nested one without becoming ambiguous", func(t *testing.T) {
		names, ambiguous := scanner.NameIndex([]scanner.Owner{
			{Package: "core", Manifests: []scanner.Manifest{{Path: "package.json", Name: "@acme/core", Root: true}}},
			{Package: "docs", Manifests: []scanner.Manifest{{Path: "vendor-copy/package.json", Name: "@acme/core"}}},
		})
		if len(ambiguous) != 0 || names["@acme/core"] != "core" {
			t.Fatalf("names = %v, ambiguous = %q", names, ambiguous)
		}
	})

	t.Run("one package claiming a name twice is not a clash", func(t *testing.T) {
		names, ambiguous := scanner.NameIndex([]scanner.Owner{{
			Package: "core",
			Manifests: []scanner.Manifest{
				{Path: "package.json", Name: "@acme/core", Root: true},
				{Path: "pubspec.yaml", Name: "@acme/core", Root: true},
			},
		}})
		if len(ambiguous) != 0 || names["@acme/core"] != "core" {
			t.Fatalf("names = %v, ambiguous = %q", names, ambiguous)
		}
	})
}

func TestPublicAPIScannerResolvesDeclaredLocalPaths(t *testing.T) {
	dirs := map[string]string{
		filepath.Clean("packages/core"):  "core",
		filepath.Clean("packages/tools"): "tools",
	}
	cases := []struct {
		pkgDir, manifestRel, local, want string
	}{
		{"packages/app", "package.json", "../core", "core"},
		{"packages/app", "src/sub/package.json", "../../../core", "core"},
		{"packages/app", "package.json", "../core/nested/deep", "core"},
		{"packages/app", "package.json", "../tools", "tools"},
		{"packages/app", "package.json", "../../elsewhere", ""},
	}
	for _, tc := range cases {
		got := scanner.ResolveLocalDir(dirs, tc.pkgDir, tc.manifestRel, tc.local)
		if got != tc.want {
			t.Errorf("ResolveLocalDir(%q, %q, %q) = %q; want %q",
				tc.pkgDir, tc.manifestRel, tc.local, got, tc.want)
		}
	}
}
