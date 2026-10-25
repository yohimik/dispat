//go:build !race

package writer

// The race detector adds its own allocations, so these budgets only hold in a
// normal build, hence the build tag.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var allocBytes []byte

// TestAllocationBudget pins the allocation counts the package documents. The
// budgets carry one allocation of headroom so a toolchain change does not fail
// the build for a rounding difference; a real regression is worth many.
func TestAllocationBudget(t *testing.T) {
	// Twenty replacements that all apply, each Write as long as its Find: the
	// counts slice and the two buffers the run alternates between.
	var (
		text strings.Builder
		reps []Replacement
	)
	for i := range 20 {
		find, write := "acme-dep"+string(rune('a'+i))+":1.2.3", "acme-dep"+string(rune('a'+i))+":1.3.0"
		text.WriteString(strings.Repeat("filler text ", 200) + find + "\n")
		reps = append(reps, Replacement{Find: find, Write: write})
	}
	data := []byte(text.String())
	got := testing.AllocsPerRun(20, func() { allocBytes, _ = ReplaceBytes(data, reps) })
	if want := 4.0; got > want {
		t.Errorf("ReplaceBytes over twenty applied replacements: %v allocations, want at most %v", got, want)
	}

	// Opening a megabyte file for a rewrite: the open file, its stat, the
	// splicer and one buffer of the file's size, where growing a buffer from
	// 512 bytes takes two dozen more.
	path := filepath.Join(t.TempDir(), "README.md")
	if err := os.WriteFile(path, []byte(strings.Repeat("filler text\n", 1<<20/12)), 0o644); err != nil {
		t.Fatal(err)
	}
	got = testing.AllocsPerRun(20, func() {
		sp, err := openSplicer(path)
		if err != nil {
			t.Fatal(err)
		}
		allocBytes = sp.bytes()
	})
	if want := 10.0; got > want {
		t.Errorf("openSplicer of a 1 MiB file: %v allocations, want at most %v", got, want)
	}
}
