package integration

// Area 11: native auto-versioning through the compiled binary. A space with
// an autoVersion block gets its files reconciled by dispat itself at the
// version stage — the parsing strategy rewriting declared ranges and own
// versions under the match / range policy, the replacing strategy
// substituting literal text in whatever else the release has to keep in step
// — and its syncLock scripts run between version and build under their own
// concurrency budget. Either strategy may be off, and with both off the lock
// scripts are the whole of the version stage.

import (
	"os"
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
	r.CommitEmpty("feat(core)%beta: risky rewrite\n---\nfix(web): stable work of its own")
	res = r.ReleaseOK()
	assert.True(t, harness.HasCodeForPackage(res.Events, "W203", "web"),
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
		Path: "packages",
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
	assert.False(t, harness.HasCodeForPackage(res.Events, "W222", "web"),
		"both rules matched, so nothing is reported stale")
}

// TestAutoVersionReplaceRuleMatchedNothing: a mistyped rule reconciles
// nothing and says so (W222) rather than failing silently for as many
// releases as it takes somebody to notice.
func TestAutoVersionReplaceRuleMatchedNothing(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Spaces["libs"] = models.SpaceConfig{
		Path: "packages",
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
	assert.True(t, harness.HasCodeForPackage(res.Events, "W222", "core"),
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
		Path: "packages", Flow: buildPublish(),
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
	cfg.Scripts["locksync"] = r.TsmarkScript("synclock.log", "$DISPAT_PACKAGE", 120*time.Millisecond)
	cfg.Spaces["libs"] = models.SpaceConfig{
		Path: "packages",
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
	cfg.Scripts["mark-lock"] = "echo locked >> ../../lock.log"
	cfg.Spaces["libs"] = models.SpaceConfig{
		Path: "packages", Flow: buildPublish(),
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
	cfg.Scripts["mark-lock"] = "echo locked >> ../../lock.log"
	cfg.Spaces["libs"] = models.SpaceConfig{
		Path: "packages", Flow: buildPublish(),
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
		Path: "packages", Flow: buildPublish(),
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
		Path: "packages", Flow: buildPublish(),
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
