// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"path"
	"path/filepath"
	"strings"

	"github.com/yohimik/dispat/pkg/ccme"
	public "github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/services/dispat/internal/globx"
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// scopeResult is one resolved scope-set (§6.1) together with the terms that
// could not be resolved. The diagnostics are collected rather than raised so
// that the caller can attribute them: an unknown name is an error in an
// include and a warning in an exclude, and the same helper resolves both a
// header scope-set and a Propagate-Scope footer.
type scopeResult struct {
	packages map[string]bool

	unknownIncludes []string // E130
	unknownExcludes []string // W130
	emptyGlobs      []string // W134
	nonPackage      []string // deliberately not packages; no diagnostic at all
	derived         bool     // resolution consulted the commit's changed files
}

func (r scopeResult) empty() bool { return len(r.packages) == 0 }

// inert reports a unit that addresses no package and should say so (W131).
//
// A unit scoping only declared non-package scopes is *expected* to resolve to
// nothing — dispat's own "chore(release)" commits are exactly that — so it is
// not inert in the sense the warning is about.
func (r scopeResult) inert() bool { return r.empty() && len(r.nonPackage) == 0 }

// resolveScopeSet implements §6.1.
//
//	includes -> base (or the file-derived set when none was written)
//	excludes -> removed from base, always last, always winning
//
// Term forms: "." is the derived set, "*" are the whole
// workspace, a term containing "*" is a glob, anything else is a package name.
func (cp *computation) resolveScopeSet(scopes ccme.ScopeSet, written bool, rec *commitRec) scopeResult {
	res := scopeResult{packages: make(map[string]bool)}

	if !written || len(scopes.Includes()) == 0 {
		// No parentheses, or a set consisting only of exclusions: the base is
		// the set of packages the commit's files belong to (§6.2).
		for name := range cp.derived(rec) {
			res.packages[name] = true
		}
		res.derived = true
	} else {
		for _, t := range scopes.Includes() {
			cp.expandTerm(t, rec, res.packages, &res, true)
		}
	}

	if len(scopes) > 0 {
		excluded := make(map[string]bool)
		for _, t := range scopes.Excludes() {
			cp.expandTerm(t, rec, excluded, &res, false)
		}
		for name := range excluded {
			delete(res.packages, name)
		}
	}
	// The single most common question a plan raises is "why did commit X (not)
	// count for package Y" — this line, at trace, is its answer.
	if cp.log.Trace().Enabled() {
		cp.log.Trace().Str("commit", rec.key).Str("scopes", scopes.String()).
			Bool("derived", res.derived).Strs("packages", sortedKeys(res.packages)).
			Msg("plan: scope resolved")
	}
	return res
}

// expandTerm adds the packages one term addresses to out.
func (cp *computation) expandTerm(t ccme.ScopeTerm, rec *commitRec, out map[string]bool, res *scopeResult, include bool) {
	switch {
	case t.IsDerived(): // "."
		for name := range cp.derived(rec) {
			out[name] = true
		}
		res.derived = true

	case t.IsAll(): // "*"
		for _, p := range cp.pkgs {
			out[p.Name] = true
		}

	case t.IsGlob():
		// A scope is written in a commit message and a package name in a folder
		// or a config file, so the two are matched case-insensitively, the way
		// every other selector in dispat matches a name.
		matched := false
		pattern := strings.ToLower(t.Name)
		for _, p := range cp.pkgs {
			if GlobMatch(pattern, strings.ToLower(p.Name)) {
				out[p.Name] = true
				matched = true
			}
		}
		if !matched {
			res.emptyGlobs = append(res.emptyGlobs, t.Name)
		}

	default:
		if name, p, ok := public.FoldLookup(cp.byName, t.Name); ok && p != nil {
			out[name] = true
			return
		}
		// A scope declared as deliberately not a package is not the typo E130
		// exists to catch. dispat's own release commit is scoped "release",
		// so without this every run would leave an error behind for the next
		// one to trip over.
		if cp.nonPackage[t.Name] {
			res.nonPackage = append(res.nonPackage, t.Name)
			return
		}
		// A typo in an include would silently drop a release, so it is an
		// error; excluding a package that was deleted or renamed is harmless
		// and common during refactors, so it is a warning (§6.1).
		if include {
			res.unknownIncludes = append(res.unknownIncludes, t.Name)
		} else {
			res.unknownExcludes = append(res.unknownExcludes, t.Name)
		}
	}
}

// reportScope raises the diagnostics a resolution collected. where names the
// footer the scope-set came from, or is empty for a header scope-set.
func (cp *computation) reportScope(res scopeResult, rec *commitRec, where string) {
	prefix := ""
	if where != "" {
		prefix = where + ": "
	}
	for _, name := range res.unknownIncludes {
		cp.err(CodeUnknownInclude, name, rec.key, prefix+"scope names no package at HEAD")
	}
	for _, name := range res.unknownExcludes {
		cp.warn(CodeUnknownScope, name, rec.key, prefix+"exclusion names no package at HEAD")
	}
	for _, glob := range res.emptyGlobs {
		cp.warn(CodeEmptyGlob, "", rec.key, prefix+"glob "+glob+" matched no package")
	}
}

// derived is derived(commit) from §6.2: the packages owning at least one path
// in the commit's changed-file list.
//
// Ownership is by longest matching path prefix over each package's scope
// folder — its own folder, or the `src` sub-folder when it declares one — so
// a file of a package nested inside another belongs to the inner one only,
// and a file outside a package's src belongs to whatever encloses it, or to
// nobody. The owner then has the last word: a file its `ignore` patterns
// exclude counts for nobody, rather than falling through to the package that
// encloses it, because the file is that package's and the package said it
// does not deserve a release.
//
// The result is memoised per commit because every unresolved unit in the
// commit asks for it, and the scope folders are prepared once per run: this
// loop is files times packages, and it runs for every commit in every pending
// window.
func (cp *computation) derived(rec *commitRec) map[string]bool {
	if rec.derivedSet != nil {
		return rec.derivedSet
	}
	if cp.scopeDirs == nil {
		cp.prepareScopeDirs()
	}
	out := make(map[string]bool)
	firstFile := make(map[string]string)
	for _, file := range rec.commit.Files {
		full := path.Clean(path.Join(cp.rootSlash(), filepath.ToSlash(file)))
		var owner *scopeDir
		for i := range cp.scopeDirs {
			sd := &cp.scopeDirs[i]
			if !underDir(full, sd.dir) {
				continue
			}
			if owner == nil || len(sd.dir) > len(owner.dir) {
				owner = sd
			}
		}
		if owner == nil || !owner.pkg.Counts(full) {
			continue
		}
		if !out[owner.pkg.Name] {
			firstFile[owner.pkg.Name] = file
		}
		out[owner.pkg.Name] = true
	}
	// One line per derived package, naming the file that put it there — the
	// trace a "why is this package in the plan" question is answered from.
	if cp.log.Trace().Enabled() {
		for _, name := range sortedKeys(out) {
			cp.log.Trace().Str("commit", rec.key).Str("package", name).
				Str("file", firstFile[name]).Msg("plan: package derived from the commit's files")
		}
	}
	rec.derivedSet = out
	return out
}

// scopeDir is one package's prepared scope folder: the cleaned, slashed path
// a changed file is compared against, kept beside the package so the
// ownership loop neither re-derives it nor looks the package up again.
type scopeDir struct {
	dir string
	pkg *model.Package
}

// prepareScopeDirs computes the comparison form of every package's scope
// folder once, and memoises the repository root in the same form. Both are
// fixed for the life of a computation and were previously recomputed for
// every (file, package) pair.
//
// It runs on the first ownership question rather than at construction, so a
// computation assembled by hand needs no setup call to be asked one.
func (cp *computation) prepareScopeDirs() {
	cp.root = filepath.ToSlash(cp.root)
	cp.scopeDirs = make([]scopeDir, 0, len(cp.pkgs))
	for _, p := range cp.pkgs {
		cp.scopeDirs = append(cp.scopeDirs, scopeDir{
			dir: path.Clean(filepath.ToSlash(p.ScopeDir())),
			pkg: p,
		})
	}
}

func (cp *computation) rootSlash() string { return cp.root }

// underDir reports whether file sits inside dir, respecting path boundaries so
// that /r/libs/core-extra is not mistaken for a file of /r/libs/core.
func underDir(file, dir string) bool {
	if dir == "" || dir == "." {
		// A package rooted at the repository root owns everything; this only
		// arises in tests and degenerate configurations.
		return true
	}
	clean := path.Clean(dir)
	if file == clean {
		return true
	}
	return strings.HasPrefix(file, strings.TrimSuffix(clean, "/")+"/")
}

// GlobMatch reports whether s matches pattern, where "*" matches any run of
// bytes, path separators included. Exported so the executor's autoVersion
// range matcher and scope resolution agree on what a glob means: a version
// range is not a filesystem path, and filepath.Match's separator rules would
// make `*` quietly miss `file:../core`. The matcher itself lives in globx,
// where .dispatexclude patterns share it.
func GlobMatch(pattern, s string) bool { return globx.Match(pattern, s) }
