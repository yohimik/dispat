//go:build !race

package scanner

// The race detector adds its own allocations, so these budgets only hold in a
// normal build, hence the build tag.

import (
	"os"
	"path/filepath"
	"testing"
)

var allocBytes []byte

// TestAllocationBudget pins the allocation counts the package documents. The
// budgets carry one allocation of headroom so a toolchain change does not fail
// the build for a rounding difference; a real regression is worth many.
func TestAllocationBudget(t *testing.T) {
	// Reading a megabyte manifest: the open file, its stat and one buffer of
	// the file's size, where growing a buffer from 512 bytes takes two
	// dozen more.
	path := filepath.Join(t.TempDir(), "project.pbxproj")
	if err := os.WriteFile(path, lineScannedCases[0].build(1<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	got := testing.AllocsPerRun(20, func() {
		var err error
		if allocBytes, err = readManifest(path); err != nil {
			t.Fatal(err)
		}
	})
	if want := 9.0; got > want {
		t.Errorf("readManifest of a 1 MiB file: %v allocations, want at most %v", got, want)
	}
}
