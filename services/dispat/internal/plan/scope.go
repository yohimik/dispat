package plan

import (
	"path"
	"path/filepath"
	"strings"

	"github.com/yohimik/dispat/pkg/ccme"
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
		matched := false
		for _, p := range cp.pkgs {
			if globMatch(t.Name, p.Name) {
				out[p.Name] = true
				matched = true
			}
		}
		if !matched {
			res.emptyGlobs = append(res.emptyGlobs, t.Name)
		}

	default:
		if _, ok := cp.byName[t.Name]; ok {
			out[t.Name] = true
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
// Ownership is by longest matching path prefix, so a file of a package nested
// inside another belongs to the inner one only. The result is memoised per
// commit because every unresolved unit in the commit asks for it.
func (cp *computation) derived(rec *commitRec) map[string]bool {
	if rec.derivedSet != nil {
		return rec.derivedSet
	}
	out := make(map[string]bool)
	for _, file := range rec.commit.Files {
		full := path.Clean(path.Join(cp.rootSlash(), filepath.ToSlash(file)))
		owner, ownerLen := "", -1
		for _, p := range cp.pkgs {
			dir := path.Clean(filepath.ToSlash(p.Dir))
			if !underDir(full, dir) {
				continue
			}
			if len(dir) > ownerLen {
				owner, ownerLen = p.Name, len(dir)
			}
		}
		if owner != "" {
			out[owner] = true
		}
	}
	rec.derivedSet = out
	return out
}

func (cp *computation) rootSlash() string { return filepath.ToSlash(cp.root) }

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

// globMatch reports whether s matches a scope term containing "*".
//
// "*" matches any run of characters, "/" included, because package names are
// frequently scoped ("@acme/ui") and "@acme/*" must reach them. The matcher is
// an iterative two-pointer walk with a single backtrack point: no regular
// expression, no recursion, and linear on every input a scope term can be.
// GlobMatch reports whether s matches pattern, where "*" matches any run of
// bytes, path separators included. Exported so the executor's autoVersion
// range matcher and scope resolution agree on what a glob means: a version
// range is not a filesystem path, and filepath.Match's separator rules would
// make `*` quietly miss `file:../core`.
func GlobMatch(pattern, s string) bool { return globMatch(pattern, s) }

func globMatch(pattern, s string) bool {
	star, mark := -1, 0
	i, j := 0, 0
	for i < len(s) {
		switch {
		case j < len(pattern) && pattern[j] == s[i]:
			i++
			j++
		case j < len(pattern) && pattern[j] == '*':
			star, mark = j, i
			j++
		case star >= 0:
			mark++
			i, j = mark, star+1
		default:
			return false
		}
	}
	for j < len(pattern) && pattern[j] == '*' {
		j++
	}
	return j == len(pattern)
}
