package config

// The setters the config language needs that no library can supply: the ones
// whose shape is dispat's own.
//
// The generic half — the object rules, the weak typing, the scalar-stands-for-
// a-one-element-container shorthand, and the setters for a string, a number, a
// flag, a list, a map and a sub-object — is pkg/config's. What is here is
// every key whose reading is a decision about dispat rather than about
// configuration: a script, whose scalar form must never split on commas
// because shell text holds them; a space's `path`, for the same reason; and
// the three keys that go through pkg/models' own normalisers so that a config
// file and the public type's UnmarshalJSON cannot come to disagree.
//
// The aliases below are the generic half under the names this package's tables
// are written in. fields.go is one line per key and reads better for it.

import (
	lib "github.com/yohimik/dispat/pkg/config"

	public "github.com/yohimik/dispat/pkg/models/v2"
)

// setter fills one field from the value written under its key. at is the key's
// full path from the root of the file, which is what an error has to name for
// the reader to find it.
type setter = lib.Setter

// fields is one struct's whole config surface: every key a file may write
// under the object, spelled in lower case, and what writing it does.
type fields = lib.Fields

// The generic decode, under this package's own names.
var (
	decodeObject = lib.DecodeObject
	merge        = lib.Merge
	keyPath      = lib.KeyPath
	indexPath    = lib.IndexPath
	wants        = lib.Wants
	weakString   = lib.WeakString
	weakInt      = lib.WeakInt
	weakBool     = lib.WeakBool
	weakList     = lib.WeakList
	splitList    = lib.SplitList

	str     = lib.String
	num     = lib.Int
	flag    = lib.Bool
	flagPtr = lib.BoolPtr
	strs    = lib.Strings
	nums    = lib.Ints
	strMap  = lib.StringMap
	rawMap  = lib.RawMap
)

// obj fills an optional sub-object. The pointer is allocated only when the
// file wrote the object, which is the distinction the whole layering rests on:
// nil is a layer saying nothing, and an object with no keys at all was pruned
// before the decoder saw it (see settings), so it says nothing either.
func obj[T any](dst **T, table func(*T) fields) setter { return lib.Object(dst, table) }

// objMap fills a map of named objects — spaces, package entries, version
// groups. The keys are the names the rest of dispat resolves against, copied
// exactly as the file wrote them and matched case-insensitively wherever they
// are looked up.
func objMap[T any](dst *map[string]T, table func(*T) fields) setter {
	return lib.ObjectMap(dst, table)
}

// objList fills a list of objects — alias tags, webhooks, headers, replace
// rules. A single object stands for the one-element list, because a repository
// with exactly one webhook should not have to write an array around it.
func objList[T any](dst *[]T, table func(*T) fields) setter { return lib.ObjectList(dst, table) }

// script fills one `scripts` entry. The scalar form is taken here rather than
// through the generic list shorthand, and that is the whole point: shell text
// holds commas, and `docker buildx build --output type=local,dest=out` is one
// command however many of them it contains. Every other shape goes to the
// public normaliser, so this and a config file read through encoding/json
// cannot come to disagree about what a script is.
func script(dst *Script) setter {
	return func(val any, at string) error {
		if s, ok := val.(string); ok {
			*dst = Script{s}
			return nil
		}
		out, err := public.NormalizeScript(val, at)
		if err != nil {
			return err
		}
		*dst = out
		return nil
	}
}

// scriptMap fills a whole `scripts` object, at any of the four levels that
// carry one. The names keep the case the level's own file wrote; the overlay
// that merges the levels is what decides which spelling survives a name two of
// them declare.
func scriptMap(dst *map[string]Script) setter {
	return lib.MapOf(dst, func(val any, at string) (Script, error) {
		var entry Script
		err := script(&entry)(val, at)
		return entry, err
	})
}

// pathList fills a space's `path`: one folder or several. The scalar form
// never splits, for the reason script does not — a folder name may hold a
// comma, and a path that quietly became two would send the space looking for
// folders that do not exist.
func pathList(dst *PathList) setter {
	return func(val any, at string) error {
		if s, ok := val.(string); ok {
			*dst = PathList{s}
			return nil
		}
		items, ok := weakList(val)
		if !ok {
			s, err := weakString(val, at)
			if err != nil {
				return err
			}
			*dst = PathList{s}
			return nil
		}
		out := make(PathList, 0, len(items))
		for i, item := range items {
			s, err := weakString(item, indexPath(at, i))
			if err != nil {
				return err
			}
			out = append(out, s)
		}
		*dst = out
		return nil
	}
}

// deps fills a `dependencies` object keyed by consumer, at the file or space
// level. The expansion lives in pkg/models, so this and the public type's own
// UnmarshalJSON cannot come to disagree about what the key accepts. Consumer
// names arrive as written, exactly like the keys of `packages` and `spaces`,
// and are resolved onto the packages they name in discovery, which folds.
func deps(dst *Dependencies) setter {
	return func(val any, _ string) error {
		out, err := public.NormalizeDependencies(val)
		if err != nil {
			return err
		}
		*dst = out
		return nil
	}
}

// providers fills a package's own `dependencies`: one consumer's entry with
// the consumer left implicit. The path is handed to the normaliser, so an
// error names the package entry the list belongs to rather than the key it
// shares with three other levels.
func providers(dst *ProviderList) setter {
	return func(val any, at string) error {
		out, err := public.NormalizeProviders(val, at)
		if err != nil {
			return err
		}
		*dst = out
		return nil
	}
}

// recordLines fills a record-line list: a file title, a header or a footer. A
// whole list written as one string is one line of prose, and inside a list a
// bare string or a bare array of strings is the text with no filters, which is
// the common case and needs no object at all.
func recordLines(dst *[]EntryLine) setter {
	return func(val any, at string) error {
		if s, ok := val.(string); ok {
			*dst = []EntryLine{{Line: []string{s}}}
			return nil
		}
		items, ok := weakList(val)
		if !ok {
			items = []any{val}
		}
		out := make([]EntryLine, 0, len(items))
		for i, item := range items {
			line, err := entryLineOf(item, indexPath(at, i))
			if err != nil {
				return err
			}
			out = append(out, line)
		}
		*dst = out
		return nil
	}
}

// recordSections fills a `sections` list. A whole list written as one string
// names one built-in section, and inside a list a bare string is a built-in
// named by key, which is the common case — reordering the defaults — and needs
// no object at all.
func recordSections(dst *[]SectionConfig) setter {
	return func(val any, at string) error {
		if s, ok := val.(string); ok {
			*dst = []SectionConfig{{Title: s}}
			return nil
		}
		items, ok := weakList(val)
		if !ok {
			items = []any{val}
		}
		out := make([]SectionConfig, 0, len(items))
		for i, item := range items {
			section, err := sectionOf(item, indexPath(at, i))
			if err != nil {
				return err
			}
			out = append(out, section)
		}
		*dst = out
		return nil
	}
}

// sectionOf reads one element of a `sections` list in either of its two
// shapes: a built-in's key, or a full object.
func sectionOf(item any, at string) (SectionConfig, error) {
	var section SectionConfig
	if item == nil {
		return section, nil
	}
	if _, isObject := item.(map[string]any); isObject {
		// Two statements rather than `return section, decodeObject(...)`, for
		// the reason entryLineOf spells out: gc and TinyGo disagree about
		// whether the returned copy is taken before or after the call.
		err := decodeObject(item, at, sectionFields(&section))
		return section, err
	}
	if s, ok := item.(string); ok {
		section.Title = s
		return section, nil
	}
	return section, wants(at, "a built-in section name or an object")
}

// numPtr fills a tri-state whole number: the pointer is allocated only because
// the file wrote the key, so a layer that says nothing about the option stays
// nil and a nearer layer's value, or the default, survives. It is the Int
// counterpart of the library's BoolPtr, which the library does not carry
// because entrySpacing is so far the only key that needs it.
func numPtr(dst **int) setter {
	return func(val any, at string) error {
		n, err := weakInt(val, at)
		if err != nil {
			return err
		}
		*dst = &n
		return nil
	}
}

// entryLineOf reads one element of a record-line list in whichever of its
// three shapes it was written.
func entryLineOf(item any, at string) (EntryLine, error) {
	var line EntryLine
	if item == nil {
		return line, nil
	}
	if _, isObject := item.(map[string]any); isObject {
		// Sequenced in two statements on purpose: a `return line, decode(&line)`
		// leaves the order of reading line and running the call to the
		// compiler, and gc and TinyGo choose differently — one returns the
		// filled line, the other a copy taken before the call filled it.
		err := decodeObject(item, at, entryLineFields(&line))
		return line, err
	}
	if lines, ok := stringList(item); ok {
		line.Line = lines
		return line, nil
	}
	return line, wants(at, "a line of text, a list of lines, or an object")
}
