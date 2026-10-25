package ccme

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestParseVersionPartsKeepsUnboundedCoreAndBuild(t *testing.T) {
	big := strings.Repeat("9", 80)
	raw := big + ".0." + big + "-alpha." + big + "+001.sha-ABC"
	parts, err := ParseVersionParts(raw)
	if err != nil {
		t.Fatal(err)
	}
	if parts.Raw != raw || parts.Major != big || parts.Minor != "0" || parts.Patch != big {
		t.Fatalf("core or raw changed: %+v", parts)
	}
	if !reflect.DeepEqual(parts.Prerelease, []string{"alpha", big}) ||
		!reflect.DeepEqual(parts.Build, []string{"001", "sha-ABC"}) {
		t.Fatalf("identifiers changed: %+v", parts)
	}
	if _, err := ParseVersion(raw); !errors.Is(err, ErrInvalidVersion) {
		t.Fatalf("bounded release parser accepted an overflowing core: %v", err)
	}
}

func TestParseVersionPartsRejectsMalformedVersions(t *testing.T) {
	for _, raw := range []string{
		"", "1", "1.2", "v1.2.3", "1.2.3.4", "00.1.2", "1.01.2", "1.2.03",
		"1.2.3-", "1.2.3+", "1.2.3-alpha..1", "1.2.3-01", "1.2.3-a.000",
		"1.2.3-α", "1.2.3+sha_1", "1.2.3 ", "-1.2.3",
	} {
		if parts, err := ParseVersionParts(raw); !errors.Is(err, ErrInvalidVersion) {
			t.Errorf("ParseVersionParts(%q) = %+v, %v; want invalid version", raw, parts, err)
		}
	}
}
