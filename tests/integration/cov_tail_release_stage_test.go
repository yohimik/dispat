// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Long-tail coverage for the stage machinery of a release: the frames the
// executor decides not to run, the hook that fails before any stage exists,
// and the export file a stage hands back. Each one is a decision the executor
// makes about a package before or instead of running its scripts, and each is
// asserted on what the folder holds afterwards as well as on what the operator
// was told, because a stage silently not running is the failure mode all of
// them share.

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestCovTailBeforeAllFailureEndsThePackageBeforeItsFirstStage: beforeAll is
// the one hook that runs before a package has a stage at all, so its failure
// has to end the package there — no version, no build, no publish, no tag —
// while the packages around it release. The outcome hook still observes the
// failure, and the stage it names is the one the package never reached.
func TestCovTailBeforeAllFailureEndsThePackageBeforeItsFirstStage(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(markerBuild, 1)
	cfg.Scripts["gate"] = models.Script{`[ "$DISPAT_PACKAGE" != gated ]`}
	cfg.Scripts["note-fail"] = models.Script{`echo "$DISPAT_PACKAGE $DISPAT_FAILED_STAGE" >> ../../onfail.log`}
	cfg.Spaces["libs"] = models.SpaceConfig{
		Path: models.PathList{"packages"},
		Flow: &models.SpaceFlowConfig{
			Build:     []string{"build"},
			Publish:   []string{"publish"},
			BeforeAll: []string{"gate"},
			OnFail:    []string{"note-fail"},
		},
	}
	r.WriteConfigModel(cfg)
	seedIndependentPackages(r, []string{"gated", "sound"})

	res := r.Release()
	assert.NotEqual(t, 0, res.Code, "a package that failed fails the run; stdout:\n%s", res.Stdout)
	assert.Contains(t, res.Stdout, "beforeAll hook failed")

	assert.False(t, r.IsTagged("gated@0.1.0"), "tags: %v", r.TagList())
	assert.True(t, r.IsTagged("sound@0.1.0"), "the package beside it is unaffected; tags: %v", r.TagList())
	assert.Equal(t, 1, buildRuns(r), "only the sound package reached a build")

	onfail, err := os.ReadFile(r.Path("onfail.log"))
	require.NoError(t, err, "onFail observes a failure that settled before any stage")
	assert.Equal(t, "gated build\n", string(onfail),
		"the stage named is the first one this package had, the one it never entered")
}

// TestCovTailVersionScriptsSkippedWhenEveryProviderDied: a consumer's version
// stage exists to sync its manifests to the versions its providers just
// published. With its own changes to release it is not skipped when a provider
// fails, but there is then nothing for the stage to sync to, so its scripts and
// their hooks do not run at all rather than run against a version nobody
// published. The same fixture with the provider alive is what proves the skip
// is about the dead provider and not about the configuration.
func TestCovTailVersionScriptsSkippedWhenEveryProviderDied(t *testing.T) {
	linked := func(t *testing.T) *harness.Repo {
		t.Helper()
		r := harness.New(t)
		cfg := libsConfig(failIfMarker, 1)
		cfg.Scripts["stamp"] = models.Script{`echo "$DISPAT_PACKAGE" >> ../../version-ran.log`}
		cfg.Scripts["before-stamp"] = models.Script{`echo "$DISPAT_PACKAGE before" >> ../../version-ran.log`}
		cfg.Spaces["libs"] = models.SpaceConfig{
			Path: models.PathList{"packages"},
			Flow: &models.SpaceFlowConfig{
				Build:         []string{"build"},
				Publish:       []string{"publish"},
				Version:       []string{"stamp"},
				BeforeVersion: []string{"before-stamp"},
			},
		}
		cfg.Dependencies = []models.DependencyConfig{{Consumer: "web", Provider: "core"}}
		r.WriteConfigModel(cfg)
		r.SeedPackage("packages", "core")
		r.SeedPackage("packages", "web")
		r.Commit("feat(core,web): both packages change in their own right")
		return r
	}

	t.Run("the provider published, so the stage has something to sync to", func(t *testing.T) {
		r := linked(t)
		r.ReleaseOK()

		ran, err := os.ReadFile(r.Path("version-ran.log"))
		require.NoError(t, err)
		assert.Contains(t, string(ran), "web before", "the hook bracketing the stage ran")
		assert.Contains(t, string(ran), "web\n", "and the stage's own script with it")
	})

	t.Run("every provider died, so the stage has nothing to sync to", func(t *testing.T) {
		r := linked(t)
		// core's build fails, so its publish never happens and the version it
		// was going to hand web is never real.
		r.WriteFile("packages/core/FAIL", "")

		res := r.Release()
		assert.NotEqual(t, 0, res.Code, "stdout:\n%s", res.Stdout)
		assert.Contains(t, res.Stdout, "no successfully updated providers, skipping scripts")
		assert.False(t, r.IsTagged("core@0.1.0"), "tags: %v", r.TagList())
		assert.True(t, r.IsTagged("web@0.1.0"),
			"a consumer with changes of its own still releases; tags: %v", r.TagList())
		assert.NoFileExists(t, r.Path("version-ran.log"),
			"the stage's scripts and their hooks are exactly what the dead provider skips")
	})
}

// TestCovTailSyncLockSkippedWhenNothingWasReconciled: syncLock exists to
// regenerate a lock file after a manifest was rewritten, so a release that
// rewrote nothing has nothing to regenerate and the script is not run. A space
// that configured no reconciling strategy at all is the deliberate exception,
// since it never produces the signal to gate on.
func TestCovTailSyncLockSkippedWhenNothingWasReconciled(t *testing.T) {
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

// TestCovTailStaticEnvExpandsAgainstTheComputedSet: a static env value is
// never shell-expanded by exec, so dispat expands it itself — against the
// computed release variables first, then the process environment, with `$$`
// standing for a literal dollar and an unknown name expanding to nothing,
// exactly as a shell would read the same text.
func TestCovTailStaticEnvExpandsAgainstTheComputedSet(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(`printf '%s\n' "$FROM_RUN" "$FROM_PROCESS" "$LITERAL" "$NOBODY_SET" > ../../env.txt`, 1)
	cfg.Env = map[string]string{
		"FROM_RUN":     "${DISPAT_PACKAGE}@$DISPAT_NEW_VERSION",
		"FROM_PROCESS": "seen $DISPAT_IT_STATIC_ENV_PROBE",
		"LITERAL":      "costs $$5",
		"NOBODY_SET":   "[$THIS_NAME_IS_NOT_SET]",
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): bootstrap")

	res := r.CommandEnv([]string{"DISPAT_IT_STATIC_ENV_PROBE=from-the-process"})
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)

	data, err := os.ReadFile(r.Path("env.txt"))
	require.NoError(t, err)
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	require.Len(t, lines, 4, "output:\n%s", data)
	assert.Equal(t, "core@0.1.0", lines[0], "the computed set answers first")
	assert.Equal(t, "seen from-the-process", lines[1], "then the process environment")
	assert.Equal(t, "costs $5", lines[2], "$$ is one literal dollar")
	assert.Equal(t, "[]", lines[3], "an unknown name expands to nothing, as in a shell")
}
