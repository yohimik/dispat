package publicapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ccme "github.com/yohimik/dispat/pkg/ccme"
	"github.com/yohimik/dispat/pkg/config"
	configwatch "github.com/yohimik/dispat/pkg/config/watch"
	"github.com/yohimik/dispat/pkg/manifest"
	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/pkg/scanner"
	"github.com/yohimik/dispat/pkg/writer"
)

func TestPublicAPIScannerWriterScannerLifecycle(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"package.json":   `{"name":"web","version":"1.0.0","dependencies":{"core":"^1.0.0"}}`,
		"pyproject.toml": "[project]\nname = \"py\"\nversion = \"1.0.0\"\ndependencies = [\"core>=1.0\"]\n",
		"Cargo.toml":     "# retained author comment\n[package]\nname = \"rust\"\nversion = \"1.0.0\"\n[dependencies]\ncore = \"1.0\"\n",
		"pubspec.yaml":   "name: dart\nversion: 1.0.0\ndependencies:\n  core: ^1.0.0\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	scan := scanner.LocalScannerx{}
	write := writer.LocalWriterx{}
	before, err := scan.ScanRoot(context.Background(), dir)
	if err != nil || len(before) != len(files) {
		t.Fatalf("initial scan = %#v, err = %v", before, err)
	}
	wantRange := map[string]string{"pyproject.toml": ">=2.0", "Cargo.toml": "2.0", "package.json": "^2.0.0", "pubspec.yaml": "^2.0.0"}
	for _, m := range before {
		if !writer.IsSupported(m.Path) || !m.IsAtPackageRoot() {
			t.Fatalf("scanner returned format without writer support: %s", m.Path)
		}
		res, err := write.Rewrite(filepath.Join(dir, m.Path), "2.0.0", []writer.Edit{{
			Name: "core", Kind: manifest.KindDependencies, Range: wantRange[m.Path],
		}})
		if err != nil || !res.VersionWritten || len(res.Applied) != 1 {
			t.Fatalf("rewrite %s = %+v, err = %v", m.Path, res, err)
		}
	}
	after, err := scan.ScanRoot(context.Background(), dir)
	if err != nil || len(after) != len(before) {
		t.Fatalf("rescan = %#v, err = %v", after, err)
	}
	for _, m := range after {
		if m.Version != "2.0.0" || len(m.Deps) != 1 || m.Deps[0].Range != wantRange[m.Path] {
			t.Errorf("rewritten manifest %s = %+v", m.Path, m)
		}
	}
	cargo, err := os.ReadFile(filepath.Join(dir, "Cargo.toml"))
	if err != nil || !bytes.Contains(cargo, []byte("# retained author comment")) {
		t.Fatalf("unrelated Cargo text was not preserved: %q, err = %v", cargo, err)
	}

	corrupt := filepath.Join(dir, "broken", "package.json")
	if err := os.MkdirAll(filepath.Dir(corrupt), 0o755); err != nil {
		t.Fatal(err)
	}
	original := []byte(`{"name":"broken","version":`)
	if err := os.WriteFile(corrupt, original, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := write.Rewrite(corrupt, "2.0.0", nil); err == nil {
		t.Fatal("corrupt manifest rewrite succeeded")
	}
	retained, err := os.ReadFile(corrupt)
	if err != nil || !bytes.Equal(retained, original) {
		t.Fatalf("failed rewrite changed corrupt input: %q, err = %v", retained, err)
	}
}

func TestPublicAPIExtendedManifestLifecycles(t *testing.T) {
	cases := []struct {
		name, path, body, dependency, nextRange string
	}{
		{"go module", "go.mod", "module example.com/app\n\ngo 1.26\n\nrequire example.com/core v1.0.0\n", "example.com/core", "v1.1.0"},
		{"requirements", "requirements.txt", "# retained\nacme-core>=1.0.0\n", "acme-core", ">=2.0.0"},
		{"composer", "composer.json", `{"name":"acme/app","version":"1.0.0","require":{"acme/core":"^1.0.0"}}`, "acme/core", "^2.0.0"},
		{"maven", "pom.xml", `<project><modelVersion>4.0.0</modelVersion><groupId>acme</groupId><artifactId>app</artifactId><version>1.0.0</version><dependencies><dependency><groupId>acme</groupId><artifactId>core</artifactId><version>1.0.0</version></dependency></dependencies></project>`, "acme:core", "2.0.0"},
		{"msbuild", "Acme.csproj", `<Project><PropertyGroup><Version>1.0.0</Version></PropertyGroup><ItemGroup><PackageReference Include="Acme.Core" Version="1.0.0" /></ItemGroup></Project>`, "Acme.Core", "2.0.0"},
		{"nuspec", "Acme.nuspec", `<package><metadata><id>Acme</id><version>1.0.0</version><dependencies><dependency id="Acme.Core" version="1.0.0" /></dependencies></metadata></package>`, "Acme.Core", "2.0.0"},
		{"nuget props", "Directory.Packages.props", `<Project><ItemGroup><PackageVersion Include="Acme.Core" Version="1.0.0" /></ItemGroup></Project>`, "Acme.Core", "2.0.0"},
		{"nuget config", "packages.config", `<packages><package id="Acme.Core" version="1.0.0" /></packages>`, "Acme.Core", "2.0.0"},
		{"plist", "Info.plist", `<?xml version="1.0"?><plist><dict><key>CFBundleShortVersionString</key><string>1.0.0</string><key>CFBundleVersion</key><string>7</string></dict></plist>`, "", ""},
		{"android", "AndroidManifest.xml", `<manifest xmlns:android="http://schemas.android.com/apk/res/android" android:versionName="1.0.0" android:versionCode="7" package="com.acme.app" />`, "", ""},
		{"gradle catalog", "libs.versions.toml", `[versions]
core = "1.0.0"
[libraries]
core = { module = "com.acme:core", version.ref = "core" }
`, "com.acme:core", "2.0.0"},
		{"gradle groovy", "build.gradle", "android {\n  defaultConfig {\n    versionName \"1.0.0\"\n  }\n}\ndependencies {\n  implementation 'androidx.core:core-ktx:1.12.0'\n}\n", "androidx.core:core-ktx", "1.13.0"},
		{"podfile", "Podfile", "pod 'AcmeCore', '~> 1.0.0'\n", "AcmeCore", "~> 2.0.0"},
		{"podspec", "Acme.podspec", "Pod::Spec.new do |s|\n  s.name = 'Acme'\n  s.version = '1.0.0'\n  s.dependency 'AcmeCore', '~> 1.0.0'\nend\n", "AcmeCore", "~> 2.0.0"},
		{"gemfile", "Gemfile", "source 'https://rubygems.org'\ngem 'acme-core', '~> 1.0.0'\n", "acme-core", "~> 2.0.0"},
		{"gemspec", "acme.gemspec", "Gem::Specification.new do |s|\n  s.name = 'acme'\n  s.version = '1.0.0'\n  s.add_dependency 'acme-core', '~> 1.0.0'\nend\n", "acme-core", "~> 2.0.0"},
		{"docker", "Dockerfile", "# retained\nFROM ghcr.io/acme/core:1.0.0\n", "ghcr.io/acme/core", "2.0.0"},
		{"compose", "compose.yaml", "services:\n  app:\n    image: ghcr.io/acme/core:1.0.0\n", "", ""},
		{"aqua", "aqua.yaml", "packages:\n  - name: acme/core@v1.0.0\n", "acme/core", "v2.0.0"},
		{"xcode", "project.pbxproj", "{\n buildSettings = {\n  MARKETING_VERSION = 1.0.0;\n  CURRENT_PROJECT_VERSION = 7;\n };\n}\n", "", ""},
		{"unity packages", "Packages/manifest.json", `{"dependencies":{"com.acme.core":"1.0.0"}}`, "com.acme.core", "2.0.0"},
		{"unity settings", "ProjectSettings/ProjectSettings.asset", "PlayerSettings:\n  bundleVersion: 1.0.0 # retained\n  AndroidBundleVersionCode: 7\n", "", ""},
		{"godot project", "project.godot", "[application]\nconfig/name=\"Acme\"\nconfig/version=\"1.0.0\"\n", "", ""},
		{"godot plugin", "plugin.cfg", "[plugin]\nname=\"Acme\"\nversion=\"1.0.0\"\n", "", ""},
		{"godot export", "export_presets.cfg", "[preset.0]\nname=\"Android\"\n[preset.0.options]\nversion/code=7\nversion/name=\"1.0.0\"\n", "", ""},
		{"unreal project", "Server.uproject", `{"FileVersion":3,"EngineAssociation":"5.4","Plugins":[{"Name":"AcmeNet"}]}`, "", ""},
		{"unreal plugin", "AcmeNet.uplugin", `{"Version":7,"VersionName":"1.0.0"}`, "", ""},
		{"unreal game config", "Config/DefaultGame.ini", "[/Script/EngineSettings.GeneralProjectSettings]\nProjectName=Acme\nProjectVersion=1.0.0\n", "", ""},
		{"unreal engine config", "Config/DefaultEngine.ini", "[/Script/AndroidRuntimeSettings.AndroidRuntimeSettings]\nStoreVersion=7\nVersionDisplayName=1.0.0\n", "", ""},
		{"defold", "game.project", "[project]\ntitle = Acme\nversion = 1.0.0\n", "", ""},
		{"o3de project", "project.json", `{"project_name":"Acme","version":"1.0.0"}`, "", ""},
		{"o3de gem", "gem.json", `{"gem_name":"Acme","version":"1.0.0","dependencies":["Atom_RHI==1.0.0"]}`, "Atom_RHI", "==2.0.0"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, tc.path)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			scan := scanner.LocalScannerx{}
			before, err := scan.Scan(t.Context(), dir)
			if err != nil || len(before) != 1 {
				t.Fatalf("scan = %+v, err = %v", before, err)
			}
			var edits []writer.Edit
			if tc.dependency != "" {
				edits = []writer.Edit{{Name: tc.dependency, Kind: manifest.KindDependencies, Range: tc.nextRange}}
			}
			res, err := (writer.LocalWriterx{}).Rewrite(path, "2.0.0", edits)
			if err != nil {
				t.Fatal(err)
			}
			if tc.dependency != "" && len(res.Applied) != 1 {
				t.Fatalf("rewrite did not apply dependency: %+v", res)
			}
			after, err := scan.Scan(t.Context(), dir)
			if err != nil || len(after) != 1 {
				t.Fatalf("rescan = %+v, err = %v", after, err)
			}
			if tc.dependency != "" {
				found := false
				for _, dep := range after[0].Deps {
					if dep.Name == tc.dependency && dep.Range == tc.nextRange {
						found = true
					}
				}
				if !found {
					t.Fatalf("rewritten dependency absent after rescan: %+v", after[0])
				}
			}
		})
	}
}

func TestPublicAPIConfigReferenceEditAndReload(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "dispat.yaml")
	shared := filepath.Join(dir, "shared.yaml")
	if err := os.WriteFile(root, []byte("flow:\n  $ref: ./shared.yaml\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(shared, []byte("build:\n  - old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	l := config.NewLoader(config.Default())
	tree, err := l.ReadTree(context.Background(), root)
	if err != nil || tree.Root["flow"] == nil {
		t.Fatalf("referenced tree = %#v, err = %v", tree, err)
	}
	target, key, err := l.ResolveEdit(context.Background(), root, []string{"flow", "build"})
	if err != nil || target != shared || len(key) != 1 || key[0] != "build" {
		t.Fatalf("edit target = %q %q, err = %v", target, key, err)
	}
	if err := config.ApplyEdits(context.Background(), target, []config.Edit{{KeyPath: key, Value: []string{"new"}}}); err != nil {
		t.Fatal(err)
	}
	tree, err = l.ReadTree(context.Background(), root)
	if err != nil || tree.Root["flow"] == nil {
		t.Fatalf("reloaded tree = %#v, err = %v", tree, err)
	}
}

func TestPublicAPIConfigWatchReloadsAnAtomicEdit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dispat.json")
	if err := os.WriteFile(path, []byte(`{"value":"old"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	load := func(ctx context.Context) (string, []string, error) {
		tree, err := config.NewLoader(config.Default()).ReadTree(ctx, path)
		if err != nil {
			return "", nil, err
		}
		value, _ := tree.Root["value"].(string)
		return value, []string{path}, nil
	}
	updates := make(chan string, 1)
	w, err := configwatch.Start(t.Context(), configwatch.Options[string]{
		Load: load, Debounce: -1, OnUpdate: func(value string) { updates <- value },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if w.Value() != "old" {
		t.Fatalf("initial value = %q", w.Value())
	}
	if err := config.ApplyEdits(context.Background(), path, []config.Edit{{KeyPath: []string{"value"}, Value: "new"}}); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-updates:
		if got != "new" || w.Value() != "new" {
			t.Fatalf("update = %q, value = %q", got, w.Value())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("watcher did not observe atomic config edit")
	}
}

func TestPublicAPIRefusalsPreserveInputsAndBoundResources(t *testing.T) {
	t.Run("malformed manifests", func(t *testing.T) {
		cases := []struct{ name, body string }{
			{"package.json", `{"name":"broken","dependencies":{`},
			{"composer.json", `{"require":[`},
			{"Cargo.toml", "[package\nname = \"broken\"\n"},
			{"pom.xml", "<project><version>1.0.0</project>"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), tc.name)
				original := []byte(tc.body)
				if err := os.WriteFile(path, original, 0o644); err != nil {
					t.Fatal(err)
				}
				if _, err := (writer.LocalWriterx{}).Rewrite(path, "2.0.0", nil); err == nil {
					t.Fatal("malformed manifest rewrite succeeded")
				}
				got, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(got, original) {
					t.Fatalf("refusal changed input: %q, err = %v", got, err)
				}
			})
		}
	})

	t.Run("oversized manifest", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "package.json")
		if err := os.WriteFile(path, bytes.Repeat([]byte{' '}, (16<<20)+1), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := scanner.ScanRoot(t.Context(), filepath.Dir(path)); !errors.Is(err, scanner.ErrManifestTooLarge) {
			t.Fatalf("scanner error = %v", err)
		}
		if _, err := (writer.LocalWriterx{}).Rewrite(path, "2.0.0", nil); !errors.Is(err, writer.ErrManifestTooLarge) {
			t.Fatalf("writer error = %v", err)
		}
		info, err := os.Stat(path)
		if err != nil || info.Size() != (16<<20)+1 {
			t.Fatalf("oversized refusal changed size: %v, err = %v", info, err)
		}
	})

	t.Run("symlink and directory are not manifests", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "target.json")
		if err := os.WriteFile(target, []byte(`{"name":"safe","version":"1.0.0"}`), 0o644); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(dir, "package.json")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		if _, err := (writer.LocalWriterx{}).Rewrite(link, "2.0.0", nil); err == nil {
			t.Fatal("writer followed manifest symlink")
		}
		body, err := os.ReadFile(target)
		if err != nil || !bytes.Contains(body, []byte(`"1.0.0"`)) {
			t.Fatalf("symlink refusal changed target: %q, err = %v", body, err)
		}
		if err := os.Remove(link); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(link, 0o755); err != nil {
			t.Fatal(err)
		}
		manifests, err := scanner.ScanRoot(t.Context(), dir)
		if err != nil || len(manifests) != 0 {
			t.Fatalf("directory manifest scan = %+v, err = %v", manifests, err)
		}
	})

	t.Run("unreadable manifest is refused", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "package.json")
		original := []byte(`{"name":"safe","version":"1.0.0"}`)
		if err := os.WriteFile(path, original, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, 0); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(path, 0o600)
		if _, err := scanner.ScanRoot(t.Context(), filepath.Dir(path)); err == nil {
			t.Fatal("scanner accepted unreadable manifest")
		}
		if _, err := (writer.LocalWriterx{}).Rewrite(path, "2.0.0", nil); err == nil {
			t.Fatal("writer accepted unreadable manifest")
		}
		if err := os.Chmod(path, 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, original) {
			t.Fatalf("permission refusal changed input: %q, err = %v", got, err)
		}
	})

	t.Run("duplicate dependency identities remain distinct by kind", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "package.json")
		body := []byte(`{"name":"app","dependencies":{"core":"^1.0.0"},"devDependencies":{"core":"~1.0.0"}}`)
		if err := os.WriteFile(path, body, 0o644); err != nil {
			t.Fatal(err)
		}
		before, err := scanner.ScanRoot(t.Context(), dir)
		if err != nil || len(before) != 1 || len(before[0].Deps) != 2 {
			t.Fatalf("duplicate-kind scan = %+v, err = %v", before, err)
		}
		result, err := (writer.LocalWriterx{}).Rewrite(path, "", []writer.Edit{
			{Name: "core", Kind: manifest.KindDependencies, Range: "^2.0.0"},
			{Name: "core", Kind: manifest.KindDevDependencies, Range: "~3.0.0"},
		})
		if err != nil || len(result.Applied) != 2 {
			t.Fatalf("duplicate-kind rewrite = %+v, err = %v", result, err)
		}
		after, err := scanner.ScanRoot(t.Context(), dir)
		if err != nil || len(after) != 1 || len(after[0].Deps) != 2 {
			t.Fatalf("duplicate-kind rescan = %+v, err = %v", after, err)
		}
		got := map[manifest.Kind]string{}
		for _, dep := range after[0].Deps {
			got[dep.Kind] = dep.Range
		}
		if got[manifest.KindDependencies] != "^2.0.0" || got[manifest.KindDevDependencies] != "~3.0.0" {
			t.Fatalf("duplicate-kind ranges = %+v", got)
		}
	})

	t.Run("unsafe version and conflicting shared range", func(t *testing.T) {
		xcode := filepath.Join(t.TempDir(), "project.pbxproj")
		original := []byte("{\n buildSettings = { MARKETING_VERSION = 1.0.0; };\n}\n")
		if err := os.WriteFile(xcode, original, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := (writer.LocalWriterx{}).Rewrite(xcode, "2.0.0; MALICIOUS", nil); err == nil {
			t.Fatal("unsafe Xcode version succeeded")
		}
		got, _ := os.ReadFile(xcode)
		if !bytes.Equal(got, original) {
			t.Fatal("unsafe version changed Xcode project")
		}

		catalog := filepath.Join(t.TempDir(), "libs.versions.toml")
		catalogBody := []byte("[versions]\nkotlin = \"1.9.0\"\n[libraries]\na = { module = \"acme:a\", version.ref = \"kotlin\" }\nb = { module = \"acme:b\", version.ref = \"kotlin\" }\n")
		if err := os.WriteFile(catalog, catalogBody, 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := (writer.LocalWriterx{}).Rewrite(catalog, "", []writer.Edit{
			{Name: "acme:a", Kind: manifest.KindDependencies, Range: "2.0.0"},
			{Name: "acme:b", Kind: manifest.KindDependencies, Range: "3.0.0"},
		})
		if err == nil {
			t.Fatal("conflicting shared range succeeded")
		}
		got, _ = os.ReadFile(catalog)
		if !bytes.Equal(got, catalogBody) {
			t.Fatal("conflicting range changed catalog")
		}
	})
}

func TestPublicAPIConfigAndModelRejectMalformedBoundaries(t *testing.T) {
	t.Run("reference cycle and malformed document", func(t *testing.T) {
		dir := t.TempDir()
		a := filepath.Join(dir, "a.yaml")
		b := filepath.Join(dir, "b.yaml")
		if err := os.WriteFile(a, []byte("$ref: ./b.yaml\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(b, []byte("$ref: ./a.yaml\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := config.NewLoader(config.Default()).ReadTree(t.Context(), a); err == nil || !strings.Contains(err.Error(), "cycle") {
			t.Fatalf("cycle error = %v", err)
		}
		bad := filepath.Join(dir, "bad.json")
		original := []byte(`{"sync":`)
		if err := os.WriteFile(bad, original, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := config.NewLoader(config.Default()).ReadTree(t.Context(), bad); err == nil {
			t.Fatal("malformed config loaded")
		}
		got, _ := os.ReadFile(bad)
		if !bytes.Equal(got, original) {
			t.Fatal("config read failure changed input")
		}
	})

	t.Run("model rejects invalid script shape", func(t *testing.T) {
		var file models.File
		err := json.Unmarshal([]byte(`{"scripts":{"build":42}}`), &file)
		if err == nil || !strings.Contains(err.Error(), "script") {
			t.Fatalf("model error = %v", err)
		}
	})
}

func TestPublicAPILinkAndBuildWriterLifecycles(t *testing.T) {
	t.Run("local links round trip through every supported format", func(t *testing.T) {
		cases := []struct{ path, body, name string }{
			{"package.json", `{"name":"app","dependencies":{"core":"1.0.0"}}`, "core"},
			{"go.mod", "module example.com/app\n\ngo 1.26\n\nrequire example.com/core v1.0.0\n", "example.com/core"},
			{"Cargo.toml", "[package]\nname = \"app\"\nversion = \"1.0.0\"\n[dependencies]\ncore = \"1.0.0\"\n", "core"},
			{"pyproject.toml", "[project]\nname = \"app\"\nversion = \"1.0.0\"\ndependencies = [\"core==1.0.0\"]\n", "core"},
			{"pubspec.yaml", "name: app\nversion: 1.0.0\ndependencies:\n  core: ^1.0.0\n", "core"},
		}
		for _, tc := range cases {
			t.Run(tc.path, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), tc.path)
				if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
					t.Fatal(err)
				}
				if !writer.IsLinkSupported(path) {
					t.Fatalf("%s is not linkable", tc.path)
				}
				result, err := writer.Relink(path, []writer.Link{{Name: tc.name, Path: "../core"}})
				if err != nil || len(result.Applied) != 1 {
					t.Fatalf("relink = %+v, err = %v", result, err)
				}
				links, err := writer.Links(path)
				if err != nil || len(links) != 1 || links[0].Name != tc.name || links[0].Path != "../core" {
					t.Fatalf("links = %+v, err = %v", links, err)
				}
				if _, err := writer.DropLinks(path); err != nil {
					t.Fatal(err)
				}
				links, err = writer.Links(path)
				if err != nil || len(links) != 0 {
					t.Fatalf("links after drop = %+v, err = %v", links, err)
				}
				manifests, err := scanner.ScanRoot(t.Context(), filepath.Dir(path))
				if err != nil || len(manifests) != 1 {
					t.Fatalf("rescan after link lifecycle = %+v, err = %v", manifests, err)
				}
			})
		}
	})

	t.Run("build counters update and invalid counters preserve bytes", func(t *testing.T) {
		cases := []struct {
			path, body   string
			integerBuild bool
		}{
			{"Info.plist", `<plist><dict><key>CFBundleShortVersionString</key><string>1.0.0</string><key>CFBundleVersion</key><string>7</string></dict></plist>`, false},
			{"AndroidManifest.xml", `<manifest xmlns:android="http://schemas.android.com/apk/res/android" android:versionName="1.0.0" android:versionCode="7" />`, true},
			{"project.pbxproj", "{\n buildSettings = {\n  CURRENT_PROJECT_VERSION = 7;\n };\n}\n", false},
			{"build.gradle", "android {\n defaultConfig {\n  versionName '1.0.0'\n  versionCode 7\n }\n}\n", true},
			{"pubspec.yaml", "name: app\nversion: 1.0.0+7\n", false},
			{"ProjectSettings/ProjectSettings.asset", "PlayerSettings:\n  bundleVersion: 1.0.0\n  AndroidBundleVersionCode: 7\n", true},
			{"export_presets.cfg", "[preset.0]\n[preset.0.options]\nversion/code=7\nversion/name=\"1.0.0\"\n", true},
			{"App.uplugin", `{"Version":7,"VersionName":"1.0.0"}`, true},
			{"Config/DefaultEngine.ini", "[/Script/AndroidRuntimeSettings.AndroidRuntimeSettings]\nStoreVersion=7\nVersionDisplayName=1.0.0\n", true},
		}
		for _, tc := range cases {
			t.Run(tc.path, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), tc.path)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
					t.Fatal(err)
				}
				result, err := writer.SetBuild(path, "42")
				if err != nil || !result.BuildWritten {
					t.Fatalf("set build = %+v, err = %v", result, err)
				}
				updated, err := os.ReadFile(path)
				if err != nil || !bytes.Contains(updated, []byte("42")) {
					t.Fatalf("updated build = %q, err = %v", updated, err)
				}
				if tc.integerBuild {
					before := append([]byte(nil), updated...)
					if _, err := writer.SetBuild(path, "bad\nvalue"); err == nil {
						t.Fatal("invalid build counter succeeded")
					}
					after, err := os.ReadFile(path)
					if err != nil || !bytes.Equal(after, before) {
						t.Fatalf("invalid counter changed file: %q, err = %v", after, err)
					}
				}
			})
		}
	})
}

func TestPublicAPIConfigResolutionSettersAndDependencyModels(t *testing.T) {
	t.Run("resolved config decodes typed setters after overrides", func(t *testing.T) {
		type area struct {
			Path    []string
			Enabled bool
		}
		type application struct {
			Name        string
			Concurrency []int
			Env         map[string]string
			Areas       map[string]area
		}
		areaFields := func(dst *area) config.Fields {
			return config.Fields{"path": config.Strings(&dst.Path), "enabled": config.Bool(&dst.Enabled)}
		}
		fields := func(dst *application) config.Fields {
			return config.Fields{
				"name": config.String(&dst.Name), "concurrency": config.Ints(&dst.Concurrency),
				"env": config.StringMap(&dst.Env), "areas": config.ObjectMap(&dst.Areas, areaFields),
			}
		}

		root := t.TempDir()
		owned := filepath.Join(root, "packages", "api")
		deep := filepath.Join(owned, "src", "internal")
		if err := os.MkdirAll(deep, 0o755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(root, "app.yaml")
		body := "name: service\nconcurrency: 2,4\nenv:\n  MODE: release\nareas:\n  api:\n    path: packages/api\n    enabled: true\n"
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		loader := config.NewLoader(config.Default())
		resolved, resolvedRoot, err := loader.Resolve(t.Context(), deep, config.Resolver{
			Names: []string{"app.yaml"}, Classify: config.MarkerClassify([]string{"areas"}, nil),
			Owns: config.FolderOwner("areas", "path"),
		})
		if err != nil || resolved != path || resolvedRoot != root {
			t.Fatalf("resolve = %q, %q, err = %v", resolved, resolvedRoot, err)
		}
		tree, err := loader.ReadTree(t.Context(), resolved)
		if err != nil {
			t.Fatal(err)
		}
		var got application
		settings := tree.Settings(loader, config.Overrides{"name": "overridden", "areas.api.enabled": false})
		if err := config.DecodeObject(settings, "", fields(&got)); err != nil {
			t.Fatal(err)
		}
		if got.Name != "overridden" || len(got.Concurrency) != 2 || got.Concurrency[1] != 4 || got.Env["MODE"] != "release" || got.Areas["api"].Enabled {
			t.Fatalf("decoded application = %+v", got)
		}
	})

	t.Run("dependency models canonicalize and reject atomically", func(t *testing.T) {
		original := models.Dependencies{
			{Consumer: "app", Provider: "core"},
			{Consumer: "app", Provider: "tools", Kind: "devDependencies", Keep: true},
			{Consumer: "docs", Provider: "remote", External: true},
		}
		encoded, err := json.Marshal(original)
		if err != nil {
			t.Fatal(err)
		}
		var roundTrip models.Dependencies
		if err := json.Unmarshal(encoded, &roundTrip); err != nil {
			t.Fatal(err)
		}
		grouped := roundTrip.Grouped()
		if len(roundTrip) != 3 || len(grouped["app"]) != 2 || !grouped["app"][1].Keep || !grouped["docs"][0].External {
			t.Fatalf("dependency round trip = %+v (%s)", roundTrip, encoded)
		}
		before, _ := json.Marshal(roundTrip)
		if err := json.Unmarshal([]byte(`{"app":[{"provider":42}]}`), &roundTrip); err == nil {
			t.Fatal("invalid dependency model succeeded")
		}
		after, _ := json.Marshal(roundTrip)
		if !bytes.Equal(after, before) {
			t.Fatalf("failed dependency decode mutated receiver: before=%s after=%s", before, after)
		}
		providers := models.Providers("core", "tools")
		wire, err := json.Marshal(providers)
		if err != nil {
			t.Fatal(err)
		}
		var decoded models.ProviderList
		if err := json.Unmarshal(wire, &decoded); err != nil || len(decoded) != 2 {
			t.Fatalf("provider round trip = %+v, err = %v", decoded, err)
		}
	})
}

func TestPublicAPICCMEModelConsumptionContract(t *testing.T) {
	result, err := ccme.DefaultParser().Parse("fix(api)^+2: keep consumers aligned\n\nRelease-As: 2.1.0")
	if err != nil || len(result.ValidUnits()) != 1 {
		t.Fatalf("parse = %+v, err = %v", result, err)
	}
	unit := result.ValidUnits()[0]
	if unit.Header.Type != "fix" || unit.Bump != ccme.BumpPatch || unit.Directives.Depth != 2 {
		t.Fatalf("parsed unit = %+v", unit)
	}

	// The public model deliberately retains its stable CCME v1 dependency
	// while the v2 parser is a separate public module. Exercise the consumer
	// contract shared by both rather than coercing their Go types together.
	want := models.File{
		Configs: []string{"sources/lib/dispat.yaml"},
		RepositoryOverrides: map[string]models.RepositoryOverrideConfig{
			"lib": {Enabled: models.Bool(false)},
		},
		Scripts: map[string]models.Script{"Build": {"go test ./..."}},
	}
	body, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var got models.File
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	override, ok := got.RepositoryOverrides["lib"]
	if !ok || override.Enabled == nil || override.IsEnabled() {
		t.Fatalf("model round trip = %+v", got)
	}
	if absent := (models.RepositoryOverrideConfig{}); !absent.IsEnabled() {
		t.Fatalf("absent participation must default to enabled")
	}
	if script, ok := got.Script("build"); !ok || len(script) != 1 {
		t.Fatalf("case-folded script = %+v, %v", script, ok)
	}
}

func TestPublicAPICCMEConformanceVectors(t *testing.T) {
	strict, err := ccme.NewParser(ccme.Config{StrictTypes: true})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, message string
		parser        *ccme.Parser
		units         int
		invalid       bool
		explicitScope bool
	}{
		{"scoped feature", "feat(api): add endpoint", ccme.DefaultParser(), 1, false, true},
		{"unscoped fix", "fix: recover publication", ccme.DefaultParser(), 1, false, false},
		{"multi unit", "fix(api): one\n\n---\nfeat(cli): two", ccme.DefaultParser(), 2, false, true},
		{"escaped separator", "docs: explain \\--- literally", ccme.DefaultParser(), 1, false, false},
		{"strict unknown type", "invented: no contract", strict, 0, true, false},
		{"invalid utf8", string([]byte{'f', 'i', 'x', ':', ' ', 0xff}), ccme.DefaultParser(), 0, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, _ := tc.parser.Parse(tc.message)
			if result.IsInvalid() != tc.invalid || len(result.ValidUnits()) != tc.units {
				t.Fatalf("result invalid=%v validUnits=%d diagnostics=%+v", result.IsInvalid(), len(result.ValidUnits()), result.Diagnostics)
			}
			if tc.units > 0 && result.ValidUnits()[0].IsScopeExplicit() != tc.explicitScope {
				t.Fatalf("scope explicit = %v", result.ValidUnits()[0].IsScopeExplicit())
			}
		})
	}
}
