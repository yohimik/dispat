package integration

// Goal 1: the bump axis admits a provider's unit for a consumer until a
// release of the provider delivered it (SPEC 13.4a, 9.2, 13.7b), and a
// consumer that proceeds past a provider publishes what that provider actually
// published (SPEC 19.5).
//
// The shape is the one a partial release leaves behind: one commit carries the
// provider's caret and the consumer's own feature, the provider's publish
// fails, and the consumer proceeds on a reason of its own and tags past the
// commit. Asked only about its own window the consumer is then never planned
// again: it keeps the provider's previous version for ever and no diagnostic
// fires. The tests below assert the catch-up and the manifest, and assert the
// two non-conforming outcomes absent.
//
// Ordering is gated rather than slept. The provider's publish waits for a file
// the consumer's version stage writes, so "the provider died after the
// consumer's manifests were written" is something the run either did or could
// not do, rather than something a timer happened to catch.

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// admissionGate is the env variable a fixture's provider publish reads to
// decide whether this run is the one that succeeds. It is a DISPAT_IT_ name
// because those are the only variables the harness lets through to a run.
const admissionProviderOK = "DISPAT_IT_ADMISSION_CORE_OK"

// admissionRepo is the two-package workspace of vector 80d: `core` in a space
// whose publish fails unless this run says otherwise, and `cli` in a space
// that reconciles its manifest natively and records, from its version stage,
// that the stage ran.
//
// hasConsumerBuild decides which half of SPEC 19.2a's failure bullet the
// fixture exercises: with no build command the consumer has produced nothing
// that could embed the provider's planned version and is re-reconciled, and
// with one it has and is blocked instead.
func admissionRepo(t *testing.T, hasConsumerBuild bool) *harness.Repo {
	t.Helper()
	r := harness.New(t)
	gate := r.Path("cli-versioned.gate")
	cfg := harness.BaseFile(2)
	cfg.Scripts = map[string]models.Script{
		"build": {"echo building $DISPAT_PACKAGE"},
		// The provider's publish waits until the consumer's version stage has
		// reconciled its manifests, and only then decides this run's outcome.
		"core-publish": {stageRelationGateWait(gate),
			`if [ -n "$` + admissionProviderOK + `" ]; then echo published; else exit 1; fi`},
		"cli-version": {"touch '" + gate + "'"},
		"cli-publish": {"echo publishing $DISPAT_PACKAGE at $DISPAT_NEW_VERSION"},
	}
	consumerFlow := &models.SpaceFlowConfig{
		Version: []string{"cli-version"}, Publish: []string{"cli-publish"}}
	if hasConsumerBuild {
		consumerFlow.Build = []string{"build"}
	}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: models.PathList{"packages/libs"},
			Flow: &models.SpaceFlowConfig{
				Build: []string{"build"}, Publish: []string{"core-publish"}}},
		"apps": {Path: models.PathList{"packages/apps"},
			Flow: consumerFlow,
			AutoVersion: &models.AutoVersionConfig{
				Match: []string{"workspace:*", "^*"},
			}},
	}
	cfg.Dependencies = []models.DependencyConfig{{Consumer: "cli", Provider: "core"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages/libs", "core")
	r.SeedPackage("packages/apps", "cli")
	r.WriteFile("packages/libs/core/package.json", `{"name": "@acme/core", "version": "0.0.0"}`)
	r.WriteFile("packages/apps/cli/package.json", `{
  "name": "@acme/cli",
  "version": "0.0.0",
  "dependencies": {"@acme/core": "workspace:*"}
}`)
	// The bootstrap release, which gives both packages a baseline: without one
	// every window is the whole history and nothing can overtake anything.
	r.Commit("feat(core,cli): bootstrap")
	r.CommandEnv([]string{admissionProviderOK + "=1"}, "release")
	require.Contains(t, r.TagList(), "core@0.1.0")
	require.Contains(t, r.TagList(), "cli@0.1.0")
	return r
}

// admissionManifest reads the consumer's manifest as the working tree holds it
// after a run.
func admissionManifest(t *testing.T, r *harness.Repo) string {
	t.Helper()
	data, err := os.ReadFile(r.Path("packages", "apps", "cli", "package.json"))
	require.NoError(t, err)
	return string(data)
}

// TestAdmissionCatchesUpAConsumerThatProceededOnItsOwn is vector 80d. One
// commit carries `feat(core)^` and `feat(cli)`; the provider's publish fails
// and the consumer proceeds, because a fresh bump of its own is a cause no
// failed provider can invalidate. What it publishes names the provider's
// BASELINE, and the next run that publishes the provider catches it up.
func TestAdmissionCatchesUpAConsumerThatProceededOnItsOwn(t *testing.T) {
	t.Run("the consumer proceeds naming the provider's baseline", func(t *testing.T) {
		r := admissionRepo(t, false)
		r.Commit("feat(core)^: streaming\n\n---\n\nfeat(cli): own flag")

		res := r.Release()
		require.NotEqual(t, 0, res.Code, "the provider's publish failed, so the run failed")
		assert.Zero(t, r.TagCount("core@0.2.0"), "the provider published nothing")
		require.Equal(t, 1, r.TagCount("cli@0.2.0"),
			"the consumer has a fresh bump of its own and proceeds; tags: %v\nstdout:\n%s",
			r.TagList(), res.Stdout)
		assert.False(t, harness.IsCodePresentForPackage(res.Events, "W194", "cli"),
			"a consumer with a cause of its own under the default relation is not blocked")

		manifest := admissionManifest(t, r)
		assert.Contains(t, manifest, `"@acme/core": "^0.1.0"`,
			"the published manifest names what the provider published (SPEC 19.5)")
		assert.NotContains(t, manifest, `"@acme/core": "^0.2.0"`,
			"a manifest naming a version that was never published must not be published")
	})

	t.Run("the run that publishes the provider catches the consumer up", func(t *testing.T) {
		r := admissionRepo(t, false)
		r.Commit("feat(core)^: streaming\n\n---\n\nfeat(cli): own flag")
		require.NotEqual(t, 0, r.Release().Code)

		// Nothing new is committed: what makes the consumer releasable again is
		// the contribution the provider still owes it.
		res := r.CommandEnv([]string{admissionProviderOK + "=1"}, "release")
		require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		require.Equal(t, 1, r.TagCount("core@0.2.0"), "tags: %v", r.TagList())
		require.Equal(t, 1, r.TagCount("cli@0.2.1"),
			"the consumer is planned again and bumped for the delivery; tags: %v\nstdout:\n%s",
			r.TagList(), res.Stdout)
		assert.Contains(t, res.Stdout, `"dueToProviders":["core"]`,
			"the owed source is what explains the release; events:\n%s", res.Stdout)
		assert.Contains(t, admissionManifest(t, r), `"@acme/core": "^0.2.0"`,
			"the catch-up is what moves the range")

		// And it converges (SPEC 19.6): a failure-free run leaves nothing.
		third := r.CommandEnv([]string{admissionProviderOK + "=1"}, "release")
		require.Equal(t, 0, third.Code, "stdout:\n%s", third.Stdout)
		assert.Equal(t, 2, r.TagCount("core@"), "nothing was released a second time: %v", r.TagList())
		assert.Equal(t, 3, r.TagCount("cli@"), "tags: %v", r.TagList())
	})

	t.Run("a provider that fails again blocks the catch-up", func(t *testing.T) {
		r := admissionRepo(t, false)
		r.Commit("feat(core)^: streaming\n\n---\n\nfeat(cli): own flag")
		require.NotEqual(t, 0, r.Release().Code)

		// The consumer is admitted again, and this time the provider is its
		// only cause, so it is blocked rather than released against a version
		// that still does not exist.
		res := r.Release()
		require.NotEqual(t, 0, res.Code)
		assert.Zero(t, r.TagCount("cli@0.2.1"), "tags: %v", r.TagList())
		assert.True(t, harness.IsCodePresentForPackage(res.Events, "W194", "cli"),
			"every admitted cause of the catch-up comes from the failure; events:\n%s", res.Stdout)
	})
}

// TestAdmissionBlocksAProceedingConsumerWhoseBuildEmbeddedThePlannedVersion is
// the conservative half of SPEC 19.2a's failure bullet. The consumer's version
// stage wrote the provider's planned version, its build then ran over the
// reconciled manifests, and only afterwards did the provider die: what the
// build produced may name a version nobody published, and dispat will neither
// publish it nor silently rebuild.
func TestAdmissionBlocksAProceedingConsumerWhoseBuildEmbeddedThePlannedVersion(t *testing.T) {
	r := admissionRepo(t, true)
	r.Commit("feat(core)^: streaming\n\n---\n\nfeat(cli): own flag")

	res := r.Release()
	require.NotEqual(t, 0, res.Code)
	assert.Zero(t, r.TagCount("cli@0.2.0"),
		"the consumer had a cause of its own but its build is already stale; tags: %v", r.TagList())
	require.True(t, harness.IsCodePresentForPackage(res.Events, "W194", "cli"),
		"events:\n%s", res.Stdout)
	assert.Contains(t, res.Stdout, "already embedded its planned version",
		"the reason says which of the two blocking rules applied")
}

// TestAdmissionCatchesUpAConsumerThatOvertookAHeldProvider is vector 82c: the
// same debt arrived at by a hold rather than by a failure. A held provider
// propagates nothing, so the consumer releases for its own fix and tags past
// the provider's pending commit; the run that lifts the hold must release the
// provider and catch the consumer up.
func TestAdmissionCatchesUpAConsumerThatOvertookAHeldProvider(t *testing.T) {
	r := admissionRepo(t, false)
	r.WriteFile("packages/libs/core/stream.txt", "streaming\n")
	r.Commit("feat(core)^: streaming")
	r.WriteFile("packages/libs/core/hold.txt", "not yet\n")
	r.Commit("chore(core): not yet\n\nRelease-As: none")
	r.WriteFile("packages/apps/cli/fix.txt", "own fix\n")
	r.Commit("fix(cli): own fix")

	first := r.CommandEnv([]string{admissionProviderOK + "=1"}, "release")
	require.Equal(t, 0, first.Code, "stdout:\n%s", first.Stdout)
	require.Equal(t, 1, r.TagCount("cli@0.1.1"),
		"the consumer releases for its own fix; tags: %v\nstdout:\n%s", r.TagList(), first.Stdout)
	require.Equal(t, 1, r.TagCount("core@"), "the provider is held; tags: %v", r.TagList())
	assert.Contains(t, admissionManifest(t, r), `"@acme/core": "^0.1.0"`,
		"a held provider hands the consumer nothing to name but its baseline")

	r.WriteFile("packages/libs/core/ship.txt", "ship it\n")
	r.Commit("chore(core): ship it\n\nRelease-As: auto")
	second := r.CommandEnv([]string{admissionProviderOK + "=1"}, "release")
	require.Equal(t, 0, second.Code, "stdout:\n%s", second.Stdout)
	require.Equal(t, 1, r.TagCount("core@0.2.0"), "the hold is lifted; tags: %v", r.TagList())
	require.Equal(t, 1, r.TagCount("cli@0.1.2"),
		"the consumer overtook the commit and is caught up; tags: %v\nstdout:\n%s",
		r.TagList(), second.Stdout)
	assert.Contains(t, admissionManifest(t, r), `"@acme/core": "^0.2.0"`)
}

// TestAdmissionLeavesPlansWithoutOvertakingUnchanged is the other side of the
// rule, and the one a false positive would show up in: nothing is admitted
// that a release of the provider has already delivered.
//
// The sequence is the ordinary one. The provider releases alone, so the
// consumer's manifest falls behind; the consumer's own next change reconciles
// it and releases; and a third run finds nothing, because the provider's
// release carried the commit and the consumer's release reached it. A delivery
// test that mistook that for a debt would plan the consumer for ever.
func TestAdmissionLeavesPlansWithoutOvertakingUnchanged(t *testing.T) {
	r := admissionRepo(t, false)
	env := []string{admissionProviderOK + "=1"}

	r.WriteFile("packages/libs/core/stream.txt", "streaming\n")
	r.Commit("feat(core)^: streaming")
	first := r.CommandEnv(env, "release")
	require.Equal(t, 0, first.Code, "stdout:\n%s", first.Stdout)
	require.Equal(t, 1, r.TagCount("core@0.2.0"), "tags: %v", r.TagList())
	require.Equal(t, 1, r.TagCount("cli@0.1.1"),
		"the caret reaches the consumer in the same run; tags: %v", r.TagList())

	r.WriteFile("packages/apps/cli/fix.txt", "own fix\n")
	r.Commit("fix(cli): own fix")
	second := r.CommandEnv(env, "release")
	require.Equal(t, 0, second.Code, "stdout:\n%s", second.Stdout)
	require.Equal(t, 1, r.TagCount("cli@0.1.2"), "tags: %v", r.TagList())
	assert.Equal(t, 2, r.TagCount("core@"), "the provider is not re-released: %v", r.TagList())

	// The delivery is discharged, so the third run plans nothing at all: the
	// gate a release pipeline stands on refuses precisely because there is
	// nothing left to release.
	status := r.Status("--require-release")
	assert.NotEqual(t, 0, status.Code, "stdout:\n%s", status.Stdout)
	assert.Contains(t, status.Stdout, `"releasing":0`,
		"the provider's release delivered its commit, so nothing is owed")
	assert.Contains(t, status.Stdout, `"package":"cli","space":"apps","dependsOn":["core"],"version":"0.1.2"`)
}
