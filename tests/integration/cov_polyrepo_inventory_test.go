// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Coverage scenarios for reading the control repository itself: the state the
// control checkout has to be in before it can name a fleet, and the states a
// declared submodule has to be in before it can own a package. These run
// before participation, imports and planning, which is why each one is a
// refusal rather than a warning.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// covPolyrepoLinkOnly writes a `.gitmodules` naming one source at path, with
// no checkout of its own, so each scenario can put whatever it wants there.
func covPolyrepoLinkOnly(t *testing.T, control *harness.Repo, name, path, url string) {
	t.Helper()
	covPolyrepoWriteGitmodules(t, control, `[submodule "`+name+`"]
	path = `+path+`
	url = `+url+`
`)
}

// TestCovPolyrepoRefusesAControlCheckoutItCannotRead: composition starts at
// the control repository, and two things about it have to hold before
// anything else is looked at: it has a HEAD the gitlinks can be read from, and
// its `.gitmodules` is a file Git's own config reader can parse. A control
// repository that has never committed and an inventory that is a folder are
// each refused before a single source is touched.
func TestCovPolyrepoRefusesAControlCheckoutItCannotRead(t *testing.T) {
	t.Run("a control repository with no commit", func(t *testing.T) {
		source := harness.New(t)
		source.SeedPackage("packages", "lib")
		source.Commit("feat(lib): bootstrap library")

		control := harness.New(t)
		addPolyrepoSource(t, control, "lib-source", "sources/lib", source)
		cfg := covPolyrepoFile()
		cfg.Spaces = covPolyrepoSpaces(map[string]string{"libs": "sources/lib/packages"})
		control.WriteConfigModel(cfg)
		// Everything is staged and nothing is committed, which is what a
		// freshly assembled control repository looks like.

		res := control.Status()
		assert.Equal(t, 1, res.Code)
		out := covPolyrepoOutput(res)
		assert.Contains(t, out, "E330")
		assert.Contains(t, out, "control repository has no HEAD")
	})

	t.Run("an inventory that is not a file", func(t *testing.T) {
		source := harness.New(t)
		source.SeedPackage("packages", "lib")
		source.Commit("feat(lib): bootstrap library")

		control := harness.New(t)
		addPolyrepoSource(t, control, "lib-source", "sources/lib", source)
		cfg := covPolyrepoFile()
		cfg.Spaces = covPolyrepoSpaces(map[string]string{"libs": "sources/lib/packages"})
		control.WriteConfigModel(cfg)
		control.Commit("chore: assemble the fleet")
		require.NoError(t, os.Remove(control.Path(".gitmodules")))
		require.NoError(t, os.MkdirAll(control.Path(".gitmodules"), 0o755))

		res := control.Status()
		assert.Equal(t, 1, res.Code)
		assert.Contains(t, covPolyrepoOutput(res), "read .gitmodules")
	})
}

// TestCovPolyrepoRefusesADeclaredSourceItCannotUse: a `.gitmodules` entry is a
// claim about a repository, and each part of that claim is checked before the
// repository is allowed to own a package: the path stays inside the control
// workspace, what is checked out there is a repository of its own, that
// repository has a HEAD, and the control HEAD pins it. Each refusal names the
// declared identity.
func TestCovPolyrepoRefusesADeclaredSourceItCannotUse(t *testing.T) {
	newControl := func(t *testing.T) *harness.Repo {
		t.Helper()
		control := harness.New(t)
		control.SeedPackage("packages", "tool")
		cfg := covPolyrepoFile()
		cfg.Spaces = covPolyrepoSpaces(map[string]string{"tools": "packages"})
		control.WriteConfigModel(cfg)
		return control
	}

	t.Run("a path that leaves the control root", func(t *testing.T) {
		control := newControl(t)
		covPolyrepoLinkOnly(t, control, "escaping-source", "../outside", "https://example.invalid/x.git")
		control.Commit("chore: declare a source above the control root")

		res := control.Status()
		assert.Equal(t, 1, res.Code)
		out := covPolyrepoOutput(res)
		assert.Contains(t, out, "escaping-source")
		assert.Contains(t, out, "escapes control root")
	})

	t.Run("a path that is an ordinary folder", func(t *testing.T) {
		control := newControl(t)
		control.WriteFile("sources/plain/README.md", "not a repository\n")
		covPolyrepoLinkOnly(t, control, "plain-source", "sources/plain", "https://example.invalid/x.git")
		control.Commit("chore: declare a folder as a source")

		res := control.Status()
		assert.Equal(t, 1, res.Code)
		out := covPolyrepoOutput(res)
		assert.Contains(t, out, "plain-source")
		assert.Contains(t, out, "resolves to Git root",
			"a folder of the control repository is the control repository, not a source")
	})

	t.Run("a repository the control HEAD does not pin", func(t *testing.T) {
		source := harness.New(t)
		source.SeedPackage("packages", "lib")
		source.Commit("feat(lib): bootstrap library")

		control := newControl(t)
		control.Git("clone", "-q", source.Root, control.Path("sources", "unpinned"))
		covPolyrepoLinkOnly(t, control, "unpinned-source", "sources/unpinned", source.Root)
		control.Git("add", ".gitmodules", "dispat.json", "packages")
		control.Git("commit", "-q", "-m", "chore: declare a source with no gitlink")

		res := control.Status()
		assert.Equal(t, 1, res.Code)
		out := covPolyrepoOutput(res)
		assert.Contains(t, out, "unpinned-source")
		assert.Contains(t, out, "is not pinned by control HEAD")
	})

	t.Run("a repository with no HEAD of its own", func(t *testing.T) {
		control := newControl(t)
		unborn := control.Path("sources", "unborn")
		require.NoError(t, os.MkdirAll(unborn, 0o755))
		control.Git("init", "-q", unborn)
		covPolyrepoLinkOnly(t, control, "unborn-source", "sources/unborn", "https://example.invalid/x.git")
		control.Git("add", ".gitmodules", "dispat.json", "packages")
		control.Git("commit", "-q", "-m", "chore: declare a source that never committed")

		res := control.Status()
		assert.Equal(t, 1, res.Code)
		out := covPolyrepoOutput(res)
		assert.Contains(t, out, "unborn-source")
		assert.Contains(t, out, "is not pinned by control HEAD")
	})
}

// TestCovPolyrepoNestedCommandRefusesARepositoryTheRunNeverComposed: the live
// coordinator is bound to the exact source identities the release started
// with. A repository that appears in the control working tree while the run is
// in flight is not one of them, so the nested command refuses to resolve a pin
// for it rather than treating an unpinned checkout as admitted.
func TestCovPolyrepoNestedCommandRefusesARepositoryTheRunNeverComposed(t *testing.T) {
	source := harness.New(t)
	source.SeedPackage("packages", "lib")
	source.Commit("feat(lib): bootstrap library")
	latecomer := harness.New(t)
	latecomer.SeedPackage("packages", "late")
	latecomer.Commit("feat(late): bootstrap the latecomer")

	control := harness.New(t)
	addPolyrepoSource(t, control, "lib-source", "sources/lib", source)
	logPath := control.Path("nested-latecomer.log")
	gitmodules := control.Path(".gitmodules")
	probe := `
git clone -q ` + harness.ShQuote(latecomer.Root) + ` ` + harness.ShQuote(control.Path("sources", "late")) + `
printf '%s\n' '[submodule "late-source"]' '	path = sources/late' '	url = ` + latecomer.Root + `' >> ` +
		harness.ShQuote(gitmodules) + `
` + control.DispatCommand("status") + ` >> ` + harness.ShQuote(logPath) + ` 2>&1
printf '>>> exit %s\n' "$?" >> ` + harness.ShQuote(logPath) + `
echo publishing
`

	cfg := covPolyrepoFile()
	cfg.Spaces = covPolyrepoSpaces(map[string]string{"libs": "sources/lib/packages"})
	cfg.Scripts["publish"] = models.Script{probe}
	control.WriteConfigModel(cfg)
	control.Commit("chore: configure a publish stage that links a new repository")

	control.ReleaseOK()
	raw, err := os.ReadFile(logPath)
	require.NoError(t, err, "the publish stage did not run")
	log := string(raw)
	assert.Contains(t, log, "late-source")
	assert.Contains(t, log, "reading live run pin")
	assert.Contains(t, log, ">>> exit 1")
	assert.Contains(t, polyrepoTags(control, "sources/lib"), "lib@0.1.0",
		"the outer release is unaffected by what the nested command refused")
	assert.NoFileExists(t, filepath.Join(control.Root, "sources", "late", "dispat.json"))
}
