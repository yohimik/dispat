package config

// The shape a parsed config file is handed over in, and the two rules that
// shape carries.
//
// Reading the file is refs.go's, folding its keys is lowerTree's, and turning
// the result into the model is decoder.go's. What is left in between is this:
// the settings map, which is the lowered tree flattened to delimited key paths
// and rebuilt from them. The round trip is not a detour. It is where an object
// with no keys stops being a key at all, and where a key spelled with the
// delimiter becomes the levels it names, and both of those are part of the
// config language rather than an implementation detail of whoever reads it
// next.
//
// The pruning is load-bearing: an opt-in block written as a bare `{}` says
// nothing rather than enabling itself at its defaults, which is what lets a
// pointer sub-object mean "this layer did not speak". Both rules predate the
// first-party decoder and are kept unchanged, because a config that loads
// today has to load the same way tomorrow.

import "strings"

// keyDelim is the separator a nested key path is spelled with. It is the
// decoder's own convention, not the config language's: settings flattens the
// tree to delimited paths and rebuilds it from them, which is where a key
// containing the delimiter becomes two levels. It is also the separator an
// error names a nested key with, so the path a message prints is the path the
// flattening speaks in.
const keyDelim = "."

// settings renders the lowered tree as the map the decoder reads. It is the
// tree flattened to delimited key paths and rebuilt from them, which is what
// makes two of the config language's quieter rules true: an object with no
// keys is not a key at all, so `autoVersion: {}` says nothing rather than
// enabling the block at its defaults; and a key spelled with the delimiter is
// the levels it names.
func settings(raw map[string]any) map[string]any {
	flat := make(map[string]any, len(raw))
	flattenInto(flat, raw, "")
	out := make(map[string]any, len(flat))
	for key, val := range flat {
		path := strings.Split(key, keyDelim)
		deepest(out, path[:len(path)-1])[path[len(path)-1]] = val
	}
	return out
}

// flattenInto records every leaf of a lowered tree under its delimited path. A
// map is descended into rather than recorded, which is why an empty one leaves
// nothing behind; everything else, lists included, is a leaf.
func flattenInto(out map[string]any, m map[string]any, prefix string) {
	if prefix != "" {
		prefix += keyDelim
	}
	for k, val := range m {
		full := prefix + k
		if child, ok := val.(map[string]any); ok {
			flattenInto(out, child, full)
			continue
		}
		out[full] = val
	}
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
