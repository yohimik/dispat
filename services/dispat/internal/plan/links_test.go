// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/ccme"
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// linkedGit is a repository of a choreographed fleet: ordinary history, plus
// the two reads link evidence makes — what a revision's tree pins, and what a
// revision's subject line says.
type linkedGit struct {
	*fakeGit
	head     string
	trees    map[string]map[string]string
	subjects map[string]string
	reads    int
	subject  int
}

func newLinkedGit(head string, history ...commit) *linkedGit {
	return &linkedGit{fakeGit: newFakeGit(history...), head: head,
		trees: map[string]map[string]string{}, subjects: map[string]string{}}
}

// pins records what one revision's tree holds for the fleet links.
func (g *linkedGit) pins(revision string, links map[string]string) *linkedGit {
	g.trees[revision] = links
	return g
}

// records marks a revision as an ordinary release commit for these tags.
func (g *linkedGit) records(revision, subject string) *linkedGit {
	g.subjects[revision] = subject
	return g
}

func (g *linkedGit) GitlinksAtPaths(_ context.Context, revision string, paths []string) (map[string]string, error) {
	g.reads++
	out := map[string]string{}
	for _, path := range paths {
		if pin := g.trees[revision][path]; pin != "" {
			out[path] = pin
		}
	}
	return out, nil
}

func (g *linkedGit) CommitSubjects(_ context.Context, revisions []string) (map[string]string, error) {
	g.subject++
	out := make(map[string]string, len(revisions))
	for _, revision := range revisions {
		if subject, ok := g.subjects[revision]; ok {
			out[revision] = subject
		}
	}
	return out, nil
}

func (g *linkedGit) HeadSHA(context.Context) (string, error) { return g.head, nil }

func (g *linkedGit) IsCommitPresent(_ context.Context, rev string) (bool, error) {
	return g.index(rev) >= 0, nil
}

// fleetPackages is the two-package graph every test below plans: a provider
// and the consumer that depends on it, in separate repositories.
func fleetPackages() ([]*model.Package, []model.Dependency) {
	libs, apps := &model.Space{Name: "libs"}, &model.Space{Name: "apps"}
	return []*model.Package{
			{Name: "lib", Dir: "/w/sdk/lib", RepoRoot: "/w/sdk", Repository: "sdk", Space: libs},
			{Name: "app", Dir: "/w/api/app", RepoRoot: "/w/api", Repository: "api", Space: apps},
		},
		[]model.Dependency{{Consumer: "app", Provider: "lib"}}
}

// TestLinkEvidenceResolvesAnAdjacentBoundary: the release commit an
// consumer's tag sits on carries the pin of its neighbour, and that pin is
// where the consumer's window in the neighbour begins.
func TestLinkEvidenceResolvesAnAdjacentBoundary(t *testing.T) {
	sdk := newLinkedGit("a2",
		commit{sha: "a1", message: "feat(lib)^: expose a stream", files: []string{"lib/one.go"}},
		commit{sha: "a2", message: "fix(lib)^: close the stream", files: []string{"lib/two.go"}})
	api := newLinkedGit("r1", commit{sha: "r1", message: "chore(release): app@1.0.0", files: []string{"app/one.go"}})
	api.tag("app", "1.0.0", "r1")
	api.records("r1", "chore(release): app@1.0.0").pins("r1", map[string]string{".links/sdk": "a1"})
	sdk.tag("lib", "1.0.0", "a1")
	sdk.records("a1", "chore(release): lib@1.0.0")

	packages, dependencies := fleetPackages()
	stats := &HistoryStats{}
	pl, err := Compute(t.Context(), api, Options{
		Packages: packages, Dependencies: dependencies,
		Initials:     map[string]ccme.Version{"lib": v(1, 0, 0), "app": v(1, 0, 0)},
		LinkEvidence: true,
		HistoryStats: stats,
		Repositories: map[string]RepositoryHistory{
			"api": {Name: "api", Root: "/w/api", Git: api, Links: map[string]string{"sdk": ".links/sdk"}},
			"sdk": {Name: "sdk", Root: "/w/sdk", Git: sdk, Linker: "api", Links: map[string]string{"api": ".links/api"}},
		},
	})
	require.NoError(t, err)
	require.False(t, pl.IsFatal(), "%v", pl.Diagnostics)
	assert.True(t, pl.Releases["lib"].IsReleasing(), "the provider's own commit releases it")
	assert.Equal(t, "1.0.1", pl.Releases["app"].Next.String(),
		"the window in sdk starts at the pin the release recorded: a2 is catch-up work and a1 is not")
	assert.EqualValues(t, 3, stats.LinkReads.Load(),
		"one subject read per repository holding release tags, and one tree read for the hop")
	assert.Equal(t, 1, api.reads, "the hop is read once and remembered")
}

// TestLinkEvidenceRelaysThroughTheRoute: two repositories that do not link
// each other are still comparable, hop by hop along the one route between
// them.
func TestLinkEvidenceRelaysThroughTheRoute(t *testing.T) {
	core := newLinkedGit("c2",
		commit{sha: "c1", message: "feat(core)^: add", files: []string{"core/one.go"}},
		commit{sha: "c2", message: "fix(core)^: repair", files: []string{"core/two.go"}})
	sdk := newLinkedGit("s1", commit{sha: "s1", message: "chore: relay", files: []string{"sdk/one.go"}})
	api := newLinkedGit("r1", commit{sha: "r1", message: "chore(release): app@1.0.0", files: []string{"app/one.go"}})
	api.tag("app", "1.0.0", "r1")
	api.records("r1", "chore(release): app@1.0.0").pins("r1", map[string]string{".links/sdk": "s1"})
	sdk.pins("s1", map[string]string{".links/core": "c1"})
	core.tag("core", "1.0.0", "c1")

	apps := &model.Space{Name: "apps"}
	packages := []*model.Package{
		{Name: "core", Dir: "/w/core/core", RepoRoot: "/w/core", Repository: "core", Space: &model.Space{Name: "libs"}},
		{Name: "app", Dir: "/w/api/app", RepoRoot: "/w/api", Repository: "api", Space: apps},
	}
	pl, err := Compute(t.Context(), api, Options{
		Packages: packages, Dependencies: []model.Dependency{{Consumer: "app", Provider: "core"}},
		Initials:     map[string]ccme.Version{"core": v(1, 0, 0), "app": v(1, 0, 0)},
		LinkEvidence: true,
		Repositories: map[string]RepositoryHistory{
			"api":  {Name: "api", Root: "/w/api", Git: api, Links: map[string]string{"sdk": ".links/sdk"}},
			"sdk":  {Name: "sdk", Root: "/w/sdk", Git: sdk, Linker: "api", Links: map[string]string{"api": ".links/api", "core": ".links/core"}},
			"core": {Name: "core", Root: "/w/core", Git: core, Linker: "sdk", Links: map[string]string{"sdk": ".links/sdk"}},
		},
	})
	require.NoError(t, err)
	require.False(t, pl.IsFatal(), "%v", pl.Diagnostics)
	assert.Equal(t, "1.0.1", pl.Releases["app"].Next.String(),
		"c2 is what the relayed boundary leaves in the window; c1 is behind it")
	assert.Equal(t, 1, sdk.reads, "the relay's tree is read once")
}

// TestLinkEvidenceRefusesWhatItCannotProve: every way the chain can break is
// E333 naming where it stopped, never a plan that guesses and never a raw Git
// failure.
func TestLinkEvidenceRefusesWhatItCannotProve(t *testing.T) {
	for _, tc := range []struct {
		name    string
		arrange func(api, sdk *linkedGit)
		message string
	}{
		{
			name:    "a tag that is not on a release commit",
			arrange: func(api, _ *linkedGit) { api.records("r1", "chore: tag it by hand") },
			message: "is not recorded by a release commit",
		},
		{
			name:    "a release commit naming another package's tag",
			arrange: func(api, _ *linkedGit) { api.records("r1", "chore(release): other@1.0.0") },
			message: "is not recorded by a release commit",
		},
		{
			name: "a release commit with no link to the provider",
			arrange: func(api, _ *linkedGit) {
				api.records("r1", "chore(release): app@1.0.0").pins("r1", map[string]string{})
			},
			message: "records no link to sdk",
		},
		{
			name: "a pin the provider checkout does not hold",
			arrange: func(api, _ *linkedGit) {
				api.records("r1", "chore(release): app@1.0.0").pins("r1", map[string]string{".links/sdk": "absent"})
			},
			message: "is not reachable from its active revision",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sdk := newLinkedGit("a2",
				commit{sha: "a1", message: "feat(lib): expose", files: []string{"lib/one.go"}},
				commit{sha: "a2", message: "fix(lib): close", files: []string{"lib/two.go"}})
			api := newLinkedGit("r1", commit{sha: "r1", message: "chore(release): app@1.0.0", files: []string{"app/one.go"}})
			api.tag("app", "1.0.0", "r1")
			tc.arrange(api, sdk)

			packages, dependencies := fleetPackages()
			pl, err := Compute(t.Context(), api, Options{
				Packages: packages, Dependencies: dependencies,
				Initials:     map[string]ccme.Version{"lib": v(1, 0, 0), "app": v(1, 0, 0)},
				LinkEvidence: true,
				Repositories: map[string]RepositoryHistory{
					"api": {Name: "api", Root: "/w/api", Git: api, Links: map[string]string{"sdk": ".links/sdk"}},
					"sdk": {Name: "sdk", Root: "/w/sdk", Git: sdk, Linker: "api", Links: map[string]string{"api": ".links/api"}},
				},
			})
			require.NoError(t, err, "a missing proof is a diagnostic, never a Git failure")
			require.True(t, pl.IsFatal())
			found := false
			for _, d := range pl.Diagnostics {
				if d.Code == CodeRepositoryBoundary {
					found = true
					assert.Contains(t, d.Message, tc.message)
					assert.Contains(t, d.Message, "add repositoryBaselines for repository sdk",
						"the remedy is the tuple, and it names the repository to write it for")
				}
			}
			assert.True(t, found, "%v", pl.Diagnostics)
		})
	}
}

// TestLinkBoundaryYieldsToAnExplicitTuple: the operator's tuple is the remedy
// the diagnostic asks for, so it has to win over the evidence that failed.
func TestLinkBoundaryYieldsToAnExplicitTuple(t *testing.T) {
	sdk := newLinkedGit("a2",
		commit{sha: "a1", message: "feat(lib)^: expose", files: []string{"lib/one.go"}},
		commit{sha: "a2", message: "fix(lib)^: close", files: []string{"lib/two.go"}})
	api := newLinkedGit("r1", commit{sha: "r1", message: "chore: hand made", files: []string{"app/one.go"}})
	api.tag("app", "1.0.0", "r1")

	packages, dependencies := fleetPackages()
	pl, err := Compute(t.Context(), api, Options{
		Packages: packages, Dependencies: dependencies,
		Initials:     map[string]ccme.Version{"lib": v(1, 0, 0), "app": v(1, 0, 0)},
		LinkEvidence: true,
		RepositoryBaselines: []RepositoryBaseline{{
			Consumer: "app", ReleaseTag: "app@1.0.0", Repository: "sdk", Revision: "a1"}},
		Repositories: map[string]RepositoryHistory{
			"api": {Name: "api", Root: "/w/api", Git: api, Links: map[string]string{"sdk": ".links/sdk"}},
			"sdk": {Name: "sdk", Root: "/w/sdk", Git: sdk, Linker: "api", Links: map[string]string{"api": ".links/api"}},
		},
	})
	require.NoError(t, err)
	require.False(t, pl.IsFatal(), "%v", pl.Diagnostics)
	assert.Equal(t, "1.0.1", pl.Releases["app"].Next.String(),
		"the tuple names a1, so only a2 is left in the window")
}

// TestLinkMoveIsNotAPackageChange: a settlement writes a pointer to another
// repository. Counting it would release whatever package encloses the link
// every time a neighbour publishes.
func TestLinkMoveIsNotAPackageChange(t *testing.T) {
	api := newLinkedGit("r2",
		commit{sha: "r1", message: "chore(release): app@1.0.0", files: []string{"app/one.go"}},
		commit{sha: "r2", message: "chore(release): settle links", files: []string{".links/sdk"}})
	api.tag("app", "1.0.0", "r1")
	api.records("r1", "chore(release): app@1.0.0").pins("r1", map[string]string{".links/sdk": "a1"})
	sdk := newLinkedGit("a1", commit{sha: "a1", message: "feat(lib): expose", files: []string{"lib/one.go"}})
	sdk.tag("lib", "1.0.0", "a1")

	packages, dependencies := fleetPackages()
	packages[1].Dir = "/w/api"
	pl, err := Compute(t.Context(), api, Options{
		Packages: packages, Dependencies: dependencies,
		Initials:     map[string]ccme.Version{"lib": v(1, 0, 0), "app": v(1, 0, 0)},
		LinkEvidence: true,
		Repositories: map[string]RepositoryHistory{
			"api": {Name: "api", Root: "/w/api", Git: api, Links: map[string]string{"sdk": ".links/sdk"}},
			"sdk": {Name: "sdk", Root: "/w/sdk", Git: sdk, Linker: "api", Links: map[string]string{"api": ".links/api"}},
		},
	})
	require.NoError(t, err)
	require.False(t, pl.IsFatal(), "%v", pl.Diagnostics)
	assert.False(t, pl.Releases["app"].IsReleasing(),
		"the only commit since the tag moved a fleet link, which is not the package's work")
}

// TestProjectSinceFollowsTheFleetRoutes: `--since <entry revision>` is one
// range per repository, projected through the links, and a repository that
// revision pins nothing for is simply everything.
func TestProjectSinceFollowsTheFleetRoutes(t *testing.T) {
	sdk := newLinkedGit("a2",
		commit{sha: "a1", message: "feat(lib): expose", files: []string{"lib/one.go"}},
		commit{sha: "a2", message: "fix(lib): close", files: []string{"lib/two.go"}})
	api := newLinkedGit("r2",
		commit{sha: "r1", message: "chore: base", files: []string{"app/one.go"}},
		commit{sha: "r2", message: "feat(app): add", files: []string{"app/two.go"}})
	api.pins("r1", map[string]string{".links/sdk": "a1"})

	packages, dependencies := fleetPackages()
	repositories := map[string]RepositoryHistory{
		"api": {Name: "api", Root: "/w/api", Git: api, Links: map[string]string{"sdk": ".links/sdk"}},
		"sdk": {Name: "sdk", Root: "/w/sdk", Git: sdk, Linker: "api", Links: map[string]string{"api": ".links/api"}},
	}
	names, err := PackagesChangedSince(t.Context(), api, Options{
		Packages: packages, Dependencies: dependencies, LinkEvidence: true, Repositories: repositories,
	}, "r1")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"app", "lib"}, names,
		"the entry's own range is r1..HEAD, and sdk's is the pin that revision recorded")

	// A revision that pins nothing projects to the empty revision, which is
	// every commit of that repository — the same answer an absent gitlink has
	// always given an orchestrated fleet.
	api.trees["r1"] = map[string]string{}
	names, err = PackagesChangedSince(t.Context(), api, Options{
		Packages: packages, Dependencies: dependencies, LinkEvidence: true, Repositories: repositories,
	}, "r1")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"app", "lib"}, names)

	// Without an entry the projection says so rather than guessing one.
	broken := map[string]RepositoryHistory{
		"api": {Name: "api", Root: "/w/api", Git: api},
		"sdk": {Name: "sdk", Root: "/w/sdk", Git: sdk},
	}
	_, err = PackagesChangedSince(t.Context(), api, Options{
		Packages: packages, Dependencies: dependencies, LinkEvidence: true, Repositories: broken,
	}, "r1")
	assert.ErrorContains(t, err, "no entry repository")
}

// TestReleaseSubjectTagsReadsOneFormat: both sagas recognise a release commit
// the same way, so the reading is one function and is checked once.
func TestReleaseSubjectTagsReadsOneFormat(t *testing.T) {
	assert.Equal(t, []string{"app@1.0.0"}, releaseSubjectTags("chore(release): app@1.0.0"))
	assert.Equal(t, []string{"app@1.0.0", "lib@2.0.0"},
		releaseSubjectTags("chore(release): app@1.0.0, lib@2.0.0"))
	assert.Nil(t, releaseSubjectTags("feat(app): something else"))
	assert.Nil(t, releaseSubjectTags(""))
	assert.Empty(t, releaseSubjectTags("chore(release): "), "a subject naming nothing records nothing")
	assert.True(t, releaseSubjectNames("chore(release): app@1.0.0, lib@2.0.0", "lib@2.0.0"))
	assert.False(t, releaseSubjectNames("chore(release): app@1.0.0", "lib@2.0.0"))
}

// TestLinkRouteIsUniqueAndSaysWhenItIsNot: the route is what every hop-by-hop
// read follows, and a fleet that is not joined says so instead of answering.
func TestLinkRouteIsUniqueAndSaysWhenItIsNot(t *testing.T) {
	cp := &computation{histories: map[string]RepositoryHistory{
		"api":   {Name: "api", Links: map[string]string{"sdk": ".links/sdk"}},
		"sdk":   {Name: "sdk", Linker: "api", Links: map[string]string{"api": ".links/api", "core": ".links/core"}},
		"core":  {Name: "core", Linker: "sdk", Links: map[string]string{"sdk": ".links/sdk"}},
		"apart": {Name: "apart"},
	}}
	assert.Equal(t, []string{"api", "sdk", "core"}, cp.linkRoute("api", "core"))
	assert.Equal(t, []string{"core", "sdk", "api"}, cp.linkRoute("core", "api"))
	assert.Equal(t, []string{"api"}, cp.linkRoute("api", "API"))
	assert.Nil(t, cp.linkRoute("api", "apart"))
	assert.Nil(t, cp.linkRoute("api", "absent"))

	entry, ok := cp.entryHistory()
	assert.False(t, ok, "two repositories with no linker is not one entry")
	cp.histories["apart"] = RepositoryHistory{Name: "apart", Linker: "core"}
	entry, ok = cp.entryHistory()
	require.True(t, ok)
	assert.Equal(t, "api", entry.Name)
}

// BenchmarkLinkBoundaryChain measures the Git reads a fleet's boundary
// evidence makes as the chain between a consumer and its provider grows: the
// number the cache is there to keep at one per hop.
func BenchmarkLinkBoundaryChain(b *testing.B) {
	for _, shape := range []struct{ repositories, depth int }{{2, 1}, {4, 3}, {8, 7}} {
		b.Run(benchName(shape.repositories, shape.depth), func(b *testing.B) {
			var reads int64
			for i := 0; i < b.N; i++ {
				stats := &HistoryStats{}
				packages, dependencies, repositories := linkChainFixture(shape.repositories)
				pl, err := Compute(context.Background(), repositories["r0"].Git, Options{
					Packages: packages, Dependencies: dependencies,
					Initials:     chainInitials(shape.repositories),
					LinkEvidence: true, HistoryStats: stats, Repositories: repositories,
				})
				if err != nil || pl.IsFatal() {
					b.Fatalf("plan failed: %v %v", err, pl.Diagnostics)
				}
				reads = stats.LinkReads.Load()
			}
			b.ReportMetric(float64(reads), "LinkReads")
		})
	}
}

func benchName(repositories, depth int) string {
	return "repositories=" + itoa(repositories) + "/depth=" + itoa(depth)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for ; n > 0; n /= 10 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
	}
	return string(digits)
}

// linkChainFixture builds a chain r0 - r1 - ... - rN where every repository
// holds one package, each depending on the next, and r0's release recorded the
// pins that reach all of them.
func linkChainFixture(n int) ([]*model.Package, []model.Dependency, map[string]RepositoryHistory) {
	packages := make([]*model.Package, 0, n)
	var dependencies []model.Dependency
	repositories := make(map[string]RepositoryHistory, n)
	gits := make([]*linkedGit, n)
	for i := 0; i < n; i++ {
		name := "r" + itoa(i)
		pkg := "p" + itoa(i)
		gits[i] = newLinkedGit("c"+itoa(i),
			commit{sha: "c" + itoa(i), message: "feat(" + pkg + "): work", files: []string{pkg + "/one.go"}})
		gits[i].tag(pkg, "1.0.0", "c"+itoa(i))
		gits[i].records("c"+itoa(i), "chore(release): "+pkg+"@1.0.0")
		packages = append(packages, &model.Package{
			Name: pkg, Dir: "/w/" + name + "/" + pkg, RepoRoot: "/w/" + name, Repository: name,
			Space: &model.Space{Name: "s" + itoa(i)}})
		if i > 0 {
			dependencies = append(dependencies, model.Dependency{Consumer: "p0", Provider: pkg})
		}
	}
	for i := 0; i < n; i++ {
		links := map[string]string{}
		if i > 0 {
			links["r"+itoa(i-1)] = ".links/r" + itoa(i-1)
		}
		if i+1 < n {
			links["r"+itoa(i+1)] = ".links/r" + itoa(i+1)
			gits[i].pins("c"+itoa(i), map[string]string{".links/r" + itoa(i+1): "c" + itoa(i+1)})
		}
		linker := ""
		if i > 0 {
			linker = "r" + itoa(i-1)
		}
		repositories["r"+itoa(i)] = RepositoryHistory{
			Name: "r" + itoa(i), Root: "/w/r" + itoa(i), Git: gits[i], Linker: linker, Links: links}
	}
	return packages, dependencies, repositories
}

func chainInitials(n int) map[string]ccme.Version {
	out := make(map[string]ccme.Version, n)
	for i := 0; i < n; i++ {
		out["p"+itoa(i)] = v(1, 0, 0)
	}
	return out
}
