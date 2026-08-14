package config

// How a config file becomes a tree, and the one thing that can happen on the
// way: a `$ref` naming another file, whose content becomes the value.
//
// Each format dispat reads has a parser of its own, and this is the one place
// that calls them: the root config, a space or package folder's own file, the
// probes the ascent classifies candidates with, and the readers behind
// `dispat compute --write` all arrive here.
//
// Parsing the file rather than letting viper do it is what keeps every key
// spelled the way the file wrote it. Viper lowercases every map key it reads,
// which is what most of the configuration wants — script, space and package
// names all match case-insensitively — and wrong for environment variable
// names, so the tree is kept exact-case and viper is handed a lowercased copy
// of it. One parse then serves both, where reading the file twice used to be
// the price of an `env` object.
//
// A reference is resolved against the directory of the file that wrote it, and
// the file it names may hold references of its own, resolved against theirs. A
// file is never cached between positions: the same fragment referenced from
// two keys is read twice and each copy is its own value, which is what makes a
// file that appears twice in one chain, and only that, a cycle.

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

// refKey is the key that makes an object a reference. It is matched exactly:
// a config file is written by hand, and one spelling keeps `$Ref` an unknown
// key rather than a reference that works in some files and not others.
const refKey = "$ref"

// maxRefDepth caps how far references may nest. The chain check below catches
// a file that reaches itself under a path it can be named by; this catches the
// ones it cannot, such as two names for one file through a symlink.
const maxRefDepth = 32

// tree is one parsed config file: the document, and every file the document
// was composed from, in the order they were read.
type tree struct {
	root  map[string]any
	files []string
}

// readTree parses a config file into a tree whose keys are spelled as written,
// with every `$ref` it holds resolved.
func readTree(path string) (*tree, error) {
	r := &refResolver{}
	doc, err := r.document(path)
	if err != nil {
		return nil, err
	}
	root, err := documentObject(doc, path)
	if err != nil {
		return nil, err
	}
	return &tree{root: root, files: r.files}, nil
}

// refResolver resolves the references of one file and everything it pulls in.
// It holds the chain being followed, so a cycle is reported as the path that
// closed it rather than as a stack overflow.
type refResolver struct {
	chain []refFrame
	files []string
}

// refFrame is one file on the chain, and the key that carried the reference
// out of it.
type refFrame struct {
	file string // as it was opened, which is how the error names it
	id   string // the same file, absolute and cleaned, for comparison
	key  string
}

// document parses one file and resolves what it holds: the entry point for the
// file readTree was asked for and for every file a reference names.
func (r *refResolver) document(path string) (any, error) {
	doc, err := decodeFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", path, err)
	}
	r.files = append(r.files, path)
	return r.value(doc, path, "")
}

// value resolves one node. A reference is replaced by the file it names; every
// other object and array is walked, because a reference can sit at any depth.
// label is where the node sits in its own file, for the errors.
func (r *refResolver) value(v any, file, label string) (any, error) {
	switch node := v.(type) {
	case map[string]any:
		return r.object(node, file, label)
	case []any:
		out := make([]any, len(node))
		for i, item := range node {
			resolved, err := r.value(item, file, fmt.Sprintf("%s[%d]", label, i))
			if err != nil {
				return nil, err
			}
			out[i] = resolved
		}
		return out, nil
	default:
		return v, nil
	}
}

// object resolves an object, which is where a reference can be. Keys are
// walked in order so that a file with two mistakes in it always reports the
// same one first.
func (r *refResolver) object(node map[string]any, file, label string) (any, error) {
	target, isRef, err := refTarget(node)
	if err != nil {
		return nil, fmt.Errorf("%s: %s: %w", file, labelOr(label), err)
	}
	if !isRef {
		out := make(map[string]any, len(node))
		for _, key := range sortedKeys(node) {
			resolved, err := r.value(node[key], file, joinLabel(label, key))
			if err != nil {
				return nil, err
			}
			out[key] = resolved
		}
		return out, nil
	}

	base, err := r.follow(target, file, label)
	if err != nil {
		return nil, err
	}
	if len(node) == 1 {
		return base, nil
	}
	// Keys written beside a reference override what it brought in, so a shared
	// fragment can be used as it is in one place and adjusted in another.
	object, ok := base.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s: %s: $ref %q: the file is %s, so the keys beside the $ref have nothing to override",
			file, labelOr(label), target, kindOf(base))
	}
	for _, key := range sortedKeys(node) {
		if key == refKey {
			continue
		}
		resolved, err := r.value(node[key], file, joinLabel(label, key))
		if err != nil {
			return nil, err
		}
		// The overriding key is written whichever way the two files spell it:
		// leaving both spellings in would hand viper two keys that fold
		// together, and which one survived would be a matter of luck.
		if existing, found := foldKey(object, key); found {
			delete(object, existing)
		}
		object[key] = resolved
	}
	return object, nil
}

// follow reads the file a reference names, with the reference on the chain so
// that a file reaching back to itself is refused instead of followed forever.
func (r *refResolver) follow(target, file, label string) (any, error) {
	path := refPath(target, file)
	r.chain = append(r.chain, refFrame{file: file, id: identity(file), key: labelOr(label)})
	defer func() { r.chain = r.chain[:len(r.chain)-1] }()
	if err := r.checkChain(path); err != nil {
		return nil, err
	}

	doc, err := r.document(path)
	if err != nil {
		var chain *chainError
		if errors.As(err, &chain) {
			// A chain failure already names every file involved, so the frames
			// it unwinds through leave it as it is.
			return nil, err
		}
		return nil, fmt.Errorf("%s: %s: $ref %q: %w", file, labelOr(label), target, err)
	}
	if doc == nil {
		return nil, fmt.Errorf("%s: %s: $ref %q: the file is empty, so the key would have no value",
			file, labelOr(label), target)
	}
	return doc, nil
}

// chainError is a cycle or a chain too deep to be meant. It carries every file
// involved, which is why the frames it unwinds through pass it on untouched
// instead of prefixing it with hops it already names.
type chainError struct{ text string }

func (e *chainError) Error() string { return e.text }

// checkChain refuses a reference that would read a file already being read,
// and one that has nested further than anyone means to.
func (r *refResolver) checkChain(target string) error {
	id := identity(target)
	for _, frame := range r.chain {
		if frame.id == id {
			return &chainError{fmt.Sprintf(
				"$ref cycle: %s; a file cannot reference itself, directly or through another",
				r.chainText(target))}
		}
	}
	if len(r.chain) > maxRefDepth {
		return &chainError{fmt.Sprintf("$ref nesting is more than %d files deep: %s",
			maxRefDepth, r.chainText(target))}
	}
	return nil
}

// chainText renders the files being read, each labelled with the key that
// carried the reference out of it, ending on the file that closed the chain.
func (r *refResolver) chainText(last string) string {
	parts := make([]string, 0, len(r.chain)+1)
	for _, frame := range r.chain {
		parts = append(parts, fmt.Sprintf("%s (%s)", frame.file, frame.key))
	}
	return strings.Join(append(parts, last), " -> ")
}

// refTarget reports whether an object is a reference, and what it names.
func refTarget(node map[string]any) (string, bool, error) {
	raw, ok := node[refKey]
	if !ok {
		return "", false, nil
	}
	target, ok := raw.(string)
	if !ok || strings.TrimSpace(target) == "" {
		return "", true, errors.New("$ref must name another config file")
	}
	return target, true, nil
}

// refPath is where a reference points: relative to the file that wrote it,
// which is the only base that lets a folder of fragments be moved as a folder,
// and taken as written when it is absolute.
func refPath(target, file string) string {
	if filepath.IsAbs(target) {
		return target
	}
	return filepath.Join(filepath.Dir(file), filepath.FromSlash(target))
}

// identity is the name two paths to one file agree on.
func identity(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(path)
}

// foldKey finds the key an object already spells some way or another.
func foldKey(node map[string]any, key string) (string, bool) {
	for name := range node {
		if strings.EqualFold(name, key) {
			return name, true
		}
	}
	return "", false
}

// joinLabel extends a node's label with one more key.
func joinLabel(label, key string) string {
	if label == "" {
		return key
	}
	return label + "." + key
}

// labelOr names the document itself when a node has no key above it.
func labelOr(label string) string {
	if label == "" {
		return "the document"
	}
	return label
}

// kindOf names what a referenced file turned out to hold, for the one error
// that has to say why it cannot be used. A file holding nothing never reaches
// here: that is refused as it is read.
func kindOf(v any) string {
	if _, isList := v.([]any); isList {
		return "a list"
	}
	return "a single value"
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
		if len(doc) == 0 {
			// TOML spells an empty document as an empty table, where the other
			// two spell it as no value at all. Saying no value keeps "this file
			// is empty" one answer across the three formats.
			return nil, nil
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
