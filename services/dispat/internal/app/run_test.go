package app

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/ccme"

	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/filter"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// runPlan builds a minimal plan over absolute package folders under root: one
// "libs" space whose effective script map defines "lint", packages in the
// given order, providers as declared. Enough structure for the selection
// helpers, which never execute anything.
func runPlan(root string, names []string, providers map[string][]string) *plan.Plan {
	libs := &model.Space{Name: "libs", Scripts: map[string]string{"lint": "run lint"}}
	pl := &plan.Plan{Releases: map[string]*plan.Release{}, Providers: providers}
	for _, n := range names {
		pl.Releases[n] = &plan.Release{
			Pkg:     &model.Package{Name: n, Dir: filepath.Join(root, "libs", n), Space: libs},
			Current: ccme.Version{Major: 1},
		}
		pl.Order = append(pl.Order, n)
	}
	return pl
}

func TestWithConsumers(t *testing.T) {
	// b consumes a, c consumes b, d is independent: selecting a pulls the
	// whole chain in, transitively, and the result keeps plan order.
	pl := runPlan(t.TempDir(), []string{"a", "b", "c", "d"},
		map[string][]string{"b": {"a"}, "c": {"b"}})
	assert.Equal(t, []string{"a", "b", "c"}, withConsumers(pl, []string{"a"}))
	assert.Equal(t, []string{"b", "c"}, withConsumers(pl, []string{"b"}))
	assert.Equal(t, []string{"d"}, withConsumers(pl, []string{"d"}), "no consumers, no additions")
	assert.Equal(t, []string{"a", "b", "c", "d"}, withConsumers(pl, []string{"d", "a"}),
		"the union comes out in plan order, not selection order")
}

// TestRunSelection pins the whole rule: a window decides what is on the table,
// the filter narrows it, --consumers expands the result.
func TestRunSelection(t *testing.T) {
	root := t.TempDir()
	// b consumes a, c consumes b, d is independent; a and c are releasing.
	pl := runPlan(root, []string{"a", "b", "c", "d"},
		map[string][]string{"b": {"a"}, "c": {"b"}})
	for _, n := range []string{"a", "c"} {
		pl.Releases[n].Pinned = true
	}
	cfg := &config.File{Spaces: map[string]config.SpaceConfig{"libs": {Path: "libs"}}}
	app := New(root, cfg, zerolog.Nop())
	ctx := context.Background()

	for name, tc := range map[string]struct {
		opts RunOptions
		want []string
	}{
		"no filter is the release window": {
			RunOptions{}, []string{"a", "c"}},
		"a package term narrows the window": {
			RunOptions{Filter: filter.Filter{Packages: []string{"a"}}}, []string{"a"}},
		"a term outside the window selects nothing": {
			RunOptions{Filter: filter.Filter{Packages: []string{"b"}}}, []string{}},
		"a glob term narrows the window": {
			RunOptions{Filter: filter.Filter{Packages: []string{"*"}}}, []string{"a", "c"}},
		"a space term narrows the window": {
			RunOptions{Filter: filter.Filter{Spaces: []string{"libs"}}}, []string{"a", "c"}},
		"the invocation folder narrows the window": {
			RunOptions{Filter: filter.Filter{Dir: filepath.Join(root, "libs", "c")}}, []string{"c"}},
		"since all covers every package": {
			RunOptions{Since: SinceAll}, []string{"a", "b", "c", "d"}},
		"since all plus a filter runs an unchanged package": {
			RunOptions{Since: SinceAll, Filter: filter.Filter{Packages: []string{"b"}}}, []string{"b"}},
		"consumers expand past the filter": {
			RunOptions{Filter: filter.Filter{Packages: []string{"a"}}, Consumers: true},
			[]string{"a", "b", "c"}},
		"the selection keeps plan order, not term order": {
			RunOptions{Since: SinceAll, Filter: filter.Filter{Packages: []string{"d", "b"}}},
			[]string{"b", "d"}},
	} {
		t.Run(name, func(t *testing.T) {
			selected, err := app.runSelection(ctx, pl, tc.opts)
			require.NoError(t, err)
			assert.Equal(t, tc.want, selected)
		})
	}

	_, err := app.runSelection(ctx, pl, RunOptions{Filter: filter.Filter{Packages: []string{"nope"}}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `--package "nope" matches no package`)
	assert.Contains(t, err.Error(), "a, b, c, d", "the error lists what was discovered")
}

func TestSincePackagesAll(t *testing.T) {
	root := t.TempDir()
	pl := runPlan(root, []string{"a", "b"}, map[string][]string{})
	a := New(root, &config.File{}, zerolog.Nop())
	selected, err := a.sincePackages(context.Background(), pl, SinceAll)
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b"}, selected)
	selected[0] = "mutated"
	assert.Equal(t, []string{"a", "b"}, pl.Order, "the selection is a copy, not the plan's own slice")
}

func TestScriptRunBlocker(t *testing.T) {
	pl := runPlan(t.TempDir(), []string{"a", "b", "c"}, map[string][]string{"c": {"a", "b"}})
	s := &scriptRun{pl: pl, results: map[string]*runOutcome{
		"a": {ran: true},
		"b": {failed: true},
	}}
	assert.Equal(t, "b", s.blocker("c"), "a failed provider blocks")
	s.results["b"] = &runOutcome{skipped: true}
	assert.Equal(t, "b", s.blocker("c"), "a skipped provider cascades")
	s.results["b"] = &runOutcome{ran: true}
	assert.Empty(t, s.blocker("c"), "clean providers block nothing")
	assert.Empty(t, s.blocker("a"), "no providers at all")
}
