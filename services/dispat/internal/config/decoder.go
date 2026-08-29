package config

// The decoder: how the settings map becomes the model, and nothing else.
//
// It is a table of setters rather than a walk over the model's types. Every
// struct the config language names has a fields table (fields.go) mapping the
// keys a file may write to closures that fill that struct's own fields, and
// decoding is looking each key up and calling what it finds. A key with no
// setter is a key the model has no field for, which is the unknown-key
// refusal every config typo lands in; that refusal is therefore structural
// rather than a check somebody has to remember to run.
//
// Reflection is what dispat used to do this with, and it is what this replaces.
// A reflected decoder has to be told the exceptions — which conversions are
// allowed, which fields are squashed, which types have a shorthand — through
// hooks that fire on a Go type and cannot see the key that produced it. The
// hazard was never hypothetical: the conversion that lifts a scalar into a
// list splits it on commas, which is right for a list of script names and
// wrong for a shell command, and the two were kept apart only by the order the
// hooks were composed in. Here they are different setters, so the order that
// used to matter cannot exist.
//
// The stance the whole file expresses, in one place rather than at each call
// site:
//
//   - Values are weakly typed. A bare number is a fine string and a quoted one
//     a fine number, because a config file's format has types and the config
//     language does not.
//   - A scalar stands in for the one-element container it belongs in, which is
//     what makes `path: pkgs` and `events: package.published` read the way
//     they do.
//   - A key written with no value is a key that said nothing: it is used, so
//     it is not a typo, and it leaves its field at the zero value validation
//     applies the default to.
//   - An unknown key is an error, and the error names it by its full path from
//     the root, because a typo the loader accepts is a script that silently
//     never runs.
//   - Keys are visited in order and the first mistake is the one reported. A
//     map has no order of its own, and a build that fails differently from one
//     run to the next is a build nobody can fix.

import (
	"fmt"
	"maps"
	"math"
	"strconv"
	"strings"

	public "github.com/yohimik/dispat/pkg/models"
)

// setter fills one field from the value written under its key. at is the
// key's full path from the root of the file, which is what an error has to
// name for the reader to find it.
type setter func(val any, at string) error

// fields is one struct's whole config surface: every key a file may write
// under the object, spelled the way the lowered tree spells it, and what
// writing it does. A struct's table is built against a particular destination
// (see fields.go), so a setter writes into that value and nowhere else.
type fields map[string]setter

// decodeObject fills one object from the map written for it.
//
// It is the only place the language's object rules live: the value has to be
// an object, its keys are visited in order, a key the table does not hold is
// unknown, and a key holding nothing says nothing.
func decodeObject(val any, at string, f fields) error {
	m, ok := val.(map[string]any)
	if !ok {
		return wants(at, "an object")
	}
	for _, key := range sortedKeys(m) {
		path := keyPath(at, key)
		set, known := f[key]
		if !known {
			return fmt.Errorf("unknown key %q", path)
		}
		if m[key] == nil {
			continue
		}
		if err := set(m[key], path); err != nil {
			return err
		}
	}
	return nil
}

// merge folds a squashed embed's table into the table of the object that
// embeds it, which is exactly what squashing meant: the embedded struct's keys
// are written at the enclosing object's level, with no key of their own.
func merge(into, embedded fields) fields {
	maps.Copy(into, embedded)
	return into
}

// keyPath is the path of a key inside the object at parent. The root object
// has no path of its own, so its keys are named by themselves.
func keyPath(parent, key string) string {
	if parent == "" {
		return key
	}
	return parent + keyDelim + key
}

// indexPath is the path of one element of the list at parent.
func indexPath(parent string, i int) string {
	return fmt.Sprintf("%s[%d]", parent, i)
}

// wants is the one sentence a value of the wrong shape earns: the key, and
// what belongs under it. Saying what was written instead would repeat the file
// back at a reader who has it open.
func wants(at, what string) error {
	return fmt.Errorf("%s: wants %s", at, what)
}

// weakString renders a scalar as the string a field holds. It goes through
// weakEnvString so that a value means the same thing whichever pass reads it —
// the env pass reads the tree a second time — and refuses the containers,
// because a list or an object written where a name belongs is a mistake no
// rendering of it could repair.
func weakString(val any, at string) (string, error) {
	switch val.(type) {
	case nil, string, bool, int, int64, float64:
		return weakEnvString(val), nil
	}
	return "", wants(at, "a string")
}

// weakInt reads a number. Every parser has its own Go type for one — JSON a
// float64, YAML an int, TOML an int64 — and a flag hands its value over as the
// text the operator typed, so all four spellings are the same number here. A
// float carrying a fraction is refused rather than truncated: the file said
// something the field cannot hold, and quietly keeping half of it is how a
// concurrency of 2.5 becomes a 2 nobody asked for.
func weakInt(val any, at string) (int, error) {
	switch t := val.(type) {
	case nil:
		return 0, nil
	case int:
		return t, nil
	case int64:
		return int(t), nil
	case float64:
		if t != math.Trunc(t) {
			return 0, wants(at, "a whole number")
		}
		return int(t), nil
	case bool:
		if t {
			return 1, nil
		}
		return 0, nil
	case string:
		if t == "" {
			return 0, nil
		}
		n, err := strconv.ParseInt(t, 0, 64)
		if err != nil {
			return 0, wants(at, "a number")
		}
		return int(n), nil
	}
	return 0, wants(at, "a number")
}

// weakBool reads a flag. Both spellings a format offers are accepted, and so
// is a number, which is how a value that travelled through an environment
// variable or a template arrives.
func weakBool(val any, at string) (bool, error) {
	switch t := val.(type) {
	case nil:
		return false, nil
	case bool:
		return t, nil
	case int:
		return t != 0, nil
	case int64:
		return t != 0, nil
	case float64:
		return t != 0, nil
	case string:
		if t == "" {
			return false, nil
		}
		b, err := strconv.ParseBool(t)
		if err != nil {
			return false, wants(at, "true or false")
		}
		return b, nil
	}
	return false, wants(at, "true or false")
}

// weakList reads the two list shapes that reach the decoder: what a parser
// produces, and the elements a list-valued flag hands over rather than its
// printed form.
func weakList(val any) ([]any, bool) {
	switch t := val.(type) {
	case []any:
		return t, true
	case []string:
		out := make([]any, len(t))
		for i, s := range t {
			out[i] = s
		}
		return out, true
	}
	return nil, false
}

// splitList is the comma shorthand for a list of plain values: `"lint,build"`
// is the two script names it reads as, and an empty string is an empty list
// rather than a list holding nothing but emptiness.
//
// It lives here and nowhere else. The keys whose values are text — a shell
// command, a folder name, a line of a changelog — have setters of their own
// that never call it, which is what keeps a comma inside a `docker buildx
// --output` argument the one command the file wrote.
func splitList(s string) []string {
	if s == "" {
		return []string{}
	}
	return strings.Split(s, ",")
}

// str fills a plain text field.
func str(dst *string) setter {
	return func(val any, at string) error {
		s, err := weakString(val, at)
		if err != nil {
			return err
		}
		*dst = s
		return nil
	}
}

// num fills a whole-number field.
func num(dst *int) setter {
	return func(val any, at string) error {
		n, err := weakInt(val, at)
		if err != nil {
			return err
		}
		*dst = n
		return nil
	}
}

// flag fills a plain boolean field.
func flag(dst *bool) setter {
	return func(val any, at string) error {
		b, err := weakBool(val, at)
		if err != nil {
			return err
		}
		*dst = b
		return nil
	}
}

// flagPtr fills a tri-state boolean: the pointer is allocated only because the
// file wrote the key, so a layer that says nothing about the option stays nil
// and a nearer layer's value, or the default, survives.
func flagPtr(dst **bool) setter {
	return func(val any, at string) error {
		b, err := weakBool(val, at)
		if err != nil {
			return err
		}
		*dst = &b
		return nil
	}
}

// strs fills a list of plain values, taking the comma shorthand for a scalar
// string and lifting any other scalar into the one-element list it stands for.
func strs(dst *[]string) setter {
	return func(val any, at string) error {
		if s, ok := val.(string); ok {
			*dst = splitList(s)
			return nil
		}
		items, ok := weakList(val)
		if !ok {
			s, err := weakString(val, at)
			if err != nil {
				return err
			}
			*dst = []string{s}
			return nil
		}
		out := make([]string, 0, len(items))
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

// nums fills a list of numbers, taking the same two shorthands strs takes:
// `concurrency: 3` is the pair it applies to, and `--concurrency 4,2` is the
// pair the operator typed.
func nums(dst *[]int) setter {
	return func(val any, at string) error {
		if s, ok := val.(string); ok {
			return fillNums(dst, at, stringItems(splitList(s)))
		}
		items, ok := weakList(val)
		if !ok {
			n, err := weakInt(val, at)
			if err != nil {
				return err
			}
			*dst = []int{n}
			return nil
		}
		return fillNums(dst, at, items)
	}
}

// fillNums reads every element of a number list, naming the element that is
// not one.
func fillNums(dst *[]int, at string, items []any) error {
	out := make([]int, 0, len(items))
	for i, item := range items {
		n, err := weakInt(item, indexPath(at, i))
		if err != nil {
			return err
		}
		out = append(out, n)
	}
	*dst = out
	return nil
}

// stringItems is a list of strings as the generic list the element loops read.
func stringItems(list []string) []any {
	out := make([]any, len(list))
	for i, s := range list {
		out[i] = s
	}
	return out
}

// strMap fills a map of names to plain values — an env layer, the initial
// versions, the parser's type table. The keys are copied exactly as they
// arrive: the lowered tree has already folded them, and folding them twice is
// how a key that legitimately holds capitals would lose them.
func strMap(dst *map[string]string) setter {
	return func(val any, at string) error {
		m, ok := val.(map[string]any)
		if !ok {
			return wants(at, "an object")
		}
		out := make(map[string]string, len(m))
		for _, k := range sortedKeys(m) {
			s, err := weakString(m[k], keyPath(at, k))
			if err != nil {
				return err
			}
			out[k] = s
		}
		*dst = out
		return nil
	}
}

// rawMap fills a free-form object dispat never reads. Its contents are the
// repository's own, so nothing inside it is a key the model has to know: the
// unknown-key refusal stops at its edge, by construction rather than by an
// exemption someone has to maintain.
func rawMap(dst *map[string]any) setter {
	return func(val any, at string) error {
		m, ok := val.(map[string]any)
		if !ok {
			return wants(at, "an object")
		}
		out := make(map[string]any, len(m))
		maps.Copy(out, m)
		*dst = out
		return nil
	}
}

// obj fills an optional sub-object. The pointer is allocated only when the
// file wrote the object, which is the distinction the whole layering rests on:
// nil is a layer saying nothing, and an object with no keys at all was pruned
// before the decoder saw it (see settings), so it says nothing either.
func obj[T any](dst **T, table func(*T) fields) setter {
	return func(val any, at string) error {
		into := new(T)
		if err := decodeObject(val, at, table(into)); err != nil {
			return err
		}
		*dst = into
		return nil
	}
}

// objMap fills a map of named objects — spaces, package entries, version
// groups. The keys are the names the rest of dispat resolves against, arriving
// folded from the lowered tree and copied from there untouched. An entry
// written with no body is still an entry: the file named the space, and naming
// it is what puts it in the map.
func objMap[T any](dst *map[string]T, table func(*T) fields) setter {
	return func(val any, at string) error {
		m, ok := val.(map[string]any)
		if !ok {
			return wants(at, "an object")
		}
		out := make(map[string]T, len(m))
		for _, k := range sortedKeys(m) {
			var into T
			if m[k] != nil {
				if err := decodeObject(m[k], keyPath(at, k), table(&into)); err != nil {
					return err
				}
			}
			out[k] = into
		}
		*dst = out
		return nil
	}
}

// objList fills a list of objects — alias tags, webhooks, headers, replace
// rules. A single object stands for the one-element list, because a repository
// with exactly one webhook should not have to write an array around it.
func objList[T any](dst *[]T, table func(*T) fields) setter {
	return func(val any, at string) error {
		items, ok := weakList(val)
		if !ok {
			if _, isObject := val.(map[string]any); !isObject {
				return wants(at, "an object or a list of objects")
			}
			items = []any{val}
		}
		out := make([]T, 0, len(items))
		for i, item := range items {
			var into T
			if item != nil {
				if err := decodeObject(item, indexPath(at, i), table(&into)); err != nil {
					return err
				}
			}
			out = append(out, into)
		}
		*dst = out
		return nil
	}
}

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
// carry one.
func scriptMap(dst *map[string]Script) setter {
	return func(val any, at string) error {
		m, ok := val.(map[string]any)
		if !ok {
			return wants(at, "an object")
		}
		out := make(map[string]Script, len(m))
		for _, k := range sortedKeys(m) {
			var entry Script
			if err := script(&entry)(m[k], keyPath(at, k)); err != nil {
				return err
			}
			out[k] = entry
		}
		*dst = out
		return nil
	}
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
// names arrive folded, exactly like the keys of `packages` and `spaces`, and
// are resolved back onto the packages they name in discovery.
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

// entryLineOf reads one element of a record-line list in whichever of its
// three shapes it was written.
func entryLineOf(item any, at string) (EntryLine, error) {
	var line EntryLine
	if item == nil {
		return line, nil
	}
	if _, isObject := item.(map[string]any); isObject {
		return line, decodeObject(item, at, entryLineFields(&line))
	}
	if lines, ok := stringList(item); ok {
		line.Line = lines
		return line, nil
	}
	return line, wants(at, "a line of text, a list of lines, or an object")
}
