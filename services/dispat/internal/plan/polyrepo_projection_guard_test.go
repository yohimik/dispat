// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/ccme"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

type projectionGuardControlGit struct {
	*composedControlGit
	links map[string]string
}

func (g *projectionGuardControlGit) HeadSHA(context.Context) (string, error) { return "c", nil }
func (g *projectionGuardControlGit) GitlinksAt(context.Context, string) (map[string]string, error) {
	return g.links, nil
}

func projectionGuardComputation(activeHead, pin string, bump ccme.Bump, target string) *computation {
	source := newFakeGit(commit{sha: "a"}, commit{sha: "b"})
	cp := &computation{
		ctx: context.Background(), controlRepo: "control",
		histories: map[string]RepositoryHistory{
			"control": {Name: "control", Control: true},
			"source":  {Name: "source", Path: "sources/source", Git: source},
		},
		repositoryHeads:  map[string]string{"source": activeHead},
		controlPathIndex: map[string]int{"sources/source": 0}, controlPathCount: 1,
		controlStates: map[string]*controlGitlinkState{}, ancNoGit: map[string]bool{},
		byName: map[string]*model.Package{
			"app":          {Name: "app", Repository: "source"},
			"control-tool": {Name: "control-tool", Repository: "control"},
		},
	}
	links := updatePersistentLink(nil, 0, 1, 0, pin)
	cp.controlStates["c"] = &controlGitlinkState{links: links}
	cp.commits = []*commitRec{{
		commit: gitx.Commit{SHA: "c"}, key: historyKey("control", "c"), repository: "control",
		units: []*ccme.Unit{{Bump: bump, Valid: true}},
		scope: []map[string]bool{{target: true}},
	}}
	return cp
}

func TestControlProjectionGuardRejectsOnlyAffectedFutureIntent(t *testing.T) {
	t.Run("future pin targeting active source", func(t *testing.T) {
		cp := projectionGuardComputation("a", "b", ccme.BumpPatch, "app")
		err := cp.validateControlProjectionHeads()
		require.ErrorIs(t, err, errFatalPlan)
		require.Len(t, cp.diags, 1)
		assert.Equal(t, CodeRepositoryBoundary, cp.diags[0].Code)
		assert.Contains(t, cp.diags[0].Message, "active revision a")
		assert.Contains(t, cp.diags[0].Message, "pins b")
	})

	for name, cp := range map[string]*computation{
		"source contains pin":       projectionGuardComputation("b", "a", ccme.BumpPatch, "app"),
		"unrelated control package": projectionGuardComputation("a", "b", ccme.BumpPatch, "control-tool"),
		"inert source unit":         projectionGuardComputation("a", "b", ccme.BumpNone, "app"),
	} {
		t.Run(name, func(t *testing.T) {
			require.NoError(t, cp.validateControlProjectionHeads())
			assert.Empty(t, cp.diags)
		})
	}
}

func TestComputeRejectsControlIntentProjectedOntoOlderSourceCheckout(t *testing.T) {
	source := &failingRepositoryHistoryGit{
		fakeGit: newFakeGit(commit{sha: "a"}, commit{sha: "b"}),
		head:    "a",
	}
	control := &projectionGuardControlGit{
		composedControlGit: &composedControlGit{
			fakeGit: newFakeGit(commit{sha: "c", message: "fix(app): future control intent"}),
			control: []gitx.ControlHistoryCommit{{
				SHA: "c", Message: "fix(app): future control intent",
				Gitlinks: map[string]gitx.GitlinkTransition{
					"sources/source": {To: "b"},
				},
			}},
		},
		links: map[string]string{"sources/source": "a"},
	}
	space := &model.Space{Name: "packages"}
	pl, err := Compute(t.Context(), control, Options{
		Packages: []*model.Package{{
			Name: "app", Dir: "/w/source/app", RepoRoot: "/w/source", Repository: "source", Space: space,
		}},
		Initials: map[string]ccme.Version{"app": v(1, 0, 0)},
		Repositories: map[string]RepositoryHistory{
			"control": {Name: "control", Root: "/w", Git: control, Control: true},
			"source":  {Name: "source", Root: "/w/source", Path: "sources/source", Git: source},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, pl)
	assert.True(t, pl.IsFatal(), "%v", pl.Diagnostics)
	assert.True(t, hasCode(pl, CodeRepositoryBoundary), "%v", pl.Diagnostics)
}

// TestControlProjectionGuardSkipsCommitsOutsideTheAffectedPackageWindow is the
// F2 regression. cp.commits is the union of every package's window, so a
// control commit one package discharged long ago is still in the list while
// another package's window holds it. Every pass that applies control intent
// admits a (commit, package) pair only while the package's own window holds
// it and its baseline does not already contain it; a guard reading the union
// instead made such a plan fatal over intent it cannot apply.
func TestControlProjectionGuardSkipsCommitsOutsideTheAffectedPackageWindow(t *testing.T) {
	key := historyKey("control", "c")

	t.Run("commit left the package window", func(t *testing.T) {
		cp := projectionGuardComputation("a", "b", ccme.BumpPatch, "app")
		cp.stableBoundaries = map[string]map[string]string{"app": {"control": key}}
		require.False(t, cp.inWindow("app", key), "precondition: the pair is no longer admitted")

		require.NoError(t, cp.validateControlProjectionHeads())
		assert.Empty(t, cp.diags)
	})

	t.Run("commit already published by the train baseline", func(t *testing.T) {
		cp := projectionGuardComputation("a", "b", ccme.BumpPatch, "app")
		cp.publishedBoundaries = map[string]map[string]string{"app": {"control": key}}
		require.True(t, cp.inWindow("app", key), "precondition: still in the window")
		require.True(t, cp.containedInBaseline("app", key), "precondition: already discharged")

		require.NoError(t, cp.validateControlProjectionHeads())
		assert.Empty(t, cp.diags)
	})

	t.Run("still fatal while the pair is admitted", func(t *testing.T) {
		cp := projectionGuardComputation("a", "b", ccme.BumpPatch, "app")
		require.True(t, cp.inWindow("app", key))
		require.False(t, cp.containedInBaseline("app", key))

		require.ErrorIs(t, cp.validateControlProjectionHeads(), errFatalPlan)
		require.Len(t, cp.diags, 1)
		assert.Equal(t, CodeRepositoryBoundary, cp.diags[0].Code)
	})
}

// TestIsControlUnitAffectingRelease pins the F3 decision. A bumpless
// `Release-As: none` can only withhold a release, so it never needs the source
// to carry the control snapshot's pin; every other directive either creates a
// release or decides the version or channel one is published under.
func TestIsControlUnitAffectingRelease(t *testing.T) {
	unit := func(bump ccme.Bump, directives ccme.Directives) *ccme.Unit {
		return &ccme.Unit{Bump: bump, Valid: true, Directives: directives}
	}
	for name, tc := range map[string]struct {
		unit *ccme.Unit
		want bool
	}{
		"nil":                {nil, false},
		"inert":              {unit(ccme.BumpNone, ccme.Directives{}), false},
		"bump":               {unit(ccme.BumpPatch, ccme.Directives{}), true},
		"bumpless hold":      {unit(ccme.BumpNone, ccme.Directives{ReleaseAs: &ccme.ReleaseAs{Kind: ccme.ReleaseAsNone, Raw: "none"}}), false},
		"hold beside a bump": {unit(ccme.BumpPatch, ccme.Directives{ReleaseAs: &ccme.ReleaseAs{Kind: ccme.ReleaseAsNone, Raw: "none"}}), true},
		"lifted hold":        {unit(ccme.BumpNone, ccme.Directives{ReleaseAs: &ccme.ReleaseAs{Kind: ccme.ReleaseAsAuto, Raw: "auto"}}), true},
		"exact pin":          {unit(ccme.BumpNone, ccme.Directives{ReleaseAs: &ccme.ReleaseAs{Kind: ccme.ReleaseAsExact, Version: v(2, 0, 0), Raw: "2.0.0"}}), true},
		"bumpless channel":   {unit(ccme.BumpNone, ccme.Directives{ChannelSet: true}), true},
		"bumpless edit":      {unit(ccme.BumpNone, ccme.Directives{Edits: []ccme.CorrectionTarget{{}}}), true},
		"bumpless delete":    {unit(ccme.BumpNone, ccme.Directives{Deletes: []ccme.CorrectionTarget{{}}}), true},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, IsControlUnitAffectingRelease(tc.unit))
		})
	}
}

// TestControlProjectionGuardIgnoresABumplessHold runs the same decision
// through the guard, so the predicate and the fatal path cannot drift apart.
func TestControlProjectionGuardIgnoresABumplessHold(t *testing.T) {
	guard := func(directives ccme.Directives) *computation {
		cp := projectionGuardComputation("a", "b", ccme.BumpNone, "app")
		cp.commits[0].units[0].Directives = directives
		return cp
	}

	t.Run("hold", func(t *testing.T) {
		cp := guard(ccme.Directives{ReleaseAs: &ccme.ReleaseAs{Kind: ccme.ReleaseAsNone, Raw: "none"}})
		require.NoError(t, cp.validateControlProjectionHeads())
		assert.Empty(t, cp.diags)
	})

	for name, directives := range map[string]ccme.Directives{
		"lifted hold": {ReleaseAs: &ccme.ReleaseAs{Kind: ccme.ReleaseAsAuto, Raw: "auto"}},
		"exact pin":   {ReleaseAs: &ccme.ReleaseAs{Kind: ccme.ReleaseAsExact, Version: v(2, 0, 0), Raw: "2.0.0"}},
		"channel":     {ChannelSet: true},
	} {
		t.Run(name, func(t *testing.T) {
			cp := guard(directives)
			require.ErrorIs(t, cp.validateControlProjectionHeads(), errFatalPlan)
			require.Len(t, cp.diags, 1)
			assert.Equal(t, CodeRepositoryBoundary, cp.diags[0].Code)
		})
	}
}

// projectionGuardRealRepository initializes a source repository with a main
// line of two commits and one sibling commit off the first, and returns their
// SHAs. The guard's question is about objects, so only a real repository can
// answer it: a double cannot be missing a commit the way a clone is.
func projectionGuardRealRepository(t *testing.T) (dir, base, head, sibling string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir = t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "main")
	git("config", "user.email", "test@example.com")
	git("config", "user.name", "Test")
	git("commit", "-q", "--allow-empty", "-m", "chore: base")
	base = git("rev-parse", "HEAD")
	git("checkout", "-q", "--detach", base)
	git("commit", "-q", "--allow-empty", "-m", "chore: sibling")
	sibling = git("rev-parse", "HEAD")
	git("checkout", "-q", "main")
	git("commit", "-q", "--allow-empty", "-m", "chore: head")
	head = git("rev-parse", "HEAD")
	return dir, base, head, sibling
}

// projectionGuardRealComputation is projectionGuardComputation with the real
// git of dir behind the source history.
func projectionGuardRealComputation(dir, activeHead, pin string) *computation {
	cp := projectionGuardComputation(activeHead, pin, ccme.BumpPatch, "app")
	cp.histories["source"] = RepositoryHistory{
		Name: "source", Root: dir, Path: "sources/source", Git: &gitx.LocalGitx{Dir: dir},
	}
	return cp
}

// TestControlProjectionGuardReportsAPinTheSourceCloneDoesNotHave is the F4
// regression. In the scenario the guard exists for, the control snapshot pins
// a source revision the local clone can simply not have; `merge-base
// --is-ancestor` calls an unknown commit a fatal error, which used to replace
// the whole diagnostic with a git exit status.
func TestControlProjectionGuardReportsAPinTheSourceCloneDoesNotHave(t *testing.T) {
	dir, base, head, sibling := projectionGuardRealRepository(t)
	const absent = "0123456789012345678901234567890123456789"

	t.Run("pin absent from the clone", func(t *testing.T) {
		cp := projectionGuardRealComputation(dir, head, absent)

		err := cp.validateControlProjectionHeads()
		require.ErrorIs(t, err, errFatalPlan)
		require.NoError(t, cp.ancestryFailed(), "a missing object is an answer, not a git failure")
		require.Len(t, cp.diags, 1)
		assert.Equal(t, CodeRepositoryBoundary, cp.diags[0].Code)
		assert.Contains(t, cp.diags[0].Message, "pins "+absent)
		assert.Contains(t, cp.diags[0].Message, "synchronize the source to include that pin")
	})

	t.Run("pin present but unreachable from the active head", func(t *testing.T) {
		cp := projectionGuardRealComputation(dir, head, sibling)

		require.ErrorIs(t, cp.validateControlProjectionHeads(), errFatalPlan)
		require.Len(t, cp.diags, 1)
		assert.Contains(t, cp.diags[0].Message, "pins "+sibling)
	})

	t.Run("pin contained in the active head", func(t *testing.T) {
		cp := projectionGuardRealComputation(dir, head, base)

		require.NoError(t, cp.validateControlProjectionHeads())
		assert.Empty(t, cp.diags)
	})
}

// TestControlProjectionGuardKeepsAnUnreadableSourceFatal is the other half of
// F4: only "this repository does not have that object" became an ordinary
// answer. A repository git cannot read at all still aborts the plan, because
// nothing it would say about containment could be trusted.
func TestControlProjectionGuardKeepsAnUnreadableSourceFatal(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	missing := filepath.Join(t.TempDir(), "never-cloned")
	cp := projectionGuardRealComputation(missing, "a", "b")

	err := cp.validateControlProjectionHeads()
	require.Error(t, err)
	assert.NotErrorIs(t, err, errFatalPlan)
	assert.Contains(t, err.Error(), "repository source")
	assert.Empty(t, cp.diags, "a repository that cannot be read proves nothing about projection")
}
