// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Coverage scenarios for the shapes a `configs` declaration is written in.
// The list of imported source configurations decides which repositories
// participate, and it is read before the control configuration is decoded, so
// every spelling the ordinary config reader accepts has to reach the same
// list of files: written out, split from one value, or composed through `$ref`
// fragments that each contribute part of it.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// covPolyrepoImportFleet links two sources, each carrying its own
// configuration, and returns the control repository. Nothing declares the
// imports yet: that is what each scenario writes.
func covPolyrepoImportFleet(t *testing.T) *harness.Repo {
	t.Helper()
	control := harness.New(t)
	for _, pkg := range []string{"one", "two"} {
		source := harness.New(t)
		source.SeedPackage("packages", pkg)
		cfg := covPolyrepoFile()
		cfg.Polyrepo = false
		cfg.Spaces = covPolyrepoSpaces(map[string]string{pkg + "s": "packages"})
		source.WriteConfigModel(cfg)
		source.Commit("feat(" + pkg + "): bootstrap " + pkg)
		addPolyrepoSource(t, control, pkg+"-source", "sources/"+pkg, source)
	}
	return control
}

// covPolyrepoImported reads back which packages a composed run found.
func covPolyrepoImported(res harness.RunResult) map[string]bool {
	found := map[string]bool{}
	for _, e := range res.Events {
		if pkg := e.Package(); pkg != "" {
			found[pkg] = true
		}
	}
	return found
}

// TestCovPolyrepoImportListComposesFromEverySpelling: the same two imports
// reach the same two repositories whether they are written as a list, as one
// value carrying both, or assembled from `$ref` fragments — including a
// fragment named by an absolute path, which a generated configuration is
// entitled to produce. A fragment's own paths are read relative to the
// fragment, which is the whole reason the list is resolved before decoding.
func TestCovPolyrepoImportListComposesFromEverySpelling(t *testing.T) {
	t.Run("a list written out", func(t *testing.T) {
		control := covPolyrepoImportFleet(t)
		control.WriteConfigRaw(map[string]any{
			"polyrepo":    true,
			"logFormat":   "json",
			"logLevel":    "info",
			"updateCheck": false,
			"github":      map[string]any{"enabled": false},
			"configs":     []string{"sources/one/dispat.json", "sources/two/dispat.json"},
		})
		control.Commit("chore: import both sources as a list")

		found := covPolyrepoImported(control.StatusOK())
		assert.True(t, found["one"])
		assert.True(t, found["two"])
	})

	t.Run("one value carrying both", func(t *testing.T) {
		control := covPolyrepoImportFleet(t)
		control.WriteConfigRaw(map[string]any{
			"polyrepo":    true,
			"logFormat":   "json",
			"logLevel":    "info",
			"updateCheck": false,
			"github":      map[string]any{"enabled": false},
			"configs":     "sources/one/dispat.json,sources/two/dispat.json",
		})
		control.Commit("chore: import both sources as one value")

		found := covPolyrepoImported(control.StatusOK())
		assert.True(t, found["one"])
		assert.True(t, found["two"])
	})

	t.Run("fragments composed through a reference", func(t *testing.T) {
		control := covPolyrepoImportFleet(t)
		// Each fragment lives beside the sources it names, and names them
		// relative to itself rather than to the control file.
		control.WriteFile("fragments/first.json", `["../sources/one/dispat.json"]`+"\n")
		absolute := filepath.Join(t.TempDir(), "second.json")
		// The control root is canonicalized before paths are held against it,
		// so the fragment names the same canonical spelling.
		canonicalRoot, err := filepath.EvalSymlinks(control.Root)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(absolute,
			[]byte(`["`+filepath.Join(canonicalRoot, "sources", "two", "dispat.json")+`"]`+"\n"), 0o644))
		control.WriteConfigRaw(map[string]any{
			"polyrepo":    true,
			"logFormat":   "json",
			"logLevel":    "info",
			"updateCheck": false,
			"github":      map[string]any{"enabled": false},
			"configs":     map[string]any{"$ref": []string{"fragments/first.json", absolute}},
		})
		control.Commit("chore: import both sources through fragments")

		found := covPolyrepoImported(control.StatusOK())
		assert.True(t, found["one"], "a fragment's relative path is read from the fragment")
		assert.True(t, found["two"], "and a fragment may be named by an absolute path")
	})

	t.Run("a value that names no file at all", func(t *testing.T) {
		control := covPolyrepoImportFleet(t)
		control.WriteConfigRaw(map[string]any{
			"polyrepo":    true,
			"logFormat":   "json",
			"logLevel":    "info",
			"updateCheck": false,
			"github":      map[string]any{"enabled": false},
			"configs":     7,
		})
		control.Commit("chore: import something that is not a path")

		res := control.Status()
		assert.Equal(t, 1, res.Code)
		out := covPolyrepoOutput(res)
		assert.Contains(t, out, `config \"7\"`)
		// The refusal is dispat's own: the operating system's wording for a
		// missing file differs between runtimes ("no such file or directory"
		// under gc, "file does not exist" under TinyGo), so the claim is the
		// diagnostic that names the value, not the errno text behind it.
		assert.Contains(t, out, "E330")
	})
}
