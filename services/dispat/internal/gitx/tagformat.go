package gitx

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/yohimik/dispat/pkg/ccme"
)

// This file is the compiled half of TagFormat: the template a format string
// becomes, and the two directions it is used in — rendering a tag for a
// version, and reading a version back out of a tag.
//
// Both directions have to come from one description of the format. A renderer
// and a hand-written parser that drift apart produce tags no later run can
// read, and a version dispat cannot read is a package it believes was never
// released.

// segKind is what one piece of a compiled format is.
type segKind int

const (
	segLiteral segKind = iota
	segName
	segVersion
	segChannel
	segCounter
	// The three core components on their own. They exist for alias tags —
	// "v{major}" is what a moving major tag is written from — and are refused
	// in a release tagFormat, which has to be able to read its own tags back
	// and cannot recover a version from a fragment of one.
	segMajor
	segMinor
	segPatch
)

type segment struct {
	kind segKind
	text string // literal text, for segLiteral
}

// tagTemplate is a parsed format. chIdx and ctIdx locate the prerelease
// placeholders, which is all the two shapes of a tag need: the prerelease
// section runs from the literal immediately preceding {channel} through
// {counter}, and the stable shape is the template with that section removed.
type tagTemplate struct {
	segs  []segment
	chIdx int // index of {channel}, -1 when absent
	ctIdx int // index of {counter}, -1 when absent
}

var placeholders = []struct {
	text string
	kind segKind
}{
	{tagNamePlaceholder, segName},
	{tagVersionPlaceholder, segVersion},
	{tagChannelPlaceholder, segChannel},
	{tagCounterPlaceholder, segCounter},
	{tagMajorPlaceholder, segMajor},
	{tagMinorPlaceholder, segMinor},
	{tagPatchPlaceholder, segPatch},
}

// template compiles the format, applying the default for the empty one. The
// error is the one Validate reports; every other caller has already been
// through Validate at config load and treats a failure as unreachable.
func (f TagFormat) template() (*tagTemplate, error) {
	tpl := compileTagFormat(string(f.WithDefault()))
	return tpl, tpl.validate()
}

func compileTagFormat(s string) *tagTemplate {
	tpl := &tagTemplate{chIdx: -1, ctIdx: -1}
	var lit strings.Builder
	flush := func() {
		if lit.Len() > 0 {
			tpl.segs = append(tpl.segs, segment{kind: segLiteral, text: lit.String()})
			lit.Reset()
		}
	}
	for i := 0; i < len(s); {
		matched := false
		if s[i] == '{' {
			for _, p := range placeholders {
				if !strings.HasPrefix(s[i:], p.text) {
					continue
				}
				flush()
				switch p.kind {
				case segChannel:
					if tpl.chIdx < 0 {
						tpl.chIdx = len(tpl.segs)
					}
				case segCounter:
					if tpl.ctIdx < 0 {
						tpl.ctIdx = len(tpl.segs)
					}
				}
				tpl.segs = append(tpl.segs, segment{kind: p.kind})
				i += len(p.text)
				matched = true
				break
			}
		}
		if !matched {
			// An unknown "{...}" is literal text, exactly as it was before
			// there was anything but {name} and {version} to substitute.
			lit.WriteByte(s[i])
			i++
		}
	}
	flush()
	return tpl
}

// spellsPrerelease reports whether the format writes the prerelease itself
// rather than leaving it inside {version}.
func (t *tagTemplate) spellsPrerelease() bool { return t.chIdx >= 0 && t.ctIdx >= 0 }

func (t *tagTemplate) count(k segKind) int {
	n := 0
	for _, s := range t.segs {
		if s.kind == k {
			n++
		}
	}
	return n
}

// validate enforces the structural rules that make a format renderable and
// readable. Everything it rejects is a format that would either lose
// information or produce a tag nothing can read back.
func (t *tagTemplate) validate() error {
	// A release tag has to be readable back into the version that produced it,
	// and a fragment of a version cannot do that: "v1" names no release in
	// particular. They belong to alias tags, which are only ever written.
	for _, c := range []struct {
		kind segKind
		text string
	}{{segMajor, tagMajorPlaceholder}, {segMinor, tagMinorPlaceholder}, {segPatch, tagPatchPlaceholder}} {
		if t.count(c.kind) > 0 {
			return fmt.Errorf("uses %s, which is only available in aliasTags", c.text)
		}
	}
	switch t.count(segVersion) {
	case 1:
	case 0:
		return fmt.Errorf("contains no %s placeholder", tagVersionPlaceholder)
	default:
		return fmt.Errorf("contains more than one %s placeholder", tagVersionPlaceholder)
	}
	if t.count(segChannel) > 1 {
		return fmt.Errorf("contains more than one %s placeholder", tagChannelPlaceholder)
	}
	if t.count(segCounter) > 1 {
		return fmt.Errorf("contains more than one %s placeholder", tagCounterPlaceholder)
	}
	switch {
	case t.chIdx >= 0 && t.ctIdx < 0:
		// A channel with no counter gives every prerelease of one train the
		// same name, so the second one cannot be tagged at all.
		return fmt.Errorf("uses %s without %s", tagChannelPlaceholder, tagCounterPlaceholder)
	case t.ctIdx >= 0 && t.chIdx < 0:
		// A counter with no channel cannot tell alpha.1 from beta.1.
		return fmt.Errorf("uses %s without %s", tagCounterPlaceholder, tagChannelPlaceholder)
	case t.chIdx < 0:
		return nil
	}

	vIdx := t.indexOf(segVersion)
	if t.chIdx < vIdx || t.ctIdx < t.chIdx {
		return fmt.Errorf("expects %s, then %s, then %s, in that order",
			tagVersionPlaceholder, tagChannelPlaceholder, tagCounterPlaceholder)
	}
	// The section between {channel} and {counter} is dropped wholesale on a
	// stable version, so nothing that must survive may sit inside it.
	for _, s := range t.segs[t.chIdx+1 : t.ctIdx] {
		if s.kind != segLiteral {
			return errors.New("places another placeholder between " +
				tagChannelPlaceholder + " and " + tagCounterPlaceholder)
		}
	}
	return nil
}

// validateAlias is validate's counterpart for an alias tag: the same
// structural rules, minus the ones that exist so a tag can be read back.
//
// An alias is write-only. Nothing ever parses one, which is what lets it drop
// the "exactly one {version}" requirement ("v{major}" has none) and skip the
// render-and-read-back round trip. What it still needs is something that
// varies with the version, or every release of every package would write the
// same ref.
func (t *tagTemplate) validateAlias() error {
	if t.count(segVersion) > 1 {
		return fmt.Errorf("contains more than one %s placeholder", tagVersionPlaceholder)
	}
	if t.count(segVersion)+t.count(segMajor)+t.count(segMinor)+t.count(segPatch) == 0 {
		return fmt.Errorf("names no part of the version: want %s, %s, %s or %s",
			tagVersionPlaceholder, tagMajorPlaceholder, tagMinorPlaceholder, tagPatchPlaceholder)
	}
	if t.count(segChannel) > 1 {
		return fmt.Errorf("contains more than one %s placeholder", tagChannelPlaceholder)
	}
	if t.count(segCounter) > 1 {
		return fmt.Errorf("contains more than one %s placeholder", tagCounterPlaceholder)
	}
	switch {
	case t.chIdx >= 0 && t.ctIdx < 0:
		return fmt.Errorf("uses %s without %s", tagChannelPlaceholder, tagCounterPlaceholder)
	case t.ctIdx >= 0 && t.chIdx < 0:
		return fmt.Errorf("uses %s without %s", tagCounterPlaceholder, tagChannelPlaceholder)
	}
	return nil
}

func (t *tagTemplate) indexOf(k segKind) int {
	for i, s := range t.segs {
		if s.kind == k {
			return i
		}
	}
	return -1
}

// sectionStart is where the prerelease section begins: the literal glued to
// {channel}, when there is one, so that the separator disappears with the
// thing it separates.
func (t *tagTemplate) sectionStart() int {
	if t.chIdx > 0 && t.segs[t.chIdx-1].kind == segLiteral {
		return t.chIdx - 1
	}
	return t.chIdx
}

// shape is the template reduced to one of the forms a tag can actually take.
type shape int

const (
	shapeFull    shape = iota // core, channel and counter
	shapeNoCount              // core and channel, for a prerelease with no counter
	shapeStable               // core only; the prerelease section is gone
)

// reduce returns the segments of one shape.
func (t *tagTemplate) reduce(sh shape) []segment {
	if !t.spellsPrerelease() || sh == shapeFull {
		return t.segs
	}
	out := make([]segment, 0, len(t.segs))
	switch sh {
	case shapeStable:
		out = append(out, t.segs[:t.sectionStart()]...)
		out = append(out, t.segs[t.ctIdx+1:]...)
	case shapeNoCount:
		// Drop the counter and the literal that would have introduced it.
		out = append(out, t.segs[:t.chIdx+1]...)
		out = append(out, t.segs[t.ctIdx+1:]...)
	}
	return out
}

// prereleaseParts splits a version's prerelease into the channel and the rest.
// Both are empty for a stable version. §11.3's shape is [channel, counter],
// but an exact Release-As may carry more identifiers, and those belong with the
// counter rather than being dropped.
func prereleaseParts(v ccme.Version) (channel, counter string) {
	if len(v.Prerelease) == 0 {
		return "", ""
	}
	return v.Prerelease[0], strings.Join(v.Prerelease[1:], ".")
}

// render writes the tag for one version.
func (t *tagTemplate) render(pkg string, v ccme.Version) string {
	channel, counter := prereleaseParts(v)
	version := v.String()
	sh := shapeFull
	if t.spellsPrerelease() {
		// {version} narrows to the core, because the prerelease is written by
		// the other two placeholders.
		version = coreString(v)
		switch {
		case channel == "":
			sh = shapeStable
		case counter == "":
			sh = shapeNoCount
		}
	}

	var b strings.Builder
	for _, s := range t.reduce(sh) {
		switch s.kind {
		case segLiteral:
			b.WriteString(s.text)
		case segName:
			b.WriteString(pkg)
		case segVersion:
			b.WriteString(version)
		case segChannel:
			b.WriteString(channel)
		case segCounter:
			b.WriteString(counter)
		case segMajor:
			b.WriteString(utoa(v.Major))
		case segMinor:
			b.WriteString(utoa(v.Minor))
		case segPatch:
			b.WriteString(utoa(v.Patch))
		}
	}
	return b.String()
}

// utoa renders one version component.
func utoa(n uint64) string { return strconv.FormatUint(n, 10) }

// renderVersion writes the version section of the tag alone: from {version}
// through {counter} with the literals between them, excluding everything
// glued before {version} (name, "v" prefixes, path decorations) and after the
// prerelease section. Shape reduction applies exactly as in render, so a
// stable version under a prerelease-spelling format drops the section with
// its separators.
func (t *tagTemplate) renderVersion(v ccme.Version) string {
	channel, counter := prereleaseParts(v)
	version := v.String()
	sh := shapeFull
	if t.spellsPrerelease() {
		version = coreString(v)
		switch {
		case channel == "":
			sh = shapeStable
		case counter == "":
			sh = shapeNoCount
		}
	}

	segs := t.reduce(sh)
	start, end := -1, -1
	for i, s := range segs {
		switch s.kind {
		case segVersion:
			if start < 0 {
				start = i
			}
			end = i
		case segChannel, segCounter:
			end = i
		}
	}
	if start < 0 { // unreachable for a validated format
		return version
	}
	var b strings.Builder
	for _, s := range segs[start : end+1] {
		switch s.kind {
		case segLiteral:
			b.WriteString(s.text)
		case segVersion:
			b.WriteString(version)
		case segChannel:
			b.WriteString(channel)
		case segCounter:
			b.WriteString(counter)
		}
	}
	return b.String()
}

// coreString renders MAJOR.MINOR.PATCH.
func coreString(v ccme.Version) string {
	return ccme.Version{Major: v.Major, Minor: v.Minor, Patch: v.Patch}.String()
}

// split returns the literal text before the version and after the whole
// prerelease section — the loose bounds Glob and Matches work with. Both
// shapes share the trailing literal, because the section ends at {counter},
// so one pair of bounds covers a stable and a prerelease tag alike.
func (t *tagTemplate) split(pkg string) (prefix, suffix string, ok bool) {
	segs := t.reduce(shapeStable)
	vIdx := -1
	for i, s := range segs {
		if s.kind == segVersion {
			vIdx = i
			break
		}
	}
	if vIdx < 0 {
		return "", "", false
	}
	lit := func(part []segment) string {
		var b strings.Builder
		for _, s := range part {
			switch s.kind {
			case segLiteral:
				b.WriteString(s.text)
			case segName:
				b.WriteString(pkg)
			default:
				// Another placeholder: nothing literal can be assumed past it.
				return b.String()
			}
		}
		return b.String()
	}
	return lit(segs[:vIdx]), lit(literalTail(segs[vIdx+1:])), true
}

// matchesAlias reports whether a name is one this alias format could have
// written for a package.
//
// It is the same backtracking walk parseVersion makes, with the accept test
// removed: an alias is never read back into a version, so what is asked here
// is only whether the literals line up and the placeholders captured
// something they are allowed to capture. That is precise enough to tell an
// alias apart from a release tag nobody can parse, which is the one question
// it exists for: "v1" is a name "v{major}" writes, and "v1.0.0.0" is not,
// because a major is digits and stops at the dot.
func (t *tagTemplate) matchesAlias(pkg, tag string) bool {
	if t.count(segVersion)+t.count(segMajor)+t.count(segMinor)+t.count(segPatch) == 0 {
		// A format that writes a constant has no shape to recognise, only a
		// name. validateAlias refuses those, so this is the guard rather than
		// a case with behaviour.
		return false
	}
	m := &segMatcher{pkg: pkg, core: true, caps: map[segKind]string{}, accept: func() bool { return true }}
	return m.match(t.segs, tag)
}

// literalTail returns the trailing run of literal segments, so that a suffix is
// read back-to-front and stops at the first placeholder.
func literalTail(segs []segment) []segment {
	i := len(segs)
	for i > 0 && (segs[i-1].kind == segLiteral || segs[i-1].kind == segName) {
		i--
	}
	return segs[i:]
}

// ---------------------------------------------------------------------------
// parsing
// ---------------------------------------------------------------------------

// parseVersion reads a version out of a tag, trying the most specific shape
// first: a tag carrying a channel and a counter must not be read as a stable
// one whose core happens to contain the separator.
func (t *tagTemplate) parseVersion(pkg, tag string) (ccme.Version, bool) {
	shapes := []shape{shapeFull}
	if t.spellsPrerelease() {
		shapes = []shape{shapeFull, shapeNoCount, shapeStable}
	}
	for _, sh := range shapes {
		if v, ok := t.matchShape(pkg, tag, sh); ok {
			return v, true
		}
	}
	return ccme.Version{}, false
}

func (t *tagTemplate) matchShape(pkg, tag string, sh shape) (ccme.Version, bool) {
	core := t.spellsPrerelease()
	m := &segMatcher{pkg: pkg, core: core, caps: map[segKind]string{}}
	var out ccme.Version
	m.accept = func() bool {
		v, err := ccme.ParseVersion(assemble(m.caps, core))
		if err != nil {
			return false
		}
		out = v
		return true
	}
	ok := m.match(t.reduce(sh), tag)
	return out, ok
}

// assemble builds the SemVer string the captures describe. With the prerelease
// spelled out, the tag's own separators are discarded and the version is
// rebuilt in SemVer's shape — which is the point: the tag names the release,
// the version orders it.
func assemble(caps map[segKind]string, core bool) string {
	v := caps[segVersion]
	if !core {
		return v
	}
	if ch := caps[segChannel]; ch != "" {
		v += "-" + ch
		if ct := caps[segCounter]; ct != "" {
			v += "." + ct
		}
	}
	return v
}

// segMatcher holds what stays fixed across the backtracking recursion — the
// package name, the byte-class mode, the capture map and the accept test —
// so the recursive step only threads the values that actually change.
type segMatcher struct {
	pkg    string
	core   bool
	caps   map[segKind]string
	accept func() bool
}

// match walks the segments against the tag, backtracking over the possible
// extents of each placeholder and calling accept once the whole tag is
// consumed. accept rejecting a split resumes the search, which is what lets an
// ambiguous-looking format like "{version}{channel}{counter}" still resolve:
// the only split whose pieces form a version wins.
func (mt *segMatcher) match(segs []segment, s string) bool {
	if len(segs) == 0 {
		return s == "" && mt.accept()
	}
	seg := segs[0]
	switch seg.kind {
	case segLiteral:
		if !strings.HasPrefix(s, seg.text) {
			return false
		}
		return mt.match(segs[1:], s[len(seg.text):])
	case segName:
		if !strings.HasPrefix(s, mt.pkg) {
			return false
		}
		return mt.match(segs[1:], s[len(mt.pkg):])
	default:
		in := classOf(seg.kind, mt.core)
		m := 0
		for m < len(s) && in(s[m]) {
			m++
		}
		// Longest capture first. Adjacent placeholders make the boundary the
		// searcher's problem — "beta10" under "{channel}{counter}" — and the
		// candidate order is what resolves it the way render fused it.
		for _, n := range captureLengths(seg.kind, s, m) {
			mt.caps[seg.kind] = s[:n]
			if mt.match(segs[1:], s[n:]) {
				return true
			}
			delete(mt.caps, seg.kind)
		}
		return false
	}
}

// captureLengths orders the candidate extents of one placeholder, longest
// first. The channel additionally tries extents ending on a non-digit before
// any ending on a digit: fused against a numeric counter, "beta10" splits at
// the letter–digit boundary — beta/10, never beta1/0 — which is what keeps a
// train's tenth release sorting after its ninth. A channel name that itself
// ends in a digit needs a separator literal in the format; the load-time
// round-trip check is what tells its author so.
func captureLengths(k segKind, s string, m int) []int {
	out := make([]int, 0, m)
	if k == segChannel {
		for n := m; n >= 1; n-- {
			if !isDigitByte(s[n-1]) {
				out = append(out, n)
			}
		}
	}
	for n := m; n >= 1; n-- {
		if k != segChannel || isDigitByte(s[n-1]) {
			out = append(out, n)
		}
	}
	return out
}

// classOf is the byte class one placeholder may capture. The classes are what
// keep a format's pieces distinguishable: a channel is a single SemVer
// identifier, a counter is the identifier tail after it — usually the bare
// number §11.3 prescribes, but an exact Release-As may carry anything SemVer
// allows, and a tag render can write must parse back.
func classOf(k segKind, core bool) func(byte) bool {
	switch k {
	case segChannel:
		return isIdentByte
	case segCounter:
		return func(c byte) bool { return isIdentByte(c) || c == '.' }
	case segMajor, segMinor, segPatch:
		// One number and no separator. These appear only in alias formats,
		// which validate refuses to let a tagFormat use, and the whole reason
		// to recognise one is that "v1" is an alias and "v1.0.0.0" is a
		// release tag somebody mistyped: a class that swallowed the dots
		// could not tell them apart.
		return isDigitByte
	default: // segVersion
		if core {
			return func(c byte) bool { return isDigitByte(c) || c == '.' }
		}
		return func(c byte) bool { return isIdentByte(c) || c == '.' || c == '+' }
	}
}

func isDigitByte(c byte) bool { return c >= '0' && c <= '9' }

func isIdentByte(c byte) bool {
	return isDigitByte(c) || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '-'
}
