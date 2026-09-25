package app

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// TestEffectivePolicyLeavesTheOwnVersionToAutoSign: the policy a flag override
// starts from writes the package's own version unless the space's sign stage
// owns it, which is the same answer the config loader gives a block beside
// autoSign; a space's own block is cloned, never edited in place.
func TestEffectivePolicyLeavesTheOwnVersionToAutoSign(t *testing.T) {
	assert.True(t, effectivePolicy(&model.Space{}).WriteVersion, "the defaults write the own version")
	signed := &model.Space{AutoSign: &model.AutoSign{Manifests: model.ScopeRoot}}
	assert.False(t, effectivePolicy(signed).WriteVersion, "the sign stage owns the own version")

	stated := &model.AutoVersion{Range: "exact", WriteVersion: true}
	clone := effectivePolicy(&model.Space{AutoVersion: stated})
	clone.Range, clone.WriteVersion = "tilde", false
	assert.Equal(t, "exact", stated.Range, "the space's block is not edited")
	assert.True(t, stated.WriteVersion)
}
