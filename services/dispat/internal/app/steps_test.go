package app

import (
	"bytes"
	"context"
	"os"
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
	cfg := &config.File{Spaces: map[string]config.SpaceConfig{"libs": {Path: config.PathList{"libs"}}}}
	return New(root, cfg, zerolog.New(buf)), pl, buf
}

// TestStepCoverage pins what a step command covers: the releasing packages,
// narrowed by the one selection every command shares.
func TestStepCoverage(t *testing.T) {
	a, pl, _ := stepApp(t)
	root := a.root
	ctx := context.Background()
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
			covered, err := a.coveredPackages(ctx, pl, WindowOptions{Filter: tc.f})
			require.NoError(t, err)
			assert.Equal(t, tc.want, covered)
		})
	}
}

func TestStepCoverageLogsEverySelectedPackageOutsideTheWindow(t *testing.T) {
	// A flow must not fail over a package the planner held or converged, so a
	// selected non-releasing package is a logged no-op — one line each, so a
	// filter covering several says which.
	a, pl, buf := stepApp(t)
	covered, err := a.coveredPackages(context.Background(), pl,
		WindowOptions{Filter: filter.Filter{Packages: []string{"b", "d", "c"}}})
	require.NoError(t, err)
	assert.Equal(t, []string{"c"}, covered)
	logged := buf.String()
	assert.Contains(t, logged, `"package":"b"`)
	assert.Contains(t, logged, `"package":"d"`)
	assert.Contains(t, logged, "package is outside the window, nothing to do")
	assert.NotContains(t, logged, `"package":"c"`, "a releasing target is not a no-op")
}

// TestStepReleasingReportsAPackageAWindowPulledIn: --since and --consumers can
// put a package with nothing pending in front of a step command, and that is a
// no-op it says out loud rather than a failure.
func TestStepReleasingReportsAPackageAWindowPulledIn(t *testing.T) {
	a, pl, buf := stepApp(t)
	assert.True(t, a.releasing(pl.Releases["a"]), "a is releasing")
	assert.False(t, a.releasing(pl.Releases["b"]), "b is not")
	assert.Contains(t, buf.String(), "package is not releasing, nothing to do")
}

func TestStepCoverageRejectsAnUnmatchedTerm(t *testing.T) {
	a, pl, _ := stepApp(t)
	ctx := context.Background()
	_, err := a.coveredPackages(ctx, pl, WindowOptions{Filter: filter.Filter{Packages: []string{"ghost"}}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `--package "ghost" matches no package`)

	_, err = a.coveredPackages(ctx, pl, WindowOptions{Filter: filter.Filter{Spaces: []string{"a"}}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "a is a package, select it with --package")
}

// TestReadExportReadsTheStageEnvironment: a step invocation has no run
// behind it, so the GitHub opt-in comes from the process environment the
// stage script handed it. Presence is the opt-in and the value is the
// attachment list, so an export set to nothing still releases the package.
func TestReadExportReadsTheStageEnvironment(t *testing.T) {
	targets := []string{"a", "c"}
	for name, tc := range map[string]struct {
		export, pkg string
		unset       bool
		want        stepExport
		covers      map[string]bool
	}{
		"a stage script exports for its own package": {
			export: "/dist/app.tgz", pkg: "c",
			want:   stepExport{value: "/dist/app.tgz", present: true, pkg: "c"},
			covers: map[string]bool{"a": false, "c": true},
		},
		"no DISPAT_PACKAGE means every covered package": {
			export: "/dist/app.tgz",
			want:   stepExport{value: "/dist/app.tgz", present: true},
			covers: map[string]bool{"a": true, "c": true},
		},
		"an empty export is still an opt-in": {
			export: "", pkg: "c",
			want:   stepExport{present: true, pkg: "c"},
			covers: map[string]bool{"c": true},
		},
		"an export for an uncovered package is dropped": {
			export: "/dist/app.tgz", pkg: "b",
			want:   stepExport{pkg: "b"},
			covers: map[string]bool{"a": false, "c": false},
		},
		"no export at all": {
			unset:  true,
			pkg:    "c",
			want:   stepExport{pkg: "c"},
			covers: map[string]bool{"a": false, "c": false},
		},
	} {
		t.Run(name, func(t *testing.T) {
			a, _, buf := stepApp(t)
			if tc.unset {
				t.Setenv(plan.GitHubExport, "x")
				require.NoError(t, os.Unsetenv(plan.GitHubExport))
			} else {
				t.Setenv(plan.GitHubExport, tc.export)
			}
			t.Setenv(plan.PackageEnvVar, tc.pkg)

			got := a.readExport(targets)
			assert.Equal(t, tc.want, got)
			for pkg, want := range tc.covers {
				assert.Equal(t, want, got.covers(pkg), "covers(%q)", pkg)
			}
			if !tc.unset && tc.pkg == "b" {
				assert.Contains(t, buf.String(), "does not cover",
					"a dropped export says so rather than going missing")
			}
		})
	}
}

// TestGitHubSpecOverridesBeatTheConfiguredPolicy: an explicit flag replaces
// the layered configuration for every package of the invocation, and an
// unset flag leaves the package's own value alone.
func TestGitHubSpecOverridesBeatTheConfiguredPolicy(t *testing.T) {
	a, _, _ := stepApp(t)
	base := model.GitHubSpec{Enabled: true,
		Owner: "acme", Repo: "mono", APIURL: "https://ghe.acme/api/v3", TokenEnv: "ACME_TOKEN"}

	assert.Equal(t, base, a.githubSpec(base, GitHubOptions{}), "no flags change nothing")

	got := a.githubSpec(base, GitHubOptions{Owner: "other", TokenEnv: "OTHER_TOKEN"})
	assert.Equal(t, "other", got.Owner)
	assert.Equal(t, "OTHER_TOKEN", got.TokenEnv)
	assert.Equal(t, "mono", got.Repo, "an unset flag leaves the configured value")
	assert.Equal(t, "https://ghe.acme/api/v3", got.APIURL)
	assert.True(t, got.Enabled, "the overrides are addressing, not policy")
}
