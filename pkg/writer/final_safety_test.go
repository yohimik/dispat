package writer

import "testing"

func TestReadPubspecInlinePath(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  string
	}{
		{"plain path", `{path: ../core}`, "../core"},
		{"quoted path", `{path: "../core dir"}`, "../core dir"},
		{"version constraint", `^2.0.0`, ""},
		{"path-looking constraint", `../core`, ""},
		{"mapping without path", `{git: https://example.com/core.git}`, ""},
		{"non-scalar path", `{path: {nested: ../core}}`, ""},
		{"malformed flow mapping", `{path:`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := readPubspecInlinePath(tc.value); got != tc.want {
				t.Errorf("readPubspecInlinePath(%q) = %q, want %q", tc.value, got, tc.want)
			}
		})
	}
}
