// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package release

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// TestSettledEnvNamesOnlyWhatPublished: once the run has settled, a package
// that did not publish, whether it failed or was cancelled, is listed at the
// version it had before the run with _RELEASING=false and is no provider
// update, while what published keeps its new version. The stage and the
// package's own release variables are the ordinary ones.
func TestSettledEnvNamesOnlyWhatPublished(t *testing.T) {
	p := mkPlan(planSpec{Deps: map[string][]string{"b": {"a", "c", "d"}}, Names: []string{"a", "b", "c", "d"}})
	results := map[string]*Result{
		"a": {Name: "a", Status: StatusFailed},
		"b": {Name: "b", Status: StatusPublished},
		"c": {Name: "c", Status: StatusCancelled},
		"d": {Name: "d", Status: StatusPublished},
	}

	env := SettledEnv(SettledEnvRequest{Plan: p, Results: results, Package: "b", Stage: "syncLock"})

	assert.Equal(t, "syncLock", envValue(t, env, "DISPAT_STAGE"))
	assert.Equal(t, "b", envValue(t, env, plan.PackageEnvVar))
	assert.Equal(t, "1.0.1", envValue(t, env, plan.NewVersionEnvVar), "the published package's own version")
	for _, unpublished := range []string{"A", "C"} {
		assert.Equal(t, "1.0.0", envValue(t, env, plan.WorkspaceEnvPrefix+unpublished+"_VERSION"),
			"%s is listed at its previous version", unpublished)
		assert.Equal(t, "false", envValue(t, env, plan.WorkspaceEnvPrefix+unpublished+"_RELEASING"))
	}
	for _, published := range []string{"B", "D"} {
		assert.Equal(t, "1.0.1", envValue(t, env, plan.WorkspaceEnvPrefix+published+"_VERSION"))
		assert.Equal(t, "true", envValue(t, env, plan.WorkspaceEnvPrefix+published+"_RELEASING"))
	}
	assert.Equal(t, "D", envValue(t, env, plan.UpdatedPackagesEnvVar), "only the provider that published is an update")
	assert.Equal(t, "d: 1.0.0 -> 1.0.1", envValue(t, env, "DISPAT_DEPENDENCIES"))
}

// TestRunRecordsPreparationUnderTheLock: a package whose version stage started
// is prepared whatever became of it, a package with no version task is not,
// and the syncLock flag says whether the syncLock commands ran, not whether
// the stage existed. Run under -race, the flags are written from concurrent
// task goroutines under the run's mutex.
func TestRunRecordsPreparationUnderTheLock(t *testing.T) {
	root := t.TempDir()
	syncing := avSpace(&model.AutoVersion{Manifests: model.ScopeNone, SyncLock: []string{"locksync"}})
	quiet := avSpace(&model.AutoVersion{Manifests: model.ScopeRoot, SyncLock: []string{"locksync"}})
	p := avPlan(root, syncing, "failing", "published", "quiet")
	p.Releases["quiet"].Pkg.Space = quiet
	for _, name := range p.Order {
		require.NoError(t, os.MkdirAll(filepath.Join(root, name), 0o755))
	}
	plain := mkPlan(planSpec{Names: []string{"plain"}}).Releases["plain"]
	p.Releases["plain"], p.Order = plain, append(p.Order, "plain")
	fillUpdates(p)

	runner := &fakeRunner{fail: map[string]bool{"build " + filepath.Join(root, "failing"): true}}
	res := newExecutor(execSpec{Runner: runner, Build: 4, Publish: 4}).Run(context.Background(), p)

	require.Equal(t, StatusFailed, res["failing"].Status)
	require.Equal(t, StatusPublished, res["published"].Status)
	require.Equal(t, StatusPublished, res["quiet"].Status)
	require.Equal(t, StatusPublished, res["plain"].Status)
	assert.True(t, res["failing"].IsPrepared, "a failed package whose version stage started")
	assert.True(t, res["failing"].IsSyncLockRun)
	assert.True(t, res["published"].IsPrepared)
	assert.True(t, res["published"].IsSyncLockRun)
	assert.True(t, res["quiet"].IsPrepared)
	assert.False(t, res["quiet"].IsSyncLockRun, "nothing was reconciled, so its syncLock commands never ran")
	assert.False(t, res["plain"].IsPrepared, "no version task, nothing prepared")
	assert.False(t, res["plain"].IsSyncLockRun)
}
