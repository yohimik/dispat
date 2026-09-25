// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/yohimik/dispat/pkg/ccme"
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
			if cp.commitCanScope(rec, p) {
				out[p.Name] = true
			}
		}

	case t.IsGlob():
		// A scope is written in a commit message and a package name in a folder
		// or a config file, so the two are matched case-insensitively, the way
		// every other selector in dispat matches a name.
		matches := cp.globMatches(globx.Fold(t.Name), rec)
		for _, i := range matches {
			out[cp.pkgs[i].Name] = true
		}
		if len(matches) == 0 {
			res.emptyGlobs = append(res.emptyGlobs, t.Name)
		}

	default:
		name := cp.byFold[globx.Fold(t.Name)]
		if p := cp.byName[name]; p != nil && cp.commitCanScope(rec, p) {
			out[name] = true
			return
		}
		// A scope declared as deliberately not a package is not the typo E130
		// exists to catch. dispat's own release commit is scoped "release",
		// so without this every run would leave an error behind for the next
		// one to trip over.
		nonPackage := cp.nonPackage
		if rec != nil && rec.repository != "" {
			if owned := cp.nonPackageByRepo[globx.Fold(rec.repository)]; owned != nil {
				nonPackage = owned
			}
		}
		if nonPackage[t.Name] {
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

// globIndex is what makes a glob term cost its matches rather than the
// workspace (CCME §13.11). The names are folded once, where a scan folded every
// name for every term of every unit. A glob whose only "*" is its last byte,
// which "@acme/*" and nearly every written glob is, selects a contiguous run
// of the sorted folded names and is found by binary search. And a pattern is
// resolved once per repository it is read from, because a train repeats the
// same Propagate-Scope on every commit.
type globIndex struct {
	folded []string // by package index
	sorted []int    // package indices, by folded name
	memo   map[globKey][]int
}

// globKey is a folded pattern and the ownership class of the commit reading
// it: the folded repository whose packages it may address, or "" for all.
type globKey struct{ pattern, class string }

// globMemoLimit bounds the memo. Patterns come from commit messages, so their
// number is somebody else's choice; past the limit a pattern is resolved and
// not kept.
const globMemoLimit = 4096

// globMatches lists, in workspace order, the packages a folded glob addresses
// from this commit.
func (cp *computation) globMatches(pattern string, rec *commitRec) []int {
	if cp.globs == nil {
		gi := &globIndex{folded: make([]string, len(cp.pkgs)), sorted: make([]int, len(cp.pkgs)),
			memo: make(map[globKey][]int)}
		for i, p := range cp.pkgs {
			gi.folded[i], gi.sorted[i] = globx.Fold(p.Name), i
		}
		sort.SliceStable(gi.sorted, func(a, b int) bool { return gi.folded[gi.sorted[a]] < gi.folded[gi.sorted[b]] })
		cp.globs = gi
	}
	gi := cp.globs
	// commitCanScope, as a key: unrestricted unless a source history reads it.
	class := ""
	if rec != nil && rec.repository != "" && len(cp.histories) > 0 && !strings.EqualFold(rec.repository, cp.controlRepo) {
		class = globx.Fold(rec.repository)
	}
	key := globKey{pattern: pattern, class: class}
	if matches, ok := gi.memo[key]; ok {
		return matches
	}
	var matches []int
	consider := func(i int) {
		if class == "" || strings.EqualFold(cp.pkgs[i].Repository, class) {
			matches = append(matches, i)
		}
	}
	if prefix := pattern[:len(pattern)-1]; strings.HasSuffix(pattern, "*") && !strings.Contains(prefix, "*") {
		from := sort.Search(len(gi.sorted), func(k int) bool { return gi.folded[gi.sorted[k]] >= prefix })
		for k := from; k < len(gi.sorted) && strings.HasPrefix(gi.folded[gi.sorted[k]], prefix); k++ {
			consider(gi.sorted[k])
		}
		sort.Ints(matches)
	} else {
		for i := range cp.pkgs {
			if IsGlobMatch(pattern, gi.folded[i]) {
				consider(i)
			}
		}
	}
	if len(gi.memo) < globMemoLimit {
		gi.memo[key] = matches
	}
	return matches
}

// commitCanScope enforces the repository ownership boundary. A source
// history can only describe packages it owns; the control history retains
// fleet-wide explicit directives, and legacy plans have no repository name.
func (cp *computation) commitCanScope(rec *commitRec, p *model.Package) bool {
	if rec == nil || rec.repository == "" || len(cp.histories) == 0 {
		return true
	}
	if strings.EqualFold(rec.repository, cp.controlRepo) {
		return true
	}
	return strings.EqualFold(rec.repository, p.Repository)
}

func (cp *computation) commitCanDerive(rec *commitRec, p *model.Package) bool {
	if rec == nil || rec.repository == "" || len(cp.histories) == 0 {
		return true
	}
	return strings.EqualFold(rec.repository, p.Repository)
}

// reportScope raises the diagnostics a resolution collected. where names the
// footer the scope-set came from, or is empty for a header scope-set.
func (cp *computation) reportScope(res scopeResult, rec *commitRec, where string) {
	prefix := ""
	if where != "" {
		prefix = where + ": "
	}
	location := " at HEAD"
	if rec.repository != "" {
		location = " in repository " + rec.repository + location
	}
	for _, name := range res.unknownIncludes {
		cp.err(CodeUnknownInclude, name, rec.key, prefix+"scope names no package"+location)
	}
	for _, name := range res.unknownExcludes {
		cp.warn(CodeUnknownScope, name, rec.key, prefix+"exclusion names no package"+location)
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
// commit asks for it, and the scope folders are indexed once per run: this
// loop runs for every commit in every pending window, so a file costs its own
// path components and not a comparison against every package (CCME §13.11).
func (cp *computation) derived(rec *commitRec) map[string]bool {
	if rec.derivedSet != nil {
		return rec.derivedSet
	}
	if cp.scopeDirs == nil {
		cp.prepareScopeDirs()
	}
	cp.readUnforeseenFiles(rec)
	out := make(map[string]bool)
	firstFile := make(map[string]string)
	for _, file := range rec.commit.Files {
		// A commit that moved a fleet link changed a pointer to another
		// repository, not a file of any package here. Counting it would make
		// every settlement a change to whatever package encloses the link.
		if cp.isLinkPath(rec.repository, filepath.ToSlash(file)) {
			continue
		}
		root := rec.root
		if root == "" {
			root = cp.rootSlash()
		}
		full := path.Clean(path.Join(filepath.ToSlash(root), filepath.ToSlash(file)))
		owner := cp.ownerOf(rec, full)
		if owner == nil || !owner.pkg.IsCounted(full) {
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
	cp.scopeByDir = make(map[string][]int, len(cp.scopeDirs))
	for i, sd := range cp.scopeDirs {
		cp.scopeByDir[sd.dir] = append(cp.scopeByDir[sd.dir], i)
	}
}

// ownerOf is the longest-prefix rule of §6.2. The prefixes of a path are the
// path itself and its ancestor folders, so they are probed longest first in
// the index of scope folders; the first folder holding a package this commit
// may derive owns the file. Among packages sharing one folder the earliest in
// workspace order wins, which is what scanning every package in that order
// and keeping the strictly longest match decided.
func (cp *computation) ownerOf(rec *commitRec, full string) *scopeDir {
	best := -1
	consider := func(dir string) {
		for _, i := range cp.scopeByDir[dir] {
			if cp.commitCanDerive(rec, cp.scopeDirs[i].pkg) && (best < 0 || i < best) {
				best = i
			}
		}
	}
	for dir := full; ; {
		consider(dir)
		// "." is a folder that owns everything, and it is as long as "/": the
		// two tie, and the tie goes to workspace order like any other.
		if len(dir) == 1 {
			consider(".")
		}
		if best >= 0 {
			return &cp.scopeDirs[best]
		}
		cut := strings.LastIndexByte(dir, '/')
		if cut < 0 || dir == "/" {
			break
		}
		if cut == 0 {
			cut = 1 // the parent of "/a" is "/", not ""
		}
		dir = dir[:cut]
	}
	// A package rooted at the repository root owns everything; this only
	// arises in tests and degenerate configurations. "." is longer than "".
	for _, catchAll := range []string{".", ""} {
		if consider(catchAll); best >= 0 {
			return &cp.scopeDirs[best]
		}
	}
	return nil
}

func (cp *computation) rootSlash() string { return cp.root }

// IsGlobMatch reports whether s matches pattern, where "*" matches any run of
// bytes, path separators included. Exported so the executor's autoVersion
// range matcher and scope resolution agree on what a glob means: a version
// range is not a filesystem path, and filepath.Match's separator rules would
// make `*` quietly miss `file:../core`. The matcher itself lives in globx,
// where .dispatexclude patterns share it.
func IsGlobMatch(pattern, s string) bool { return globx.IsMatch(pattern, s) }
