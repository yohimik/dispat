// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Releasing a choreographed fleet through the binary: what each repository
// records, what it settles before it publishes, and what a second run of the
// same command does.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// crossRepositoryFleet is the fleet most release scenarios need: a consumer
// in one repository depending on a provider in another, linked both ways.
func crossRepositoryFleet(t *testing.T) *choreographyFleet {
	t.Helper()
	fleet := newChoreographyFleet(t, "api", "sdk")
	fleet.writeConfig("api", func(cfg *models.File) {
		cfg.Dependencies = models.Dependencies{{Consumer: "api-pkg", Provider: "sdk-pkg"}}
	})
	fleet.peer("api").Commit("chore: declare the cross-repository edge")
	fleet.push("api")
	fleet.link("api", "sdk")
	return fleet
}

// settledPins are the fleet links one run reported recording.
func settledPins(res harness.RunResult) []harness.Event {
	var out []harness.Event
	for _, event := range res.Events {
		if event.Str("message") == "recorded fleet links" {
			out = append(out, event)
		}
	}
	return out
}

// TestChoreographyRecordsEveryPeerLocallyAndConverges: each repository tags
// and commits its own release, and running the same command again releases
// nothing.
func TestChoreographyRecordsEveryPeerLocallyAndConverges(t *testing.T) {
	fleet := crossRepositoryFleet(t)
	api := fleet.peer("api")

	res := api.ReleaseOK("--package", "*")
	assert.Equal(t, []string{"api-pkg@0.1.0"}, api.TagList())
	assert.Equal(t, []string{"sdk-pkg@0.1.0"}, tagsIn(api.Repo, ".links/sdk"))
	assert.Contains(t, subjects(api.Repo)[0], "chore(release): api-pkg@0.1.0")
	assert.FileExists(t, api.Path("packages", "api-pkg", "CHANGELOG.md"))
	assert.FileExists(t, api.Path(".links", "sdk", "packages", "sdk-pkg", "CHANGELOG.md"))
	require.NotEmpty(t, settledPins(res), "the consumer recorded what it incorporated")

	again := api.ReleaseOK("--package", "*")
	assert.Equal(t, []string{"api-pkg@0.1.0"}, api.TagList(), "a second run releases nothing")
	assert.Empty(t, settledPins(again), "and settles nothing: the links already say this")
	for _, event := range again.Events {
		if event.Str("message") == "done" {
			assert.EqualValues(t, 0, event["published"])
		}
	}
}

// TestChoreographyMissingProviderTagBlocksConsumer makes the source record
// fail after its publish script. The consumer is blocked rather than tagged
// against a provider version that never acquired its release record.
func TestChoreographyMissingProviderTagBlocksConsumer(t *testing.T) {
	fleet := crossRepositoryFleet(t)
	fleet.writeConfig("sdk", func(cfg *models.File) {
		cfg.Scripts["tag-collision"] = models.Script{
			"git tag -a sdk-pkg@0.1.0 HEAD^ -m injected",
		}
		cfg.Run = &models.RunConfig{BeforeCommit: []string{"tag-collision"}}
	})
	fleet.peer("sdk").Commit("chore: inject a late source tag collision")
	fleet.push("sdk")
	fleet.follow("api", "sdk")
	api := fleet.peer("api")

	res := api.Release("--package", "*")
	require.NotEqual(t, 0, res.Code, "a source tag collision is critical")
	assert.Equal(t, 0, api.TagCount("api-pkg@0.1.0"), "consumer cannot record absent provider tag")
	assert.True(t, harness.IsCodePresentForPackage(res.Events, "W194", "api-pkg"),
		"consumer is blocked by the failed source record: %s", res.Stdout)
}

// TestChoreographyCatchesUpAfterProviderOnlyRetry exercises the composed
// history and fleet-link boundary: an app publishes its own work while its
// library fails, misses the successful library-only retry at a later commit,
// and later receives the owed propagation without another library release or
// new source commit. The owed window over the library's repository is what
// keeps the library's unit in the plan once both have released past it.
func TestChoreographyCatchesUpAfterProviderOnlyRetry(t *testing.T) {
	fleet := crossRepositoryFleet(t)
	fleet.writeConfig("api", func(cfg *models.File) {
		cfg.Dependencies = models.Dependencies{{Consumer: "api-pkg", Provider: "sdk-pkg"}}
		cfg.Flow = &models.SpaceFlowConfig{Publish: []string{"publish"}}
	})
	api := fleet.peer("api")
	api.Commit("chore: keep app build free of unpublished provider versions")
	fleet.push("api")
	api.ReleaseOK("--package", "*")

	fleet.configureIn(api.Repo, "sdk", "chore: gate provider publication", func(cfg *models.File) {
		cfg.Scripts["publish"] = models.Script{
			`if [ -n "$DISPAT_IT_SDK_OK" ]; then echo published; else exit 1; fi`,
		}
	})
	api.Git("add", ".links/sdk")
	api.Commit("chore: pin provider publication policy")
	fleet.workIn(api.Repo, "sdk", "sdk-pkg", "feat(sdk-pkg)^: streaming")
	api.Git("add", ".links/sdk")
	api.Commit("chore: pin provider feature")
	fleet.workOnly("api", "feat(api-pkg): own flag")

	failed := api.Release("--package", "*")
	require.NotEqual(t, 0, failed.Code, "provider publish failed")
	require.Equal(t, 1, api.TagCount("api-pkg@0.2.0"), "app should ship its own work: %s", failed.Stdout)
	assert.Equal(t, []string{"sdk-pkg@0.1.0"}, tagsIn(api.Repo, ".links/sdk"),
		"provider should keep only its bootstrap tag")

	// The library's retry lands on a commit of its own, past the app's release:
	// two releases on one commit could not be ordered afterwards.
	fleet.workIn(api.Repo, "sdk", "sdk-pkg", "chore(sdk-pkg): retry the provider")
	api.Git("add", ".links/sdk")
	api.Commit("chore: pin the provider retry")
	provider := api.CommandEnv([]string{"DISPAT_IT_SDK_OK=1"}, "--package", "sdk-pkg")
	require.Equal(t, 0, provider.Code, "provider-only retry: %s", provider.Stdout)
	require.Contains(t, tagsIn(api.Repo, ".links/sdk"), "sdk-pkg@0.2.0")
	assert.Equal(t, 0, api.TagCount("api-pkg@0.2.1"), "app was excluded from provider retry")

	catchUp := api.CommandEnv([]string{"DISPAT_IT_SDK_OK=1"}, "--package", "*")
	require.Equal(t, 0, catchUp.Code, "fleet catch-up: %s", catchUp.Stdout)
	assert.Equal(t, 1, api.TagCount("api-pkg@0.2.1"))
	assert.Equal(t, []string{"sdk-pkg@0.1.0", "sdk-pkg@0.2.0"}, tagsIn(api.Repo, ".links/sdk"),
		"provider is never republished")
	assert.True(t, harness.IsCodePresentForPackage(catchUp.Events, "W193", "api-pkg"),
		"the app's only new cause is delivered provider propagation: %s", catchUp.Stdout)
	settled := api.Status("--require-release", "--package", "*")
	assert.NotEqual(t, 0, settled.Code, "the catch-up converges")
	assert.Contains(t, settled.Stdout, `"releasing":0`)
}

// TestChoreographyRemovedProviderCreatesNoDebt: retiring a package while its
// source repository stays in the fleet leaves the consumer's old release, which
// picked the provider up, as history. It is not a request to plan a deleted
// package or to release its former consumer again.
func TestChoreographyRemovedProviderCreatesNoDebt(t *testing.T) {
	fleet := crossRepositoryFleet(t)
	api := fleet.peer("api")
	api.ReleaseOK("--package", "*")
	require.Equal(t, 1, api.TagCount("api-pkg@0.1.0"))
	require.Contains(t, tagsIn(api.Repo, ".links/sdk"), "sdk-pkg@0.1.0")
	fleet.workIn(api.Repo, "sdk", "sdk-pkg", "feat(sdk-pkg)^: propagate the library")
	api.Git("add", ".links/sdk")
	api.Commit("chore: pin the library change")
	fleet.workOnly("api", "feat(api-pkg): own work")
	api.ReleaseOK("--package", "*")
	require.Equal(t, 1, api.TagCount("api-pkg@0.2.0"), "the consumer picked the provider up")

	fleet.writeConfig("api", func(cfg *models.File) {
		cfg.Dependencies = nil
	})
	api.Git("-C", ".links/sdk", "rm", "-r", "--", "packages/sdk-pkg")
	api.WriteFile(".links/sdk/packages/.keep", "source remains in the fleet\n")
	api.Git("-C", ".links/sdk", "add", "--", "packages/.keep")
	api.Git("-C", ".links/sdk", "commit", "-q", "-m", "chore: retire the sdk package")
	api.Git("add", ".links/sdk")
	api.Commit("chore: remove the sdk dependency and pin its retirement")

	status := api.Status("--package", "*")
	require.Equal(t, 0, status.Code, "stdout:\n%s\nstderr:\n%s", status.Stdout, status.Stderr)
	assert.NotContains(t, status.Stdout+status.Stderr, "unknown provider")
	assert.NotContains(t, status.Stdout, `"name":"sdk-pkg"`)
	assert.Contains(t, status.Stdout, `"releasing":0`)
	assert.Equal(t, 1, api.TagCount("api-pkg@0.2.0"), "the old consumer tag remains authoritative")
	assert.Zero(t, api.TagCount("api-pkg@0.2.1"), "the removed provider creates no new debt")
}

// TestChoreographySettlesTheProviderRevisionBeforePublishing: the evidence a
// later plan reads is in the consumer's own tree, recorded before the release
// commit the tag sits on.
func TestChoreographySettlesTheProviderRevisionBeforePublishing(t *testing.T) {
	fleet := crossRepositoryFleet(t)
	api := fleet.peer("api")

	res := api.ReleaseOK("--package", "*")
	pins := settledPins(res)
	require.Len(t, pins, 1)
	assert.Equal(t, "api", pins[0].Str("repository"))

	provider := api.Git("-C", ".links/sdk", "rev-parse", "HEAD")
	assert.Equal(t, provider, gitlinkAt(api.Repo, "HEAD", ".links/sdk"),
		"the release commit's tree records the provider revision this release incorporated")
	assert.Equal(t, provider, gitlinkAt(api.Repo, "refs/tags/api-pkg@0.1.0", ".links/sdk"),
		"and so does the commit the tag names")

	// The settlement is its own commit, made before the release commit.
	log := subjects(api.Repo)
	assert.Equal(t, "chore(release): api-pkg@0.1.0", log[0])
	assert.Equal(t, "chore(release): api-pkg@0.1.0", log[1], "the settlement precedes the release commit")
}

// TestChoreographyRefusesAPackageWhoseRouteCannotRecord: evidence that stops
// halfway is worse than none, so the package is refused before it publishes.
func TestChoreographyRefusesAPackageWhoseRouteCannotRecord(t *testing.T) {
	fleet := crossRepositoryFleet(t)
	fleet.writeConfig("sdk", func(cfg *models.File) {
		cfg.Commit = &models.CommitConfig{Enabled: models.Bool(false)}
	})
	fleet.peer("sdk").Commit("fix(sdk-pkg): stop writing release commits")
	fleet.push("sdk")
	fleet.follow("api", "sdk")
	api := fleet.peer("api")

	res := api.Release("--package", "*")
	assert.NotZero(t, res.Code, "stdout:\n%s", res.Stdout)
	requireDiagnostic(t, res, "E333")
	assert.NotContains(t, api.TagList(), "api-pkg@0.1.0", "the consumer never published")
}

// TestChoreographyRecordsNothingForATagOnlyConsumer: a consumer that writes
// no release commit has nowhere to record evidence, and says so at info
// rather than failing.
func TestChoreographyRecordsNothingForATagOnlyConsumer(t *testing.T) {
	fleet := crossRepositoryFleet(t)
	fleet.writeConfig("api", func(cfg *models.File) {
		cfg.Dependencies = models.Dependencies{{Consumer: "api-pkg", Provider: "sdk-pkg"}}
		cfg.Commit = &models.CommitConfig{Enabled: models.Bool(false)}
	})
	fleet.peer("api").Commit("fix(api-pkg): release without a commit")
	fleet.push("api")
	api := fleet.peer("api")

	res := api.ReleaseOK("--package", "*")
	assert.Contains(t, api.TagList(), "api-pkg@0.1.0")
	assert.Empty(t, settledPins(res))
	assert.Contains(t, res.Stdout, "no fleet link evidence is recorded")
}

// TestChoreographyRunsTheEntryHooksOnce: every peer of a fleet is an imported
// configuration, the entry included, and the entry's own hooks are the run's.
func TestChoreographyRunsTheEntryHooksOnce(t *testing.T) {
	fleet := crossRepositoryFleet(t)
	api := fleet.peer("api")
	marker := api.Path("beforeAll.count")
	fleet.writeConfig("api", func(cfg *models.File) {
		cfg.Dependencies = models.Dependencies{{Consumer: "api-pkg", Provider: "sdk-pkg"}}
		cfg.Scripts["gate"] = models.Script{"printf 'x' >> " + shellQuote(marker)}
		cfg.Run = &models.RunConfig{BeforeAll: []string{"gate"}}
	})
	api.Commit("chore: add a run gate")

	api.ReleaseOK("--package", "*")
	data := readAbs(t, marker)
	assert.Equal(t, "x", data, "the entry's beforeAll ran exactly once")
}

// TestChoreographyRevertsInTheRepositoryThatOwnsTheFolder: a failed package
// is rolled back through its own repository, not through whichever checkout
// happens to contain it.
func TestChoreographyRevertsInTheRepositoryThatOwnsTheFolder(t *testing.T) {
	fleet := crossRepositoryFleet(t)
	fleet.writeConfig("sdk", func(cfg *models.File) {
		cfg.Scripts = releaseFlow("echo building", "printf 'half written\\n' > packages/sdk-pkg/main.txt; exit 1")
		cfg.Flow = &models.SpaceFlowConfig{Build: []string{"build"}, Publish: []string{"publish"}}
		cfg.RevertOnFail = models.Bool(true)
	})
	fleet.peer("sdk").Commit("fix(sdk-pkg): fail while publishing")
	fleet.push("sdk")
	fleet.follow("api", "sdk")
	api := fleet.peer("api")

	res := api.Release("--package", "*")
	assert.NotZero(t, res.Code)
	assert.Equal(t, "sdk-pkg\n", readAbs(t, api.Path(".links", "sdk", "packages", "sdk-pkg", "main.txt")),
		"the provider's own repository restored its own file")
	assert.Empty(t, tagsIn(api.Repo, ".links/sdk"))
}

// TestChoreographyBypassesTheLockPerRepository: an unsafe setting is the
// repository's own, and one peer cannot unlock another.
func TestChoreographyBypassesTheLockPerRepository(t *testing.T) {
	fleet := crossRepositoryFleet(t)
	fleet.writeConfig("api", func(cfg *models.File) {
		cfg.Dependencies = models.Dependencies{{Consumer: "api-pkg", Provider: "sdk-pkg"}}
		cfg.UnsafeDisableLock = true
	})
	fleet.peer("api").Commit("chore: release this repository without the lock")
	fleet.push("api")

	res := fleet.peer("api").CommandEnv(harness.LockEnabled, "--package", "*")
	requireDiagnostic(t, res, "W331")
	for _, event := range res.Events {
		if event.Code() != "W331" {
			continue
		}
		raw, ok := event["repositories"].([]any)
		require.True(t, ok)
		assert.Equal(t, []any{"api"}, raw, "only the repository that asked for it releases unlocked")
	}
}

// TestChoreographyConcurrentConsumersSettleInLaneOrder: two packages in
// different repositories publishing at once take the lanes they settle in the
// same order, so neither waits on the other. The handshake proves the
// publishes really overlap; the bound is what turns a deadlock into a
// failure instead of a hang.
func TestChoreographyConcurrentConsumersSettleInLaneOrder(t *testing.T) {
	fleet := newChoreographyFleet(t, "api", "sdk", "web")
	fleet.link("api", "sdk")
	fleet.link("api", "web")
	fleet.follow("api", "sdk")
	api := fleet.peer("api")
	// sdk and web both depend on api's package, so both settle the same two
	// lanes: their own and api's.
	for _, name := range []string{"sdk", "web"} {
		fleet.writeConfig(name, func(cfg *models.File) {
			cfg.Dependencies = models.Dependencies{{Consumer: name + "-pkg", Provider: "api-pkg"}}
		})
		fleet.peer(name).Commit("chore: depend on the api package")
		fleet.push(name)
	}
	fleet.follow("api", "sdk")
	fleet.follow("api", "web")

	markers := api.Path("markers")
	require.NoError(t, os.MkdirAll(markers, 0o755))
	// The handshake belongs to the two consumers' own configurations: a peer
	// owns its packages' scripts, which is what makes these two publishes
	// two repositories' work rather than one.
	for _, pair := range [][2]string{{"sdk", "web"}, {"web", "sdk"}} {
		name, peer := pair[0], pair[1]
		fleet.writeConfig(name, func(cfg *models.File) {
			cfg.Dependencies = models.Dependencies{{Consumer: name + "-pkg", Provider: "api-pkg"}}
			cfg.Scripts["publish"] = models.Script{
				handshakeScript(filepath.Join(markers, name), filepath.Join(markers, peer))}
		})
		fleet.peer(name).Commit("chore: hand shake with " + peer)
		fleet.push(name)
	}
	fleet.follow("api", "sdk")
	fleet.follow("api", "web")

	// Everything releases: the provider first, then the two consumers, which
	// is what puts their publishes — and their settlements — side by side.
	res := api.Release("--package", "*")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.FileExists(t, filepath.Join(markers, "sdk"))
	assert.FileExists(t, filepath.Join(markers, "web"))
	assert.Equal(t, []string{"sdk-pkg@0.1.0"}, tagsIn(api.Repo, ".links/sdk"))
	assert.Equal(t, []string{"web-pkg@0.1.0"}, tagsIn(api.Repo, ".links/web"))
}

// TestChoreographyRunsTheSettlingRepositoryHooks: a settlement is a commit
// dispat makes in that repository, so that repository's commit hooks bracket
// it.
func TestChoreographyRunsTheSettlingRepositoryHooks(t *testing.T) {
	fleet := crossRepositoryFleet(t)
	api := fleet.peer("api")
	marker := api.Path("hooks.log")
	fleet.writeConfig("api", func(cfg *models.File) {
		cfg.Dependencies = models.Dependencies{{Consumer: "api-pkg", Provider: "sdk-pkg"}}
		cfg.Scripts["note"] = models.Script{"printf '%s\\n' \"$DISPAT_STAGE\" >> " + shellQuote(marker)}
		cfg.Run = &models.RunConfig{BeforeCommit: []string{"note"}, AfterCommit: []string{"note"}}
	})
	api.Commit("chore: watch the commit hooks")

	api.ReleaseOK("--package", "*")
	stages := strings.Fields(readAbs(t, marker))
	assert.GreaterOrEqual(t, len(stages), 4,
		"the settlement and the release commit each fire the pair: %v", stages)
	assert.Equal(t, "beforeCommit", stages[0])
	assert.Equal(t, "afterCommit", stages[1])
}

// TestChoreographyPushesEveryRepositoryItRecorded: with pushing on, each
// repository's own branch and tags reach its own remote, and a second run
// converges without pushing anything new.
func TestChoreographyPushesEveryRepositoryItRecorded(t *testing.T) {
	fleet := crossRepositoryFleet(t)
	for _, name := range []string{"api", "sdk"} {
		fleet.writeConfig(name, func(cfg *models.File) {
			if name == "api" {
				cfg.Dependencies = models.Dependencies{{Consumer: "api-pkg", Provider: "sdk-pkg"}}
			}
			cfg.Commit = &models.CommitConfig{
				Enabled: models.Bool(true), Push: true, Branch: harness.DefaultBranch}
		})
		fleet.peer(name).Commit("chore: push " + name + " releases")
		fleet.push(name)
	}
	fleet.follow("api", "sdk")
	api := fleet.peer("api")

	res := api.CommandEnv(fileProtocolEnv(), "--package", "*")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, api.Git("rev-parse", "HEAD"),
		api.Git("ls-remote", "origin", "refs/heads/"+harness.DefaultBranch)[:40],
		"the entry pushed what it recorded")
	assert.Contains(t, api.Git("ls-remote", "origin", "refs/tags/api-pkg@0.1.0"), "api-pkg@0.1.0")

	again := api.ReleaseOK("--package", "*")
	assert.Empty(t, settledPins(again))
}

// pushingFleet is a cross-repository fleet whose repositories both push what
// they record to their own remotes, which is what every settlement guard is
// about.
func pushingFleet(t *testing.T) *choreographyFleet {
	t.Helper()
	fleet := crossRepositoryFleet(t)
	for _, name := range []string{"api", "sdk"} {
		fleet.writeConfig(name, func(cfg *models.File) {
			if name == "api" {
				cfg.Dependencies = models.Dependencies{{Consumer: "api-pkg", Provider: "sdk-pkg"}}
			}
			cfg.Commit = &models.CommitConfig{
				Enabled: models.Bool(true), Push: true, Branch: harness.DefaultBranch}
		})
		fleet.peer(name).Commit("chore: push " + name + " releases")
		fleet.push(name)
	}
	fleet.follow("api", "sdk")
	return fleet
}

// TestChoreographyRefusesToPushASettlementFromADetachedCheckout: a repository
// that will push what it records needs a branch to push it on, and E337 is
// the same refusal an orchestrated checkpoint makes.
func TestChoreographyRefusesToPushASettlementFromADetachedCheckout(t *testing.T) {
	fleet := pushingFleet(t)
	api := fleet.peer("api")
	fleet.writeConfig("api", func(cfg *models.File) {
		cfg.Dependencies = models.Dependencies{{Consumer: "api-pkg", Provider: "sdk-pkg"}}
		cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true), Push: true}
	})
	api.Commit("chore: push without naming a branch")
	api.Git("checkout", "-q", "--detach")

	res := api.CommandEnv(fileProtocolEnv(), "--package", "*")
	assert.NotZero(t, res.Code, "stdout:\n%s", res.Stdout)
	requireDiagnostic(t, res, "E337")
	assert.NotContains(t, api.TagList(), "api-pkg@0.1.0", "nothing published")
}

// TestChoreographyRefusesAPinItsTargetHasNotPushed: a recorded link must
// never outrun the repository it points at, so a revision the provider's own
// remote does not hold stops the consumer before it publishes.
func TestChoreographyRefusesAPinItsTargetHasNotPushed(t *testing.T) {
	fleet := pushingFleet(t)
	api := fleet.peer("api")
	require.Equal(t, 0, api.CommandEnv(fileProtocolEnv(), "--package", "*").Code)

	// The provider's checkout moves by a commit that releases nothing and is
	// never pushed: the next settlement would pin a revision its remote does
	// not hold.
	api.WriteFile(".links/sdk/notes.txt", "unpushed\n")
	api.Git("-C", ".links/sdk", "add", "-A")
	api.Git("-C", ".links/sdk", "commit", "-q", "-m", "chore: work nobody can fetch")
	fleet.workOnly("api", "fix(api-pkg): something to release")

	res := api.CommandEnv(fileProtocolEnv(), "--package", "*")
	assert.NotZero(t, res.Code, "stdout:\n%s", res.Stdout)
	assert.Contains(t, res.Stdout+res.Stderr, "cannot record a fleet link")
	assert.Equal(t, 1, api.TagCount("api-pkg@"), "the second release never published")
}

// TestChoreographyConvergesAfterARejectedSettlementPush: a settlement that
// cannot reach its remote fails the run and leaves only ordinary commits
// behind, so the next run of the same command finishes the job and releases
// each package exactly once.
func TestChoreographyConvergesAfterARejectedSettlementPush(t *testing.T) {
	fleet := pushingFleet(t)
	api := fleet.peer("api")
	remote := api.Git("remote", "get-url", "origin")
	hook := filepath.Join(remote, "hooks", "pre-receive")
	require.NoError(t, os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o755))

	failed := api.CommandEnv(fileProtocolEnv(), "--package", "*")
	assert.NotZero(t, failed.Code, "stdout:\n%s", failed.Stdout)
	assert.NotContains(t, api.TagList(), "api-pkg@0.1.0", "the consumer never published")

	require.NoError(t, os.Remove(hook))
	healed := api.CommandEnv(fileProtocolEnv(), "--package", "*")
	require.Equal(t, 0, healed.Code, "stdout:\n%s\nstderr:\n%s", healed.Stdout, healed.Stderr)
	assert.Equal(t, []string{"api-pkg@0.1.0"}, api.TagList())
	assert.Equal(t, 1, api.TagCount("api-pkg@"), "nothing was released twice")
	assert.Equal(t, 1, strings.Count(api.Git("-C", ".links/sdk", "tag"), "sdk-pkg@"))

	again := api.CommandEnv(fileProtocolEnv(), "--package", "*")
	require.Equal(t, 0, again.Code)
	assert.Equal(t, 1, api.TagCount("api-pkg@"), "and the third run releases nothing")
}

// TestChoreographyNestedCommitStepRecordsTheEvidence: a package whose publish
// stage runs `dispat commit` records its own release through that step, and
// the fleet links it depends on are settled before the commit so the tag's
// own commit carries them.
func TestChoreographyNestedCommitStepRecordsTheEvidence(t *testing.T) {
	fleet := crossRepositoryFleet(t)
	api := fleet.peer("api")
	fleet.writeConfig("api", func(cfg *models.File) {
		cfg.Dependencies = models.Dependencies{{Consumer: "api-pkg", Provider: "sdk-pkg"}}
		cfg.Scripts["publish"] = models.Script{api.DispatCommand("commit", "--tag")}
		cfg.Commit = &models.CommitConfig{Enabled: models.Bool(false)}
	})
	api.Commit("chore: record through a nested commit step")

	res := api.ReleaseOK("--package", "*")
	assert.Contains(t, api.TagList(), "api-pkg@0.1.0")
	provider := api.Git("-C", ".links/sdk", "rev-parse", "HEAD")
	assert.Equal(t, provider, gitlinkAt(api.Repo, "refs/tags/api-pkg@0.1.0", ".links/sdk"),
		"the commit the tag names carries the revision this release incorporated")
	_ = res
}

// TestChoreographyLocksEveryRepositoryInNameOrder: the fleet lock covers every
// participating repository, taken in one deterministic order so two runs of
// the same fleet contend rather than deadlock.
func TestChoreographyLocksEveryRepositoryInNameOrder(t *testing.T) {
	fleet := pushingFleet(t)
	api := fleet.peer("api")

	res := api.CommandEnv(append(fileProtocolEnv(), harness.LockEnabled...),
		"--package", "*", "--log-level", "debug")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	var order []any
	for _, event := range res.Events {
		if event.Str("message") == "fleet release locks acquired" {
			order = event["order"].([]any)
		}
	}
	assert.Equal(t, []any{"api", "sdk"}, order, "locks are taken in repository-name order")
	requireNoDiagnostic(t, res, "W331")
}

// TestChoreographyVerifiesADetachedPeerAgainstTheRoster: every linked
// checkout is detached once a release has left it at the revision the link
// records, so the repository at the far end of a route states no branch of
// its own. The roster is where the fleet wrote down which branch that peer
// releases on, and it is enough to ask a remote whether it holds a revision.
func TestChoreographyVerifiesADetachedPeerAgainstTheRoster(t *testing.T) {
	// The case where no roster states a branch anywhere is a refusal the unit
	// suite pins (TestSettleLinksVerifiesADetachedPeerAgainstItsRosterBranch):
	// reaching it from outside needs a fleet whose settlement is pending and
	// whose every roster is silent, which is a fixture about the fixture.
	for _, tc := range []struct {
		name    string
		peers   []string
		stated  string
		refused bool
	}{
		{name: "the roster of the repository that links it", peers: []string{"api", "sdk"}, stated: "api"},
		{name: "any roster that states one", peers: []string{"api", "sdk", "web"}, stated: "web"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fleet := newChoreographyFleet(t, tc.peers...)
			for _, name := range tc.peers {
				fleet.writeConfig(name, func(cfg *models.File) {
					if name == "api" {
						cfg.Dependencies = models.Dependencies{{Consumer: "api-pkg", Provider: "sdk-pkg"}}
					}
					cfg.Commit = &models.CommitConfig{
						Enabled: models.Bool(true), Push: true, Branch: harness.DefaultBranch}
				})
				fleet.peer(name).Commit("chore: state this repository's release policy")
				fleet.push(name)
			}
			for _, peer := range tc.peers[1:] {
				fleet.link("api", peer)
			}
			api := fleet.peer("api")
			require.Equal(t, 0, api.CommandEnv(fileProtocolEnv(), "--package", "*").Code,
				"the fleet releases once with every repository on a branch")

			// Now the ordinary shape: the linked checkout sits at the revision
			// the link records, on no branch, and says nothing about one.
			api.Git("-C", ".links/sdk", "checkout", "-q", "--detach")
			for _, name := range tc.peers {
				if name == "api" {
					continue
				}
				fleet.configureIn(api.Repo, name, "chore: leave the branch to the roster", func(cfg *models.File) {
					cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true), Push: true}
					for i := range cfg.Repositories {
						cfg.Repositories[i].Branch = ""
					}
				})
			}
			fleet.writeConfig("api", func(cfg *models.File) {
				cfg.Dependencies = models.Dependencies{{Consumer: "api-pkg", Provider: "sdk-pkg"}}
				cfg.Commit = &models.CommitConfig{
					Enabled: models.Bool(true), Push: true, Branch: harness.DefaultBranch}
				for i := range cfg.Repositories {
					cfg.Repositories[i].Branch = ""
				}
			})
			if tc.stated != "" {
				fleet.configureIn(api.Repo, tc.stated, "chore: state the branch in this roster", func(cfg *models.File) {
					cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true), Push: true}
					for i := range cfg.Repositories {
						cfg.Repositories[i].Branch = harness.DefaultBranch
					}
				})
			}
			if tc.stated == "api" {
				fleet.writeConfig("api", func(cfg *models.File) {
					cfg.Dependencies = models.Dependencies{{Consumer: "api-pkg", Provider: "sdk-pkg"}}
					cfg.Commit = &models.CommitConfig{
						Enabled: models.Bool(true), Push: true, Branch: harness.DefaultBranch}
				})
			}
			api.Commit("chore: leave the peers' branches to the roster")
			api.Git("push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)
			// The provider moves by a commit that releases nothing, and the
			// consumer's own change is committed without staging the drift, so
			// the settlement the next release makes is genuinely pending.
			api.WriteFile(".links/sdk/notes.txt", "moved without releasing\n")
			api.Git("-C", ".links/sdk", "add", "-A")
			api.Git("-C", ".links/sdk", "commit", "-q", "-m", "chore: move without releasing")
			api.Git("-C", ".links/sdk", "push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)
			fleet.workOnly("api", "fix(api-pkg): something to release")

			res := api.CommandEnv(fileProtocolEnv(), "--package", "*")
			if tc.refused {
				assert.NotZero(t, res.Code, "stdout:\n%s", res.Stdout)
				combined := res.Stdout + res.Stderr
				assert.Contains(t, combined, "has no branch to verify revision")
				assert.Contains(t, combined, "commit.branch")
				assert.Contains(t, combined, "fleet roster")
				assert.Equal(t, 1, api.TagCount("api-pkg@"), "the second release never published")
				return
			}
			require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.Equal(t, 2, api.TagCount("api-pkg@"))
			assert.Equal(t, api.Git("-C", ".links/sdk", "rev-parse", "HEAD"),
				gitlinkAt(api.Repo, "HEAD", ".links/sdk"))
		})
	}
}

// TestChoreographySettlementAlreadyOnItsRemotePushesNothing: a consumer that
// releases again while its provider has not moved records nothing new, and
// asks its remote rather than pushing a revision the remote already holds.
func TestChoreographySettlementAlreadyOnItsRemotePushesNothing(t *testing.T) {
	fleet := pushingFleet(t)
	api := fleet.peer("api")
	require.Equal(t, 0, api.CommandEnv(fileProtocolEnv(), "--package", "*").Code)
	settled := api.Git("rev-parse", "HEAD")

	// The consumer's own change, with the provider exactly where it was.
	fleet.workOnly("api", "fix(api-pkg): a change of its own")
	res := api.CommandEnv(fileProtocolEnv(), "--package", "*")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Empty(t, settledPins(res), "the links already record this revision")
	assert.Contains(t, api.TagList(), "api-pkg@0.1.1")
	assert.Equal(t, api.Git("rev-parse", "HEAD"),
		api.Git("ls-remote", "origin", "refs/heads/"+harness.DefaultBranch)[:40])
	assert.NotEqual(t, settled, api.Git("rev-parse", "HEAD"))
}

// TestChoreographyRefusesASettlementTheConsumerDoesNotDeclare: a link only the
// provider declares still joins the two repositories for reading, but the
// consumer is the one that has to record the hop, and a consumer that declares
// no link has nowhere to record it. The release stops before any lane is
// taken, so nothing publishes anywhere.
func TestChoreographyRefusesASettlementTheConsumerDoesNotDeclare(t *testing.T) {
	fleet := newChoreographyFleet(t, "api", "sdk")
	fleet.writeConfig("api", func(cfg *models.File) {
		cfg.Dependencies = models.Dependencies{{Consumer: "api-pkg", Provider: "sdk-pkg"}}
	})
	fleet.peer("api").Commit("chore: declare the cross-repository edge")
	fleet.push("api")
	// Only the provider declares the link, so the run has to start there.
	fleet.linkOneWay("sdk", "api")
	sdk := fleet.peer("sdk")

	res := sdk.Release("--package", "*")
	assert.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout+res.Stderr, "repository api holds no fleet link to sdk")
	assert.Empty(t, tagsIn(sdk.Repo, ".links/api"), "the consumer never published")
}

// TestChoreographySettlesTwoProvidersInOneCommit: a consumer reading two
// repositories records both hops, in one commit and in a fixed order, so the
// tree its tag sits on carries every revision that release incorporated.
func TestChoreographySettlesTwoProvidersInOneCommit(t *testing.T) {
	fleet := newChoreographyFleet(t, "api", "core", "sdk")
	fleet.writeConfig("api", func(cfg *models.File) {
		cfg.Dependencies = models.Dependencies{
			{Consumer: "api-pkg", Provider: "sdk-pkg"},
			{Consumer: "api-pkg", Provider: "core-pkg"},
		}
	})
	fleet.peer("api").Commit("chore: depend on two repositories")
	fleet.push("api")
	fleet.link("api", "sdk")
	fleet.link("api", "core")
	api := fleet.peer("api")

	res := api.ReleaseOK("--package", "*")
	pins := settledPins(res)
	require.Len(t, pins, 1, "one commit records both hops")
	assert.Equal(t, []any{".links/core", ".links/sdk"}, pins[0]["links"],
		"the hops are recorded in one fixed order")
	for _, peer := range []string{"core", "sdk"} {
		assert.Equal(t, api.Git("-C", ".links/"+peer, "rev-parse", "HEAD"),
			gitlinkAt(api.Repo, "refs/tags/api-pkg@0.1.0", ".links/"+peer),
			"the commit the tag names carries %s", peer)
	}
}

// movingHookFleet is a pushing fleet whose entry runs one script at a named
// stage, making a commit nobody planned the first time it runs and nothing on
// any later run — which is what lets a scenario prove both the refusal and the
// convergence with one fixture.
func movingHookFleet(t *testing.T, stage string) (*choreographyFleet, string) {
	t.Helper()
	fleet := pushingFleet(t)
	api := fleet.peer("api")
	marker := api.Path("moved.once")
	fleet.writeConfig("api", func(cfg *models.File) {
		cfg.Dependencies = models.Dependencies{{Consumer: "api-pkg", Provider: "sdk-pkg"}}
		cfg.Commit = &models.CommitConfig{
			Enabled: models.Bool(true), Push: true, Branch: harness.DefaultBranch}
		cfg.Scripts["drift"] = models.Script{
			"if [ ! -f " + shellQuote(marker) + " ]; then : > " + shellQuote(marker) +
				"; git -C " + shellQuote(api.Root) + " commit -q --allow-empty -m 'chore: a commit nobody planned'; fi"}
		switch stage {
		case "beforeCommit":
			cfg.Run = &models.RunConfig{BeforeCommit: []string{"drift"}}
		default:
			cfg.Run = &models.RunConfig{BeforePush: []string{"drift"}}
		}
	})
	api.Commit("chore: move this repository under its own settlement")
	api.Git("push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)
	return fleet, marker
}

// TestChoreographyRefusesASettlementItsHeadMovedUnderneath: a settlement is
// planned against a revision and announces the one it creates, so a
// repository whose HEAD moves under it — before the commit or between the
// commit and the push — is drift. The package is refused before it publishes,
// and the next run of the same command releases it once.
func TestChoreographyRefusesASettlementItsHeadMovedUnderneath(t *testing.T) {
	for _, tc := range []struct {
		stage  string
		wanted string
	}{
		{stage: "beforeCommit", wanted: "repository changed after planning"},
		{stage: "beforePush", wanted: "HEAD moved from recorded source revision"},
	} {
		t.Run(tc.stage, func(t *testing.T) {
			fleet, marker := movingHookFleet(t, tc.stage)
			api := fleet.peer("api")

			res := api.CommandEnv(fileProtocolEnv(), "--package", "*")
			assert.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.Contains(t, res.Stdout+res.Stderr, tc.wanted)
			assert.Equal(t, 0, api.TagCount("api-pkg@"), "the consumer never published")
			assert.FileExists(t, marker, "the hook moved the repository exactly once")

			healed := api.CommandEnv(fileProtocolEnv(), "--package", "*")
			require.Equal(t, 0, healed.Code, "stdout:\n%s\nstderr:\n%s", healed.Stdout, healed.Stderr)
			assert.Equal(t, 1, api.TagCount("api-pkg@"), "the retry released it exactly once")
		})
	}
}

// TestChoreographyVerifiesAMovedDetachedPeerAgainstTheRoster: a pending
// settlement has to prove the provider's remote already holds the revision it
// is about to pin, and a linked checkout sits on no branch of its own. The
// branch that question is asked about comes from the roster — the one the
// repository that links it states, or any roster that states one at all — and
// a fleet where no roster states one refuses rather than guessing.
//
// It differs from TestChoreographyVerifiesADetachedPeerAgainstTheRoster in the
// one thing that makes the lookup happen: the provider has moved since the
// last release, so the consumer has a pin to record rather than one it already
// holds.
func TestChoreographyVerifiesAMovedDetachedPeerAgainstTheRoster(t *testing.T) {
	for _, tc := range []struct {
		name    string
		peers   []string
		stated  string
		refused bool
	}{
		{name: "no roster states a branch for it", peers: []string{"api", "sdk"}, refused: true},
		{name: "the roster of the repository that links it", peers: []string{"api", "sdk"}, stated: "api"},
		{name: "any roster that states one", peers: []string{"api", "sdk", "web"}, stated: "web"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fleet := newChoreographyFleet(t, tc.peers...)
			for _, name := range tc.peers {
				fleet.writeConfig(name, func(cfg *models.File) {
					if name == "api" {
						cfg.Dependencies = models.Dependencies{{Consumer: "api-pkg", Provider: "sdk-pkg"}}
					}
					cfg.Commit = &models.CommitConfig{
						Enabled: models.Bool(true), Push: true, Branch: harness.DefaultBranch}
				})
				fleet.peer(name).Commit("chore: state this repository's release policy")
				fleet.push(name)
			}
			for _, peer := range tc.peers[1:] {
				fleet.link("api", peer)
			}
			api := fleet.peer("api")
			require.Equal(t, 0, api.CommandEnv(fileProtocolEnv(), "--package", "*").Code,
				"the fleet releases once with every repository on a branch")

			// The ordinary shape afterwards: the linked checkout sits at the
			// revision the link records, on no branch, and moves without
			// releasing anything of its own.
			api.Git("-C", ".links/sdk", "checkout", "-q", "--detach")
			for _, name := range tc.peers[1:] {
				fleet.configureIn(api.Repo, name, "chore: leave the branch to the roster", func(cfg *models.File) {
					cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true), Push: true}
					for i := range cfg.Repositories {
						if tc.stated != name {
							cfg.Repositories[i].Branch = ""
						}
					}
				})
			}
			fleet.writeConfig("api", func(cfg *models.File) {
				cfg.Dependencies = models.Dependencies{{Consumer: "api-pkg", Provider: "sdk-pkg"}}
				cfg.Commit = &models.CommitConfig{
					Enabled: models.Bool(true), Push: true, Branch: harness.DefaultBranch}
				if tc.stated != "api" {
					for i := range cfg.Repositories {
						cfg.Repositories[i].Branch = ""
					}
				}
			})
			api.Commit("chore: leave the peers' branches to the roster")
			api.Git("push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)
			// The provider moves by a commit that releases nothing, and the
			// consumer's own change is committed without staging the drift, so
			// the settlement the next release makes is genuinely pending.
			api.WriteFile(".links/sdk/notes.txt", "moved without releasing\n")
			api.Git("-C", ".links/sdk", "add", "-A")
			api.Git("-C", ".links/sdk", "commit", "-q", "-m", "chore: move without releasing")
			api.Git("-C", ".links/sdk", "push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)
			fleet.workOnly("api", "fix(api-pkg): something to release")

			res := api.CommandEnv(fileProtocolEnv(), "--package", "*")
			if tc.refused {
				assert.NotZero(t, res.Code, "stdout:\n%s", res.Stdout)
				combined := res.Stdout + res.Stderr
				assert.Contains(t, combined, "has no branch to verify revision")
				assert.Contains(t, combined, "commit.branch")
				assert.Contains(t, combined, "fleet roster")
				assert.Equal(t, 1, api.TagCount("api-pkg@"), "the second release never published")
				return
			}
			require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.Equal(t, 2, api.TagCount("api-pkg@"))
			assert.Equal(t, api.Git("-C", ".links/sdk", "rev-parse", "HEAD"),
				gitlinkAt(api.Repo, "HEAD", ".links/sdk"))
		})
	}
}

// TestChoreographyRefusesToRecordALinkOverATrackedFile: a settlement records
// fleet links and nothing else. `update-index --cacheinfo` would happily
// replace a tracked file with a gitlink and the commit would delete that file
// from the tree, so a path the repository tracks as an ordinary file refuses
// the settlement instead.
func TestChoreographyRefusesToRecordALinkOverATrackedFile(t *testing.T) {
	fleet := crossRepositoryFleet(t)
	api := fleet.peer("api")
	// The checkout stays a real repository; only what HEAD records for that
	// path becomes an ordinary file, which is the state the guard is about.
	api.WriteFile("placeholder.txt", "a file where the link belongs\n")
	blob := api.Git("hash-object", "-w", "--", "placeholder.txt")
	api.Git("update-index", "--force-remove", "--", ".links/sdk")
	api.Git("update-index", "--add", "--cacheinfo", "100644,"+blob+",.links/sdk")
	api.Git("commit", "-q", "-m", "chore: track a file where the fleet link belongs")

	res := api.Release("--package", "*")
	assert.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout+res.Stderr, "is not a fleet link")
	assert.Equal(t, 0, api.TagCount("api-pkg@"), "the consumer never published")
}
