package config

// The shape a parsed config file is handed the decode in, and the two rules
// that shape carries.
//
// Reading the file is refs.go's and turning the result into a model is
// decode.go's. What is left in between is this: the settings map, which is the
// tree with an object holding no keys pruned away and a key spelled with the
// delimiter turned into the levels it names. Neither is an implementation
// detail of whoever reads next; both are part of the config language.
//
// The pruning is load-bearing: an opt-in block written as a bare `{}` says
// nothing rather than enabling itself at its defaults, which is what lets a
// pointer sub-object mean "this layer did not speak".
//
// It used to be spelled as a flatten into delimited paths and a rebuild from
// them, which is where both rules came from and which is why they are stated
// that way here. The round trip is gone — one recursive walk writes the leaves
// straight into the result, creating a level only when a leaf actually lands
// under it — and the rules are unchanged, including the pruning of a branch
// whose every leaf turned out to be an empty object.

import "strings"

// Settings renders the tree as the map DecodeObject reads, with ov written
// over it.
//
// The tree is not touched: every object in the result is a new map, so a
// caller that goes back to Tree.Root after decoding finds the file as it was
// read. Lists and scalars are shared with the tree rather than copied, because
// nothing in this package writes into one; a caller that means to write into
// the result should Clone the tree first.
//
// A nil Loader reads the defaults, which is the delimiter DefaultKeyDelim and
// nothing else that matters here.
func (t *Tree) Settings(l *Loader, ov Overrides) map[string]any {
	l = l.loader()
	out := make(map[string]any, len(t.Root))
	s := settings{delim: l.opts.KeyDelim, out: out}
	s.fill(t.Root, nil)
	ov.applyTo(out, l.opts.KeyDelim)
	return out
}

// settings carries the one walk's destination and delimiter.
type settings struct {
	delim string
	out   map[string]any
}

// fill records every leaf of the tree under the path it sits at. A map is
// descended into rather than recorded, which is why an empty one leaves
// nothing behind and why a branch holding nothing but empty ones disappears
// with them; everything else, lists included, is a leaf.
//
// path is the levels above m, already split on the delimiter. It is extended
// in place: a sibling's extension overwrites the previous sibling's, which is
// safe because the walk is depth-first and nothing keeps the slice — the
// strings in it become map keys, and a string is its own copy.
func (s *settings) fill(m map[string]any, path []string) {
	for _, k := range SortedKeys(m) {
		v := m[k]
		segs := s.extend(path, k)
		if child, ok := v.(map[string]any); ok {
			s.fill(child, segs)
			continue
		}
		deepest(s.out, segs[:len(segs)-1])[segs[len(segs)-1]] = v
	}
}

// extend adds one written key to a path, as the one level it usually is, or as
// the levels it names when it carries the delimiter. The common key pays for a
// scan and nothing else.
func (s *settings) extend(path []string, key string) []string {
	if !strings.Contains(key, s.delim) {
		return append(path, key)
	}
	return append(path, strings.Split(key, s.delim)...)
}

// deepest walks a path, creating the maps it names, and returns the innermost
// one. A path element already holding something that is not a map is replaced:
// the walk is building the shape the leaves go into, and a leaf's own path is
// what says the shape.
func deepest(m map[string]any, path []string) map[string]any {
	for _, k := range path {
		child, ok := m[k].(map[string]any)
		if !ok {
			child = map[string]any{}
			m[k] = child
		}
		m = child
	}
	return m
}

// IsSet answers whether a parsed tree holds a value at a top-level key, which
// is what a loader asks before it refuses one. The key is matched
// case-insensitively, like every other key of the language. A key written with
// no value is not set: the file mentioned it and said nothing.
func IsSet(root map[string]any, key string) bool {
	_, value, ok := LookupFold(root, key)
	return ok && value != nil
}

// Clone copies a tree, deeply, for a caller that means to write into one.
func (t *Tree) Clone() *Tree {
	if t == nil {
		return nil
	}
	return &Tree{
		Root:  cloneMap(t.Root),
		Files: append([]string(nil), t.Files...),
	}
}

// cloneMap copies one object, deeply.
func cloneMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = cloneValue(v)
	}
	return out
}

// cloneValue copies one node. The arms name every container that can reach
// here: the parsers produce string-keyed maps, generic maps (a yaml mapping
// with a non-string key), lists and scalars, and an override adds a list of
// strings. Everything else falls through as it came, a scalar being immutable
// and shared safely.
func cloneValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return cloneMap(t)
	case map[any]any:
		out := make(map[any]any, len(t))
		for k, val := range t {
			out[k] = cloneValue(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = cloneValue(val)
		}
		return out
	case []string:
		return append([]string(nil), t...)
	default:
		return v
	}
}
