// Package scanner reads dependency manifests — package.json, go.mod,
// Cargo.toml — into one ecosystem-neutral shape: the package's declared
// identity (name, version) and its declared dependencies with their ranges
// and manifest fields. It only reads; rewriting manifests is the writer
// package's job.
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
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/yohimik/dispat/pkg/manifest"
)

// Kind is the manifest dependency field a declaration came from — the shared
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
	// LocalPath is the declared filesystem path when the dependency points
	// into the same repository — an npm "file:"/"link:" range, a go.mod
	// replace to a relative path, a Cargo `path` key — relative to the
	// manifest's folder. Empty otherwise. It is the strongest workspace-edge
	// signal: it survives name mismatches between folder and manifest.
	LocalPath string
}

// Ecosystems the built-in parsers recognise.
const (
	EcosystemNpm      = "npm"      // package.json
	EcosystemGoMod    = "gomod"    // go.mod
	EcosystemCargo    = "cargo"    // Cargo.toml
	EcosystemPython   = "python"   // pyproject.toml (PEP 621 and Poetry)
	EcosystemComposer = "composer" // composer.json
	EcosystemMaven    = "maven"    // pom.xml
	EcosystemNuGet    = "nuget"    // *.csproj
	EcosystemPub      = "pub"      // pubspec.yaml
)

// Manifest is one parsed manifest file.
type Manifest struct {
	// Path of the manifest file relative to the scanned folder, using slashes.
	Path string
	// Ecosystem the manifest belongs to: one of the Ecosystem* constants.
	Ecosystem string
	// Name is the package's declared name; empty when the ecosystem has no
	// name field or the manifest omits it.
	Name string
	// Version is the package's declared own version; empty when absent
	// (go.mod has none by design).
	Version string
	// Deps are the manifest's declared dependencies, sorted by field then
	// name for deterministic output.
	Deps []DeclaredDep
	// Root reports that the manifest sits directly in the scanned folder
	// rather than in a sub-folder.
	Root bool
}

// Scanner turns a folder into its parsed manifests. Both methods share one
// error contract: a manifest that fails to parse is skipped, its error joined
// into the returned error, and the successfully parsed manifests are returned
// either way, so callers may report the error and keep the partial result.
type Scanner interface {
	// Scan returns every recognised manifest under dir in deterministic
	// (path-sorted) order, descending into sub-folders but skipping
	// dependency and build-output folders (node_modules, vendor, dist, ...,
	// and every dot-folder).
	Scan(ctx context.Context, dir string) ([]Manifest, error)
	// ScanRoot parses only the manifests sitting directly in dir — the files
	// that declare the folder's own identity — without descending anywhere.
	ScanRoot(ctx context.Context, dir string) ([]Manifest, error)
}

// parseFunc parses one manifest file's bytes. rel is the file's path
// relative to the scanned folder (slash-separated).
type parseFunc func(rel string, data []byte) (Manifest, error)

// parsers maps exact manifest file names onto their parsers.
var parsers = map[string]parseFunc{
	"package.json":   parseNpm,
	"go.mod":         parseGoMod,
	"Cargo.toml":     parseCargo,
	"pyproject.toml": parsePython,
	"composer.json":  parseComposer,
	"pom.xml":        parseMaven,
	"pubspec.yaml":   parsePubspec,
	"pubspec.yml":    parsePubspec,
}

// suffixParsers recognise manifests by file extension — .NET projects name
// the file after the project (App.csproj), so an exact-name table cannot
// hold them.
var suffixParsers = map[string]parseFunc{
	".csproj": parseCsproj,
}

// patternParsers recognise manifests by an arbitrary name predicate — the
// line-by-line manifest families whose file names vary (requirements.txt,
// requirements-dev.txt, ...).
var patternParsers = []struct {
	match func(name string) bool
	parse parseFunc
}{
	{isRequirementsFile, parseRequirements},
}

// parserFor resolves a file name onto its parser: by exact name, then by
// suffix, then by pattern.
func parserFor(name string) (parseFunc, bool) {
	if parse, ok := parsers[name]; ok {
		return parse, true
	}
	if parse, ok := suffixParsers[filepath.Ext(name)]; ok {
		return parse, true
	}
	for _, p := range patternParsers {
		if p.match(name) {
			return p.parse, true
		}
	}
	return nil, false
}

// skipDirs are folder names never descended into: installed dependencies,
// virtual environments and build output, where copied or generated manifests
// describe third-party code rather than the workspace (a `dist/package.json`
// is a build artifact; a Python venv contains thousands of third-party
// manifests under site-packages). Dot-folders are skipped separately.
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
}

// maxManifestBytes caps a single manifest read. A manifest is a hand-written
// file measured in kilobytes; anything near this bound is generated output or
// garbage, and a scanner that walks arbitrary checkouts must not slurp a
// 2 GB file into memory over a name collision.
const maxManifestBytes = 16 << 20

// ErrManifestTooLarge marks a manifest skipped for exceeding the read cap;
// joined into the scan error like any parse failure.
var ErrManifestTooLarge = errors.New("scanner: manifest exceeds 16 MiB")

// readManifest is os.ReadFile behind the size cap.
func readManifest(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > maxManifestBytes {
		return nil, fmt.Errorf("%w (%d bytes)", ErrManifestTooLarge, info.Size())
	}
	return os.ReadFile(path)
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
			return ctxErr
		}
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if path != dir && (skipDirs[name] || strings.HasPrefix(name, ".")) {
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
			return err
		}
		rel = filepath.ToSlash(rel)
		data, err := readManifest(path)
		if err != nil {
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

// ScanRoot is the package-level convenience over New().ScanRoot.
func ScanRoot(ctx context.Context, dir string) ([]Manifest, error) {
	return New().ScanRoot(ctx, dir)
}

// Owner is one package's parsed manifests: the input NameIndex maps over.
type Owner struct {
	Package   string
	Manifests []Manifest
}

// NameIndex maps every declared manifest name onto the package declaring it,
// under one rule shared by every consumer of the mapping: root manifests bind
// before nested ones (a package's own identity beats a vendored or example
// manifest deeper inside another package), and a name two packages declare at
// root priority is ambiguous — returned in ambiguous (sorted) instead of
// mapped, because deriving relations from it would be guessing.
func NameIndex(owners []Owner) (names map[string]string, ambiguous []string) {
	names = make(map[string]string)
	dropped := make(map[string]bool)
	for _, root := range []bool{true, false} {
		for _, o := range owners {
			for _, m := range o.Manifests {
				if m.Root != root || m.Name == "" {
					continue
				}
				owner, taken := names[m.Name]
				switch {
				case !taken:
					names[m.Name] = o.Package
				case owner != o.Package && root && !dropped[m.Name]:
					dropped[m.Name] = true
					ambiguous = append(ambiguous, m.Name)
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
