package config

// What "the same name" means, and the weakly typed readers underneath it.

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

// everyRune yields every Unicode code point a string can carry.
func everyRune(yield func(rune) bool) {
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if utf8.ValidRune(r) && !yield(r) {
			return
		}
	}
}

// foldClass is r's Unicode SimpleFold class, the letters strings.EqualFold
// takes for one letter, starting at r.
func foldClass(r rune) []rune {
	class := []rune{r}
	for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
		class = append(class, next)
	}
	return class
}

// ordinaryLower is the one letter the letters of r's class lowercase to when
// lowercasing changes them, provided it is in the class itself. Σ, σ and ς
// answer σ, because Σ lowercases to it; İ answers nothing, because its lower
// case i belongs to another class.
func ordinaryLower(r rune) (rune, bool) {
	class := foldClass(r)
	var lower rune
	found := false
	for _, letter := range class {
		mapped := unicode.ToLower(letter)
		if mapped == letter {
			continue
		}
		if found && mapped != lower {
			return 0, false
		}
		lower, found = mapped, true
	}
	if !found || !slices.Contains(class, lower) {
		return 0, false
	}
	return lower, true
}

// isLowercasingDisagreement reports whether strings.ToLower and
// strings.EqualFold disagree about r: lowercasing leaves r apart from the
// lower case the rest of its class agrees on (ς beside σ, µ beside μ), takes
// it out of its class (İ to i), or keeps apart letters that are each their own
// lower case.
func isLowercasingDisagreement(r rune) bool {
	lower := unicode.ToLower(r)
	if ordinary, ok := ordinaryLower(r); ok {
		return lower != ordinary
	}
	return lower != r || unicode.SimpleFold(r) != r
}

// sweepFailer reports what a sweep over every rune found wrong and stops the
// test once the list is long enough to diagnose, rather than printing a line
// for each of a million runes.
func sweepFailer(t *testing.T) func(format string, args ...any) {
	failures := 0
	return func(format string, args ...any) {
		t.Helper()
		t.Errorf(format, args...)
		if failures++; failures >= 20 {
			t.FailNow()
		}
	}
}

// TestFoldKeepsOneSpellingPerFoldClass: every rune folds to a letter of its
// own SimpleFold class, every letter of a class folds to the same one, and
// that letter is lower case whenever the class holds a lower-case letter.
func TestFoldKeepsOneSpellingPerFoldClass(t *testing.T) {
	fail := sweepFailer(t)
	for r := range everyRune {
		folded := Fold(string(r))
		if !strings.EqualFold(folded, string(r)) {
			fail("Fold(%U) = %q, outside its fold class", r, folded)
			continue
		}
		if next := unicode.SimpleFold(r); Fold(string(next)) != folded {
			fail("Fold(%U) = %q but Fold(%U) = %q", r, folded, next, Fold(string(next)))
		}
		letter, _ := utf8.DecodeRuneInString(folded)
		if slices.ContainsFunc(foldClass(r), unicode.IsLower) && !unicode.IsLower(letter) {
			fail("Fold(%U) = %U, not lower case although its class holds a lower-case letter", r, letter)
		}
	}
}

// TestFoldIsStringsToLowerWhereLowercasingAgreesWithEqualFold: Fold writes
// what strings.ToLower writes, except for the letters ToLower and EqualFold
// disagree on. Those are derived from the Unicode tables rather than listed:
// such a letter keys as the lower case the rest of its class agrees on (ς as
// σ, µ as μ), or as itself when lowercasing would take it out of its class
// (İ).
func TestFoldIsStringsToLowerWhereLowercasingAgreesWithEqualFold(t *testing.T) {
	fail := sweepFailer(t)
	disagreements := map[rune]bool{}
	for r := range everyRune {
		folded, lower := Fold(string(r)), strings.ToLower(string(r))
		if !isLowercasingDisagreement(r) {
			if folded != lower {
				fail("Fold(%U) = %q, strings.ToLower = %q", r, folded, lower)
			}
			continue
		}
		disagreements[r] = true
		ordinary, ok := ordinaryLower(r)
		switch {
		case ok && folded != string(ordinary):
			fail("Fold(%U) = %q, want its class's lower case %q", r, folded, string(ordinary))
		case !ok && unicode.SimpleFold(r) == r && folded != string(r):
			fail("Fold(%U) = %q, want the letter itself", r, folded)
		}
	}
	// The derivation finds the letters lowercasing splits among the explicit
	// rows below, and none of the others.
	for r, want := range map[rune]bool{
		'ς': true, 'ſ': true, 'µ': true, 'ͅ': true, 'ι': true, 'İ': true,
		'σ': false, 's': false, 'μ': false, 'ι': false, 'K': false, 'ı': false,
	} {
		if disagreements[r] != want {
			t.Errorf("%U: lowercasing disagreement = %v, want %v", r, disagreements[r], want)
		}
	}
}

// TestFoldKeysTheOrdinaryLowerCaseLetter: a table keyed by the documented
// lower-case name finds a name however it is spelled. Each row is a letter
// whose fold class holds more than one upper and one lower case, or a
// letter that is a class of its own.
func TestFoldKeysTheOrdinaryLowerCaseLetter(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"μ", "μ"}, {"Μ", "μ"}, {"µ", "μ"}, // Greek mu and the micro sign
		{"ι", "ι"}, {"Ι", "ι"}, {"ͅ", "ι"}, {"ι", "ι"}, // iota, its subscript and prosgegrammeni
		{"σ", "σ"}, {"Σ", "σ"}, {"ς", "σ"}, // final sigma
		{"s", "s"}, {"S", "s"}, {"ſ", "s"}, // long s
		{"k", "k"}, {"K", "k"}, {"K", "k"}, // Kelvin sign
		{"İ", "İ"}, {"ı", "ı"}, {"I", "i"}, // dotted and dotless i are classes of their own
		{"µService", "μservice"}, {"ΙΟΝ", "ιον"},
	} {
		if got := Fold(tc.in); got != tc.want {
			t.Errorf("Fold(%q) = %q %U, want %q %U", tc.in, got, []rune(got), tc.want, []rune(tc.want))
		}
	}
}

// TestFoldAgreesWithUnicodeSimpleFold: the canonical key and LookupFold's
// EqualFold lookup must agree even when lowercasing gives different results.
func TestFoldAgreesWithUnicodeSimpleFold(t *testing.T) {
	for _, s := range []string{
		"", "build", "Build", "BUILD", "logLevel", "log-level", "log_level_2",
		"ÄÖÜ", "straße", "İstanbul", "İ", "ıi", "日本語", "mixedÄ", "a1B2c3", "Σ", "σ", "ς", "K",
	} {
		for _, other := range []string{"Σ", "σ", "ς", "K", "k", "K", "İ", "I", "i", "ı", s} {
			if got, want := Fold(s) == Fold(other), strings.EqualFold(s, other); got != want {
				t.Errorf("Fold(%q) == Fold(%q) = %v, EqualFold = %v", s, other, got, want)
			}
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
		{[]string{"a"}, "[a]"}, {[]any{"a", "b"}, "[a b]"},
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
