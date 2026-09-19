package publicapi_test

// The writer's public entry points: the literal replacer, the swappable
// Writerx value, the format-forced rewrite, and the refusals every entry point
// shares. Each refusal checks that the file on disk is exactly what it was, in
// line with the package's contract that nothing is written until the result is
// known to be good.

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yohimik/dispat/pkg/manifest"
	"github.com/yohimik/dispat/pkg/scanner"
	"github.com/yohimik/dispat/pkg/writer"
)

func TestPublicAPIWriterReplacesLiteralText(t *testing.T) {
	t.Run("a workflow file the manifest writers cannot reach", func(t *testing.T) {
		dir := t.TempDir()
		path := writeFile(t, dir, ".github/workflows/release.yml", `name: release
on: push
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - run: docker build -t ghcr.io/acme/api:1.0.0 .
      - run: docker push ghcr.io/acme/api:1.0.0
`)
		res, err := writer.Replace(path, []writer.Replacement{
			{Find: "ghcr.io/acme/api:1.0.0", Write: "ghcr.io/acme/api:2.0.0"},
			{Find: "actions/checkout@v4", Write: "actions/checkout@v4"},
			{Find: "ghcr.io/acme/web:1.0.0", Write: "ghcr.io/acme/web:2.0.0"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if res.Path != path || res.Count != 2 {
			t.Fatalf("replace = %+v", res)
		}
		if len(res.Applied) != 1 || res.Applied[0].Find != "ghcr.io/acme/api:1.0.0" {
			t.Errorf("applied = %+v", res.Applied)
		}
		if len(res.Skipped) != 1 || res.Skipped[0].Find != "actions/checkout@v4" {
			t.Errorf("skipped = %+v", res.Skipped)
		}
		if len(res.Missing) != 1 || res.Missing[0].Find != "ghcr.io/acme/web:1.0.0" {
			t.Errorf("missing = %+v", res.Missing)
		}
		body := readFile(t, path)
		if strings.Count(body, "ghcr.io/acme/api:2.0.0") != 2 || strings.Contains(body, ":1.0.0") {
			t.Fatalf("replaced file = %q", body)
		}
		if !strings.Contains(body, "runs-on: ubuntu-latest") {
			t.Fatalf("unrelated lines were lost: %q", body)
		}
	})

	t.Run("replacements apply in the order the caller chose", func(t *testing.T) {
		path := writeFile(t, t.TempDir(), "notes.md", "version 1.0.0\n")
		res, err := writer.Replace(path, []writer.Replacement{
			{Find: "1.0.0", Write: "2.0.0"},
			{Find: "2.0.0", Write: "3.0.0"},
		})
		if err != nil || len(res.Applied) != 2 || res.Count != 2 {
			t.Fatalf("chained replace = %+v, err = %v", res, err)
		}
		if got := readFile(t, path); got != "version 3.0.0\n" {
			t.Fatalf("chained result = %q", got)
		}
	})

	t.Run("a replacement with nothing to find is refused", func(t *testing.T) {
		path := writeFile(t, t.TempDir(), "notes.md", "version 1.0.0\n")
		_, err := writer.Replace(path, []writer.Replacement{
			{Find: "1.0.0", Write: "2.0.0"},
			{Find: "", Write: "x"},
		})
		if !errors.Is(err, writer.ErrEmptyFind) {
			t.Fatalf("empty find error = %v", err)
		}
		if got := readFile(t, path); got != "version 1.0.0\n" {
			t.Fatalf("refusal changed the file: %q", got)
		}
	})

	t.Run("a binary file is refused", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "logo.png")
		original := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0, 1, 2}, 64)...)
		if err := os.WriteFile(path, original, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Replace(path, []writer.Replacement{{Find: "PNG", Write: "GIF"}}); !errors.Is(err, writer.ErrBinaryFile) {
			t.Fatalf("binary error = %v", err)
		}
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, original) {
			t.Fatalf("binary refusal changed the file")
		}
	})

	t.Run("a file over the read cap is refused", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "huge.txt")
		if err := os.WriteFile(path, bytes.Repeat([]byte{'a'}, (16<<20)+1), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Replace(path, []writer.Replacement{{Find: "a", Write: "b"}}); !errors.Is(err, writer.ErrManifestTooLarge) {
			t.Fatalf("oversized error = %v", err)
		}
	})

	t.Run("a missing file is reported rather than created", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "absent.txt")
		if _, err := writer.Replace(path, []writer.Replacement{{Find: "a", Write: "b"}}); err == nil {
			t.Fatal("a missing file replaced successfully")
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("the refusal created the file")
		}
	})

	t.Run("in-memory replacement never modifies its input", func(t *testing.T) {
		data := []byte("core 1.0.0 and core 1.0.0")
		out, counts := writer.ReplaceBytes(data, []writer.Replacement{
			{Find: "1.0.0", Write: "2.0.0"},
			{Find: "", Write: "x"},
			{Find: "core", Write: "core"},
			{Find: "absent", Write: "x"},
		})
		if string(out) != "core 2.0.0 and core 2.0.0" {
			t.Fatalf("out = %q", out)
		}
		if string(data) != "core 1.0.0 and core 1.0.0" {
			t.Fatalf("input was modified: %q", data)
		}
		if len(counts) != 4 || counts[0] != 2 || counts[1] != 0 || counts[2] != 2 || counts[3] != 0 {
			t.Fatalf("counts = %v", counts)
		}
	})
}

func TestPublicAPIWriterSwappableValueCoversEveryEntryPoint(t *testing.T) {
	w := writer.New()
	dir := t.TempDir()
	path := writeFile(t, dir, "package.json", `{
  "name": "@acme/app",
  "version": "1.0.0",
  "dependencies": { "@acme/core": "^1.0.0" }
}
`)

	res, err := w.Rewrite(path, "2.0.0", []writer.Edit{
		{Name: "@acme/core", Kind: manifest.KindDependencies, Range: "^2.0.0"},
	})
	if err != nil || !res.VersionWritten || len(res.Applied) != 1 {
		t.Fatalf("rewrite = %+v, err = %v", res, err)
	}

	link, err := w.Relink(path, []writer.Link{{Name: "@acme/core", Path: "../core"}})
	if err != nil || len(link.Applied) != 1 {
		t.Fatalf("relink = %+v, err = %v", link, err)
	}
	links, err := w.Links(path)
	if err != nil || len(links) != 1 || links[0].Path != "../core" {
		t.Fatalf("links = %+v, err = %v", links, err)
	}
	dropped, err := w.DropLinks(path)
	if err != nil || len(dropped.Applied) != 1 {
		t.Fatalf("drop = %+v, err = %v", dropped, err)
	}
	if links, err := w.Links(path); err != nil || len(links) != 0 {
		t.Fatalf("links after drop = %+v, err = %v", links, err)
	}

	rep, err := w.Replace(path, []writer.Replacement{{Find: "@acme/app", Write: "@acme/application"}})
	if err != nil || len(rep.Applied) != 1 {
		t.Fatalf("replace = %+v, err = %v", rep, err)
	}

	plist := writeFile(t, dir, "Info.plist",
		`<plist><dict><key>CFBundleVersion</key><string>7</string></dict></plist>`)
	build, err := w.SetBuild(plist, "8")
	if err != nil || !build.BuildWritten {
		t.Fatalf("set build = %+v, err = %v", build, err)
	}
}

func TestPublicAPIWriterFormatForcedRewrite(t *testing.T) {
	t.Run("an aqua import kept under its own file name", func(t *testing.T) {
		dir := t.TempDir()
		path := writeFile(t, dir, "aqua/imports/tools.yaml", `packages:
  - name: cli/cli
    version: v2.55.0
`)
		if writer.IsSupported(path) {
			t.Fatal("an import file name should have no writer of its own")
		}
		res, err := writer.RewriteAs(path, manifest.FormatAqua, "", []writer.Edit{
			{Name: "cli/cli", Range: "v2.60.0"},
		})
		if err != nil || len(res.Applied) != 1 || res.Path != path {
			t.Fatalf("rewrite as aqua = %+v, err = %v", res, err)
		}
		if !strings.Contains(readFile(t, path), "v2.60.0") {
			t.Fatalf("rewritten import = %q", readFile(t, path))
		}
	})

	t.Run("the kind vocabulary is canonicalised without touching the caller's slice", func(t *testing.T) {
		dir := t.TempDir()
		path := writeFile(t, dir, "package.json",
			`{"name":"acme","version":"1.0.0","dependencies":{"core":"^1.0.0"}}`)
		edits := []writer.Edit{{Name: "core", Kind: manifest.Kind("dependencies"), Range: "^2.0.0"}}
		res, err := writer.Rewrite(path, "", edits)
		if err != nil || len(res.Applied) != 1 {
			t.Fatalf("rewrite = %+v, err = %v", res, err)
		}
		if edits[0].Kind != manifest.Kind("dependencies") {
			t.Fatalf("the caller's edits were rewritten: %+v", edits)
		}

		forced := writeFile(t, dir, "forced.json",
			`{"name":"acme","version":"1.0.0","dependencies":{"core":"^1.0.0"}}`)
		other := []writer.Edit{{Name: "core", Kind: manifest.Kind("dependencies"), Range: "^3.0.0"}}
		if _, err := writer.RewriteAs(forced, manifest.FormatNpm, "", other); err != nil {
			t.Fatal(err)
		}
		if other[0].Kind != manifest.Kind("dependencies") {
			t.Fatalf("the caller's edits were rewritten: %+v", other)
		}
	})

	t.Run("refusals", func(t *testing.T) {
		dir := t.TempDir()
		path := writeFile(t, dir, "package.json",
			`{"name":"acme","version":"1.0.0","dependencies":{"core":"^1.0.0"}}`)
		bad := []writer.Edit{{Name: "core", Kind: manifest.Kind("bundleDependencies"), Range: "^2.0.0"}}

		if _, err := writer.Rewrite(path, "", bad); err == nil || !strings.Contains(err.Error(), "unknown dependency kind") {
			t.Errorf("unknown kind through Rewrite = %v", err)
		}
		if _, err := writer.RewriteAs(path, manifest.FormatNpm, "", bad); err == nil ||
			!strings.Contains(err.Error(), "unknown dependency kind") {
			t.Errorf("unknown kind through RewriteAs = %v", err)
		}
		if _, err := writer.RewriteAs(path, manifest.Format("invented"), "", nil); !errors.Is(err, writer.ErrUnsupportedManifest) {
			t.Errorf("unknown format = %v", err)
		}
		notes := writeFile(t, dir, "NOTES.md", "# notes\n")
		if _, err := writer.Rewrite(notes, "2.0.0", nil); !errors.Is(err, writer.ErrUnsupportedManifest) {
			t.Errorf("unsupported manifest = %v", err)
		}
		absent := filepath.Join(dir, "absent", "package.json")
		if _, err := writer.Rewrite(absent, "2.0.0", nil); err == nil {
			t.Error("a missing manifest was rewritten")
		}
		if _, err := writer.RewriteAs(absent, manifest.FormatNpm, "2.0.0", nil); err == nil {
			t.Error("a missing manifest was rewritten by format")
		}
		if got := readFile(t, path); !strings.Contains(got, `"^1.0.0"`) {
			t.Fatalf("a refusal changed the manifest: %q", got)
		}
	})

	t.Run("an oversized manifest is refused by every entry point", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "package.json")
		if err := os.WriteFile(path, bytes.Repeat([]byte{' '}, (16<<20)+1), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.RewriteAs(path, manifest.FormatNpm, "2.0.0", nil); !errors.Is(err, writer.ErrManifestTooLarge) {
			t.Errorf("RewriteAs = %v", err)
		}
		if _, err := writer.Relink(path, []writer.Link{{Name: "core", Path: "../core"}}); !errors.Is(err, writer.ErrManifestTooLarge) {
			t.Errorf("Relink = %v", err)
		}
		if _, err := writer.Links(path); !errors.Is(err, writer.ErrManifestTooLarge) {
			t.Errorf("Links = %v", err)
		}
		plist := filepath.Join(filepath.Dir(path), "Info.plist")
		if err := os.WriteFile(plist, bytes.Repeat([]byte{' '}, (16<<20)+1), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.SetBuild(plist, "7"); !errors.Is(err, writer.ErrManifestTooLarge) {
			t.Errorf("SetBuild = %v", err)
		}
	})
}

func TestPublicAPIWriterRefusesToFollowASymbolicLink(t *testing.T) {
	dir := t.TempDir()
	target := writeFile(t, dir, "real/package.json",
		`{"name":"acme","version":"1.0.0","dependencies":{"core":"^1.0.0"}}`)
	link := filepath.Join(dir, "package.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	original := readFile(t, target)

	for _, call := range []struct {
		name string
		run  func() error
	}{
		{"rewrite", func() error {
			_, err := writer.Rewrite(link, "2.0.0", nil)
			return err
		}},
		{"replace", func() error {
			_, err := writer.Replace(link, []writer.Replacement{{Find: "1.0.0", Write: "2.0.0"}})
			return err
		}},
		{"relink", func() error {
			_, err := writer.Relink(link, []writer.Link{{Name: "core", Path: "../core"}})
			return err
		}},
		{"links", func() error {
			_, err := writer.Links(link)
			return err
		}},
	} {
		if err := call.run(); err == nil || !strings.Contains(err.Error(), "symbolic link") {
			t.Errorf("%s through a symlink = %v", call.name, err)
		}
	}
	if got := readFile(t, target); got != original {
		t.Fatalf("a symlink refusal changed the target: %q", got)
	}
}

func TestPublicAPIWriterSupportTablesAgreeWithTheScanner(t *testing.T) {
	names := map[manifest.Format]string{
		manifest.FormatNpm:                  "package.json",
		manifest.FormatGoMod:                "go.mod",
		manifest.FormatCargo:                "Cargo.toml",
		manifest.FormatPyProject:            "pyproject.toml",
		manifest.FormatRequirements:         "requirements.txt",
		manifest.FormatComposer:             "composer.json",
		manifest.FormatMaven:                "pom.xml",
		manifest.FormatMSBuildProject:       "Acme.csproj",
		manifest.FormatNuSpec:               "Acme.nuspec",
		manifest.FormatPackagesProps:        "Directory.Packages.props",
		manifest.FormatPackagesConfig:       "packages.config",
		manifest.FormatPubspec:              "pubspec.yaml",
		manifest.FormatPlist:                "Info.plist",
		manifest.FormatAndroidManifest:      "AndroidManifest.xml",
		manifest.FormatGradleCatalog:        "libs.versions.toml",
		manifest.FormatGradleBuild:          "build.gradle",
		manifest.FormatXcodeProject:         "project.pbxproj",
		manifest.FormatPodfile:              "Podfile",
		manifest.FormatPodspec:              "Acme.podspec",
		manifest.FormatGemfile:              "Gemfile",
		manifest.FormatGemspec:              "acme.gemspec",
		manifest.FormatDockerfile:           "Dockerfile",
		manifest.FormatCompose:              "compose.yaml",
		manifest.FormatAqua:                 "aqua.yaml",
		manifest.FormatUnityPackages:        "Packages/manifest.json",
		manifest.FormatUnityProjectSettings: "ProjectSettings/ProjectSettings.asset",
		manifest.FormatGodotProject:         "project.godot",
		manifest.FormatGodotPlugin:          "plugin.cfg",
		manifest.FormatGodotExportPresets:   "export_presets.cfg",
		manifest.FormatUnrealProject:        "Acme.uproject",
		manifest.FormatUnrealPlugin:         "Acme.uplugin",
		manifest.FormatUnrealGameConfig:     "Config/DefaultGame.ini",
		manifest.FormatUnrealEngineConfig:   "Config/DefaultEngine.ini",
		manifest.FormatDefoldProject:        "game.project",
		manifest.FormatO3DEProject:          "project.json",
		manifest.FormatO3DEGem:              "gem.json",
	}
	for _, format := range manifest.Formats {
		name, ok := names[format]
		if !ok {
			t.Fatalf("format %q has no example file name in this test", format)
		}
		if !writer.IsSupported(name) || !writer.Supported(name) {
			t.Errorf("%s (%s) has no writer", name, format)
		}
		if scanner.EcosystemOf(format) == "" {
			t.Errorf("%s (%s) has no ecosystem", name, format)
		}
	}
	linkable := map[string]bool{
		"package.json": true, "go.mod": true, "Cargo.toml": true,
		"pyproject.toml": true, "pubspec.yaml": true,
	}
	for _, name := range names {
		if writer.IsLinkSupported(name) != linkable[name] || writer.SupportsLink(name) != linkable[name] {
			t.Errorf("%s link support = %v; want %v", name, writer.IsLinkSupported(name), linkable[name])
		}
	}
	for _, name := range []string{"NOTES.md", "manifest.json", "unknown"} {
		if writer.IsSupported(name) || writer.IsLinkSupported(name) {
			t.Errorf("%s is claimed by the writer", name)
		}
	}
}

func TestPublicAPIWriterLinkRefusalsAndUnlinkableFormats(t *testing.T) {
	dir := t.TempDir()

	t.Run("a link with no dependency name is refused", func(t *testing.T) {
		path := writeFile(t, dir, "package.json", `{"name":"acme","dependencies":{"core":"^1.0.0"}}`)
		if _, err := writer.Relink(path, []writer.Link{{Path: "../core"}}); err == nil ||
			!strings.Contains(err.Error(), "no dependency name") {
			t.Fatalf("nameless link = %v", err)
		}
	})

	t.Run("a file no format claims is refused", func(t *testing.T) {
		notes := writeFile(t, dir, "NOTES.md", "# notes\n")
		if _, err := writer.Relink(notes, []writer.Link{{Name: "core", Path: "../core"}}); !errors.Is(err, writer.ErrUnsupportedManifest) {
			t.Errorf("relink = %v", err)
		}
		if _, err := writer.Links(notes); !errors.Is(err, writer.ErrUnsupportedManifest) {
			t.Errorf("links = %v", err)
		}
		if _, err := writer.DropLinks(notes); !errors.Is(err, writer.ErrUnsupportedManifest) {
			t.Errorf("drop links = %v", err)
		}
	})

	t.Run("a missing manifest is reported", func(t *testing.T) {
		absent := filepath.Join(dir, "absent", "package.json")
		if _, err := writer.Relink(absent, []writer.Link{{Name: "core", Path: "../core"}}); err == nil {
			t.Error("a missing manifest was relinked")
		}
		if _, err := writer.Links(absent); err == nil {
			t.Error("a missing manifest listed links")
		}
	})

	t.Run("a recognised format with nowhere to put a redirect skips every link", func(t *testing.T) {
		gemfile := writeFile(t, dir, "Gemfile", "source 'https://rubygems.org'\ngem 'acme-core'\n")
		original := readFile(t, gemfile)
		res, err := writer.Relink(gemfile, []writer.Link{{Name: "acme-core", Path: "../core"}})
		if err != nil || len(res.Skipped) != 1 || len(res.Applied) != 0 {
			t.Fatalf("relink = %+v, err = %v", res, err)
		}
		links, err := writer.Links(gemfile)
		if err != nil || len(links) != 0 {
			t.Fatalf("links = %+v, err = %v", links, err)
		}
		drop, err := writer.DropLinks(gemfile)
		if err != nil || len(drop.Applied) != 0 || drop.Path != gemfile {
			t.Fatalf("drop = %+v, err = %v", drop, err)
		}
		if got := readFile(t, gemfile); got != original {
			t.Fatalf("an unlinkable format was rewritten: %q", got)
		}
	})

	t.Run("removing a redirect that is not there is missing, not an error", func(t *testing.T) {
		path := writeFile(t, dir, "unlinked/package.json", `{"name":"acme","dependencies":{"core":"^1.0.0"}}`)
		original := readFile(t, path)
		res, err := writer.Relink(path, []writer.Link{{Name: "core"}})
		if err != nil || len(res.Missing) != 1 || len(res.Applied) != 0 {
			t.Fatalf("relink = %+v, err = %v", res, err)
		}
		if got := readFile(t, path); got != original {
			t.Fatalf("a missing removal rewrote the manifest: %q", got)
		}
	})

	t.Run("a manifest a linker cannot parse is refused", func(t *testing.T) {
		cases := map[string]string{
			"package.json":   `{"dependencies":{`,
			"go.mod":         "module example.com/x\n\nrequire (\n",
			"Cargo.toml":     "[package\n",
			"pyproject.toml": "[project\n",
		}
		for name, body := range cases {
			path := writeFile(t, t.TempDir(), name, body)
			if _, err := writer.Links(path); err == nil {
				t.Errorf("Links(%s) parsed a malformed manifest", name)
			}
			if _, err := writer.DropLinks(path); err == nil {
				t.Errorf("DropLinks(%s) parsed a malformed manifest", name)
			}
			if got := readFile(t, path); got != body {
				t.Errorf("%s was rewritten by a failed listing: %q", name, got)
			}
		}
	})
}
