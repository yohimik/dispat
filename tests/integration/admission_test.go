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
// Delivery is read from tags and ancestry alone. When the provider publishes
// in a run the consumer sat out, the owed window of SPEC 13.3 keeps the commit
// visible.
//
// Ordering is gated rather than slept. The provider's publish waits for a file
// the consumer's postVersion hook writes, so "the provider died after the
// consumer's manifests were written" is something the run either did or could
// not do, rather than something a timer happened to catch.

import (
	"encoding/base64"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// admissionProviderOK is the env variable a fixture's provider publish reads to
// decide whether this run is the one that succeeds, and admissionConsumerFails
// the one that makes the consumer's publish fail. They are DISPAT_IT_ names
// because those are the only variables the harness lets through to a run.
const (
	admissionProviderOK    = "DISPAT_IT_ADMISSION_CORE_OK"
	admissionConsumerFails = "DISPAT_IT_ADMISSION_CLI_FAIL"
)

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
func admissionRepo(t *testing.T, shape admissionShape, adjust ...func(*models.File)) *harness.Repo {
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
		"cli-publish": {`if [ -n "$` + admissionConsumerFails + `" ]; then exit 1; fi`,
			"echo publishing $DISPAT_PACKAGE at $DISPAT_NEW_VERSION"},
	}
	if shape.nestedTag {
		bin, _ := harness.Build(t)
		cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true),
			Name: "admission-test", Email: "admission@example.com"}
		cfg.Scripts["cli-publish"] = models.Script{bin + " commit --tag"}
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
	for _, configure := range adjust {
		configure(&cfg)
	}
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
	if shape.bootstrapConsumerOnly {
		r.Commit("feat(cli): bootstrap")
		res := r.CommandEnv([]string{admissionProviderOK + "=1"}, "--package", "cli")
		require.Equal(t, 0, res.Code, "consumer bootstrap: %s\n%s", res.Stdout, res.Stderr)
	} else {
		r.Commit("feat(core,cli): bootstrap")
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

// admissionProceeded is vector 80d's first run: the provider's publish of the
// commit fails and the consumer proceeds at it on its own feature.
func admissionProceeded(t *testing.T, shape admissionShape, adjust ...func(*models.File)) *harness.Repo {
	t.Helper()
	r := admissionRepo(t, shape, adjust...)
	r.Commit("feat(core)^: streaming\n\n---\n\nfeat(cli): own flag")
	require.NotEqual(t, 0, r.Release().Code, "the provider's publish failed, so the run failed")
	require.Equal(t, 1, r.TagCount("cli@0.2.0"), "the consumer proceeded on its own work; tags: %v", r.TagList())
	return r
}

// assertAdmissionSettled asserts that a failure-free plan releases nothing:
// the catch-up converged (SPEC 19.6).
func assertAdmissionSettled(t *testing.T, r *harness.Repo) {
	t.Helper()
	settled := r.Status("--require-release")
	assert.NotEqual(t, 0, settled.Code, "the catch-up converges; stdout:\n%s", settled.Stdout)
	assert.Contains(t, settled.Stdout, `"releasing":0`)
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

// TestAdmissionFailedReconciliationWithholdsConsumerPublication: when a
// provider fails after the consumer's version stage, reverting its planned
// input version is a publication prerequisite. Both native and script failures
// withhold the consumer's upload; repairing the fault lets each package publish
// exactly once at the still-pending version.
func TestAdmissionFailedReconciliationWithholdsConsumerPublication(t *testing.T) {
	for _, failure := range []string{"native", "script"} {
		t.Run(failure, func(t *testing.T) {
			r := admissionRepo(t, admissionShape{hasVersionScript: true}, func(cfg *models.File) {
				cfg.Scripts["core-publish"] = models.Script{
					cfg.Scripts["core-publish"][0],
					`if [ "$DISPAT_IT_RECONCILE_FAILURE" = native ]; then chmod 555 ../../apps/cli; fi`,
					cfg.Scripts["core-publish"][1],
				}
				cfg.Scripts["cli-version"] = append(cfg.Scripts["cli-version"],
					`if [ "$DISPAT_IT_RECONCILE_FAILURE" = script ] && [ -z "$DISPAT_UPDATED_PACKAGES" ]; then exit 12; fi`)
			})
			consumer := r.Path("packages", "apps", "cli")
			t.Cleanup(func() { _ = os.Chmod(consumer, 0o755) })
			if failure == "native" {
				require.NoError(t, os.Chmod(consumer, 0o555))
				probeErr := os.WriteFile(r.Path("packages", "apps", "cli", "probe"), []byte("x"), 0o600)
				require.NoError(t, os.Chmod(consumer, 0o755))
				if probeErr == nil {
					t.Skip("this user can write into a 0555 directory")
				}
				require.True(t, os.IsPermission(probeErr), "fixture requires permission denial: %v", probeErr)
			}
			r.Commit("feat(core)^: streaming\n\n---\n\nfeat(cli): own flag")
			require.NoError(t, os.Remove(r.Path("cli-versioned.gate")), "the failed publish must wait for this run’s version stage")
			failed := r.CommandEnv([]string{"DISPAT_IT_RECONCILE_FAILURE=" + failure}, "release")
			require.Equal(t, 1, failed.Code, "stdout:\n%s\nstderr:\n%s", failed.Stdout, failed.Stderr)
			assert.Contains(t, failed.Stdout, "reconciling to the providers that published failed")
			assert.NotContains(t, failed.Stdout, "publishing cli at 0.2.0")
			assert.Zero(t, r.TagCount("core@0.2.0"))
			assert.Zero(t, r.TagCount("cli@0.2.0"))

			require.NoError(t, os.Chmod(consumer, 0o755))
			retried := r.CommandEnv([]string{admissionProviderOK + "=1"}, "release")
			require.Equal(t, 0, retried.Code, "stdout:\n%s\nstderr:\n%s", retried.Stdout, retried.Stderr)
			assert.Equal(t, 1, r.TagCount("core@0.2.0"))
			assert.Equal(t, 1, r.TagCount("cli@0.2.0"))
			assert.Contains(t, admissionManifest(t, r), `"@acme/core": "^0.2.0"`)
		})
	}
}

// TestAdmissionCatchesUpAfterTheProviderShipsAlone keeps the consumer out of
// the provider's successful retry at a later commit. The consumer's baseline
// reaches no release of the provider carrying the commit it is owed, so the
// owed window keeps the commit in the plan and a run with no new commit
// catches the consumer up once. Delivery is read from tags and ancestry, so a
// lightweight consumer tag is read exactly like an annotated one.
func TestAdmissionCatchesUpAfterTheProviderShipsAlone(t *testing.T) {
	env := []string{admissionProviderOK + "=1"}
	for name, isLightweight := range map[string]bool{"annotated consumer tag": false, "lightweight consumer tag": true} {
		t.Run(name, func(t *testing.T) {
			r := admissionProceeded(t, admissionShape{})
			assert.Contains(t, admissionManifest(t, r), `"@acme/core": "^0.1.0"`)
			if isLightweight {
				commit := r.Git("rev-list", "-n1", "cli@0.2.0")
				r.Git("tag", "-d", "cli@0.2.0")
				r.Git("tag", "cli@0.2.0", commit)
				require.Equal(t, "commit", r.Git("cat-file", "-t", "cli@0.2.0"), "the tag is lightweight")
			}
			r.CommitEmpty("chore(core): retry the provider")
			provider := r.CommandEnv(env, "--package", "core")
			require.Equal(t, 0, provider.Code, "provider-only retry: %s", provider.Stdout)
			require.Equal(t, 1, r.TagCount("core@0.2.0"))
			assert.Zero(t, r.TagCount("cli@0.2.1"), "the consumer was absent from this run")

			catchUp := r.CommandEnv(env, "release")
			require.Equal(t, 0, catchUp.Code, "catch-up run: %s", catchUp.Stdout)
			require.Equal(t, 1, r.TagCount("cli@0.2.1"), "the consumer picks up core without a new commit")
			assert.Equal(t, 1, r.TagCount("core@0.2.0"), "core is never republished")
			assert.True(t, harness.IsCodePresentForPackage(catchUp.Events, "W193", "cli"))
			assert.Contains(t, admissionManifest(t, r), `"@acme/core": "^0.2.0"`)
			assertAdmissionSettled(t, r)
		})
	}
}

// TestAdmissionCatchesUpAHeldConsumerAfterItsProviderShipped: the consumer is
// held when its provider finally publishes. The hold commit moved the head
// past the consumer's release, so the provider may go, the hold withholds the
// consumer's version and reports it, and the run that lifts the hold catches
// the consumer up.
func TestAdmissionCatchesUpAHeldConsumerAfterItsProviderShipped(t *testing.T) {
	env := []string{admissionProviderOK + "=1"}
	r := admissionProceeded(t, admissionShape{})
	r.CommitEmpty("release(cli): not yet\n\nRelease-As: none")
	held := r.CommandEnv(env, "release")
	require.Equal(t, 0, held.Code, "stdout:\n%s\nstderr:\n%s", held.Stdout, held.Stderr)
	require.Equal(t, 1, r.TagCount("core@0.2.0"), "the provider ships; tags: %v", r.TagList())
	assert.Zero(t, r.TagCount("cli@0.2.1"), "the held consumer does not")
	assert.True(t, harness.IsCodePresentForPackage(held.Events, "W154", "cli"),
		"the hold reports the version it withholds; stdout:\n%s", held.Stdout)

	r.CommitEmpty("release(cli): resume\n\nRelease-As: auto")
	resumed := r.CommandEnv(env, "release")
	require.Equal(t, 0, resumed.Code, "stdout:\n%s\nstderr:\n%s", resumed.Stdout, resumed.Stderr)
	assert.Equal(t, 1, r.TagCount("cli@0.2.1"), "tags: %v", r.TagList())
	assert.Equal(t, 1, r.TagCount("core@0.2.0"), "the provider is never republished")
	assert.True(t, harness.IsCodePresentForPackage(resumed.Events, "W193", "cli"))
	assert.Contains(t, admissionManifest(t, r), `"@acme/core": "^0.2.0"`)
	assertAdmissionSettled(t, r)
}

// TestAdmissionCatchesUpAConsumerThatFailedAfterItsProviderShipped: the
// retry at a later commit releases both and the consumer fails after the
// provider published. The provider's tag sits past the consumer's release, so
// nothing is reported, the debt stays visible, and the next run catches the
// consumer up at the version the failed run planned.
func TestAdmissionCatchesUpAConsumerThatFailedAfterItsProviderShipped(t *testing.T) {
	r := admissionProceeded(t, admissionShape{})
	r.CommitEmpty("chore(core): retry the provider")
	failed := r.CommandEnv([]string{admissionProviderOK + "=1", admissionConsumerFails + "=1"}, "release")
	require.NotEqual(t, 0, failed.Code, "stdout:\n%s", failed.Stdout)
	require.Equal(t, 1, r.TagCount("core@0.2.0"), "tags: %v", r.TagList())
	assert.Zero(t, r.TagCount("cli@0.2.1"))

	status := r.StatusOK()
	assert.Equal(t, "0.2.0 -> 0.2.1", harness.GraphLine(status.Events, "cli").Str("version"),
		"the version the failed run planned; stdout:\n%s", status.Stdout)
	assert.True(t, harness.IsCodePresentForPackage(status.Events, "W193", "cli"))

	catchUp := r.CommandEnv([]string{admissionProviderOK + "=1"}, "release")
	require.Equal(t, 0, catchUp.Code, "stdout:\n%s\nstderr:\n%s", catchUp.Stdout, catchUp.Stderr)
	assert.Equal(t, 1, r.TagCount("cli@0.2.1"), "tags: %v", r.TagList())
	assert.Equal(t, 1, r.TagCount("core@0.2.0"), "the provider is never republished")
	assert.Contains(t, admissionManifest(t, r), `"@acme/core": "^0.2.0"`)
	assertAdmissionSettled(t, r)
}

// TestAdmissionCatchesUpAConsumerThatProceededAtALaterCommit: the consumer did
// not overtake its provider at the provider's own commit but a commit later,
// on a change of its own while the provider still failed. The provider then
// publishes in a run the consumer sat out, and the consumer is still owed it.
func TestAdmissionCatchesUpAConsumerThatProceededAtALaterCommit(t *testing.T) {
	env := []string{admissionProviderOK + "=1"}
	r := admissionRepo(t, admissionShape{})
	r.WriteFile("packages/libs/core/stream.txt", "streaming\n")
	r.Commit("feat(core)^: streaming")
	first := r.Release()
	require.NotEqual(t, 0, first.Code, "the provider fails")
	assert.Zero(t, r.TagCount("cli@0.1.1"), "the consumer's only cause is the failed provider")

	r.WriteFile("packages/apps/cli/flag.txt", "own flag\n")
	r.Commit("feat(cli): own flag")
	second := r.Release()
	require.NotEqual(t, 0, second.Code, "the provider fails again")
	require.Equal(t, 1, r.TagCount("cli@0.2.0"), "the consumer proceeds on its own work; tags: %v", r.TagList())
	assert.Contains(t, admissionManifest(t, r), `"@acme/core": "^0.1.0"`)

	r.CommitEmpty("chore(core): retry the provider")
	provider := r.CommandEnv(env, "--package", "core")
	require.Equal(t, 0, provider.Code, "stdout:\n%s", provider.Stdout)
	require.Equal(t, 1, r.TagCount("core@0.2.0"))
	assert.Zero(t, r.TagCount("cli@0.2.1"))

	catchUp := r.CommandEnv(env, "release")
	require.Equal(t, 0, catchUp.Code, "stdout:\n%s\nstderr:\n%s", catchUp.Stdout, catchUp.Stderr)
	assert.Equal(t, 1, r.TagCount("cli@0.2.1"), "tags: %v", r.TagList())
	assert.True(t, harness.IsCodePresentForPackage(catchUp.Events, "W193", "cli"))
	assert.Contains(t, admissionManifest(t, r), `"@acme/core": "^0.2.0"`)
	assertAdmissionSettled(t, r)
}

// TestAdmissionReadsTagsCarryingAReceiptAsOrdinaryReleases: release tags
// written by 1.11.0-rc.5 carry `release <tag> dispat-seen-v1:<payload>` as
// their message. That text is ordinary tag text: whatever the payload says,
// and whether it decodes at all, the tag is a release at its commit and the
// consumer is owed what tags and ancestry say it is owed.
func TestAdmissionReadsTagsCarryingAReceiptAsOrdinaryReleases(t *testing.T) {
	r := admissionProceeded(t, admissionShape{})
	r.CommitEmpty("chore(core): retry the provider")
	provider := r.CommandEnv([]string{admissionProviderOK + "=1"}, "--package", "core")
	require.Equal(t, 0, provider.Code, "stdout:\n%s", provider.Stdout)
	commit := r.Git("rev-list", "-n1", "cli@0.2.0")
	encoded := func(receipt string) string { return base64.RawURLEncoding.EncodeToString([]byte(receipt)) }
	for _, row := range []struct{ name, payload string }{
		{"the receipt rc.5 wrote", encoded(`{"core":"core@0.1.0"}`)},
		{"a malformed payload", "!"},
		{"a provider the workspace does not have", encoded(`{"ghost":"ghost@0.1.0"}`)},
		{"a forged delivery", encoded(`{"core":"core@0.2.0"}`)},
	} {
		t.Run(row.name, func(t *testing.T) {
			r.Git("tag", "-d", "cli@0.2.0")
			r.Git("tag", "-a", "cli@0.2.0", commit, "-m", "release cli@0.2.0 dispat-seen-v1:"+row.payload)
			status := r.StatusOK()
			assert.Equal(t, "0.2.0 -> 0.2.1", harness.GraphLine(status.Events, "cli").Str("version"),
				"stdout:\n%s", status.Stdout)
			assert.True(t, harness.IsCodePresentForPackage(status.Events, "W193", "cli"))
		})
	}
}

// TestAdmissionNestedTagCatchesUpAfterTheProviderShipsAlone exercises a publish
// flow that calls the native step command in commit mode: the consumer's
// nested `dispat commit --tag` writes its release commit and tags it, and the
// outer run records a changelog in a commit of its own, which moves the head
// past the consumer's tag. The provider then ships alone, and the consumer
// catches up once.
func TestAdmissionNestedTagCatchesUpAfterTheProviderShipsAlone(t *testing.T) {
	env := []string{admissionProviderOK + "=1"}
	r := admissionProceeded(t, admissionShape{nestedTag: true})
	require.NotEqual(t, r.Git("rev-parse", "HEAD"), r.Git("rev-list", "-n1", "cli@0.2.0"),
		"history:\n%s", r.Git("log", "--oneline", "--decorate", "-4"))

	provider := r.CommandEnv(env, "--package", "core")
	require.Equal(t, 0, provider.Code, "provider-only retry: %s", provider.Stdout)
	catchUp := r.CommandEnv(env, "release")
	require.Equal(t, 0, catchUp.Code, "catch-up run: %s", catchUp.Stdout)
	assert.Equal(t, 1, r.TagCount("cli@0.2.1"), "tags: %v", r.TagList())
	assert.Equal(t, 1, r.TagCount("core@0.2.0"), "the provider is never republished")
	assertAdmissionSettled(t, r)
}

// TestAdmissionCatchesUpFromAnUnreleasedProvider: the consumer released before
// the provider ever did, so its baseline reaches no release of the provider
// and the owed window is the whole history. The provider's first release,
// alone at a later commit, is still owed to the consumer's earlier release.
func TestAdmissionCatchesUpFromAnUnreleasedProvider(t *testing.T) {
	env := []string{admissionProviderOK + "=1"}
	r := admissionRepo(t, admissionShape{bootstrapConsumerOnly: true})
	r.Commit("feat(core)^: first published core\n\n---\n\nfeat(cli): own flag")
	require.NotEqual(t, 0, r.Release().Code)
	require.Equal(t, 1, r.TagCount("cli@0.2.0"))
	r.CommitEmpty("chore(core): retry the provider")
	provider := r.CommandEnv(env, "--package", "core")
	require.Equal(t, 0, provider.Code, "provider-only retry: %s", provider.Stdout)
	catchUp := r.CommandEnv(env, "release")
	require.Equal(t, 0, catchUp.Code, "catch-up run: %s", catchUp.Stdout)
	assert.Equal(t, 1, r.TagCount("cli@0.2.1"), "tags: %v", r.TagList())
	assert.Equal(t, 1, r.TagCount("core@0.1.0"), "the provider is never republished")
	assertAdmissionSettled(t, r)
}

// TestAdmissionCancelAfterProviderShipsAlone proves a consumer can discard
// the delayed pickup before it has published it, even though its own earlier
// release tag already contains the original source commit.
func TestAdmissionCancelAfterProviderShipsAlone(t *testing.T) {
	r := admissionProceeded(t, admissionShape{})
	r.CommitEmpty("chore(core): retry the provider")
	require.Equal(t, 0, r.CommandEnv([]string{admissionProviderOK + "=1"}, "--package", "core").Code)
	r.CommitEmpty("cancel(cli): drop the delayed pickup")

	status := r.Status("--require-release")
	assert.NotEqual(t, 0, status.Code, "the cancel discards the owed release")
	assert.Contains(t, status.Stdout, `"releasing":0`)
	assert.False(t, harness.IsCodePresent(status.Events, "W170"), "the cancel discarded the pickup")
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
