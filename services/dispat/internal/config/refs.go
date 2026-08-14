package config

// How a config file becomes a tree. Each format dispat reads has a parser of
// its own, and this is the one place that calls them: the root config, a space
// or package folder's own file, the probes the ascent classifies candidates
// with, and the readers behind `dispat compute --write` all arrive here.
//
// Parsing the file rather than letting viper do it is what keeps every key
// spelled the way the file wrote it. Viper lowercases every map key it reads,
// which is what most of the configuration wants — script, space and package
// names all match case-insensitively — and wrong for environment variable
// names, so the tree is kept exact-case and viper is handed a lowercased copy
// of it. One parse then serves both, where reading the file twice used to be
// the price of an `env` object.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
	"github.com/spf13/viper"
	yaml "gopkg.in/yaml.v3"

	"github.com/spf13/pflag"
)

// errUnsupportedFormat is what a file dispat has no parser for returns. The
// extension decides the format, here as everywhere else.
var errUnsupportedFormat = errors.New("dispat reads json, yaml and toml config files")

// tree is one parsed config file: the document, and every file the document
// was composed from, in the order they were read.
type tree struct {
	root  map[string]any
	files []string
}

// readTree parses a config file into a tree whose keys are spelled as written.
func readTree(path string) (*tree, error) {
	doc, err := decodeFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", path, err)
	}
	root, err := documentObject(doc, path)
	if err != nil {
		return nil, err
	}
	return &tree{root: root, files: []string{path}}, nil
}

// documentObject asserts what a config file's top level has to be. An empty
// file is an empty object rather than an error: "this file says nothing" is
// validation's answer to give, and it can name the keys it wanted.
func documentObject(doc any, path string) (map[string]any, error) {
	switch top := doc.(type) {
	case nil:
		return map[string]any{}, nil
	case map[string]any:
		return top, nil
	default:
		return nil, fmt.Errorf("cannot read %s: the top level is not an object", path)
	}
}

// decodeFile parses one file with its format's own parser and returns whatever
// the document holds.
func decodeFile(path string) (any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		var doc any
		if err := json.Unmarshal(data, &doc); err != nil {
			return nil, err
		}
		return doc, nil
	case ".yaml", ".yml":
		var doc any
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return nil, err
		}
		return doc, nil
	case ".toml":
		// A TOML document is a table and nothing else, so the target says so
		// rather than asking the decoder to guess.
		doc := map[string]any{}
		if err := toml.Unmarshal(data, &doc); err != nil {
			return nil, err
		}
		return doc, nil
	default:
		return nil, errUnsupportedFormat
	}
}

// viperFromTree hands a parsed tree to viper, which is still what decodes the
// model: the unknown-key refusal, the case-insensitive key matching and the
// flag bindings are all its.
//
// The copy is not an optimisation to skip. MergeConfigMap lowercases the map
// it is given in place, recursively, and then keeps those very sub-maps — so
// handing it the tree itself would rename the keys the env pass still has to
// read exactly, and leave viper sharing memory with them.
func viperFromTree(t *tree, flags *pflag.FlagSet) (*viper.Viper, error) {
	v := viper.New()
	if err := v.MergeConfigMap(cloneTree(t.root)); err != nil {
		return nil, err
	}
	if flags == nil {
		return v, nil
	}
	for key, flagName := range boundFlags {
		if f := flags.Lookup(flagName); f != nil {
			if err := v.BindPFlag(key, f); err != nil {
				return nil, fmt.Errorf("binding flag %s: %w", flagName, err)
			}
		}
	}
	return v, nil
}

// boundFlags are the config keys an explicitly set flag overrides, and the
// flags that override them.
var boundFlags = map[string]string{
	"concurrency": "concurrency",
	"logLevel":    "log-level",
	"logFormat":   "log-format",
}

// lookupString reads a string field of a parsed node, case-insensitively.
func lookupString(node map[string]any, key string) (string, bool) {
	v, ok := lookupFold(node, key)
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

// cloneTree copies a parsed tree, deeply.
func cloneTree(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = cloneValue(v)
	}
	return out
}

// cloneValue copies one node. The three parsers in use produce string-keyed
// maps, generic maps (a yaml mapping with a non-string key) and slices;
// everything else they produce is a scalar, which is immutable and shared
// safely.
func cloneValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return cloneTree(t)
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
	case nil, string, bool, int, int64, float64:
		return v
	default:
		return cloneReflect(v)
	}
}

// cloneReflect copies the typed maps and slices today's parsers do not
// produce, so that changing one of them can never quietly leave viper sharing
// memory with the tree. Anything that is not a map or a slice is a scalar and
// is returned as it came.
func cloneReflect(v any) any {
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Map:
		out := reflect.MakeMapWithSize(rv.Type(), rv.Len())
		iter := rv.MapRange()
		for iter.Next() {
			out.SetMapIndex(iter.Key(), clonedElem(iter.Value().Interface(), rv.Type().Elem()))
		}
		return out.Interface()
	case reflect.Slice:
		out := reflect.MakeSlice(rv.Type(), rv.Len(), rv.Len())
		for i := 0; i < rv.Len(); i++ {
			out.Index(i).Set(clonedElem(rv.Index(i).Interface(), rv.Type().Elem()))
		}
		return out.Interface()
	default:
		return v
	}
}

// clonedElem is one element of a reflected map or slice, as a value the
// element's own type accepts: a nil holds no type to build one from.
func clonedElem(v any, typ reflect.Type) reflect.Value {
	cloned := cloneValue(v)
	if cloned == nil {
		return reflect.Zero(typ)
	}
	return reflect.ValueOf(cloned)
}
