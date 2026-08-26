package app

import (
	"context"
	"os"
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
	libs := &model.Space{Name: "libs", Scripts: map[string]config.Script{"lint": {"run lint"}}}
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

// runApp is an App over runPlan's shape: one "libs" space, nothing executed.
func runApp(root string) *App {
	cfg := &config.File{Spaces: map[string]config.SpaceConfig{"libs": {Path: config.PathList{"libs"}}}}
	return New(root, cfg, zerolog.Nop())
}

// TestReportNothingResolved pins the split the run command draws when the
// sweep resolved the script in no covered package: a selection the user
// named errors, a selection the window assembled on its own is a reported
// no-op.
func TestReportNothingResolved(t *testing.T) {
	root := t.TempDir()
	pl := runPlan(root, []string{"a", "b"}, nil)
	a := runApp(root)

	sel, err := filter.Resolve(filter.Filter{Packages: []string{"a"}}, a.planWorkspace(pl))
	require.NoError(t, err)
	require.True(t, sel.Active())
	err = a.reportNothingResolved("stamp", sel, []string{"a"})
	require.Error(t, err, "an explicit selection without the script is a refusal")
	assert.EqualError(t, err, `no selected package defines script "stamp" (selected: a)`)

	assert.NoError(t, a.reportNothingResolved("stamp", filter.Result{}, []string{"a", "b"}),
		"a window-only selection without the script is a no-op, not a failure")
}

// TestScriptDefinedAnywhereSeesAnEmptySpaceFile: a script written in a space
// folder's own config file counts as defined even when the space discovers no
// package — the one carrier the package walk cannot answer for. The config
// goes through Load like a real run's, because Load is what settles the
// defaults discovery leans on.
func TestScriptDefinedAnywhereSeesAnEmptySpaceFile(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "emptyspace"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "emptyspace", "dispat.json"),
		[]byte(`{"scripts": {"special": ["echo hi"]}}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "dispat.json"),
		[]byte(`{"spaces": {"empty": {"path": "emptyspace"}}}`), 0o644))
	cfg, err := config.Load(filepath.Join(root, "dispat.json"), nil)
	require.NoError(t, err)
	a := New(root, cfg, zerolog.Nop())

	assert.True(t, a.scriptDefinedAnywhere("special"),
		"a space folder's script needs no package to be seen")
	assert.True(t, a.scriptDefinedAnywhere("SPECIAL"),
		"the lookup folds case like every other level")
	assert.False(t, a.scriptDefinedAnywhere("ghost"))
}

func TestScriptWorkStage(t *testing.T) {
	w := &scriptWork{name: "Lint"}
	assert.Equal(t, "run:Lint", w.stage(), "the stage carries the name as the user spelled it")
}

// TestScriptWorkResolve pins what a package resolving the script means, and
// what a package that does not resolve it means: a nil task, never an error.
func TestScriptWorkResolve(t *testing.T) {
	root := t.TempDir()
	pl := runPlan(root, []string{"a", "b"}, map[string][]string{"b": {"a"}})
	// Only b's space defines "build"; a's does not.
	pl.Releases["b"].Pkg.Space = &model.Space{Name: "apps", Scripts: map[string]config.Script{"build": {"go build"}}}
	a := runApp(root)

	w := &scriptWork{app: a, pl: pl, name: "build", covered: coveredReleases(pl, []string{"a", "b"})}
	task, err := w.resolve(context.Background(), pl.Releases["b"])
	require.NoError(t, err)
	assert.NotNil(t, task, "the package defines the script")

	task, err = w.resolve(context.Background(), pl.Releases["a"])
	require.NoError(t, err)
	assert.Nil(t, task, "a package that does not define the script is a no-op, not a failure")
}

// TestScriptWorkRunsEveryCommand: a run script bound to several commands runs
// all of them in the package's folder, in order, and the arguments typed after
// `--` land on the last one — the script's work, rather than its setup.
func TestScriptWorkRunsEveryCommand(t *testing.T) {
	root := t.TempDir()
	pl := runPlan(root, []string{"a"}, nil)
	pl.Releases["a"].Pkg.Space = &model.Space{Name: "libs",
		Scripts: map[string]config.Script{"test": {"npm ci", "npm run test"}}}
	f := &fakeRunner{}
	w := &scriptWork{app: runApp(root), pl: pl, name: "test", args: []string{"--watch"},
		runner: f, covered: coveredReleases(pl, []string{"a"})}

	task, err := w.resolve(context.Background(), pl.Releases["a"])
	require.NoError(t, err)
	require.NotNil(t, task)
	require.NoError(t, task(context.Background()))

	assert.Equal(t, []string{"npm ci", "npm run test --watch"}, f.ran)
	assert.Equal(t, []string{pl.Releases["a"].Pkg.Dir, pl.Releases["a"].Pkg.Dir}, f.dirs,
		"every command runs in the package folder, none of them where a previous one left off")
}

// TestScriptWorkCarriesProviderOutputs proves the run-command counterpart of
// the pipeline's accumulation: a covered provider's exports reach the consumer
// before its script is resolved, and an uncovered provider's do not.
func TestScriptWorkCarriesProviderOutputs(t *testing.T) {
	root := t.TempDir()
	pl := runPlan(root, []string{"a", "b", "c"}, map[string][]string{"c": {"a", "b"}})
	pl.Releases["a"].Outputs = []plan.Output{{Name: "FROM_A", Value: "1"}}
	pl.Releases["b"].Outputs = []plan.Output{{Name: "FROM_B", Value: "2"}}

	// The run covers a and c, but not b.
	w := &scriptWork{app: runApp(root), pl: pl, name: "lint",
		covered: coveredReleases(pl, []string{"a", "c"})}
	_, err := w.resolve(context.Background(), pl.Releases["c"])
	require.NoError(t, err)
	assert.Equal(t, []plan.Output{{Name: "FROM_A", Value: "1"}}, pl.Releases["c"].Outputs,
		"only a provider the run covered has outputs worth carrying")
}
