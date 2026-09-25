// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Area 11: native auto-versioning through the compiled binary. A space with
// an autoVersion block gets its files reconciled by dispat itself at the
// version stage — the parsing strategy rewriting declared ranges and own
// versions under the match / range policy, the replacing strategy
// replacing literal text in whatever else the release has to keep in step
// — and its syncLock scripts run between version and build under their own
// concurrency budget. Either strategy may be off, and with both off the lock
// scripts are the whole of the version stage.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
	cfg.Scripts["locksync"] = models.Script{"cp package.json lock-snapshot.json"}
	cfg.Spaces["libs"] = models.SpaceConfig{
		Path: models.PathList{"packages"},
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
	cfg.Scripts["locksync"] = models.Script{r.TsmarkScript("synclock.log", "$DISPAT_PACKAGE", 120*time.Millisecond)}
	cfg.Spaces["libs"] = models.SpaceConfig{
		Path: models.PathList{"packages"},
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
	cfg.Scripts["locksync"] = models.Script{`echo "lock for $DISPAT_PACKAGE@$DISPAT_NEW_VERSION" >> ../../package-lock.json`}
	cfg.Spaces["libs"] = models.SpaceConfig{
		Path: models.PathList{"packages"},
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
	assert.True(t, harness.IsCodePresentForPackage(res.Events, "W221", "web"),
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
	assert.True(t, harness.IsCodePresentForPackage(res.Events, "W192", "web"),
		"the drifted manifest version must be reported")
	assert.True(t, harness.IsCodePresentForPackage(res.Events, "W197", "web"),
		"the caught-up range must be reported: core released in an earlier run")
	web, err := os.ReadFile(r.Path("packages", "web", "package.json"))
	require.NoError(t, err)
	assert.Contains(t, string(web), `"@acme/core": "^0.1.0"`, "range caught up to core's baseline")
	assert.NotContains(t, string(web), "9.9.9", "the computed version overwrote the drift")

	// Run 3: core moves to beta, web releases stable ranging over it — W203.
	r.CommitEmpty("feat(core)%beta: risky rewrite\n---\nfix(web): stable work of its own")
	res = r.ReleaseOK()
	assert.True(t, harness.IsCodePresentForPackage(res.Events, "W203", "web"),
		"a stable release ranging over a prerelease provider must be reported")
}

// TestAutoVersionReplaceStrategy: a Gradle-shaped space where nothing parses
// as a manifest. `manifests: none` turns the parsing strategy off and the
// replace rules carry the whole reconciliation: the provider's coordinate in
// the consumer's build script, and the package's own version in its README.
func TestAutoVersionReplaceStrategy(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Spaces["libs"] = models.SpaceConfig{
		Path: models.PathList{"packages"},
		Flow: buildPublish(),
		AutoVersion: &models.AutoVersionConfig{
			Manifests: "none",
			Replace: []models.AutoVersionReplaceConfig{
				{
					Files: []string{"*.gradle"},
					Find:  "com.acme:{provider}:{providerPrevious}",
					Write: "com.acme:{provider}:{providerVersion}",
				},
				{
					Files: []string{"README.md"},
					Find:  "com.acme:{name}:{previous}",
					Write: "com.acme:{name}:{version}",
				},
			},
		},
	}
	cfg.Dependencies = []models.DependencyConfig{{Consumer: "web", Provider: "core"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "web")
	r.WriteFile("packages/core/build.gradle", "group = 'com.acme'\n")
	r.WriteFile("packages/web/build.gradle",
		"dependencies {\n  implementation 'com.acme:core:0.0.0'\n  testImplementation 'com.acme:core:0.0.0'\n}\n")
	r.WriteFile("packages/web/README.md", "Add com.acme:web:0.0.0 to your build.\n")
	r.WriteFile("packages/web/logo.png", "\x89PNG\x00 com.acme:web:0.0.0")
	r.Commit("feat(core,web): bootstrap")

	res := r.ReleaseOK()
	require.Contains(t, r.TagList(), "core@0.1.0")
	require.Contains(t, r.TagList(), "web@0.1.0")

	gradle, err := os.ReadFile(r.Path("packages", "web", "build.gradle"))
	require.NoError(t, err)
	assert.Equal(t,
		"dependencies {\n  implementation 'com.acme:core:0.1.0'\n  testImplementation 'com.acme:core:0.1.0'\n}\n",
		string(gradle), "every occurrence of the coordinate, and nothing else")

	readme, err := os.ReadFile(r.Path("packages", "web", "README.md"))
	require.NoError(t, err)
	assert.Equal(t, "Add com.acme:web:0.1.0 to your build.\n", string(readme))

	png, err := os.ReadFile(r.Path("packages", "web", "logo.png"))
	require.NoError(t, err)
	assert.Contains(t, string(png), "0.0.0", "a binary file is skipped, not corrupted")
	assert.False(t, harness.IsCodePresentForPackage(res.Events, "W222", "web"),
		"both rules matched, so nothing is reported stale")
}

// TestAutoVersionReplaceRuleMatchedNothing: a mistyped rule reconciles
// nothing and says so (W222) rather than failing silently for as many
// releases as it takes somebody to notice.
func TestAutoVersionReplaceRuleMatchedNothing(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Spaces["libs"] = models.SpaceConfig{
		Path: models.PathList{"packages"},
		Flow: buildPublish(),
		AutoVersion: &models.AutoVersionConfig{
			Manifests: "none",
			Replace: []models.AutoVersionReplaceConfig{
				{Files: []string{"*.txt"}, Find: "typo-{previous}", Write: "typo-{version}"},
			},
		},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.WriteFile("packages/core/notes.txt", "nothing the rule looks for\n")
	r.Commit("feat(core): bootstrap")

	res := r.ReleaseOK()
	assert.True(t, harness.IsCodePresentForPackage(res.Events, "W222", "core"),
		"a rule that matched nothing must be reported")
}

// TestAutoVersionManifestNamesMakeAnEdgeVisible: a package whose manifests
// declare no name the workspace can learn becomes visible to `dispat compute`
// and to auto-versioning alike once the configuration states what it is
// called — the two share one index, so they cannot disagree.
func TestAutoVersionManifestNamesMakeAnEdgeVisible(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Spaces["libs"] = models.SpaceConfig{
		Path: models.PathList{"packages"}, Flow: buildPublish(),
		// Maven declares exact versions, so the range policy says so; a bare
		// {} block would be pruned by the loader as absent anyway.
		AutoVersion: &models.AutoVersionConfig{Range: "exact"},
	}
	cfg.Packages = map[string]models.PackageConfig{
		"core": {ManifestNames: []string{"com.acme:core"}},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "web")
	// core is a Gradle module: no manifest here declares "com.acme:core".
	r.WriteFile("packages/core/build.gradle", "group = 'com.acme'\n")
	r.WriteFile("packages/web/pom.xml", `<project>
  <groupId>com.acme</groupId>
  <artifactId>web</artifactId>
  <version>0.0.0</version>
  <dependencies>
    <dependency>
      <groupId>com.acme</groupId>
      <artifactId>core</artifactId>
      <version>0.0.0</version>
    </dependency>
  </dependencies>
</project>`)
	r.Commit("feat(core,web): bootstrap")

	// compute sees the edge only because the name was stated.
	res := r.Command("compute")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "web -> core", "the stated name resolves the coordinate")

	// So does auto-versioning: the pom's declared version is reconciled.
	res = r.Command("autoversion", "--package", "web", "--sync-lock=false")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	pom, err := os.ReadFile(r.Path("packages", "web", "pom.xml"))
	require.NoError(t, err)
	assert.Contains(t, string(pom),
		"<artifactId>core</artifactId>\n      <version>0.1.0</version>",
		"the coordinate the stated name resolved is reconciled")
}

// TestAutoVersionSyncLockOnly: an autoVersion block carrying neither
// reconciling strategy is how a space asks for "regenerate the lock file
// between version and build, one at a time". There is no manifest change to
// key off, so the scripts run every release, still under the budget.
func TestAutoVersionSyncLockOnly(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 4)
	cfg.Scripts["locksync"] = models.Script{r.TsmarkScript("synclock.log", "$DISPAT_PACKAGE", 120*time.Millisecond)}
	cfg.Spaces["libs"] = models.SpaceConfig{
		Path: models.PathList{"packages"},
		Flow: buildPublish(),
		AutoVersion: &models.AutoVersionConfig{
			Manifests: "none",
			SyncLock:  []string{"locksync"},
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
	require.Len(t, ivs, 3, "every package ran its lock script, with nothing reconciled to key off")
	harness.AssertConcurrencyBudget(t, ivs, 1)

	for _, n := range names {
		data, err := os.ReadFile(r.Path("packages", n, "package.json"))
		require.NoError(t, err)
		assert.Contains(t, string(data), `"version": "0.0.0"`, "no strategy means no rewrite")
	}
}

// TestAutoVersionSyncLockOnlyStandalone: the same mode through `dispat
// autoversion`, where the serial loop is the budget by construction.
func TestAutoVersionSyncLockOnlyStandalone(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Scripts["mark-lock"] = models.Script{"echo locked >> ../../lock.log"}
	cfg.Spaces["libs"] = models.SpaceConfig{
		Path: models.PathList{"packages"}, Flow: buildPublish(),
		AutoVersion: &models.AutoVersionConfig{Manifests: "none", SyncLock: []string{"mark-lock"}},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.WriteFile("packages/core/package.json", `{"name": "core", "version": "0.0.0"}`)
	r.Commit("feat(core): bootstrap")

	for i := 1; i <= 2; i++ {
		res := r.Command("autoversion")
		require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
		lock, err := os.ReadFile(r.Path("lock.log"))
		require.NoError(t, err)
		assert.Equal(t, i, strings.Count(string(lock), "locked"),
			"with nothing to reconcile the lock script runs every time")
	}
}

// TestAutoVersionPolicyFlagsStillRunSyncLock: a policy flag reconciles
// through the flag's policy, and the syncLock loop must follow the same
// resolved policy rather than reading the space's block behind its back.
func TestAutoVersionPolicyFlagsStillRunSyncLock(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Scripts["mark-lock"] = models.Script{"echo locked >> ../../lock.log"}
	cfg.Spaces["libs"] = models.SpaceConfig{
		Path: models.PathList{"packages"}, Flow: buildPublish(),
		AutoVersion: &models.AutoVersionConfig{SyncLock: []string{"mark-lock"}},
	}
	cfg.Dependencies = []models.DependencyConfig{{Consumer: "web", Provider: "core"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.WriteFile("packages/core/package.json", `{"name": "core", "version": "0.0.0"}`)
	r.SeedPackage("packages", "web")
	r.WriteFile("packages/web/package.json",
		`{"name": "web", "version": "0.0.0", "dependencies": {"core": "^0.0.0"}}`)
	r.Commit("feat(core,web): bootstrap")

	res := r.Command("autoversion", "--range", "tilde")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	web, err := os.ReadFile(r.Path("packages", "web", "package.json"))
	require.NoError(t, err)
	assert.Contains(t, string(web), `"core": "~0.1.0"`, "the flag's policy applied")
	lock, err := os.ReadFile(r.Path("lock.log"))
	require.NoError(t, err, "syncLock must still run under a flag-overridden policy")
	assert.Equal(t, 2, strings.Count(string(lock), "locked"))
}

// TestAutoVersionNoReplaceFlag: --no-replace skips the rules for one
// invocation, leaving the parsing strategy to do its half alone.
func TestAutoVersionNoReplaceFlag(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Spaces["libs"] = models.SpaceConfig{
		Path: models.PathList{"packages"}, Flow: buildPublish(),
		AutoVersion: &models.AutoVersionConfig{
			Replace: []models.AutoVersionReplaceConfig{
				{Files: []string{"README.md"}, Find: "core@{previous}", Write: "core@{version}"},
			},
		},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.WriteFile("packages/core/package.json", `{"name": "core", "version": "0.0.0"}`)
	r.WriteFile("packages/core/README.md", "npm i core@0.0.0\n")
	r.Commit("feat(core): bootstrap")

	res := r.Command("autoversion", "--no-replace", "--sync-lock=false")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	pkg, err := os.ReadFile(r.Path("packages", "core", "package.json"))
	require.NoError(t, err)
	assert.Contains(t, string(pkg), `"version": "0.1.0"`, "the parsing strategy still ran")
	readme, err := os.ReadFile(r.Path("packages", "core", "README.md"))
	require.NoError(t, err)
	assert.Equal(t, "npm i core@0.0.0\n", string(readme), "the rules were skipped")

	// Without the flag the same invocation completes the job.
	res = r.Command("autoversion", "--sync-lock=false")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	readme, err = os.ReadFile(r.Path("packages", "core", "README.md"))
	require.NoError(t, err)
	assert.Equal(t, "npm i core@0.1.0\n", string(readme))
}

// TestAutoVersionManifestsNoneFlag: --manifests none turns the parsing
// strategy off for one invocation; anything outside the three values is a
// usage error.
func TestAutoVersionManifestsNoneFlag(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Spaces["libs"] = models.SpaceConfig{
		Path: models.PathList{"packages"}, Flow: buildPublish(),
		AutoVersion: &models.AutoVersionConfig{Enabled: models.Bool(true)},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.WriteFile("packages/core/package.json", `{"name": "core", "version": "0.0.0"}`)
	r.Commit("feat(core): bootstrap")

	res := r.Command("autoversion", "--manifests", "none", "--sync-lock=false")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	pkg, err := os.ReadFile(r.Path("packages", "core", "package.json"))
	require.NoError(t, err)
	assert.Contains(t, string(pkg), `"version": "0.0.0"`, "the parsing strategy was off")

	res = r.Command("autoversion", "--manifests", "sideways")
	assert.Equal(t, 2, res.Code, "an unknown --manifests value is a usage error")
}

// TestAutoPropagateReleasesLikeAutoVersion: the same workspace released once
// under `autoVersion` and once under `autoPropagate` leaves the same manifests,
// the same tags and the same stage names behind. The hook written as
// beforePropagate runs as the version stage's hook, and the syncLock script
// the block names runs between it and the build.
func TestAutoPropagateReleasesLikeAutoVersion(t *testing.T) {
	release := func(t *testing.T, isPropagate bool) (web, core, stages string, tags []string) {
		t.Helper()
		r := harness.New(t)
		cfg := libsConfig(`echo "build:$DISPAT_STAGE" >> ../../stages.log`, 1)
		cfg.Scripts["locksync"] = models.Script{`echo "lock:$DISPAT_STAGE" >> ../../stages.log`}
		cfg.Scripts["mark"] = models.Script{`echo "hook:$DISPAT_STAGE" >> ../../stages.log`}
		policy := &models.AutoVersionConfig{Match: []string{"workspace:*"}, SyncLock: []string{"locksync"}}
		space := models.SpaceConfig{Path: models.PathList{"packages"}, Flow: buildPublish()}
		if isPropagate {
			space.AutoPropagate = policy
			space.Flow.BeforePropagate = []string{"mark"}
		} else {
			space.AutoVersion = policy
			space.Flow.BeforeVersion = []string{"mark"}
		}
		cfg.Spaces["libs"] = space
		cfg.Dependencies = []models.DependencyConfig{{Consumer: "web", Provider: "core"}}
		r.WriteConfigModel(cfg)
		r.SeedPackage("packages", "core")
		r.SeedPackage("packages", "web")
		r.WriteFile("packages/core/package.json", `{"name": "@acme/core", "version": "0.0.0"}`)
		r.WriteFile("packages/web/package.json",
			`{"name": "@acme/web", "version": "0.0.0", "dependencies": {"@acme/core": "workspace:*"}}`)
		r.Commit("feat(core,web): bootstrap")
		r.ReleaseOK()
		return readFile(t, r, "packages", "web", "package.json"), readFile(t, r, "packages", "core", "package.json"),
			readFile(t, r, "stages.log"), r.TagList()
	}
	web, core, stages, tags := release(t, false)
	pWeb, pCore, pStages, pTags := release(t, true)

	assert.Contains(t, pWeb, `"@acme/core": "^0.1.0"`, "the range is reconciled")
	assert.Contains(t, pWeb, `"version": "0.1.0"`, "the own version is written")
	assert.Equal(t, web, pWeb)
	assert.Equal(t, core, pCore)
	assert.Equal(t, tags, pTags)
	assert.Equal(t, stages, pStages, "the stage and hook names are the version stage's either way")
	assert.Equal(t, 2, strings.Count(pStages, "hook:beforeVersion\n"), "stages:\n%s", pStages)
	assert.Equal(t, 2, strings.Count(pStages, "lock:syncLock\n"), "stages:\n%s", pStages)
}

// TestAutoSignWritesTheOwnVersionBeforePropagate: with autoSign beside
// autoPropagate the sign stage writes each package's own version and the
// propagate stage after it writes the dependency ranges alone. A snapshot taken
// by each stage's post hook shows which stage wrote what, and the lock script
// still runs after a change the sign stage made.
func TestAutoSignWritesTheOwnVersionBeforePropagate(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Scripts["after-sign"] = models.Script{"cp package.json after-sign.json"}
	cfg.Scripts["after-propagate"] = models.Script{"cp package.json after-propagate.json"}
	cfg.Scripts["locksync"] = models.Script{"cp package.json lock-snapshot.json"}
	flow := buildPublish()
	flow.PostSign = []string{"after-sign"}
	flow.PostPropagate = []string{"after-propagate"}
	cfg.Spaces["libs"] = models.SpaceConfig{
		Path:          models.PathList{"packages"},
		Flow:          flow,
		AutoSign:      &models.AutoSignConfig{Enabled: models.Bool(true)},
		AutoPropagate: &models.AutoVersionConfig{Match: []string{"workspace:*"}, SyncLock: []string{"locksync"}},
	}
	cfg.Dependencies = []models.DependencyConfig{{Consumer: "web", Provider: "core"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "web")
	r.WriteFile("packages/core/package.json", `{"name": "@acme/core", "version": "0.0.0"}`)
	r.WriteFile("packages/web/package.json",
		`{"name": "@acme/web", "version": "0.0.0", "dependencies": {"@acme/core": "workspace:*"}}`)
	r.Commit("feat(core,web): bootstrap")

	res := r.ReleaseOK()
	require.Contains(t, r.TagList(), "web@0.1.0")

	afterSign := readFile(t, r, "packages", "web", "after-sign.json")
	assert.Contains(t, afterSign, `"version": "0.1.0"`, "the sign stage wrote the own version")
	assert.Contains(t, afterSign, `"@acme/core": "workspace:*"`, "and left the range to the propagate stage")
	afterPropagate := readFile(t, r, "packages", "web", "after-propagate.json")
	assert.Contains(t, afterPropagate, `"@acme/core": "^0.1.0"`, "the propagate stage wrote the range")
	assert.Equal(t, afterPropagate, readFile(t, r, "packages", "web", "package.json"))
	assert.Contains(t, readFile(t, r, "packages", "core", "lock-snapshot.json"), `"version": "0.1.0"`,
		"the lock follows a manifest only the sign stage changed")

	written := 0
	for _, ev := range res.Events {
		switch ev.Str("message") {
		case "manifest version written":
			assert.Equal(t, "sign", ev.Str("stage"), "%v", ev)
			written++
		case "manifest reconciled":
			assert.Equal(t, "version", ev.Str("stage"), "%v", ev)
			assert.Equal(t, false, ev["versionWritten"], "the propagate stage writes no own version: %v", ev)
		}
	}
	assert.Equal(t, 2, written, "one own-version write per package")
}

// TestAutoSignStandaloneAutoversionWritesRangesOnly: `dispat autoversion`
// runs the propagate stage's reconciliation, so for a package whose sign stage
// owns the own version it writes the ranges alone; --write-version asks for
// the own version explicitly.
func TestAutoSignStandaloneAutoversionWritesRangesOnly(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Spaces["libs"] = models.SpaceConfig{
		Path:        models.PathList{"packages"},
		Flow:        buildPublish(),
		AutoSign:    &models.AutoSignConfig{Enabled: models.Bool(true)},
		AutoVersion: &models.AutoVersionConfig{Match: []string{"workspace:*"}},
	}
	cfg.Dependencies = []models.DependencyConfig{{Consumer: "web", Provider: "core"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "web")
	r.WriteFile("packages/core/package.json", `{"name": "@acme/core", "version": "0.0.0"}`)
	r.WriteFile("packages/web/package.json",
		`{"name": "@acme/web", "version": "0.0.0", "dependencies": {"@acme/core": "workspace:*"}}`)
	r.Commit("feat(core,web): bootstrap")

	res := r.Command("autoversion", "--sync-lock=false")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	web := readFile(t, r, "packages", "web", "package.json")
	assert.Contains(t, web, `"@acme/core": "^0.1.0"`)
	assert.Contains(t, web, `"version": "0.0.0"`, "the own version is the sign stage's")

	res = r.Command("autoversion", "--sync-lock=false", "--write-version")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, readFile(t, r, "packages", "web", "package.json"), `"version": "0.1.0"`)
}

// TestAutoSignAllScopeWritesOnlyThePackagesOwnManifests: `manifests: all`
// lets the sign stage reach a manifest whose folder is part of its format's
// name, Unity's ProjectSettings/ProjectSettings.asset, and still writes the
// package's own manifests alone: an example nested inside the package keeps
// its own version. A manifest the scan cannot parse is reported from the sign
// stage and the ones it could parse are written all the same.
func TestAutoSignAllScopeWritesOnlyThePackagesOwnManifests(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Spaces["libs"] = models.SpaceConfig{
		Path:     models.PathList{"packages"},
		Flow:     buildPublish(),
		AutoSign: &models.AutoSignConfig{Manifests: "all"},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "game")
	r.WriteFile("packages/game/ProjectSettings/ProjectSettings.asset", unityProjectSettings)
	r.WriteFile("packages/game/package.json", `{"name": "@acme/game", "version": "0.0.0"}`)
	r.WriteFile("packages/game/examples/demo/package.json", `{"name": "demo", "version": "7.7.7"}`)
	r.WriteFile("packages/game/examples/broken/package.json", `{"name": "broken", "version": `)
	r.Commit("feat(game): first release")

	res := r.ReleaseOK()
	require.True(t, r.IsTagged("game@0.1.0"), "tags: %v", r.TagList())
	assert.Contains(t, readFile(t, r, "packages", "game", "package.json"), `"version": "0.1.0"`)
	assert.Contains(t, readFile(t, r, "packages", "game", "ProjectSettings", "ProjectSettings.asset"),
		"bundleVersion: 0.1.0", "the scope reached the format whose folder is part of its name")
	assert.Contains(t, readFile(t, r, "packages", "game", "examples", "demo", "package.json"), `"version": "7.7.7"`,
		"a nested example keeps its own version")
	parse := jsonLine(t, res, "auto-signing: some manifests failed to parse")
	assert.Equal(t, "sign", parse.Str("stage"), "reported from the sign stage: %v", parse)
}

// TestAutoSignRefusesABlockThatWritesNothingOrTwice: the sign stage owns the
// package's own version, so a configuration that would have it write nothing,
// or have the version stage write the same field beside it, is refused when
// the configuration loads, naming the keys, before any script or tag. A
// `writeVersion: true` inherited from the top level is refused for a space
// that enables autoSign itself, exactly as one stated beside it.
func TestAutoSignRefusesABlockThatWritesNothingOrTwice(t *testing.T) {
	r := refusalRepo(t)
	signing := func(sign *models.AutoSignConfig, version *models.AutoVersionConfig) func(*models.File) {
		return func(cfg *models.File) {
			cfg.Spaces["libs"] = models.SpaceConfig{Path: models.PathList{"packages"}, Flow: buildPublish(),
				AutoSign: sign, AutoVersion: version}
		}
	}
	runRefusals(t, r, []refusal{
		{name: "a scope that scans nothing",
			mutate: signing(&models.AutoSignConfig{Manifests: "none"}, nil),
			want:   `autoSign: manifests: "none" would write nothing`},
		{name: "a scope the loader does not know",
			mutate: signing(&models.AutoSignConfig{Manifests: "nested"}, nil),
			want:   `autoSign: manifests: unknown value "nested" (want "root" or "all")`},
		{name: "writeVersion stated beside it",
			mutate: signing(&models.AutoSignConfig{Enabled: models.Bool(true)},
				&models.AutoVersionConfig{WriteVersion: models.Bool(true)}),
			want: "autoVersion.writeVersion and autoSign both write the package's own version"},
		{name: "writeVersion inherited from the top level",
			mutate: func(cfg *models.File) {
				signing(&models.AutoSignConfig{Enabled: models.Bool(true)}, nil)(cfg)
				cfg.AutoVersion = &models.AutoVersionConfig{WriteVersion: models.Bool(true)}
			},
			want: "autoVersion.writeVersion and autoSign both write the package's own version"},
	})
}

// TestAutoVersionRefusesAnOnlyNamingNoPackage: autoVersion.only
// narrows a rewrite to named providers, so a name that is no package narrows
// it to nothing — a typo that would otherwise present as "the rewrite silently
// stopped happening". It is refused wherever the block was written.
func TestAutoVersionRefusesAnOnlyNamingNoPackage(t *testing.T) {
	for name, tc := range map[string]struct {
		adjust func(*models.File)
		want   string
	}{
		"declared by the space": {
			adjust: func(cfg *models.File) {
				cfg.Spaces["libs"] = autoVersionSpace(&models.AutoVersionConfig{Only: []string{"nobody"}})
			},
			want: `space "libs": autoVersion.only: unknown package "nobody"`,
		},
		"declared by one package of the space": {
			adjust: func(cfg *models.File) {
				cfg.Spaces["libs"] = models.SpaceConfig{
					Path: models.PathList{"packages"}, Flow: buildPublish(),
					Packages: map[string]models.PackageConfig{
						"core": {AutoVersion: &models.AutoVersionConfig{Only: []string{"nobody"}}},
					},
				}
			},
			want: `space "libs": package "core": autoVersion.only: unknown package "nobody"`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := harness.New(t)
			cfg := libsConfig(echoBuild, 1)
			tc.adjust(&cfg)
			r.WriteConfigModel(cfg)
			r.SeedPackage("packages", "core")
			r.Commit("feat(core): bootstrap")
			configRefused(t, r, tc.want)
		})
	}
}

// TestAutoVersionSyncLockSkippedWhenNothingWasReconciled: syncLock exists to
// regenerate a lock file after a manifest was rewritten, so a release that
// rewrote nothing has nothing to regenerate and the script is not run. A space
// that configured no reconciling strategy at all is the deliberate exception,
// since it never produces the signal to gate on.
func TestAutoVersionSyncLockSkippedWhenNothingWasReconciled(t *testing.T) {
	syncSpace := func(av *models.AutoVersionConfig) models.SpaceConfig {
		return models.SpaceConfig{
			Path: models.PathList{"packages"}, Flow: buildPublish(), AutoVersion: av,
		}
	}

	t.Run("a reconciling space with nothing to reconcile skips it", func(t *testing.T) {
		r := harness.New(t)
		cfg := libsConfig(echoBuild, 1)
		cfg.Scripts["locksync"] = models.Script{`echo ran >> ../../locksync.log`}
		cfg.Spaces["libs"] = syncSpace(&models.AutoVersionConfig{
			// Writing the package's own version is what would otherwise
			// change a file on every release; without it this manifest
			// declares nothing the run can reconcile.
			WriteVersion: models.Bool(false),
			SyncLock:     []string{"locksync"},
		})
		r.WriteConfigModel(cfg)
		r.SeedPackage("packages", "core")
		r.WriteFile("packages/core/package.json", `{"name": "@acme/core", "version": "0.0.0"}`)
		r.Commit("feat(core): bootstrap")

		res := r.ReleaseOK("--log-level", "debug")
		assert.Contains(t, res.Stdout, "syncLock: nothing was reconciled")
		assert.NoFileExists(t, r.Path("locksync.log"),
			"a lock nothing invalidated is not regenerated")
		assert.True(t, r.IsTagged("core@0.1.0"), "tags: %v", r.TagList())
	})
}

// TestAutoVersionSubstringNameMatchReachesAPackageWithNoManifest: the
// substring fallback exists for the workspaces where a package has no manifest
// to declare a name in — a Gradle module, a folder of shell scripts — while
// its consumers still name it in theirs. The declared name's last segment is
// the package's folder name, and "exact" leaves the same declaration alone.
func TestAutoVersionSubstringNameMatchReachesAPackageWithNoManifest(t *testing.T) {
	setup := func(t *testing.T, nameMatch string) *harness.Repo {
		t.Helper()
		r := harness.New(t)
		cfg := libsConfig(echoBuild, 1)
		cfg.Spaces["libs"] = models.SpaceConfig{
			Path:        models.PathList{"packages"},
			Flow:        buildPublish(),
			AutoVersion: &models.AutoVersionConfig{NameMatch: nameMatch},
		}
		cfg.Dependencies = []models.DependencyConfig{{Consumer: "web", Provider: "app"}}
		r.WriteConfigModel(cfg)
		// app is a folder with no manifest at all, so nothing declares the
		// name its consumer writes.
		r.SeedPackage("packages", "app")
		r.SeedPackage("packages", "web")
		r.WriteFile("packages/web/package.json", `{
  "name": "@acme/web",
  "version": "0.0.0",
  "dependencies": {"@acme/app": "0.0.0"}
}`)
		r.Commit("feat(app,web): bootstrap")
		return r
	}

	t.Run("substring", func(t *testing.T) {
		r := setup(t, "substring")
		r.ReleaseOK()
		require.True(t, r.IsTagged("app@0.1.0"), "tags: %v", r.TagList())
		data, err := os.ReadFile(r.Path("packages", "web", "package.json"))
		require.NoError(t, err)
		assert.Contains(t, string(data), `"@acme/app": "^0.1.0"`,
			"the declared name's last segment is the package's folder name")
	})

	t.Run("exact", func(t *testing.T) {
		r := setup(t, "exact")
		r.ReleaseOK()
		require.True(t, r.IsTagged("app@0.1.0"), "tags: %v", r.TagList())
		data, err := os.ReadFile(r.Path("packages", "web", "package.json"))
		require.NoError(t, err)
		assert.Contains(t, string(data), `"@acme/app": "0.0.0"`,
			"without the fallback nothing connects the two, which is the default")
	})
}

// TestAutoVersionReplaceRewritesOnlyWhatItMay: a replace rule walks the
// package folder, and the folders a workspace walk never enters are skipped
// here too — the version text inside node_modules belongs to somebody else's
// code. A link is not a file to rewrite either: rewriting it would write
// through it, twice.
func TestAutoVersionReplaceRewritesOnlyWhatItMay(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating a link needs a privilege the test runner may not have")
	}
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Spaces["libs"] = models.SpaceConfig{
		Path: models.PathList{"packages"},
		Flow: buildPublish(),
		AutoVersion: &models.AutoVersionConfig{
			Manifests: "none",
			Replace: []models.AutoVersionReplaceConfig{
				{Files: []string{"*.marker"}, Find: "core {previous}", Write: "core {version}"},
				// A rule about a provider, in a package that has none: it
				// expands to nothing, so it selects no files at all. The
				// selector reads an empty glob list as "nothing", which is the
				// opposite of what an empty list means for a range policy.
				{Files: []string{"*.marker"}, Find: "{provider} {providerPrevious}",
					Write: "{provider} {providerVersion}"},
			},
		},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.WriteFile("packages/core/version.marker", "core 0.0.0\n")
	r.WriteFile("packages/core/node_modules/vendored/version.marker", "core 0.0.0\n")
	require.NoError(t, os.Symlink(r.Path("packages", "core", "version.marker"),
		r.Path("packages", "core", "link.marker")))
	r.Commit("feat(core): bootstrap")

	r.ReleaseOK()
	require.True(t, r.IsTagged("core@0.1.0"), "tags: %v", r.TagList())

	own, err := os.ReadFile(r.Path("packages", "core", "version.marker"))
	require.NoError(t, err)
	assert.Equal(t, "core 0.1.0\n", string(own))

	vendored, err := os.ReadFile(r.Path("packages", "core", "node_modules", "vendored", "version.marker"))
	require.NoError(t, err)
	assert.Equal(t, "core 0.0.0\n", string(vendored), "a rule must not reach into node_modules")

	info, err := os.Lstat(r.Path("packages", "core", "link.marker"))
	require.NoError(t, err)
	assert.NotZero(t, info.Mode()&os.ModeSymlink, "the link is still a link, not a rewritten copy")
}

// TestAutoVersionReportsManifestsItCannotParse: a manifest that does
// not parse is missing from the name index every later reconciliation reads,
// so a consumer naming that package could silently go unversioned. It is a
// warning rather than a debug line for that reason, said once where the index
// is built and once where the package's own files are reconciled, and the
// manifests that did parse are still rewritten.
func TestAutoVersionReportsManifestsItCannotParse(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Spaces["libs"] = autoVersionSpace(&models.AutoVersionConfig{Manifests: "all"})
	cfg.Dependencies = []models.DependencyConfig{{Consumer: "web", Provider: "core"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "web")
	r.WriteFile("packages/core/package.json", `{"name": "@acme/core", "version": "0.0.0"}`)
	// web's root manifest is not JSON at all: the identity of the package is
	// what the index loses, and the nested one still has to be reconciled.
	r.WriteFile("packages/web/package.json", `{"name": "@acme/web", "version":`)
	r.WriteFile("packages/web/tools/package.json",
		`{"name": "@acme/web-tools", "version": "0.0.0", "dependencies": {"@acme/core": "^0.0.1"}}`)
	r.Commit("feat(core,web): bootstrap")

	res := r.ReleaseOK()
	assert.Contains(t, res.Stdout, "root manifest failed to parse",
		"the index says which package it lost an identity for")
	assert.Contains(t, res.Stdout, "some manifests failed to parse",
		"and the package's own reconciliation says it read a partial scan")

	assert.Contains(t, arRead(t, r, "packages", "web", "tools", "package.json"),
		`"@acme/core": "^0.1.0"`, "the manifests that did parse are still reconciled")
	assert.True(t, r.IsTagged("web@0.1.0"), "and an unreadable manifest is not a failed release; tags: %v", r.TagList())
}

// TestAutoVersionDerivesNothingFromAnAmbiguousName: two packages
// declaring one manifest name make that name answer to nothing, because
// rewriting a range for it would pick one of them arbitrarily. The name is
// reported (W220) and the declaration naming it is left exactly as written.
func TestAutoVersionDerivesNothingFromAnAmbiguousName(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Spaces["libs"] = autoVersionSpace(&models.AutoVersionConfig{Enabled: models.Bool(true)})
	r.WriteConfigModel(cfg)
	for _, name := range []string{"one", "two"} {
		r.SeedPackage("packages", name)
		// One manifest identity, two packages behind it.
		r.WriteFile("packages/"+name+"/package.json", `{"name": "@acme/shared", "version": "0.0.0"}`)
	}
	r.SeedPackage("packages", "app")
	r.WriteFile("packages/app/package.json",
		`{"name": "@acme/app", "version": "0.0.0", "dependencies": {"@acme/shared": "workspace:*"}}`)
	r.Commit("feat(one,two,app): bootstrap")

	res := r.ReleaseOK()
	assert.True(t, harness.IsCodePresent(res.Events, "W220"), "stdout:\n%s", res.Stdout)
	assert.Contains(t, arRead(t, r, "packages", "app", "package.json"),
		`"@acme/shared": "workspace:*"`,
		"a name answering to two packages answers to neither")
	assert.Contains(t, arRead(t, r, "packages", "app", "package.json"),
		`"version": "0.1.0"`, "the package's own version is not an ambiguous name")
}

// TestAutoVersionSelectorsNarrowTheRewrite: the three selectors each
// leave a declaration alone for a different reason — the field it sits in is
// not one of the configured kinds, the provider is not one of the configured
// names, or the range as written is not one the match globs claim. One
// manifest carries all three next to a declaration nothing narrows, so the
// rewrite that does happen proves the others were narrowed rather than broken.
func TestAutoVersionSelectorsNarrowTheRewrite(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Spaces["libs"] = autoVersionSpace(&models.AutoVersionConfig{
		Kinds: []string{"dependencies"},
		Only:  []string{"core", "extra"},
		Match: []string{"workspace:*"},
	})
	cfg.Dependencies = []models.DependencyConfig{
		{Consumer: "web", Provider: "core"},
		{Consumer: "web", Provider: "extra"},
		{Consumer: "web", Provider: "tools", Kind: "devDependencies"},
		{Consumer: "web", Provider: "aside"},
	}
	r.WriteConfigModel(cfg)
	for _, name := range []string{"core", "extra", "tools", "aside"} {
		r.SeedPackage("packages", name)
		r.WriteFile("packages/"+name+"/package.json",
			`{"name": "@acme/`+name+`", "version": "0.0.0"}`)
	}
	r.SeedPackage("packages", "web")
	r.WriteFile("packages/web/package.json", `{
  "name": "@acme/web",
  "version": "0.0.0",
  "dependencies": {
    "@acme/core": "workspace:*",
    "@acme/extra": "1.0.0",
    "@acme/aside": "workspace:*"
  },
  "devDependencies": {"@acme/tools": "workspace:*"}
}`)
	r.Commit("feat(core,extra,tools,aside,web): bootstrap")

	r.ReleaseOK()
	web := arRead(t, r, "packages", "web", "package.json")
	assert.Contains(t, web, `"@acme/core": "^0.1.0"`, "the declaration no selector narrows is rewritten")
	assert.Contains(t, web, `"@acme/extra": "1.0.0"`,
		"a range the match globs do not claim is a hand pin the policy protects")
	assert.Contains(t, web, `"@acme/aside": "workspace:*"`,
		"a provider outside `only` is none of this block's business")
	assert.Contains(t, web, `"@acme/tools": "workspace:*"`,
		"and a field outside `kinds` is not rewritten wherever the provider is listed")
}

// TestAutoVersionResolvesAProviderByItsDeclaredPath: a declaration
// naming a package by a name no manifest in the workspace carries is still a
// workspace edge when it points at the folder with a file: range. That is how
// a workspace whose declared names and folder names disagree is reconciled at
// all, and the replace strategy resolves the same declaration the same way.
func TestAutoVersionResolvesAProviderByItsDeclaredPath(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Spaces["libs"] = autoVersionSpace(&models.AutoVersionConfig{Range: "exact"})
	cfg.Dependencies = []models.DependencyConfig{{Consumer: "web", Provider: "core"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "web")
	r.WriteFile("packages/core/package.json", `{"name": "@acme/core", "version": "0.0.0"}`)
	// "the-core" is a name nothing in the workspace declares; the path is
	// what says which package it is.
	r.WriteFile("packages/web/package.json",
		`{"name": "@acme/web", "version": "0.0.0", "dependencies": {"the-core": "file:../core"}}`)
	r.Commit("feat(core,web): bootstrap")

	r.ReleaseOK()
	assert.Contains(t, arRead(t, r, "packages", "web", "package.json"),
		`"the-core": "0.1.0"`, "the declared path named the provider the declared name did not")
}

// TestAutoVersionOnlyUpdatedLeavesTheRestBehind: `--only-updated` is
// the flag of a job wired to run after every commit — it asks for this run's
// updates alone, so a range that had fallen behind a provider released in an
// earlier run stays behind rather than quietly catching up, and a replace rule
// scoped to such a provider expands into nothing. Without the flag the same
// fixture catches both up, which is what proves the flag is doing the
// narrowing.
func TestAutoVersionOnlyUpdatedLeavesTheRestBehind(t *testing.T) {
	fixture := func(t *testing.T) *harness.Repo {
		t.Helper()
		r := harness.New(t)
		cfg := libsConfig(echoBuild, 1)
		cfg.Spaces["libs"] = autoVersionSpace(&models.AutoVersionConfig{
			WriteVersion: models.Bool(false),
			Replace: []models.AutoVersionReplaceConfig{{
				Files: []string{"README.md"},
				Find:  "{provider}: pinned",
				Write: "{provider}: {providerVersion}",
			}},
		})
		cfg.Dependencies = []models.DependencyConfig{{Consumer: "web", Provider: "core"}}
		r.WriteConfigModel(cfg)
		r.SeedPackage("packages", "core")
		r.SeedPackage("packages", "web")
		r.WriteFile("packages/core/package.json", `{"name": "@acme/core", "version": "0.1.0"}`)
		r.WriteFile("packages/web/package.json",
			`{"name": "@acme/web", "version": "0.0.0", "dependencies": {"@acme/core": "^0.0.1"}}`)
		r.WriteFile("packages/web/README.md", "core: pinned\n")
		r.Commit("feat(core,web): bootstrap")
		// core is already released at this commit and has nothing pending;
		// web's files still name the version before it, which is the "fallen
		// behind" state both runs below start from.
		r.Git("tag", "core@0.1.0")
		return r
	}

	t.Run("the flag keeps a run to its own updates", func(t *testing.T) {
		r := fixture(t)
		res := r.Command("autoversion", "--only-updated", "--since", "all")
		require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Contains(t, arRead(t, r, "packages", "web", "package.json"),
			`"@acme/core": "^0.0.1"`, "no provider this run updates, so no range moves")
		assert.Equal(t, "core: pinned\n", arRead(t, r, "packages", "web", "README.md"),
			"and a rule scoped to such a provider expands into nothing")
	})

	t.Run("without it the same run catches both up", func(t *testing.T) {
		r := fixture(t)
		res := r.Command("autoversion", "--since", "all")
		require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Contains(t, arRead(t, r, "packages", "web", "package.json"),
			`"@acme/core": "^0.1.0"`, "the catch-up is what the flag was turning off")
		assert.Equal(t, "core: 0.1.0\n", arRead(t, r, "packages", "web", "README.md"))
	})
}

// TestAutoVersionRangePolicySpellsEachEcosystem: the keyword policies
// are npm's, and an ecosystem that has no caret cannot be handed one. Python
// pins with ==, and a policy that is neither a keyword nor a {version}
// template is written through verbatim, which is how a workspace protocol
// survives a reconciliation that is otherwise about versions.
func TestAutoVersionRangePolicySpellsEachEcosystem(t *testing.T) {
	t.Run("a python specifier pins whatever keyword was asked for", func(t *testing.T) {
		r := harness.New(t)
		cfg := libsConfig(echoBuild, 1)
		cfg.Spaces["libs"] = autoVersionSpace(&models.AutoVersionConfig{Range: "caret"})
		cfg.Dependencies = []models.DependencyConfig{{Consumer: "app", Provider: "lib"}}
		r.WriteConfigModel(cfg)
		r.SeedPackage("packages", "lib")
		r.SeedPackage("packages", "app")
		r.WriteFile("packages/lib/pyproject.toml", "[project]\nname = \"acme-lib\"\nversion = \"0.0.0\"\n")
		r.WriteFile("packages/app/pyproject.toml",
			"[project]\nname = \"acme-app\"\nversion = \"0.0.0\"\ndependencies = [\"acme-lib==0.0.1\"]\n")
		r.Commit("feat(lib,app): bootstrap")

		r.ReleaseOK()
		app := arRead(t, r, "packages", "app", "pyproject.toml")
		assert.Contains(t, app, "acme-lib==0.1.0", "a caret is not a thing a specifier can carry")
		assert.Contains(t, app, `version = "0.1.0"`, "and the package's own version still advances")
	})

	t.Run("a literal policy is written through as it stands", func(t *testing.T) {
		r := harness.New(t)
		cfg := libsConfig(echoBuild, 1)
		cfg.Spaces["libs"] = autoVersionSpace(&models.AutoVersionConfig{Range: "workspace:^"})
		cfg.Dependencies = []models.DependencyConfig{{Consumer: "web", Provider: "core"}}
		r.WriteConfigModel(cfg)
		r.SeedPackage("packages", "core")
		r.SeedPackage("packages", "web")
		r.WriteFile("packages/core/package.json", `{"name": "@acme/core", "version": "0.0.0"}`)
		r.WriteFile("packages/web/package.json",
			`{"name": "@acme/web", "version": "0.0.0", "dependencies": {"@acme/core": "workspace:*"}}`)
		r.Commit("feat(core,web): bootstrap")

		r.ReleaseOK()
		assert.Contains(t, arRead(t, r, "packages", "web", "package.json"),
			`"@acme/core": "workspace:^"`,
			"a policy naming no version is a protocol, not a range to compute")
	})
}

// TestAutoVersionReplaceRuleStepsOverAFolderItCannotEnter: failing a release over
// an unreadable folder no rule was ever going to reach would be the worse
// trade, so the folder is named in a warning and skipped whole, and the files
// the rule could reach are rewritten as if it were not there.
func TestAutoVersionReplaceRuleStepsOverAFolderItCannotEnter(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a folder's mode does not gate a directory read on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root enters a folder whatever its mode says")
	}
	r := harness.New(t)
	r.WriteConfigModel(libsConfig(echoBuild, 1))
	r.SeedPackage("packages", "core")
	r.WriteFile("packages/core/pin.txt", "pinned at 0.0.1\n")
	r.WriteFile("packages/core/sealed/pin.txt", "pinned at 0.0.1\n")
	r.Commit("feat(core): bootstrap")

	sealed := r.Path("packages", "core", "sealed")
	require.NoError(t, os.Chmod(sealed, 0o000))
	t.Cleanup(func() { _ = os.Chmod(sealed, 0o755) })

	res := r.Command("autoreplacer", "--replace", "pinned at 0.0.1=>pinned at {version}",
		"--files", "*.txt", "--since", "all")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "folder skipped", "the folder that was stepped over is named")
	assert.Contains(t, res.Stdout, "sealed")

	assert.Equal(t, "pinned at 0.1.0\n", arRead(t, r, "packages", "core", "pin.txt"),
		"everything the rule could reach was still rewritten")
}

// TestAutoVersionManifestSurvivesAPartialDiskWrite exercises the public manifest
// writer through the CLI. A short temporary-file write cannot replace a
// package manifest with its truncated prefix, including on runtimes that
// report the short count without an error.
func TestAutoVersionManifestSurvivesAPartialDiskWrite(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the file-size limit fixture uses a POSIX shell")
	}
	r := singlePackageRepo(t, echoBuild)
	body := `{"name":"core","version":"0.0.0","description":"` +
		strings.Repeat("previous manifest note", 150000) + `"}`
	r.WriteFile("packages/core/package.json", body)
	r.Commit("feat(core): first feature")
	path := r.Path("packages", "core", "package.json")
	require.NoError(t, os.Chmod(path, 0o640))

	failed := r.Shell("trap '' XFSZ; ulimit -f 2048; dispat autowriter --package core --set-version 0.1.0")

	require.Equal(t, 1, failed.Code, "stdout:\n%s\nstderr:\n%s", failed.Stdout, failed.Stderr)
	assert.Regexp(t, `file too large|short write`, failed.Stdout+failed.Stderr)
	assert.Equal(t, body, readRepoFile(t, r, "packages/core/package.json"))
	partial, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".dispat-write-*"))
	require.NoError(t, err)
	assert.Empty(t, partial, "the incomplete manifest was removed")
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o640), info.Mode().Perm())

	retried := r.Command("autowriter", "--package", "core", "--set-version", "0.1.0")
	require.Equal(t, 0, retried.Code, "stdout:\n%s\nstderr:\n%s", retried.Stdout, retried.Stderr)
	assert.Equal(t, strings.Replace(body, `"version":"0.0.0"`, `"version":"0.1.0"`, 1),
		readRepoFile(t, r, "packages/core/package.json"))
}

// autoVersionSpace is the libs space with one autoVersion block and nothing
// else: every scenario here differs only in that block and in the manifests
// on disk.
func autoVersionSpace(av *models.AutoVersionConfig) models.SpaceConfig {
	return models.SpaceConfig{Path: models.PathList{"packages"}, Flow: buildPublish(), AutoVersion: av}
}
