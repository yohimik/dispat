// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/ccme"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// The owed windows of §13.3: for every provider P and every consumer D that P
// reaches over the propagation kinds, the union also holds the commits after
// the newest release of P that D's baseline reaches. A consumer that released
// past a provider's commit and then sat out the provider's release has that
// commit in no ordinary window any more, so without these windows the unit is
// never parsed and the debt §13.4a still owes is invisible.
//
// Every fixture releases every package at or after the commit it is about.
// A package tagged before all history, or never released, widens the union to
// the whole history, which holds the commit anyway and hides the defect.

// owedHistory is vector 80d's shape: one commit carries the provider's caret
// and the consumer's own feature, the provider fails and the consumer
// proceeds, and utils releases its own fix there so that no ordinary window
// reaches back past the commit.
func owedHistory() []commit {
	return []commit{
		{sha: "c1", message: "chore: base"},
		{sha: "c2", message: "feat(core)^: streaming\n\n---\n\nfeat(app): own flag\n\n---\n\nfix(utils): tidy"},
		{sha: "c3", message: "chore(core): retry the provider"},
	}
}

// TestOwedWindowCatchesUpAConsumerThatSatOutItsProvider: core published c2 at
// c3 in a run app sat out. app's baseline (c2) reaches no release of core that
// carries c2, so app is still owed core's version and catches up; once app has
// released after core, nothing is owed and nothing is picked up twice.
func TestOwedWindowCatchesUpAConsumerThatSatOutItsProvider(t *testing.T) {
	git := newFakeGit(owedHistory()...).
		tag("core", "1.0.0", "c1").tag("utils", "1.0.0", "c1").tag("app", "1.0.0", "c1").
		tag("app", "1.1.0", "c2").tag("utils", "1.0.1", "c2").
		tag("core", "1.1.0", "c3")

	p := compute(t, git, nil)
	core, utils, app := p.Releases["core"], p.Releases["utils"], p.Releases["app"]
	assert.False(t, core.IsReleasing(), "core released everything it had")
	assert.False(t, utils.IsReleasing(), "utils released everything it had")
	require.True(t, app.IsReleasing(), "app is owed core's release of c2: %v", codes(p))
	assertVersion(t, v(1, 1, 1), app.Next)
	assert.Equal(t, ccme.BumpPatch, app.PropagatedBump)
	assert.True(t, app.CatchUp, "core is not in this plan, so the release is a catch-up")
	assert.True(t, hasCode(p, CodeCatchUp), "W193 explains the release: %v", codes(p))
	require.Len(t, app.Sources, 1)
	assert.Equal(t, StaleSource{Provider: "core", Commit: "c2", commitKey: "c2", Level: 1, Bump: ccme.BumpPatch},
		app.Sources[0])
	assert.Equal(t, []string{"core"}, app.DueTo)

	// app released the catch-up at c3, after core: its baseline now reaches
	// core's release carrying c2, so the owed window is an ordinary one and the
	// commit is delivered.
	settled := compute(t, git.tag("app", "1.1.1", "c3"), nil)
	for _, name := range []string{"core", "utils", "app"} {
		assert.False(t, settled.Releases[name].IsReleasing(), "%s: nothing is owed twice", name)
	}
}

// TestOwedWindowIsTheWholeHistoryForAConsumerOlderThanItsProvider: app
// released before core ever did, so app's baseline reaches no release of core
// at all and every commit core has released since is one app got ahead of.
func TestOwedWindowIsTheWholeHistoryForAConsumerOlderThanItsProvider(t *testing.T) {
	git := newFakeGit(
		commit{sha: "c1", message: "feat(app): first app\n\n---\n\nfeat(utils): first utils"},
		commit{sha: "c2", message: "feat(core)^: first core\n\n---\n\nfeat(app): own flag"},
		commit{sha: "c3", message: "chore(core): retry the provider"},
	).tag("app", "1.0.0", "c1").tag("utils", "1.0.0", "c1").tag("app", "1.1.0", "c2").
		tag("utils", "1.0.1", "c3").tag("core", "1.0.0", "c3")

	p := compute(t, git, nil)
	app := p.Releases["app"]
	require.True(t, app.IsReleasing(), "app is owed core's first release: %v", codes(p))
	assertVersion(t, v(1, 1, 1), app.Next)
	assert.True(t, app.CatchUp)
	require.Len(t, app.Sources, 1)
	assert.Equal(t, "core", app.Sources[0].Provider)
	assert.Equal(t, "c2", app.Sources[0].Commit)
}

// TestOwedWindowCatchesUpAConsumerThatProceededAtALaterCommit: app did not
// release at the provider's commit but two commits later, still before core
// published; the owed window starts at core's last release app reached.
func TestOwedWindowCatchesUpAConsumerThatProceededAtALaterCommit(t *testing.T) {
	git := newFakeGit(
		commit{sha: "c1", message: "chore: base"},
		commit{sha: "c2", message: "feat(core)^: streaming\n\n---\n\nfix(utils): tidy"},
		commit{sha: "c3", message: "feat(app): own flag"},
		commit{sha: "c4", message: "chore(core): retry the provider"},
	).tag("core", "1.0.0", "c1").tag("utils", "1.0.0", "c1").tag("app", "1.0.0", "c1").
		tag("utils", "1.0.1", "c2").tag("app", "1.1.0", "c3").tag("core", "1.1.0", "c4")

	p := compute(t, git, nil)
	app := p.Releases["app"]
	require.True(t, app.IsReleasing(), "app released past c2 before core published it: %v", codes(p))
	assertVersion(t, v(1, 1, 1), app.Next)
	assert.True(t, app.CatchUp)
	require.Len(t, app.Sources, 1)
	assert.Equal(t, "c2", app.Sources[0].Commit)
}

// TestOwedWindowPlansAFailedConsumerAtTheVersionFirstPlanned is G3 across the
// owed window: app proceeded at c2, the run that published core at c3 also
// planned app's catch-up and app failed there. The next plan owes app the same
// version that run planned, with or without a new commit on top.
func TestOwedWindowPlansAFailedConsumerAtTheVersionFirstPlanned(t *testing.T) {
	history := owedHistory()
	failedRun := compute(t, newFakeGit(history...).
		tag("core", "1.0.0", "c1").tag("utils", "1.0.0", "c1").tag("app", "1.0.0", "c1").
		tag("app", "1.1.0", "c2").tag("utils", "1.0.1", "c2"), nil)
	require.True(t, failedRun.Releases["core"].IsReleasing())
	planned := failedRun.Releases["app"]
	require.True(t, planned.IsReleasing(), "the run that publishes core plans app after it")
	assertVersion(t, v(1, 1, 1), planned.Next)

	for name, extra := range map[string][]commit{
		"at the same commit": nil,
		"after a new commit": {{sha: "c4", message: "docs: notes"}},
	} {
		t.Run(name, func(t *testing.T) {
			git := newFakeGit(append(history, extra...)...).
				tag("core", "1.0.0", "c1").tag("utils", "1.0.0", "c1").tag("app", "1.0.0", "c1").
				tag("app", "1.1.0", "c2").tag("utils", "1.0.1", "c2").tag("core", "1.1.0", "c3")
			p := compute(t, git, nil)
			app := p.Releases["app"]
			require.True(t, app.IsReleasing(), "app failed after core published: %v", codes(p))
			assertVersion(t, planned.Next, app.Next, "the version the failed run planned")
			assert.True(t, app.CatchUp)
		})
	}
}

// TestOwedWindowIsTakenPerReachablePair: core's `^^` reaches theme two edges
// away. ui caught up in the run that published core, theme sat it out. Neither
// edge's window holds c2 (ui's baseline reaches core's release, and theme's
// reaches ui's), so only the pair core → theme keeps theme's debt visible.
func TestOwedWindowIsTakenPerReachablePair(t *testing.T) {
	libs := &model.Space{Name: "libs"}
	pkgs := []*model.Package{
		{Name: "core", Dir: "/r/core", Space: libs},
		{Name: "ui", Dir: "/r/ui", Space: libs},
		{Name: "theme", Dir: "/r/theme", Space: libs},
	}
	deps := []model.Dependency{{Consumer: "ui", Provider: "core"}, {Consumer: "theme", Provider: "ui"}}
	git := newFakeGit(
		commit{sha: "c1", message: "chore: base"},
		commit{sha: "c2", message: "feat(core)^^: x\n\n---\n\nfeat(ui): own\n\n---\n\nfeat(theme): own"},
		commit{sha: "c3", message: "chore(core): retry the provider"},
	).tag("core", "1.0.0", "c1").tag("ui", "1.0.0", "c1").tag("theme", "1.0.0", "c1").
		tag("ui", "1.1.0", "c2").tag("theme", "1.1.0", "c2").
		tag("core", "1.1.0", "c3").tag("ui", "1.1.1", "c3")

	p, err := Compute(context.Background(), git, Options{Packages: pkgs, Dependencies: deps, Root: "/r"})
	require.NoError(t, err)
	assert.False(t, p.Releases["ui"].IsReleasing(), "ui caught up after core")
	theme := p.Releases["theme"]
	require.True(t, theme.IsReleasing(), "core still owes theme c2: %v", codes(p))
	assertVersion(t, v(1, 1, 1), theme.Next)
	require.Len(t, theme.Sources, 1)
	assert.Equal(t, StaleSource{Provider: "core", Commit: "c2", commitKey: "c2", Level: 2, Bump: ccme.BumpPatch},
		theme.Sources[0])
}

// TestOwedWindowCatchesUpAFixedGroupMemberThatGotAhead: core published c2 in
// a run app sat out, and app shares a fixed version with tool. The owed window
// makes the debt visible, and the group moves as one to deliver it
// (TestFixedGroupMovesAsOneWhenAMemberGotAheadOfItsProvider is the same-run
// half of the seam).
func TestOwedWindowCatchesUpAFixedGroupMemberThatGotAhead(t *testing.T) {
	p := planGroupMemberAhead(t, aheadGroupMember(newFakeGit(groupMemberAheadHistory()...)).tag("core", "1.1.0", "c3"))
	app, tool := p.Releases["app"], p.Releases["tool"]
	require.True(t, app.IsReleasing(), "app is owed core's c2: %v", codes(p))
	assertVersion(t, v(1, 1, 1), app.Next)
	assert.True(t, app.CatchUp)
	assert.True(t, tool.IsReleasing(), "the group moves as one")
	assertVersion(t, v(1, 1, 1), tool.Next)
}

// TestOwedWindowReadsNoHistoryWhenNothingIsOwed is the cost side of §13.3: a
// window is read only for a pair whose provider release lies behind the union.
// Consumers released together with their providers, or got ahead of a
// provider while another package's window already spans the commit, cost no
// history read beyond the ordinary one per distinct boundary.
func TestOwedWindowReadsNoHistoryWhenNothingIsOwed(t *testing.T) {
	for name, tc := range map[string]struct {
		git  *fakeGit
		want map[string]int
	}{
		"every consumer released with its provider": {
			git: newFakeGit(
				commit{sha: "c1", message: "chore: base"},
				commit{sha: "c2", message: "feat(core)^: streaming"},
				commit{sha: "c3", message: "fix(app): own fix"},
			).tag("core", "1.0.0", "c1").tag("utils", "1.0.0", "c1").tag("app", "1.0.0", "c1").
				tag("core", "1.1.0", "c2").tag("app", "1.0.1", "c2"),
			want: map[string]int{"core@1.1.0": 1, "utils@1.0.0": 1},
		},
		"a consumer ahead of a provider whose release the union already spans": {
			git: newFakeGit(
				commit{sha: "c0", message: "chore: base"},
				commit{sha: "c1", message: "fix(core): first"},
				commit{sha: "c2", message: "feat(core)^: streaming\n\n---\n\nfeat(app): own flag"},
				commit{sha: "c3", message: "chore(core): retry the provider"},
			).tag("utils", "1.0.0", "c0").tag("app", "1.0.0", "c0").tag("core", "1.0.0", "c1").
				tag("app", "1.1.0", "c2").tag("core", "1.1.0", "c3"),
			want: map[string]int{"utils@1.0.0": 1, "app@1.1.0": 1, "core@1.1.0": 1},
		},
	} {
		t.Run(name, func(t *testing.T) {
			git := counted(tc.git)
			pkgs, deps := testPackages()
			stats := &HistoryStats{}
			p, err := Compute(context.Background(), git, Options{Packages: pkgs, Dependencies: deps, Root: "/r",
				HistoryStats: stats})
			require.NoError(t, err)
			assert.Equal(t, tc.want, git.logQueries, "one read per ordinary boundary and none besides")
			assert.EqualValues(t, len(tc.want), stats.CommitWindows.Load())
			if app := p.Releases["app"]; app.Baseline.Minor == 1 {
				assert.True(t, app.CatchUp, "the debt is still found, through the ordinary union")
			}
		})
	}
}

// TestOwedWindowsPlanAsTheWholeHistoryDoes is the equivalence §13.3 promises:
// a plan read through the ordinary and owed windows is the plan read through
// the whole history. Each case plans a generated workspace twice, the second
// time beside a phantom package that never released, whose window is the whole
// history and so widens the union to all of it; every real package must come
// out the same. Seeded, so a failure is reproducible from its case index, and
// counted, so the owed windows demonstrably mattered in enough cases rather
// than the ordinary union having held everything by chance.
func TestOwedWindowsPlanAsTheWholeHistoryDoes(t *testing.T) {
	const cases = 400
	rng := rand.New(rand.NewSource(20260924))
	behindEveryWindow, compared := 0, 0
	for round := 0; round < cases; round++ {
		git, pkgs, deps := randomOwedHistory(rng)
		space := pkgs[0].Space
		narrow, err := Compute(context.Background(), git, Options{Packages: pkgs, Dependencies: deps, Root: "/r"})
		require.NoError(t, err)
		phantom := &model.Package{Name: "phantom", Dir: "/r/phantom", Space: space}
		whole, err := Compute(context.Background(), git, Options{
			Packages: append(append([]*model.Package{}, pkgs...), phantom), Dependencies: deps, Root: "/r"})
		require.NoError(t, err)
		if narrow.IsInvalid() || whole.IsInvalid() {
			continue // a generated history can land on a version guard; both plans report it
		}
		compared++
		oldest := oldestOrdinaryBoundary(git, pkgs)
		isBehind := false
		for _, p := range pkgs {
			want, got := owedFingerprint(whole.Releases[p.Name]), owedFingerprint(narrow.Releases[p.Name])
			require.Equal(t, want, got, "round %d: %s", round, p.Name)
			for _, s := range whole.Releases[p.Name].Sources {
				if git.index(s.Commit) <= oldest {
					isBehind = true
				}
			}
		}
		if isBehind {
			behindEveryWindow++
		}
	}
	assert.Greater(t, compared, cases/2, "most generated histories are plannable")
	assert.GreaterOrEqual(t, behindEveryWindow, 20,
		"cases whose whole-history plan owes a commit outside every ordinary window")
	t.Logf("compared %d, owed a commit behind every ordinary window in %d", compared, behindEveryWindow)
}

// randomOwedHistory generates a workspace where consumers get ahead of their
// providers: a random acyclic graph, a stream of caret units, and one to three
// releases per package at random positions in a linear history, versions rising
// with position. Every package releases at a real commit, so the union is never
// the whole history by construction.
func randomOwedHistory(rng *rand.Rand) (*fakeGit, []*model.Package, []model.Dependency) {
	n := 3 + rng.Intn(3)
	names := make([]string, n)
	for i := range names {
		names[i] = fmt.Sprintf("p%d", i)
	}
	space := &model.Space{Name: "s"}
	pkgs := make([]*model.Package, n)
	for i, name := range names {
		pkgs[i] = &model.Package{Name: name, Dir: "/r/" + name, Space: space}
	}
	order := rng.Perm(n)
	var deps []model.Dependency
	for i, from := range order {
		for _, to := range order[i+1:] {
			if rng.Intn(2) == 0 {
				deps = append(deps, model.Dependency{Consumer: names[to], Provider: names[from]})
			}
		}
	}
	commits := 4 + rng.Intn(8)
	history := make([]commit, commits)
	for i := range history {
		message := randomOwedMessage(rng, names)
		history[i] = commit{sha: fmt.Sprintf("c%d", i+1), message: message}
	}
	git := newFakeGit(history...)
	for _, name := range names {
		positions := rng.Perm(commits)[:1+rng.Intn(3)]
		sort.Ints(positions)
		for k, at := range positions {
			git.tag(name, fmt.Sprintf("1.%d.0", k), history[at].sha)
		}
	}
	return git, pkgs, deps
}

// randomOwedMessage writes one commit: mostly a unit that propagates, over one
// package or two, sometimes a unit that does not, a hold or a cancel, and now
// and then a message that is no record at all.
func randomOwedMessage(rng *rand.Rand, names []string) string {
	name := names[rng.Intn(len(names))]
	switch rng.Intn(10) {
	case 0:
		return "docs: notes"
	case 1:
		return fmt.Sprintf("feat(%s,%s)^: shared work", name, names[rng.Intn(len(names))])
	case 2:
		return fmt.Sprintf("chore(%s): housekeeping\n\nRelease-As: none", name)
	case 3:
		return fmt.Sprintf("cancel(%s): reset release state", name)
	case 4:
		return fmt.Sprintf("feat(%s)^: work\n\n---\n\nfeat(%s): own work", name, names[rng.Intn(len(names))])
	}
	kind := []string{"feat", "fix", "perf"}[rng.Intn(3)]
	knob := []string{"", "^", "^^", "^++*", "^++2"}[rng.Intn(5)]
	return fmt.Sprintf("%s(%s)%s: work", kind, name, knob)
}

// oldestOrdinaryBoundary is the history index of the oldest stable release of
// the workspace: every commit at or behind it is in no package's window.
func oldestOrdinaryBoundary(git *fakeGit, pkgs []*model.Package) int {
	oldest := len(git.history)
	for _, p := range pkgs {
		stable := -1
		for _, tag := range git.tagsFor[p.Name] {
			stable = max(stable, git.index(git.tags[tag]))
		}
		oldest = min(oldest, stable)
	}
	return oldest
}

// owedFingerprint renders what the bump axis decided for one package, its
// contributions as a sorted set: the two plans rank their unions differently.
func owedFingerprint(r *Release) string {
	sources := make([]string, 0, len(r.Sources))
	for _, s := range r.Sources {
		sources = append(sources, fmt.Sprintf("%s@%s/%d/%s", s.Provider, s.Commit, s.Level, s.Bump))
	}
	sort.Strings(sources)
	return fmt.Sprintf("releasing=%t next=%s bump=%s propagated=%s catchup=%t dueTo=%s sources=%s",
		r.IsReleasing(), r.Next, r.Bump, r.PropagatedBump, r.CatchUp,
		strings.Join(r.DueTo, ","), strings.Join(sources, " "))
}

// TestOwedWindowsOverMergesPlanAsTheWholeHistoryDoes is the same equivalence
// over real repositories with side branches and merges, planned through Git's
// union read and the marker index as well as a read per boundary, where
// "behind the union" and "reached by a baseline" are questions about a graph
// rather than positions on a line.
func TestOwedWindowsOverMergesPlanAsTheWholeHistoryDoes(t *testing.T) {
	if testing.Short() {
		t.Skip("builds real repositories")
	}
	ctx := context.Background()
	behindEveryWindow := 0
	for seed := int64(1); seed <= 12; seed++ {
		git, pkgs, deps := randomOwedRepository(t, seed)
		for _, isUnionRead := range []bool{true, false} {
			var history TagInventoryGitx = git
			if !isUnionRead {
				history = perBoundaryGit{inner: git}
			}
			narrow, err := Compute(ctx, history, Options{Packages: pkgs, Dependencies: deps, Root: git.Dir})
			require.NoError(t, err)
			phantom := &model.Package{Name: "phantom", Dir: git.Dir + "/phantom"}
			whole, err := Compute(ctx, history, Options{Packages: append(append([]*model.Package{}, pkgs...), phantom),
				Dependencies: deps, Root: git.Dir})
			require.NoError(t, err)
			require.Falsef(t, narrow.IsInvalid() || whole.IsInvalid(), "seed %d: %v", seed, codes(narrow))
			for _, p := range pkgs {
				require.Equalf(t, owedFingerprint(whole.Releases[p.Name]), owedFingerprint(narrow.Releases[p.Name]),
					"seed %d (union read %t): %s", seed, isUnionRead, p.Name)
			}
			if isUnionRead && isOwingOutsideTheUnion(t, git, pkgs, whole) {
				behindEveryWindow++
			}
		}
	}
	assert.GreaterOrEqual(t, behindEveryWindow, 3,
		"seeds whose whole-history plan owes a commit outside every ordinary window")
	t.Logf("owed a commit behind every ordinary window in %d seeds", behindEveryWindow)
}

// randomOwedRepository grows a history with side branches and merges whose
// early commits carry propagating units, and releases every package one to
// three times in its later part, so the ordinary union leaves the early units
// out and consumers get ahead of their providers across branches.
func randomOwedRepository(t *testing.T, seed int64) (*gitx.LocalGitx, []*model.Package, []model.Dependency) {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	root := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
		return strings.TrimSpace(string(out))
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")

	names := []string{"a", "b", "c", "d"}
	messages := []string{
		"feat(a)^: reaches the consumers", "fix(a)^^: reaches two levels", "feat(b)^: reaches d",
		"feat(a)^: provider work\n\n---\n\nfeat(b): own work", "feat(c): own work", "fix(d): own work",
		"docs: notes", "cancel(b): reset release state",
	}
	serial := 0
	commit := func(isEarly bool) {
		serial++
		message := messages[rng.Intn(len(messages))]
		if isEarly && rng.Intn(3) > 0 {
			message = messages[rng.Intn(4)]
		}
		pkg := names[rng.Intn(len(names))]
		dir := filepath.Join(root, "pkgs", pkg)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%d.txt", serial)), []byte("x"), 0o644))
		run("add", ".")
		run("commit", "-qm", message)
	}
	commit(true)
	branches := []string{"main"}
	for step := 0; step < 22; step++ {
		isEarly := step < 10
		switch r := rng.Intn(10); {
		case r < 6:
			run("checkout", "-q", branches[rng.Intn(len(branches))])
			commit(isEarly)
		case r < 8:
			name := fmt.Sprintf("side%d", step)
			run("checkout", "-q", "-b", name)
			branches = append(branches, name)
			commit(isEarly)
		default:
			run("checkout", "-q", "main")
			if other := branches[rng.Intn(len(branches))]; other != "main" {
				run("merge", "-q", "--no-ff", "-m", "chore: merge "+other, other)
			}
		}
	}
	run("checkout", "-q", "main")
	for _, b := range branches[1:] {
		run("merge", "-q", "--no-ff", "-m", "chore: merge "+b, b)
	}
	commit(false)

	reachable := strings.Fields(run("rev-list", "--reverse", "--topo-order", "HEAD"))
	late := reachable[len(reachable)/3:]
	for _, pkg := range names {
		positions := rng.Perm(len(late))[:1+rng.Intn(3)]
		sort.Ints(positions)
		for k, at := range positions {
			run("tag", fmt.Sprintf("%s@1.%d.0", pkg, k), late[at])
		}
	}
	pkgs := make([]*model.Package, len(names))
	for i, name := range names {
		pkgs[i] = &model.Package{Name: name, Dir: filepath.Join(root, "pkgs", name)}
	}
	deps := []model.Dependency{
		{Consumer: "b", Provider: "a"}, {Consumer: "c", Provider: "a"}, {Consumer: "d", Provider: "b"},
	}
	return &gitx.LocalGitx{Dir: root}, pkgs, deps
}

// isOwingOutsideTheUnion reports whether the plan owes some package a commit
// that no package's ordinary window holds: one reachable from every package's
// stable release.
func isOwingOutsideTheUnion(t *testing.T, git *gitx.LocalGitx, pkgs []*model.Package, pl *Plan) bool {
	t.Helper()
	union := make(map[string]bool)
	for _, p := range pkgs {
		tags, err := git.Tags(context.Background(), p.Name, gitx.DefaultTagFormat)
		require.NoError(t, err)
		stable, _ := tags.StableBaseline()
		window, err := git.Commits(context.Background(), stable.Name)
		require.NoError(t, err)
		for _, c := range window {
			union[c.SHA] = true
		}
	}
	for _, p := range pkgs {
		for _, s := range pl.Releases[p.Name].Sources {
			if !union[s.Commit] {
				return true
			}
		}
	}
	return false
}
