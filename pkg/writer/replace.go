package writer

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/yohimik/dispat/pkg/manifest"
)

// Replacement points one dependency at a local folder, the way a go.mod
// replace directive does:
//
//	replace github.com/acme/core => ../core
//
// It is the same idea in every format that has it: the dependency keeps its
// declaration, and a separate directive says where the code actually comes
// from. An empty Path removes that directive and lets the declaration resolve
// normally again, which is what a release has to do before publishing.
type Replacement struct {
	// Name of the dependency to redirect, spelled the way the manifest
	// declares it and the scanner reports it.
	Name string
	// Version narrows the redirect to one required version. Only go.mod can
	// express this (`replace acme/core v1.2.0 => ../core`); the other formats
	// key their directive on the name alone and ignore it.
	Version string
	// Path is the folder to point at, relative to the manifest, in the
	// spelling the reader gives back as DeclaredDep.LocalPath. Empty removes
	// the redirect.
	Path string
}

// ReplaceResult reports what a Replace call actually did. It splits the same
// three ways Result does, and for the same reasons, but over replacements
// rather than edits.
type ReplaceResult struct {
	// Path of the manifest the call targeted, echoed back so a caller
	// batching several keeps the correlation.
	Path string
	// Applied are the replacements that changed the file.
	Applied []Replacement
	// Missing are removals aimed at a directive the manifest does not have.
	Missing []Replacement
	// Skipped are the replacements the format cannot express. On a manifest
	// whose format has no redirect at all, that is every one of them.
	Skipped []Replacement
}

// replaceFunc applies replacements to one manifest format.
type replaceFunc func(path string, replacements []Replacement) (ReplaceResult, error)

// replacers maps each format onto its replace writer. Only formats that point
// a package at a local folder through a separate, package-keyed directive
// appear here.
//
// Formats whose redirect is an option on the dependency line instead of a
// directive of its own (a Gemfile's `path:`, a Podfile's `:path`, an editable
// requirements install) are absent: changing one means rewriting a
// declaration, not managing a directive.
var replacers = map[manifest.Format]replaceFunc{
	manifest.FormatNpm:       replaceNpm,
	manifest.FormatGoMod:     replaceGoMod,
	manifest.FormatCargo:     replaceCargo,
	manifest.FormatPubspec:   replacePubspec,
	manifest.FormatPyProject: replacePyproject,
}

// Replace redirects dependencies at local folders inside the manifest at path,
// and removes the redirects whose Path is empty. The file is rewritten only
// when something changed, atomically, and only after the result is proved to
// still parse.
//
// ReplaceResult carries the same three outcomes Rewrite reports. Applied are
// the replacements that changed the file. Missing are removals aimed at a
// directive the manifest does not have. Skipped are the ones the format cannot
// express, which is every replacement on a manifest with no such directive. A
// replacement already spelled exactly as asked is none of the three.
//
// A file name no manifest format claims gives ErrUnsupportedManifest, matching
// Rewrite. A recognised manifest whose format has no redirect writes nothing
// and reports every replacement skipped; SupportsReplace answers that in
// advance.
func Replace(path string, replacements []Replacement) (ReplaceResult, error) {
	for _, r := range replacements {
		if r.Name == "" {
			return ReplaceResult{}, fmt.Errorf("writer: replacement with no dependency name")
		}
	}
	format, ok := manifest.FormatOf(filepath.Base(path))
	if !ok {
		return ReplaceResult{}, fmt.Errorf("%s: %w", path, ErrUnsupportedManifest)
	}
	info, err := os.Stat(path)
	if err != nil {
		return ReplaceResult{}, err
	}
	if info.Size() > maxManifestBytes {
		return ReplaceResult{}, tooLarge(path, info.Size())
	}

	replace, ok := replacers[format]
	if !ok {
		// The manifest is one this package knows; its format simply has
		// nowhere to put a redirect.
		return ReplaceResult{Path: path, Skipped: replacements}, nil
	}
	res, err := replace(path, replacements)
	res.Path = path
	return res, err
}

// SupportsReplace reports whether the manifest file name has a format that can
// hold a redirect. It shares the replacers table with Replace, so the two can
// never disagree.
func SupportsReplace(path string) bool {
	format, ok := manifest.FormatOf(filepath.Base(path))
	if !ok {
		return false
	}
	_, ok = replacers[format]
	return ok
}
