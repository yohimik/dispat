// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

func nestedTagNamespaceRepo(t *testing.T) *harness.Repo {
	t.Helper()
	aSource := harness.New(t)
	aSource.SeedPackage("packages", "a")
	aConfig := polyrepoFile()
	aConfig["tagFormat"] = "v{version}"
	aConfig["spaces"] = centralSpaces(map[string]string{"a": "packages"})
	writePolyrepoJSON(t, aSource, "dispat.json", aConfig)
	aSource.Commit("feat(a): initial")
	aSource.Git("tag", "v1.0.0")
	aSource.WriteFile("packages/a/next.txt", "next\n")
	aSource.Commit("feat(a): next")
	aSource.Git("tag", "v1.1.0")

	bSource := harness.New(t)
	bSource.SeedPackage("packages", "b")
	bConfig := polyrepoFile()
	bConfig["tagFormat"] = "v{version}"
	bConfig["changelog"] = map[string]any{"enabled": true}
	bConfig["spaces"] = centralSpaces(map[string]string{"b": "packages"})
	writePolyrepoJSON(t, bSource, "dispat.json", bConfig)
	bSource.Commit("feat(b): already released")
	bSource.Git("tag", "v1.1.0")

	control := harness.New(t)
	addPolyrepoSource(t, control, "source-a", "sources/a", aSource)
	addPolyrepoSource(t, control, "source-b", "sources/b", bSource)
	cfg := polyrepoFile()
	cfg["configs"] = []string{"sources/a/dispat.json", "sources/b/dispat.json"}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: compose sources")
	return control
}

// TestPolyrepoNestedStepMasksTagsWithinItsOwner gives two imported sources the
// repository-local tag v1.1.0. The environment describes a nested A step after
// A wrote that tag. Asking the step to write B's changelog must remain a no-op:
// A's run tag cannot erase B's already-published baseline.
func TestPolyrepoNestedStepMasksTagsWithinItsOwner(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  []string
	}{
		{name: "invoking tag without workspace listing"},
		{name: "invoking tag and releasing workspace listing", env: []string{
			"DISPAT_WORKSPACE_PACKAGES=A B",
			"DISPAT_WORKSPACE_PACKAGE_A_NAME=a",
			"DISPAT_WORKSPACE_PACKAGE_A_VERSION=1.1.0",
			"DISPAT_WORKSPACE_PACKAGE_A_RELEASING=true",
			"DISPAT_WORKSPACE_PACKAGE_B_NAME=b",
			"DISPAT_WORKSPACE_PACKAGE_B_VERSION=1.1.0",
			"DISPAT_WORKSPACE_PACKAGE_B_RELEASING=false",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			control := nestedTagNamespaceRepo(t)
			ordinary := control.Command("changelog", "--package", "b", "--file", "ORDINARY.md")
			require.Equal(t, 0, ordinary.Code, "stdout:\n%s\nstderr:\n%s", ordinary.Stdout, ordinary.Stderr)
			_, err := os.Stat(control.Path("sources", "b", "packages", "b", "ORDINARY.md"))
			assert.ErrorIs(t, err, os.ErrNotExist, "the unwired plan observes B's baseline")

			env := append([]string{
				"DISPAT_PACKAGE=a",
				"DISPAT_TAG=v1.1.0",
				"DISPAT_NEW_VERSION=1.1.0",
			}, tc.env...)
			res := control.CommandEnv(env, "changelog", "--package", "b", "--file", "NESTED.md")
			require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			_, err = os.Stat(control.Path("sources", "b", "packages", "b", "NESTED.md"))
			assert.ErrorIs(t, err, os.ErrNotExist, "B stays released at its own v1.1.0 tag")
		})
	}
}
