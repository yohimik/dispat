// Package globx holds the one glob matcher the tool shares: scope terms,
// autoVersion range globs and .dispatexclude patterns must all agree on what
// a glob means, and none of them describe filesystem paths — so
// filepath.Match's separator rules would be the wrong semantics for all
// three.
package globx

import "strings"

// IsMatch reports whether s matches pattern, where "*" matches any run of
// bytes, path separators included ("@acme/*" reaches "@acme/ui"). Only the
// pattern carries a metacharacter: a "*" in s is an ordinary byte.
//
// "*" is the only metacharacter, so the pattern is its literal segments: the
// first is a prefix test, the last a suffix test on what remains, and each
// segment between them takes its leftmost occurrence after the previous one.
// Leftmost is sufficient, because a match that places a segment further right
// still matches with that segment moved left. That is O(len(pattern)+len(s))
// on every input (CCME §18.3), which a walk that restarts one byte after its
// last "*" is not: "*aaab" against a run of "a" costs their product. No
// regular expression, no recursion.
func IsMatch(pattern, s string) bool {
	first := strings.IndexByte(pattern, '*')
	if first < 0 {
		return pattern == s
	}
	last := strings.LastIndexByte(pattern, '*')
	prefix, suffix := pattern[:first], pattern[last+1:]
	if len(s) < len(prefix)+len(suffix) ||
		!strings.HasPrefix(s, prefix) || !strings.HasSuffix(s, suffix) {
		return false
	}
	s = s[len(prefix) : len(s)-len(suffix)]
	for middle := pattern[first:last]; middle != ""; {
		// middle is "*seg*seg…*seg": drop the star, take the segment.
		middle = middle[1:]
		end := strings.IndexByte(middle, '*')
		if end < 0 {
			end = len(middle)
		}
		seg := middle[:end]
		middle = middle[end:]
		if seg == "" {
			continue // "**" is "*"
		}
		at := index(s, seg)
		if at < 0 {
			return false
		}
		s = s[at+len(seg):]
	}
	return true
}

// index returns the offset of the first occurrence of the non-empty sep in s,
// or -1. It is Knuth-Morris-Pratt, because the bound IsMatch promises has to
// hold on hostile input and strings.Index only promises it on average.
func index(s, sep string) int {
	var stack [32]int
	fail := stack[:0]
	if len(sep) > len(stack) {
		fail = make([]int, 0, len(sep))
	}
	fail = append(fail, 0)
	for i, k := 1, 0; i < len(sep); i++ {
		for k > 0 && sep[i] != sep[k] {
			k = fail[k-1]
		}
		if sep[i] == sep[k] {
			k++
		}
		fail = append(fail, k)
	}
	for i, k := 0, 0; i < len(s); i++ {
		for k > 0 && s[i] != sep[k] {
			k = fail[k-1]
		}
		if s[i] == sep[k] {
			k++
		}
		if k == len(sep) {
			return i - k + 1
		}
	}
	return -1
}
