// Package envname holds the one definition of a portable environment
// variable name. It is a leaf on purpose: the script outputs the release
// executor reads, the conditions `dispat if` tests and the configuration's
// execution.secretEnv all name variables the same way, and none of them
// should pull in another's package to agree on it.
package envname

// IsValid reports whether name is a portable environment variable name:
// [A-Za-z_][A-Za-z0-9_]*. One definition is what keeps a name dispat accepts
// in an output, a name it accepts in a condition and a name it accepts as a
// secret variable the same set.
func IsValid(name string) bool {
	if name == "" {
		return false
	}
	for i, c := range name {
		letter := (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == '_'
		if !letter && (i == 0 || c < '0' || c > '9') {
			return false
		}
	}
	return true
}
