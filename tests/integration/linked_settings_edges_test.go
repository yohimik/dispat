// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"
)

// A linked fleet has no control repository, but a command naming a space
// directly still belongs to the entry repository. Equal local space names
// in another peer must not lend it a command, environment, or work folder.
func TestLinkedSpaceExecUsesEntrySettings(t *testing.T) {
	fleet := newChoreographyFleet(t, "api", "sdk")
	for _, name := range fleet.names {
		fleet.writeConfig(name, func(cfg *models.File) {
			cfg.Spaces = map[string]models.SpaceConfig{
				"tools": {
					Path: models.PathList{"packages"},
					Scripts: map[string]models.Script{
						"identify": {"printf '%s' \"$OWNER\" > owner.txt"},
					},
				},
			}
			cfg.Env = map[string]string{"OWNER": name}
		})
		fleet.peer(name).Commit("chore: configure local space commands")
		fleet.push(name)
	}
	fleet.link("api", "sdk")
	entry := fleet.enter("api")
	res := entry.Command("exec", "identify", "--for", "space:tools", "--in", "space:tools")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	data, err := os.ReadFile(entry.Path("packages", "owner.txt"))
	require.NoError(t, err)
	assert.Equal(t, "api", string(data))
	assert.NoFileExists(t, entry.Path(".links", "sdk", "packages", "owner.txt"))
}
