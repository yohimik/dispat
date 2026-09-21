package globx

import (
	"strings"
	"testing"
	"time"

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
		{"a*a", "a", false}, // the prefix and the suffix may not share a byte
		{"a*a", "aa", true},
		{"a**b", "ab", true}, // "**" is "*"
		{"*ab*ab*", "abab", true},
		{"*ab*ab*", "aba", false},
		{"a*b*c", "a-c-b-c", true}, // leftmost placement of "b" is enough
		// Only the pattern carries a metacharacter: a "*" in the subject is a
		// byte like any other, and must not consume the pattern's star.
		{"*", "*x", true},
		{"a*", "a*b", true},
		{"*b", "*ab", true},
		{"a*b", "a*", false},
	} {
		assert.Equalf(t, tc.want, IsMatch(tc.pattern, tc.s), "IsMatch(%q, %q)", tc.pattern, tc.s)
	}
}

// reference is the definition itself, written recursively: "*" matches any
// run of bytes. It is exponential and exists only to be obviously right.
func reference(pattern, s string) bool {
	if pattern == "" {
		return s == ""
	}
	if pattern[0] == '*' {
		for i := 0; i <= len(s); i++ {
			if reference(pattern[1:], s[i:]) {
				return true
			}
		}
		return false
	}
	return s != "" && pattern[0] == s[0] && reference(pattern[1:], s[1:])
}

// TestMatchAgreesWithTheDefinition checks every pattern and every subject up
// to a small length over a two-letter alphabet, which is where a segment
// placed too far left or right, or a prefix overlapping a suffix, shows up.
func TestMatchAgreesWithTheDefinition(t *testing.T) {
	var words func(alphabet string, n int) []string
	words = func(alphabet string, n int) []string {
		out := []string{""}
		for prev := out; n > 0; n-- {
			var next []string
			for _, w := range prev {
				for _, c := range alphabet {
					next = append(next, w+string(c))
				}
			}
			out, prev = append(out, next...), next
		}
		return out
	}
	for _, pattern := range words("ab*", 5) {
		for _, s := range words("ab", 6) {
			if got, want := IsMatch(pattern, s), reference(pattern, s); got != want {
				t.Fatalf("IsMatch(%q, %q) = %v, the definition says %v", pattern, s, got, want)
			}
		}
	}
}

// TestMatchIsLinear pins the bound of CCME §18.3 on the input that makes a
// restarting matcher quadratic: every restart re-reads half the subject.
func TestMatchIsLinear(t *testing.T) {
	const n = 1 << 20
	subject := strings.Repeat("a", n)
	pattern := "*" + strings.Repeat("a", n/2) + "b"
	start := time.Now()
	assert.False(t, IsMatch(pattern, subject))
	// The quadratic walk needs n*n/4 = 2.7e11 byte comparisons here, which is
	// minutes; the linear one needs about 1.5e6.
	assert.Less(t, time.Since(start), 5*time.Second)
}
