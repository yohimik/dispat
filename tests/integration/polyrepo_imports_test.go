// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Goal 52: the shapes a `configs` declaration is written in.
// The list of imported source configurations decides which repositories
// participate, and it is read before the control configuration is decoded, so
// every spelling the ordinary config reader accepts has to reach the same
// list of files: written out, split from one value, or composed through `$ref`
// fragments that each contribute part of it.

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// importFleet links two sources, each carrying its own
// configuration, and returns the control repository. Nothing declares the
// imports yet: that is what each scenario writes.
func importFleet(t *testing.T) *harness.Repo {
	t.Helper()
	control := harness.New(t)
	for _, pkg := range []string{"one", "two"} {
		source := harness.New(t)
		source.SeedPackage("packages", pkg)
		cfg := polyrepoModelFile()
		cfg.Polyrepo = false
		cfg.Spaces = polyrepoModelSpaces(map[string]string{pkg + "s": "packages"})
		source.WriteConfigModel(cfg)
		source.Commit("feat(" + pkg + "): bootstrap " + pkg)
		addPolyrepoSource(t, control, pkg+"-source", "sources/"+pkg, source)
	}
	return control
}

// importedPackages reads back which packages a composed run found.
func importedPackages(res harness.RunResult) map[string]bool {
	found := map[string]bool{}
	for _, e := range res.Events {
		if pkg := e.Package(); pkg != "" {
			found[pkg] = true
		}
	}
	return found
}

// TestPolyrepoInitialsBelongToTheConfigThatOwnsThePackage: an initials entry
// is read against the packages its own configuration declares. The control
// configuration naming a package an imported source declares warns that the
// entry matches none of its packages, names the repository, and the package
// plans from its own history rather than from the entry.
func TestPolyrepoInitialsBelongToTheConfigThatOwnsThePackage(t *testing.T) {
	control := importFleet(t)
	control.WriteConfigRaw(map[string]any{
		"polyrepo": true, "logFormat": "json", "logLevel": "info", "updateCheck": false,
		"github":   map[string]any{"enabled": false},
		"configs":  []string{"sources/one/dispat.json", "sources/two/dispat.json"},
		"initials": map[string]any{"one": "5.0.0"},
	})
	control.Commit("chore: state an initial for a package a source declares")

	res := control.StatusOK()
	warned := findEvent(t, res.Events, "initials entry matches no discovered package owned by this config, ignoring")
	assert.Equal(t, "warn", warned.Str("level"))
	assert.Equal(t, "one", warned.Package())
	assert.Equal(t, "control", warned.Str("repository"))
	assert.Equal(t, "0.0.0 -> 0.1.0", harness.GraphLine(res.Events, "one").Str("version"),
		"the package plans from its own history")
}

// TestPolyrepoImportListComposesFromEverySpelling: the same two imports
// reach the same two repositories whether they are written as a list or as
// one value carrying both, and a value naming no file is refused.
func TestPolyrepoImportListComposesFromEverySpelling(t *testing.T) {
	t.Run("a list written out", func(t *testing.T) {
		control := importFleet(t)
		control.WriteConfigRaw(map[string]any{
			"polyrepo":    true,
			"logFormat":   "json",
			"logLevel":    "info",
			"updateCheck": false,
			"github":      map[string]any{"enabled": false},
			"configs":     []string{"sources/one/dispat.json", "sources/two/dispat.json"},
		})
		control.Commit("chore: import both sources as a list")

		found := importedPackages(control.StatusOK())
		assert.True(t, found["one"])
		assert.True(t, found["two"])
	})

	t.Run("one value carrying both", func(t *testing.T) {
		control := importFleet(t)
		control.WriteConfigRaw(map[string]any{
			"polyrepo":    true,
			"logFormat":   "json",
			"logLevel":    "info",
			"updateCheck": false,
			"github":      map[string]any{"enabled": false},
			"configs":     "sources/one/dispat.json,sources/two/dispat.json",
		})
		control.Commit("chore: import both sources as one value")

		found := importedPackages(control.StatusOK())
		assert.True(t, found["one"])
		assert.True(t, found["two"])
	})

	t.Run("a value that names no file at all", func(t *testing.T) {
		control := importFleet(t)
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
		out := combinedOutput(res)
		assert.Contains(t, out, `config \"7\"`)
		// The refusal is dispat's own: the operating system's wording for a
		// missing file differs between runtimes ("no such file or directory"
		// under gc, "file does not exist" under TinyGo), so the claim is the
		// diagnostic that names the value, not the errno text behind it.
		assert.Contains(t, out, "E330")
	})
}
