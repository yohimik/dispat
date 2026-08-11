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
	} {
		if f, ok := FormatOf(name); ok {
			t.Errorf("FormatOf(%q) = %q, want no match", name, f)
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
