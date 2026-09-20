package integration

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestPreviewRefusesAnIncompletePlan keeps the read-only command on the same
// safety boundary as status and release. A preview assembled without a
// valid publish order could advertise an impossible release and must therefore
// print neither partial notes nor the ordinary empty-plan message.
func TestPreviewRefusesAnIncompletePlan(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Dependencies = []models.DependencyConfig{
		{Consumer: "app", Provider: "core"},
		{Consumer: "core", Provider: "app"},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "app")
	r.Commit("feat(core,app): cyclic release graph")

	res := r.Command("preview")
	require.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	out := res.Stdout + res.Stderr
	assert.True(t, harness.IsCodePresent(res.Events, "E200"), "events: %#v", res.Events)
	assert.Contains(t, out, "refusing to preview")
	assert.NotContains(t, out, "## core@", "an incomplete plan must not render release notes")
	assert.NotContains(t, out, "no pending changes", "the failure must not look like an empty plan")
}

// TestPreviewExplainsAnExplicitChangelogChannelMismatch covers the distinction
// between a bare preview, which is useful as pending-note inspection, and an
// explicit record preview, which promises to show what that record will write.
func TestPreviewExplainsAnExplicitChangelogChannelMismatch(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Changelog = &models.ChangelogConfig{
		Enabled:  models.Bool(true),
		Channels: []string{"beta"},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): stable-only candidate")

	explicit := r.Command("preview", "--changelog", "--package", "core")
	require.Equal(t, 0, explicit.Code, "stderr:\n%s", explicit.Stderr)
	assert.Contains(t, explicit.Stdout, "changelog entry withheld: the channels do not admit stable")
	assert.NotContains(t, explicit.Stdout, "stable-only candidate",
		"an explicit record preview must not imply the withheld entry will be written")

	bare := r.Command("preview", "--package", "core")
	require.Equal(t, 0, bare.Code, "stderr:\n%s", bare.Stderr)
	assert.Contains(t, bare.Stdout, "stable-only candidate",
		"the flagless preview remains a pending-note inspection")
}

// TestSelectionTraceAccountsForEverySafetyNarrowing makes trace output useful
// when a filtered release is refused: it carries the exact dependency or
// group partition that produced the operator-facing warning.
func TestSelectionTraceAccountsForEverySafetyNarrowing(t *testing.T) {
	t.Run("dependency withholding", func(t *testing.T) {
		r := filterRepo(t)

		res := r.Release("--package", "web", "--strict", "--log-level", "trace")
		require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		out := res.Stdout + res.Stderr
		assert.Contains(t, out, "plan: narrowing withheld a package")
		assert.Contains(t, out, `"package":"web"`)
		assert.Contains(t, out, `"waitingFor":["core"]`)
		assert.Empty(t, r.TagList(), "trace inspection does not weaken strict refusal")
	})

	t.Run("version group split", func(t *testing.T) {
		r := groupRepo(t)

		res := r.Release("--package", "core", "--strict", "--log-level", "trace")
		require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		out := res.Stdout + res.Stderr
		assert.Contains(t, out, "plan: narrowing split a group")
		assert.Contains(t, out, `"group":"shared"`)
		assert.Contains(t, out, `"releasing":["core"]`)
		assert.Contains(t, out, `"leftBehind":["tool","web"]`)
		assert.Empty(t, r.TagList(), "trace inspection does not weaken strict refusal")
	})
}
