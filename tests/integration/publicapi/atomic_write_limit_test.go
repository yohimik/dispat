//go:build linux || darwin

// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package publicapi_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/yohimik/dispat/pkg/config"
	"github.com/yohimik/dispat/pkg/writer"
)

// TestPublicAPIAtomicEditsSurvivePartialDiskWrites models a filesystem quota
// after a temporary file was created. A partial write must leave the original
// bytes usable and remove its incomplete temporary file.
func TestPublicAPIAtomicEditsSurvivePartialDiskWrites(t *testing.T) {
	for _, kind := range []string{"manifest", "configuration"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "package.json")
			body := `{"name":"app","version":"1.0.0","description":"` + strings.Repeat("x", 4096) + `"}`
			var write func() error
			if kind == "manifest" {
				write = func() error { _, err := writer.Rewrite(path, "1.0.1", nil); return err }
			} else {
				path = filepath.Join(dir, "app.json")
				body = `{"name":"app"}`
				write = func() error {
					return config.ApplyEdits(t.Context(), path, []config.Edit{{KeyPath: []string{"name"}, Value: strings.Repeat("x", 4096)}})
				}
			}
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			err := writeWithFileLimit(t, write)
			if !errors.Is(err, syscall.EFBIG) {
				t.Fatalf("write = %v; want the filesystem limit", err)
			}
			actual, err := os.ReadFile(path)
			if err != nil || string(actual) != body {
				t.Fatalf("partial write changed original: %q, %v", actual, err)
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".dispat-write-") || strings.Contains(entry.Name(), ".tmp-") {
					t.Fatalf("partial write left %s", entry.Name())
				}
			}
			if kind == "configuration" {
				backup, err := os.ReadFile(path + config.BackupSuffix)
				if err != nil || string(backup) != body {
					t.Fatalf("backup = %q, %v; want intact prior configuration", backup, err)
				}
			}
		})
	}
}

// This test is deliberately serial. Restore the process limit before any
// assertion, cleanup, or coverage flush can write another file.
func writeWithFileLimit(t *testing.T, write func() error) error {
	t.Helper()
	var original syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_FSIZE, &original); err != nil {
		t.Fatal(err)
	}
	bounded := original
	bounded.Cur = 1024
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &bounded); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &original); err != nil {
			t.Fatal(err)
		}
	}()
	return write()
}
