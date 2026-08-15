package ccme

import "testing"

func TestFooterKeyEnd(t *testing.T) {
	t.Parallel()

	tests := []struct {
		line string
		want int
	}{
		{"BREAKING CHANGE: gone", 15},
		{"BREAKING-CHANGE: gone", 15},
		// Case is significant for BREAKING CHANGE alone (§8.1.1). The
		// hyphenated miscasing is still a well-formed footer key, just not a
		// breaking one; the spaced miscasing is not a footer at all.
		{"breaking-change: gone", 15},
		{"Breaking change: gone", -1},
		{"breaking change: gone", -1},
		{"Propagate: minor", 9},
		{"Signed-off-by: A <a%example.com>", 13},
		{"Closes #12", 6},
		{"Refs: #4", 4},
		{"", -1},
		{"just a sentence", -1},
		{"Key:no-space", -1},
		{"Key :value", -1},
		{"  Indented: value", -1},
		{"BREAKING CHANGE:", 15}, // empty value is legal, W157 (edge case 19e)
		{"BREAKING-CHANGE:", 15},
		{"Propagate:", -1}, // only BREAKING CHANGE may have an empty value
		{"Key: ", 3},
	}
	for _, tc := range tests {
		if got := footerKeyEnd(tc.line); got != tc.want {
			t.Errorf("footerKeyEnd(%q) = %d, want %d", tc.line, got, tc.want)
		}
	}
}

func TestFooterBlockDetection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		lines []string
		want  bool
	}{
		{"single trailer", []string{"Propagate: minor"}, true},
		{"two trailers", []string{"Propagate: minor", "Channel: beta"}, true},
		{"indented continuation", []string{"Propagate: minor", "  continued"}, true},
		{"breaking multiline", []string{"BREAKING CHANGE: a", "unindented free text"}, true},
		{"hyphenated breaking multiline", []string{"BREAKING-CHANGE: a", "unindented free text"}, true},
		{"miscased does not consume", []string{"breaking-change: a", "unindented prose"}, false},
		{"trailing prose", []string{"Propagate: minor", "unindented prose"}, false},
		{"prose first", []string{"prose", "Propagate: minor"}, false},
		{"empty", nil, false},
	}
	for _, tc := range tests {
		if got := isFooterBlock(tc.lines); got != tc.want {
			t.Errorf("%s: isFooterBlock(%q) = %v, want %v", tc.name, tc.lines, got, tc.want)
		}
	}
}

func TestNearlyFooterBlockWarning(t *testing.T) {
	t.Parallel()

	p := DefaultParser()
	res, err := p.Parse("fix(core): a\n\nCloses: #12\nthis line is not footer-shaped")
	if err != nil {
		t.Fatal(err)
	}
	if !hasCode(res, CodeW151) {
		t.Errorf("missing W151 (codes: %s)", codesOf(res))
	}
	if len(res.Units[0].Footers) != 0 {
		t.Errorf("the paragraph must be body, got footers %+v", res.Units[0].Footers)
	}
	if res.Units[0].Body == "" {
		t.Errorf("the paragraph must become body text")
	}
}

func TestOnlyTheLastParagraphIsConsideredForFooters(t *testing.T) {
	t.Parallel()

	p := DefaultParser()
	res, err := p.Parse("fix(core): a\n\nPropagate: minor\n\nordinary body paragraph")
	if err != nil {
		t.Fatal(err)
	}
	u := res.Units[0]
	if len(u.Footers) != 0 {
		t.Errorf("mid-message trailers must stay body text, got %+v", u.Footers)
	}
	if u.Directives.PropagateSet {
		t.Errorf("a mid-message trailer must not set a directive")
	}
}

func TestFooterDirectives(t *testing.T) {
	t.Parallel()

	const msg = `feat(@acme/core): new plugin API

Propagate: minor
Propagate-Depth: all
Propagate-Scope: @acme/*, -@acme/experimental-*
Propagate-Channel: stable
Channel: beta
Release-As: 4.0.0
Reverts: 4f2a1c9`

	p := DefaultParser()
	res, err := p.Parse(msg)
	if err != nil {
		t.Fatalf("%v (codes: %s)", err, codesOf(res))
	}
	d := res.Units[0].Directives

	if d.Propagate != PropagateMinor || !d.PropagateSet {
		t.Errorf("Propagate = %q", d.Propagate)
	}
	if !d.Depth.IsAll() || !d.DepthSet {
		t.Errorf("Propagate-Depth = %s", d.Depth)
	}
	// §8.4 removed the per-unit override: the edge kinds always come from
	// configuration now.
	if len(d.Kinds) != len(DefaultPropagateKinds()) {
		t.Errorf("Kinds = %v, want the configured default", d.Kinds)
	}
	if got := d.PropagateScope.String(); got != "@acme/*,-@acme/experimental-*" {
		t.Errorf("Propagate-Scope = %q", got)
	}
	if len(d.PropagateScope.Excludes()) != 1 {
		t.Errorf("Propagate-Scope excludes = %v", d.PropagateScope.Excludes())
	}
	if d.PropagateChannel.To != ChannelStable {
		t.Errorf("Propagate-Channel = %q", d.PropagateChannel)
	}
	if d.Channel.To != "beta" || !d.ChannelSet {
		t.Errorf("Channel = %q", d.Channel)
	}
	if d.ReleaseAs == nil || d.ReleaseAs.Kind != ReleaseAsExact || d.ReleaseAs.Version.String() != "4.0.0" {
		t.Errorf("Release-As = %+v", d.ReleaseAs)
	}
	if len(d.Reverts) != 1 || d.Reverts[0] != "4f2a1c9" {
		t.Errorf("Reverts = %v", d.Reverts)
	}
}

func TestFooterKeysAreCaseInsensitiveButNotHyphenInsensitive(t *testing.T) {
	t.Parallel()

	p := DefaultParser()

	res, err := p.Parse("feat(core): a\n\npropagate-depth: 3")
	if err != nil {
		t.Fatal(err)
	}
	if res.Units[0].Directives.Depth != 3 {
		t.Errorf("lowercased key not recognised: %+v", res.Units[0].Directives)
	}

	res, err = p.Parse("feat(core): a\n\nPropagateDepth: 3")
	if err != nil {
		t.Fatal(err)
	}
	if res.Units[0].Directives.DepthSet {
		t.Errorf("PropagateDepth must not match Propagate-Depth")
	}
	if !hasCode(res, CodeW150) {
		t.Errorf("missing W150 (codes: %s)", codesOf(res))
	}
}

func TestInvalidFooterValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		footer string
		code   string
	}{
		{"Propagate: med", CodeE151},
		{"Propagate-Depth: two", CodeE151},
		{"Propagate-Channel-Depth: two", CodeE151},
		{"Propagate-Channel-Scope: a b", CodeE151},
		{"Propagate-Scope: a b", CodeE151},
		{"Propagate-Scope: a,,b", CodeE151},
		{"Release-As: 1.2", CodeE151},
		{"Release-As: v1.2.3", CodeE151},
		{"Release-As: 01.2.3", CodeE151},
		// §8.6: Release-As has no bump form; edge case 73i.
		{"Release-As: patch", CodeE151},
		{"Release-As: minor", CodeE151},
		{"Release-As: major", CodeE151},
		{"Channel: latest", CodeE180},
		{"Channel: LATEST", CodeE181},
		{"Propagate-Channel: Beta", CodeE181},
	}
	p := DefaultParser()
	for _, tc := range tests {
		res, err := p.Parse("feat(core): a\n\n" + tc.footer)
		if err == nil || firstError(res) != tc.code {
			t.Errorf("%q = %v (%s), want %s", tc.footer, err, codesOf(res), tc.code)
		}
	}
}

func TestValidReleaseAsForms(t *testing.T) {
	t.Parallel()

	tests := []struct {
		value string
		kind  ReleaseAsKind
	}{
		{"4.0.0", ReleaseAsExact},
		{"1.0.0-beta.4", ReleaseAsExact},
		{"none", ReleaseAsNone},
		{"auto", ReleaseAsAuto},
	}
	p := DefaultParser()
	for _, tc := range tests {
		res, err := p.Parse("fix(core): a\n\nRelease-As: " + tc.value)
		if err != nil {
			t.Errorf("Release-As: %s = %v", tc.value, err)
			continue
		}
		ra := res.Units[0].Directives.ReleaseAs
		if ra == nil || ra.Kind != tc.kind {
			t.Errorf("Release-As: %s = %+v", tc.value, ra)
			continue
		}
		// §13.6: bumpOf(unit) comes from the type and "!" alone. No form of
		// Release-As touches it — in particular a hold retains its unit's
		// bump, because the pending work accumulates (§8.6.1).
		if res.Units[0].Bump != BumpPatch {
			t.Errorf("Release-As: %s changed the fix unit's bump to %s",
				tc.value, res.Units[0].Bump)
		}
	}
}

func TestReleaseAsExactOnMultiPackageScope(t *testing.T) {
	t.Parallel()

	p := DefaultParser()
	for _, header := range []string{"release(core,cli): x", "release(*): x"} {
		res, err := p.Parse(header + "\n\nRelease-As: 4.0.0")
		if err == nil || firstError(res) != CodeE154 {
			t.Errorf("%q = %v (%s), want E154", header, err, codesOf(res))
		}
	}
	for _, header := range []string{"release(core): x", "release(core,-cli): x"} {
		if _, err := p.Parse(header + "\n\nRelease-As: 4.0.0"); err != nil {
			t.Errorf("%q = %v, want success", header, err)
		}
	}
}

func TestInlineFooterReconciliation(t *testing.T) {
	t.Parallel()

	strict := DefaultParser()

	t.Run("identical is redundant", func(t *testing.T) {
		res, err := strict.Parse("feat(core)^minor: a\n\nPropagate: minor")
		if err != nil {
			t.Fatal(err)
		}
		if !hasCode(res, CodeW110) {
			t.Errorf("missing W110 (codes: %s)", codesOf(res))
		}
		if res.Units[0].Directives.Propagate != PropagateMinor {
			t.Errorf("Propagate = %q", res.Units[0].Directives.Propagate)
		}
	})

	t.Run("different is an error", func(t *testing.T) {
		res, err := strict.Parse("feat(core)^minor: a\n\nPropagate: major")
		if err == nil || firstError(res) != CodeE112 {
			t.Errorf("= %v (%s), want E112", err, codesOf(res))
		}
	})

	t.Run("lenient lets the footer win", func(t *testing.T) {
		lenient := MustNewParser(Config{Lenient: true})
		res, err := lenient.Parse("feat(core)^minor: a\n\nPropagate: major")
		if err != nil {
			t.Fatalf("%v (codes: %s)", err, codesOf(res))
		}
		if !hasCode(res, CodeW112) {
			t.Errorf("missing W112 (codes: %s)", codesOf(res))
		}
		if res.Units[0].Directives.Propagate != PropagateMajor {
			t.Errorf("Propagate = %q, want major", res.Units[0].Directives.Propagate)
		}
	})

	t.Run("channel conflict", func(t *testing.T) {
		res, err := strict.Parse("feat(core)%beta: a\n\nChannel: rc")
		if err == nil || firstError(res) != CodeE112 {
			t.Errorf("= %v (%s), want E112", err, codesOf(res))
		}
	})

	t.Run("depth conflict", func(t *testing.T) {
		res, err := strict.Parse("feat(core)+2: a\n\nPropagate-Depth: 3")
		if err == nil || firstError(res) != CodeE112 {
			t.Errorf("= %v (%s), want E112", err, codesOf(res))
		}
	})

	t.Run("doubled caret and footer depth agree", func(t *testing.T) {
		res, err := strict.Parse("feat(core)^^: a\n\nPropagate-Depth: all")
		if err != nil {
			t.Fatalf("%v (codes: %s)", err, codesOf(res))
		}
		if !hasCode(res, CodeW110) {
			t.Errorf("missing W110 (codes: %s)", codesOf(res))
		}
	})
}

func TestCancelRejectsDirectives(t *testing.T) {
	t.Parallel()

	p := DefaultParser()
	inputs := []struct {
		msg  string
		code string
	}{
		{"cancel(core)!: x", CodeE170},
		{"cancel(core)^minor: x", CodeE171},
		{"cancel(core)%beta: x", CodeE171},
		{"cancel(core)+2: x", CodeE171},
		{"cancel(core): x\n\nChannel: beta", CodeE171},
		{"cancel(core): x\n\nBREAKING CHANGE: nope", CodeE171},
		{"cancel(core): x\n\nRelease-As: none", CodeE171},
	}
	for _, tc := range inputs {
		res, err := p.Parse(tc.msg)
		if err == nil || firstError(res) != tc.code {
			t.Errorf("%q = %v (%s), want %s", tc.msg, err, codesOf(res), tc.code)
		}
	}

	// A cancel unit with a scope-set, a body and an ignored trailer is legal.
	res, err := p.Parse("cancel(*): reset release state\n\nAdopting CCME.\n\nSigned-off-by: A <a%x>")
	if err != nil {
		t.Errorf("%v (codes: %s)", err, codesOf(res))
	}
}

func TestReleaseUnits(t *testing.T) {
	t.Parallel()

	p := DefaultParser()

	res, err := p.Parse("release(cli)%stable: graduate 2.0")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Units[0].IsRelease() || !res.Units[0].IsControl() {
		t.Errorf("release unit not recognised")
	}
	if res.Units[0].Directives.Channel.To != ChannelStable {
		t.Errorf("channel = %q, want stable", res.Units[0].Directives.Channel)
	}
	if hasCode(res, CodeW141) {
		t.Errorf("unexpected W141")
	}

	res, err = p.Parse("release(cli): nothing to do")
	if err != nil {
		t.Fatal(err)
	}
	if !hasCode(res, CodeW141) {
		t.Errorf("missing W141 (codes: %s)", codesOf(res))
	}

	res, err = p.Parse("release(cli): x\n\nBREAKING CHANGE: nope")
	if err == nil || firstError(res) != CodeE141 {
		t.Errorf("= %v (%s), want E141", err, codesOf(res))
	}
}

func TestIssueTrailersAreNotUnknown(t *testing.T) {
	t.Parallel()

	p := DefaultParser()
	res, err := p.Parse("fix(core): a\n\nCloses #12\nFixes: #13\nRefs: #14\nResolves: #15")
	if err != nil {
		t.Fatal(err)
	}
	if hasCode(res, CodeW150) {
		t.Errorf("issue trailers must not produce W150 (codes: %s)", codesOf(res))
	}
	if len(res.Units[0].Footers) != 4 {
		t.Fatalf("footers = %+v, want 4", res.Units[0].Footers)
	}
	if res.Units[0].Footers[0].Value != "#12" {
		t.Errorf("issue value = %q, want #12", res.Units[0].Footers[0].Value)
	}
	for _, f := range res.Units[0].Footers {
		if !f.IssueReference {
			t.Errorf("%q not flagged as an issue reference", f.Key)
		}
	}
}

func TestNonBreakingContinuationIsJoinedWithSpace(t *testing.T) {
	t.Parallel()

	p := DefaultParser()
	res, err := p.Parse("feat(core): a\n\nPropagate-Scope: @acme/*,\n  -@acme/internal-*")
	if err != nil {
		t.Fatalf("%v (codes: %s)", err, codesOf(res))
	}
	got := res.Units[0].Directives.PropagateScope.String()
	if got != "@acme/*,-@acme/internal-*" {
		t.Errorf("Propagate-Scope = %q", got)
	}
}

func TestCustomTrailerLists(t *testing.T) {
	t.Parallel()

	p := MustNewParser(Config{
		MessageLevelTrailers: []string{"Ticket"},
		IssueTrailers:        []string{},
	})
	res, err := p.Parse("fix(core): a\n\nTicket: ABC-1\nCloses: #2")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Units[0].Footers[0].MessageLevel {
		t.Errorf("Ticket should be message-level")
	}
	if res.Units[0].Footers[1].IssueReference {
		t.Errorf("Closes should no longer be an issue trailer")
	}
	if !hasCode(res, CodeW150) {
		t.Errorf("Closes should now be an unknown key (codes: %s)", codesOf(res))
	}
}

// TestImpliedDepthYieldsToFooter covers §8.3's distinction between a depth a
// sigil implies and one the author wrote. "^" and "%%" state a default, so a
// footer naming a depth wins silently; "+N" and "++N" are the author's own
// number, so a differing footer is a contradiction; and "^^" asserts all, so
// even a footer must agree with it.
func TestImpliedDepthYieldsToFooter(t *testing.T) {
	t.Parallel()

	p := DefaultParser()
	lenient := MustNewParser(Config{Lenient: true})

	tests := []struct {
		name    string
		message string
		depth   Depth
		cdepth  Depth
		code    string // "" means no diagnostic at all
		err     bool
	}{
		{
			name:    "caret implies, footer states",
			message: "feat(core)^minor: a\n\nPropagate-Depth: 3",
			depth:   3,
		},
		{
			name:    "double at implies, footer states",
			message: "feat(core)%%beta: a\n\nPropagate-Channel-Depth: 3",
			depth:   0,
			cdepth:  3,
		},
		{
			name:    "explicit +N contradicted",
			message: "feat(core)+2: a\n\nPropagate-Depth: 3",
			depth:   3,
			code:    CodeE112,
			err:     true,
		},
		{
			name:    "explicit ++N contradicted",
			message: "feat(core)++2: a\n\nPropagate-Channel-Depth: 3",
			cdepth:  3,
			code:    CodeE112,
			err:     true,
		},
		{
			name:    "explicit +N restated",
			message: "feat(core)+3: a\n\nPropagate-Depth: 3",
			depth:   3,
			code:    CodeW110,
		},
		{
			name:    "double caret contradicted by a footer",
			message: "feat(core)^^minor: a\n\nPropagate-Depth: 2",
			depth:   2,
			code:    CodeE113,
			err:     true,
		},
		{
			name:    "double caret restated by a footer",
			message: "feat(core)^^minor: a\n\nPropagate-Depth: all",
			depth:   DepthAll,
			code:    CodeW110,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			res, err := p.Parse(tc.message)
			if (err != nil) != tc.err {
				t.Fatalf("err = %v, want error: %v (codes: %s)", err, tc.err, codesOf(res))
			}
			d := res.Units[0].Directives
			if d.Depth != tc.depth {
				t.Errorf("Depth = %s, want %s", d.Depth, tc.depth)
			}
			if d.ChannelDepth != tc.cdepth {
				t.Errorf("ChannelDepth = %s, want %s", d.ChannelDepth, tc.cdepth)
			}
			switch {
			case tc.code == "":
				if len(res.Diagnostics) != 0 {
					t.Errorf("want a silent parse, got %s", codesOf(res))
				}
			case !hasCode(res, tc.code):
				t.Errorf("missing %s (codes: %s)", tc.code, codesOf(res))
			}

			// Lenient mode downgrades only E112; E113 is a contradiction the
			// author has to resolve either way (§16).
			if tc.code == CodeE112 {
				res, err := lenient.Parse(tc.message)
				if err != nil {
					t.Errorf("lenient: %v (codes: %s)", err, codesOf(res))
				}
				if !hasCode(res, CodeW112) {
					t.Errorf("lenient: missing W112 (codes: %s)", codesOf(res))
				}
			}
		})
	}
}

// TestFooterScopeValueCharset pins the §5.2 charset in the footer spelling:
// multibyte terms are legal scope-chars, ASCII control characters are not.
func TestFooterScopeValueCharset(t *testing.T) {
	t.Parallel()

	p := DefaultParser()
	res, err := p.Parse("feat^: a\n\nPropagate-Scope: cafés,приложение")
	if err != nil {
		t.Fatalf("multibyte footer scopes must parse: %v (%s)", err, codesOf(res))
	}
	if got := res.Units[0].Directives.PropagateScope.String(); got != "cafés,приложение" {
		t.Errorf("Propagate-Scope = %q", got)
	}

	res, err = p.Parse("feat^: a\n\nPropagate-Scope: caf\vés")
	if err == nil || !hasCode(res, CodeE151) {
		t.Errorf("control char in a footer scope = %v (%s), want E151", err, codesOf(res))
	}
}

// TestMiscasedBreakingInsideFreeFormValueIsNotW155 is the regression fence
// for the false positive: a "Breaking change:" line *inside* a BREAKING
// CHANGE footer's free-form value is part of the breaking change already in
// force, not a failed attempt to declare one.
func TestMiscasedBreakingInsideFreeFormValueIsNotW155(t *testing.T) {
	t.Parallel()

	p := DefaultParser()
	res, err := p.Parse("feat: a\n\nBREAKING CHANGE: the API moved.\nBreaking change: this line is value text.")
	if err != nil {
		t.Fatalf("%v (codes: %s)", err, codesOf(res))
	}
	if hasCode(res, CodeW155) {
		t.Errorf("W155 fired inside a free-form BREAKING CHANGE value (codes: %s)", codesOf(res))
	}
	if res.Units[0].Directives.BreakingChange == "" {
		t.Error("the breaking change itself must still be in force")
	}

	// The fence must not weaken the real case: a miscased line that opens the
	// final paragraph is still the silent failure W155 exists to catch.
	res, _ = p.Parse("feat: a\n\nBreaking change: this was meant to be breaking.")
	if !hasCode(res, CodeW155) {
		t.Errorf("the real miscasing must still warn (codes: %s)", codesOf(res))
	}
}
