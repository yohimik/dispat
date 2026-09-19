package publicapi_test

// Local links and build counters: the two writes that are not version
// rewrites. A link is the directive that points a dependency at a folder in
// the same checkout, and a build counter is the monotonic number a store
// orders uploads by; neither moves when a version does, and both have their
// own entry point.

import (
	"errors"
	"strings"
	"testing"

	"github.com/yohimik/dispat/pkg/scanner"
	"github.com/yohimik/dispat/pkg/writer"
)

// renderLinks renders the directives a manifest carries as "name=path" lines.
func renderLinks(links []writer.Link) string {
	out := make([]string, 0, len(links))
	for _, l := range links {
		out = append(out, l.Name+"="+l.Path)
	}
	return strings.Join(out, ",")
}

func TestPublicAPIWriterManagesNpmOverrides(t *testing.T) {
	t.Run("the override map is created in the manager's own spelling", func(t *testing.T) {
		cases := []struct{ name, body, field string }{
			{"npm by default", `{
  "name": "acme",
  "dependencies": { "@acme/core": "^1.0.0" }
}
`, "overrides"},
			{"pnpm by its package manager", `{
  "name": "acme",
  "packageManager": "pnpm@9.1.0",
  "dependencies": { "@acme/core": "^1.0.0" }
}
`, "pnpm"},
			{"yarn by its package manager", `{
  "name": "acme",
  "packageManager": "yarn@4.2.2",
  "dependencies": { "@acme/core": "^1.0.0" }
}
`, "resolutions"},
			{"an existing resolutions map wins", `{
  "name": "acme",
  "resolutions": {},
  "dependencies": { "@acme/core": "^1.0.0" }
}
`, "resolutions"},
			{"an existing pnpm overrides map wins", `{
  "name": "acme",
  "pnpm": { "overrides": {} },
  "dependencies": { "@acme/core": "^1.0.0" }
}
`, "pnpm"},
			{"a compact file keeps its shape", `{"name":"acme","dependencies":{"@acme/core":"^1.0.0"}}`, "overrides"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				path := writeFile(t, t.TempDir(), "package.json", tc.body)
				res, err := writer.Relink(path, []writer.Link{{Name: "@acme/core", Path: "../core"}})
				if err != nil || len(res.Applied) != 1 {
					t.Fatalf("relink = %+v, err = %v", res, err)
				}
				out := readFile(t, path)
				if !strings.Contains(out, tc.field) || !strings.Contains(out, `"file:../core"`) {
					t.Fatalf("rewritten package.json:\n%s", out)
				}
				links, err := writer.Links(path)
				if err != nil || renderLinks(links) != "@acme/core=../core" {
					t.Fatalf("links = %+v, err = %v", links, err)
				}

				// Writing the same redirect again changes nothing.
				again, err := writer.Relink(path, []writer.Link{{Name: "@acme/core", Path: "../core"}})
				if err != nil || len(again.Applied) != 0 {
					t.Fatalf("idempotent relink = %+v, err = %v", again, err)
				}
				if readFile(t, path) != out {
					t.Fatal("a no-op relink rewrote the file")
				}

				// Removing it takes the emptied map with it.
				dropped, err := writer.DropLinks(path)
				if err != nil || len(dropped.Applied) != 1 {
					t.Fatalf("drop = %+v, err = %v", dropped, err)
				}
				after := readFile(t, path)
				if strings.Contains(after, "file:../core") {
					t.Fatalf("the redirect survived removal:\n%s", after)
				}
				if strings.Contains(after, `"overrides"`) || strings.Contains(after, `"resolutions"`) ||
					strings.Contains(after, `"pnpm"`) {
					t.Fatalf("an emptied override map was left behind:\n%s", after)
				}
				if !strings.Contains(after, `"@acme/core": "^1.0.0"`) &&
					!strings.Contains(after, `"@acme/core":"^1.0.0"`) {
					t.Fatalf("the declaration was lost:\n%s", after)
				}
			})
		}
	})

	t.Run("an existing entry is repointed and its neighbours survive", func(t *testing.T) {
		path := writeFile(t, t.TempDir(), "package.json", `{
  "name": "acme",
  "overrides": {
    "@acme/core": "file:../old",
    "@acme/ui": "file:../ui",
    "left-pad": "^1.3.0"
  }
}
`)
		res, err := writer.Relink(path, []writer.Link{{Name: "@acme/core", Path: "../core"}})
		if err != nil || len(res.Applied) != 1 {
			t.Fatalf("relink = %+v, err = %v", res, err)
		}
		links, err := writer.Links(path)
		if err != nil || renderLinks(links) != "@acme/core=../core,@acme/ui=../ui" {
			t.Fatalf("links = %+v, err = %v", links, err)
		}
		out := readFile(t, path)
		if !strings.Contains(out, `"left-pad": "^1.3.0"`) {
			t.Fatalf("a version override was disturbed:\n%s", out)
		}

		// Removing the first entry keeps the rest, and removing a middle one
		// keeps the commas right.
		first, err := writer.Relink(path, []writer.Link{{Name: "@acme/core"}})
		if err != nil || len(first.Applied) != 1 {
			t.Fatalf("remove first = %+v, err = %v", first, err)
		}
		last, err := writer.Relink(path, []writer.Link{{Name: "@acme/ui"}, {Name: "absent"}})
		if err != nil || len(last.Applied) != 1 || len(last.Missing) != 1 {
			t.Fatalf("remove last = %+v, err = %v", last, err)
		}
		out = readFile(t, path)
		if !strings.Contains(out, `"left-pad": "^1.3.0"`) || strings.Contains(out, "file:") {
			t.Fatalf("rewritten package.json:\n%s", out)
		}
	})

	t.Run("a removal finds the directive wherever the file keeps it", func(t *testing.T) {
		path := writeFile(t, t.TempDir(), "package.json", `{
  "name": "acme",
  "packageManager": "yarn@4.2.2",
  "pnpm": { "overrides": { "@acme/core": "file:../core" } },
  "dependencies": { "@acme/core": "^1.0.0" }
}
`)
		links, err := writer.Links(path)
		if err != nil || renderLinks(links) != "@acme/core=../core" {
			t.Fatalf("links = %+v, err = %v", links, err)
		}
		res, err := writer.DropLinks(path)
		if err != nil || len(res.Applied) != 1 {
			t.Fatalf("drop = %+v, err = %v", res, err)
		}
		if strings.Contains(readFile(t, path), "file:../core") {
			t.Fatalf("the redirect survived:\n%s", readFile(t, path))
		}
	})

	t.Run("only a file or link spec counts as a redirect", func(t *testing.T) {
		path := writeFile(t, t.TempDir(), "package.json", `{
  "name": "acme",
  "overrides": {
    "@acme/ui": "link:../ui",
    "left-pad": "^1.3.0",
    "nested": { "inner": "file:../inner" }
  },
  "resolutions": { "@acme/core": "file:../core" }
}
`)
		links, err := writer.Links(path)
		if err != nil || renderLinks(links) != "@acme/core=../core,@acme/ui=../ui" {
			t.Fatalf("links = %+v, err = %v", links, err)
		}
	})

	t.Run("a non-object override field is refused without changing bytes", func(t *testing.T) {
		body := `{"name":"acme","overrides":5}`
		path := writeFile(t, t.TempDir(), "package.json", body)
		res, err := writer.Relink(path, []writer.Link{{Name: "core", Path: "../core"}})
		if err == nil || !strings.Contains(err.Error(), `"overrides" is not an object`) {
			t.Fatalf("relink = %+v, err = %v", res, err)
		}
		if got := readFile(t, path); got != body {
			t.Fatalf("the refusal changed the file:\n%s", got)
		}
	})
}

func TestPublicAPIWriterManagesTOMLAndPubspecLinks(t *testing.T) {
	t.Run("cargo patch table", func(t *testing.T) {
		path := writeFile(t, t.TempDir(), "Cargo.toml", `[package]
name = "acme-app"
version = "1.0.0"

[dependencies]
acme-core = "1.0"
acme-ui = "1.0"
`)
		res, err := writer.Relink(path, []writer.Link{
			{Name: "acme-core", Path: "../core"},
			{Name: "acme-ui", Path: "../ui"},
		})
		if err != nil || len(res.Applied) != 2 {
			t.Fatalf("relink = %+v, err = %v", res, err)
		}
		out := readFile(t, path)
		if !strings.Contains(out, "[patch.crates-io]") || !strings.Contains(out, `acme-core = { path = "../core" }`) {
			t.Fatalf("rewritten Cargo.toml:\n%s", out)
		}
		links, err := writer.Links(path)
		if err != nil || renderLinks(links) != "acme-core=../core,acme-ui=../ui" {
			t.Fatalf("links = %+v, err = %v", links, err)
		}

		repoint, err := writer.Relink(path, []writer.Link{
			{Name: "acme-core", Path: "../moved"},
			{Name: "acme-ui", Path: "../ui"},
		})
		if err != nil || len(repoint.Applied) != 1 {
			t.Fatalf("repoint = %+v, err = %v", repoint, err)
		}

		partial, err := writer.Relink(path, []writer.Link{{Name: "acme-core"}, {Name: "absent"}})
		if err != nil || len(partial.Applied) != 1 || len(partial.Missing) != 1 {
			t.Fatalf("partial removal = %+v, err = %v", partial, err)
		}
		if !strings.Contains(readFile(t, path), "[patch.crates-io]") {
			t.Fatal("the table was dropped while it still held an entry")
		}

		dropped, err := writer.DropLinks(path)
		if err != nil || len(dropped.Applied) != 1 {
			t.Fatalf("drop = %+v, err = %v", dropped, err)
		}
		out = readFile(t, path)
		if strings.Contains(out, "[patch.crates-io]") {
			t.Fatalf("an emptied table was left behind:\n%s", out)
		}
		if !strings.Contains(out, `acme-core = "1.0"`) {
			t.Fatalf("the declaration was lost:\n%s", out)
		}
	})

	t.Run("a cargo patch table the file already declares", func(t *testing.T) {
		path := writeFile(t, t.TempDir(), "Cargo.toml", `[package]
name = "acme-app"
version = "1.0.0"

[patch.crates-io]
other = { git = "https://example.com/other.git" }

[dependencies]
acme-core = "1.0"
`)
		res, err := writer.Relink(path, []writer.Link{{Name: "acme-core", Path: "../core"}})
		if err != nil || len(res.Applied) != 1 {
			t.Fatalf("relink = %+v, err = %v", res, err)
		}
		out := readFile(t, path)
		if !strings.Contains(out, "other = { git = \"https://example.com/other.git\" }") {
			t.Fatalf("a git patch was disturbed:\n%s", out)
		}
		links, err := writer.Links(path)
		if err != nil || renderLinks(links) != "acme-core=../core" {
			t.Fatalf("links = %+v, err = %v", links, err)
		}
	})

	t.Run("uv sources in a pyproject", func(t *testing.T) {
		path := writeFile(t, t.TempDir(), "pyproject.toml", `[project]
name = "acme-core"
version = "1.0.0"
dependencies = ["acme-io"]
`)
		res, err := writer.Relink(path, []writer.Link{{Name: "acme-io", Path: "../io"}})
		if err != nil || len(res.Applied) != 1 {
			t.Fatalf("relink = %+v, err = %v", res, err)
		}
		out := readFile(t, path)
		if !strings.Contains(out, "[tool.uv.sources]") {
			t.Fatalf("rewritten pyproject:\n%s", out)
		}
		links, err := writer.Links(path)
		if err != nil || renderLinks(links) != "acme-io=../io" {
			t.Fatalf("links = %+v, err = %v", links, err)
		}
		if _, err := writer.DropLinks(path); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(readFile(t, path), "tool.uv.sources") {
			t.Fatal("an emptied table was left behind")
		}
	})

	t.Run("pubspec dependency overrides", func(t *testing.T) {
		path := writeFile(t, t.TempDir(), "pubspec.yaml", `name: acme_app
version: 1.0.0

dependencies:
  acme_core: ^1.0.0
  acme_ui: ^1.0.0
`)
		res, err := writer.Relink(path, []writer.Link{
			{Name: "acme_core", Path: "../core"},
			{Name: "acme_ui", Path: "../ui"},
		})
		if err != nil || len(res.Applied) != 2 {
			t.Fatalf("relink = %+v, err = %v", res, err)
		}
		out := readFile(t, path)
		if !strings.Contains(out, "dependency_overrides:") || !strings.Contains(out, "path: ../core") {
			t.Fatalf("rewritten pubspec:\n%s", out)
		}
		links, err := writer.Links(path)
		if err != nil || renderLinks(links) != "acme_core=../core,acme_ui=../ui" {
			t.Fatalf("links = %+v, err = %v", links, err)
		}

		again, err := writer.Relink(path, []writer.Link{{Name: "acme_core", Path: "../core"}})
		if err != nil || len(again.Applied) != 0 {
			t.Fatalf("idempotent relink = %+v, err = %v", again, err)
		}

		moved, err := writer.Relink(path, []writer.Link{{Name: "acme_core", Path: "../moved"}})
		if err != nil || len(moved.Applied) != 1 {
			t.Fatalf("repoint = %+v, err = %v", moved, err)
		}

		partial, err := writer.Relink(path, []writer.Link{{Name: "acme_core"}, {Name: "absent"}})
		if err != nil || len(partial.Applied) != 1 || len(partial.Missing) != 1 {
			t.Fatalf("partial removal = %+v, err = %v", partial, err)
		}

		dropped, err := writer.DropLinks(path)
		if err != nil || len(dropped.Applied) != 1 {
			t.Fatalf("drop = %+v, err = %v", dropped, err)
		}
		out = readFile(t, path)
		if strings.Contains(out, "dependency_overrides:") {
			t.Fatalf("an emptied override block was left behind:\n%s", out)
		}
		mans, err := scanner.ScanRoot(t.Context(), strings.TrimSuffix(path, "/pubspec.yaml"))
		if err != nil || len(mans) != 1 || len(mans[0].Deps) != 2 {
			t.Fatalf("rescan after the link lifecycle = %+v, err = %v", mans, err)
		}
	})

	t.Run("a pubspec that already declares overrides keeps the ones it has", func(t *testing.T) {
		path := writeFile(t, t.TempDir(), "pubspec.yaml", `name: acme_app
version: 1.0.0

dependencies:
  acme_core: ^1.0.0

dependency_overrides:
  acme_other:
    git:
      url: https://example.com/other.git
`)
		res, err := writer.Relink(path, []writer.Link{{Name: "acme_core", Path: "../core"}})
		if err != nil || len(res.Applied) != 1 {
			t.Fatalf("relink = %+v, err = %v", res, err)
		}
		out := readFile(t, path)
		if !strings.Contains(out, "https://example.com/other.git") {
			t.Fatalf("a git override was disturbed:\n%s", out)
		}
		links, err := writer.Links(path)
		if err != nil || renderLinks(links) != "acme_core=../core" {
			t.Fatalf("links = %+v, err = %v", links, err)
		}
	})
}

func TestPublicAPIWriterManagesGoModuleReplaces(t *testing.T) {
	path := writeFile(t, t.TempDir(), "go.mod", `module example.com/acme/app

go 1.26

require (
	example.com/acme/core v1.0.0
	example.com/acme/tools v0.1.0
)

replace example.com/acme/upstream => example.com/fork/upstream v0.1.0
`)
	res, err := writer.Relink(path, []writer.Link{
		{Name: "example.com/acme/core", Path: "../core"},
		{Name: "example.com/acme/tools", Version: "v0.1.0", Path: "../tools"},
	})
	if err != nil || len(res.Applied) != 2 {
		t.Fatalf("relink = %+v, err = %v", res, err)
	}
	out := readFile(t, path)
	if !strings.Contains(out, "example.com/acme/core => ../core") ||
		!strings.Contains(out, "example.com/acme/tools v0.1.0 => ../tools") {
		t.Fatalf("rewritten go.mod:\n%s", out)
	}
	links, err := writer.Links(path)
	if err != nil || renderLinks(links) != "example.com/acme/core=../core,example.com/acme/tools=../tools" {
		t.Fatalf("links = %+v, err = %v", links, err)
	}
	if !strings.Contains(out, "example.com/fork/upstream v0.1.0") {
		t.Error("a module-to-module replace was treated as a local link")
	}

	again, err := writer.Relink(path, []writer.Link{{Name: "example.com/acme/core", Path: "../core"}})
	if err != nil || len(again.Applied) != 0 {
		t.Fatalf("idempotent relink = %+v, err = %v", again, err)
	}

	moved, err := writer.Relink(path, []writer.Link{{Name: "example.com/acme/core", Path: "../moved"}})
	if err != nil || len(moved.Applied) != 1 {
		t.Fatalf("repoint = %+v, err = %v", moved, err)
	}

	missing, err := writer.Relink(path, []writer.Link{{Name: "example.com/acme/absent"}})
	if err != nil || len(missing.Missing) != 1 {
		t.Fatalf("removal of an absent replace = %+v, err = %v", missing, err)
	}

	dropped, err := writer.DropLinks(path)
	if err != nil || len(dropped.Applied) != 2 {
		t.Fatalf("drop = %+v, err = %v", dropped, err)
	}
	out = readFile(t, path)
	if strings.Contains(out, "=> ../") {
		t.Fatalf("a local replace survived removal:\n%s", out)
	}
	if !strings.Contains(out, "example.com/fork/upstream v0.1.0") {
		t.Fatalf("a module-to-module replace was removed:\n%s", out)
	}
}

func TestPublicAPIWriterSetsBuildCounters(t *testing.T) {
	t.Run("every format that carries one", func(t *testing.T) {
		cases := []struct{ name, rel, body, want string }{
			{"plist", "Info.plist",
				`<plist><dict><key>CFBundleVersion</key><string>7</string></dict></plist>`,
				"<string>42</string>"},
			{"android", "AndroidManifest.xml",
				`<manifest xmlns:android="http://schemas.android.com/apk/res/android" android:versionCode="7" />`,
				`android:versionCode="42"`},
			{"xcode", "project.pbxproj",
				"{\n\tbuildSettings = {\n\t\tCURRENT_PROJECT_VERSION = 7;\n\t};\n}\n",
				"CURRENT_PROJECT_VERSION = 42;"},
			{"gradle groovy", "build.gradle",
				"android {\n  defaultConfig {\n    versionCode 7\n  }\n}\n",
				"versionCode 42"},
			{"gradle kotlin", "build.gradle.kts",
				"android {\n  defaultConfig {\n    versionCode = 7\n  }\n}\n",
				"versionCode = 42"},
			{"pubspec with a counter", "pubspec.yaml", "name: acme\nversion: 1.0.0+7\n", "version: 1.0.0+42"},
			{"pubspec without one", "pubspec.yaml", "name: acme\nversion: 1.0.0\n", "version: 1.0.0+42"},
			{"unity", "ProjectSettings/ProjectSettings.asset",
				"PlayerSettings:\n  bundleVersion: 1.0.0\n  AndroidBundleVersionCode: 7\n",
				"AndroidBundleVersionCode: 42"},
			{"godot presets", "export_presets.cfg",
				"[preset.0]\n[preset.0.options]\nversion/code=7\n",
				"version/code=42"},
			{"uplugin", "Acme.uplugin", `{"Version":7,"VersionName":"1.0.0"}`, `"Version":42`},
			{"uplugin quoted", "Acme.uplugin", `{"Version":"7","VersionName":"1.0.0"}`, `"Version":"42"`},
			{"unreal engine config", "Config/DefaultEngine.ini",
				"[/Script/AndroidRuntimeSettings.AndroidRuntimeSettings]\nStoreVersion=7\n",
				"StoreVersion=42"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				path := writeFile(t, t.TempDir(), tc.rel, tc.body)
				res, err := writer.SetBuild(path, "42")
				if err != nil || !res.BuildWritten || res.Path != path {
					t.Fatalf("set build = %+v, err = %v", res, err)
				}
				out := readFile(t, path)
				if !strings.Contains(out, tc.want) {
					t.Fatalf("rewritten %s lacks %q:\n%s", tc.rel, tc.want, out)
				}
				if res.VersionWritten {
					t.Error("a counter write reported a version write")
				}
				again, err := writer.SetBuild(path, "42")
				if err != nil || again.BuildWritten {
					t.Fatalf("idempotent set build = %+v, err = %v", again, err)
				}
				if readFile(t, path) != out {
					t.Fatal("a no-op counter write rewrote the file")
				}
			})
		}
	})

	t.Run("a counter the file does not declare is not created", func(t *testing.T) {
		cases := []struct{ rel, body string }{
			{"Info.plist", `<plist><dict><key>CFBundleShortVersionString</key><string>1.0.0</string></dict></plist>`},
			{"Info.plist", `<plist><dict><key>CFBundleVersion</key><string>$(CURRENT_PROJECT_VERSION)</string></dict></plist>`},
			{"Info.plist", `<plist><array/></plist>`},
			{"AndroidManifest.xml", `<manifest package="com.acme.app" />`},
			{"project.pbxproj", "{\n\tbuildSettings = {\n\t\tMARKETING_VERSION = 1.0.0;\n\t};\n}\n"},
			{"build.gradle", "android {\n  defaultConfig {\n    versionName '1.0.0'\n  }\n}\n"},
			{"build.gradle", "dependencies {\n  implementation 'a:b:1.0'\n}\n"},
			{"pubspec.yaml", "name: acme\n"},
			{"pubspec.yaml", "name: acme\nversion:\n"},
			{"ProjectSettings/ProjectSettings.asset", "PlayerSettings:\n  bundleVersion: 1.0.0\n"},
			{"export_presets.cfg", "[preset.0]\nname=\"Android\"\n"},
			{"Acme.uplugin", `{"VersionName":"1.0.0"}`},
			{"Config/DefaultEngine.ini", "[/Script/Other]\nSomething=1\n"},
		}
		for _, tc := range cases {
			path := writeFile(t, t.TempDir(), tc.rel, tc.body)
			res, err := writer.SetBuild(path, "42")
			if err != nil {
				t.Errorf("%s (%q): %v", tc.rel, tc.body, err)
				continue
			}
			if res.BuildWritten {
				t.Errorf("%s (%q) grew a counter it never declared", tc.rel, tc.body)
			}
			if got := readFile(t, path); got != tc.body {
				t.Errorf("%s was rewritten:\n%s", tc.rel, got)
			}
		}
	})

	t.Run("refusals", func(t *testing.T) {
		if _, err := writer.SetBuild(writeFile(t, t.TempDir(), "Info.plist", "<plist/>"), ""); err == nil {
			t.Error("an empty counter was accepted")
		}
		notes := writeFile(t, t.TempDir(), "NOTES.md", "# notes\n")
		if _, err := writer.SetBuild(notes, "42"); !errors.Is(err, writer.ErrUnsupportedManifest) {
			t.Errorf("unsupported manifest = %v", err)
		}
		npm := writeFile(t, t.TempDir(), "package.json", `{"name":"acme"}`)
		if _, err := writer.SetBuild(npm, "42"); !errors.Is(err, writer.ErrNoBuildCounter) {
			t.Errorf("format with no counter = %v", err)
		}

		integers := []struct{ rel, body string }{
			{"AndroidManifest.xml", `<manifest xmlns:android="http://schemas.android.com/apk/res/android" android:versionCode="7" />`},
			{"build.gradle", "android {\n  defaultConfig {\n    versionCode 7\n  }\n}\n"},
			{"Acme.uplugin", `{"Version":7,"VersionName":"1.0.0"}`},
			{"Config/DefaultEngine.ini", "[/Script/AndroidRuntimeSettings.AndroidRuntimeSettings]\nStoreVersion=7\n"},
			{"export_presets.cfg", "[preset.0]\n[preset.0.options]\nversion/code=7\n"},
			{"ProjectSettings/ProjectSettings.asset", "PlayerSettings:\n  AndroidBundleVersionCode: 7\n"},
		}
		for _, tc := range integers {
			path := writeFile(t, t.TempDir(), tc.rel, tc.body)
			if _, err := writer.SetBuild(path, "1.0-beta"); err == nil {
				t.Errorf("%s accepted a counter that is not an integer", tc.rel)
			}
			if got := readFile(t, path); got != tc.body {
				t.Errorf("%s was changed by a refusal:\n%s", tc.rel, got)
			}
		}

		xcode := writeFile(t, t.TempDir(), "project.pbxproj",
			"{\n\tbuildSettings = {\n\t\tCURRENT_PROJECT_VERSION = 7;\n\t};\n}\n")
		if _, err := writer.SetBuild(xcode, `42";`); err == nil {
			t.Error("a project file accepted a counter carrying structural bytes")
		}

		malformed := []struct{ rel, body string }{
			{"Info.plist", "<plist><dict><key>a</key></plist>"},
			{"AndroidManifest.xml", `<manifest android:versionCode="7"><application></manifest>`},
			{"Acme.uplugin", `{"Version":`},
		}
		for _, tc := range malformed {
			path := writeFile(t, t.TempDir(), tc.rel, tc.body)
			if _, err := writer.SetBuild(path, "42"); err == nil {
				t.Errorf("%s: a malformed manifest accepted a counter", tc.rel)
			}
			if got := readFile(t, path); got != tc.body {
				t.Errorf("%s was changed by a refusal:\n%s", tc.rel, got)
			}
		}
	})

	t.Run("every declaring configuration moves together", func(t *testing.T) {
		path := writeFile(t, t.TempDir(), "project.pbxproj", `{
	objects = {
		DEBUG = {
			buildSettings = {
				CURRENT_PROJECT_VERSION = 7;
			};
		};
		RELEASE = {
			buildSettings = {
				CURRENT_PROJECT_VERSION = 7;
			};
		};
	};
}
`)
		if _, err := writer.SetBuild(path, "42"); err != nil {
			t.Fatal(err)
		}
		if strings.Count(readFile(t, path), "CURRENT_PROJECT_VERSION = 42;") != 2 {
			t.Fatalf("rewritten project file:\n%s", readFile(t, path))
		}
	})

	t.Run("a unity project stamping only per-platform counters", func(t *testing.T) {
		path := writeFile(t, t.TempDir(), "ProjectSettings/ProjectSettings.asset",
			"PlayerSettings:\n  bundleVersion: 1.0.0\n  buildNumber:\n    Standalone: 7\n    iPhone: 9\n")
		res, err := writer.SetBuild(path, "42")
		if err != nil || !res.BuildWritten {
			t.Fatalf("set build = %+v, err = %v", res, err)
		}
		out := readFile(t, path)
		if strings.Count(out, "42") != 2 {
			t.Fatalf("every per-platform counter must move together:\n%s", out)
		}
	})

	t.Run("every godot preset moves together", func(t *testing.T) {
		path := writeFile(t, t.TempDir(), "export_presets.cfg", `[preset.0]

name="Android"

[preset.0.options]

version/code=7

[preset.1]

name="iOS"

[preset.1.options]

version/code=7
`)
		if _, err := writer.SetBuild(path, "42"); err != nil {
			t.Fatal(err)
		}
		if strings.Count(readFile(t, path), "version/code=42") != 2 {
			t.Fatalf("rewritten presets:\n%s", readFile(t, path))
		}
	})

	t.Run("a counter deferring to another build setting is left alone", func(t *testing.T) {
		path := writeFile(t, t.TempDir(), "project.pbxproj", `{
	objects = {
		DEBUG = {
			buildSettings = {
				CURRENT_PROJECT_VERSION = 7;
			};
		};
		RELEASE = {
			buildSettings = {
				CURRENT_PROJECT_VERSION = "$(CURRENT_PROJECT_VERSION)";
			};
		};
	};
}
`)
		res, err := writer.SetBuild(path, "42")
		if err != nil || !res.BuildWritten {
			t.Fatalf("set build = %+v, err = %v", res, err)
		}
		out := readFile(t, path)
		if !strings.Contains(out, "CURRENT_PROJECT_VERSION = 42;") {
			t.Errorf("the literal counter was not written:\n%s", out)
		}
		if !strings.Contains(out, `CURRENT_PROJECT_VERSION = "$(CURRENT_PROJECT_VERSION)";`) {
			t.Errorf("a deferred counter was overwritten:\n%s", out)
		}
	})
}
