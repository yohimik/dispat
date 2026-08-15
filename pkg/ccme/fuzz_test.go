package ccme

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// fuzzSeeds covers every structural feature of the grammar, so that `go test`
// alone (which runs the seed corpus without -fuzz) already exercises the
// invariants below across the whole surface.
var fuzzSeeds = []string{
	"",
	" ",
	"\n\n\n",
	utf8BOM + "feat: x",
	utf8BOM + utf8BOM,
	utf8BOM + utf8BOM + "feat: x",
	utf8BOM + utf8BOM + utf8BOM + "feat: x",
	"---",
	"---\n---",
	"feat(core): a\n---",
	"feat: x",
	"feat(core): x",
	"feat(@acme/core,@acme/cli): x",
	"feat(*,-docs-site): x",
	"feat(.,-ui): x",
	"feat(core)^minor+2: x",
	"feat(core)^^minor: x",
	"feat(core)^^: x",
	"feat(core)^^%beta!: x",
	"feat(core)^^^minor: x",
	"feat(core)^^minor+2: x",
	"cancel(*): reset release state",
	"release(cli)%stable: graduate",
	"Feat: x",
	"feat:x",
	"feat:  x",
	"feat:",
	"feat(): x",
	"feat(core: x",
	"feat(core ,cli): x",
	"feat(core)%latest: x",
	"feat(core)%" + strings.Repeat("b", 40) + ": x",
	"feat(core): a\nbody without a blank line",
	"feat(core): a\n\nbody",
	"feat(core): a\n\nbody\n\nBREAKING CHANGE: gone",
	"feat(core): a\n\nBREAKING CHANGE: multi\nline free text",
	"feat(core): a\n\nPropagate: minor\nPropagate-Depth: all\nChannel: beta",
	"feat(core): a\n\nPropagate-Scope: @acme/*,\n  -@acme/internal-*",
	"feat(core): a\n\nRelease-As: 1.2.3",
	"feat(core): a\n\nRelease-As: not-a-version",
	"feat(core): a\n\nCloses #12\nSigned-off-by: A <a%x>",
	"feat(core): a\n\nCloses: #12\nnot footer shaped",
	// §8.1.1: every BREAKING CHANGE spelling, cased and miscased.
	"BREAKING CHANGE: gone",
	"BREAKING-CHANGE: gone",
	"feat(core): a\n\nBREAKING CHANGE: gone",
	"feat(core): a\n\nBREAKING-CHANGE: gone",
	"feat(core): a\n\nBreaking change: gone",
	"feat(core): a\n\nbreaking-change: gone",
	"feat(core): a\n\nBREAKING CHANGE:",
	"feat(core): a\n\nBREAKING CHANGE: ",
	"feat(core): a\n\nBREAKING CHANGE: gone\n\ntrailing prose",
	"feat(core)!: a\n\nBREAKING CHANGE: why",
	"feat(a)(b): x",
	"feat(a,): x",
	": x",
	"breaking: x",
	"release(api): Release-As: 3.0.0",
	"release(api): hold\n\nRelease-As: none",
	"release(api): resume\n\nRelease-As: auto",
	"feat(core): a\n\n---\n\nfix(cli): b",
	"---\nfeat(core): a",
	"feat(core): a\n---\n---\nfix(cli): b",
	"docs(core): a\n\nbefore\n\n\\---\n\nafter",
	"feat(core): a\r\n\r\nbody\r\n",
	"feat(core): a  \n\nbody\t\n\n\n",
	"feat: ééé",
	"feat: " + strings.Repeat("x", 200),
	"----\n----",
	"\\---",
	":",
	"(",
	"^^",

	// The two-axis grammar of §5.3 / §8.3: both sigil pairs, both depths,
	// channel transitions, and the shapes that must be rejected.
	"feat(core)^: x",
	"feat(core)^^: x",
	"feat(core)^^^: x",
	"feat(core)++2: x",
	"feat(core)++: x",
	"feat(core)+++2: x",
	"feat(core)++1++2: x",
	"feat(core)%%beta: x",
	"feat(core)%%: x",
	"feat(core)%%%beta: x",
	"feat(core)%%inherit: x",
	"feat(core)%%none: x",
	"feat(core)%%beta++3: x",
	"feat(core)++3%%beta: x",
	"feat(core)^^minor%%beta++1: x",
	"feat(core)+2++1: x",
	"feat(core)+00: x",
	"feat(core)+007: x",
	"feat(core)+9999: x",
	"feat(core)++20000: x",
	"feat(core)%beta>rc: x",
	"feat(core)%%*>stable++*: x",
	"feat(core)%%beta>*: x",
	"feat(core)%%a>b>c: x",
	"feat(core)%>stable: x",
	"feat(core)%beta>beta: x",
	"feat(core)%%beta>inherit: x",
	"feat(core)%inherit: x",
	"feat(core): a\n\nPropagate: minor\nPropagate-Depth: 0",
	"feat(core): a\n\nPropagate-Channel: beta\nPropagate-Channel-Depth: 2",
	"feat(core): a\n\nPropagate-Channel-Scope: @acme/*, -@acme/x",
	"feat(core): a\n\nPropagate-Channel: *>stable",
	"fix(core): a\n\nEdits: 4f2a1c9",
	"fix(core): a\n\nEdits: 4f2a1c9#2\nDeletes: abcdef0",
	"chore(core): a\n\nDeletes: *",
	"fix(core): a\n\nEdits: *#1",
	"fix(core): a\n\nEdits: 4F2A1C9",
	"revert(core): a\n\nReverts: not-a-sha",
	"++",
	"%%",
	"%>",
	">",
}

// FuzzParse asserts that no input can panic the parser and that the
// documented invariants hold for whatever it returns.
func FuzzParse(f *testing.F) {
	for _, s := range fuzzSeeds {
		f.Add(s)
	}
	// A second parser exercises the lenient and strict paths, which have their
	// own error handling.
	strict := DefaultParser()
	lenient := MustNewParser(Config{Lenient: true, StrictTypes: true, MaxDescriptionLength: 20})

	f.Fuzz(func(t *testing.T, msg string) {
		for _, p := range []*Parser{strict, lenient} {
			res, err := p.Parse(msg)
			if res == nil {
				t.Fatal("Parse returned a nil Result")
			}
			checkResultInvariants(t, res, err)

			if !utf8.ValidString(msg) {
				continue
			}
			// E001 and E158 are decided on the raw bytes, before and
			// independently of normalisation, so a message rejected by either
			// has no normalised form to reparse.
			if hasAnyCode(res, CodeE001, CodeE158) {
				continue
			}
			// Otherwise Parse normalises first, and Normalize is idempotent, so
			// parsing the normalised form must be indistinguishable.
			again, errAgain := p.Parse(res.Message)
			if (err == nil) != (errAgain == nil) {
				t.Errorf("reparsing the normalised message changed the error: %v vs %v", err, errAgain)
			}
			if len(again.Units) != len(res.Units) {
				t.Errorf("reparsing the normalised message changed the unit count: %d vs %d",
					len(again.Units), len(res.Units))
			}
			if strings.Join(again.Codes(), ",") != strings.Join(res.Codes(), ",") {
				t.Errorf("reparsing the normalised message changed the diagnostics: %v vs %v",
					res.Codes(), again.Codes())
			}
		}
	})
}

// FuzzParseSubject covers the single-line entry point, which has its own
// allocation-packing path.
func FuzzParseSubject(f *testing.F) {
	for _, s := range fuzzSeeds {
		f.Add(s)
	}
	p := DefaultParser()
	f.Fuzz(func(t *testing.T, subject string) {
		res, err := p.ParseSubject(subject)
		if res == nil {
			t.Fatal("ParseSubject returned a nil Result")
		}
		checkResultInvariants(t, res, err)
		if len(res.Units) > 1 {
			t.Errorf("a subject produced %d units", len(res.Units))
		}
	})
}

// FuzzNormalize pins the contract between the fast-path predicate and the
// rewriter: normalising must reach a fixed point that the predicate then
// recognises as needing no work. A disagreement here would mean Parse and
// Normalize could see different text.
func FuzzNormalize(f *testing.F) {
	for _, s := range fuzzSeeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, msg string) {
		once := Normalize(msg)
		if twice := Normalize(once); twice != once {
			t.Fatalf("Normalize is not idempotent: %q -> %q -> %q", msg, once, twice)
		}
		if needsNormalizing(once) {
			t.Fatalf("needsNormalizing still reports work after normalising: %q -> %q", msg, once)
		}
		// The postconditions of §4.1, stated directly.
		if strings.HasPrefix(once, utf8BOM) {
			t.Errorf("BOM survived normalisation: %q", once)
		}
		if strings.ContainsRune(once, '\r') {
			t.Errorf("CR survived normalisation: %q", once)
		}
		for _, line := range strings.Split(once, "\n") {
			if line != strings.TrimRight(line, " \t") {
				t.Errorf("trailing whitespace survived on line %q of %q", line, once)
			}
		}
		if strings.HasSuffix(once, "\n") {
			t.Errorf("trailing blank line survived: %q", once)
		}
	})
}

// checkResultInvariants asserts everything the package documents about a
// Result, regardless of whether the input was valid.
func checkResultInvariants(t *testing.T, res *Result, err error) {
	t.Helper()

	if (err != nil) != res.HasErrors() {
		t.Errorf("err = %v but HasErrors() = %v", err, res.HasErrors())
	}
	if err != nil {
		pe, ok := err.(*ParseError)
		if !ok {
			t.Fatalf("error type = %T, want *ParseError", err)
		}
		if len(pe.Diagnostics) != len(res.Errors()) {
			t.Errorf("ParseError carries %d diagnostics, Result reports %d",
				len(pe.Diagnostics), len(res.Errors()))
		}
		if pe.Error() == "" {
			t.Errorf("ParseError.Error() is empty")
		}
	}
	if len(res.Errors())+len(res.Warnings()) != len(res.Diagnostics) {
		t.Errorf("errors + warnings != all diagnostics")
	}

	lineCount := strings.Count(res.Message, "\n") + 1
	for _, d := range res.Diagnostics {
		if d.Code == "" {
			t.Errorf("diagnostic with an empty code: %+v", d)
		}
		if d.Position.Line < 1 || d.Position.Column < 1 {
			t.Errorf("%s has a non-positive position %s", d.Code, d.Position)
		}
		if d.Position.Line > lineCount {
			t.Errorf("%s at line %d but the message has %d lines",
				d.Code, d.Position.Line, lineCount)
		}
		if d.UnitIndex < -1 || d.UnitIndex >= len(res.Units) {
			t.Errorf("%s has out-of-range UnitIndex %d (units: %d)",
				d.Code, d.UnitIndex, len(res.Units))
		}
		if d.String() == "" {
			t.Errorf("%s renders as an empty string", d.Code)
		}
	}

	// A unit is valid exactly when it carries no error diagnostic.
	for i, u := range res.Units {
		if u.Index != i {
			t.Errorf("unit %d reports Index %d", i, u.Index)
		}
		hasErr := false
		for _, d := range u.Diagnostics {
			if d.IsError() {
				hasErr = true
			}
			if d.UnitIndex != i {
				t.Errorf("unit %d holds a diagnostic tagged for unit %d", i, d.UnitIndex)
			}
		}
		if u.Valid == hasErr {
			t.Errorf("unit %d: Valid = %v but hasError = %v", i, u.Valid, hasErr)
		}
		if u.Start.Line < 1 || u.Start.Line > lineCount {
			t.Errorf("unit %d starts at line %d, message has %d lines", i, u.Start.Line, lineCount)
		}
		if !u.Valid {
			continue
		}
		if u.Header.Type == "" {
			t.Errorf("valid unit %d has an empty type", i)
		}
		if u.Header.Description == "" {
			t.Errorf("valid unit %d has an empty description", i)
		}
		if u.Bump < BumpNone || u.Bump > BumpMajor {
			t.Errorf("valid unit %d has an out-of-range bump %d", i, int(u.Bump))
		}
		if u.IsControl() && u.Bump != BumpNone {
			t.Errorf("control unit %d produced bump %s", i, u.Bump)
		}
		if u.Header.HasScopeSet != (len(u.Header.Scopes) > 0) {
			t.Errorf("unit %d: HasScopeSet = %v with %d terms",
				i, u.Header.HasScopeSet, len(u.Header.Scopes))
		}
	}

	// Zero-copy contract: everything a caller reads back is a window onto the
	// normalised message. The one exception is a unit containing an escaped
	// separator, which cannot be contiguous and is rebuilt.
	if strings.Contains(res.Message, "\\") {
		return
	}
	for i, u := range res.Units {
		mustBeSubstring(t, res.Message, u.Raw, i, "Raw")
		mustBeSubstring(t, res.Message, u.Body, i, "Body")
		mustBeSubstring(t, res.Message, u.Header.Raw, i, "Header.Raw")
		mustBeSubstring(t, res.Message, u.Header.Description, i, "Header.Description")
		for _, s := range u.Header.Scopes {
			mustBeSubstring(t, res.Message, s.Raw, i, "scope term")
		}
	}
}

// hasAnyCode reports whether the result carries any of the given codes.
func hasAnyCode(res *Result, codes ...string) bool {
	for _, d := range res.Diagnostics {
		for _, c := range codes {
			if d.Code == c {
				return true
			}
		}
	}
	return false
}

func mustBeSubstring(t *testing.T, haystack, needle string, unit int, what string) {
	t.Helper()
	if needle == "" {
		return
	}
	if !strings.Contains(haystack, needle) {
		t.Errorf("unit %d %s = %q is not a substring of the message", unit, what, needle)
	}
}
