package integration

// Area 11: the static `env` layers. A dispat config may declare plain
// environment variables at the top level, on a space and on a package, and
// they merge with the most local layer winning. What only the running binary
// can witness is the part that matters: that a key arrives in a script's
// environment with its case intact (dispat lowercases every map key it reads,
// so the exact spelling has to be restored on the way out), that a value
// referring to `$DISPAT_VERSION` expands against the package the script is
// running for, and that the keys which could never reach a script intact are
// refused at load rather than dropped in silence.
//
// The refusals are the load-bearing half. A script may trust `DISPAT_VERSION`
// precisely because no static key is allowed to shadow it.

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestStaticEnvReachesScripts: the `env` objects merge across the top-level,
// space and package layers with the most local winning, keys keep their exact
// case through the binary, values expand $DISPAT_* references per package, and
// a static key can never shadow a computed DISPAT_* variable.
func TestStaticEnvReachesScripts(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(
		`printf '%s|%s|%s|%s|%s' "$ROOT_V" "$SHARED" "$MiXed_Case" "$CUSTOM_TAG" "$DISPAT_VERSION" > env.txt`, 1)
	cfg.Env = map[string]string{
		"ROOT_V":     "root",
		"SHARED":     "from-root",
		"MiXed_Case": "kept",
		// A static key may not claim the DISPAT_ namespace, but it may well
		// build a value out of one.
		"CUSTOM_TAG": "custom_$DISPAT_VERSION",
	}
	libs := cfg.Spaces["libs"]
	libs.Env = map[string]string{"SHARED": "from-space"}
	cfg.Spaces["libs"] = libs
	cfg.Packages = map[string]models.PackageConfig{
		"core": {Env: map[string]string{"SHARED": "from-package"}},
	}
	// The top-level layer also reaches the run-level hooks, which run in the
	// monorepo root outside any space or package.
	cfg.Scripts["hook-probe"] = models.Script{`printf '%s|%s' "$ROOT_V" "$SHARED" > hook_env.txt`}
	cfg.Run = &models.RunConfig{PostAll: []string{"hook-probe"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): env probe")
	r.ReleaseOK()

	data, err := os.ReadFile(r.Path("packages", "core", "env.txt"))
	require.NoError(t, err)
	assert.Equal(t, "root|from-package|kept|custom_0.1.0|0.1.0", string(data),
		"layers merge most-local-first, case survives, and $DISPAT_VERSION expands")

	hook, err := os.ReadFile(r.Path("hook_env.txt"))
	require.NoError(t, err)
	assert.Equal(t, "root|from-root", string(hook),
		"a run hook sees the top-level layer only: no space or package map applies")
}

// TestStaticEnvCannotShadowComputedVariables: the DISPAT_ prefix is reserved,
// so the one way a configuration could lie to a script about its own release
// is refused at load time rather than silently ignored.
func TestStaticEnvCannotShadowComputedVariables(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Env = map[string]string{"DISPAT_VERSION": "9.9.9"}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first")

	// A config-load refusal happens before the configured logger exists, so it
	// is reported on stderr by the bootstrap logger.
	res := r.Status()
	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stderr, "reserved DISPAT_ prefix")
}

// TestStaticEnvRefusesUnusableKeys: the two other spellings that could never
// reach a script intact, each named where it was written.
func TestStaticEnvRefusesUnusableKeys(t *testing.T) {
	cases := map[string]struct{ key, want string }{
		"equals in the key": {"A=B", "must not contain '='"},
		"empty key":         {"", "contains an empty key"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r := harness.New(t)
			cfg := libsConfig(echoBuild, 1)
			cfg.Env = map[string]string{c.key: "v"}
			r.WriteConfigModel(cfg)
			r.SeedPackage("packages", "core")
			r.Commit("feat(core): first")

			res := r.Status()
			require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.Contains(t, res.Stderr, c.want)
		})
	}
}

// TestStaticEnvFromFolderConfigFiles: the two in-folder layers — a space
// folder's own config file and a package folder's — reach the scripts through
// the binary, with their key case intact and the most local winning.
func TestStaticEnvFromFolderConfigFiles(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(`printf '%s|%s|%s|%s' "$Root" "$Shared" "$SpaceFile" "$PkgFile" > env.txt`, 1)
	cfg.Env = map[string]string{"Root": "root", "Shared": "root"}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")

	writeModel := func(relPath string, v any) {
		data, err := json.MarshalIndent(v, "", "  ")
		require.NoError(t, err)
		r.WriteFile(relPath, string(data))
	}
	writeModel("packages/dispat.json", models.SpaceFile{
		Env: map[string]string{"Shared": "space-file", "SpaceFile": "yes"},
	})
	writeModel("packages/core/dispat.json", models.PackageConfig{
		Env: map[string]string{"Shared": "pkg-file", "PkgFile": "yes"},
	})
	r.Commit("feat(core): folder env")
	r.ReleaseOK()

	data, err := os.ReadFile(r.Path("packages", "core", "env.txt"))
	require.NoError(t, err)
	assert.Equal(t, "root|pkg-file|yes|yes", string(data))
}

// TestStaticEnvReachesTheLoginScript: a space's env reaches its login script,
// which runs once per space in the space folder with no package in view. The
// registry a package publishes to is configuration, so the command
// authenticating against it needs the same value the publish will use.
func TestStaticEnvReachesTheLoginScript(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Scripts["login"] = models.Script{`printf '%s|%s' "$REGISTRY" "$DISPAT_SPACE" > ../login_env.txt`}
	libs := cfg.Spaces["libs"]
	libs.Flow.Login = []string{"login"}
	libs.Env = map[string]string{"REGISTRY": "https://npm.corp.example"}
	cfg.Spaces["libs"] = libs
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): login env")
	r.ReleaseOK()

	data, err := os.ReadFile(r.Path("login_env.txt"))
	require.NoError(t, err)
	assert.Equal(t, "https://npm.corp.example|libs", string(data))
}

// TestStaticEnvFromARefKeepsKeyCase: an `env` object written in a referenced
// fragment reaches a script exactly as an inline one does, case and all. The
// two features meet here: the loader parses a fragment with its own parser,
// and the exact spelling has to survive that as much as it survives the root
// file's parse.
func TestStaticEnvFromARefKeepsKeyCase(t *testing.T) {
	r := harness.New(t)
	r.WriteFile("cfg/env.yaml", "MiXed_Case: kept\nROOT_V: root\n")
	r.WriteConfigRaw(map[string]any{
		"logLevel":    "info",
		"logFormat":   "json",
		"github":      map[string]any{"enabled": false},
		"updateCheck": false,
		"scripts": map[string]any{
			"build":   `printf '%s|%s' "$MiXed_Case" "$ROOT_V" > env.txt`,
			"publish": "echo publishing",
		},
		"env": map[string]any{"$ref": "./cfg/env.yaml"},
		"spaces": map[string]any{
			"libs": map[string]any{"path": "packages",
				"flow": map[string]any{"build": []string{"build"}, "publish": []string{"publish"}}},
		},
	})
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): referenced env")

	r.ReleaseOK()
	written, err := os.ReadFile(r.Path("packages", "core", "env.txt"))
	require.NoError(t, err)
	assert.Equal(t, "kept|root", string(written),
		"a referenced env object keeps its keys spelled as the fragment wrote them")
}

// TestDotenvReachesScriptsAndDispat: the `.env` beside the terminal is read
// into the run's own environment, so a script sees it — and so does dispat,
// which is what lets a token live in a file. The two rules that keep it
// predictable are proven beside it: a variable the environment already sets is
// never replaced, and the config's own `env` still wins over both.
func TestDotenvReachesScriptsAndDispat(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(`printf '%s|%s|%s' "$FROM_FILE" "$FROM_ENVIRONMENT" "$FROM_CONFIG" > env.txt`, 1)
	cfg.Env = map[string]string{"FROM_CONFIG": "config"}
	r.WriteConfigModel(cfg)
	r.WriteFile(".env", "FROM_FILE=file\nFROM_ENVIRONMENT=file\nFROM_CONFIG=file\nDISPAT_IT_TOKEN=from-file\n")
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): dotenv")
	r.WorkFrom() // the repository root is where the .env sits

	res := r.CommandEnv([]string{"FROM_ENVIRONMENT=environment"}, "release")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	written, err := os.ReadFile(r.Path("packages", "core", "env.txt"))
	require.NoError(t, err)
	assert.Equal(t, "file|environment|config", string(written),
		"the file fills in what nothing else set, the environment beats the file, the config beats both")

	// And dispat's own reads see it: the token the github step looks up is
	// resolved from the file, which is only visible because the step reports
	// the variable it read.
	assert.NotContains(t, res.Stdout, "from-file", "a value from an environment file is never logged")
}

// TestDotenvFileFlag: --env-file replaces the default file, is repeatable with
// the later file winning, and a named file that is not there stops the run
// rather than being quietly skipped.
func TestDotenvFileFlag(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(libsConfig(`printf '%s|%s' "$SOURCE" "$ONLY_BASE" > env.txt`, 1))
	r.WriteFile(".env", "SOURCE=default\n")
	r.WriteFile("base.env", "SOURCE=base\nONLY_BASE=yes\n")
	r.WriteFile("ci.env", "SOURCE=ci\n")
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): dotenv flag")
	r.WorkFrom()

	res := r.Release("--env-file", "base.env", "--env-file", "ci.env")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	written, err := os.ReadFile(r.Path("packages", "core", "env.txt"))
	require.NoError(t, err)
	assert.Equal(t, "ci|yes", string(written),
		"the named files replace ./.env, and the later one wins")

	missing := r.Status("--env-file", "absent.env")
	assert.Equal(t, 1, missing.Code, "a named file that is not there is a mistake worth stopping for")
	assert.Contains(t, missing.Stderr, "cannot read the environment file")
}

// TestDotenvSteersDispatItself: an environment file feeds dispat's own reads,
// not only the scripts it runs. The changelog footer is where that is visible
// without a network: dispat expands $VARIABLE in record text itself, while
// writing the file, so a value that only ever existed in the file arriving in
// the changelog is the whole claim.
func TestDotenvSteersDispatItself(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Changelog = &models.ChangelogConfig{
		EntryFormatConfig: models.EntryFormatConfig{
			Footer: []models.EntryLine{{Line: []string{"released by $DISPAT_IT_RELEASER"}}},
		},
	}
	r.WriteConfigModel(cfg)
	r.WriteFile(".env", "DISPAT_IT_RELEASER=the-env-file\n")
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): dotenv switch")
	r.WorkFrom()

	r.ReleaseOK()
	log, err := os.ReadFile(r.Path("packages", "core", "CHANGELOG.md"))
	require.NoError(t, err)
	assert.Contains(t, string(log), "released by the-env-file",
		"dispat expanded a variable that only the environment file defined")
}
