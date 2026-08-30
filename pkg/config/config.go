// Package config reads configuration files the way a hand-written file wants
// to be read.
//
// It parses json, yaml and toml into one tree, composes that tree from several
// files through a `$ref` key, finds the file a command was run beneath by
// walking up the directory tree, and turns the result into your own structs
// through a table of setters rather than through reflection. A key is spelled
// in the file however its author spells it, matched case-insensitively
// wherever it is looked up, and refused when one object gives a name two
// spellings. A key no table holds is an error, and the error names it by its
// full path from the root, because a typo the loader accepts is configuration
// that silently never applies.
//
// The package has no reflection in it at all, which is what makes it link
// under TinyGo and what makes its decode table the readable statement of a
// config surface rather than a set of struct tags with hooks behind them.
//
// The entry point is a Loader:
//
//	l := config.NewLoader(config.Options{})
//	t, err := l.ReadTree(ctx, "config.yaml")
//	var cfg Config
//	err = config.DecodeObject(t.Settings(l, nil), "", configFields(&cfg))
package config

import (
	"encoding/json"
	"os"

	toml "github.com/pelletier/go-toml/v2"
	yaml "gopkg.in/yaml.v3"
)

// Defaults for the options below. They are exported because a caller that
// overrides one of them usually wants to say what it is overriding.
const (
	// DefaultRefKey is the key that makes an object a reference.
	DefaultRefKey = "$ref"
	// DefaultMaxRefDepth caps how far references may nest.
	DefaultMaxRefDepth = 32
	// DefaultKeyDelim is the separator a nested key path is spelled with.
	DefaultKeyDelim = "."
)

// Unmarshal parses one file's bytes into whatever the document holds: an
// object, a list or a scalar. Returning a nil value with a nil error says the
// file is empty, which the loader reports as such rather than as a value.
type Unmarshal func(data []byte) (any, error)

// DefaultFormats returns a fresh copy of the format table: the three formats
// this package parses, keyed by the lower-cased file extension that selects
// them.
//
// The table is the whole format story. A caller adding a format registers it
// here; a caller registering the empty extension claims every file the other
// entries do not, which is how a program with its own wording for "I have no
// parser for this" says it.
func DefaultFormats() map[string]Unmarshal {
	return map[string]Unmarshal{
		".json": unmarshalJSON,
		".yaml": unmarshalYAML,
		".yml":  unmarshalYAML,
		".toml": unmarshalTOML,
	}
}

func unmarshalJSON(data []byte) (any, error) {
	var doc any
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	return doc, nil
}

func unmarshalYAML(data []byte) (any, error) {
	var doc any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	return doc, nil
}

func unmarshalTOML(data []byte) (any, error) {
	// A TOML document is a table and nothing else, so the target says so
	// rather than asking the decoder to guess.
	doc := map[string]any{}
	if err := toml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if len(doc) == 0 {
		// TOML spells an empty document as an empty table, where the other two
		// spell it as no value at all. Saying no value keeps "this file is
		// empty" one answer across the three formats.
		return nil, nil
	}
	return doc, nil
}

// Options configures a Loader. Every field has a valid zero value, so
// Options{} is the whole default configuration and a caller changing one thing
// writes one field.
type Options struct {
	// RefKey is the key that makes an object a reference. It is matched
	// exactly: a config file is written by hand, and one spelling keeps `$Ref`
	// an unknown key rather than a reference that works in some files and not
	// others. Zero value: DefaultRefKey.
	RefKey string

	// MaxRefDepth caps how far references may nest. The chain check catches a
	// file that reaches itself under a path it can be named by; this catches
	// the ones it cannot, such as two names for one file through a symlink.
	// Zero value: DefaultMaxRefDepth. A negative value refuses every
	// reference.
	MaxRefDepth int

	// KeyDelim is the separator Settings spells a nested key path with: the
	// tree is flattened to delimited paths and rebuilt from them, which is
	// where a key containing the delimiter becomes two levels. Zero value:
	// DefaultKeyDelim.
	//
	// The key paths errors name are rendered with DefaultKeyDelim regardless,
	// because the decode is a package-level table rather than a loader's.
	KeyDelim string

	// ReadFile reads one file's bytes. Zero value: os.ReadFile. A caller
	// serving configuration from an embedded filesystem, a test fixture or a
	// network cache replaces it; every path this package opens goes through
	// it.
	ReadFile func(path string) ([]byte, error)

	// Formats maps a lower-cased file extension, dot included, to its parser.
	// A nil map selects DefaultFormats; a non-nil map replaces the table
	// wholesale, so add to a copy of DefaultFormats rather than starting from
	// scratch unless you mean to drop the standard formats.
	//
	// An entry under the empty string handles every file no other entry
	// claims. Without one, such a file is ErrUnsupportedFormat.
	Formats map[string]Unmarshal
}

// Default returns the options fully populated, the convenient starting point
// when you want to adjust one field against the values the package documents:
//
//	o := config.Default()
//	o.RefKey = "$include"
//	l := config.NewLoader(o)
func Default() Options {
	return Options{
		RefKey:      DefaultRefKey,
		MaxRefDepth: DefaultMaxRefDepth,
		KeyDelim:    DefaultKeyDelim,
		ReadFile:    os.ReadFile,
		Formats:     DefaultFormats(),
	}
}

// withDefaults returns a copy with every zero-valued field replaced by its
// default. MaxRefDepth is the one field a caller can mean to set to zero — a
// depth of nought is "follow no reference at all" — so a negative value is
// what it takes to distinguish that from an unset field; the zero value takes
// the default, and any negative value is normalised to -1, which refuses the
// first reference.
func (o Options) withDefaults() Options {
	if o.RefKey == "" {
		o.RefKey = DefaultRefKey
	}
	if o.MaxRefDepth == 0 {
		o.MaxRefDepth = DefaultMaxRefDepth
	}
	if o.KeyDelim == "" {
		o.KeyDelim = DefaultKeyDelim
	}
	if o.ReadFile == nil {
		o.ReadFile = os.ReadFile
	}
	if o.Formats == nil {
		o.Formats = DefaultFormats()
	}
	return o
}

// Loader reads configuration files under one set of options. It holds no state
// between calls, so a Loader is safe for concurrent use and a program usually
// builds one and keeps it.
type Loader struct {
	opts Options
}

// NewLoader returns a Loader reading files as opts says. The options are
// copied with their zero values filled in, so later edits to the caller's
// struct do not reach the loader; the format table itself is taken by
// reference, and a caller mutating it afterwards is mutating the loader.
func NewLoader(opts Options) *Loader {
	return &Loader{opts: opts.withDefaults()}
}

// Options returns the loader's options, with every zero value filled in. It is
// how a caller reads back what a Loader built from Options{} actually does.
func (l *Loader) Options() Options { return l.opts }

// loader returns l, or a default one when l is nil, so that every entry point
// takes a nil Loader as "the defaults" rather than panicking on it.
func (l *Loader) loader() *Loader {
	if l == nil {
		return defaultLoader
	}
	return l
}

// defaultLoader is what a nil *Loader stands in for.
var defaultLoader = NewLoader(Options{})
