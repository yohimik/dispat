// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package publicapi_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/yohimik/dispat/pkg/scanner"
	"github.com/yohimik/dispat/pkg/writer"
)

func TestPublicAPIWriterRefusesAquaAliasesThatTheReaderCanFollow(t *testing.T) {
	body := `shared: &shared
  name: cli/cli@v2.55.0
packages:
  - *shared
`
	path := writeFile(t, t.TempDir(), "aqua.yaml", body)
	manifests, err := scanner.ScanRoot(t.Context(), filepath.Dir(path))
	if err != nil {
		t.Fatalf("scan aliased Aqua manifest: %v", err)
	}
	if len(manifests) != 1 {
		t.Fatalf("scan aliased Aqua manifest count = %d", len(manifests))
	}
	m := manifests[0]
	if len(m.Deps) != 1 || m.Deps[0].Name != "cli/cli" {
		t.Fatalf("scanner did not follow the safe read alias: %#v", m.Deps)
	}
	_, err = writer.Rewrite(path, "", []writer.Edit{{Name: "cli/cli", Range: "v2.56.0"}})
	if err == nil || !strings.Contains(err.Error(), "anchors and aliases") {
		t.Fatalf("writer accepted aliased package source: %v", err)
	}
	if got := readFile(t, path); got != body {
		t.Fatalf("refusal changed Aqua bytes:\n%s", got)
	}
}

func TestPublicAPIWriterLeavesMetadataOnlyPlistsByteExact(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{name: "empty dictionary", body: `<?xml version="1.0"?><plist><dict></dict></plist>`},
		{name: "metadata and unrelated value", body: `<?xml version="1.0"?><plist><dict><!-- keep --><array/><key>CFBundleVersion</key><integer>7</integer><?build untouched?></dict></plist>`},
		{name: "no dictionary", body: `<?xml version="1.0"?><plist><array><string>metadata</string></array></plist>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeFile(t, t.TempDir(), "Info.plist", tc.body)
			res, err := writer.Rewrite(path, "2.0.0", []writer.Edit{{Name: "unused", Range: "1"}})
			if err != nil {
				t.Fatalf("rewrite metadata-only plist: %v", err)
			}
			if res.VersionWritten || len(res.Missing) != 1 {
				t.Fatalf("unexpected result: %#v", res)
			}
			if got := readFile(t, path); got != tc.body {
				t.Fatalf("metadata-only plist changed:\n%s", got)
			}
		})
	}
}

func TestPublicAPIWriterHandlesEscapedPythonArraysAndLiteralKeysIdempotently(t *testing.T) {
	body := `[project]
name = "app"
version = {dynamic = true}
dependencies = [
  "marker; implementation_name == \"x[y]\"",
  "core>=1.0",
]

[tool.poetry.dependencies]
'literal.key' = "^1.0"
"keep.key" = "^7.0"
`
	path := writeFile(t, t.TempDir(), "pyproject.toml", body)
	edits := []writer.Edit{
		{Name: "core", Kind: "dependencies", Range: ">=2.0"},
		{Name: "literal.key", Range: "^2.0"},
	}
	first, err := writer.Rewrite(path, "9.9.9", edits)
	if err != nil {
		t.Fatalf("first rewrite: %v", err)
	}
	if first.VersionWritten || len(first.Applied) != 2 {
		t.Fatalf("unexpected first result: %#v", first)
	}
	out := readFile(t, path)
	for _, want := range []string{
		`"marker; implementation_name == \"x[y]\""`,
		`"core>=2.0"`,
		`'literal.key' = "^2.0"`,
		`"keep.key" = "^7.0"`,
		`version = {dynamic = true}`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rewritten pyproject lacks %q:\n%s", want, out)
		}
	}
	second, err := writer.Rewrite(path, "9.9.9", edits)
	if err != nil {
		t.Fatalf("second rewrite: %v", err)
	}
	if second.VersionWritten || len(second.Applied) != 0 || len(second.Missing) != 0 || len(second.Skipped) != 0 {
		t.Fatalf("idempotent result = %#v", second)
	}
	if got := readFile(t, path); got != out {
		t.Fatal("idempotent rewrite changed pyproject bytes")
	}
}
