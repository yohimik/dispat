package integration

// Area 10: native auto-versioning through the compiled binary. A space with
// an autoVersion block gets its manifests rewritten by dispat itself at the
// version stage — ranges reconciled to end-of-run versions under the match /
// range policy, own versions updated — and its syncLock scripts run between
// version and build under their own concurrency budget.

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestAutoVersionReleaseRewritesManifests: a workspace:* range is reconciled
// to the provider's released version, a hand-pinned range outside the match
// globs survives, both manifests' own version fields advance, and the
// syncLock script observes the already-rewritten manifest before the build.
func TestAutoVersionReleaseRewritesManifests(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Scripts["locksync"] = "cp package.json lock-snapshot.json"
	cfg.Spaces["libs"] = models.SpaceConfig{
		Path: "packages",
		Flow: buildPublish(),
		AutoVersion: &models.AutoVersionConfig{
			Match:    []string{"workspace:*"},
			SyncLock: []string{"locksync"},
		},
	}
	cfg.Dependencies = []models.DependencyConfig{{Consumer: "web", Provider: "core"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "web")
	r.WriteFile("packages/core/package.json", `{"name": "@acme/core", "version": "0.0.0"}`)
	r.WriteFile("packages/web/package.json", `{
  "name": "@acme/web",
  "version": "0.0.0",
  "dependencies": {"@acme/core": "workspace:*", "left-pad": "1.3.0"}
}`)
	r.Commit("feat(core,web): bootstrap")

	res := r.ReleaseOK()
	require.Contains(t, r.TagList(), "core@0.1.0")
	require.Contains(t, r.TagList(), "web@0.1.0")

	web, err := os.ReadFile(r.Path("packages", "web", "package.json"))
	require.NoError(t, err)
	assert.Contains(t, string(web), `"@acme/core": "^0.1.0"`, "matched range reconciled (caret default)")
	assert.Contains(t, string(web), `"left-pad": "1.3.0"`, "non-workspace pin untouched")
	assert.Contains(t, string(web), `"version": "0.1.0"`, "own version written (§12.4)")

	core, err := os.ReadFile(r.Path("packages", "core", "package.json"))
	require.NoError(t, err)
	assert.Contains(t, string(core), `"version": "0.1.0"`)

	// syncLock ran inside the package folder after the rewrite: its snapshot
	// already carries the reconciled range.
	snap, err := os.ReadFile(r.Path("packages", "web", "lock-snapshot.json"))
	require.NoError(t, err, "syncLock must run; stdout:\n%s", res.Stdout)
	assert.Contains(t, string(snap), `"@acme/core": "^0.1.0"`, "syncLock sees the rewritten manifest")
}

// TestAutoVersionSyncLockSerialised: with several packages auto-versioning at
// once, their syncLock scripts never overlap (default budget 1) while builds
// keep the build budget — the corrupted-shared-lockfile guard over the real
// scheduler and binary.
func TestAutoVersionSyncLockSerialised(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 4)
	cfg.Scripts["locksync"] = r.TsmarkScript("synclock.log", "$DISPAT_PACKAGE", 120*time.Millisecond)
	cfg.Spaces["libs"] = models.SpaceConfig{
		Path: "packages",
		Flow: buildPublish(),
		AutoVersion: &models.AutoVersionConfig{
			SyncLock: []string{"locksync"},
		},
	}
	r.WriteConfigModel(cfg)
	names := packageNames(3, "pkg")
	for _, n := range names {
		r.WriteFile("packages/"+n+"/package.json", `{"name": "@acme/`+n+`", "version": "0.0.0"}`)
	}
	seedIndependentPackages(r, names)

	r.ReleaseOK()
	ivs := r.Timeline("synclock.log")
	require.Len(t, ivs, 3, "every package ran its syncLock")
	harness.AssertConcurrencyBudget(t, ivs, 1)
}
