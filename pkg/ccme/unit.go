package ccme

import (
	"errors"
	"strings"
)

// Sentinel errors for footer value validation. They never reach the caller as
// errors: the semantic pass turns each into an E151 diagnostic carrying the
// footer's position.
var (
	errEmptyScopeSet    = errors.New("empty scope term")
	errIllegalScopeChar = errors.New("illegal character in scope term")
)

// Unit is one <header>[body][footers] block of a commit message (§2, §4.4).
type Unit struct {
	// Index is the unit's position in the message, starting at 0.
	Index int
	// Start is where the unit's header begins in the normalised message.
	Start Position
	// Raw is the unit's text.
	Raw string
	// Header is the parsed header. It is zero-valued when the header itself
	// failed to parse.
	Header Header
	// Body is the free-form body, without the trailing footer block.
	Body string
	// Footers are the trailers of the unit's final paragraph.
	Footers []Footer
	// Directives is the reconciled directive state: inline sigils and footers
	// merged, then filled in from configuration and the spec defaults.
	Directives Directives
	// Breaking reports "!" on the header or a BREAKING CHANGE footer (§5.4).
	Breaking bool
	// TypeBump is the bump the type alone maps to (§7.1).
	TypeBump Bump
	// Bump is the unit's direct bump: TypeBump, raised to major by a breaking
	// marker, then overridden by Release-As (§13.6).
	Bump Bump
	// Valid reports that no error-severity diagnostic is attached to the unit.
	// An invalid unit contributes nothing, but its siblings still apply (§16).
	Valid bool
	// Diagnostics are the diagnostics raised for this unit.
	Diagnostics []Diagnostic
}

// addError attaches an error diagnostic and invalidates the unit.
func (u *Unit) addError(d Diagnostic) {
	d.UnitIndex = u.Index
	u.Diagnostics = append(u.Diagnostics, d)
	u.Valid = false
}

// IsCancel reports whether the unit is a cancel barrier (§10).
func (u *Unit) IsCancel() bool { return u.Header.Type == TypeCancel }

// IsRelease reports whether the unit is a directive-only release unit (§7.2).
func (u *Unit) IsRelease() bool { return u.Header.Type == TypeRelease }

// IsControl reports whether the unit is a control unit, which never produces a
// bump of its own (§7.1).
func (u *Unit) IsControl() bool { return u.IsCancel() || u.IsRelease() }

// HasExplicitScope reports whether the header carried a scope-set. When it did
// not, the unit's packages are derived from the commit's changed files (§6.2).
func (u *Unit) HasExplicitScope() bool { return u.Header.HasScopeSet }

// Scopes returns the unit's scope terms.
func (u *Unit) Scopes() ScopeSet { return u.Header.Scopes }

// BreakingDescription returns the text of a BREAKING CHANGE footer, if any.
func (u *Unit) BreakingDescription() string { return u.Directives.BreakingChange }

// unitBuilder accumulates diagnostics for a single unit during the semantic
// pass, so that several independent problems are reported at once.
type unitBuilder struct {
	p *Parser
	u *Unit
}

func (b *unitBuilder) errf(code string, pos Position, format string, args ...any) {
	b.u.addError(Diagnostic{
		Code:     code,
		Severity: SeverityError,
		Message:  formatMessage(format, args),
		Position: pos,
	})
}

func (b *unitBuilder) warnf(code string, pos Position, format string, args ...any) {
	d := warn(code, pos, format, args...)
	d.UnitIndex = b.u.Index
	b.u.Diagnostics = append(b.u.Diagnostics, d)
}

// footerDirectives is the directive state extracted from a unit's footers,
// before reconciliation with the header's inline directives.
type footerDirectives struct {
	propagate    *Propagate
	depth        *Depth
	scope        ScopeSet
	scopeSet     bool
	channel      *ChannelValue
	pchannel     *ChannelValue
	channelDepth *Depth
	cscope       ScopeSet
	cscopeSet    bool
	releaseAs    *ReleaseAs
	reverts      []string
	breaking     *string
	// registryKeys counts footers from the §8.1 registry, which is what makes
	// a cancel unit E171 and a release unit non-inert.
	registryKeys int
}

// applySemantics runs everything that follows a successful header parse:
// footer validation, type rules, directive reconciliation and bump mapping.
func (p *Parser) applySemantics(u *Unit) {
	b := &unitBuilder{p: p, u: u}

	fd := p.readFooters(b)

	u.Directives.Reverts = fd.reverts
	if fd.breaking != nil {
		u.Directives.BreakingChange = *fd.breaking
	}
	u.Breaking = u.Header.Breaking || fd.breaking != nil

	switch u.Header.Type {
	case TypeCancel:
		// cancel takes a scope-set and nothing else (§10.2).
		if u.Header.Breaking {
			b.errf(CodeE170, u.Header.Position, "a cancel unit must not carry '!'")
		}
		if !u.Header.Inline.IsEmpty() {
			b.errf(CodeE171, u.Header.Position, "a cancel unit must not carry inline directives")
		}
		if fd.registryKeys > 0 {
			b.errf(CodeE171, u.Header.Position, "a cancel unit must not carry release directives in footers")
		}
	case TypeRelease:
		if u.Breaking {
			b.errf(CodeE141, u.Header.Position, "a release unit must not be marked breaking")
		}
		if u.Header.Inline.IsEmpty() && fd.registryKeys == 0 {
			b.warnf(CodeW141, u.Header.Position, "release unit carries no directive and is inert")
		}
	}

	p.reconcile(b, fd)
	p.applyDefaults(u)
	p.checkPropagationRedundancy(b)
	p.checkScopeOverlap(b)
	p.checkReleaseAsScope(b)
	p.applyBumps(b)
}

// readFooters validates every footer value against its key (§8.1) and returns
// the directive state they carry.
func (p *Parser) readFooters(b *unitBuilder) footerDirectives {
	var fd footerDirectives
	for _, f := range b.u.Footers {
		if f.MessageLevel {
			// Authorship trailers are ignored wherever they appear (§4.5).
			continue
		}
		if !f.Known {
			switch {
			case f.MiscasedBreaking:
				// The silent failure §8.1.1 exists to prevent: this looks like
				// a breaking change to a human and is not one to the parser.
				b.warnf(CodeW155, f.Position,
					"footer key %q is not %q: case is significant, so this is NOT a breaking change",
					f.Key, FooterBreakingChange)
			case !f.IssueReference:
				b.warnf(CodeW150, f.Position, "unknown footer key %q ignored", f.Key)
			}
			continue
		}
		fd.registryKeys++
		switch f.CanonicalKey {
		case FooterBreakingChange:
			value := f.Value
			fd.breaking = &value
			if value == "" {
				b.warnf(CodeW157, f.Position,
					"%s carries no explanation", FooterBreakingChange)
			}

		case FooterPropagate:
			v, ok := ParsePropagate(f.Value)
			if !ok {
				b.errf(CodeE151, f.Position, "invalid %s value %q", f.CanonicalKey, f.Value)
				continue
			}
			fd.propagate = &v

		case FooterPropagateDepth:
			d, err := parseDepthValue(f.Value, f.Position)
			if err != nil {
				b.errf(CodeE151, f.Position, "invalid %s value %q", f.CanonicalKey, f.Value)
				continue
			}
			fd.depth = &d

		case FooterPropagateScope:
			scope, err := parseScopeSetValue(f.Value, f.Position)
			if err != nil {
				b.errf(CodeE151, f.Position, "invalid %s value %q", f.CanonicalKey, f.Value)
				continue
			}
			fd.scope, fd.scopeSet = scope, true

		case FooterPropagateChannel:
			v, err := p.parseChannelValue(f.Value, f.Position, true)
			if err != nil {
				d := asDiagnostic(err)
				b.errf(d.Code, d.Position, "%s: %s", f.CanonicalKey, d.Message)
				continue
			}
			fd.pchannel = &v

		case FooterPropagateChannelDepth:
			d, err := parseDepthValue(f.Value, f.Position)
			if err != nil {
				b.errf(CodeE151, f.Position, "invalid %s value %q", f.CanonicalKey, f.Value)
				continue
			}
			fd.channelDepth = &d

		case FooterPropagateChannelScope:
			scope, err := parseScopeSetValue(f.Value, f.Position)
			if err != nil {
				b.errf(CodeE151, f.Position, "invalid %s value %q", f.CanonicalKey, f.Value)
				continue
			}
			fd.cscope, fd.cscopeSet = scope, true

		case FooterChannel:
			v, err := p.parseChannelValue(f.Value, f.Position, false)
			if err != nil {
				d := asDiagnostic(err)
				b.errf(d.Code, d.Position, "%s: %s", f.CanonicalKey, d.Message)
				continue
			}
			fd.channel = &v

		case FooterReleaseAs:
			ra, err := parseReleaseAs(f.Value)
			if err != nil {
				if errors.Is(err, errReleaseAsBump) {
					b.errf(CodeE151, f.Position, "%s: %s", f.CanonicalKey, err.Error())
				} else {
					b.errf(CodeE151, f.Position,
						"invalid %s value %q: expected an exact version, none or auto",
						f.CanonicalKey, f.Value)
				}
				continue
			}
			fd.releaseAs = ra

		case FooterReverts:
			fd.reverts = append(fd.reverts, f.Value)
		}
	}
	return fd
}

// reconcile merges inline directives with footers. Inline and footer forms are
// exactly equivalent; stating both is redundant, contradicting is an error
// (§5.3).
func (p *Parser) reconcile(b *unitBuilder, fd footerDirectives) {
	u := b.u
	pos := u.Header.Position
	in := u.Header.Inline

	if v := reconcileDirective(b, FooterPropagate, in.Propagate, fd.propagate, pos); v != nil {
		u.Directives.Propagate, u.Directives.PropagateSet = *v, true
	}
	if v := p.reconcileDepth(b, FooterPropagateDepth, in.Depth, fd.depth, pos,
		in.depthWasImplied(), in.depthFromDoubleCaretSigil()); v != nil {
		u.Directives.Depth, u.Directives.DepthSet = *v, true
	}
	if v := reconcileDirective(b, FooterChannel, in.Channel, fd.channel, pos); v != nil {
		u.Directives.Channel, u.Directives.ChannelSet = *v, true
	}
	if v := reconcileDirective(b, FooterPropagateChannel, in.PropagateChannel, fd.pchannel, pos); v != nil {
		u.Directives.PropagateChannel, u.Directives.PropagateChannelSet = *v, true
	}
	if v := p.reconcileDepth(b, FooterPropagateChannelDepth, in.ChannelDepth, fd.channelDepth, pos,
		in.channelDepthWasImplied(), false); v != nil {
		u.Directives.ChannelDepth, u.Directives.ChannelDepthSet = *v, true
	}
	if fd.scopeSet {
		u.Directives.PropagateScope, u.Directives.PropagateScopeSet = fd.scope, true
	}
	if fd.cscopeSet {
		u.Directives.PropagateChannelScope, u.Directives.PropagateChannelScopeSet = fd.cscope, true
	}
	u.Directives.ReleaseAs = fd.releaseAs
}

// reconcileDepth resolves a depth, where the inline side may have been merely
// implied rather than written (§8.3).
//
// A sigil that implies a depth — "^" implies 1, "@@" implies 1 — states a
// default, not an intent, so a footer that names one wins silently. "^^" is
// different: it *asserts* a depth of all, so a footer that disagrees is E113,
// exactly as a disagreeing "+N" is. Only a depth the author wrote as a number
// takes the ordinary E112/W110 path.
func (p *Parser) reconcileDepth(b *unitBuilder, key string, inline, footer *Depth,
	pos Position, implied, asserted bool) *Depth {

	if inline == nil || footer == nil || !(implied || asserted) {
		return reconcileDirective(b, key, inline, footer, pos)
	}
	if implied {
		return footer
	}
	// asserted: "^^"
	if !footer.IsAll() {
		b.errf(CodeE113, pos,
			"footer %s: %s contradicts the depth of all asserted by '^^'", key, *footer)
		return footer
	}
	b.warnf(CodeW110, pos, "footer %s redundantly restates the depth implied by '^^'", key)
	return inline
}

// reconcileDirective resolves one key that can be written both inline and as a
// footer.
func reconcileDirective[T comparable](b *unitBuilder, key string, inline, footer *T, pos Position) *T {
	switch {
	case inline == nil && footer == nil:
		return nil
	case inline == nil:
		return footer
	case footer == nil:
		return inline
	case *inline == *footer:
		b.warnf(CodeW110, pos, "%s is stated both inline and as a footer with the same value", key)
		return inline
	case b.p.cfg.Lenient:
		b.warnf(CodeW112, pos, "footer %s overrode the inline directive under lenient mode", key)
		return footer
	default:
		b.errf(CodeE112, pos, "inline directive and footer set %s to different values", key)
		return footer
	}
}

// applyDefaults fills unset directives from configuration, which in turn
// defaults to the values in §14.
func (p *Parser) applyDefaults(u *Unit) {
	d := &u.Directives
	if !d.PropagateSet {
		d.Propagate = p.cfg.Propagation.Bump
	}
	if !d.DepthSet {
		d.Depth = p.cfg.Propagation.Depth
	}
	if !d.PropagateChannelSet {
		d.PropagateChannel = configChannelValue(p.cfg.Propagation.Channel)
	}
	if !d.ChannelDepthSet {
		d.ChannelDepth = p.cfg.Propagation.ChannelDepth
	}
	// §8.4 removed the per-unit override, so the edge kinds always come from
	// configuration. The slice is shared, and documented read-only.
	d.Kinds = p.cfg.Propagation.Kinds
	// Propagate-Channel-Scope defaults to the unit's Propagate-Scope (§8.1).
	if !d.PropagateChannelScopeSet && d.PropagateScopeSet {
		d.PropagateChannelScope = d.PropagateScope
	}
}

// checkPropagationRedundancy emits W152 for a pairing that spells "no
// propagation" twice (§8.3, §15.3 #38).
func (p *Parser) checkPropagationRedundancy(b *unitBuilder) {
	u := b.u
	d := u.Directives
	pos := u.Header.Position

	// "Supplied" means written by the unit, in the header or a footer — never
	// taken from configuration (§8.3b).
	bumpValueSupplied := d.PropagateSet && d.Propagate != PropagateNone
	bumpNothing := d.Propagate == PropagateNone || d.Depth == 0
	bumpTouched := d.PropagateSet || d.DepthSet

	chanValueSupplied := d.PropagateChannelSet &&
		d.PropagateChannel.Word != ChannelNone && !d.PropagateChannel.IsZero()
	chanNothing := d.PropagateChannel.Word == ChannelNone || d.ChannelDepth == 0
	chanTouched := d.PropagateChannelSet || d.ChannelDepthSet

	// W201 is the more specific finding and suppresses W152 for that axis: a
	// value was asked for and the depth throws it away.
	switch {
	case bumpValueSupplied && d.Depth == 0:
		b.warnf(CodeW201, pos,
			"propagation bump %q reaches nobody: the depth on that axis is 0",
			string(d.Propagate))
	case bumpTouched && bumpNothing:
		b.warnf(CodeW152, pos,
			"the propagation directive resolves to no propagation; deleting it changes nothing")
	}

	switch {
	case chanValueSupplied && d.ChannelDepth == 0:
		b.warnf(CodeW201, pos,
			"propagation channel %q reaches nobody: the channel depth is 0",
			d.PropagateChannel.String())
	case chanTouched && chanNothing:
		b.warnf(CodeW152, pos,
			"the channel-propagation directive resolves to no propagation; deleting it changes nothing")
	}

	// W207: a transition that goes nowhere, on either channel key.
	for _, c := range []struct {
		key string
		val ChannelValue
		set bool
	}{
		{FooterChannel, d.Channel, d.ChannelSet},
		{FooterPropagateChannel, d.PropagateChannel, d.PropagateChannelSet},
	} {
		if c.set && c.val.IsTransition() && c.val.From == c.val.To {
			b.warnf(CodeW207, pos,
				"%s transition %q has the same source and target and is inert",
				c.key, c.val.String())
		}
	}
}

// checkScopeOverlap emits W133 when a package is both included and excluded.
// Excludes always win (§15.2 #24).
func (p *Parser) checkScopeOverlap(b *unitBuilder) {
	scopes := b.u.Header.Scopes
	if len(scopes) < 2 {
		return
	}
	// Scope-sets hold a handful of terms, so the quadratic scan is faster than
	// building a set and, unlike a map, allocates nothing.
	for _, t := range scopes {
		if !t.Exclude {
			continue
		}
		for _, other := range scopes {
			if !other.Exclude && other.Name == t.Name {
				b.warnf(CodeW133, t.Position,
					"package %q is both included and excluded; the exclusion wins", t.Name)
				break
			}
		}
	}
}

// checkReleaseAsScope enforces E154 for the cases detectable without a
// workspace: two or more explicit include terms, or an include term that
// addresses the whole workspace (§8.6).
func (p *Parser) checkReleaseAsScope(b *unitBuilder) {
	ra := b.u.Directives.ReleaseAs
	if ra == nil || ra.Kind != ReleaseAsExact {
		return
	}
	includes, multi := 0, false
	for _, t := range b.u.Header.Scopes {
		if t.Exclude {
			continue
		}
		includes++
		if t.IsAll() {
			multi = true
		}
	}
	if includes > 1 {
		multi = true
	}
	if multi {
		b.errf(CodeE154, b.u.Header.Position,
			"an exact Release-As of %s cannot apply to a multi-package scope-set", ra.Version)
	}
}

// applyBumps maps the type to a bump and applies the breaking marker and
// Release-As overrides (§7.1, §13.6).
func (p *Parser) applyBumps(b *unitBuilder) {
	u := b.u
	if u.IsControl() {
		u.TypeBump, u.Bump = BumpNone, BumpNone
	} else {
		bump, known := p.cfg.Types[u.Header.Type]
		if !known {
			if p.cfg.StrictTypes {
				b.errf(CodeE140, u.Header.Position, "unknown type %q", u.Header.Type)
			} else {
				b.warnf(CodeW140, u.Header.Position,
					"unknown type %q mapped to bump none", u.Header.Type)
			}
			bump = BumpNone
		}
		u.TypeBump = bump
		u.Bump = bump
		if u.Breaking {
			u.Bump = BumpMajor
		}
	}

	// §13.6: bumpOf(unit) comes from the type mapping and "!" alone. No footer
	// overrides it — Release-As acts on the release, not on the bump. In
	// particular a hold retains its unit's bump, because the pending work
	// accumulates rather than being discarded; that is the whole difference
	// between a hold and a cancel (§8.6.2).
}

// isScopeChar reports whether c may appear inside a scope term: §5.2's
// scope-char, any byte above US-ASCII space except "(", ")", "," and ":".
// High bytes pass — scope names may be UTF-8 — but ASCII control characters
// do not.
func isScopeChar(c byte) bool {
	return c > 0x20 && c != '(' && c != ')' && c != ',' && c != ':'
}

// parseScopeSetValue parses a scope-set written as a footer value, applying
// the scope-term charset of §5.2.
func parseScopeSetValue(v string, pos Position) (ScopeSet, error) {
	if v == "" {
		return nil, errEmptyScopeSet
	}
	var out ScopeSet
	start := 0
	for i := 0; i <= len(v); i++ {
		if i < len(v) && v[i] != ',' {
			continue
		}
		term := v[start:i]
		// Whitespace around a footer scope term is padding, not part of the
		// name; a name may not contain whitespace at all (§5.2).
		trimmed := strings.TrimSpace(term)
		if trimmed == "" {
			return nil, errEmptyScopeSet
		}
		for j := 0; j < len(trimmed); j++ {
			if !isScopeChar(trimmed[j]) {
				return nil, errIllegalScopeChar
			}
		}
		out = append(out, newScopeTerm(trimmed, pos))
		start = i + 1
	}
	return out, nil
}

// errReleaseAsBump reports the removed bump form, which is E151 with an
// explanation rather than a bare "invalid value": it was legal in earlier
// drafts and reads plausible, so the diagnostic has to say why it is gone.
var errReleaseAsBump = errors.New(
	"Release-As has no bump form: how large a change is, is declared by the type — " +
		"change the type, or map it in the types configuration")

// parseReleaseAs parses a Release-As value: an exact semver, a hold, or a
// resume (§8.6). All three are package-level; there is no bump form.
func parseReleaseAs(v string) (*ReleaseAs, error) {
	switch v {
	case "none":
		return &ReleaseAs{Kind: ReleaseAsNone, Raw: v}, nil
	case "auto":
		return &ReleaseAs{Kind: ReleaseAsAuto, Raw: v}, nil
	case "patch", "minor", "major":
		return nil, errReleaseAsBump
	}
	version, err := ParseVersion(v)
	if err != nil {
		return nil, err
	}
	return &ReleaseAs{Kind: ReleaseAsExact, Version: version, Raw: v}, nil
}
