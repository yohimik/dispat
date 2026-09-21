package integration

// Goal 7: the publication order and the skip cascade through a package that is
// not releasing.
//
// Both are taken over the whole workspace graph and restricted to the plan
// afterwards (SPEC 19.2, 19.3). The shape here is the commonest one an
// incremental release has: `app` depends on `ui`, `ui` depends on `core`, and
// `ui` has nothing to release, so the plan holds no edge at all between the two
// packages that do. What is proven is that `core` still publishes first, and
// that a `core` whose publish failed still stops `app`.
//
// The order claim is gated rather than slept: `core`'s publish waits for a file
// `app`'s build writes on its way out, so `app` is ready to publish while
// `core` has not begun, and "it published second" is something the run either
// did or could not do rather than something a timer happened to catch.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// publishOrderRepo is the three-package chain with the middle package left out
// of the plan. Every space keeps the default relation, because this claim is
// about the graph rather than about what a build reads: nothing a relation can
// say moves a publication out of dependency order.
//
// fixedGroup names the packages whose versions move together, which is how a
// consumer gets into the plan with no release reason of its own; an empty list
// leaves every package versioning on its own.
func publishOrderRepo(t *testing.T, libPublish string, fixedGroup []string) *harness.Repo {
	t.Helper()
	r := harness.New(t)
	gate := r.Path("app-build.gate")
	cfg := harness.BaseFile(3)
	cfg.Scripts = map[string]models.Script{
		"lib-build": {r.TsmarkScript("timeline.log", "$DISPAT_PACKAGE-build", 0)},
		// Held until the consumer's build has finished, so the consumer reaches
		// its publish first and the order it publishes in is the graph's answer
		// rather than the scheduler's timing.
		"lib-publish": {stageRelationGateWait(gate),
			r.TsmarkScript("timeline.log", "$DISPAT_PACKAGE-publish", 300*time.Millisecond)},
		"fail-publish": {stageRelationGateWait(gate), "exit 1"},
		"app-build": {r.TsmarkScript("timeline.log", "app-build", 200*time.Millisecond),
			"touch '" + gate + "'"},
		"app-publish": {r.TsmarkScript("timeline.log", "app-publish", 0)},
	}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: models.PathList{"packages/libs"},
			Flow: &models.SpaceFlowConfig{
				Build: []string{"lib-build"}, Publish: []string{libPublish}}},
		"apps": {Path: models.PathList{"packages/apps"},
			Flow: &models.SpaceFlowConfig{
				Build: []string{"app-build"}, Publish: []string{"app-publish"}}},
	}
	cfg.Dependencies = []models.DependencyConfig{
		{Consumer: "app", Provider: "ui"},
		{Consumer: "ui", Provider: "core"},
	}
	if len(fixedGroup) > 0 {
		cfg.VersionGroups = map[string]models.VersionGroupConfig{
			"platform": {Versioning: models.VersioningFixed}}
		cfg.Packages = map[string]models.PackageConfig{}
		for _, name := range fixedGroup {
			cfg.Packages[name] = models.PackageConfig{VersionGroup: "platform"}
		}
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages/libs", "core")
	r.SeedPackage("packages/libs", "ui")
	r.SeedPackage("packages/apps", "app")
	return r
}

// TestPublishOrderHoldsThroughAPackageThatIsNotReleasing: `app` resolves `core`
// through `ui` at install time, so publishing `app` first would record a range
// reconciled against a version nobody had published, and no later run could
// tell from ancestry that anything was wrong. The middle package having nothing
// to release changes none of that.
func TestPublishOrderHoldsThroughAPackageThatIsNotReleasing(t *testing.T) {
	r := publishOrderRepo(t, "lib-publish", nil)
	// No caret: the bumps reach nobody, so `ui` has nothing to release and the
	// two packages that do have no edge between them inside the plan.
	r.Commit("feat(core,app): each for its own reasons")

	res := r.ReleaseOK()
	require.Zerof(t, r.TagCount("ui@"), "ui is not in the plan; tags: %v", r.TagList())
	for _, name := range []string{"core", "app"} {
		require.Equalf(t, 1, r.TagCount(name+"@"), "%s released; tags: %v\nstdout:\n%s",
			name, r.TagList(), res.Stdout)
	}

	tl := r.Timeline("timeline.log")
	appBuild := harness.Find(t, tl, "app-build")
	corePublish := harness.Find(t, tl, "core-publish")

	// The gate is the evidence: the consumer had finished building, and so had
	// everything standing between it and its own publish, before the provider's
	// publish began.
	harness.AssertSequential(t, appBuild, corePublish)
	harness.AssertSequential(t, corePublish, harness.Find(t, tl, "app-publish"))
}

// TestPublishOrderBlocksAConsumerBehindAFailedProviderItDoesNotNameDirectly:
// the closure that blocks is the closure that orders. A consumer with no
// release reason of its own is skipped behind the provider that failed, named
// by `blockedBy` even though it names only the package between them; a consumer
// with a fresh reason of its own proceeds under the default relation, exactly
// as it does when the failed provider is one it declares.
func TestPublishOrderBlocksAConsumerBehindAFailedProviderItDoesNotNameDirectly(t *testing.T) {
	t.Run("a consumer riding a group release is blocked", func(t *testing.T) {
		// The only reason `app` is in the plan is the group its version is
		// fixed to, which is no reason to release it against a provider that
		// never published.
		r := publishOrderRepo(t, "fail-publish", []string{"app", "core"})
		r.Commit("feat(core): the group moves")

		res := r.Release()
		require.Equal(t, 1, res.Code, "the provider publish fails the run\nstdout:\n%s", res.Stdout)
		assert.True(t, harness.IsCodePresentForPackage(res.Events, "W194", "app"),
			"the consumer is skipped behind a provider it never names: %s", res.Stdout)
		assert.Equal(t, "core", publishOrderBlockedBy(t, res, "app"),
			"the sentence names the package that actually failed")
		assert.Zerof(t, r.TagCount("app@"),
			"nothing may publish behind a provider that never published; tags: %v", r.TagList())
	})

	t.Run("a consumer with work of its own proceeds", func(t *testing.T) {
		r := publishOrderRepo(t, "fail-publish", nil)
		r.Commit("feat(core,app): both carry work of their own")

		res := r.Release()
		require.Equal(t, 1, res.Code, "the provider still fails the run\nstdout:\n%s", res.Stdout)
		assert.False(t, harness.IsCodePresent(res.Events, "W194"),
			"a fresh own bump proceeds under a relation that does not block")
		assert.Equal(t, 1, r.TagCount("app@"), "tags: %v", r.TagList())
	})

	t.Run("a consumer whose declared provider published proceeds", func(t *testing.T) {
		// The other side of the same decision, and the reason the reached
		// providers are kept out of it: `app` carries no work of its own and is
		// in the plan only because both providers propagated to it. One of them
		// published, which is a version `app` really does pick up, so the other
		// one's failure does not withhold it.
		r := harness.New(t)
		cfg := harness.BaseFile(3)
		cfg.Scripts = map[string]models.Script{
			"build":        {echoBuild},
			"publish":      {"echo publishing"},
			"fail-publish": {"exit 1"},
		}
		cfg.Spaces = map[string]models.SpaceConfig{
			"libs": {Path: models.PathList{"packages/libs"},
				Flow: &models.SpaceFlowConfig{Build: []string{"build"}, Publish: []string{"publish"}}},
			"broken": {Path: models.PathList{"packages/broken"},
				Flow: &models.SpaceFlowConfig{Build: []string{"build"}, Publish: []string{"fail-publish"}}},
			"apps": {Path: models.PathList{"packages/apps"},
				Flow: &models.SpaceFlowConfig{Build: []string{"build"}, Publish: []string{"publish"}}},
		}
		cfg.Dependencies = []models.DependencyConfig{
			{Consumer: "app", Provider: "core"},
			{Consumer: "app", Provider: "lib"},
		}
		r.WriteConfigModel(cfg)
		r.SeedPackage("packages/libs", "core")
		r.SeedPackage("packages/broken", "lib")
		r.SeedPackage("packages/apps", "app")
		r.Commit("feat(core,lib)^: both providers reach the application")

		res := r.Release()
		require.Equal(t, 1, res.Code, "the failing provider fails the run\nstdout:\n%s", res.Stdout)
		assert.False(t, harness.IsCodePresentForPackage(res.Events, "W194", "app"),
			"a provider that published is a reason to release: %s", res.Stdout)
		assert.Equal(t, 1, r.TagCount("app@"), "tags: %v", r.TagList())
		assert.Zerof(t, r.TagCount("lib@"), "the failed provider published nothing; tags: %v", r.TagList())
	})
}

// publishOrderBlockedBy reads the `blockedBy` field of one package's summary
// line, which is what a CI log offers an operator in place of the release they
// expected.
func publishOrderBlockedBy(t *testing.T, res harness.RunResult, pkg string) string {
	t.Helper()
	for _, e := range res.Events {
		if e.Package() == pkg && e.Code() == "W194" && e.Str("blockedBy") != "" {
			return e.Str("blockedBy")
		}
	}
	t.Fatalf("no blocked summary line for %q\nstdout:\n%s", pkg, res.Stdout)
	return ""
}
