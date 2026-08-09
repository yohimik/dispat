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
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// runPlan builds a minimal plan over absolute package folders under root: one
// "libs" space defining the "lint" run script, packages in the given order,
// providers as declared. Enough structure for the selection helpers, which
// never execute anything.
func runPlan(root string, names []string, providers map[string][]string) *plan.Plan {
	libs := &model.Space{Name: "libs", RunScripts: map[string]string{"lint": "run lint"}}
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

func TestRunTarget(t *testing.T) {
	root := t.TempDir()
	pl := runPlan(root, []string{"core", "web"}, map[string][]string{})
	a := New(root, &config.File{}, zerolog.Nop())

	target, err := a.runTarget(pl, "lint", RunOptions{})
	require.NoError(t, err)
	assert.Empty(t, target, "no narrowing options, no target")

	_, err = a.runTarget(pl, "lint", RunOptions{Package: "nope"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unknown package "nope"`)
	assert.Contains(t, err.Error(), "core, web", "the error lists what was discovered")

	_, err = a.runTarget(pl, "absent", RunOptions{Package: "core"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `does not define run script "absent"`)

	target, err = a.runTarget(pl, "lint", RunOptions{Package: "core"})
	require.NoError(t, err)
	assert.Equal(t, "core", target)

	// The dispat <script> shorthand narrows through the invocation folder.
	dir := filepath.Join(root, "libs", "web")
	target, err = a.runTarget(pl, "lint", RunOptions{Dir: dir})
	require.NoError(t, err)
	assert.Equal(t, "web", target)

	// An explicit window beats the folder inference.
	target, err = a.runTarget(pl, "lint", RunOptions{Dir: dir, Since: "HEAD~1"})
	require.NoError(t, err)
	assert.Empty(t, target, "--since suppresses folder narrowing")

	target, err = a.runTarget(pl, "lint", RunOptions{Dir: dir, Consumers: true})
	require.NoError(t, err)
	assert.Empty(t, target, "--consumers suppresses folder narrowing")
}

func TestPackageAt(t *testing.T) {
	root := t.TempDir()
	pl := runPlan(root, []string{"core"}, map[string][]string{})
	a := New(root, &config.File{}, zerolog.Nop())

	core := filepath.Join(root, "libs", "core")
	assert.Equal(t, "core", a.packageAt(pl, core), "the folder itself")
	assert.Equal(t, "core", a.packageAt(pl, filepath.Join(core, "src", "deep")), "a nested folder")
	assert.Empty(t, a.packageAt(pl, root), "the monorepo root is not inside any package")
	assert.Empty(t, a.packageAt(pl, filepath.Join(root, "libs", "core-extra")),
		"a sibling sharing the name prefix is not inside the package")
	assert.Empty(t, a.packageAt(pl, filepath.Join(root, "elsewhere")))
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
