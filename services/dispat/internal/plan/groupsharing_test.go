// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// Versioning groups on a shared prerelease train: the resting-channel rule,
// the member target floor, and the three sharing axes.
//
// Every fixture here builds a group of three members over a linear history,
// because the shapes these tests are about — a rider that never left the
// train, a sparse member that never joined it, a leg that failed after a
// neighbour published — all need one member to be in a different position
// from the others.

// sharedGroup is a group's versioning rule as a fixture states it: the semver
// mode every member versions under.
type sharedGroup struct {
	mode model.Versioning
}

// pkgs builds a three-member group ("shared": a, b, d) under the rule, with a
// member optionally overridden to a mode of its own so a fixture can make one
// member sparse.
func (g sharedGroup) pkgs(overrides map[string]model.Versioning) []*model.Package {
	out := make([]*model.Package, 0, 3)
	for _, name := range []string{"a", "b", "d"} {
		mode := g.mode
		if override, ok := overrides[name]; ok {
			mode = override
		}
		out = append(out, &model.Package{Name: name, Dir: "/r/pkgs/" + name, Space: &model.Space{
			Name:       "shared",
			Versioning: mode,
		}})
	}
	return out
}

func (g sharedGroup) compute(t *testing.T, git *fakeGit, overrides map[string]model.Versioning) *Plan {
	t.Helper()
	p, err := Compute(context.Background(), git, Options{Packages: g.pkgs(overrides), Root: "/r"})
	require.NoError(t, err)
	return p
}

// assertNext asserts one member's planned version and whether it releases.
func assertNext(t *testing.T, p *Plan, name, want string, releasing bool) {
	t.Helper()
	rel := p.Releases[name]
	require.NotNil(t, rel, "package %s", name)
	assert.Equal(t, want, rel.Next.String(), "%s: version", name)
	assert.Equal(t, releasing, rel.IsReleasing(), "%s: releasing", name)
}

// assertQuiet asserts that no member of the group releases anything.
func assertQuiet(t *testing.T, p *Plan) {
	t.Helper()
	for _, name := range []string{"a", "b", "d"} {
		assert.False(t, p.Releases[name].IsReleasing(), "%s must not release", name)
	}
}

// findDiagnostic returns the first diagnostic with the code, raised against
// the package.
func findDiagnostic(p *Plan, code, pkg string) (Diagnostic, bool) {
	for _, d := range p.Diagnostics {
		if d.Code == code && d.Pkg == pkg {
			return d, true
		}
	}
	return Diagnostic{}, false
}

// ---------------------------------------------------------------------------
// X1: a resting channel is not a proposal
// ---------------------------------------------------------------------------

func TestRestingStableMemberDoesNotGraduateItsGroup(t *testing.T) {
	// a and d ride an rc train; b never joined it and rests on stable. b's
	// channel is where its own tags put it, not a request to end the train,
	// so a fix in d continues the train rather than graduating the group.
	for _, mode := range []model.Versioning{model.VersioningFixedMajorMinor, model.VersioningFixed} {
		t.Run(string(mode), func(t *testing.T) {
			git := newFakeGit(
				commit{sha: "c1", message: "feat(d)%rc: start the train\n\n---\n\nrelease(a)%rc: enter"},
				commit{sha: "c2", message: "fix(d): more train work"},
			).tag("a", "1.10.3", "").tag("b", "1.10.0", "").tag("d", "1.10.0", "").
				tag("a", "1.11.0-rc.0", "c1").tag("d", "1.11.0-rc.0", "c1")

			p := sharedGroup{mode: mode}.compute(t, git, map[string]model.Versioning{
				"b": model.VersioningFixedMajorMinorSparse,
			})

			assertNext(t, p, "d", "1.11.0-rc.1", true)
			assertNext(t, p, "a", "1.11.0-rc.1", true)
			assert.True(t, p.Releases["a"].FixedRide, "a has no changes of its own and rides")
			assert.False(t, p.Releases["b"].IsReleasing(), "the sparse member stays where it is")
			assert.Equal(t, "1.10.0", p.Releases["b"].Next.String())
		})
	}
}

func TestRestingPrereleaseMemberDoesNotUngraduateItsGroup(t *testing.T) {
	// The mirror image: a and d graduated, b's leg failed and left it on the
	// train. b is caught up to the group's published version rather than
	// dragging a and d back onto a channel neither of them asked for.
	git := newFakeGit(
		commit{sha: "c1", message: "feat(d)%rc: start the train\n\n---\n\nrelease(a,b)%rc: enter"},
		commit{sha: "c2", message: "release(a,d)%rc>stable: graduate"},
	).tag("a", "1.10.0", "").tag("b", "1.10.0", "").tag("d", "1.10.0", "").
		tag("a", "1.11.0-rc.0", "c1").tag("b", "1.11.0-rc.0", "c1").tag("d", "1.11.0-rc.0", "c1").
		tag("a", "1.11.0", "c2").tag("d", "1.11.0", "c2")

	p := sharedGroup{mode: model.VersioningFixed}.compute(t, git, nil)

	assertNext(t, p, "b", "1.11.0", true)
	assert.True(t, p.Releases["b"].FixedRide, "b catches up to the group's published version")
	assert.False(t, p.Releases["a"].IsReleasing(), "a published already and has nothing to add")
	assert.False(t, p.Releases["d"].IsReleasing())
	assert.False(t, hasCode(p, CodeChannelEntryPatch),
		"the group is not entering a channel, so no channel-entry patch applies")
}

func TestNewcomerWithoutABaselineProposesNoChannel(t *testing.T) {
	// A member with no tag at all has no baseline channel to rest on, so it
	// must not be read as proposing stable against a group mid-train.
	git := newFakeGit(
		commit{sha: "c1", message: "feat(d)%rc: start the train\n\n---\n\nrelease(a)%rc: enter"},
		commit{sha: "c2", message: "fix(d): more train work"},
	).tag("a", "1.10.3", "").tag("d", "1.10.0", "").
		tag("a", "1.11.0-rc.0", "c1").tag("d", "1.11.0-rc.0", "c1")

	p := sharedGroup{mode: model.VersioningFixedMajorMinor}.compute(t, git, nil)

	assertNext(t, p, "d", "1.11.0-rc.1", true)
	assertNext(t, p, "b", "1.11.0-rc.1", true)
	assert.True(t, p.Releases["b"].FixedRide, "the newcomer joins the train it is riding")
}
