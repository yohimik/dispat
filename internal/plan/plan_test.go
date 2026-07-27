package plan

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/monorel/internal/gitx"
	"github.com/yohimik/monorel/internal/model"
	"github.com/yohimik/monorel/internal/semver"
)

// fakeGit serves canned tags and history. logs is keyed by the sinceTag
// argument ("" for full history).
type fakeGit struct {
	tags map[string]gitx.Tag
	logs map[string][]string
}

func (f *fakeGit) LatestTag(_ context.Context, pkg string) (gitx.Tag, bool, error) {
	t, ok := f.tags[pkg]
	return t, ok, nil
}

func (f *fakeGit) Subjects(_ context.Context, sinceTag string) ([]string, error) {
	return f.logs[sinceTag], nil
}

func (f *fakeGit) CreateTag(context.Context, string, string) error { return nil }

func testPackages() ([]*model.Package, []model.Dependency) {
	libs := &model.Space{Name: "libs", BuildWaitsPublish: true}
	apps := &model.Space{Name: "apps"}
	pkgs := []*model.Package{
		{Name: "core", Dir: "/r/libs/core", Space: libs},
		{Name: "utils", Dir: "/r/libs/utils", Space: libs},
		{Name: "app", Dir: "/r/apps/app", Space: apps},
	}
	deps := []model.Dependency{
		{Consumer: "app", Provider: "core"},
		{Consumer: "app", Provider: "utils"},
	}
	return pkgs, deps
}

func TestComputePropagation(t *testing.T) {
	pkgs, deps := testPackages()
	git := &fakeGit{
		tags: map[string]gitx.Tag{
			"core": {Name: "core@1.2.3", Version: semver.Version{Major: 1, Minor: 2, Patch: 3}},
			"app":  {Name: "app@0.1.0", Version: semver.Version{Minor: 1}},
		},
		logs: map[string][]string{
			// core: one feature since its tag -> minor.
			"core@1.2.3": {"feat(core): add streaming", "chore: cleanup"},
			// app history since its tag has no own conventional commits.
			"app@0.1.0": {"feat(core): add streaming", "docs: readme"},
			// utils was never tagged and never mentioned -> unchanged.
			"": {"feat(core): add streaming", "chore: cleanup"},
		},
	}

	p, err := Compute(context.Background(), git, pkgs, deps)
	require.NoError(t, err)

	core := p.Releases["core"]
	assert.Equal(t, semver.BumpMinor, core.Bump)
	assert.Equal(t, semver.Version{Major: 1, Minor: 3}, core.Next, "core -> 1.3.0")

	app := p.Releases["app"]
	assert.Equal(t, semver.BumpPatch, app.Bump, "consumer of changed core gets a patch")
	assert.Equal(t, semver.BumpNone, app.OwnBump)
	assert.Equal(t, semver.Version{Minor: 1, Patch: 1}, app.Next, "app -> 0.1.1")
	assert.Equal(t, []string{"core"}, app.DueTo)

	utils := p.Releases["utils"]
	assert.False(t, utils.Changed(), "utils must be unchanged")

	// Providers come before consumers.
	pos := map[string]int{}
	for i, n := range p.Order {
		pos[n] = i
	}
	assert.Less(t, pos["core"], pos["app"])
	assert.Less(t, pos["utils"], pos["app"])
}

func TestComputeOwnBumpWins(t *testing.T) {
	pkgs, deps := testPackages()
	git := &fakeGit{
		tags: map[string]gitx.Tag{
			"core": {Name: "core@1.0.0", Version: semver.Version{Major: 1}},
			"app":  {Name: "app@2.0.0", Version: semver.Version{Major: 2}},
		},
		logs: map[string][]string{
			"core@1.0.0": {"fix(core): edge case"},
			// app has its own feature: minor beats the propagated patch.
			"app@2.0.0": {"feat(app): new screen", "fix(core): edge case"},
			"":          {},
		},
	}
	p, err := Compute(context.Background(), git, pkgs, deps)
	require.NoError(t, err)

	app := p.Releases["app"]
	assert.Equal(t, semver.BumpMinor, app.Bump, "own feat wins over propagated patch")
	assert.Equal(t, semver.Version{Major: 2, Minor: 1}, app.Next, "app -> 2.1.0")
}

func TestComputeSinglePatchForMultipleProviders(t *testing.T) {
	pkgs, deps := testPackages()
	git := &fakeGit{
		tags: map[string]gitx.Tag{
			"core":  {Name: "core@1.0.0", Version: semver.Version{Major: 1}},
			"utils": {Name: "utils@1.0.0", Version: semver.Version{Major: 1}},
			"app":   {Name: "app@1.0.0", Version: semver.Version{Major: 1}},
		},
		logs: map[string][]string{
			"core@1.0.0":  {"fix(core): a", "fix(utils): b"},
			"utils@1.0.0": {"fix(core): a", "fix(utils): b"},
			"app@1.0.0":   {"fix(core): a", "fix(utils): b"},
		},
	}
	p, err := Compute(context.Background(), git, pkgs, deps)
	require.NoError(t, err)

	app := p.Releases["app"]
	// Both providers changed, but the consumer only gets one patch bump.
	assert.Equal(t, semver.Version{Major: 1, Patch: 1}, app.Next, "app -> 1.0.1")
	assert.ElementsMatch(t, []string{"core", "utils"}, app.DueTo)
}

func TestComputeBreakingChange(t *testing.T) {
	pkgs, deps := testPackages()
	git := &fakeGit{
		tags: map[string]gitx.Tag{
			"core": {Name: "core@1.5.2", Version: semver.Version{Major: 1, Minor: 5, Patch: 2}},
		},
		logs: map[string][]string{
			"core@1.5.2": {"BREAKING CHANGE(core): drop old API"},
			"":           {"BREAKING CHANGE(core): drop old API", "feat(app): support new core API"},
		},
	}
	p, err := Compute(context.Background(), git, pkgs, deps)
	require.NoError(t, err)

	assert.Equal(t, semver.Version{Major: 2}, p.Releases["core"].Next, "core -> 2.0.0")

	// app was never tagged: whole history counts; its own feat commit exists.
	app := p.Releases["app"]
	assert.Equal(t, semver.BumpMinor, app.Bump)
	assert.Equal(t, semver.Version{Minor: 1}, app.Next, "first release -> 0.1.0")
}

func TestComputeCycle(t *testing.T) {
	pkgs, _ := testPackages()
	deps := []model.Dependency{
		{Consumer: "app", Provider: "core"},
		{Consumer: "core", Provider: "app"},
	}
	git := &fakeGit{tags: map[string]gitx.Tag{}, logs: map[string][]string{}}
	_, err := Compute(context.Background(), git, pkgs, deps)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cycle")
}
