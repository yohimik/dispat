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
	"github.com/yohimik/dispat/pkg/ccme"
	"github.com/yohimik/dispat/pkg/manifest"
	"github.com/yohimik/dispat/pkg/writer"

	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// arRepo lays out a two-package workspace on disk — core, and web depending on
// it — and returns an App over it plus the plan. core is releasing to 2.0.0;
// web is not, so the two together cover the releasing and the unchanged case.
func arRepo(t *testing.T) (*App, *plan.Plan, *bytes.Buffer) {
	t.Helper()
	root := t.TempDir()
	space := &model.Space{Name: "libs"}
	pl := &plan.Plan{Releases: map[string]*plan.Release{}, Providers: map[string][]string{"web": {"core"}}}
	for _, name := range []string{"core", "web"} {
		pl.Releases[name] = &plan.Release{
			Pkg:     &model.Package{Name: name, Dir: filepath.Join(root, "packages", name), Space: space},
			Current: ccme.Version{Major: 1},
		}
		pl.Order = append(pl.Order, name)
	}
	pl.Releases["core"].Pinned = true // releasing
	pl.Releases["core"].Next = ccme.Version{Major: 2}

	seedAt(t, root, "packages/core/package.json", `{"name": "@acme/core", "version": "1.0.0"}`)
	seedAt(t, root, "packages/web/package.json",
		`{"name": "@acme/web", "version": "1.0.0", "dependencies": {"@acme/core": "^1.0.0", "left-pad": "^1.0.0"}}`)

	buf := &bytes.Buffer{}
	cfg := &config.File{Spaces: map[string]config.SpaceConfig{"libs": {Path: "packages"}}, BuildConcurrency: 2}
	return New(root, cfg, zerolog.New(buf)), pl, buf
}

func arSeed(t *testing.T, a *App, rel, body string) {
	t.Helper()
	seedAt(t, a.root, rel, body)
}

func seedAt(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
}

func arRead(t *testing.T, a *App, rel string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(a.root, filepath.FromSlash(rel)))
	require.NoError(t, err)
	return string(body)
}

func arSet(name, rng string) writer.Edit {
	return writer.Edit{Name: name, Kind: manifest.KindDependencies, Range: rng}
}

// arRun sweeps the whole workspace, which is what --since all does, so a test
// does not need a git history to reach both packages.
func arRun(t *testing.T, a *App, pl *plan.Plan, opts AutoReplaceOptions) error {
	t.Helper()
	opts.Window.Since = SinceAll
	work, err := a.newReplaceWork(context.Background(), pl, opts)
	if err != nil {
		return err
	}
	covered, err := a.coveredPackages(context.Background(), pl, opts.Window)
	require.NoError(t, err)
	_, drainErr := a.runSweep(context.Background(), pl, covered, work, sweepOptions{OnError: opts.OnError})
	require.NoError(t, drainErr)
	if opts.Strict {
		return work.stale()
	}
	return nil
}

// TestAutoReplaceEditsEveryCoveredPackage: one --set reaches every package that
// declares the dependency, and leaves the ones that do not alone.
func TestAutoReplaceEditsEveryCoveredPackage(t *testing.T) {
	a, pl, _ := arRepo(t)
	require.NoError(t, arRun(t, a, pl, AutoReplaceOptions{Edits: []writer.Edit{arSet("@acme/core", "^2.0.0")}}))
	assert.Contains(t, arRead(t, a, "packages/web/package.json"), `"@acme/core": "^2.0.0"`)
	assert.Contains(t, arRead(t, a, "packages/web/package.json"), `"left-pad": "^1.0.0"`,
		"an untouched dependency keeps its range")
	assert.Contains(t, arRead(t, a, "packages/core/package.json"), `"version": "1.0.0"`,
		"a package that declares nothing is left byte for byte alone")
}

// TestAutoReplaceVersionPlaceholder: {version} in a range is the named
// package's planned version, and in --set-version the covered package's own.
func TestAutoReplaceVersionPlaceholder(t *testing.T) {
	a, pl, _ := arRepo(t)
	require.NoError(t, arRun(t, a, pl, AutoReplaceOptions{
		Version: VersionPlaceholder,
		Edits:   []writer.Edit{arSet("@acme/core", "^"+VersionPlaceholder)},
	}))
	assert.Contains(t, arRead(t, a, "packages/web/package.json"), `"@acme/core": "^2.0.0"`,
		"the provider's planned version")
	assert.Contains(t, arRead(t, a, "packages/core/package.json"), `"version": "2.0.0"`,
		"a releasing package writes the version it is heading for")
	assert.Contains(t, arRead(t, a, "packages/web/package.json"), `"version": "1.0.0"`,
		"a package with nothing pending keeps the version it has")
}

// TestAutoReplaceRejectsAnUnresolvablePlaceholder: writing "{version}" into a
// manifest for someone to find later is worse than refusing.
func TestAutoReplaceRejectsAnUnresolvablePlaceholder(t *testing.T) {
	a, pl, _ := arRepo(t)
	err := arRun(t, a, pl, AutoReplaceOptions{Edits: []writer.Edit{arSet("left-pad", "^"+VersionPlaceholder)}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "names no package in this workspace")
	assert.Contains(t, arRead(t, a, "packages/web/package.json"), `"left-pad": "^1.0.0"`,
		"nothing was written before the refusal")
}

// TestAutoReplaceOnlyUpdated: an edit naming a package this run does not
// release is dropped, and one naming a releasing package is kept.
func TestAutoReplaceOnlyUpdated(t *testing.T) {
	a, pl, buf := arRepo(t)
	require.NoError(t, arRun(t, a, pl, AutoReplaceOptions{OnlyUpdated: true,
		Edits: []writer.Edit{arSet("@acme/core", "^2.0.0"), arSet("left-pad", "^9.9.9")}}))
	web := arRead(t, a, "packages/web/package.json")
	assert.Contains(t, web, `"@acme/core": "^2.0.0"`, "core is releasing, so its edit stands")
	assert.Contains(t, web, `"left-pad": "^1.0.0"`, "left-pad is no package of this run")
	assert.Contains(t, buf.String(), "does not name a package this run updates")
}

func TestAutoReplaceOnlyUpdatedCanEmptyTheInvocation(t *testing.T) {
	a, pl, _ := arRepo(t)
	work, err := a.newReplaceWork(context.Background(), pl,
		AutoReplaceOptions{OnlyUpdated: true, Edits: []writer.Edit{arSet("left-pad", "^9.9.9")}})
	require.NoError(t, err)
	assert.True(t, work.nothingToWrite(), "a run updating none of the named packages writes nothing")
}

// TestAutoReplaceRemovesAndAddsARedirect: --replace is the writer's, applied
// across the selection.
func TestAutoReplaceRedirects(t *testing.T) {
	a, pl, _ := arRepo(t)
	require.NoError(t, arRun(t, a, pl, AutoReplaceOptions{
		Replacements: []writer.Replacement{{Name: "@acme/core", Path: "../core"}}}))
	assert.Contains(t, arRead(t, a, "packages/web/package.json"), "file:../core")

	require.NoError(t, arRun(t, a, pl, AutoReplaceOptions{
		Replacements: []writer.Replacement{{Name: "@acme/core"}}}))
	assert.NotContains(t, arRead(t, a, "packages/web/package.json"), "file:../core",
		"an empty path removes the redirect again")
}

// TestAutoReplaceStrictIsAskedAcrossTheSweep: an edit missing from one
// package's manifest is the ordinary case; an edit no manifest anywhere
// declares is the stale one.
func TestAutoReplaceStrictIsAskedAcrossTheSweep(t *testing.T) {
	a, pl, _ := arRepo(t)
	assert.NoError(t, arRun(t, a, pl, AutoReplaceOptions{Strict: true,
		Edits: []writer.Edit{arSet("@acme/core", "^2.0.0")}}),
		"core does not declare it, web does: the edit landed")

	err := arRun(t, a, pl, AutoReplaceOptions{Strict: true,
		Edits: []writer.Edit{arSet("nowhere", "^2.0.0")}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "1 edit(s) matched no manifest")
}

// TestAutoReplaceStrictAcceptsAnAlreadyCorrectRange: a re-run writes nothing
// and must not then report the edit as stale.
func TestAutoReplaceStrictAcceptsAnAlreadyCorrectRange(t *testing.T) {
	a, pl, _ := arRepo(t)
	edits := []writer.Edit{arSet("@acme/core", "^2.0.0")}
	require.NoError(t, arRun(t, a, pl, AutoReplaceOptions{Edits: edits}))
	assert.NoError(t, arRun(t, a, pl, AutoReplaceOptions{Edits: edits, Strict: true}),
		"the second pass changes nothing and is still clean")
}

// TestAutoReplaceScopeAll reaches a nested manifest the root scope does not.
func TestAutoReplaceScopeAll(t *testing.T) {
	a, pl, _ := arRepo(t)
	arSeed(t, a, "packages/web/example/package.json",
		`{"name": "example", "version": "0.0.1", "dependencies": {"@acme/core": "^1.0.0"}}`)
	edits := []writer.Edit{arSet("@acme/core", "^2.0.0")}

	require.NoError(t, arRun(t, a, pl, AutoReplaceOptions{Edits: edits}))
	assert.Contains(t, arRead(t, a, "packages/web/example/package.json"), `"@acme/core": "^1.0.0"`,
		"the root scope stops at the package folder")

	require.NoError(t, arRun(t, a, pl, AutoReplaceOptions{Edits: edits, Manifests: string(model.ScopeAll)}))
	assert.Contains(t, arRead(t, a, "packages/web/example/package.json"), `"@acme/core": "^2.0.0"`)
}

// TestAutoReplaceOwnVersionStaysOnTheRootManifests: a nested example has its
// own version story, whatever the scope.
func TestAutoReplaceOwnVersionStaysOnTheRootManifests(t *testing.T) {
	a, pl, _ := arRepo(t)
	arSeed(t, a, "packages/core/example/package.json", `{"name": "example", "version": "0.0.1"}`)
	require.NoError(t, arRun(t, a, pl, AutoReplaceOptions{
		Version: VersionPlaceholder, Manifests: string(model.ScopeAll)}))
	assert.Contains(t, arRead(t, a, "packages/core/package.json"), `"version": "2.0.0"`)
	assert.Contains(t, arRead(t, a, "packages/core/example/package.json"), `"version": "0.0.1"`)
}

// TestAutoReplaceLeavesANestedPackageToItsOwner: under the all scope two
// packages whose folders nest would otherwise edit one file from two
// goroutines at once.
func TestAutoReplaceLeavesANestedPackageToItsOwner(t *testing.T) {
	a, pl, _ := arRepo(t)
	// web now lives inside core's folder, and declares core.
	nested := filepath.Join(a.root, "packages", "core", "web")
	pl.Releases["web"].Pkg.Dir = nested
	arSeed(t, a, "packages/core/web/package.json",
		`{"name": "@acme/web", "version": "1.0.0", "dependencies": {"@acme/core": "^1.0.0"}}`)

	work, err := a.newReplaceWork(context.Background(), pl, AutoReplaceOptions{
		Manifests: string(model.ScopeAll), Edits: []writer.Edit{arSet("@acme/core", "^2.0.0")}})
	require.NoError(t, err)
	mans, err := work.manifests(context.Background(), pl.Releases["core"])
	require.NoError(t, err)
	require.Len(t, mans, 1)
	assert.Equal(t, "package.json", mans[0].Path, "web's manifest is left to web")
}

func TestAutoReplaceSkipsAPackageWithNothingWritable(t *testing.T) {
	a, pl, buf := arRepo(t)
	// A folder with no manifest any writer covers.
	require.NoError(t, os.RemoveAll(filepath.Join(a.root, "packages", "core", "package.json")))
	arSeed(t, a, "packages/core/notes.txt", "nothing to parse")

	work, err := a.newReplaceWork(context.Background(), pl,
		AutoReplaceOptions{Edits: []writer.Edit{arSet("@acme/core", "^2.0.0")}})
	require.NoError(t, err)
	task, err := work.resolve(context.Background(), pl.Releases["core"])
	require.NoError(t, err)
	assert.Nil(t, task, "a package with nothing writable is a no-op")
	assert.Contains(t, buf.String(), "no manifest here that this command can write")
}

func TestAutoReplaceRejectsAnUnknownScope(t *testing.T) {
	a, pl, _ := arRepo(t)
	_, err := a.newReplaceWork(context.Background(), pl, AutoReplaceOptions{Manifests: "none"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unknown manifest scope "none"`)
}

func TestAutoReplaceReportsAPathRelativeToTheRoot(t *testing.T) {
	a, _, _ := arRepo(t)
	w := &replaceWork{app: a}
	assert.Equal(t, "packages/web/package.json",
		w.relative(filepath.Join(a.root, "packages", "web", "package.json")))
}

func TestPlannedVersionIsWhatThePackageEndsUpAt(t *testing.T) {
	_, pl, _ := arRepo(t)
	assert.Equal(t, "2.0.0", plannedVersion(pl.Releases["core"]), "a releasing package is heading somewhere")
	assert.Equal(t, "1.0.0", plannedVersion(pl.Releases["web"]), "an unchanged one stays where it is")
}

func TestNeedsWorkspaceOnlyWhenSomethingAsksForIt(t *testing.T) {
	assert.False(t, AutoReplaceOptions{Edits: []writer.Edit{arSet("a", "1.0.0")}}.needsWorkspace())
	assert.True(t, AutoReplaceOptions{OnlyUpdated: true}.needsWorkspace())
	assert.True(t, AutoReplaceOptions{Version: VersionPlaceholder}.needsWorkspace())
	assert.True(t, AutoReplaceOptions{Edits: []writer.Edit{arSet("a", "^"+VersionPlaceholder)}}.needsWorkspace())
}
