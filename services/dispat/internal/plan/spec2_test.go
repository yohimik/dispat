package plan

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/ccme/v2"
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// Vector 134: suppression is a source-set predicate, so publishing the
// suppressed source changes the retry input and may expose finite catch-up.
func TestSpec2SuppressionLapseCanExposeCatchUp(t *testing.T) {
	history := []commit{
		{sha: "c0", message: "chore: setup"},
		{sha: "c1", message: "feat(core)^: streaming"},
		{sha: "c2", message: "cancel(core): suppress the pending source"},
	}
	before := newFakeGit(history...).tag("core", "1.0.0", "c0").tag("app", "1.0.0", "c0")
	assert.False(t, compute(t, before, nil).Releases["app"].Changed(),
		"while core is suppressed there is no source from which app can inherit work")

	after := newFakeGit(history...).tag("core", "1.0.0", "c0").tag("core", "1.1.0", "c2").
		tag("app", "1.0.0", "c0")
	app := compute(t, after, nil).Releases["app"]
	assert.True(t, app.Changed(), "publishing core discharges its suppression and exposes app's debt")
	assert.True(t, app.CatchUp)
	assert.Equal(t, []string{"core"}, app.DueTo)
}

// Vector 135: upward stale-source reporting uses the same channel
// resolvability predicate as downward propagation.
func TestSpec2RejectedBumpProducesNoStaleSourceRow(t *testing.T) {
	git := newFakeGit(
		commit{sha: "c0", message: "release(app)%rc: app's line"},
		commit{sha: "c1", message: "feat(core)^%beta: core's line"},
	).tag("core", "1.0.0", "").tag("app", "1.0.0", "").tag("app", "1.0.1-rc.0", "c0")

	p := compute(t, git, nil)
	assert.False(t, p.Releases["app"].Changed())
	assert.Empty(t, p.StaleSources("app"), "a W208 rejection must not leave an upward stale row")
	assert.True(t, hasCode(p, CodeBumpSuppressed), "W208, got %v", codes(p))
}

// Vector 136: bump admission reads resolved channels, but rejecting that bump
// does not run backwards and suppress the independently admitted channel.
func TestSpec2RejectedBumpDoesNotSuppressChannel(t *testing.T) {
	git := newFakeGit(
		commit{sha: "c1", message: "feat(core)^%beta%%rc++1: split axes"},
	).tag("core", "1.0.0", "").tag("app", "1.0.0", "")

	p := compute(t, git, nil)
	app := p.Releases["app"]
	assert.Equal(t, ccme.BumpNone, app.PropagatedBump)
	assert.Empty(t, p.StaleSources("app"))
	assert.Equal(t, "rc", app.Channel)
	assert.True(t, app.ChannelChanged(), "the independently admitted channel still applies")
	assert.True(t, hasCode(p, CodeBumpSuppressed), "W208, got %v", codes(p))
}

// Vector 137: inherit is resolved from the source baseline on every plan. A
// source graduation between retries is therefore observable drift, outside
// the retry invariant, rather than version stability.
func TestSpec2SourceGraduationChangesInheritedRetry(t *testing.T) {
	history := []commit{
		{sha: "c0", message: "release(core)%beta: start source train"},
		{sha: "c1", message: "fix(core)^%%inherit++1: inherited retry"},
		{sha: "c2", message: "release(core)%stable: graduate source"},
	}
	first := newFakeGit(history...).tag("core", "1.0.0", "").tag("core", "1.0.1-beta.0", "c0").
		tag("app", "1.0.0", "")
	firstApp := compute(t, first, nil).Releases["app"]
	require.True(t, firstApp.Changed())
	assert.Equal(t, "beta", firstApp.Channel)
	assertVersion(t, pre(1, 0, 1, "beta", "0"), firstApp.Next)

	retry := newFakeGit(history...).tag("core", "1.0.0", "").tag("core", "1.0.1-beta.0", "c0").
		tag("core", "1.0.1", "c2").tag("app", "1.0.0", "")
	retryApp := compute(t, retry, nil).Releases["app"]
	require.True(t, retryApp.Changed())
	assert.Equal(t, "stable", retryApp.Channel)
	assertVersion(t, v(1, 0, 1), retryApp.Next)
}

// Vector 138: self-source exclusion belongs to a unit, not to a bucket or
// target. Another unit in the same commit may still admit the same target.
func TestSpec2SelfSourceCheckIsPerUnit(t *testing.T) {
	space := &model.Space{Name: "libs"}
	pkgs := []*model.Package{
		{Name: "core", Dir: "/r/core", Space: space},
		{Name: "utils", Dir: "/r/utils", Space: space},
		{Name: "cli", Dir: "/r/cli", Space: space},
	}
	deps := []model.Dependency{{Consumer: "cli", Provider: "core"}, {Consumer: "cli", Provider: "utils"}}
	git := newFakeGit(commit{sha: "c1", message: "fix(cli,core)^: cli is a source\n---\nfix(utils)^: cli is only a target"}).
		tag("core", "1.0.0", "").tag("utils", "1.0.0", "").tag("cli", "1.0.0", "")

	p, err := Compute(context.Background(), git, Options{Packages: pkgs, Dependencies: deps, Root: "/r"})
	require.NoError(t, err)
	cli := p.Releases["cli"]
	assert.True(t, cli.Changed())
	assert.Equal(t, []string{"utils"}, cli.DueTo,
		"the cli/core unit excludes cli as its own source, while the utils unit still admits it")
	assert.Len(t, cli.Sources, 1)
	assert.Equal(t, "utils", cli.Sources[0].Provider)
}
