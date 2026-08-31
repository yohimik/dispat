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

// TestChangedAndUnchangedPartitionThePlan pins what `dispat for --unchanged`
// is: not a second window with rules of its own, but the complement of the one
// `--changed` selects. The two are asserted together at every composition,
// because the only property worth having is that each package lands in exactly
// one of them — a flag that moved a package out of the changed half without
// moving it into the unchanged one would leave it unreachable by either loop.
func TestChangedAndUnchangedPartitionThePlan(t *testing.T) {
	root := t.TempDir()
	// b consumes a, c consumes b, d is independent; a alone is releasing, so
	// the window holds a, and a, b and c with --consumers.
	pl := runPlan(root, []string{"a", "b", "c", "d"},
		map[string][]string{"b": {"a"}, "c": {"b"}})
	pl.Releases["a"].Pinned = true
	app := runApp(root)
	ctx := context.Background()

	for name, tc := range map[string]struct {
		opts      WindowOptions
		changed   []string
		unchanged []string
	}{
		"the release window and its complement": {
			WindowOptions{}, []string{"a"}, []string{"b", "c", "d"}},
		"consumers move packages from one half to the other": {
			WindowOptions{Consumers: true}, []string{"a", "b", "c"}, []string{"d"}},
		"a filter narrows both halves the same way": {
			WindowOptions{Filter: filter.Filter{Packages: []string{"a", "d"}}},
			[]string{"a"}, []string{"d"}},
		"since all leaves the complement empty": {
			WindowOptions{Since: SinceAll}, []string{"a", "b", "c", "d"}, []string{}},
		"a term inside the window covers nothing unchanged": {
			WindowOptions{Filter: filter.Filter{Packages: []string{"a"}}},
			[]string{"a"}, []string{}},
		"the invocation folder narrows both": {
			WindowOptions{Filter: filter.Filter{Dir: filepath.Join(root, "libs", "b")}},
			[]string{}, []string{"b"}},
	} {
		t.Run(name, func(t *testing.T) {
			changed, err := app.changedSelection(ctx, pl, tc.opts)
			require.NoError(t, err)
			assert.Equal(t, tc.changed, changed)
			unchanged, err := app.unchangedSelection(ctx, pl, tc.opts)
			require.NoError(t, err)
			assert.Equal(t, tc.unchanged, unchanged, "the complement, in plan order")
			assert.Empty(t, intersect(changed, unchanged), "no package may be in both halves")
		})
	}

	// A term matching nothing is an error on either half, never an empty loop.
	_, err := app.unchangedSelection(ctx, pl, WindowOptions{Filter: filter.Filter{Packages: []string{"nope"}}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `--package "nope" matches no package`)
}

// intersect is the overlap of two selections, which the partition claim needs
// to be empty.
func intersect(a, b []string) []string {
	in := make(map[string]bool, len(a))
	for _, name := range a {
		in[name] = true
	}
	var both []string
	for _, name := range b {
		if in[name] {
			both = append(both, name)
		}
	}
	return both
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
