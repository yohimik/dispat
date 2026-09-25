package envname

import "testing"

func TestIsValid(t *testing.T) {
	cases := map[string]bool{
		"":              false,
		"A":             true,
		"_":             true,
		"a1":            true,
		"DISPAT_OUTPUT": true,
		"1A":            false,
		"A-B":           false,
		"A B":           false,
		"ÄB":            false,
		"_9":            true,
	}
	for name, want := range cases {
		if got := IsValid(name); got != want {
			t.Errorf("IsValid(%q) = %v, want %v", name, got, want)
		}
	}
}
