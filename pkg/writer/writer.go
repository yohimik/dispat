// Package writer rewrites dependency manifests in place, format-preserving:
// only the version text being changed is replaced, every other byte of the
// file — formatting, key order, comments — survives verbatim. It is the
// writing counterpart of the scanner package and shares its field-name
// spelling for dependency kinds.
//
// Every ecosystem the scanner reads has a writer here: package.json and
// composer.json (byte-precise JSON scalar replacement), go.mod (via
// golang.org/x/mod/modfile, format-preserving by design), Cargo.toml,
// pyproject.toml and Gradle version catalogs (TOML span splicing), pom.xml and
// .csproj (XML span splicing), requirements files, pubspec.yaml, Podfiles,
// podspecs, Gemfiles and gemspecs (per-line splicing), and the platform
// manifests Info.plist, AndroidManifest.xml, project.pbxproj and Gradle build
// scripts.
//
// Every writer proves its output parses before a byte lands on disk. The
// formats with no cheap grammar to check against — the Xcode project file, the
// Ruby manifests and the Gradle build scripts — instead refuse any replacement
// carrying a byte that could end a literal, require the file's brace balance to
// be unchanged, and re-run the reader over the result.
//
// A value deferring to something outside the file is left alone rather than
// replaced with a literal, since overwriting it would sever the indirection it
// exists for: a Maven ${property}, a Cargo workspace inheritance, an Xcode
// $(MARKETING_VERSION), a gemspec's version constant, and a pom's parent
// version, which selects what the module inherits from rather than what it
// ships.
//
// Build numbers (CFBundleVersion, android:versionCode,
// CURRENT_PROJECT_VERSION) are deliberately not written: they are monotonic
// counters rather than semantic versions.
package writer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yohimik/dispat/pkg/manifest"
)

// Edit is one requested change inside a manifest: set the named dependency's
// declared version text within one manifest field.
type Edit struct {
	// Name of the dependency as the manifest declares it.
	Name string
	// Kind is the manifest field holding the declaration — the shared
	// pkg/manifest vocabulary, the same type the scanner reports. Only the
	// four dependency kinds are valid; Rewrite rejects anything else rather
	// than descend into an arbitrary part of the manifest.
	Kind manifest.Kind
	// Range is the new version text to write, verbatim: "1.2.3", "^1.2.3",
	// "workspace:*", "v1.2.3".
	Range string
}

// Result reports what a rewrite actually did.
type Result struct {
	// Path of the manifest the rewrite targeted, echoed back so a caller
	// batching several rewrites keeps the correlation.
	Path string
	// Applied are the edits that changed the file.
	Applied []Edit
	// Missing are the edits whose dependency the manifest does not declare in
	// the targeted field. The caller asked for something that is not there.
	Missing []Edit
	// Skipped are the edits whose dependency is declared but whose version
	// cannot be written: it defers to something outside the file (a Maven
	// ${property}, a Cargo workspace inheritance, an MSBuild or Xcode
	// property, a NuGet pack-time token), it is pinned to a git revision or a
	// folder instead of a version, or the format spreads the constraint across
	// several literals.
	//
	// Skipped is separate from Missing because the two call for different
	// responses. Missing usually means the caller and the manifest disagree
	// about what is declared. Skipped is the normal state of a healthy
	// manifest, and warning about it would be noise.
	Skipped []Edit
	// VersionWritten reports that the manifest's own version field was
	// rewritten.
	VersionWritten bool
}

// ErrUnsupportedManifest marks a path no writer covers; test with errors.Is.
var ErrUnsupportedManifest = errors.New("writer: no writer for this manifest")

// ErrManifestTooLarge marks a manifest refused for exceeding the read cap.
var ErrManifestTooLarge = errors.New("writer: manifest exceeds 16 MiB")

// maxManifestBytes mirrors the scanner's cap: a manifest is measured in
// kilobytes, and a writer must not slurp gigabytes over a name collision.
const maxManifestBytes = 16 << 20

// rewriteFunc rewrites one manifest format.
type rewriteFunc func(path, version string, edits []Edit) (Result, error)

// dispatch resolves a manifest file name onto its writer — the one table
// Supported and Rewrite share, so the two can never disagree.
func dispatch(base string) (rewriteFunc, bool) {
	switch {
	case base == "package.json":
		return rewriteNpm, true
	case base == "go.mod":
		return func(path, _ string, edits []Edit) (Result, error) { return rewriteGoMod(path, edits) }, true
	case isRequirementsFile(base):
		return func(path, _ string, edits []Edit) (Result, error) { return rewriteRequirements(path, edits) }, true
	case base == "Info.plist":
		return rewritePlist, true
	case base == "AndroidManifest.xml":
		return rewriteAndroidManifest, true
	case base == "libs.versions.toml":
		return func(path, _ string, edits []Edit) (Result, error) { return rewriteGradleCatalog(path, edits) }, true
	case base == "project.pbxproj":
		return rewriteXcodeProj, true
	case base == "Podfile":
		return func(path, _ string, edits []Edit) (Result, error) { return rewritePodfile(path, edits) }, true
	case strings.HasSuffix(base, ".podspec"):
		return rewritePodspec, true
	case base == "build.gradle", base == "build.gradle.kts":
		return rewriteGradleBuild, true
	case base == "Gemfile":
		return func(path, _ string, edits []Edit) (Result, error) { return rewriteGemfile(path, edits) }, true
	case strings.HasSuffix(base, ".gemspec"):
		return rewriteGemspec, true
	case base == "Cargo.toml":
		return rewriteCargo, true
	// F# and VB projects share the SDK-style schema the C# writer handles.
	case strings.HasSuffix(base, ".csproj"), strings.HasSuffix(base, ".fsproj"),
		strings.HasSuffix(base, ".vbproj"):
		return rewriteCsproj, true
	case strings.HasSuffix(base, ".nuspec"):
		return rewriteNuspec, true
	case base == "Directory.Packages.props":
		return func(path, _ string, edits []Edit) (Result, error) { return rewritePackagesProps(path, edits) }, true
	case base == "packages.config":
		return func(path, _ string, edits []Edit) (Result, error) { return rewritePackagesConfig(path, edits) }, true
	case base == "composer.json":
		return rewriteComposer, true
	case base == "pom.xml":
		return rewriteMaven, true
	case base == "pubspec.yaml", base == "pubspec.yml":
		return rewritePubspec, true
	case base == "pyproject.toml":
		return rewritePyproject, true
	}
	return nil, false
}

// Rewrite applies the edits to the manifest file at path, dispatching on the
// file name. version, when non-empty, also rewrites the manifest's own version
// field where the format has one — go.mod, requirements files, Podfiles,
// Gemfiles and the flat NuGet dependency lists have none, so the parameter is
// ignored for them and Result.VersionWritten stays false. The file is
// rewritten only when something changed.
func Rewrite(path, version string, edits []Edit) (Result, error) {
	for _, e := range edits {
		// The long spelling "dependencies" is accepted as a convenience for
		// the zero kind; anything else outside the four fields is refused.
		if k := e.Kind; !k.Valid() && k != "dependencies" {
			return Result{}, fmt.Errorf("writer: edit %q: unknown dependency kind %q", e.Name, k)
		}
	}
	rewrite, ok := dispatch(filepath.Base(path))
	if !ok {
		return Result{}, fmt.Errorf("%s: %w", path, ErrUnsupportedManifest)
	}
	info, err := os.Stat(path)
	if err != nil {
		return Result{}, err
	}
	if info.Size() > maxManifestBytes {
		return Result{}, fmt.Errorf("%s: %w (%d bytes)", path, ErrManifestTooLarge, info.Size())
	}
	res, err := rewrite(path, version, edits)
	res.Path = path
	return res, err
}

// Supported reports whether the manifest file name has a writer.
func Supported(path string) bool {
	_, ok := dispatch(filepath.Base(path))
	return ok
}
