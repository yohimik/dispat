// Package scanner reads dependency manifests — package.json, go.mod,
// Cargo.toml — into one ecosystem-neutral shape: the package's declared
// identity (name, version) and its declared dependencies with their ranges
// and manifest fields. It only reads; rewriting manifests is the writer
// package's job.
//
// The scanner is deliberately lightweight: a handful of file-name probes and
// thin per-format parsers, no SBOM machinery. New ecosystems are added by
// registering another parser over their manifest file name.
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
)

// Kind is the manifest dependency field a declaration came from. The zero
// value is the plain `dependencies` field, mirroring the config model's
// dependency kinds, so the two convert by a cast.
type Kind string

// Dependency kinds, spelled exactly like the manifest fields they stand for.
const (
	KindDependencies         Kind = ""
	KindDevDependencies      Kind = "devDependencies"
	KindPeerDependencies     Kind = "peerDependencies"
	KindOptionalDependencies Kind = "optionalDependencies"
)

// String implements fmt.Stringer, spelling the zero value out.
func (k Kind) String() string {
	if k == KindDependencies {
		return "dependencies"
	}
	return string(k)
}

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

// Scanner turns a folder into its parsed manifests.
type Scanner interface {
	// Scan returns every recognised manifest under dir in deterministic
	// (path-sorted) order, descending into sub-folders but skipping
	// dependency and VCS folders (node_modules, vendor, target, dot-folders).
	//
	// A manifest that fails to parse is skipped, its error joined into the
	// returned error; the successfully parsed manifests are returned either
	// way, so callers may report the error and keep the partial result.
	Scan(ctx context.Context, dir string) ([]Manifest, error)
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

// skipDirs are folder names never descended into: installed dependencies and
// build output, where copied manifests describe third-party code.
var skipDirs = map[string]bool{
	"node_modules": true,
	"vendor":       true,
	"target":       true,
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
		data, err := os.ReadFile(path)
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

// ScanRoot parses only the manifests sitting directly in dir — the files
// that declare the folder's own identity — without descending anywhere. Same
// error contract as Scan: parse failures are joined into the error while the
// parsed manifests are returned either way.
func ScanRoot(dir string) ([]Manifest, error) {
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
		parse, ok := parserFor(name)
		if !ok {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
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

// sortDeps fixes a manifest's dependency order: by field, then name. Maps in
// the source formats carry no order worth preserving.
func sortDeps(deps []DeclaredDep) {
	sort.Slice(deps, func(i, j int) bool {
		if a, b := kindRank(deps[i].Kind), kindRank(deps[j].Kind); a != b {
			return a < b
		}
		return deps[i].Name < deps[j].Name
	})
}

// isRoot reports whether a slash-relative manifest path sits directly in the
// scanned folder.
func isRoot(rel string) bool { return !strings.Contains(rel, "/") }
