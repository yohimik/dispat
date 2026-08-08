package globx

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMatch(t *testing.T) {
	for _, tc := range []struct {
		pattern, s string
		want       bool
	}{
		{"core", "core", true},
		{"core", "corelib", false},
		{"@acme/*", "@acme/ui", true},
		{"@acme/*", "@acme/deep/nested", true}, // "*" crosses "/" on purpose
		{"*", "anything", true},
		{"*", "", true},
		{"tmp-*", "tmp-a", true},
		{"tmp-*", "tmp-", true},
		{"tmp-*", "tmp", false},
		{"*-fixture", "load-fixture", true},
		{"*mid*", "a-mid-b", true},
		{"literal", "literally", false}, // pattern exhausted before the input
		{"a*c", "abbbc", true},
		{"a*c", "ab", false},
		{"", "", true},
		{"", "x", false},
	} {
		assert.Equalf(t, tc.want, Match(tc.pattern, tc.s), "Match(%q, %q)", tc.pattern, tc.s)
	}
}
