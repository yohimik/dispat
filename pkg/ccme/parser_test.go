package ccme

import (
	"strconv"
	"strings"
	"testing"
)

func TestNormalize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"bom", "\uFEFF" + "feat: x", "feat: x"},
		{"crlf", "feat: x\r\n\r\nbody", "feat: x\n\nbody"},
		{"cr", "feat: x\rbody", "feat: x\nbody"},
		{"trailing spaces", "feat: x  \nbody\t\t", "feat: x\nbody"},
		{"trailing blank lines", "feat: x\n\n\n\n", "feat: x"},
		{"leading whitespace preserved", "feat: x\n\nBREAKING CHANGE: a\n  more", "feat: x\n\nBREAKING CHANGE: a\n  more"},
		{"whitespace-only line becomes blank", "feat: x\n   \nbody", "feat: x\n\nbody"},
		{"idempotent", "feat: x\n\nbody", "feat: x\n\nbody"},
		// Every leading BOM goes, not just the first: stripping one at a time
		// would make Normalize non-idempotent, and a doubled BOM would parse
		// as one unit on the first pass and none on the second.
		// Regression, found by FuzzParse.
		{"doubled bom", "\uFEFF\uFEFF" + "feat: x", "feat: x"},
		{"tripled bom", "\uFEFF\uFEFF\uFEFF" + "feat: x", "feat: x"},
		{"bom only", "\uFEFF", ""},
		{"doubled bom only", "\uFEFF\uFEFF", ""},
		{"bom not at the front is content", "feat: \uFEFFx", "feat: \uFEFFx"},
	}
	for _, tc := range tests {
		got := Normalize(tc.in)
		if got != tc.want {
			t.Errorf("%s: Normalize(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
		if again := Normalize(got); again != got {
			t.Errorf("%s: Normalize is not idempotent: %q -> %q", tc.name, got, again)
		}
		// The fast-path predicate must agree with the rewriter, or Parse and
		// Normalize would disagree about what the normalised message is.
		if needsNormalizing(got) {
			t.Errorf("%s: needsNormalizing still reports work on %q", tc.name, got)
		}
	}
}

// TestParseIsStableUnderNormalisation pins the property FuzzParse checks:
// because Parse normalises first and Normalize is a fixed point, reparsing
// Result.Message must be indistinguishable from parsing the original.
func TestParseIsStableUnderNormalisation(t *testing.T) {
	t.Parallel()
	p := DefaultParser()

	for _, msg := range []string{
		"\uFEFF\uFEFF",
		"\uFEFF\uFEFF" + "feat(core): a",
		"\uFEFF\uFEFF\uFEFF" + "feat(core): a\r\n\r\nbody  \n\n",
		"---",
		"feat(core): a\n---",
		"feat(core): a  \r\n\r\nbody\t\n\n\n",
	} {
		res, err := p.Parse(msg)
		again, errAgain := p.Parse(res.Message)

		if (err == nil) != (errAgain == nil) {
			t.Errorf("Parse(%q): error changed on reparse: %v vs %v", msg, err, errAgain)
		}
		if len(again.Units) != len(res.Units) {
			t.Errorf("Parse(%q): unit count changed on reparse: %d vs %d",
				msg, len(res.Units), len(again.Units))
		}
		if strings.Join(again.Codes(), ",") != strings.Join(res.Codes(), ",") {
			t.Errorf("Parse(%q): diagnostics changed on reparse: %v vs %v",
				msg, res.Codes(), again.Codes())
		}
		if again.Message != res.Message {
			t.Errorf("Parse(%q): Message is not a fixed point: %q vs %q",
				msg, res.Message, again.Message)
		}
	}
}

func TestEmptyAndInvalidMessages(t *testing.T) {
	t.Parallel()

	p := DefaultParser()

	for _, in := range []string{"", "   ", "\n\n\n", "\t \n  "} {
		res, err := p.Parse(in)
		if err == nil || firstError(res) != CodeE002 {
			t.Errorf("Parse(%q) = %v (%s), want E002", in, err, codesOf(res))
		}
	}

	res, err := p.Parse("feat: \xff\xfe")
	if err == nil || firstError(res) != CodeE001 {
		t.Errorf("invalid UTF-8 = %v (%s), want E001", err, codesOf(res))
	}
}

// TestAppendixB2MultiUnit reproduces the multi-unit vectors of Appendix B.2.
func TestAppendixB2MultiUnit(t *testing.T) {
	t.Parallel()
	p := DefaultParser()

	t.Run("vector 28", func(t *testing.T) {
		res, err := p.Parse("feat(core): a\n\n---\n\nfix(cli): b")
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Units) != 2 {
			t.Fatalf("got %d units, want 2", len(res.Units))
		}
		if res.Units[0].Bump != BumpMinor || res.Units[0].Scopes().String() != "core" {
			t.Errorf("unit 0 = %s/%s", res.Units[0].Scopes(), res.Units[0].Bump)
		}
		if res.Units[1].Bump != BumpPatch || res.Units[1].Scopes().String() != "cli" {
			t.Errorf("unit 1 = %s/%s", res.Units[1].Scopes(), res.Units[1].Bump)
		}
		if hasCode(res, CodeW132) {
			t.Errorf("W132 must not fire when every unit is scoped")
		}
	})

	t.Run("vector 29 footers bind to their unit", func(t *testing.T) {
		res, err := p.Parse("feat(core): a\n\nBREAKING CHANGE: gone\n\n---\n\nfix(cli): b")
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Units) != 2 {
			t.Fatalf("got %d units, want 2", len(res.Units))
		}
		if !res.Units[0].Breaking || res.Units[0].Bump != BumpMajor {
			t.Errorf("unit 0 should be a major breaking change, got %s", res.Units[0].Bump)
		}
		if res.Units[0].BreakingDescription() != "gone" {
			t.Errorf("breaking description = %q", res.Units[0].BreakingDescription())
		}
		if res.Units[1].Breaking || res.Units[1].Bump != BumpPatch {
			t.Errorf("the footer must not reach unit 1, got %s", res.Units[1].Bump)
		}
	})

	t.Run("vector 30 message-level trailer", func(t *testing.T) {
		res, err := p.Parse("fix(core): a\n\n---\n\nfix(cli): b\n\nSigned-off-by: A <a%example.com>")
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Units) != 2 {
			t.Fatalf("got %d units, want 2", len(res.Units))
		}
		f := res.Units[1].Footers
		if len(f) != 1 || !f[0].MessageLevel {
			t.Fatalf("footers = %+v, want one message-level trailer", f)
		}
		if hasCode(res, CodeW150) {
			t.Errorf("a message-level trailer must not produce W150")
		}
	})

	t.Run("vector 31 escaped separator", func(t *testing.T) {
		msg := "docs(core): describe the format\n\nThe delimiter is:\n\n\\---\n\nand it separates units."
		res, err := p.Parse(msg)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Units) != 1 {
			t.Fatalf("got %d units, want 1", len(res.Units))
		}
		if !strings.Contains(res.Units[0].Body, "\n---\n") {
			t.Errorf("body = %q, want a literal --- line", res.Units[0].Body)
		}
	})
}

func TestSeparatorHandling(t *testing.T) {
	t.Parallel()
	p := DefaultParser()

	t.Run("leading separator", func(t *testing.T) {
		res, err := p.Parse("---\nfeat(core): a")
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Units) != 1 {
			t.Fatalf("got %d units, want 1", len(res.Units))
		}
		if !hasCode(res, CodeW001) {
			t.Errorf("missing W001 (codes: %s)", codesOf(res))
		}
	})

	t.Run("consecutive separators", func(t *testing.T) {
		res, err := p.Parse("feat(core): a\n---\n---\nfix(cli): b")
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Units) != 2 {
			t.Fatalf("got %d units, want 2", len(res.Units))
		}
		if !hasCode(res, CodeW001) {
			t.Errorf("missing W001 (codes: %s)", codesOf(res))
		}
	})

	t.Run("trailing whitespace is stripped first", func(t *testing.T) {
		res, err := p.Parse("feat(core): a\n--- \nfix(cli): b")
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Units) != 2 {
			t.Fatalf("got %d units, want 2", len(res.Units))
		}
	})

	t.Run("leading whitespace is not a separator", func(t *testing.T) {
		res, err := p.Parse("feat(core): a\n\n ---\n\nmore body")
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Units) != 1 {
			t.Fatalf("got %d units, want 1", len(res.Units))
		}
	})

	t.Run("longer rule is not a separator", func(t *testing.T) {
		res, err := p.Parse("feat(core): a\n\n----\n\nmore body")
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Units) != 1 {
			t.Fatalf("got %d units, want 1", len(res.Units))
		}
	})

	t.Run("configured separator", func(t *testing.T) {
		q := MustNewParser(Config{Separator: "%%%"})
		res, err := q.Parse("feat(core): a\n%%%\nfix(cli): b")
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Units) != 2 {
			t.Fatalf("got %d units, want 2", len(res.Units))
		}
		// The default separator is now ordinary body text.
		res, err = q.Parse("feat(core): a\n\n---\n\nbody")
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Units) != 1 {
			t.Fatalf("got %d units, want 1", len(res.Units))
		}
	})
}

// TestSeparatorOnlyMessages covers the shapes where every unit is empty, and
// pins the position of the W001 that reports it.
//
// Regression: a message ending in a separator leaves an empty region starting
// one line past the end, and W001 was reported at that non-existent line.
// Found by FuzzParse; the invariant that caught it is checkResultInvariants.
func TestSeparatorOnlyMessages(t *testing.T) {
	t.Parallel()
	p := DefaultParser()

	for _, tc := range []struct {
		name  string
		msg   string
		units int
		warns int
	}{
		{"only a separator", "---", 0, 2},
		{"two separators", "---\n---", 0, 3},
		{"trailing separator", "feat(core): a\n---", 1, 1},
		{"leading separator", "---\nfeat(core): a", 1, 1},
		{"trailing separator and newline", "feat(core): a\n---\n", 1, 1},
	} {
		res, err := p.Parse(tc.msg)
		if err != nil {
			t.Errorf("%s: Parse(%q) = %v", tc.name, tc.msg, err)
			continue
		}
		if len(res.Units) != tc.units {
			t.Errorf("%s: %d units, want %d", tc.name, len(res.Units), tc.units)
		}
		if got := len(res.Warnings()); got != tc.warns {
			t.Errorf("%s: %d warnings, want %d (codes: %s)",
				tc.name, got, tc.warns, codesOf(res))
		}
		lineCount := strings.Count(res.Message, "\n") + 1
		for _, d := range res.Diagnostics {
			if d.Position.Line < 1 || d.Position.Line > lineCount {
				t.Errorf("%s: %s at line %d, message has %d lines",
					tc.name, d.Code, d.Position.Line, lineCount)
			}
		}
	}
}

func TestSeparatorValidation(t *testing.T) {
	t.Parallel()

	// §4.3: at least three characters, ASCII printable, no whitespace, and not
	// starting with a character that can begin a type.
	for _, sep := range []string{"-", "--", "-- -", "abc", "a--", "==\t=", "→→→"} {
		if _, err := NewParser(Config{Separator: sep}); err == nil {
			t.Errorf("Separator %q = nil, want an error", sep)
		}
	}
	for _, sep := range []string{"---", "%%%", "###", "-----"} {
		if _, err := NewParser(Config{Separator: sep}); err != nil {
			t.Errorf("Separator %q = %v, want success", sep, err)
		}
	}

	// The empty string is the zero value and therefore means "the default".
	p, err := NewParser(Config{Separator: ""})
	if err != nil {
		t.Fatalf("Separator \"\" = %v, want the default", err)
	}
	if got := p.Config().Separator; got != DefaultSeparator {
		t.Errorf("Separator \"\" resolved to %q, want %q", got, DefaultSeparator)
	}
}

func TestMultiUnitScopingWarning(t *testing.T) {
	t.Parallel()
	p := DefaultParser()

	res, err := p.Parse("feat(core): a\n\n---\n\nfix: b")
	if err != nil {
		t.Fatal(err)
	}
	if !hasCode(res, CodeW132) {
		t.Errorf("missing W132 (codes: %s)", codesOf(res))
	}

	res, _ = p.Parse("feat: a")
	if hasCode(res, CodeW132) {
		t.Errorf("W132 must not fire on a single-unit message")
	}
}

func TestErrorIsolatedToItsUnit(t *testing.T) {
	t.Parallel()
	p := DefaultParser()

	res, err := p.Parse("feat(core)^med: a\n\n---\n\nfix(cli): b")
	if err == nil {
		t.Fatal("want an error")
	}
	if len(res.Units) != 2 {
		t.Fatalf("got %d units, want 2", len(res.Units))
	}
	if res.Units[0].Valid {
		t.Errorf("unit 0 should be invalid")
	}
	valid := res.ValidUnits()
	if len(valid) != 1 || valid[0].Header.Type != "fix" {
		t.Errorf("ValidUnits = %+v, want just the fix unit", valid)
	}
	if res.Bump() != BumpPatch {
		t.Errorf("Bump = %s, want patch (the invalid unit contributes nothing)", res.Bump())
	}
	var pe *ParseError
	if !asParseError(err, &pe) {
		t.Fatalf("error type = %T, want *ParseError", err)
	}
	if len(pe.Codes()) != 1 || pe.Codes()[0] != CodeE111 {
		t.Errorf("codes = %v, want [E111]", pe.Codes())
	}
}

func asParseError(err error, target **ParseError) bool {
	pe, ok := err.(*ParseError)
	if ok {
		*target = pe
	}
	return ok
}

func TestBlankLineAfterHeaderRequired(t *testing.T) {
	t.Parallel()
	p := DefaultParser()

	res, err := p.Parse("feat(core): a\nbody on the next line")
	if err == nil || firstError(res) != CodeE100 {
		t.Errorf("= %v (%s), want E100", err, codesOf(res))
	}

	if _, err := p.Parse("feat(core): a"); err != nil {
		t.Errorf("a header-only unit must be legal: %v", err)
	}
}

func TestUnitPositions(t *testing.T) {
	t.Parallel()
	p := DefaultParser()

	res, err := p.Parse("feat(core): a\n\nbody\n\n---\n\nfix(cli): b")
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Units[0].Start; got.Line != 1 {
		t.Errorf("unit 0 starts at %s, want line 1", got)
	}
	if got := res.Units[1].Start; got.Line != 7 {
		t.Errorf("unit 1 starts at %s, want line 7", got)
	}
}

func TestParserIsReusableAndConcurrent(t *testing.T) {
	t.Parallel()
	p := DefaultParser()

	const msg = "feat(core)^^minor%beta!: a\n\nbody\n\nPropagate-Channel-Depth: 2\n\n---\n\nfix(cli): b"
	done := make(chan string, 8)
	for i := 0; i < 8; i++ {
		go func() {
			res, err := p.Parse(msg)
			if err != nil {
				done <- err.Error()
				return
			}
			done <- res.Units[0].Header.Description + "|" + res.Units[0].Bump.String()
		}()
	}
	want := "a|major"
	for i := 0; i < 8; i++ {
		if got := <-done; got != want {
			t.Fatalf("concurrent parse = %q, want %q", got, want)
		}
	}
}

func TestConfigIsCopied(t *testing.T) {
	t.Parallel()

	p := DefaultParser()
	cfg := p.Config()
	cfg.Types["feat"] = BumpMajor
	cfg.Propagation.Kinds[0] = KindAll

	res, err := p.ParseSubject("feat(core): x")
	if err != nil {
		t.Fatal(err)
	}
	if res.Units[0].Bump != BumpMinor {
		t.Errorf("mutating the returned Config changed the parser")
	}
	if res.Units[0].Directives.Kinds[0] != KindDependencies {
		t.Errorf("mutating the returned Config changed the propagation kinds")
	}
}

func TestInvalidConfigs(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cfg  Config
	}{
		{"uppercase type", Config{Types: map[string]Bump{"Feat": BumpMinor}}},
		{"empty type", Config{Types: map[string]Bump{"": BumpMinor}}},
		{"hyphenated type", Config{Types: map[string]Bump{"fe-at": BumpMinor}}},
		{"bad propagate", Config{Propagation: PropagationConfig{Bump: "med"}}},
		{"bad depth", Config{Propagation: PropagationConfig{Depth: Depth(-7)}}},
		{"bad kind", Config{Propagation: PropagationConfig{Kinds: []DependencyKind{"nope"}}}},
		{"bad propagate channel", Config{Propagation: PropagationConfig{Channel: "Beta"}}},
		{"bad allowed channel", Config{AllowedChannels: []string{"Beta"}}},
		{"reserved allowed channel", Config{AllowedChannels: []string{"latest"}}},
		{"bad separator", Config{Separator: "ab"}},
	}
	for _, tc := range cases {
		if _, err := NewParser(tc.cfg); err == nil {
			t.Errorf("%s: NewParser = nil, want an error", tc.name)
		}
	}
}

func TestZeroConfigEqualsDefaultConfig(t *testing.T) {
	t.Parallel()

	zero := MustNewParser(Config{}).Config()
	explicit := MustNewParser(DefaultConfig()).Config()

	if zero.Separator != explicit.Separator ||
		zero.MaxDescriptionLength != explicit.MaxDescriptionLength ||
		zero.Propagation.Bump != explicit.Propagation.Bump ||
		zero.Propagation.Depth != explicit.Propagation.Depth ||
		zero.Propagation.Channel != explicit.Propagation.Channel ||
		len(zero.Types) != len(explicit.Types) ||
		len(zero.Propagation.Kinds) != len(explicit.Propagation.Kinds) ||
		len(zero.MessageLevelTrailers) != len(explicit.MessageLevelTrailers) ||
		len(zero.IssueTrailers) != len(explicit.IssueTrailers) {
		t.Errorf("Config{} = %+v, want the same as DefaultConfig() = %+v", zero, explicit)
	}

	// A single field can be changed without restating the rest.
	p := MustNewParser(Config{Lenient: true})
	if _, err := p.ParseSubject("Feat: x"); err != nil {
		t.Errorf("Config{Lenient: true} did not keep the other defaults: %v", err)
	}
	if p.Config().Separator != DefaultSeparator {
		t.Errorf("separator = %q, want the default", p.Config().Separator)
	}
}

// TestClonePreservesNilVersusEmpty guards the distinction the whole
// zero-value-means-default scheme rests on. append([]T(nil), s...) returns nil
// for an empty s, which would turn "none" back into "the default".
func TestClonePreservesNilVersusEmpty(t *testing.T) {
	t.Parallel()

	empty := Config{
		Types:                map[string]Bump{},
		AllowedChannels:      []string{},
		MessageLevelTrailers: []string{},
		IssueTrailers:        []string{},
		Propagation:          PropagationConfig{Kinds: []DependencyKind{}},
	}.Clone()

	if empty.Types == nil || empty.AllowedChannels == nil || empty.MessageLevelTrailers == nil ||
		empty.IssueTrailers == nil || empty.Propagation.Kinds == nil {
		t.Errorf("Clone turned a non-nil empty collection into nil: %+v", empty)
	}

	nils := Config{}.Clone()
	if nils.Types != nil || nils.AllowedChannels != nil || nils.MessageLevelTrailers != nil ||
		nils.IssueTrailers != nil || nils.Propagation.Kinds != nil {
		t.Errorf("Clone turned nil into a non-nil collection: %+v", nils)
	}

	// The same must survive the trip through NewParser and back out.
	got := MustNewParser(Config{AllowedChannels: []string{}}).Config()
	if got.AllowedChannels == nil {
		t.Errorf("an empty AllowedChannels became nil, re-enabling every channel")
	}
	if _, err := MustNewParser(Config{AllowedChannels: []string{}}).ParseSubject("feat(core)%beta: x"); err == nil {
		t.Errorf("an empty AllowedChannels must reject every channel name")
	}
}

func TestEmptySlicesDisableTheirDefaults(t *testing.T) {
	t.Parallel()

	// A non-nil empty slice is "none", as opposed to nil meaning "the default".
	p := MustNewParser(Config{
		Types:                map[string]Bump{},
		MessageLevelTrailers: []string{},
		Propagation:          PropagationConfig{Kinds: []DependencyKind{}},
	})
	res, err := p.Parse("feat(core): a\n\nSigned-off-by: A <a%x>")
	if err != nil {
		t.Fatal(err)
	}
	if res.Units[0].Bump != BumpNone || !hasCode(res, CodeW140) {
		t.Errorf("an empty type table must make every type unknown (codes: %s)", codesOf(res))
	}
	if !hasCode(res, CodeW150) {
		t.Errorf("Signed-off-by should now be an unknown key (codes: %s)", codesOf(res))
	}
	if len(res.Units[0].Directives.Kinds) != 0 {
		t.Errorf("kinds = %v, want empty", res.Units[0].Directives.Kinds)
	}
}

func TestConfiguredPropagationDefaults(t *testing.T) {
	t.Parallel()

	p := MustNewParser(Config{
		Propagation: PropagationConfig{
			Bump:    PropagateInherit,
			Depth:   DepthAll,
			Kinds:   []DependencyKind{KindAll},
			Channel: ChannelStable,
		},
	})
	res, err := p.ParseSubject("feat(core): x")
	if err != nil {
		t.Fatal(err)
	}
	d := res.Units[0].Directives
	if d.Propagate != PropagateInherit || !d.Depth.IsAll() {
		t.Errorf("configured defaults not applied: %+v", d)
	}
	if d.PropagateChannel.Word != ChannelStable && d.PropagateChannel.To != ChannelStable {
		t.Errorf("Propagate-Channel = %q, want stable", d.PropagateChannel)
	}

	// An inline directive still overrides the configured default (§8.3).
	res, err = p.ParseSubject("feat(core)+2: x")
	if err != nil {
		t.Fatal(err)
	}
	if res.Units[0].Directives.Depth != 2 {
		t.Errorf("inline +2 did not override the configured depth")
	}
}

func TestPropagateInheritBump(t *testing.T) {
	t.Parallel()

	if got := PropagateInherit.Bump(BumpMajor); got != BumpMajor {
		t.Errorf("inherit.Bump(major) = %s", got)
	}
	if got := PropagatePatch.Bump(BumpMajor); got != BumpPatch {
		t.Errorf("patch.Bump(major) = %s", got)
	}
	if got := PropagateNone.Bump(BumpMajor); got != BumpNone {
		t.Errorf("none.Bump(major) = %s", got)
	}
}

func TestMaxBump(t *testing.T) {
	t.Parallel()

	if MaxBump(BumpMinor, BumpPatch) != BumpMinor {
		t.Error("max(minor, patch) != minor")
	}
	if MaxBump(BumpPatch, BumpMajor) != BumpMajor {
		t.Error("max(patch, major) != major")
	}
	if MaxBump(BumpNone, BumpNone) != BumpNone {
		t.Error("max(none, none) != none")
	}
}

func TestFullExampleFromSpecSummary(t *testing.T) {
	t.Parallel()

	const msg = `feat(@acme/core)^^minor%beta: streaming reader

Replaces the buffered reader with an incremental one.

Propagate-Channel: beta
Propagate-Channel-Depth: all

---

fix(@acme/cli): correct exit code on SIGINT

---

cancel(@acme/legacy-adapter): reset release state`

	p := DefaultParser()
	res, err := p.Parse(msg)
	if err != nil {
		t.Fatalf("%v (codes: %s)", err, codesOf(res))
	}
	if len(res.Units) != 3 {
		t.Fatalf("got %d units, want 3", len(res.Units))
	}

	u := res.Units[0]
	if u.Header.Type != "feat" || u.Scopes().String() != "@acme/core" {
		t.Errorf("unit 0 header = %+v", u.Header)
	}
	if u.Directives.Propagate != PropagateMinor || !u.Directives.Depth.IsAll() {
		t.Errorf("unit 0 propagation = %s/%s", u.Directives.Propagate, u.Directives.Depth)
	}
	if u.Directives.Channel.To != "beta" || !u.Directives.ChannelSet {
		t.Errorf("unit 0 channel = %q", u.Directives.Channel)
	}
	if u.Directives.PropagateChannel.To != "beta" || !u.Directives.ChannelDepth.IsAll() {
		t.Errorf("unit 0 channel axis = %q/%s",
			u.Directives.PropagateChannel, u.Directives.ChannelDepth)
	}
	if u.Bump != BumpMinor {
		t.Errorf("unit 0 bump = %s, want minor", u.Bump)
	}
	if u.Body != "Replaces the buffered reader with an incremental one." {
		t.Errorf("unit 0 body = %q", u.Body)
	}
	// §8.4 has no per-unit override, so the edge kinds come from configuration.
	wantKinds := DefaultPropagateKinds()
	if len(u.Directives.Kinds) != len(wantKinds) {
		t.Fatalf("unit 0 kinds = %v, want %v", u.Directives.Kinds, wantKinds)
	}
	for i, k := range wantKinds {
		if u.Directives.Kinds[i] != k {
			t.Errorf("unit 0 kinds = %v, want %v", u.Directives.Kinds, wantKinds)
			break
		}
	}

	if res.Units[1].Bump != BumpPatch {
		t.Errorf("unit 1 bump = %s, want patch", res.Units[1].Bump)
	}
	if !res.Units[2].IsCancel() || res.Units[2].Bump != BumpNone {
		t.Errorf("unit 2 = %+v, want an inert cancel", res.Units[2])
	}
	if res.Bump() != BumpMinor {
		t.Errorf("message bump = %s, want minor", res.Bump())
	}
}

func TestWorkedExampleD2(t *testing.T) {
	t.Parallel()

	const msg = "refactor(@acme/core)^^inherit!: remove the v1 plugin interface\n\n" +
		"BREAKING CHANGE: `registerPlugin` is gone. Use `plugins: []` in the\n" +
		"config object."

	p := DefaultParser()
	res, err := p.Parse(msg)
	if err != nil {
		t.Fatalf("%v (codes: %s)", err, codesOf(res))
	}
	u := res.Units[0]
	if u.Bump != BumpMajor {
		t.Errorf("bump = %s, want major", u.Bump)
	}
	if u.Directives.Propagate != PropagateInherit || !u.Directives.Depth.IsAll() {
		t.Errorf("propagation = %s/%s, want inherit/all", u.Directives.Propagate, u.Directives.Depth)
	}
	if !strings.HasSuffix(u.BreakingDescription(), "config object.") {
		t.Errorf("breaking description = %q, want the continuation joined", u.BreakingDescription())
	}
}

func TestWorkedExampleD7(t *testing.T) {
	t.Parallel()

	// §D.7 was rewritten when the Release-As bump form was removed: a
	// repository that ships compiled artefacts states that once in `types`
	// rather than overriding the bump on every commit.
	types := DefaultTypes()
	types["build"] = BumpPatch
	p := MustNewParser(Config{Types: types})

	res, err := p.Parse("build(*,-docs-site,-e2e)+0: bump TypeScript to 5.6")
	if err != nil {
		t.Fatalf("%v (codes: %s)", err, codesOf(res))
	}
	u := res.Units[0]
	if u.TypeBump != BumpPatch || u.Bump != BumpPatch {
		t.Errorf("type bump = %s, bump = %s; want patch, patch", u.TypeBump, u.Bump)
	}
	if u.Directives.Depth != 0 {
		t.Errorf("depth = %s, want 0", u.Directives.Depth)
	}
	if len(u.Scopes().Excludes()) != 2 {
		t.Errorf("excludes = %v, want 2", u.Scopes().Excludes())
	}

	// The old spelling is now an error with an explanation.
	res, err = p.Parse("chore(core): bump TypeScript\n\nRelease-As: patch")
	if err == nil || firstError(res) != CodeE151 {
		t.Errorf("Release-As: patch = %v (%s), want E151", err, codesOf(res))
	}
}

// TestDefaultLimitBoundaries exercises the §14.1 bounds at their default
// values rather than lowered test values: the boundary itself is legal, one
// past it is E158, and a near-limit input costs nothing pathological.
func TestDefaultLimitBoundaries(t *testing.T) {
	t.Parallel()

	p := DefaultParser()

	terms := make([]string, DefaultScopeTermsPerUnit)
	for i := range terms {
		terms[i] = "p" + strconv.Itoa(i)
	}
	atLimit := "feat(" + strings.Join(terms, ",") + "): x"
	if res, err := p.Parse(atLimit); err != nil {
		t.Errorf("%d scope terms is the boundary and must parse: %v (%s)",
			DefaultScopeTermsPerUnit, err, codesOf(res))
	}
	over := "feat(" + strings.Join(append(terms, "one-more"), ",") + "): x"
	if res, err := p.Parse(over); err == nil || !hasCode(res, CodeE158) {
		t.Errorf("%d scope terms = %v (%s), want E158", DefaultScopeTermsPerUnit+1, err, codesOf(res))
	}

	// A message just under the byte bound parses; just over is E158.
	line := strings.Repeat("x", 1023) + "\n"
	body := strings.Repeat(line, (DefaultMessageBytes/1024)-1)
	under := "feat: near the limit\n\n" + body
	if len(under) > DefaultMessageBytes {
		t.Fatalf("fixture miscounted: %d bytes", len(under))
	}
	if res, err := p.Parse(under); err != nil {
		t.Errorf("a message under limits.messageBytes must parse: %v (%s)", err, codesOf(res))
	}
	overMsg := under + strings.Repeat("y", DefaultMessageBytes-len(under)+1)
	if res, err := p.Parse(overMsg); err == nil || !hasCode(res, CodeE158) {
		t.Errorf("a message over limits.messageBytes = %v (%s), want E158", err, codesOf(res))
	}
}
