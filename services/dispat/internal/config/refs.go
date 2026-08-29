package config

// How a config file becomes a tree, and the one thing that can happen on the
// way: a `$ref` naming another file, whose content becomes the value.
//
// Each format dispat reads has a parser of its own, and this is the one place
// that calls them: the root config, a space or package folder's own file, the
// probes the ascent classifies candidates with, and the readers behind
// `dispat compute --write` all arrive here.
//
// Parsing the file here is what keeps every key spelled the way the file wrote
// it, and nothing downstream renames one. A name is matched case-insensitively
// wherever it is looked up — a script, a space, a package, a commit scope — so
// the folding happens at the lookup, where the two spellings actually meet, and
// never at the key, where it would rename what the author wrote. Two keys of
// one object that fold together are refused as it is decoded, because a name
// with two spellings in one place has no lookup that could answer for it.
//
// A reference is resolved against the directory of the file that wrote it, and
// the file it names may hold references of its own, resolved against theirs. A
// reference may name several files instead of one, which are read in order and
// merged: objects key by key with the later file winning, lists end to end. A
// file is never cached between positions: the same fragment referenced from
// two keys is read twice and each copy is its own value, which is what makes a
// file that appears twice in one chain, and only that, a cycle.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
	yaml "gopkg.in/yaml.v3"

	"github.com/spf13/pflag"

	public "github.com/yohimik/dispat/pkg/models"
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
	targets, isRef, err := refTargets(node)
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

	base, err := r.followAll(targets, file, label)
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
		return nil, fmt.Errorf("%s: %s: %s", file, labelOr(label), nothingToOverride(targets, base))
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
		// leaving both spellings in would hand the decode two keys that fold
		// together, and which one survived would be a matter of luck.
		if existing, found := foldKey(object, key); found {
			delete(object, existing)
		}
		object[key] = resolved
	}
	return object, nil
}

// followAll reads every file a reference names and merges what they hold, in
// the order they are written: objects key by key with the later file winning,
// lists by adding one after another. One file is the whole answer, which is
// what a reference naming a single file has always been.
func (r *refResolver) followAll(targets []string, file, label string) (any, error) {
	base, err := r.follow(targets[0], file, label)
	if err != nil || len(targets) == 1 {
		return base, err
	}
	for _, target := range targets[1:] {
		next, err := r.follow(target, file, label)
		if err != nil {
			return nil, err
		}
		merged, err := mergeFragments(base, next, targets[0], target)
		if err != nil {
			return nil, fmt.Errorf("%s: %s: %w", file, labelOr(label), err)
		}
		base = merged
	}
	return base, nil
}

// mergeFragments adds one referenced file to what the files before it hold.
// Objects are merged whole key by whole key, the way a key written beside a
// reference overrides it; lists are joined. Anything else has no meaning to
// give: a single value cannot be merged with another, and an object and a list
// are not the same kind of answer, so both are refused rather than guessed at.
func mergeFragments(base, next any, firstTarget, target string) (any, error) {
	switch first := base.(type) {
	case map[string]any:
		object, ok := next.(map[string]any)
		if !ok {
			return nil, unmergeableFragments(firstTarget, base, target, next)
		}
		for _, key := range sortedKeys(object) {
			// The later file's spelling of a key is the one that survives:
			// leaving both in would hand the decode two keys that fold
			// together, and which one won would be a matter of luck.
			if existing, found := foldKey(first, key); found {
				delete(first, existing)
			}
			first[key] = object[key]
		}
		return first, nil
	case []any:
		list, ok := next.([]any)
		if !ok {
			return nil, unmergeableFragments(firstTarget, base, target, next)
		}
		return append(first, list...), nil
	default:
		return nil, unmergeableFragments(firstTarget, base, target, next)
	}
}

// unmergeableFragments says which two files disagreed about what a reference
// holds, and what each of them holds instead.
func unmergeableFragments(firstTarget string, first any, target string, next any) error {
	return fmt.Errorf("$ref: %q holds %s and %q holds %s; the files of one $ref must all hold objects, or all hold lists",
		firstTarget, kindOf(first), target, kindOf(next))
}

// nothingToOverride explains a reference whose files leave the keys beside it
// with nothing to override, naming the files when there is more than one.
func nothingToOverride(targets []string, base any) string {
	if len(targets) == 1 {
		return fmt.Sprintf("$ref %q: the file is %s, so the keys beside the $ref have nothing to override",
			targets[0], kindOf(base))
	}
	return fmt.Sprintf("$ref: the files merge to %s, so the keys beside the $ref have nothing to override",
		kindOf(base))
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

// refTargets reports whether an object is a reference, and the files it names:
// one for a reference naming a file, each of them in order for a reference
// naming a list of files. A list of one is the same reference written the long
// way, and is read as one rather than refused.
func refTargets(node map[string]any) ([]string, bool, error) {
	raw, ok := node[refKey]
	if !ok {
		return nil, false, nil
	}
	switch value := raw.(type) {
	case string:
		if strings.TrimSpace(value) == "" {
			return nil, true, errRefTarget
		}
		return []string{value}, true, nil
	case []any:
		if len(value) == 0 {
			return nil, true, errors.New("$ref names no files: the list must hold at least one path")
		}
		targets := make([]string, 0, len(value))
		for i, item := range value {
			target, ok := item.(string)
			if !ok || strings.TrimSpace(target) == "" {
				return nil, true, fmt.Errorf("$ref[%d] must name another config file", i)
			}
			targets = append(targets, target)
		}
		return targets, true, nil
	default:
		return nil, true, errRefTarget
	}
}

// errRefTarget is what a reference naming anything but a file, or a list of
// them, returns.
var errRefTarget = errors.New("$ref must name another config file, or a list of them")

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

// foldKey finds the key a map already spells some way or another, for the
// callers that want the key alone: to replace what is there, or to refuse a
// second spelling of it. It is public.FoldLookup with the value dropped, so
// the tree and the decoded model agree about what "the same name" means.
func foldKey[T any](node map[string]T, key string) (string, bool) {
	name, _, ok := public.FoldLookup(node, key)
	return name, ok
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

// kindOf names what a referenced file turned out to hold, for the errors that
// have to say why it cannot be used. A file holding nothing never reaches
// here: that is refused as it is read.
func kindOf(v any) string {
	switch v.(type) {
	case map[string]any:
		return "an object"
	case []any:
		return "a list"
	default:
		return "a single value"
	}
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

// normalizeTree is the decode's view of a parsed file: the same document with
// every map turned into a string-keyed one, and with the flags that override a
// config key written over it. Keys are copied, never renamed.
//
// It is a copy rather than the tree itself because the flag overlay writes into
// it, and a config file the loader edited on its way past would be a surprise
// to every reader that goes back to the tree.
func normalizeTree(t *tree, flags *pflag.FlagSet) map[string]any {
	raw := normalizeMap(t.root)
	if flags == nil {
		return raw
	}
	for key, flagName := range boundFlags {
		f := flags.Lookup(flagName)
		// Only a flag the caller actually passed overrides the file. An unset
		// flag carries its default, and writing that over a configured value
		// would make every run look like it had been asked for the default.
		if f == nil || !f.Changed {
			continue
		}
		// The overlay replaces the file's spelling rather than sitting beside
		// it: a file writing `logLevel` and an overlay writing `loglevel` would
		// otherwise be two keys the decode refuses as a collision, over a flag
		// the operator passed correctly.
		if existing, found := foldKey(raw, key); found && existing != key {
			delete(raw, existing)
		}
		raw[key] = flagValue(f)
	}
	return raw
}

// flagValue renders an explicitly set flag as the value the decode reads. A
// list-valued flag hands over its elements rather than its printed form:
// --concurrency 4,2 prints as "[4,2]", which is a string no list field can be
// weakly typed out of.
func flagValue(f *pflag.Flag) any {
	if sv, ok := f.Value.(pflag.SliceValue); ok {
		return sv.GetSlice()
	}
	return f.Value.String()
}

// boundFlags are the config keys an explicitly set flag overrides, and the
// flags that override them.
var boundFlags = map[string]string{
	"concurrency": "concurrency",
	"logLevel":    "log-level",
	"logFormat":   "log-format",
}

// normalizeMap copies a parsed tree, deeply, with every key as it was written.
func normalizeMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = normalizeValue(v)
	}
	return out
}

// normalizeValue copies one node of the decode's view. A generic map — a yaml
// mapping with a non-string key — becomes a string-keyed one on the way, so
// everything below here is one kind of map and the decode never meets the
// other.
//
// The arms name every container that can reach here. The three parsers produce
// string-keyed maps, generic maps, lists and scalars, and the flag overlay adds
// a list of strings; everything else falls through as it came, because a scalar
// is immutable and shared safely and no other typed container is ever built. A
// parser that began producing one would be a case to add here, which is a
// smaller thing to get right than the reflection that used to copy them all.
func normalizeValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return normalizeMap(t)
	case map[any]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[weakEnvString(k)] = normalizeValue(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = normalizeValue(val)
		}
		return out
	case []string:
		return copyStrings(t)
	default:
		return v
	}
}

// copyStrings copies the one typed container that reaches the tree: a
// list-valued flag hands over its elements as they are stored, and copying them
// keeps a view of the config from sharing a slice with the flag set.
func copyStrings(s []string) []string {
	return append([]string(nil), s...)
}

// isSet answers whether a parsed tree holds a value at a top-level key, which
// is what the folder loaders ask before they refuse a key. The key is matched
// case-insensitively, like every other key of the language. A key written with
// no value is not set: the file mentioned it and said nothing.
func isSet(raw map[string]any, key string) bool {
	value, ok := lookupFold(raw, key)
	return ok && value != nil
}

// cloneTree copies a parsed tree, deeply.
func cloneTree(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = cloneValue(v)
	}
	return out
}

// cloneValue copies one node, naming the same containers lowerValue does: the
// three parsers produce string-keyed maps, generic maps (a yaml mapping with a
// non-string key), lists and scalars, and the flag overlay adds a list of
// strings. Everything else falls through as it came, a scalar being immutable
// and shared safely.
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
	case []string:
		return copyStrings(t)
	default:
		return v
	}
}
