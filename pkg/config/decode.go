package config

// The decoder: how a settings map becomes your structs, and nothing else.
//
// It is a table of setters rather than a walk over the model's types. Every
// struct the config language names has a fields table mapping the keys a file
// may write to closures that fill that struct's own fields, and decoding is
// looking each key up and calling what it finds. A key with no setter is a key
// the model has no field for, which is the unknown-key refusal every config
// typo lands in; that refusal is therefore structural rather than a check
// somebody has to remember to run.
//
// Reflection is what a decoder usually does this with, and it is what this
// replaces. A reflected decoder has to be told the exceptions — which
// conversions are allowed, which fields are squashed, which types have a
// shorthand — through hooks that fire on a Go type and cannot see the key that
// produced it. The hazard is not hypothetical: the conversion that lifts a
// scalar into a list splits it on commas, which is right for a list of names
// and wrong for a shell command, and the two are kept apart only by the order
// the hooks were composed in. Here they are different setters, so the order
// that used to matter cannot exist.
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
//     it is not a typo, and it leaves its field at the zero value the caller's
//     own defaults apply to.
//   - An unknown key is an error, and the error names it by its full path from
//     the root, because a typo the loader accepts is configuration that
//     silently never applies.
//   - Keys are visited in order and the first mistake is the one reported. A
//     map has no order of its own, and a load that fails differently from one
//     run to the next is a load nobody can fix.

import (
	"context"
	"fmt"
	"maps"
)

// Setter fills one field from the value written under its key. at is the key's
// full path from the root of the file, which is what an error has to name for
// the reader to find it.
type Setter func(val any, at string) error

// Fields is one struct's whole config surface: every key a file may write
// under the object, spelled in lower case, and what writing it does. A file
// spells the key however it likes and DecodeObject folds it to find the
// setter. A struct's table is built against a particular destination, so a
// setter writes into that value and nowhere else.
type Fields map[string]Setter

// foldScan is the object size below which the fold-duplicate check compares
// keys against a slice rather than building a map. Nearly every object in
// nearly every config file is smaller than this, and a map allocated to hold
// four keys costs more than the scan it saves.
const foldScan = 8

// DecodeObject fills one object from the map written for it.
//
// It is the only place the language's object rules live: the value has to be
// an object, no two of its keys may fold together, its keys are visited in
// order, a key the table does not hold is unknown, and a key holding nothing
// says nothing. The table is keyed in lower case and the key is folded to find
// its setter, which is what lets `logLevel` and `loglevel` both load.
func DecodeObject(val any, at string, f Fields) error {
	m, ok := val.(map[string]any)
	if !ok {
		return Wants(at, "an object")
	}
	// One sort answers both questions asked of this object: which keys fold
	// together, and in which order they are visited. They were two sorts of the
	// same map, and the second could only ever agree with the first.
	keys := SortedKeys(m)
	if err := refuseFoldDuplicates(keys, at); err != nil {
		return err
	}
	for _, key := range keys {
		path := KeyPath(at, key)
		set, known := f[Fold(key)]
		if !known {
			return &UnknownKeyError{Key: path}
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

// Decode is DecodeObject with the loader's events around it, for a caller that
// wants the decode's outcome in the same log as the load's. The decode itself
// is a package-level function because a fields table belongs to a struct
// rather than to a loader.
func (l *Loader) Decode(ctx context.Context, val any, at string, f Fields) error {
	log := l.loader().logger(ctx)
	err := DecodeObject(val, at, f)
	switch {
	case !log.Enabled(LevelDebug):
	case err != nil:
		log.Log(LevelDebug, EventDecodeFailed, Str("at", objectAt(at)), Err(err))
	default:
		log.Log(LevelDebug, EventDecodeDone, Str("at", objectAt(at)), Num("keys", len(f)))
	}
	return err
}

// refuseFoldDuplicates refuses an object holding two keys that fold together.
//
// Every name in a configuration is matched case-insensitively, so `Build` and
// `build` in one object are one name written twice, and which of the two a
// lookup found would be whatever the map iteration happened to hand it. The
// keys arrive sorted, so a file with several collisions in it always reports
// the same one first, and the pair is reported in the order the two spellings
// were seen.
func refuseFoldDuplicates(keys []string, at string) error {
	if len(keys) <= foldScan {
		var folded [foldScan]string
		for i, key := range keys {
			fold := Fold(key)
			for j := 0; j < i; j++ {
				if folded[j] == fold {
					return &FoldCollisionError{At: objectAt(at), First: keys[j], Second: key}
				}
			}
			folded[i] = fold
		}
		return nil
	}
	seen := make(map[string]string, len(keys))
	for _, key := range keys {
		fold := Fold(key)
		if prev, dup := seen[fold]; dup {
			return &FoldCollisionError{At: objectAt(at), First: prev, Second: key}
		}
		seen[fold] = key
	}
	return nil
}

// objectAt names the object a whole-object error is about. The root has no key
// path of its own, so it is named for what it is.
func objectAt(at string) string {
	if at == "" {
		return "the document"
	}
	return at
}

// Merge folds a squashed embed's table into the table of the object that
// embeds it, which is exactly what squashing means: the embedded struct's keys
// are written at the enclosing object's level, with no key of their own.
func Merge(into, embedded Fields) Fields {
	maps.Copy(into, embedded)
	return into
}

// KeyPath is the path of a key inside the object at parent. The root object
// has no path of its own, so its keys are named by themselves.
//
// The separator is DefaultKeyDelim whatever Options.KeyDelim says: the decode
// is a package-level table rather than a loader's, and the path it prints is a
// sentence for a person rather than a key anything looks up.
func KeyPath(parent, key string) string {
	if parent == "" {
		return key
	}
	return parent + DefaultKeyDelim + key
}

// IndexPath is the path of one element of the list at parent.
func IndexPath(parent string, i int) string {
	return fmt.Sprintf("%s[%d]", parent, i)
}
