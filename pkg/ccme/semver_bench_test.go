package ccme

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
)

var benchmarkVersion Version
var benchmarkVersionParts VersionParts

// The official SemVer 2.0.0 regex is a realistic validating alternative to
// the index scanner: https://semver.org/spec/v2.0.0.html#is-there-a-suggested-regular-expression-regex-to-check-a-semver-string
var benchmarkSemverRegex = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-((?:0|[1-9][0-9]*|[0-9]*[a-zA-Z-][0-9a-zA-Z-]*)(?:\.(?:0|[1-9][0-9]*|[0-9]*[a-zA-Z-][0-9a-zA-Z-]*))*))?(?:\+([0-9a-zA-Z-]+(?:\.[0-9a-zA-Z-]+)*))?$`)

func parseVersionPartsRegex(raw string) (VersionParts, bool) {
	match := benchmarkSemverRegex.FindStringSubmatch(raw)
	if match == nil {
		return VersionParts{}, false
	}
	parts := VersionParts{Raw: raw, Major: match[1], Minor: match[2], Patch: match[3]}
	if match[4] != "" {
		parts.Prerelease = strings.Split(match[4], ".")
	}
	if match[5] != "" {
		parts.Build = strings.Split(match[5], ".")
	}
	return parts, true
}

func TestBenchmarkRegexMatchesScannerFixtures(t *testing.T) {
	for _, raw := range []string{
		"1.2.3", "12.34.56-rc.7+sha.0123456789abcdef",
		strings.Repeat("9", 80) + ".0.3-alpha." + strings.Repeat("8", 80) + "+001",
	} {
		parts, err := ParseVersionParts(raw)
		if err != nil {
			t.Fatal(err)
		}
		other, ok := parseVersionPartsRegex(raw)
		if !ok || !reflect.DeepEqual(parts, other) {
			t.Errorf("regex differs for %q: %+v vs %+v", raw, parts, other)
		}
	}
	for _, raw := range []string{"01.2.3", "1.2.3-01", "1.2.3+", "1.2.3-β", "1.2.3+foo_bar"} {
		if _, err := ParseVersionParts(raw); err == nil {
			t.Errorf("scanner accepted invalid %q", raw)
		}
		if _, ok := parseVersionPartsRegex(raw); ok {
			t.Errorf("regex accepted invalid %q", raw)
		}
	}
}

func BenchmarkParseVersion(b *testing.B) {
	for _, raw := range []string{"1.2.3", "12.34.56-rc.7+sha.0123456789abcdef"} {
		b.Run(raw, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				version, err := ParseVersion(raw)
				if err != nil {
					b.Fatal(err)
				}
				benchmarkVersion = version
			}
		})
	}
}

func BenchmarkParseVersionParts(b *testing.B) {
	for _, raw := range []string{"1.2.3", "12.34.56-rc.7+sha.0123456789abcdef",
		strings.Repeat("9", 80) + ".0.3-alpha." + strings.Repeat("8", 80) + "+001"} {
		b.Run(raw, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				parts, err := ParseVersionParts(raw)
				if err != nil {
					b.Fatal(err)
				}
				benchmarkVersionParts = parts
			}
		})
	}
}

func BenchmarkParseVersionRegex(b *testing.B) {
	for _, raw := range []string{"1.2.3", "12.34.56-rc.7+sha.0123456789abcdef",
		strings.Repeat("9", 80) + ".0.3-alpha." + strings.Repeat("8", 80) + "+001"} {
		b.Run(raw, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				parts, ok := parseVersionPartsRegex(raw)
				if !ok {
					b.Fatal("valid fixture refused")
				}
				benchmarkVersionParts = parts
			}
		})
	}
}
