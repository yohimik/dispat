// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Coverage scenarios for composing the fleet: the submodule inventory a
// control repository is read from, the explicit boundaries a configuration may
// supply for it, and the record destinations each source resolves from its own
// remote. Everything here happens before a plan exists, so each refusal is
// asserted together with the fact that nothing was written.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// covPolyrepoWriteGitmodules replaces the control repository's submodule
// inventory with hand-written text. `.gitmodules` is an ordinary tracked file
// that a merge, a rebase or an editor can leave in any of these states, and
// dispat reads it before it is allowed to touch a single repository.
func covPolyrepoWriteGitmodules(t *testing.T, control *harness.Repo, text string) {
	t.Helper()
	control.WriteFile(".gitmodules", text)
}

// TestCovPolyrepoRefusesASubmoduleInventoryItCannotRead: the `.gitmodules`
// inventory decides which repositories exist, so every shape that would make
// that answer ambiguous stops composition. A control repository with no
// inventory at all, two names that fold together, a path that leaves the
// control workspace, and two submodules whose checkouts nest are each named
// back with the identity that caused them.
func TestCovPolyrepoRefusesASubmoduleInventoryItCannotRead(t *testing.T) {
	t.Run("a control repository with no inventory", func(t *testing.T) {
		control := harness.New(t)
		control.SeedPackage("packages", "tool")
		cfg := covPolyrepoFile()
		cfg.Spaces = covPolyrepoSpaces(map[string]string{"tools": "packages"})
		control.WriteConfigModel(cfg)
		control.Commit("chore: ask for a composed workspace with nothing linked")

		res := control.Status()
		assert.Equal(t, 1, res.Code)
		assert.Contains(t, covPolyrepoOutput(res), "has no .gitmodules")
	})

	t.Run("two identities that fold together", func(t *testing.T) {
		source := harness.New(t)
		source.SeedPackage("packages", "lib")
		source.Commit("feat(lib): bootstrap library")

		control := harness.New(t)
		addPolyrepoSource(t, control, "lib-source", "sources/lib", source)
		covPolyrepoWriteGitmodules(t, control, `[submodule "lib-source"]
	path = sources/lib
	url = `+source.Root+`
[submodule "LIB-SOURCE"]
	path = sources/lib
	url = `+source.Root+`
`)
		cfg := covPolyrepoFile()
		cfg.Spaces = covPolyrepoSpaces(map[string]string{"libs": "sources/lib/packages"})
		control.WriteConfigModel(cfg)
		control.Commit("chore: link one repository under two spellings")

		res := control.Status()
		assert.Equal(t, 1, res.Code)
		out := covPolyrepoOutput(res)
		assert.Contains(t, out, "duplicate submodule name")
		assert.Contains(t, out, "case-insensitive")
	})

	t.Run("a path that leaves the control workspace", func(t *testing.T) {
		source := harness.New(t)
		source.SeedPackage("packages", "lib")
		source.Commit("feat(lib): bootstrap library")

		control := harness.New(t)
		addPolyrepoSource(t, control, "lib-source", "sources/lib", source)
		// The declared path still lands inside the control root, and only the
		// symlink it names takes the checkout out of the workspace.
		require.NoError(t, os.Remove(control.Path("sources", "lib", ".git")))
		require.NoError(t, os.RemoveAll(control.Path("sources", "lib")))
		require.NoError(t, os.Symlink(source.Root, control.Path("sources", "lib")))

		cfg := covPolyrepoFile()
		cfg.Spaces = covPolyrepoSpaces(map[string]string{"libs": "sources/lib/packages"})
		control.WriteConfigModel(cfg)
		control.Git("add", "-A")
		control.Git("commit", "-q", "-m", "chore: point a submodule path out of the workspace")

		res := control.Status()
		assert.Equal(t, 1, res.Code)
		assert.Contains(t, covPolyrepoOutput(res), "resolves outside the control workspace")
	})

	t.Run("two checkouts that nest", func(t *testing.T) {
		outer := harness.New(t)
		outer.SeedPackage("packages", "outer")
		outer.Commit("feat(outer): bootstrap the outer source")
		inner := harness.New(t)
		inner.SeedPackage("packages", "inner")
		inner.Commit("feat(inner): bootstrap the inner source")

		control := harness.New(t)
		addPolyrepoSource(t, control, "outer-source", "sources/outer", outer)
		// A second checkout standing inside the first one's worktree. Git
		// itself refuses to register that as a submodule, so the inventory
		// entry is the hand-written one a merge or an editor leaves behind.
		control.Git("clone", "-q", inner.Root, control.Path("sources", "outer", "nested"))
		covPolyrepoWriteGitmodules(t, control, `[submodule "outer-source"]
	path = sources/outer
	url = `+outer.Root+`
[submodule "inner-source"]
	path = sources/outer/nested
	url = `+inner.Root+`
`)

		cfg := covPolyrepoFile()
		cfg.Spaces = covPolyrepoSpaces(map[string]string{"outers": "sources/outer/packages"})
		control.WriteConfigModel(cfg)
		control.Commit("chore: link one source inside another")

		res := control.Status()
		assert.Equal(t, 1, res.Code)
		out := covPolyrepoOutput(res)
		assert.Contains(t, out, "submodule roots overlap")
		assert.Contains(t, out, "outer-source")
		assert.Contains(t, out, "inner-source")
	})
}

// TestCovPolyrepoRefusesAnImportItCannotAttributeToARepository: an imported
// configuration establishes its declaring repository as a participant, so
// dispat has to be able to say which linked repository owns the file. A file
// outside every repository, a file in a nested repository nobody linked, two
// files claiming the same repository, and a file that imports further
// configurations of its own are each refused with the path named.
func TestCovPolyrepoRefusesAnImportItCannotAttributeToARepository(t *testing.T) {
	newFleet := func(t *testing.T) (*harness.Repo, *harness.Repo) {
		t.Helper()
		source := harness.New(t)
		source.SeedPackage("packages", "lib")
		sourceCfg := covPolyrepoFile()
		sourceCfg.Polyrepo = false
		sourceCfg.Spaces = covPolyrepoSpaces(map[string]string{"libs": "packages"})
		source.WriteConfigModel(sourceCfg)
		source.Commit("feat(lib): bootstrap library")

		control := harness.New(t)
		addPolyrepoSource(t, control, "lib-source", "sources/lib", source)
		return control, source
	}

	t.Run("a configuration outside the control root", func(t *testing.T) {
		control, _ := newFleet(t)
		outside := filepath.Join(t.TempDir(), "dispat.json")
		require.NoError(t, os.WriteFile(outside, []byte("{}\n"), 0o644))
		cfg := covPolyrepoFile()
		cfg.Configs = []string{"sources/lib/dispat.json"}
		control.WriteConfigModel(cfg)
		control.Commit("chore: import the source configuration")

		res := control.Status("--configs", outside)
		assert.Equal(t, 1, res.Code)
		assert.Contains(t, covPolyrepoOutput(res), "path escapes control root")
	})

	t.Run("a configuration inside the control metadata", func(t *testing.T) {
		control, _ := newFleet(t)
		control.WriteFile(filepath.Join(".git", "imported.json"), "{}\n")
		cfg := covPolyrepoFile()
		cfg.Configs = []string{"sources/lib/dispat.json"}
		control.WriteConfigModel(cfg)
		control.Commit("chore: import the source configuration")

		res := control.Status("--configs", ".git/imported.json")
		assert.Equal(t, 1, res.Code)
		assert.Contains(t, covPolyrepoOutput(res), "not inside an initialized Git repository")
	})

	t.Run("a configuration owned by no linked repository", func(t *testing.T) {
		control, _ := newFleet(t)
		control.WriteFile("release/extra.json", "{}\n")
		cfg := covPolyrepoFile()
		cfg.Configs = []string{"sources/lib/dispat.json", "release/extra.json"}
		control.WriteConfigModel(cfg)
		control.Commit("chore: import a configuration the control repository owns")

		res := control.Status()
		assert.Equal(t, 1, res.Code)
		assert.Contains(t, covPolyrepoOutput(res), "not an initialized .gitmodules repository")
	})

	t.Run("two configurations claiming one repository", func(t *testing.T) {
		control, _ := newFleet(t)
		control.WriteFile("sources/lib/release/dispat.json", "{}\n")
		commitPolyrepoSource(t, control, "sources/lib", "chore: add a second configuration")
		checkpointPolyrepoSource(t, control, "sources/lib")
		cfg := covPolyrepoFile()
		cfg.Configs = []string{"sources/lib/dispat.json", "sources/lib/release/dispat.json"}
		control.WriteConfigModel(cfg)
		control.Commit("chore: import one repository twice")

		res := control.Status()
		assert.Equal(t, 1, res.Code)
		out := covPolyrepoOutput(res)
		assert.Contains(t, out, "conflicting imported configs")
		assert.Contains(t, out, "lib-source")
	})

	t.Run("an import that imports further configurations", func(t *testing.T) {
		control, _ := newFleet(t)
		nested := covPolyrepoFile()
		nested.Polyrepo = false
		nested.Configs = []string{"packages/lib"}
		nested.Spaces = covPolyrepoSpaces(map[string]string{"libs": "packages"})
		data := control.Path("sources", "lib", "dispat.json")
		require.NoError(t, os.WriteFile(data, covPolyrepoJSON(t, nested), 0o644))
		commitPolyrepoSource(t, control, "sources/lib", "chore: let the source import more configurations")
		checkpointPolyrepoSource(t, control, "sources/lib")
		cfg := covPolyrepoFile()
		cfg.Configs = []string{"sources/lib/dispat.json"}
		control.WriteConfigModel(cfg)
		control.Commit("chore: import a configuration that imports more")

		res := control.Status()
		assert.Equal(t, 1, res.Code)
		assert.Contains(t, covPolyrepoOutput(res), "nested workspace imports are not allowed")
	})
}

// TestCovPolyrepoRefusesABaselineTupleItCannotResolve: an explicit
// `repositoryBaselines` entry is the recovery for a boundary dispat cannot
// infer, so every part of it is resolved while the configuration is still
// loading: all four fields present, the repository a participant of this run,
// and the revision an actual commit reachable from that repository's HEAD.
func TestCovPolyrepoRefusesABaselineTupleItCannotResolve(t *testing.T) {
	newFleet := func(t *testing.T) *harness.Repo {
		t.Helper()
		source := harness.New(t)
		source.SeedPackage("packages", "lib")
		source.Commit("feat(lib): bootstrap library")
		legacy := harness.New(t)
		legacy.SeedPackage("packages", "legacy")
		legacy.Commit("feat(legacy): bootstrap the legacy source")

		control := harness.New(t)
		addPolyrepoSource(t, control, "lib-source", "sources/lib", source)
		addPolyrepoSource(t, control, "legacy-source", "sources/legacy", legacy)
		return control
	}

	base := func() models.File {
		cfg := covPolyrepoFile()
		cfg.Spaces = covPolyrepoSpaces(map[string]string{"libs": "sources/lib/packages"})
		cfg.RepositoryOverrides = map[string]models.RepositoryOverrideConfig{
			"legacy-source": {Enabled: models.Bool(false)},
		}
		return cfg
	}

	cases := []struct {
		name     string
		baseline models.RepositoryBaselineConfig
		says     string
	}{
		{
			name:     "a tuple missing a field",
			baseline: models.RepositoryBaselineConfig{Consumer: "lib", ReleaseTag: "lib@1.0.0", Repository: "lib-source"},
			says:     "consumer, releaseTag, repository and revision are required",
		},
		{
			name: "a repository nothing links",
			baseline: models.RepositoryBaselineConfig{
				Consumer: "lib", ReleaseTag: "lib@1.0.0", Repository: "ghost-source", Revision: "HEAD",
			},
			says: "unknown repository",
		},
		{
			name: "a repository this run excluded",
			baseline: models.RepositoryBaselineConfig{
				Consumer: "lib", ReleaseTag: "lib@1.0.0", Repository: "legacy-source", Revision: "HEAD",
			},
			says: "excluded by repositoryOverrides",
		},
		{
			name: "a revision that is not a commit",
			baseline: models.RepositoryBaselineConfig{
				Consumer: "lib", ReleaseTag: "lib@1.0.0", Repository: "lib-source", Revision: "refs/heads/nowhere",
			},
			says: "is not a commit in repository",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			control := newFleet(t)
			cfg := base()
			cfg.RepositoryBaselines = []models.RepositoryBaselineConfig{tc.baseline}
			control.WriteConfigModel(cfg)
			control.Commit("chore: state an unusable baseline")

			res := control.Status()
			assert.Equal(t, 1, res.Code)
			assert.Contains(t, covPolyrepoOutput(res), tc.says)
			assert.Empty(t, polyrepoTags(control, "sources/lib"))
		})
	}

	t.Run("a revision the repository cannot reach", func(t *testing.T) {
		control := newFleet(t)
		// A commit that exists as an object in the source and is on no branch
		// reachable from its HEAD: the repository has it, the history does not.
		control.WriteFile("sources/lib/packages/lib/sidelined.txt", "another line\n")
		control.Git("-C", "sources/lib", "checkout", "-q", "-b", "sidelined")
		unreachable := commitPolyrepoSource(t, control, "sources/lib", "chore(lib): a sidelined commit")
		control.Git("-C", "sources/lib", "checkout", "-q", harness.DefaultBranch)
		control.Git("-C", "sources/lib", "branch", "-q", "-D", "sidelined")

		cfg := base()
		cfg.RepositoryBaselines = []models.RepositoryBaselineConfig{{
			Consumer: "lib", ReleaseTag: "lib@1.0.0", Repository: "lib-source", Revision: unreachable,
		}}
		control.WriteConfigModel(cfg)
		control.Commit("chore: state a baseline outside the source history")

		res := control.Status()
		assert.Equal(t, 1, res.Code)
		assert.Contains(t, covPolyrepoOutput(res), "is not reachable from repository")
	})
}

// TestCovPolyrepoSourceRecordDestinationsComeFromTheirOwnRemotes: the release
// records of a source package are published against that source's repository,
// so each source resolves its own coordinates from its own remote before the
// plan is reported. Every spelling a remote is written in reaches the same two
// path segments, and a remote that names no repository on the expected host
// resolves to nothing rather than borrowing the control repository's identity.
func TestCovPolyrepoSourceRecordDestinationsComeFromTheirOwnRemotes(t *testing.T) {
	type link struct {
		name, pkg, url string
		owner, repo    string
	}

	run := func(t *testing.T, apiURL string, links []link) map[string]harness.Event {
		t.Helper()
		control := harness.New(t)
		cfg := covPolyrepoFile()
		cfg.LogLevel = "debug"
		cfg.GitHub = &models.GitHubConfig{Enabled: models.Bool(false), APIURL: apiURL}
		spaces := map[string]string{}
		for _, l := range links {
			source := harness.New(t)
			source.SeedPackage("packages", l.pkg)
			source.Commit("feat(" + l.pkg + "): bootstrap " + l.pkg)
			addPolyrepoSource(t, control, l.name, "sources/"+l.pkg, source)
			if l.url != "" {
				control.Git("-C", "sources/"+l.pkg, "config", "remote.origin.url", l.url)
			} else {
				control.Git("-C", "sources/"+l.pkg, "remote", "remove", "origin")
			}
			spaces[l.pkg+"s"] = "sources/" + l.pkg + "/packages"
		}
		cfg.Spaces = covPolyrepoSpaces(spaces)
		control.WriteConfigModel(cfg)
		control.Commit("chore: assemble sources with their own remotes")

		res := control.StatusOK()
		resolved := map[string]harness.Event{}
		for _, e := range res.Events {
			if e.Str("message") == "resolved source record destination" {
				resolved[e.Str("repository")] = e
			}
		}
		return resolved
	}

	t.Run("every spelling of a remote", func(t *testing.T) {
		links := []link{
			{name: "https-source", pkg: "alpha", url: "https://github.com/acme/alpha.git", owner: "acme", repo: "alpha"},
			{name: "scp-source", pkg: "beta", url: "git@github.com:acme/beta.git", owner: "acme", repo: "beta"},
			{name: "ssh-source", pkg: "gamma", url: "ssh://git@github.com/acme/gamma", owner: "acme", repo: "gamma"},
			{name: "elsewhere-source", pkg: "delta", url: "https://git.example.com/acme/delta.git"},
			{name: "deep-source", pkg: "epsilon", url: "https://github.com/acme/team/epsilon.git"},
			{name: "unreadable-source", pkg: "zeta", url: "https://[::1"},
			{name: "remoteless-source", pkg: "eta"},
		}
		resolved := run(t, "", links)
		for _, l := range links {
			e, ok := resolved[l.name]
			require.True(t, ok, "repository %s resolved no record destination", l.name)
			assert.Equal(t, l.owner, e.Str("owner"), "repository %s owner", l.name)
			assert.Equal(t, l.repo, e.Str("repo"), "repository %s repo", l.name)
		}
	})

	t.Run("an enterprise API host moves the expected host with it", func(t *testing.T) {
		links := []link{
			{name: "enterprise-source", pkg: "alpha", url: "https://git.example.com/acme/alpha.git", owner: "acme", repo: "alpha"},
			{name: "public-source", pkg: "beta", url: "https://github.com/acme/beta.git"},
		}
		resolved := run(t, "https://git.example.com/api/v3", links)
		for _, l := range links {
			e, ok := resolved[l.name]
			require.True(t, ok, "repository %s resolved no record destination", l.name)
			assert.Equal(t, l.owner, e.Str("owner"), "repository %s owner", l.name)
			assert.Equal(t, l.repo, e.Str("repo"), "repository %s repo", l.name)
		}
	})

	t.Run("the public API host is the default host written out", func(t *testing.T) {
		links := []link{
			{name: "public-source", pkg: "alpha", url: "https://github.com/acme/alpha.git", owner: "acme", repo: "alpha"},
		}
		resolved := run(t, "https://api.github.com", links)
		e, ok := resolved["public-source"]
		require.True(t, ok)
		assert.Equal(t, "acme", e.Str("owner"))
		assert.Equal(t, "alpha", e.Str("repo"))
	})
}

// covPolyrepoJSON marshals a config model the way the harness writes one, for
// the files a scenario has to place somewhere other than the control root.
func covPolyrepoJSON(t *testing.T, cfg models.File) []byte {
	t.Helper()
	data, err := json.MarshalIndent(cfg, "", "  ")
	require.NoError(t, err)
	return append(data, '\n')
}
