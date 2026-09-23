// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/models"
)

func spaceCommandFleet(t *testing.T, shared bool) *choreographyFleet {
	t.Helper()
	fleet := newChoreographyFleet(t, "api", "sdk")
	for _, name := range fleet.names {
		fleet.writeConfig(name, func(cfg *models.File) {
			space := name
			if shared {
				space = "tools"
			}
			cfg.Spaces = map[string]models.SpaceConfig{space: {
				Path:    models.PathList{"packages"},
				Scripts: map[string]models.Script{"identify": {`printf '%s' "$OWNER" > owner.txt`}},
			}}
			cfg.Env = map[string]string{"OWNER": name}
			if shared {
				cfg.VersionGroups = map[string]models.VersionGroupConfig{"Train": {Versioning: "fixed"}}
				sc := cfg.Spaces[space]
				sc.VersionGroup = "Train"
				if name == "api" {
					sc.Path = models.PathList{"packages", ".links"}
				}
				cfg.Spaces[space] = sc
			}
			cfg.Scripts["select-peer"] = models.Script{`cd .links/sdk/packages/sdk-pkg && dispat for --changed --do 'echo SELECTED:$DISPAT_ITEM' && dispat if --changed --consumers --then 'echo HELD'`}
			cfg.Scripts["visit-peer"] = models.Script{"cd .links/sdk/packages && dispat exec identify --for cwd --in cwd"}
			cfg.Scripts["fallback"] = models.Script{`printf 'root-%s' "$OWNER" > owner.txt`}
		})
		if shared && name == "api" {
			fleet.peer(name).WriteFile(".links/.dispatexclude", "sdk\n")
		}
		fleet.peer(name).Commit("chore: configure repository space commands")
		fleet.push(name)
	}
	fleet.link("api", "sdk")
	return fleet
}

// A peer-only space is a usable script subject and directory. Its own root
// provides fallback scripts and environment, even when the entry differs.
func TestFleetSpaceCommandsResolvePeerOwner(t *testing.T) {
	fleet := spaceCommandFleet(t, false)
	entry := fleet.enter("api")
	for _, row := range []struct {
		script, want string
		fallback     bool
	}{
		{"identify", "sdk", false}, {"fallback", "root-sdk", true},
	} {
		args := []string{"exec", row.script, "--for", "space:SDK", "--in", "space:sdk"}
		if row.fallback {
			args = append(args, "--fallback")
		}
		res := entry.Command(args...)
		require.Zero(t, res.Code, "%s\n%s", res.Stdout, res.Stderr)
		body, err := os.ReadFile(entry.Path(".links", "sdk", "packages", "owner.txt"))
		require.NoError(t, err)
		assert.Equal(t, row.want, string(body))
		assert.NoFileExists(t, entry.Path("packages", "owner.txt"))
	}
}

// Selecting spaces visits each physical owner's primary folder. A group name
// both peers declare lists once, because a group loop iterates over names while
// each peer keeps its own group. A current-folder subject selects the actual
// owner despite an equal entry name.
func TestFleetSpaceLoopsAndCurrentFolderKeepDistinctOwners(t *testing.T) {
	fleet := spaceCommandFleet(t, true)
	entry := fleet.enter("api")
	res := entry.Command("for", "-s", "tools", "--do", `echo "$DISPAT_ITEM"; echo visited > "$DISPAT_DIR/visited.txt"`)
	require.Zero(t, res.Code, "%s\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, 2, strings.Count(res.Stdout, "tools\n"))
	assert.FileExists(t, entry.Path("packages", "visited.txt"))
	assert.FileExists(t, entry.Path(".links", "sdk", "packages", "visited.txt"))
	groups := entry.Command("for", "-g", "train", "--do", `echo "GROUP:$DISPAT_GROUP"`)
	require.Zero(t, groups.Code, "%s\n%s", groups.Stdout, groups.Stderr)
	assert.Equal(t, 1, strings.Count(groups.Stdout, "GROUP:Train\n"), "a group name two peers declare lists once")
	res = entry.Shell("dispat exec visit-peer --for root")
	require.Zero(t, res.Code, "%s\n%s", res.Stdout, res.Stderr)
	body, err := os.ReadFile(entry.Path(".links", "sdk", "packages", "owner.txt"))
	require.NoError(t, err)
	assert.Equal(t, "sdk", string(body))
	assert.NoFileExists(t, entry.Path("packages", "owner.txt"))
}

// An entry without that space cannot choose arbitrarily between equal peer
// names for a command with side effects. An explicit current folder resolves it.
func TestFleetSpaceCommandRefusesAmbiguousPeerOwners(t *testing.T) {
	fleet := newChoreographyFleet(t, "entry", "api", "sdk")
	for _, name := range []string{"api", "sdk"} {
		fleet.writeConfig(name, func(cfg *models.File) {
			cfg.Spaces = map[string]models.SpaceConfig{"tools": {
				Path:    models.PathList{"packages"},
				Scripts: map[string]models.Script{"identify": {`echo "$OWNER" > owner.txt`}},
			}}
			cfg.Env = map[string]string{"OWNER": name}
		})
		fleet.peer(name).Commit("chore: configure equal peer space names")
		fleet.push(name)
	}
	fleet.writeConfig("entry", func(cfg *models.File) {
		cfg.Scripts["select-peer"] = models.Script{`cd .links/sdk/packages/sdk-pkg && dispat for --changed --do 'echo SELECTED:$DISPAT_ITEM' && dispat if --changed --consumers --then 'echo HELD'`}
		cfg.Scripts["visit-peer"] = models.Script{"cd .links/sdk/packages && dispat exec identify --for cwd --in cwd"}
	})
	fleet.peer("entry").Commit("chore: add peer command")
	fleet.push("entry")
	fleet.link("entry", "api")
	fleet.link("entry", "sdk")
	entry := fleet.enter("entry")
	res := entry.Command("exec", "identify", "--for", "space:tools", "--in", "space:tools")
	require.NotZero(t, res.Code)
	assert.Contains(t, res.Stdout+res.Stderr, "multiple peer repositories")
	for _, name := range []string{"api", "sdk"} {
		assert.NoFileExists(t, entry.Path(".links", name, "packages", "owner.txt"))
	}
	res = entry.Shell("dispat exec visit-peer --for root")
	require.Zero(t, res.Code, "%s\n%s", res.Stdout, res.Stderr)
	body, err := os.ReadFile(entry.Path(".links", "sdk", "packages", "owner.txt"))
	require.NoError(t, err)
	assert.Equal(t, "sdk\n", string(body))
}

// A nested helper retains the package cwd for its implicit window selection;
// inheriting the fleet configuration cannot widen it to all peer packages.
func TestFleetNestedHelpersKeepTheirCurrentPackageSelection(t *testing.T) {
	fleet := spaceCommandFleet(t, false)
	result := fleet.enter("api").Shell("dispat exec select-peer --for root")
	require.Zero(t, result.Code, "%s\n%s", result.Stdout, result.Stderr)
	assert.Contains(t, result.Stdout, "SELECTED:sdk-pkg\n")
	assert.NotContains(t, result.Stdout, "SELECTED:api-pkg\n")
	assert.Contains(t, result.Stdout, "HELD\n")
}
