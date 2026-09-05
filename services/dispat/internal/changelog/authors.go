// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package changelog

import (
	"strings"

	"github.com/yohimik/dispat/pkg/ccme/v2"
	"github.com/yohimik/dispat/services/dispat/internal/globx"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// The authors policy values, spelled once so the renderer, the validator and
// the flags cannot drift apart.
const (
	AuthorsOff     = "off"
	AuthorsInline  = "inline"
	AuthorsSection = "section"
	AuthorsBoth    = "both"

	AuthorsFullName = "fullname"
	AuthorsUsername = "username"

	AuthorsCommitsCCME = "ccme"
	AuthorsCommitsAll  = "all"
)

// authorsInline reports whether the placement writes the per-line suffix.
func authorsInline(placement string) bool {
	return placement == AuthorsInline || placement == AuthorsBoth
}

// authorsSectioned reports whether the placement writes the section block.
func authorsSectioned(placement string) bool {
	return placement == AuthorsSection || placement == AuthorsBoth
}

// FilterAuthors applies the include-then-exclude glob filters.
//
// An empty include list admits everyone, so the ordinary configuration filters
// nothing and pays for nothing. Exclude runs afterwards and wins, which is the
// only order that lets a broad include ("*") coexist with a narrow refusal
// ("*[bot]"): the other way round, an author matching both would be admitted
// by the include the exclude exists to override.
func FilterAuthors(list []plan.Author, include, exclude []string) []plan.Author {
	if len(include) == 0 && len(exclude) == 0 {
		return list
	}
	out := make([]plan.Author, 0, len(list))
	for _, a := range list {
		if len(include) > 0 && !matchAuthor(a, include) {
			continue
		}
		if matchAuthor(a, exclude) {
			continue
		}
		out = append(out, a)
	}
	return out
}

// matchAuthor reports whether any pattern reaches the author on any of the
// three ways of naming them.
//
// All three axes are tried against every pattern because an operator writing a
// filter is thinking of a person, not of a field: "*@acme.com" is obviously an
// address and "dependabot*" obviously a name, and asking which key a pattern
// belongs to would be a question with no good answer. Matching is
// case-insensitive on both sides, the way every other name comparison in the
// tool is.
func matchAuthor(a plan.Author, patterns []string) bool {
	subjects := [...]string{
		strings.ToLower(a.Name),
		strings.ToLower(a.Username()),
		strings.ToLower(a.Email),
	}
	for _, p := range patterns {
		p = strings.ToLower(p)
		for _, s := range subjects {
			if s != "" && globx.Match(p, s) {
				return true
			}
		}
	}
	return false
}

// authorLabel renders one author under the configured format. The username
// form falls back to the name when there is no email, and the fullname form
// falls back to the username when there is no name, so neither ever renders an
// empty bullet.
func authorLabel(a plan.Author, format string) string {
	if format == AuthorsUsername {
		return a.Username()
	}
	if a.Name == "" {
		return a.Username()
	}
	return a.Name
}

// authorSuffix is the inline attribution appended to one entry line: " (by A,
// B)", or nothing when the placement does not ask for it or the filters admit
// nobody.
//
// It reads the unit's own authors rather than the release's, so a line names
// the people behind that change and not everyone in the window. A restatement
// (§7.4.2) is therefore attributed to whoever wrote the restating commit,
// which is the record the entry actually describes.
func authorSuffix(rel *plan.Release, u *ccme.Unit, f Format) string {
	if !authorsInline(f.AuthorsPlacement) {
		return ""
	}
	authors := FilterAuthors(rel.AuthorsFor(u), f.AuthorsInclude, f.AuthorsExclude)
	if len(authors) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(" (by ")
	for i, a := range authors {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(authorLabel(a, f.AuthorsFormat))
	}
	b.WriteByte(')')
	return b.String()
}

// sectionAuthors is who the section lists, before formatting.
//
// Under "ccme" it is the union of the authors of the units the entry actually
// renders, so the list and the lines above it describe the same work; the
// narrowing NotesUnits does for a prerelease and for a revert falls out of
// that for free. Under "all" it is every author of the release's window, which
// credits the people whose commits carried no release record — a build fix, a
// dependency bump written outside the convention — at the cost of naming
// people no line above mentions.
func sectionAuthors(rel *plan.Release, f Format) []plan.Author {
	var list []plan.Author
	if f.AuthorsCommits == AuthorsCommitsAll {
		list = rel.AllAuthors()
	} else {
		for _, u := range rel.NotesUnits() {
			list = append(list, rel.AuthorsFor(u)...)
		}
	}
	return FilterAuthors(dedupeAuthors(list), f.AuthorsInclude, f.AuthorsExclude)
}

// dedupeAuthors keeps first occurrences, so the section follows the order the
// release collected its units in: newest commit first, which is the order the
// planner builds Units in and the order the window authors are already in.
// Deliberately not the rendered order (breaking, then features, then fixes):
// that would sort people by the size of the change they happened to make.
func dedupeAuthors(in []plan.Author) []plan.Author {
	if len(in) < 2 {
		return in
	}
	seen := make(map[string]bool, len(in))
	out := make([]plan.Author, 0, len(in))
	for _, a := range in {
		k := strings.ToLower(a.Email)
		if k == "" {
			k = strings.ToLower(a.Name)
		}
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, a)
	}
	return out
}

// authorsSection renders the "### Authors" block, or nothing when the
// placement does not ask for it or nothing survives the filters. Rendering
// nothing rather than an empty heading is deliberate: a heading over no names
// reads as a failed write, and RenderBody drops an empty block without leaving
// a blank line behind.
func authorsSection(rel *plan.Release, f Format) string {
	if !authorsSectioned(f.AuthorsPlacement) {
		return ""
	}
	authors := sectionAuthors(rel, f)
	if len(authors) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("### " + f.AuthorsTitle + "\n\n")
	for _, a := range authors {
		b.WriteString("- " + authorLabel(a, f.AuthorsFormat) + "\n")
	}
	return b.String()
}
