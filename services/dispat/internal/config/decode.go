package config

// How a parsed config file becomes the model, and the conventions that
// decoding keeps.
//
// This used to be viper's job, and the conventions are still the ones viper
// applied, because the configuration language is defined by them: every map
// key is lowercased, so script, space and package names match
// case-insensitively; field names match case-insensitively too; scalars are
// weakly typed, so a bare number is a fine string and a bare string is a fine
// one-element list; and an unknown key is refused rather than dropped, which
// is what turns a config typo into an error instead of a silent no-op.
//
// dispat used two lines of viper and paid for a config file reader it never
// called, a filesystem abstraction, a file watcher and a cast library. What it
// used is here: the lowered tree (lowerTree), the shape the decoder reads
// (settings) and the decoder itself (decodeExact). The 214 mapstructure tags
// and the four decode hooks are unchanged — they were always
// go-viper/mapstructure hooks, and viper only ever composed them.

import (
	"reflect"
	"strings"

	"github.com/go-viper/mapstructure/v2"
)

// keyDelim is the separator a nested key path is spelled with. It is the
// decoder's own convention, not the config language's: settings flattens the
// tree to delimited paths and rebuilds it from them, which is where a key
// containing the delimiter becomes two levels. Kept as it was rather than
// changed, because a config that loads today must load the same way tomorrow.
const keyDelim = "."

// decodeOption adjusts the decoder before it runs. weakDecode is the one the
// package passes; the signature is a function of the config so a caller can
// add a hook without this file knowing what for.
type decodeOption func(*mapstructure.DecoderConfig)

// decodeExact decodes a settings map into the model, refusing any key the
// model has no field for.
//
// The stance is fixed here rather than at each call site: unknown keys are an
// error, input is weakly typed, field names match case-insensitively, and the
// two conversions every config file relies on — a duration written as a
// string, and a scalar written where a list belongs — run before the caller's
// own hooks in the chain the options build.
func decodeExact(src map[string]any, dst any, opts ...decodeOption) error {
	dc := &mapstructure.DecoderConfig{
		TagName:          "mapstructure",
		MatchName:        strings.EqualFold,
		WeaklyTypedInput: true,
		DecodeHook: mapstructure.ComposeDecodeHookFunc(
			mapstructure.StringToTimeDurationHookFunc(),
			stringToWeakSliceHook(","),
		),
	}
	for _, opt := range opts {
		opt(dc)
	}
	dc.ErrorUnused = true
	dc.Result = dst
	dec, err := mapstructure.NewDecoder(dc)
	if err != nil {
		return err
	}
	return dec.Decode(src)
}

// stringToWeakSliceHook splits a string into a list when a list is what the
// field holds. mapstructure's own StringToSliceHookFunc refuses anything but a
// []string destination, which would leave a scalar written where a list of
// records or of flow steps belongs undecodable.
func stringToWeakSliceHook(sep string) mapstructure.DecodeHookFunc {
	return func(from, to reflect.Type, data any) (any, error) {
		if from.Kind() != reflect.String || to.Kind() != reflect.Slice {
			return data, nil
		}
		raw := data.(string)
		if raw == "" {
			return []string{}, nil
		}
		return strings.Split(raw, sep), nil
	}
}

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
