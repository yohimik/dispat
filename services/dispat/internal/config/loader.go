package config

// The loader dispat reads every config file through, and the names this
// package has always called its pieces by.
//
// Parsing a file, following its `$ref`s, rendering the tree as the decoder's
// input, folding a name at the lookup, and the whole decode table are
// pkg/config's now. What is left here is the domain: which keys dispat's model
// holds, what a package folder's file may declare, and the wording dispat's
// errors have always used.
//
// The names below are aliases rather than a second implementation, so a reader
// following `readTree` or `sortedKeys` from any of dispat's own files lands
// where the code actually is.

import (
	"context"
	"errors"

	lib "github.com/yohimik/dispat/pkg/config"
)

// errUnsupportedFormat is what a file dispat has no parser for returns. The
// extension decides the format, here as everywhere else.
//
// It reaches the reader through the format table rather than around it: an
// entry registered under no extension at all claims every file the three
// parsers leave, which is how dispat keeps its own wording for a file it
// cannot read while the library keeps a general one.
var errUnsupportedFormat = errors.New("dispat reads json, yaml and toml config files")

// maxRefDepth caps how far references may nest, which is the library's own
// default under the name this package refuses by.
const maxRefDepth = lib.DefaultMaxRefDepth

// keyDelim is the separator a nested key path is spelled with — the decoder's
// convention, not the config language's, and the separator an error names a
// nested key with.
const keyDelim = lib.DefaultKeyDelim

// loader is the one pkg/config Loader every read in this package goes through.
// One loader rather than one per call is what keeps the format table, the
// reference key and the depth cap the same for the root config, a folder's own
// file, the ascent's probes and the writers alike.
var loader = lib.NewLoader(lib.Options{Formats: dispatFormats(), Logger: bootLogger{}})

// dispatFormats is the library's table with dispat's own refusal in it, under
// the empty extension the library reserves for exactly this.
func dispatFormats() map[string]lib.Unmarshal {
	formats := lib.DefaultFormats()
	formats[""] = func([]byte) (any, error) { return nil, errUnsupportedFormat }
	return formats
}

// tree is one parsed config file: the document, and every file the document
// was composed from, in the order they were read.
type tree = lib.Tree

// readTree parses a config file into a tree whose keys are spelled as written,
// with every `$ref` it holds resolved.
func readTree(path string) (*tree, error) {
	return loader.ReadTree(context.Background(), path)
}

// settings renders a parsed tree as the map the decoder reads: the tree with
// an object holding no keys pruned away and a key spelled with the delimiter
// turned into the levels it names. Both rules are the config language's rather
// than an implementation detail — an opt-in block written as a bare `{}` says
// nothing rather than enabling itself at its defaults.
func settings(raw map[string]any) map[string]any {
	return (&tree{Root: raw}).Settings(loader, nil)
}

// isSet answers whether a parsed tree holds a value at a top-level key, which
// is what the folder loaders ask before they refuse a key. A key written with
// no value is not set: the file mentioned it and said nothing.
func isSet(raw map[string]any, key string) bool { return lib.IsSet(raw, key) }

// lookupFold finds what a config map holds under a name, whatever case either
// side spells it with.
func lookupFold(node map[string]any, key string) (any, bool) {
	_, value, ok := lib.LookupFold(node, key)
	return value, ok
}

// foldKey finds the key a map already spells some way or another, for the
// callers that want the key alone: to replace what is there, or to refuse a
// second spelling of it.
func foldKey[T any](m map[string]T, key string) (string, bool) { return lib.FoldKey(m, key) }

// sortedKeys returns a map's keys in order, so a config with several mistakes
// always reports the same one first.
func sortedKeys[V any](m map[string]V) []string { return lib.SortedKeys(m) }
