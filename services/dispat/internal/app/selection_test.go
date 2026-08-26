package app

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/filter"
)

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

// TestCoveredPackages pins the whole rule every sweeping command shares: a
// window decides what is on the table, the filter narrows it, --consumers
// expands the result.
func TestCoveredPackages(t *testing.T) {
	root := t.TempDir()
	// b consumes a, c consumes b, d is independent; a and c are releasing.
	pl := runPlan(root, []string{"a", "b", "c", "d"},
		map[string][]string{"b": {"a"}, "c": {"b"}})
	for _, n := range []string{"a", "c"} {
		pl.Releases[n].Pinned = true
	}
	app := runApp(root)
	ctx := context.Background()

	for name, tc := range map[string]struct {
		opts WindowOptions
		want []string
	}{
		"no filter is the release window": {
			WindowOptions{}, []string{"a", "c"}},
		"a package term narrows the window": {
			WindowOptions{Filter: filter.Filter{Packages: []string{"a"}}}, []string{"a"}},
		"a term outside the window covers nothing": {
			WindowOptions{Filter: filter.Filter{Packages: []string{"b"}}}, []string{}},
		"a glob term narrows the window": {
			WindowOptions{Filter: filter.Filter{Packages: []string{"*"}}}, []string{"a", "c"}},
		"a space term narrows the window": {
			WindowOptions{Filter: filter.Filter{Spaces: []string{"libs"}}}, []string{"a", "c"}},
		"the invocation folder narrows the window": {
			WindowOptions{Filter: filter.Filter{Dir: filepath.Join(root, "libs", "c")}}, []string{"c"}},
		"since all covers every package": {
			WindowOptions{Since: SinceAll}, []string{"a", "b", "c", "d"}},
		"since all plus a filter covers an unchanged package": {
			WindowOptions{Since: SinceAll, Filter: filter.Filter{Packages: []string{"b"}}}, []string{"b"}},
		"consumers expand past the filter": {
			WindowOptions{Filter: filter.Filter{Packages: []string{"a"}}, Consumers: true},
			[]string{"a", "b", "c"}},
		"the selection keeps plan order, not term order": {
			WindowOptions{Since: SinceAll, Filter: filter.Filter{Packages: []string{"d", "b"}}},
			[]string{"b", "d"}},
	} {
		t.Run(name, func(t *testing.T) {
			covered, err := app.coveredPackages(ctx, pl, tc.opts)
			require.NoError(t, err)
			assert.Equal(t, tc.want, covered)
		})
	}

	_, err := app.coveredPackages(ctx, pl, WindowOptions{Filter: filter.Filter{Packages: []string{"nope"}}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `--package "nope" matches no package`)
	assert.Contains(t, err.Error(), "a, b, c, d", "the error lists what was discovered")
}

// TestCoveredSelectionReportsActivity pins what makes a selection explicit:
// a term and the invocation folder switch the filter's Result on, the window
// alone never does — the whole distinction `dispat run` draws when nothing
// resolves a script — and the coveredPackages wrapper returns the same
// covered packages.
func TestCoveredSelectionReportsActivity(t *testing.T) {
	root := t.TempDir()
	pl := runPlan(root, []string{"a", "b"}, nil)
	pl.Releases["a"].Pinned = true
	app := runApp(root)
	ctx := context.Background()

	for name, tc := range map[string]struct {
		opts   WindowOptions
		active bool
	}{
		"no filter is not explicit":         {WindowOptions{}, false},
		"since alone is not explicit":       {WindowOptions{Since: SinceAll}, false},
		"a package term is explicit":        {WindowOptions{Filter: filter.Filter{Packages: []string{"a"}}}, true},
		"the invocation folder is explicit": {WindowOptions{Filter: filter.Filter{Dir: filepath.Join(root, "libs", "a")}}, true},
		"a term stays explicit under since": {WindowOptions{Since: SinceAll, Filter: filter.Filter{Packages: []string{"b"}}}, true},
	} {
		t.Run(name, func(t *testing.T) {
			sel, covered, err := app.coveredSelection(ctx, pl, tc.opts)
			require.NoError(t, err)
			assert.Equal(t, tc.active, sel.Active())
			viaWrapper, err := app.coveredPackages(ctx, pl, tc.opts)
			require.NoError(t, err)
			assert.Equal(t, covered, viaWrapper, "the wrapper is the same selection minus the Result")
		})
	}
}

func TestSincePackagesAll(t *testing.T) {
	root := t.TempDir()
	pl := runPlan(root, []string{"a", "b"}, map[string][]string{})
	a := New(root, &config.File{}, zerolog.Nop())
	covered, err := a.sincePackages(context.Background(), pl, SinceAll)
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b"}, covered)
	covered[0] = "mutated"
	assert.Equal(t, []string{"a", "b"}, pl.Order, "the selection is a copy, not the plan's own slice")
}
