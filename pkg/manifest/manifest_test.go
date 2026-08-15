package manifest

import "testing"

func TestKind(t *testing.T) {
	if KindDependencies.String() != "dependencies" {
		t.Error("the zero kind spells itself out")
	}
	if KindDevDependencies.String() != "devDependencies" {
		t.Error("named kinds stringify verbatim")
	}
	for _, k := range []Kind{KindDependencies, KindDevDependencies, KindPeerDependencies, KindOptionalDependencies} {
		if !k.Valid() {
			t.Errorf("%q must be valid", k)
		}
	}
	if Kind("scripts").Valid() {
		t.Error("a non-dependency field is not a kind")
	}
}

func TestParseKind(t *testing.T) {
	for word, want := range map[string]Kind{
		"":                     KindDependencies,
		"dependencies":         KindDependencies,
		"devDependencies":      KindDevDependencies,
		"peerDependencies":     KindPeerDependencies,
		"optionalDependencies": KindOptionalDependencies,
	} {
		got, ok := ParseKind(word)
		if !ok || got != want {
			t.Errorf("ParseKind(%q) = %q, %v; want %q", word, got, ok, want)
		}
	}
	for _, word := range []string{"Dependencies", "dev", "scripts", "dependency"} {
		if _, ok := ParseKind(word); ok {
			t.Errorf("ParseKind(%q) must refuse a word that is not a kind", word)
		}
	}
}

func TestIsRequirementsFile(t *testing.T) {
	for name, want := range map[string]bool{
		"requirements.txt":           true,
		"requirements-dev.txt":       true,
		"dev-requirements.txt":       true,
		"requirements-latest.txt":    true,
		"OLD-REQUIREMENTS-NOTES.txt": false,
		"readme.txt":                 false,
		"requirements.md":            false,
	} {
		if got := IsRequirementsFile(name); got != want {
			t.Errorf("IsRequirementsFile(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestNormalizePyName(t *testing.T) {
	for in, want := range map[string]string{
		"Acme_Core":  "acme-core",
		"acme.core":  "acme-core",
		"acme--core": "acme-core",
		" requests ": "requests",
		"uvicorn":    "uvicorn",
	} {
		if got := NormalizePyName(in); got != want {
			t.Errorf("NormalizePyName(%q) = %q, want %q", in, got, want)
		}
	}
}
