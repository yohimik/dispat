// Package writer rewrites dependency manifests in place, format-preserving:
// only the version text being changed is replaced, every other byte of the
// file — formatting, key order, comments — survives verbatim. It is the
// writing counterpart of the scanner package and shares its field-name
// spelling for dependency kinds.
//
// v1 writes package.json (byte-precise JSON scalar replacement) and go.mod
// (via golang.org/x/mod/modfile, format-preserving by design). Other
// ecosystems are read-only until they gain a writer.
package writer

import (
	"fmt"
	"path/filepath"
)

// Edit is one requested change inside a manifest: set the named dependency's
// declared version text within one manifest field.
type Edit struct {
	// Name of the dependency as the manifest declares it.
	Name string
	// Kind is the manifest field holding the declaration, spelled like the
	// field ("" means `dependencies`), matching the scanner's kinds.
	Kind string
	// Range is the new version text to write, verbatim: "1.2.3", "^1.2.3",
	// "workspace:*", "v1.2.3".
	Range string
}

// Result reports what a rewrite actually did.
type Result struct {
	// Applied are the edits that changed the file.
	Applied []Edit
	// Missing are the edits whose dependency was not declared in the
	// targeted field; the file is written without them.
	Missing []Edit
	// VersionWritten reports that the manifest's own version field was
	// rewritten.
	VersionWritten bool
}

// Rewrite applies the edits to the manifest file at path, dispatching on the
// file name (package.json, go.mod, requirements*.txt). version, when
// non-empty, also rewrites the manifest's own version field where the format
// has one (go.mod and requirements files do not). The file is rewritten only
// when something changed.
func Rewrite(path, version string, edits []Edit) (Result, error) {
	base := filepath.Base(path)
	switch {
	case base == "package.json":
		return rewriteNpm(path, version, edits)
	case base == "go.mod":
		return rewriteGoMod(path, edits)
	case isRequirementsFile(base):
		return rewriteRequirements(path, edits)
	default:
		return Result{}, fmt.Errorf("%s: no writer for this manifest", path)
	}
}

// Supported reports whether the manifest file name has a writer.
func Supported(path string) bool {
	base := filepath.Base(path)
	return base == "package.json" || base == "go.mod" || isRequirementsFile(base)
}
