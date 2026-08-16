package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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
				Path: config.PathList{"packages"},
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
		subject Location
		want    string
	}{
		"top level":  {LocationRoot(), "root-build"},
		"space":      {LocationSpace("libs"), "space-build"},
		"package":    {LocationPackage("core"), "core-build"},
		"standalone": {LocationPackage("standalone"), "alone-build"},
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
	_, code, err := runExec(t, a, ExecOptions{Script: "only-root", Subject: LocationPackage("core")})
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
	for name, subj := range map[string]Location{
		"from a package": LocationPackage("core"),
		"from a space":   LocationSpace("libs"),
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
	f, _, err := runExec(t, a, ExecOptions{Script: "build", Subject: LocationPackage("core"), Fallback: true})
	require.NoError(t, err)
	assert.Equal(t, []string{"core-build"}, f.ran, "the nearest level that has the name answers")
}

func TestExecFallbackReportsEveryLevelItTried(t *testing.T) {
	// A missing script and a misplaced one read differently, which is the
	// reason the two modes have separate messages.
	a := execRepo(t, layeredConfig())
	_, _, err := runExec(t, a, ExecOptions{Script: "ghost", Subject: LocationPackage("core"), Fallback: true})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nor in", "the layered mode names the whole chain")
}

func TestExecRefusesAnUnknownSubject(t *testing.T) {
	a := execRepo(t, layeredConfig())
	for name, subj := range map[string]Location{
		"package": LocationPackage("ghost"),
		"space":   LocationSpace("ghost"),
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
	from := LocationRoot()
	for name, subj := range map[string]Location{
		"package": LocationPackage("ghost"),
		"space":   LocationSpace("ghost"),
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
	from := LocationPackage("core")
	f, _, err := runExec(t, a, ExecOptions{
		Script: "build", Subject: LocationSpace("libs"), ScriptFrom: &from,
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"core-build"}, f.ran, "the text comes from --script-from")
	assert.Contains(t, f.envs[0], "MSG=from-space", "the environment stays with the subject")
}

func TestExecFallbackLayersFromTheOverriddenLocation(t *testing.T) {
	// With --script-from the chain starts where the text was redirected to,
	// not at the subject: the flag moved the lookup, so it moves all of it.
	a := execRepo(t, layeredConfig())
	from := LocationSpace("libs")
	f, _, err := runExec(t, a, ExecOptions{
		Script: "only-space", Subject: LocationRoot(), ScriptFrom: &from, Fallback: true,
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"space-only"}, f.ran)
}

func TestExecStaticEnvLayersOverTheSubject(t *testing.T) {
	// The declared env is always layered, file under space under package,
	// because that is what a release hands a script.
	a := execRepo(t, layeredConfig())
	for name, tc := range map[string]struct {
		subject Location
		want    string
	}{
		"top level": {LocationRoot(), "MSG=from-root"},
		"space":     {LocationSpace("libs"), "MSG=from-space"},
		"package":   {LocationPackage("core"), "MSG=from-core"},
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
	for name, subj := range map[string]Location{
		"top level": LocationRoot(),
		"space":     LocationSpace("libs"),
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

func TestParseSubject(t *testing.T) {
	// The levels a --for or a --script-from may name.
	for spec, want := range map[string]Location{
		"root":       LocationRoot(),
		"cwd":        LocationCwd(),
		"pkg:core":   LocationPackage("core"),
		"space:libs": LocationSpace("libs"),
		// The name is everything after the first colon, so a name may contain
		// one: "@acme/ui" has none, but a scoped path form might.
		"pkg:@acme/ui": LocationPackage("@acme/ui"),
	} {
		t.Run(spec, func(t *testing.T) {
			got, err := ParseSubject(spec)
			require.NoError(t, err)
			assert.Equal(t, want, got)
		})
	}
	for name, spec := range map[string]string{
		// A bare word is a folder, and a folder is not a level: --for wants to
		// know which scripts map to read, and no folder answers that.
		"a folder":     "core",
		"a path":       "./packages/core",
		"empty name":   "pkg:",
		"unknown kind": "package:core",
		"empty":        "",
		"colon only":   ":",
	} {
		t.Run("invalid/"+name, func(t *testing.T) {
			_, err := ParseSubject(spec)
			require.Error(t, err, "an unusable subject must not parse")
		})
	}
}

func TestParseLocation(t *testing.T) {
	// --in takes everything a subject does, and a folder besides.
	for spec, want := range map[string]Location{
		"root":       LocationRoot(),
		"cwd":        LocationCwd(),
		"pkg:core":   LocationPackage("core"),
		"space:libs": LocationSpace("libs"),
		"build":      LocationPath("build"),
		"./build":    LocationPath("./build"),
		"../sibling": LocationPath("../sibling"),
		"/abs/path":  LocationPath("/abs/path"),
		// The reserved words win, so a folder actually called one of them is
		// spelled the way a shell would disambiguate it too.
		"./root": LocationPath("./root"),
		"./cwd":  LocationPath("./cwd"),
	} {
		t.Run(spec, func(t *testing.T) {
			got, err := ParseLocation(spec)
			require.NoError(t, err)
			assert.Equal(t, want, got)
		})
	}
	for name, spec := range map[string]string{
		"empty name":   "pkg:",
		"unknown kind": "package:core",
		"colon only":   ":",
	} {
		t.Run("invalid/"+name, func(t *testing.T) {
			_, err := ParseLocation(spec)
			require.Error(t, err, "a malformed location must not parse as a folder")
		})
	}
}

func TestLocationDeferredMarksWhatNeedsAConfiguration(t *testing.T) {
	// What `dispat if` asks before deciding whether to load anything.
	for name, loc := range map[string]Location{
		"root":    LocationRoot(),
		"package": LocationPackage("core"),
		"space":   LocationSpace("libs"),
	} {
		assert.True(t, loc.Deferred(), name+" can only be placed by a configuration")
	}
	for name, loc := range map[string]Location{
		"cwd":  LocationCwd(),
		"path": LocationPath("./build"),
	} {
		assert.False(t, loc.Deferred(), name+" is answered by the command line alone")
	}
}

func TestResolveSubjectReadsTheFolder(t *testing.T) {
	// --for cwd is the filter's own reading of a folder, so the level it finds
	// is the package or space `dispat run` would have narrowed to.
	a := execRepo(t, layeredConfig())
	for name, tc := range map[string]struct {
		dir  string
		want Location
	}{
		"inside a package":    {filepath.Join(a.root, "packages", "core"), LocationPackage("core")},
		"below a package":     {filepath.Join(a.root, "packages", "core", "src"), LocationPackage("core")},
		"inside a space":      {filepath.Join(a.root, "packages"), LocationSpace("libs")},
		"a standalone":        {filepath.Join(a.root, "standalone"), LocationPackage("standalone")},
		"the monorepo root":   {a.root, LocationRoot()},
		"in nothing at all":   {filepath.Join(a.root, "docs"), LocationRoot()},
		"a package not there": {filepath.Join(a.root, "packages", "gone"), LocationSpace("libs")},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := a.ResolveSubject(LocationCwd(), tc.dir)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestResolveSubjectLeavesANamedLevelAlone(t *testing.T) {
	// Only cwd is resolved. A named level is already the answer, and reading
	// the folder for it would let where you stand change what you asked for.
	a := execRepo(t, layeredConfig())
	for _, loc := range []Location{LocationRoot(), LocationPackage("core"), LocationSpace("libs")} {
		got, err := a.ResolveSubject(loc, filepath.Join(a.root, "standalone"))
		require.NoError(t, err)
		assert.Equal(t, loc, got)
	}
}

func TestExecFromTheCurrentFolder(t *testing.T) {
	// The whole feature, end to end inside the app: standing in core's folder
	// runs core's build with core's environment, without either being named.
	a := execRepo(t, layeredConfig())
	core := filepath.Join(a.root, "packages", "core")
	f, code, err := runExec(t, a, ExecOptions{Script: "build", Subject: LocationCwd(), Dir: core})
	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, []string{"core-build"}, f.ran)
	assert.Contains(t, f.envs[0], "MSG=from-core", "the environment follows the folder too")
}

func TestExecScriptFromTheCurrentFolder(t *testing.T) {
	// cwd on --script-from moves the lookup and nothing else, exactly as a
	// named level does there.
	a := execRepo(t, layeredConfig())
	core := filepath.Join(a.root, "packages", "core")
	from := LocationCwd()
	f, _, err := runExec(t, a, ExecOptions{
		Script: "build", Subject: LocationSpace("libs"), ScriptFrom: &from, Dir: core,
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"core-build"}, f.ran, "the text came from the folder")
	assert.Contains(t, f.envs[0], "MSG=from-space", "the environment stayed with the subject")
}

func TestExecRefusesTheReleaseVariablesFromAFolderThatIsNoPackage(t *testing.T) {
	// The check the controller cannot make: --for cwd may or may not be a
	// package, and only the resolved folder says which.
	a := execRepo(t, layeredConfig())
	_, code, err := runExec(t, a, ExecOptions{
		Script: "build", Subject: LocationCwd(), Fallback: true,
		Env: EnvScopeDispat, Dir: a.root,
	})
	require.Error(t, err)
	assert.Equal(t, 1, code)
	assert.Contains(t, err.Error(), "needs a package")
}

func TestResolveDirPlacesEveryKind(t *testing.T) {
	a := execRepo(t, layeredConfig())
	stood := filepath.Join(a.root, "standalone")
	for name, tc := range map[string]struct {
		loc  Location
		want string
	}{
		"a package":   {LocationPackage("core"), filepath.Join(a.root, "packages", "core")},
		"a space":     {LocationSpace("libs"), filepath.Join(a.root, "packages")},
		"the root":    {LocationRoot(), a.root},
		"cwd":         {LocationCwd(), stood},
		"a rel path":  {LocationPath("../packages/api"), filepath.Join(a.root, "packages", "api")},
		"an abs path": {LocationPath(filepath.Join(a.root, "packages")), filepath.Join(a.root, "packages")},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := a.ResolveDir(tc.loc, stood)
			require.NoError(t, err)
			assert.Equal(t, tc.want, filepath.Clean(got))
		})
	}
}

func TestResolveDirRefusesWhatIsNotAFolder(t *testing.T) {
	// Caught here rather than by the shell, which would name neither the flag
	// nor the value that was wrong.
	a := execRepo(t, layeredConfig())
	for name, loc := range map[string]Location{
		"a missing folder":   LocationPath("nowhere"),
		"a file":             LocationPath(filepath.Join("packages", "core", "package.json")),
		"an unknown package": LocationPackage("ghost"),
		"an unknown space":   LocationSpace("ghost"),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := a.ResolveDir(loc, a.root)
			require.Error(t, err)
		})
	}
}

func TestPlainDirNeedsNoConfiguration(t *testing.T) {
	// What `dispat if --in` leans on: the two kinds a command line settles on
	// its own, resolved with no App and therefore no config file.
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "build"), 0o755))

	got, err := PlainDir(LocationCwd(), root)
	require.NoError(t, err)
	assert.Equal(t, root, got)

	got, err = PlainDir(LocationPath("build"), root)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "build"), got)

	_, err = PlainDir(LocationPath("nowhere"), root)
	require.Error(t, err, "a missing folder is refused without a configuration too")

	for _, loc := range []Location{LocationRoot(), LocationPackage("core"), LocationSpace("libs")} {
		_, err := PlainDir(loc, root)
		require.Error(t, err, "a level is not something a folder alone can place")
	}
}

func TestExecResolvesTheFolderOnceWhenBothFlagsAskForIt(t *testing.T) {
	// --script-from repeating the subject is the subject, so the folder is read
	// once and the widening is reported once. Two identical lines about one
	// folder would read as a bug in the command rather than a note about it.
	var buf strings.Builder
	a := execRepo(t, layeredConfig())
	a.log = zerolog.New(&buf)

	from := LocationCwd()
	_, code, err := runExec(t, a, ExecOptions{
		Script: "build", Subject: LocationCwd(), ScriptFrom: &from, Dir: a.root,
	})
	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, 1, strings.Count(buf.String(), "no package and no space"),
		"one folder, one answer, one line about it")
}

func TestExecDiscoversTheWorkspaceOnce(t *testing.T) {
	// Three locations in one invocation used to be three filesystem walks.
	// The cache is what keeps --for cwd as cheap as the flag it replaced.
	a := execRepo(t, layeredConfig())
	core := filepath.Join(a.root, "packages", "core")
	first, err := a.packages()
	require.NoError(t, err)

	from := LocationCwd()
	in := LocationPackage("api")
	_, _, err = runExec(t, a, ExecOptions{
		Script: "build", Subject: LocationCwd(), ScriptFrom: &from, In: &in, Dir: core,
	})
	require.NoError(t, err)

	second, err := a.packages()
	require.NoError(t, err)
	require.NotEmpty(t, second)
	// Same backing array, so no second walk happened: a repeat of discovery
	// would have built fresh *model.Package values.
	assert.Same(t, first[0], second[0], "discovery ran once for the whole invocation")
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
