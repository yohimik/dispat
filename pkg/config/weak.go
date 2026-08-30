package config

// Weak typing: how a value written in one format's types becomes the value a
// Go field holds.
//
// A bare number is a fine string and a quoted one a fine number, because a
// config file's format has types and a configuration language does not. Every
// parser has its own Go type for a number — JSON a float64, YAML an int, TOML
// an int64 — and an override handed over as text is a fourth spelling of the
// same number, so all four are read here and nowhere else.

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// WeakScalarString renders a scalar the way the weakly typed decode renders
// one. It is also what a generic map's key becomes on the way into the tree,
// so the two renderings can never disagree.
//
// The numeric cases go through strconv rather than fmt.Sprint: a large float
// formatted with %v would come out in scientific notation, which is not what
// the file said.
func WeakScalarString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case nil:
		return ""
	default:
		return fmt.Sprint(t)
	}
}

// WeakString renders a scalar as the string a field holds. It goes through
// WeakScalarString so that a value means the same thing whichever pass reads
// it, and refuses the containers, because a list or an object written where a
// name belongs is a mistake no rendering of it could repair.
func WeakString(val any, at string) (string, error) {
	switch val.(type) {
	case nil, string, bool, int, int64, float64:
		return WeakScalarString(val), nil
	}
	return "", Wants(at, "a string")
}

// WeakInt reads a number in any of the spellings that reach it. A float
// carrying a fraction is refused rather than truncated: the file said
// something the field cannot hold, and quietly keeping half of it is how a
// concurrency of 2.5 becomes a 2 nobody asked for.
func WeakInt(val any, at string) (int, error) {
	switch t := val.(type) {
	case nil:
		return 0, nil
	case int:
		return t, nil
	case int64:
		return int(t), nil
	case float64:
		if t != math.Trunc(t) {
			return 0, Wants(at, "a whole number")
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
			return 0, Wants(at, "a number")
		}
		return int(n), nil
	}
	return 0, Wants(at, "a number")
}

// WeakBool reads a flag. Both spellings a format offers are accepted, and so
// is a number, which is how a value that travelled through an environment
// variable or a template arrives.
//
// Nothing is not one of them, unlike the other two readers: a boolean is only
// ever one key's whole value, and a key holding nothing is skipped before any
// setter runs. There is no list or map of booleans for a null element to
// arrive in.
func WeakBool(val any, at string) (bool, error) {
	switch t := val.(type) {
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
			return false, Wants(at, "true or false")
		}
		return b, nil
	}
	return false, Wants(at, "true or false")
}

// WeakList reads the two list shapes that reach the decoder: what a parser
// produces, and the elements a list-valued override hands over rather than its
// printed form.
func WeakList(val any) ([]any, bool) {
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

// SplitList is the comma shorthand for a list of plain values: `"lint,build"`
// is the two names it reads as, and an empty string is an empty list rather
// than a list holding nothing but emptiness.
//
// It is called by the setters that take the shorthand and by nothing else. A
// key whose value is text — a shell command, a folder name, a line of prose —
// has a setter that never calls it, which is what keeps a comma inside such a
// value the character the file wrote.
func SplitList(s string) []string {
	if s == "" {
		return []string{}
	}
	return strings.Split(s, ",")
}

// stringItems is a list of strings as the generic list the element loops read.
func stringItems(list []string) []any {
	out := make([]any, len(list))
	for i, s := range list {
		out[i] = s
	}
	return out
}
