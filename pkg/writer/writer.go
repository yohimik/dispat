// Package writer rewrites dependency manifests in place, format-preserving:
// only the version text being changed is replaced, every other byte of the
// file — formatting, key order, comments — survives verbatim. It is the
// writing counterpart of the scanner package and shares its field-name
// spelling for dependency kinds.
//
// v1 writes package.json (byte-precise JSON scalar replacement), go.mod (via
// golang.org/x/mod/modfile, format-preserving by design) and requirements
// files (per-line splicing). Other ecosystems are read-only until they gain a
// writer, and only package.json has an own-version field to write.
package writer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Edit is one requested change inside a manifest: set the named dependency's
// declared version text within one manifest field.
type Edit struct {
	// Name of the dependency as the manifest declares it.
	Name string
	// Kind is the manifest field holding the declaration, spelled like the
	// field ("" means `dependencies`), matching the scanner's kinds. Only the
	// four dependency fields are valid; Rewrite rejects anything else rather
	// than descend into an arbitrary part of the manifest.
	Kind string
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
	// Missing are the edits whose dependency was not declared in the
	// targeted field; the file is written without them.
	Missing []Edit
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
	}
	return nil, false
}

// validKinds are the dependency fields an Edit may target.
var validKinds = map[string]bool{
	"": true, "dependencies": true, "devDependencies": true,
	"peerDependencies": true, "optionalDependencies": true,
}

// Rewrite applies the edits to the manifest file at path, dispatching on the
// file name (package.json, go.mod, requirements*.txt). version, when
// non-empty, also rewrites the manifest's own version field where the format
// has one (go.mod and requirements files have none; the parameter is ignored
// for them and Result.VersionWritten stays false). The file is rewritten only
// when something changed.
func Rewrite(path, version string, edits []Edit) (Result, error) {
	for _, e := range edits {
		if !validKinds[e.Kind] {
			return Result{}, fmt.Errorf("writer: edit %q: unknown dependency kind %q", e.Name, e.Kind)
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
