package app

import (
	"strings"
	"testing"
)

func TestReturnAddressRequiresAnExactReachableHTTPSOrigin(t *testing.T) {
	for _, tc := range []struct {
		endpoint, listen, want string
	}{
		{"", "", ""},
		{"https://first.example:8443", "", "first.example:8443"},
		{"https://first.example:8443", "0.0.0.0:8443", "0.0.0.0:8443"},
		{"https://[::1]:8443", "", "[::1]:8443"},
	} {
		got, err := ValidateReturnAddress(tc.endpoint, tc.listen)
		if err != nil || got != tc.want {
			t.Errorf("ValidateReturnAddress(%q, %q) = %q, %v; want %q", tc.endpoint, tc.listen, got, err, tc.want)
		}
	}
	for _, tc := range []struct{ endpoint, listen string }{
		{"", "127.0.0.1:8443"},
		{"http://first.example:8443", ""},
		{"https://user:secret@first.example:8443", ""},
		{"https://first.example", ""},
		{"https://first.example:0", ""},
		{"https://first.example:8443/path", ""},
		{"https://first.example:8443?token=secret", ""},
		{"https://first.example:8443", "not-a-listen-address"},
	} {
		_, err := ValidateReturnAddress(tc.endpoint, tc.listen)
		if err == nil || strings.Contains(err.Error(), "secret") {
			t.Errorf("unsafe return address %q / %q gave %v", tc.endpoint, tc.listen, err)
		}
	}
}
