package ccme

import (
	"strings"
	"testing"
)

// allDiagnosticCodes lists every code this package can emit. Adding a code
// constant without adding it here, and without adding an input that produces
// it to the table below, fails TestEveryDiagnosticCodeIsReachable.
//
// It is deliberately hand-maintained: the point is to force a conscious
// decision, and a test for the new behaviour, whenever the registry grows.
var allDiagnosticCodes = []string{
	CodeE001, CodeE002, CodeE100, CodeE101, CodeE102, CodeE103, CodeE104,
	CodeE110, CodeE111, CodeE112, CodeE113, CodeE120, CodeE121, CodeE140,
	CodeE141, CodeE151, CodeE154, CodeE158, CodeE170, CodeE171, CodeE180,
	CodeE181,
	CodeW001, CodeW101, CodeW110, CodeW112, CodeW120, CodeW132, CodeW133,
	CodeW140, CodeW141, CodeW150, CodeW151, CodeW152, CodeW155, CodeW156,
	CodeW157, CodeW121, CodeW201, CodeW207,
}

// diagnosticCase is a minimal input that provokes exactly one code, so the
// table doubles as documentation of what each code means in practice.
type diagnosticCase struct {
	code    string
	message string
	cfg     Config
	subject bool // use ParseSubject rather than Parse
}

var diagnosticCases = []diagnosticCase{
	// Errors (§16).
	{code: CodeE001, message: "feat: \xff\xfe"},
	{code: CodeE002, message: "   \n  \n"},
	{code: CodeE100, message: "feat(core): a\nbody with no blank line"},
	{code: CodeE101, message: "Feat: x"},
	{code: CodeE102, message: "feat(core ,cli): x"},
	{code: CodeE103, message: "feat(core: x"},
	{code: CodeE104, message: "feat(): x"},
	{code: CodeE110, message: "feat(core)^minor^patch: x"},
	{code: CodeE111, message: "feat(core)^med: x"},
	{code: CodeE112, message: "feat(core)^minor: a\n\nPropagate: major"},
	{code: CodeE113, message: "feat(core)^^minor+2: x"},
	{code: CodeE120, message: "feat:x"},
	{code: CodeE121, message: "feat:"},
	{code: CodeE140, message: "wibble(core): x", cfg: Config{StrictTypes: true}},
	{code: CodeE141, message: "release(cli)!: x"},
	{code: CodeE151, message: "feat(core): a\n\nPropagate: med"},
	{code: CodeE154, message: "release(core,cli): x\n\nRelease-As: 4.0.0"},
	{code: CodeE158, message: strings.Repeat("feat(core): a\n\n---\n\n", 40) + "fix(core): b",
		cfg: Config{Limits: Limits{UnitsPerMessage: 8}}},
	{code: CodeE170, message: "cancel(*)!: x"},
	{code: CodeE171, message: "cancel(core)^minor: x"},
	{code: CodeE180, message: "feat(core)@latest: x"},
	{code: CodeE181, message: "feat(core)@Beta: x"},

	// Warnings (§16).
	{code: CodeW001, message: "---\nfeat(core): a"},
	{code: CodeW101, message: "Feat: x", cfg: Config{Lenient: true}},
	{code: CodeW110, message: "feat(core)^^minor+*: x"},
	{code: CodeW112, message: "feat(core)^minor: a\n\nPropagate: major", cfg: Config{Lenient: true}},
	{code: CodeW120, message: "feat: " + strings.Repeat("x", 101)},
	{code: CodeW132, message: "feat(core): a\n\n---\n\nfix: b"},
	{code: CodeW133, message: "feat(api,-api): x"},
	{code: CodeW140, message: "wibble(core): x"},
	{code: CodeW141, message: "release(cli): nothing to do"},
	{code: CodeW150, message: "feat(core): a\n\nX-Team: infra"},
	{code: CodeW151, message: "fix(core): a\n\nCloses: #12\nnot footer shaped"},
	{code: CodeW152, message: "feat(core)^none+*: x"},
	// §8.3b: a value on an axis whose depth is 0 reaches nobody. W201 is the
	// more specific finding and suppresses W152 for that axis.
	{code: CodeW201, message: "feat(core): a\n\nPropagate: minor\nPropagate-Depth: 0"},
	{code: CodeW207, message: "feat(core)@beta>beta: x"},
	{code: CodeW121, message: "feat:x", cfg: Config{Lenient: true}},
	// The two silent failures of §8.1.1, plus the empty-value note.
	{code: CodeW155, message: "feat(core): a\n\nBreaking change: gone"},
	{code: CodeW156, message: "feat(core): a\n\nBREAKING CHANGE: gone\n\nordinary trailing prose"},
	{code: CodeW157, message: "feat(core): a\n\nBREAKING CHANGE:"},
}

// TestMiscasedBreakingChangeIsNotBreaking is the §8.1.1 guarantee in its most
// consequential form: neither miscasing may ship a major change as a minor
// one, and both must say so.
func TestMiscasedBreakingChangeIsNotBreaking(t *testing.T) {
	t.Parallel()

	p := DefaultParser()
	for _, key := range []string{
		"Breaking change", "breaking change", "BREAKING Change", // not footers at all
		"breaking-change", "Breaking-Change", "BREAKING-change", // footers, unknown key
	} {
		res, err := p.Parse("feat(core): a\n\n" + key + ": gone")
		if err != nil {
			t.Errorf("%q: %v", key, err)
			continue
		}
		u := res.Units[0]
		if u.Breaking || u.Bump != BumpMinor {
			t.Errorf("%q was treated as breaking: bump = %s", key, u.Bump)
		}
		if !hasCode(res, CodeW155) {
			t.Errorf("%q: missing W155 (codes: %s)", key, codesOf(res))
		}
	}

	// The two exact spellings are breaking.
	for _, key := range []string{"BREAKING CHANGE", "BREAKING-CHANGE"} {
		res, err := p.Parse("feat(core): a\n\n" + key + ": gone")
		if err != nil {
			t.Errorf("%q: %v", key, err)
			continue
		}
		u := res.Units[0]
		if !u.Breaking || u.Bump != BumpMajor {
			t.Errorf("%q was not treated as breaking: bump = %s", key, u.Bump)
		}
		if u.BreakingDescription() != "gone" {
			t.Errorf("%q: description = %q", key, u.BreakingDescription())
		}
		if hasCode(res, CodeW155) {
			t.Errorf("%q: unexpected W155", key)
		}
	}
}

// TestStrandedBreakingChange covers §8.1.1 rule 4: only the final paragraph is
// a footer block, so a BREAKING CHANGE anywhere else does nothing.
func TestStrandedBreakingChange(t *testing.T) {
	t.Parallel()

	p := DefaultParser()
	res, err := p.Parse("feat(core): a\n\nBREAKING CHANGE: gone\n\nsome trailing prose")
	if err != nil {
		t.Fatal(err)
	}
	u := res.Units[0]
	if u.Breaking || u.Bump != BumpMinor {
		t.Errorf("a body BREAKING CHANGE must have no effect, got bump %s", u.Bump)
	}
	if !hasCode(res, CodeW156) {
		t.Errorf("missing W156 (codes: %s)", codesOf(res))
	}

	// In the footer block it works, and must not also warn.
	res, err = p.Parse("feat(core): a\n\nsome body\n\nBREAKING CHANGE: gone")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Units[0].Breaking {
		t.Error("a footer-block BREAKING CHANGE must be breaking")
	}
	if hasCode(res, CodeW156) {
		t.Errorf("unexpected W156 (codes: %s)", codesOf(res))
	}
}

// TestEmptyBreakingChangeValue covers edge case 19e: the value is free text and
// may be empty, and §4.1 has already stripped the trailing space by then.
func TestEmptyBreakingChangeValue(t *testing.T) {
	t.Parallel()

	p := DefaultParser()
	for _, msg := range []string{
		"feat(core): a\n\nBREAKING CHANGE: ",
		"feat(core): a\n\nBREAKING CHANGE:",
		"feat(core): a\n\nBREAKING-CHANGE:",
	} {
		res, err := p.Parse(msg)
		if err != nil {
			t.Errorf("%q: %v", msg, err)
			continue
		}
		u := res.Units[0]
		if !u.Breaking || u.Bump != BumpMajor {
			t.Errorf("%q: an empty value is still breaking, got bump %s", msg, u.Bump)
		}
		if u.BreakingDescription() != "" {
			t.Errorf("%q: description = %q, want empty", msg, u.BreakingDescription())
		}
		if !hasCode(res, CodeW157) {
			t.Errorf("%q: missing W157 (codes: %s)", msg, codesOf(res))
		}
	}

	// Only BREAKING CHANGE may carry an empty value: an ordinary key followed
	// by a bare colon is body prose, not a footer.
	res, err := p.Parse("feat(core): a\n\nPropagate:")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Units[0].Footers) != 0 {
		t.Errorf("%q became a footer: %+v", "Propagate:", res.Units[0].Footers)
	}
}

// TestParserLimits covers §14.1: the parser bounds are always enforced, and
// exceeding one is message-scoped — the commit contributes nothing (§16).
func TestParserLimits(t *testing.T) {
	t.Parallel()

	t.Run("units per message", func(t *testing.T) {
		p := MustNewParser(Config{Limits: Limits{UnitsPerMessage: 3}})
		ok := strings.Repeat("feat(core): a\n\n---\n\n", 2) + "fix(core): b"
		if _, err := p.Parse(ok); err != nil {
			t.Errorf("3 units should be within the limit: %v", err)
		}
		over := strings.Repeat("feat(core): a\n\n---\n\n", 3) + "fix(core): b"
		res, err := p.Parse(over)
		if err == nil || firstError(res) != CodeE158 {
			t.Errorf("4 units = %v (%s), want E158", err, codesOf(res))
		}
		if len(res.ValidUnits()) != 0 {
			t.Errorf("E158 is message-scoped: no unit may survive, got %d",
				len(res.ValidUnits()))
		}
	})

	t.Run("message bytes", func(t *testing.T) {
		p := MustNewParser(Config{Limits: Limits{MessageBytes: 32}})
		res, err := p.Parse("feat(core): " + strings.Repeat("x", 64))
		if err == nil || firstError(res) != CodeE158 {
			t.Errorf("oversized message = %v (%s), want E158", err, codesOf(res))
		}
		if len(res.Units) != 0 {
			t.Errorf("an oversized message must produce no units, got %d", len(res.Units))
		}
		if _, err := p.Parse("feat(core): small"); err != nil {
			t.Errorf("a short message should pass: %v", err)
		}
	})

	t.Run("scope terms per unit", func(t *testing.T) {
		p := MustNewParser(Config{Limits: Limits{ScopeTermsPerUnit: 4}})
		terms := make([]string, 8)
		for i := range terms {
			terms[i] = "pkg"
		}
		res, err := p.Parse("feat(" + strings.Join(terms, ",") + "): x")
		if err == nil || firstError(res) != CodeE158 {
			t.Errorf("oversized scope-set = %v (%s), want E158", err, codesOf(res))
		}
		if len(res.ValidUnits()) != 0 {
			t.Errorf("E158 is message-scoped even when detected in one unit")
		}
		if _, err := p.Parse("feat(a,b,c,d): x"); err != nil {
			t.Errorf("4 terms should be within the limit: %v", err)
		}
	})

	t.Run("defaults are applied and disableable", func(t *testing.T) {
		got := DefaultParser().Config().Limits
		if got.UnitsPerMessage != DefaultUnitsPerMessage ||
			got.ScopeTermsPerUnit != DefaultScopeTermsPerUnit ||
			got.MessageBytes != DefaultMessageBytes {
			t.Errorf("default limits = %+v", got)
		}
		off := MustNewParser(Config{Limits: Limits{MessageBytes: -1, UnitsPerMessage: -1}})
		big := strings.Repeat("feat(core): a\n\n---\n\n", 200) + "fix(core): b"
		if _, err := off.Parse(big); err != nil {
			t.Errorf("negative limits should disable the bound: %v", err)
		}
	})
}

// TestReleaseAsHasNoBumpForm covers edge case 73i: the bump form was removed,
// and the diagnostic has to explain why rather than say "invalid value".
func TestReleaseAsHasNoBumpForm(t *testing.T) {
	t.Parallel()

	p := DefaultParser()
	for _, v := range []string{"patch", "minor", "major"} {
		res, err := p.Parse("chore(core): a\n\nRelease-As: " + v)
		if err == nil || firstError(res) != CodeE151 {
			t.Errorf("Release-As: %s = %v (%s), want E151", v, err, codesOf(res))
			continue
		}
		if !strings.Contains(res.Errors()[0].Message, "no bump form") {
			t.Errorf("Release-As: %s message = %q, want it to explain the removal",
				v, res.Errors()[0].Message)
		}
	}

	// The three surviving values leave the unit's own bump untouched (§13.6).
	for _, v := range []string{"4.0.0", "none", "auto"} {
		res, err := p.Parse("fix(core): a\n\nRelease-As: " + v)
		if err != nil {
			t.Errorf("Release-As: %s = %v", v, err)
			continue
		}
		if res.Units[0].Bump != BumpPatch {
			t.Errorf("Release-As: %s changed the bump to %s", v, res.Units[0].Bump)
		}
	}
}

// TestSilentFailureWarningsCannotBeSuppressed covers §14.2: configuration must
// not be able to turn off W155 or W156. The package offers no suppression
// mechanism at all, so this holds by construction — the test exists to make
// that a property somebody has to deliberately break rather than one that
// could be lost by adding a plausible-looking Config field.
func TestSilentFailureWarningsCannotBeSuppressed(t *testing.T) {
	t.Parallel()

	// The most permissive configuration the API allows.
	permissive := Config{
		Lenient:              true,
		MaxDescriptionLength: -1,
		Types:                map[string]Bump{},
		MessageLevelTrailers: []string{"BREAKING CHANGE", "BREAKING-CHANGE", "Breaking change"},
		IssueTrailers:        []string{"BREAKING CHANGE", "Breaking change"},
		Limits:               Limits{UnitsPerMessage: -1, ScopeTermsPerUnit: -1, MessageBytes: -1},
	}
	for _, cfg := range []Config{{}, permissive} {
		p := MustNewParser(cfg)

		res, _ := p.Parse("feat(core): a\n\nBreaking change: gone")
		if !hasCode(res, CodeW155) {
			t.Errorf("W155 suppressed by configuration (codes: %s)", codesOf(res))
		}
		res, _ = p.Parse("feat(core): a\n\nBREAKING CHANGE: gone\n\ntrailing prose")
		if !hasCode(res, CodeW156) {
			t.Errorf("W156 suppressed by configuration (codes: %s)", codesOf(res))
		}
	}
}

// TestDiagnosticsAreDeterministic covers §17.2: nothing in the output may
// depend on map-iteration order, so repeated parses of one message must
// produce byte-identical diagnostics in the same order.
func TestDiagnosticsAreDeterministic(t *testing.T) {
	t.Parallel()

	// A message that raises several diagnostics of different kinds at once.
	const msg = "wibble(api,-api)^none+*: a\n\n" +
		"BREAKING CHANGE: gone\n\ntrailing prose\n\n" +
		"---\n\n" +
		"fix: b\n\nBreaking change: x\nX-Team: infra"

	p := DefaultParser()
	first, _ := p.Parse(msg)
	want := make([]string, 0, len(first.Diagnostics))
	for _, d := range first.Diagnostics {
		want = append(want, d.String())
	}
	if len(want) < 4 {
		t.Fatalf("fixture raises only %d diagnostics: %s", len(want), codesOf(first))
	}

	for i := 0; i < 50; i++ {
		got := make([]string, 0, len(want))
		res, _ := p.Parse(msg)
		for _, d := range res.Diagnostics {
			got = append(got, d.String())
		}
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Fatalf("run %d differed:\n first: %v\n  this: %v", i, want, got)
		}
	}

	// Diagnostics are grouped by unit in unit order; message-level ones carry
	// UnitIndex -1 and are not interleaved with a unit's own.
	lastUnit := -2
	for _, d := range first.Diagnostics {
		if d.UnitIndex >= 0 && d.UnitIndex < lastUnit {
			t.Errorf("unit %d diagnostic appears after unit %d", d.UnitIndex, lastUnit)
		}
		if d.UnitIndex >= 0 {
			lastUnit = d.UnitIndex
		}
	}
}

// TestSilentFailureCodesAreWarnings documents the set commit-lint tooling is
// expected to reject even though the release engine tolerates it.
func TestSilentFailureCodesAreWarnings(t *testing.T) {
	t.Parallel()

	for _, c := range SilentFailureCodes() {
		if c[0] != 'W' {
			t.Errorf("%s is in SilentFailureCodes but is not a warning", c)
		}
		found := false
		for _, d := range allDiagnosticCodes {
			if d == c {
				found = true
			}
		}
		if !found {
			t.Errorf("%s is in SilentFailureCodes but not in allDiagnosticCodes", c)
		}
	}
}

// TestEveryDiagnosticCodeIsReachable is the release gate on §16: every code the
// package declares must be produced by a real input, and every code produced
// must be declared.
func TestEveryDiagnosticCodeIsReachable(t *testing.T) {
	t.Parallel()

	declared := make(map[string]bool, len(allDiagnosticCodes))
	for _, c := range allDiagnosticCodes {
		if declared[c] {
			t.Errorf("code %s is listed twice in allDiagnosticCodes", c)
		}
		declared[c] = true
	}

	covered := make(map[string]bool, len(diagnosticCases))
	for _, tc := range diagnosticCases {
		tc := tc
		t.Run(tc.code, func(t *testing.T) {
			t.Parallel()
			p, err := NewParser(tc.cfg)
			if err != nil {
				t.Fatalf("config: %v", err)
			}
			var res *Result
			if tc.subject {
				res, _ = p.ParseSubject(tc.message)
			} else {
				res, _ = p.Parse(tc.message)
			}
			if !hasCode(res, tc.code) {
				t.Errorf("Parse(%q) produced %v, want %s", tc.message, res.Codes(), tc.code)
			}
			// An error code must invalidate something; a warning must not.
			isError := strings.HasPrefix(tc.code, "E")
			if isError != res.HasErrors() {
				t.Errorf("%s: HasErrors() = %v, want %v (codes: %s)",
					tc.code, res.HasErrors(), isError, codesOf(res))
			}
		})
		covered[tc.code] = true
	}

	for _, c := range allDiagnosticCodes {
		if !covered[c] {
			t.Errorf("code %s is declared but no input in diagnosticCases produces it", c)
		}
	}
	for c := range covered {
		if !declared[c] {
			t.Errorf("code %s is produced by a test but missing from allDiagnosticCodes", c)
		}
	}
}

// TestDiagnosticCodesAreWellFormed guards the shape the registry promises: one
// letter, three digits, errors and warnings distinguishable by prefix alone.
func TestDiagnosticCodesAreWellFormed(t *testing.T) {
	t.Parallel()

	for _, c := range allDiagnosticCodes {
		if len(c) != 4 {
			t.Errorf("code %q is not four characters", c)
			continue
		}
		if c[0] != 'E' && c[0] != 'W' {
			t.Errorf("code %q does not start with E or W", c)
		}
		for i := 1; i < 4; i++ {
			if c[i] < '0' || c[i] > '9' {
				t.Errorf("code %q has a non-digit at index %d", c, i)
			}
		}
	}
}

// TestErrorsInvalidateOnlyTheirOwnUnit is the §16 guarantee that makes partial
// results usable: a broken unit must not take its siblings down with it.
func TestErrorsInvalidateOnlyTheirOwnUnit(t *testing.T) {
	t.Parallel()

	p := DefaultParser()
	// Three units: bad, good, bad.
	res, err := p.Parse(
		"feat(core)^med: broken\n\n---\n\nfix(cli): fine\n\n---\n\ncancel(api)!: also broken")
	if err == nil {
		t.Fatal("expected an error")
	}
	if len(res.Units) != 3 {
		t.Fatalf("got %d units, want 3", len(res.Units))
	}
	if res.Units[0].Valid || res.Units[2].Valid {
		t.Error("the broken units should be invalid")
	}
	if !res.Units[1].Valid {
		t.Error("the good unit should have survived")
	}
	valid := res.ValidUnits()
	if len(valid) != 1 || valid[0].Header.Type != "fix" {
		t.Errorf("ValidUnits() = %+v, want just the fix", valid)
	}
	if res.Bump() != BumpPatch {
		t.Errorf("Bump() = %s, want patch from the surviving unit", res.Bump())
	}
	if got := len(res.Errors()); got != 2 {
		t.Errorf("Errors() = %d, want 2 (codes: %s)", got, codesOf(res))
	}
}
