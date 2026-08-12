// Package ignore holds the change-scope ignore rules: the patterns that keep
// some of a package's own files from counting as changes to it.
//
// It answers one question — "does this path count?" — and knows nothing about
// packages, spaces or config files. That keeps it a leaf: the config package
// builds the rules, the model carries them, and the planner asks.
//
// The patterns are the tool's own globs (globx), not gitignore's. There is
// one glob dialect in dispat and this is it: "*" matches any run of
// characters, path separators included, which is what makes "docs/*" reach
// every depth without a second wildcard to learn.
package ignore

import (
	"fmt"
	"path"
	"strings"

	"github.com/yohimik/dispat/services/dispat/internal/globx"
)

// rule is one compiled pattern.
type rule struct {
	pattern string
	// negate re-includes what an earlier pattern excluded.
	negate bool
	// dirOnly came from a pattern ending in "/": it matches the folder and
	// everything under it.
	dirOnly bool
	// bare came from a pattern with no separator, which matches a path's last
	// segment as well as the whole path — so "*.md" and "README.md" reach any
	// depth without being written "*/README.md" too.
	bare bool
}

// Rules is one level's compiled patterns, in the order they were written.
// The zero value and a nil *Rules match nothing, so a level that says nothing
// costs a nil check.
type Rules struct {
	rules []rule
}

// Compile prepares one level's patterns. Blank lines and #-comments are
// dropped, so the same function serves a config list and a file's contents.
//
// An empty pattern and a lone "!" are errors rather than no-ops: both are
// something the author meant, and neither can be carried out.
func Compile(patterns []string) (*Rules, error) {
	out := &Rules{}
	for i, raw := range patterns {
		p := strings.TrimSpace(raw)
		if p == "" || strings.HasPrefix(p, "#") {
			continue
		}
		var r rule
		if p == "!" {
			return nil, fmt.Errorf("pattern %d: %q re-includes nothing; write the pattern to re-include after the %q", i, p, "!")
		}
		switch {
		case strings.HasPrefix(p, "!"):
			r.negate, p = true, p[1:]
		case strings.HasPrefix(p, `\!`):
			// An escaped bang is a literal one, for the rare file actually
			// named "!something".
			p = p[1:]
		}
		if strings.HasSuffix(p, "/") {
			r.dirOnly, p = true, strings.TrimSuffix(p, "/")
		}
		p = strings.TrimPrefix(p, "./")
		p = strings.TrimPrefix(p, "/") // a leading slash reads as "from here"
		if p == "" {
			return nil, fmt.Errorf("pattern %d: %q names nothing", i, raw)
		}
		r.pattern = p
		r.bare = !strings.Contains(p, "/")
		out.rules = append(out.rules, r)
	}
	if len(out.rules) == 0 {
		return nil, nil
	}
	return out, nil
}

// Decide reports whether any pattern matched rel — a slash-separated path
// relative to the folder that declared these patterns — and, if one did, what
// the last one to match said. Two results rather than one, because a level
// that says nothing about a path must leave an outer level's verdict standing
// while a level that re-includes it must overturn it.
//
// A path outside the folder is never matched; callers that cannot rule that
// out pass the result of Relative.
func (r *Rules) Decide(rel string) (matched, ignored bool) {
	if r == nil || rel == "" {
		return false, false
	}
	base := path.Base(rel)
	for i := len(r.rules) - 1; i >= 0; i-- {
		if r.rules[i].matches(rel, base) {
			return true, !r.rules[i].negate
		}
	}
	return false, false
}

// Match reports whether rel is ignored by these patterns alone.
func (r *Rules) Match(rel string) bool {
	matched, ignored := r.Decide(rel)
	return matched && ignored
}

func (r rule) matches(rel, base string) bool {
	if globx.Match(r.pattern, rel) {
		return true
	}
	if r.bare && globx.Match(r.pattern, base) {
		return true
	}
	if !r.dirOnly {
		return false
	}
	// "docs/" covers everything under docs, which is the whole point of
	// naming a folder rather than a file. A folder named without a path
	// reaches any depth, for the same reason a bare file name does: one is
	// how you say "wherever it is".
	if !r.bare {
		return strings.HasPrefix(rel, r.pattern+"/")
	}
	for rest := rel; ; {
		i := strings.IndexByte(rest, '/')
		if i < 0 {
			return false // the last segment is the file, not a folder
		}
		if globx.Match(r.pattern, rest[:i]) {
			return true
		}
		rest = rest[i+1:]
	}
}

// Layer is one level's rules together with the folder they were written in,
// which is what their paths are relative to.
type Layer struct {
	Dir   string // absolute, slash-separated
	Rules *Rules
}

// Chain is the levels that apply to one package, weakest first: the
// repository, then the space, then the package itself. Levels concatenate
// rather than replace, and the last pattern to match anywhere in the chain
// decides — so a package can re-include a file the repository excluded, and
// only a package can, because nothing sits below it.
//
// Packages of one space share their outer layers by pointer; only the last is
// their own.
type Chain []Layer

// Ignores reports whether the file at the absolute slash-separated path is
// ignored for the package this chain belongs to. Later layers are nearer the
// package, so each one that has something to say overrules the ones before
// it, and a layer that says nothing leaves their verdict standing.
func (c Chain) Ignores(file string) bool {
	ignored := false
	for _, l := range c {
		rel, ok := Relative(l.Dir, file)
		if !ok {
			continue
		}
		if matched, verdict := l.Rules.Decide(rel); matched {
			ignored = verdict
		}
	}
	return ignored
}

// Relative returns file's path relative to dir, and whether it is inside it
// at all. Both are absolute slash-separated paths; the boundary is respected,
// so /r/libs/core-extra is not a file of /r/libs/core.
func Relative(dir, file string) (string, bool) {
	dir = path.Clean(dir)
	if dir == "" || dir == "." || dir == "/" {
		return strings.TrimPrefix(file, "/"), true
	}
	if file == dir {
		return "", false
	}
	prefix := strings.TrimSuffix(dir, "/") + "/"
	if !strings.HasPrefix(file, prefix) {
		return "", false
	}
	return file[len(prefix):], true
}
