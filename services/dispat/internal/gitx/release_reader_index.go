// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package gitx

import "strings"

// PackageTagFormat is one package's release tag format, as a
// ReleaseReaderIndex is built from.
type PackageTagFormat struct {
	Package string
	Format  TagFormat
}

// ReleaseReaderIndex answers which of many packages would read a name back as
// one of its releases, by asking only the packages whose format could.
//
// Every shape a release format is read in opens with the format's leading
// literal and {name} segments and closes with its trailing ones, and those are
// byte-exact tests, so a name that does not carry a package's rendered lead
// and tail cannot be read by it. Indexing the packages by whichever of the two
// spells the name makes a lookup a walk of the name's bytes instead of one
// parse per package. A format that never spells the name reads the same for
// every package, so its first package answers for all of them.
type ReleaseReaderIndex struct {
	prefixes *tagPrefixNode[indexedReader]
	// suffixes are keyed by the reversed tail, for the formats that spell the
	// name after the version only.
	suffixes *tagPrefixNode[indexedReader]
}

type indexedReader struct {
	position int
	reader   VersionReader
}

// NewReleaseReaderIndex indexes the packages' formats, compiling each
// distinct format once. A format that does not compile reads nothing, as its
// VersionReader would. The zero ReleaseReaderIndex reads nothing.
func NewReleaseReaderIndex(packages []PackageTagFormat) ReleaseReaderIndex {
	x := ReleaseReaderIndex{prefixes: &tagPrefixNode[indexedReader]{}, suffixes: &tagPrefixNode[indexedReader]{}}
	templates := make(map[TagFormat]*tagTemplate)
	answeredForAll := make(map[TagFormat]bool)
	for position, p := range packages {
		format := p.Format.WithDefault()
		if answeredForAll[format] {
			continue
		}
		tpl, compiled := templates[format]
		if !compiled {
			tpl = compileReleaseFormat(format)
			templates[format] = tpl
		}
		if tpl == nil {
			continue
		}
		answeredForAll[format] = tpl.count(segName) == 0
		x.add(indexedReader{position: position, reader: VersionReader{tpl: tpl, pkg: p.Package}})
	}
	return x
}

func compileReleaseFormat(format TagFormat) *tagTemplate {
	tpl, err := format.template()
	if err != nil {
		return nil
	}
	return tpl
}

func (x ReleaseReaderIndex) add(r indexedReader) {
	tpl := r.reader.tpl
	lead, isLeadNamed := renderBoundary(leadingBoundary(tpl.segs), r.reader.pkg)
	if isLeadNamed {
		x.prefixes.add(lead, r)
		return
	}
	tail, isTailNamed := renderBoundary(literalTail(tpl.segs), r.reader.pkg)
	if isTailNamed {
		x.suffixes.add(reverseBytes(tail), r)
		return
	}
	x.prefixes.add(lead, r)
}

// FindFirstReader returns the position, among the packages the index was
// built from, of the first one whose format reads the name as a release.
func (x ReleaseReaderIndex) FindFirstReader(tag string) (int, bool) {
	first := -1
	node := x.prefixes
	for depth := 0; node != nil; depth++ {
		first = firstReading(node.matchers, tag, first)
		if depth == len(tag) {
			break
		}
		node = node.child(tag[depth])
	}
	node = x.suffixes
	for depth := 0; node != nil; depth++ {
		first = firstReading(node.matchers, tag, first)
		if depth == len(tag) {
			break
		}
		node = node.child(tag[len(tag)-1-depth])
	}
	return first, first >= 0
}

// firstReading returns the smaller of first and the first position among
// readers that reads the tag. Readers are added in position order, so the
// scan stops at the first one that reads it or that cannot improve on first.
func firstReading(readers []indexedReader, tag string, first int) int {
	for _, r := range readers {
		if first >= 0 && r.position >= first {
			return first
		}
		if _, ok := r.reader.ParseVersion(tag); ok {
			return r.position
		}
	}
	return first
}

// leadingBoundary is the run of literal and {name} segments a format opens
// with. validate places {version} before the prerelease section, so every
// reduced shape keeps this run whole.
func leadingBoundary(segs []segment) []segment {
	i := 0
	for i < len(segs) && (segs[i].kind == segLiteral || segs[i].kind == segName) {
		i++
	}
	return segs[:i]
}

// renderBoundary writes a run of literal and {name} segments for one package,
// and reports whether the package's name is part of it.
func renderBoundary(segs []segment, pkg string) (string, bool) {
	var b strings.Builder
	isNamed := false
	for _, s := range segs {
		if s.kind == segName {
			b.WriteString(pkg)
			isNamed = true
			continue
		}
		b.WriteString(s.text)
	}
	return b.String(), isNamed
}

func reverseBytes(s string) string {
	out := make([]byte, len(s))
	for i := range len(s) {
		out[len(s)-1-i] = s[i]
	}
	return string(out)
}
