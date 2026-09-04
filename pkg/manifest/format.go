package manifest

import (
	"path/filepath"
	"sort"
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
	FormatAqua            Format = "aqua"             // aqua.yaml, .aqua.yaml, aqua/aqua.yaml

	// The game engines. Each keeps its version somewhere a package manager
	// would not look, and several of them declare dependencies beside it.
	FormatUnityPackages        Format = "unity-packages"         // Packages/manifest.json
	FormatUnityProjectSettings Format = "unity-project-settings" // ProjectSettings/ProjectSettings.asset
	FormatGodotProject         Format = "godot-project"          // project.godot
	FormatGodotPlugin          Format = "godot-plugin"           // plugin.cfg
	FormatGodotExportPresets   Format = "godot-export-presets"   // export_presets.cfg
	FormatUnrealProject        Format = "unreal-project"         // *.uproject
	FormatUnrealPlugin         Format = "unreal-plugin"          // *.uplugin
	FormatUnrealGameConfig     Format = "unreal-game-config"     // Config/DefaultGame.ini
	FormatUnrealEngineConfig   Format = "unreal-engine-config"   // Config/DefaultEngine.ini
	FormatDefoldProject        Format = "defold-project"         // game.project
	FormatO3DEProject          Format = "o3de-project"           // project.json
	FormatO3DEGem              Format = "o3de-gem"               // gem.json
)

// Formats lists every recognised format. Both halves range over it to prove
// they cover the same ground.
var Formats = []Format{
	FormatNpm, FormatGoMod, FormatCargo, FormatPyProject, FormatRequirements,
	FormatComposer, FormatMaven, FormatMSBuildProject, FormatNuSpec,
	FormatPackagesProps, FormatPackagesConfig, FormatPubspec, FormatPlist,
	FormatAndroidManifest, FormatGradleCatalog, FormatGradleBuild,
	FormatXcodeProject, FormatPodfile, FormatPodspec, FormatGemfile,
	FormatGemspec, FormatDockerfile, FormatCompose, FormatAqua,
	FormatUnityPackages, FormatUnityProjectSettings, FormatGodotProject,
	FormatGodotPlugin, FormatGodotExportPresets, FormatUnrealProject,
	FormatUnrealPlugin, FormatUnrealGameConfig, FormatUnrealEngineConfig,
	FormatDefoldProject, FormatO3DEProject, FormatO3DEGem,
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
	"aqua.yaml":                FormatAqua,
	"aqua.yml":                 FormatAqua,
	".aqua.yaml":               FormatAqua,
	".aqua.yml":                FormatAqua,
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
	// The engine files whose names say what they are wherever they sit. The
	// four that do not are in byPathSuffix instead.
	"project.godot":      FormatGodotProject,
	"plugin.cfg":         FormatGodotPlugin,
	"export_presets.cfg": FormatGodotExportPresets,
	"game.project":       FormatDefoldProject,
	"project.json":       FormatO3DEProject,
	"gem.json":           FormatO3DEGem,
}

// byExtension maps a file extension onto its format, for the families that
// name the file after the project rather than the format.
var byExtension = map[string]Format{
	".csproj":   FormatMSBuildProject,
	".fsproj":   FormatMSBuildProject,
	".vbproj":   FormatMSBuildProject,
	".nuspec":   FormatNuSpec,
	".podspec":  FormatPodspec,
	".gemspec":  FormatGemspec,
	".uproject": FormatUnrealProject,
	".uplugin":  FormatUnrealPlugin,
}

// byPathSuffix maps a slash-anchored path suffix onto its format, for the
// formats whose base name means something else everywhere else: manifest.json
// is a web app manifest in most repositories, .asset is every serialised Unity
// object, and an Unreal config file is only a manifest inside Config/.
var byPathSuffix = map[string]Format{
	"Packages/manifest.json":                FormatUnityPackages,
	"ProjectSettings/ProjectSettings.asset": FormatUnityProjectSettings,
	"Config/DefaultGame.ini":                FormatUnrealGameConfig,
	"Config/DefaultEngine.ini":              FormatUnrealEngineConfig,
}

// byPathBase maps an ambiguous base name onto the path suffixes worth testing.
// FormatOfPath runs once per file in a workspace walk, so the suffix scan is
// reached only by the handful of files that could match and every other file
// pays one map lookup instead of a comparison per suffix.
var byPathBase = func() map[string][]string {
	base := make(map[string][]string, len(byPathSuffix))
	for suffix := range byPathSuffix {
		name := suffix[strings.LastIndexByte(suffix, '/')+1:]
		base[name] = append(base[name], suffix)
	}
	// Longest first, so the most specific suffix wins and the order does not
	// depend on how the map above happened to be walked.
	for _, suffixes := range base {
		sort.Slice(suffixes, func(i, j int) bool { return len(suffixes[i]) > len(suffixes[j]) })
	}
	return base
}()

// byFormatSuffix is byPathSuffix read the other way, so a caller holding a
// format can ask where that format is kept without scanning the table.
var byFormatSuffix = func() map[Format]string {
	out := make(map[Format]string, len(byPathSuffix))
	for suffix, format := range byPathSuffix {
		out[format] = suffix
	}
	return out
}()

// PathSuffix returns the slash-anchored path a path-qualified format is always
// kept at, and whether the format has one at all.
//
// The four that do are the formats whose base name means something else
// everywhere else, which is why their folder is part of the name rather than a
// place the author chose: a Unity project's settings are
// ProjectSettings/ProjectSettings.asset or they are not those settings. A
// caller deciding whether a manifest belongs to the folder it scanned needs
// that distinction, because such a file is nested and still the folder's own.
func PathSuffix(f Format) (string, bool) {
	suffix, ok := byFormatSuffix[f]
	return suffix, ok
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

// slashed renders a path with forward separators whatever host it came from.
// filepath.ToSlash is not enough: it rewrites the running platform's separator
// only, so a Windows path handed to a Linux process keeps its backslashes and
// matches nothing. The cost is that a Unix file whose name genuinely contains a
// backslash is read as though it were nested, which is a trade every path here
// is happy to make.
func slashed(path string) string {
	if !strings.ContainsRune(path, '\\') {
		return path
	}
	return strings.ReplaceAll(path, `\`, "/")
}

// FormatOfPath resolves a file's path onto its format: first the formats only
// recognisable by where they sit, then FormatOf on the base name. The path may
// be relative or absolute, and may use either separator.
//
// It is the entry point a walk uses, because four formats cannot be told from
// their base name alone. FormatOf stays the answer where only a name is known,
// and deliberately never learns those four: a bare manifest.json is a web app
// manifest, and treating it as Unity's would be a guess.
func FormatOfPath(path string) (Format, bool) {
	base := path
	if i := strings.LastIndexAny(base, `/\`); i >= 0 {
		base = base[i+1:]
	}
	if suffixes, ok := byPathBase[base]; ok {
		p := slashed(path)
		for _, suffix := range suffixes {
			// Anchored on a separator, so "MyPackages/manifest.json" is not
			// Unity's and "x/Packages/manifest.json" is.
			if p == suffix || strings.HasSuffix(p, "/"+suffix) {
				return byPathSuffix[suffix], true
			}
		}
		// A base name reserved for a path-qualified format is nothing else.
		return "", false
	}
	return FormatOf(base)
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
