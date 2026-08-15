package changelog

// The configurable text around an entry: the file title, the release name, and
// the header and footer lines. Two rules govern all of it — a line is written
// only for the packages its filters name, and the text is interpolated against
// the releasing package's own variables — so they live together here, away
// from the section rendering they bracket.

import (
	"os"
	"strings"

	"github.com/yohimik/dispat/services/dispat/internal/globx"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// Lookup resolves a variable name to its value, reporting whether it is
// defined at all.
type Lookup func(name string) (string, bool)

// ReleaseLookup is what a release's record text interpolates against: the
// release's own DISPAT_* variables and the outputs its scripts exported,
// falling back to the process environment. The release wins, because dispat's
// own process has no DISPAT_PACKAGE of its own to confuse it with — and
// because a nested `dispat github` inside a stage script must interpolate the
// package it is recording, not the package whose script invoked it.
//
// The variables are read once, here, rather than per line.
func ReleaseLookup(rel *plan.Release) Lookup {
	vars := make(map[string]string)
	if rel != nil && rel.Pkg != nil {
		collectVars(vars, rel.Vars())
		collectVars(vars, rel.OutputVars())
	}
	return func(name string) (string, bool) {
		if v, ok := vars[name]; ok {
			return v, true
		}
		return os.LookupEnv(name)
	}
}

func collectVars(into map[string]string, pairs []string) {
	for _, p := range pairs {
		if name, value, ok := strings.Cut(p, "="); ok {
			into[name] = value
		}
	}
}

// Expand replaces $VAR and ${VAR} in s. A name nothing defines expands to
// empty, the way a shell expands an unset variable: record text is prose, and
// leaving a half-written ${...} in a published release reads worse than the
// gap it would have filled. A nil lookup expands nothing.
func Expand(s string, look Lookup) string {
	if s == "" || look == nil || !strings.ContainsRune(s, '$') {
		return s
	}
	return os.Expand(s, func(name string) string {
		v, _ := look(name)
		return v
	})
}

// RenderLines renders the lines of one block — a file title, a header, a
// footer — that apply to the release's package, expanded and newline
// terminated. Nothing to write yields the empty string, which the caller drops
// rather than joining as a blank block.
func RenderLines(lines []model.EntryLine, rel *plan.Release, look Lookup) string {
	if len(lines) == 0 {
		return ""
	}
	var b strings.Builder
	for _, l := range lines {
		if !applies(l, rel) {
			continue
		}
		for _, text := range l.Line {
			b.WriteString(Expand(text, look))
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// applies reports whether a line is written for this release. Every filter that
// is set must match (a line filtered by space *and* group belongs to packages
// in both), while the values within one filter are alternatives. A line with no
// filters belongs to every release of every package.
//
// The channels filter is the one that asks about the release rather than the
// package: it is how a line reaches the betas alone, the stables alone, or one
// named prerelease channel.
func applies(l model.EntryLine, rel *plan.Release) bool {
	pkg := rel.Pkg
	space, group := "", ""
	if pkg.Space != nil {
		space, group = pkg.Space.Name, pkg.VersionGroupName()
	}
	return matchesAny(l.Package, pkg.Name) &&
		matchesAny(l.Space, space) &&
		matchesAny(l.Group, group) &&
		model.ChannelsAdmit(l.Channels, rel.Channel)
}

// matchesAny reports whether value matches one of the patterns, with no
// patterns meaning "unfiltered". Matching is the case-insensitive globbing the
// --package/--space/--group flags use, so a pattern written for the command
// line selects the same packages in a config file.
func matchesAny(patterns []string, value string) bool {
	if len(patterns) == 0 {
		return true
	}
	if value == "" {
		return false // a package outside any space matches no space or group filter
	}
	for _, p := range patterns {
		if globx.Match(strings.ToLower(p), strings.ToLower(value)) {
			return true
		}
	}
	return false
}
