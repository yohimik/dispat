package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestVersioningShared(t *testing.T) {
	assert.False(t, VersioningIndependent.Shared())
	assert.False(t, Versioning("").Shared(), "the zero value is independent")
	assert.True(t, VersioningFixed.Shared())
	assert.True(t, VersioningFixedSparse.Shared())
	assert.True(t, VersioningFixedMajorMinor.Shared())
	assert.True(t, VersioningFixedMajorMinorSparse.Shared())
	assert.True(t, VersioningFixedMajor.Shared())
	assert.True(t, VersioningFixedMajorSparse.Shared())
}

// TestVersioningDepthAndSparseness pins the two numbers every other layer
// reads a mode through: how much of the version the group holds in common,
// and whether an unchanged member rides along or stays behind.
func TestVersioningDepthAndSparseness(t *testing.T) {
	cases := []struct {
		mode   Versioning
		depth  int
		sparse bool
	}{
		{VersioningIndependent, 0, false},
		{Versioning(""), 0, false},
		{Versioning("nonsense"), 0, false},
		{VersioningFixed, 3, false},
		{VersioningFixedSparse, 3, true},
		{VersioningFixedMajorMinor, 2, false},
		{VersioningFixedMajorMinorSparse, 2, true},
		{VersioningFixedMajor, 1, false},
		{VersioningFixedMajorSparse, 1, true},
	}
	for _, c := range cases {
		t.Run(string(c.mode), func(t *testing.T) {
			assert.Equal(t, c.depth, c.mode.SharedDepth())
			assert.Equal(t, c.sparse, c.mode.Sparse())
			assert.Equal(t, c.depth > 0, c.mode.Shared(), "sharing is having a depth")
		})
	}
	assert.Equal(t, SharedVersioningDepth, VersioningFixed.SharedDepth(),
		"the full depth is what fixed shares")
}

// TestSparseModesPairWithAPlainMode fences the naming contract the
// configuration relies on: every sparse mode is a plain mode plus the Sparse
// suffix, and the two agree on everything but sparseness.
func TestSparseModesPairWithAPlainMode(t *testing.T) {
	pairs := map[Versioning]Versioning{
		VersioningFixed:           VersioningFixedSparse,
		VersioningFixedMajorMinor: VersioningFixedMajorMinorSparse,
		VersioningFixedMajor:      VersioningFixedMajorSparse,
	}
	for plain, sparse := range pairs {
		assert.Equal(t, string(plain)+"Sparse", string(sparse))
		assert.Equal(t, plain.SharedDepth(), sparse.SharedDepth(), "a pair shares one depth")
		assert.False(t, plain.Sparse())
		assert.True(t, sparse.Sparse())
	}
}

func TestDepKindString(t *testing.T) {
	assert.Equal(t, "dependencies", KindDependencies.String(), "the zero value spells itself out")
	assert.Equal(t, "devDependencies", KindDevDependencies.String())
	assert.Equal(t, "peerDependencies", KindPeerDependencies.String())
}

// TestRecordSpecsHoldPrereleasesBack pins the two-part gate both recorders
// read: the policy must be enabled, and a prerelease must not be held back.
// The specs answer identically, which is what lets the changelog and the
// GitHub release be configured the same way.
func TestRecordSpecsHoldPrereleasesBack(t *testing.T) {
	cases := []struct {
		name       string
		enabled    bool
		prerelease bool
		isPre      bool
		want       bool
	}{
		{"stable, all on", true, true, false, true},
		{"prerelease, all on", true, true, true, true},
		{"stable, prereleases held back", true, false, false, true},
		{"prerelease, prereleases held back", true, false, true, false},
		{"disabled outright", false, true, false, false},
		{"disabled beats the prerelease flag", false, true, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cl := ChangelogSpec{Enabled: c.enabled, Prerelease: c.prerelease}
			gh := GitHubSpec{Enabled: c.enabled, Prerelease: c.prerelease}
			assert.Equal(t, c.want, cl.Records(c.isPre))
			assert.Equal(t, c.want, gh.Records(c.isPre), "both specs answer alike")
		})
	}
}
