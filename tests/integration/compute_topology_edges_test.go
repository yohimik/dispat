// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"
)

// TestComputeMinimalJoinsExistingComponentsWithoutReplacingTheirLinks proves
// minimal topology treats a healthy link as part of the spanning tree. It
// adds the single edge needed to reach that component and keeps the component's
// existing edge as the only route between its members.
//
// The edge lands at an endpoint of the existing api-sdk-web chain. Adding it
// at the interior sdk would create a branch and violate minimal's path shape.
func TestComputeMinimalJoinsExistingComponentsWithoutReplacingTheirLinks(t *testing.T) {
	fleet := newChoreographyFleet(t, "api", "sdk", "web", "shop")
	fleet.link("api", "sdk")
	fleet.link("sdk", "web")
	fleet.follow("api", "sdk")
	entry := fleet.enter("api")
	fleet.materialize(entry, ".links/sdk", "web")
	sdkPin := entry.Git("rev-parse", "HEAD:.links/sdk")
	webPin := entry.Git("-C", ".links/sdk", "rev-parse", "HEAD:.links/web")

	preview := entry.CommandEnv(fileProtocolEnv(), "compute", "--topology", "minimal", "--check")
	assert.Equal(t, 1, preview.Code, "%s\n%s", preview.Stdout, preview.Stderr)
	assert.Contains(t, preview.Stdout, "+ link api shop")
	assert.NotContains(t, preview.Stdout, "+ link sdk shop")
	assert.NotContains(t, preview.Stdout, "+ link api sdk")
	assert.NotContains(t, preview.Stdout, "+ link api web")
	assert.NotContains(t, preview.Stdout, "+ link sdk web")

	result := entry.CommandEnv(fileProtocolEnv(), "compute", "--topology", "minimal", "--write")
	require.Equal(t, 0, result.Code, "%s\n%s", result.Stdout, result.Stderr)
	assert.Contains(t, result.Stdout, "linked shop from api")
	assert.NotContains(t, result.Stdout, "linked sdk from api")
	assert.NotContains(t, result.Stdout, "linked web from api")
	assert.Equal(t, ".links/shop",
		entry.Git("config", "--file", ".gitmodules", "submodule.shop.path"))
	modules := readAbs(t, entry.Path(".gitmodules"))
	assert.NotContains(t, modules, "submodule \"web\"",
		"minimal topology must not add a second route from api to web")
	assert.Contains(t, modules, "submodule \"shop\"",
		"the entry is a free endpoint of the preserved chain")
	assert.Equal(t, sdkPin, entry.Git("rev-parse", "HEAD:.links/sdk"),
		"compute must keep the existing edge from the entry")
	assert.Equal(t, webPin, entry.Git("-C", ".links/sdk", "rev-parse", "HEAD:.links/web"),
		"compute must keep the existing edge inside the connected component")
	assert.Equal(t, ".links/web",
		entry.Git("-C", ".links/sdk", "config", "--file", ".gitmodules", "submodule.web.path"))
}

// A preview can offer only the first writable chain edge. --write follows
// each newly materialized peer, recomposes the fleet, and continues from a
// free endpoint until every roster member belongs to the path.
func TestComputeMinimalWritesFourMemberChainByRecomposition(t *testing.T) {
	fleet := newChoreographyFleet(t, "api", "sdk", "shop", "web")
	entry := fleet.enter("api")

	preview := entry.CommandEnv(fileProtocolEnv(), "compute", "--topology", "minimal", "--check")
	assert.Equal(t, 1, preview.Code, "%s\n%s", preview.Stdout, preview.Stderr)
	assert.Contains(t, preview.Stdout, "+ link api sdk")
	assert.Equal(t, 1, strings.Count(preview.Stdout, "+ link "), "only the next writable edge is offered")

	result := entry.CommandEnv(fileProtocolEnv(), "compute", "--topology", "minimal", "--write")
	require.Equal(t, 0, result.Code, "stdout:\n%s\nstderr:\n%s", result.Stdout, result.Stderr)
	assert.Contains(t, result.Stdout, "created 3 fleet link operation(s)")
	assert.Contains(t, result.Stdout, "linked sdk from api")
	assert.Contains(t, result.Stdout, "linked shop from api")
	assert.Contains(t, result.Stdout, "linked web from sdk")
	assert.Equal(t, ".links/sdk", entry.Git("config", "--file", ".gitmodules", "submodule.sdk.path"))
	assert.Equal(t, ".links/shop", entry.Git("config", "--file", ".gitmodules", "submodule.shop.path"))
	assert.Equal(t, ".links/web", entry.Git("-C", ".links/sdk", "config", "--file", ".gitmodules", "submodule.web.path"))
	assert.NotContains(t, readAbs(t, entry.Path(".links", "sdk", ".gitmodules")), "submodule \"shop\"")

	check := entry.CommandEnv(fileProtocolEnv(), "compute", "--topology", "minimal", "--check")
	assert.Equal(t, 0, check.Code, "%s\n%s", check.Stdout, check.Stderr)
	assert.NotContains(t, check.Stdout, "+ link ")
	again := entry.CommandEnv(fileProtocolEnv(), "compute", "--topology", "minimal", "--write")
	assert.Equal(t, 0, again.Code, "%s\n%s", again.Stdout, again.Stderr)
	assert.NotContains(t, again.Stdout, "linked ")
}

func TestComputeMinimalRefusesExistingBranchWithoutEdits(t *testing.T) {
	fleet := newChoreographyFleet(t, "api", "sdk", "shop", "web")
	fleet.link("api", "sdk")
	fleet.link("api", "shop")
	fleet.link("api", "web")
	entry := fleet.enter("api")
	before := entry.Git("status", "--porcelain=v1")

	result := entry.CommandEnv(fileProtocolEnv(), "compute", "--topology", "minimal", "--write")
	require.NotZero(t, result.Code, "stdout:\n%s\nstderr:\n%s", result.Stdout, result.Stderr)
	assert.Contains(t, result.Stdout+result.Stderr, "chain")
	assert.Equal(t, before, entry.Git("status", "--porcelain=v1"))
}

func TestComputeMinimalKeepsStagedRepairWhenNewPeerRevealsBranch(t *testing.T) {
	fleet := newChoreographyFleet(t, "api", "sdk", "shop", "web", "zed")
	fleet.link("sdk", "shop")
	fleet.link("sdk", "web")
	fleet.link("sdk", "zed")
	entry := fleet.peer("api")

	result := entry.CommandEnv(fileProtocolEnv(), "compute", "--topology", "minimal", "--write")
	require.NotZero(t, result.Code, "stdout:\n%s\nstderr:\n%s", result.Stdout, result.Stderr)
	assert.Contains(t, result.Stdout, "linked sdk from api")
	assert.Contains(t, result.Stdout+result.Stderr, "chain")
	assert.Contains(t, result.Stdout+result.Stderr, "review the staged changes before retrying")
	assert.Contains(t, entry.Git("diff", "--cached", "--name-only"), ".gitmodules")
}

func TestComputeMinimalInteractiveAcceptsOnlyThePresentedLink(t *testing.T) {
	fleet := newChoreographyFleet(t, "api", "sdk", "web")
	entry := fleet.peer("api")

	result := entry.CommandInputEnv(fileProtocolEnv(), "y\n", "compute", "--topology", "minimal", "--interactive")
	require.Equal(t, 0, result.Code, "stdout:\n%s\nstderr:\n%s", result.Stdout, result.Stderr)
	assert.Contains(t, result.Stdout, "linked sdk from api")
	assert.NotContains(t, result.Stdout, "linked web from")
	assert.DirExists(t, entry.Path(".links", "sdk"))
	assert.NoDirExists(t, entry.Path(".links", "web"))
	assert.Contains(t, result.Stdout, "+ link api web", "the later suggestion is shown for a future choice")
}

// The entry initially knows only sdk. Cloning sdk reveals its roster entry
// for web, which the entry must learn separately from forming the chain.
func newSparseRosterChainFleet(t *testing.T) *choreographyFleet {
	t.Helper()
	fleet := newChoreographyFleet(t, "api", "sdk", "web")
	fleet.writeConfig("api", func(cfg *models.File) {
		cfg.Repositories = cfg.Repositories[:1] // api names sdk, and sdk names web.
	})
	fleet.peer("api").Commit("chore: name sdk before discovering web")
	fleet.push("api")
	return fleet
}

func TestComputeMinimalReportsNewRosterWorkAfterLinkWrite(t *testing.T) {
	fleet := newSparseRosterChainFleet(t)
	entry := fleet.enter("api")

	first := entry.CommandEnv(fileProtocolEnv(), "compute", "--topology", "minimal", "--write")
	require.NotZero(t, first.Code, "stdout:\n%s\nstderr:\n%s", first.Stdout, first.Stderr)
	assert.Contains(t, first.Stdout, "linked sdk from api")
	assert.Contains(t, first.Stdout, "+ repository api web")
	assert.Contains(t, first.Stdout+first.Stderr, "fleet repair is incomplete")

	second := entry.CommandEnv(fileProtocolEnv(), "compute", "--topology", "minimal", "--write")
	require.Equal(t, 0, second.Code, "stdout:\n%s\nstderr:\n%s", second.Stdout, second.Stderr)
	assert.Contains(t, readAbs(t, entry.Path("dispat.json")), `"name": "web"`)
	check := entry.CommandEnv(fileProtocolEnv(), "compute", "--topology", "minimal", "--check")
	assert.Equal(t, 0, check.Code, "stdout:\n%s\nstderr:\n%s", check.Stdout, check.Stderr)
}

func TestComputeMinimalInteractiveShowsNewRosterWithoutAcceptingIt(t *testing.T) {
	fleet := newSparseRosterChainFleet(t)
	entry := fleet.peer("api")

	result := entry.CommandInputEnv(fileProtocolEnv(), "y\n", "compute", "--topology", "minimal", "--interactive")
	require.Equal(t, 0, result.Code, "stdout:\n%s\nstderr:\n%s", result.Stdout, result.Stderr)
	assert.Contains(t, result.Stdout, "linked sdk from api")
	assert.Contains(t, result.Stdout, "+ repository api web")
	assert.NotContains(t, readAbs(t, entry.Path("dispat.json")), `"name": "web"`,
		"a newly discovered roster entry was not part of the accepted selection")
}

// A missing checkout must be initialized before the next join is planned.
// Otherwise compute could mistake an inaccessible endpoint for an available
// owner or attach at an interior vertex and create a branch.
func TestComputeMinimalWritesTheJoiningLinkWhereACheckoutExists(t *testing.T) {
	fleet := newChoreographyFleet(t, "zed", "mid", "alpha")
	fleet.link("zed", "mid")
	zed := fleet.peer("zed")
	zed.Git("submodule", "deinit", "-f", "--", ".links/mid")

	check := zed.CommandEnv(fileProtocolEnv(), "compute", "--topology", "minimal", "--check")
	assert.Equal(t, 1, check.Code, "%s\n%s", check.Stdout, check.Stderr)
	assert.NotContains(t, check.Stdout, "+ link ")
	assert.Contains(t, check.Stdout, "+ init zed .links/mid")
	assert.NotContains(t, check.Stdout, "+ link zed mid", "zed and mid are joined already")
	assert.NotContains(t, check.Stdout, "+ link alpha mid",
		"neither end of that pair is a checkout this run can write in")
}

// TestComputeStarAddsOnlyMissingHubEdges proves star topology preserves a
// direct entry edge that already exists and creates only the missing spoke.
func TestComputeStarAddsOnlyMissingHubEdges(t *testing.T) {
	fleet := newChoreographyFleet(t, "api", "sdk", "web")
	fleet.link("api", "sdk")
	entry := fleet.peer("api")
	sdkPin := entry.Git("rev-parse", "HEAD:.links/sdk")

	preview := entry.CommandEnv(fileProtocolEnv(), "compute", "--topology", "star", "--check")
	assert.Equal(t, 1, preview.Code, "%s\n%s", preview.Stdout, preview.Stderr)
	assert.Contains(t, preview.Stdout, "+ link api web")
	assert.NotContains(t, preview.Stdout, "+ link api sdk")

	result := entry.CommandEnv(fileProtocolEnv(), "compute", "--topology", "star", "--write")
	require.Equal(t, 0, result.Code, "%s\n%s", result.Stdout, result.Stderr)
	assert.Contains(t, result.Stdout, "linked web from api")
	assert.NotContains(t, result.Stdout, "linked sdk from api")
	assert.Equal(t, sdkPin, entry.Git("rev-parse", "HEAD:.links/sdk"),
		"the existing spoke remains pinned at the revision the entry recorded")
	assert.Equal(t, ".links/sdk",
		entry.Git("config", "--file", ".gitmodules", "submodule.sdk.path"))
	assert.Equal(t, ".links/web",
		entry.Git("config", "--file", ".gitmodules", "submodule.web.path"))
}
