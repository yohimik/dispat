package integration

// Area 11: native auto-versioning through the compiled binary. A space with
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

// TestAutoVersionDiagnosticsAndCommitInclude drives the four autoVersion
// diagnostics through the real binary across three runs, with the release
// commit picking up the root lock file syncLock regenerates (commit.include):
//
//	run 1  both packages release with no configured edge: W221, and the
//	       release commit stages package-lock.json from the repo root
//	run 2  web's manifest was hand-edited backwards: W192 (drifted own
//	       version) and W197 (range caught up to a provider released in an
//	       earlier run)
//	run 3  core goes to beta while web releases stable over it: W203
func TestAutoVersionDiagnosticsAndCommitInclude(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Scripts["locksync"] = `echo "lock for $DISPAT_PACKAGE@$DISPAT_NEW_VERSION" >> ../../package-lock.json`
	cfg.Spaces["libs"] = models.SpaceConfig{
		Path: "packages",
		Flow: buildPublish(),
		AutoVersion: &models.AutoVersionConfig{
			SyncLock: []string{"locksync"},
		},
	}
	cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true), Include: []string{"package-lock.json"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "web")
	r.WriteFile("packages/core/package.json", `{"name": "@acme/core", "version": "0.0.0"}`)
	r.WriteFile("packages/web/package.json",
		`{"name": "@acme/web", "version": "0.0.0", "dependencies": {"@acme/core": "workspace:*"}}`)
	r.Commit("feat(core,web): bootstrap")

	// Run 1: no dependencies edge is configured, so the rewrite of web's
	// range is optimistic about core's in-flight publish — W221.
	res := r.ReleaseOK()
	assert.True(t, harness.HasCodeForPackage(res.Events, "W221", "web"),
		"a rewritten edge with no configured counterpart must be reported")
	staged := r.Git("show", "--name-only", "--format=", "HEAD")
	assert.Contains(t, staged, "package-lock.json",
		"commit.include stages the root lock file syncLock regenerated")
	assert.Contains(t, staged, "packages/web/package.json")

	// Run 2: hand-edit web's manifest backwards — its own version drifts off
	// the baseline and its range lags core's released version.
	r.WriteFile("packages/web/package.json",
		`{"name": "@acme/web", "version": "9.9.9", "dependencies": {"@acme/core": "^0.0.9"}}`)
	r.Commit("fix(web): regressed manifest committed by hand")
	res = r.ReleaseOK()
	assert.True(t, harness.HasCodeForPackage(res.Events, "W192", "web"),
		"the drifted manifest version must be reported")
	assert.True(t, harness.HasCodeForPackage(res.Events, "W197", "web"),
		"the caught-up range must be reported: core released in an earlier run")
	web, err := os.ReadFile(r.Path("packages", "web", "package.json"))
	require.NoError(t, err)
	assert.Contains(t, string(web), `"@acme/core": "^0.1.0"`, "range caught up to core's baseline")
	assert.NotContains(t, string(web), "9.9.9", "the computed version overwrote the drift")

	// Run 3: core moves to beta, web releases stable ranging over it — W203.
	r.CommitEmpty("feat(core)@beta: risky rewrite\n---\nfix(web): stable work of its own")
	res = r.ReleaseOK()
	assert.True(t, harness.HasCodeForPackage(res.Events, "W203", "web"),
		"a stable release ranging over a prerelease provider must be reported")
}
