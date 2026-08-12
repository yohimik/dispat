package app

// The derived half of `dispat autowriter`: the edits nobody types out.
//
// `--set` and `--link` name one dependency each on the command line, which is
// fine for one edit and hopeless for a monorepo. `--set-local`, `--link-local`
// and `--unlink-local` derive the same edits from what the manifests already
// declare: every dependency that resolves to another package in this workspace
// gets its range reconciled, its link written, or its link removed.
//
// Resolution is release.WorkspaceNames', the same index auto-versioning and
// compute answer "whose manifest name is this" with, so the three can never
// disagree about which declarations are internal.

import (
	"path/filepath"
	"strings"

	"github.com/yohimik/dispat/pkg/scanner"
	"github.com/yohimik/dispat/pkg/writer"

	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// derives reports that this invocation asks for anything the manifests decide,
// which is what makes the per-manifest pass worth running at all.
func (w *writerWork) derives() bool { return w.setLocal || w.linkLocal || w.unlinkLocal }

// derive turns one manifest's declared dependencies into the edits and links
// the local flags asked for.
//
// The rules are deliberately thinner than auto-versioning's: no kinds filter,
// no match globs, no `only` list, because `dispat autowriter` reads no config
// and inventing a second policy here is how two commands start disagreeing.
// What narrows the set is --only-updated, and nothing else.
func (w *writerWork) derive(rel *plan.Release, m scanner.Manifest) ([]writer.Edit, []writer.Link) {
	var (
		edits []writer.Edit
		links []writer.Link
	)
	// npm cannot hold a derived link at all; see linkableFormat.
	linkable := (w.linkLocal || w.unlinkLocal) && w.linkableFormat(rel.Pkg.Name, m)

	for _, d := range m.Deps {
		provider := w.providerOf(rel, m, d)
		if provider == "" {
			continue // not a package of this workspace
		}
		if w.onlyUpdated && !updating(w.pl, provider) {
			w.app.log.Debug().Str("package", rel.Pkg.Name).Str("manifest", m.Path).
				Str("dependency", d.Name).Str("provider", provider).
				Msg("derived edit dropped: this run does not update the provider")
			continue
		}
		if w.setLocal && !w.explicitSet[d.Name] {
			if e, ok := w.derivedEdit(rel, m, d, provider); ok {
				edits = append(edits, e)
			}
		}
		if linkable && !w.explicitLink[d.Name] {
			links = append(links, w.derivedLink(rel, m, d, provider))
		}
	}
	return edits, links
}

// providerOf resolves one declaration onto the workspace package it names, the
// way reconcileManifests does: the manifest-name index first, then a declared
// local path pointing into a package folder. A package naming itself resolves
// to nothing, so a nested example requiring its own module is left alone rather
// than linked to the folder it already sits in.
func (w *writerWork) providerOf(rel *plan.Release, m scanner.Manifest, d scanner.DeclaredDep) string {
	provider := w.names[d.Name]
	if provider == "" && d.LocalPath != "" {
		provider = scanner.ResolveLocalDir(w.dirs, rel.Pkg.Dir, m.Path, d.LocalPath)
	}
	if provider == rel.Pkg.Name {
		return ""
	}
	return provider
}

// derivedEdit is one --set-local range: the provider's end-of-run version,
// spelled by the --range policy through the same renderer auto-versioning uses,
// so a Docker tag never grows a caret and a go.mod never loses its "v".
//
// A range the manifest already spells this way produces nothing. Letting it
// through would report an edit applied on every converged run and re-trigger
// the syncLock scripts for a file nothing changed.
func (w *writerWork) derivedEdit(rel *plan.Release, m scanner.Manifest, d scanner.DeclaredDep, provider string) (writer.Edit, bool) {
	next := release.RangeText(w.rangePolicy, plannedVersion(w.pl.Releases[provider]), m.Ecosystem)
	if next == d.Range {
		w.app.log.Debug().Str("package", rel.Pkg.Name).Str("manifest", m.Path).
			Str("dependency", d.Name).Str("range", d.Range).
			Msg("derived edit dropped: the range already reads this way")
		return writer.Edit{}, false
	}
	return writer.Edit{Name: d.Name, Kind: d.Kind, Range: next}, true
}

// derivedLink is one --link-local or --unlink-local directive. An empty Path is
// what removes the redirect, so --unlink-local is the same walk with the target
// left blank.
//
// Version is deliberately not carried across. Only go.mod can express a
// version-narrowed redirect, and a link covering every required version is what
// a local checkout wants: narrowing it to the version the manifest happens to
// require today would stop working the moment that range moved.
func (w *writerWork) derivedLink(rel *plan.Release, m scanner.Manifest, d scanner.DeclaredDep, provider string) writer.Link {
	if w.unlinkLocal {
		return writer.Link{Name: d.Name}
	}
	return writer.Link{
		Name: d.Name,
		Path: localPath(rel.Pkg.Dir, m.Path, w.pl.Releases[provider].Pkg.Dir),
	}
}

// linkableFormat reports that derived links may be written into this manifest,
// warning once per package when they may not.
//
// npm is the exception, and it is npm's rule rather than this package's: an
// override for a package the manifest depends on *directly* is refused unless
// the two specs match exactly, and a derived link is aimed at exactly those
// direct dependencies. Writing one would produce a package.json that npm
// errors on at install. An explicit `--link name=path` still writes it, because
// there the caller has said which dependency they mean.
func (w *writerWork) linkableFormat(pkg string, m scanner.Manifest) bool {
	if m.Ecosystem != scanner.EcosystemNpm {
		return true
	}
	w.mu.Lock()
	warned := w.npmWarned[pkg]
	w.npmWarned[pkg] = true
	w.mu.Unlock()
	if !warned {
		w.app.log.Warn().Str("package", pkg).Str("manifest", m.Path).
			Msg("derived links skip package.json: npm refuses an override for a direct dependency unless the specs match exactly")
	}
	return false
}

// concat joins two slices into a new one, leaving both inputs untouched. The
// sweep hands every package the same edits value, so appending onto one of its
// slices in place would let two packages write the same backing array.
func concat[T any](base, extra []T) []T {
	if len(extra) == 0 {
		return base
	}
	out := make([]T, 0, len(base)+len(extra))
	out = append(out, base...)
	return append(out, extra...)
}

// localPath is the folder to point a link at, as the manifest has to spell it.
//
// The path is relative to the *manifest's* folder rather than to the package
// folder, because that is where every one of these directives is resolved from:
// a go.mod two levels down inside a package resolves "../.." against its own
// directory. Slashes are forced because a manifest is not a filesystem path,
// and a leading "./" is added where there is no "../" already, since go.mod
// requires a local path to look like one.
func localPath(pkgDir, manifestRel, providerDir string) string {
	from := filepath.Dir(filepath.Join(pkgDir, filepath.FromSlash(manifestRel)))
	rel, err := filepath.Rel(from, providerDir)
	if err != nil {
		// Different volumes on Windows, and nothing relative to say. The
		// absolute path is still a path the manifest can hold.
		return filepath.ToSlash(providerDir)
	}
	slashed := filepath.ToSlash(rel)
	if slashed == "." || !strings.HasPrefix(slashed, "..") {
		return "./" + slashed
	}
	return slashed
}
