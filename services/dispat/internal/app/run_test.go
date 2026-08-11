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

// runApp is an App over runPlan's shape: one "libs" space, nothing executed.
func runApp(root string) *App {
	cfg := &config.File{Spaces: map[string]config.SpaceConfig{"libs": {Path: "libs"}}}
	return New(root, cfg, zerolog.Nop())
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
	pl.Releases["b"].Pkg.Space = &model.Space{Name: "apps", Scripts: map[string]string{"build": "go build"}}
	a := runApp(root)

	w := &scriptWork{app: a, pl: pl, name: "build", covered: coveredReleases(pl, []string{"a", "b"})}
	task, err := w.resolve(context.Background(), pl.Releases["b"])
	require.NoError(t, err)
	assert.NotNil(t, task, "the package defines the script")

	task, err = w.resolve(context.Background(), pl.Releases["a"])
	require.NoError(t, err)
	assert.Nil(t, task, "a package that does not define the script is a no-op, not a failure")
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
