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
	"github.com/yohimik/dispat/services/dispat/internal/model"
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

// TestStepExportReadsTheStageEnvironment: a step invocation has no run
// behind it, so the GitHub opt-in comes from the process environment the
// stage script handed it, attributed to the package DISPAT_PACKAGE names.
func TestStepExportReadsTheStageEnvironment(t *testing.T) {
	targets := []string{"a", "c"}
	for name, tc := range map[string]struct {
		export, pkg    string
		wantExport     string
		wantForPackage string
	}{
		"a stage script exports for its own package": {
			"/dist/app.tgz", "c", "/dist/app.tgz", "c"},
		"no DISPAT_PACKAGE means the whole invocation": {
			"/dist/app.tgz", "", "/dist/app.tgz", ""},
		"an export for an uncovered package is ignored": {
			"/dist/app.tgz", "b", "", "b"},
		"no export at all": {
			"", "c", "", "c"},
	} {
		t.Run(name, func(t *testing.T) {
			a, _, buf := stepApp(t)
			t.Setenv(plan.GitHubExport, tc.export)
			t.Setenv("DISPAT_PACKAGE", tc.pkg)

			export, forPackage := a.stepExport(targets)
			assert.Equal(t, tc.wantExport, export)
			assert.Equal(t, tc.wantForPackage, forPackage)
			if tc.export != "" && tc.wantExport == "" {
				assert.Contains(t, buf.String(), "does not cover",
					"an ignored export says so rather than going missing")
			}
		})
	}
}

// TestGitHubSpecOverridesBeatTheConfiguredPolicy: an explicit flag replaces
// the layered configuration for every package of the invocation, and an
// unset flag leaves the package's own value alone.
func TestGitHubSpecOverridesBeatTheConfiguredPolicy(t *testing.T) {
	a, _, _ := stepApp(t)
	base := model.GitHubSpec{Enabled: true, Prerelease: true,
		Owner: "acme", Repo: "mono", APIURL: "https://ghe.acme/api/v3", TokenEnv: "ACME_TOKEN"}

	assert.Equal(t, base, a.githubSpec(base, GitHubOptions{}), "no flags change nothing")

	got := a.githubSpec(base, GitHubOptions{Owner: "other", TokenEnv: "OTHER_TOKEN"})
	assert.Equal(t, "other", got.Owner)
	assert.Equal(t, "OTHER_TOKEN", got.TokenEnv)
	assert.Equal(t, "mono", got.Repo, "an unset flag leaves the configured value")
	assert.Equal(t, "https://ghe.acme/api/v3", got.APIURL)
	assert.True(t, got.Enabled, "the overrides are addressing, not policy")
}
