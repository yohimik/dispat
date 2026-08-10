// Package manifest holds the vocabulary the scanner and writer modules share:
// the dependency-field kinds a manifest declares and the file-name rules both
// sides must agree on. It exists so the reading and writing halves of
// dispat's manifest support can never drift apart on what a kind is called
// or which files count as manifests.
package manifest

import "strings"

// Kind is the manifest dependency field a declaration came from (or an edit
// targets). The zero value is the plain `dependencies` field, mirroring the
// config model's dependency kinds, so the two convert by a cast.
type Kind string

// Dependency kinds, spelled exactly like the manifest fields they stand for.
const (
	KindDependencies         Kind = ""
	KindDevDependencies      Kind = "devDependencies"
	KindPeerDependencies     Kind = "peerDependencies"
	KindOptionalDependencies Kind = "optionalDependencies"
)

// String implements fmt.Stringer, spelling the zero value out.
func (k Kind) String() string {
	if k == KindDependencies {
		return "dependencies"
	}
	return string(k)
}

// Valid reports whether k is one of the four dependency kinds.
func (k Kind) Valid() bool {
	switch k {
	case KindDependencies, KindDevDependencies, KindPeerDependencies, KindOptionalDependencies:
		return true
	}
	return false
}

// NameWords splits a file's base name into its separator-delimited words.
func NameWords(base string) []string {
	return strings.FieldsFunc(base, func(r rune) bool {
		return r == '-' || r == '_' || r == '.'
	})
}

// NormalizePyName renders a Python distribution name in its PEP 503
// normalised form: lowercased, every run of "-", "_" and "." collapsed to a
// single "-", so "Acme_Core" and "acme.core" name the same package.
func NormalizePyName(name string) string {
	var b strings.Builder
	b.Grow(len(name))
	run := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		if r == '-' || r == '_' || r == '.' {
			run = true
			continue
		}
		if run && b.Len() > 0 {
			b.WriteByte('-')
		}
		run = false
		b.WriteRune(r)
	}
	return b.String()
}
