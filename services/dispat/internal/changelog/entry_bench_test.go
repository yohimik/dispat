package changelog

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/yohimik/dispat/pkg/ccme"
)

// largeChangelog is about 2 MB of released entries for core@1.0.x.
func largeChangelog() []byte {
	var history strings.Builder
	for i := 0; history.Len() < 2<<20; i++ {
		fmt.Fprintf(&history, "## core@1.0.%d (2026-01-01)\n\n### Fixes\n\n- fix number %d with a body line\n\n", i, i)
	}
	return []byte(history.String())
}

// TestIsEntryPresentDoesNotCopyTheFile pins the bytes the idempotence check
// allocates on a 2 MB changelog: the marker, never a copy of the file or a
// slice of its lines.
func TestIsEntryPresentDoesNotCopyTheFile(t *testing.T) {
	content := largeChangelog()
	const calls = 20
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for range calls {
		if IsEntryPresent(content, "core@2.0.0") {
			t.Fatal("the tag is absent")
		}
	}
	runtime.ReadMemStats(&after)
	if perCall := (after.TotalAlloc - before.TotalAlloc) / calls; perCall > 1<<10 {
		t.Fatalf("IsEntryPresent allocated %d bytes per call on a %d-byte file", perCall, len(content))
	}
}

// BenchmarkIsEntryPresentAbsent measures the idempotence check on a 2 MB
// changelog that does not carry the tag, which is every fresh write: the
// whole file is scanned.
func BenchmarkIsEntryPresentAbsent(b *testing.B) {
	content := largeChangelog()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if IsEntryPresent(content, "core@2.0.0") {
			b.Fatal("the tag is absent")
		}
	}
}

// BenchmarkRecordWritesAboveALargeChangelog measures prepending one entry to
// a 2 MB changelog: one read, the idempotence check, and the whole-file
// rewrite.
func BenchmarkRecordWritesAboveALargeChangelog(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "CHANGELOG.md")
	content := append([]byte("# Changelog\n\n"), largeChangelog()...)
	rel := testRelease(dir, ccme.Version{Major: 2})
	w := &FileWriter{Now: func() time.Time { return testDate }}
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		if err := os.WriteFile(path, content, 0o644); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		if err := w.Record(ctx, rel); err != nil {
			b.Fatal(err)
		}
	}
}
