package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"

	lib "github.com/yohimik/dispat/pkg/config"
)

// fileReadCounter counts the loader's file-read events, which is how a test
// sees how many times a command reads its configuration files.
type fileReadCounter struct{ reads int }

func (c *fileReadCounter) Write(p []byte) (int, error) {
	if bytes.Contains(p, []byte(lib.EventFileRead)) {
		c.reads++
	}
	return len(p), nil
}

// countFileReads runs load with the loader's events routed into a counter,
// and returns how many files it read.
func countFileReads(tb testing.TB, load func() error) int {
	tb.Helper()
	counter := &fileReadCounter{}
	UseLogger(zerolog.New(counter).Level(zerolog.TraceLevel))
	defer bootLog.Store(nil)
	if err := load(); err != nil {
		tb.Fatal(err)
	}
	return counter.reads
}

// writeComposedRepo lays out a repository whose root configuration pulls its
// scripts in through a $ref, and returns its root.
func writeComposedRepo(tb testing.TB) string {
	tb.Helper()
	root := tb.TempDir()
	cfg, err := json.Marshal(validConfig())
	if err != nil {
		tb.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(cfg, &raw); err != nil {
		tb.Fatal(err)
	}
	scripts, err := json.Marshal(raw["scripts"])
	if err != nil {
		tb.Fatal(err)
	}
	raw["scripts"] = map[string]any{"$ref": "./scripts.json"}
	composed, err := json.Marshal(raw)
	if err != nil {
		tb.Fatal(err)
	}
	for name, body := range map[string][]byte{"dispat.json": composed, "scripts.json": scripts} {
		if err := os.WriteFile(filepath.Join(root, name), body, 0o644); err != nil {
			tb.Fatal(err)
		}
	}
	for _, dir := range []string{"packages/libs/core", "packages/apps/app"} {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(dir)), 0o755); err != nil {
			tb.Fatal(err)
		}
	}
	return root
}

// resolveAndLoadControl is what the CLI does with --root and no --config:
// find the configuration file, then load it as the control file.
func resolveAndLoadControl(root string) (*File, error) {
	path, _, err := ResolveFile(root, defaultFileNames[0], false)
	if err != nil {
		return nil, err
	}
	return LoadControl(path, nil, false)
}

// BenchmarkResolveAndLoadControl reports, beside the cost, how many files one
// resolve-and-load reads: the root configuration and the fragment it refers
// to are two files.
func BenchmarkResolveAndLoadControl(b *testing.B) {
	root := writeComposedRepo(b)
	load := func() error {
		_, err := resolveAndLoadControl(root)
		return err
	}
	reads := countFileReads(b, load)
	b.ReportAllocs()
	for b.Loop() {
		if err := load(); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(reads), "files-read/op")
}
