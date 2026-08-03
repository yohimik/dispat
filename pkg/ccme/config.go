package ccme

import (
	"errors"
	"fmt"
	"sort"
)

// Default configuration values (§14).
const (
	DefaultSeparator            = "---"
	DefaultMaxDescriptionLength = 100
	DefaultPropagate            = PropagatePatch
	DefaultDepth                = Depth(0)
	DefaultChannelDepth         = Depth(0)
	DefaultPropagateChannel     = ChannelInherit
)

// DefaultPropagateKinds returns the manifest fields traversed by default
// (§8.4). devDependencies is deliberately absent.
func DefaultPropagateKinds() []DependencyKind {
	return []DependencyKind{KindDependencies, KindPeerDependencies, KindOptionalDependencies}
}

// DefaultTypes returns a fresh copy of the type-to-bump table of §7.1.
func DefaultTypes() map[string]Bump {
	return map[string]Bump{
		"feat":     BumpMinor,
		"fix":      BumpPatch,
		"perf":     BumpPatch,
		"revert":   BumpPatch,
		"refactor": BumpNone,
		"docs":     BumpNone,
		"style":    BumpNone,
		"test":     BumpNone,
		"build":    BumpNone,
		"ci":       BumpNone,
		"chore":    BumpNone,
	}
}

// DefaultMessageLevelTrailers returns the trailer keys that describe
// authorship or review rather than release intent (§4.5). They are ignored
// wherever they appear and never prevent a paragraph from being a footer
// block.
func DefaultMessageLevelTrailers() []string {
	return []string{
		"Signed-off-by", "Co-authored-by", "Change-Id", "Reviewed-by",
		"Acked-by", "Tested-by", "Reported-by", "Suggested-by", "Cc",
	}
}

// DefaultIssueTrailers returns the issue-reference trailer keys, which are
// ignored for versioning but may be surfaced in a changelog (§4.5).
func DefaultIssueTrailers() []string {
	return []string{"Closes", "Fixes", "Refs", "Resolves"}
}

// PropagationConfig holds the configurable propagation defaults (§14). Every
// field is overridden by a directive written on the unit itself; the
// precedence chain is footer → inline sigil → this configuration → the
// specification default (§8.3). A footer wins over the sigil it contradicts,
// with E112, or with W112 under Config.Lenient.
//
// Propagation has two independent axes (§5.3). The bump axis is Bump plus
// Depth, written "^", "^^" and "+N"; the channel axis is Channel plus
// ChannelDepth, written "@@" and "++N". Both depths default to 0, so neither
// axis reaches anybody until a unit or this configuration opts in.
type PropagationConfig struct {
	// Bump is the default Propagate value. Zero value: PropagatePatch.
	Bump Propagate

	// Depth is the default Propagate-Depth (§8.3). Zero value: 0, which is
	// also the specification default — a unit does not propagate unless it
	// says so, so there is no ambiguity between "unset" and "no propagation".
	//
	// Repositories that bundle rather than declare their dependencies should
	// set 1; use DepthAll for the full transitive closure.
	Depth Depth

	// ChannelDepth is the default Propagate-Channel-Depth (§8.3a). Zero value:
	// 0 — a unit moves nobody else's channel unless it says so. Set 1 or
	// DepthAll to carry release trains along by default.
	ChannelDepth Depth

	// Kinds is the set of dependency edges propagation follows (§8.4). It is
	// configuration only — the spec has no per-unit override — so every unit
	// of every message sees this list. A nil slice selects
	// DefaultPropagateKinds; a non-nil empty slice traverses no edges at all.
	Kinds []DependencyKind

	// Channel is the default Propagate-Channel value. Zero value:
	// ChannelInherit.
	Channel string
}

// Default parser bounds (§14.1). Unlike the rest of §14's safety limits,
// which gate a release run and are opt-in, these are always enforced: a
// hostile commit message is untrusted input, and exceeding a bound must be a
// diagnostic rather than unbounded work (§18.3).
const (
	DefaultUnitsPerMessage   = 64
	DefaultScopeTermsPerUnit = 256
	DefaultMessageBytes      = 1 << 20
)

// Limits are the parser bounds of §14.1. Exceeding any of them is E158, which
// is message-scoped: the commit contributes nothing.
//
// The zero value of each field selects the default. A negative value disables
// that bound, which is only appropriate for trusted input.
type Limits struct {
	// UnitsPerMessage caps the number of units in one message. Zero value: 64.
	UnitsPerMessage int
	// ScopeTermsPerUnit caps the terms in one scope-set. Zero value: 256.
	ScopeTermsPerUnit int
	// MessageBytes caps the message length. Zero value: 1 MiB.
	MessageBytes int
}

// Config is the complete parser configuration: every option in one struct.
//
// The zero value is valid and means "the specification defaults", so
// Config{Lenient: true} changes exactly one thing. Any field left at its zero
// value is filled in from §14, which is why DefaultConfig is only needed when
// you want to read the defaults rather than set them.
//
// Keys of §14 that affect release computation rather than parsing
// (tagFormat, initialVersion, preserveMajorZero, rangeStrategy, rootPathMap,
// ignoredPaths, branchChannels, propagation.respectRanges) are deliberately
// absent: this package parses messages.
type Config struct {
	// Separator is the unit separator line (§4.2, §4.3). It must be at least
	// three ASCII-printable characters, must contain no whitespace, and must
	// not begin with a character that can begin a type. Zero value: "---".
	// Repositories that exchange patches by mail should set "%%%".
	Separator string

	// Types maps a type to its default direct bump (§7.1). A nil map selects
	// DefaultTypes; a non-nil map replaces the table wholesale, so add to a
	// copy of DefaultTypes rather than starting from scratch unless you mean
	// to drop the standard types. Type names must consist of a-z only.
	Types map[string]Bump

	// StrictTypes turns an unknown type into E140 instead of W140.
	StrictTypes bool

	// Lenient downgrades selected errors to warnings (§16): an uppercase type
	// is lowercased with W101, a missing space after ':' is accepted with
	// W121, and a footer contradicting an inline directive wins with W112
	// instead of raising E112. Two or more spaces after ':' remain E120 even
	// here, because the extra space is indistinguishable from a description
	// that begins with one (§5.5).
	Lenient bool

	// MaxDescriptionLength is the W120 threshold, counted in Unicode scalar
	// values. Zero value: 100. A negative value disables the check.
	MaxDescriptionLength int

	// Propagation holds the propagation defaults.
	Propagation PropagationConfig

	// Limits are the always-enforced parser bounds of §14.1.
	Limits Limits

	// AllowedChannels restricts channel names (channels.allowed in §14). A nil
	// slice is unrestricted; a non-nil slice rejects any channel name outside
	// it with E181. "stable" is always accepted, because it is a graduation
	// directive rather than a channel name (§11.5).
	AllowedChannels []string

	// MessageLevelTrailers are the authorship and review trailers ignored
	// wherever they appear (§4.5). A nil slice selects
	// DefaultMessageLevelTrailers; a non-nil empty slice disables the
	// behaviour. Matching is case-insensitive.
	MessageLevelTrailers []string

	// IssueTrailers are the issue-reference trailers, ignored for versioning
	// but surfaced on Footer.IssueReference for changelog use (§4.5). A nil
	// slice selects DefaultIssueTrailers. Matching is case-insensitive.
	IssueTrailers []string
}

// DefaultConfig returns the specification defaults, fully populated. It is
// equivalent to Config{} once NewParser has filled in the zero values, and is
// the convenient starting point when you want to adjust one field:
//
//	cfg := ccme.DefaultConfig()
//	cfg.Types["deps"] = ccme.BumpPatch
//	p, err := ccme.NewParser(cfg)
func DefaultConfig() Config {
	return Config{
		Separator:            DefaultSeparator,
		Types:                DefaultTypes(),
		StrictTypes:          false,
		Lenient:              false,
		MaxDescriptionLength: DefaultMaxDescriptionLength,
		Propagation: PropagationConfig{
			Bump:    DefaultPropagate,
			Depth:        DefaultDepth,
			ChannelDepth: DefaultChannelDepth,
			Kinds:        DefaultPropagateKinds(),
			Channel:      DefaultPropagateChannel,
		},
		Limits: Limits{
			UnitsPerMessage:   DefaultUnitsPerMessage,
			ScopeTermsPerUnit: DefaultScopeTermsPerUnit,
			MessageBytes:      DefaultMessageBytes,
		},
		MessageLevelTrailers: DefaultMessageLevelTrailers(),
		IssueTrailers:        DefaultIssueTrailers(),
	}
}

// Clone returns a deep copy, so that mutating the result cannot affect the
// original or any parser built from it.
//
// The nil-versus-empty distinction is preserved exactly, because it is
// load-bearing: a nil slice selects the default, a non-nil empty one means
// "none".
func (c Config) Clone() Config {
	out := c
	if c.Types != nil {
		out.Types = make(map[string]Bump, len(c.Types))
		for name, bump := range c.Types {
			out.Types[name] = bump
		}
	}
	out.Propagation.Kinds = cloneSlice(c.Propagation.Kinds)
	out.AllowedChannels = cloneSlice(c.AllowedChannels)
	out.MessageLevelTrailers = cloneSlice(c.MessageLevelTrailers)
	out.IssueTrailers = cloneSlice(c.IssueTrailers)
	return out
}

// cloneSlice copies a slice, mapping nil to nil and an empty slice to a
// distinct empty slice.
//
// This is deliberately not append([]T(nil), s...): that expression returns nil
// when s is empty, which would silently turn "none" into "use the default".
func cloneSlice[T any](s []T) []T {
	if s == nil {
		return nil
	}
	out := make([]T, len(s))
	copy(out, s)
	return out
}

// withDefaults returns a deep copy with every zero-valued field replaced by
// its specification default.
func (c Config) withDefaults() Config {
	out := c.Clone()
	if out.Separator == "" {
		out.Separator = DefaultSeparator
	}
	if out.Types == nil {
		out.Types = DefaultTypes()
	}
	if out.MaxDescriptionLength == 0 {
		out.MaxDescriptionLength = DefaultMaxDescriptionLength
	}
	if out.Propagation.Bump == "" {
		out.Propagation.Bump = DefaultPropagate
	}
	if out.Propagation.Kinds == nil {
		out.Propagation.Kinds = DefaultPropagateKinds()
	}
	if out.Propagation.Channel == "" {
		out.Propagation.Channel = DefaultPropagateChannel
	}
	if out.MessageLevelTrailers == nil {
		out.MessageLevelTrailers = DefaultMessageLevelTrailers()
	}
	if out.IssueTrailers == nil {
		out.IssueTrailers = DefaultIssueTrailers()
	}
	if out.Limits.UnitsPerMessage == 0 {
		out.Limits.UnitsPerMessage = DefaultUnitsPerMessage
	}
	if out.Limits.ScopeTermsPerUnit == 0 {
		out.Limits.ScopeTermsPerUnit = DefaultScopeTermsPerUnit
	}
	if out.Limits.MessageBytes == 0 {
		out.Limits.MessageBytes = DefaultMessageBytes
	}
	return out
}

// Validate reports the first problem with a configuration. NewParser calls it
// after filling in defaults, so Config{}.Validate() checking a partially
// filled struct may report a zero value that NewParser would have accepted;
// prefer letting NewParser do the validation.
func (c Config) Validate() error {
	if err := validateSeparator(c.Separator); err != nil {
		return err
	}
	// Sorted so that a configuration with two bad type names always reports
	// the same one: §17.2 forbids observable map-iteration order.
	names := make([]string, 0, len(c.Types))
	for name := range c.Types {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := validateTypeName(name); err != nil {
			return err
		}
	}
	if _, ok := ParsePropagate(string(c.Propagation.Bump)); !ok {
		return fmt.Errorf("ccme: config: invalid propagation.bump %q", string(c.Propagation.Bump))
	}
	if c.Propagation.Depth < 0 && c.Propagation.Depth != DepthAll {
		return fmt.Errorf("ccme: config: invalid propagation.depth %d", int(c.Propagation.Depth))
	}
	for _, kind := range c.Propagation.Kinds {
		if _, ok := ParseDependencyKind(string(kind)); !ok {
			return fmt.Errorf("ccme: config: invalid propagation.kind %q", string(kind))
		}
	}
	if c.Propagation.Channel != ChannelInherit && c.Propagation.Channel != ChannelStable {
		if err := validateChannelName(c.Propagation.Channel); err != nil {
			return fmt.Errorf("ccme: config: propagation.channel: %w", err)
		}
	}
	for _, ch := range c.AllowedChannels {
		if err := validateChannelName(ch); err != nil {
			return fmt.Errorf("ccme: config: channels.allowed: %w", err)
		}
	}
	return nil
}

func validateSeparator(sep string) error {
	if len(sep) < 3 {
		return fmt.Errorf("ccme: config: separator %q must be at least three characters", sep)
	}
	for i := 0; i < len(sep); i++ {
		c := sep[i]
		if c < 0x21 || c > 0x7e {
			return fmt.Errorf(
				"ccme: config: separator %q must be ASCII printable and contain no whitespace", sep)
		}
	}
	if isLower(sep[0]) {
		return fmt.Errorf(
			"ccme: config: separator %q must not begin with a character that can begin a type", sep)
	}
	return nil
}

func validateTypeName(name string) error {
	if name == "" {
		return errors.New("ccme: config: type name must not be empty")
	}
	for i := 0; i < len(name); i++ {
		if !isLower(name[i]) {
			return fmt.Errorf("ccme: config: type name %q must consist of a-z only", name)
		}
	}
	return nil
}

// validateChannelName applies the charset rules of §11.2 outside of parsing,
// where positions are not available.
func validateChannelName(ch string) error {
	if ch == "" {
		return errors.New("channel name must not be empty")
	}
	if ch == "latest" {
		return errors.New(`channel name "latest" is reserved`)
	}
	if len(ch) > 32 {
		return fmt.Errorf("channel name %q exceeds 32 characters", ch)
	}
	if !isLower(ch[0]) {
		return fmt.Errorf("channel name %q must begin with a lowercase letter", ch)
	}
	for i := 0; i < len(ch); i++ {
		if !isChannelChar(ch[i]) {
			return fmt.Errorf("channel name %q contains an illegal character", ch)
		}
	}
	return nil
}
