package config

// `runOnly` from the two sides a configuration decides: what the ladder
// resolves it to, and what a level may not write.
//
// Every claim is made against the resolved model, through Load and
// DiscoverPackages, because the merge has no seam of its own: the ladder is
// only observable in what a package ended up with.

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	public "github.com/yohimik/dispat/pkg/models"
)

// placementAt is one stated value, as the model carries it.
func placementAt(build, publish string) *RunOnly {
	return &RunOnly{Build: build, Publish: publish}
}

// runOnlyLadderConfig is the fixture the ladder claims are made on: a
// repository default, a space that overrides it, and two packages that reach
// the ladder from different heights.
func runOnlyLadderConfig() File {
	return File{
		Scripts: map[string]Script{"build": {"echo b"}},
		RunOnly: placementAt(public.RunOnlyOrchestrator, public.RunOnlyOrchestrator),
		Spaces: map[string]SpaceConfig{
			"libs": {
				Path:    PathList{"packages/libs"},
				Flow:    &SpaceFlowConfig{Build: []string{"build"}},
				RunOnly: placementAt(public.RunOnlyWorker, public.RunOnlyWorker),
			},
			"apps": {
				Path: PathList{"packages/apps"},
				Flow: &SpaceFlowConfig{Build: []string{"build"}},
			},
		},
		Packages: map[string]PackageConfig{"tool": {Path: "tools/tool"}},
	}
}

// TestRunOnlyResolvesThroughTheLadder: the nearest level that states the key
// wins, as a pair, and a level that states nothing inherits. The space
// folder's own file outranks the root file's entry for that space, and a
// package folder file outranks everything above it.
func TestRunOnlyResolvesThroughTheLadder(t *testing.T) {
	root := writeModelRepo(t, runOnlyLadderConfig(),
		"packages/libs/core", "packages/libs/utils", "packages/apps/app", "tools/tool")
	writeSpaceFile(t, root, "packages/libs", SpaceFile{
		RunOnly: placementAt(public.RunOnlyWorker, public.RunOnlyOrchestrator),
	})
	writePackageFile(t, root, "packages/libs/core", PackageConfig{
		RunOnly: placementAt(public.RunOnlyBoth, public.RunOnlyBoth),
	})

	loaded, err := Load(filepath.Join(root, "dispat.json"), nil)
	require.NoError(t, err)
	pkgs, _, _, err := DiscoverPackages(loaded, root)
	require.NoError(t, err)
	by := packagesByName(pkgs)

	for name, want := range map[string]*RunOnly{
		"core":  placementAt(public.RunOnlyBoth, public.RunOnlyBoth),
		"utils": placementAt(public.RunOnlyWorker, public.RunOnlyOrchestrator),
		"app":   placementAt(public.RunOnlyOrchestrator, public.RunOnlyOrchestrator),
		"tool":  placementAt(public.RunOnlyOrchestrator, public.RunOnlyOrchestrator),
	} {
		require.Contains(t, by, name)
		assert.Equal(t, want, by[name].Space.RunOnly, "the ladder resolved %s", name)
	}
}

// TestRunOnlyIsUnstatedWhenNobodySaidSo: a workspace that never writes the
// key carries nothing, which is what keeps the resolved line and every
// placement decision the ones they were before the key existed.
func TestRunOnlyIsUnstatedWhenNobodySaidSo(t *testing.T) {
	root := writeModelRepo(t, minimalConfig(), "pkgs/core")
	loaded, err := Load(filepath.Join(root, "dispat.json"), nil)
	require.NoError(t, err)
	pkgs, _, _, err := DiscoverPackages(loaded, root)
	require.NoError(t, err)
	by := packagesByName(pkgs)
	require.Contains(t, by, "core")
	assert.Nil(t, by["core"].Space.RunOnly, "nobody stated the key")
	assert.Equal(t, public.RunOnlyBoth, by["core"].Space.RunOnly.ResolveBuild())
	assert.Equal(t, public.RunOnlyBoth, by["core"].Space.RunOnly.ResolvePublish())
}

// runOnlyRaw is the fixture for the shapes the typed model cannot express: a
// list of one, a list of three, a misspelled word.
func runOnlyRaw(t *testing.T, stated any) error {
	t.Helper()
	cfg := minimalConfig()
	root := writeModelRepo(t, cfg, "pkgs/core")
	writePackageRaw(t, root, "pkgs/core", map[string]any{"runOnly": stated})
	loaded, err := Load(filepath.Join(root, "dispat.json"), nil)
	if err != nil {
		return err
	}
	_, _, _, err = DiscoverPackages(loaded, root)
	return err
}

// TestRunOnlyRefusals: every shape and every word a level may not write, each
// refused with the execution code attached, because a configuration no
// distributed run could be executed under is one code a CI job switches on
// rather than a sentence it matches.
func TestRunOnlyRefusals(t *testing.T) {
	for name, row := range map[string]struct {
		stated any
		want   string
	}{
		"a misspelled value":       {stated: "orchestartor", want: "is not a placement"},
		"a value of another key":   {stated: "any", want: "is not a placement"},
		"a list of one":            {stated: []any{"worker"}, want: "states both stages"},
		"a list of three":          {stated: []any{"worker", "worker", "worker"}, want: "states both stages"},
		"a misspelled stage value": {stated: []any{"worker", "nowhere"}, want: "is not a placement"},
		"an object":                {stated: map[string]any{"build": "worker"}, want: "wants"},
	} {
		t.Run(name, func(t *testing.T) {
			err := runOnlyRaw(t, row.stated)
			require.Error(t, err)
			assert.Contains(t, err.Error(), row.want)
			assert.Contains(t, err.Error(), "runOnly", "the refusal names the key")
			assert.Equal(t, DiagnosticExecution, DiagnosticCode(err))
		})
	}
}

// TestRunOnlyRefusesAPublishOnAWorkerBesideALogin: a space that logs in
// publishes on the orchestrator, so a package of it pinning its publish to a
// worker is two statements that cannot both hold, and the refusal says which
// two.
func TestRunOnlyRefusesAPublishOnAWorkerBesideALogin(t *testing.T) {
	for name, row := range map[string]struct {
		stated    *RunOnly
		isRefused bool
	}{
		"both stages on a worker":     {stated: placementAt(public.RunOnlyWorker, public.RunOnlyWorker), isRefused: true},
		"the publish alone":           {stated: placementAt(public.RunOnlyBoth, public.RunOnlyWorker), isRefused: true},
		"the build alone":             {stated: placementAt(public.RunOnlyWorker, public.RunOnlyOrchestrator)},
		"a publish on this machine":   {stated: placementAt(public.RunOnlyWorker, public.RunOnlyBoth)},
		"a publish pinned to us both": {stated: placementAt(public.RunOnlyOrchestrator, public.RunOnlyOrchestrator)},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := minimalConfig()
			cfg.Scripts["login"] = Script{"echo in"}
			withLibs(&cfg, func(sc *SpaceConfig) {
				sc.Flow.Login = []string{"login"}
				sc.RunOnly = row.stated
			})
			root := writeModelRepo(t, cfg, "pkgs/core")
			loaded, err := Load(filepath.Join(root, "dispat.json"), nil)
			require.NoError(t, err)
			_, _, _, err = DiscoverPackages(loaded, root)
			if !row.isRefused {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), "flow.login")
			assert.Equal(t, DiagnosticExecution, DiagnosticCode(err))
		})
	}
}

// TestRunOnlyIsNotANodeStartupSetting: the key is a package's, so a level
// that carries it is a level that may say where its own work runs. The
// `execution` object is the opposite kind of setting and stays where it was:
// this asserts the two are not confused, by writing runOnly at every level
// that has a ladder.
func TestRunOnlyIsNotANodeStartupSetting(t *testing.T) {
	cfg := runOnlyLadderConfig()
	root := writeModelRepo(t, cfg, "packages/libs/core", "packages/apps/app", "tools/tool")
	writeSpaceRaw(t, root, "packages/libs", map[string]any{
		"runOnly":  "worker",
		"packages": map[string]any{"core": map[string]any{"runOnly": []any{"worker", "both"}}},
	})

	loaded, err := Load(filepath.Join(root, "dispat.json"), nil)
	require.NoError(t, err)
	pkgs, _, _, err := DiscoverPackages(loaded, root)
	require.NoError(t, err)
	by := packagesByName(pkgs)
	require.Contains(t, by, "core")
	assert.Equal(t, placementAt(public.RunOnlyWorker, public.RunOnlyBoth), by["core"].Space.RunOnly,
		"a space folder file's own packages entry is the nearest statement of all")
}
