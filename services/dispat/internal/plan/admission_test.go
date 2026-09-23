// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"context"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/ccme"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// The bump axis admits a unit for a dependent until a release of the unit's
// source DELIVERED it (§13.4a, §9.2 phase 3). These tests fence the predicate,
// the owed set it produces, the catch-up the two together make possible, and
// the claim that makes the whole thing safe: on a history where no dependent
// overtook a commit, planning is bit-for-bit what the window-only rule
// computed.

// admissionFixture builds a computation with tags and windows loaded from a
// linear history, which is the smallest thing that can answer the delivery
// question: it needs the tag inventory, the baselines those tags resolve to and
// the windows they bound, and nothing after §13.3.
func admissionFixture(t *testing.T, git *fakeGit, pkgs []*model.Package) *computation {
	t.Helper()
	cp := &computation{ctx: context.Background(), git: git, pkgs: pkgs, log: zerolog.Nop(),
		byName: map[string]*model.Package{}, rel: map[string]*Release{}, tags: map[string]gitx.Tags{},
		window: map[string]*commitSet{}, windowKey: map[string]string{},
		byKey: map[string]*commitRec{}, parents: map[string][]string{}}
	for _, p := range pkgs {
		cp.byName[p.Name] = p
	}
	require.NoError(t, cp.loadLegacyTagsAndWindows())
	return cp
}

// TestAdmissionDeliveredPredicate is the table of §13.4a's delivery test, read
// through the owed set that is the only thing asking it: every way a source can
// owe a dependent a version, and the one way it cannot.
//
// The history is one line, so "carries" and "reaches" are positions in it:
// c1 and c4 are release points, c2 and c3 carry work.
func TestAdmissionDeliveredPredicate(t *testing.T) {
	// core -> app, and `other` exists so that the union spans the whole
	// history whatever core and app have released: a commit no window holds is
	// not planned at all, and then no admission question is asked about it.
	history := []commit{
		{sha: "c1", message: "chore: base"},
		{sha: "c2", message: "feat(core)^: streaming"},
		{sha: "c3", message: "feat(app): own work"},
		{sha: "c4", message: "chore: later"},
	}
	pkgs := []*model.Package{
		{Name: "core", Dir: "/r/core"},
		{Name: "app", Dir: "/r/app"},
		{Name: "other", Dir: "/r/other"},
	}

	for name, tc := range map[string]struct {
		tags func(*fakeGit) *fakeGit
		// commit is the unit's commit, sources the unit's source set.
		commit  string
		sources []string
		want    []string
		why     string
	}{
		"unreleased target owes everything": {
			tags:    func(f *fakeGit) *fakeGit { return f.tag("core", "1.0.0", "c1").tag("other", "1.0.0", "") },
			commit:  "c2",
			sources: []string{"core"},
			want:    nil,
			why:     "an untagged target has no baseline, so nothing overtook the commit",
		},
		"target behind the commit is not asked": {
			tags: func(f *fakeGit) *fakeGit {
				return f.tag("core", "1.0.0", "c1").tag("app", "1.0.0", "c1").tag("other", "1.0.0", "")
			},
			commit:  "c2",
			sources: []string{"core"},
			want:    nil,
			why:     "the commit is still in the target's own window; the cheap half answers",
		},
		"overtaking target is owed the undelivered source": {
			tags: func(f *fakeGit) *fakeGit {
				return f.tag("core", "1.0.0", "c1").tag("app", "1.1.0", "c3").tag("other", "1.0.0", "")
			},
			commit:  "c2",
			sources: []string{"core"},
			want:    []string{"core"},
			why:     "app released past the commit while core's newest release is behind it",
		},
		"a release carrying the commit that the target reached delivered it": {
			tags: func(f *fakeGit) *fakeGit {
				return f.tag("core", "1.0.0", "c1").tag("core", "1.1.0", "c2").
					tag("app", "1.1.0", "c3").tag("other", "1.0.0", "")
			},
			commit:  "c2",
			sources: []string{"core"},
			want:    nil,
			why:     "core released c2 before app's own release reached past it",
		},
		"a release carrying the commit the target has not reached owes it still": {
			tags: func(f *fakeGit) *fakeGit {
				return f.tag("core", "1.0.0", "c1").tag("core", "1.1.0", "c4").
					tag("app", "1.1.0", "c3").tag("other", "1.0.0", "")
			},
			commit:  "c2",
			sources: []string{"core"},
			want:    []string{"core"},
			why:     "core's release sits ahead of app's baseline, so app never resolved it",
		},
		"only the sources that still owe are named": {
			tags: func(f *fakeGit) *fakeGit {
				return f.tag("core", "1.1.0", "c2").tag("other", "1.0.0", "c1").
					tag("app", "1.1.0", "c3")
			},
			commit:  "c2",
			sources: []string{"core", "other"},
			want:    []string{"other"},
			why:     "prov[d] |= owed, not the whole source set",
		},
	} {
		t.Run(name, func(t *testing.T) {
			cp := admissionFixture(t, tc.tags(newFakeGit(history...)), pkgs)
			assert.Equal(t, tc.want, cp.owedSources("app", tc.commit, tc.sources), tc.why)
		})
	}
}

// TestAdmissionAsksNothingUnderTheWindowOnlyRule pins the differential test's
// own seam: with the delivery half off, the overtaking case answers exactly
// what it answered before §13.4a existed.
func TestAdmissionAsksNothingUnderTheWindowOnlyRule(t *testing.T) {
	git := newFakeGit(
		commit{sha: "c1", message: "chore: base"},
		commit{sha: "c2", message: "feat(core)^: streaming"},
		commit{sha: "c3", message: "feat(app): own work"},
	).tag("core", "1.0.0", "c1").tag("app", "1.1.0", "c3").tag("other", "1.0.0", "")
	pkgs := []*model.Package{
		{Name: "core", Dir: "/r/core"}, {Name: "app", Dir: "/r/app"}, {Name: "other", Dir: "/r/other"},
	}
	cp := admissionFixture(t, git, pkgs)
	cp.withoutDelivery = true
	assert.Nil(t, cp.owedSources("app", "c2", []string{"core"}))
}

// TestAdmissionCatchesUpAConsumerThatOvertookItsProvider is the defect, end to
// end through Compute: one commit carries the provider's caret and the
// consumer's own feature, the provider's publish failed and the consumer
// released on its own, and the next plan still owes the consumer the provider's
// version.
func TestAdmissionCatchesUpAConsumerThatOvertookItsProvider(t *testing.T) {
	// app released 1.1.0 at c2 on its own feature; core's publish of the same
	// commit failed, so core is still at 1.0.0 with c2 pending.
	git := newFakeGit(
		commit{sha: "c1", message: "chore: base"},
		commit{sha: "c2", message: "feat(core)^: streaming\n\nfeat(app): own work"},
	).tag("core", "1.0.0", "c1").tag("utils", "1.0.0", "").tag("app", "1.1.0", "c2")

	p := compute(t, git, nil)
	core, app := p.Releases["core"], p.Releases["app"]

	require.True(t, core.IsReleasing(), "the provider's own window still holds the commit")
	assertVersion(t, v(1, 1, 0), core.Next)
	require.True(t, app.IsReleasing(),
		"the consumer released past the commit on its own bump and is still owed the provider's version")
	assertVersion(t, v(1, 1, 1), app.Next)
	assert.Equal(t, []string{"core"}, app.DueTo)
	assert.Empty(t, app.Units, "the consumer's own window is empty: this is a pure catch-up")
	require.Len(t, app.Sources, 1)
	assert.Equal(t, "c2", app.Sources[0].Commit)

	// And the second half of convergence (§19.6): once the provider's release
	// carries the commit the consumer's baseline reaches, nothing is owed.
	settled := newFakeGit(
		commit{sha: "c1", message: "chore: base"},
		commit{sha: "c2", message: "feat(core)^: streaming\n\nfeat(app): own work"},
	).tag("core", "1.0.0", "c1").tag("core", "1.1.0", "c2").
		tag("utils", "1.0.0", "").tag("app", "1.1.0", "c2").tag("app", "1.1.1", "c2")
	after := compute(t, settled, nil)
	assert.False(t, after.Releases["app"].IsReleasing(), "the delivery discharges the obligation")
	assert.False(t, after.Releases["core"].IsReleasing())
}

// TestAdmissionOwedSetNamesOnlyTheProvidersStillOwed fences prov[d] |= owed
// against the reading that attributes the whole source set: a unit written over
// two providers, one of which has already delivered the commit to the
// overtaking consumer, must explain the consumer's release by the other alone.
func TestAdmissionOwedSetNamesOnlyTheProvidersStillOwed(t *testing.T) {
	git := newFakeGit(
		commit{sha: "c1", message: "chore: base"},
		commit{sha: "c2", message: "feat(core,utils)^: shared work"},
		commit{sha: "c3", message: "feat(app): own work"},
	).tag("core", "1.1.0", "c2"). // core published the commit
					tag("utils", "1.0.0", "c1"). // utils never did
					tag("app", "1.1.0", "c3")    // app released past it on its own

	p := compute(t, git, nil)
	app := p.Releases["app"]
	require.True(t, app.IsReleasing())
	assert.Equal(t, []string{"utils"}, app.DueTo,
		"core delivered c2 to app's baseline; only utils still owes it")
	require.Len(t, app.Sources, 1)
	assert.Equal(t, "utils", app.Sources[0].Provider)
}

// TestAdmissionReportsTheCatchUpItPlans checks that an overtaking consumer
// reaches the plan through the ordinary catch-up vocabulary rather than as an
// unexplained release: W193 names the provider at the version it published.
//
// The provider's release sits AHEAD of the consumer's baseline here, which is
// the shape a deselected or failed consumer leaves behind: the provider
// published the commit in a run the consumer sat out, so the commit is in
// neither package's window and only the delivery test can still see the debt.
func TestAdmissionReportsTheCatchUpItPlans(t *testing.T) {
	git := newFakeGit(
		commit{sha: "c1", message: "chore: base"},
		commit{sha: "c2", message: "feat(core)^: streaming"},
		commit{sha: "c3", message: "chore: later"},
	).tag("core", "1.0.0", "c1").tag("core", "1.1.0", "c3"). // core published past the commit
									tag("utils", "1.0.0", "").tag("app", "1.1.0", "c2") // app's own release stopped at it

	p := compute(t, git, nil)
	app := p.Releases["app"]
	require.False(t, p.Releases["core"].IsReleasing(), "the provider released everything it had")
	require.True(t, app.IsReleasing())
	assert.True(t, app.CatchUp, "the provider is not in this plan, so the release is a catch-up")
	found := false
	for _, d := range p.Diagnostics {
		if d.Code == CodeCatchUp && d.Pkg == "app" {
			found = true
			assert.Contains(t, d.Message, "core@1.1.0")
		}
	}
	assert.True(t, found, "W193 must explain the release: %v", p.Diagnostics)
}

// ---------------------------------------------------------------------------
// the differential test
// ---------------------------------------------------------------------------

// TestAdmissionLeavesHistoriesWithoutOvertakingUnchanged is the claim that
// makes the delivery test a strict addition: it changes planning only where a
// dependent got ahead of a commit, and nowhere else.
//
// Two claims over the same generated histories, because each is worth a
// different kind of evidence. Where no commit sits at or behind any
// dependent's baseline the delivery half cannot fire at all, and the two rules
// must produce the same plan down to the diagnostics. Where one does, the new
// rule must still be MONOTONE: every bump the window-only rule assigned is
// still assigned, and every contribution it recorded is still recorded, so
// nothing that released before stops releasing.
//
// The histories are generated rather than written out because the interaction
// is between three things at once, namely where each package's tags sit, which
// commits carry propagating units, and which dependents the graph reaches, and
// a hand-written fixture pins one triple per test. Seeded, so a failure is
// reproducible from the case index alone.
func TestAdmissionLeavesHistoriesWithoutOvertakingUnchanged(t *testing.T) {
	const cases = 400
	rng := rand.New(rand.NewSource(20260922))
	identical, monotone, fatal := 0, 0, 0

	for round := 0; round < cases; round++ {
		// Half the cases are settled by construction, with every release point
		// behind every record, and half are free, so both claims are made
		// about a populated bucket rather than about whatever the dice gave.
		git, pkgs, deps := randomAdmissionHistory(rng, round%2 == 0)

		before := computeAdmission(t, git, pkgs, deps, true)
		after := computeAdmission(t, git, pkgs, deps, false)
		if before.IsInvalid() || after.IsInvalid() {
			// A generated history may land on a version guard (E185, E195).
			// Both rules report it; comparing the plans past an error compares
			// two abandoned computations.
			fatal++
			continue
		}

		if isOvertakingImpossible(git, pkgs) {
			identical++
			require.Equal(t, admissionFingerprint(before), admissionFingerprint(after),
				"round %d: no dependent overtook a commit, so the plans must be identical", round)
			continue
		}
		monotone++
		assertAdmissionMonotone(t, round, before, after)
	}

	// Both buckets have to be populated, or the test proves one claim about
	// nothing: a generator that never overtakes would assert identity over
	// histories the delivery test never reads, and one that always does would
	// assert monotonicity and never identity.
	assert.Greater(t, identical, 40, "histories where nothing overtook a commit")
	assert.Greater(t, monotone, 40, "histories where something did")
	t.Logf("identical %d, monotone %d, skipped on a version guard %d", identical, monotone, fatal)
}

// randomAdmissionHistory generates one workspace and one linear history: a
// random acyclic graph, a random unit stream, and a random release position per
// package. Dense and small, because the cases that matter are diamonds and
// packages released at different depths, and both need packages that share
// providers.
//
// isSettled asks for the shape an ordinary repository is in: every package's
// release point sits behind every record, so nothing overtook anything and the
// delivery half of the admission has nothing to answer. The free shape places
// tags anywhere, which is where a dependent gets ahead of a commit.
func randomAdmissionHistory(rng *rand.Rand, isSettled bool) (
	*fakeGit, []*model.Package, []model.Dependency) {

	n := 2 + rng.Intn(4)
	names := make([]string, n)
	for i := range names {
		names[i] = fmt.Sprintf("p%d", i)
	}
	space := &model.Space{Name: "s"}
	pkgs := make([]*model.Package, n)
	for i, name := range names {
		pkgs[i] = &model.Package{Name: name, Dir: "/r/" + name, Space: space}
	}
	// Edges run from a provider to a consumer later in a random order, so the
	// graph is acyclic and name order says nothing about topology.
	order := rng.Perm(n)
	var deps []model.Dependency
	for i, from := range order {
		for _, to := range order[i+1:] {
			if rng.Intn(2) == 0 {
				deps = append(deps, model.Dependency{Consumer: names[to], Provider: names[from]})
			}
		}
	}

	commits := 2 + rng.Intn(6)
	// split is where the released part of the history ends and the pending part
	// begins. In the settled shape nothing before it is a record and no tag
	// sits after it; in the free shape it is just a position like any other.
	split := rng.Intn(commits)
	history := make([]commit, 0, commits)
	for i := 0; i < commits; i++ {
		message := randomAdmissionMessage(rng, names)
		if isSettled && i < split {
			// A scopeless record: a real commit that addresses no package, so
			// the released part of the history carries nothing that could be
			// owed to anybody (and no parse error either, which would abandon
			// the whole computation before propagation ran).
			message = []string{"docs: notes", "refactor: tidy up", "chore: bookkeeping"}[rng.Intn(3)]
		}
		history = append(history, commit{sha: fmt.Sprintf("c%d", i+1), message: message})
	}
	git := newFakeGit(history...)
	for _, name := range names {
		// "" is a tag before all recorded history, which is how a package that
		// has released everything it knows about is described; an index into
		// the history is a package that released up to there and no further.
		at := ""
		if isSettled {
			if split > 0 && rng.Intn(2) == 0 {
				at = history[rng.Intn(split)].sha
			}
		} else if pick := rng.Intn(commits + 2); pick < commits {
			at = history[pick].sha
		}
		git.tag(name, fmt.Sprintf("1.%d.0", rng.Intn(3)), at)
	}
	return git, pkgs, deps
}

// randomAdmissionMessage writes one commit message: a CCME unit over a random
// package with a random propagation knob, a two-package unit, a hold, or a
// message that is not a record at all.
func randomAdmissionMessage(rng *rand.Rand, names []string) string {
	switch rng.Intn(8) {
	case 0:
		return "docs: notes"
	case 1:
		return "refactor: tidy up"
	case 2:
		return fmt.Sprintf("chore(%s): housekeeping\n\nRelease-As: none", names[rng.Intn(len(names))])
	case 3:
		a, b := names[rng.Intn(len(names))], names[rng.Intn(len(names))]
		return fmt.Sprintf("feat(%s,%s)^: shared work", a, b)
	}
	kind := []string{"feat", "fix", "perf"}[rng.Intn(3)]
	// The knob is written where CCME writes it: the propagation carets, then
	// the depth, then the breaking marker last.
	knob := []string{"", "^", "^^", "^++1", "^++2", "^++*", "^!", "!"}[rng.Intn(8)]
	return fmt.Sprintf("%s(%s)%s: work", kind, names[rng.Intn(len(names))], knob)
}

// computeAdmission plans the generated workspace under one of the two rules.
func computeAdmission(t *testing.T, git TagInventoryGitx, pkgs []*model.Package,
	deps []model.Dependency, windowOnly bool) *Plan {

	t.Helper()
	p, err := Compute(context.Background(), git, Options{Packages: pkgs, Dependencies: deps,
		Root: "/r", withoutDelivery: windowOnly})
	require.NoError(t, err)
	return p
}

// isOvertakingImpossible reports whether the delivery half of the admission
// cannot fire on this history: no commit at or behind any package's baseline
// carries a unit, so no dependent can be owed a contribution it got ahead of.
//
// It is deliberately an over-approximation computed from the history itself
// rather than from the rule it is classifying: it asks nothing about which
// dependents a unit reaches, so a history it rejects may well have nothing
// owed. What it may never do is call a history safe when a unit sits behind a
// baseline, and that is a position comparison in a linear history.
func isOvertakingImpossible(git *fakeGit, pkgs []*model.Package) bool {
	deepest := -1
	for _, p := range pkgs {
		for _, name := range git.tagsFor[p.Name] {
			if sha := git.tags[name]; sha != "" {
				deepest = max(deepest, git.index(sha))
			}
		}
	}
	for i := 0; i <= deepest && i < len(git.history); i++ {
		if strings.Contains(git.history[i].message, "(") {
			return false // a scoped record at or behind somebody's baseline
		}
	}
	return true
}

// admissionFingerprint renders everything the bump axis decides, so that two
// plans compared through it differ whenever any of it does: the versions, the
// two bumps and the effective one, the release verdicts, the attribution and
// its tuples, the provider updates, and the diagnostics.
func admissionFingerprint(p *Plan) string {
	var b strings.Builder
	for _, name := range p.Order {
		r := p.Releases[name]
		fmt.Fprintf(&b, "%s next=%s channel=%s own=%s prop=%s bump=%s new=%t releasing=%t held=%t catchup=%t\n",
			name, r.Next, r.Channel, r.OwnBump, r.PropagatedBump, r.Bump,
			r.NewWork, r.IsReleasing(), r.Held, r.CatchUp)
		fmt.Fprintf(&b, "  dueTo=%s\n", strings.Join(r.DueTo, ","))
		for _, s := range r.Sources {
			fmt.Fprintf(&b, "  source=%s@%s level=%d bump=%s\n", s.Provider, s.Commit, s.Level, s.Bump)
		}
		for _, u := range r.Updates {
			fmt.Fprintf(&b, "  update=%s %s->%s\n", u.Name, u.From, u.To)
		}
	}
	diags := make([]string, 0, len(p.Diagnostics))
	for _, d := range p.Diagnostics {
		diags = append(diags, d.Code+" "+d.Pkg+" "+d.Message)
	}
	sort.Strings(diags)
	b.WriteString(strings.Join(diags, "\n"))
	return b.String()
}

// assertAdmissionMonotone checks the one thing that must hold on every
// history, overtaking or not: the delivery test only ever adds. A package the
// window-only rule released still releases, at a bump no lower, explained by a
// superset of the same providers and the same tuples.
func assertAdmissionMonotone(t *testing.T, round int, before, after *Plan) {
	t.Helper()
	for _, name := range before.Order {
		was, now := before.Releases[name], after.Releases[name]
		require.NotNil(t, now, "round %d: %s left the plan", round, name)
		if was.IsReleasing() {
			assert.True(t, now.IsReleasing(), "round %d: %s stopped releasing", round, name)
		}
		assert.LessOrEqual(t, ccme.MaxBump(was.Bump, now.Bump), now.Bump,
			"round %d: %s: the effective bump went down", round, name)
		for _, provider := range was.DueTo {
			assert.Contains(t, now.DueTo, provider,
				"round %d: %s: provider %s lost its attribution", round, name, provider)
		}
		tuples := make(map[string]bool, len(now.Sources))
		for _, s := range now.Sources {
			tuples[s.Provider+"@"+s.Commit] = true
		}
		for _, s := range was.Sources {
			assert.True(t, tuples[s.Provider+"@"+s.Commit],
				"round %d: %s: tuple %s@%s was dropped", round, name, s.Provider, s.Commit)
		}
	}
}

// TestAdmissionAsksOnlyTheSourcesWithinReach fences the agreement of §9.2 and
// §13.7b on one reaching set: a unit written over two providers at different
// distances from an overtaking consumer is owed only by the provider within
// the unit's depth. `theme` sits one edge from `ui` and two from `core`; a
// caret unit over both reaches `theme` through `ui` alone, so once `ui` has
// delivered the commit nothing is owed, however far behind `core` still is.
func TestAdmissionAsksOnlyTheSourcesWithinReach(t *testing.T) {
	libs := &model.Space{Name: "libs"}
	pkgs := []*model.Package{
		{Name: "core", Dir: "/r/core", Space: libs},
		{Name: "ui", Dir: "/r/ui", Space: libs},
		{Name: "theme", Dir: "/r/theme", Space: libs},
	}
	deps := []model.Dependency{{Consumer: "ui", Provider: "core"}, {Consumer: "theme", Provider: "ui"}}
	history := []commit{
		{sha: "c1", message: "chore: base"},
		{sha: "c2", message: "feat(core,ui)^: shared work"},
		{sha: "c3", message: "feat(theme): own work"},
	}
	plan := func(git *fakeGit) *Plan {
		p, err := Compute(context.Background(), git, Options{Packages: pkgs, Dependencies: deps, Root: "/r"})
		require.NoError(t, err)
		return p
	}

	// ui released the commit before theme's own release reached past it;
	// core never released it, and core is out of the unit's reach of theme.
	delivered := plan(newFakeGit(history...).
		tag("core", "1.0.0", "c1").tag("ui", "1.0.0", "c1").tag("ui", "1.1.0", "c2").tag("theme", "1.1.0", "c3"))
	assert.False(t, delivered.Releases["theme"].IsReleasing(),
		"ui delivered the commit and core, two edges away, is not a source that reaches theme")
	assert.True(t, delivered.Releases["core"].IsReleasing(), "core's own window still holds the commit")

	// The control: with ui's release of the commit absent, ui alone owes it.
	owed := plan(newFakeGit(history...).
		tag("core", "1.0.0", "c1").tag("ui", "1.0.0", "c1").tag("theme", "1.1.0", "c3"))
	theme := owed.Releases["theme"]
	require.True(t, theme.IsReleasing(), "ui has not delivered the commit to theme")
	// DueTo is the explanation chain (theme through ui through core); the
	// admitted unit's own attribution is the owed set, and it names ui alone.
	assert.Contains(t, theme.DueTo, "ui")
	require.Len(t, theme.Sources, 1, "one admitted contribution: %v", theme.Sources)
	assert.Equal(t, "ui", theme.Sources[0].Provider, "owed by ui alone, never by core two edges away")
	assert.Equal(t, "c2", theme.Sources[0].Commit)
}
