package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/config"
)

// `dispat exec`'s two questions, asked apart: which text the subject resolves
// to, and what the subject adds to the environment. Both run against a fake
// runner over a workspace on disk, since script and env layering are facts
// about a discovered package rather than about the config map alone. That the
// text then reaches a real shell is the integration suite's claim.

// execRepo writes a two-package workspace and returns the App over it. The
// config is stated in full per test, because the config is the test input.
func execRepo(t *testing.T, cfg *config.File) *App {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{"packages/core", "packages/api", "standalone"} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, dir), 0o755))
		name := filepath.Base(dir)
		require.NoError(t, os.WriteFile(filepath.Join(root, dir, "package.json"),
			[]byte(`{"name":"`+name+`","version":"1.0.0"}`), 0o644))
	}
	return New(root, cfg, zerolog.Nop())
}

// layeredConfig is the shape most of these tests need: a name declared at
// every level, so which level answered is visible in the output.
func layeredConfig() *config.File {
	return &config.File{
		Scripts: map[string]config.Script{"build": {"root-build"}, "only-root": {"root-only"}},
		Env:     map[string]string{"MSG": "from-root", "ROOT_ONLY": "yes"},
		Spaces: map[string]config.SpaceConfig{
			"libs": {
				Path: "packages",
				// Flow is what config.Load fills in; a config built by hand
				// states it, as every other discovery test here does.
				Flow:    &config.SpaceFlowConfig{},
				Scripts: map[string]config.Script{"build": {"space-build"}, "only-space": {"space-only"}},
				Env:     map[string]string{"MSG": "from-space"},
				Packages: map[string]config.PackageConfig{
					"core": {
						Scripts: map[string]config.Script{"build": {"core-build"}},
						Env:     map[string]string{"MSG": "from-core"},
					},
				},
			},
		},
		Packages: map[string]config.PackageConfig{
			"standalone": {Path: "standalone", Scripts: map[string]config.Script{"build": {"alone-build"}}},
		},
	}
}

// runExec drives Exec with a fake runner and returns it for inspection.
func runExec(t *testing.T, a *App, opts ExecOptions) (*fakeRunner, int, error) {
	t.Helper()
	f := &fakeRunner{}
	opts.Runner = f
	code, err := a.Exec(context.Background(), opts)
	return f, code, err
}

func TestExecResolvesTheSubjectsScript(t *testing.T) {
	// The subject decides which map is read, and only that map: naming a level
	// must not silently run text from a level nobody asked about.
	a := execRepo(t, layeredConfig())
	for name, tc := range map[string]struct {
		subject ExecSubject
		want    string
	}{
		"top level":  {ExecSubjectRoot(), "root-build"},
		"space":      {ExecSubjectSpace("libs"), "space-build"},
		"package":    {ExecSubjectPackage("core"), "core-build"},
		"standalone": {ExecSubjectPackage("standalone"), "alone-build"},
	} {
		t.Run(name, func(t *testing.T) {
			f, code, err := runExec(t, a, ExecOptions{Script: "build", Subject: tc.subject})
			require.NoError(t, err)
			assert.Equal(t, 0, code)
			assert.Equal(t, []string{tc.want}, f.ran)
		})
	}
}

func TestExecScriptLookupIsCaseInsensitive(t *testing.T) {
	// Viper lowercases the config's map keys, so a name spelled with capitals
	// on the command line has to find the entry anyway.
	a := execRepo(t, &config.File{Scripts: map[string]config.Script{"build": {"root-build"}}})
	f, _, err := runExec(t, a, ExecOptions{Script: "BUILD"})
	require.NoError(t, err)
	assert.Equal(t, []string{"root-build"}, f.ran)
}

func TestExecWithoutFallbackRefusesANameFromAnotherLevel(t *testing.T) {
	// The whole point of the exact mode: a script defined a level away is a
	// mistake worth reporting, not text to run quietly.
	a := execRepo(t, layeredConfig())
	_, code, err := runExec(t, a, ExecOptions{Script: "only-root", Subject: ExecSubjectPackage("core")})
	require.Error(t, err)
	assert.Equal(t, 1, code)
	assert.Contains(t, err.Error(), `no script "only-root"`)
	assert.Contains(t, err.Error(), `package "core"`, "the message names the level that was read")
	assert.NotContains(t, err.Error(), "nor in", "the exact mode tried one level, and says so")
}

func TestExecFallbackWalksUpToTheTopLevel(t *testing.T) {
	// --fallback is dispat run's own resolution, so a name declared once at
	// the top level is reachable from any package.
	a := execRepo(t, layeredConfig())
	for name, subj := range map[string]ExecSubject{
		"from a package": ExecSubjectPackage("core"),
		"from a space":   ExecSubjectSpace("libs"),
	} {
		t.Run(name, func(t *testing.T) {
			f, _, err := runExec(t, a, ExecOptions{Script: "only-root", Subject: subj, Fallback: true})
			require.NoError(t, err)
			assert.Equal(t, []string{"root-only"}, f.ran)
		})
	}
}

func TestExecFallbackStillPrefersTheNearerLevel(t *testing.T) {
	// Falling back must not flatten the layers: the package's own answer wins
	// over its space's, which wins over the top level's.
	a := execRepo(t, layeredConfig())
	f, _, err := runExec(t, a, ExecOptions{Script: "build", Subject: ExecSubjectPackage("core"), Fallback: true})
	require.NoError(t, err)
	assert.Equal(t, []string{"core-build"}, f.ran, "the nearest level that has the name answers")
}

func TestExecFallbackReportsEveryLevelItTried(t *testing.T) {
	// A missing script and a misplaced one read differently, which is the
	// reason the two modes have separate messages.
	a := execRepo(t, layeredConfig())
	_, _, err := runExec(t, a, ExecOptions{Script: "ghost", Subject: ExecSubjectPackage("core"), Fallback: true})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nor in", "the layered mode names the whole chain")
}

func TestExecRefusesAnUnknownSubject(t *testing.T) {
	a := execRepo(t, layeredConfig())
	for name, subj := range map[string]ExecSubject{
		"package": ExecSubjectPackage("ghost"),
		"space":   ExecSubjectSpace("ghost"),
	} {
		t.Run(name, func(t *testing.T) {
			_, code, err := runExec(t, a, ExecOptions{Script: "build", Subject: subj})
			require.Error(t, err)
			assert.Equal(t, 1, code)
			assert.Contains(t, err.Error(), "ghost")
		})
	}
}

func TestExecRefusesAnUnknownSubjectEvenWhenTheTextResolved(t *testing.T) {
	// --script-from can make the lookup succeed while the subject is still
	// nonsense, which is the one way the environment gets to fail on its own.
	// It must fail rather than quietly hand the script an empty environment.
	a := execRepo(t, layeredConfig())
	from := ExecSubjectRoot()
	for name, subj := range map[string]ExecSubject{
		"package": ExecSubjectPackage("ghost"),
		"space":   ExecSubjectSpace("ghost"),
	} {
		t.Run(name, func(t *testing.T) {
			_, code, err := runExec(t, a, ExecOptions{
				Script: "only-root", Subject: subj, ScriptFrom: &from,
			})
			require.Error(t, err)
			assert.Equal(t, 1, code)
			assert.Contains(t, err.Error(), "ghost")
		})
	}
}

func TestExecScriptFromMovesTheTextAndNotTheEnvironment(t *testing.T) {
	// The crossed case: core's text, api's context. If --script-from ever
	// touched the environment the two subjects would collapse back into one,
	// which is the design this flag exists to avoid.
	a := execRepo(t, layeredConfig())
	from := ExecSubjectPackage("core")
	f, _, err := runExec(t, a, ExecOptions{
		Script: "build", Subject: ExecSubjectSpace("libs"), ScriptFrom: &from,
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"core-build"}, f.ran, "the text comes from --script-from")
	assert.Contains(t, f.envs[0], "MSG=from-space", "the environment stays with the subject")
}

func TestExecFallbackLayersFromTheOverriddenLocation(t *testing.T) {
	// With --script-from the chain starts where the text was redirected to,
	// not at the subject: the flag moved the lookup, so it moves all of it.
	a := execRepo(t, layeredConfig())
	from := ExecSubjectSpace("libs")
	f, _, err := runExec(t, a, ExecOptions{
		Script: "only-space", Subject: ExecSubjectRoot(), ScriptFrom: &from, Fallback: true,
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"space-only"}, f.ran)
}

func TestExecStaticEnvLayersOverTheSubject(t *testing.T) {
	// The declared env is always layered, file under space under package,
	// because that is what a release hands a script.
	a := execRepo(t, layeredConfig())
	for name, tc := range map[string]struct {
		subject ExecSubject
		want    string
	}{
		"top level": {ExecSubjectRoot(), "MSG=from-root"},
		"space":     {ExecSubjectSpace("libs"), "MSG=from-space"},
		"package":   {ExecSubjectPackage("core"), "MSG=from-core"},
	} {
		t.Run(name, func(t *testing.T) {
			f, _, err := runExec(t, a, ExecOptions{Script: "build", Subject: tc.subject, Fallback: true})
			require.NoError(t, err)
			assert.Contains(t, f.envs[0], tc.want)
			assert.Contains(t, f.envs[0], "ROOT_ONLY=yes",
				"a name only the file declares still reaches the nearer subject")
		})
	}
}

func TestExecExpandsReferencesInDeclaredValues(t *testing.T) {
	// $NAME in a declared value resolves the way it does inside a release,
	// which is what keeps a script movable between exec and a stage.
	t.Setenv("OUTER", "expanded")
	a := execRepo(t, &config.File{
		Scripts: map[string]config.Script{"build": {"root-build"}},
		Env:     map[string]string{"DERIVED": "v-$OUTER"},
	})
	f, _, err := runExec(t, a, ExecOptions{Script: "build"})
	require.NoError(t, err)
	assert.Contains(t, f.envs[0], "DERIVED=v-expanded")
}

func TestExecAddsNothingBeyondTheDeclaredEnv(t *testing.T) {
	// The default scope computes no plan, so no DISPAT_* variable is invented.
	// A repository with no tags at all must still work, which is what proves
	// git was never consulted.
	a := execRepo(t, &config.File{Scripts: map[string]config.Script{"build": {"root-build"}}})
	f, code, err := runExec(t, a, ExecOptions{Script: "build", Env: EnvScopeStatic})
	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Empty(t, f.envs[0], "nothing is declared, so nothing is added")
}

func TestExecPropagatesTheScriptsExitCode(t *testing.T) {
	a := execRepo(t, &config.File{Scripts: map[string]config.Script{"build": {"boom"}}})
	f := &fakeRunner{outcomes: map[string]error{"boom": exitErr(t, 7)}}
	code, err := a.Exec(context.Background(), ExecOptions{Script: "build", Runner: f})
	require.NoError(t, err)
	assert.Equal(t, 7, code)
}

func TestExecOnFailureDecidesTheCode(t *testing.T) {
	a := execRepo(t, &config.File{Scripts: map[string]config.Script{"build": {"boom"}}})
	f := &fakeRunner{outcomes: map[string]error{"boom": exitErr(t, 7), "notify": exitErr(t, 3)}}
	code, err := a.Exec(context.Background(), ExecOptions{Script: "build", OnFailure: "notify", Runner: f})
	require.NoError(t, err)
	assert.Equal(t, 3, code)
	assert.Equal(t, []string{"boom", "notify"}, f.ran)
}

// TestExecRunsEveryCommandOfTheScript: a script bound to several commands runs
// all of them, in order, as separate invocations — each reaching the shell as
// it was written, rather than joined into one string dispat composed.
func TestExecRunsEveryCommandOfTheScript(t *testing.T) {
	a := execRepo(t, &config.File{
		Scripts: map[string]config.Script{"build": {"npm ci", "npm run build"}},
	})
	f, code, err := runExec(t, a, ExecOptions{Script: "build"})
	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, []string{"npm ci", "npm run build"}, f.ran)
}

// TestExecStopsAtTheFirstFailingCommand: a sequence run here gates its own
// remainder, so the command after a failure never runs and the failure's code
// is the script's.
func TestExecStopsAtTheFirstFailingCommand(t *testing.T) {
	a := execRepo(t, &config.File{
		Scripts: map[string]config.Script{"build": {"npm ci", "boom", "never"}},
	})
	f := &fakeRunner{outcomes: map[string]error{"boom": exitErr(t, 7)}}
	code, err := a.Exec(context.Background(), ExecOptions{Script: "build", Runner: f})
	require.NoError(t, err)
	assert.Equal(t, 7, code)
	assert.Equal(t, []string{"npm ci", "boom"}, f.ran)
}

// TestExecFailureScriptStillRunsAfterASequence: --on-failure reacts to the
// script, whichever of its commands was the one that failed.
func TestExecFailureScriptStillRunsAfterASequence(t *testing.T) {
	a := execRepo(t, &config.File{
		Scripts: map[string]config.Script{"build": {"npm ci", "boom"}},
	})
	f := &fakeRunner{outcomes: map[string]error{"boom": exitErr(t, 7), "notify": exitErr(t, 3)}}
	code, err := a.Exec(context.Background(), ExecOptions{Script: "build", OnFailure: "notify", Runner: f})
	require.NoError(t, err)
	assert.Equal(t, 3, code)
	assert.Equal(t, []string{"npm ci", "boom", "notify"}, f.ran)
}

// TestExecArgsLandOnTheLastCommand: the last command is the script's work, so
// `-- --watch` watches the tests rather than installing in watch mode.
func TestExecArgsLandOnTheLastCommand(t *testing.T) {
	a := execRepo(t, &config.File{
		Scripts: map[string]config.Script{"test": {"npm ci", "npm run test"}},
	})
	f, _, err := runExec(t, a, ExecOptions{Script: "test", Args: []string{"--watch"}})
	require.NoError(t, err)
	assert.Equal(t, []string{"npm ci", "npm run test --watch"}, f.ran)
}

func TestExecRunsInTheGivenDirectory(t *testing.T) {
	a := execRepo(t, &config.File{Scripts: map[string]config.Script{"build": {"pwd"}}})
	f, _, err := runExec(t, a, ExecOptions{Script: "build", Dir: "/tmp/elsewhere"})
	require.NoError(t, err)
	assert.Equal(t, []string{"/tmp/elsewhere"}, f.dirs,
		"exec runs where it was invoked, whatever subject it was given")
}

func TestExecRefusesTheReleaseVariablesWithoutAPackage(t *testing.T) {
	// The DISPAT_* variables describe one package's release, so a space or the
	// top level cannot supply them. Refusing beats a quietly smaller
	// environment discovered inside the script.
	a := execRepo(t, layeredConfig())
	for name, subj := range map[string]ExecSubject{
		"top level": ExecSubjectRoot(),
		"space":     ExecSubjectSpace("libs"),
	} {
		t.Run(name, func(t *testing.T) {
			_, code, err := runExec(t, a, ExecOptions{
				Script: "build", Subject: subj, Fallback: true, Env: EnvScopeDispat,
			})
			require.Error(t, err)
			assert.Equal(t, 1, code)
			assert.Contains(t, err.Error(), "needs a package")
		})
	}
}

func TestParseScriptFrom(t *testing.T) {
	for spec, want := range map[string]ExecSubject{
		"root":       ExecSubjectRoot(),
		"pkg:core":   ExecSubjectPackage("core"),
		"space:libs": ExecSubjectSpace("libs"),
		// The name is everything after the first colon, so a name may contain
		// one: "@acme/ui" has none, but a scoped path form might.
		"pkg:@acme/ui": ExecSubjectPackage("@acme/ui"),
	} {
		t.Run(spec, func(t *testing.T) {
			got, err := ParseScriptFrom(spec)
			require.NoError(t, err)
			assert.Equal(t, want, got)
		})
	}
	for name, spec := range map[string]string{
		"no colon":     "core",
		"empty name":   "pkg:",
		"unknown kind": "package:core",
		"empty":        "",
		"colon only":   ":",
	} {
		t.Run("invalid/"+name, func(t *testing.T) {
			_, err := ParseScriptFrom(spec)
			require.Error(t, err, "an unusable --script-from must not parse")
		})
	}
}

func TestValidEnvScope(t *testing.T) {
	for _, ok := range []string{EnvScopeStatic, EnvScopeDispat, EnvScopeBoth} {
		assert.True(t, ValidEnvScope(ok), ok)
	}
	for _, bad := range []string{"", "STATIC", "all", "none"} {
		assert.False(t, ValidEnvScope(bad), bad)
	}
	// Only the two scopes that name release variables pay for a plan, which is
	// the command's whole performance claim.
	assert.False(t, NeedsPlan(EnvScopeStatic))
	assert.True(t, NeedsPlan(EnvScopeDispat))
	assert.True(t, NeedsPlan(EnvScopeBoth))
}

func TestWithoutStaticDropsTheDeclaredPairs(t *testing.T) {
	// --env dispat is "the release variables, without the configuration's
	// own". A declared name can never be a DISPAT_ one, so dropping by name is
	// exact rather than a guess.
	got := withoutStatic(
		[]string{"MSG=from-core", "DISPAT_VERSION=1.0.0", "OTHER=x"},
		[]string{"MSG=from-core"},
	)
	assert.Equal(t, []string{"DISPAT_VERSION=1.0.0", "OTHER=x"}, got)
	// Nothing declared means nothing to drop, and the input is returned as it
	// stands rather than copied.
	env := []string{"DISPAT_VERSION=1.0.0"}
	assert.Equal(t, env, withoutStatic(env, nil))
}
