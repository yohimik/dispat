package ccme

import (
	"strings"
	"testing"
)

// codesOf renders a result's diagnostics as a comparable string.
func codesOf(r *Result) string { return strings.Join(r.Codes(), ",") }

// firstError returns the code of the first error diagnostic, or "".
func firstError(r *Result) string {
	for _, d := range r.Diagnostics {
		if d.IsError() {
			return d.Code
		}
	}
	return ""
}

// TestAppendixB1Parsing reproduces every vector of Appendix B.1.
func TestAppendixB1Parsing(t *testing.T) {
	t.Parallel()

	type expect struct {
		errCode  string
		typ      string
		scopes   []string
		propagat *Propagate
		depth    *Depth
		channel  *ChannelValue
		pchannel *ChannelValue
		cdepth   *Depth
		breaking bool
		desc     string
		warnings []string
	}
	ptrP := func(p Propagate) *Propagate { return &p }
	ptrD := func(d Depth) *Depth { return &d }
	ch := func(to string) *ChannelValue { return &ChannelValue{To: to} }
	tr := func(from, to string) *ChannelValue { return &ChannelValue{From: from, To: to} }
	word := func(w string) *ChannelValue { return &ChannelValue{Word: w} }

	tests := []struct {
		name  string
		input string
		want  expect
	}{
		{"B1/1 no scope", "feat: x", expect{typ: "feat", desc: "x"}},
		{"B1/2 one scope", "fix(core): x", expect{typ: "fix", scopes: []string{"core"}, desc: "x"}},
		{"B1/3 two scopes", "feat(core,cli): x", expect{typ: "feat", scopes: []string{"core", "cli"}, desc: "x"}},
		{"B1/4 space after comma", "feat(core, cli): x", expect{typ: "feat", scopes: []string{"core", "cli"}, desc: "x"}},
		{"B1/5 space before comma", "feat(core ,cli): x", expect{errCode: CodeE102}},
		{"B1/6 npm scope", "feat(@acme/theme): x", expect{typ: "feat", scopes: []string{"@acme/theme"}, desc: "x"}},
		{"B1/7 npm scope and channel", "feat(@acme/theme)@beta: x", expect{
			typ: "feat", scopes: []string{"@acme/theme"}, channel: ch("beta"), desc: "x"}},
		{"B1/8 glob and exclusion", "feat(*,-docs-site): x", expect{
			typ: "feat", scopes: []string{"*", "-docs-site"}, desc: "x"}},
		{"B1/9 derived minus", "feat(.,-ui): x", expect{
			typ: "feat", scopes: []string{".", "-ui"}, desc: "x"}},
		{"B1/10 propagate and depth", "feat(core)^minor+2: x", expect{
			typ: "feat", scopes: []string{"core"}, propagat: ptrP(PropagateMinor), depth: ptrD(2), desc: "x"}},
		{"B1/11 order independent", "feat(core)+2^minor: x", expect{
			typ: "feat", scopes: []string{"core"}, propagat: ptrP(PropagateMinor), depth: ptrD(2), desc: "x"}},
		// §8.3: a caret implies depth 1, and a bare caret is legal.
		{"B1/10a bare caret", "feat(core)^: x", expect{
			typ: "feat", scopes: []string{"core"}, depth: ptrD(1), desc: "x"}},
		{"B1/10b caret implies depth 1", "feat(core)^minor: x", expect{
			typ: "feat", scopes: []string{"core"}, propagat: ptrP(PropagateMinor), depth: ptrD(1), desc: "x"}},
		{"B1/12 duplicate caret", "feat(core)^minor^patch: x", expect{errCode: CodeE110}},
		{"B1/13 abbreviated value", "feat(core)^med: x", expect{errCode: CodeE111}},
		{"B1/14 breaking with depth all", "feat(core)^minor+*!: x", expect{
			typ: "feat", scopes: []string{"core"}, propagat: ptrP(PropagateMinor),
			depth: ptrD(DepthAll), breaking: true, desc: "x"}},
		{"B1/14a doubled caret", "feat(core)^^minor: x", expect{
			typ: "feat", scopes: []string{"core"}, propagat: ptrP(PropagateMinor),
			depth: ptrD(DepthAll), desc: "x"}},
		{"B1/14b bare doubled caret", "feat(core)^^: x", expect{
			typ: "feat", scopes: []string{"core"}, depth: ptrD(DepthAll), desc: "x"}},
		{"B1/14c doubled caret breaking", "feat(core)^^!: x", expect{
			typ: "feat", scopes: []string{"core"}, depth: ptrD(DepthAll), breaking: true, desc: "x"}},
		{"B1/14d redundant depth", "feat(core)^^minor+*: x", expect{
			typ: "feat", scopes: []string{"core"}, propagat: ptrP(PropagateMinor),
			depth: ptrD(DepthAll), desc: "x", warnings: []string{CodeW110}}},
		{"B1/14e conflicting depth", "feat(core)^^minor+2: x", expect{errCode: CodeE113}},
		{"B1/14f conflicting depth reversed", "feat(core)+2^^minor: x", expect{errCode: CodeE113}},
		{"B1/14g caret then doubled", "feat(core)^minor^^: x", expect{errCode: CodeE110}},
		{"B1/14h third caret", "feat(core)^^^minor: x", expect{errCode: CodeE110}},
		{"B1/14i doubled caret bad value", "feat(core)^^med: x", expect{errCode: CodeE111}},
		{"B1/14j doubled caret and channel", "feat(core)^^@beta: x", expect{
			typ: "feat", scopes: []string{"core"}, depth: ptrD(DepthAll),
			channel: ch("beta"), desc: "x"}},
		// --- channel axis, §5.3 / §11.2 ---
		{"B1/14k ++N alone", "feat(core)++2: x", expect{
			typ: "feat", scopes: []string{"core"}, cdepth: ptrD(2), desc: "x"}},
		{"B1/14l bare ++", "feat(core)++: x", expect{errCode: CodeE111}},
		{"B1/14m third plus", "feat(core)+++2: x", expect{errCode: CodeE110}},
		{"B1/14n two ++N", "feat(core)++1++2: x", expect{errCode: CodeE110}},
		{"B1/14o ++N wins over @@", "feat(core)@@beta++3: x", expect{
			typ: "feat", scopes: []string{"core"}, pchannel: ch("beta"), cdepth: ptrD(3), desc: "x"}},
		{"B1/14p order independent", "feat(core)++3@@beta: x", expect{
			typ: "feat", scopes: []string{"core"}, pchannel: ch("beta"), cdepth: ptrD(3), desc: "x"}},
		{"B1/14q both axes", "feat(core)^^minor@@beta++1: x", expect{
			typ: "feat", scopes: []string{"core"}, propagat: ptrP(PropagateMinor), depth: ptrD(DepthAll),
			pchannel: ch("beta"), cdepth: ptrD(1), desc: "x"}},
		{"B1/14r + and ++ are distinct", "feat(core)+2++1: x", expect{
			typ: "feat", scopes: []string{"core"}, depth: ptrD(2), cdepth: ptrD(1), desc: "x"}},
		{"B1/14r1 saturation", "feat(core)+9999: x", expect{
			typ: "feat", scopes: []string{"core"}, depth: ptrD(DepthAll), desc: "x"}},
		{"B1/14r2 saturation unbounded", "feat(core)+20000: x", expect{
			typ: "feat", scopes: []string{"core"}, depth: ptrD(DepthAll), desc: "x"}},
		{"B1/14r3 saturation on channel axis", "feat(core)++20000: x", expect{
			typ: "feat", scopes: []string{"core"}, cdepth: ptrD(DepthAll), desc: "x"}},
		{"B1/14r4 leading zero", "feat(core)+00: x", expect{errCode: CodeE111}},
		{"B1/14r5 leading zero 007", "feat(core)+007: x", expect{errCode: CodeE111}},
		{"B1/14s channel transition", "feat(core)@beta>rc: x", expect{
			typ: "feat", scopes: []string{"core"}, channel: tr("beta", "rc"), desc: "x"}},
		{"B1/14t any prerelease to stable", "feat(core)@@*>stable++*: x", expect{
			typ: "feat", scopes: []string{"core"},
			pchannel: tr(ChannelAnyPrerelease, ChannelStable), cdepth: ptrD(DepthAll), desc: "x"}},
		{"B1/14u star as to", "feat(core)@@beta>*: x", expect{errCode: CodeE111}},
		{"B1/14v two arrows", "feat(core)@@a>b>c: x", expect{errCode: CodeE111}},
		{"B1/14w empty from", "feat(core)@>stable: x", expect{errCode: CodeE111}},
		{"B1/14x inherit as a side", "feat(core)@@beta>inherit: x", expect{errCode: CodeE111}},
		{"B1/14y @@inherit is a word", "feat(core)@@inherit: x", expect{
			typ: "feat", scopes: []string{"core"}, pchannel: word(ChannelInherit), cdepth: ptrD(1), desc: "x"}},
		{"B1/14z @@none is a word", "feat(core)@@none: x", expect{
			typ: "feat", scopes: []string{"core"}, pchannel: word(ChannelNone), cdepth: ptrD(1), desc: "x"}},
		{"B1/14z1 bare @@", "feat(core)@@: x", expect{errCode: CodeE111}},
		{"B1/14z2 third at", "feat(core)@@@beta: x", expect{errCode: CodeE110}},
		{"B1/14z3 inherit not a Channel value", "feat(core)@inherit: x", expect{errCode: CodeE111}},

		{"B1/15 bang before directives", "feat(core)!^minor: x", expect{errCode: CodeE120}},
		{"B1/16 uppercase type", "Feat: x", expect{errCode: CodeE101}},
		{"B1/17 no space after colon", "feat:x", expect{errCode: CodeE120}},
		{"B1/18 two spaces after colon", "feat:  x", expect{errCode: CodeE120}},
		{"B1/19 empty description", "feat: ", expect{errCode: CodeE121}},
		{"B1/20 empty scope-set", "feat(): x", expect{errCode: CodeE104}},
		{"B1/21 unbalanced parenthesis", "feat(core: x", expect{errCode: CodeE103}},
		{"B1/22 colon in description", "feat(core): fix: y", expect{
			typ: "feat", scopes: []string{"core"}, desc: "fix: y"}},
		{"B1/23 cancel", "cancel(*): reset release state", expect{
			typ: "cancel", scopes: []string{"*"}, desc: "reset release state"}},
		{"B1/24 cancel breaking", "cancel(*)!: x", expect{errCode: CodeE170}},
		{"B1/25 cancel with directive", "cancel(core)^minor: x", expect{errCode: CodeE171}},
		{"B1/26 release stable", "release(cli)@stable: x", expect{
			typ: "release", scopes: []string{"cli"}, channel: ch(ChannelStable), desc: "x"}},
		{"B1/27 release breaking", "release(cli)!: x", expect{errCode: CodeE141}},
		// BREAKING CHANGE can never be a type: uppercase and containing a
		// space, it fails §5.1 twice (§5.1, §8.1.1 rule 1).
		{"B1/27a breaking change as header", "BREAKING CHANGE: gone", expect{errCode: CodeE100}},
		{"B1/27a hyphenated as header", "BREAKING-CHANGE: gone", expect{errCode: CodeE100}},
		{"B1/27b lowercase breaking type", "breaking: x", expect{
			typ: "breaking", desc: "x", warnings: []string{CodeW140}}},
		{"B1/27c second scope-set", "feat(a)(b): x", expect{errCode: CodeE103}},
		{"B1/27d trailing comma in scope", "feat(a,): x", expect{errCode: CodeE104}},
		{"B1/27e digit in type", "feat2: x", expect{errCode: CodeE101}},
		{"B1/27f no type", ": x", expect{errCode: CodeE100}},
		{"B1/27g footer on the header line", "release(api): Release-As: 3.0.0", expect{
			typ: "release", scopes: []string{"api"}, desc: "Release-As: 3.0.0",
			warnings: []string{CodeW141}}},
	}

	p := DefaultParser()
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res, err := p.ParseSubject(tc.input)

			if tc.want.errCode != "" {
				if err == nil {
					t.Fatalf("ParseSubject(%q) = no error, want %s (codes: %s)",
						tc.input, tc.want.errCode, codesOf(res))
				}
				if got := firstError(res); got != tc.want.errCode {
					t.Fatalf("ParseSubject(%q) error = %s, want %s (codes: %s)",
						tc.input, got, tc.want.errCode, codesOf(res))
				}
				if res.Units[0].Valid {
					t.Errorf("unit should be invalid")
				}
				return
			}

			if err != nil {
				t.Fatalf("ParseSubject(%q) = %v, want success", tc.input, err)
			}
			u := res.Units[0]
			if u.Header.Type != tc.want.typ {
				t.Errorf("type = %q, want %q", u.Header.Type, tc.want.typ)
			}
			if got := u.Header.Scopes.String(); got != strings.Join(tc.want.scopes, ",") {
				t.Errorf("scopes = %q, want %q", got, strings.Join(tc.want.scopes, ","))
			}
			if (len(tc.want.scopes) > 0) != u.Header.HasScopeSet {
				t.Errorf("HasScopeSet = %v", u.Header.HasScopeSet)
			}
			if u.Header.Description != tc.want.desc {
				t.Errorf("description = %q, want %q", u.Header.Description, tc.want.desc)
			}
			if u.Header.Breaking != tc.want.breaking {
				t.Errorf("breaking = %v, want %v", u.Header.Breaking, tc.want.breaking)
			}
			assertPtrEq(t, "inline propagate", u.Header.Inline.Propagate, tc.want.propagat)
			assertPtrEq(t, "inline depth", u.Header.Inline.Depth, tc.want.depth)
			assertPtrEq(t, "inline channel", u.Header.Inline.Channel, tc.want.channel)
			assertPtrEq(t, "inline propagate-channel", u.Header.Inline.PropagateChannel, tc.want.pchannel)
			assertPtrEq(t, "inline channel depth", u.Header.Inline.ChannelDepth, tc.want.cdepth)
			for _, code := range tc.want.warnings {
				if !hasCode(res, code) {
					t.Errorf("missing warning %s (codes: %s)", code, codesOf(res))
				}
			}
		})
	}
}

func assertPtrEq[T comparable](t *testing.T, name string, got, want *T) {
	t.Helper()
	switch {
	case got == nil && want == nil:
	case got == nil:
		t.Errorf("%s = nil, want %v", name, *want)
	case want == nil:
		t.Errorf("%s = %v, want nil", name, *got)
	case *got != *want:
		t.Errorf("%s = %v, want %v", name, *got, *want)
	}
}

// withType adds one entry to a type table and returns it, so a Config literal
// can extend DefaultTypes in a single expression.
func withType(types map[string]Bump, name string, bump Bump) map[string]Bump {
	types[name] = bump
	return types
}

func hasCode(r *Result, code string) bool {
	for _, d := range r.Diagnostics {
		if d.Code == code {
			return true
		}
	}
	return false
}

func TestHeaderDirectiveEquivalences(t *testing.T) {
	t.Parallel()

	// §8.3: these pairs must produce identical directive state.
	pairs := [][2]string{
		{"feat(core)^^minor: x", "feat(core)^minor+*: x"},
		{"feat(core)^^: x", "feat(core)+*: x"},
		{"feat(core)^minor: x", "feat(core)^minor+1: x"},
		{"feat(core)^minor: x", "feat(core)^minor+direct: x"},
		{"feat(core)+all: x", "feat(core)+*: x"},
	}

	p := DefaultParser()
	for _, pair := range pairs {
		a, errA := p.ParseSubject(pair[0])
		b, errB := p.ParseSubject(pair[1])
		if errA != nil || errB != nil {
			t.Fatalf("%q / %q: %v / %v", pair[0], pair[1], errA, errB)
		}
		da, db := a.Units[0].Directives, b.Units[0].Directives
		if da.Propagate != db.Propagate || da.Depth != db.Depth {
			t.Errorf("%q -> (%s, %s); %q -> (%s, %s): not equivalent",
				pair[0], da.Propagate, da.Depth, pair[1], db.Propagate, db.Depth)
		}
	}
}

func TestHeaderDefaults(t *testing.T) {
	t.Parallel()

	p := DefaultParser()
	res, err := p.ParseSubject("feat(core): x")
	if err != nil {
		t.Fatal(err)
	}
	d := res.Units[0].Directives
	if d.Propagate != PropagatePatch {
		t.Errorf("default Propagate = %q, want patch", d.Propagate)
	}
	// §8.3: both axes default to depth 0 — a unit propagates nothing unless
	// it says so.
	if d.Depth != 0 {
		t.Errorf("default Depth = %s, want 0", d.Depth)
	}
	if d.ChannelDepth != 0 {
		t.Errorf("default ChannelDepth = %s, want 0", d.ChannelDepth)
	}
	if d.PropagateSet || d.DepthSet || d.ChannelDepthSet {
		t.Errorf("defaults must not be reported as author-stated")
	}
	if d.PropagateChannel.Word != ChannelInherit {
		t.Errorf("default Propagate-Channel = %q, want inherit", d.PropagateChannel)
	}
	want := DefaultPropagateKinds()
	if len(d.Kinds) != len(want) {
		t.Fatalf("default kinds = %v, want %v", d.Kinds, want)
	}
	for i := range want {
		if d.Kinds[i] != want[i] {
			t.Fatalf("default kinds = %v, want %v", d.Kinds, want)
		}
	}
}

func TestDepthValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in      string
		want    Depth
		wantErr bool
	}{
		{"0", 0, false},
		{"1", 1, false},
		{"direct", 1, false},
		{"3", 3, false},
		{"all", DepthAll, false},
		{"*", DepthAll, false},
		{"1024", 1024, false},
		{"1025", DepthAll, false},   // saturates
		{"999999", DepthAll, false}, // saturates
		{"01", 0, true},             // §5.3: a leading zero is E111, only "0" may start with one
		{"00", 0, true},
		{"007", 0, true},
		{"0*", 0, true},
		{"-1", 0, true},
		{"1x", 0, true},
		{"", 0, true},
		{"tw", 0, true},
	}
	for _, tc := range tests {
		got, err := parseDepthValue(tc.in, Position{Line: 1, Column: 1})
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseDepthValue(%q) = %s, want error", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseDepthValue(%q) = %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseDepthValue(%q) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

func TestChannelValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		subject string
		code    string
	}{
		{"feat(core)@beta: x", ""},
		{"feat(core)@rc-2: x", ""},
		{"feat(core)@a: x", ""},
		{"feat(core)@stable: x", ""},
		{"feat(core)@latest: x", CodeE180},
		{"feat(core)@Beta: x", CodeE181},
		{"feat(core)@1beta: x", CodeE181},
		{"feat(core)@be_ta: x", CodeE181},
		{"feat(core)@" + strings.Repeat("b", 33) + ": x", CodeE181},
		{"feat(core)@" + strings.Repeat("b", 32) + ": x", ""},
	}
	p := DefaultParser()
	for _, tc := range tests {
		res, err := p.ParseSubject(tc.subject)
		got := firstError(res)
		if got != tc.code {
			t.Errorf("ParseSubject(%q) error = %q (%v), want %q", tc.subject, got, err, tc.code)
		}
	}
}

func TestAllowedChannels(t *testing.T) {
	t.Parallel()

	p := MustNewParser(Config{AllowedChannels: []string{"beta", "rc"}})
	if _, err := p.ParseSubject("feat(core)@beta: x"); err != nil {
		t.Errorf("allowed channel rejected: %v", err)
	}
	res, err := p.ParseSubject("feat(core)@alpha: x")
	if err == nil || firstError(res) != CodeE181 {
		t.Errorf("disallowed channel = %v (%s), want E181", err, codesOf(res))
	}
	if _, err := p.ParseSubject("feat(core)@stable: x"); err != nil {
		t.Errorf("stable must always be accepted: %v", err)
	}
}

func TestLenientMode(t *testing.T) {
	t.Parallel()

	p := MustNewParser(Config{Lenient: true})

	res, err := p.ParseSubject("Feat: x")
	if err != nil {
		t.Fatalf("lenient uppercase type: %v", err)
	}
	if res.Units[0].Header.Type != "feat" {
		t.Errorf("type = %q, want feat", res.Units[0].Header.Type)
	}
	if !hasCode(res, CodeW101) {
		t.Errorf("missing W101 (codes: %s)", codesOf(res))
	}

	// A missing space is unambiguous, so lenient mode accepts it with W121.
	res, err = p.ParseSubject("feat:x")
	if err != nil {
		t.Fatalf("lenient separator: %v", err)
	}
	if res.Units[0].Header.Description != "x" {
		t.Errorf("description = %q, want x", res.Units[0].Header.Description)
	}
	if !hasCode(res, CodeW121) {
		t.Errorf("missing W121 (codes: %s)", codesOf(res))
	}
	if hasCode(res, CodeW120) {
		t.Errorf("W120 is the description-length warning, not the separator one")
	}

	// §5.5: two or more spaces stay E120 even here. The extra space is
	// indistinguishable from a description that deliberately begins with one,
	// so there is no "obvious" repair for lenient mode to apply.
	for _, in := range []string{"feat:  x", "feat:   x"} {
		res, err := p.ParseSubject(in)
		if err == nil {
			t.Errorf("lenient mode accepted %q (codes: %s)", in, codesOf(res))
			continue
		}
		if firstError(res) != CodeE120 {
			t.Errorf("%q = %s, want E120", in, codesOf(res))
		}
	}

	// Strict mode must still reject all three.
	strict := DefaultParser()
	for _, in := range []string{"Feat: x", "feat:x", "feat:   x"} {
		if _, err := strict.ParseSubject(in); err == nil {
			t.Errorf("strict mode accepted %q", in)
		}
	}
}

func TestDescriptionLength(t *testing.T) {
	t.Parallel()

	p := DefaultParser()
	long := strings.Repeat("é", 101) // 101 scalar values, 202 bytes
	res, err := p.ParseSubject("feat: " + long)
	if err != nil {
		t.Fatal(err)
	}
	if !hasCode(res, CodeW120) {
		t.Errorf("missing W120 for a 101-character description (codes: %s)", codesOf(res))
	}

	res, _ = p.ParseSubject("feat: " + strings.Repeat("é", 100))
	if hasCode(res, CodeW120) {
		t.Errorf("unexpected W120 at exactly the limit")
	}

	off := MustNewParser(Config{MaxDescriptionLength: -1})
	res, _ = off.ParseSubject("feat: " + long)
	if hasCode(res, CodeW120) {
		t.Errorf("W120 emitted with the check disabled")
	}
}

func TestUnknownType(t *testing.T) {
	t.Parallel()

	p := DefaultParser()
	res, err := p.ParseSubject("wibble(core): x")
	if err != nil {
		t.Fatalf("unknown type must be accepted by default: %v", err)
	}
	if res.Units[0].Bump != BumpNone {
		t.Errorf("bump = %s, want none", res.Units[0].Bump)
	}
	if !hasCode(res, CodeW140) {
		t.Errorf("missing W140 (codes: %s)", codesOf(res))
	}

	strict := MustNewParser(Config{StrictTypes: true})
	res, err = strict.ParseSubject("wibble(core): x")
	if err == nil || firstError(res) != CodeE140 {
		t.Errorf("strictTypes = %v (%s), want E140", err, codesOf(res))
	}

	custom := MustNewParser(Config{Types: withType(DefaultTypes(), "wibble", BumpMinor)})
	res, err = custom.ParseSubject("wibble(core): x")
	if err != nil {
		t.Fatal(err)
	}
	if res.Units[0].Bump != BumpMinor {
		t.Errorf("bump = %s, want minor", res.Units[0].Bump)
	}
}

func TestTypeBumpMapping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		subject string
		want    Bump
	}{
		{"feat(core): x", BumpMinor},
		{"fix(core): x", BumpPatch},
		{"perf(core): x", BumpPatch},
		{"revert(core): x", BumpPatch},
		{"refactor(core): x", BumpNone},
		{"docs(core): x", BumpNone},
		{"style(core): x", BumpNone},
		{"test(core): x", BumpNone},
		{"build(core): x", BumpNone},
		{"ci(core): x", BumpNone},
		{"chore(core): x", BumpNone},
		{"feat(core)!: x", BumpMajor},
		{"chore(core)!: x", BumpMajor},
		{"docs(core)!: x", BumpMajor},
		{"cancel(core): x", BumpNone},
		{"release(core)@beta: x", BumpNone},
	}
	p := DefaultParser()
	for _, tc := range tests {
		res, err := p.ParseSubject(tc.subject)
		if err != nil {
			t.Errorf("ParseSubject(%q) = %v", tc.subject, err)
			continue
		}
		if got := res.Units[0].Bump; got != tc.want {
			t.Errorf("ParseSubject(%q).Bump = %s, want %s", tc.subject, got, tc.want)
		}
	}
}

func TestScopeTermClassification(t *testing.T) {
	t.Parallel()

	p := DefaultParser()
	res, err := p.ParseSubject("feat(*,.,-legacy,@acme/*,plain): x")
	if err != nil {
		t.Fatal(err)
	}
	scopes := res.Units[0].Scopes()
	if len(scopes) != 5 {
		t.Fatalf("got %d terms, want 5", len(scopes))
	}
	if !scopes[0].IsAll() {
		t.Errorf("'*' must be workspace-wide")
	}
	if !scopes[1].IsDerived() {
		t.Errorf("'.' must be the derived set")
	}
	if !scopes[2].Exclude || scopes[2].Name != "legacy" {
		t.Errorf("'-legacy' = %+v, want an exclusion of legacy", scopes[2])
	}
	if !scopes[3].IsGlob() {
		t.Errorf("'@acme/*' must be a glob")
	}
	if scopes[4].IsGlob() || scopes[4].IsAll() || scopes[4].IsDerived() || scopes[4].Exclude {
		t.Errorf("'plain' must be an ordinary include")
	}
	if got, want := len(scopes.Includes()), 4; got != want {
		t.Errorf("includes = %d, want %d", got, want)
	}
	if got, want := len(scopes.Excludes()), 1; got != want {
		t.Errorf("excludes = %d, want %d", got, want)
	}
}

func TestScopeOverlapWarning(t *testing.T) {
	t.Parallel()

	p := DefaultParser()
	res, err := p.ParseSubject("feat(api,-api): x")
	if err != nil {
		t.Fatal(err)
	}
	if !hasCode(res, CodeW133) {
		t.Errorf("missing W133 (codes: %s)", codesOf(res))
	}
}

// TestInertDirectiveDiagnostics is §8.3b's table: W152 when the whole
// directive resolves to nothing, W201 when a value was supplied and the depth
// discarded it, and never both for one axis.
func TestInertDirectiveDiagnostics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		subject string
		want    []string // exactly the propagation codes expected
	}{
		// --- bump axis ---
		{"#38 none at all depths", "feat(core)^none+*: x", []string{CodeW152}},
		{"#38 doubled caret none", "feat(core)^^none: x", []string{CodeW152}},
		{"#38 bare +0", "feat(core)+0: x", []string{CodeW152}},
		{"#38g value discarded by depth 0", "feat(core)^minor+0: x", []string{CodeW201}},
		{"#38g inherit counts as a value", "feat(core)^inherit+0: x", []string{CodeW201}},
		{"#38a release is silenced by its type", "release(core)^minor: x", nil},
		{"a real bump at a real depth", "feat(core)^minor+2: x", nil},
		// "^none" alone leaves the depth at the caret's implied 1, so the
		// directive still resolves to "propagate nothing" — redundant, not
		// inert.
		{"#38 caret none", "feat(core)^none: x", []string{CodeW152}},

		// --- channel axis, mirroring the bump axis exactly ---
		{"#38d bare ++0", "feat(core)++0: x", []string{CodeW152}},
		{"#38d @@none", "feat(core)@@none: x", []string{CodeW152}},
		{"#38d @@none++*", "feat(core)@@none++*: x", []string{CodeW152}},
		{"#38h value discarded by channel depth 0", "feat(core)@@beta++0: x", []string{CodeW201}},
		{"a real channel at a real depth", "feat(core)@@beta++2: x", nil},

		// --- both axes at once ---
		{"both inert", "feat(core)^minor+0@@beta++0: x", []string{CodeW201, CodeW201}},
		{"one of each", "feat(core)^none+*@@beta++0: x", []string{CodeW152, CodeW201}},
	}

	p := DefaultParser()
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			res, err := p.ParseSubject(tc.subject)
			if err != nil {
				t.Fatalf("%v (codes: %s)", err, codesOf(res))
			}
			var got []string
			for _, d := range res.Diagnostics {
				if d.Code == CodeW152 || d.Code == CodeW201 {
					got = append(got, d.Code)
				}
			}
			if len(got) != len(tc.want) {
				t.Fatalf("propagation codes = %v, want %v (all codes: %s)",
					got, tc.want, codesOf(res))
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("propagation codes = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

// TestInertChannelTransition covers W207: a transition with equal sides moves
// nobody, on either channel key (§11.2).
func TestInertChannelTransition(t *testing.T) {
	t.Parallel()

	p := DefaultParser()
	for _, subject := range []string{
		"feat(core)@beta>beta: x",
		"feat(core)@@beta>beta++1: x",
		"feat(core)@rc>rc@@beta>beta++1: x",
	} {
		res, err := p.ParseSubject(subject)
		if err != nil {
			t.Errorf("%q: %v (codes: %s)", subject, err, codesOf(res))
			continue
		}
		if !hasCode(res, CodeW207) {
			t.Errorf("%q: missing W207 (codes: %s)", subject, codesOf(res))
		}
	}

	// A transition that actually moves something must stay silent.
	res, err := p.ParseSubject("feat(core)@@beta>rc++1: x")
	if err != nil {
		t.Fatal(err)
	}
	if hasCode(res, CodeW207) {
		t.Errorf("unexpected W207 (codes: %s)", codesOf(res))
	}
}

func TestDiagnosticPositions(t *testing.T) {
	t.Parallel()

	p := DefaultParser()
	// "feat(core)^med: x" — the bad value starts at byte 11 (1-based column 12).
	res, _ := p.ParseSubject("feat(core)^med: x")
	if len(res.Errors()) != 1 {
		t.Fatalf("codes: %s", codesOf(res))
	}
	got := res.Errors()[0].Position
	if got.Line != 1 || got.Column != 12 {
		t.Errorf("position = %s, want 1:12", got)
	}
}

func TestSubjectRejectsMultipleLines(t *testing.T) {
	t.Parallel()

	p := DefaultParser()
	res, err := p.ParseSubject("feat: x\n\nbody")
	if err == nil || firstError(res) != CodeE100 {
		t.Errorf("ParseSubject with a body = %v (%s), want E100", err, codesOf(res))
	}
}
