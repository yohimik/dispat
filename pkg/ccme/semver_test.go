package ccme

import (
	"strings"
	"testing"
)

func TestParseVersion(t *testing.T) {
	t.Parallel()

	valid := []struct {
		in         string
		major      uint64
		minor      uint64
		patch      uint64
		prerelease []string
		build      []string
	}{
		{"0.0.0", 0, 0, 0, nil, nil},
		{"1.4.2", 1, 4, 2, nil, nil},
		{"10.20.30", 10, 20, 30, nil, nil},
		{"1.0.0-beta.4", 1, 0, 0, []string{"beta", "4"}, nil},
		{"1.0.0-rc.1+build.5", 1, 0, 0, []string{"rc", "1"}, []string{"build", "5"}},
		{"1.2.3+sha.abc", 1, 2, 3, nil, []string{"sha", "abc"}},
		{"1.3.0-beta10", 1, 3, 0, []string{"beta10"}, nil},
		{"1.0.0-0", 1, 0, 0, []string{"0"}, nil},
		{"1.0.0-x-y-z.-", 1, 0, 0, []string{"x-y-z", "-"}, nil},
	}
	for _, tc := range valid {
		got, err := ParseVersion(tc.in)
		if err != nil {
			t.Errorf("ParseVersion(%q) = %v", tc.in, err)
			continue
		}
		if got.Major != tc.major || got.Minor != tc.minor || got.Patch != tc.patch {
			t.Errorf("ParseVersion(%q) core = %d.%d.%d", tc.in, got.Major, got.Minor, got.Patch)
		}
		if strings.Join(got.Prerelease, ".") != strings.Join(tc.prerelease, ".") {
			t.Errorf("ParseVersion(%q) prerelease = %v, want %v", tc.in, got.Prerelease, tc.prerelease)
		}
		if strings.Join(got.Build, ".") != strings.Join(tc.build, ".") {
			t.Errorf("ParseVersion(%q) build = %v, want %v", tc.in, got.Build, tc.build)
		}
	}

	invalid := []string{
		"", "1", "1.2", "v1.2.3", "1.2.3.4", "01.2.3", "1.02.3", "1.2.03",
		"1.2.3-", "1.2.3+", "1.2.3-01", "1.2.3-beta..1", "1.2.3 ", " 1.2.3",
		"1.2.3-be$ta", "a.b.c", "-1.2.3",
	}
	for _, in := range invalid {
		if v, err := ParseVersion(in); err == nil {
			t.Errorf("ParseVersion(%q) = %v, want an error", in, v)
		}
	}
}

func TestVersionStringDropsBuildMetadata(t *testing.T) {
	t.Parallel()

	v, err := ParseVersion("1.2.3-rc.1+build.5")
	if err != nil {
		t.Fatal(err)
	}
	if got := v.String(); got != "1.2.3-rc.1" {
		t.Errorf("String() = %q, want 1.2.3-rc.1", got)
	}
}

func TestVersionCompare(t *testing.T) {
	t.Parallel()

	// Ordered strictly ascending by SemVer precedence.
	ordered := []string{
		"0.9.0",
		"1.0.0-alpha",
		"1.0.0-alpha.1",
		"1.0.0-alpha.beta",
		"1.0.0-beta",
		"1.0.0-beta.2",
		"1.0.0-beta.9",
		"1.0.0-beta.10",
		"1.0.0-rc.1",
		"1.0.0",
		"1.0.1",
		"1.1.0",
		"2.0.0",
	}
	versions := make([]Version, len(ordered))
	for i, s := range ordered {
		v, err := ParseVersion(s)
		if err != nil {
			t.Fatalf("ParseVersion(%q) = %v", s, err)
		}
		versions[i] = v
	}
	for i := 0; i < len(versions); i++ {
		for j := 0; j < len(versions); j++ {
			got := versions[i].Compare(versions[j])
			want := compareInt(i, j)
			if got != want {
				t.Errorf("Compare(%q, %q) = %d, want %d", ordered[i], ordered[j], got, want)
			}
		}
	}

	// Build metadata is ignored for precedence.
	a, _ := ParseVersion("1.2.3+a")
	b, _ := ParseVersion("1.2.3+b")
	if a.Compare(b) != 0 {
		t.Errorf("build metadata must not affect precedence")
	}
}

func TestParseVersionUint64Bounds(t *testing.T) {
	t.Parallel()

	// The full uint64 range is legal SemVer; only true overflow is rejected.
	max := "18446744073709551615" // 2^64 - 1
	v, err := ParseVersion(max + ".0.0")
	if err != nil {
		t.Fatalf("ParseVersion(max uint64) = %v", err)
	}
	if v.Major != ^uint64(0) {
		t.Errorf("Major = %d, want max uint64", v.Major)
	}
	if _, err := ParseVersion("18446744073709551616.0.0"); err == nil {
		t.Error("2^64 must overflow")
	}
	if _, err := ParseVersion("99999999999999999999.0.0"); err == nil {
		t.Error("a 20-digit overflow must be rejected")
	}
}

func TestVersionCore(t *testing.T) {
	t.Parallel()

	v, _ := ParseVersion("1.0.1-beta.4+sha.abc")
	core := v.Core()
	if core.String() != "1.0.1" {
		t.Errorf("Core() = %q, want 1.0.1", core.String())
	}
	if core.IsPrerelease() || len(core.Build) != 0 || core.Raw != "" {
		t.Error("Core() must drop prerelease, build and Raw")
	}
}

// TestBumpedSemantics pins the documented behaviour before 1.0.0 freezes it:
// bumping moves the core and drops prerelease/build — a prerelease baseline
// graduates *and* moves — while BumpNone returns the value unchanged.
func TestBumpedSemantics(t *testing.T) {
	t.Parallel()

	stable, _ := ParseVersion("1.2.3")
	for _, tc := range []struct {
		bump Bump
		want string
	}{
		{BumpMajor, "2.0.0"},
		{BumpMinor, "1.3.0"},
		{BumpPatch, "1.2.4"},
	} {
		if got := stable.Bumped(tc.bump).String(); got != tc.want {
			t.Errorf("1.2.3 bumped %v = %q, want %q", tc.bump, got, tc.want)
		}
	}

	pre, _ := ParseVersion("1.2.0-beta.1")
	if got := pre.Bumped(BumpMinor).String(); got != "1.3.0" {
		t.Errorf("1.2.0-beta.1 bumped minor = %q, want 1.3.0 (graduate and move)", got)
	}
	if got := pre.Bumped(BumpNone); got.String() != "1.2.0-beta.1" || got.Raw != "1.2.0-beta.1" {
		t.Errorf("BumpNone must return the version unchanged, got %q (raw %q)", got.String(), got.Raw)
	}
}

func TestVersionTextMarshalling(t *testing.T) {
	t.Parallel()

	v, _ := ParseVersion("1.2.3-rc.1+build.5")
	text, err := v.MarshalText()
	if err != nil {
		t.Fatal(err)
	}
	if string(text) != "1.2.3-rc.1" {
		t.Errorf("MarshalText = %q, want 1.2.3-rc.1 (build metadata dropped)", text)
	}

	var back Version
	if err := back.UnmarshalText(text); err != nil {
		t.Fatal(err)
	}
	if back.Compare(v) != 0 {
		t.Errorf("round trip changed precedence: %q vs %q", back.String(), v.String())
	}
	if err := back.UnmarshalText([]byte("not-a-version")); err == nil {
		t.Error("UnmarshalText must reject malformed input")
	}
}

func TestIsPrerelease(t *testing.T) {
	t.Parallel()

	v, _ := ParseVersion("1.0.0-beta.0")
	if !v.IsPrerelease() {
		t.Error("1.0.0-beta.0 should be a prerelease")
	}
	v, _ = ParseVersion("1.0.0+meta")
	if v.IsPrerelease() {
		t.Error("build metadata is not a prerelease")
	}
}
