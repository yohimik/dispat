// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestComputeMinimalJoinsExistingComponentsWithoutReplacingTheirLinks proves
// minimal topology treats a healthy link as part of the spanning tree. It
// adds the single edge needed to reach that component and keeps the component's
// existing edge as the only route between its members.
//
// The edge lands at the component's centre, which is sdk and not the entry:
// api is an end of the chain api-sdk-web, so joining shop there would leave a
// route of three hops where joining it at sdk leaves two, and section 27.9
// charges link evidence and settlement by the longest route there is.
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
	assert.Contains(t, preview.Stdout, "+ link sdk shop")
	assert.NotContains(t, preview.Stdout, "+ link api shop")
	assert.NotContains(t, preview.Stdout, "+ link api sdk")
	assert.NotContains(t, preview.Stdout, "+ link api web")
	assert.NotContains(t, preview.Stdout, "+ link sdk web")

	result := entry.CommandEnv(fileProtocolEnv(), "compute", "--topology", "minimal", "--write")
	require.Equal(t, 0, result.Code, "%s\n%s", result.Stdout, result.Stderr)
	assert.Contains(t, result.Stdout, "linked shop from sdk")
	assert.NotContains(t, result.Stdout, "linked sdk from api")
	assert.NotContains(t, result.Stdout, "linked web from api")
	assert.Equal(t, ".links/shop",
		entry.Git("-C", ".links/sdk", "config", "--file", ".gitmodules", "submodule.shop.path"))
	modules := readAbs(t, entry.Path(".gitmodules"))
	assert.NotContains(t, modules, "submodule \"web\"",
		"minimal topology must not add a second route from api to web")
	assert.NotContains(t, modules, "submodule \"shop\"",
		"the entry is an end of the chain, so the link to shop is not written there")
	assert.Equal(t, sdkPin, entry.Git("rev-parse", "HEAD:.links/sdk"),
		"compute must keep the existing edge from the entry")
	assert.Equal(t, webPin, entry.Git("-C", ".links/sdk", "rev-parse", "HEAD:.links/web"),
		"compute must keep the existing edge inside the connected component")
	assert.Equal(t, ".links/web",
		entry.Git("-C", ".links/sdk", "config", "--file", ".gitmodules", "submodule.web.path"))
}

// TestComputeMinimalWritesTheJoiningLinkWhereACheckoutExists proves the centre
// of a component is where the link goes only while that centre is a checkout
// this run holds. Here the component is zed and a peer whose checkout was
// never materialised, so the centre is the peer that sorts first and is a name
// and nothing else; the member nearest it that does have a checkout writes the
// link instead, and the missing checkout is reported as its own change.
func TestComputeMinimalWritesTheJoiningLinkWhereACheckoutExists(t *testing.T) {
	fleet := newChoreographyFleet(t, "zed", "mid", "alpha")
	fleet.link("zed", "mid")
	zed := fleet.peer("zed")
	zed.Git("submodule", "deinit", "-f", "--", ".links/mid")

	check := zed.CommandEnv(fileProtocolEnv(), "compute", "--topology", "minimal", "--check")
	assert.Equal(t, 1, check.Code, "%s\n%s", check.Stdout, check.Stderr)
	assert.Contains(t, check.Stdout, "+ link zed alpha")
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
