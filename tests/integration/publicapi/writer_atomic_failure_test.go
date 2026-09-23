// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package publicapi_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yohimik/dispat/pkg/writer"
)

// TestPublicAPIWriterKeepsOriginalBytesWhenItsDirectoryCannotBeWrittenTo
// exercises a real filesystem failure after each format has parsed its input
// and prepared a change. A read-only manifest in a writable directory is
// replaceable by rename; the containing directory itself must refuse the
// temporary file for this to test the transaction boundary.
func TestPublicAPIWriterKeepsOriginalBytesWhenItsDirectoryCannotBeWrittenTo(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write through directory permission bits")
	}
	for _, tc := range []struct {
		name, file, body string
		write            func(string) error
	}{
		{"JSON dependency and own version", "package.json", `{"name":"app","version":"1.0.0","dependencies":{"core":"^1.0.0"}}`, func(path string) error {
			_, err := writer.Rewrite(path, "2.0.0", []writer.Edit{{Name: "core", Range: "^2.0.0"}})
			return err
		}},
		{"Go module requirement", "go.mod", "module example.com/app\n\ngo 1.26\n\nrequire example.com/core v1.0.0\n", func(path string) error {
			_, err := writer.Rewrite(path, "", []writer.Edit{{Name: "example.com/core", Range: "v1.1.0"}})
			return err
		}},
		{"Aqua package pin", "aqua.yaml", "packages:\n  - name: cli/cli@v1.0.0\n", func(path string) error {
			_, err := writer.Rewrite(path, "", []writer.Edit{{Name: "cli/cli", Range: "v2.0.0"}})
			return err
		}},
		{"Gradle catalog version", "libs.versions.toml", "[versions]\ncore = \"1.0.0\"\n[libraries]\ncore = { module = \"acme:core\", version.ref = \"core\" }\n", func(path string) error {
			_, err := writer.Rewrite(path, "", []writer.Edit{{Name: "acme:core", Range: "2.0.0"}})
			return err
		}},
		{"pubspec version", "pubspec.yaml", "name: app\nversion: 1.0.0+7\n", func(path string) error {
			_, err := writer.Rewrite(path, "2.0.0", nil)
			return err
		}},
		{"Unity project version", "ProjectSettings/ProjectSettings.asset", "PlayerSettings:\n  bundleVersion: 1.0.0\n", func(path string) error {
			_, err := writer.Rewrite(path, "2.0.0", nil)
			return err
		}},
		{"Android build counter", "AndroidManifest.xml", `<manifest xmlns:android="http://schemas.android.com/apk/res/android" android:versionCode="7" />`, func(path string) error {
			_, err := writer.SetBuild(path, "8")
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, tc.file)
			parent := filepath.Dir(path)
			if err := os.MkdirAll(parent, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(tc.body), 0o640); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(parent, 0o500); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })
			if err := tc.write(path); !errors.Is(err, os.ErrPermission) {
				t.Fatalf("rewrite error = %v, want permission failure creating its temporary file", err)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.body {
				t.Fatalf("failed write changed manifest bytes: %q", got)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0o640 {
				t.Fatalf("failed write changed manifest mode to %o", info.Mode().Perm())
			}
			entries, err := os.ReadDir(parent)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".dispat-write-") {
					t.Fatalf("failed write left temporary file %s", entry.Name())
				}
			}
		})
	}
}
