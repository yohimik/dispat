package integration

// Area 2: the dependency graph must drive script execution order, not just
// task existence. architecture.md's "Task graph" section makes three
// separate promises, checked one at a time:
//
//   - a consumer's version/build stage waits on every changed provider's
//     build (always), and additionally on the provider's publish when the
//     provider's space sets isBuildWaitingPublish (opt-in);
//   - a consumer's publish always waits on every changed provider's
//     publish, regardless of that flag — publishing against an unpublished
//     provider version is invalid either way;
//   - a package bumped only by provider updates (DueTo non-empty) runs a
//     version task immediately before its build, whose DISPAT_UPDATED_*
//     environment names exactly the providers it is catching up to.

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestOrderChainRunsInTopologicalOrder builds a three-deep chain
// (base <- mid <- top) purely from `dependencies` edges — each package has
// its own qualifying commit, so the edges alone (not propagation) are what
// must order the run.
func TestOrderChainRunsInTopologicalOrder(t *testing.T) {
	r := harness.New(t)
	cfg := harness.BaseFile(3)
	cfg.Scripts = map[string]models.Script{
		"build":   {r.TsmarkScript("build.log", "$DISPAT_PACKAGE", 20*time.Millisecond)},
		"publish": {r.TsmarkScript("publish.log", "$DISPAT_PACKAGE", 20*time.Millisecond)},
	}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: models.PathList{"packages"}, Flow: buildPublish()},
	}
	cfg.Dependencies = []models.DependencyConfig{
		{Consumer: "mid", Provider: "base"},
		{Consumer: "top", Provider: "mid"},
	}
	r.WriteConfigModel(cfg)

	for _, name := range []string{"base", "mid", "top"} {
		r.SeedPackage("packages", name)
	}
	r.Commit("feat(base,mid,top): bootstrap the chain")

	r.ReleaseOK()

	build := r.Timeline("build.log")
	publish := r.Timeline("publish.log")
	baseB, midB, topB := harness.Find(t, build, "base"), harness.Find(t, build, "mid"), harness.Find(t, build, "top")
	baseP, midP, topP := harness.Find(t, publish, "base"), harness.Find(t, publish, "mid"), harness.Find(t, publish, "top")

	harness.AssertSequential(t, baseB, midB)
	harness.AssertSequential(t, midB, topB)
	harness.AssertSequential(t, baseP, midP)
	harness.AssertSequential(t, midP, topP)
}

// providerConsumerRepo writes a two-package repository (provider, consumer)
// in two spaces — the flag under test is a property of the *provider's*
// space — wired to log every stage into one timeline.log so ordering across
// build and publish can be compared directly. The caret commit reaches the
// consumer via propagation, so it releases purely because of the provider:
// the DueTo shape the version-task test also relies on.
func providerConsumerRepo(t *testing.T, isBuildWaitingPublish bool, providerPublishSleep time.Duration) *harness.Repo {
	t.Helper()
	r := harness.New(t)
	cfg := harness.BaseFile(2)
	cfg.Scripts = map[string]models.Script{
		"provider-build":   {r.TsmarkScript("timeline.log", "provider-build", 0)},
		"provider-publish": {r.TsmarkScript("timeline.log", "provider-publish", providerPublishSleep)},
		"consumer-build":   {r.TsmarkScript("timeline.log", "consumer-build", 0)},
		"consumer-publish": {r.TsmarkScript("timeline.log", "consumer-publish", 0)},
	}
	cfg.Spaces = map[string]models.SpaceConfig{
		"provider": {Path: models.PathList{"packages/provider"}, IsBuildWaitingPublish: models.Bool(isBuildWaitingPublish),
			Flow: &models.SpaceFlowConfig{Build: []string{"provider-build"}, Publish: []string{"provider-publish"}}},
		"consumer": {Path: models.PathList{"packages/consumer"},
			Flow: &models.SpaceFlowConfig{Build: []string{"consumer-build"}, Publish: []string{"consumer-publish"}}},
	}
	cfg.Dependencies = []models.DependencyConfig{{Consumer: "consumer", Provider: "provider"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages/provider", "provider")
	r.SeedPackage("packages/consumer", "consumer")
	r.Commit("feat(provider)^: reaches its one consumer")
	return r
}

// TestOrderBuildWaitsForPublishWhenConfigured proves isBuildWaitingPublish
// directly: with it set, the consumer's build must not start until the
// provider's *publish* — not merely its build — has finished. The
// provider's publish sleeps long enough that "started during it" and
// "started after it" are unambiguous under process-launch jitter.
func TestOrderBuildWaitsForPublishWhenConfigured(t *testing.T) {
	r := providerConsumerRepo(t, true, 250*time.Millisecond)
	r.ReleaseOK()

	tl := r.Timeline("timeline.log")
	harness.AssertSequential(t,
		harness.Find(t, tl, "provider-publish"),
		harness.Find(t, tl, "consumer-build"))
}

// TestOrderBuildDoesNotWaitForPublishByDefault proves the converse: with
// the flag at its default, the consumer's build is gated only on the
// provider's build, so it runs *during* the provider's generously slow
// publish (timing evidence). Its own publish is a different story — that
// one is unconditionally gated on the provider's publish, which is the
// hard, timing-independent half of this test.
func TestOrderBuildDoesNotWaitForPublishByDefault(t *testing.T) {
	r := providerConsumerRepo(t, false, 800*time.Millisecond)
	r.ReleaseOK()

	tl := r.Timeline("timeline.log")
	providerBuild := harness.Find(t, tl, "provider-build")
	providerPublish := harness.Find(t, tl, "provider-publish")
	consumerBuild := harness.Find(t, tl, "consumer-build")
	consumerPublish := harness.Find(t, tl, "consumer-publish")

	// Timing evidence: the consumer's near-instant build fits inside the
	// provider's 800ms publish window — wide enough that even a stalled
	// runner launches the consumer inside it — so it plainly did not wait.
	harness.AssertSequential(t, providerBuild, consumerBuild) // still gated on the build
	assert.Truef(t, consumerBuild.Start.Before(providerPublish.End),
		"consumer build started at %s, after the provider's publish ended at %s — isBuildWaitingPublish is false, it should not have waited",
		consumerBuild.Start, providerPublish.End)

	// Structural guarantee, independent of any timing: the consumer's own
	// publish still waits for the provider's publish regardless of the flag.
	harness.AssertSequential(t, providerPublish, consumerPublish)
}

// TestOrderDiamondDependencyConverges checks a fan-out/fan-in shape: b and
// c both depend on a, and d depends on both. a must finish before either b
// or c starts; b and c may (and, given two slots and 100ms builds, reliably
// do) run concurrently; d must wait for both — at the build stage and at
// the publish stage.
func TestOrderDiamondDependencyConverges(t *testing.T) {
	r := harness.New(t)
	cfg := harness.BaseFile(2)
	cfg.Scripts = map[string]models.Script{
		"build":   {r.TsmarkScript("build.log", "$DISPAT_PACKAGE", 100*time.Millisecond)},
		"publish": {r.TsmarkScript("publish.log", "$DISPAT_PACKAGE", 20*time.Millisecond)},
	}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: models.PathList{"packages"}, Flow: buildPublish()},
	}
	cfg.Dependencies = []models.DependencyConfig{
		{Consumer: "b", Provider: "a"},
		{Consumer: "c", Provider: "a"},
		{Consumer: "d", Provider: "b"},
		{Consumer: "d", Provider: "c"},
	}
	r.WriteConfigModel(cfg)
	for _, name := range []string{"a", "b", "c", "d"} {
		r.SeedPackage("packages", name)
	}
	r.Commit("feat(a,b,c,d): bootstrap the diamond")

	r.ReleaseOK()

	build := r.Timeline("build.log")
	a, b, c, d := harness.Find(t, build, "a"), harness.Find(t, build, "b"),
		harness.Find(t, build, "c"), harness.Find(t, build, "d")
	harness.AssertSequential(t, a, b)
	harness.AssertSequential(t, a, c)
	harness.AssertOverlaps(t, b, c)
	harness.AssertSequential(t, b, d)
	harness.AssertSequential(t, c, d)

	publish := r.Timeline("publish.log")
	aP, bP, cP, dP := harness.Find(t, publish, "a"), harness.Find(t, publish, "b"),
		harness.Find(t, publish, "c"), harness.Find(t, publish, "d")
	harness.AssertSequential(t, aP, bP)
	harness.AssertSequential(t, aP, cP)
	harness.AssertSequential(t, bP, dP)
	harness.AssertSequential(t, cP, dP)
}

// TestOrderVersionTaskPrecedesBuildWithUpdatedProviderEnv checks the third
// promise: a consumer released only because of a provider (DueTo, no commit
// of its own) runs a version task whose DISPAT_UPDATED_* environment names
// the provider and its old/new versions — the input a version script
// actually reconciles manifests against. One shared space configures the
// run.version script for *both* packages, yet a version task only exists for a
// package that picks a version up from somebody, so it must not run for the
// provider even though nothing in the config says so.
func TestOrderVersionTaskPrecedesBuildWithUpdatedProviderEnv(t *testing.T) {
	const envFile = "version-env.txt"
	r := harness.New(t)
	cfg := harness.BaseFile(2)
	cfg.Scripts = map[string]models.Script{
		"build":   {"echo building"},
		"publish": {"echo publishing"},
		"sync":    {"env | grep '^DISPAT_' | sort > " + envFile},
	}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: models.PathList{"packages"}, Flow: &models.SpaceFlowConfig{
			Version: []string{"sync"}, Build: []string{"build"}, Publish: []string{"publish"},
		}},
	}
	cfg.Dependencies = []models.DependencyConfig{{Consumer: "consumer", Provider: "provider"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "provider")
	r.SeedPackage("packages", "consumer")
	r.Commit("feat(provider)^: reaches its one consumer")

	r.ReleaseOK()
	require.True(t, r.HasTag("consumer@0.0.1"), "tags: %v", r.TagList())
	require.NoFileExists(t, r.Path("packages", "provider", envFile),
		"the provider has no providers of its own, so it picks up nobody's version: its version task — and run.version script — must not run at all")

	data, err := os.ReadFile(r.Path("packages", "consumer", envFile))
	require.NoError(t, err)
	env := string(data)
	assert.Contains(t, env, "DISPAT_STAGE=version")
	assert.Contains(t, env, "DISPAT_UPDATED_PACKAGES=PROVIDER")
	assert.Contains(t, env, "DISPAT_UPDATED_PROVIDER_NAME=provider")
	assert.Contains(t, env, "DISPAT_UPDATED_PROVIDER_OLD_VERSION=0.0.0")
	assert.Contains(t, env, "DISPAT_UPDATED_PROVIDER_NEW_VERSION=0.1.0")
}
