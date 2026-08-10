package manifest

import (
	"path/filepath"
	"strings"
)

// Format identifies one manifest file format. It exists so the reader and the
// writer agree on which files count as manifests and what each one is, instead
// of each keeping its own list of names. Those lists drifted before this type
// did the job: a format could be readable and silently unwritable, and adding
// one meant remembering to edit two modules.
type Format string

// The formats both halves recognise. The value is a stable identifier rather
// than a file name, because several names map onto one format.
const (
	FormatNpm             Format = "npm"              // package.json
	FormatGoMod           Format = "gomod"            // go.mod
	FormatCargo           Format = "cargo"            // Cargo.toml
	FormatPyProject       Format = "pyproject"        // pyproject.toml
	FormatRequirements    Format = "requirements"     // requirements*.txt
	FormatComposer        Format = "composer"         // composer.json
	FormatMaven           Format = "maven"            // pom.xml
	FormatMSBuildProject  Format = "msbuild-project"  // *.csproj, *.fsproj, *.vbproj
	FormatNuSpec          Format = "nuspec"           // *.nuspec
	FormatPackagesProps   Format = "packages-props"   // Directory.Packages.props
	FormatPackagesConfig  Format = "packages-config"  // packages.config
	FormatPubspec         Format = "pubspec"          // pubspec.yaml, pubspec.yml
	FormatPlist           Format = "plist"            // Info.plist
	FormatAndroidManifest Format = "android-manifest" // AndroidManifest.xml
	FormatGradleCatalog   Format = "gradle-catalog"   // libs.versions.toml
	FormatGradleBuild     Format = "gradle-build"     // build.gradle, build.gradle.kts
	FormatXcodeProject    Format = "xcode-project"    // project.pbxproj
	FormatPodfile         Format = "podfile"          // Podfile
	FormatPodspec         Format = "podspec"          // *.podspec
	FormatGemfile         Format = "gemfile"          // Gemfile
	FormatGemspec         Format = "gemspec"          // *.gemspec
)

// Formats lists every recognised format. Both halves range over it to prove
// they cover the same ground.
var Formats = []Format{
	FormatNpm, FormatGoMod, FormatCargo, FormatPyProject, FormatRequirements,
	FormatComposer, FormatMaven, FormatMSBuildProject, FormatNuSpec,
	FormatPackagesProps, FormatPackagesConfig, FormatPubspec, FormatPlist,
	FormatAndroidManifest, FormatGradleCatalog, FormatGradleBuild,
	FormatXcodeProject, FormatPodfile, FormatPodspec, FormatGemfile,
	FormatGemspec,
}

// byName maps an exact file name onto its format.
var byName = map[string]Format{
	"package.json":             FormatNpm,
	"go.mod":                   FormatGoMod,
	"Cargo.toml":               FormatCargo,
	"pyproject.toml":           FormatPyProject,
	"composer.json":            FormatComposer,
	"pom.xml":                  FormatMaven,
	"pubspec.yaml":             FormatPubspec,
	"pubspec.yml":              FormatPubspec,
	"Info.plist":               FormatPlist,
	"AndroidManifest.xml":      FormatAndroidManifest,
	"project.pbxproj":          FormatXcodeProject,
	"Podfile":                  FormatPodfile,
	"Gemfile":                  FormatGemfile,
	"Directory.Packages.props": FormatPackagesProps,
	"packages.config":          FormatPackagesConfig,
	"build.gradle":             FormatGradleBuild,
	"build.gradle.kts":         FormatGradleBuild,
	// A Gradle version catalog sits at gradle/libs.versions.toml by
	// convention, but settings.gradle may declare one anywhere, so the base
	// name is the honest match.
	"libs.versions.toml": FormatGradleCatalog,
}

// byExtension maps a file extension onto its format, for the families that
// name the file after the project rather than the format.
var byExtension = map[string]Format{
	".csproj":  FormatMSBuildProject,
	".fsproj":  FormatMSBuildProject,
	".vbproj":  FormatMSBuildProject,
	".nuspec":  FormatNuSpec,
	".podspec": FormatPodspec,
	".gemspec": FormatGemspec,
}

// FormatOf resolves a file's base name onto its format: by exact name, then by
// extension, then by the one family whose names vary. A name that matches
// nothing is not a manifest.
func FormatOf(name string) (Format, bool) {
	if f, ok := byName[name]; ok {
		return f, true
	}
	if f, ok := byExtension[filepath.Ext(name)]; ok {
		return f, true
	}
	if IsRequirementsFile(name) {
		return FormatRequirements, true
	}
	return "", false
}

// IsRequirementsFile reports a pip requirements file: a .txt whose base name
// starts or ends with the word "requirements" (requirements.txt,
// requirements-dev.txt, dev-requirements.txt). A name merely containing the
// word somewhere in the middle (OLD-REQUIREMENTS-NOTES.txt) is prose, not a
// manifest.
func IsRequirementsFile(name string) bool {
	lower := strings.ToLower(name)
	if !strings.HasSuffix(lower, ".txt") {
		return false
	}
	words := NameWords(strings.TrimSuffix(lower, ".txt"))
	return len(words) > 0 && (words[0] == "requirements" || words[len(words)-1] == "requirements")
}
