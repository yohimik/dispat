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
}

func TestDepKindString(t *testing.T) {
	assert.Equal(t, "dependencies", KindDependencies.String(), "the zero value spells itself out")
	assert.Equal(t, "devDependencies", KindDevDependencies.String())
	assert.Equal(t, "peerDependencies", KindPeerDependencies.String())
}
