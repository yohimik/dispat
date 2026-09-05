// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/ccme"

	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// Narrow is a pure operation over a computed plan — no history, no git — so
// these tests state the graph directly and assert the rule: what still
// releases, what the release order holds back, and which versioning groups a
// selection splits.

// chainPlan is a plan over `a <- b <- c` (c consumes b consumes a) where every
// package releases, with each package's space given by spaces[name] (empty
// meaning an independent one) and the whole thing in dependency order.
func chainPlan(spaces map[string]*model.Space) *Plan {
	order := []string{"a", "b", "c"}
	p := &Plan{
		Order:     order,
		Releases:  make(map[string]*Release, len(order)),
		Providers: map[string][]string{"b": {"a"}, "c": {"b"}},
	}
	for _, name := range order {
		space := spaces[name]
		if space == nil {
			space = &model.Space{Name: name}
		}
		p.Releases[name] = &Release{
			Pkg:     &model.Package{Name: name, Space: space},
			Bump:    ccme.BumpMinor,
			NewWork: true,
		}
	}
	return p
}

// releasing lists the packages a narrowed plan still releases, read back
// through Releasing() rather than through Narrow's return value: the two must
// agree, because the executor only ever asks the former.
func releasing(p *Plan) []string {
	var out []string
	for _, rel := range p.Releasing() {
		out = append(out, rel.Pkg.Name)
	}
	return out
}

func TestNarrowReleasesTheSelectionAndDeselectsTheRest(t *testing.T) {
	p := chainPlan(nil)
	n := p.Narrow([]string{"a"})

	assert.True(t, n.Clean(), "a provider selected on its own costs nothing")
	assert.Equal(t, []string{"a"}, n.Release)
	assert.Equal(t, []string{"a"}, releasing(p))
	assert.Equal(t, []string{"b", "c"}, p.Deselected())
	// Nobody asked for b and c, so neither is a finding and neither carries a
	// reason to explain: they are simply outside the selection.
	assert.Empty(t, p.Releases["b"].WaitingFor)
	assert.Empty(t, p.Releases["c"].WaitingFor)
}

func TestNarrowWithholdsAConsumerWhoseProviderIsLeftOut(t *testing.T) {
	p := chainPlan(nil)
	n := p.Narrow([]string{"c"})

	require.Len(t, n.Withheld, 1)
	assert.Equal(t, Withheld{Pkg: "c", Waiting: []string{"b"}}, n.Withheld[0])
	assert.False(t, n.Clean())
	assert.Empty(t, n.Release, "releasing c before b is the one thing publish order forbids")
	assert.Empty(t, releasing(p))
	assert.Equal(t, []string{"b"}, p.Releases["c"].WaitingFor)
}

func TestNarrowWithholdingIsTransitiveDownTheChain(t *testing.T) {
	// b and c are both asked for; a is not. b waits for a, and c waits for b —
	// which the single dependency-ordered pass gets without a second look,
	// because b's answer is settled before c is reached.
	p := chainPlan(nil)
	n := p.Narrow([]string{"b", "c"})

	require.Len(t, n.Withheld, 2)
	assert.Equal(t, Withheld{Pkg: "b", Waiting: []string{"a"}}, n.Withheld[0])
	assert.Equal(t, Withheld{Pkg: "c", Waiting: []string{"b"}}, n.Withheld[1])
	assert.Empty(t, releasing(p))
}

func TestNarrowIgnoresProvidersThatAreNotReleasing(t *testing.T) {
	// Only a package this run would have released can hold a consumer back. An
	// unchanged provider has nothing to be published before, and a held one is
	// excluded from the plan by its own directive (§13.6a).
	for _, tc := range []struct {
		name    string
		provide func(*Release)
	}{
		{"unchanged", func(r *Release) { r.Bump, r.NewWork = ccme.BumpNone, false }},
		{"held", func(r *Release) { r.Held = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := chainPlan(nil)
			tc.provide(p.Releases["b"])
			n := p.Narrow([]string{"c"})

			assert.True(t, n.Clean())
			assert.Equal(t, []string{"c"}, n.Release)
			assert.Equal(t, []string{"c"}, releasing(p))
		})
	}
}

func TestNarrowNamesOneProviderPerPackageHoweverManyEdgesDeclareIt(t *testing.T) {
	// One pair declared under two dependency kinds is two edges and one
	// provider; the reason a package waits must not repeat it.
	p := chainPlan(nil)
	p.Providers["c"] = []string{"b", "b", "a"}
	n := p.Narrow([]string{"c"})

	require.Len(t, n.Withheld, 1)
	assert.Equal(t, []string{"b", "a"}, n.Withheld[0].Waiting)
}

func TestNarrowIgnoresSelectedPackagesThatAreNotReleasing(t *testing.T) {
	// A selection may legitimately name a package with nothing pending — a
	// space term reaches every package of the space. Nothing releases, and
	// nothing is a finding either: there was never a release to withhold.
	p := chainPlan(nil)
	p.Releases["a"].Bump, p.Releases["a"].NewWork = ccme.BumpNone, false
	n := p.Narrow([]string{"a"})

	assert.True(t, n.Clean())
	assert.Empty(t, n.Release)
	assert.Empty(t, releasing(p))
}

func TestNarrowReportsAVersioningGroupItSplits(t *testing.T) {
	// A shared version is the group's promise, and releasing part of the group
	// suspends it until the next run rides the rest up (W234). That is a
	// warning, not a refusal: nothing is published out of order.
	libs := &model.Space{Name: "libs", Versioning: model.VersioningFixed}
	p := chainPlan(map[string]*model.Space{"a": libs, "b": libs, "c": libs})
	p.Providers = nil // an independent group, so only the split is in play
	n := p.Narrow([]string{"a", "b"})

	assert.False(t, n.Clean())
	assert.Equal(t, []string{"a", "b"}, n.Release, "the split members still release")
	require.Len(t, n.Split, 1)
	assert.Equal(t, SplitGroup{Name: "libs", Releasing: []string{"a", "b"}, LeftBehind: []string{"c"}},
		n.Split[0])
}

func TestNarrowGroupSplitCountsMembersTheOrderWithheld(t *testing.T) {
	// c is selected and cannot go, which splits its group exactly as leaving it
	// out would have: the split is read off the packages that actually release.
	libs := &model.Space{Name: "libs", Versioning: model.VersioningFixed}
	p := chainPlan(map[string]*model.Space{"a": libs, "b": libs, "c": libs})
	n := p.Narrow([]string{"a", "c"})

	require.Len(t, n.Withheld, 1)
	assert.Equal(t, "c", n.Withheld[0].Pkg)
	require.Len(t, n.Split, 1)
	assert.Equal(t, []string{"a"}, n.Split[0].Releasing)
	assert.Equal(t, []string{"b", "c"}, n.Split[0].LeftBehind)
}

func TestNarrowWholeGroupSelectedIsClean(t *testing.T) {
	// A declared versionGroups key spanning two spaces is one group, and taking
	// all of it is no split at all.
	group := func(space string) *model.Space {
		return &model.Space{Name: space, Versioning: model.VersioningFixedMajor, VersionGroup: "cli"}
	}
	p := chainPlan(map[string]*model.Space{"a": group("libs"), "b": group("apps"), "c": group("apps")})
	n := p.Narrow([]string{"a", "b", "c"})

	assert.True(t, n.Clean())
	assert.Empty(t, n.Split)
	assert.Equal(t, []string{"a", "b", "c"}, n.Release)
	assert.Empty(t, p.Deselected())
}

func TestNarrowIndependentPackagesFormNoGroup(t *testing.T) {
	p := chainPlan(nil) // every space independent
	n := p.Narrow([]string{"a"})
	assert.Empty(t, n.Split)
	assert.Equal(t, "", p.Releases["a"].Pkg.VersionGroupName())
}
