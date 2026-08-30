package config

// How a config file becomes a tree, and the one thing that can happen on the
// way: a `$ref` naming another file, whose content becomes the value.
//
// Each format has a parser of its own, and this is the one place that calls
// them: the file a caller asked for, the folders' own files, and the probes
// the ascent classifies candidates with all arrive here.
//
// Parsing the file here is what keeps every key spelled the way the file wrote
// it, and nothing downstream renames one.
//
// A reference is resolved against the directory of the file that wrote it, and
// the file it names may hold references of its own, resolved against theirs. A
// reference may name several files instead of one, which are read in order and
// merged: objects key by key with the later file winning, lists end to end. A
// file is never cached between positions: the same fragment referenced from
// two keys is read twice and each copy is its own value, which is what makes a
// file that appears twice in one chain, and only that, a cycle.
//
// The walk builds the tree rather than copying one. Every object and list it
// passes is rebuilt, generic maps — a yaml mapping with a non-string key —
// become string-keyed on the way, and the result shares nothing with the
// parsers' output but its scalars, which are immutable. There used to be a
// second full deep copy after this one, to convert those maps and to give the
// overlay something safe to write into; the conversion happens here now, and
// the overlay writes into the settings map rather than into the tree.

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Tree is one parsed config file: the document, and every file the document
// was composed from, in the order they were read.
//
// Root is string-keyed the whole way down and holds only what the parsers
// produce — objects, lists and scalars — with every key spelled as its file
// wrote it. Nothing in this package mutates it after ReadTree returns.
type Tree struct {
	Root  map[string]any
	Files []string
}

// ReadTree parses a config file into a tree whose keys are spelled as written,
// with every `$ref` it holds resolved.
func (l *Loader) ReadTree(ctx context.Context, path string) (*Tree, error) {
	l = l.loader()
	log := l.logger(ctx)
	start := time.Now()
	r := &refResolver{l: l, log: log}
	doc, err := r.document(path)
	if err != nil {
		return nil, err
	}
	root, err := documentObject(doc, path)
	if err != nil {
		return nil, err
	}
	if log.Enabled(LevelDebug) {
		log.Log(LevelDebug, EventTreeLoaded, Str("path", path), Num("files", len(r.files)),
			Str("duration", time.Since(start).String()))
	}
	return &Tree{Root: root, Files: r.files}, nil
}

// refResolver resolves the references of one file and everything it pulls in.
// It holds the chain being followed, so a cycle is reported as the path that
// closed it rather than as a stack overflow.
type refResolver struct {
	l     *Loader
	log   Logger
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
// file ReadTree was asked for and for every file a reference names.
func (r *refResolver) document(path string) (any, error) {
	doc, err := r.l.decodeFile(r.log, path)
	if err != nil {
		return nil, &FileError{Path: path, Err: err}
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
	case map[any]any:
		return r.object(stringKeyed(node), file, label)
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

// stringKeyed converts a generic map — a yaml mapping with a non-string key —
// into the one kind of map everything below here meets. Keys are rendered the
// way the weakly typed decode renders a scalar, so a key and a value of the
// same number come out the same text.
//
// Two keys that render alike are one key afterwards, and the pairs are placed
// in a fixed order so which of them survives is decided by the file rather
// than by the map iteration that happened to run: by rendered name, and, for
// the two spellings of one name, by the Go type they arrived as.
func stringKeyed(m map[any]any) map[string]any {
	type pair struct {
		name string
		kind string
		val  any
	}
	pairs := make([]pair, 0, len(m))
	for k, v := range m {
		pairs = append(pairs, pair{name: WeakScalarString(k), kind: fmt.Sprintf("%T", k), val: v})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].name != pairs[j].name {
			return pairs[i].name < pairs[j].name
		}
		return pairs[i].kind < pairs[j].kind
	})
	out := make(map[string]any, len(pairs))
	for _, p := range pairs {
		out[p.name] = p.val
	}
	return out
}

// object resolves an object, which is where a reference can be. Keys are
// walked in order so that a file with two mistakes in it always reports the
// same one first.
func (r *refResolver) object(node map[string]any, file, label string) (any, error) {
	targets, isRef, err := r.l.refTargets(node)
	if err != nil {
		return nil, &KeyError{File: file, Key: labelOr(label), Err: err}
	}
	if !isRef {
		out := make(map[string]any, len(node))
		for _, key := range SortedKeys(node) {
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
		return nil, &KeyError{File: file, Key: labelOr(label),
			Err: nothingToOverride(targets, base)}
	}
	for _, key := range SortedKeys(node) {
		if key == r.l.opts.RefKey {
			continue
		}
		resolved, err := r.value(node[key], file, joinLabel(label, key))
		if err != nil {
			return nil, err
		}
		// The overriding key is written whichever way the two files spell it:
		// leaving both spellings in would hand the decode two keys that fold
		// together, and which one survived would be a matter of luck.
		if existing, found := FoldKey(object, key); found {
			delete(object, existing)
		}
		object[key] = resolved
	}
	if r.log.Enabled(LevelTrace) {
		r.log.Log(LevelTrace, EventRefMerge, Str("file", file), Str("key", labelOr(label)),
			Num("keys", len(node)-1))
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
			return nil, &KeyError{File: file, Key: labelOr(label), Err: err}
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
		for _, key := range SortedKeys(object) {
			// The later file's spelling of a key is the one that survives:
			// leaving both in would hand the decode two keys that fold
			// together, and which one won would be a matter of luck.
			if existing, found := FoldKey(first, key); found {
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
func nothingToOverride(targets []string, base any) error {
	if len(targets) == 1 {
		return fmt.Errorf("$ref %q: the file is %s, so the keys beside the $ref have nothing to override",
			targets[0], kindOf(base))
	}
	return fmt.Errorf("$ref: the files merge to %s, so the keys beside the $ref have nothing to override",
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
	if r.log.Enabled(LevelTrace) {
		r.log.Log(LevelTrace, EventRefFollow, Str("file", file), Str("key", labelOr(label)),
			Str("target", path), Num("depth", len(r.chain)))
	}

	doc, err := r.document(path)
	if err != nil {
		var chain *RefChainError
		if errors.As(err, &chain) {
			// A chain failure already names every file involved, so the frames
			// it unwinds through leave it as it is.
			return nil, err
		}
		return nil, &KeyError{File: file, Key: labelOr(label),
			Err: fmt.Errorf("$ref %q: %w", target, err)}
	}
	if doc == nil {
		return nil, &KeyError{File: file, Key: labelOr(label),
			Err: fmt.Errorf("$ref %q: the file is empty, so the key would have no value", target)}
	}
	return doc, nil
}

// checkChain refuses a reference that would read a file already being read,
// and one that has nested further than anyone means to.
func (r *refResolver) checkChain(target string) error {
	id := identity(target)
	for _, frame := range r.chain {
		if frame.id == id {
			return &RefChainError{Err: ErrRefCycle, Chain: r.chainText(target)}
		}
	}
	if len(r.chain) > r.l.opts.MaxRefDepth {
		return &RefChainError{Err: ErrRefDepth, Chain: r.chainText(target),
			Depth: r.l.opts.MaxRefDepth}
	}
	return nil
}

// chainText renders the files being read, each labelled with the key that
// carried the reference out of it, ending on the file that closed the chain.
func (r *refResolver) chainText(last string) []string {
	parts := make([]string, 0, len(r.chain)+1)
	for _, frame := range r.chain {
		parts = append(parts, fmt.Sprintf("%s (%s)", frame.file, frame.key))
	}
	return append(parts, last)
}

// refTargets reports whether an object is a reference, and the files it names:
// one for a reference naming a file, each of them in order for a reference
// naming a list of files. A list of one is the same reference written the long
// way, and is read as one rather than refused.
func (l *Loader) refTargets(node map[string]any) ([]string, bool, error) {
	raw, ok := node[l.opts.RefKey]
	if !ok {
		return nil, false, nil
	}
	switch value := raw.(type) {
	case string:
		if strings.TrimSpace(value) == "" {
			return nil, true, ErrRefTarget
		}
		return []string{value}, true, nil
	case []any:
		if len(value) == 0 {
			return nil, true, fmt.Errorf("%s names no files: the list must hold at least one path", l.opts.RefKey)
		}
		targets := make([]string, 0, len(value))
		for i, item := range value {
			target, ok := item.(string)
			if !ok || strings.TrimSpace(target) == "" {
				return nil, true, fmt.Errorf("%s[%d] must name another config file", l.opts.RefKey, i)
			}
			targets = append(targets, target)
		}
		return targets, true, nil
	default:
		return nil, true, ErrRefTarget
	}
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

// joinLabel extends a node's label with one more key.
func joinLabel(label, key string) string {
	if label == "" {
		return key
	}
	return label + DefaultKeyDelim + key
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
func (l *Loader) decodeFile(log Logger, path string) (any, error) {
	data, err := l.opts.ReadFile(path)
	if err != nil {
		return nil, err
	}
	ext := strings.ToLower(filepath.Ext(path))
	parse, ok := l.opts.Formats[ext]
	if !ok {
		// A format registered under no extension at all claims what the others
		// leave, which is where a caller puts its own wording for a file it has
		// no parser for.
		if parse, ok = l.opts.Formats[""]; !ok {
			return nil, ErrUnsupportedFormat
		}
	}
	if log.Enabled(LevelTrace) {
		log.Log(LevelTrace, EventFileRead, Str("path", path), Num("bytes", len(data)), Str("format", ext))
	}
	return parse(data)
}
