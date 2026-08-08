package ccme

import (
	"fmt"
	"strings"
)

// Diagnostic codes from §16 (Diagnostics registry).
//
// Codes that require a workspace, a dependency graph or git history to detect
// are deliberately absent: this package parses messages, it does not compute
// releases. See the package documentation for the exact list.
const (
	CodeE001 = "E001" // message is not valid UTF-8
	CodeE002 = "E002" // message is empty
	CodeE100 = "E100" // unit header does not match the grammar
	CodeE101 = "E101" // type contains uppercase or illegal characters
	CodeE102 = "E102" // whitespace inside a scope-set other than after a comma
	CodeE103 = "E103" // unbalanced or nested parentheses
	CodeE104 = "E104" // empty scope-set
	CodeE110 = "E110" // duplicate inline directive sigil
	CodeE111 = "E111" // unknown inline directive value, or an illegally empty value
	CodeE112 = "E112" // inline and footer set the same key to different values
	CodeE113 = "E113" // "^^" combined with an explicit "+N" where N is not all
	CodeE120 = "E120" // missing or malformed ": " separator
	CodeE121 = "E121" // empty description
	CodeE140 = "E140" // unknown type under strictTypes
	CodeE141 = "E141" // release unit with "!"
	CodeE151 = "E151" // footer value is not valid for its key
	CodeE154 = "E154" // exact Release-As on a multi-package scope-set
	CodeE170 = "E170" // cancel unit with "!"
	CodeE171 = "E171" // cancel unit with directives or footers
	CodeE158 = "E158" // a limits.* cap was exceeded; message-scoped
	CodeE180 = "E180" // reserved channel name "latest"
	CodeE181 = "E181" // channel name contains uppercase or illegal characters

	CodeW001 = "W001" // empty unit discarded
	CodeW101 = "W101" // type lowercased under lenient mode
	CodeW110 = "W110" // redundant restatement of a directive
	CodeW112 = "W112" // footer overrode inline under lenient mode
	CodeW120 = "W120" // description exceeds maxDescriptionLength
	CodeW121 = "W121" // missing space after ": " accepted under lenient mode
	CodeW132 = "W132" // multi-unit commit with unscoped units
	CodeW133 = "W133" // package both included and excluded
	CodeW140 = "W140" // unknown type mapped to none
	CodeW141 = "W141" // release unit with no directives
	CodeW150 = "W150" // unknown footer key ignored
	CodeW151 = "W151" // trailing paragraph nearly footer-shaped but treated as body
	CodeW152 = "W152" // redundant no-op propagation pairing
	CodeW155 = "W155" // footer key matches BREAKING CHANGE only case-insensitively
	CodeW156 = "W156" // a BREAKING CHANGE line sits in the body, not the footer block
	CodeW157 = "W157" // BREAKING CHANGE with an empty value
	CodeW201 = "W201" // a propagation value was supplied while its axis's depth is 0
	CodeW207 = "W207" // a channel transition whose from equals its to; inert
)

// silentFailureCodes backs SilentFailureCodes; an array so the policy set
// cannot be mutated even from inside the package.
var silentFailureCodes = [...]string{CodeW155, CodeW156}

// SilentFailureCodes returns the warnings §16 singles out as
// silent-wrong-answer warnings rather than style notes: each one means the
// message says something different from what its author meant, with no error
// to stop it. The returned slice is a copy; callers may modify it freely.
//
// Commit-lint implementations SHOULD reject a commit carrying any of them.
// W172 belongs to this set too but requires git history, so it is not emitted
// by this package.
func SilentFailureCodes() []string {
	out := make([]string, len(silentFailureCodes))
	copy(out, silentFailureCodes[:])
	return out
}

// Severity classifies a Diagnostic. Errors make the offending unit's
// contribution undefined; warnings never block a release (§16).
type Severity int

const (
	// SeverityWarning never invalidates a unit.
	SeverityWarning Severity = iota
	// SeverityError invalidates the unit it is attached to. Other units in the
	// same message still apply.
	SeverityError
)

// String implements fmt.Stringer.
func (s Severity) String() string {
	switch s {
	case SeverityError:
		return "error"
	case SeverityWarning:
		return "warning"
	default:
		return "unknown"
	}
}

// Position is a location inside the normalised message. Both fields are
// 1-based; Column counts bytes, which is what a caret-pointing renderer needs.
type Position struct {
	Line   int
	Column int
}

// String implements fmt.Stringer, rendering "line:column". Like every other
// String in the package it avoids fmt, so rendering diagnostics stays off the
// allocator's hot path.
func (p Position) String() string { return itoa(p.Line) + ":" + itoa(p.Column) }

// shift returns the position n bytes to the right on the same line.
func (p Position) shift(n int) Position {
	return Position{Line: p.Line, Column: p.Column + n}
}

// Diagnostic is a single entry produced by a parse. Every diagnostic carries
// the exact position at which it was raised, so callers can render a caret
// under the offending character (§20.7).
type Diagnostic struct {
	Code      string
	Severity  Severity
	Message   string
	Position  Position
	UnitIndex int // index into Result.Units, or -1 for message-level diagnostics
}

// String implements fmt.Stringer.
func (d Diagnostic) String() string {
	return fmt.Sprintf("%s: %s %s: %s", d.Position, d.Severity, d.Code, d.Message)
}

// IsError reports whether the diagnostic invalidates its unit.
func (d Diagnostic) IsError() bool { return d.Severity == SeverityError }

// fatalError carries a Diagnostic through Go's error channel. It is internal:
// callers see diagnostics on the Result and a *ParseError from Parse.
type fatalError struct{ diag Diagnostic }

func (e *fatalError) Error() string { return e.diag.String() }

// formatMessage renders a diagnostic message, skipping the formatting
// machinery for the many call sites whose format string is already complete.
func formatMessage(format string, args []any) string {
	if len(args) == 0 {
		return format
	}
	return fmt.Sprintf(format, args...)
}

// fail builds an error-severity diagnostic wrapped as an error.
func fail(code string, pos Position, format string, args ...any) error {
	return &fatalError{diag: Diagnostic{
		Code:      code,
		Severity:  SeverityError,
		Message:   formatMessage(format, args),
		Position:  pos,
		UnitIndex: -1,
	}}
}

// warn builds a warning-severity diagnostic.
func warn(code string, pos Position, format string, args ...any) Diagnostic {
	return Diagnostic{
		Code:      code,
		Severity:  SeverityWarning,
		Message:   formatMessage(format, args),
		Position:  pos,
		UnitIndex: -1,
	}
}

// asDiagnostic converts an error produced by this package back into a
// Diagnostic. It panics on foreign errors, which cannot occur: every error
// funnelled here was built by fail. The panic is deliberate — the silent
// alternative would fabricate a diagnostic with a zero Position, violating
// the Line >= 1 invariant every real diagnostic holds.
func asDiagnostic(err error) Diagnostic {
	fe, ok := err.(*fatalError)
	if !ok {
		panic("ccme: internal error: foreign error reached asDiagnostic: " + err.Error())
	}
	return fe.diag
}

// ParseError aggregates every error-severity diagnostic produced by a parse.
// A non-nil ParseError does not mean the Result is unusable: units without
// errors are still populated and still apply (§16).
type ParseError struct {
	Diagnostics []Diagnostic
}

// Error implements error.
func (e *ParseError) Error() string {
	if len(e.Diagnostics) == 0 {
		return "ccme: parse failed"
	}
	if len(e.Diagnostics) == 1 {
		return "ccme: " + e.Diagnostics[0].String()
	}
	parts := make([]string, 0, len(e.Diagnostics))
	for _, d := range e.Diagnostics {
		parts = append(parts, d.String())
	}
	return fmt.Sprintf("ccme: %d errors: %s", len(parts), strings.Join(parts, "; "))
}

// Codes returns the diagnostic codes in order, which is convenient in tests.
func (e *ParseError) Codes() []string {
	codes := make([]string, 0, len(e.Diagnostics))
	for _, d := range e.Diagnostics {
		codes = append(codes, d.Code)
	}
	return codes
}
