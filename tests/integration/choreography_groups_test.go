// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Version groups in an identity-linked fleet, read through the binary. Every
// peer's own configuration is an ordinary repository-local root (CCME §27.11),
// so the groups it declares and the implicit groups of the spaces it versions
// as one belong to that peer alone (§27.3). A same-named group in another peer
// is another group, with a version of its own.

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// platformGroupShape is one way a peer versions the packages of its
// `packages` folder as one group named platform.
type platformGroupShape struct {
	name string
	join func(cfg *models.File, peer string)
}

// platformGroupShapes are both of them: a versionGroups declaration the
// peer's space joins, and a space named platform that versions as a group of
// its own.
var platformGroupShapes = []platformGroupShape{
	{"declared group", func(cfg *models.File, peer string) {
		cfg.VersionGroups = map[string]models.VersionGroupConfig{"platform": {Versioning: "fixed"}}
		space := cfg.Spaces[peer]
		space.VersionGroup = "platform"
		cfg.Spaces[peer] = space
	}},
	{"shared space", func(cfg *models.File, _ string) {
		cfg.Spaces = map[string]models.SpaceConfig{"platform": {Path: models.PathList{"packages"}, Versioning: "fixed"}}
	}},
}

// platformFleet links api and web. Each versions its own two packages as a
// group named platform, api's at 3.4.0 and web's at 1.2.0, beside a
// standalone tool at 0.3.0 outside the group. A fix to web's own package and
// a fix to each tool wait to release.
func platformFleet(t *testing.T, shape platformGroupShape) *choreographyFleet {
	t.Helper()
	fleet := newChoreographyFleet(t, "api", "web")
	baselines := map[string]string{"api": "3.4.0", "web": "1.2.0"}
	for _, name := range fleet.names {
		peer := fleet.peer(name)
		tool := name + "-tool"
		peer.SeedPackage("packages", name+"-lib")
		peer.SeedPackage("tools", tool)
		fleet.writeConfig(name, func(cfg *models.File) {
			shape.join(cfg, name)
			cfg.Packages = map[string]models.PackageConfig{tool: {Path: "tools/" + tool}}
		})
		peer.Commit("chore: version " + name + " as one platform")
		for _, pkg := range []string{peer.pkg, name + "-lib"} {
			peer.Git("tag", pkg+"@"+baselines[name])
		}
		peer.Git("tag", tool+"@0.3.0")
		peer.WriteFile(filepath.Join("tools", tool, "work.txt"), "fixed\n")
		peer.Commit("fix(" + tool + "): repair the tool")
		fleet.push(name)
		peer.Git("push", "-q", "origin", "--tags")
	}
	fleet.work("web", "fix(web-pkg): repair the web platform")
	fleet.link("api", "web")
	return fleet
}

// planByPackage is the verdict and version every plan graph line reported,
// keyed by package: what two runs have to agree on to plan one release.
func planByPackage(res harness.RunResult) map[string]string {
	plan := map[string]string{}
	for _, event := range res.Events {
		if event.Package() == "" || event.Str("level") != "info" || event.Str("space") == "" || event.Str("stage") != "" {
			continue
		}
		plan[event.Package()] = event.Str("message") + " " + event.Str("version")
	}
	return plan
}

// TestLinkedGroupsStayRepositoryLocal: two linked peers each version a group
// named platform, at different baselines. Each group releases on its own
// version and the other peer's is untouched; the plan is the same from either
// entry; an unqualified `--group platform` still selects both local groups
// and nothing else, which is §27.3's selector union; and a peer whose
// versionGroup names only the other peer's group is refused when its
// configuration loads.
func TestLinkedGroupsStayRepositoryLocal(t *testing.T) {
	for _, shape := range platformGroupShapes {
		t.Run(shape.name, func(t *testing.T) {
			fleet := platformFleet(t, shape)
			plans := map[string]map[string]string{}
			for _, name := range fleet.names {
				plans[name] = planByPackage(fleet.enter(name).StatusOK("--package", "*"))
			}
			assert.Equal(t, plans["api"], plans["web"], "the plan does not depend on the entry")
			assert.Equal(t, map[string]string{
				"api-pkg": "unchanged 3.4.0", "api-lib": "unchanged 3.4.0",
				"web-pkg": "● changed 1.2.0 -> 1.2.1", "web-lib": "● changed 1.2.0 -> 1.2.1",
				"api-tool": "● changed 0.3.0 -> 0.3.1", "web-tool": "● changed 0.3.0 -> 0.3.1",
			}, plans["api"], "each group keeps its own repository's version")

			entry := fleet.enter("api")
			byGroup := planByPackage(entry.StatusOK("--group", "platform"))
			for _, pkg := range []string{"api-pkg", "api-lib", "web-pkg", "web-lib"} {
				assert.Equal(t, plans["api"][pkg], byGroup[pkg], "--group platform selects %s", pkg)
			}
			for _, pkg := range []string{"api-tool", "web-tool"} {
				assert.Equal(t, "⊝ not selected 0.3.0 -> 0.3.1", byGroup[pkg], "--group platform leaves %s out", pkg)
			}

			entry.ReleaseOK("--group", "platform")
			assert.ElementsMatch(t, []string{"api-pkg@3.4.0", "api-lib@3.4.0", "api-tool@0.3.0"}, entry.TagList())
			assert.ElementsMatch(t, []string{"web-pkg@1.2.0", "web-lib@1.2.0", "web-tool@0.3.0", "web-pkg@1.2.1", "web-lib@1.2.1"},
				tagsIn(entry, ".links/web"))
		})
		t.Run(shape.name+" named only by a peer", func(t *testing.T) {
			fleet := newChoreographyFleet(t, "api", "web")
			fleet.writeConfig("api", func(cfg *models.File) { shape.join(cfg, "api") })
			fleet.writeConfig("web", func(cfg *models.File) {
				space := cfg.Spaces["web"]
				space.VersionGroup = "platform"
				cfg.Spaces["web"] = space
			})
			for _, name := range fleet.names {
				fleet.peer(name).Commit("chore: version " + name + " with the platform")
				fleet.push(name)
			}
			fleet.link("api", "web")
			// A configuration that never loaded is reported by the boot logger,
			// which quotes the error, so the assertion reads the unquoted part.
			for _, name := range fleet.names {
				res := fleet.enter(name).Status("--package", "*")
				require.NotZero(t, res.Code, "entry %s\nstdout:\n%s", name, res.Stdout)
				assert.Contains(t, res.Stdout+res.Stderr, "matches no versionGroups entry and no space", "entry %s", name)
			}
		})
	}
}
