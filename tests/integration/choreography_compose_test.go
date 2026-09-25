// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// What a choreographed fleet composes to, read through the binary: which
// repositories a run finds, which it refuses, and what it reports about the
// links it walked.

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// composedRepositories is the fleet one run reported composing.
func composedRepositories(res harness.RunResult) []string {
	for _, event := range res.Events {
		if event.Str("message") != "polyrepo workspace composed" {
			continue
		}
		raw, ok := event["repositories"].([]any)
		if !ok {
			return nil
		}
		names := make([]string, 0, len(raw))
		for _, name := range raw {
			names = append(names, name.(string))
		}
		return names
	}
	return nil
}

// plannedPackages is every package the plan reported a version for.
func plannedPackages(res harness.RunResult) []string {
	var out []string
	for _, event := range res.Events {
		if event.Package() != "" && event.Str("version") != "" {
			out = append(out, event.Package())
		}
	}
	return out
}

// TestChoreographyComposesOneFleetFromAnyEntry: the fleet is a property of
// the links, so a run started in either peer plans the same two packages and
// names the same two repositories.
func TestChoreographyComposesOneFleetFromAnyEntry(t *testing.T) {
	fleet := newChoreographyFleet(t, "api", "sdk")
	fleet.link("api", "sdk")

	for _, entry := range []string{"api", "sdk"} {
		t.Run(entry, func(t *testing.T) {
			run := fleet.enter(entry)
			res := run.StatusOK("--package", "*")
			assert.ElementsMatch(t, []string{"api", "sdk"}, composedRepositories(res),
				"stdout:\n%s", res.Stdout)
			assert.ElementsMatch(t, []string{"api-pkg", "sdk-pkg"}, plannedPackages(res))
			for _, event := range res.Events {
				if event.Str("message") == "polyrepo workspace composed" {
					assert.Empty(t, event.Str("saga"))
					assert.Equal(t, entry, event.Str("entry"))
				}
			}
			requireNoDiagnostic(t, res, "W332")
			requireNoDiagnostic(t, res, "W333")
		})
	}
}

// TestChoreographyRefusesASecondLinkPath: three repositories linked in a ring
// give two answers to which revisions lie between two of them, so the run
// stops with E338 instead of choosing one.
func TestChoreographyRefusesASecondLinkPath(t *testing.T) {
	fleet := newChoreographyFleet(t, "api", "sdk", "web")
	fleet.link("api", "sdk")
	fleet.link("api", "web")
	fleet.link("sdk", "web")
	// api's checkout of sdk predates sdk's link to web; following it is what
	// brings the second route into the fleet api reads.
	fleet.follow("api", "sdk")

	run := fleet.enter("api")
	res := run.Status("--package", "*")
	assert.Equal(t, 1, res.Code)
	requireDiagnostic(t, res, "E338")
}

// TestChoreographyRefusesAnIdentityItCannotTrust: a link is only a link when
// both ends agree who is at the other end of it.
func TestChoreographyRefusesAnIdentityItCannotTrust(t *testing.T) {
	for _, tc := range []struct {
		name   string
		code   string
		adjust func(*models.File)
	}{
		{"a peer that calls itself something else", "E339",
			func(cfg *models.File) { cfg.Repository = "shop" }},
		{"a peer with no repository identity", "E339", func(cfg *models.File) {
			cfg.Repository = ""
			cfg.Repositories = nil
		}},
		{"a roster without its own identity", "E339",
			func(cfg *models.File) { cfg.Repository = "" }},
		{"a roster whose identity is blank", "E339",
			func(cfg *models.File) { cfg.Repository = "  " }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fleet := newChoreographyFleet(t, "api", "sdk")
			fleet.link("api", "sdk")
			fleet.writeConfig("sdk", tc.adjust)
			fleet.peer("sdk").Commit("fix(sdk-pkg): change what this repository says it is")
			fleet.push("sdk")
			fleet.refresh("api", "sdk")

			res := fleet.peer("api").Status("--package", "*")
			assert.Equal(t, 1, res.Code)
			requireDiagnostic(t, res, tc.code)
			checked := fleet.peer("api").Command("compute", "--check")
			assert.Equal(t, 1, checked.Code, "%s\n%s", checked.Stdout, checked.Stderr)
			requireDiagnostic(t, checked, tc.code)
			assert.NotContains(t, checked.Stdout, "are in sync")
		})
	}
}

// TestChoreographyRefusesALinkNobodyMaterialized: a fleet is walked through
// its checkouts, and a link that was never initialized is refused with the
// command that repairs it.
func TestChoreographyRefusesALinkNobodyMaterialized(t *testing.T) {
	fleet := newChoreographyFleet(t, "api", "sdk")
	fleet.link("api", "sdk")

	// A clone with nothing initialized is exactly what a CI runner starts
	// with before it materializes the fleet.
	bare := harness.Clone(t, fleet.peer("api").remote)
	res := bare.Status("--package", "*")
	assert.Equal(t, 1, res.Code)
	requireDiagnostic(t, res, "E330")
	assert.Contains(t, res.Stdout+res.Stderr, "git submodule update --init")

	// Initialized, the same checkout composes the whole fleet.
	bare.Git("-c", "protocol.file.allow=always", "submodule", "update", "--init", "--", ".links/sdk")
	assert.ElementsMatch(t, []string{"api", "sdk"},
		composedRepositories(bare.StatusOK("--package", "*")))
}

// TestChoreographyIgnoresASubmoduleTheRosterDoesNotName: a repository may
// vendor anything it likes; only the roster makes a submodule a peer.
func TestChoreographyIgnoresASubmoduleTheRosterDoesNotName(t *testing.T) {
	fleet := newChoreographyFleet(t, "api", "sdk")
	fleet.link("api", "sdk")
	vendor := harness.New(t)
	vendor.WriteFile("README.md", "vendored\n")
	vendor.Commit("chore: vendored dependency")
	fleet.peer("api").Git("-c", "protocol.file.allow=always", "submodule", "add", "-q",
		"--name", "vendor", "--", vendor.Root, "third-party/vendor")
	fleet.peer("api").Commit("chore: vendor a dependency")
	fleet.push("api")

	res := fleet.peer("api").StatusOK("--package", "*")
	assert.ElementsMatch(t, []string{"api", "sdk"}, composedRepositories(res))
}

// TestChoreographyReportsAOneSidedLinkAndARosterGap: a fleet that still
// composes but whose declarations disagree is reported, never refused,
// because both are states `dispat compute` proposes a repair for.
func TestChoreographyReportsAOneSidedLinkAndARosterGap(t *testing.T) {
	fleet := newChoreographyFleet(t, "api", "sdk", "web")
	fleet.linkOneWay("api", "sdk")
	fleet.linkOneWay("api", "web")
	// sdk never heard of web, and links nothing back.
	fleet.writeConfig("sdk", func(cfg *models.File) {
		cfg.Repositories = []models.RepositoryLinkConfig{{
			Name: "api", URL: fleet.peer("api").remote, Branch: harness.DefaultBranch}}
	})
	fleet.peer("sdk").Commit("fix(sdk-pkg): narrow the roster")
	fleet.push("sdk")
	fleet.refresh("api", "sdk")

	res := fleet.peer("api").StatusOK("--package", "*")
	assert.ElementsMatch(t, []string{"api", "sdk", "web"}, composedRepositories(res))
	requireDiagnostic(t, res, "W332")
	requireDiagnostic(t, res, "W333")
}

// TestChoreographyRefusesTheKeysOnlyAControlRepositoryOwns: a fleet with no
// control repository cannot honour policy written for one, and refuses it
// rather than dropping it.
func TestChoreographyRefusesTheKeysOnlyAControlRepositoryOwns(t *testing.T) {
	for _, tc := range []struct {
		name   string
		adjust func(*models.File)
		code   string
	}{
		{"central imports", func(cfg *models.File) { cfg.Configs = []string{".links/sdk/dispat.json"} }, "E332"},
		{"central commit policy", func(cfg *models.File) {
			cfg.RepositoryOverrides = map[string]models.RepositoryOverrideConfig{
				"sdk": {Commit: &models.CommitConfig{Enabled: models.Bool(true)}}}
		}, "E332"},
		{"a baseline naming the control repository", func(cfg *models.File) {
			cfg.RepositoryBaselines = []models.RepositoryBaselineConfig{{
				Consumer: "api-pkg", ReleaseTag: "api-pkg@1.0.0", Repository: "control", Revision: "HEAD"}}
		}, "E332"},
		{"a roster with no identity", func(cfg *models.File) { cfg.Repository = "" }, "E339"},
		{"a reserved identity", func(cfg *models.File) { cfg.Repository = "control" }, "E339"},
		{"a roster naming this repository", func(cfg *models.File) {
			cfg.Repositories = append(cfg.Repositories, models.RepositoryLinkConfig{Name: "API"})
		}, "E339"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fleet := newChoreographyFleet(t, "api", "sdk")
			fleet.link("api", "sdk")
			fleet.writeConfig("api", tc.adjust)
			res := fleet.peer("api").Status("--package", "*")
			assert.Equal(t, 1, res.Code)
			requireDiagnostic(t, res, tc.code)
		})
	}
}

// TestChoreographyAbsentKeysKeepSingleRepositoryBehaviour: a configuration
// that names no saga is the configuration it has always been, submodules and
// all.
func TestChoreographyAbsentKeysKeepSingleRepositoryBehaviour(t *testing.T) {
	fleet := newChoreographyFleet(t, "api", "sdk")
	fleet.link("api", "sdk")
	fleet.writeConfig("api", func(cfg *models.File) {
		cfg.Repository = ""
		cfg.Repositories = nil
	})
	res := fleet.peer("api").StatusOK("--package", "*")
	assert.Nil(t, composedRepositories(res), "nothing was composed at all")
	assert.Equal(t, []string{"api-pkg"}, plannedPackages(res))
}

// TestChoreographyRefusesAmbiguousOrEscapingRoster keeps malformed peer
// identities and paths out of composition, before a plan can choose a
// repository or a package on the basis of that declaration.
func TestChoreographyRefusesAmbiguousOrEscapingRoster(t *testing.T) {
	for _, tc := range []struct {
		name   string
		adjust func(*models.File)
		want   string
	}{
		{"missing identity", func(c *models.File) { c.Repository = "" }, "requires `repository`"},
		{"invalid identity", func(c *models.File) { c.Repository = "api team" }, "not a repository identity"},
		{"invalid peer identity", func(c *models.File) { c.Repositories = []models.RepositoryLinkConfig{{Name: "sdk/team"}} }, "not a repository identity"},
		{"duplicate peer identity", func(c *models.File) { c.Repositories = []models.RepositoryLinkConfig{{Name: "sdk"}, {Name: "SDK"}} }, "repeats repositories[0]"},
		{"absolute peer path", func(c *models.File) { c.Repositories = []models.RepositoryLinkConfig{{Name: "sdk", Path: "/outside"}} }, "must be relative"},
		{"escaping peer path", func(c *models.File) {
			c.Repositories = []models.RepositoryLinkConfig{{Name: "sdk", Path: "../outside"}}
		}, "escapes the repository root"},
		{"root peer path", func(c *models.File) { c.Repositories = []models.RepositoryLinkConfig{{Name: "sdk", Path: "."}} }, "escapes the repository root"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fleet := newChoreographyFleet(t, "api", "sdk")
			fleet.writeConfig("api", tc.adjust)
			r := fleet.peer("api")
			head := r.Git("rev-parse", "HEAD")
			res := r.Status()
			require.Equal(t, 1, res.Code)
			requireDiagnostic(t, res, "E339")
			assert.Contains(t, diagnosticText(res), tc.want)
			assert.Empty(t, plannedPackages(res))
			assert.Equal(t, head, r.Git("rev-parse", "HEAD"))
		})
	}
}

// TestChoreographyReleasesOnePeerWithPolyrepoFalse: the standalone escape
// hatch, said out loud in the log so a reader knows the fleet was skipped.
func TestChoreographyReleasesOnePeerWithPolyrepoFalse(t *testing.T) {
	fleet := newChoreographyFleet(t, "api", "sdk")
	fleet.link("api", "sdk")
	run := fleet.enter("api")
	res := run.StatusOK("--package", "*", "--polyrepo=false")
	assert.Nil(t, composedRepositories(res))
	assert.Equal(t, []string{"api-pkg"}, plannedPackages(res))
	assert.Contains(t, res.Stdout, "releasing this repository alone")
}

// TestChoreographyExcludesADisabledPeer: participation is the entry's
// question, and an excluded peer takes no part in anything.
func TestChoreographyExcludesADisabledPeer(t *testing.T) {
	fleet := newChoreographyFleet(t, "api", "sdk")
	fleet.link("api", "sdk")
	fleet.writeConfig("api", func(cfg *models.File) {
		cfg.RepositoryOverrides = map[string]models.RepositoryOverrideConfig{
			"sdk": {Enabled: models.Bool(false)}}
	})
	res := fleet.peer("api").StatusOK("--package", "*")
	assert.Equal(t, []string{"api"}, composedRepositories(res))
	assert.Equal(t, []string{"api-pkg"}, plannedPackages(res))

	fleet.writeConfig("api", func(cfg *models.File) {
		cfg.RepositoryOverrides = map[string]models.RepositoryOverrideConfig{
			"absent": {Enabled: models.Bool(false)}}
	})
	refused := fleet.peer("api").Status("--package", "*")
	assert.Equal(t, 1, refused.Code)
	requireDiagnostic(t, refused, "E332")
}

// TestLinkedFleetRejectsTheRemovedSagaFlag: fleet membership comes from
// the configuration and links, with no protocol override on the command line.
func TestLinkedFleetRejectsTheRemovedSagaFlag(t *testing.T) {
	fleet := newChoreographyFleet(t, "api", "sdk")
	fleet.link("api", "sdk")
	res := fleet.peer("api").StatusOK("--package", "*")
	assert.ElementsMatch(t, []string{"api", "sdk"}, composedRepositories(res))
	refused := fleet.peer("api").Status("--saga", "choreography")
	assert.NotEqual(t, 0, refused.Code)
	assert.Contains(t, strings.ToLower(refused.Stdout+refused.Stderr), "unknown flag")
}

// TestChoreographyRefusesAFleetInventoryItCannotRead: the link inventory is
// read from `.gitmodules`, and a file Git cannot parse is reported as the
// unreadable repository it makes rather than as a fleet with no links.
func TestChoreographyRefusesAFleetInventoryItCannotRead(t *testing.T) {
	fleet := newChoreographyFleet(t, "api", "sdk")
	fleet.link("api", "sdk")
	api := fleet.peer("api")
	api.WriteFile(".gitmodules", "this is not a configuration file\n")

	res := api.Status("--package", "*")
	assert.Equal(t, 1, res.Code, "stdout:\n%s", res.Stdout)
	requireDiagnostic(t, res, "E330")
	assert.Contains(t, res.Stdout+res.Stderr, ".gitmodules")
}

// TestChoreographyRefusesALinkPathThatIsNotARepository: a link path holding
// something other than a checkout of its peer is the same refusal as a link
// nobody materialized, because Git answers questions asked there from the
// repository around it.
func TestChoreographyRefusesALinkPathThatIsNotARepository(t *testing.T) {
	fleet := newChoreographyFleet(t, "api", "sdk")
	fleet.link("api", "sdk")
	api := fleet.peer("api")
	api.Git("submodule", "deinit", "-f", "--", ".links/sdk")
	api.WriteFile(".links/sdk/not-a-repository.txt", "just a folder\n")

	res := api.Status("--package", "*")
	assert.Equal(t, 1, res.Code, "stdout:\n%s", res.Stdout)
	requireDiagnostic(t, res, "E330")
	assert.Contains(t, res.Stdout+res.Stderr, "git submodule update --init")
}

// TestChoreographyNamesTheEntryRootForWhatItIs: the composed line's anchor
// field is `control` only where a control repository exists; a fleet that has
// none calls it what it is.
func TestChoreographyNamesTheEntryRootForWhatItIs(t *testing.T) {
	fleet := newChoreographyFleet(t, "api", "sdk")
	fleet.link("api", "sdk")
	run := fleet.enter("api")

	res := run.StatusOK("--package", "*")
	var composed harness.Event
	for _, event := range res.Events {
		if event.Str("message") == "polyrepo workspace composed" {
			composed = event
		}
	}
	require.NotNil(t, composed)
	assert.NotEmpty(t, composed.Str("root"), "the anchor is the entry repository's root")
	assert.NotContains(t, composed, "control", "there is no control repository to name")
	assert.Equal(t, "api", composed.Str("entry"))
}

// TestChoreographyRefusesAShallowCheckout: a fleet's history is what every
// window is measured in, and a checkout that holds only its tip cannot answer
// for any of it — at either end of a link.
func TestChoreographyRefusesAShallowCheckout(t *testing.T) {
	t.Run("the repository the run started in", func(t *testing.T) {
		fleet := newChoreographyFleet(t, "api", "sdk")
		fleet.link("api", "sdk")
		shallow := harness.CloneShallow(t, fleet.peer("api").remote)
		shallow.Git("-c", "protocol.file.allow=always", "submodule", "update", "--init", "--", ".links/sdk")

		res := shallow.Status("--package", "*")
		assert.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		requireDiagnostic(t, res, "E330")
		assert.Contains(t, res.Stdout+res.Stderr, "shallow")
	})

	t.Run("a peer at the other end of a link", func(t *testing.T) {
		fleet := newChoreographyFleet(t, "api", "sdk")
		fleet.link("api", "sdk")
		clone := harness.Clone(t, fleet.peer("api").remote)
		// Over a plain local path Git ignores --depth, so the peer is fetched
		// over the file transport, where the depth is honoured.
		clone.Git("config", "submodule.sdk.url", "file://"+fleet.peer("sdk").remote)
		clone.Git("-c", "protocol.file.allow=always", "submodule", "update",
			"--init", "--depth", "1", "--", ".links/sdk")
		require.Equal(t, "true", clone.Git("-C", ".links/sdk", "rev-parse", "--is-shallow-repository"))

		res := clone.Status("--package", "*")
		assert.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		requireDiagnostic(t, res, "E330")
		assert.Contains(t, res.Stdout+res.Stderr, "shallow")
	})
}

// TestChoreographyRefusesARosterNameThatOnlyFoldsOntoItsLink: a fleet link
// carries the peer's identity exactly, so a roster entry that differs from the
// submodule only in case is a near-miss the walk names rather than a peer it
// silently fails to find.
func TestChoreographyRefusesARosterNameThatOnlyFoldsOntoItsLink(t *testing.T) {
	fleet := newChoreographyFleet(t, "api", "sdk")
	fleet.link("api", "sdk")
	fleet.writeConfig("api", func(cfg *models.File) {
		cfg.Repositories = []models.RepositoryLinkConfig{{
			Name: "SDK", URL: fleet.peer("sdk").remote, Branch: harness.DefaultBranch}}
	})
	api := fleet.peer("api")
	api.Commit("chore: spell the peer differently from its link")

	res := api.Status("--package", "*")
	assert.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	requireDiagnostic(t, res, "E339")
	assert.Contains(t, res.Stdout+res.Stderr, "carries the peer's identity exactly")
}

// TestChoreographyOverridesDecideWhoTakesPart: participation is the entry's
// question and the entry's alone — an override may enable a peer, and may not
// exclude the repository the run started in.
func TestChoreographyOverridesDecideWhoTakesPart(t *testing.T) {
	fleet := newChoreographyFleet(t, "api", "sdk", "web")
	fleet.link("api", "sdk")
	fleet.link("api", "web")
	fleet.follow("api", "sdk")
	api := fleet.peer("api")

	fleet.writeConfig("api", func(cfg *models.File) {
		cfg.RepositoryOverrides = map[string]models.RepositoryOverrideConfig{
			"sdk": {Enabled: models.Bool(true)}, "web": {Enabled: models.Bool(true)}}
	})
	enabled := api.StatusOK("--package", "*")
	assert.ElementsMatch(t, []string{"api", "sdk", "web"}, composedRepositories(enabled),
		"an override that enables a peer leaves the fleet as it was")

	fleet.writeConfig("api", func(cfg *models.File) {
		cfg.RepositoryOverrides = map[string]models.RepositoryOverrideConfig{
			"sdk": {Enabled: models.Bool(false)}, "web": {Enabled: models.Bool(false)}}
	})
	narrowed := api.StatusOK("--package", "*")
	assert.Equal(t, []string{"api"}, composedRepositories(narrowed))
	assert.Equal(t, []string{"api-pkg"}, plannedPackages(narrowed))

	fleet.writeConfig("api", func(cfg *models.File) {
		cfg.RepositoryOverrides = map[string]models.RepositoryOverrideConfig{
			"api": {Enabled: models.Bool(false)}}
	})
	refused := api.Status("--package", "*")
	assert.Equal(t, 1, refused.Code, "stdout:\n%s", refused.Stdout)
	requireDiagnostic(t, refused, "E332")
	assert.Contains(t, refused.Stdout+refused.Stderr, "excludes the repository the run started in")
}

// TestChoreographyReadsOneBoundaryTwoPeersBothState: a boundary is a statement
// about two repositories and either of them may write it down, so a fleet
// where both do states it once rather than twice over.
func TestChoreographyReadsOneBoundaryTwoPeersBothState(t *testing.T) {
	fleet := crossRepositoryFleet(t)
	api := fleet.peer("api")
	provider := api.Git("-C", ".links/sdk", "rev-parse", "HEAD")
	api.Git("tag", "-a", "api-pkg@0.1.0", "-m", "tagged by hand")
	fleet.workIn(api.Repo, "sdk", "sdk-pkg", "fix(sdk-pkg)^: work after the hand-made tag")

	baseline := models.RepositoryBaselineConfig{
		Consumer: "api-pkg", ReleaseTag: "api-pkg@0.1.0", Repository: "sdk", Revision: provider}
	fleet.configureIn(api.Repo, "sdk", "chore: state the boundary here too", func(cfg *models.File) {
		cfg.RepositoryBaselines = []models.RepositoryBaselineConfig{baseline}
	})
	fleet.writeConfig("api", func(cfg *models.File) {
		cfg.Dependencies = models.Dependencies{{Consumer: "api-pkg", Provider: "sdk-pkg"}}
		cfg.RepositoryBaselines = []models.RepositoryBaselineConfig{baseline}
	})
	api.Commit("chore: state the boundary in both repositories")

	res := api.StatusOK("--package", "*")
	requireNoDiagnostic(t, res, "E333")
	assert.Equal(t, "propagated from sdk-pkg", harness.GraphLine(res.Events, "api-pkg").Str("reason"),
		"the one declared revision is the boundary: the work after it reaches the consumer: %s", res.Stdout)
}

// TestChoreographyRefusesConflictingBoundariesTwoPeersState: two peers that
// write the same boundary down at different commits contradict each other, and
// a fleet has no control file to settle which one is right. The conflicting
// tuple is E333 (CCME §27.6) naming both peers and both revisions, and a run
// from either end refuses it the same way (§27.12 vector 2) rather than
// planning from whichever tuple its own file happened to state.
func TestChoreographyRefusesConflictingBoundariesTwoPeersState(t *testing.T) {
	fleet, before, work := handTaggedBoundaryFleet(t, true)
	api := fleet.peer("api")
	fleet.configureIn(api.Repo, "sdk", "chore: state the boundary at the work", func(cfg *models.File) {
		cfg.RepositoryBaselines = sdkBaseline(work)
	})
	fleet.writeConfig("api", func(cfg *models.File) {
		cfg.Dependencies = models.Dependencies{{Consumer: "api-pkg", Provider: "sdk-pkg"}}
		cfg.RepositoryBaselines = sdkBaseline(before)
	})
	api.Commit("chore: state the boundary before the work")
	fleet.push("api")
	// sdk catches up with its own remote, where the tuple was pushed from api's
	// checkout, and follows api, so a clone of sdk reads both statements too.
	fleet.peer("sdk").Git("pull", "-q", "--ff-only", "origin", harness.DefaultBranch)
	fleet.follow("sdk", "api")

	for name, entry := range map[string]*harness.Repo{"api": api.Repo, "a fresh sdk clone": fleet.enter("sdk")} {
		t.Run(name, func(t *testing.T) {
			res := entry.Status("--package", "*")
			assert.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			requireDiagnostic(t, res, "E333")
			out := res.Stdout + res.Stderr
			for _, named := range []string{`\"api\"`, `\"sdk\"`, before, work, "conflicting baselines"} {
				assert.Contains(t, out, named)
			}
		})
	}
}

// TestChoreographyRefusesALinkedPeerWithNoConfiguration: a peer is a
// repository that states what it releases, and a checkout with no dispat file
// of its own is refused by name rather than composed against its linker's.
func TestChoreographyRefusesALinkedPeerWithNoConfiguration(t *testing.T) {
	fleet := newChoreographyFleet(t, "api", "sdk")
	fleet.link("api", "sdk")
	api := fleet.peer("api")
	require.NoError(t, os.Remove(api.Path(".links", "sdk", "dispat.json")))

	res := api.Status("--package", "*")
	assert.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	requireDiagnostic(t, res, "E330")
	assert.Contains(t, res.Stdout+res.Stderr, "no dispat config file at")
}

// TestChoreographyRefusesALinkPathItCannotEnter: a link path is a folder of
// this repository, and one that leaves it or was never made is refused as the
// repository it does not hold.
func TestChoreographyRefusesALinkPathItCannotEnter(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
	}{
		{"a path that leaves the repository", "../outside"},
		{"a path nothing was ever made at", ".links/never-made"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fleet := newChoreographyFleet(t, "api", "sdk")
			api := fleet.peer("api")
			// Written by hand: `git submodule add` makes the folder, and what
			// is under test is a declaration whose folder is not there.
			api.WriteFile(".gitmodules", "[submodule \"sdk\"]\n\tpath = "+tc.path+
				"\n\turl = "+fleet.peer("sdk").remote+"\n")

			res := api.Status("--package", "*")
			assert.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			requireDiagnostic(t, res, "E330")
		})
	}
}

// TestChoreographyReadsAnInventoryThatNamesNoLinks: `git config` answers a
// file with no matching key by saying nothing and failing, which is a
// repository with no fleet links rather than a file that cannot be read; an
// entry naming nothing is skipped the same way.
func TestChoreographyReadsAnInventoryThatNamesNoLinks(t *testing.T) {
	for _, tc := range []struct {
		name     string
		contents string
	}{
		{"a file with no entries at all", "# nothing is linked here\n"},
		{"an entry with no identity", "[submodule \"\"]\n\tpath = .links/nobody\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fleet := newChoreographyFleet(t, "api", "sdk")
			api := fleet.peer("api")
			api.WriteFile(".gitmodules", tc.contents)

			res := api.StatusOK("--package", "*")
			assert.Equal(t, []string{"api"}, composedRepositories(res),
				"a repository with no links is a fleet of one")
			assert.Equal(t, []string{"api-pkg"}, plannedPackages(res))
		})
	}
}

// TestChoreographyRefusesAnInventoryItCannotLookAt: the link inventory is a
// file, and a path the filesystem will not answer about at all is neither a
// repository with no links nor one whose `.gitmodules` says nothing. It is
// refused by name, the way an unreadable file is.
func TestChoreographyRefusesAnInventoryItCannotLookAt(t *testing.T) {
	fleet := newChoreographyFleet(t, "api", "sdk")
	fleet.link("api", "sdk")
	api := fleet.peer("api")
	// A link to itself: the path exists as an entry and resolves to nothing,
	// which is the one shape that is neither "absent" nor "readable".
	require.NoError(t, os.Remove(api.Path(".gitmodules")))
	require.NoError(t, os.Symlink(".gitmodules", api.Path(".gitmodules")))

	res := api.Status("--package", "*")
	assert.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	requireDiagnostic(t, res, "E330")
	assert.Contains(t, res.Stdout+res.Stderr, ".gitmodules")
}
