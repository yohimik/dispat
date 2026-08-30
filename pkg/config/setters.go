package config

// The setters a fields table is built from: one per shape a config key can
// hold. A caller writing its own setter writes a Setter closure of the same
// form, and these are the examples as much as they are the library.

import "maps"

// String fills a plain text field.
func String(dst *string) Setter {
	return func(val any, at string) error {
		s, err := WeakString(val, at)
		if err != nil {
			return err
		}
		*dst = s
		return nil
	}
}

// Int fills a whole-number field.
func Int(dst *int) Setter {
	return func(val any, at string) error {
		n, err := WeakInt(val, at)
		if err != nil {
			return err
		}
		*dst = n
		return nil
	}
}

// Bool fills a plain boolean field.
func Bool(dst *bool) Setter {
	return func(val any, at string) error {
		b, err := WeakBool(val, at)
		if err != nil {
			return err
		}
		*dst = b
		return nil
	}
}

// BoolPtr fills a tri-state boolean: the pointer is allocated only because the
// file wrote the key, so a layer that says nothing about the option stays nil
// and a nearer layer's value, or the default, survives.
func BoolPtr(dst **bool) Setter {
	return func(val any, at string) error {
		b, err := WeakBool(val, at)
		if err != nil {
			return err
		}
		*dst = &b
		return nil
	}
}

// Strings fills a list of plain values, taking the comma shorthand for a
// scalar string and lifting any other scalar into the one-element list it
// stands for.
func Strings(dst *[]string) Setter {
	return func(val any, at string) error {
		if s, ok := val.(string); ok {
			*dst = SplitList(s)
			return nil
		}
		items, ok := WeakList(val)
		if !ok {
			s, err := WeakString(val, at)
			if err != nil {
				return err
			}
			*dst = []string{s}
			return nil
		}
		out := make([]string, 0, len(items))
		for i, item := range items {
			s, err := WeakString(item, IndexPath(at, i))
			if err != nil {
				return err
			}
			out = append(out, s)
		}
		*dst = out
		return nil
	}
}

// Ints fills a list of numbers, taking the same two shorthands Strings takes:
// a scalar is the one-element list it stands for, and a comma-separated string
// is the list somebody typed on a command line.
func Ints(dst *[]int) Setter {
	return func(val any, at string) error {
		if s, ok := val.(string); ok {
			return fillInts(dst, at, stringItems(SplitList(s)))
		}
		items, ok := WeakList(val)
		if !ok {
			n, err := WeakInt(val, at)
			if err != nil {
				return err
			}
			*dst = []int{n}
			return nil
		}
		return fillInts(dst, at, items)
	}
}

// fillInts reads every element of a number list, naming the element that is
// not one.
func fillInts(dst *[]int, at string, items []any) error {
	out := make([]int, 0, len(items))
	for i, item := range items {
		n, err := WeakInt(item, IndexPath(at, i))
		if err != nil {
			return err
		}
		out = append(out, n)
	}
	*dst = out
	return nil
}

// StringMap fills a map of names to plain values — an env layer, a table of
// initials, a parser's type table. The keys are copied exactly as the file
// wrote them, which is what an env layer needs (PATH and Path are two
// variables) and what every other name gets for free.
func StringMap(dst *map[string]string) Setter {
	return func(val any, at string) error {
		m, ok := val.(map[string]any)
		if !ok {
			return Wants(at, "an object")
		}
		keys := SortedKeys(m)
		if err := refuseFoldDuplicates(keys, at); err != nil {
			return err
		}
		out := make(map[string]string, len(m))
		for _, k := range keys {
			s, err := WeakString(m[k], KeyPath(at, k))
			if err != nil {
				return err
			}
			out[k] = s
		}
		*dst = out
		return nil
	}
}

// RawMap fills a free-form object the program never reads. Its contents are
// the repository's own, so nothing inside it is a key the model has to know:
// the unknown-key refusal stops at its edge, by construction rather than by an
// exemption someone has to maintain. The fold-duplicate refusal stops there
// too, and for the same reason: nothing looks anything up in here, so two keys
// that fold together are the file author's own business.
func RawMap(dst *map[string]any) Setter {
	return func(val any, at string) error {
		m, ok := val.(map[string]any)
		if !ok {
			return Wants(at, "an object")
		}
		out := make(map[string]any, len(m))
		maps.Copy(out, m)
		*dst = out
		return nil
	}
}

// Object fills an optional sub-object. The pointer is allocated only when the
// file wrote the object, which is the distinction a layered configuration
// rests on: nil is a layer saying nothing, and an object with no keys at all
// was pruned before the decoder saw it (see Tree.Settings), so it says nothing
// either.
func Object[T any](dst **T, table func(*T) Fields) Setter {
	return func(val any, at string) error {
		into := new(T)
		if err := DecodeObject(val, at, table(into)); err != nil {
			return err
		}
		*dst = into
		return nil
	}
}

// ObjectMap fills a map of named objects. The keys are the names the rest of
// the program resolves against, copied exactly as the file wrote them and
// matched case-insensitively wherever they are looked up. An entry written
// with no body is still an entry: the file named it, and naming it is what
// puts it in the map.
func ObjectMap[T any](dst *map[string]T, table func(*T) Fields) Setter {
	return func(val any, at string) error {
		m, ok := val.(map[string]any)
		if !ok {
			return Wants(at, "an object")
		}
		keys := SortedKeys(m)
		if err := refuseFoldDuplicates(keys, at); err != nil {
			return err
		}
		out := make(map[string]T, len(m))
		for _, k := range keys {
			var into T
			if m[k] != nil {
				if err := DecodeObject(m[k], KeyPath(at, k), table(&into)); err != nil {
					return err
				}
			}
			out[k] = into
		}
		*dst = out
		return nil
	}
}

// ObjectList fills a list of objects. A single object stands for the
// one-element list, because a configuration with exactly one of something
// should not have to write an array around it.
func ObjectList[T any](dst *[]T, table func(*T) Fields) Setter {
	return func(val any, at string) error {
		items, ok := WeakList(val)
		if !ok {
			if _, isObject := val.(map[string]any); !isObject {
				return Wants(at, "an object or a list of objects")
			}
			items = []any{val}
		}
		out := make([]T, 0, len(items))
		for i, item := range items {
			var into T
			if item != nil {
				if err := DecodeObject(item, IndexPath(at, i), table(&into)); err != nil {
					return err
				}
			}
			out = append(out, into)
		}
		*dst = out
		return nil
	}
}
