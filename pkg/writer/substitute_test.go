package writer

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// seedText writes one text file and returns its path.
func seedText(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// readText reads a file back as a string.
func readText(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// subNames renders a result's substitutions as "find=>write" for comparison.
func subNames(subs []Substitution) string {
	parts := make([]string, 0, len(subs))
	for _, s := range subs {
		parts = append(parts, s.Find+"=>"+s.Write)
	}
	return strings.Join(parts, ",")
}

func TestSubstituteReplacesEveryOccurrence(t *testing.T) {
	path := seedText(t, "README.md", "acme-core:1.2.3 and again acme-core:1.2.3\nplus acme-core:1.2.3\n")
	res, err := Substitute(path, []Substitution{{Find: "acme-core:1.2.3", Write: "acme-core:1.3.0"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Count != 3 {
		t.Errorf("Count = %d, want 3", res.Count)
	}
	if got := subNames(res.Applied); got != "acme-core:1.2.3=>acme-core:1.3.0" {
		t.Errorf("Applied = %q", got)
	}
	want := "acme-core:1.3.0 and again acme-core:1.3.0\nplus acme-core:1.3.0\n"
	if got := readText(t, path); got != want {
		t.Errorf("file = %q, want %q", got, want)
	}
	if res.Path != path {
		t.Errorf("Path = %q, want %q", res.Path, path)
	}
}

func TestSubstituteAppliesInOrderOverTheLast(t *testing.T) {
	// The second substitution sees what the first wrote, which is the
	// documented chaining and the reason the caller picks the order.
	path := seedText(t, "notes.txt", "one\n")
	res, err := Substitute(path, []Substitution{
		{Find: "one", Write: "two"},
		{Find: "two", Write: "three"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := readText(t, path); got != "three\n" {
		t.Errorf("file = %q, want %q", got, "three\n")
	}
	if len(res.Applied) != 2 {
		t.Errorf("Applied = %q, want both", subNames(res.Applied))
	}
}

func TestSubstituteSplitsMissingSkippedAndApplied(t *testing.T) {
	path := seedText(t, "Dockerfile", "FROM acme/base:1.0.0\n")
	res, err := Substitute(path, []Substitution{
		{Find: "acme/base:1.0.0", Write: "acme/base:1.1.0"},
		{Find: "acme/other:1.0.0", Write: "acme/other:1.1.0"},
		{Find: "FROM", Write: "FROM"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := subNames(res.Applied); got != "acme/base:1.0.0=>acme/base:1.1.0" {
		t.Errorf("Applied = %q", got)
	}
	if got := subNames(res.Missing); got != "acme/other:1.0.0=>acme/other:1.1.0" {
		t.Errorf("Missing = %q", got)
	}
	if got := subNames(res.Skipped); got != "FROM=>FROM" {
		t.Errorf("Skipped = %q", got)
	}
	if res.Count != 1 {
		t.Errorf("Count = %d, want 1", res.Count)
	}
}

func TestSubstituteAnEmptyWriteDeletes(t *testing.T) {
	path := seedText(t, "list.txt", "keep DROPME keep\n")
	if _, err := Substitute(path, []Substitution{{Find: " DROPME", Write: ""}}); err != nil {
		t.Fatal(err)
	}
	if got := readText(t, path); got != "keep keep\n" {
		t.Errorf("file = %q", got)
	}
}

func TestSubstituteLeavesAnUnchangedFileAlone(t *testing.T) {
	path := seedText(t, "notes.txt", "nothing to do here\n")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Substitute(path, []Substitution{{Find: "absent", Write: "x"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Applied) != 0 || res.Count != 0 {
		t.Errorf("a miss reported work: %+v", res)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// No write at all, so the file keeps its modification time: the atomic
	// rename would have replaced the inode.
	if !after.ModTime().Equal(before.ModTime()) {
		t.Error("a no-op substitution rewrote the file")
	}
}

func TestSubstitutePreservesPermissions(t *testing.T) {
	path := seedText(t, "script.sh", "VERSION=1.0.0\n")
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Substitute(path, []Substitution{{Find: "1.0.0", Write: "1.1.0"}}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("mode = %v, want 0755", info.Mode().Perm())
	}
}

func TestSubstituteRefusesAnEmptyFind(t *testing.T) {
	path := seedText(t, "notes.txt", "x\n")
	_, err := Substitute(path, []Substitution{{Find: "x", Write: "y"}, {Find: "", Write: "z"}})
	if !errors.Is(err, ErrEmptyFind) {
		t.Fatalf("got %v, want ErrEmptyFind", err)
	}
	// The whole call is refused before anything is written.
	if got := readText(t, path); got != "x\n" {
		t.Errorf("file = %q, want it untouched", got)
	}
}

func TestSubstituteRefusesABinaryFile(t *testing.T) {
	path := seedText(t, "blob.bin", "prefix\x00 1.0.0 suffix")
	_, err := Substitute(path, []Substitution{{Find: "1.0.0", Write: "1.1.0"}})
	if !errors.Is(err, ErrBinaryFile) {
		t.Fatalf("got %v, want ErrBinaryFile", err)
	}
	if got := readText(t, path); !strings.Contains(got, "1.0.0") {
		t.Errorf("file = %q, want it untouched", got)
	}
}

func TestSubstituteAcceptsANULBeyondTheSniff(t *testing.T) {
	// The sniff looks at the head alone, the way git and grep do, so a text
	// file with one stray byte far into it is still text.
	path := seedText(t, "long.txt", "1.0.0\n"+strings.Repeat("a", binarySniff)+"\x00")
	if _, err := Substitute(path, []Substitution{{Find: "1.0.0", Write: "1.1.0"}}); err != nil {
		t.Fatal(err)
	}
	if got := readText(t, path); !strings.HasPrefix(got, "1.1.0\n") {
		t.Errorf("file = %q", got[:8])
	}
}

func TestSubstituteRefusesAnOversizedFile(t *testing.T) {
	path := seedText(t, "huge.txt", "1.0.0\n")
	if err := os.Truncate(path, maxManifestBytes+1); err != nil {
		t.Skipf("cannot make a sparse file here: %v", err)
	}
	_, err := Substitute(path, []Substitution{{Find: "1.0.0", Write: "1.1.0"}})
	if !errors.Is(err, ErrManifestTooLarge) {
		t.Fatalf("got %v, want ErrManifestTooLarge", err)
	}
}

func TestSubstituteReportsAMissingFile(t *testing.T) {
	_, err := Substitute(filepath.Join(t.TempDir(), "absent.txt"), []Substitution{{Find: "a", Write: "b"}})
	if err == nil {
		t.Fatal("a missing file must error")
	}
}

func TestSubstituteBytesLeavesTheInputAlone(t *testing.T) {
	in := []byte("1.0.0")
	out, counts := SubstituteBytes(in, []Substitution{{Find: "1.0.0", Write: "2.0.0"}})
	if string(in) != "1.0.0" {
		t.Errorf("input mutated: %q", in)
	}
	if string(out) != "2.0.0" {
		t.Errorf("out = %q", out)
	}
	if len(counts) != 1 || counts[0] != 1 {
		t.Errorf("counts = %v", counts)
	}
}

func TestSubstituteBytesIgnoresAnEmptyFind(t *testing.T) {
	// Direct callers get the same protection Substitute enforces up front: an
	// empty pattern matches everywhere, so it does nothing rather than
	// shredding the input.
	out, counts := SubstituteBytes([]byte("abc"), []Substitution{{Find: "", Write: "X"}})
	if string(out) != "abc" || counts[0] != 0 {
		t.Errorf("out = %q, counts = %v", out, counts)
	}
}

// The internal replacer's own edges, which no format writer can reach on
// purpose but which stand between a bug and a corrupted manifest.

func TestReplacerRefusesOverlappingPatches(t *testing.T) {
	path := seedText(t, "notes.txt", "0123456789")
	rep, err := openReplacer(path)
	if err != nil {
		t.Fatal(err)
	}
	rep.replace(span{0, 5}, []byte("A"))
	rep.replace(span{3, 8}, []byte("B"))
	err = rep.commit(nil)
	if !errors.Is(err, errOverlappingPatches) {
		t.Fatalf("got %v, want errOverlappingPatches", err)
	}
	if got := readText(t, path); got != "0123456789" {
		t.Errorf("a refused commit wrote %q", got)
	}
}

func TestReplacerRefusesSpansOnARegeneratedFile(t *testing.T) {
	path := seedText(t, "notes.txt", "0123456789")
	rep, err := openReplacer(path)
	if err != nil {
		t.Fatal(err)
	}
	rep.setWhole([]byte("whole"))
	rep.replace(span{0, 1}, []byte("X"))
	if err := rep.commit(nil); !errors.Is(err, errMixedEdits) {
		t.Fatalf("got %v, want errMixedEdits", err)
	}
	if got := readText(t, path); got != "0123456789" {
		t.Errorf("a refused commit wrote %q", got)
	}
}

func TestReplacerAppliesPatchesInOffsetOrder(t *testing.T) {
	// Queued out of order and of different widths, so a splice that used stale
	// offsets would land visibly wrong.
	path := seedText(t, "notes.txt", "aaa bbb ccc")
	rep, err := openReplacer(path)
	if err != nil {
		t.Fatal(err)
	}
	rep.replace(span{8, 11}, []byte("CCCCC"))
	rep.replace(span{0, 3}, []byte("A"))
	rep.replace(span{4, 7}, []byte("BB"))
	if err := rep.commit(nil); err != nil {
		t.Fatal(err)
	}
	if got := readText(t, path); got != "A BB CCCCC" {
		t.Errorf("file = %q, want %q", got, "A BB CCCCC")
	}
}

func TestReplacerLeavesTheFileAloneWhenVerifyFails(t *testing.T) {
	path := seedText(t, "notes.txt", "before")
	rep, err := openReplacer(path)
	if err != nil {
		t.Fatal(err)
	}
	rep.setWhole([]byte("after"))
	boom := errors.New("boom")
	if err := rep.commit(func([]byte) error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("got %v, want the verifier's error", err)
	}
	if got := readText(t, path); got != "before" {
		t.Errorf("file = %q, want it untouched", got)
	}
}

func TestReplacerAtAndTextReadTheFileAsFound(t *testing.T) {
	path := seedText(t, "notes.txt", "hello world")
	rep, err := openReplacer(path)
	if err != nil {
		t.Fatal(err)
	}
	rep.replace(span{0, 5}, []byte("bye"))
	// The queued patch is not applied to the working copy, so a locator may
	// keep using offsets into it right up to the commit.
	if got := string(rep.at(span{6, 11})); got != "world" {
		t.Errorf("at = %q", got)
	}
	if rep.text() != "hello world" {
		t.Errorf("text = %q", rep.text())
	}
}

func TestOpenReplacerRefusesAnOversizedFile(t *testing.T) {
	path := seedText(t, "huge.txt", "x")
	if err := os.Truncate(path, maxManifestBytes+1); err != nil {
		t.Skipf("cannot make a sparse file here: %v", err)
	}
	if _, err := openReplacer(path); !errors.Is(err, ErrManifestTooLarge) {
		t.Fatalf("got %v, want ErrManifestTooLarge", err)
	}
}
