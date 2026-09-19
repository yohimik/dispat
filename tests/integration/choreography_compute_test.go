// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// `dispat compute` on a fleet that is not linked yet: what it proposes, what
// it writes, and what it refuses to do on its own.

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

// unlinkedFleet is a fleet whose members know about each other and are joined
// by nothing, which is the state a new fleet starts in.
func unlinkedFleet(t *testing.T, names ...string) *choreographyFleet {
	t.Helper()
	return newChoreographyFleet(t, names...)
}

// TestChoreographyComputeLinksAnUnlinkedFleet: the roster says who belongs,
// and compute is what turns that into the links a release walks.
func TestChoreographyComputeLinksAnUnlinkedFleet(t *testing.T) {
	fleet := unlinkedFleet(t, "api", "sdk")
	api := fleet.peer("api")

	check := api.CommandEnv(fileProtocolEnv(), "compute", "--check")
	assert.Equal(t, 1, check.Code, "an unlinked fleet is work pending")
	assert.Contains(t, check.Stdout, "+ link api sdk")

	res := api.CommandEnv(fileProtocolEnv(), "compute", "--write")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "linked sdk from api")
	assert.DirExists(t, api.Path(".links", "sdk", "packages"))
	assert.Equal(t, ".links/sdk", api.Git("config", "--file", ".gitmodules", "submodule.sdk.path"))

	// The other half is declared inside the peer's checkout, without cloning
	// anything and without committing.
	assert.Equal(t, ".links/api",
		api.Git("-C", ".links/sdk", "config", "--file", ".gitmodules", "submodule.api.path"))
	assert.DirExists(t, api.Path(".links", "sdk", ".links", "api"))
	staged := api.Git("-C", ".links/sdk", "diff", "--cached", "--name-only")
	assert.Contains(t, staged, ".gitmodules")
	assert.Contains(t, staged, ".links/api")

	// Committed, the fleet composes.
	api.Commit("chore: link the fleet")
	commitLinked(api, ".links/sdk", "chore: link back")
	assert.ElementsMatch(t, []string{"api", "sdk"},
		composedRepositories(api.StatusOK("--package", "*")))

	// And computing again proposes nothing.
	settled := api.CommandEnv(fileProtocolEnv(), "compute", "--check")
	assert.Equal(t, 0, settled.Code, "stdout:\n%s", settled.Stdout)
	assert.Contains(t, settled.Stdout, "fleet links")
}

// TestChoreographyComputeConnectsWithoutASecondPath: three repositories need
// two links, never three, because a third would be a second route between two
// of them and composition refuses those.
func TestChoreographyComputeConnectsWithoutASecondPath(t *testing.T) {
	fleet := unlinkedFleet(t, "api", "sdk", "web")
	api := fleet.peer("api")

	res := api.CommandEnv(fileProtocolEnv(), "compute", "--write")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, 2, strings.Count(res.Stdout, "+ link "), "two links connect three repositories")
	assert.DirExists(t, api.Path(".links", "sdk", "packages"))
	assert.DirExists(t, api.Path(".links", "web", "packages"))

	api.Commit("chore: link the fleet")
	for _, peer := range []string{"sdk", "web"} {
		commitLinked(api, ".links/"+peer, "chore: link back")
	}
	status := api.StatusOK("--package", "*")
	assert.ElementsMatch(t, []string{"api", "sdk", "web"}, composedRepositories(status))
	requireNoDiagnostic(t, status, "E338")
}

// TestChoreographyComputeInitializesADeclaredLink: a link the fleet declares
// and the checkout is missing is proposed as its own change.
func TestChoreographyComputeInitializesADeclaredLink(t *testing.T) {
	fleet := unlinkedFleet(t, "api", "sdk")
	fleet.link("api", "sdk")
	api := fleet.peer("api")
	// Deinitializing is what a fresh clone looks like before the fleet is
	// materialized.
	api.Git("submodule", "deinit", "-f", "--", ".links/sdk")

	check := api.CommandEnv(fileProtocolEnv(), "compute", "--check")
	assert.Equal(t, 1, check.Code)
	assert.Contains(t, check.Stdout, "+ init api .links/sdk")

	res := api.CommandEnv(fileProtocolEnv(), "compute", "--write")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "initialized .links/sdk in api")
	assert.FileExists(t, api.Path(".links", "sdk", "dispat.json"))
	assert.ElementsMatch(t, []string{"api", "sdk"},
		composedRepositories(api.StatusOK("--package", "*")))
}

// TestChoreographyComputeWritesARosterEntryIntoItsOwnerConfig: a peer's
// roster is its own statement, so the entry lands in that peer's file.
func TestChoreographyComputeWritesARosterEntryIntoItsOwnerConfig(t *testing.T) {
	fleet := newChoreographyFleet(t, "api", "sdk", "web")
	fleet.link("api", "sdk")
	fleet.link("api", "web")
	fleet.follow("api", "sdk")
	api := fleet.peer("api")
	// sdk has never heard of web.
	fleet.configureIn(api.Repo, "sdk", "chore: narrow the roster", func(cfg *models.File) {
		cfg.Repositories = []models.RepositoryLinkConfig{{
			Name: "api", URL: fleet.peer("api").remote, Branch: harness.DefaultBranch}}
	})

	check := api.CommandEnv(fileProtocolEnv(), "compute", "--check")
	assert.Equal(t, 1, check.Code)
	assert.Contains(t, check.Stdout, "+ repository sdk web")

	res := api.CommandEnv(fileProtocolEnv(), "compute", "--write")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	written := readAbs(t, api.Path(".links", "sdk", "dispat.json"))
	assert.Contains(t, written, `"name": "web"`, "the roster entry landed in the peer's own file")
	assert.NotContains(t, readAbs(t, api.Path("dispat.json")), `"repositories"`+`: null`)

	api.Git("-C", ".links/sdk", "add", "-A")
	commitLinked(api, ".links/sdk", "chore: learn about web")
	requireNoDiagnostic(t, api.StatusOK("--package", "*"), "W333")
}

// TestChoreographyComputeWithholdsARosterEntryWithNoURL: an entry nothing can
// be fetched from would repair the roster only halfway, so it is reported
// instead of written.
func TestChoreographyComputeWithholdsARosterEntryWithNoURL(t *testing.T) {
	fleet := newChoreographyFleet(t, "api", "sdk")
	fleet.link("api", "sdk")
	fleet.writeConfig("api", func(cfg *models.File) {
		cfg.Repositories = append(cfg.Repositories, models.RepositoryLinkConfig{Name: "web"})
	})
	api := fleet.peer("api")
	api.Commit("chore: name a repository nobody can fetch")

	res := api.CommandEnv(fileProtocolEnv(), "compute", "--check")
	assert.NotContains(t, res.Stdout, "+ repository sdk web")
	assert.Contains(t, res.Stdout+res.Stderr, "W333")
	assert.Contains(t, res.Stdout+res.Stderr, "no roster states a url")
}

// TestChoreographyComputeAsksBeforeEachLink: interactive mode is per
// suggestion, and a refusal leaves the fleet exactly as it was.
func TestChoreographyComputeAsksBeforeEachLink(t *testing.T) {
	fleet := unlinkedFleet(t, "api", "sdk")
	api := fleet.peer("api")

	res := api.CommandInput("n\n", "compute", "--interactive")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "apply? [y/N]")
	assert.NoDirExists(t, api.Path(".links", "sdk"))

	yes := api.CommandInputEnv(fileProtocolEnv(), "y\n", "compute", "--interactive")
	require.Equal(t, 0, yes.Code, "stdout:\n%s\nstderr:\n%s", yes.Stdout, yes.Stderr)
	assert.DirExists(t, api.Path(".links", "sdk", "packages"))
}

// TestChoreographyComputeChangesNothingWithoutAnApplyFlag: reading a fleet is
// not repairing one.
func TestChoreographyComputeChangesNothingWithoutAnApplyFlag(t *testing.T) {
	fleet := unlinkedFleet(t, "api", "sdk")
	api := fleet.peer("api")

	res := api.CommandEnv(fileProtocolEnv(), "compute")
	require.Equal(t, 0, res.Code)
	assert.Contains(t, res.Stdout, "apply all with --write")
	assert.NoDirExists(t, api.Path(".links", "sdk"))
	assert.Equal(t, "", api.Git("status", "--porcelain=v1"), "nothing was written at all")
}

// TestChoreographyComputeReadsAFleetThatDoesNotCompose: compute exists to
// repair a fleet, so a second route between two repositories is a finding it
// reports rather than a refusal that stops it reading anything.
func TestChoreographyComputeReadsAFleetThatDoesNotCompose(t *testing.T) {
	fleet := newChoreographyFleet(t, "api", "sdk", "web")
	fleet.link("api", "sdk")
	fleet.link("api", "web")
	fleet.link("sdk", "web")
	fleet.follow("api", "sdk")
	api := fleet.peer("api")

	refused := api.Status("--package", "*")
	assert.Equal(t, 1, refused.Code, "a release cannot read a fleet with two routes")
	requireDiagnostic(t, refused, "E338")

	res := api.CommandEnv(fileProtocolEnv(), "compute")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout+res.Stderr, "E338", "the ring is reported where it can be repaired")
	assert.NotContains(t, res.Stdout, "connects ",
		"a fleet already joined needs no more links; the half of a link only one end "+
			"declares is a declaration rather than a route, and is proposed as one")
}

// TestChoreographyComputeSummarisesWhatItActuallyDid: a run that created
// links and edited no file must not claim to have written one, nor point at
// backup copies nobody made.
func TestChoreographyComputeSummarisesWhatItActuallyDid(t *testing.T) {
	fleet := unlinkedFleet(t, "api", "sdk")
	api := fleet.peer("api")

	res := api.CommandEnv(fileProtocolEnv(), "compute", "--write")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "linked sdk from api")
	assert.Contains(t, res.Stdout, "fleet link operation(s)")
	assert.NotContains(t, res.Stdout, "applied 1 change(s) to  ")
	assert.NotContains(t, res.Stdout, ".backup",
		"no configuration file was copied, so none is named")

	// A run that does edit a file still says which one, and says it once.
	fleet.configureIn(api.Repo, "sdk", "chore: narrow the roster", func(cfg *models.File) {
		cfg.Repositories = nil
	})
	fleet.writeConfig("api", func(cfg *models.File) {
		cfg.Repositories = append(cfg.Repositories, models.RepositoryLinkConfig{
			Name: "web", URL: api.Path("nothing-to-clone"), Branch: harness.DefaultBranch})
	})
	api.Commit("chore: name a third repository")
	edited := api.CommandEnv(fileProtocolEnv(), "compute", "--write")
	assert.Contains(t, edited.Stdout, "applied ")
	assert.Contains(t, edited.Stdout, "dispat.json")
	assert.Contains(t, edited.Stdout, ".backup")
}

// TestChoreographyComputeProposesNothingForAFleetOfOne: a fleet needs two
// members before anything can join them, so a repository whose roster names
// nobody is already as linked as it can be.
func TestChoreographyComputeProposesNothingForAFleetOfOne(t *testing.T) {
	fleet := newChoreographyFleet(t, "api")
	api := fleet.peer("api")

	res := api.CommandEnv(fileProtocolEnv(), "compute", "--check")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "fleet links")
	assert.NotContains(t, res.Stdout, "+ link ")
	assert.Equal(t, "", api.Git("status", "--porcelain=v1"))
}

// TestChoreographyComputeLinksOnlyWhatItCanReach: a link is created inside a
// checkout this run holds, so a pair of roster members it never walked into is
// a pair it cannot join — it says what it can do and leaves the rest.
func TestChoreographyComputeLinksOnlyWhatItCanReach(t *testing.T) {
	// The entry sorts last, so the first pair the spanning tree considers is
	// two repositories this run has no checkout of.
	fleet := newChoreographyFleet(t, "zed")
	zed := fleet.peer("zed")
	fleet.writeConfig("zed", func(cfg *models.File) {
		cfg.Repositories = []models.RepositoryLinkConfig{
			{Name: "shop", URL: zed.Path("no-such-remote-shop"), Branch: harness.DefaultBranch},
			{Name: "web", URL: zed.Path("no-such-remote-web"), Branch: harness.DefaultBranch},
		}
	})
	zed.Commit("chore: name two repositories this checkout has never seen")

	res := zed.CommandEnv(fileProtocolEnv(), "compute", "--check")
	assert.Equal(t, 1, res.Code, "an unlinked fleet is work pending")
	assert.Contains(t, res.Stdout, "+ link zed shop")
	assert.NotContains(t, res.Stdout, "+ link shop web",
		"neither end of that pair is a checkout this run can write in")
}

// TestChoreographyComputeUsesTheLinkPathTheRosterAsks: the fleet default is a
// default, and a roster entry that states a path of its own is where the link
// is made.
func TestChoreographyComputeUsesTheLinkPathTheRosterAsks(t *testing.T) {
	fleet := unlinkedFleet(t, "api", "sdk")
	api := fleet.peer("api")
	fleet.writeConfig("api", func(cfg *models.File) {
		cfg.Repositories = []models.RepositoryLinkConfig{{
			Name: "sdk", URL: fleet.peer("sdk").remote,
			Path: "vendor/./sdk", Branch: harness.DefaultBranch}}
	})
	api.Commit("chore: ask for a link path of its own")

	res := api.CommandEnv(fileProtocolEnv(), "compute", "--write")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "linked sdk from api at vendor/sdk")
	assert.DirExists(t, api.Path("vendor", "sdk", "packages"))
	assert.Equal(t, "vendor/sdk", api.Git("config", "--file", ".gitmodules", "submodule.sdk.path"))
}

// TestChoreographyComputeWithholdsALinkWithNoURL: a link is a checkout, and a
// roster entry nothing can be fetched from is reported rather than attempted.
func TestChoreographyComputeWithholdsALinkWithNoURL(t *testing.T) {
	fleet := newChoreographyFleet(t, "api", "sdk")
	fleet.link("api", "sdk")
	fleet.writeConfig("api", func(cfg *models.File) {
		cfg.Repositories = append(cfg.Repositories, models.RepositoryLinkConfig{Name: "web"})
	})
	api := fleet.peer("api")
	api.Commit("chore: name a repository nobody can fetch")

	res := api.CommandEnv(fileProtocolEnv(), "compute", "--write")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout+res.Stderr, "the roster states no url for this peer")
	assert.NoDirExists(t, api.Path(".links", "web"))
}

// TestChoreographyComputePinsTheBackLinkAtWhatTheRemoteHolds: the other half
// of a link points at this repository as its peer will fetch it, so the
// revision comes from the remote the configuration names — and a remote that
// cannot answer is said out loud rather than papered over.
func TestChoreographyComputePinsTheBackLinkAtWhatTheRemoteHolds(t *testing.T) {
	t.Run("the remote the configuration names", func(t *testing.T) {
		fleet := unlinkedFleet(t, "api", "sdk")
		api := fleet.peer("api")
		remote := api.Git("remote", "get-url", "origin")
		api.Git("remote", "rename", "origin", "upstream")
		fleet.writeConfig("api", func(cfg *models.File) {
			cfg.Commit = &models.CommitConfig{
				Enabled: models.Bool(true), Branch: harness.DefaultBranch, Remote: "upstream"}
		})
		api.Commit("chore: publish through a remote of its own name")
		api.Git("push", "-q", "upstream", "HEAD:refs/heads/"+harness.DefaultBranch)

		res := api.CommandEnv(fileProtocolEnv(), "compute", "--write")
		require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Equal(t, remote,
			api.Git("-C", ".links/sdk", "config", "--file", ".gitmodules", "submodule.api.url"))
	})

	t.Run("a remote that does not hold the branch yet", func(t *testing.T) {
		fleet := unlinkedFleet(t, "api", "sdk")
		api := fleet.peer("api")
		empty := filepath.Join(t.TempDir(), "empty.git")
		api.Git("init", "-q", "--bare", empty)
		api.Git("remote", "set-url", "origin", empty)
		fleet.writeConfig("api", func(cfg *models.File) {
			cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true)}
		})
		api.Commit("chore: leave the branch to the checkout")

		res := api.CommandEnv(fileProtocolEnv(), "compute", "--write")
		require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Contains(t, res.Stdout+res.Stderr, "a revision the remote does not have yet")
		assert.Contains(t, api.Git("-C", ".links/sdk", "ls-files", "-s", "--", ".links/api"),
			api.Git("rev-parse", "HEAD"),
			"the back link is staged at this repository's own head")
	})

	t.Run("no remote at all", func(t *testing.T) {
		fleet := unlinkedFleet(t, "api", "sdk")
		api := fleet.peer("api")
		api.Git("remote", "remove", "origin")

		res := api.CommandEnv(fileProtocolEnv(), "compute", "--write")
		require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Contains(t, res.Stdout+res.Stderr, "no remote to be fetched from")
		assert.NoDirExists(t, api.Path(".links", "sdk", ".links", "api"))

		api.Commit("chore: link the fleet halfway")
		composed := api.StatusOK("--package", "*")
		assert.ElementsMatch(t, []string{"api", "sdk"}, composedRepositories(composed))
		requireDiagnostic(t, composed, "W332")
	})
}

// TestChoreographyComputeRefusesARosterURLCarryingASecret: a fleet link's URL
// is written into a committed `.gitmodules`, so a remote carrying user
// information would publish it. The link is refused, nothing is written, and
// the secret itself never reaches the run's output.
func TestChoreographyComputeRefusesARosterURLCarryingASecret(t *testing.T) {
	fleet := newChoreographyFleet(t, "api")
	api := fleet.peer("api")
	fleet.writeConfig("api", func(cfg *models.File) {
		cfg.Repositories = []models.RepositoryLinkConfig{{
			Name: "shop", URL: "https://deploy:hunter2@example.invalid/shop.git",
			Branch: harness.DefaultBranch}}
	})
	api.Commit("chore: name a repository through a url carrying a password")

	res := api.CommandEnv(fileProtocolEnv(), "compute", "--write")
	assert.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	combined := res.Stdout + res.Stderr
	assert.Contains(t, combined, "carries user information")
	assert.NotContains(t, combined, "hunter2", "the secret is never written out")
	assert.NoDirExists(t, api.Path(".links", "shop"))
	assert.NoFileExists(t, api.Path(".gitmodules"))
}

// TestChoreographyComputeRefusesALinkFolderOverAFile: the other half of a link
// is an empty folder and a staged pin, and a peer already holding a file where
// that folder belongs is reported by path rather than written around.
func TestChoreographyComputeRefusesALinkFolderOverAFile(t *testing.T) {
	fleet := unlinkedFleet(t, "api", "sdk")
	sdk := fleet.peer("sdk")
	sdk.WriteFile(".links/api", "a file where a fleet link belongs\n")
	sdk.Commit("chore: occupy the link folder with a file")
	fleet.push("sdk")
	api := fleet.peer("api")

	res := api.CommandEnv(fileProtocolEnv(), "compute", "--write")
	assert.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout+res.Stderr, "fleet link folder .links/api")
}

// TestChoreographyComputeStopsWhenTheRepairedFleetCannotBeRead: creating a
// link makes another peer's configuration readable for the first time, and a
// fleet that peer then describes in terms this run cannot resolve is a fleet
// no further work can be derived from. The run keeps what it made and stops.
func TestChoreographyComputeStopsWhenTheRepairedFleetCannotBeRead(t *testing.T) {
	fleet := unlinkedFleet(t, "api", "sdk")
	api := fleet.peer("api")
	// The peer states a boundary about a repository no roster names. Nothing
	// reads that statement until the link exists.
	fleet.writeConfig("sdk", func(cfg *models.File) {
		cfg.RepositoryBaselines = []models.RepositoryBaselineConfig{{
			Consumer: "api-pkg", ReleaseTag: "api-pkg@0.1.0", Repository: "ghost", Revision: "HEAD"}}
	})
	fleet.peer("sdk").Commit("chore: state a boundary about a repository nobody has")
	fleet.push("sdk")

	res := api.CommandEnv(fileProtocolEnv(), "compute", "--write")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "linked sdk from api")
	assert.DirExists(t, api.Path(".links", "sdk", "packages"))

	api.Commit("chore: link the fleet")
	commitLinked(api, ".links/sdk", "chore: link back")
	refused := api.Status("--package", "*")
	assert.Equal(t, 1, refused.Code, "what the peer states is what a release refuses")
	assert.Contains(t, refused.Stdout+refused.Stderr, "ghost")
}

// TestChoreographyComputeDoesNotRepeatAnInitItAlreadyMade: the repair loop
// reads the fleet again after every round, and a link whose checkout still
// cannot be composed is proposed again — the run applies each change once and
// stops, rather than spinning on the one it cannot fix.
func TestChoreographyComputeDoesNotRepeatAnInitItAlreadyMade(t *testing.T) {
	fleet := unlinkedFleet(t, "api", "sdk")
	sdk := fleet.peer("sdk")
	require.NoError(t, os.Remove(sdk.Path("dispat.json")))
	sdk.Commit("chore: a repository that states nothing about itself")
	fleet.push("sdk")
	api := fleet.peer("api")

	res := api.CommandEnv(fileProtocolEnv(), "compute", "--write")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, 1, strings.Count(res.Stdout, "linked sdk from api"))
	assert.Equal(t, 1, strings.Count(res.Stdout, "initialized .links/sdk in api"),
		"the same checkout is initialized once however often it is proposed")
	assert.DirExists(t, api.Path(".links", "sdk", "packages"))
}

// TestChoreographyComputeRefusesABackLinkURLCarryingASecret: the other half of
// a link records this repository's own remote, and that URL is written into a
// `.gitmodules` the peer commits. A remote carrying user information is
// refused there for the same reason a roster entry is, and the secret never
// reaches the run's output.
func TestChoreographyComputeRefusesABackLinkURLCarryingASecret(t *testing.T) {
	fleet := unlinkedFleet(t, "api", "sdk")
	api := fleet.peer("api")
	// Unreachable on purpose and locally so: what is under test is the refusal
	// to write the URL, and the branch tip cannot be asked for either way.
	api.Git("remote", "set-url", "origin", "https://deploy:hunter2@127.0.0.1:1/api.git")

	res := api.CommandEnv(fileProtocolEnv(), "compute", "--write")
	assert.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	combined := res.Stdout + res.Stderr
	assert.Contains(t, combined, "carries user information")
	assert.NotContains(t, combined, "hunter2", "the secret is never written out")
	assert.Empty(t, api.Git("-C", ".links/sdk", "ls-files", "-s", "--", ".links/api"),
		"nothing was staged in the peer")
}

// TestChoreographyComputeDeclaresTheHalfOfAOneSidedLink: a link one repository
// declares and its peer does not declare back is what composition reports as
// W332, and it is what compute repairs — by writing the missing declaration
// and nothing else. No clone, no second route, nothing committed.
func TestChoreographyComputeDeclaresTheHalfOfAOneSidedLink(t *testing.T) {
	fleet := newChoreographyFleet(t, "api", "sdk")
	fleet.linkOneWay("api", "sdk")
	api := fleet.peer("api")
	checkout := api.Git("-C", ".links/sdk", "rev-parse", "HEAD")
	requireDiagnostic(t, api.StatusOK("--package", "*"), "W332")

	check := api.CommandEnv(fileProtocolEnv(), "compute", "--check")
	assert.Equal(t, 1, check.Code, "a link declared at one end only is work pending")
	assert.Contains(t, check.Stdout, "+ link sdk api")
	assert.Contains(t, check.Stdout, "declares the other half of the link api already holds")

	res := api.CommandEnv(fileProtocolEnv(), "compute", "--write")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "declared api inside sdk at .links/api")
	assert.NotContains(t, res.Stdout, "linked sdk from api", "the checkout was already there")
	assert.Equal(t, checkout, api.Git("-C", ".links/sdk", "rev-parse", "HEAD"),
		"and it is the same checkout, not a second clone")
	assert.Equal(t, ".links/api",
		api.Git("-C", ".links/sdk", "config", "--file", ".gitmodules", "submodule.api.path"))
	staged := api.Git("-C", ".links/sdk", "diff", "--cached", "--name-only")
	assert.Contains(t, staged, ".gitmodules")
	assert.Contains(t, staged, ".links/api")

	settled := api.CommandEnv(fileProtocolEnv(), "compute", "--check")
	assert.Equal(t, 0, settled.Code, "stdout:\n%s", settled.Stdout)

	commitLinked(api, ".links/sdk", "chore: link back")
	composed := api.StatusOK("--package", "*")
	assert.ElementsMatch(t, []string{"api", "sdk"}, composedRepositories(composed))
	requireNoDiagnostic(t, composed, "W332")
	requireNoDiagnostic(t, composed, "E338")
}

// TestChoreographyComputeWithholdsAOneSidedHalfWithoutARemote: the missing
// half pins the repository that holds the link at a revision its own remote
// can serve, so a repository with no remote has the half withheld with the
// same warning a new link's back half gets.
func TestChoreographyComputeWithholdsAOneSidedHalfWithoutARemote(t *testing.T) {
	fleet := newChoreographyFleet(t, "api", "sdk")
	fleet.linkOneWay("api", "sdk")
	api := fleet.peer("api")
	api.Git("remote", "remove", "origin")

	res := api.CommandEnv(fileProtocolEnv(), "compute", "--write")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout+res.Stderr, "no remote to be fetched from")
	assert.NoDirExists(t, api.Path(".links", "sdk", ".links", "api"))
	requireDiagnostic(t, api.StatusOK("--package", "*"), "W332")
}
