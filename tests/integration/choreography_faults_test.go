// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// What a choreographed fleet does when Git itself refuses.
//
// Everything a fleet reads and everything it records is a Git invocation, and
// each one is a way the work can stop. A broken object store is not a state a
// fixture can assemble, so the invocation is made to fail instead: the runs
// here drive the real binary against real repositories with a stand-in `git`
// first on their PATH (see internal/harness/gitfault.go), which passes every
// call through to the real Git except the one the scenario is about.
//
// What each of them proves is the same shape: the run stops, it says so in its
// own words rather than leaving a Git status behind, nothing was published,
// and the same command without the fault converges.

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// faultingConsumerFleet is the fleet every settlement fault is injected into:
// a consumer in one repository reading a provider in another, whose publish
// stage leaves a mark so a scenario can prove it never ran.
func faultingConsumerFleet(t *testing.T) (*choreographyFleet, string) {
	t.Helper()
	fleet := crossRepositoryFleet(t)
	api := fleet.peer("api")
	marker := api.Path("published.txt")
	fleet.writeConfig("api", func(cfg *models.File) {
		cfg.Dependencies = models.Dependencies{{Consumer: "api-pkg", Provider: "sdk-pkg"}}
		cfg.Scripts["publish"] = models.Script{"printf '%s\\n' \"$DISPAT_PACKAGE\" >> " + shellQuote(marker)}
	})
	api.Commit("chore: leave a mark when a package publishes")
	return fleet, marker
}

// TestChoreographyFaultRefusesASettlementItCannotRecord: a settlement is made
// before publication precisely so that it can fail, and every Git call it
// makes is a way it does. Whichever one refuses, the consumer never publishes
// and the same command run afterwards without the fault releases it once.
func TestChoreographyFaultRefusesASettlementItCannotRecord(t *testing.T) {
	for _, tc := range []struct {
		name string
		// wanted is what the run says about the call that failed, which is how
		// a row proves it reached the arm it is about rather than some other
		// invocation of the same verb.
		wanted  string
		matches int
		fault   harness.GitFault
	}{
		{name: "the links this repository already records", wanted: "reading its fleet links",
			matches: 1, fault: harness.GitFault{Pattern: "*ls-tree -z*", Nth: 1}},
		{name: "the tree the settlement starts from", wanted: "recording fleet links",
			matches: 2, fault: harness.GitFault{Pattern: "*ls-tree -z*", Nth: 2}},
		{name: "the temporary index it builds the commit in", wanted: "recording fleet links",
			matches: 1, fault: harness.GitFault{Pattern: "*read-tree*"}},
		{name: "the pins written into that index", wanted: "recording fleet links",
			matches: 1, fault: harness.GitFault{Pattern: "*update-index --add --cacheinfo*", Nth: 1}},
		{name: "the tree it writes", wanted: "recording fleet links",
			matches: 1, fault: harness.GitFault{Pattern: "*write-tree*"}},
		{name: "the commit it builds", wanted: "recording fleet links",
			matches: 1, fault: harness.GitFault{Pattern: "*commit-tree*"}},
		{name: "the reference it moves", wanted: "recording fleet links",
			matches: 1, fault: harness.GitFault{Pattern: "*update-ref*"}},
		{name: "the index refresh afterwards", wanted: "the index still holds the previous pins",
			matches: 2, fault: harness.GitFault{Pattern: "*update-index --add --cacheinfo*", Nth: 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fleet, marker := faultingConsumerFleet(t)
			api := fleet.peer("api")
			fault := harness.NewGitFault(t, tc.fault)

			res := api.CommandEnv(append(fileProtocolEnv(), fault.Env()...), "--package", "*")
			assert.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.Contains(t, res.Stdout+res.Stderr, harness.GitFaultMarker,
				"the run stopped on the injected failure")
			assert.Contains(t, res.Stdout+res.Stderr, tc.wanted)
			assert.Equal(t, tc.matches, fault.Matches(), "exactly the selected calls were made")
			assert.Equal(t, 0, api.TagCount("api-pkg@"), "the consumer never published")
			assert.NoFileExists(t, marker, "no publish stage ran for the refused package")

			healed := api.CommandEnv(fileProtocolEnv(), "--package", "*")
			require.Equal(t, 0, healed.Code, "stdout:\n%s\nstderr:\n%s", healed.Stdout, healed.Stderr)
			assert.Equal(t, 1, api.TagCount("api-pkg@"), "the retry released it exactly once")
			assert.Equal(t, api.Git("-C", ".links/sdk", "rev-parse", "HEAD"),
				gitlinkAt(api.Repo, "refs/tags/api-pkg@0.1.0", ".links/sdk"),
				"and the commit the tag names carries the revision it incorporated")
		})
	}
}

// TestChoreographyFaultCannotReadTheRevisionToSettle: the far end of a route
// records nothing; what it contributes is the revision its parent has to pin,
// and a repository that cannot say which revision that is stops the consumer
// before it publishes rather than pinning a guess.
//
// The provider is quiet here — it released in the first run and has not moved
// since — so the settlement is the fourth and last time this run asks that
// repository what it is at: the plan, the post-plan check, the snapshot guard
// and then this.
func TestChoreographyFaultCannotReadTheRevisionToSettle(t *testing.T) {
	fleet, marker := faultingConsumerFleet(t)
	api := fleet.peer("api")
	api.ReleaseOK("--package", "*")
	require.NoError(t, os.Remove(marker))
	fleet.workOnly("api", "fix(api-pkg): a change of its own")

	fault := harness.NewGitFault(t, harness.GitFault{
		Pattern: "*/.links/sdk rev-parse HEAD^{commit}*", Nth: 4})
	res := api.CommandEnv(append(fileProtocolEnv(), fault.Env()...), "--package", "*")
	assert.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout+res.Stderr, "repository sdk: reading the revision to settle")
	assert.Equal(t, 1, api.TagCount("api-pkg@"), "the second release never published")
	assert.NoFileExists(t, marker, "no publish stage ran for the refused package")

	healed := api.CommandEnv(fileProtocolEnv(), "--package", "*")
	require.Equal(t, 0, healed.Code, "stdout:\n%s\nstderr:\n%s", healed.Stdout, healed.Stderr)
	assert.Equal(t, 2, api.TagCount("api-pkg@"), "the retry released it exactly once")
}

// TestChoreographyFaultReadsAReplyItCannotParse: Git's plumbing output is a
// format, and a record that is not in it is reported as the malformed answer
// it is rather than parsed into a pin or a subject nobody wrote.
func TestChoreographyFaultReadsAReplyItCannotParse(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fault  harness.GitFault
		wanted string
	}{
		{name: "a tree entry with no path", wanted: "malformed ls-tree record",
			fault: harness.GitFault{Pattern: "*ls-tree -z*", Output: "160000 commit deadbeef"}},
		{name: "a tree entry that is missing a field", wanted: "malformed ls-tree metadata",
			fault: harness.GitFault{Pattern: "*ls-tree -z*", Output: "160000 commit\t.links/sdk"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fleet, marker := faultingConsumerFleet(t)
			api := fleet.peer("api")
			fault := harness.NewGitFault(t, tc.fault)

			res := api.CommandEnv(append(fileProtocolEnv(), fault.Env()...), "--package", "*")
			assert.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.Contains(t, res.Stdout+res.Stderr, tc.wanted)
			assert.Equal(t, 0, api.TagCount("api-pkg@"), "the consumer never published")
			assert.NoFileExists(t, marker)
		})
	}
}

// TestChoreographyFaultCannotReadTheReleaseSubjects: the first thing every
// boundary asks is whether a tag sits on a release commit, and a repository
// whose log cannot be read stops the plan naming that repository rather than
// answering that it proves nothing.
func TestChoreographyFaultCannotReadTheReleaseSubjects(t *testing.T) {
	fleet := crossRepositoryFleet(t)
	api := fleet.peer("api")
	api.ReleaseOK("--package", "*")
	fleet.workIn(api.Repo, "sdk", "sdk-pkg", "fix(sdk-pkg): work the consumer has not seen")

	fault := harness.NewGitFault(t, harness.GitFault{Pattern: "*log --no-walk*"})
	res := api.CommandEnv(append(fileProtocolEnv(), fault.Env()...), "status", "--package", "*")
	assert.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	combined := res.Stdout + res.Stderr
	assert.Contains(t, combined, "release subjects")
	assert.Contains(t, combined, harness.GitFaultMarker)

	// An answer that is not the format is the same refusal, said in the
	// parser's own words rather than as a subject nobody wrote.
	garbled := harness.NewGitFault(t, harness.GitFault{
		Pattern: "*log --no-walk*", Output: "a line with no record separator"})
	malformed := api.CommandEnv(append(fileProtocolEnv(), garbled.Env()...), "status", "--package", "*")
	assert.NotZero(t, malformed.Code, "stdout:\n%s", malformed.Stdout)
	assert.Contains(t, malformed.Stdout+malformed.Stderr, "malformed log record")

	healed := api.StatusOK("--package", "*")
	requireNoDiagnostic(t, healed, "E333")
}

// TestChoreographyFaultCannotReadALinkTreeWhilePlanning: a tree a boundary
// walk cannot read is an unproven boundary, which is E333 naming the tuple
// that states it, never a Git status escaping as a fatal.
func TestChoreographyFaultCannotReadALinkTreeWhilePlanning(t *testing.T) {
	fleet := crossRepositoryFleet(t)
	api := fleet.peer("api")
	api.ReleaseOK("--package", "*")
	provider := api.Git("-C", ".links/sdk", "rev-parse", "HEAD")
	fleet.workIn(api.Repo, "sdk", "sdk-pkg", "fix(sdk-pkg): work the consumer has not seen")

	fault := harness.NewGitFault(t, harness.GitFault{Pattern: "*ls-tree -z*"})
	res := api.CommandEnv(append(fileProtocolEnv(), fault.Env()...), "status", "--package", "*")
	assert.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	requireDiagnostic(t, res, "E333")
	assert.Contains(t, res.Stdout+res.Stderr, "cannot be read")

	// The tuple the diagnostic names is the remedy, and it needs no Git read
	// of its own.
	fleet.writeConfig("api", func(cfg *models.File) {
		cfg.Dependencies = models.Dependencies{{Consumer: "api-pkg", Provider: "sdk-pkg"}}
		cfg.RepositoryBaselines = []models.RepositoryBaselineConfig{{
			Consumer: "api-pkg", ReleaseTag: "api-pkg@0.1.0", Repository: "sdk", Revision: provider}}
	})
	api.Commit("chore: state the boundary the links cannot be read for")
	requireNoDiagnostic(t, api.StatusOK("--package", "*"), "E333")
}

// TestChoreographyFaultCannotComposeTheFleet: composition asks each repository
// two questions through Git, and a repository that answers neither is the same
// E330 as one that was never materialized — the fleet is refused, not walked
// past.
func TestChoreographyFaultCannotComposeTheFleet(t *testing.T) {
	for _, tc := range []struct {
		name  string
		fault harness.GitFault
	}{
		{"the revision a repository is at", harness.GitFault{Pattern: "* rev-parse HEAD"}},
		{"whether a link path is its own repository", harness.GitFault{
			Pattern: "*rev-parse --show-toplevel*"}},
		{"whether a repository holds its whole history", harness.GitFault{
			Pattern: "*rev-parse --is-shallow-repository*"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fleet := newChoreographyFleet(t, "api", "sdk")
			fleet.link("api", "sdk")
			api := fleet.peer("api")
			fault := harness.NewGitFault(t, tc.fault)

			res := api.CommandEnv(append(fileProtocolEnv(), fault.Env()...), "status", "--package", "*")
			assert.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			requireDiagnostic(t, res, "E330")

			assert.ElementsMatch(t, []string{"api", "sdk"},
				composedRepositories(api.StatusOK("--package", "*")),
				"the same checkout composes once Git answers again")
		})
	}
}

// TestChoreographyFaultCannotReadTheLinkInventory: `.gitmodules` is read
// through Git, and the two statuses it answers with mean different things —
// "no key matched" is a repository with no fleet links, anything else is a
// file that cannot be read and is refused as one.
func TestChoreographyFaultCannotReadTheLinkInventory(t *testing.T) {
	for _, tc := range []struct {
		name     string
		fault    harness.GitFault
		refused  bool
		composed []string
	}{
		{name: "a file that cannot be read at all", refused: true,
			fault: harness.GitFault{Pattern: "*--get-regexp*", Code: 128}},
		{name: "a file that names no links", composed: []string{"api"},
			fault: harness.GitFault{Pattern: "*--get-regexp*", Code: 1}},
		{name: "an answer that is not the format", refused: true,
			fault: harness.GitFault{Pattern: "*--get-regexp*", Output: "submodule.sdk.path"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fleet := newChoreographyFleet(t, "api", "sdk")
			fleet.link("api", "sdk")
			api := fleet.peer("api")
			fault := harness.NewGitFault(t, tc.fault)

			res := api.CommandEnv(append(fileProtocolEnv(), fault.Env()...), "status", "--package", "*")
			if tc.refused {
				assert.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
				requireDiagnostic(t, res, "E330")
				assert.Contains(t, res.Stdout+res.Stderr, ".gitmodules")
				return
			}
			require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.Equal(t, tc.composed, composedRepositories(res),
				"a repository with no links is a fleet of one")
		})
	}
}

// TestChoreographyFaultStopsComputeAndConvergesOnASecondRun: a fleet link
// `compute` could not finish writing is reported rather than claimed, and the
// same command run afterwards without the fault completes the fleet.
func TestChoreographyFaultStopsComputeAndConvergesOnASecondRun(t *testing.T) {
	fleet := unlinkedFleet(t, "api", "sdk")
	api := fleet.peer("api")
	fault := harness.NewGitFault(t, harness.GitFault{Pattern: "*submodule add*"})

	res := api.CommandEnv(append(fileProtocolEnv(), fault.Env()...), "compute", "--write")
	assert.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout+res.Stderr, harness.GitFaultMarker)
	assert.Contains(t, res.Stdout+res.Stderr, "creating the fleet link to sdk")
	assert.NoDirExists(t, api.Path(".links", "sdk", "packages"),
		"nothing was cloned for a link that could not be created")

	healed := api.CommandEnv(fileProtocolEnv(), "compute", "--write")
	require.Equal(t, 0, healed.Code, "stdout:\n%s\nstderr:\n%s", healed.Stdout, healed.Stderr)
	assert.Equal(t, ".links/api",
		api.Git("-C", ".links/sdk", "config", "--file", ".gitmodules", "submodule.api.path"))
	api.Commit("chore: link the fleet")
	commitLinked(api, ".links/sdk", "chore: link back")
	assert.ElementsMatch(t, []string{"api", "sdk"},
		composedRepositories(api.StatusOK("--package", "*")))
}

// TestChoreographyFaultStopsComputeDeclaringTheOtherHalf: the back half of a
// link is a declaration and a pin written inside the peer, and a run that
// cannot write either says which repository it was for and commits nothing.
//
// What it leaves behind is a link declared at one end only, which is exactly
// what composition reports as W332 and what compute repairs, so the same
// command run again without the fault finishes the job.
func TestChoreographyFaultStopsComputeDeclaringTheOtherHalf(t *testing.T) {
	for _, tc := range []struct {
		name   string
		wanted string
		fault  harness.GitFault
	}{
		{name: "the declaration in .gitmodules", wanted: "declaring the other half of the link to api",
			fault: harness.GitFault{Pattern: "*config --file .gitmodules*", Nth: 1}},
		{name: "the pin it stages", wanted: "declaring the other half of the link to api",
			fault: harness.GitFault{Pattern: "*update-index --add --cacheinfo*"}},
		{name: "the revision to pin it at", wanted: "the revision to pin the back link at",
			fault: harness.GitFault{Pattern: "*rev-parse HEAD^{commit}*"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fleet := unlinkedFleet(t, "api", "sdk")
			api := fleet.peer("api")
			fault := harness.NewGitFault(t, tc.fault)

			res := api.CommandEnv(append(fileProtocolEnv(), fault.Env()...), "compute", "--write")
			assert.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.Contains(t, res.Stdout+res.Stderr, tc.wanted)
			assert.Empty(t, api.Git("-C", ".links/sdk", "ls-tree", "HEAD", "--", ".gitmodules"),
				"nothing a half-written declaration left behind was committed in the peer")

			healed := api.CommandEnv(fileProtocolEnv(), "compute", "--write")
			require.Equal(t, 0, healed.Code, "stdout:\n%s\nstderr:\n%s", healed.Stdout, healed.Stderr)
			assert.Equal(t, ".links/api",
				api.Git("-C", ".links/sdk", "config", "--file", ".gitmodules", "submodule.api.path"))
			requireNoDiagnostic(t, api.StatusOK("--package", "*"), "W332")
		})
	}
}

// TestChoreographyFaultStopsComputeInitializingADeclaredLink: a declared link
// whose checkout compute cannot make is the one change of that run, and the
// failure names the repository and the path rather than reporting a repaired
// fleet.
func TestChoreographyFaultStopsComputeInitializingADeclaredLink(t *testing.T) {
	fleet := unlinkedFleet(t, "api", "sdk")
	fleet.link("api", "sdk")
	api := fleet.peer("api")
	api.Git("submodule", "deinit", "-f", "--", ".links/sdk")

	fault := harness.NewGitFault(t, harness.GitFault{Pattern: "*submodule update --init*"})
	res := api.CommandEnv(append(fileProtocolEnv(), fault.Env()...), "compute", "--write")
	assert.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout+res.Stderr, "initializing the fleet link at .links/sdk")
	assert.NoFileExists(t, api.Path(".links", "sdk", "dispat.json"))

	healed := api.CommandEnv(fileProtocolEnv(), "compute", "--write")
	require.Equal(t, 0, healed.Code, "stdout:\n%s\nstderr:\n%s", healed.Stdout, healed.Stderr)
	assert.FileExists(t, api.Path(".links", "sdk", "dispat.json"))
}

// TestChoreographyFaultCannotReadTheConsumersOwnRevision: the repository that
// records a settlement reads its own HEAD twice — once to know what it is
// settling on top of, and once inside the commit it builds through a temporary
// index. Either read failing refuses the package before it publishes.
//
// The ordinals are this run's own: by the time the settlement starts, the
// consumer has been asked what it is at three times, and the commit asks twice
// more with the drift check between them. They are asserted through the
// sentence each arm reports, so a run that asks differently fails here rather
// than passing against the wrong call.
func TestChoreographyFaultCannotReadTheConsumersOwnRevision(t *testing.T) {
	for _, tc := range []struct {
		name   string
		nth    int
		wanted string
	}{
		{name: "the revision it settles on top of", nth: 4,
			wanted: "repository api: reading the revision to settle"},
		{name: "the parent of the commit it builds", nth: 6,
			wanted: "repository api: recording fleet links"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fleet, marker := faultingConsumerFleet(t)
			api := fleet.peer("api")
			fault := harness.NewGitFault(t, harness.GitFault{
				Pattern: "*-C " + canonicalRoot(t, api.Repo) + " rev-parse HEAD^{commit}*",
				Nth:     tc.nth})

			res := api.CommandEnv(append(fileProtocolEnv(), fault.Env()...), "--package", "*")
			assert.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.Contains(t, res.Stdout+res.Stderr, tc.wanted)
			assert.Equal(t, 0, api.TagCount("api-pkg@"), "the consumer never published")
			assert.NoFileExists(t, marker, "no publish stage ran for the refused package")

			healed := api.CommandEnv(fileProtocolEnv(), "--package", "*")
			require.Equal(t, 0, healed.Code, "stdout:\n%s\nstderr:\n%s", healed.Stdout, healed.Stderr)
			assert.Equal(t, 1, api.TagCount("api-pkg@"), "the retry released it exactly once")
		})
	}
}
