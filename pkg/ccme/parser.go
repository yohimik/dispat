// Package ccme implements a parser for Conventional Commits: Monorepo
// Extension (CCME) 2.0.0, a strict superset of Conventional Commits 1.0.0.
// The 2.0.0 specification keeps the 1.0.0 message grammar unchanged; its
// release-planning algorithm and workspace obligations belong to the engine,
// not this parser.
//
// The parser is a single left-to-right index scan with one byte of lookahead:
// no regular-expression engine, no backtracking, no recursion, and therefore
// no input that can trigger superlinear behaviour (§20). Every diagnostic is
// raised at a known position, so a caller can render a caret under the
// offending character.
//
// # Usage
//
//	p := ccme.DefaultParser()
//	res, err := p.Parse(message)
//	if err != nil {
//		// err lists the error-severity diagnostics; res is still populated
//		// with the units that parsed cleanly.
//	}
//	for _, u := range res.ValidUnits() {
//		fmt.Println(u.Header.Type, u.Scopes(), u.Bump)
//	}
//
// All configuration lives in a single Config struct whose zero value is the
// specification default, so only the fields you care about need setting:
//
//	p, err := ccme.NewParser(ccme.Config{
//		Separator:   "%%%",
//		StrictTypes: true,
//		Propagation: ccme.PropagationConfig{Depth: ccme.DepthAll},
//	})
//
// A Parser is immutable after construction and safe for concurrent use, so a
// single package-level parser can serve every goroutine in a service.
//
// # Propagation has two axes
//
// §5.3 gives propagation two independent axes, each with a value and a depth,
// and each expressible inline or as a footer:
//
//	axis     value        depth   footers
//	bump     "^", "^^"    "+N"    Propagate, Propagate-Depth, Propagate-Scope
//	channel  "%%"         "++N"   Propagate-Channel, Propagate-Channel-Depth,
//	                              Propagate-Channel-Scope
//
// "%" is on neither axis: it sets the unit's own channel. Both depths default
// to 0, so a unit reaches nobody until it opts in. The doubled sigils are
// fixed two-character tokens rather than repetition counts, so "^^^", "%%%"
// and "+++" are all E110.
//
// A channel value may also be a transition, "from>to", where "*" on the left
// matches any prerelease (§11.2).
//
// # Performance
//
// The package is built for sweeping large histories. Parsing is O(n) in
// message length with no backtracking, and the hot path is allocation-lean:
// Header.Raw, Unit.Raw, Unit.Body, Header.Description and every scope term are
// substrings of the input rather than copies, and a message that is already
// normalised is not rewritten at all. A clean single-unit message costs a
// handful of small allocations, none of them proportional to the body size.
//
// Two consequences are worth knowing. Result and its Units retain a reference
// to the message string, so holding a Result alive holds the message alive.
// And Directives.Kinds always aliases the parser's configuration, since §8.4 has no
// per-unit override, so it must be treated as read-only.
//
// # Scope
//
// This package parses messages. It does not read git, load a workspace, walk a
// dependency graph, or compute versions, so the diagnostics that require any
// of those are out of scope and are never emitted: E001 is raised only for
// invalid UTF-8 in the message itself, and E130, E153, E156, E157, E182, E185,
// E191, E195, E196, E210, E211, E212, E213, W130, W131, W134, W135, W153,
// W154, W158, W160, W170, W171, W172, W185, W186, W190, W192, W209, W210,
// W211, W212, W213 and W215 belong to the release engine. E154 is enforced for the
// cases that are decidable from the message alone, and the correction footers
// of §7.4 are shape-validated here (E151, E173, W214) while their targets are
// resolved by the engine (§13.4b).
//
// The parser bounds of §14.1 are always enforced, because a commit message is
// untrusted input (§18): exceeding limits.unitsPerMessage,
// limits.scopeTermsPerUnit or limits.messageBytes is E158, which is
// message-scoped: the commit contributes nothing.
//
// The hold machinery of §8.6.1 is split the same way: this package parses and
// classifies Release-As values, all three of which are package-level since
// §8.6 has no bump form, but resolving which directive wins over a window,
// and what that means for a release plan, belongs to the engine.
//
// Section references in the documentation are to the CCME specification.
package ccme

import (
	"strings"
	"unicode/utf8"
)

// Parser parses CCME commit messages. Construct one with NewParser,
// MustNewParser or DefaultParser; the zero value is not usable.
//
// A Parser holds no mutable state and is safe for concurrent use.
type Parser struct {
	cfg Config
	// allowedChannels is nil when channel names are unrestricted.
	allowedChannels []string
	messageTrailers []string
	issueTrailers   []string
	// escapedSep is "\" + the configured separator, precomputed so splitUnits
	// does not concatenate it once per parsed message.
	escapedSep string
}

// NewParser builds a Parser from a single configuration struct. Every
// zero-valued field is filled in from the specification defaults (§14), so
// NewParser(ccme.Config{}) is the fully default parser and
// NewParser(ccme.Config{Lenient: true}) changes exactly one thing.
//
// The configuration is copied, so the caller may reuse or mutate the struct
// afterwards. An invalid configuration is returned as an error.
func NewParser(cfg Config) (*Parser, error) {
	full := cfg.withDefaults()
	if err := full.Validate(); err != nil {
		return nil, err
	}
	return &Parser{
		cfg:             full,
		allowedChannels: full.AllowedChannels,
		messageTrailers: full.MessageLevelTrailers,
		issueTrailers:   full.IssueTrailers,
		escapedSep:      "\\" + full.Separator,
	}, nil
}

// MustNewParser is NewParser for configurations known to be valid, such as
// package-level defaults. It panics on an invalid configuration.
func MustNewParser(cfg Config) *Parser {
	p, err := NewParser(cfg)
	if err != nil {
		panic(err)
	}
	return p
}

// DefaultParser returns a parser configured entirely from the specification
// defaults. It is shorthand for MustNewParser(Config{}).
func DefaultParser() *Parser { return MustNewParser(Config{}) }

// Config returns a deep copy of the parser's effective configuration, with
// every default already filled in.
func (p *Parser) Config() Config { return p.cfg.Clone() }

// Result is the outcome of a parse.
//
// A Result keeps a reference to the normalised message: Units, bodies and
// descriptions are substrings of it, so retaining a Result retains the
// message.
type Result struct {
	// Message is the normalised message (§4.1).
	Message string
	// Units are every unit in written order, including units that failed to
	// parse. Check Unit.Valid, or use ValidUnits.
	Units []*Unit
	// Diagnostics are every diagnostic raised.
	//
	// The order is deterministic and is a pure function of the input, as §17.2
	// requires: unit-level diagnostics are grouped by unit in unit order, and
	// within a unit they follow the order the parser raises them. Nothing here
	// depends on map-iteration order.
	Diagnostics []Diagnostic
}

// ValidUnits returns the units with no error-severity diagnostic.
//
// When every unit is valid, the overwhelmingly common case, it returns
// Units itself rather than a copy, so treat the result as read-only.
func (r *Result) ValidUnits() []*Unit {
	n := 0
	for _, u := range r.Units {
		if u.Valid {
			n++
		}
	}
	if n == len(r.Units) {
		return r.Units
	}
	out := make([]*Unit, 0, n)
	for _, u := range r.Units {
		if u.Valid {
			out = append(out, u)
		}
	}
	return out
}

// Errors returns the error-severity diagnostics.
func (r *Result) Errors() []Diagnostic { return r.filter(SeverityError) }

// Warnings returns the warning-severity diagnostics.
func (r *Result) Warnings() []Diagnostic { return r.filter(SeverityWarning) }

func (r *Result) filter(s Severity) []Diagnostic {
	n := 0
	for _, d := range r.Diagnostics {
		if d.Severity == s {
			n++
		}
	}
	if n == 0 {
		return nil
	}
	out := make([]Diagnostic, 0, n)
	for _, d := range r.Diagnostics {
		if d.Severity == s {
			out = append(out, d)
		}
	}
	return out
}

// HasErrors reports whether any error-severity diagnostic was raised.
func (r *Result) HasErrors() bool {
	for _, d := range r.Diagnostics {
		if d.Severity == SeverityError {
			return true
		}
	}
	return false
}

// Codes returns every diagnostic code in order, which is convenient in tests.
func (r *Result) Codes() []string {
	if len(r.Diagnostics) == 0 {
		return nil
	}
	out := make([]string, 0, len(r.Diagnostics))
	for _, d := range r.Diagnostics {
		out = append(out, d.Code)
	}
	return out
}

// Bump returns the highest direct bump over the valid units. It is the
// message's contribution before scope resolution and propagation (§13.6).
func (r *Result) Bump() Bump {
	b := BumpNone
	for _, u := range r.Units {
		if u.Valid {
			b = MaxBump(b, u.Bump)
		}
	}
	return b
}

// err builds the aggregate error, or nil when nothing failed. It allocates
// only when there is something to report.
func (r *Result) err() error {
	errs := r.filter(SeverityError)
	if errs == nil {
		return nil
	}
	return &ParseError{Diagnostics: errs}
}

// Parse parses a complete commit message: normalisation (§4.1), splitting into
// units (§4.2), then header, body, footers and semantics for each unit.
//
// The returned Result is always non-nil. A non-nil error means at least one
// error-severity diagnostic was raised; the units that parsed cleanly are
// still present and still apply, because an error invalidates only the
// offending unit (§16).
func (p *Parser) Parse(message string) (*Result, error) {
	if !utf8.ValidString(message) {
		return fatalResult("", CodeE001, "message is not valid UTF-8")
	}
	// §14.1: the length bound is checked before any work, because the whole
	// point is to bound the work (§18.3).
	if n := p.cfg.Limits.MessageBytes; n > 0 && len(message) > n {
		return fatalResult("", CodeE158,
			formatMessage("message is %d bytes, over the limit of %d", []any{len(message), n}))
	}

	normalised := Normalize(message)
	// Normalisation reduces a whitespace-only message to the empty string, so
	// this one comparison covers every form of §15.1 case 1.
	if normalised == "" {
		return fatalResult(normalised, CodeE002, "message is empty")
	}

	lines := splitLines(normalised)
	sources, dropped := splitUnits(normalised, lines, p.cfg.Separator, p.escapedSep)

	if n := p.cfg.Limits.UnitsPerMessage; n > 0 && len(sources) > n {
		return fatalResult(normalised, CodeE158,
			formatMessage("message has %d units, over the limit of %d",
				[]any{len(sources), n}))
	}

	var res *Result
	if len(sources) == 1 {
		// The overwhelmingly common shape: Result, Unit and the pointer slice
		// come out of one allocation.
		buf := new(parseBuf)
		buf.ptrs[0] = &buf.unit
		res = &buf.res
		res.Message = normalised
		res.Units = buf.ptrs[:]
		res.Diagnostics = dropped
		p.parseUnit(&buf.unit, 0, sources[0])
		res.Diagnostics = append(res.Diagnostics, buf.unit.Diagnostics...)
		p.checkMultiUnitScoping(res) // a no-op below two units, kept for symmetry
		res.propagateMessageScopedErrors()
		return res, res.err()
	}

	res = &Result{Message: normalised, Diagnostics: dropped}
	if n := len(sources); n > 0 {
		// One backing array for every unit rather than n separate allocations.
		backing := make([]Unit, n)
		res.Units = make([]*Unit, n)
		for i := range sources {
			u := &backing[i]
			p.parseUnit(u, i, sources[i])
			res.Units[i] = u
			res.Diagnostics = append(res.Diagnostics, u.Diagnostics...)
		}
	}

	p.checkMultiUnitScoping(res)
	res.propagateMessageScopedErrors()
	return res, res.err()
}

// propagateMessageScopedErrors applies the blast radius §16 assigns to E158.
// It is raised where it is detected, inside one unit's scope-set, but it is
// message-scoped, so once any unit trips it the whole commit contributes
// nothing.
func (r *Result) propagateMessageScopedErrors() {
	tripped := false
	for _, d := range r.Diagnostics {
		if d.Code == CodeE158 {
			tripped = true
			break
		}
	}
	if !tripped {
		return
	}
	for _, u := range r.Units {
		u.Valid = false
	}
}

// parseBuf packs the objects a single-unit parse needs into one allocation.
// Its fields are only ever reached through the returned Result, which keeps
// the whole buffer alive for exactly as long as the caller needs it.
type parseBuf struct {
	res   Result
	unit  Unit
	ptrs  [1]*Unit
	lines [1]string
}

// fatalResult builds the Result for a message that cannot be parsed at all.
func fatalResult(message, code, text string) (*Result, error) {
	res := &Result{
		Message: message,
		Diagnostics: []Diagnostic{{
			Code:      code,
			Severity:  SeverityError,
			Message:   text,
			Position:  Position{Line: 1, Column: 1},
			UnitIndex: -1,
		}},
	}
	return res, res.err()
}

// ParseSubject parses a single commit subject, the header line on its own.
// It is the narrow entry point for commit-lint style checks; the message is
// normalised first, and a subject containing a line break is rejected.
func (p *Parser) ParseSubject(subject string) (*Result, error) {
	if !utf8.ValidString(subject) {
		return fatalResult("", CodeE001, "subject is not valid UTF-8")
	}

	if n := p.cfg.Limits.MessageBytes; n > 0 && len(subject) > n {
		return fatalResult("", CodeE158,
			formatMessage("subject is %d bytes, over the limit of %d",
				[]any{len(subject), n}))
	}

	normalised := Normalize(subject)
	if normalised == "" {
		return fatalResult(normalised, CodeE002, "subject is empty")
	}
	if i := strings.IndexByte(normalised, '\n'); i >= 0 {
		res := &Result{
			Message: normalised,
			Diagnostics: []Diagnostic{{
				Code:      CodeE100,
				Severity:  SeverityError,
				Message:   "a subject must be a single line; use Parse for a full message",
				Position:  Position{Line: 1, Column: i + 1},
				UnitIndex: -1,
			}},
		}
		return res, res.err()
	}

	// A subject is one unit of one line, so everything fits in one allocation.
	buf := new(parseBuf)
	buf.ptrs[0] = &buf.unit
	buf.lines[0] = normalised
	res := &buf.res
	res.Message = normalised
	res.Units = buf.ptrs[:]
	p.parseUnit(&buf.unit, 0, unitSource{
		msg:       normalised,
		lines:     buf.lines[:],
		startLine: 1,
	})
	res.Diagnostics = buf.unit.Diagnostics
	res.propagateMessageScopedErrors()
	return res, res.err()
}

// checkMultiUnitScoping emits W132 when a commit has two or more units and
// fewer than all of them carry an explicit scope-set (§6.3).
func (p *Parser) checkMultiUnitScoping(res *Result) {
	if len(res.Units) < 2 {
		return
	}
	unscoped := 0
	for _, u := range res.Units {
		if !u.Header.HasScopeSet {
			unscoped++
		}
	}
	if unscoped == 0 {
		return
	}
	res.Diagnostics = append(res.Diagnostics, warn(CodeW132, Position{Line: 1, Column: 1},
		"%d of %d units carry no scope-set and therefore all resolve to the same derived set",
		unscoped, len(res.Units)))
}

// offsetDetached marks a unit whose text is not a contiguous substring of the
// message, which happens only when it contains an escaped separator (§4.2).
const offsetDetached = -1

// unitSource is a unit's lines together with where they sit in the message,
// which is what keeps diagnostic positions accurate and lets the unit's text
// be sliced rather than rebuilt.
type unitSource struct {
	msg       string   // the normalised message
	lines     []string // the unit's lines, blank edges already trimmed
	offset    int      // byte offset of lines[0] in msg, or offsetDetached
	startLine int      // 1-based line number of lines[0]
}

// text returns lines[a:b] joined with LF. When the unit is contiguous in the
// message, which it always is unless it contains an escaped separator, the
// result is a substring and costs nothing.
func (src unitSource) text(a, b int) string {
	if a >= b {
		return ""
	}
	if src.offset == offsetDetached {
		return strings.Join(src.lines[a:b], "\n")
	}
	start := src.offset + lineOffset(src.lines, a)
	return src.msg[start : start+lineSpan(src.lines[a:b])]
}

// trimmedText is text with blank lines dropped from both ends.
func (src unitSource) trimmedText(a, b int) string {
	for a < b && src.lines[a] == "" {
		a++
	}
	for b > a && src.lines[b-1] == "" {
		b--
	}
	return src.text(a, b)
}

// lineOffset returns the byte offset of lines[k] relative to lines[0].
func lineOffset(lines []string, k int) int {
	n := 0
	for i := 0; i < k; i++ {
		n += len(lines[i]) + 1
	}
	return n
}

// lineSpan returns the byte length of lines joined with LF.
func lineSpan(lines []string) int {
	if len(lines) == 0 {
		return 0
	}
	n := len(lines) - 1
	for _, l := range lines {
		n += len(l)
	}
	return n
}

// parseUnit implements §20.4: header, required blank line, then a final
// paragraph that is a footer block only if every one of its lines is a footer
// start or continuation. Only the last paragraph is ever examined, so the body
// is never split up.
func (p *Parser) parseUnit(u *Unit, index int, src unitSource) {
	lines := src.lines
	u.Index = index
	u.Start = Position{Line: src.startLine, Column: 1}
	u.Raw = src.text(0, len(lines))
	u.Valid = true

	header, warns, err := p.parseHeader(lines[0], u.Start)
	u.Header = header
	for _, w := range warns {
		w.UnitIndex = index
		u.Diagnostics = append(u.Diagnostics, w)
	}
	if err != nil {
		u.addError(asDiagnostic(err))
		return
	}

	// §4.4: a blank line is required between the header and anything else.
	const restStart = 2
	if len(lines) > 1 {
		if lines[1] != "" {
			u.addError(asDiagnostic(fail(CodeE100,
				Position{Line: src.startLine + 1, Column: 1},
				"a blank line is required between the header and the body")))
			return
		}
	}

	if len(lines) > restStart {
		// Blank edges are already trimmed, so the message ends on content.
		end := len(lines)
		para := end
		for para > restStart && lines[para-1] != "" {
			para--
		}
		last := lines[para:end]
		bodyEnd := end
		if isFooterBlock(last) {
			bodyEnd = para
			u.Body = src.trimmedText(restStart, para)
			u.Footers = p.parseFooters(last, Position{Line: src.startLine + para, Column: 1})
		} else {
			if nearlyFooterBlock(last) {
				d := warn(CodeW151, Position{Line: src.startLine + para, Column: 1},
					"the final paragraph starts like a footer block but is treated as body text")
				d.UnitIndex = index
				u.Diagnostics = append(u.Diagnostics, d)
			}
			u.Body = src.trimmedText(restStart, end)
		}
		u.checkStrandedBreakingChange(lines, restStart, bodyEnd, src.startLine)
		u.checkMiscasedBreakingChange(last, src.startLine+para)
	}

	p.applySemantics(u)
}

// checkStrandedBreakingChange emits W156 for a BREAKING CHANGE line sitting in
// the body rather than the footer block, where it has no effect. It is the
// second of the two silent failures §8.1.1 exists to surface: the author wrote
// a breaking change and the parser is about to ignore it.
func (u *Unit) checkStrandedBreakingChange(lines []string, from, to, startLine int) {
	for i := from; i < to; i++ {
		end := footerKeyEnd(lines[i])
		if end < 0 || !isBreakingKey(lines[i][:end]) {
			continue
		}
		d := warn(CodeW156, Position{Line: startLine + i, Column: 1},
			"%s in the body has no effect: only the unit's final paragraph is a footer block",
			FooterBreakingChange)
		d.UnitIndex = u.Index
		u.Diagnostics = append(u.Diagnostics, d)
	}
}

// checkMiscasedBreakingChange emits W155 for the spelling that is not a footer
// at all: "Breaking change: ..." halts the generic key loop at the space, so
// nothing downstream will ever see it (§20.5).
//
// A BREAKING CHANGE footer's value is free text that may span the following
// lines, so a line inside such a value is part of the breaking change already
// in force, not a failed attempt to declare one; the walk mirrors
// isFooterBlock's and skips those lines.
func (u *Unit) checkMiscasedBreakingChange(lines []string, startLine int) {
	prevBreaking := false
	for i, l := range lines {
		if end := footerKeyEnd(l); end >= 0 {
			prevBreaking = isBreakingKey(l[:end])
			continue
		}
		if prevBreaking {
			continue // free-form BREAKING CHANGE value, not a miscasing
		}
		if !miscasedBreakingPrefix(l) {
			continue
		}
		d := warn(CodeW155, Position{Line: startLine + i, Column: 1},
			"%q is not %q: case is significant, so this is NOT a breaking change and is not even a footer",
			l[:len(FooterBreakingChange)], FooterBreakingChange)
		d.UnitIndex = u.Index
		u.Diagnostics = append(u.Diagnostics, d)
	}
}
