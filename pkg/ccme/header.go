package ccme

import (
	"strings"
	"unicode/utf8"
)

// Header is a parsed unit header (§5):
//
//	<type>[(<scope-set>)][<inline-directives>][!]: <description>
type Header struct {
	// Raw is the header line exactly as it appeared after normalisation.
	Raw string
	// Type is the lowercase type.
	Type string
	// HasScopeSet reports whether parentheses were written at all. It is what
	// distinguishes "feat: x" (derived scope, §6.2) from "feat(*): x".
	HasScopeSet bool
	// Scopes holds the scope terms in written order.
	Scopes ScopeSet
	// Inline holds the directives written with sigils.
	Inline InlineDirectives
	// Breaking reports whether "!" preceded the colon.
	Breaking bool
	// Description is the remainder of the line after ": ".
	Description string
	// Position is the start of the header in the message.
	Position Position
}

// parseHeader implements §20.3: five phases, one pass, one byte of lookahead.
// The returned diagnostics are warnings only; a violation is returned as an
// error carrying the offending position.
func (p *Parser) parseHeader(line string, pos Position) (Header, []Diagnostic, error) {
	sc := &scanner{s: line}
	h := Header{Raw: line, Position: pos}
	var warns []Diagnostic

	at := func(i int) Position { return pos.shift(i) }

	// --- 1. type ---------------------------------------------------------
	//
	// BREAKING CHANGE is uppercase and contains a space, so it fails §5.1
	// twice over and can never be a type. Writing it as a header is a common
	// enough mistake that §5.1 requires a dedicated diagnostic rather than the
	// generic "type must be lowercase".
	if strings.HasPrefix(line, FooterBreakingChange) || strings.HasPrefix(line, footerBreakingHyphen) {
		return h, warns, fail(CodeE100, pos,
			"%s is a footer, not a type: write %q on the header, or put the footer in the unit's final paragraph",
			FooterBreakingChange, "!")
	}

	typeStart := sc.i
	typePred := isLower
	if p.cfg.Lenient {
		typePred = isASCIILetter
	}
	typ := sc.readWhile(typePred)
	if typ == "" {
		if !sc.eof() && isUpperASCII(sc.peek()) {
			return h, warns, fail(CodeE101, at(sc.i),
				"type must be lowercase a-z, found %q", string(sc.peek()))
		}
		return h, warns, fail(CodeE100, at(sc.i), "header must begin with a type")
	}
	if lowered := toLowerASCII(typ); lowered != typ {
		warns = append(warns, warn(CodeW101, at(typeStart),
			"type %q lowercased to %q under lenient mode", typ, lowered))
		typ = lowered
	}
	h.Type = typ
	if !sc.eof() && !isTypeTerminator(sc.peek()) {
		if isUpperASCII(sc.peek()) {
			return h, warns, fail(CodeE101, at(sc.i),
				"type must be lowercase a-z, found %q", string(sc.peek()))
		}
		return h, warns, fail(CodeE101, at(sc.i),
			"illegal character %q in type", string(sc.peek()))
	}

	// --- 2. optional scope-set -------------------------------------------
	//
	// A scope term is always a contiguous run of the header (every other case
	// of the switch below ends the term), so each one is sliced out of the
	// line rather than accumulated into a buffer.
	if sc.accept('(') {
		h.HasScopeSet = true
		termStart := sc.i
	scopeLoop:
		for {
			if sc.eof() {
				return h, warns, fail(CodeE103, at(sc.i),
					"unterminated scope-set: missing ')'")
			}
			ci := sc.i
			switch sc.next() {
			case ')':
				if ci == termStart {
					return h, warns, fail(CodeE104, at(ci), "empty scope term")
				}
				// The closing term counts against the cap too; checking only
				// at commas would quietly admit limit+1 terms.
				if n := p.cfg.Limits.ScopeTermsPerUnit; n > 0 && len(h.Scopes) >= n {
					return h, warns, fail(CodeE158, at(ci),
						"scope-set has more than %d terms", n)
				}
				h.Scopes = append(h.Scopes, newScopeTerm(line[termStart:ci], at(termStart)))
				break scopeLoop
			case ',':
				if ci == termStart {
					return h, warns, fail(CodeE104, at(ci), "empty scope term")
				}
				if n := p.cfg.Limits.ScopeTermsPerUnit; n > 0 && len(h.Scopes) >= n {
					return h, warns, fail(CodeE158, at(ci),
						"scope-set has more than %d terms", n)
				}
				h.Scopes = append(h.Scopes, newScopeTerm(line[termStart:ci], at(termStart)))
				// Whitespace immediately after a comma is permitted (§5.2).
				for !sc.eof() && sc.peek() == ' ' {
					sc.next()
				}
				termStart = sc.i
			case ' ', '\t':
				return h, warns, fail(CodeE102, at(ci),
					"whitespace inside a scope-set is only allowed after a comma")
			case '(':
				return h, warns, fail(CodeE103, at(ci),
					"parentheses must not nest inside a scope-set")
			case ':':
				return h, warns, fail(CodeE103, at(ci),
					"unbalanced scope-set: ':' reached before ')'")
			default:
				// §5.2's scope-char excludes the ASCII control range as a
				// whole, not just space and tab.
				if !isScopeChar(line[ci]) {
					return h, warns, fail(CodeE102, at(ci),
						"control character inside a scope-set")
				}
			}
		}
	}

	// --- 3. inline directives --------------------------------------------
	//
	// Two independent axes (§5.3). The bump axis is "^", "^^" and "+N"; the
	// channel axis is "%%" and "++N". "%" sits on neither: it sets the unit's
	// own channel.
	//
	// sawCaret enforces the once-per-header rule across both caret spellings,
	// and the two depthSource fields record which token supplied each depth,
	// which is what makes every combination order-independent (§20.3).
	sawCaret := false
	for !sc.eof() && isSigil(sc.peek()) {
		sigilPos := sc.i
		sigil := sc.next()

		// Each doubled token is a fixed two-character token distinguished from
		// its single form by one peek, and each guards against a third
		// repetition: without that, "^^^minor" would tokenise as "^^" with an
		// empty value followed by "^minor" and parse silently as "^^minor".
		// Repeated sigils are never a count.
		doubled := !sc.eof() && sc.peek() == sigil

		if sigil == '^' && doubled {
			sc.next()
			if sawCaret {
				return h, warns, fail(CodeE110, at(sigilPos),
					"duplicate propagation sigil: '^' and '^^' are the same sigil")
			}
			if !sc.eof() && sc.peek() == '^' {
				return h, warns, fail(CodeE110, at(sc.i),
					"'^^^' is not a token: carets are not a repetition count")
			}
			sawCaret = true

			valPos := sc.i
			if value := sc.readUntilAny(inlineStopChars); value != "" {
				pv, err := parsePropagateValue(value, at(valPos))
				if err != nil {
					return h, warns, err
				}
				h.Inline.Propagate = &pv
			}
			// "^^" exists to assert "all", so an explicit +N that disagrees is
			// an error rather than a silent override.
			if h.Inline.depthFrom == depthFromPlus {
				if !h.Inline.Depth.IsAll() {
					return h, warns, fail(CodeE113, at(sigilPos),
						"'^^' asserts a depth of all but '+%s' already set it", h.Inline.Depth)
				}
				warns = append(warns, warn(CodeW110, at(sigilPos),
					"'^^' redundantly restates a depth of all"))
			}
			all := DepthAll
			h.Inline.Depth = &all
			h.Inline.depthFrom = depthFromDoubleCaret
			continue
		}

		if sigil == '%' && doubled {
			sc.next()
			if !sc.eof() && sc.peek() == '%' {
				return h, warns, fail(CodeE110, at(sc.i),
					"'%%%' is not a token: percent signs are not a repetition count")
			}
			if h.Inline.PropagateChannel != nil {
				return h, warns, fail(CodeE110, at(sigilPos), "duplicate '%%' directive")
			}
			valPos := sc.i
			value := sc.readUntilAny(inlineStopChars)
			if value == "" {
				return h, warns, fail(CodeE111, at(valPos),
					"'%%' requires a channel: a channel with no name carries no default")
			}
			cv, err := p.parseChannelValue(value, at(valPos), true)
			if err != nil {
				return h, warns, err
			}
			h.Inline.PropagateChannel = &cv
			// "%%" is itself the opt-in and supplies a depth of 1, but only if
			// no explicit ++N has been seen; a later one still overrides it.
			if h.Inline.channelDepthFrom == depthUnset {
				one := Depth(1)
				h.Inline.ChannelDepth = &one
				h.Inline.channelDepthFrom = depthFromDoubleSigil
			}
			continue
		}

		if sigil == '+' && doubled {
			sc.next()
			if !sc.eof() && sc.peek() == '+' {
				return h, warns, fail(CodeE110, at(sc.i),
					"'+++' is not a token: pluses are not a repetition count")
			}
			if h.Inline.channelDepthFrom == depthFromDoublePlus {
				return h, warns, fail(CodeE110, at(sigilPos), "duplicate '++' directive")
			}
			valPos := sc.i
			value := sc.readUntilAny(inlineStopChars)
			if value == "" {
				return h, warns, fail(CodeE111, at(valPos),
					"'++' requires a depth: it carries no default")
			}
			dv, err := parseDepthValue(value, at(valPos))
			if err != nil {
				return h, warns, err
			}
			// An explicit ++N wins over the 1 that "%%" implies, silently.
			h.Inline.ChannelDepth = &dv
			h.Inline.channelDepthFrom = depthFromDoublePlus
			continue
		}

		valPos := sc.i
		value := sc.readUntilAny(inlineStopChars)
		// An empty value is legal only after a caret, whose value is a bump and
		// bumps have a default. A channel with no name and a depth with no
		// number carry nothing worth guessing (§5.3).
		if value == "" && sigil != '^' {
			return h, warns, fail(CodeE111, at(valPos),
				"empty value after '%s'", string(sigil))
		}

		switch sigil {
		case '^':
			if sawCaret {
				return h, warns, fail(CodeE110, at(sigilPos),
					"duplicate propagation sigil: '^' and '^^' are the same sigil")
			}
			sawCaret = true
			if value != "" {
				pv, err := parsePropagateValue(value, at(valPos))
				if err != nil {
					return h, warns, err
				}
				h.Inline.Propagate = &pv
			}
			// "^" implies depth 1 only in the absence of an explicit +N; a
			// later +N still supplies one and wins, without diagnostic (§8.3).
			if h.Inline.depthFrom == depthUnset {
				one := Depth(1)
				h.Inline.Depth = &one
				h.Inline.depthFrom = depthFromCaret
			}

		case '+':
			dv, err := parseDepthValue(value, at(valPos))
			if err != nil {
				return h, warns, err
			}
			switch h.Inline.depthFrom {
			case depthFromDoubleCaret:
				if !dv.IsAll() {
					return h, warns, fail(CodeE113, at(sigilPos),
						"'+%s' contradicts the depth of all asserted by '^^'", value)
				}
				warns = append(warns, warn(CodeW110, at(sigilPos),
					"'+%s' redundantly restates the depth implied by '^^'", value))
				continue
			case depthFromPlus:
				return h, warns, fail(CodeE110, at(sigilPos), "duplicate '+' directive")
			}
			// Unset, or implied by "^": the explicit depth supplies the value.
			h.Inline.Depth = &dv
			h.Inline.depthFrom = depthFromPlus

		case '%':
			if h.Inline.Channel != nil {
				return h, warns, fail(CodeE110, at(sigilPos), "duplicate '%' directive")
			}
			cv, err := p.parseChannelValue(value, at(valPos), false)
			if err != nil {
				return h, warns, err
			}
			h.Inline.Channel = &cv
		}
	}

	// --- 4. breaking marker -----------------------------------------------
	if sc.accept('!') {
		h.Breaking = true
	}

	// --- 5. ": " and description -------------------------------------------
	//
	// A second scope-set is a parenthesis problem, not a separator problem, so
	// "feat(a)(b): x" is E103 rather than the generic E120 (§20.3).
	if !sc.eof() && sc.peek() == '(' {
		return h, warns, fail(CodeE103, at(sc.i),
			"a header carries at most one scope-set")
	}
	if !sc.accept(':') {
		if sc.eof() {
			return h, warns, fail(CodeE120, at(sc.i), "missing ': ' before the description")
		}
		return h, warns, fail(CodeE120, at(sc.i),
			"expected ':' but found %q", string(sc.peek()))
	}
	if sc.eof() {
		// Trailing whitespace has already been stripped (§4.1), so "feat: "
		// arrives here as "feat:", an empty description rather than a bad separator.
		return h, warns, fail(CodeE121, at(sc.i), "empty description")
	}
	if !sc.accept(' ') {
		// Lenient mode accepts a *missing* space (§5.5).
		if !p.cfg.Lenient {
			return h, warns, fail(CodeE120, at(sc.i), "':' must be followed by exactly one space")
		}
		warns = append(warns, warn(CodeW121, at(sc.i),
			"missing space after ':' accepted under lenient mode"))
	} else if !sc.eof() && sc.peek() == ' ' {
		// Two or more spaces stay E120 even under lenient mode, because the
		// intended description cannot be recovered from them (§5.5).
		return h, warns, fail(CodeE120, at(sc.i),
			"':' must be followed by exactly one space")
	}

	descPos := sc.i
	h.Description = sc.rest()
	if h.Description == "" {
		return h, warns, fail(CodeE121, at(descPos), "empty description")
	}
	if p.cfg.MaxDescriptionLength > 0 {
		if n := utf8.RuneCountInString(h.Description); n > p.cfg.MaxDescriptionLength {
			warns = append(warns, warn(CodeW120, at(descPos),
				"description is %d characters, limit is %d", n, p.cfg.MaxDescriptionLength))
		}
	}
	return h, warns, nil
}

// parsePropagateValue matches a Propagate value byte-for-byte; abbreviations
// are E111 (§5.3).
func parsePropagateValue(v string, pos Position) (Propagate, error) {
	if p, ok := ParsePropagate(v); ok {
		return p, nil
	}
	return "", fail(CodeE111, pos,
		"unknown propagation value %q: expected none, patch, minor, major or inherit", v)
}

// parseDepthValue implements the depth table lookup plus digit loop of §20.3.
//
// The digit run is unbounded in length and saturates at 1024 rather than being
// rejected, because no dependency graph is deeper: "+20000" is "all", not an
// error (vectors 14r1-14r3). Leading zeros are rejected, though; "0" alone is
// the only depth that may start with one (14r4, 14r5).
func parseDepthValue(v string, pos Position) (Depth, error) {
	switch v {
	case "*", "all":
		return DepthAll, nil
	case "direct":
		return Depth(1), nil
	case "":
		return 0, fail(CodeE111, pos, "empty depth value")
	}
	if v[0] == '0' && len(v) > 1 {
		return 0, fail(CodeE111, pos,
			"depth %q has a leading zero: only %q may start with one", v, "0")
	}
	for i := 0; i < len(v); i++ {
		if !isDigit(v[i]) {
			return 0, fail(CodeE111, pos,
				"unknown depth value %q: expected a non-negative integer, direct, all or *", v)
		}
	}
	n := 0
	for i := 0; i < len(v); i++ {
		n = n*10 + int(v[i]-'0')
		if n > depthSaturation {
			return DepthAll, nil
		}
	}
	return Depth(n), nil
}

// parseChannelValue implements the channel-value grammar of §11.2:
//
//	channel-value = [ from ">" ] to
//
// allowWords admits the two whole-value words "inherit" and "none", which
// Propagate-Channel accepts and Channel does not. They are values, not channel
// names, so they may never appear on either side of a transition.
//
// ">" cannot occur in a channel name, so the split is unambiguous and needs no
// lookahead; readUntilAny does not stop at it, so the whole transition arrives
// as one value.
func (p *Parser) parseChannelValue(v string, pos Position, allowWords bool) (ChannelValue, error) {
	if allowWords && (v == ChannelInherit || v == ChannelNone) {
		return ChannelValue{Word: v}, nil
	}
	gt := strings.IndexByte(v, '>')
	if gt < 0 {
		to, err := p.parseChannelSide(v, pos, false)
		if err != nil {
			return ChannelValue{}, err
		}
		return ChannelValue{To: to}, nil
	}
	if strings.IndexByte(v[gt+1:], '>') >= 0 {
		return ChannelValue{}, fail(CodeE111, pos,
			"channel value %q has more than one '>': a transition has two sides", v)
	}
	from, err := p.parseChannelSide(v[:gt], pos, true)
	if err != nil {
		return ChannelValue{}, err
	}
	to, err := p.parseChannelSide(v[gt+1:], pos, false)
	if err != nil {
		return ChannelValue{}, err
	}
	return ChannelValue{From: from, To: to}, nil
}

// parseChannelSide validates one side of a channel value (§11.2). Both sides
// are validated in full, so "%%beta>Latest" is E181 on the right and
// "%%Beta>stable" is E181 on the left.
func (p *Parser) parseChannelSide(v string, pos Position, asFrom bool) (string, error) {
	switch {
	case v == "":
		return "", fail(CodeE111, pos, "empty side in a channel transition")
	case v == ChannelAnyPrerelease:
		if asFrom {
			return ChannelAnyPrerelease, nil
		}
		// "move them to some prerelease or other" is not a releasable
		// instruction.
		return "", fail(CodeE111, pos, "%q is a from-value only", ChannelAnyPrerelease)
	case v == ChannelInherit || v == ChannelNone:
		return "", fail(CodeE111, pos,
			"%q is a value, not a channel: it cannot be a side of a transition", v)
	case v == ChannelStable:
		return ChannelStable, nil
	case v == "latest":
		return "", fail(CodeE180, pos, `channel name "latest" is reserved`)
	}
	if !isLower(v[0]) {
		return "", fail(CodeE181, pos,
			"channel name %q must begin with a lowercase letter", v)
	}
	for i := 0; i < len(v); i++ {
		if !isChannelChar(v[i]) {
			return "", fail(CodeE181, pos,
				"illegal character %q in channel name %q", string(v[i]), v)
		}
	}
	if len(v) > 32 {
		return "", fail(CodeE181, pos, "channel name %q exceeds 32 characters", v)
	}
	if p.allowedChannels != nil {
		// channels.allowed restricts both sides of every value (§11.2).
		found := false
		for _, ch := range p.allowedChannels {
			if ch == v {
				found = true
				break
			}
		}
		if !found {
			return "", fail(CodeE181, pos, "channel %q is not in the allowed list", v)
		}
	}
	return v, nil
}
