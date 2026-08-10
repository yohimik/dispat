package app

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/filter"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// stepApp builds the plan the step commands select over — packages a, b, c,
// d, of which a and c are releasing — plus an App logging into buf.
func stepApp(t *testing.T) (*App, *plan.Plan, *bytes.Buffer) {
	t.Helper()
	root := t.TempDir()
	pl := runPlan(root, []string{"a", "b", "c", "d"}, map[string][]string{})
	for _, n := range []string{"a", "c"} {
		pl.Releases[n].Pinned = true
	}
	buf := &bytes.Buffer{}
	cfg := &config.File{Spaces: map[string]config.SpaceConfig{"libs": {Path: "libs"}}}
	return New(root, cfg, zerolog.New(buf)), pl, buf
}

func TestStepTargets(t *testing.T) {
	a, pl, _ := stepApp(t)
	root := a.root
	for name, tc := range map[string]struct {
		f    filter.Filter
		want []string
	}{
		"no filter covers every releasing package": {
			filter.Filter{}, []string{"a", "c"}},
		"a package term narrows": {
			filter.Filter{Packages: []string{"c"}}, []string{"c"}},
		"a space term narrows": {
			filter.Filter{Spaces: []string{"libs"}}, []string{"a", "c"}},
		"a glob term narrows": {
			filter.Filter{Packages: []string{"*"}}, []string{"a", "c"}},
		"the invocation folder narrows": {
			filter.Filter{Dir: filepath.Join(root, "libs", "a")}, []string{"a"}},
		"a selection outside the release is empty": {
			filter.Filter{Packages: []string{"b"}}, []string{}},
	} {
		t.Run(name, func(t *testing.T) {
			targets, err := a.stepTargets(pl, tc.f)
			require.NoError(t, err)
			assert.Equal(t, tc.want, targets)
		})
	}
}

func TestStepTargetsLogsEverySelectedPackageThatIsNotReleasing(t *testing.T) {
	// A flow must not fail over a package the planner held or converged, so a
	// selected non-releasing package is a logged no-op — one line each, so a
	// filter covering several says which.
	a, pl, buf := stepApp(t)
	targets, err := a.stepTargets(pl, filter.Filter{Packages: []string{"b", "d", "c"}})
	require.NoError(t, err)
	assert.Equal(t, []string{"c"}, targets)
	logged := buf.String()
	assert.Contains(t, logged, `"package":"b"`)
	assert.Contains(t, logged, `"package":"d"`)
	assert.Contains(t, logged, "package is not releasing, nothing to do")
	assert.NotContains(t, logged, `"package":"c"`, "a releasing target is not a no-op")
}

func TestStepTargetsRejectsAnUnmatchedTerm(t *testing.T) {
	a, pl, _ := stepApp(t)
	_, err := a.stepTargets(pl, filter.Filter{Packages: []string{"ghost"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `--package "ghost" matches no package`)

	_, err = a.stepTargets(pl, filter.Filter{Spaces: []string{"a"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "a is a package — select it with --package")
}
