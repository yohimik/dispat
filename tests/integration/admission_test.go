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
// the consumer's postVersion hook writes, so "the provider died after the
// consumer's manifests were written" is something the run either did or could
// not do, rather than something a timer happened to catch.

import (
	"os"
	"strings"
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

// admissionShape is what one fixture of this file differs in.
//
// hasBuild decides which half of SPEC 19.2a's failure bullet the fixture
// exercises: with no build command the consumer has produced nothing that
// could embed the provider's planned version and is re-reconciled, and with
// one it has and is blocked instead. hasVersionScript decides which
// reconciling strategy the re-reconciliation has to redo: the native one
// alone, or the native one and the space's own `flow.version` scripts.
type admissionShape struct {
	hasBuild              bool
	hasVersionScript      bool
	nestedTag             bool
	bootstrapConsumerOnly bool
	bootstrapBuildTag     bool
	lateTagCollision      bool
}

// admissionRepo is the two-package workspace of vector 80d: `core` in a space
// whose publish fails unless this run says otherwise, and `cli` in a space
// that reconciles its manifest natively.
//
// The provider's publish waits on a gate the consumer's postVersion hook
// opens, which is after the native reconciliation has written the provider's
// planned version. That is what makes "the provider died after the consumer's
// manifests were written" the thing the run either did or could not do: opened
// from the version script instead, the gate would be a race against dispat's
// own reconciliation.
func admissionRepo(t *testing.T, shape admissionShape) *harness.Repo {
	t.Helper()
	r := harness.New(t)
	gate := r.Path("cli-versioned.gate")
	cfg := harness.BaseFile(2)
	cfg.Scripts = map[string]models.Script{
		"build": {"echo building $DISPAT_PACKAGE"},
		"core-publish": {stageRelationGateWait(gate),
			`if [ -n "$` + admissionProviderOK + `" ]; then echo published; else exit 1; fi`},
		"cli-version":      {"echo versioning $DISPAT_PACKAGE against ${DISPAT_UPDATED_PACKAGES:-nothing}"},
		"cli-post-version": {"touch '" + gate + "'"},
		"cli-publish":      {"echo publishing $DISPAT_PACKAGE at $DISPAT_NEW_VERSION"},
	}
	if shape.nestedTag {
		bin, _ := harness.Build(t)
		cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true),
			Name: "admission-test", Email: "admission@example.com"}
		cfg.Scripts["cli-publish"] = models.Script{bin + " commit --tag"}
	}
	if shape.lateTagCollision {
		cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true),
			Name: "admission-test", Email: "admission@example.com"}
		cfg.Scripts["tag-collision"] = models.Script{
			`if [ -n "$DISPAT_IT_ADMISSION_COLLIDE" ]; then git tag -a core@0.2.0 HEAD^ -m injected; fi`,
		}
		cfg.Run = &models.RunConfig{BeforeCommit: []string{"tag-collision"}}
	}
	consumerFlow := &models.SpaceFlowConfig{
		PostVersion: []string{"cli-post-version"}, Publish: []string{"cli-publish"}}
	if shape.hasBuild {
		consumerFlow.Build = []string{"build"}
	}
	if shape.hasVersionScript {
		consumerFlow.Version = []string{"cli-version"}
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
	if shape.bootstrapBuildTag {
		r.Commit("feat(core): bootstrap")
		require.NoError(t, os.WriteFile(gate, nil, 0o600))
		provider := r.CommandEnv([]string{admissionProviderOK + "=1"}, "--package", "core")
		require.Equal(t, 0, provider.Code, "provider bootstrap: %s", provider.Stdout)
		require.NoError(t, os.Remove(gate))
		r.Git("tag", "-d", "core@0.1.0")
		r.Git("tag", "-a", "core@0.1.0+build.7", "-m", "baseline with build metadata")
		r.Commit("feat(cli): bootstrap")
	} else if shape.bootstrapConsumerOnly {
		r.Commit("feat(cli): bootstrap")
	} else {
		r.Commit("feat(core,cli): bootstrap")
	}
	if shape.bootstrapConsumerOnly || shape.bootstrapBuildTag {
		res := r.CommandEnv([]string{admissionProviderOK + "=1"}, "--package", "cli")
		require.Equal(t, 0, res.Code, "consumer bootstrap: %s\n%s", res.Stdout, res.Stderr)
	} else {
		r.CommandEnv([]string{admissionProviderOK + "=1"}, "release")
		require.Contains(t, r.TagList(), "core@0.1.0")
	}
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
		// With version scripts as well, so the re-reconciliation has both
		// strategies to redo.
		r := admissionRepo(t, admissionShape{hasVersionScript: true})
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
		assert.Contains(t, res.Stdout, "versioning cli against nothing",
			"the version scripts are re-run with the dead provider out of DISPAT_UPDATED_*")
	})

	t.Run("the run that publishes the provider catches the consumer up", func(t *testing.T) {
		r := admissionRepo(t, admissionShape{})
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
		r := admissionRepo(t, admissionShape{})
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

// TestAdmissionCatchesUpAfterTheProviderShipsAlone keeps the consumer out of
// the provider's successful retry. Both tags can then point at the same
// commit, so a later invocation must remember which provider version the
// consumer actually shipped with rather than infer delivery from ancestry.
func TestAdmissionCatchesUpAfterTheProviderShipsAlone(t *testing.T) {
	r := admissionRepo(t, admissionShape{})
	r.Commit("feat(core)^: streaming\n\n---\n\nfeat(cli): own flag")
	require.NotEqual(t, 0, r.Release().Code)
	require.Equal(t, 1, r.TagCount("cli@0.2.0"), "the app shipped its own work")
	assert.Contains(t, admissionManifest(t, r), `"@acme/core": "^0.1.0"`)

	provider := r.CommandEnv([]string{admissionProviderOK + "=1"}, "--package", "core")
	require.Equal(t, 0, provider.Code, "provider-only retry: %s", provider.Stdout)
	require.Equal(t, 1, r.TagCount("core@0.2.0"))
	assert.Zero(t, r.TagCount("cli@0.2.1"), "consumer was absent from this run")

	catchUp := r.CommandEnv([]string{admissionProviderOK + "=1"}, "release")
	require.Equal(t, 0, catchUp.Code, "catch-up run: %s", catchUp.Stdout)
	require.Equal(t, 1, r.TagCount("cli@0.2.1"), "consumer must pick up core without a new commit")
	assert.Equal(t, 1, r.TagCount("core@0.2.0"), "core is never republished")
	assert.True(t, harness.IsCodePresentForPackage(catchUp.Events, "W193", "cli"))
	assert.Contains(t, admissionManifest(t, r), `"@acme/core": "^0.2.0"`)

	settled := r.Status("--require-release")
	assert.NotEqual(t, 0, settled.Code, "the catch-up must converge")
	assert.Contains(t, settled.Stdout, `"releasing":0`)
}

// TestAdmissionNestedTagRetainsProviderReceipt exercises a publish flow that
// calls the native step command. That command replans with the provider tag
// masked by its outer run; its immutable tag must still record what the outer
// run actually observed when the consumer shipped.
func TestAdmissionNestedTagRetainsProviderReceipt(t *testing.T) {
	r := admissionRepo(t, admissionShape{nestedTag: true})
	r.Commit("feat(core)^: streaming\n\n---\n\nfeat(cli): own flag")
	require.NotEqual(t, 0, r.Release().Code)
	require.Equal(t, 1, r.TagCount("cli@0.2.0"), "the app shipped its own work")

	provider := r.CommandEnv([]string{admissionProviderOK + "=1"}, "--package", "core")
	require.Equal(t, 0, provider.Code, "provider-only retry: %s", provider.Stdout)
	catchUp := r.CommandEnv([]string{admissionProviderOK + "=1"}, "release")
	require.Equal(t, 0, catchUp.Code, "catch-up run: %s", catchUp.Stdout)
	assert.Equal(t, 1, r.TagCount("cli@0.2.1"), "the nested tag must retain delivery evidence")
	assert.Equal(t, 1, r.TagCount("core@0.2.0"), "provider is never republished")
}

// TestAdmissionNestedOtherPackageKeepsItsOwnReceipt exercises an explicit
// --package target inside another package's publish script. The outer
// consumer's provider receipt must not be copied onto the unrelated tag.
func TestAdmissionNestedOtherPackageKeepsItsOwnReceipt(t *testing.T) {
	r := harness.New(t)
	bin, _ := harness.Build(t)
	cfg := harness.BaseFile(2)
	cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true),
		Name: "admission-test", Email: "admission@example.com"}
	cfg.Scripts = map[string]models.Script{
		"publish":      {"echo publishing $DISPAT_PACKAGE"},
		"nested-other": {`if [ -n "$DISPAT_IT_NEST_OTHER" ]; then ` + bin + ` commit --tag --package util; fi`},
	}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: models.PathList{"packages/libs"},
			Flow: &models.SpaceFlowConfig{Publish: []string{"publish"}}},
		"apps": {Path: models.PathList{"packages/apps"},
			Flow: &models.SpaceFlowConfig{Publish: []string{"nested-other"}}},
	}
	cfg.Dependencies = []models.DependencyConfig{{Consumer: "cli", Provider: "core"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages/libs", "core")
	r.SeedPackage("packages/apps", "cli")
	r.SeedPackage("packages/apps", "util")
	r.Commit("feat(core,cli,util): bootstrap")
	r.ReleaseOK()
	r.WriteFile("packages/libs/core/change.txt", "provider")
	r.WriteFile("packages/apps/cli/change.txt", "consumer")
	r.WriteFile("packages/apps/util/change.txt", "unrelated")
	r.Commit("feat(core)^: streaming\n\n---\n\nfeat(cli): own flag\n\n---\n\nfeat(util): own flag")
	provider := r.CommandEnv(nil, "--package", "core")
	require.Equal(t, 0, provider.Code, "provider release: %s", provider.Stdout)

	res := r.CommandEnv([]string{"DISPAT_IT_NEST_OTHER=1"}, "--package", "cli")
	require.Equal(t, 0, res.Code, "nested cross-package commit: %s\n%s", res.Stdout, res.Stderr)
	require.Equal(t, 1, r.TagCount("util@0.2.0"), "the explicit nested target must tag util; tags: %v\n%s", r.TagList(), res.Stdout)
	subject := strings.TrimSpace(r.Git("for-each-ref", "--format=%(contents:subject)", "refs/tags/util@0.2.0"))
	assert.Equal(t, "release util@0.2.0 dispat-seen-v1:e30", subject,
		"util has no provider sources and must not inherit cli's core observation")
}

// TestAdmissionCatchesUpFromAnUnreleasedProvider proves an empty observed tag
// is a real root boundary. The first provider release must not be mistaken
// for one the consumer could have picked up in its earlier own release.
func TestAdmissionCatchesUpFromAnUnreleasedProvider(t *testing.T) {
	r := admissionRepo(t, admissionShape{bootstrapConsumerOnly: true})
	r.Commit("feat(core)^: first published core\n\n---\n\nfeat(cli): own flag")
	require.NotEqual(t, 0, r.Release().Code)
	require.Equal(t, 1, r.TagCount("cli@0.2.0"))
	provider := r.CommandEnv([]string{admissionProviderOK + "=1"}, "--package", "core")
	require.Equal(t, 0, provider.Code, "provider-only retry: %s", provider.Stdout)
	catchUp := r.CommandEnv([]string{admissionProviderOK + "=1"}, "release")
	require.Equal(t, 0, catchUp.Code, "catch-up run: %s", catchUp.Stdout)
	assert.Equal(t, 1, r.TagCount("cli@0.2.1"))
	assert.Equal(t, 1, r.TagCount("core@0.1.0"), "provider is never republished")
}

// TestAdmissionReceiptKeepsTheExactProviderTag proves that a parseable tag
// with SemVer build metadata is recorded by its actual ref name. Rendering
// the parsed Version would drop +build.7 and cite a tag that never existed.
func TestAdmissionReceiptKeepsTheExactProviderTag(t *testing.T) {
	r := admissionRepo(t, admissionShape{bootstrapBuildTag: true})
	r.Commit("feat(core)^: streaming\n\n---\n\nfeat(cli): own flag")
	require.NotEqual(t, 0, r.Release().Code)
	require.Equal(t, 1, r.TagCount("cli@0.2.0"))
	provider := r.CommandEnv([]string{admissionProviderOK + "=1"}, "--package", "core")
	require.Equal(t, 0, provider.Code, "provider-only retry: %s", provider.Stdout)
	catchUp := r.CommandEnv([]string{admissionProviderOK + "=1"}, "release")
	require.Equal(t, 0, catchUp.Code, "exact-tag catch-up: %s", catchUp.Stdout)
	assert.Equal(t, 1, r.TagCount("cli@0.2.1"))
	assert.Equal(t, 1, r.TagCount("core@0.2.0"), "provider is never republished")
}

// TestAdmissionRefusesDamagedDeliveryEvidence prevents a damaged immutable
// receipt from silently turning a three-run catch-up back into the old
// ancestry-only interpretation.
func TestAdmissionRefusesDamagedDeliveryEvidence(t *testing.T) {
	t.Run("malformed receipt", func(t *testing.T) {
		r := admissionRepo(t, admissionShape{})
		commit := r.Git("rev-list", "-n1", "cli@0.1.0")
		r.Git("tag", "-d", "cli@0.1.0")
		r.Git("tag", "-a", "cli@0.1.0", commit, "-m", "release cli@0.1.0 dispat-seen-v1:!")
		status := r.Status()
		assert.NotEqual(t, 0, status.Code)
		assert.Contains(t, status.Stdout+status.Stderr, "invalid provider receipt")
	})
	t.Run("named provider tag missing", func(t *testing.T) {
		r := admissionRepo(t, admissionShape{})
		r.Commit("feat(core)^: streaming\n\n---\n\nfeat(cli): own flag")
		require.NotEqual(t, 0, r.Release().Code)
		require.Equal(t, 1, r.TagCount("cli@0.2.0"))
		r.Git("tag", "-d", "core@0.1.0")
		status := r.Status()
		assert.NotEqual(t, 0, status.Code)
		assert.Contains(t, status.Stdout+status.Stderr, "records missing provider tag core@0.1.0")
	})
}

// TestAdmissionFinalTagFailureCannotForgeDelivery covers deferred tagging:
// all publish scripts have succeeded, but a concurrent tag collision makes
// the provider's final record fail. The consumer must not receive a tag whose
// receipt claims that missing provider record was observed.
func TestAdmissionFinalTagFailureCannotForgeDelivery(t *testing.T) {
	r := admissionRepo(t, admissionShape{lateTagCollision: true})
	r.Commit("feat(core)^: streaming\n\n---\n\nfeat(cli): own flag")
	res := r.CommandEnv([]string{admissionProviderOK + "=1", "DISPAT_IT_ADMISSION_COLLIDE=1"}, "release")
	require.NotEqual(t, 0, res.Code, "a final tag collision is critical")
	assert.Contains(t, res.Stdout, "provider's tag is missing")
	assert.Equal(t, 0, r.TagCount("cli@0.2.0"), "consumer tag cannot carry false receipt")
	assert.Equal(t, 1, r.TagCount("core@0.2.0"), "collision fixture wrote the wrong tag")
}

// TestAdmissionCancelAfterProviderShipsAlone proves a consumer can discard
// the delayed pickup before it has published it, even though its own earlier
// release tag already contains the original source commit.
func TestAdmissionCancelAfterProviderShipsAlone(t *testing.T) {
	r := admissionRepo(t, admissionShape{})
	r.Commit("feat(core)^: streaming\n\n---\n\nfeat(cli): own flag")
	require.NotEqual(t, 0, r.Release().Code)
	require.Equal(t, 0, r.CommandEnv([]string{admissionProviderOK + "=1"}, "--package", "core").Code)
	r.CommitEmpty("cancel(cli): drop the delayed pickup")

	status := r.Status("--require-release")
	assert.NotEqual(t, 0, status.Code, "the cancel discards the owed release")
	assert.Contains(t, status.Stdout, `"releasing":0`)
	assert.Zero(t, r.TagCount("cli@0.2.1"))
}

// TestAdmissionProviderAloneDoesNotCreateAConsumerRelease covers the control:
// a provider's own version advancing without a propagation directive gives
// the consumer no automatic bump, even across the same three run sequence.
func TestAdmissionProviderAloneDoesNotCreateAConsumerRelease(t *testing.T) {
	r := admissionRepo(t, admissionShape{})
	r.Commit("feat(core): streaming\n\n---\n\nfeat(cli): own flag")
	require.NotEqual(t, 0, r.Release().Code)
	require.Equal(t, 0, r.CommandEnv([]string{admissionProviderOK + "=1"}, "--package", "core").Code)
	status := r.Status("--require-release")
	assert.NotEqual(t, 0, status.Code)
	assert.Contains(t, status.Stdout, `"releasing":0`)
	assert.Zero(t, r.TagCount("cli@0.2.1"))
}

// TestAdmissionBlocksAProceedingConsumerWhoseBuildEmbeddedThePlannedVersion is
// the conservative half of SPEC 19.2a's failure bullet. The consumer's version
// stage wrote the provider's planned version, its build then ran over the
// reconciled manifests, and only afterwards did the provider die: what the
// build produced may name a version nobody published, and dispat will neither
// publish it nor silently rebuild.
func TestAdmissionBlocksAProceedingConsumerWhoseBuildEmbeddedThePlannedVersion(t *testing.T) {
	r := admissionRepo(t, admissionShape{hasBuild: true})
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

// TestAdmissionCatchesUpAConsumerThatOvertookAHeldProvider is vector 82b1: the
// same debt arrived at by a hold rather than by a failure. A held provider
// propagates nothing, so the consumer releases for its own fix and tags past
// the provider's pending commit; the run that lifts the hold must release the
// provider and catch the consumer up.
func TestAdmissionCatchesUpAConsumerThatOvertookAHeldProvider(t *testing.T) {
	r := admissionRepo(t, admissionShape{})
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
	r := admissionRepo(t, admissionShape{})
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

// TestAdmissionOwesEveryConsumerThatOvertookOnePendingCommit is the same debt
// owed to two consumers at once, in a workspace where a never-released package
// keeps the union of pending windows spanning the provider's earlier release.
//
// That shape is what makes the delivery test do its whole job rather than its
// cheap half: the provider has a release the planner can see and that release
// does not carry the pending commit, so each consumer is asked about a real
// candidate and told no. Two consumers ask about the same provider, which is
// also the only way the candidate list is read twice.
func TestAdmissionOwesEveryConsumerThatOvertookOnePendingCommit(t *testing.T) {
	r := harness.New(t)
	gate := r.Path("consumers-versioned.gate")
	cfg := harness.BaseFile(3)
	cfg.Scripts = map[string]models.Script{
		// One command, because every entry of a script list runs in its own
		// shell: an `exit 0` in the first would end that shell, not the stage.
		"core-publish": {`if [ -n "$` + admissionProviderOK + `" ]; then echo published; else ` +
			stageRelationGateWait(gate) + `; exit 1; fi`},
		// The gate opens once both consumers have reconciled, which is what
		// puts the provider's failure after both version stages.
		"app-post-version": {"printf x >> '" + gate + ".marks'",
			`if [ "$(wc -c < '` + gate + `.marks' | tr -d ' ')" -ge 2 ]; then touch '` + gate + `'; fi`},
		"app-publish": {"echo publishing $DISPAT_PACKAGE"},
	}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: models.PathList{"packages/libs"},
			Flow: &models.SpaceFlowConfig{Publish: []string{"core-publish"}}},
		"apps": {Path: models.PathList{"packages/apps"},
			Flow: &models.SpaceFlowConfig{
				PostVersion: []string{"app-post-version"}, Publish: []string{"app-publish"}},
			AutoVersion: &models.AutoVersionConfig{Match: []string{"workspace:*", "^*"}}},
		// Never released and never committed to, so its window is the whole
		// history and the union keeps holding the provider's own release.
		"docs": {Path: models.PathList{"packages/docs"},
			Flow: &models.SpaceFlowConfig{Publish: []string{"app-publish"}}},
	}
	cfg.Dependencies = []models.DependencyConfig{
		{Consumer: "cli", Provider: "core"},
		{Consumer: "web", Provider: "core"},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages/libs", "core")
	r.SeedPackage("packages/docs", "guide")
	r.WriteFile("packages/libs/core/package.json", `{"name": "@acme/core", "version": "0.0.0"}`)
	for _, name := range []string{"cli", "web"} {
		r.SeedPackage("packages/apps", name)
		r.WriteFile("packages/apps/"+name+"/package.json", `{
  "name": "@acme/`+name+`",
  "version": "0.0.0",
  "dependencies": {"@acme/core": "workspace:*"}
}`)
	}
	r.Commit("feat(core,cli,web): bootstrap")
	bootstrap := r.CommandEnv([]string{admissionProviderOK + "=1"}, "release")
	require.Equal(t, 0, bootstrap.Code,
		"the bootstrap release is the one the provider survives; stdout:\n%s\nstderr:\n%s",
		bootstrap.Stdout, bootstrap.Stderr)
	require.Contains(t, r.TagList(), "core@0.1.0")
	require.Zero(t, r.TagCount("guide@"), "the third package never releases: %v", r.TagList())

	r.WriteFile("packages/libs/core/stream.txt", "streaming\n")
	r.Commit("feat(core)^: streaming\n\n---\n\nfeat(cli,web): own flag")
	require.NotEqual(t, 0, r.Release().Code, "the provider's publish fails")
	for _, name := range []string{"cli", "web"} {
		require.Equal(t, 1, r.TagCount(name+"@0.2.0"),
			"%s proceeds on its own bump; tags: %v", name, r.TagList())
	}
	require.Zero(t, r.TagCount("core@0.2.0"), "tags: %v", r.TagList())

	// Both consumers overtook the same pending commit, and the provider's one
	// visible release does not carry it, so both are still owed it.
	res := r.Status()
	require.Equal(t, 0, res.Code, "stdout:\n%s", res.Stdout)
	for _, name := range []string{"cli", "web"} {
		assert.Equal(t, "0.2.0 -> 0.2.1", harness.GraphLine(res.Events, name).Str("version"),
			"%s is planned again; stdout:\n%s", name, res.Stdout)
		assert.Equal(t, "propagated from core", harness.GraphLine(res.Events, name).Str("reason"))
	}
}
