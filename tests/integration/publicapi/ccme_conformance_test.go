package publicapi_test

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	ccme "github.com/yohimik/dispat/pkg/ccme"
)

// shaFull and shaShort are the two commit-id shapes a correction footer may
// name: the abbreviated minimum of §7.4.1 and a full 40-character id.
const (
	shaShort = "abc1234"
	shaFull  = "0123456789abcdef0123456789abcdef01234567"
)

// ccmeVector is one conformance scenario: a message, the parser that reads it,
// and the diagnostic codes the specification requires it to raise.
type ccmeVector struct {
	name    string
	cfg     *ccme.Config
	subject bool
	message string
	want    []string
	absent  []string
	// invalid asserts the message contributes nothing.
	invalid bool
	// check runs extra assertions against the result.
	check func(t *testing.T, res *ccme.Result)
}

func runCCMEVectors(t *testing.T, vectors []ccmeVector) {
	t.Helper()
	for _, v := range vectors {
		t.Run(v.name, func(t *testing.T) {
			cfg := ccme.Config{}
			if v.cfg != nil {
				cfg = *v.cfg
			}
			p, err := ccme.NewParser(cfg)
			if err != nil {
				t.Fatalf("NewParser: %v", err)
			}
			var res *ccme.Result
			if v.subject {
				res, err = p.ParseSubject(v.message)
			} else {
				res, err = p.Parse(v.message)
			}
			if res == nil {
				t.Fatal("Parse returned a nil Result")
			}
			codes := res.Codes()
			for _, want := range v.want {
				if !slices.Contains(codes, want) {
					t.Errorf("missing %s: codes = %v", want, codes)
				}
			}
			for _, absent := range v.absent {
				if slices.Contains(codes, absent) {
					t.Errorf("unexpected %s: codes = %v", absent, codes)
				}
			}
			if v.invalid != res.IsInvalid() {
				t.Errorf("IsInvalid() = %v, want %v (codes = %v)", res.IsInvalid(), v.invalid, codes)
			}
			//nolint:staticcheck // the deprecated alias is part of the published surface.
			if res.HasErrors() != res.IsInvalid() {
				t.Errorf("HasErrors() disagrees with IsInvalid()")
			}
			if (err != nil) != v.invalid {
				t.Errorf("err = %v, want invalid = %v", err, v.invalid)
			}
			if err != nil {
				var perr *ccme.ParseError
				if !errors.As(err, &perr) {
					t.Fatalf("error is not a *ccme.ParseError: %T", err)
				}
				if len(perr.Codes()) != len(res.Errors()) {
					t.Errorf("ParseError.Codes() = %v, Errors() = %d", perr.Codes(), len(res.Errors()))
				}
				if perr.Error() == "" {
					t.Error("ParseError.Error() is empty")
				}
			}
			for _, d := range res.Diagnostics {
				if d.Position.Line < 1 || d.Position.Column < 1 {
					t.Errorf("diagnostic %s carries a non-1-based position %s", d.Code, d.Position)
				}
				if !ccme.IsDiagnosticCode(d.Code) {
					t.Errorf("diagnostic %s is not one this package claims to emit", d.Code)
				}
				if d.String() == "" {
					t.Errorf("diagnostic %s renders empty", d.Code)
				}
				if d.IsError() != (d.Severity == ccme.SeverityError) {
					t.Errorf("diagnostic %s: IsError disagrees with Severity", d.Code)
				}
			}
			if v.check != nil {
				v.check(t, res)
			}
		})
	}
}

// TestPublicAPICCMEHeaderGrammarConformance drives every §5 header rule through
// the exported parser: the type charset, the scope-set grammar and its term
// cap, the breaking marker, and the ": " separator under both strictness modes.
func TestPublicAPICCMEHeaderGrammarConformance(t *testing.T) {
	lenient := ccme.Config{Lenient: true}
	twoTerms := ccme.Config{Limits: ccme.Limits{ScopeTermsPerUnit: 2}}
	oneTerm := ccme.Config{Limits: ccme.Limits{ScopeTermsPerUnit: 1}}
	noLimit := ccme.Config{MaxDescriptionLength: -1}
	shortDesc := ccme.Config{MaxDescriptionLength: 4}

	runCCMEVectors(t, []ccmeVector{
		{
			name:    "a scoped header parses into its terms",
			message: "feat(app,core,-core,*,.): add a thing",
			want:    []string{ccme.CodeW133},
			absent:  []string{ccme.CodeE100},
			check: func(t *testing.T, res *ccme.Result) {
				u := res.Units[0]
				if !u.IsScopeExplicit() {
					t.Error("IsScopeExplicit() = false for a written scope-set")
				}
				//nolint:staticcheck // the deprecated alias is part of the published surface.
				if u.HasExplicitScope() != u.IsScopeExplicit() {
					t.Error("HasExplicitScope disagrees with IsScopeExplicit")
				}
				names := u.Scopes().Names()
				if want := []string{"app", "core", "core", "*", "."}; !slices.Equal(names, want) {
					t.Errorf("Names() = %v, want %v", names, want)
				}
				if got := u.Scopes().String(); got != "app,core,-core,*,." {
					t.Errorf("ScopeSet.String() = %q", got)
				}
				if got := len(u.Scopes().Includes()); got != 4 {
					t.Errorf("Includes() = %d terms, want 4", got)
				}
				excl := u.Scopes().Excludes()
				if len(excl) != 1 || excl[0].Name != "core" || !excl[0].Exclude {
					t.Errorf("Excludes() = %#v", excl)
				}
				if excl[0].String() != "-core" {
					t.Errorf("ScopeTerm.String() = %q, want the raw term", excl[0].String())
				}
				if !u.Scopes()[3].IsAll() || !u.Scopes()[3].IsGlob() {
					t.Error("the * term is neither all nor a glob")
				}
				if !u.Scopes()[4].IsDerived() {
					t.Error("the . term is not derived")
				}
				if u.Bump != ccme.BumpMinor || res.Bump() != ccme.BumpMinor {
					t.Errorf("bump = %v / %v, want minor", u.Bump, res.Bump())
				}
			},
		},
		{
			name:    "an unscoped header derives its packages",
			message: "fix: repair a thing",
			check: func(t *testing.T, res *ccme.Result) {
				if res.Units[0].IsScopeExplicit() {
					t.Error("IsScopeExplicit() = true without parentheses")
				}
				if res.Warnings() != nil {
					t.Errorf("Warnings() = %v, want none", res.Warnings())
				}
				if res.Errors() != nil {
					t.Errorf("Errors() = %v, want none", res.Errors())
				}
			},
		},
		{
			name:    "a lone exclusion marker is a literal name",
			message: "fix(-): repair a thing",
			check: func(t *testing.T, res *ccme.Result) {
				term := res.Units[0].Scopes()[0]
				if term.Exclude || term.Name != "-" {
					t.Errorf("term = %#v, want the literal name %q", term, "-")
				}
			},
		},
		{
			name:    "BREAKING CHANGE written as a header is not a type",
			message: "BREAKING CHANGE: everything moved",
			want:    []string{ccme.CodeE100},
			invalid: true,
		},
		{
			name:    "the hyphenated breaking alias is not a type either",
			message: "BREAKING-CHANGE: everything moved",
			want:    []string{ccme.CodeE100},
			invalid: true,
		},
		{
			name:    "an uppercase type is refused",
			message: "Feat: add a thing",
			want:    []string{ccme.CodeE101},
			invalid: true,
		},
		{
			name:    "a header not beginning with a letter is E100",
			message: "1feat: add a thing",
			want:    []string{ccme.CodeE100},
			invalid: true,
		},
		{
			name:    "an uppercase letter inside the type is refused",
			message: "featX: add a thing",
			want:    []string{ccme.CodeE101},
			invalid: true,
		},
		{
			name:    "a digit inside the type is an illegal character",
			message: "feat9: add a thing",
			want:    []string{ccme.CodeE101},
			invalid: true,
		},
		{
			name:    "lenient mode lowercases a miscased type",
			cfg:     &lenient,
			message: "FeAt: add a thing",
			want:    []string{ccme.CodeW101},
			check: func(t *testing.T, res *ccme.Result) {
				if got := res.Units[0].Header.Type; got != "feat" {
					t.Errorf("type = %q, want feat", got)
				}
			},
		},
		{
			name:    "an unterminated scope-set is E103",
			message: "feat(app",
			want:    []string{ccme.CodeE103},
			invalid: true,
		},
		{
			name:    "a colon inside a scope-set is E103",
			message: "feat(app: add a thing",
			want:    []string{ccme.CodeE103},
			invalid: true,
		},
		{
			name:    "nested parentheses are E103",
			message: "feat(app(core)): add a thing",
			want:    []string{ccme.CodeE103},
			invalid: true,
		},
		{
			name:    "a second scope-set is E103",
			message: "feat(app)(core): add a thing",
			want:    []string{ccme.CodeE103},
			invalid: true,
		},
		{
			name:    "an empty scope-set is E104",
			message: "feat(): add a thing",
			want:    []string{ccme.CodeE104},
			invalid: true,
		},
		{
			name:    "an empty trailing scope term is E104",
			message: "feat(app,): add a thing",
			want:    []string{ccme.CodeE104},
			invalid: true,
		},
		{
			name:    "an empty leading scope term is E104",
			message: "feat(,app): add a thing",
			want:    []string{ccme.CodeE104},
			invalid: true,
		},
		{
			name:    "a space inside a scope term is E102",
			message: "feat(app core): add a thing",
			want:    []string{ccme.CodeE102},
			invalid: true,
		},
		{
			name:    "a tab inside a scope term is E102",
			message: "feat(app\tcore): add a thing",
			want:    []string{ccme.CodeE102},
			invalid: true,
		},
		{
			name:    "a control character inside a scope term is E102",
			message: "feat(app\x01core): add a thing",
			want:    []string{ccme.CodeE102},
			invalid: true,
		},
		{
			name:    "whitespace after a comma is permitted",
			message: "feat(app, core): add a thing",
			check: func(t *testing.T, res *ccme.Result) {
				if want := []string{"app", "core"}; !slices.Equal(res.Units[0].Scopes().Names(), want) {
					t.Errorf("Names() = %v, want %v", res.Units[0].Scopes().Names(), want)
				}
			},
		},
		{
			name:    "the scope-term cap is reached at a closing parenthesis",
			cfg:     &twoTerms,
			message: "feat(a,b,c): add a thing",
			want:    []string{ccme.CodeE158},
			invalid: true,
		},
		{
			name:    "the scope-term cap is reached at a comma",
			cfg:     &oneTerm,
			message: "feat(a,b,c): add a thing",
			want:    []string{ccme.CodeE158},
			invalid: true,
		},
		{
			name:    "a missing separator at end of line is E120",
			message: "feat",
			want:    []string{ccme.CodeE120},
			invalid: true,
		},
		{
			name:    "a character where the colon belongs is E120",
			message: "feat!x: add a thing",
			want:    []string{ccme.CodeE120},
			invalid: true,
		},
		{
			name:    "a colon with nothing after it is E121",
			message: "feat:",
			want:    []string{ccme.CodeE121},
			invalid: true,
		},
		{
			name:    "a missing space after the colon is E120",
			message: "feat:add a thing",
			want:    []string{ccme.CodeE120},
			invalid: true,
		},
		{
			name:    "lenient mode accepts a missing space after the colon",
			cfg:     &lenient,
			message: "feat:add a thing",
			want:    []string{ccme.CodeW121},
			check: func(t *testing.T, res *ccme.Result) {
				if got := res.Units[0].Header.Description; got != "add a thing" {
					t.Errorf("description = %q", got)
				}
			},
		},
		{
			name:    "two spaces after the colon stay E120 under lenient mode",
			cfg:     &lenient,
			message: "feat:  add a thing",
			want:    []string{ccme.CodeE120},
			invalid: true,
		},
		{
			name:    "a description over the limit warns",
			cfg:     &shortDesc,
			message: "feat: far too long a description",
			want:    []string{ccme.CodeW120},
		},
		{
			name:    "a negative limit disables the description check",
			cfg:     &noLimit,
			message: "feat: " + strings.Repeat("x", 400),
			absent:  []string{ccme.CodeW120},
		},
		{
			name:    "the breaking marker raises the bump to major",
			message: "fix(app)!: repair a thing",
			check: func(t *testing.T, res *ccme.Result) {
				u := res.Units[0]
				if !u.Breaking || !u.Header.Breaking {
					t.Error("the breaking marker was not recorded")
				}
				if u.TypeBump != ccme.BumpPatch || u.Bump != ccme.BumpMajor {
					t.Errorf("TypeBump = %v, Bump = %v", u.TypeBump, u.Bump)
				}
			},
		},
		{
			name:    "a blank line is required between header and body",
			message: "feat: add a thing\nno blank line",
			want:    []string{ccme.CodeE100},
			invalid: true,
		},
	})
}

// TestPublicAPICCMEDirectiveAxesConformance drives §5.3's two propagation axes
// and §11's channel grammar through the exported parser: every sigil spelling,
// every doubled-token guard, and the footer reconciliation of §8.3.
func TestPublicAPICCMEDirectiveAxesConformance(t *testing.T) {
	lenient := ccme.Config{Lenient: true}
	onlyBeta := ccme.Config{AllowedChannels: []string{"beta"}}

	runCCMEVectors(t, []ccmeVector{
		{
			name:    "a bare caret implies a depth of one",
			message: "feat(app)^: add a thing",
			check: func(t *testing.T, res *ccme.Result) {
				d := res.Units[0].Directives
				if !d.DepthSet || d.Depth != ccme.Depth(1) {
					t.Errorf("depth = %v (set = %v), want 1", d.Depth, d.DepthSet)
				}
				if d.PropagateSet {
					t.Error("a bare caret must not state a propagate value")
				}
				if d.Propagate != ccme.PropagatePatch {
					t.Errorf("propagate default = %q", string(d.Propagate))
				}
			},
		},
		{
			name:    "a valued caret sets the propagated bump",
			message: "feat(app)^minor: add a thing",
			check: func(t *testing.T, res *ccme.Result) {
				d := res.Units[0].Directives
				if !d.PropagateSet || d.Propagate != ccme.PropagateMinor {
					t.Errorf("propagate = %q (set = %v)", string(d.Propagate), d.PropagateSet)
				}
			},
		},
		{
			name:    "a doubled caret asserts the transitive closure",
			message: "feat(app)^^minor: add a thing",
			check: func(t *testing.T, res *ccme.Result) {
				d := res.Units[0].Directives
				if !d.Depth.IsAll() {
					t.Errorf("depth = %v, want all", d.Depth)
				}
				if d.Depth.String() != "all" {
					t.Errorf("Depth.String() = %q", d.Depth.String())
				}
			},
		},
		{
			name:    "a tripled caret is not a repetition count",
			message: "feat(app)^^^minor: add a thing",
			want:    []string{ccme.CodeE110},
			invalid: true,
		},
		{
			name:    "restating the propagation sigil is E110",
			message: "feat(app)^^minor^: add a thing",
			want:    []string{ccme.CodeE110},
			invalid: true,
		},
		{
			name:    "restating a valued caret is E110",
			message: "feat(app)^minor^patch: add a thing",
			want:    []string{ccme.CodeE110},
			invalid: true,
		},
		{
			name:    "a doubled caret after a single one is the same sigil twice",
			message: "feat(app)^minor^^: add a thing",
			want:    []string{ccme.CodeE110},
			invalid: true,
		},
		{
			name:    "a doubled channel sigil validates its channel too",
			message: "feat(app)%%Beta: add a thing",
			want:    []string{ccme.CodeE181},
			invalid: true,
		},
		{
			name:    "an unknown propagation value is E111",
			message: "feat(app)^enormous: add a thing",
			want:    []string{ccme.CodeE111},
			invalid: true,
		},
		{
			name:    "an unknown propagation value after a doubled caret is E111",
			message: "feat(app)^^enormous: add a thing",
			want:    []string{ccme.CodeE111},
			invalid: true,
		},
		{
			name:    "an explicit depth contradicting the doubled caret is E113",
			message: "feat(app)^^minor+2: add a thing",
			want:    []string{ccme.CodeE113},
			invalid: true,
		},
		{
			name:    "an explicit depth restating the doubled caret is W110",
			message: "feat(app)^^minor+*: add a thing",
			want:    []string{ccme.CodeW110},
		},
		{
			name:    "a doubled caret after an explicit all is W110",
			message: "feat(app)+*^^minor: add a thing",
			want:    []string{ccme.CodeW110},
		},
		{
			name:    "a doubled caret after a numeric depth is E113",
			message: "feat(app)+2^^minor: add a thing",
			want:    []string{ccme.CodeE113},
			invalid: true,
		},
		{
			name:    "an explicit depth overrides the one a caret implies",
			message: "feat(app)^minor+3: add a thing",
			absent:  []string{ccme.CodeW110, ccme.CodeE113},
			check: func(t *testing.T, res *ccme.Result) {
				if got := res.Units[0].Directives.Depth; got != ccme.Depth(3) {
					t.Errorf("depth = %v, want 3", got)
				}
			},
		},
		{
			name:    "a second explicit depth is E110",
			message: "feat(app)+2+3: add a thing",
			want:    []string{ccme.CodeE110},
			invalid: true,
		},
		{
			name:    "an empty depth value is E111",
			message: "feat(app)+: add a thing",
			want:    []string{ccme.CodeE111},
			invalid: true,
		},
		{
			name:    "a depth with a leading zero is E111",
			message: "feat(app)+02: add a thing",
			want:    []string{ccme.CodeE111},
			invalid: true,
		},
		{
			name:    "a non-numeric depth is E111",
			message: "feat(app)+deep: add a thing",
			want:    []string{ccme.CodeE111},
			invalid: true,
		},
		{
			name:    "the word direct is a depth of one",
			message: "feat(app)+direct: add a thing",
			check: func(t *testing.T, res *ccme.Result) {
				if got := res.Units[0].Directives.Depth; got != ccme.Depth(1) {
					t.Errorf("depth = %v, want 1", got)
				}
			},
		},
		{
			name:    "the word all is the transitive closure",
			message: "feat(app)+all: add a thing",
			check: func(t *testing.T, res *ccme.Result) {
				if !res.Units[0].Directives.Depth.IsAll() {
					t.Error("+all did not resolve to the closure")
				}
			},
		},
		{
			name:    "a zero depth is written without a leading zero rule",
			message: "feat(app)+0: add a thing",
			want:    []string{ccme.CodeW152},
			check: func(t *testing.T, res *ccme.Result) {
				if got := res.Units[0].Directives.Depth; got != ccme.Depth(0) {
					t.Errorf("depth = %v, want 0", got)
				}
			},
		},
		{
			name:    "an absurd numeric depth saturates to the closure",
			message: "feat(app)+20000: add a thing",
			check: func(t *testing.T, res *ccme.Result) {
				if !res.Units[0].Directives.Depth.IsAll() {
					t.Error("a saturating depth did not become the closure")
				}
			},
		},
		{
			name:    "a channel sigil sets the unit's own channel",
			message: "feat(app)%beta: add a thing",
			check: func(t *testing.T, res *ccme.Result) {
				d := res.Units[0].Directives
				if !d.ChannelSet || d.Channel.To != "beta" {
					t.Errorf("channel = %#v (set = %v)", d.Channel, d.ChannelSet)
				}
				if d.Channel.IsZero() || d.Channel.IsWord() || d.Channel.IsTransition() {
					t.Errorf("channel predicates disagree with %#v", d.Channel)
				}
				if d.Channel.String() != "beta" {
					t.Errorf("ChannelValue.String() = %q", d.Channel.String())
				}
			},
		},
		{
			name:    "a doubled channel sigil implies a channel depth of one",
			message: "feat(app)%%beta: add a thing",
			check: func(t *testing.T, res *ccme.Result) {
				d := res.Units[0].Directives
				if !d.PropagateChannelSet || d.PropagateChannel.To != "beta" {
					t.Errorf("propagate-channel = %#v", d.PropagateChannel)
				}
				if d.ChannelDepth != ccme.Depth(1) {
					t.Errorf("channel depth = %v, want 1", d.ChannelDepth)
				}
			},
		},
		{
			name:    "a tripled percent sign is not a repetition count",
			message: "feat(app)%%%beta: add a thing",
			want:    []string{ccme.CodeE110},
			invalid: true,
		},
		{
			name:    "a second doubled channel sigil is E110",
			message: "feat(app)%%beta%%alpha: add a thing",
			want:    []string{ccme.CodeE110},
			invalid: true,
		},
		{
			name:    "a doubled channel sigil with no channel is E111",
			message: "feat(app)%%: add a thing",
			want:    []string{ccme.CodeE111},
			invalid: true,
		},
		{
			name:    "a second channel sigil is E110",
			message: "feat(app)%beta%alpha: add a thing",
			want:    []string{ccme.CodeE110},
			invalid: true,
		},
		{
			name:    "an empty channel value is E111",
			message: "feat(app)%: add a thing",
			want:    []string{ccme.CodeE111},
			invalid: true,
		},
		{
			name:    "an explicit channel depth needs a number",
			message: "feat(app)++: add a thing",
			want:    []string{ccme.CodeE111},
			invalid: true,
		},
		{
			name:    "an unparseable channel depth is E111",
			message: "feat(app)++deep: add a thing",
			want:    []string{ccme.CodeE111},
			invalid: true,
		},
		{
			name:    "a tripled plus is not a repetition count",
			message: "feat(app)+++2: add a thing",
			want:    []string{ccme.CodeE110},
			invalid: true,
		},
		{
			name:    "a second explicit channel depth is E110",
			message: "feat(app)++2++3: add a thing",
			want:    []string{ccme.CodeE110},
			invalid: true,
		},
		{
			name:    "an explicit channel depth outranks the one %% implies",
			message: "feat(app)%%beta++3: add a thing",
			check: func(t *testing.T, res *ccme.Result) {
				if got := res.Units[0].Directives.ChannelDepth; got != ccme.Depth(3) {
					t.Errorf("channel depth = %v, want 3", got)
				}
			},
		},
		{
			name:    "a channel depth written before the channel still wins",
			message: "feat(app)++3%%beta: add a thing",
			check: func(t *testing.T, res *ccme.Result) {
				if got := res.Units[0].Directives.ChannelDepth; got != ccme.Depth(3) {
					t.Errorf("channel depth = %v, want 3", got)
				}
			},
		},
		{
			name:    "the whole-value words are Propagate-Channel only",
			message: "feat(app)%%inherit: add a thing",
			absent:  []string{ccme.CodeW152, ccme.CodeW201},
			check: func(t *testing.T, res *ccme.Result) {
				cv := res.Units[0].Directives.PropagateChannel
				if !cv.IsWord() || cv.Word != ccme.ChannelInherit {
					t.Errorf("propagate-channel = %#v, want the inherit word", cv)
				}
				if cv.String() != "inherit" {
					t.Errorf("ChannelValue.String() = %q", cv.String())
				}
			},
		},
		{
			name:    "the none word disables channel propagation",
			message: "feat(app)%%none: add a thing",
			want:    []string{ccme.CodeW152},
		},
		{
			name:    "inherit is not a channel name",
			message: "feat(app)%inherit: add a thing",
			want:    []string{ccme.CodeE111},
			invalid: true,
		},
		{
			name:    "the any-prerelease wildcard is a from-value only",
			message: "feat(app)%*: add a thing",
			want:    []string{ccme.CodeE111},
			invalid: true,
		},
		{
			name:    "a transition from any prerelease is accepted",
			message: "feat(app)%*>stable: add a thing",
			check: func(t *testing.T, res *ccme.Result) {
				cv := res.Units[0].Directives.Channel
				if !cv.IsTransition() || cv.From != ccme.ChannelAnyPrerelease || cv.To != ccme.ChannelStable {
					t.Errorf("channel = %#v", cv)
				}
				if cv.String() != "*>stable" {
					t.Errorf("ChannelValue.String() = %q", cv.String())
				}
			},
		},
		{
			name:    "a transition with two arrows is E111",
			message: "feat(app)%a>b>c: add a thing",
			want:    []string{ccme.CodeE111},
			invalid: true,
		},
		{
			name:    "a transition with an empty source is E111",
			message: "feat(app)%>beta: add a thing",
			want:    []string{ccme.CodeE111},
			invalid: true,
		},
		{
			name:    "a transition with an empty target is E111",
			message: "feat(app)%beta>: add a thing",
			want:    []string{ccme.CodeE111},
			invalid: true,
		},
		{
			name:    "a transition whose sides match is inert",
			message: "feat(app)%beta>beta: add a thing",
			want:    []string{ccme.CodeW207},
		},
		{
			name:    "an inert transition on the propagation axis is W207 too",
			message: "feat(app)%%beta>beta: add a thing",
			want:    []string{ccme.CodeW207},
		},
		{
			name:    "the reserved channel name is E180",
			message: "feat(app)%latest: add a thing",
			want:    []string{ccme.CodeE180},
			invalid: true,
		},
		{
			name:    "an uppercase channel name is E181",
			message: "feat(app)%Beta: add a thing",
			want:    []string{ccme.CodeE181},
			invalid: true,
		},
		{
			name:    "an illegal character in a channel name is E181",
			message: "feat(app)%be_ta: add a thing",
			want:    []string{ccme.CodeE181},
			invalid: true,
		},
		{
			name:    "an over-long channel name is E181",
			message: "feat(app)%" + strings.Repeat("b", 33) + ": add a thing",
			want:    []string{ccme.CodeE181},
			invalid: true,
		},
		{
			name:    "an uppercase source side is E181",
			message: "feat(app)%Beta>stable: add a thing",
			want:    []string{ccme.CodeE181},
			invalid: true,
		},
		{
			name:    "a channel outside the allowed list is E181",
			cfg:     &onlyBeta,
			message: "feat(app)%alpha: add a thing",
			want:    []string{ccme.CodeE181},
			invalid: true,
		},
		{
			name:    "a channel inside the allowed list is accepted",
			cfg:     &onlyBeta,
			message: "feat(app)%beta: add a thing",
			absent:  []string{ccme.CodeE181},
		},
		{
			name:    "stable is always accepted even under an allowed list",
			cfg:     &onlyBeta,
			message: "feat(app)%stable: add a thing",
			absent:  []string{ccme.CodeE181},
		},
		{
			name:    "an inline directive and an equal footer are redundant",
			message: "feat(app)^minor: add a thing\n\nPropagate: minor",
			want:    []string{ccme.CodeW110},
		},
		{
			name:    "an inline directive contradicted by a footer is E112",
			message: "feat(app)^minor: add a thing\n\nPropagate: major",
			want:    []string{ccme.CodeE112},
			invalid: true,
		},
		{
			name:    "lenient mode lets the footer win with W112",
			cfg:     &lenient,
			message: "feat(app)^minor: add a thing\n\nPropagate: major",
			want:    []string{ccme.CodeW112},
			check: func(t *testing.T, res *ccme.Result) {
				if got := res.Units[0].Directives.Propagate; got != ccme.PropagateMajor {
					t.Errorf("propagate = %q, want the footer's value", string(got))
				}
			},
		},
		{
			name:    "a depth footer silently outranks the one a caret implies",
			message: "feat(app)^: add a thing\n\nPropagate-Depth: 4",
			absent:  []string{ccme.CodeE112, ccme.CodeW110, ccme.CodeE113},
			check: func(t *testing.T, res *ccme.Result) {
				if got := res.Units[0].Directives.Depth; got != ccme.Depth(4) {
					t.Errorf("depth = %v, want 4", got)
				}
			},
		},
		{
			name:    "a depth footer contradicting the doubled caret is E113",
			message: "feat(app)^^minor: add a thing\n\nPropagate-Depth: 2",
			want:    []string{ccme.CodeE113},
			invalid: true,
		},
		{
			name:    "a depth footer restating the doubled caret is W110",
			message: "feat(app)^^minor: add a thing\n\nPropagate-Depth: all",
			want:    []string{ccme.CodeW110},
		},
		{
			name:    "a channel-depth footer outranks the one %% implies",
			message: "feat(app)%%beta: add a thing\n\nPropagate-Channel-Depth: 5",
			absent:  []string{ccme.CodeE112, ccme.CodeW110},
			check: func(t *testing.T, res *ccme.Result) {
				if got := res.Units[0].Directives.ChannelDepth; got != ccme.Depth(5) {
					t.Errorf("channel depth = %v, want 5", got)
				}
			},
		},
		{
			name:    "an explicit channel depth contradicted by a footer is E112",
			message: "feat(app)++2: add a thing\n\nPropagate-Channel-Depth: 5",
			want:    []string{ccme.CodeE112},
			invalid: true,
		},
		{
			name:    "a propagation bump with a zero depth reaches nobody",
			message: "feat(app): add a thing\n\nPropagate: minor",
			want:    []string{ccme.CodeW201},
			absent:  []string{ccme.CodeW152},
		},
		{
			name:    "a propagation directive resolving to nothing is W152",
			message: "feat(app): add a thing\n\nPropagate: none",
			want:    []string{ccme.CodeW152},
			absent:  []string{ccme.CodeW201},
		},
		{
			name:    "a channel value with a zero depth reaches nobody",
			message: "feat(app): add a thing\n\nPropagate-Channel: beta",
			want:    []string{ccme.CodeW201},
		},
		{
			name:    "a channel directive resolving to nothing is W152",
			message: "feat(app): add a thing\n\nPropagate-Channel: none",
			want:    []string{ccme.CodeW152},
		},
		{
			name:    "Propagate-Channel-Scope defaults to Propagate-Scope",
			message: "feat(app)^^minor: add a thing\n\nPropagate-Scope: core,-legacy",
			check: func(t *testing.T, res *ccme.Result) {
				d := res.Units[0].Directives
				if !d.PropagateScopeSet || len(d.PropagateScope) != 2 {
					t.Fatalf("propagate-scope = %#v", d.PropagateScope)
				}
				if d.PropagateChannelScopeSet {
					t.Error("the channel scope must be inherited, not stated")
				}
				if !slices.Equal(d.PropagateChannelScope.Names(), d.PropagateScope.Names()) {
					t.Errorf("channel scope %v did not inherit %v",
						d.PropagateChannelScope.Names(), d.PropagateScope.Names())
				}
			},
		},
		{
			name:    "a stated channel scope is kept",
			message: "feat(app)%%beta: add a thing\n\nPropagate-Channel-Scope: core",
			check: func(t *testing.T, res *ccme.Result) {
				d := res.Units[0].Directives
				if !d.PropagateChannelScopeSet || len(d.PropagateChannelScope) != 1 {
					t.Errorf("channel scope = %#v", d.PropagateChannelScope)
				}
			},
		},
	})
}

// TestPublicAPICCMEFooterRegistryConformance drives the §8.1 footer registry,
// the two BREAKING CHANGE silent failures of §8.1.1, the correction footers of
// §7.4 and the control types of §7 and §10 through the exported parser.
func TestPublicAPICCMEFooterRegistryConformance(t *testing.T) {
	strict := ccme.Config{StrictTypes: true}
	noTrailers := ccme.Config{MessageLevelTrailers: []string{}, IssueTrailers: []string{}}
	ownTypes := ccme.Config{Types: map[string]ccme.Bump{"deps": ccme.BumpPatch}}

	runCCMEVectors(t, []ccmeVector{
		{
			name:    "a breaking-change footer makes the unit breaking",
			message: "fix(app): repair a thing\n\nBREAKING CHANGE: the flag moved",
			check: func(t *testing.T, res *ccme.Result) {
				u := res.Units[0]
				if !u.Breaking || u.Bump != ccme.BumpMajor {
					t.Errorf("breaking = %v, bump = %v", u.Breaking, u.Bump)
				}
				if got := u.BreakingDescription(); got != "the flag moved" {
					t.Errorf("BreakingDescription() = %q", got)
				}
				if !u.Footers[0].IsBreakingChange() {
					t.Error("the footer does not report itself as breaking")
				}
				if u.Footers[0].Separator != ": " {
					t.Errorf("separator = %q", u.Footers[0].Separator)
				}
			},
		},
		{
			name:    "the hyphenated alias is the same key",
			message: "fix(app): repair a thing\n\nBREAKING-CHANGE: the flag moved",
			check: func(t *testing.T, res *ccme.Result) {
				if !res.Units[0].Breaking {
					t.Error("the hyphenated alias did not mark the unit breaking")
				}
			},
		},
		{
			name:    "an empty breaking-change value warns",
			message: "fix(app): repair a thing\n\nBREAKING CHANGE:",
			want:    []string{ccme.CodeW157},
			check: func(t *testing.T, res *ccme.Result) {
				if !res.Units[0].Breaking {
					t.Error("an empty breaking-change value must still be breaking")
				}
			},
		},
		{
			name:    "a breaking-change value spans its following lines",
			message: "fix(app): repair a thing\n\nBREAKING CHANGE: the flag moved\nand so did the file",
			check: func(t *testing.T, res *ccme.Result) {
				want := "the flag moved\nand so did the file"
				if got := res.Units[0].BreakingDescription(); got != want {
					t.Errorf("BreakingDescription() = %q, want %q", got, want)
				}
			},
		},
		{
			name:    "a miscased breaking key is not breaking",
			message: "fix(app): repair a thing\n\nBreaking-Change: the flag moved",
			want:    []string{ccme.CodeW155},
			check: func(t *testing.T, res *ccme.Result) {
				if res.Units[0].Breaking {
					t.Error("a miscased key must not be breaking")
				}
				if !res.Units[0].Footers[0].MiscasedBreaking {
					t.Error("the footer does not report the miscasing")
				}
			},
		},
		{
			name:    "a spaced miscasing is not even a footer",
			message: "fix(app): repair a thing\n\nRefs: 12\nBreaking change: the flag moved",
			want:    []string{ccme.CodeW155},
		},
		{
			name:    "a breaking-change line stranded in the body is W156",
			message: "fix(app): repair a thing\n\nBREAKING CHANGE: the flag moved\n\nRefs: 12",
			want:    []string{ccme.CodeW156},
		},
		{
			name:    "a trailing paragraph that only starts like a footer is W151",
			message: "fix(app): repair a thing\n\nRefs: 12\nand then some prose about it",
			want:    []string{ccme.CodeW151},
		},
		{
			name:    "an unknown footer key is ignored with W150",
			message: "fix(app): repair a thing\n\nUnheard-Of: 12",
			want:    []string{ccme.CodeW150},
			check: func(t *testing.T, res *ccme.Result) {
				f := res.Units[0].Footers[0]
				if f.Known || f.CanonicalKey != f.Key {
					t.Errorf("footer = %#v", f)
				}
			},
		},
		{
			name:    "an authorship trailer is ignored wherever it appears",
			message: "fix(app): repair a thing\n\nSigned-off-by: A Person <a@example.com>",
			absent:  []string{ccme.CodeW150},
			check: func(t *testing.T, res *ccme.Result) {
				if !res.Units[0].Footers[0].MessageLevel {
					t.Error("the trailer is not reported as message-level")
				}
			},
		},
		{
			name:    "an issue trailer is surfaced rather than warned about",
			message: "fix(app): repair a thing\n\nCloses #12",
			absent:  []string{ccme.CodeW150},
			check: func(t *testing.T, res *ccme.Result) {
				f := res.Units[0].Footers[0]
				if !f.IssueReference || f.Separator != " #" || f.Value != "#12" {
					t.Errorf("footer = %#v", f)
				}
			},
		},
		{
			name:    "disabling the trailer tables makes them ordinary unknown keys",
			cfg:     &noTrailers,
			message: "fix(app): repair a thing\n\nSigned-off-by: A Person",
			want:    []string{ccme.CodeW150},
		},
		{
			name:    "a footer value continues on an indented line",
			message: "fix(app): repair a thing\n\nPropagate-Scope: core,\n  utils",
			check: func(t *testing.T, res *ccme.Result) {
				names := res.Units[0].Directives.PropagateScope.Names()
				if want := []string{"core", "utils"}; !slices.Equal(names, want) {
					t.Errorf("scope = %v, want %v", names, want)
				}
			},
		},
		{
			name:    "an invalid propagate footer value is E151",
			message: "fix(app): repair a thing\n\nPropagate: enormous",
			want:    []string{ccme.CodeE151},
			invalid: true,
		},
		{
			name:    "an invalid depth footer value is E151",
			message: "fix(app): repair a thing\n\nPropagate-Depth: deep",
			want:    []string{ccme.CodeE151},
			invalid: true,
		},
		{
			name:    "an invalid channel-depth footer value is E151",
			message: "fix(app): repair a thing\n\nPropagate-Channel-Depth: deep",
			want:    []string{ccme.CodeE151},
			invalid: true,
		},
		{
			name:    "an empty scope footer term is E151",
			message: "fix(app): repair a thing\n\nPropagate-Scope: core,,utils",
			want:    []string{ccme.CodeE151},
			invalid: true,
		},
		{
			name:    "a scope footer term carrying whitespace is E151",
			message: "fix(app): repair a thing\n\nPropagate-Scope: core utils",
			want:    []string{ccme.CodeE151},
			invalid: true,
		},
		{
			name:    "an invalid channel-scope footer value is E151",
			message: "fix(app): repair a thing\n\nPropagate-Channel-Scope: core utils",
			want:    []string{ccme.CodeE151},
			invalid: true,
		},
		{
			name:    "a channel footer carries the channel grammar's own code",
			message: "fix(app): repair a thing\n\nChannel: Beta",
			want:    []string{ccme.CodeE181},
			invalid: true,
		},
		{
			name:    "a propagate-channel footer carries it too",
			message: "fix(app): repair a thing\n\nPropagate-Channel: latest",
			want:    []string{ccme.CodeE180},
			invalid: true,
		},
		{
			name:    "a channel footer sets the unit's channel",
			message: "fix(app): repair a thing\n\nChannel: beta",
			check: func(t *testing.T, res *ccme.Result) {
				if got := res.Units[0].Directives.Channel.To; got != "beta" {
					t.Errorf("channel = %q", got)
				}
			},
		},
		{
			name:    "an exact Release-As is parsed as a version",
			message: "fix(app): repair a thing\n\nRelease-As: 2.1.0",
			check: func(t *testing.T, res *ccme.Result) {
				ra := res.Units[0].Directives.ReleaseAs
				if ra == nil || ra.Kind != ccme.ReleaseAsExact {
					t.Fatalf("release-as = %#v", ra)
				}
				if ra.Version.String() != "2.1.0" || ra.String() != "2.1.0" {
					t.Errorf("version = %q, raw = %q", ra.Version.String(), ra.String())
				}
				if ra.Kind.IsHold() || ra.Kind.String() != "exact" {
					t.Errorf("kind = %v", ra.Kind)
				}
			},
		},
		{
			name:    "Release-As none holds the package",
			message: "fix(app): repair a thing\n\nRelease-As: none",
			check: func(t *testing.T, res *ccme.Result) {
				ra := res.Units[0].Directives.ReleaseAs
				if ra == nil || !ra.Kind.IsHold() || ra.Kind.String() != "none" {
					t.Fatalf("release-as = %#v", ra)
				}
				if res.Units[0].Bump != ccme.BumpPatch {
					t.Errorf("a hold must retain its unit's bump, got %v", res.Units[0].Bump)
				}
			},
		},
		{
			name:    "Release-As auto lifts a hold",
			message: "fix(app): repair a thing\n\nRelease-As: auto",
			check: func(t *testing.T, res *ccme.Result) {
				ra := res.Units[0].Directives.ReleaseAs
				if ra == nil || ra.Kind != ccme.ReleaseAsAuto || ra.Kind.String() != "auto" {
					t.Fatalf("release-as = %#v", ra)
				}
			},
		},
		{
			name:    "Release-As has no bump form",
			message: "fix(app): repair a thing\n\nRelease-As: minor",
			want:    []string{ccme.CodeE151},
			invalid: true,
		},
		{
			name:    "an unparseable Release-As value is E151",
			message: "fix(app): repair a thing\n\nRelease-As: soon",
			want:    []string{ccme.CodeE151},
			invalid: true,
		},
		{
			name:    "an exact Release-As over two packages is E154",
			message: "fix(app,core): repair a thing\n\nRelease-As: 2.1.0",
			want:    []string{ccme.CodeE154},
			invalid: true,
		},
		{
			name:    "an exact Release-As over the whole workspace is E154",
			message: "fix(*): repair a thing\n\nRelease-As: 2.1.0",
			want:    []string{ccme.CodeE154},
			invalid: true,
		},
		{
			name:    "an exclusion does not make a scope-set multi-package",
			message: "fix(app,-legacy): repair a thing\n\nRelease-As: 2.1.0",
			absent:  []string{ccme.CodeE154},
		},
		{
			name:    "a Reverts value that is not a sha stays informational",
			message: "fix(app): repair a thing\n\nReverts: yesterday",
			want:    []string{ccme.CodeW214},
			check: func(t *testing.T, res *ccme.Result) {
				if want := []string{"yesterday"}; !slices.Equal(res.Units[0].Directives.Reverts, want) {
					t.Errorf("reverts = %v", res.Units[0].Directives.Reverts)
				}
			},
		},
		{
			name:    "a Reverts value that is a sha raises nothing",
			message: "fix(app): repair a thing\n\nReverts: " + shaFull,
			absent:  []string{ccme.CodeW214},
		},
		{
			name:    "the correction footers name a commit and a unit",
			message: "fix(app): repair a thing\n\nEdits: " + shaShort + "#2\nDeletes: *",
			check: func(t *testing.T, res *ccme.Result) {
				d := res.Units[0].Directives
				if len(d.Edits) != 1 || d.Edits[0].SHA != shaShort || d.Edits[0].UnitSelector != 2 {
					t.Fatalf("edits = %#v", d.Edits)
				}
				if d.Edits[0].IsWildcard() || d.Edits[0].String() != shaShort+"#2" {
					t.Errorf("edit target = %#v", d.Edits[0])
				}
				if len(d.Deletes) != 1 || !d.Deletes[0].IsWildcard() || d.Deletes[0].String() != "*" {
					t.Fatalf("deletes = %#v", d.Deletes)
				}
			},
		},
		{
			name:    "a bare sha target carries no selector",
			message: "fix(app): repair a thing\n\nEdits: " + shaFull,
			check: func(t *testing.T, res *ccme.Result) {
				if got := res.Units[0].Directives.Edits[0].UnitSelector; got != 0 {
					t.Errorf("selector = %d, want 0", got)
				}
			},
		},
		{
			name:    "a correction target that is not a sha is E151",
			message: "fix(app): repair a thing\n\nEdits: yesterday",
			want:    []string{ccme.CodeE151},
			invalid: true,
		},
		{
			name:    "an uppercase sha does not validate",
			message: "fix(app): repair a thing\n\nEdits: ABC1234",
			want:    []string{ccme.CodeE151},
			invalid: true,
		},
		{
			name:    "an over-long sha does not validate",
			message: "fix(app): repair a thing\n\nDeletes: " + strings.Repeat("a", 65),
			want:    []string{ccme.CodeE151},
			invalid: true,
		},
		{
			name:    "the wildcard takes no unit selector",
			message: "fix(app): repair a thing\n\nEdits: *#2",
			want:    []string{ccme.CodeE151},
			invalid: true,
		},
		{
			name:    "a selector separator with no selector is E151",
			message: "fix(app): repair a thing\n\nEdits: " + shaShort + "#",
			want:    []string{ccme.CodeE151},
			invalid: true,
		},
		{
			name:    "a selector with a leading zero is E151",
			message: "fix(app): repair a thing\n\nEdits: " + shaShort + "#01",
			want:    []string{ccme.CodeE151},
			invalid: true,
		},
		{
			name:    "a non-numeric selector is E151",
			message: "fix(app): repair a thing\n\nEdits: " + shaShort + "#two",
			want:    []string{ccme.CodeE151},
			invalid: true,
		},
		{
			name:    "a selector past the bound is E151",
			message: "fix(app): repair a thing\n\nDeletes: " + shaShort + "#99999999",
			want:    []string{ccme.CodeE151},
			invalid: true,
		},
		{
			name:    "a cancel unit takes a scope-set and nothing else",
			message: "cancel(app): drop the pending records",
			check: func(t *testing.T, res *ccme.Result) {
				u := res.Units[0]
				if !u.IsCancel() || !u.IsControl() || u.IsRelease() {
					t.Errorf("control predicates disagree for %q", u.Header.Type)
				}
				if u.Bump != ccme.BumpNone || u.TypeBump != ccme.BumpNone {
					t.Errorf("a control unit produced a bump: %v", u.Bump)
				}
			},
		},
		{
			name:    "a cancel unit must not be breaking",
			message: "cancel(app)!: drop the pending records",
			want:    []string{ccme.CodeE170},
			invalid: true,
		},
		{
			name:    "a cancel unit must not carry inline directives",
			message: "cancel(app)^minor: drop the pending records",
			want:    []string{ccme.CodeE171},
			invalid: true,
		},
		{
			name:    "a cancel unit must not carry registry footers",
			message: "cancel(app): drop the pending records\n\nPropagate: minor",
			want:    []string{ccme.CodeE171},
			invalid: true,
		},
		{
			name:    "a release unit with no directive is inert",
			message: "release(app): cut a release",
			want:    []string{ccme.CodeW141},
			check: func(t *testing.T, res *ccme.Result) {
				if !res.Units[0].IsRelease() || !res.Units[0].IsControl() {
					t.Error("the release unit is not reported as a control unit")
				}
			},
		},
		{
			name:    "a release unit must not be breaking",
			message: "release(app)!: cut a release",
			want:    []string{ccme.CodeE141},
			invalid: true,
		},
		{
			name:    "a breaking-change footer also makes a release unit E141",
			message: "release(app): cut a release\n\nBREAKING CHANGE: everything moved",
			want:    []string{ccme.CodeE141},
			invalid: true,
		},
		{
			name:    "a release unit must not carry a correction footer",
			message: "release(app): cut a release\n\nEdits: " + shaShort,
			want:    []string{ccme.CodeE173},
			invalid: true,
		},
		{
			name:    "a release unit carrying a directive is not inert",
			message: "release(app): cut a release\n\nRelease-As: 3.0.0",
			absent:  []string{ccme.CodeW141},
		},
		{
			name:    "an unknown type maps to none with a warning",
			message: "frobnicate(app): do a thing",
			want:    []string{ccme.CodeW140},
			check: func(t *testing.T, res *ccme.Result) {
				if res.Units[0].Bump != ccme.BumpNone {
					t.Errorf("bump = %v, want none", res.Units[0].Bump)
				}
			},
		},
		{
			name:    "strict types turn an unknown type into an error",
			cfg:     &strict,
			message: "frobnicate(app): do a thing",
			want:    []string{ccme.CodeE140},
			invalid: true,
		},
		{
			name:    "a configured type table replaces the standard one",
			cfg:     &ownTypes,
			message: "deps(app): raise a range",
			absent:  []string{ccme.CodeW140},
			check: func(t *testing.T, res *ccme.Result) {
				if res.Units[0].Bump != ccme.BumpPatch {
					t.Errorf("bump = %v, want patch", res.Units[0].Bump)
				}
			},
		},
		{
			name:    "a standard type is unknown once the table is replaced",
			cfg:     &ownTypes,
			message: "feat(app): add a thing",
			want:    []string{ccme.CodeW140},
		},
	})
}

// TestPublicAPICCMEMessageStructureConformance drives §4's normalisation, unit
// splitting and message-scoped bounds through both exported entry points.
func TestPublicAPICCMEMessageStructureConformance(t *testing.T) {
	twoUnits := ccme.Config{Limits: ccme.Limits{UnitsPerMessage: 2}}
	oneScope := ccme.Config{Limits: ccme.Limits{ScopeTermsPerUnit: 1}}
	smallMessage := ccme.Config{Limits: ccme.Limits{MessageBytes: 32}}
	mailSeparator := ccme.Config{Separator: "%%%"}

	runCCMEVectors(t, []ccmeVector{
		{
			name:    "propagation footer cannot bypass the scope limit",
			cfg:     &oneScope,
			message: "fix(app): bounded propagation\n\nPropagate-Scope: core,utils",
			want:    []string{ccme.CodeE158},
			invalid: true,
		},
		{
			name:    "channel propagation footer cannot bypass the scope limit",
			cfg:     &oneScope,
			message: "fix(app): bounded channel propagation\n\nPropagate-Channel-Scope: core,utils",
			want:    []string{ccme.CodeE158},
			invalid: true,
		},
		{
			name:    "an empty message is E002",
			message: "",
			want:    []string{ccme.CodeE002},
			invalid: true,
		},
		{
			name:    "a whitespace-only message normalises to empty",
			message: "   \n\t\n   \n",
			want:    []string{ccme.CodeE002},
			invalid: true,
		},
		{
			name:    "an empty subject is E002",
			subject: true,
			message: "\n",
			want:    []string{ccme.CodeE002},
			invalid: true,
		},
		{
			name:    "invalid UTF-8 in a message is E001",
			message: "feat(app): add a \xff thing",
			want:    []string{ccme.CodeE001},
			invalid: true,
		},
		{
			name:    "invalid UTF-8 in a subject is E001",
			subject: true,
			message: "feat(app): add a \xff thing",
			want:    []string{ccme.CodeE001},
			invalid: true,
		},
		{
			name:    "a subject spanning two lines is refused",
			subject: true,
			message: "feat(app): add a thing\n\nand a body",
			want:    []string{ccme.CodeE100},
			invalid: true,
		},
		{
			name:    "a well-formed subject parses as one unit",
			subject: true,
			message: "feat(app)^minor: add a thing",
			check: func(t *testing.T, res *ccme.Result) {
				if len(res.Units) != 1 || !res.Units[0].Valid {
					t.Fatalf("units = %#v", res.Units)
				}
				if res.Units[0].Start.Line != 1 || res.Units[0].Start.Column != 1 {
					t.Errorf("start = %s", res.Units[0].Start)
				}
			},
		},
		{
			name:    "a subject over the byte bound is E158",
			cfg:     &smallMessage,
			subject: true,
			message: "feat(app): " + strings.Repeat("x", 64),
			want:    []string{ccme.CodeE158},
			invalid: true,
		},
		{
			name:    "a message over the byte bound is E158",
			cfg:     &smallMessage,
			message: "feat(app): " + strings.Repeat("x", 64),
			want:    []string{ccme.CodeE158},
			invalid: true,
		},
		{
			name:    "a message over the unit bound is E158",
			cfg:     &twoUnits,
			message: "feat(a): one\n---\nfix(b): two\n---\nfix(c): three",
			want:    []string{ccme.CodeE158},
			invalid: true,
		},
		{
			name:    "the unit bound invalidates every unit of the message",
			cfg:     &twoUnits,
			message: "feat(a): one\n---\nfix(b): two\n---\nfix(c): three",
			want:    []string{ccme.CodeE158},
			invalid: true,
			check: func(t *testing.T, res *ccme.Result) {
				if len(res.ValidUnits()) != 0 {
					t.Errorf("ValidUnits() = %d, want none", len(res.ValidUnits()))
				}
			},
		},
		{
			name:    "a multi-unit message keeps every unit in written order",
			message: "feat(a): one\n\nbody of one\n---\nfix(b): two\n\nRefs: 12",
			check: func(t *testing.T, res *ccme.Result) {
				if len(res.Units) != 2 {
					t.Fatalf("units = %d, want 2", len(res.Units))
				}
				if res.Units[0].Header.Type != "feat" || res.Units[1].Header.Type != "fix" {
					t.Errorf("types = %q, %q", res.Units[0].Header.Type, res.Units[1].Header.Type)
				}
				if res.Units[0].Index != 0 || res.Units[1].Index != 1 {
					t.Errorf("indexes = %d, %d", res.Units[0].Index, res.Units[1].Index)
				}
				if res.Units[0].Body != "body of one" {
					t.Errorf("body = %q", res.Units[0].Body)
				}
				if res.Bump() != ccme.BumpMinor {
					t.Errorf("Bump() = %v, want minor", res.Bump())
				}
				if len(res.ValidUnits()) != 2 {
					t.Errorf("ValidUnits() = %d, want 2", len(res.ValidUnits()))
				}
			},
		},
		{
			name:    "a multi-unit message with an unscoped unit is W132",
			message: "feat(a): one\n---\nfix: two",
			want:    []string{ccme.CodeW132},
		},
		{
			name:    "a broken unit leaves its siblings applying",
			message: "feat(a): one\n---\nFix(b): two",
			want:    []string{ccme.CodeE101},
			invalid: true,
			check: func(t *testing.T, res *ccme.Result) {
				valid := res.ValidUnits()
				if len(valid) != 1 || valid[0].Header.Type != "feat" {
					t.Fatalf("ValidUnits() = %#v", valid)
				}
				if res.Bump() != ccme.BumpMinor {
					t.Errorf("Bump() = %v, want the surviving unit's minor", res.Bump())
				}
			},
		},
		{
			name:    "a blank line after a separator is not part of the unit",
			message: "feat(a): one\n---\n\nfix(b): two\n",
			check: func(t *testing.T, res *ccme.Result) {
				if len(res.Units) != 2 || res.Units[1].Header.Type != "fix" {
					t.Fatalf("units = %#v", res.Units)
				}
			},
		},
		{
			name:    "extra blank lines before a body are not body text",
			message: "feat(a): one\n\n\n\nbody line",
			check: func(t *testing.T, res *ccme.Result) {
				if got := res.Units[0].Body; got != "body line" {
					t.Errorf("body = %q, want the blank edges trimmed", got)
				}
			},
		},
		{
			name:    "an empty unit between two separators is discarded",
			message: "feat(a): one\n---\n---\nfix(b): two",
			want:    []string{ccme.CodeW001},
		},
		{
			name:    "a message ending on a separator reports the separator's line",
			message: "feat(a): one\n---\n",
			want:    []string{ccme.CodeW001},
			check: func(t *testing.T, res *ccme.Result) {
				for _, d := range res.Diagnostics {
					if d.Code == ccme.CodeW001 && d.Position.Line > 2 {
						t.Errorf("W001 reported at %s, past the message", d.Position)
					}
				}
			},
		},
		{
			name:    "an escaped separator becomes body text",
			message: "feat(a): one\n\nthe rule below is body:\n\\---\nstill body",
			check: func(t *testing.T, res *ccme.Result) {
				if len(res.Units) != 1 {
					t.Fatalf("units = %d, want 1", len(res.Units))
				}
				if !strings.Contains(res.Units[0].Body, "\n---\n") {
					t.Errorf("body = %q, want the unescaped rule", res.Units[0].Body)
				}
				if strings.Contains(res.Units[0].Body, "\\---") {
					t.Errorf("body = %q, want the backslash removed", res.Units[0].Body)
				}
			},
		},
		{
			name:    "a configured separator replaces the default one",
			cfg:     &mailSeparator,
			message: "feat(a): one\n%%%\nfix(b): two",
			check: func(t *testing.T, res *ccme.Result) {
				if len(res.Units) != 2 {
					t.Errorf("units = %d, want 2", len(res.Units))
				}
			},
		},
		{
			name:    "the default separator is body text under a configured one",
			cfg:     &mailSeparator,
			message: "feat(a): one\n\n---\nstill one unit",
			check: func(t *testing.T, res *ccme.Result) {
				if len(res.Units) != 1 {
					t.Errorf("units = %d, want 1", len(res.Units))
				}
			},
		},
		{
			name:    "a longer rule is not the separator",
			message: "feat(a): one\n\n----\nstill one unit",
			check: func(t *testing.T, res *ccme.Result) {
				if len(res.Units) != 1 {
					t.Errorf("units = %d, want 1", len(res.Units))
				}
			},
		},
		{
			name:    "a message needing normalisation is normalised before parsing",
			message: "\uFEFF\uFEFFfeat(app): add a thing  \r\n\r\nbody line\t\r\n\r\n\r\n",
			check: func(t *testing.T, res *ccme.Result) {
				want := "feat(app): add a thing\n\nbody line"
				if res.Message != want {
					t.Errorf("Message = %q, want %q", res.Message, want)
				}
				if got := ccme.Normalize(res.Message); got != res.Message {
					t.Errorf("Normalize is not idempotent: %q", got)
				}
			},
		},
	})
}

// TestPublicAPICCMENormalizationIsIdempotent exercises the exported Normalize
// helper directly, including the fast path that returns its input untouched.
func TestPublicAPICCMENormalizationIsIdempotent(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"an empty message is left alone", "", ""},
		{"an already normalised message is returned unchanged", "feat: x\n\nbody", "feat: x\n\nbody"},
		{"a leading newline is not a trailing blank line", "\nfeat: x", "\nfeat: x"},
		{"a byte-order mark is stripped", "\uFEFFfeat: x", "feat: x"},
		{"doubled byte-order marks are both stripped", "\uFEFF\uFEFFfeat: x", "feat: x"},
		{"carriage returns become line feeds", "feat: x\r\rbody", "feat: x\n\nbody"},
		{"CRLF becomes LF", "feat: x\r\n\r\nbody", "feat: x\n\nbody"},
		{"trailing spaces go from every line", "feat: x  \n\nbody \t", "feat: x\n\nbody"},
		{"a trailing space on an interior line alone", "feat: x \n\nbody", "feat: x\n\nbody"},
		{"trailing blank lines go from the end", "feat: x\n\n\n", "feat: x"},
		{"a trailing tab alone is trimmed", "feat: x\t", "feat: x"},
		{"interior blank lines survive", "feat: x\n\nbody\n\nmore", "feat: x\n\nbody\n\nmore"},
		{"leading whitespace is significant", "feat: x\n\n  indented", "feat: x\n\n  indented"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ccme.Normalize(c.in)
			if got != c.want {
				t.Fatalf("Normalize(%q) = %q, want %q", c.in, got, c.want)
			}
			if again := ccme.Normalize(got); again != got {
				t.Errorf("Normalize is not idempotent: %q -> %q", got, again)
			}
		})
	}
}

// TestPublicAPICCMEVersionArithmetic exercises the exported SemVer surface the
// release engine shares with the parser: parsing, ordering, bumping and the
// text marshalling a configuration model round-trips a version through.
func TestPublicAPICCMEVersionArithmetic(t *testing.T) {
	t.Run("valid versions parse into their components", func(t *testing.T) {
		v, err := ccme.ParseVersion("1.2.3-beta.1+build.5")
		if err != nil {
			t.Fatalf("ParseVersion: %v", err)
		}
		if v.Major != 1 || v.Minor != 2 || v.Patch != 3 {
			t.Errorf("core = %d.%d.%d", v.Major, v.Minor, v.Patch)
		}
		if !slices.Equal(v.Prerelease, []string{"beta", "1"}) {
			t.Errorf("prerelease = %v", v.Prerelease)
		}
		if !slices.Equal(v.Build, []string{"build", "5"}) {
			t.Errorf("build = %v", v.Build)
		}
		if !v.IsPrerelease() {
			t.Error("IsPrerelease() = false")
		}
		if got := v.String(); got != "1.2.3-beta.1" {
			t.Errorf("String() = %q, want build metadata dropped", got)
		}
		if got := v.Core().String(); got != "1.2.3" {
			t.Errorf("Core() = %q", got)
		}
		if v.Core().IsPrerelease() {
			t.Error("Core() kept the prerelease")
		}
	})

	t.Run("a zero version renders every component", func(t *testing.T) {
		if got := (ccme.Version{}).String(); got != "0.0.0" {
			t.Errorf("String() = %q, want 0.0.0", got)
		}
	})

	t.Run("malformed versions are rejected", func(t *testing.T) {
		for _, bad := range []string{
			"", "v1.2.3", "1", "1.2", "1.2.3.4", "01.2.3", "1.02.3", "1.2.03",
			"1.2.x", "1.x.3", "x.2.3", "1.2.3-", "1.2.3-beta..1", "1.2.3-beta.01",
			"1.2.3-beta!", "1.2.3+", "1.2.3+build!", "1.2.3 ",
			"99999999999999999999999.0.0",
		} {
			if _, err := ccme.ParseVersion(bad); err == nil {
				t.Errorf("ParseVersion(%q) accepted a malformed version", bad)
			} else if !errors.Is(err, ccme.ErrInvalidVersion) {
				t.Errorf("ParseVersion(%q) error %v does not wrap ErrInvalidVersion", bad, err)
			}
		}
	})

	t.Run("precedence follows SemVer", func(t *testing.T) {
		ordered := []string{
			"1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta",
			"1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1", "1.0.0",
			"1.0.1", "1.1.0", "2.0.0",
		}
		parsed := make([]ccme.Version, len(ordered))
		for i, s := range ordered {
			v, err := ccme.ParseVersion(s)
			if err != nil {
				t.Fatalf("ParseVersion(%q): %v", s, err)
			}
			parsed[i] = v
		}
		for i := 0; i < len(parsed); i++ {
			if got := parsed[i].Compare(parsed[i]); got != 0 {
				t.Errorf("%s compared to itself = %d", ordered[i], got)
			}
			for j := i + 1; j < len(parsed); j++ {
				if got := parsed[i].Compare(parsed[j]); got != -1 {
					t.Errorf("%s vs %s = %d, want -1", ordered[i], ordered[j], got)
				}
				if got := parsed[j].Compare(parsed[i]); got != 1 {
					t.Errorf("%s vs %s = %d, want 1", ordered[j], ordered[i], got)
				}
			}
		}
	})

	t.Run("a version assembled by hand still orders", func(t *testing.T) {
		// A Version is an exported struct, so a caller holding a baseline it
		// built itself compares it here too; an identifier that is empty is
		// neither numeric nor greater than one that is.
		blank := ccme.Version{Major: 1, Prerelease: []string{""}}
		named := ccme.Version{Major: 1, Prerelease: []string{"beta"}}
		if got := blank.Compare(named); got != -1 {
			t.Errorf("an empty identifier against a named one = %d, want -1", got)
		}
		if got := named.Compare(blank); got != 1 {
			t.Errorf("a named identifier against an empty one = %d, want 1", got)
		}
		if got := blank.Compare(blank); got != 0 {
			t.Errorf("an empty identifier against itself = %d, want 0", got)
		}
	})

	t.Run("build metadata is ignored by precedence", func(t *testing.T) {
		a, _ := ccme.ParseVersion("1.0.0+left")
		b, _ := ccme.ParseVersion("1.0.0+right")
		if got := a.Compare(b); got != 0 {
			t.Errorf("Compare = %d, want build metadata ignored", got)
		}
	})

	t.Run("bumping moves the core above a prerelease baseline", func(t *testing.T) {
		base, err := ccme.ParseVersion("1.2.0-beta.1+meta")
		if err != nil {
			t.Fatalf("ParseVersion: %v", err)
		}
		for _, c := range []struct {
			bump ccme.Bump
			want string
		}{
			{ccme.BumpMajor, "2.0.0"},
			{ccme.BumpMinor, "1.3.0"},
			{ccme.BumpPatch, "1.2.1"},
		} {
			if got := base.Bumped(c.bump).String(); got != c.want {
				t.Errorf("Bumped(%v) = %q, want %q", c.bump, got, c.want)
			}
			if base.Bumped(c.bump).IsPrerelease() {
				t.Errorf("Bumped(%v) kept the prerelease", c.bump)
			}
		}
		if got := base.Bumped(ccme.BumpNone); got.Raw != base.Raw {
			t.Errorf("Bumped(none) = %#v, want the version unchanged", got)
		}
	})

	t.Run("a version crosses a module boundary as text", func(t *testing.T) {
		type model struct {
			Initial ccme.Version `json:"initial"`
		}
		in := model{}
		if err := in.Initial.UnmarshalText([]byte("3.4.5-rc.1")); err != nil {
			t.Fatalf("UnmarshalText: %v", err)
		}
		data, err := json.Marshal(in)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		if string(data) != `{"initial":"3.4.5-rc.1"}` {
			t.Fatalf("marshalled to %s", data)
		}
		var out model
		if err := json.Unmarshal(data, &out); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if out.Initial.Compare(in.Initial) != 0 {
			t.Errorf("round trip changed the version: %s", out.Initial)
		}
		if err := out.Initial.UnmarshalText([]byte("not a version")); err == nil {
			t.Error("UnmarshalText accepted a malformed version")
		}
		text, err := in.Initial.MarshalText()
		if err != nil || string(text) != "3.4.5-rc.1" {
			t.Errorf("MarshalText = %q, %v", text, err)
		}
	})
}

// TestPublicAPICCMEValueTypeContracts exercises the exported value types every
// consumer of a Result reads: bump ordering, propagation values, depths,
// dependency kinds and the diagnostic vocabulary.
func TestPublicAPICCMEValueTypeContracts(t *testing.T) {
	t.Run("bumps parse, order and render", func(t *testing.T) {
		for _, c := range []struct {
			text string
			want ccme.Bump
		}{
			{"none", ccme.BumpNone},
			{"patch", ccme.BumpPatch},
			{"minor", ccme.BumpMinor},
			{"major", ccme.BumpMajor},
		} {
			got, ok := ccme.ParseBump(c.text)
			if !ok || got != c.want {
				t.Errorf("ParseBump(%q) = %v, %v", c.text, got, ok)
			}
			if got.String() != c.text {
				t.Errorf("Bump.String() = %q, want %q", got.String(), c.text)
			}
		}
		if _, ok := ccme.ParseBump("enormous"); ok {
			t.Error("ParseBump accepted an unknown level")
		}
		if got := ccme.Bump(99).String(); got != "invalid" {
			t.Errorf("Bump(99).String() = %q", got)
		}
		if got := ccme.MaxBump(ccme.BumpPatch, ccme.BumpMajor); got != ccme.BumpMajor {
			t.Errorf("MaxBump = %v", got)
		}
		if got := ccme.MaxBump(ccme.BumpMajor, ccme.BumpPatch); got != ccme.BumpMajor {
			t.Errorf("MaxBump is not symmetric: %v", got)
		}
	})

	t.Run("propagation values parse and resolve", func(t *testing.T) {
		for _, c := range []struct {
			value ccme.Propagate
			unit  ccme.Bump
			want  ccme.Bump
		}{
			{ccme.PropagateNone, ccme.BumpMajor, ccme.BumpNone},
			{ccme.PropagatePatch, ccme.BumpMajor, ccme.BumpPatch},
			{ccme.PropagateMinor, ccme.BumpPatch, ccme.BumpMinor},
			{ccme.PropagateMajor, ccme.BumpNone, ccme.BumpMajor},
			{ccme.PropagateInherit, ccme.BumpMinor, ccme.BumpMinor},
			{ccme.Propagate("nonsense"), ccme.BumpMajor, ccme.BumpNone},
		} {
			if got := c.value.Bump(c.unit); got != c.want {
				t.Errorf("Propagate(%q).Bump(%v) = %v, want %v", string(c.value), c.unit, got, c.want)
			}
		}
		for _, ok := range []string{"none", "patch", "minor", "major", "inherit"} {
			if _, valid := ccme.ParsePropagate(ok); !valid {
				t.Errorf("ParsePropagate(%q) rejected a valid value", ok)
			}
		}
		for _, bad := range []string{"", "min", "MINOR", "inherits"} {
			if _, valid := ccme.ParsePropagate(bad); valid {
				t.Errorf("ParsePropagate(%q) accepted an invalid value", bad)
			}
		}
	})

	t.Run("depths render their two spellings", func(t *testing.T) {
		if !ccme.DepthAll.IsAll() || ccme.DepthAll.String() != "all" {
			t.Errorf("DepthAll = %v", ccme.DepthAll)
		}
		if got := ccme.Depth(0).String(); got != "0" {
			t.Errorf("Depth(0).String() = %q", got)
		}
		if got := ccme.Depth(12).String(); got != "12" {
			t.Errorf("Depth(12).String() = %q", got)
		}
		if got := ccme.Depth(-7).String(); got != "-7" {
			t.Errorf("Depth(-7).String() = %q", got)
		}
	})

	t.Run("dependency kinds are validated", func(t *testing.T) {
		for _, ok := range []ccme.DependencyKind{
			ccme.KindDependencies, ccme.KindDevDependencies, ccme.KindPeerDependencies,
			ccme.KindOptionalDependencies, ccme.KindAll,
		} {
			if got, valid := ccme.ParseDependencyKind(string(ok)); !valid || got != ok {
				t.Errorf("ParseDependencyKind(%q) = %q, %v", string(ok), string(got), valid)
			}
		}
		if _, valid := ccme.ParseDependencyKind("bundledDependencies"); valid {
			t.Error("ParseDependencyKind accepted an unknown field")
		}
		kinds := ccme.DefaultPropagateKinds()
		if slices.Contains(kinds, ccme.KindDevDependencies) {
			t.Errorf("DefaultPropagateKinds() = %v, want devDependencies absent", kinds)
		}
	})

	t.Run("the diagnostic vocabulary classifies its own codes", func(t *testing.T) {
		if !ccme.IsDiagnosticCode(ccme.CodeE158) || !ccme.IsDiagnosticCode(ccme.CodeW214) {
			t.Error("IsDiagnosticCode denied a code the package emits")
		}
		for _, engine := range []string{"E210", "W172", "", "nonsense"} {
			if ccme.IsDiagnosticCode(engine) {
				t.Errorf("IsDiagnosticCode(%q) = true, want the engine's codes excluded", engine)
			}
		}
		silent := ccme.SilentFailureCodes()
		if !slices.Contains(silent, ccme.CodeW155) || !slices.Contains(silent, ccme.CodeW156) {
			t.Errorf("SilentFailureCodes() = %v", silent)
		}
		silent[0] = "mutated"
		if again := ccme.SilentFailureCodes(); again[0] == "mutated" {
			t.Error("SilentFailureCodes() returned a shared slice")
		}
	})

	t.Run("severities and positions render", func(t *testing.T) {
		if ccme.SeverityError.String() != "error" || ccme.SeverityWarning.String() != "warning" {
			t.Error("severity rendering changed")
		}
		if got := ccme.Severity(9).String(); got != "unknown" {
			t.Errorf("Severity(9).String() = %q", got)
		}
		if got := (ccme.Position{Line: 3, Column: 14}).String(); got != "3:14" {
			t.Errorf("Position.String() = %q", got)
		}
		if got := (ccme.ReleaseAsKind(9)).String(); got != "invalid" {
			t.Errorf("ReleaseAsKind(9).String() = %q", got)
		}
	})

	t.Run("a parse error renders one, several and no diagnostics", func(t *testing.T) {
		empty := &ccme.ParseError{}
		if got := empty.Error(); got != "ccme: parse failed" {
			t.Errorf("empty ParseError.Error() = %q", got)
		}
		if got := empty.Codes(); len(got) != 0 {
			t.Errorf("empty ParseError.Codes() = %v", got)
		}
		p := ccme.DefaultParser()
		_, err := p.Parse("Feat: one")
		if err == nil {
			t.Fatal("a miscased type parsed cleanly")
		}
		if !strings.HasPrefix(err.Error(), "ccme: ") || strings.Contains(err.Error(), "errors:") {
			t.Errorf("single-diagnostic error = %q", err.Error())
		}
		_, err = p.Parse("Feat: one\n---\nFix: two")
		if err == nil {
			t.Fatal("two miscased types parsed cleanly")
		}
		if !strings.Contains(err.Error(), "2 errors:") {
			t.Errorf("multi-diagnostic error = %q", err.Error())
		}
	})
}

// TestPublicAPICCMEConfigurationSurface exercises Config construction,
// defaulting, cloning and validation through the exported constructors.
func TestPublicAPICCMEConfigurationSurface(t *testing.T) {
	t.Run("the zero config is the specification default", func(t *testing.T) {
		p, err := ccme.NewParser(ccme.Config{})
		if err != nil {
			t.Fatalf("NewParser: %v", err)
		}
		got := p.Config()
		want := ccme.DefaultConfig()
		if got.Separator != want.Separator || got.MaxDescriptionLength != want.MaxDescriptionLength {
			t.Errorf("separator/limit = %q/%d", got.Separator, got.MaxDescriptionLength)
		}
		if got.Propagation.Bump != want.Propagation.Bump || got.Propagation.Channel != want.Propagation.Channel {
			t.Errorf("propagation = %#v", got.Propagation)
		}
		if got.Limits != want.Limits {
			t.Errorf("limits = %#v, want %#v", got.Limits, want.Limits)
		}
		if len(got.Types) != len(want.Types) {
			t.Errorf("types = %d entries, want %d", len(got.Types), len(want.Types))
		}
		if !slices.Equal(got.MessageLevelTrailers, ccme.DefaultMessageLevelTrailers()) {
			t.Errorf("message trailers = %v", got.MessageLevelTrailers)
		}
		if !slices.Equal(got.IssueTrailers, ccme.DefaultIssueTrailers()) {
			t.Errorf("issue trailers = %v", got.IssueTrailers)
		}
	})

	t.Run("the reported config is a copy", func(t *testing.T) {
		p := ccme.DefaultParser()
		cfg := p.Config()
		cfg.Types["feat"] = ccme.BumpNone
		cfg.Propagation.Kinds[0] = ccme.KindDevDependencies
		res, err := p.Parse("feat(app): add a thing")
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if res.Units[0].Bump != ccme.BumpMinor {
			t.Errorf("mutating the reported config changed the parser: bump = %v", res.Units[0].Bump)
		}
	})

	t.Run("the default type table is a fresh copy", func(t *testing.T) {
		first := ccme.DefaultTypes()
		first["feat"] = ccme.BumpNone
		if ccme.DefaultTypes()["feat"] != ccme.BumpMinor {
			t.Error("DefaultTypes() returned a shared map")
		}
	})

	t.Run("cloning preserves the nil-versus-empty distinction", func(t *testing.T) {
		src := ccme.Config{
			Types:                map[string]ccme.Bump{"deps": ccme.BumpPatch},
			AllowedChannels:      []string{},
			MessageLevelTrailers: []string{"Reviewed-By"},
		}
		clone := src.Clone()
		clone.Types["deps"] = ccme.BumpMajor
		clone.MessageLevelTrailers[0] = "mutated"
		if src.Types["deps"] != ccme.BumpPatch || src.MessageLevelTrailers[0] != "Reviewed-By" {
			t.Error("Clone shared its backing storage")
		}
		if clone.AllowedChannels == nil || len(clone.AllowedChannels) != 0 {
			t.Errorf("Clone turned an empty slice into %#v", clone.AllowedChannels)
		}
		if clone.IssueTrailers != nil {
			t.Errorf("Clone turned a nil slice into %#v", clone.IssueTrailers)
		}
	})

	t.Run("an empty trailer list disables the default table", func(t *testing.T) {
		p, err := ccme.NewParser(ccme.Config{MessageLevelTrailers: []string{}})
		if err != nil {
			t.Fatalf("NewParser: %v", err)
		}
		if got := p.Config().MessageLevelTrailers; got == nil || len(got) != 0 {
			t.Errorf("message trailers = %#v, want an empty non-nil slice", got)
		}
	})

	t.Run("an invalid configuration is refused", func(t *testing.T) {
		cases := []struct {
			name string
			cfg  ccme.Config
		}{
			{"a separator under three characters", ccme.Config{Separator: "--"}},
			{"a separator carrying whitespace", ccme.Config{Separator: "- -"}},
			{"a separator outside printable ASCII", ccme.Config{Separator: "--ÿ"}},
			{"a separator that could begin a type", ccme.Config{Separator: "abc"}},
			{"an empty type name", ccme.Config{Types: map[string]ccme.Bump{"": ccme.BumpPatch}}},
			{"a miscased type name", ccme.Config{Types: map[string]ccme.Bump{"Feat": ccme.BumpPatch}}},
			{"a type name carrying a digit", ccme.Config{Types: map[string]ccme.Bump{"feat2": ccme.BumpPatch}}},
			{"an unknown propagation bump", ccme.Config{
				Propagation: ccme.PropagationConfig{Bump: ccme.Propagate("enormous")}}},
			{"a negative propagation depth", ccme.Config{
				Propagation: ccme.PropagationConfig{Depth: ccme.Depth(-2)}}},
			{"a negative channel depth", ccme.Config{
				Propagation: ccme.PropagationConfig{ChannelDepth: ccme.Depth(-2)}}},
			{"an unknown dependency kind", ccme.Config{
				Propagation: ccme.PropagationConfig{Kinds: []ccme.DependencyKind{"bundled"}}}},
			{"a reserved default channel", ccme.Config{
				Propagation: ccme.PropagationConfig{Channel: "latest"}}},
			{"a miscased default channel", ccme.Config{
				Propagation: ccme.PropagationConfig{Channel: "Beta"}}},
			{"an over-long default channel", ccme.Config{
				Propagation: ccme.PropagationConfig{Channel: strings.Repeat("b", 33)}}},
			{"a default channel carrying an illegal character", ccme.Config{
				Propagation: ccme.PropagationConfig{Channel: "be_ta"}}},
			{"a reserved allowed channel", ccme.Config{AllowedChannels: []string{"latest"}}},
			{"an empty allowed channel", ccme.Config{AllowedChannels: []string{""}}},
			{"a negative unit bound", ccme.Config{Limits: ccme.Limits{UnitsPerMessage: -1}}},
			{"a negative scope-term bound", ccme.Config{Limits: ccme.Limits{ScopeTermsPerUnit: -1}}},
			{"a negative byte bound", ccme.Config{Limits: ccme.Limits{MessageBytes: -1}}},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				if _, err := ccme.NewParser(c.cfg); err == nil {
					t.Fatalf("NewParser accepted %#v", c.cfg)
				}
				defer func() {
					if recover() == nil {
						t.Error("MustNewParser did not panic on an invalid configuration")
					}
				}()
				ccme.MustNewParser(c.cfg)
			})
		}
	})

	t.Run("a partially filled config validates its own zero values", func(t *testing.T) {
		if err := (ccme.Config{}).Validate(); err == nil {
			t.Error("Validate accepted a zero separator; NewParser fills it in first")
		}
		full := ccme.DefaultConfig()
		if err := full.Validate(); err != nil {
			t.Errorf("DefaultConfig().Validate() = %v", err)
		}
	})

	t.Run("the configured propagation defaults reach every unit", func(t *testing.T) {
		p, err := ccme.NewParser(ccme.Config{
			Propagation: ccme.PropagationConfig{
				Bump:         ccme.PropagateMajor,
				Depth:        ccme.DepthAll,
				ChannelDepth: ccme.Depth(2),
				Kinds:        []ccme.DependencyKind{ccme.KindAll},
				Channel:      "beta",
			},
		})
		if err != nil {
			t.Fatalf("NewParser: %v", err)
		}
		res, err := p.Parse("feat(app): add a thing")
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		d := res.Units[0].Directives
		if d.Propagate != ccme.PropagateMajor || !d.Depth.IsAll() {
			t.Errorf("bump axis = %q / %v", string(d.Propagate), d.Depth)
		}
		if d.PropagateChannel.To != "beta" || d.ChannelDepth != ccme.Depth(2) {
			t.Errorf("channel axis = %#v / %v", d.PropagateChannel, d.ChannelDepth)
		}
		if !slices.Equal(d.Kinds, []ccme.DependencyKind{ccme.KindAll}) {
			t.Errorf("kinds = %v", d.Kinds)
		}
		if d.PropagateSet || d.DepthSet || d.PropagateChannelSet || d.ChannelDepthSet {
			t.Error("a configured default must not report itself as stated by the author")
		}
	})

	t.Run("an empty kinds list traverses no edges", func(t *testing.T) {
		p, err := ccme.NewParser(ccme.Config{
			Propagation: ccme.PropagationConfig{Kinds: []ccme.DependencyKind{}},
		})
		if err != nil {
			t.Fatalf("NewParser: %v", err)
		}
		res, err := p.Parse("feat(app): add a thing")
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if got := res.Units[0].Directives.Kinds; got == nil || len(got) != 0 {
			t.Errorf("kinds = %#v, want an empty non-nil slice", got)
		}
	})

	t.Run("a stable default channel is accepted", func(t *testing.T) {
		if _, err := ccme.NewParser(ccme.Config{
			Propagation: ccme.PropagationConfig{Channel: ccme.ChannelStable},
		}); err != nil {
			t.Errorf("NewParser refused a stable default channel: %v", err)
		}
	})
}
