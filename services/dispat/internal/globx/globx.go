// Package globx holds the one glob matcher the tool shares: scope terms,
// autoVersion range globs and .dispatignore patterns must all agree on what
// a glob means, and none of them describe filesystem paths — so
// filepath.Match's separator rules would be the wrong semantics for all
// three.
package globx

// Match reports whether s matches pattern, where "*" matches any run of
// bytes, path separators included ("@acme/*" reaches "@acme/ui"). The
// matcher is an iterative two-pointer walk with a single backtrack point: no
// regular expression, no recursion, and linear on every input a pattern can
// be.
func Match(pattern, s string) bool {
	star, mark := -1, 0
	i, j := 0, 0
	for i < len(s) {
		switch {
		case j < len(pattern) && pattern[j] == s[i]:
			i++
			j++
		case j < len(pattern) && pattern[j] == '*':
			star, mark = j, i
			j++
		case star >= 0:
			mark++
			i, j = mark, star+1
		default:
			return false
		}
	}
	for j < len(pattern) && pattern[j] == '*' {
		j++
	}
	return j == len(pattern)
}
