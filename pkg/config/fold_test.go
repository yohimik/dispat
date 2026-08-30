package config

// What "the same name" means, and the weakly typed readers underneath it.

import (
	"reflect"
	"strings"
	"testing"
)

// TestFoldAgreesWithStringsToLower: the fast path is an optimisation, not a
// second definition, so it has to answer what strings.ToLower answers for
// every shape of name a config file can carry.
func TestFoldAgreesWithStringsToLower(t *testing.T) {
	for _, s := range []string{
		"", "build", "Build", "BUILD", "logLevel", "log-level", "log_level_2",
		"ÄÖÜ", "straße", "İstanbul", "ıi", "日本語", "mixedÄ", "a1B2c3",
	} {
		if got, want := Fold(s), strings.ToLower(s); got != want {
			t.Errorf("Fold(%q) = %q, want %q", s, got, want)
		}
	}
}

// TestFoldReturnsTheInputWhenThereIsNothingToDo: a name already written in
// lower-case ASCII — which is nearly every key of nearly every config file —
// comes back as the string that went in.
func TestFoldReturnsTheInputWhenThereIsNothingToDo(t *testing.T) {
	for _, in := range []string{"loglevel", "", "log-level", "a1b2"} {
		if got := Fold(in); got != in {
			t.Errorf("Fold(%q) = %q", in, got)
		}
	}
}

// TestLookupFold: the exact key is tried first, which is both the common case
// and the cheap one; only a name spelled differently pays for the scan.
func TestLookupFold(t *testing.T) {
	m := map[string]string{"MiXed": "v", "plain": "p"}
	for _, tc := range []struct {
		ask, key, val string
		ok            bool
	}{
		{"MiXed", "MiXed", "v", true},
		{"mixed", "MiXed", "v", true},
		{"MIXED", "MiXed", "v", true},
		{"plain", "plain", "p", true},
		{"absent", "", "", false},
	} {
		key, val, ok := LookupFold(m, tc.ask)
		if key != tc.key || val != tc.val || ok != tc.ok {
			t.Errorf("LookupFold(%q) = %q, %q, %v; want %q, %q, %v",
				tc.ask, key, val, ok, tc.key, tc.val, tc.ok)
		}
		name, found := FoldKey(m, tc.ask)
		if name != tc.key || found != tc.ok {
			t.Errorf("FoldKey(%q) = %q, %v", tc.ask, name, found)
		}
	}
}

// TestSortedKeys: a map has no order of its own, and this is where every
// deterministic first error comes from.
func TestSortedKeys(t *testing.T) {
	got := SortedKeys(map[string]int{"c": 1, "a": 2, "B": 3})
	if want := []string{"B", "a", "c"}; !reflect.DeepEqual(want, got) {
		t.Errorf("SortedKeys = %#v, want %#v", got, want)
	}
	if got := SortedKeys(map[string]int{}); len(got) != 0 {
		t.Errorf("SortedKeys of nothing = %#v", got)
	}
}

// TestWeakScalarStringRendersEveryScalar: a number goes through strconv rather
// than fmt, because a large float formatted with %v would come out in
// scientific notation, which is not what the file said.
func TestWeakScalarStringRendersEveryScalar(t *testing.T) {
	for _, tc := range []struct {
		in   any
		want string
	}{
		{"text", "text"}, {true, "true"}, {false, "false"},
		{7, "7"}, {int64(-3), "-3"}, {1.5, "1.5"},
		{1e21, "1000000000000000000000"},
		{nil, ""},
		{[]string{"a"}, "[a]"},
	} {
		if got := WeakScalarString(tc.in); got != tc.want {
			t.Errorf("WeakScalarString(%#v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestWeakReaders: each reader's whole table, including what it refuses.
func TestWeakReaders(t *testing.T) {
	t.Run("string", func(t *testing.T) {
		for _, in := range []any{nil, "s", true, 1, int64(1), 1.5} {
			if _, err := WeakString(in, "at"); err != nil {
				t.Errorf("WeakString(%#v): %v", in, err)
			}
		}
		if _, err := WeakString([]any{}, "at"); err == nil || err.Error() != "at: wants a string" {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("int", func(t *testing.T) {
		for _, tc := range []struct {
			in   any
			want int
		}{
			{nil, 0}, {7, 7}, {int64(7), 7}, {7.0, 7}, {true, 1}, {false, 0},
			{"", 0}, {"7", 7}, {"0x10", 16},
		} {
			got, err := WeakInt(tc.in, "at")
			if err != nil || got != tc.want {
				t.Errorf("WeakInt(%#v) = %d, %v; want %d", tc.in, got, err, tc.want)
			}
		}
		for _, tc := range []struct{ in, want any }{
			{1.5, "at: wants a whole number"},
			{"x", "at: wants a number"},
			{[]any{}, "at: wants a number"},
		} {
			if _, err := WeakInt(tc.in, "at"); err == nil || err.Error() != tc.want {
				t.Errorf("WeakInt(%#v) err = %v, want %v", tc.in, err, tc.want)
			}
		}
	})

	t.Run("bool", func(t *testing.T) {
		if _, err := WeakBool(nil, "at"); err == nil || err.Error() != "at: wants true or false" {
			t.Errorf("nothing is not one of the two spellings: %v", err)
		}
	})

	t.Run("list", func(t *testing.T) {
		if got, ok := WeakList([]any{1}); !ok || len(got) != 1 {
			t.Errorf("WeakList = %#v, %v", got, ok)
		}
		got, ok := WeakList([]string{"a", "b"})
		if !ok || !reflect.DeepEqual([]any{"a", "b"}, got) {
			t.Errorf("WeakList = %#v, %v", got, ok)
		}
		if _, ok := WeakList("a"); ok {
			t.Error("a string is not a list here; the setters decide their own shorthand")
		}
	})

	t.Run("splitList", func(t *testing.T) {
		if got := SplitList(""); !reflect.DeepEqual([]string{}, got) {
			t.Errorf("SplitList(\"\") = %#v, want an empty list", got)
		}
		if got := SplitList("a,b"); !reflect.DeepEqual([]string{"a", "b"}, got) {
			t.Errorf("SplitList = %#v", got)
		}
	})
}
