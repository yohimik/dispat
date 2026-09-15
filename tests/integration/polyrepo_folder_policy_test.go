// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestPolyrepoFolderInputsFollowConfigurationOwnership proves the process
// boundary follows the same rule for space files, package files, ignore files
// and exclude files. Central traversal reads those files in the control
// repository, does not read them through a source checkout (including a
// symlink spelling), and imported discovery retains ordinary local behavior.
func TestPolyrepoFolderInputsFollowConfigurationOwnership(t *testing.T) {
	central := harness.New(t)
	central.SeedPackage("packages", "central")
	central.SeedPackage("packages", "central-filtered")
	writePolyrepoJSON(t, central, "packages/dispat.json", map[string]any{
		"tagFormat": "untrusted-space-{name}@{version}",
	})
	writePolyrepoJSON(t, central, "packages/central/dispat.json", map[string]any{
		"tagFormat": "untrusted-package-{name}@{version}",
	})
	central.WriteFile("packages/.dispatexclude", "central-filtered\n")
	central.WriteFile("packages/central/.dispatignore", "ignored.txt\n")
	central.Commit("feat: seed centrally managed packages")

	imported := harness.New(t)
	imported.SeedPackage("packages", "imported")
	imported.SeedPackage("packages", "imported-filtered")
	importedCfg := polyrepoFile()
	delete(importedCfg, "polyrepo")
	importedCfg["spaces"] = map[string]any{
		"imported": map[string]any{
			"path":      []string{"packages"},
			"tagFormat": "imported-root-{name}@{version}",
		},
	}
	writePolyrepoJSON(t, imported, "dispat.json", importedCfg)
	writePolyrepoJSON(t, imported, "packages/dispat.json", map[string]any{
		"tagFormat": "imported-space-{name}@{version}",
	})
	writePolyrepoJSON(t, imported, "packages/imported/dispat.json", map[string]any{
		"tagFormat": "imported-package-{name}@{version}",
	})
	imported.WriteFile("packages/.dispatexclude", "imported-filtered\n")
	imported.WriteFile("packages/imported/.dispatignore", "ignored.txt\n")
	imported.Commit("feat(imported): seed imported packages")

	control := harness.New(t)
	addPolyrepoSource(t, control, "central-source", "sources/central", central)
	addPolyrepoSource(t, control, "imported-source", "sources/imported", imported)
	require.NoError(t, os.Symlink(filepath.Join("sources", "central"), control.Path("central-alias")))
	control.SeedPackage("owned", "control")
	control.SeedPackage("owned", "control-filtered")
	writePolyrepoJSON(t, control, "owned/dispat.json", map[string]any{
		"tagFormat": "control-space-{name}@{version}",
	})
	writePolyrepoJSON(t, control, "owned/control/dispat.json", map[string]any{
		"tagFormat": "control-package-{name}@{version}",
	})
	control.WriteFile("owned/.dispatexclude", "control-filtered\n")
	control.WriteFile("owned/control/.dispatignore", "ignored.txt\n")
	cfg := polyrepoFile()
	cfg["configs"] = []string{"sources/imported/dispat.json"}
	cfg["spaces"] = map[string]any{
		"central": map[string]any{
			"path":      []string{"central-alias/packages"},
			"tagFormat": "central-{name}@{version}",
		},
		"owned": map[string]any{
			"path":      []string{"owned"},
			"tagFormat": "owned-root-{name}@{version}",
		},
	}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("feat(control): assemble mixed folder-policy workspace")

	status := control.StatusOK()
	assert.NotEmpty(t, harness.GraphLine(status.Events, "central").Str("message"))
	assert.NotEmpty(t, harness.GraphLine(status.Events, "central-filtered").Str("message"),
		"the central source's .dispatexclude has no authority")
	assert.NotEmpty(t, harness.GraphLine(status.Events, "control").Str("message"))
	assert.Empty(t, harness.GraphLine(status.Events, "control-filtered").Str("message"),
		"the control-owned .dispatexclude retains ordinary behavior")
	assert.NotEmpty(t, harness.GraphLine(status.Events, "imported").Str("message"))
	assert.Empty(t, harness.GraphLine(status.Events, "imported-filtered").Str("message"),
		"the imported .dispatexclude retains ordinary behavior")

	control.ReleaseOK()
	assert.Contains(t, polyrepoTags(control, "sources/central"), "central-central@0.1.0")
	assert.Contains(t, polyrepoTags(control, "sources/central"), "central-central-filtered@0.1.0")
	assert.NotContains(t, polyrepoTags(control, "sources/central"), "untrusted-package-central@0.1.0")
	assert.Contains(t, polyrepoTags(control, "sources/imported"), "imported-package-imported@0.1.0")
	assert.Contains(t, control.TagList(), "control-package-control@0.1.0")

	control.WriteFile("central-alias/packages/central/ignored.txt", "central source must count this\n")
	commitPolyrepoSource(t, control, "sources/central", "fix: source-local ignore cannot hide this")
	control.WriteFile("sources/imported/packages/imported/ignored.txt", "imported source ignores this\n")
	commitPolyrepoSource(t, control, "sources/imported", "fix: imported ignore hides this")
	control.WriteFile("owned/control/ignored.txt", "control ignores this\n")
	control.Git("add", "sources/central", "sources/imported", "owned/control/ignored.txt")
	control.Git("commit", "-q", "-m", "fix: checkpoint ignore-policy fixtures")

	after := control.StatusOK()
	assert.Equal(t, "0.1.0 -> 0.1.1", harness.GraphLine(after.Events, "central").Str("version"),
		"a centrally managed source .dispatignore is not read")
	assert.Equal(t, "unchanged", harness.GraphLine(after.Events, "imported").Str("message"),
		"the imported package's .dispatignore remains active")
	assert.Equal(t, "unchanged", harness.GraphLine(after.Events, "control").Str("message"),
		"the control-owned package's .dispatignore remains active")
}

// TestPolyrepoEmptySpaceFolderScriptsFollowConfigurationOwnership keeps the
// run command's typo guard aligned with package discovery. A script defined
// only by an empty control-owned or imported space folder is a known no-op;
// the same file in a centrally managed source is not configuration.
func TestPolyrepoEmptySpaceFolderScriptsFollowConfigurationOwnership(t *testing.T) {
	central := harness.New(t)
	central.SeedPackage("packages", "hosted")
	require.NoError(t, os.MkdirAll(central.Path("empty"), 0o755))
	writePolyrepoJSON(t, central, "empty/dispat.json", map[string]any{
		"scripts": map[string]any{"source-empty": []string{"echo must-not-run"}},
	})
	central.Commit("feat(hosted): seed source and empty folder")

	imported := harness.New(t)
	require.NoError(t, os.MkdirAll(imported.Path("empty"), 0o755))
	writePolyrepoJSON(t, imported, "dispat.json", map[string]any{
		"spaces": map[string]any{"empty": map[string]any{"path": []string{"empty"}}},
	})
	writePolyrepoJSON(t, imported, "empty/dispat.json", map[string]any{
		"scripts": map[string]any{"imported-empty": []string{"echo imported-empty"}},
	})
	imported.Commit("chore: define imported empty-space script")

	control := harness.New(t)
	addPolyrepoSource(t, control, "central-source", "sources/central", central)
	addPolyrepoSource(t, control, "imported-source", "sources/imported", imported)
	require.NoError(t, os.MkdirAll(control.Path("empty"), 0o755))
	writePolyrepoJSON(t, control, "empty/dispat.json", map[string]any{
		"scripts": map[string]any{"control-empty": []string{"echo control-empty"}},
	})
	cfg := polyrepoFile()
	cfg["configs"] = []string{"sources/imported/dispat.json"}
	cfg["spaces"] = map[string]any{
		"packages": map[string]any{"path": []string{"sources/central/packages"}},
		"empty":    map[string]any{"path": []string{"empty"}},
		"source":   map[string]any{"path": []string{"sources/central/empty"}},
	}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: assemble empty-space script workspace")

	control.RunScriptOK("control-empty")
	control.RunScriptOK("imported-empty")
	unknown := control.RunScript("source-empty")
	assert.NotZero(t, unknown.Code)
	assert.Contains(t, unknown.Stdout+unknown.Stderr, "no script")
}
