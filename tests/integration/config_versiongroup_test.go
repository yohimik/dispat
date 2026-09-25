// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Goal 36, the configuration half: a versionGroup stated once on the space
// has to reach the packages of that space through the override ladder. Every
// other group scenario states the membership on the packages themselves,
// which is exactly the shape that hid this: a space folder's own config file
// plus any layer speaking about one package used to be refused for a
// `versioning` nobody wrote.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// spaceGroupConfig is the fixture: two spaces joined to one declared group by
// the spaces themselves, with the plain build/publish flow.
func spaceGroupConfig() models.File {
	cfg := harness.BaseFile(1)
	cfg.Scripts = map[string]models.Script{"build": {echoBuild}, "publish": {"echo publishing"}}
	cfg.VersionGroups = map[string]models.VersionGroupConfig{"platform": {Versioning: models.VersioningFixed}}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: models.PathList{"packages"}, Flow: buildPublish(), VersionGroup: "platform"},
		"svc":  {Path: models.PathList{"services"}, Flow: buildPublish(), VersionGroup: "platform"},
	}
	return cfg
}

// TestSpaceVersionGroupReachesPackagesWithOverrideLayers: a space states the
// group once, its folder states something of its own, and two of its packages
// carry override layers — the layered spelling of the same membership every
// other scenario writes per package. The plan has to load, name the group for
// every member, and version them as one.
func TestSpaceVersionGroupReachesPackagesWithOverrideLayers(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(spaceGroupConfig())
	r.SeedPackage("packages", "lib1")
	r.SeedPackage("packages", "lib2")
	r.SeedPackage("services", "app1")
	// The space folder speaks for itself and configures one of its packages;
	// another package speaks through its own folder's file. Neither says
	// anything about versioning, so both stay in the group the space joined.
	spaceFile(t, r, "packages", models.SpaceFile{
		RevertOnFail: models.Bool(true),
		Packages:     map[string]models.PackageConfig{"lib1": {TagFormat: "lib1-v{version}"}},
	})
	packageFile(t, r, "services/app1", models.PackageConfig{RevertOnFail: models.Bool(true)})
	r.Commit("feat(lib1): the group's first release")

	// The plan itself: every package is planned, and each one names the group
	// it versions with, which is how a script inside a loop reads it.
	res := r.StatusOK("-p", "*")
	for _, name := range []string{"lib1", "lib2", "app1"} {
		assert.NotEmpty(t, harness.GraphLine(res.Events, name), "%s is missing from the graph:\n%s", name, res.Stdout)
	}
	res = r.Command("for", "-p", "*", "--do", `echo "$DISPAT_ITEM|${DISPAT_GROUP-unset}"`)
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	for _, name := range []string{"lib1", "lib2", "app1"} {
		assert.Contains(t, res.Stdout, name+"|platform",
			"%s must report the group its space joined", name)
	}

	// And they version as one: the member with an override layer rides with
	// the group, in its own tag spelling.
	r.ReleaseOK()
	assert.True(t, r.IsTagged("lib1-v0.1.0"), "tags: %v", r.TagList())
	assert.True(t, r.IsTagged("lib2@0.1.0"), "tags: %v", r.TagList())
	assert.True(t, r.IsTagged("app1@0.1.0"), "the other space rides the group; tags: %v", r.TagList())
}

// TestSpaceVersionGroupIsStillSupersededPerPackage: the ladder still decides
// who is in the group. A package layer naming its own versioning leaves the
// group, and a layer naming both axes at once is the contradiction it always
// was, reported against the layer that wrote it.
func TestSpaceVersionGroupIsStillSupersededPerPackage(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(spaceGroupConfig())
	r.SeedPackage("packages", "lib1")
	r.SeedPackage("services", "app1")
	spaceFile(t, r, "packages", models.SpaceFile{RevertOnFail: models.Bool(true)})
	packageFile(t, r, "services/app1", models.PackageConfig{Versioning: models.VersioningIndependent})
	r.Commit("feat(lib1): moves the group, not the detached member")

	res := r.Command("for", "-p", "*", "--do", `echo "$DISPAT_ITEM|${DISPAT_GROUP-unset}"`)
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "lib1|platform")
	assert.Contains(t, res.Stdout, "app1|unset",
		"an independent member is in no group at all:\n%s", res.Stdout)

	r.ReleaseOK()
	assert.True(t, r.IsTagged("lib1@0.1.0"), "tags: %v", r.TagList())
	assert.Zero(t, r.TagCount("app1@"), "the detached member does not ride; tags: %v", r.TagList())

	// Both axes in one layer stay mutually exclusive, and the message names
	// the package whose file wrote them.
	packageFile(t, r, "services/app1", models.PackageConfig{
		Versioning: models.VersioningFixed, VersionGroup: "platform"})
	res = r.Status("-p", "*")
	assert.NotEqual(t, 0, res.Code)
	assert.Contains(t, res.Stdout+res.Stderr, "mutually exclusive")
	assert.Contains(t, res.Stdout+res.Stderr, "app1")

	// A space folder's own file is a layer like any other: stating both axes
	// there is the same contradiction, refused before the merge could keep one
	// of them silently, and the message names the file.
	packageFile(t, r, "services/app1", models.PackageConfig{Versioning: models.VersioningIndependent})
	spaceFile(t, r, "packages", models.SpaceFile{
		Versioning: models.VersioningFixedMajor, VersionGroup: "platform"})
	res = r.Status("-p", "*")
	assert.NotEqual(t, 0, res.Code)
	assert.Contains(t, res.Stdout+res.Stderr, "versioning and versionGroup are mutually exclusive")
	assert.Contains(t, res.Stdout+res.Stderr, "packages/dispat.json")
}
