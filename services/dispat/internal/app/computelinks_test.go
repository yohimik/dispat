// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/config"
)

// computeFleet is a choreographed workspace as compute sees it: whichever
// peers this run composed, and the rosters they declare.
func computeFleet(t *testing.T, repositories ...config.Repository) *App {
	t.Helper()
	root := "/w/api"
	if len(repositories) > 0 {
		root = repositories[0].Root
	}
	a := New(root, &config.File{}, zerolog.Nop())
	if len(repositories) > 0 {
		repositories[0].Entry = true
	}
	a.workspace = &config.Workspace{ControlRoot: root, Repositories: repositories}
	if len(repositories) > 0 {
		a.cfg = repositories[0].Config
	}
	return a
}

func fleetPeer(name string, peers []string, links map[string]string) config.Repository {
	cfg := &config.File{Repository: name}
	for _, peer := range peers {
		cfg.Repositories = append(cfg.Repositories, config.RepositoryLinkConfig{
			Name: peer, URL: "https://example.test/" + peer + ".git"})
	}
	return config.Repository{Name: name, Root: filepath.Join("/w", name), Config: cfg,
		Imported: true, Links: links}
}

func kinds(changes []linkSuggestion, kind string) []linkSuggestion {
	var out []linkSuggestion
	for _, change := range changes {
		if change.kind == kind {
			out = append(out, change)
		}
	}
	return out
}

func requireLinkSuggestions(t *testing.T, a *App, topology string) []linkSuggestion {
	t.Helper()
	changes, err := a.suggestLinks(topology)
	require.NoError(t, err)
	return changes
}

// TestSuggestLinksConnectsTheRosterWithASpanningTree: a fleet that names
// three peers and links none of them needs exactly two links, never three,
// because a third would be a second path between two repositories and
// composition refuses those.
func TestSuggestLinksConnectsTheRosterWithASpanningTree(t *testing.T) {
	a := computeFleet(t, fleetPeer("api", []string{"sdk", "web"}, nil))
	links := kinds(requireLinkSuggestions(t, a, ""), linkChangeLink)
	require.Len(t, links, 2, "three repositories need two links")
	assert.Equal(t, "api", links[0].repository)
	assert.Equal(t, "sdk", links[0].peer)
	assert.Equal(t, ".links/sdk", links[0].path)
	assert.Equal(t, "https://example.test/sdk.git", links[0].url)
	assert.Equal(t, "web", links[1].peer)

	// Seeded with a link that already exists, only the missing one is left:
	// the same answer whatever order the fleet was assembled in.
	a = computeFleet(t,
		fleetPeer("api", []string{"sdk", "web"}, map[string]string{"sdk": ".links/sdk"}),
		fleetPeer("sdk", []string{"api", "web"}, map[string]string{"api": ".links/api"}))
	links = kinds(requireLinkSuggestions(t, a, ""), linkChangeLink)
	require.Len(t, links, 1)
	assert.Equal(t, "web", links[0].peer)

	// A fully linked fleet proposes nothing at all.
	a = computeFleet(t,
		fleetPeer("api", []string{"sdk"}, map[string]string{"sdk": ".links/sdk"}),
		fleetPeer("sdk", []string{"api"}, map[string]string{"api": ".links/api"}))
	assert.Empty(t, kinds(requireLinkSuggestions(t, a, ""), linkChangeLink))
}

// TestSuggestLinksNeverProposesASecondPath: a link between two repositories
// that are already joined through a third would make the route ambiguous,
// which is exactly what E338 refuses.
func TestSuggestLinksNeverProposesASecondPath(t *testing.T) {
	a := computeFleet(t,
		fleetPeer("api", []string{"sdk", "web"}, map[string]string{"sdk": ".links/sdk"}),
		fleetPeer("sdk", []string{"api", "web"}, map[string]string{"api": ".links/api", "web": ".links/web"}),
		fleetPeer("web", []string{"api", "sdk"}, map[string]string{"sdk": ".links/sdk"}))
	assert.Empty(t, kinds(requireLinkSuggestions(t, a, ""), linkChangeLink),
		"every repository already reaches every other one")
}

func TestSuggestLinksRefusesAnExistingCycle(t *testing.T) {
	a := computeFleet(t,
		fleetPeer("api", []string{"sdk", "web"}, map[string]string{"sdk": ".links/sdk", "web": ".links/web"}),
		fleetPeer("sdk", []string{"api", "web"}, map[string]string{"api": ".links/api", "web": ".links/web"}),
		fleetPeer("web", []string{"api", "sdk"}, map[string]string{"api": ".links/api", "sdk": ".links/sdk"}))
	a.workspace.Findings = []config.LinkFinding{{
		Code: config.DiagnosticLinkGraph, Repository: "web", Peer: "sdk",
		Message: `E338: linked fleet: repository "sdk" is reached twice; fleet links must form one tree`,
	}}

	for _, topology := range []string{"minimal", "star"} {
		t.Run(topology, func(t *testing.T) {
			changes, err := a.suggestLinks(topology)
			assert.Nil(t, changes)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "existing fleet link topology is cyclic")
			assert.Contains(t, err.Error(), config.DiagnosticLinkGraph)
			assert.Contains(t, err.Error(), "compute never removes links")
		})
	}
}

func TestSuggestLinksRefusesAnInconsistentRepositoryIdentity(t *testing.T) {
	a := computeFleet(t, fleetPeer("api", []string{"sdk"}, map[string]string{"sdk": ".links/sdk"}))
	a.workspace.Findings = []config.LinkFinding{{
		Code: config.DiagnosticIdentity, Repository: "api", Peer: "sdk",
		Message: `E339: linked fleet: repository "sdk" calls itself "SDK-old"`,
	}}

	changes, err := a.suggestLinks("minimal")
	assert.Nil(t, changes)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "existing fleet repository identity is inconsistent")
	assert.Contains(t, err.Error(), config.DiagnosticIdentity)
	assert.Contains(t, err.Error(), "compute never rewrites repository identities")
}

func TestSuggestLinksStarUsesTheEntryAsHub(t *testing.T) {
	a := computeFleet(t, fleetPeer("zeta", []string{"alpha", "middle"}, nil))
	links := kinds(requireLinkSuggestions(t, a, "star"), linkChangeLink)
	require.Len(t, links, 2)
	assert.Equal(t, "zeta", links[0].repository)
	assert.Equal(t, "alpha", links[0].peer)
	assert.Equal(t, "zeta", links[1].repository)
	assert.Equal(t, "middle", links[1].peer)

	// The empty option is the existing minimal contract, and rerunning after
	// those edges exist is idempotent.
	minimal := kinds(requireLinkSuggestions(t, a, ""), linkChangeLink)
	require.Len(t, minimal, 2)
	a.workspace.Repositories[0].Links = map[string]string{
		"alpha": ".links/alpha", "middle": ".links/middle",
	}
	assert.Empty(t, kinds(requireLinkSuggestions(t, a, "star"), linkChangeLink))
}

func TestSuggestLinksStarRefusesAnExistingNonHubLink(t *testing.T) {
	a := computeFleet(t,
		fleetPeer("zeta", []string{"alpha", "middle"}, nil),
		fleetPeer("alpha", []string{"zeta", "middle"}, map[string]string{"middle": ".links/middle"}),
		fleetPeer("middle", []string{"zeta", "alpha"}, map[string]string{"alpha": ".links/alpha"}))

	changes, err := a.suggestLinks("star")
	assert.Nil(t, changes)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "incompatible with existing link alpha <-> middle")
	assert.Contains(t, err.Error(), "existing links are retained")
}

func TestSuggestLinksExcludesDisabledRosterPeers(t *testing.T) {
	root := t.TempDir()
	disabled := false
	cfg := &config.File{
		Repository: "api", Polyrepo: true,
		Packages: map[string]config.PackageConfig{"api": {Path: "pkg"}},
		Repositories: []config.RepositoryLinkConfig{{
			Name: "offline", URL: "https://unavailable.invalid/offline.git",
		}},
		RepositoryOverrides: map[string]config.RepositoryOverrideConfig{
			"offline": {Enabled: &disabled},
		},
		Commit: &config.CommitConfig{}, Run: &config.RunConfig{},
	}
	settleRepo(t, root, cfg)
	cfgPath := filepath.Join(root, "dispat.json")
	data, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(cfgPath, data, 0o644))
	settleGit(t, root, "add", "dispat.json")
	settleGit(t, root, "commit", "-qm", "chore: configure fleet")

	workspace, err := config.ComposeWorkspaceForRepair(t.Context(), cfg, cfgPath, root, nil, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, workspace)
	require.Equal(t, []string{"offline"}, workspace.DisabledRepositoryNames())
	a := New(root, cfg, zerolog.Nop())
	a.workspace = workspace

	for _, topology := range []string{"minimal", "star"} {
		t.Run(topology, func(t *testing.T) {
			changes := requireLinkSuggestions(t, a, topology)
			for _, change := range changes {
				assert.NotEqual(t, "offline", change.peer)
				assert.NotEqual(t, "offline", change.repository)
			}
			assert.Empty(t, kinds(changes, linkChangeLink))
			assert.Empty(t, kinds(changes, linkChangeInit))
			assert.Empty(t, kinds(changes, linkChangeRepository))
		})
	}
}

// Unavailable roster members must not be unioned before compute knows an
// edge can be created. Otherwise an unreachable early pair can make a later
// reachable edge look redundant and leave the fleet disconnected.
func TestSuggestLinksMinimalDoesNotUnionUnavailablePeers(t *testing.T) {
	a := computeFleet(t, fleetPeer("zeta", []string{"alpha", "middle"}, nil))
	links := kinds(requireLinkSuggestions(t, a, "minimal"), linkChangeLink)
	require.Len(t, links, 2)
	assert.ElementsMatch(t, []string{"alpha", "middle"}, []string{links[0].peer, links[1].peer})
	for _, link := range links {
		assert.Equal(t, "zeta", link.repository)
	}
}

// TestSuggestLinksProposesTheCheckoutAndTheRosterEntry: the other two things
// a fleet can be missing are a link nobody materialized and a peer that has
// not heard of a member.
func TestSuggestLinksProposesTheCheckoutAndTheRosterEntry(t *testing.T) {
	a := computeFleet(t,
		fleetPeer("api", []string{"sdk", "web"}, map[string]string{"sdk": ".links/sdk"}),
		fleetPeer("sdk", []string{"api"}, map[string]string{"api": ".links/api"}))
	a.workspace.Findings = []config.LinkFinding{{
		Code: config.DiagnosticRepositoryInvalid, Repository: "api", Peer: "sdk",
		Message: "E330: choreography: repository \"sdk\" is not initialized"}}

	changes := requireLinkSuggestions(t, a, "")
	inits := kinds(changes, linkChangeInit)
	require.Len(t, inits, 1)
	assert.Equal(t, "api", inits[0].repository)
	assert.Equal(t, ".links/sdk", inits[0].path)

	roster := kinds(changes, linkChangeRepository)
	require.Len(t, roster, 1, "sdk's roster does not name web")
	assert.Equal(t, "sdk", roster[0].repository)
	assert.Equal(t, "web", roster[0].peer)
	assert.Contains(t, roster[0].render(), "+ repository sdk web")

	// An orchestrated workspace proposes none of this.
	a.workspace.Repositories = append(a.workspace.Repositories, config.Repository{Name: config.ControlRepository, Control: true})
	assert.Nil(t, requireLinkSuggestions(t, a, ""))
}

// TestSuggestLinksWithholdsARosterEntryWithNoURL: an entry nothing can be
// fetched from would repair the roster only halfway, so it is reported
// instead of written.
func TestSuggestLinksWithholdsARosterEntryWithNoURL(t *testing.T) {
	api := fleetPeer("api", []string{"sdk"}, map[string]string{"sdk": ".links/sdk"})
	api.Config.Repositories = append(api.Config.Repositories, config.RepositoryLinkConfig{Name: "web"})
	sdk := fleetPeer("sdk", []string{"api"}, map[string]string{"api": ".links/api"})
	a := computeFleet(t, api, sdk)

	var logged bytes.Buffer
	a.log = zerolog.New(&logged)
	roster := kinds(requireLinkSuggestions(t, a, ""), linkChangeRepository)
	assert.Empty(t, roster, "web is known by name alone")
	assert.Contains(t, logged.String(), config.DiagnosticRosterDisagreement)
	assert.Contains(t, logged.String(), "no roster states a url")
	assert.Contains(t, logged.String(), "\"peer\":\"web\"")

	// With a url anywhere in the fleet the entry is proposed again.
	api.Config.Repositories[1].URL = "https://example.test/web.git"
	roster = kinds(requireLinkSuggestions(t, a, ""), linkChangeRepository)
	require.Len(t, roster, 1)
	assert.Equal(t, "sdk", roster[0].repository)
	assert.Equal(t, "https://example.test/web.git", roster[0].url)
}

// TestCollectLinkEditsWritesTheRosterIntoItsOwnerConfig: a peer's roster is
// the peer's own statement, so the entry lands in the peer's file.
func TestCollectLinkEditsWritesTheRosterIntoItsOwnerConfig(t *testing.T) {
	root := t.TempDir()
	apiPath := filepath.Join(root, "dispat.json")
	sdkPath := filepath.Join(root, "sdk.json")
	for path, cfg := range map[string]config.File{
		apiPath: {Repository: "api",
			Repositories: []config.RepositoryLinkConfig{{Name: "sdk"}},
			Packages:     map[string]config.PackageConfig{"app": {Path: "pkg"}}},
		sdkPath: {Repository: "sdk",
			Repositories: []config.RepositoryLinkConfig{{Name: "api"}},
			Packages:     map[string]config.PackageConfig{"lib": {Path: "pkg"}}},
	} {
		data, err := json.Marshal(cfg)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, data, 0o644))
	}
	api := fleetPeer("api", []string{"sdk", "web"}, nil)
	api.ConfigPath = apiPath
	sdk := fleetPeer("sdk", []string{"api"}, nil)
	sdk.ConfigPath = sdkPath
	a := computeFleet(t, api, sdk)

	var edits fileEdits
	require.NoError(t, a.collectLinkEdits(&edits, apiPath, []linkSuggestion{{
		kind: linkChangeRepository, repository: "sdk", peer: "web",
		url: "https://example.test/web.git"}}))
	require.Equal(t, []string{sdkPath}, edits.order, "the roster entry belongs to the peer that states it")
	require.Len(t, edits.byFile[sdkPath], 1)
	assert.Equal(t, []string{"repositories"}, edits.byFile[sdkPath][0].KeyPath)
	written, ok := edits.byFile[sdkPath][0].Value.([]config.RepositoryLinkConfig)
	require.True(t, ok)
	require.Len(t, written, 2)
	assert.Equal(t, "api", written[0].Name, "the roster stays in identity order")
	assert.Equal(t, "web", written[1].Name)
	assert.Equal(t, written, a.workspace.RepositoryByName("sdk").Config.Repositories,
		"the in-memory configuration matches what was written")

	// A change of another kind writes no configuration at all.
	var none fileEdits
	require.NoError(t, a.collectLinkEdits(&none, apiPath, []linkSuggestion{{
		kind: linkChangeLink, repository: "api", peer: "sdk"}}))
	assert.Empty(t, none.order)
}

// TestApplyLinkChangesCreatesBothHalvesOfALink: the forward half is a real
// checkout, and the other half is declared inside it without cloning
// anything, pinned at a revision the peer's remote can serve.
func TestApplyLinkChangesCreatesBothHalvesOfALink(t *testing.T) {
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "protocol.file.allow")
	t.Setenv("GIT_CONFIG_VALUE_0", "always")
	base := t.TempDir()
	api, sdk := filepath.Join(base, "api"), filepath.Join(base, "sdk")
	apiCfg := &config.File{Commit: &config.CommitConfig{}, Run: &config.RunConfig{}}
	sdkCfg := &config.File{Commit: &config.CommitConfig{}, Run: &config.RunConfig{}}
	settleRepo(t, api, apiCfg)
	settleRepo(t, sdk, sdkCfg)
	origin := filepath.Join(base, "api.git")
	settleGit(t, base, "init", "-q", "--bare", origin)
	settleGit(t, api, "remote", "add", "origin", origin)
	settleGit(t, api, "push", "-q", "origin", "HEAD:refs/heads/main")

	a := New(api, apiCfg, zerolog.Nop())
	a.workspace = &config.Workspace{ControlRoot: api,
		Repositories: []config.Repository{
			{Name: "api", Root: api, Config: apiCfg, Commit: apiCfg.Commit, Imported: true, Entry: true},
		}}
	var out bytes.Buffer
	require.NoError(t, a.applyLinkChanges(t.Context(), filepath.Join(api, "dispat.json"), []linkSuggestion{{
		kind: linkChangeLink, repository: "api", peer: "sdk", path: ".links/sdk", url: sdk, branch: "main",
	}}, "minimal", &out))

	assert.FileExists(t, filepath.Join(api, ".links", "sdk", "pkg", "input"), "the forward half is a checkout")
	assert.Contains(t, settleGit(t, api, "config", "--file", ".gitmodules", "submodule.sdk.url"), "sdk")
	nested := filepath.Join(api, ".links", "sdk")
	assert.Equal(t, ".links/api", settleGit(t, nested, "config", "--file", ".gitmodules", "submodule.api.path"),
		"the other half is declared inside the peer")
	assert.DirExists(t, filepath.Join(nested, ".links", "api"))
	staged := settleGit(t, nested, "diff", "--cached", "--name-only")
	assert.Contains(t, staged, ".gitmodules")
	assert.Contains(t, staged, ".links/api")
	assert.Equal(t, settleGit(t, api, "rev-parse", "HEAD"),
		settleGit(t, nested, "ls-files", "-s", "--", ".links/api")[7:47],
		"the back link is pinned at the revision the remote holds")
	assert.Contains(t, out.String(), "linked sdk from api")
	assert.Contains(t, out.String(), "declared api inside sdk")

	// Nothing was committed anywhere: the operator reads the diff and does
	// that themselves.
	assert.NotEmpty(t, settleGit(t, api, "status", "--porcelain=v1"))
	assert.Equal(t, 1, len(settleGit(t, nested, "log", "--format=%H"))/40,
		"the peer checkout carries only the revision it was cloned at")
}

func TestApplyLinkChangesReportsStarConflictRevealedByCheckout(t *testing.T) {
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "protocol.file.allow")
	t.Setenv("GIT_CONFIG_VALUE_0", "always")
	base := t.TempDir()
	api, sdk := filepath.Join(base, "api"), filepath.Join(base, "sdk")
	apiCfg := &config.File{Repository: "api", Polyrepo: true, Commit: &config.CommitConfig{}, Run: &config.RunConfig{},
		Packages:     map[string]config.PackageConfig{"api": {Path: "pkg"}},
		Repositories: []config.RepositoryLinkConfig{{Name: "sdk", URL: sdk, Branch: "main"}}}
	sdkCfg := &config.File{Repository: "sdk", Polyrepo: true, Commit: &config.CommitConfig{}, Run: &config.RunConfig{},
		Packages: map[string]config.PackageConfig{"sdk": {Path: "pkg"}},
		Repositories: []config.RepositoryLinkConfig{
			{Name: "api", URL: api, Branch: "main"},
			{Name: "web", URL: filepath.Join(base, "web"), Branch: "main"},
		}}
	settleRepo(t, api, apiCfg)
	settleRepo(t, sdk, sdkCfg)
	for root, cfg := range map[string]*config.File{api: apiCfg, sdk: sdkCfg} {
		data, err := json.Marshal(cfg)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(root, "dispat.json"), data, 0o644))
		settleGit(t, root, "add", "dispat.json")
		settleGit(t, root, "commit", "-qm", "chore: configure fleet")
	}

	// The sdk already links web, but api cannot know that until sdk is cloned.
	// A gitlink is enough evidence even when the linked checkout is absent.
	require.NoError(t, os.WriteFile(filepath.Join(sdk, ".gitmodules"), []byte(
		"[submodule \"web\"]\n\tpath = .links/web\n\turl = "+filepath.Join(base, "web")+"\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(sdk, ".links", "web"), 0o755))
	settleGit(t, sdk, "add", ".gitmodules")
	settleGit(t, sdk, "update-index", "--add", "--cacheinfo", "160000", settleGit(t, sdk, "rev-parse", "HEAD"), ".links/web")
	settleGit(t, sdk, "commit", "-qm", "chore: link web")

	origin := filepath.Join(base, "api.git")
	settleGit(t, base, "init", "-q", "--bare", origin)
	settleGit(t, api, "remote", "add", "origin", origin)
	settleGit(t, api, "push", "-q", "origin", "HEAD:refs/heads/main")

	var logged bytes.Buffer
	a := New(api, apiCfg, zerolog.New(&logged))
	a.workspace = &config.Workspace{ControlRoot: api, Repositories: []config.Repository{{
		Name: "api", Root: api, ConfigPath: filepath.Join(api, "dispat.json"), Config: apiCfg,
		Commit: apiCfg.Commit, Imported: true, Entry: true,
	}}}
	var out bytes.Buffer
	err := a.applyLinkChanges(t.Context(), filepath.Join(api, "dispat.json"), []linkSuggestion{{
		kind: linkChangeLink, repository: "api", peer: "sdk", path: ".links/sdk", url: sdk, branch: "main",
	}}, "star", &out)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "incompatible with star topology")
	assert.Contains(t, err.Error(), "existing link sdk <-> web")
	assert.Contains(t, err.Error(), "after 1 staged link operation(s)")
	assert.Contains(t, err.Error(), "review the staged changes before retrying")
	assert.Contains(t, logged.String(), "the fleet could not be verified after applying link changes")
	assert.Contains(t, logged.String(), "review the staged changes before retrying")
	assert.NotEmpty(t, settleGit(t, api, "status", "--porcelain=v1"), "the reported partial link stays staged for review")
}

// TestApplyLinkChangesLeavesALinkItCannotCreate: a roster entry with no url
// cannot be cloned, and a repository with no remote cannot be linked back to.
// Both are reported and neither is a failure.
func TestApplyLinkChangesLeavesALinkItCannotCreate(t *testing.T) {
	base := t.TempDir()
	api := filepath.Join(base, "api")
	apiCfg := &config.File{Commit: &config.CommitConfig{}, Run: &config.RunConfig{}}
	settleRepo(t, api, apiCfg)
	a := New(api, apiCfg, zerolog.Nop())
	a.workspace = &config.Workspace{ControlRoot: api,
		Repositories: []config.Repository{
			{Name: "api", Root: api, Config: apiCfg, Commit: apiCfg.Commit, Imported: true, Entry: true},
		}}
	var out bytes.Buffer
	require.NoError(t, a.applyLinkChanges(t.Context(), filepath.Join(api, "dispat.json"), []linkSuggestion{{
		kind: linkChangeLink, repository: "api", peer: "sdk", path: ".links/sdk"}}, "minimal", &out))
	assert.NoDirExists(t, filepath.Join(api, ".links", "sdk"))
	assert.Empty(t, out.String())

	// A repository this run did not compose is not one it can write into.
	require.NoError(t, a.applyLinkChanges(t.Context(), filepath.Join(api, "dispat.json"), []linkSuggestion{{
		kind: linkChangeLink, repository: "absent", peer: "sdk", path: ".links/sdk", url: "https://example.test/sdk.git"}}, "minimal", &out))
	assert.Empty(t, out.String())
}

// TestLinkSuggestionRendering: the listing is what --check prints and what
// --interactive asks about, one line per proposal.
func TestLinkSuggestionRendering(t *testing.T) {
	assert.Equal(t, "+ link api sdk  connects sdk to the fleet",
		linkSuggestion{kind: linkChangeLink, repository: "api", peer: "sdk", detail: "connects sdk to the fleet"}.render())
	assert.Equal(t, "+ init api .links/sdk  missing",
		linkSuggestion{kind: linkChangeInit, repository: "api", path: ".links/sdk", detail: "missing"}.render())
	assert.Equal(t, "+ repository sdk web  unknown",
		linkSuggestion{kind: linkChangeRepository, repository: "sdk", peer: "web", detail: "unknown"}.render())

	var set changeSet
	set.add(linkSuggestion{kind: linkChangeLink, repository: "api", peer: "sdk"})
	assert.Equal(t, 1, set.len())
	require.Len(t, set.all(), 1)
	assert.Equal(t, set.links[0].render(), set.all()[0].render())
}

func TestSuggestLinksKeepsRosterPathsLocalToTheirOwner(t *testing.T) {
	zeta := fleetPeer("zeta", []string{"alpha", "middle"}, nil)
	zeta.Config.Repositories[0].Path = "./entry//alpha"
	alpha := fleetPeer("alpha", []string{"zeta", "middle"}, nil)
	alpha.Config.Repositories[1].Path = "alpha-only/middle"
	a := computeFleet(t, zeta, alpha)

	star := kinds(requireLinkSuggestions(t, a, "star"), linkChangeLink)
	require.Len(t, star, 2)
	byPeer := map[string]linkSuggestion{star[0].peer: star[0], star[1].peer: star[1]}
	assert.Equal(t, "entry/alpha", byPeer["alpha"].path)
	assert.Equal(t, ".links/middle", byPeer["middle"].path,
		"the entry must not borrow alpha's repository-relative path")

	// With zeta and alpha already joined, minimal chooses alpha as the owner
	// of the missing edge and therefore uses alpha's own declaration.
	a.workspace.Repositories[0].Links = map[string]string{"alpha": "entry/alpha"}
	a.workspace.Repositories[1].Links = map[string]string{"zeta": ".links/zeta"}
	minimal := kinds(requireLinkSuggestions(t, a, "minimal"), linkChangeLink)
	require.Len(t, minimal, 1)
	assert.Equal(t, "alpha", minimal[0].repository)
	assert.Equal(t, "middle", minimal[0].peer)
	assert.Equal(t, "alpha-only/middle", minimal[0].path)

	// A newly copied membership entry carries shared fetch metadata but no
	// path from the peer that supplied it.
	zeta = fleetPeer("zeta", []string{"alpha"}, nil)
	a = computeFleet(t, zeta, alpha)
	roster := kinds(requireLinkSuggestions(t, a, "minimal"), linkChangeRepository)
	require.Len(t, roster, 1)
	assert.Equal(t, "zeta", roster[0].repository)
	assert.Equal(t, "middle", roster[0].peer)
	assert.Empty(t, roster[0].path)
}

// TestSuggestLinksProposesTheHalfOfAOneSidedLink: a link one repository
// declares and its peer does not declare back is the state composition reports
// as W332, and what repairs it is the missing declaration alone. The pair is
// already joined, so no second route is ever proposed with it.
func TestSuggestLinksProposesTheHalfOfAOneSidedLink(t *testing.T) {
	a := computeFleet(t,
		fleetPeer("api", []string{"sdk"}, map[string]string{"sdk": "vendor/sdk"}),
		fleetPeer("sdk", []string{"api"}, nil))

	links := kinds(requireLinkSuggestions(t, a, ""), linkChangeLink)
	require.Len(t, links, 1, "the pair is joined; only the declaration is missing")
	assert.Equal(t, "sdk", links[0].repository, "the change belongs to the repository that lacks it")
	assert.Equal(t, "api", links[0].peer)
	assert.Equal(t, "api", links[0].linker, "the far end already holds the checkout")
	assert.Equal(t, "vendor/sdk", links[0].path, "which is where the linker keeps it")
	assert.Empty(t, links[0].url, "nothing is fetched for a back half")
	assert.Equal(t, "+ link sdk api  declares the other half of the link api already holds", links[0].render())

	// Declared at both ends, there is nothing left to propose.
	a = computeFleet(t,
		fleetPeer("api", []string{"sdk"}, map[string]string{"sdk": "vendor/sdk"}),
		fleetPeer("sdk", []string{"api"}, map[string]string{"api": ".links/api"}))
	assert.Empty(t, kinds(requireLinkSuggestions(t, a, ""), linkChangeLink))

	// A peer this run never walked into has no checkout to write the
	// declaration in, so the half is not proposed for it.
	a = computeFleet(t, fleetPeer("api", []string{"sdk"}, map[string]string{"sdk": ".links/sdk"}))
	assert.Empty(t, kinds(requireLinkSuggestions(t, a, ""), linkChangeLink))
}

// TestSuggestLinksFoldsTheIdentityOfABackLink: the two ends of a link may
// spell each other's identity in different cases, and a half that exists under
// either spelling is not missing.
func TestSuggestLinksFoldsTheIdentityOfABackLink(t *testing.T) {
	a := computeFleet(t,
		fleetPeer("api", []string{"sdk"}, map[string]string{"sdk": ".links/sdk"}),
		fleetPeer("sdk", []string{"api"}, map[string]string{"API": ".links/api"}))
	assert.Empty(t, kinds(requireLinkSuggestions(t, a, ""), linkChangeLink))
}
