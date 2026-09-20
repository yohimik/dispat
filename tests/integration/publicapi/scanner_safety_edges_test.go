package publicapi_test

import (
	"testing"

	"github.com/yohimik/dispat/pkg/scanner"
)

// An empty plist is well formed as "no declared metadata" for the scanner:
// it remains discoverable by path without inventing an identity or version.
func TestPublicAPIScannerTreatsEmptyPlistAsMetadataFree(t *testing.T) {
	m := scanFixture(t, "Info.plist", "")
	if m.Ecosystem != scanner.EcosystemPlist || m.Name != "" || m.Version != "" || m.BuildNumber != "" {
		t.Fatalf("empty Info.plist = %+v", m)
	}
}
