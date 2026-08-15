// Package scanner reads dependency manifests (package.json, go.mod,
// Cargo.toml, Dockerfiles and compose files among twenty-odd others) into one
// ecosystem-neutral shape: the package's declared identity (name, version) and
// its declared dependencies with their ranges and manifest fields. It only
// reads; rewriting manifests is the writer package's job.
//
// The scanner is deliberately lightweight: a handful of file-name probes and
// thin per-format parsers, no SBOM machinery. The recognised manifest names
// are fixed at build time; supporting a new ecosystem means adding a parser
// to this package.
package scanner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/yohimik/dispat/pkg/manifest"
)

// Kind is the manifest dependency field a declaration came from: the shared
// pkg/manifest vocabulary, aliased so the reader and the writer can never
// disagree on what a kind is called.
type Kind = manifest.Kind

// Dependency kinds, re-exported from pkg/manifest.
const (
	KindDependencies         = manifest.KindDependencies
	KindDevDependencies      = manifest.KindDevDependencies
	KindPeerDependencies     = manifest.KindPeerDependencies
	KindOptionalDependencies = manifest.KindOptionalDependencies
)

// kindRank orders kinds the way manifests conventionally list them; used only
// to keep a manifest's dependency slice deterministic.
func kindRank(k Kind) int {
	switch k {
	case KindDependencies:
		return 0
	case KindDevDependencies:
		return 1
	case KindPeerDependencies:
		return 2
	default:
		return 3
	}
}

// DeclaredDep is one dependency declaration inside a manifest.
type DeclaredDep struct {
	// Name as the manifest declares it: "@acme/core", "github.com/acme/x",
	// a crate name. For a renamed Cargo dependency this is the real package
	// name (the `package` key), not the alias.
	Name string
	// Range is the declared version text, verbatim: "^1.2.0", "workspace:*",
	// "v1.2.3". Empty when the manifest declares no version (e.g. a Cargo
	// path-only dependency).
	Range string
	// Kind is the manifest field the declaration sits in.
	Kind Kind
	// LocalPath is the declared filesystem path when the dependency points into
	// the same repository (an npm "file:"/"link:" range, a go.mod replace to a
	// relative path, a Cargo `path` key) relative to the manifest's folder. Empty
	// otherwise. It is the strongest workspace-edge signal: it survives name
	// mismatches between folder and manifest.
	LocalPath string
}

// Ecosystem names the package manager or platform family a manifest belongs
// to. Several formats can share one: every NuGet list is "nuget", and a
// Podfile and a podspec are both "cocoapods".
type Ecosystem string

// Ecosystems the built-in parsers recognise. The names are spelled after the
// manifest format rather than the platform that ships it: Info.plist is Apple
// bundle metadata on macOS and tvOS as much as on iOS, and a Gradle build
// script is not exclusively Android.
const (
	EcosystemNpm       Ecosystem = "npm"       // package.json
	EcosystemGoMod     Ecosystem = "gomod"     // go.mod
	EcosystemCargo     Ecosystem = "cargo"     // Cargo.toml
	EcosystemPython    Ecosystem = "python"    // pyproject.toml (PEP 621 and Poetry)
	EcosystemComposer  Ecosystem = "composer"  // composer.json
	EcosystemMaven     Ecosystem = "maven"     // pom.xml
	EcosystemNuGet     Ecosystem = "nuget"     // *.csproj, *.nuspec, packages.config
	EcosystemPub       Ecosystem = "pub"       // pubspec.yaml
	EcosystemPlist     Ecosystem = "plist"     // Info.plist
	EcosystemCocoaPods Ecosystem = "cocoapods" // Podfile, *.podspec
	EcosystemXcode     Ecosystem = "xcode"     // project.pbxproj
	EcosystemAndroid   Ecosystem = "android"   // AndroidManifest.xml
	EcosystemGradle    Ecosystem = "gradle"    // libs.versions.toml, build.gradle(.kts)
	EcosystemRubyGems  Ecosystem = "rubygems"  // Gemfile, *.gemspec
	EcosystemDocker    Ecosystem = "docker"    // Dockerfile, compose.yaml
)

// ecosystems maps each format onto the ecosystem its manifests report. It is
// the explicit spelling of what every parser sets, and the fence test walks it
// against manifest.Formats so a format can never arrive without one.
var ecosystems = map[manifest.Format]Ecosystem{
	manifest.FormatNpm:             EcosystemNpm,
	manifest.FormatGoMod:           EcosystemGoMod,
	manifest.FormatCargo:           EcosystemCargo,
	manifest.FormatPyProject:       EcosystemPython,
	manifest.FormatRequirements:    EcosystemPython,
	manifest.FormatComposer:        EcosystemComposer,
	manifest.FormatMaven:           EcosystemMaven,
	manifest.FormatMSBuildProject:  EcosystemNuGet,
	manifest.FormatNuSpec:          EcosystemNuGet,
	manifest.FormatPackagesProps:   EcosystemNuGet,
	manifest.FormatPackagesConfig:  EcosystemNuGet,
	manifest.FormatPubspec:         EcosystemPub,
	manifest.FormatPlist:           EcosystemPlist,
	manifest.FormatAndroidManifest: EcosystemAndroid,
	manifest.FormatGradleCatalog:   EcosystemGradle,
	manifest.FormatGradleBuild:     EcosystemGradle,
	manifest.FormatXcodeProject:    EcosystemXcode,
	manifest.FormatPodfile:         EcosystemCocoaPods,
	manifest.FormatPodspec:         EcosystemCocoaPods,
	manifest.FormatGemfile:         EcosystemRubyGems,
	manifest.FormatGemspec:         EcosystemRubyGems,
	manifest.FormatDockerfile:      EcosystemDocker,
	manifest.FormatCompose:         EcosystemDocker,
}

// EcosystemOf reports the ecosystem a format's manifests belong to.
func EcosystemOf(f manifest.Format) Ecosystem { return ecosystems[f] }

// Manifest is one parsed manifest file.
type Manifest struct {
	// Path of the manifest file relative to the scanned folder, using slashes.
	Path string
	// Ecosystem the manifest belongs to: one of the Ecosystem* constants.
	Ecosystem Ecosystem
	// Name is the package's declared name; empty when the ecosystem has no
	// name field or the manifest omits it.
	Name string
	// Version is the package's declared own version; empty when absent
	// (go.mod has none by design).
	Version string
	// BuildNumber is the monotonic build counter the mobile formats carry beside
	// their marketing version, CFBundleVersion, android:versionCode,
	// CURRENT_PROJECT_VERSION. It is not a semantic version and no writer
	// rewrites it; empty for every format without one.
	BuildNumber string
	// Deps are the manifest's declared dependencies, sorted by field then
	// name for deterministic output.
	Deps []DeclaredDep
	// Indirect are the requirements the manifest records as transitive
	// bookkeeping rather than as its own declarations, sorted the same way.
	// Only go.mod has the distinction, and only its parser fills this; every
	// other format leaves it nil.
	//
	// They are kept apart from Deps because they are not something the package
	// asked for, so reconciling their ranges would rewrite a number the
	// toolchain owns. What they are good for is redirection: only a main
	// module's replace directives govern a Go build, so a module reached
	// transitively still has to be pointed at a local folder from here. A
	// requirement listed in Deps never appears here as well.
	Indirect []DeclaredDep
	// Root reports that the manifest sits directly in the scanned folder
	// rather than in a sub-folder.
	Root bool
}

// Scanner turns a folder into its parsed manifests. Both methods share one
// error contract: a manifest that fails to parse is skipped, its error joined
// into the returned error, and the successfully parsed manifests are returned
// either way, so callers may report the error and keep the partial result. A
// folder Scan cannot read is stepped over on the same terms, so one
// unreadable sub-tree costs its own manifests and no others.
type Scanner interface {
	// Scan returns every recognised manifest under dir in deterministic
	// (path-sorted) order, descending into sub-folders but skipping
	// dependency and build-output folders (node_modules, vendor, dist, ...,
	// and every dot-folder).
	Scan(ctx context.Context, dir string) ([]Manifest, error)
	// ScanRoot parses only the manifests sitting directly in dir (the files that
	// declare the folder's own identity) without descending anywhere.
	ScanRoot(ctx context.Context, dir string) ([]Manifest, error)
}

// parseFunc parses one manifest file's bytes. rel is the file's path
// relative to the scanned folder (slash-separated).
type parseFunc func(rel string, data []byte) (Manifest, error)

// parsers maps each format pkg/manifest recognises onto its reader. The file
// names themselves live there, shared with the writer, so a format cannot be
// readable here and unwritable there without one of the two lists visibly
// lacking an entry.
var parsers = map[manifest.Format]parseFunc{
	manifest.FormatNpm:             parseNpm,
	manifest.FormatGoMod:           parseGoMod,
	manifest.FormatCargo:           parseCargo,
	manifest.FormatPyProject:       parsePython,
	manifest.FormatRequirements:    parseRequirements,
	manifest.FormatComposer:        parseComposer,
	manifest.FormatMaven:           parseMaven,
	manifest.FormatMSBuildProject:  parseCsproj,
	manifest.FormatNuSpec:          parseNuspec,
	manifest.FormatPackagesProps:   parsePackagesProps,
	manifest.FormatPackagesConfig:  parsePackagesConfig,
	manifest.FormatPubspec:         parsePubspec,
	manifest.FormatPlist:           parsePlist,
	manifest.FormatAndroidManifest: parseAndroidManifest,
	manifest.FormatGradleCatalog:   parseGradleCatalog,
	manifest.FormatGradleBuild:     parseGradleBuild,
	manifest.FormatXcodeProject:    parseXcodeProj,
	manifest.FormatPodfile:         parsePodfile,
	manifest.FormatPodspec:         parsePodspec,
	manifest.FormatGemfile:         parseGemfile,
	manifest.FormatGemspec:         parseGemspec,
	manifest.FormatDockerfile:      parseDockerfile,
	manifest.FormatCompose:         parseCompose,
}

// parserFor resolves a file name onto its parser.
func parserFor(name string) (parseFunc, bool) {
	format, ok := manifest.FormatOf(name)
	if !ok {
		return nil, false
	}
	parse, ok := parsers[format]
	return parse, ok
}

// skipDirs are folder names never descended into: installed dependencies,
// virtual environments and build output, where copied or generated manifests
// describe third-party code rather than the workspace (a `dist/package.json`
// is a build artifact; a Python venv contains thousands of third-party
// manifests under site-packages). Dot-folders are skipped separately, which
// already covers .gradle, SwiftPM's .build and Flutter's .symlinks; Gradle's
// own output folder is literally "build", listed here. An .xcodeproj is
// deliberately absent, project.pbxproj lives inside one.
var skipDirs = map[string]bool{
	"node_modules":     true,
	"bower_components": true,
	"vendor":           true,
	"target":           true,
	"dist":             true,
	"build":            true,
	"out":              true,
	"bin":              true,
	"obj":              true,
	"venv":             true,
	"env":              true,
	"__pycache__":      true,
	"Pods":             true,
	"Carthage":         true,
	"DerivedData":      true,
	"xcuserdata":       true,
}

// SkipDir reports a folder name a workspace walk must not enter: the
// dependency trees, virtual environments and build output listed above, plus
// every dot-folder. It is the rule Scan itself follows, exported so a caller
// walking a package folder for some other reason stays out of exactly the
// same places rather than keeping a second list that drifts from this one.
func SkipDir(name string) bool {
	return strings.HasPrefix(name, ".") || skipDirs[name]
}

// maxManifestBytes caps a single manifest read. A manifest is a hand-written
// file measured in kilobytes; anything near this bound is generated output or
// garbage, and a scanner that walks arbitrary checkouts must not slurp a
// 2 GB file into memory over a name collision.
const maxManifestBytes = 16 << 20

// ErrManifestTooLarge marks a manifest skipped for exceeding the read cap;
// joined into the scan error like any parse failure.
var ErrManifestTooLarge = errors.New("scanner: manifest exceeds 16 MiB")

// errNotAFile marks a directory wearing a manifest's name: a symlink to a
// folder called Podfile is not a manifest, and both scan entry points skip it
// the way the walk skips real directories.
var errNotAFile = errors.New("scanner: not a regular file")

// readManifest reads one manifest behind the size cap. The size is checked
// against the open handle and again against what was read, so a file growing
// between the two cannot slip past the cap.
func readManifest(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, errNotAFile
	}
	if info.Size() > maxManifestBytes {
		return nil, fmt.Errorf("%w (%d bytes)", ErrManifestTooLarge, info.Size())
	}
	data, err := io.ReadAll(io.LimitReader(f, maxManifestBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxManifestBytes {
		return nil, fmt.Errorf("%w (%d bytes)", ErrManifestTooLarge, int64(len(data)))
	}
	return data, nil
}

// fsScanner is the filesystem Scanner.
type fsScanner struct{}

// New returns the filesystem-backed Scanner.
func New() Scanner { return fsScanner{} }

// Scan implements Scanner.
func (fsScanner) Scan(ctx context.Context, dir string) ([]Manifest, error) {
	var (
		mans []Manifest
		errs []error
	)
	walkErr := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			// Cancellation ends the walk cleanly and joins the context error,
			// exactly as ScanRoot breaks its loop: the manifests already read
			// come back beside it either way.
			errs = append(errs, ctxErr)
			return filepath.SkipAll
		}
		if err != nil {
			// A folder the walk cannot read (permissions, a vanished entry) is
			// reported and stepped over rather than ending the scan: the
			// partial-result contract covers a whole sub-tree exactly as it
			// covers one unparseable file, and truncating the walk here would
			// silently leave the rest of the repository unscanned.
			errs = append(errs, err)
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if path != dir && SkipDir(name) {
				return filepath.SkipDir
			}
			return nil
		}
		parse, ok := parserFor(name)
		if !ok {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			// Even a path that cannot be made relative stays inside the
			// partial-result contract: reported and stepped over, never the
			// end of the walk.
			errs = append(errs, err)
			return nil
		}
		rel = filepath.ToSlash(rel)
		data, err := readManifest(path)
		if err != nil {
			if errors.Is(err, errNotAFile) {
				return nil
			}
			errs = append(errs, fmt.Errorf("%s: %w", rel, err))
			return nil
		}
		m, err := parse(rel, data)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", rel, err))
			return nil
		}
		mans = append(mans, m)
		return nil
	})
	if walkErr != nil {
		errs = append(errs, walkErr)
	}
	sort.Slice(mans, func(i, j int) bool { return mans[i].Path < mans[j].Path })
	return mans, errors.Join(errs...)
}

// ScanRoot implements Scanner.
func (fsScanner) ScanRoot(ctx context.Context, dir string) ([]Manifest, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	var (
		mans []Manifest
		errs []error
	)
	for _, name := range names {
		if ctxErr := ctx.Err(); ctxErr != nil {
			errs = append(errs, ctxErr)
			break
		}
		parse, ok := parserFor(name)
		if !ok {
			continue
		}
		data, err := readManifest(filepath.Join(dir, name))
		if err != nil {
			if errors.Is(err, errNotAFile) {
				continue
			}
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
			continue
		}
		m, err := parse(name, data)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
			continue
		}
		mans = append(mans, m)
	}
	return mans, errors.Join(errs...)
}

// Scan is the package-level convenience over New().Scan.
func Scan(ctx context.Context, dir string) ([]Manifest, error) {
	return New().Scan(ctx, dir)
}

// ScanRoot is the package-level convenience over New().ScanRoot.
func ScanRoot(ctx context.Context, dir string) ([]Manifest, error) {
	return New().ScanRoot(ctx, dir)
}

// Owner is one package's identity as NameIndex sees it: the names its
// configuration states outright, and the names its manifests declare.
type Owner struct {
	Package string
	// Names are the manifest names the package is known by regardless of what
	// its files say. They exist for the packages whose manifests declare no
	// name a workspace can learn: a Gradle module, a bare Makefile project, a
	// folder whose manifest this package cannot parse. A stated name outranks
	// a declared one, since it is the operator saying so.
	Names []string
	// Manifests are the package's parsed manifests.
	Manifests []Manifest
}

// Where a name came from, in the order the claims bind.
const (
	statedName = iota // Owner.Names: the configuration said so
	rootName          // a manifest sitting directly in the package folder
	nestedName        // a manifest somewhere below it
)

// NameIndex maps every manifest name onto the package it belongs to, under one
// rule shared by every consumer of the mapping: a stated name binds before a
// declared one, and a root manifest before a nested one (a package's own
// identity beats a vendored or example manifest deeper inside another
// package). A name two packages claim at the same rank is ambiguous, returned
// in ambiguous (sorted) instead of mapped, because deriving relations from it
// would be guessing.
func NameIndex(owners []Owner) (names map[string]string, ambiguous []string) {
	names = make(map[string]string)
	boundAt := make(map[string]int)
	dropped := make(map[string]bool)
	for rank := statedName; rank <= nestedName; rank++ {
		for _, o := range owners {
			for _, name := range ownerNames(o, rank) {
				owner, taken := names[name]
				switch {
				case !taken:
					names[name] = o.Package
					boundAt[name] = rank
				case owner != o.Package && boundAt[name] == rank && !dropped[name]:
					// A lower-ranked claim loses quietly; two claims of equal
					// rank have nothing to separate them.
					dropped[name] = true
					ambiguous = append(ambiguous, name)
				}
			}
		}
	}
	sort.Strings(ambiguous)
	for _, name := range ambiguous {
		delete(names, name)
	}
	return names, ambiguous
}

// ownerNames is the names one owner claims at one rank.
func ownerNames(o Owner, rank int) []string {
	if rank == statedName {
		return o.Names
	}
	claimed := make([]string, 0, len(o.Manifests))
	for _, m := range o.Manifests {
		if m.Name == "" || m.Root != (rank == rootName) {
			continue
		}
		claimed = append(claimed, m.Name)
	}
	return claimed
}

// ResolveLocalDir maps a declared local path (an npm "file:" range, a go.mod
// relative replace, a Cargo `path` key) onto the package whose folder it
// points into: dirs indexes cleaned package folders by name, pkgDir is the
// consuming package's folder, manifestRel the declaring manifest's
// slash-relative path inside it. The lookup ascends from the exact target, so
// a path into a package's sub-folder still finds the package. Empty when the
// path leaves every known package.
func ResolveLocalDir(dirs map[string]string, pkgDir, manifestRel, local string) string {
	dir := filepath.Clean(filepath.Join(pkgDir, filepath.Dir(filepath.FromSlash(manifestRel)), filepath.FromSlash(local)))
	for {
		if name, ok := dirs[dir]; ok {
			return name
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// sortDeps fixes a manifest's dependency order: by field, then name, then the
// remaining fields. Maps in the source formats carry no order worth
// preserving, and the comparator is total under a stable sort so two
// declarations tying on every key (a pubspec override next to its dependency,
// say) still come out in one deterministic order.
func sortDeps(deps []DeclaredDep) {
	sort.SliceStable(deps, func(i, j int) bool {
		if a, b := kindRank(deps[i].Kind), kindRank(deps[j].Kind); a != b {
			return a < b
		}
		if deps[i].Name != deps[j].Name {
			return deps[i].Name < deps[j].Name
		}
		if deps[i].Range != deps[j].Range {
			return deps[i].Range < deps[j].Range
		}
		return deps[i].LocalPath < deps[j].LocalPath
	})
}

// isRoot reports whether a slash-relative manifest path sits directly in the
// scanned folder.
func isRoot(rel string) bool { return !strings.Contains(rel, "/") }
