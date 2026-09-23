package config

// What "the same name" means, in one place.
//
// A name is matched case-insensitively wherever it is looked up — a section, a
// task, a variable — so the folding happens at the lookup, where the two
// spellings actually meet, and never at the key, where it would rename what
// the author wrote. Two keys of one object that fold together are refused as
// it is decoded, because a name with two spellings in one place has no lookup
// that could answer for it.

import (
	"sort"
	"strings"
	"unicode"
)

// Fold renders a name in the one spelling the decode's tables are keyed by.
//
// It is the canonical representative of each Unicode SimpleFold cycle, the
// equivalence LookupFold uses. A lower-case ASCII name takes the allocation-
// free path. Lowercasing alone would split Σ and ς into different keys.
func Fold(s string) string {
	upper := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 0x80 {
			return strings.Map(func(r rune) rune {
				smallest := r
				for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
					if next < smallest {
						smallest = next
					}
				}
				lower := unicode.ToLower(smallest)
				if lower == smallest {
					return smallest
				}
				for next := unicode.SimpleFold(smallest); next != smallest; next = unicode.SimpleFold(next) {
					if next == lower {
						return lower
					}
				}
				return smallest
			}, s)
		}
		if 'A' <= c && c <= 'Z' {
			upper = true
		}
	}
	if !upper {
		return s
	}
	out := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		out[i] = c
	}
	return string(out)
}

// LookupFold finds what a map holds under a name, whatever case either side
// spells it with. It answers with the map's own key as well as the value, for
// the callers that have to record which entry they read.
//
// The exact key is tried first, which is both the common case and the cheap
// one; only a name spelled differently pays for the scan. Two keys of one map
// that fold together would make the scan's answer a matter of luck, so
// decoding refuses them, and every map reaching here holds at most one.
func LookupFold[T any](m map[string]T, name string) (string, T, bool) {
	if value, ok := m[name]; ok {
		return name, value, true
	}
	for key, value := range m {
		if strings.EqualFold(key, name) {
			return key, value, true
		}
	}
	var zero T
	return "", zero, false
}

// FoldKey finds the key a map already spells some way or another, for the
// callers that want the key alone: to replace what is there, or to refuse a
// second spelling of it.
func FoldKey[T any](m map[string]T, key string) (string, bool) {
	name, _, ok := LookupFold(m, key)
	return name, ok
}

// SortedKeys returns a map's keys in order, so a config with several mistakes
// always reports the same one first. A map has no order of its own, and a load
// that fails differently from one run to the next is a load nobody can fix.
func SortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
