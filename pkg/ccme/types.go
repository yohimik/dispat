package ccme

import "strings"

// Bump is a semantic-version increment level, ordered
// none < patch < minor < major (§2).
type Bump int

// Bump levels.
const (
	BumpNone Bump = iota
	BumpPatch
	BumpMinor
	BumpMajor
)

// String implements fmt.Stringer.
func (b Bump) String() string {
	switch b {
	case BumpNone:
		return "none"
	case BumpPatch:
		return "patch"
	case BumpMinor:
		return "minor"
	case BumpMajor:
		return "major"
	default:
		return "invalid"
	}
}

// ParseBump maps a spelled-out bump level to a Bump.
func ParseBump(s string) (Bump, bool) {
	switch s {
	case "none":
		return BumpNone, true
	case "patch":
		return BumpPatch, true
	case "minor":
		return BumpMinor, true
	case "major":
		return BumpMajor, true
	default:
		return BumpNone, false
	}
}

// MaxBump returns the higher of two bumps (§2).
func MaxBump(a, b Bump) Bump {
	if a > b {
		return a
	}
	return b
}

// Propagate is the bump handed to dependents of a unit's packages (§8.2).
type Propagate string

// Propagate values.
const (
	PropagateNone    Propagate = "none"
	PropagatePatch   Propagate = "patch"
	PropagateMinor   Propagate = "minor"
	PropagateMajor   Propagate = "major"
	PropagateInherit Propagate = "inherit"
)

// ParsePropagate validates a Propagate value byte-for-byte. Abbreviations are
// rejected (§5.3).
func ParsePropagate(s string) (Propagate, bool) {
	switch Propagate(s) {
	case PropagateNone, PropagatePatch, PropagateMinor, PropagateMajor, PropagateInherit:
		return Propagate(s), true
	default:
		return "", false
	}
}

// Bump returns the concrete bump a Propagate value denotes, given the bump the
// originating unit itself produces. inherit copies that bump (§8.2).
func (p Propagate) Bump(unitBump Bump) Bump {
	switch p {
	case PropagateInherit:
		return unitBump
	case PropagatePatch:
		return BumpPatch
	case PropagateMinor:
		return BumpMinor
	case PropagateMajor:
		return BumpMajor
	default:
		return BumpNone
	}
}

// Depth is a Propagate-Depth value (§8.3): the number of graph edges a
// propagation travels. DepthAll denotes the full transitive closure.
type Depth int

// DepthAll is the "+*" / "all" depth.
const DepthAll Depth = -1

// depthSaturation is the point past which a numeric depth is treated as all;
// no dependency graph is deeper (§20.3).
const depthSaturation = 1024

// IsAll reports whether d is the transitive closure.
func (d Depth) IsAll() bool { return d == DepthAll }

// String implements fmt.Stringer.
func (d Depth) String() string {
	if d.IsAll() {
		return "all"
	}
	return itoa(int(d))
}

// DependencyKind is a manifest dependency field traversed as a graph edge
// (§8.4).
type DependencyKind string

// Dependency kinds.
const (
	KindDependencies         DependencyKind = "dependencies"
	KindDevDependencies      DependencyKind = "devDependencies"
	KindPeerDependencies     DependencyKind = "peerDependencies"
	KindOptionalDependencies DependencyKind = "optionalDependencies"
	KindAll                  DependencyKind = "all"
)

// ParseDependencyKind validates a single dependency-edge kind (§8.4).
func ParseDependencyKind(s string) (DependencyKind, bool) {
	switch DependencyKind(s) {
	case KindDependencies, KindDevDependencies, KindPeerDependencies,
		KindOptionalDependencies, KindAll:
		return DependencyKind(s), true
	default:
		return "", false
	}
}

// Reserved channel values (§11.2). None of them may be used as a channel name,
// and a package may not be named after them.
const (
	// ChannelStable is the non-prerelease line. Legal on either side of a
	// transition, and as a whole value.
	ChannelStable = "stable"
	// ChannelInherit means "the origin's channel". A Propagate-Channel value
	// only, never a side of a transition.
	ChannelInherit = "inherit"
	// ChannelNone disables channel propagation. A Propagate-Channel value
	// only, never a side of a transition.
	ChannelNone = "none"
	// ChannelAnyPrerelease is "*", legal only as a transition's from-side,
	// where it matches any prerelease channel and never matches stable.
	ChannelAnyPrerelease = "*"
)

// ChannelValue is a parsed channel directive value (§11.2):
//
//	channel-value = [ from ">" ] to
//	from          = channel-name / "stable" / "*"
//	to            = channel-name / "stable"
//
// Propagate-Channel additionally accepts the whole-value words "inherit" and
// "none", which are reported in Word.
type ChannelValue struct {
	// Word is ChannelInherit or ChannelNone when the entire value is one of
	// those. From and To are then empty.
	Word string
	// From is the transition's source, or empty when the value names only a
	// target. ChannelAnyPrerelease matches any prerelease channel.
	From string
	// To is the target: a channel name or ChannelStable. Empty when Word is
	// set.
	To string
}

// IsTransition reports whether the value is a <from>><to> form.
func (c ChannelValue) IsTransition() bool { return c.From != "" }

// IsWord reports whether the whole value was inherit or none.
func (c ChannelValue) IsWord() bool { return c.Word != "" }

// IsZero reports whether no channel value was given at all.
func (c ChannelValue) IsZero() bool { return c.Word == "" && c.From == "" && c.To == "" }

// configChannelValue lifts a configured channel string into a ChannelValue.
// Configuration cannot express a transition, so the result is either one of
// the two words or a plain target.
func configChannelValue(ch string) ChannelValue {
	switch ch {
	case "":
		return ChannelValue{Word: ChannelInherit}
	case ChannelInherit, ChannelNone:
		return ChannelValue{Word: ch}
	default:
		return ChannelValue{To: ch}
	}
}

// String renders the value as it would be written.
func (c ChannelValue) String() string {
	switch {
	case c.Word != "":
		return c.Word
	case c.From != "":
		return c.From + ">" + c.To
	default:
		return c.To
	}
}

// Reserved type names that carry control semantics rather than a bump (§7).
const (
	TypeCancel  = "cancel"
	TypeRelease = "release"
)

// ScopeTerm is one comma-separated term of a scope-set (§5.2). Resolution of a
// term to concrete packages requires a workspace and is out of scope for this
// package; the term is reported exactly as written, with the leading "-" of an
// exclusion stripped from Name.
type ScopeTerm struct {
	// Raw is the term as written, including any leading "-".
	Raw string
	// Name is the term without its exclusion marker.
	Name string
	// Exclude reports whether the term was written with a leading "-".
	Exclude bool
	// Position is where the term begins in the message.
	Position Position
}

// newScopeTerm splits an exclusion marker off a raw term. A term consisting of
// a lone "-" is treated as the literal name "-", because the grammar requires
// at least one scope-char after the marker (Appendix C).
func newScopeTerm(raw string, pos Position) ScopeTerm {
	t := ScopeTerm{Raw: raw, Name: raw, Position: pos}
	if len(raw) > 1 && raw[0] == '-' {
		t.Exclude = true
		t.Name = raw[1:]
	}
	return t
}

// IsGlob reports whether the term contains the "*" wildcard.
func (t ScopeTerm) IsGlob() bool { return strings.ContainsRune(t.Name, '*') }

// IsAll reports whether the term addresses every package in the workspace,
func (t ScopeTerm) IsAll() bool { return t.Name == "*" }

// IsDerived reports whether the term is ".", the file-derived set (§6.2).
func (t ScopeTerm) IsDerived() bool { return t.Name == "." }

// String implements fmt.Stringer.
func (t ScopeTerm) String() string { return t.Raw }

// ScopeSet is an ordered list of scope terms.
type ScopeSet []ScopeTerm

// Includes returns the terms without an exclusion marker.
func (s ScopeSet) Includes() ScopeSet {
	out := make(ScopeSet, 0, len(s))
	for _, t := range s {
		if !t.Exclude {
			out = append(out, t)
		}
	}
	return out
}

// Excludes returns the terms with an exclusion marker, markers stripped.
func (s ScopeSet) Excludes() ScopeSet {
	out := make(ScopeSet, 0, len(s))
	for _, t := range s {
		if t.Exclude {
			out = append(out, t)
		}
	}
	return out
}

// Names returns every term's Name in order.
func (s ScopeSet) Names() []string {
	out := make([]string, 0, len(s))
	for _, t := range s {
		out = append(out, t.Name)
	}
	return out
}

// String renders the set as it would be written inside parentheses.
func (s ScopeSet) String() string {
	parts := make([]string, 0, len(s))
	for _, t := range s {
		parts = append(parts, t.Raw)
	}
	return strings.Join(parts, ",")
}

// ReleaseAsKind discriminates the three forms of a Release-As value (§8.6).
type ReleaseAsKind int

// Release-As forms. All three operate at the same level — the package, for the
// current window — because Release-As decides whether and at what version a
// package is released, never how large a change is (§8.6).
//
// There is deliberately no bump form: how large a change is, is a property of
// the change, and the type already declares it. Release-As: minor is E151.
const (
	// ReleaseAsExact pins a specific version.
	ReleaseAsExact ReleaseAsKind = iota
	// ReleaseAsNone holds the package: it is not released in this window, but
	// its pending units are retained rather than discarded, and accumulate
	// until the hold is lifted (§8.6.1). This is what distinguishes it from
	// cancel, which erases.
	ReleaseAsNone
	// ReleaseAsAuto lifts an active hold and returns to normal computation,
	// releasing everything that accumulated at the max() of all of it.
	ReleaseAsAuto
)

// IsHold reports whether the directive pauses publishing (§8.6.1).
func (k ReleaseAsKind) IsHold() bool { return k == ReleaseAsNone }

// String implements fmt.Stringer.
func (k ReleaseAsKind) String() string {
	switch k {
	case ReleaseAsExact:
		return "exact"
	case ReleaseAsNone:
		return "none"
	case ReleaseAsAuto:
		return "auto"
	default:
		return "invalid"
	}
}

// ReleaseAs is a parsed Release-As footer value (§8.6). It never carries a
// bump: Release-As acts on the release, not on the size of the change.
type ReleaseAs struct {
	Kind    ReleaseAsKind
	Version Version // valid when Kind is ReleaseAsExact
	Raw     string
}

// String implements fmt.Stringer.
func (r ReleaseAs) String() string { return r.Raw }

// InlineDirectives holds the directives written in a header with sigils
// (§5.3). A nil field means the sigil was absent; it does not mean "default".
type InlineDirectives struct {
	// Propagate is set by "^x" or a non-empty "^^x".
	Propagate *Propagate
	// Depth is set by "+N", by a bare or valued "^" (as 1), or by "^^" (as all).
	Depth *Depth
	// Channel is set by "%x": the unit's own channel.
	Channel *ChannelValue
	// PropagateChannel is set by "%%x": the channel given to dependents.
	PropagateChannel *ChannelValue
	// ChannelDepth is set by "++N", or implied as 1 by "%%".
	ChannelDepth *Depth

	// depthFrom and channelDepthFrom record which token supplied each depth.
	// They are scanner scratch rather than result: the semantic pass needs to
	// tell an implied depth from an explicit one, and they are what make every
	// combination of the sigils order-independent (§20.3).
	depthFrom        depthSource
	channelDepthFrom depthSource
}

// depthSource records which token supplied an inline depth (§20.3).
type depthSource uint8

const (
	depthUnset depthSource = iota
	// depthFromCaret is "^": implies 1, and an explicit +N overrides it
	// silently.
	depthFromCaret
	// depthFromDoubleCaret is "^^": asserts all, so an explicit +N that
	// disagrees is E113 rather than a silent override.
	depthFromDoubleCaret
	// depthFromPlus is an explicit "+N"; a second one is E110.
	depthFromPlus
	// depthFromDoubleSigil is "%%": implies channel depth 1.
	depthFromDoubleSigil
	// depthFromDoublePlus is an explicit "++N"; a second one is E110.
	depthFromDoublePlus
)

// IsEmpty reports whether the header carried no inline directive at all.
func (d InlineDirectives) IsEmpty() bool {
	return d.Propagate == nil && d.Depth == nil && d.Channel == nil &&
		d.PropagateChannel == nil && d.ChannelDepth == nil
}

// depthFromDoubleCaretSigil reports whether an inline depth came from "^^"
// rather than an explicit "+N". It is what separates the redundant "^^minor+*"
// (W110) from the contradictory "^^minor+2" (E113).
func (d InlineDirectives) depthFromDoubleCaretSigil() bool {
	return d.depthFrom == depthFromDoubleCaret
}

// depthWasImplied reports whether an inline depth was merely implied by "^"
// rather than stated. An implied depth is a default and yields silently to an
// explicit "+N" or a Propagate-Depth footer; "^^" is not implied but asserted,
// which is what depthFromDoubleCaretSigil separates out.
func (d InlineDirectives) depthWasImplied() bool {
	return d.depthFrom == depthFromCaret
}

// channelDepthWasImplied reports whether the inline channel depth came from
// "%%" rather than an explicit "++N".
func (d InlineDirectives) channelDepthWasImplied() bool {
	return d.channelDepthFrom == depthFromDoubleSigil
}

// Directives is the reconciled directive state of a unit: inline sigils and
// footers merged, then filled in from configuration and the spec defaults.
// The *Set fields report whether the value was stated by the author rather
// than inherited from a default.
type Directives struct {
	// --- bump axis (§8.2, §8.3) ---

	Propagate    Propagate
	PropagateSet bool

	Depth    Depth
	DepthSet bool

	PropagateScope    ScopeSet
	PropagateScopeSet bool

	// --- channel axis (§8.3a, §9.3) ---

	PropagateChannel    ChannelValue
	PropagateChannelSet bool

	ChannelDepth    Depth
	ChannelDepthSet bool

	PropagateChannelScope    ScopeSet
	PropagateChannelScopeSet bool

	// --- the unit's own channel (§11.1) ---

	Channel    ChannelValue
	ChannelSet bool

	// --- release control (§8.6) ---

	ReleaseAs *ReleaseAs

	// Kinds are the manifest fields traversed as propagation edges. They come
	// from configuration alone: §8.4 removed the per-unit override, because
	// which dependency fields imply "must be republished" is a fact about the
	// repository, not about any one commit.
	//
	// The slice aliases the parser's configuration and must be treated as
	// read-only.
	Kinds []DependencyKind

	// Reverts holds the values of any Reverts footers, which are informational
	// (§8.1).
	Reverts []string

	// BreakingChange is the text of a BREAKING CHANGE footer, if present.
	BreakingChange string
}

// itoa is a tiny non-allocating-path integer formatter, kept local so that
// String methods do not pull fmt into hot paths.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
