package integration

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// Shell keys cannot distinguish punctuation in package names. A collision
// must be reported and keep one complete entry, rather than mix two packages.
func TestWorkspaceEnvironmentReportsSanitizedNameCollisions(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(libsConfig(`printf '%s|%s|%s' "$DISPAT_WORKSPACE_PACKAGES" "$DISPAT_WORKSPACE_CORE_UTILS_NAME" "$DISPAT_WORKSPACE_CORE_UTILS_VERSION" > workspace.txt`, 1))
	for _, name := range []string{"core-utils", "core.utils"} {
		r.SeedPackage("packages", name)
	}
	r.Commit("feat(core-utils): feature\n\n---\n\nfix(core.utils): fix")
	res := r.ReleaseOK()
	assert.Contains(t, res.Stdout, "key collides with")
	for _, name := range []string{"core-utils", "core.utils"} {
		contents, err := os.ReadFile(r.Path("packages", name, "workspace.txt"))
		require.NoError(t, err)
		assert.Equal(t, "CORE_UTILS|core-utils|0.1.0", string(contents))
	}
	assert.ElementsMatch(t, []string{"core-utils@0.1.0", "core.utils@0.0.1"}, r.TagList(), "an environment alias collision does not merge release identities")
}
