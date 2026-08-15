package writer

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repNames renders a result's replacements as "find=>write" for comparison.
func repNames(reps []Replacement) string {
	parts := make([]string, 0, len(reps))
	for _, s := range reps {
		parts = append(parts, s.Find+"=>"+s.Write)
	}
	return strings.Join(parts, ",")
}

func TestReplaceReplacesEveryOccurrence(t *testing.T) {
	path := seed(t, "README.md", "acme-core:1.2.3 and again acme-core:1.2.3\nplus acme-core:1.2.3\n")
	res, err := Replace(path, []Replacement{{Find: "acme-core:1.2.3", Write: "acme-core:1.3.0"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Count != 3 {
		t.Errorf("Count = %d, want 3", res.Count)
	}
	if got := repNames(res.Applied); got != "acme-core:1.2.3=>acme-core:1.3.0" {
		t.Errorf("Applied = %q", got)
	}
	want := "acme-core:1.3.0 and again acme-core:1.3.0\nplus acme-core:1.3.0\n"
	if got := read(t, path); got != want {
		t.Errorf("file = %q, want %q", got, want)
	}
	if res.Path != path {
		t.Errorf("Path = %q, want %q", res.Path, path)
	}
}

func TestReplaceAppliesInOrderOverTheLast(t *testing.T) {
	// The second replacement sees what the first wrote, which is the
	// documented chaining and the reason the caller picks the order.
	path := seed(t, "notes.txt", "one\n")
	res, err := Replace(path, []Replacement{
		{Find: "one", Write: "two"},
		{Find: "two", Write: "three"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := read(t, path); got != "three\n" {
		t.Errorf("file = %q, want %q", got, "three\n")
	}
	if len(res.Applied) != 2 {
		t.Errorf("Applied = %q, want both", repNames(res.Applied))
	}
}

func TestReplaceSplitsMissingSkippedAndApplied(t *testing.T) {
	path := seed(t, "Dockerfile", "FROM acme/base:1.0.0\n")
	res, err := Replace(path, []Replacement{
		{Find: "acme/base:1.0.0", Write: "acme/base:1.1.0"},
		{Find: "acme/other:1.0.0", Write: "acme/other:1.1.0"},
		{Find: "FROM", Write: "FROM"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := repNames(res.Applied); got != "acme/base:1.0.0=>acme/base:1.1.0" {
		t.Errorf("Applied = %q", got)
	}
	if got := repNames(res.Missing); got != "acme/other:1.0.0=>acme/other:1.1.0" {
		t.Errorf("Missing = %q", got)
	}
	if got := repNames(res.Skipped); got != "FROM=>FROM" {
		t.Errorf("Skipped = %q", got)
	}
	if res.Count != 1 {
		t.Errorf("Count = %d, want 1", res.Count)
	}
}

func TestReplaceAnEmptyWriteDeletes(t *testing.T) {
	path := seed(t, "list.txt", "keep DROPME keep\n")
	if _, err := Replace(path, []Replacement{{Find: " DROPME", Write: ""}}); err != nil {
		t.Fatal(err)
	}
	if got := read(t, path); got != "keep keep\n" {
		t.Errorf("file = %q", got)
	}
}

func TestReplaceLeavesAnUnchangedFileAlone(t *testing.T) {
	path := seed(t, "notes.txt", "nothing to do here\n")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Replace(path, []Replacement{{Find: "absent", Write: "x"}})
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
		t.Error("a no-op replacement rewrote the file")
	}
}

func TestReplacePreservesPermissions(t *testing.T) {
	path := seed(t, "script.sh", "VERSION=1.0.0\n")
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Replace(path, []Replacement{{Find: "1.0.0", Write: "1.1.0"}}); err != nil {
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

func TestReplaceRefusesAnEmptyFind(t *testing.T) {
	path := seed(t, "notes.txt", "x\n")
	_, err := Replace(path, []Replacement{{Find: "x", Write: "y"}, {Find: "", Write: "z"}})
	if !errors.Is(err, ErrEmptyFind) {
		t.Fatalf("got %v, want ErrEmptyFind", err)
	}
	// The whole call is refused before anything is written.
	if got := read(t, path); got != "x\n" {
		t.Errorf("file = %q, want it untouched", got)
	}
}

func TestReplaceRefusesABinaryFile(t *testing.T) {
	path := seed(t, "blob.bin", "prefix\x00 1.0.0 suffix")
	_, err := Replace(path, []Replacement{{Find: "1.0.0", Write: "1.1.0"}})
	if !errors.Is(err, ErrBinaryFile) {
		t.Fatalf("got %v, want ErrBinaryFile", err)
	}
	if got := read(t, path); !strings.Contains(got, "1.0.0") {
		t.Errorf("file = %q, want it untouched", got)
	}
}

func TestReplaceAcceptsANULBeyondTheSniff(t *testing.T) {
	// The sniff looks at the head alone, the way git and grep do, so a text
	// file with one stray byte far into it is still text.
	path := seed(t, "long.txt", "1.0.0\n"+strings.Repeat("a", binarySniff)+"\x00")
	if _, err := Replace(path, []Replacement{{Find: "1.0.0", Write: "1.1.0"}}); err != nil {
		t.Fatal(err)
	}
	if got := read(t, path); !strings.HasPrefix(got, "1.1.0\n") {
		t.Errorf("file = %q", got[:8])
	}
}

func TestReplaceRefusesAnOversizedFile(t *testing.T) {
	path := seed(t, "huge.txt", "1.0.0\n")
	if err := os.Truncate(path, maxManifestBytes+1); err != nil {
		t.Skipf("cannot make a sparse file here: %v", err)
	}
	_, err := Replace(path, []Replacement{{Find: "1.0.0", Write: "1.1.0"}})
	if !errors.Is(err, ErrManifestTooLarge) {
		t.Fatalf("got %v, want ErrManifestTooLarge", err)
	}
}

func TestReplaceReportsAMissingFile(t *testing.T) {
	_, err := Replace(filepath.Join(t.TempDir(), "absent.txt"), []Replacement{{Find: "a", Write: "b"}})
	if err == nil {
		t.Fatal("a missing file must error")
	}
}

func TestReplaceBytesLeavesTheInputAlone(t *testing.T) {
	in := []byte("1.0.0")
	out, counts := ReplaceBytes(in, []Replacement{{Find: "1.0.0", Write: "2.0.0"}})
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

func TestReplaceBytesIgnoresAnEmptyFind(t *testing.T) {
	// Direct callers get the same protection Replace enforces up front: an
	// empty pattern matches everywhere, so it does nothing rather than
	// shredding the input.
	out, counts := ReplaceBytes([]byte("abc"), []Replacement{{Find: "", Write: "X"}})
	if string(out) != "abc" || counts[0] != 0 {
		t.Errorf("out = %q, counts = %v", out, counts)
	}
}

// The internal splicer's own edges, which no format writer can reach on
// purpose but which stand between a bug and a corrupted manifest.

func TestSplicerRefusesOverlappingPatches(t *testing.T) {
	path := seed(t, "notes.txt", "0123456789")
	sp, err := openSplicer(path)
	if err != nil {
		t.Fatal(err)
	}
	sp.replace(span{0, 5}, []byte("A"))
	sp.replace(span{3, 8}, []byte("B"))
	err = sp.commit(nil)
	if !errors.Is(err, errOverlappingPatches) {
		t.Fatalf("got %v, want errOverlappingPatches", err)
	}
	if got := read(t, path); got != "0123456789" {
		t.Errorf("a refused commit wrote %q", got)
	}
}

func TestSplicerRefusesSpansOnARegeneratedFile(t *testing.T) {
	path := seed(t, "notes.txt", "0123456789")
	sp, err := openSplicer(path)
	if err != nil {
		t.Fatal(err)
	}
	sp.setWhole([]byte("whole"))
	sp.replace(span{0, 1}, []byte("X"))
	if err := sp.commit(nil); !errors.Is(err, errMixedEdits) {
		t.Fatalf("got %v, want errMixedEdits", err)
	}
	if got := read(t, path); got != "0123456789" {
		t.Errorf("a refused commit wrote %q", got)
	}
}

func TestSplicerAppliesPatchesInOffsetOrder(t *testing.T) {
	// Queued out of order and of different widths, so a splice that used stale
	// offsets would land visibly wrong.
	path := seed(t, "notes.txt", "aaa bbb ccc")
	sp, err := openSplicer(path)
	if err != nil {
		t.Fatal(err)
	}
	sp.replace(span{8, 11}, []byte("CCCCC"))
	sp.replace(span{0, 3}, []byte("A"))
	sp.replace(span{4, 7}, []byte("BB"))
	if err := sp.commit(nil); err != nil {
		t.Fatal(err)
	}
	if got := read(t, path); got != "A BB CCCCC" {
		t.Errorf("file = %q, want %q", got, "A BB CCCCC")
	}
}

func TestSplicerLeavesTheFileAloneWhenVerifyFails(t *testing.T) {
	path := seed(t, "notes.txt", "before")
	sp, err := openSplicer(path)
	if err != nil {
		t.Fatal(err)
	}
	sp.setWhole([]byte("after"))
	boom := errors.New("boom")
	if err := sp.commit(func([]byte) error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("got %v, want the verifier's error", err)
	}
	if got := read(t, path); got != "before" {
		t.Errorf("file = %q, want it untouched", got)
	}
}

func TestSplicerAtAndTextReadTheFileAsFound(t *testing.T) {
	path := seed(t, "notes.txt", "hello world")
	sp, err := openSplicer(path)
	if err != nil {
		t.Fatal(err)
	}
	sp.replace(span{0, 5}, []byte("bye"))
	// The queued patch is not applied to the working copy, so a locator may
	// keep using offsets into it right up to the commit.
	if got := string(sp.at(span{6, 11})); got != "world" {
		t.Errorf("at = %q", got)
	}
	if sp.text() != "hello world" {
		t.Errorf("text = %q", sp.text())
	}
}

func TestOpenSplicerRefusesAnOversizedFile(t *testing.T) {
	path := seed(t, "huge.txt", "x")
	if err := os.Truncate(path, maxManifestBytes+1); err != nil {
		t.Skipf("cannot make a sparse file here: %v", err)
	}
	if _, err := openSplicer(path); !errors.Is(err, ErrManifestTooLarge) {
		t.Fatalf("got %v, want ErrManifestTooLarge", err)
	}
}
