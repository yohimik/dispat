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

// A named entry-space command should read that space's folder configuration,
// not every peer folder file in the fleet. The malformed peer still fails if
// it is the space the user actually asks to execute.
func TestFleetSpaceCommandDoesNotReadUnselectedPeerFolder(t *testing.T) {
	fleet := newChoreographyFleet(t, "api", "sdk")
	fleet.writeConfig("api", func(cfg *models.File) {
		sc := cfg.Spaces["api"]
		sc.Scripts = map[string]models.Script{"identify": {"echo entry > owner.txt"}}
		cfg.Spaces["api"] = sc
	})
	fleet.peer("api").Commit("chore: declare entry space script")
	fleet.push("api")
	fleet.peer("sdk").WriteFile("packages/dispat.json", "{malformed")
	fleet.peer("sdk").Commit("chore: leave a malformed peer space file")
	fleet.push("sdk")
	fleet.link("api", "sdk")
	entry := fleet.enter("api")

	good := entry.Command("exec", "identify", "--for", "space:api", "--in", "space:api")
	require.Zero(t, good.Code, "%s\n%s", good.Stdout, good.Stderr)
	body, err := os.ReadFile(entry.Path("packages", "owner.txt"))
	require.NoError(t, err)
	assert.Equal(t, "entry\n", string(body))

	loop := entry.Command("for", "-s", "api", "--do", `echo "$DISPAT_SPACE"`)
	require.Zero(t, loop.Code, "%s\n%s", loop.Stdout, loop.Stderr)
	assert.Contains(t, loop.Stdout, "api\n")

	bad := entry.Command("exec", "identify", "--for", "space:sdk", "--in", "space:sdk")
	require.NotZero(t, bad.Code)
	assert.Contains(t, bad.Stdout+bad.Stderr, "packages/dispat.json")
}
