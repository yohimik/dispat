// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package publicapi_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/yohimik/dispat/pkg/writer"
)

// TOML dotted keys are a third spelling of a dependency detail table. The
// scanner recognizes one as a dependency, so the public writer must be able
// to update the same declaration without touching sibling fields or confusing
// a quoted literal dot with a dotted key separator.
func TestPublicAPIWriterUpdatesCargoDottedDependencyVersion(t *testing.T) {
	for _, tc := range []struct {
		name, body, dependency, want string
	}{
		{"bare key", `[package]
name = "app"
version = "1.0.0"

[dependencies]
core.version = "1.0"
core.features = ["std"]
`, "core", `core.version = "2.0"`},
		{"quoted alias and rename", `[package]
name = "app"
version = "1.0.0"

[dependencies]
"renamed".package = "core"
"renamed".version = "1.0"
"renamed".features = ["std"]
`, "core", `"renamed".version = "2.0"`},
		{"literal alias and rename", `[package]
name = "app"
version = "1.0.0"

[dependencies]
'renamed'.package = 'core'
'renamed'.version = '1.0'
'renamed'.features = ["std"]
`, "core", `'renamed'.version = '2.0'`},
		{"escaped alias and spaced separators", `[package]
name = "app"
version = "1.0.0"

[dependencies]
"co\u0072e" . version = "1.0"
"co\u0072e" . features = ["std"]
`, "core", `"co\u0072e" . version = "2.0"`},
		{"quoted parent table", `["package"]
name = "app"
version = "1.0.0"

["dependencies"]
core.version = "1.0"
core.features = ["std"]
`, "core", `core.version = "2.0"`},
		{"literal parent table", `['package']
name = 'app'
version = '1.0.0'

['dependencies']
core.version = '1.0'
core.features = ["std"]
`, "core", `core.version = '2.0'`},
		{"quoted alias subtable", `[package]
name = "app"
version = "1.0.0"

[dependencies."renamed"]
package = "core"
version = "1.0"
features = ["std"]
`, "core", `version = "2.0"`},
		{"literal dot is one key", `[package]
name = "app"
version = "1.0.0"

[dependencies]
"core.version" = "1.0"
`, "core.version", `"core.version" = "2.0"`},
		{"literal dot with inline version", `[package]
name = "app"
version = "1.0.0"

[dependencies]
"core.version" = { version = "1.0", features = ["std"] }
`, "core.version", `"core.version" = { version = "2.0", features = ["std"] }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeFile(t, t.TempDir(), "Cargo.toml", tc.body)
			manifest := scanOne(t, filepath.Dir(path))
			if len(manifest.Deps) != 1 || manifest.Deps[0].Name != tc.dependency || manifest.Deps[0].Range != "1.0" {
				t.Fatalf("scanner did not recognize Cargo dependency: %+v", manifest.Deps)
			}
			ownVersion := ""
			if tc.name == "quoted parent table" || tc.name == "literal parent table" {
				ownVersion = "2.0.0"
			}
			result, err := writer.Rewrite(path, ownVersion, []writer.Edit{{Name: tc.dependency, Range: "2.0"}})
			if err != nil || len(result.Applied) != 1 {
				t.Fatalf("rewrite Cargo dependency = %+v, %v", result, err)
			}
			if ownVersion != "" && !result.VersionWritten {
				t.Fatalf("rewrite did not locate version in quoted package table: %+v", result)
			}
			out := readFile(t, path)
			if !strings.Contains(out, tc.want) || (tc.name != "literal dot is one key" && !strings.Contains(out, `features = ["std"]`)) {
				t.Fatalf("dependency rewrite changed the wrong bytes:\n%s", out)
			}
			if ownVersion != "" && !strings.Contains(out, "version = \"2.0.0\"") && !strings.Contains(out, "version = '2.0.0'") {
				t.Fatalf("package version was not updated in quoted table:\n%s", out)
			}
		})
	}
	t.Run("workspace inheritance stays inherited", func(t *testing.T) {
		const body = `[package]
name = "app"
version = "1.0.0"

[workspace.dependencies]
core = "3.0"

[dependencies]
core.workspace = true
`
		path := writeFile(t, t.TempDir(), "Cargo.toml", body)
		manifest := scanOne(t, filepath.Dir(path))
		if len(manifest.Deps) != 1 || manifest.Deps[0].Name != "core" || manifest.Deps[0].Range != "" {
			t.Fatalf("scanner did not recognize inherited dependency: %+v", manifest.Deps)
		}
		result, err := writer.Rewrite(path, "", []writer.Edit{{Name: "core", Range: "2.0"}})
		if err != nil || len(result.Skipped) != 1 || len(result.Applied) != 0 {
			t.Fatalf("rewrite inherited dependency = %+v, %v", result, err)
		}
		if got := readFile(t, path); got != body {
			t.Fatalf("inherited dependency rewrite changed the file:\n%s", got)
		}
	})
	t.Run("literal dot and dotted path coexist", func(t *testing.T) {
		const body = `[package]
name = "app"
version = "1.0.0"

[dependencies]
core.version = "1.0"
"core.version" = "4.0"
`
		path := writeFile(t, t.TempDir(), "Cargo.toml", body)
		manifest := scanOne(t, filepath.Dir(path))
		if len(manifest.Deps) != 2 {
			t.Fatalf("scanner dependencies = %+v", manifest.Deps)
		}
		result, err := writer.Rewrite(path, "", []writer.Edit{{Name: "core.version", Range: "5.0"}})
		if err != nil || len(result.Applied) != 1 {
			t.Fatalf("rewrite literal dotted name = %+v, %v", result, err)
		}
		out := readFile(t, path)
		if !strings.Contains(out, `core.version = "1.0"`) || !strings.Contains(out, `"core.version" = "5.0"`) {
			t.Fatalf("literal key and dotted path were conflated:\n%s", out)
		}
	})
	t.Run("literal dot subtable and ordinary subtable coexist", func(t *testing.T) {
		const body = `[package]
name = "app"
version = "1.0.0"

[dependencies.core]
version = "1.0"

[dependencies."core.version"]
version = "4.0"
`
		path := writeFile(t, t.TempDir(), "Cargo.toml", body)
		manifest := scanOne(t, filepath.Dir(path))
		if len(manifest.Deps) != 2 {
			t.Fatalf("scanner dependencies = %+v", manifest.Deps)
		}
		result, err := writer.Rewrite(path, "", []writer.Edit{{Name: "core.version", Range: "5.0"}})
		if err != nil || len(result.Applied) != 1 {
			t.Fatalf("rewrite literal dotted subtable = %+v, %v", result, err)
		}
		out := readFile(t, path)
		if !strings.Contains(out, "[dependencies.core]\nversion = \"1.0\"") ||
			!strings.Contains(out, "[dependencies.\"core.version\"]\nversion = \"5.0\"") {
			t.Fatalf("literal subtable and dotted path were conflated:\n%s", out)
		}
	})
	t.Run("literal dotted table and dependency subtable coexist", func(t *testing.T) {
		const body = `[package]
name = "app"
version = "1.0.0"

["dependencies.core"]
version = "9.0"

[dependencies.core]
version = "1.0"
`
		path := writeFile(t, t.TempDir(), "Cargo.toml", body)
		manifest := scanOne(t, filepath.Dir(path))
		if len(manifest.Deps) != 1 || manifest.Deps[0].Name != "core" || manifest.Deps[0].Range != "1.0" {
			t.Fatalf("scanner dependencies = %+v", manifest.Deps)
		}
		result, err := writer.Rewrite(path, "", []writer.Edit{{Name: "core", Range: "2.0"}})
		if err != nil || len(result.Applied) != 1 {
			t.Fatalf("rewrite dependency subtable = %+v, %v", result, err)
		}
		out := readFile(t, path)
		if !strings.Contains(out, "[\"dependencies.core\"]\nversion = \"9.0\"") ||
			!strings.Contains(out, "[dependencies.core]\nversion = \"2.0\"") {
			t.Fatalf("literal table and dependency subtable were conflated:\n%s", out)
		}
	})
}

func TestPublicAPIWriterIgnoresTOMLStringsThatResembleCargoDeclarations(t *testing.T) {
	for _, tc := range []struct {
		name, readme, fake, real string
	}{
		{"multiline basic", `"""
[dependencies]
core = "9.0"
"""`, `core = "9.0"`, `core = "2.0"`},
		{"multiline literal", `'''
[dependencies]
core = '9.0'
'''`, `core = '9.0'`, `core = '2.0'`},
		{"escaped multiline delimiter", `"""
text \"""
[dependencies]
core = "9.0"
"""`, `core = "9.0"`, `core = "2.0"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := "[package]\nname = \"app\"\nversion = \"1.0.0\"\nreadme = " + tc.readme +
				"\n\n[dependencies]\n" + strings.Replace(tc.fake, "9.0", "1.0", 1) + "\n"
			path := writeFile(t, t.TempDir(), "Cargo.toml", body)
			manifest := scanOne(t, filepath.Dir(path))
			if len(manifest.Deps) != 1 || manifest.Deps[0].Name != "core" || manifest.Deps[0].Range != "1.0" {
				t.Fatalf("scanner dependencies = %+v", manifest.Deps)
			}
			result, err := writer.Rewrite(path, "", []writer.Edit{{Name: "core", Range: "2.0"}})
			if err != nil || len(result.Applied) != 1 {
				t.Fatalf("rewrite dependency = %+v, %v", result, err)
			}
			out := readFile(t, path)
			if !strings.Contains(out, tc.fake) || !strings.Contains(out, "[dependencies]\n"+tc.real) {
				t.Fatalf("rewriter selected text within the readme instead of the dependency:\n%s", out)
			}
		})
	}
	for _, tc := range []struct{ name, value string }{
		{"quoted feature", `features = ["x, version = '9.0'"], version = "1.0"`},
		{"escaped quote", `features = ["x, version = \"9.0\""], version = "1.0"`},
		{"nested inline version", `metadata = { version = "9.0" }, version = "1.0"`},
		{"multiline literal before version", `features = ["""text " , version = '9.0' """], version = "1.0"`},
		{"hash inside multiline literal", `features = ["""text # , version = '9.0' """], version = "1.0"`},
		{"multiline literal after version", `version = "1.0", features = ['''text, version = "9.0"''']`},
		{"multiple multiline literals", `features = ["""first, version = '9.0' """, '''second, version = "8.0"'''], version = "1.0"`},
		{"four and five quote closers", `features = ["""first"""", '''second'''''], version = "1.0"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := "[package]\nname = \"app\"\nversion = \"1.0.0\"\n\n[dependencies]\ncore = { " + tc.value + " }\n"
			path := writeFile(t, t.TempDir(), "Cargo.toml", body)
			manifest := scanOne(t, filepath.Dir(path))
			if len(manifest.Deps) != 1 || manifest.Deps[0].Name != "core" || manifest.Deps[0].Range != "1.0" {
				t.Fatalf("scanner dependencies = %+v", manifest.Deps)
			}
			result, err := writer.Rewrite(path, "", []writer.Edit{{Name: "core", Range: "2.0"}})
			if err != nil || len(result.Applied) != 1 {
				t.Fatalf("rewrite dependency = %+v, %v", result, err)
			}
			out := readFile(t, path)
			if !strings.Contains(out, strings.Replace(tc.value, `version = "1.0"`, `version = "2.0"`, 1)) {
				t.Fatalf("rewriter selected a quoted or nested version instead of the dependency version:\n%s", out)
			}
		})
	}
}

func TestPublicAPIWriterIgnoresMultilineTOMLDecoysAcrossFormats(t *testing.T) {
	t.Run("Python package version", func(t *testing.T) {
		const body = `[tool.meta]
readme = """
[project]
version = "9.0.0"
"""

[project]
name = "app"
version = "1.0.0"
dependencies = ["core==1.0"]
`
		path := writeFile(t, t.TempDir(), "pyproject.toml", body)
		manifest := scanOne(t, filepath.Dir(path))
		if manifest.Version != "1.0.0" {
			t.Fatalf("scanner package version = %q", manifest.Version)
		}
		result, err := writer.Rewrite(path, "2.0.0", nil)
		if err != nil || !result.VersionWritten {
			t.Fatalf("rewrite package version = %+v, %v", result, err)
		}
		out := readFile(t, path)
		if !strings.Contains(out, "[project]\nversion = \"9.0.0\"\n\"\"\"") ||
			!strings.Contains(out, "[project]\nname = \"app\"\nversion = \"2.0.0\"") {
			t.Fatalf("Python version rewrite selected multiline readme text:\n%s", out)
		}
	})
	t.Run("Gradle version catalog", func(t *testing.T) {
		const body = `[metadata]
notes = '''
[versions]
core = "9.0"
'''

[versions]
core = "1.0"

[libraries]
core = { module = "acme:core", version.ref = "core" }
`
		path := writeFile(t, t.TempDir(), "libs.versions.toml", body)
		manifest := scanOne(t, filepath.Dir(path))
		if len(manifest.Deps) != 1 || manifest.Deps[0].Name != "acme:core" || manifest.Deps[0].Range != "1.0" {
			t.Fatalf("scanner catalog dependencies = %+v", manifest.Deps)
		}
		result, err := writer.Rewrite(path, "", []writer.Edit{{Name: "acme:core", Range: "2.0"}})
		if err != nil || len(result.Applied) != 1 {
			t.Fatalf("rewrite catalog version = %+v, %v", result, err)
		}
		out := readFile(t, path)
		if !strings.Contains(out, "[versions]\ncore = \"9.0\"\n'''") ||
			!strings.Contains(out, "[versions]\ncore = \"2.0\"") {
			t.Fatalf("catalog rewrite selected multiline notes text:\n%s", out)
		}
	})
	t.Run("Cargo patch insertion", func(t *testing.T) {
		const body = `[package]
name = "app"
version = "1.0.0"
readme = """
[patch.crates-io]
core = { path = "../wrong" }
"""

[dependencies]
core = "1.0"
`
		path := writeFile(t, t.TempDir(), "Cargo.toml", body)
		result, err := writer.Relink(path, []writer.Link{{Name: "core", Path: "../core"}})
		if err != nil || len(result.Applied) != 1 {
			t.Fatalf("insert Cargo patch = %+v, %v", result, err)
		}
		out := readFile(t, path)
		if !strings.Contains(out, "[patch.crates-io]\ncore = { path = \"../wrong\" }\n\"\"\"") ||
			!strings.Contains(out, "[patch.crates-io]\ncore = { path = \"../core\" }") {
			t.Fatalf("patch insertion selected multiline readme text:\n%s", out)
		}
	})
}
