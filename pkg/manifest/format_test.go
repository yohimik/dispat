package manifest

import (
	"slices"
	"testing"
)

func TestFormatOfRecognisesEveryName(t *testing.T) {
	for _, tc := range []struct {
		name string
		want Format
	}{
		{"package.json", FormatNpm},
		{"go.mod", FormatGoMod},
		{"Cargo.toml", FormatCargo},
		{"pyproject.toml", FormatPyProject},
		{"requirements.txt", FormatRequirements},
		{"requirements-dev.txt", FormatRequirements},
		{"dev-requirements.txt", FormatRequirements},
		{"composer.json", FormatComposer},
		{"pom.xml", FormatMaven},
		{"App.csproj", FormatMSBuildProject},
		{"App.fsproj", FormatMSBuildProject},
		{"App.vbproj", FormatMSBuildProject},
		{"Acme.nuspec", FormatNuSpec},
		{"Directory.Packages.props", FormatPackagesProps},
		{"packages.config", FormatPackagesConfig},
		{"pubspec.yaml", FormatPubspec},
		{"pubspec.yml", FormatPubspec},
		{"Info.plist", FormatPlist},
		{"AndroidManifest.xml", FormatAndroidManifest},
		{"libs.versions.toml", FormatGradleCatalog},
		{"build.gradle", FormatGradleBuild},
		{"build.gradle.kts", FormatGradleBuild},
		{"project.pbxproj", FormatXcodeProject},
		{"Podfile", FormatPodfile},
		{"Alamofire.podspec", FormatPodspec},
		{"Gemfile", FormatGemfile},
		{"acme.gemspec", FormatGemspec},
		{"Dockerfile", FormatDockerfile},
		{"dockerfile", FormatDockerfile},
		{"Dockerfile.dev", FormatDockerfile},
		{"api.Dockerfile", FormatDockerfile},
		{"Containerfile", FormatDockerfile},
		{"Containerfile.build", FormatDockerfile},
		{"api.containerfile", FormatDockerfile},
		{"compose.yaml", FormatCompose},
		{"compose.yml", FormatCompose},
		{"compose.override.yaml", FormatCompose},
		{"compose.override.yml", FormatCompose},
		{"docker-compose.yaml", FormatCompose},
		{"docker-compose.yml", FormatCompose},
		{"docker-compose.override.yaml", FormatCompose},
		{"docker-compose.override.yml", FormatCompose},
		{"aqua.yaml", FormatAqua},
		{"aqua.yml", FormatAqua},
		{".aqua.yaml", FormatAqua},
		{".aqua.yml", FormatAqua},
		{"project.godot", FormatGodotProject},
		{"plugin.cfg", FormatGodotPlugin},
		{"export_presets.cfg", FormatGodotExportPresets},
		{"MyGame.uproject", FormatUnrealProject},
		{"AcmeNet.uplugin", FormatUnrealPlugin},
		{"game.project", FormatDefoldProject},
		{"project.json", FormatO3DEProject},
		{"gem.json", FormatO3DEGem},
	} {
		got, ok := FormatOf(tc.name)
		if !ok || got != tc.want {
			t.Errorf("FormatOf(%q) = %q,%v, want %q", tc.name, got, ok, tc.want)
		}
	}
}

func TestFormatOfRejectsNearMisses(t *testing.T) {
	// The rules must not over-match. Each of these contains a manifest's name
	// or extension without being one.
	for _, name := range []string{
		"", "README.md", "notes.txt", "Podfilex", "Gemfile.lock",
		"notes.podspec.txt", "settings.gradle", "packages.lock.json",
		"Directory.Build.props", "package.json.bak", "my-go.mod",
		"OLD-REQUIREMENTS-NOTES.txt", "requirements.md",
		".dockerignore", ".dockerfile", "Dockerfile.md", "Dockerfile.txt",
		"Dockerfile.", "dockerfile-notes.md", "docker-compose.prod.yml",
		"compose.json", "my-compose.yaml", "Containerfile.rst",
		"MyGame.uprojectdirs", "project.godot.bak", "plugin.cfgx",
		// The four path-qualified names are nothing on their own. A bare
		// manifest.json is a web app manifest, and FormatOf is told only a name.
		"manifest.json", "ProjectSettings.asset", "DefaultGame.ini", "DefaultEngine.ini",
	} {
		if f, ok := FormatOf(name); ok {
			t.Errorf("FormatOf(%q) = %q, want no match", name, f)
		}
	}
}

func TestFormatOfPathResolvesTheFormatsThatNeedTheirFolder(t *testing.T) {
	for _, tc := range []struct {
		path string
		want Format
	}{
		// Relative, exactly the suffix, and nested behind any prefix.
		{"Packages/manifest.json", FormatUnityPackages},
		{"apps/client/Packages/manifest.json", FormatUnityPackages},
		{"ProjectSettings/ProjectSettings.asset", FormatUnityProjectSettings},
		{"game/ProjectSettings/ProjectSettings.asset", FormatUnityProjectSettings},
		{"Config/DefaultGame.ini", FormatUnrealGameConfig},
		{"Config/DefaultEngine.ini", FormatUnrealEngineConfig},
		// Absolute, which is what every writer call site passes.
		{"/repo/apps/client/Packages/manifest.json", FormatUnityPackages},
		{"/repo/server/Config/DefaultGame.ini", FormatUnrealGameConfig},
		// Windows separators, which is what WalkDir hands over there.
		{`C:\repo\client\Packages\manifest.json`, FormatUnityPackages},
		{`apps\server\Config\DefaultEngine.ini`, FormatUnrealEngineConfig},
		// Everything else still resolves by its base name.
		{"apps/web/package.json", FormatNpm},
		{"go.mod", FormatGoMod},
		{"tools/editor/project.godot", FormatGodotProject},
		{"Plugins/AcmeNet/AcmeNet.uplugin", FormatUnrealPlugin},
	} {
		got, ok := FormatOfPath(tc.path)
		if !ok || got != tc.want {
			t.Errorf("FormatOfPath(%q) = %q,%v, want %q", tc.path, got, ok, tc.want)
		}
	}
}

func TestFormatOfPathRejectsTheAlmostRightFolder(t *testing.T) {
	// The suffix match is anchored on a separator, and a base name reserved for
	// a path-qualified format is nothing anywhere else. Both halves matter: the
	// first stops MyPackages/ passing for Packages/, the second stops every
	// web app manifest in every repository being read as Unity's.
	for _, path := range []string{
		"manifest.json",
		"web/manifest.json",
		"public/manifest.json",
		"MyPackages/manifest.json",
		"src/Packages.old/manifest.json",
		"ProjectSettings.asset",
		"Assets/Materials/ProjectSettings.asset",
		"MyProjectSettings/ProjectSettings.asset",
		"DefaultGame.ini",
		"Saved/Config/Windows/DefaultGame.ini.bak",
		"NotConfig/DefaultEngine.ini",
	} {
		if f, ok := FormatOfPath(path); ok {
			t.Errorf("FormatOfPath(%q) = %q, want no match", path, f)
		}
	}
}

func TestFormatOfPathAgreesWithFormatOfEverywhereElse(t *testing.T) {
	// The two entry points may differ only on the path-qualified formats. Any
	// other disagreement means a file is readable through one door and not the
	// other, which is the drift the shared table exists to prevent.
	for name := range byName {
		want, _ := FormatOf(name)
		got, ok := FormatOfPath("some/folder/" + name)
		if !ok || got != want {
			t.Errorf("FormatOfPath(some/folder/%s) = %q,%v, want %q", name, got, ok, want)
		}
	}
}

func TestPathSuffixAnswersForTheFormatsThatNeedTheirFolder(t *testing.T) {
	for _, tc := range []struct {
		format Format
		want   string
	}{
		{FormatUnityPackages, "Packages/manifest.json"},
		{FormatUnityProjectSettings, "ProjectSettings/ProjectSettings.asset"},
		{FormatUnrealGameConfig, "Config/DefaultGame.ini"},
		{FormatUnrealEngineConfig, "Config/DefaultEngine.ini"},
	} {
		got, ok := PathSuffix(tc.format)
		if !ok || got != tc.want {
			t.Errorf("PathSuffix(%q) = %q,%v, want %q", tc.format, got, ok, tc.want)
		}
	}
}

func TestPathSuffixDeclinesEveryOtherFormat(t *testing.T) {
	// A format named by its base name alone has no folder to report, whether
	// the name is fixed (go.mod), an extension family (*.uplugin) or one the
	// engine also keeps in a folder by convention (project.godot).
	for _, format := range []Format{FormatNpm, FormatGoMod, FormatGodotProject, FormatUnrealPlugin, FormatO3DEProject} {
		if suffix, ok := PathSuffix(format); ok {
			t.Errorf("PathSuffix(%q) = %q,true, want no suffix", format, suffix)
		}
	}
	if suffix, ok := PathSuffix(Format("not-a-format")); ok {
		t.Errorf("PathSuffix of an unknown format = %q,true, want no suffix", suffix)
	}
}

func TestPathSuffixAgreesWithFormatOfPath(t *testing.T) {
	// The two halves of the same table: a format's own suffix has to resolve
	// back to it, or one of them has drifted.
	for _, format := range Formats {
		suffix, ok := PathSuffix(format)
		if !ok {
			continue
		}
		got, ok := FormatOfPath(suffix)
		if !ok || got != format {
			t.Errorf("FormatOfPath(PathSuffix(%q)) = %q,%v, want %q", format, got, ok, format)
		}
	}
}

func TestFormatsListsEveryConstantOnce(t *testing.T) {
	seen := map[Format]bool{}
	for _, f := range Formats {
		if seen[f] {
			t.Errorf("%q appears twice in Formats", f)
		}
		seen[f] = true
		if f == "" {
			t.Error("Formats holds an empty format")
		}
	}
	// Every format a name resolves to must be listed, or the two halves have
	// nothing to range over when they check their coverage.
	for name := range byName {
		f, _ := FormatOf(name)
		if !slices.Contains(Formats, f) {
			t.Errorf("%q resolves to %q, which Formats omits", name, f)
		}
	}
	for ext := range byExtension {
		f, _ := FormatOf("x" + ext)
		if !slices.Contains(Formats, f) {
			t.Errorf("%q resolves to %q, which Formats omits", ext, f)
		}
	}
	if f, _ := FormatOf("requirements.txt"); !slices.Contains(Formats, f) {
		t.Error("the requirements format is missing from Formats")
	}
}
