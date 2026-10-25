package writer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// replaceBenchExpansions is how many rule expansions reach the file. Each
// becomes a replacement followed by its probe, the list the release Replacer
// hands the writer.
const replaceBenchExpansions = 20

// replaceBenchFile is a megabyte of text with every expansion's old text in it
// a few times, the file before a release reconciles it.
func replaceBenchFile() string {
	var b strings.Builder
	for i := 0; b.Len() < 1<<20; i++ {
		if i%1000 == 0 {
			fmt.Fprintf(&b, "depends on acme-dep%d:1.2.3\n", (i/1000)%replaceBenchExpansions)
			continue
		}
		fmt.Fprintf(&b, "line %d of a document that mentions no version\n", i)
	}
	return b.String()
}

func replaceBenchList() []Replacement {
	reps := make([]Replacement, 0, 2*replaceBenchExpansions)
	for i := 0; i < replaceBenchExpansions; i++ {
		rep := Replacement{
			Find:  fmt.Sprintf("acme-dep%d:1.2.3", i),
			Write: fmt.Sprintf("acme-dep%d:1.3.0", i),
		}
		reps = append(reps, rep, Replacement{Find: rep.Write, Write: rep.Write})
	}
	return reps
}

func BenchmarkReplace(b *testing.B) {
	before := replaceBenchFile()
	reps := replaceBenchList()
	after, _ := ReplaceBytes([]byte(before), reps)

	b.Run("applied", func(b *testing.B) {
		path := filepath.Join(b.TempDir(), "README.md")
		b.ReportAllocs()
		b.SetBytes(int64(len(before)))
		for i := 0; i < b.N; i++ {
			b.StopTimer()
			if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
				b.Fatal(err)
			}
			b.StartTimer()
			res, err := Replace(path, reps)
			if err != nil {
				b.Fatal(err)
			}
			if len(res.Applied) != replaceBenchExpansions {
				b.Fatalf("applied %d replacements, want %d", len(res.Applied), replaceBenchExpansions)
			}
		}
	})
	b.Run("unchanged", func(b *testing.B) {
		path := filepath.Join(b.TempDir(), "README.md")
		if err := os.WriteFile(path, after, 0o644); err != nil {
			b.Fatal(err)
		}
		b.ReportAllocs()
		b.SetBytes(int64(len(after)))
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			res, err := Replace(path, reps)
			if err != nil {
				b.Fatal(err)
			}
			if len(res.Skipped) != replaceBenchExpansions {
				b.Fatalf("skipped %d replacements, want %d", len(res.Skipped), replaceBenchExpansions)
			}
		}
	})
}
