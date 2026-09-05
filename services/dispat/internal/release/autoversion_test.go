// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package release

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/ccme"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// Auto-versioning runs against real manifests: these tests lay packages out
// in a temp folder, wire an AutoVersion space into a hand-built plan and
// assert on the file bytes the run leaves behind — plus the diagnostics it
// logs on the way.

// allKinds is the resolved default kind set.
func allKinds() map[model.DepKind]bool {
	return map[model.DepKind]bool{
		model.KindDependencies: true, model.KindDevDependencies: true,
		model.KindPeerDependencies: true, model.KindOptionalDependencies: true,
	}
}

// avSpace is a one-space fixture with auto-versioning on and inert scripts.
func avSpace(av *model.AutoVersion) *model.Space {
	return &model.Space{Name: "libs", BuildScript: []string{"build"}, PublishScript: []string{"publish"}, AutoVersion: av}
}

// avPlan builds a plan over real package folders under root: every named
// package releases 1.0.0 -> 1.0.1 in the given space unless the test mutates
// its release afterwards.
func avPlan(root string, space *model.Space, names ...string) *plan.Plan {
	p := &plan.Plan{Releases: map[string]*plan.Release{}, Providers: map[string][]string{}}
	for _, n := range names {
		rel := &plan.Release{
			Pkg:         &model.Package{Name: n, Dir: filepath.Join(root, n), Space: space},
			Current:     ccme.Version{Major: 1},
			Baseline:    ccme.Version{Major: 1},
			HasBaseline: true,
			OwnBump:     ccme.BumpPatch,
			Bump:        ccme.BumpPatch,
			NewWork:     true,
			Units: []*ccme.Unit{{
				Header: ccme.Header{Type: "fix", Description: "own change"},
				Bump:   ccme.BumpPatch, Valid: true,
			}},
		}
		// A stable-line plan's fresh changeset is its whole changeset.
		rel.FreshUnits = rel.Units
		rel.Next = rel.Current.Bumped(rel.Bump)
		p.Releases[n] = rel
		p.Order = append(p.Order, n)
	}
	// Updates is deliberately not filled here: every caller declares the
	// edges afterwards, so each one calls fillUpdates once it has.
	return p
}

func seedFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func fileText(t *testing.T, root, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	require.NoError(t, err)
	return string(data)
}

func TestAutoVersionRewritesRangesAndOwnVersion(t *testing.T) {
	root := t.TempDir()
	space := avSpace(&model.AutoVersion{Kinds: allKinds(), WriteVersion: true})
	seedFile(t, root, "core/package.json", `{"name": "@acme/core", "version": "1.0.0"}`)
	seedFile(t, root, "web/package.json",
		`{"name": "@acme/web", "version": "1.0.0", "dependencies": {"@acme/core": "workspace:*"}}`)
	p := avPlan(root, space, "core", "web")

	r := &fakeRunner{}
	res := newExecutor(execSpec{Runner: r, Build: 2, Publish: 2}).Run(context.Background(), p)
	require.Equal(t, StatusPublished, res["web"].Status, "%v", res["web"].Err)

	web := fileText(t, root, "web/package.json")
	// No DueTo anywhere, yet the version stage ran natively: the caret
	// default reconciled the range and the own version advanced (§9.4, §12.4).
	assert.Contains(t, web, `"@acme/core": "^1.0.1"`)
	assert.Contains(t, web, `"version": "1.0.1"`)
	core := fileText(t, root, "core/package.json")
	assert.Contains(t, core, `"version": "1.0.1"`, "providers auto-version themselves too")
}

func TestAutoVersionPolicyFilters(t *testing.T) {
	root := t.TempDir()
	space := avSpace(&model.AutoVersion{
		Kinds: map[model.DepKind]bool{model.KindDependencies: true}, // devDependencies excluded
		Only:  map[string]bool{"core": true},                        // tools excluded
		Match: []string{"workspace:*", "workspace:^"},               // pins excluded
		Range: "exact",
	})
	seedFile(t, root, "core/package.json", `{"name": "@acme/core"}`)
	seedFile(t, root, "tools/package.json", `{"name": "@acme/tools"}`)
	seedFile(t, root, "web/package.json", `{
  "name": "@acme/web",
  "dependencies": {
    "@acme/core": "workspace:*",
    "@acme/tools": "workspace:*",
    "left-pad": "^1.3.0"
  },
  "devDependencies": {"@acme/core": "workspace:*"},
  "peerDependencies": {"@acme/core": "1.0.0"}
}`)
	p := avPlan(root, space, "core", "tools", "web")

	r := &fakeRunner{}
	res := newExecutor(execSpec{Runner: r, Build: 2, Publish: 2}).Run(context.Background(), p)
	require.Equal(t, StatusPublished, res["web"].Status, "%v", res["web"].Err)

	web := fileText(t, root, "web/package.json")
	assert.Contains(t, web, `"@acme/core": "1.0.1"`, "eligible range rewritten exactly")
	assert.Contains(t, web, `"@acme/tools": "workspace:*"`, "only: filter protects tools")
	assert.Contains(t, web, `"left-pad": "^1.3.0"`, "non-workspace deps never touched")
	assert.Contains(t, web, `"devDependencies": {"@acme/core": "workspace:*"}`, "kind filter holds")
	assert.Contains(t, web, `"peerDependencies": {"@acme/core": "1.0.0"}`, "match filter protects the pin")
}

func TestAutoVersionLiteralRangeAndTemplate(t *testing.T) {
	root := t.TempDir()
	space := avSpace(&model.AutoVersion{Kinds: allKinds(), Range: "workspace:*"})
	seedFile(t, root, "core/package.json", `{"name": "@acme/core"}`)
	seedFile(t, root, "web/package.json",
		`{"name": "@acme/web", "dependencies": {"@acme/core": "1.0.0"}}`)
	p := avPlan(root, space, "core", "web")
	res := newExecutor(execSpec{Runner: &fakeRunner{}, Build: 2, Publish: 2}).Run(context.Background(), p)
	require.Equal(t, StatusPublished, res["web"].Status, "%v", res["web"].Err)
	assert.Contains(t, fileText(t, root, "web/package.json"), `"@acme/core": "workspace:*"`,
		"the opposite direction: normalising ranges TO a literal")

	assert.Equal(t, ">=2.0.0", RangeText(">={version}", "2.0.0", "npm"))
	assert.Equal(t, "~2.0.0", RangeText("tilde", "2.0.0", "npm"))
	assert.Equal(t, "v2.0.0", RangeText("caret", "2.0.0", "gomod"), "go.mod always gets exact canonical versions")
	assert.Equal(t, "==2.0.0", RangeText("caret", "2.0.0", "python"), "Python specifiers have no caret")
	assert.Equal(t, "2.0.0", RangeText("caret", "2.0.0", "docker"), "a Docker tag is a plain label, never a range")
	assert.Equal(t, "2.0.0", RangeText("exact", "2.0.0", "docker"))
	assert.Equal(t, "2.0.0-alpine", RangeText("{version}-alpine", "2.0.0", "docker"),
		"a template still passes through, which is how a variant tag is spelled")
}

func TestAutoVersionDocker(t *testing.T) {
	root := t.TempDir()
	space := avSpace(&model.AutoVersion{Kinds: allKinds(), WriteVersion: true})
	// The base image declares nothing readable, so it answers to the name its
	// consumers use — exactly what manifestNames is for.
	seedFile(t, root, "svc/Dockerfile",
		"FROM registry.example.com/core:1.0.0 AS build\nCOPY --from=build /a /b\n")
	seedFile(t, root, "svc/compose.yaml",
		"services:\n  svc:\n    build: .\n    image: registry.example.com/svc:1.0.0\n  base:\n    image: registry.example.com/core:1.0.0\n")
	p := avPlan(root, space, "core", "svc")
	p.Releases["core"].Pkg.ManifestNames = []string{"registry.example.com/core"}
	// The two versions must differ, or the assertions below could not tell the
	// package's own version from its provider's.
	core := p.Releases["core"]
	core.Bump = ccme.BumpMinor
	core.Next = core.Current.Bumped(core.Bump)

	res := newExecutor(execSpec{Runner: &fakeRunner{}, Build: 2, Publish: 2}).Run(context.Background(), p)
	require.Equal(t, StatusPublished, res["svc"].Status, "%v", res["svc"].Err)

	dockerfile := fileText(t, root, "svc/Dockerfile")
	assert.Contains(t, dockerfile, "FROM registry.example.com/core:1.1.0 AS build",
		"the base image tag follows the provider's new version")
	assert.Contains(t, dockerfile, "COPY --from=build /a /b", "a stage alias is not an image")

	compose := fileText(t, root, "svc/compose.yaml")
	assert.Contains(t, compose, "image: registry.example.com/svc:1.0.1",
		"the built service carries the package's own version")
	assert.Contains(t, compose, "image: registry.example.com/core:1.1.0",
		"the pulled service carries the provider's")
}

func TestAutoVersionGoMod(t *testing.T) {
	root := t.TempDir()
	space := avSpace(&model.AutoVersion{Kinds: allKinds(), WriteVersion: true})
	seedFile(t, root, "core/go.mod", "module example.com/core\n\ngo 1.25.0\n")
	seedFile(t, root, "svc/go.mod",
		"module example.com/svc\n\ngo 1.25.0\n\nrequire example.com/core v1.0.0\n\nreplace example.com/core => ../core\n")
	p := avPlan(root, space, "core", "svc")
	res := newExecutor(execSpec{Runner: &fakeRunner{}, Build: 2, Publish: 2}).Run(context.Background(), p)
	require.Equal(t, StatusPublished, res["svc"].Status, "%v", res["svc"].Err)
	svc := fileText(t, root, "svc/go.mod")
	assert.Contains(t, svc, "require example.com/core v1.0.1")
	assert.Contains(t, svc, "replace example.com/core => ../core", "replace untouched")
}

func TestAutoVersionCatchUpAndDriftDiagnostics(t *testing.T) {
	root := t.TempDir()
	space := avSpace(&model.AutoVersion{Kinds: allKinds(), WriteVersion: true})
	seedFile(t, root, "core/package.json", `{"name": "@acme/core", "version": "1.0.0"}`)
	// web's manifest lags core's released baseline AND drifted its own
	// version away from the tags.
	seedFile(t, root, "web/package.json",
		`{"name": "@acme/web", "version": "9.9.9", "dependencies": {"@acme/core": "^0.9.0"}}`)
	p := avPlan(root, space, "core", "web")
	// core is not releasing this run: baseline 1.0.0 from an earlier run.
	core := p.Releases["core"]
	core.OwnBump, core.Bump, core.NewWork, core.Units, core.FreshUnits = ccme.BumpNone, ccme.BumpNone, false, nil, nil
	core.Next = core.Current

	var logBuf bytes.Buffer
	e := newExecutor(execSpec{Runner: &fakeRunner{}, Build: 2, Publish: 2})
	e.Log = syncedLog(&logBuf)
	res := e.Run(context.Background(), p)
	require.Equal(t, StatusPublished, res["web"].Status, "%v", res["web"].Err)

	web := fileText(t, root, "web/package.json")
	assert.Contains(t, web, `"@acme/core": "^1.0.0"`, "caught up to the baseline of a non-releasing provider")
	assert.Contains(t, web, `"version": "1.0.1"`, "computed version written over the drifted one")
	logs := logBuf.String()
	assert.Contains(t, logs, plan.CodeRangeCatchUp, "W197: reconciled against a provider outside this run")
	assert.Contains(t, logs, plan.CodeManifestVersionDrift, "W192: manifest version disagreed with the baseline")
}

func TestAutoVersionStableOverPrereleaseW203(t *testing.T) {
	root := t.TempDir()
	space := avSpace(&model.AutoVersion{Kinds: allKinds()})
	seedFile(t, root, "core/package.json", `{"name": "@acme/core"}`)
	seedFile(t, root, "web/package.json",
		`{"name": "@acme/web", "dependencies": {"@acme/core": "workspace:*"}}`)
	p := avPlan(root, space, "core", "web")
	core := p.Releases["core"]
	core.Channel = "beta"
	core.Next = ccme.Version{Major: 1, Patch: 1, Prerelease: []string{"beta", "1"}}

	var logBuf bytes.Buffer
	e := newExecutor(execSpec{Runner: &fakeRunner{}, Build: 2, Publish: 2})
	e.Log = syncedLog(&logBuf)
	res := e.Run(context.Background(), p)
	require.Equal(t, StatusPublished, res["web"].Status, "%v", res["web"].Err)
	assert.Contains(t, fileText(t, root, "web/package.json"), `"@acme/core": "^1.0.1-beta.1"`)
	assert.Contains(t, logBuf.String(), plan.CodeStableOverPrerelease,
		"W203: a stable release now ranges over a prerelease provider")
}

func TestAutoVersionFailedProviderFallsBackToBaseline(t *testing.T) {
	// core releases but its build fails; web proceeds on its own bump and its
	// manifest must point at core's baseline, never the never-released Next.
	root := t.TempDir()
	space := avSpace(&model.AutoVersion{Kinds: allKinds()})
	seedFile(t, root, "core/package.json", `{"name": "@acme/core"}`)
	seedFile(t, root, "web/package.json",
		`{"name": "@acme/web", "dependencies": {"@acme/core": "workspace:*"}}`)
	p := avPlan(root, space, "core", "web")
	p.Providers["web"] = []string{"core"}
	fillUpdates(p)
	// No DueTo: web is not bumped *because of* core, so a core failure skips
	// nothing — but ordering still guarantees core's outcome is known first.

	r := &fakeRunner{fail: map[string]bool{"build " + filepath.Join(root, "core"): true}}
	res := newExecutor(execSpec{Runner: r, Build: 1, Publish: 1}).Run(context.Background(), p)
	require.Equal(t, StatusFailed, res["core"].Status)
	require.Equal(t, StatusPublished, res["web"].Status, "%v", res["web"].Err)
	assert.Contains(t, fileText(t, root, "web/package.json"), `"@acme/core": "^1.0.0"`,
		"failed provider: the baseline, not the planned version")
}

func TestAutoVersionSubstringNameMatch(t *testing.T) {
	// The "app" package has no parseable manifest at all, so no manifest name
	// maps onto it — nameMatch: substring matches the declared "@core/app"
	// by its last segment. Without the option the declaration is left alone.
	for _, substring := range []bool{true, false} {
		root := t.TempDir()
		space := avSpace(&model.AutoVersion{Kinds: allKinds(), NameSubstring: substring})
		seedFile(t, root, "app/artifact.bin", "not a manifest")
		seedFile(t, root, "web/package.json",
			`{"name": "@acme/web", "dependencies": {"@core/app": "workspace:*", "app-kit": "^9"}}`)
		p := avPlan(root, space, "app", "web")
		res := newExecutor(execSpec{Runner: &fakeRunner{}, Build: 2, Publish: 2}).Run(context.Background(), p)
		require.Equal(t, StatusPublished, res["web"].Status, "%v", res["web"].Err)
		web := fileText(t, root, "web/package.json")
		if substring {
			assert.Contains(t, web, `"@core/app": "^1.0.1"`, "last segment matched the folder name")
		} else {
			assert.Contains(t, web, `"@core/app": "workspace:*"`, "exact matching leaves the unknown name alone")
		}
		assert.Contains(t, web, `"app-kit": "^9"`, "a name merely containing the package name never matches")
	}
}

func TestAutoVersionRequirementsFile(t *testing.T) {
	// The line-by-line manifest shape end to end: a requirements.txt consumer
	// declared with the underscored spelling is reconciled to == the
	// provider's new version (Python keyword policies all pin).
	root := t.TempDir()
	space := avSpace(&model.AutoVersion{Kinds: allKinds()})
	seedFile(t, root, "core/pyproject.toml", "[project]\nname = \"acme-core\"\nversion = \"1.0.0\"\n")
	seedFile(t, root, "app/requirements.txt", "requests>=2.0\nAcme_Core==1.0.0  # workspace\n")
	p := avPlan(root, space, "core", "app")
	res := newExecutor(execSpec{Runner: &fakeRunner{}, Build: 2, Publish: 2}).Run(context.Background(), p)
	require.Equal(t, StatusPublished, res["app"].Status, "%v", res["app"].Err)
	got := fileText(t, root, "app/requirements.txt")
	assert.Equal(t, "requests>=2.0\nAcme_Core==1.0.1  # workspace\n", got,
		"only the workspace line's specifier changes; spelling and comment survive")
}

func TestSyncLockRunsBetweenVersionAndBuildSerialised(t *testing.T) {
	root := t.TempDir()
	space := avSpace(&model.AutoVersion{Kinds: allKinds(), WriteVersion: true, SyncLock: []string{"locksync"}})
	names := []string{"a", "b", "c"}
	for _, n := range names {
		seedFile(t, root, n+"/package.json", `{"name": "@acme/`+n+`", "version": "1.0.0"}`)
	}
	p := avPlan(root, space, names...)

	r := &fakeRunner{delay: 30 * time.Millisecond}
	res := newExecutor(execSpec{Runner: r, Build: 4, Publish: 4}).Run(context.Background(), p)
	for _, n := range names {
		require.Equal(t, StatusPublished, res[n].Status, "%s: %v", n, res[n].Err)
		dir := filepath.Join(root, n)
		assert.Less(t, r.indexOf("locksync "+dir), r.indexOf("build "+dir),
			"%s: syncLock precedes the build", n)
	}
	assert.Equal(t, 1, r.maxCur["locksync"], "syncLock defaults to strict serialisation")

	env := r.envPrefix(t, "locksync")
	assert.Contains(t, env, "DISPAT_STAGE=syncLock")
}

func TestSyncLockBudgetConfigurable(t *testing.T) {
	root := t.TempDir()
	space := avSpace(&model.AutoVersion{Kinds: allKinds(), WriteVersion: true, SyncLock: []string{"locksync"}, SyncLockConcurrency: 3})
	names := []string{"a", "b", "c"}
	for _, n := range names {
		seedFile(t, root, n+"/package.json", `{"name": "@acme/`+n+`", "version": "1.0.0"}`)
	}
	p := avPlan(root, space, names...)
	r := &fakeRunner{delay: 30 * time.Millisecond}
	res := newExecutor(execSpec{Runner: r, Build: 4, Publish: 4}).Run(context.Background(), p)
	for _, n := range names {
		require.Equal(t, StatusPublished, res[n].Status, "%s: %v", n, res[n].Err)
	}
	assert.Greater(t, r.maxCur["locksync"], 1, "a configured budget lifts the serialisation")
}

func TestSyncLockFailureFailsThePackageBeforeBuild(t *testing.T) {
	root := t.TempDir()
	space := avSpace(&model.AutoVersion{Kinds: allKinds(), WriteVersion: true, SyncLock: []string{"locksync"}})
	seedFile(t, root, "a/package.json", `{"name": "@acme/a", "version": "1.0.0"}`)
	p := avPlan(root, space, "a")
	dir := filepath.Join(root, "a")
	r := &fakeRunner{fail: map[string]bool{"locksync " + dir: true}}
	res := newExecutor(execSpec{Runner: r, Build: 1, Publish: 1}).Run(context.Background(), p)
	require.Equal(t, StatusFailed, res["a"].Status)
	assert.Equal(t, "syncLock", res["a"].FailedStage)
	assert.Equal(t, -1, r.indexOf("build "+dir), "the build never ran")
}

func TestSyncLockSkippedWhenNothingChanged(t *testing.T) {
	// The version stage rewrote no manifest (no version write, no workspace
	// dependency), so the syncLock task completes without spawning its
	// subprocess: a quiet release must not pay one lock regeneration per
	// package for nothing.
	root := t.TempDir()
	space := avSpace(&model.AutoVersion{Kinds: allKinds(), SyncLock: []string{"locksync"}})
	seedFile(t, root, "a/package.json", `{"name": "@acme/a", "version": "1.0.0"}`)
	p := avPlan(root, space, "a")
	r := &fakeRunner{}
	res := newExecutor(execSpec{Runner: r, Build: 1, Publish: 1}).Run(context.Background(), p)
	require.Equal(t, StatusPublished, res["a"].Status, "%v", res["a"].Err)
	assert.Equal(t, -1, r.indexOf("locksync "+filepath.Join(root, "a")),
		"no manifest changed: the syncLock subprocess must not run")
}

func TestAutoVersionSkipsUserScriptShortCircuitButNotNative(t *testing.T) {
	// The "no live updates -> skip version scripts" rule: when every DueTo
	// provider failed, the user version script stays silent but the native
	// reconciliation still runs (it writes baselines, not dead versions).
	root := t.TempDir()
	space := avSpace(&model.AutoVersion{Kinds: allKinds()})
	space.VersionScript = []string{"user-version"}
	seedFile(t, root, "core/package.json", `{"name": "@acme/core"}`)
	seedFile(t, root, "web/package.json",
		`{"name": "@acme/web", "dependencies": {"@acme/core": "workspace:*"}}`)
	p := avPlan(root, space, "core", "web")
	p.Providers["web"] = []string{"core"}
	p.Releases["web"].DueTo = []string{"core"}
	fillUpdates(p)

	coreDir := filepath.Join(root, "core")
	r := &fakeRunner{fail: map[string]bool{"build " + coreDir: true}}
	res := newExecutor(execSpec{Runner: r, Build: 1, Publish: 1}).Run(context.Background(), p)
	require.Equal(t, StatusFailed, res["core"].Status)
	require.Equal(t, StatusPublished, res["web"].Status, "%v", res["web"].Err)

	webDir := filepath.Join(root, "web")
	assert.Equal(t, -1, r.indexOf("user-version "+webDir), "user script skipped: nothing live to sync")
	assert.Contains(t, fileText(t, root, "web/package.json"), `"@acme/core": "^1.0.0"`,
		"native reconciliation still ran, against the baseline")
}

func TestAutoVersionOrderingStrings(t *testing.T) {
	assert.Equal(t, "syncLock", taskSyncLock.String())
	assert.True(t, strings.HasPrefix(stageTitle(taskSyncLock), "S"))
}

// syncedLog returns a logger writing into buf through zerolog's sync writer:
// the executor logs from concurrent task goroutines, and a bare bytes.Buffer
// is not a concurrency-safe sink.
func syncedLog(buf *bytes.Buffer) zerolog.Logger {
	return zerolog.New(zerolog.SyncWriter(buf))
}

func TestAutoVersionRewriteFailureFailsTheVersionStage(t *testing.T) {
	// A symlink manifest is refused before any write: the native step's error
	// must fail the package at the version stage, and with revertOnFail set the
	// reverter must be asked to roll the folder back. This remains effective
	// when the test process has root privileges.
	root := t.TempDir()
	space := avSpace(&model.AutoVersion{Kinds: allKinds(), WriteVersion: true})
	space.RevertOnFail = true
	seedFile(t, root, "target.json", `{"name": "@acme/a", "version": "1.0.0"}`)
	dir := filepath.Join(root, "a")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.Symlink(filepath.Join(root, "target.json"), filepath.Join(dir, "package.json")))

	rv := &fakeReverter{}
	e := newExecutor(execSpec{Runner: &fakeRunner{}, Build: 1, Publish: 1})
	e.Reverter = rv
	res := e.Run(context.Background(), avPlan(root, space, "a"))

	require.Equal(t, StatusFailed, res["a"].Status)
	assert.Equal(t, "version", res["a"].FailedStage, "auto-versioning fails the version stage")
	require.Error(t, res["a"].Err)
	assert.Equal(t, []string{dir}, rv.dirs, "revertOnFail rolls the failing folder back")
	assert.Equal(t, `{"name": "@acme/a", "version": "1.0.0"}`, fileText(t, root, "target.json"),
		"the symlink target is preserved")
}

func TestAutoVersionConverges(t *testing.T) {
	// The multi-run convention applied natively: a second release over the
	// same plan rewrites nothing — the manifest already carries the computed
	// version and range, so the file bytes are stable and syncLock stays
	// silent.
	root := t.TempDir()
	space := avSpace(&model.AutoVersion{Kinds: allKinds(), WriteVersion: true, SyncLock: []string{"locksync"}})
	seedFile(t, root, "core/package.json", `{"name": "@acme/core", "version": "1.0.0"}`)
	seedFile(t, root, "web/package.json",
		`{"name": "@acme/web", "version": "1.0.0", "dependencies": {"@acme/core": "^1.0.0"}}`)

	res := newExecutor(execSpec{Runner: &fakeRunner{}, Build: 2, Publish: 2}).
		Run(context.Background(), avPlan(root, space, "core", "web"))
	require.Equal(t, StatusPublished, res["web"].Status, "%v", res["web"].Err)
	first := fileText(t, root, "web/package.json")
	assert.Contains(t, first, `"@acme/core": "^1.0.1"`)

	r2 := &fakeRunner{}
	res = newExecutor(execSpec{Runner: r2, Build: 2, Publish: 2}).
		Run(context.Background(), avPlan(root, space, "core", "web"))
	require.Equal(t, StatusPublished, res["web"].Status, "%v", res["web"].Err)
	assert.Equal(t, first, fileText(t, root, "web/package.json"), "second run rewrites nothing")
	assert.Equal(t, -1, r2.indexOf("locksync "+filepath.Join(root, "web")),
		"nothing changed: the second run's syncLock is skipped")
}

func TestAutoVersionUnscheduledEdgeW221(t *testing.T) {
	// web's manifest depends on core and both release, but the config
	// declares no edge (empty plan.Providers): the rewrite is optimistic
	// about core's publish, which W221 must say out loud.
	root := t.TempDir()
	space := avSpace(&model.AutoVersion{Kinds: allKinds()})
	seedFile(t, root, "core/package.json", `{"name": "@acme/core"}`)
	seedFile(t, root, "web/package.json",
		`{"name": "@acme/web", "dependencies": {"@acme/core": "workspace:*"}}`)
	p := avPlan(root, space, "core", "web")

	var logBuf bytes.Buffer
	e := newExecutor(execSpec{Runner: &fakeRunner{}, Build: 2, Publish: 2})
	e.Log = syncedLog(&logBuf)
	res := e.Run(context.Background(), p)
	require.Equal(t, StatusPublished, res["web"].Status, "%v", res["web"].Err)
	assert.Contains(t, logBuf.String(), plan.CodeUnscheduledRewriteEdge,
		"a rewritten edge with no configured counterpart must be reported")

	// The counter-case: with the edge declared, no W221.
	seedFile(t, root, "web/package.json",
		`{"name": "@acme/web", "dependencies": {"@acme/core": "workspace:*"}}`)
	p = avPlan(root, space, "core", "web")
	p.Providers["web"] = []string{"core"}
	fillUpdates(p)
	logBuf.Reset()
	e = newExecutor(execSpec{Runner: &fakeRunner{}, Build: 2, Publish: 2})
	e.Log = syncedLog(&logBuf)
	res = e.Run(context.Background(), p)
	require.Equal(t, StatusPublished, res["web"].Status, "%v", res["web"].Err)
	assert.NotContains(t, logBuf.String(), plan.CodeUnscheduledRewriteEdge)
}

func TestAutoVersionWriteFailureFailsTheVersionStage(t *testing.T) {
	// A manifest the rewriter cannot replace (its folder refuses the temp
	// file) fails the version stage, and with it the package: half-synced
	// manifests must not build.
	if os.Getuid() == 0 {
		t.Skip("permission checks are meaningless as root")
	}
	root := t.TempDir()
	space := avSpace(&model.AutoVersion{Kinds: allKinds(), WriteVersion: true})
	seedFile(t, root, "core/package.json", `{"name": "core", "version": "1.0.0"}`)
	p := avPlan(root, space, "core")
	require.NoError(t, os.Chmod(filepath.Join(root, "core"), 0o555))
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(root, "core"), 0o755) })

	r := &fakeRunner{}
	res := newExecutor(execSpec{Runner: r, Tagger: &fakeTagger{}, Changelog: &fakeChangelog{}, Build: 1, Publish: 1}).Run(context.Background(), p)
	require.Equal(t, StatusFailed, res["core"].Status)
	assert.Equal(t, "version", res["core"].FailedStage)
	assert.Equal(t, 0, r.countPrefix("build"), "a failed version stage stops the package before its build")
}

func TestAutoVersionPackagesStandalone(t *testing.T) {
	// The standalone entry point reconciles exactly like the version stage,
	// reports what changed, and converges: a second pass rewrites nothing.
	root := t.TempDir()
	space := avSpace(&model.AutoVersion{Kinds: allKinds(), WriteVersion: true})
	seedFile(t, root, "core/package.json", `{"name": "core", "version": "1.0.0"}`)
	seedFile(t, root, "web/package.json", `{"name": "web", "version": "1.0.0", "dependencies": {"core": "^1.0.0"}}`)
	p := avPlan(root, space, "core", "web")
	p.Providers["web"] = []string{"core"}
	fillUpdates(p)

	changed, err := autoVersionPackages(p, []string{"core", "web"}, nil)
	require.NoError(t, err)
	assert.True(t, changed["core"], "the own-version write counts as a change")
	assert.True(t, changed["web"])
	assert.Contains(t, fileText(t, root, "web/package.json"), `"core": "^1.0.1"`)
	assert.Contains(t, fileText(t, root, "web/package.json"), `"version": "1.0.1"`)

	changed, err = autoVersionPackages(p, []string{"core", "web"}, nil)
	require.NoError(t, err)
	assert.Empty(t, changed, "already-reconciled manifests change nothing")
}

func TestAutoVersionPackagesSkipsSpacesWithoutPolicy(t *testing.T) {
	root := t.TempDir()
	space := &model.Space{Name: "libs"} // no autoVersion block
	seedFile(t, root, "core/package.json", `{"name": "core", "version": "1.0.0"}`)
	p := avPlan(root, space, "core")
	changed, err := autoVersionPackages(p, []string{"core"}, nil)
	require.NoError(t, err)
	assert.Empty(t, changed)
	assert.Contains(t, fileText(t, root, "core/package.json"), `"version": "1.0.0"`, "untouched")
}

func TestAutoVersionPackagesPolicyOverride(t *testing.T) {
	// The policy callback replaces the space's block for the invocation: a
	// no-block space reconciles under the supplied policy.
	root := t.TempDir()
	space := &model.Space{Name: "libs"}
	seedFile(t, root, "core/package.json", `{"name": "core", "version": "1.0.0"}`)
	p := avPlan(root, space, "core")
	policy := func(*plan.Release) *model.AutoVersion {
		return &model.AutoVersion{Kinds: allKinds(), WriteVersion: true}
	}
	changed, err := autoVersionPackages(p, []string{"core"}, policy)
	require.NoError(t, err)
	assert.True(t, changed["core"])
	assert.Contains(t, fileText(t, root, "core/package.json"), `"version": "1.0.1"`)
}

// The syncLock-only strategy: neither reconciling strategy is configured, so
// the version stage does nothing but the lock scripts still run. A space
// wanting "go mod tidy between version and build, serialised" has no manifest
// change to key off, so the change gate has to step aside for it.

// syncLockOnlySpace is that space: an autoVersion block carrying scripts and
// nothing else.
func syncLockOnlySpace(concurrency int) *model.Space {
	return avSpace(&model.AutoVersion{
		Manifests:           model.ScopeNone,
		Kinds:               allKinds(),
		SyncLock:            []string{"locksync"},
		SyncLockConcurrency: concurrency,
	})
}

func TestSyncLockRunsWithNeitherStrategy(t *testing.T) {
	root := t.TempDir()
	space := syncLockOnlySpace(0)
	assert.False(t, space.AutoVersion.Reconciles())
	seedFile(t, root, "a/package.json", `{"name": "@acme/a", "version": "1.0.0"}`)
	p := avPlan(root, space, "a")

	r := &fakeRunner{}
	res := newExecutor(execSpec{Runner: r, Build: 1, Publish: 1}).Run(context.Background(), p)
	require.Equal(t, StatusPublished, res["a"].Status, "%v", res["a"].Err)
	dir := filepath.Join(root, "a")
	assert.NotEqual(t, -1, r.indexOf("locksync "+dir),
		"a space with no reconciling strategy still runs its lock scripts")
	assert.Less(t, r.indexOf("locksync "+dir), r.indexOf("build "+dir),
		"still between version and build")
	// The manifest was left exactly as it was found: nothing reconciles.
	assert.Contains(t, fileText(t, root, "a/package.json"), `"version": "1.0.0"`)
}

func TestSyncLockOnlyStaysSerialised(t *testing.T) {
	// The budget is the whole point of the mode: parallel writers corrupt a
	// shared lock file whether or not anything was reconciled first.
	root := t.TempDir()
	space := syncLockOnlySpace(0)
	names := []string{"a", "b", "c"}
	for _, n := range names {
		seedFile(t, root, n+"/package.json", `{"name": "@acme/`+n+`"}`)
	}
	p := avPlan(root, space, names...)

	r := &fakeRunner{delay: 30 * time.Millisecond}
	res := newExecutor(execSpec{Runner: r, Build: 4, Publish: 4}).Run(context.Background(), p)
	for _, n := range names {
		require.Equal(t, StatusPublished, res[n].Status, "%s: %v", n, res[n].Err)
	}
	assert.Equal(t, 3, r.countPrefix("locksync"), "every package regenerated its lock")
	assert.Equal(t, 1, r.maxCur["locksync"], "the default budget still serialises them")
}

func TestSyncLockOnlyBudgetConfigurable(t *testing.T) {
	root := t.TempDir()
	space := syncLockOnlySpace(3)
	names := []string{"a", "b", "c"}
	for _, n := range names {
		seedFile(t, root, n+"/package.json", `{"name": "@acme/`+n+`"}`)
	}
	p := avPlan(root, space, names...)

	r := &fakeRunner{delay: 30 * time.Millisecond}
	res := newExecutor(execSpec{Runner: r, Build: 4, Publish: 4}).Run(context.Background(), p)
	for _, n := range names {
		require.Equal(t, StatusPublished, res[n].Status, "%s: %v", n, res[n].Err)
	}
	assert.Greater(t, r.maxCur["locksync"], 1, "a configured budget lifts the serialisation here too")
}

// autoVersionPackages drives the standalone reconciler over several packages
// in order, the way a sweep does, and reports which of them changed.
func autoVersionPackages(p *plan.Plan, pkgs []string, policy func(*plan.Release) *model.AutoVersion) (map[string]bool, error) {
	return autoVersionPackagesCtx(context.Background(), p, pkgs, policy, zerolog.Nop())
}

func autoVersionPackagesCtx(ctx context.Context, p *plan.Plan, pkgs []string, policy func(*plan.Release) *model.AutoVersion, log zerolog.Logger) (map[string]bool, error) {
	v := NewAutoVersioner(context.Background(), p, nil, log)
	changed := map[string]bool{}
	for _, pkg := range pkgs {
		if err := v.Package(ctx, p.Releases[pkg], policy); err != nil {
			return changed, err
		}
	}
	for _, pkg := range pkgs {
		if v.Changed(pkg) {
			changed[pkg] = true
		}
	}
	return changed, nil
}

// TestAutoVersionOnlyUpdatedLeavesTheCatchUpAlone: the policy's OnlyUpdated
// restricts the reconciliation to the run's own updates, so a range that had
// fallen behind a provider released earlier stays as it is — and the W197 that
// would have explained the catch-up is never emitted, because nothing caught
// up.
func TestAutoVersionOnlyUpdatedLeavesTheCatchUpAlone(t *testing.T) {
	root := t.TempDir()
	seedFile(t, root, "core/package.json", `{"name": "@acme/core", "version": "1.0.0"}`)
	seedFile(t, root, "web/package.json",
		`{"name": "@acme/web", "version": "1.0.0", "dependencies": {"@acme/core": "^0.9.0"}}`)
	p := avPlan(root, avSpace(nil), "core", "web")
	p.Providers["web"] = []string{"core"}
	// core is not releasing this run: its baseline comes from an earlier one.
	// fillUpdates runs after that is set, so core stays out of web's Updates.
	core := p.Releases["core"]
	core.OwnBump, core.Bump, core.NewWork, core.Units, core.FreshUnits = ccme.BumpNone, ccme.BumpNone, false, nil, nil
	core.Next = core.Current
	fillUpdates(p)

	var logBuf bytes.Buffer
	policy := func(*plan.Release) *model.AutoVersion {
		return &model.AutoVersion{Kinds: allKinds(), WriteVersion: true, OnlyUpdated: true}
	}
	changed, err := autoVersionPackagesCtx(context.Background(), p, []string{"core", "web"}, policy, syncedLog(&logBuf))
	require.NoError(t, err)

	web := fileText(t, root, "web/package.json")
	assert.Contains(t, web, `"@acme/core": "^0.9.0"`, "the stale range is left alone")
	assert.NotContains(t, logBuf.String(), plan.CodeRangeCatchUp, "nothing caught up, so nothing to warn about")
	assert.True(t, changed["web"], "web's own version is still written")

	// Without the flag the same run does catch the range up, which is what
	// makes the flag's effect the flag's own.
	plain := func(*plan.Release) *model.AutoVersion {
		return &model.AutoVersion{Kinds: allKinds(), WriteVersion: true}
	}
	_, err = autoVersionPackagesCtx(context.Background(), p, []string{"core", "web"}, plain, syncedLog(&logBuf))
	require.NoError(t, err)
	assert.Contains(t, fileText(t, root, "web/package.json"), `"@acme/core": "^1.0.0"`)
}
