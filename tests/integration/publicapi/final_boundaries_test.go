// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package publicapi_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/yohimik/dispat/pkg/config"
	"github.com/yohimik/dispat/pkg/scanner"
	"github.com/yohimik/dispat/pkg/writer"
)

// TestPublicAPIWriterPreservesUnaddressedPubspecYAMLSyntax verifies that valid
// YAML constructs outside the simple mapping keys the writer targets are
// passed over byte-for-byte while an ordinary dependency beside them is edited.
func TestPublicAPIWriterPreservesUnaddressedPubspecYAMLSyntax(t *testing.T) {
	const src = `--- # document marker
name: acme
dependencies:
  ? "metadata key"
  "display name": ^1.0.0
  core: ^1.0.0
`
	path := writeFile(t, t.TempDir(), "pubspec.yaml", src)
	res, err := writer.Rewrite(path, "", []writer.Edit{{Name: "core", Range: "^2.0.0"}})
	if err != nil || len(res.Applied) != 1 {
		t.Fatalf("Rewrite = %+v, %v", res, err)
	}
	out := readFile(t, path)
	for _, preserved := range []string{"--- # document marker", `  ? "metadata key"`, `  "display name": ^1.0.0`} {
		if !strings.Contains(out, preserved) {
			t.Errorf("unaddressed YAML syntax %q was changed:\n%s", preserved, out)
		}
	}
	if !strings.Contains(out, "  core: ^2.0.0") {
		t.Errorf("the ordinary dependency was not rewritten:\n%s", out)
	}
}

// TestPublicAPIWriterDropsAMiddlePubspecOverrideBlock removes the only local
// override when the block has blank padding and another top-level section
// follows it. The empty block and its padding go; the following section stays.
func TestPublicAPIWriterDropsAMiddlePubspecOverrideBlock(t *testing.T) {
	const src = `name: acme
dependencies:
  core: ^1.0.0

dependency_overrides:

  core:
    path: ../core

flutter:
  uses-material-design: true
`
	path := writeFile(t, t.TempDir(), "pubspec.yaml", src)
	res, err := writer.DropLinks(path)
	if err != nil || len(res.Applied) != 1 {
		t.Fatalf("DropLinks = %+v, %v", res, err)
	}
	out := readFile(t, path)
	if strings.Contains(out, "dependency_overrides:") || strings.Contains(out, "path: ../core") {
		t.Fatalf("the emptied override block survived:\n%s", out)
	}
	if !strings.Contains(out, "flutter:\n  uses-material-design: true") {
		t.Fatalf("the following top-level section was disturbed:\n%s", out)
	}
}

// TestPublicAPIWriterRefusesAnIncompatibleGoModuleMajorBeforeWriting covers
// the semantic validation performed after x/mod formats a requested change.
// A v2 requirement on a module path with no /v2 suffix must not land merely
// because it was syntactically spliceable.
func TestPublicAPIWriterRefusesAnIncompatibleGoModuleMajorBeforeWriting(t *testing.T) {
	const src = `module example.com/acme/app

go 1.26

require example.com/acme/core v1.0.0
`
	path := writeFile(t, t.TempDir(), "go.mod", src)
	res, err := writer.Rewrite(path, "", []writer.Edit{{Name: "example.com/acme/core", Range: "v2.0.0"}})
	if err == nil || !strings.Contains(err.Error(), "should be v0 or v1") {
		t.Fatalf("Rewrite = %+v, %v; want the incompatible major refused", res, err)
	}
	if got := readFile(t, path); got != src {
		t.Fatalf("a refused module requirement changed go.mod:\n%s", got)
	}
}

// TestPublicAPIConfigRefusesAnUnencodableYAMLValueBeforeWriting exercises the
// public edit boundary with a Go value YAML cannot represent. Rendering fails
// before Commit, preserving both the configuration and the absence of a backup.
func TestPublicAPIConfigRefusesAnUnencodableYAMLValueBeforeWriting(t *testing.T) {
	const src = "name: acme\nvalue: old\n"
	path := writeFile(t, t.TempDir(), "dispat.yaml", src)
	err := config.ApplyEdits(context.Background(), path, []config.Edit{
		{KeyPath: []string{"name"}, Value: "renamed"},
		{KeyPath: []string{"value"}, Value: make(chan int)},
	})
	if err == nil || !strings.Contains(err.Error(), "cannot marshal type") {
		t.Fatalf("ApplyEdits = %v; want an unsupported YAML value refusal", err)
	}
	if got := readFile(t, path); got != src {
		t.Fatalf("a refused YAML edit changed the config:\n%s", got)
	}
	if _, statErr := os.Stat(path + config.BackupSuffix); !os.IsNotExist(statErr) {
		t.Fatalf("a refused YAML edit created a backup: %v", statErr)
	}
}

// TestPublicAPIScannerDoesNotTreatRubyOptionSuffixesAsLocalPaths proves the
// token boundary around Ruby hash options. A plugin-specific `subpath:` option
// must not be shortened to the standard `path:` local-dependency option.
func TestPublicAPIScannerDoesNotTreatRubyOptionSuffixesAsLocalPaths(t *testing.T) {
	m := scanFixture(t, "Gemfile", `source "https://rubygems.org"
gem "acme-core", "~> 1.0", subpath: "../not-a-local-link"
`)
	assertDeps(t, m, "dependencies|acme-core|~> 1.0|")
}

// TestPublicAPIScannerReportsAnEmptyAquaDocument treats a conventional Aqua
// file as a manifest even when it is empty, and reports its missing mapping
// rather than silently turning it into an empty dependency inventory.
func TestPublicAPIScannerReportsAnEmptyAquaDocument(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "aqua.yaml", "")
	mans, err := scanner.ScanRoot(t.Context(), dir)
	if err == nil || !strings.Contains(err.Error(), "document must be a mapping") {
		t.Fatalf("ScanRoot = %+v, %v; want the empty Aqua document reported", mans, err)
	}
	if len(mans) != 0 {
		t.Fatalf("an empty Aqua document became a manifest: %+v", mans)
	}
}
