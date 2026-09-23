// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package config

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// choreoConfig is one peer's configuration: its own identity, the roster of
// the fleet, and a package of its own so discovery has something to own.
func choreoConfig(identity string, peers ...string) File {
	cfg := File{
		Repository: identity,
		Packages:   map[string]PackageConfig{identity + "-pkg": {Path: "pkgs/" + identity}},
	}
	for _, peer := range peers {
		cfg.Repositories = append(cfg.Repositories, RepositoryLinkConfig{Name: peer})
	}
	return cfg
}

// choreoRepo writes one peer as a real repository, because every question this
// file asks — is the link populated, what does the linked checkout call
// itself, which revision is pinned — is a question about Git.
func choreoRepo(t *testing.T, identity string, cfg File) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), identity)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "pkgs", identity), 0o755))
	workspaceGit(t, root, "init", "-b", "main")
	workspaceGit(t, root, "config", "user.name", "Test")
	workspaceGit(t, root, "config", "user.email", "test@example.com")
	require.NoError(t, os.WriteFile(filepath.Join(root, "pkgs", identity, "README.md"), []byte(identity), 0o644))
	choreoWriteConfig(t, root, cfg)
	workspaceGit(t, root, "add", ".")
	workspaceGit(t, root, "commit", "-m", "feat: initial")
	return root
}

func choreoWriteConfig(t *testing.T, root string, cfg File) {
	t.Helper()
	data, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "dispat.json"), data, 0o644))
}

// choreoLink creates the forward half of a fleet link: a real submodule, at
// the path the roster defaults to.
func choreoLink(t *testing.T, from, target, name string) {
	t.Helper()
	choreoLinkAt(t, from, target, name, DefaultLinkPath(name))
}

func choreoLinkAt(t *testing.T, from, target, name, path string) {
	t.Helper()
	workspaceGit(t, from, "-c", "protocol.file.allow=always", "submodule", "add", "--name", name, "--", target, path)
	workspaceGit(t, from, "commit", "-m", "chore: link "+name)
}

func choreoCompose(t *testing.T, root string, opts LinkedOptions) (*Workspace, *File, error) {
	t.Helper()
	path := filepath.Join(root, "dispat.json")
	cfg, err := Load(path, nil)
	require.NoError(t, err)
	workspace, err := ComposeLinked(t.Context(), cfg, path, root, opts)
	return workspace, cfg, err
}

func choreoNames(workspace *Workspace) []string {
	names := make([]string, 0, len(workspace.Repositories))
	for i := range workspace.Repositories {
		names = append(names, workspace.Repositories[i].Name)
	}
	return names
}

// choreoPair builds the two-sided pair every fleet is made of: each peer holds
// its own checkout of the other, and the copy inside each of those checkouts
// stays unpopulated, which is the state the identity cut exists for.
func choreoPair(t *testing.T) (string, string) {
	t.Helper()
	api := choreoRepo(t, "api", choreoConfig("api", "sdk"))
	sdk := choreoRepo(t, "sdk", choreoConfig("sdk", "api"))
	choreoLink(t, api, sdk, "sdk")
	choreoLink(t, sdk, api, "api")
	// api cloned sdk before sdk declared the link back, which is what a fleet
	// linked one repository at a time looks like. Following the peer's branch
	// is how the checkout catches up, and it leaves the advisory pin drift
	// every settled fleet carries between releases.
	choreoFollowRemote(t, api, DefaultLinkPath("sdk"))
	return api, sdk
}

// choreoFollowRemote moves one link checkout to the branch tip of the
// repository it points at, without recursing into the back-link it carries.
func choreoFollowRemote(t *testing.T, root, path string) {
	t.Helper()
	workspaceGit(t, root, "-c", "protocol.file.allow=always", "submodule", "update", "--remote", "--", path)
}

// TestChoreographyComposesOneFleetFromEitherEntry: the fleet is a property of
// the links, not of where the command was run, so both peers compose the same
// two repositories — each one as the entry, each one owning its own packages.
func TestChoreographyComposesOneFleetFromEitherEntry(t *testing.T) {
	api, sdk := choreoPair(t)
	for _, tc := range []struct{ entry, root, peer string }{
		{"api", api, "sdk"},
		{"sdk", sdk, "api"},
	} {
		t.Run(tc.entry, func(t *testing.T) {
			workspace, cfg, err := choreoCompose(t, tc.root, LinkedOptions{})
			require.NoError(t, err)
			require.NotNil(t, workspace)
			assert.True(t, workspace.IsLinked())
			assert.ElementsMatch(t, []string{"api", "sdk"}, choreoNames(workspace))

			entry := workspace.EntryRepository()
			require.NotNil(t, entry)
			assert.Equal(t, tc.entry, entry.Name)
			assert.Equal(t, entry.Name, workspace.Repositories[0].Name, "the entry is reported first")
			assert.Empty(t, entry.Linker)
			assert.Equal(t, []string{tc.peer}, entry.LinkPeers())
			assert.Equal(t, DefaultLinkPath(tc.peer), entry.Links[tc.peer])

			peer := workspace.RepositoryByName(tc.peer)
			require.NotNil(t, peer)
			assert.Equal(t, tc.entry, peer.Linker, "the peer records the link it was reached through")
			assert.Equal(t, DefaultLinkPath(tc.peer), peer.GitlinkPath)
			assert.True(t, peer.Imported, "every peer owns its own configuration")
			assert.False(t, peer.Control)
			assert.NotEmpty(t, peer.CompositionHead)
			assert.Equal(t, []string{tc.entry, tc.peer}, workspace.LinkRoute(tc.entry, tc.peer))
			assert.Equal(t, []string{tc.peer}, workspace.LinkRoute(tc.peer, tc.peer))
			assert.Nil(t, workspace.LinkRoute(tc.entry, "absent"))
			assert.Empty(t, workspace.LinkFindings(), "a two-sided pair has nothing to report")

			pkgs, _, _, err := DiscoverWorkspace(cfg, tc.root, workspace)
			require.NoError(t, err)
			owners := map[string]string{}
			for _, p := range pkgs {
				owners[p.Name] = p.Repository
			}
			assert.Equal(t, map[string]string{"api-pkg": "api", "sdk-pkg": "sdk"}, owners)
		})
	}
}

// TestChoreographyRefusesASecondPathToARepository: three repositories linked
// in a ring give two answers to "which hops lie between api and web", so the
// walk refuses the ring rather than picking one.
func TestChoreographyRefusesASecondPathToARepository(t *testing.T) {
	api := choreoRepo(t, "api", choreoConfig("api", "sdk", "web"))
	sdk := choreoRepo(t, "sdk", choreoConfig("sdk", "api", "web"))
	web := choreoRepo(t, "web", choreoConfig("web", "api", "sdk"))
	// sdk links web first, so api's checkout of sdk carries that link: two
	// routes from api to web, one direct and one through sdk.
	choreoLink(t, sdk, web, "web")
	choreoLink(t, api, sdk, "sdk")
	choreoLink(t, api, web, "web")

	_, _, err := choreoCompose(t, api, LinkedOptions{})
	require.Error(t, err)
	requireWorkspaceDiagnostic(t, err, DiagnosticLinkGraph)
	assert.ErrorContains(t, err, "must form one tree")

	workspace, _, err := choreoCompose(t, api, LinkedOptions{Lenient: true})
	require.NoError(t, err, "compute repairs fleets and must be able to read a broken one")
	require.NotEmpty(t, workspace.LinkFindings())
	assert.Equal(t, DiagnosticLinkGraph, workspace.LinkFindings()[0].Code)
}

// TestChoreographyRefusesIdentityProblems: a link is only a link when both
// ends agree on who is at the other end of it.
func TestChoreographyRefusesIdentityProblems(t *testing.T) {
	for _, tc := range []struct {
		name    string
		peer    File
		linkAs  string
		message string
	}{
		{
			name:    "a peer that calls itself something else",
			peer:    choreoConfig("shop", "api"),
			linkAs:  "sdk",
			message: "calls itself",
		},
		{
			name:    "a peer with no repository identity",
			peer:    File{Packages: map[string]PackageConfig{"sdk-pkg": {Path: "pkgs/sdk"}}},
			linkAs:  "sdk",
			message: "must declare its own repository identity",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := choreoRepo(t, "api", choreoConfig("api", "sdk"))
			peer := choreoRepo(t, "sdk", tc.peer)
			choreoLink(t, api, peer, tc.linkAs)
			_, _, err := choreoCompose(t, api, LinkedOptions{})
			require.Error(t, err)
			requireWorkspaceDiagnostic(t, err, DiagnosticIdentity)
			assert.ErrorContains(t, err, tc.message)
		})
	}

	t.Run("a link whose name only folds to the roster entry", func(t *testing.T) {
		api := choreoRepo(t, "api", choreoConfig("api", "sdk"))
		sdk := choreoRepo(t, "sdk", choreoConfig("sdk", "api"))
		choreoLinkAt(t, api, sdk, "SDK", DefaultLinkPath("SDK"))
		_, _, err := choreoCompose(t, api, LinkedOptions{})
		require.Error(t, err)
		requireWorkspaceDiagnostic(t, err, DiagnosticIdentity)
		assert.ErrorContains(t, err, "carries the peer's identity exactly")
	})
}

// TestChoreographyRefusesAnUninitializedPeer: the copy of a peer that sits
// inside another peer's checkout is deliberately never populated, so a run
// started there cannot compose the fleet and is told where it can.
func TestChoreographyRefusesAnUninitializedPeer(t *testing.T) {
	api, _ := choreoPair(t)
	nested := filepath.Join(api, DefaultLinkPath("sdk"))
	require.DirExists(t, filepath.Join(nested, DefaultLinkPath("api")), "the back-link exists as an empty folder")

	_, _, err := choreoCompose(t, nested, LinkedOptions{})
	require.Error(t, err)
	requireWorkspaceDiagnostic(t, err, DiagnosticRepositoryInvalid)
	assert.ErrorContains(t, err, "is not initialized")
	assert.ErrorContains(t, err, "git submodule update --init")

	// The repository that owns the link still composes the whole fleet, and
	// the advisory drift the update left behind is not a composition problem.
	workspace, _, err := choreoCompose(t, api, LinkedOptions{})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"api", "sdk"}, choreoNames(workspace))

	workspace, _, err = choreoCompose(t, nested, LinkedOptions{Lenient: true})
	require.NoError(t, err)
	assert.Equal(t, []string{"sdk"}, choreoNames(workspace))
	require.Len(t, workspace.LinkFindings(), 1)
	assert.Equal(t, DiagnosticRepositoryInvalid, workspace.LinkFindings()[0].Code)
}

// TestChoreographyIgnoresSubmodulesTheRosterDoesNotName: a repository may
// vendor anything it likes as a submodule; only the roster makes one a peer.
func TestChoreographyIgnoresSubmodulesTheRosterDoesNotName(t *testing.T) {
	api := choreoRepo(t, "api", choreoConfig("api", "sdk"))
	sdk := choreoRepo(t, "sdk", choreoConfig("sdk", "api"))
	vendor := choreoRepo(t, "vendor", File{Packages: map[string]PackageConfig{"vendor-pkg": {Path: "pkgs/vendor"}}})
	choreoLink(t, api, sdk, "sdk")
	choreoLink(t, sdk, api, "api")
	choreoFollowRemote(t, api, DefaultLinkPath("sdk"))
	choreoLinkAt(t, api, vendor, "vendor", "third-party/vendor")

	workspace, _, err := choreoCompose(t, api, LinkedOptions{})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"api", "sdk"}, choreoNames(workspace))
	assert.Nil(t, workspace.RepositoryByName("vendor"))
	assert.Empty(t, workspace.LinkFindings())
}

// TestChoreographyReportsLinkAndRosterFindings: a fleet that still composes
// but whose declarations disagree is reported rather than refused, because
// both states are ones `dispat compute` proposes a repair for.
func TestChoreographyReportsLinkAndRosterFindings(t *testing.T) {
	api := choreoRepo(t, "api", choreoConfig("api", "sdk", "web"))
	sdk := choreoRepo(t, "sdk", choreoConfig("sdk", "api"))
	web := choreoRepo(t, "web", choreoConfig("web", "api", "sdk"))
	choreoLink(t, api, sdk, "sdk")
	choreoLink(t, api, web, "web")

	workspace, _, err := choreoCompose(t, api, LinkedOptions{})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"api", "sdk", "web"}, choreoNames(workspace))
	byCode := map[string][]LinkFinding{}
	for _, finding := range workspace.LinkFindings() {
		byCode[finding.Code] = append(byCode[finding.Code], finding)
	}
	require.Len(t, byCode[DiagnosticLinkOneSided], 2, "neither peer links api back")
	assert.Equal(t, "api", byCode[DiagnosticLinkOneSided][0].Repository)
	assert.Contains(t, byCode[DiagnosticLinkOneSided][0].Message, "does not link it back")
	require.Len(t, byCode[DiagnosticRosterDisagreement], 1, "sdk has not heard of web")
	assert.Equal(t, "sdk", byCode[DiagnosticRosterDisagreement][0].Repository)
	assert.Equal(t, "web", byCode[DiagnosticRosterDisagreement][0].Peer)

	// The route across the fleet is still readable: the links form a tree.
	assert.Equal(t, []string{"sdk", "api", "web"}, workspace.LinkRoute("sdk", "web"))
}

// TestChoreographyExcludesADisabledPeer: participation is the entry's
// question, and an exclusion keeps the repository out of the walk entirely.
func TestChoreographyExcludesADisabledPeer(t *testing.T) {
	cfg := choreoConfig("api", "sdk")
	cfg.RepositoryOverrides = map[string]RepositoryOverrideConfig{"sdk": {Enabled: models.Bool(false)}}
	api := choreoRepo(t, "api", cfg)
	choreoLink(t, api, choreoRepo(t, "sdk", choreoConfig("sdk", "api")), "sdk")

	workspace, _, err := choreoCompose(t, api, LinkedOptions{})
	require.NoError(t, err)
	assert.Equal(t, []string{"api"}, choreoNames(workspace))
	assert.Equal(t, []string{"sdk"}, workspace.DisabledRepositoryNames())
	assert.Empty(t, workspace.RepositoryByName("api").Links, "an excluded peer is not a link")

	unknown := choreoConfig("api", "sdk")
	unknown.RepositoryOverrides = map[string]RepositoryOverrideConfig{"absent": {Enabled: models.Bool(false)}}
	choreoWriteConfig(t, api, unknown)
	_, _, err = choreoCompose(t, api, LinkedOptions{})
	requireWorkspaceDiagnostic(t, err, DiagnosticComposition)
	assert.ErrorContains(t, err, "no roster entry declares")

	self := choreoConfig("api", "sdk")
	self.RepositoryOverrides = map[string]RepositoryOverrideConfig{"api": {Enabled: models.Bool(false)}}
	choreoWriteConfig(t, api, self)
	_, _, err = choreoCompose(t, api, LinkedOptions{})
	requireWorkspaceDiagnostic(t, err, DiagnosticComposition)
	assert.ErrorContains(t, err, "the run started in")
}

// TestChoreographyMergesBaselinesFromEveryPeer: with no control file to write
// them in, an explicit boundary is declared by the repository that knows it,
// and the run has to read all of them.
func TestChoreographyMergesBaselinesFromEveryPeer(t *testing.T) {
	api := choreoRepo(t, "api", choreoConfig("api", "sdk"))
	sdkCfg := choreoConfig("sdk", "api")
	sdk := choreoRepo(t, "sdk", sdkCfg)
	revision := workspaceGit(t, sdk, "rev-parse", "HEAD")
	sdkCfg.RepositoryBaselines = []RepositoryBaselineConfig{{
		Consumer: "api-pkg", ReleaseTag: "api-pkg@1.0.0", Repository: "sdk", Revision: revision[:len(revision)-1],
	}}
	choreoWriteConfig(t, sdk, sdkCfg)
	workspaceGit(t, sdk, "commit", "-am", "fix: declare a boundary")
	choreoLink(t, api, sdk, "sdk")
	choreoLink(t, sdk, api, "api")
	choreoFollowRemote(t, api, DefaultLinkPath("sdk"))

	_, cfg, err := choreoCompose(t, api, LinkedOptions{})
	require.NoError(t, err)
	require.Len(t, cfg.RepositoryBaselines, 1)
	assert.Equal(t, "sdk", cfg.RepositoryBaselines[0].Repository)
	assert.Len(t, cfg.RepositoryBaselines[0].Revision, 40, "a merged baseline is resolved to a full commit")

	// The same boundary written on both sides is one boundary, and a peer that
	// declares nothing adds nothing.
	both := choreoConfig("api", "sdk")
	both.RepositoryBaselines = sdkCfg.RepositoryBaselines
	choreoWriteConfig(t, api, both)
	_, cfg, err = choreoCompose(t, api, LinkedOptions{})
	require.NoError(t, err)
	assert.Len(t, cfg.RepositoryBaselines, 1, "the same boundary from two peers is one baseline")
}

// TestValidateLinkedRefusesContradictoryConfigurations: every way of writing a
// fleet that could not be released as written, refused where it is written.
func TestValidateLinkedRefusesContradictoryConfigurations(t *testing.T) {
	base := func() *File {
		cfg := choreoConfig("api", "sdk")
		return &cfg
	}
	for _, tc := range []struct {
		name    string
		mutate  func(*File)
		code    string
		message string
	}{
		{"a roster with no identity", func(c *File) { c.Repository = "" }, DiagnosticIdentity, "requires `repository`"},
		{"an identity Git cannot name", func(c *File) { c.Repository = "api/two" }, DiagnosticIdentity, "is not a repository identity"},
		{"the reserved control identity", func(c *File) { c.Repository = "Control" }, DiagnosticIdentity, "is reserved"},
		{"a roster naming this repository", func(c *File) {
			c.Repositories = []RepositoryLinkConfig{{Name: "API"}}
		}, DiagnosticIdentity, "is this repository"},
		{"a roster naming a peer twice", func(c *File) {
			c.Repositories = []RepositoryLinkConfig{{Name: "sdk"}, {Name: "SDK"}}
		}, DiagnosticIdentity, "repeats repositories[0]"},
		{"a link path outside the repository", func(c *File) {
			c.Repositories = []RepositoryLinkConfig{{Name: "sdk", Path: "../sdk"}}
		}, DiagnosticIdentity, "escapes the repository root"},
		{"an absolute link path", func(c *File) {
			c.Repositories = []RepositoryLinkConfig{{Name: "sdk", Path: "/srv/sdk"}}
		}, DiagnosticIdentity, "must be relative"},
		{"central imports", func(c *File) { c.Configs = []string{"../sdk/dispat.json"} },
			DiagnosticComposition, "cannot import `configs`"},
		{"central commit policy", func(c *File) {
			c.RepositoryOverrides = map[string]RepositoryOverrideConfig{"sdk": {Commit: &CommitConfig{Enabled: models.Bool(true)}}}
		}, DiagnosticComposition, "belongs to repository \"sdk\"'s own linked configuration"},
		{"a baseline naming the control repository", func(c *File) {
			c.RepositoryBaselines = []RepositoryBaselineConfig{{
				Consumer: "api-pkg", ReleaseTag: "api-pkg@1.0.0", Repository: "control", Revision: "HEAD"}}
		}, DiagnosticComposition, "central control identity"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base()
			tc.mutate(cfg)
			err := validate(cfg, false)
			require.Error(t, err)
			assert.ErrorContains(t, err, tc.message)
			assert.Equal(t, tc.code, DiagnosticCode(err))
		})
	}

	t.Run("an accepted fleet", func(t *testing.T) {
		cfg := base()
		require.NoError(t, validate(cfg, false))
		assert.True(t, cfg.IsLinked())
		assert.True(t, cfg.Polyrepo, "a linked fleet is a polyrepository fleet")
	})

	t.Run("a central file is untouched", func(t *testing.T) {
		cfg := &File{Packages: map[string]PackageConfig{"api": {Path: "pkgs/api"}}}
		require.NoError(t, validate(cfg, false))
		assert.False(t, cfg.Polyrepo)
		assert.False(t, cfg.IsLinked())
	})
}

func TestSagaKeyIsRejected(t *testing.T) {
	root := writeRawRepo(t, map[string]any{
		"saga":       "choreography",
		"repository": "api",
		"packages":   map[string]any{"api-pkg": map[string]any{"path": "pkgs/api"}},
	}, "pkgs/api")
	_, err := Load(filepath.Join(root, "dispat.json"), nil)
	require.Error(t, err)
	assert.ErrorContains(t, err, `unknown key "saga"`)
}

// TestComposeWorkspaceDelegatesOnlyToAChoreographedFleet: the delegation is
// the one change the central path sees, so it is asserted from outside.
func TestComposeWorkspaceDelegatesOnlyToAChoreographedFleet(t *testing.T) {
	api, _ := choreoPair(t)
	path := filepath.Join(api, "dispat.json")
	cfg, err := Load(path, nil)
	require.NoError(t, err)

	workspace, err := ComposeWorkspaceWithPinResolver(context.Background(), cfg, path, api, nil, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, workspace)
	assert.True(t, workspace.IsLinked())
	canonical, err := filepath.EvalSymlinks(api)
	require.NoError(t, err)
	assert.Equal(t, canonical, workspace.ControlRoot, "the entry root is the workspace anchor")

	// `--polyrepo=false` is the standalone escape hatch: the flag is applied
	// after loading, and composition then finds nothing to compose.
	cfg.Polyrepo = false
	workspace, err = ComposeWorkspaceWithPinResolver(context.Background(), cfg, path, api, nil, nil, nil)
	require.NoError(t, err)
	assert.Nil(t, workspace)

	cfg.Polyrepo = true
	_, err = ComposeWorkspaceWithPinResolver(context.Background(), cfg, path, api, []string{"../sdk/dispat.json"}, nil, nil)
	requireWorkspaceDiagnostic(t, err, DiagnosticComposition)
	assert.ErrorContains(t, err, "--configs")

	central := &File{Packages: map[string]PackageConfig{"api": {Path: "pkgs/api"}}}
	workspace, err = ComposeWorkspaceWithPinResolver(context.Background(), central, path, api, nil, nil, nil)
	require.NoError(t, err)
	assert.Nil(t, workspace, "a file naming no identity and no imports composes nothing")
	assert.False(t, workspace.IsLinked())
	assert.Nil(t, workspace.EntryRepository())
	assert.Nil(t, workspace.LinkFindings())
	assert.Nil(t, workspace.LinkRoute("api", "sdk"))
}

// TestPackageOwnershipScopesOverlapsPerRepository: a linked peer lives
// inside the checkout of the repository that links it, so the containment that
// is an ownership mistake in one tree of folders is ordinary here.
func TestPackageOwnershipScopesOverlapsPerRepository(t *testing.T) {
	root := t.TempDir()
	outer := filepath.Join(root, "api")
	inner := filepath.Join(outer, ".links", "sdk")
	require.NoError(t, os.MkdirAll(inner, 0o755))
	pkgs := []*model.Package{
		{Name: "api-pkg", Dir: outer, Repository: "api"},
		{Name: "sdk-pkg", Dir: inner, Repository: "sdk"},
	}
	err := validatePackageOwnershipMode(pkgs, false)
	require.Error(t, err)
	assert.Equal(t, DiagnosticOwnershipInvalid, DiagnosticCode(err))
	assert.NoError(t, validatePackageOwnershipMode(pkgs, true))
	assert.Error(t, validatePackageOwnershipMode(pkgs, false), "the central mode keeps its answer")

	// Names stay one graph whichever topology composed the fleet.
	duplicate := []*model.Package{
		{Name: "api-pkg", Dir: outer, Repository: "api"},
		{Name: "API-PKG", Dir: inner, Repository: "sdk"},
	}
	for _, perRepository := range []bool{false, true} {
		err := validatePackageOwnershipMode(duplicate, perRepository)
		require.Error(t, err)
		assert.Equal(t, DiagnosticComposition, DiagnosticCode(err))
	}
}

// TestReadLinkInventoryReadsWhatIsDeclared: the inventory is read from the
// file alone, because a fleet that is not linked yet is exactly the state
// `dispat compute` is asked to repair.
func TestReadLinkInventoryReadsWhatIsDeclared(t *testing.T) {
	root := choreoRepo(t, "api", choreoConfig("api"))
	inventory, err := readLinkInventory(t.Context(), root)
	require.NoError(t, err)
	assert.Empty(t, inventory, "a repository with no .gitmodules declares no links")

	modules := filepath.Join(root, ".gitmodules")
	require.NoError(t, os.WriteFile(modules, nil, 0o644))
	inventory, err = readLinkInventory(t.Context(), root)
	require.NoError(t, err)
	assert.Empty(t, inventory, "an empty .gitmodules declares no links")

	require.NoError(t, os.WriteFile(modules,
		[]byte("[submodule \"sdk\"]\n\tpath = ./.links//sdk\n\turl = ../sdk\n[submodule \"bare\"]\n\turl = ../bare\n"), 0o644))
	inventory, err = readLinkInventory(t.Context(), root)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"sdk": ".links/sdk"}, inventory,
		"paths are normalized and an entry without a path is not a link")

	require.NoError(t, os.WriteFile(modules, []byte("this is not a config file\n"), 0o644))
	_, err = readLinkInventory(t.Context(), root)
	require.Error(t, err)
	assert.Equal(t, DiagnosticRepositoryInvalid, DiagnosticCode(err))
}

// TestPeerConfigPathDoesNotAscend: a peer sits inside its linker's checkout,
// and an ascent would compose it against a configuration it does not own.
func TestPeerConfigPathDoesNotAscend(t *testing.T) {
	root := choreoRepo(t, "api", choreoConfig("api"))
	path, err := peerConfigPath(root)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "dispat.json"), path)

	nested := filepath.Join(root, ".links", "sdk")
	require.NoError(t, os.MkdirAll(nested, 0o755))
	_, err = peerConfigPath(nested)
	require.Error(t, err)
	assert.ErrorContains(t, err, "no dispat config file at")
	assert.ErrorContains(t, err, "dispat.json, dispat.yaml, dispat.yml, dispat.toml")

	require.NoError(t, os.WriteFile(filepath.Join(nested, "dispat.yaml"), []byte("repository: sdk\n"), 0o644))
	path, err = peerConfigPath(nested)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(nested, "dispat.yaml"), path)
}

// TestWorkspaceAnswersAboutLinksWithoutAComposition: the accessors a planner
// and a recorder read are asked of a workspace built by hand as well as of a
// composed one, so each one answers safely when there is nothing to say.
func TestWorkspaceAnswersAboutLinksWithoutAComposition(t *testing.T) {
	central := &Workspace{Repositories: []Repository{
		{Name: ControlRepository, Root: "/w", Control: true},
		{Name: "sdk", Root: "/w/sources/sdk"},
	}}
	entry := central.EntryRepository()
	require.NotNil(t, entry)
	assert.Equal(t, ControlRepository, entry.Name,
		"a workspace built without the entry flag still answers with the control repository")
	assert.False(t, central.IsLinked())
	assert.Empty(t, central.RepositoryByName("sdk").LinkPeers(), "a central source links nothing")

	apart := &Workspace{Repositories: []Repository{
		{Name: "api", Root: "/w/api", Entry: true},
		{Name: "sdk", Root: "/w/sdk"},
	}}
	assert.Nil(t, apart.LinkRoute("api", "sdk"), "no chain of links is no route")
	assert.Equal(t, []string{"api"}, apart.LinkRoute("api", "API"))

	var absent *Repository
	assert.Nil(t, absent.LinkPeers())
}

// TestDefaultLinkPathStaysOutOfPackageDiscovery: the default is a dot folder
// on purpose — discovery never descends into one, so a linked checkout cannot
// be mistaken for its linker's package folder.
func TestDefaultLinkPathStaysOutOfPackageDiscovery(t *testing.T) {
	assert.Equal(t, ".links/sdk", DefaultLinkPath("sdk"))
	assert.True(t, filepath.Base(filepath.Dir(DefaultLinkPath("sdk")))[0] == '.')
}
