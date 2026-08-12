package writer

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/yohimik/dispat/pkg/manifest"
)

// Link points one dependency at a local folder, the way a go.mod replace
// directive does:
//
//	replace github.com/acme/core => ../core
//
// It is the same idea in every format that has it: the dependency keeps its
// declaration, and a separate directive says where the code actually comes
// from. An empty Path removes that directive and lets the declaration resolve
// normally again, which is what a release has to do before publishing.
type Link struct {
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

// LinkResult reports what a Relink call actually did. It splits the same three
// ways Result does, and for the same reasons, but over links rather than edits.
type LinkResult struct {
	// Path of the manifest the call targeted, echoed back so a caller
	// batching several keeps the correlation.
	Path string
	// Applied are the links that changed the file.
	Applied []Link
	// Missing are removals aimed at a directive the manifest does not have.
	Missing []Link
	// Skipped are the links the format cannot express. On a manifest whose
	// format has no redirect at all, that is every one of them.
	Skipped []Link
}

// linkFunc applies links to one manifest format.
type linkFunc func(path string, links []Link) (LinkResult, error)

// linkers maps each format onto its link writer. Only formats that point a
// package at a local folder through a separate, package-keyed directive appear
// here.
//
// Formats whose redirect is an option on the dependency line instead of a
// directive of its own (a Gemfile's `path:`, a Podfile's `:path`, an editable
// requirements install) are absent: changing one means rewriting a
// declaration, not managing a directive.
var linkers = map[manifest.Format]linkFunc{
	manifest.FormatNpm:       linkNpm,
	manifest.FormatGoMod:     linkGoMod,
	manifest.FormatCargo:     linkCargo,
	manifest.FormatPubspec:   linkPubspec,
	manifest.FormatPyProject: linkPyproject,
}

// Relink redirects dependencies at local folders inside the manifest at path,
// and removes the redirects whose Path is empty. The file is rewritten only
// when something changed, atomically, and only after the result is proved to
// still parse.
//
// LinkResult carries the same three outcomes Rewrite reports. Applied are the
// links that changed the file. Missing are removals aimed at a directive the
// manifest does not have. Skipped are the ones the format cannot express, which
// is every link on a manifest with no such directive. A link already spelled
// exactly as asked is none of the three.
//
// A file name no manifest format claims gives ErrUnsupportedManifest, matching
// Rewrite. A recognised manifest whose format has no redirect writes nothing
// and reports every link skipped; SupportsLink answers that in advance.
func Relink(path string, links []Link) (LinkResult, error) {
	for _, l := range links {
		if l.Name == "" {
			return LinkResult{}, fmt.Errorf("writer: link with no dependency name")
		}
	}
	format, ok := manifest.FormatOf(filepath.Base(path))
	if !ok {
		return LinkResult{}, fmt.Errorf("%s: %w", path, ErrUnsupportedManifest)
	}
	info, err := os.Stat(path)
	if err != nil {
		return LinkResult{}, err
	}
	if info.Size() > maxManifestBytes {
		return LinkResult{}, tooLarge(path, info.Size())
	}

	link, ok := linkers[format]
	if !ok {
		// The manifest is one this package knows; its format simply has
		// nowhere to put a redirect.
		return LinkResult{Path: path, Skipped: links}, nil
	}
	res, err := link(path, links)
	res.Path = path
	return res, err
}

// SupportsLink reports whether the manifest file name has a format that can
// hold a redirect. It shares the linkers table with Relink, so the two can
// never disagree.
func SupportsLink(path string) bool {
	format, ok := manifest.FormatOf(filepath.Base(path))
	if !ok {
		return false
	}
	_, ok = linkers[format]
	return ok
}
