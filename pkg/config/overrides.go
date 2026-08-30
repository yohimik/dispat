package config

// The layer written over the file: command-line flags, an environment
// binding, a value a program computes for itself.
//
// An override is a key path and the value to put there, applied once the tree
// has become the settings map. Applying it there rather than to the tree is
// what lets it name a nested key with the same delimiter the file could have
// used, and what keeps the tree the file as it was read.
//
// The rule that makes an override safe is the delete-then-set: a file writing
// `logLevel` and an override writing `loglevel` would otherwise be two keys
// the decode refuses as a collision, over a value the operator passed
// correctly. Whichever spelling the file used goes, and the override's own
// spelling is what lands.

import "strings"

// Overrides is a set of values written over a loaded configuration, keyed by
// the delimited path of the key each one replaces. A key with no delimiter in
// it names a top-level key, which is the common case.
//
// The zero value — a nil map — is no overrides at all, so a caller with
// nothing to override passes nil.
type Overrides map[string]any

// MergeOverrides overlays one set of overrides onto another and returns a new
// map: over wins key by key. A nil over leaves base alone, which is what "this
// layer says nothing" has to mean.
func MergeOverrides(base, over Overrides) Overrides {
	if len(over) == 0 {
		return base
	}
	merged := make(Overrides, len(base)+len(over))
	for k, v := range base {
		merged[k] = v
	}
	for k, v := range over {
		merged[k] = v
	}
	return merged
}

// applyTo writes every override into a settings map, walking to the key each
// one names and creating the levels that are missing. Paths are applied in
// order, so two overrides that disagree about the shape of one level always
// resolve the same way.
func (o Overrides) applyTo(out map[string]any, delim string) {
	for _, key := range SortedKeys(o) {
		node := out
		segs := strings.Split(key, delim)
		for _, seg := range segs[:len(segs)-1] {
			name, existing, found := LookupFold(node, seg)
			child, isObject := existing.(map[string]any)
			if found && isObject {
				// A level the file already wrote keeps the file's spelling: the
				// override is replacing a key, not renaming the object above it.
				node = child
				continue
			}
			if found {
				delete(node, name)
			}
			child = map[string]any{}
			node[seg] = child
			node = child
		}
		last := segs[len(segs)-1]
		if existing, found := FoldKey(node, last); found && existing != last {
			delete(node, existing)
		}
		node[last] = o[key]
	}
}
