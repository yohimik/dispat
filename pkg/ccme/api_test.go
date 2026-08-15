package ccme

import (
	"errors"
	"strings"
	"testing"
)

// This file covers the exported surface that the behaviour-driven tests reach
// only incidentally: constructors, helpers, String methods and the small value
// types. Everything exported should be exercised at least once before release.

func TestBumpHelpers(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		bump Bump
		want string
	}{
		{BumpNone, "none"},
		{BumpPatch, "patch"},
		{BumpMinor, "minor"},
		{BumpMajor, "major"},
		{Bump(99), "invalid"},
	} {
		if got := tc.bump.String(); got != tc.want {
			t.Errorf("Bump(%d).String() = %q, want %q", int(tc.bump), got, tc.want)
		}
	}

	for _, tc := range []struct {
		in   string
		want Bump
		ok   bool
	}{
		{"none", BumpNone, true},
		{"patch", BumpPatch, true},
		{"minor", BumpMinor, true},
		{"major", BumpMajor, true},
		{"", BumpNone, false},
		{"Minor", BumpNone, false},
		{"inherit", BumpNone, false},
	} {
		got, ok := ParseBump(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("ParseBump(%q) = %s, %v; want %s, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}

	// Ordering is the whole point of the type (§2).
	if !(BumpNone < BumpPatch && BumpPatch < BumpMinor && BumpMinor < BumpMajor) {
		t.Error("bump levels are not ordered none < patch < minor < major")
	}
	for _, tc := range []struct{ a, b, want Bump }{
		{BumpNone, BumpNone, BumpNone},
		{BumpPatch, BumpMinor, BumpMinor},
		{BumpMajor, BumpPatch, BumpMajor},
		{BumpMinor, BumpMinor, BumpMinor},
	} {
		if got := MaxBump(tc.a, tc.b); got != tc.want {
			t.Errorf("MaxBump(%s, %s) = %s, want %s", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestPropagateHelpers(t *testing.T) {
	t.Parallel()

	for _, in := range []string{"none", "patch", "minor", "major", "inherit"} {
		if got, ok := ParsePropagate(in); !ok || string(got) != in {
			t.Errorf("ParsePropagate(%q) = %q, %v", in, got, ok)
		}
	}
	for _, in := range []string{"", "med", "Minor", "all", "direct"} {
		if got, ok := ParsePropagate(in); ok {
			t.Errorf("ParsePropagate(%q) = %q, want rejected", in, got)
		}
	}

	// inherit copies the unit's own bump; everything else is fixed (§8.2).
	for _, tc := range []struct {
		p    Propagate
		unit Bump
		want Bump
	}{
		{PropagateInherit, BumpMajor, BumpMajor},
		{PropagateInherit, BumpNone, BumpNone},
		{PropagateNone, BumpMajor, BumpNone},
		{PropagatePatch, BumpMajor, BumpPatch},
		{PropagateMinor, BumpPatch, BumpMinor},
		{PropagateMajor, BumpNone, BumpMajor},
		{Propagate("nonsense"), BumpMajor, BumpNone},
	} {
		if got := tc.p.Bump(tc.unit); got != tc.want {
			t.Errorf("%q.Bump(%s) = %s, want %s", tc.p, tc.unit, got, tc.want)
		}
	}
}

func TestDepthHelpers(t *testing.T) {
	t.Parallel()

	if !DepthAll.IsAll() {
		t.Error("DepthAll.IsAll() = false")
	}
	if Depth(1).IsAll() || Depth(0).IsAll() {
		t.Error("a numeric depth reported IsAll")
	}
	for _, tc := range []struct {
		d    Depth
		want string
	}{
		{DepthAll, "all"},
		{0, "0"},
		{1, "1"},
		{42, "42"},
		{1024, "1024"},
	} {
		if got := tc.d.String(); got != tc.want {
			t.Errorf("Depth(%d).String() = %q, want %q", int(tc.d), got, tc.want)
		}
	}
}

func TestDependencyKindHelpers(t *testing.T) {
	t.Parallel()

	for _, in := range []string{
		"dependencies", "devDependencies", "peerDependencies", "optionalDependencies", "*",
	} {
		if got, ok := ParseDependencyKind(in); !ok || string(got) != in {
			t.Errorf("ParseDependencyKind(%q) = %q, %v", in, got, ok)
		}
	}
	for _, in := range []string{"", "Dependencies", "deps", "dependency"} {
		if _, ok := ParseDependencyKind(in); ok {
			t.Errorf("ParseDependencyKind(%q) was accepted", in)
		}
	}
}

func TestScopeTermAndSetHelpers(t *testing.T) {
	t.Parallel()

	p := DefaultParser()
	res, err := p.ParseSubject("feat(core,-docs,@acme/*,.): x")
	if err != nil {
		t.Fatal(err)
	}
	scopes := res.Units[0].Scopes()

	if got, want := scopes.String(), "core,-docs,@acme/*,."; got != want {
		t.Errorf("ScopeSet.String() = %q, want %q", got, want)
	}
	if got, want := strings.Join(scopes.Names(), "|"), "core|docs|@acme/*|."; got != want {
		t.Errorf("ScopeSet.Names() = %q, want %q", got, want)
	}
	if got, want := scopes.Includes().String(), "core,@acme/*,."; got != want {
		t.Errorf("Includes() = %q, want %q", got, want)
	}
	if got, want := scopes.Excludes().String(), "-docs"; got != want {
		t.Errorf("Excludes() = %q, want %q", got, want)
	}
	if got := scopes[1].String(); got != "-docs" {
		t.Errorf("ScopeTerm.String() = %q, want -docs", got)
	}

	// A lone "-" is a package name, not an empty exclusion (Appendix C).
	res, err = p.ParseSubject("feat(-): x")
	if err != nil {
		t.Fatal(err)
	}
	if lone := res.Units[0].Scopes()[0]; lone.Exclude || lone.Name != "-" {
		t.Errorf(`scope "-" = %+v, want a literal include named "-"`, lone)
	}
}

func TestUnitAccessors(t *testing.T) {
	t.Parallel()

	p := DefaultParser()

	res, err := p.Parse("feat(core)!: a\n\nBREAKING CHANGE: the reason")
	if err != nil {
		t.Fatal(err)
	}
	u := res.Units[0]
	if u.IsCancel() || u.IsRelease() || u.IsControl() {
		t.Error("a feat unit reported as a control unit")
	}
	if !u.HasExplicitScope() {
		t.Error("HasExplicitScope() = false for feat(core)")
	}
	if got := u.BreakingDescription(); got != "the reason" {
		t.Errorf("BreakingDescription() = %q", got)
	}
	if u.TypeBump != BumpMinor || u.Bump != BumpMajor {
		t.Errorf("TypeBump = %s, Bump = %s; want minor, major", u.TypeBump, u.Bump)
	}
	if len(u.Footers) != 1 || !u.Footers[0].IsBreakingChange() {
		t.Errorf("footers = %+v, want one BREAKING CHANGE", u.Footers)
	}

	res, _ = p.Parse("cancel(*): reset release state")
	if !res.Units[0].IsCancel() || !res.Units[0].IsControl() {
		t.Error("cancel unit not recognised as a control unit")
	}
	res, _ = p.Parse("release(cli)%beta: x")
	if !res.Units[0].IsRelease() || !res.Units[0].IsControl() {
		t.Error("release unit not recognised as a control unit")
	}
	res, _ = p.Parse("feat: no scope")
	if res.Units[0].HasExplicitScope() {
		t.Error("HasExplicitScope() = true for an unscoped unit")
	}
	if res.Units[0].BreakingDescription() != "" {
		t.Error("BreakingDescription() should be empty without a footer")
	}
}

func TestResultAccessors(t *testing.T) {
	t.Parallel()

	p := DefaultParser()
	// One valid unit, one invalid, and a warning, so every accessor has
	// something to report.
	res, err := p.Parse("feat(core)^med: bad\n\n---\n\nwibble(cli): unknown type")
	if err == nil {
		t.Fatal("expected an error")
	}

	if !res.HasErrors() {
		t.Error("HasErrors() = false")
	}
	if got := len(res.Errors()); got != 1 {
		t.Errorf("Errors() = %d, want 1", got)
	}
	if got := len(res.Warnings()); got == 0 {
		t.Errorf("Warnings() = 0, want the W140 for the unknown type (codes: %s)", codesOf(res))
	}
	if len(res.Errors())+len(res.Warnings()) != len(res.Diagnostics) {
		t.Error("Errors() + Warnings() != Diagnostics")
	}
	if got := len(res.ValidUnits()); got != 1 {
		t.Errorf("ValidUnits() = %d, want 1", got)
	}
	// The unknown type maps to none, and the invalid unit contributes nothing.
	if got := res.Bump(); got != BumpNone {
		t.Errorf("Bump() = %s, want none", got)
	}

	var pe *ParseError
	if !errors.As(err, &pe) {
		t.Fatalf("error type = %T, want *ParseError", err)
	}
	if got := pe.Codes(); len(got) != 1 || got[0] != CodeE111 {
		t.Errorf("ParseError.Codes() = %v, want [E111]", got)
	}
	if !strings.Contains(pe.Error(), CodeE111) {
		t.Errorf("ParseError.Error() = %q, want it to mention E111", pe.Error())
	}

	// A clean parse allocates nothing for diagnostics and reports nil slices.
	clean, err := p.Parse("feat(core): a")
	if err != nil {
		t.Fatal(err)
	}
	if clean.Errors() != nil || clean.Warnings() != nil || clean.Codes() != nil {
		t.Error("a clean parse should report nil diagnostic slices")
	}
	if clean.HasErrors() {
		t.Error("HasErrors() = true on a clean parse")
	}
	if got := clean.Bump(); got != BumpMinor {
		t.Errorf("Bump() = %s, want minor", got)
	}

	// A multi-error message aggregates.
	multi, err := p.Parse("feat(core)^med: a\n\n---\n\nfix(cli)%Beta: b")
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := len(multi.Errors()); got != 2 {
		t.Errorf("Errors() = %d, want 2 (codes: %s)", got, codesOf(multi))
	}
	if !strings.Contains(err.Error(), "2 errors") {
		t.Errorf("aggregate error = %q, want it to say how many", err.Error())
	}
}

func TestParseErrorEmpty(t *testing.T) {
	t.Parallel()

	// Defensive: the zero value must still render something usable.
	pe := &ParseError{}
	if pe.Error() == "" {
		t.Error("empty ParseError.Error() is empty")
	}
	if got := pe.Codes(); len(got) != 0 {
		t.Errorf("empty ParseError.Codes() = %v", got)
	}
}

func TestDiagnosticRendering(t *testing.T) {
	t.Parallel()

	if got := SeverityError.String(); got != "error" {
		t.Errorf("SeverityError.String() = %q", got)
	}
	if got := SeverityWarning.String(); got != "warning" {
		t.Errorf("SeverityWarning.String() = %q", got)
	}
	if got := Severity(9).String(); got != "unknown" {
		t.Errorf("Severity(9).String() = %q", got)
	}
	if got := (Position{Line: 3, Column: 14}).String(); got != "3:14" {
		t.Errorf("Position.String() = %q, want 3:14", got)
	}

	d := Diagnostic{
		Code:     CodeE111,
		Severity: SeverityError,
		Message:  "unknown propagation value",
		Position: Position{Line: 1, Column: 12},
	}
	want := "1:12: error E111: unknown propagation value"
	if got := d.String(); got != want {
		t.Errorf("Diagnostic.String() = %q, want %q", got, want)
	}
	if !d.IsError() {
		t.Error("IsError() = false for an error diagnostic")
	}
	if (Diagnostic{Severity: SeverityWarning}).IsError() {
		t.Error("IsError() = true for a warning")
	}
}

func TestReleaseAsRendering(t *testing.T) {
	t.Parallel()

	p := DefaultParser()
	res, err := p.Parse("fix(core): a\n\nRelease-As: 4.0.0")
	if err != nil {
		t.Fatal(err)
	}
	ra := res.Units[0].Directives.ReleaseAs
	if ra == nil {
		t.Fatal("Release-As not parsed")
	}
	if got := ra.String(); got != "4.0.0" {
		t.Errorf("ReleaseAs.String() = %q, want the raw value", got)
	}
	if ra.Version.Raw != "4.0.0" {
		t.Errorf("Version.Raw = %q", ra.Version.Raw)
	}
}

func TestVersionBumped(t *testing.T) {
	t.Parallel()

	base, err := ParseVersion("1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		bump Bump
		want string
	}{
		{BumpMajor, "2.0.0"},
		{BumpMinor, "1.3.0"},
		{BumpPatch, "1.2.4"},
		{BumpNone, "1.2.3"},
	} {
		if got := base.Bumped(tc.bump).String(); got != tc.want {
			t.Errorf("%s.Bumped(%s) = %s, want %s", base, tc.bump, got, tc.want)
		}
	}

	// A bump drops the prerelease and build metadata: applyBump operates on the
	// stable baseline, and metadata is never carried forward (§12.1).
	pre, err := ParseVersion("1.2.3-beta.1+build.5")
	if err != nil {
		t.Fatal(err)
	}
	bumped := pre.Bumped(BumpPatch)
	if got := bumped.String(); got != "1.2.4" {
		t.Errorf("prerelease.Bumped(patch) = %s, want 1.2.4", got)
	}
	if bumped.IsPrerelease() || len(bumped.Build) != 0 || bumped.Raw != "" {
		t.Errorf("Bumped did not clear the prerelease, build and raw fields: %+v", bumped)
	}

	// Documented asymmetry: BumpNone returns the receiver untouched, so a
	// prerelease keeps its identifiers. Callers that want "the core version"
	// must not rely on Bumped(BumpNone) to strip them.
	if got := pre.Bumped(BumpNone).String(); got != "1.2.3-beta.1" {
		t.Errorf("prerelease.Bumped(none) = %s, want the receiver unchanged", got)
	}
}

func TestErrInvalidVersionIsMatchable(t *testing.T) {
	t.Parallel()

	_, err := ParseVersion("not-a-version")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !errors.Is(err, ErrInvalidVersion) {
		t.Errorf("error %v does not match ErrInvalidVersion", err)
	}
}

func TestFooterFields(t *testing.T) {
	t.Parallel()

	p := DefaultParser()
	res, err := p.Parse("fix(core): a\n\nPropagate: minor\nCloses #7\nSigned-off-by: A <a%x>\nX-Team: infra")
	if err != nil {
		t.Fatal(err)
	}
	got := res.Units[0].Footers
	if len(got) != 4 {
		t.Fatalf("footers = %+v, want 4", got)
	}

	if !got[0].Known || got[0].CanonicalKey != FooterPropagate || got[0].Separator != ": " {
		t.Errorf("registry footer = %+v", got[0])
	}
	if got[1].Separator != " #" || got[1].Value != "#7" || !got[1].IssueReference {
		t.Errorf("issue footer = %+v", got[1])
	}
	if !got[2].MessageLevel {
		t.Errorf("Signed-off-by not flagged message-level: %+v", got[2])
	}
	if got[3].Known || got[3].MessageLevel || got[3].IssueReference {
		t.Errorf("unknown footer misclassified: %+v", got[3])
	}
	if got[3].CanonicalKey != "X-Team" {
		t.Errorf("unknown key should keep its spelling, got %q", got[3].CanonicalKey)
	}
	for i, f := range got {
		if f.Position.Line != 3+i {
			t.Errorf("footer %d at line %d, want %d", i, f.Position.Line, 3+i)
		}
	}
}

func TestConfigAccessors(t *testing.T) {
	t.Parallel()

	// DefaultConfig is a fully populated, valid configuration.
	def := DefaultConfig()
	if err := def.Validate(); err != nil {
		t.Errorf("DefaultConfig() is invalid: %v", err)
	}
	if def.Separator != DefaultSeparator || def.MaxDescriptionLength != DefaultMaxDescriptionLength {
		t.Errorf("DefaultConfig() = %+v", def)
	}
	if def.Propagation.Bump != DefaultPropagate || def.Propagation.Depth != DefaultDepth ||
		def.Propagation.ChannelDepth != DefaultChannelDepth {
		t.Errorf("DefaultConfig().Propagation = %+v", def.Propagation)
	}
	if def.Propagation.Channel != DefaultPropagateChannel {
		t.Errorf("DefaultConfig().Propagation.Channel = %q", def.Propagation.Channel)
	}

	// Clone is deep: mutating a copy cannot reach the original.
	clone := def.Clone()
	clone.Types["feat"] = BumpMajor
	clone.Propagation.Kinds[0] = KindAll
	clone.MessageLevelTrailers[0] = "Nope"
	clone.IssueTrailers[0] = "Nope"
	if def.Types["feat"] != BumpMinor {
		t.Error("Clone shares the type table")
	}
	if def.Propagation.Kinds[0] != KindDependencies {
		t.Error("Clone shares the propagation kinds")
	}
	if def.MessageLevelTrailers[0] == "Nope" || def.IssueTrailers[0] == "Nope" {
		t.Error("Clone shares the trailer lists")
	}

	// An invalid configuration is reported, not silently accepted.
	bad := Config{Separator: "x-x"}
	if err := bad.withDefaults().Validate(); err == nil {
		t.Error("Validate() accepted a separator starting with a type character")
	}
	badDepth := Config{Propagation: PropagationConfig{ChannelDepth: Depth(-5)}}
	if err := badDepth.withDefaults().Validate(); err == nil {
		t.Error("Validate() accepted a negative propagation.channelDepth")
	}

	// The default helpers hand out fresh copies.
	a, b := DefaultTypes(), DefaultTypes()
	a["feat"] = BumpMajor
	if b["feat"] != BumpMinor {
		t.Error("DefaultTypes() returns a shared map")
	}
	ka, kb := DefaultPropagateKinds(), DefaultPropagateKinds()
	ka[0] = KindAll
	if kb[0] != KindDependencies {
		t.Error("DefaultPropagateKinds() returns a shared slice")
	}
	ma, mb := DefaultMessageLevelTrailers(), DefaultMessageLevelTrailers()
	ma[0] = "Nope"
	if mb[0] == "Nope" {
		t.Error("DefaultMessageLevelTrailers() returns a shared slice")
	}
	ia, ib := DefaultIssueTrailers(), DefaultIssueTrailers()
	ia[0] = "Nope"
	if ib[0] == "Nope" {
		t.Error("DefaultIssueTrailers() returns a shared slice")
	}
}

func TestMustNewParserPanics(t *testing.T) {
	t.Parallel()

	defer func() {
		if recover() == nil {
			t.Error("MustNewParser did not panic on an invalid configuration")
		}
	}()
	MustNewParser(Config{Separator: "ab"})
}

func TestParserConfigRoundTrip(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Separator:            "%%%",
		StrictTypes:          true,
		Lenient:              true,
		MaxDescriptionLength: 72,
		AllowedChannels:      []string{"beta"},
	}
	p := MustNewParser(cfg)
	got := p.Config()

	if got.Separator != "%%%" || !got.StrictTypes || !got.Lenient || got.MaxDescriptionLength != 72 {
		t.Errorf("Config() lost a setting: %+v", got)
	}
	if len(got.AllowedChannels) != 1 || got.AllowedChannels[0] != "beta" {
		t.Errorf("Config().AllowedChannels = %v", got.AllowedChannels)
	}
	// Defaults are filled in, not left zero.
	if got.Types == nil || got.Propagation.Kinds == nil || got.Propagation.Depth != DefaultDepth {
		t.Errorf("Config() did not report the resolved defaults: %+v", got)
	}
}
