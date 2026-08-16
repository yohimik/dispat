package app

import (
	"bytes"
	"os"
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

// clearRunEnv strips the wiring variables so a test starts outside any run,
// whatever environment the test process itself inherited.
func clearRunEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{plan.PackageEnvVar, plan.TagEnvVar, plan.NewVersionEnvVar,
		plan.UpdatedPackagesEnvVar, plan.WorkspacePackagesEnvVar} {
		t.Setenv(name, "")
		require.NoError(t, os.Unsetenv(name))
	}
}

func wiredEnv(t *testing.T, pkg, tag, version string) {
	t.Helper()
	t.Setenv(plan.PackageEnvVar, pkg)
	t.Setenv(plan.TagEnvVar, tag)
	t.Setenv(plan.NewVersionEnvVar, version)
}

// TestWireStepOutsideARun: with no DISPAT_* wiring in the environment the
// steps behave exactly as before — no scoping, no masking, no env.
func TestWireStepOutsideARun(t *testing.T) {
	clearRunEnv(t)
	a := New(t.TempDir(), &config.File{}, zerolog.Nop())
	var w WindowOptions

	env, err := a.wireStep(&w)
	require.NoError(t, err)
	assert.Nil(t, env)
	assert.Empty(t, a.ignoreTags)
	assert.Empty(t, w.Filter.Packages)
}

// TestWireStepScopesAndMasks: inside a run the step narrows to the invoking
// package and masks the run's own tag from baseline resolution.
func TestWireStepScopesAndMasks(t *testing.T) {
	clearRunEnv(t)
	wiredEnv(t, "core", "core@1.2.0", "1.2.0")
	a := New(t.TempDir(), &config.File{}, zerolog.Nop())
	var w WindowOptions

	env, err := a.wireStep(&w)
	require.NoError(t, err)
	require.NotNil(t, env)
	assert.Equal(t, []string{"core@1.2.0"}, a.ignoreTags)
	assert.Equal(t, []string{"core"}, w.Filter.Packages, "no filter of its own, so the run's package")
}

// TestWireStepKeepsAnExplicitFilter: a filter the invocation spelled itself
// outranks the implicit narrowing; the mask still applies.
func TestWireStepKeepsAnExplicitFilter(t *testing.T) {
	clearRunEnv(t)
	wiredEnv(t, "core", "core@1.2.0", "1.2.0")
	a := New(t.TempDir(), &config.File{}, zerolog.Nop())
	var w WindowOptions
	w.Filter.Spaces = []string{"libs"}

	env, err := a.wireStep(&w)
	require.NoError(t, err)
	require.NotNil(t, env)
	assert.Empty(t, w.Filter.Packages, "the explicit space term stands")
	assert.Equal(t, []string{"core@1.2.0"}, a.ignoreTags)
}

// TestWireStepRefusesAGarbageVersion: an environment that names a run but
// pins an unparseable version is E219, not a silent unwired fallback.
func TestWireStepRefusesAGarbageVersion(t *testing.T) {
	clearRunEnv(t)
	wiredEnv(t, "core", "core@1.2.0", "not-a-version")
	var logs bytes.Buffer
	a := New(t.TempDir(), &config.File{}, zerolog.New(&logs))
	var w WindowOptions

	_, err := a.wireStep(&w)
	require.Error(t, err)
	assert.Contains(t, logs.String(), plan.CodeStepUnalignable)
}

// stepEnvRelease builds the minimal plan alignStep reads: one package,
// releasing at the given version.
func stepEnvRelease(t *testing.T, name, version string) *plan.Plan {
	t.Helper()
	next, err := ccme.ParseVersion(version)
	require.NoError(t, err)
	rel := &plan.Release{
		Pkg:  &model.Package{Name: name, Space: &model.Space{Name: "libs"}},
		Next: next, Bump: ccme.BumpMinor, NewWork: true, Channel: channelOfVersion(next),
	}
	return &plan.Plan{Order: []string{name}, Releases: map[string]*plan.Release{name: rel}}
}

// TestAlignStepAgreement: a replan that matches the run passes through
// silently, no correction, no warning.
func TestAlignStepAgreement(t *testing.T) {
	var logs bytes.Buffer
	a := New(t.TempDir(), &config.File{}, zerolog.New(&logs))
	pl := stepEnvRelease(t, "core", "1.2.0")
	env := &runEnv{pkg: "core", tag: "core@1.2.0", next: mustVersion(t, "1.2.0")}

	require.NoError(t, a.alignStep(pl, env))
	assert.NotContains(t, logs.String(), plan.CodeStepAligned)
}

// TestAlignStepCorrectsDrift: the run's version outranks the replan's, the
// correction is W228, and the aligned release renders the run's own tag.
func TestAlignStepCorrectsDrift(t *testing.T) {
	var logs bytes.Buffer
	a := New(t.TempDir(), &config.File{}, zerolog.New(&logs))
	pl := stepEnvRelease(t, "core", "1.3.0")
	env := &runEnv{pkg: "core", tag: "core@1.2.0", next: mustVersion(t, "1.2.0")}

	require.NoError(t, a.alignStep(pl, env))
	assert.Contains(t, logs.String(), plan.CodeStepAligned)
	assert.Equal(t, "1.2.0", pl.Releases["core"].Next.String())
	assert.Equal(t, "core@1.2.0", pl.Releases["core"].TagName())
}

// TestAlignStepRefusesTheUnalignable: a run releasing a package the step's
// plan does not, and a version rendering a foreign tag, are both E219 with
// nothing written.
func TestAlignStepRefusesTheUnalignable(t *testing.T) {
	var logs bytes.Buffer
	a := New(t.TempDir(), &config.File{}, zerolog.New(&logs))

	pl := stepEnvRelease(t, "core", "1.2.0")
	err := a.alignStep(pl, &runEnv{pkg: "ghost", tag: "ghost@1.0.0", next: mustVersion(t, "1.0.0")})
	require.Error(t, err)
	assert.Contains(t, logs.String(), plan.CodeStepUnalignable)

	// The run's tag disagrees with what the aligned version renders: a
	// tagFormat changed mid-run, which no alignment can paper over.
	err = a.alignStep(pl, &runEnv{pkg: "core", tag: "elsewhere@1.2.0", next: mustVersion(t, "1.2.0")})
	require.Error(t, err)
}

// TestStepEnvReadsTheRunsUpdates: the DISPAT_UPDATED_* listing comes back as
// the run's provider movements, in listing order, raw names from _NAME.
func TestStepEnvReadsTheRunsUpdates(t *testing.T) {
	clearRunEnv(t)
	wiredEnv(t, "app", "app@1.2.0", "1.2.0")
	t.Setenv(plan.UpdatedPackagesEnvVar, "CORE MY_LIB")
	t.Setenv(plan.UpdatedEnvPrefix+"CORE_NAME", "core")
	t.Setenv(plan.UpdatedEnvPrefix+"CORE_OLD_VERSION", "1.0.0")
	t.Setenv(plan.UpdatedEnvPrefix+"CORE_NEW_VERSION", "1.1.0")
	t.Setenv(plan.UpdatedEnvPrefix+"MY_LIB_NAME", "my.lib")
	t.Setenv(plan.UpdatedEnvPrefix+"MY_LIB_OLD_VERSION", "2.0.0")
	t.Setenv(plan.UpdatedEnvPrefix+"MY_LIB_NEW_VERSION", "2.0.1")

	env, err := stepRunEnv()
	require.NoError(t, err)
	require.NotNil(t, env)
	assert.True(t, env.updatesListed)
	require.Len(t, env.updates, 2)
	assert.Equal(t, "core", env.updates[0].Name)
	assert.Equal(t, "1.0.0", env.updates[0].From.String())
	assert.Equal(t, "1.1.0", env.updates[0].To.String())
	assert.Equal(t, "my.lib", env.updates[1].Name)
}

// TestStepEnvEmptyListingIsStillAListing: an empty DISPAT_UPDATED_PACKAGES
// says "no live updates" — distinguished from an environment that carries no
// listing at all, which says nothing.
func TestStepEnvEmptyListingIsStillAListing(t *testing.T) {
	clearRunEnv(t)
	wiredEnv(t, "app", "app@1.2.0", "1.2.0")
	t.Setenv(plan.UpdatedPackagesEnvVar, "")

	env, err := stepRunEnv()
	require.NoError(t, err)
	assert.True(t, env.updatesListed)
	assert.Empty(t, env.updates)

	require.NoError(t, os.Unsetenv(plan.UpdatedPackagesEnvVar))
	env, err = stepRunEnv()
	require.NoError(t, err)
	assert.False(t, env.updatesListed)
}

// TestStepEnvRefusesAGarbageUpdateListing: a listing naming a key whose
// variables do not describe an update is E219, the same rule as an
// unparseable pinned version.
func TestStepEnvRefusesAGarbageUpdateListing(t *testing.T) {
	clearRunEnv(t)
	wiredEnv(t, "app", "app@1.2.0", "1.2.0")
	t.Setenv(plan.UpdatedPackagesEnvVar, "CORE")
	t.Setenv(plan.UpdatedEnvPrefix+"CORE_NAME", "core")
	t.Setenv(plan.UpdatedEnvPrefix+"CORE_OLD_VERSION", "not-a-version")
	t.Setenv(plan.UpdatedEnvPrefix+"CORE_NEW_VERSION", "1.1.0")
	var logs bytes.Buffer
	a := New(t.TempDir(), &config.File{}, zerolog.New(&logs))
	var w WindowOptions

	_, err := a.wireStep(&w)
	require.Error(t, err)
	assert.Contains(t, logs.String(), plan.CodeStepUnalignable)
}

// TestStepEnvReadsTheReleasingWorkspace: the releasing entries of the
// workspace listing come back for tag masking; an entry that does not parse
// is skipped rather than fatal, and a non-releasing entry is not read.
func TestStepEnvReadsTheReleasingWorkspace(t *testing.T) {
	clearRunEnv(t)
	wiredEnv(t, "app", "app@1.2.0", "1.2.0")
	t.Setenv(plan.WorkspacePackagesEnvVar, "CORE UTILS BROKEN")
	t.Setenv(plan.WorkspaceEnvPrefix+"CORE_NAME", "core")
	t.Setenv(plan.WorkspaceEnvPrefix+"CORE_VERSION", "1.1.0")
	t.Setenv(plan.WorkspaceEnvPrefix+"CORE_RELEASING", "true")
	t.Setenv(plan.WorkspaceEnvPrefix+"UTILS_NAME", "utils")
	t.Setenv(plan.WorkspaceEnvPrefix+"UTILS_VERSION", "0.3.0")
	t.Setenv(plan.WorkspaceEnvPrefix+"UTILS_RELEASING", "false")
	t.Setenv(plan.WorkspaceEnvPrefix+"BROKEN_NAME", "broken")
	t.Setenv(plan.WorkspaceEnvPrefix+"BROKEN_VERSION", "not-a-version")
	t.Setenv(plan.WorkspaceEnvPrefix+"BROKEN_RELEASING", "true")

	env, err := stepRunEnv()
	require.NoError(t, err)
	require.Len(t, env.releasing, 1)
	assert.Equal(t, "core", env.releasing[0].name)
	assert.Equal(t, "1.1.0", env.releasing[0].version.String())
}

// TestAlignStepAlignsDriftedUpdates: the run's updates listing outranks the
// replan's, the correction is W228, and a matching listing passes silently.
func TestAlignStepAlignsDriftedUpdates(t *testing.T) {
	var logs bytes.Buffer
	a := New(t.TempDir(), &config.File{}, zerolog.New(&logs))
	pl := stepEnvRelease(t, "app", "1.2.0")
	pl.Releases["app"].Updates = []plan.ProviderUpdate{
		{Name: "core", From: mustVersion(t, "1.1.0"), To: mustVersion(t, "1.2.0")}}
	run := []plan.ProviderUpdate{
		{Name: "core", From: mustVersion(t, "1.0.0"), To: mustVersion(t, "1.1.0")},
		{Name: "utils", From: mustVersion(t, "0.2.0"), To: mustVersion(t, "0.3.0")}}
	env := &runEnv{pkg: "app", tag: "app@1.2.0", next: mustVersion(t, "1.2.0"),
		updates: run, updatesListed: true}

	require.NoError(t, a.alignStep(pl, env))
	assert.Contains(t, logs.String(), plan.CodeStepAligned)
	assert.Equal(t, run, pl.Releases["app"].Updates)

	logs.Reset()
	require.NoError(t, a.alignStep(pl, env))
	assert.NotContains(t, logs.String(), plan.CodeStepAligned, "an aligned listing stays silent")
}

// TestAlignStepWithoutAListingKeepsTheReplans: an environment carrying no
// updates listing says nothing about them, and the replan's stand.
func TestAlignStepWithoutAListingKeepsTheReplans(t *testing.T) {
	a := New(t.TempDir(), &config.File{}, zerolog.Nop())
	pl := stepEnvRelease(t, "app", "1.2.0")
	planned := []plan.ProviderUpdate{
		{Name: "core", From: mustVersion(t, "1.0.0"), To: mustVersion(t, "1.1.0")}}
	pl.Releases["app"].Updates = planned
	env := &runEnv{pkg: "app", tag: "app@1.2.0", next: mustVersion(t, "1.2.0")}

	require.NoError(t, a.alignStep(pl, env))
	assert.Equal(t, planned, pl.Releases["app"].Updates)
}

// TestReadExportPrefersTheOutputFile: an export appended to $DISPAT_OUTPUT in
// the same script wins over the variable the stage inherited.
func TestReadExportPrefersTheOutputFile(t *testing.T) {
	clearRunEnv(t)
	out := filepath.Join(t.TempDir(), "output")
	require.NoError(t, os.WriteFile(out,
		[]byte("IMAGE=x\nDISPAT_EXPORT_GITHUB=/dist/a.tgz /dist/b.tgz\n"), 0o644))
	t.Setenv(plan.GitHubExport, "/stale/old.tgz")
	t.Setenv("DISPAT_OUTPUT", out)
	t.Setenv(plan.PackageEnvVar, "core")

	a := New(t.TempDir(), &config.File{}, zerolog.Nop())
	e := a.readExport([]string{"core"})
	assert.True(t, e.covers("core"))
	assert.Equal(t, "/dist/a.tgz /dist/b.tgz", e.value, "the file's last word wins over the environment")
}

// TestReadExportDeduplicates: a path restated across the environment and the
// output file, or within one export line, is one attachment.
func TestReadExportDeduplicates(t *testing.T) {
	clearRunEnv(t)
	out := filepath.Join(t.TempDir(), "output")
	require.NoError(t, os.WriteFile(out,
		[]byte("DISPAT_EXPORT_GITHUB=/dist/a.tgz /dist/b.tgz /dist/a.tgz\n"), 0o644))
	t.Setenv(plan.GitHubExport, "/dist/a.tgz")
	t.Setenv("DISPAT_OUTPUT", out)
	t.Setenv(plan.PackageEnvVar, "core")

	a := New(t.TempDir(), &config.File{}, zerolog.Nop())
	e := a.readExport([]string{"core"})
	assert.Equal(t, "/dist/a.tgz /dist/b.tgz", e.value, "one path, one attachment, whatever restated it")
}

func mustVersion(t *testing.T, s string) ccme.Version {
	t.Helper()
	v, err := ccme.ParseVersion(s)
	require.NoError(t, err)
	return v
}
