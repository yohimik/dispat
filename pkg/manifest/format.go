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
	FormatDockerfile      Format = "dockerfile"       // Dockerfile, Dockerfile.*, *.Dockerfile
	FormatCompose         Format = "compose"          // compose.yaml, docker-compose.yml, ...
)

// Formats lists every recognised format. Both halves range over it to prove
// they cover the same ground.
var Formats = []Format{
	FormatNpm, FormatGoMod, FormatCargo, FormatPyProject, FormatRequirements,
	FormatComposer, FormatMaven, FormatMSBuildProject, FormatNuSpec,
	FormatPackagesProps, FormatPackagesConfig, FormatPubspec, FormatPlist,
	FormatAndroidManifest, FormatGradleCatalog, FormatGradleBuild,
	FormatXcodeProject, FormatPodfile, FormatPodspec, FormatGemfile,
	FormatGemspec, FormatDockerfile, FormatCompose,
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
	// The Compose specification loads a base file and, beside it, an override
	// file that layers on top. Both are manifests: the override is where a
	// repository routinely pins the tag it actually deploys.
	"compose.yaml":                 FormatCompose,
	"compose.yml":                  FormatCompose,
	"compose.override.yaml":        FormatCompose,
	"compose.override.yml":         FormatCompose,
	"docker-compose.yaml":          FormatCompose,
	"docker-compose.yml":           FormatCompose,
	"docker-compose.override.yaml": FormatCompose,
	"docker-compose.override.yml":  FormatCompose,
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
// extension, then by the two families whose names vary. A name that matches
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
	if IsDockerfile(name) {
		return FormatDockerfile, true
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

// dockerBuildFileNames are the two spellings of a container build file: the
// Docker one and the Podman one, which is the same format under another name.
var dockerBuildFileNames = []string{"dockerfile", "containerfile"}

// proseExtensions are the suffixes that make a Dockerfile-prefixed name
// documentation rather than a build file. "Dockerfile.dev" is a Dockerfile;
// "Dockerfile.md" is an article about one.
var proseExtensions = map[string]bool{
	"md": true, "markdown": true, "txt": true, "rst": true, "adoc": true,
}

// IsDockerfile reports a container build file. Unlike every other format here,
// this one has no fixed name and no extension: Docker takes the base name
// "Dockerfile", the convention for a variant is a suffix ("Dockerfile.dev"),
// and the convention for keeping several in one folder is a prefix
// ("api.Dockerfile"). Podman's "Containerfile" is the same format spelled
// differently and is accepted on the same terms.
//
// The comparison ignores case, because "-f dockerfile" builds exactly as
// "-f Dockerfile" does and repositories spell it both ways.
func IsDockerfile(name string) bool {
	lower := strings.ToLower(name)
	for _, base := range dockerBuildFileNames {
		switch {
		case lower == base:
			return true
		case strings.HasPrefix(lower, base+"."):
			suffix := lower[len(base)+1:]
			if i := strings.LastIndexByte(suffix, '.'); i >= 0 {
				suffix = suffix[i+1:]
			}
			return suffix != "" && !proseExtensions[suffix]
		case strings.HasSuffix(lower, "."+base):
			// A leading dot would make the whole name the extension
			// (".dockerfile" is a hidden file, not a project's build file).
			return len(lower) > len(base)+1
		}
	}
	return false
}
