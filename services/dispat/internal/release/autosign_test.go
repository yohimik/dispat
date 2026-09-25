// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package release

// The sign stage: the optional first stage of a package's release, before the
// version stage and the build. It exists only where a space configures it,
// runs under the build budget, and its native half writes the package's own
// version, which the version stage then leaves alone.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// signedSpace is avSpace with the sign stage's native write on and a version
// stage that writes ranges alone, the way the config loader resolves an
// autoVersion block beside an enabled autoSign.
func signedSpace(scope model.ManifestScope, syncLock ...string) *model.Space {
	space := avSpace(&model.AutoVersion{Kinds: allKinds(), SyncLock: syncLock})
	space.AutoSign = &model.AutoSign{Manifests: scope}
	return space
}

// logLines decodes a JSON log buffer into its lines.
func logLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var lines []map[string]any
	for _, raw := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if raw == "" {
			continue
		}
		var line map[string]any
		require.NoError(t, json.Unmarshal([]byte(raw), &line), raw)
		lines = append(lines, line)
	}
	return lines
}

// TestSignStageRunsFirst: every hook and stage of a package with a sign stage
// fires in one order, the sign frame first, and each carries its own name in
// DISPAT_STAGE and in the stage events.
func TestSignStageRunsFirst(t *testing.T) {
	p := mkPlan(planSpec{Names: []string{"a"}})
	sp := p.Releases["a"].Pkg.Space
	sp.BeforeAllScript = []string{"h-all"}
	sp.BeforeSignScript = []string{"h-bs"}
	sp.SignScript = []string{"sign"}
	sp.PostSignScript = []string{"h-ps"}
	sp.BeforeVersionScript = []string{"h-bv"}
	sp.VersionScript = []string{"version"}
	sp.PostVersionScript = []string{"h-pv"}
	// Neither strategy: the version stage exists for its syncLock alone, which
	// then runs every release.
	sp.AutoVersion = &model.AutoVersion{Manifests: model.ScopeNone, SyncLock: []string{"lock"}}
	sp.BeforeBuildScript = []string{"h-bb"}
	r := &fakeRunner{}
	obs, res := runObserved(t, p, r)

	require.Equal(t, StatusPublished, res["a"].Status, "%v", res["a"].Err)
	order := []string{"h-all a", "h-bs a", "sign a", "h-ps a", "h-bv a", "version a", "h-pv a",
		"lock a", "h-bb a", "build a", "publish a"}
	for i := 1; i < len(order); i++ {
		assert.Less(t, r.indexOf(order[i-1]), r.indexOf(order[i]), "%s must run before %s", order[i-1], order[i])
	}
	assert.Equal(t, 1, r.countPrefix("h-all"), "beforeAll runs once, at the sign stage")
	assert.Equal(t, "sign", envValue(t, r.envs["sign a"], "DISPAT_STAGE"))
	assert.Equal(t, "beforeSign", envValue(t, r.envs["h-bs a"], "DISPAT_STAGE"))
	assert.Equal(t, "postSign", envValue(t, r.envs["h-ps a"], "DISPAT_STAGE"))
	assert.Equal(t, "version", envValue(t, r.envs["version a"], "DISPAT_STAGE"))
	assert.Equal(t, []string{
		"stage.started:sign", "stage.succeeded:sign",
		"stage.started:version", "stage.succeeded:version",
		"stage.started:syncLock", "stage.succeeded:syncLock",
		"stage.started:build", "stage.succeeded:build",
		"stage.started:publish", "stage.succeeded:publish",
		"package.published:",
	}, obs.forPackage("a"))
}

// TestSignStageAbsentWithoutConfiguration: a package whose space configures
// neither autoSign nor a sign sequence has no sign task, so nothing about its
// run changes, an auto-versioning one included.
func TestSignStageAbsentWithoutConfiguration(t *testing.T) {
	root := t.TempDir()
	seedFile(t, root, "a/package.json", `{"name": "@acme/a", "version": "1.0.0"}`)
	p := avPlan(root, avSpace(&model.AutoVersion{Kinds: allKinds(), WriteVersion: true}), "a")
	fillUpdates(p)
	obs, res := runObserved(t, p, &fakeRunner{})

	require.Equal(t, StatusPublished, res["a"].Status, "%v", res["a"].Err)
	assert.Equal(t, []string{
		"stage.started:version", "stage.succeeded:version",
		"stage.started:build", "stage.succeeded:build",
		"stage.started:publish", "stage.succeeded:publish",
		"package.published:",
	}, obs.forPackage("a"))
	assert.False(t, hasSignTask(p.Releases["a"]))
	assert.Equal(t, taskVersion, resolveFirstTask(p.Releases["a"]))
}

// TestSignStageFromOneHook: a hook alone gives a package a sign stage, so a
// configured hook never goes silent, and a package whose first task is the
// sign stage still runs beforeAll exactly once.
func TestSignStageFromOneHook(t *testing.T) {
	p := mkPlan(planSpec{Names: []string{"a"}})
	sp := p.Releases["a"].Pkg.Space
	sp.PostSignScript = []string{"h-ps"}
	sp.BeforeAllScript = []string{"h-all"}
	r := &fakeRunner{}
	res := newExecutor(execSpec{Runner: r, Tagger: &fakeTagger{}, Build: 1, Publish: 1}).Run(context.Background(), p)

	require.Equal(t, StatusPublished, res["a"].Status, "%v", res["a"].Err)
	assert.Less(t, r.indexOf("h-all a"), r.indexOf("h-ps a"))
	assert.Less(t, r.indexOf("h-ps a"), r.indexOf("build a"))
	assert.Equal(t, 1, r.countPrefix("h-all"))
	assert.Equal(t, taskSign, resolveFirstTask(p.Releases["a"]))
}

// overlapRunner measures how many of the watched commands run at once, across
// commands: the build budget is one pool for the sign, version and build
// tasks, so their scripts must never overlap beyond it.
type overlapRunner struct {
	mu      sync.Mutex
	watched map[string]bool
	cur     int
	peak    int
}

func (o *overlapRunner) Run(_ context.Context, _, command string, _ []string, _, _ io.Writer) error {
	if !o.watched[command] {
		return nil
	}
	o.mu.Lock()
	o.cur++
	o.peak = max(o.peak, o.cur)
	o.mu.Unlock()
	time.Sleep(5 * time.Millisecond)
	o.mu.Lock()
	o.cur--
	o.mu.Unlock()
	return nil
}

// TestSignStageSharesTheBuildBudget: sign tasks take build slots, so under a
// build budget of one no sign script overlaps another or a build.
func TestSignStageSharesTheBuildBudget(t *testing.T) {
	p := mkPlan(planSpec{Names: []string{"a", "b", "c", "d"}})
	p.Releases["a"].Pkg.Space.SignScript = []string{"sign"}
	runner := &overlapRunner{watched: map[string]bool{"sign": true, "build": true}}
	e := newExecutor(execSpec{Build: 1, Publish: 4})
	e.Runner = runner
	res := e.Run(context.Background(), p)

	for _, name := range []string{"a", "b", "c", "d"} {
		require.Equal(t, StatusPublished, res[name].Status, "%s: %v", name, res[name].Err)
	}
	assert.Equal(t, 1, runner.peak, "sign and build share the build budget of one")
}

// TestSignFailureFailsAtTheSignStage: a failing sign script fails the package
// at its sign stage, before anything else of it runs; revertOnFail rolls the
// folder back, onFail names the stage, and the package counts as prepared.
func TestSignFailureFailsAtTheSignStage(t *testing.T) {
	p := mkPlan(planSpec{Names: []string{"a"}})
	sp := p.Releases["a"].Pkg.Space
	sp.RevertOnFail = true
	sp.SignScript = []string{"sign"}
	sp.OnFailScript = []string{"on-fail"}
	rv := &fakeReverter{}
	r := &fakeRunner{fail: map[string]bool{"sign a": true}}
	obs := &fakeObserver{}
	e := newExecutor(execSpec{Runner: r, Tagger: &fakeTagger{}, Build: 1, Publish: 1})
	e.Reverter, e.Observer = rv, obs
	res := e.Run(context.Background(), p)

	require.Equal(t, StatusFailed, res["a"].Status)
	assert.Equal(t, "sign", res["a"].FailedStage)
	assert.True(t, res["a"].IsPrepared, "a package whose sign stage started prepared its release files")
	assert.Equal(t, []string{"a"}, rv.dirs, "revertOnFail rolls the folder back")
	assert.Equal(t, -1, r.indexOf("build a"), "nothing after the sign stage runs")
	assert.Equal(t, "sign", envValue(t, r.envs["on-fail a"], "DISPAT_FAILED_STAGE"))
	ev, ok := obs.find("a", EventPackageFailed)
	require.True(t, ok)
	assert.Equal(t, "sign", ev.FailedStage)
}

// TestAutoSignFailureIsLabelled: the native write failing fails the sign
// stage under its own label, never the version stage's.
func TestAutoSignFailureIsLabelled(t *testing.T) {
	root := t.TempDir()
	seedFile(t, root, "target.json", `{"name": "@acme/a", "version": "1.0.0"}`)
	dir := filepath.Join(root, "a")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.Symlink(filepath.Join(root, "target.json"), filepath.Join(dir, "package.json")))
	p := avPlan(root, signedSpace(model.ScopeRoot), "a")
	fillUpdates(p)
	var buf bytes.Buffer
	e := newExecutor(execSpec{Runner: &fakeRunner{}, Build: 1, Publish: 1})
	e.Log = syncedLog(&buf)
	res := e.Run(context.Background(), p)

	require.Equal(t, StatusFailed, res["a"].Status)
	assert.Equal(t, "sign", res["a"].FailedStage)
	assert.Contains(t, buf.String(), `"message":"auto-signing failed"`)
	assert.NotContains(t, buf.String(), "auto-versioning failed")
	assert.Equal(t, `{"name": "@acme/a", "version": "1.0.0"}`, fileText(t, root, "target.json"))
}

// TestAutoSignWritesTheOwnVersionOnce: the sign stage writes each package's own
// version, reporting a drifted one as W192 there, and the version stage after it
// writes the dependency ranges alone.
func TestAutoSignWritesTheOwnVersionOnce(t *testing.T) {
	root := t.TempDir()
	seedFile(t, root, "core/package.json", `{"name": "@acme/core", "version": "0.9.0"}`)
	seedFile(t, root, "web/package.json",
		`{"name": "@acme/web", "version": "1.0.0", "dependencies": {"@acme/core": "workspace:*"}}`)
	p := avPlan(root, signedSpace(model.ScopeRoot), "core", "web")
	p.Providers["web"] = []string{"core"}
	fillUpdates(p)
	var buf bytes.Buffer
	e := newExecutor(execSpec{Runner: &fakeRunner{}, Build: 2, Publish: 2})
	e.Log = syncedLog(&buf)
	res := e.Run(context.Background(), p)
	require.Equal(t, StatusPublished, res["web"].Status, "%v", res["web"].Err)

	web := fileText(t, root, "web/package.json")
	assert.Contains(t, web, `"version": "1.0.1"`)
	assert.Contains(t, web, `"@acme/core": "^1.0.1"`)
	assert.Contains(t, fileText(t, root, "core/package.json"), `"version": "1.0.1"`)

	written, drift := map[string]int{}, 0
	for _, line := range logLines(t, &buf) {
		switch {
		case line["message"] == "manifest version written":
			assert.Equal(t, "sign", line["stage"])
			written[line["package"].(string)]++
		case line["message"] == "manifest reconciled":
			assert.Equal(t, "version", line["stage"])
			assert.Equal(t, false, line["versionWritten"], "the version stage writes no own version")
		case line["code"] == "W192":
			assert.Equal(t, "sign", line["stage"], "drift is reported by the stage that writes the version")
			assert.Equal(t, "core", line["package"])
			drift++
		}
	}
	assert.Equal(t, map[string]int{"core": 1, "web": 1}, written, "one own-version write per package")
	assert.Equal(t, 1, drift)
}

// TestAutoSignScopes: root scans the manifests in the package folder, all scans
// every manifest under it, and either way only the package's own manifests are
// written, so a nested example keeps its version.
func TestAutoSignScopes(t *testing.T) {
	unity := "%YAML 1.1\n%TAG !u! tag:unity3d.com,2011:\n--- !u!129 &1\nPlayerSettings:\n  bundleVersion: 1.0.0\n"
	for _, c := range []struct {
		scope        model.ManifestScope
		wantSettings string
	}{
		{model.ScopeRoot, "bundleVersion: 1.0.0"},
		{model.ScopeAll, "bundleVersion: 1.0.1"},
	} {
		t.Run(string(c.scope), func(t *testing.T) {
			root := t.TempDir()
			seedFile(t, root, "game/package.json", `{"name": "@acme/game", "version": "1.0.0"}`)
			seedFile(t, root, "game/ProjectSettings/ProjectSettings.asset", unity)
			seedFile(t, root, "game/examples/demo/package.json", `{"name": "demo", "version": "0.3.0"}`)
			p := avPlan(root, signedSpace(c.scope), "game")
			fillUpdates(p)
			res := newExecutor(execSpec{Runner: &fakeRunner{}, Build: 1, Publish: 1}).Run(context.Background(), p)
			require.Equal(t, StatusPublished, res["game"].Status, "%v", res["game"].Err)

			assert.Contains(t, fileText(t, root, "game/package.json"), `"version": "1.0.1"`)
			assert.Contains(t, fileText(t, root, "game/ProjectSettings/ProjectSettings.asset"), c.wantSettings)
			assert.Contains(t, fileText(t, root, "game/examples/demo/package.json"), `"version": "0.3.0"`,
				"a nested manifest is not the package's own")
		})
	}
}

// TestSyncLockRunsAfterASignOnlyChange: a release whose only file change is the
// own version the sign stage wrote still regenerates its lock, because the
// lock follows the manifest whichever stage changed it.
func TestSyncLockRunsAfterASignOnlyChange(t *testing.T) {
	root := t.TempDir()
	seedFile(t, root, "a/package.json", `{"name": "@acme/a", "version": "1.0.0"}`)
	p := avPlan(root, signedSpace(model.ScopeRoot, "lock"), "a")
	fillUpdates(p)
	r := &fakeRunner{}
	res := newExecutor(execSpec{Runner: r, Build: 1, Publish: 1}).Run(context.Background(), p)

	require.Equal(t, StatusPublished, res["a"].Status, "%v", res["a"].Err)
	lock := filepath.Join(root, "a")
	assert.GreaterOrEqual(t, r.indexOf("lock "+lock), 0, "the sign stage's write is a change to regenerate from")
	assert.Less(t, r.indexOf("lock "+lock), r.indexOf("build "+lock))
}

// TestSignStageNames: the task kind's two spellings, from the one table.
func TestSignStageNames(t *testing.T) {
	assert.Equal(t, "sign", taskSign.String())
	assert.Equal(t, "Sign", stageTitle(taskSign))
	assert.Equal(t, "auto-signing failed", formatNativeFailure(taskSign))
	assert.Equal(t, "auto-versioning failed", formatNativeFailure(taskVersion))
	assert.True(t, isPreparingStage(taskSign))
}
