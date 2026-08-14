package integration_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// assertSameFolder compares a folder a test knows against one a script printed
// with pwd. Both sides go through EvalSymlinks first: a shell reports the
// physical path, and on macOS a temp directory is reached through a symlink
// (/tmp -> /private/tmp), so comparing the strings as they come would fail on
// two names for one folder.
func assertSameFolder(t *testing.T, want, stdout string) {
	t.Helper()
	real, err := filepath.EvalSymlinks(want)
	require.NoError(t, err)
	var found bool
	for _, line := range strings.Fields(stdout) {
		if got, err := filepath.EvalSymlinks(line); err == nil && got == real {
			found = true
			break
		}
	}
	assert.True(t, found, "no line of the output is %s (after resolving symlinks):\n%s", real, stdout)
}

// `dispat exec` against the real binary. Two questions per scenario: which
// declared script the subject resolved to, and what the subject put in the
// environment. Both are witnessed by the script's own stdout, which passes
// straight through.

// execConfig is the workspace these scenarios share: one name declared at
// every level so the level that answered is visible, one name declared only at
// the top so falling back is visible, and an env value declared at every level
// so the layering is visible.
func execConfig() models.File {
	cfg := harness.BaseFile(2)
	cfg.Scripts = map[string]models.Script{
		"which":  {"echo level=root"},
		"shared": {"echo shared MSG=$MSG"},
		"vars":   {`echo "MSG=$MSG V=$DISPAT_VERSION P=$DISPAT_PACKAGE S=$DISPAT_STAGE"`},
	}
	cfg.Env = map[string]string{"MSG": "from-root", "ROOT_ONLY": "root"}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {
			Path:    "packages",
			Scripts: map[string]models.Script{"which": {"echo level=space"}},
			Env:     map[string]string{"MSG": "from-space"},
			Packages: map[string]models.PackageConfig{
				"core": {
					Scripts: map[string]models.Script{"which": {"echo level=core"}},
					Env:     map[string]string{"MSG": "from-core"},
				},
			},
		},
	}
	return cfg
}

// execRepo seeds the shared workspace and commits it, so a plan exists for the
// scenarios that ask for one.
func execRepo(t *testing.T) *harness.Repo {
	t.Helper()
	r := harness.New(t)
	r.WriteConfigModel(execConfig())
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "api")
	r.Commit("feat(core): first release")
	return r
}

func TestExecResolvesTheSubjectsScript(t *testing.T) {
	// One subject, one answer, and the folder the command runs in never enters
	// into it. Without --fallback only the named level is read, so a name
	// declared a level away is a reported mistake rather than text run quietly.
	r := execRepo(t)

	res := r.Command("exec", "which")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "level=root")

	res = r.Command("exec", "which", "--for", "space:libs")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "level=space")

	res = r.Command("exec", "which", "--for", "pkg:core")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "level=core")

	// api declares nothing of its own, so the exact mode has nothing to run
	// and says which level it read.
	res = r.Command("exec", "which", "--for", "pkg:api")
	assert.Equal(t, 1, res.Code)
	// The message is a JSON log field, so the quotes around the names arrive
	// escaped; the two halves are what matter, not the punctuation.
	assert.Contains(t, res.Stdout, "no script")
	assert.Contains(t, res.Stdout, "api", "the message names the level that was read")

	// The same invocation from inside a package folder resolves identically:
	// the flags decide, never the working directory.
	res = r.CommandAt("packages/core", "exec", "which")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "level=root",
		"standing in a package must not change which script is found")
}

// TestExecForCwdReadsTheInvocationFolder: --for cwd is the one way the folder
// enters into it, and it reads that folder exactly as `dispat run` does. The
// deepest match wins, a package beats the space holding it, and a folder inside
// neither is the top level rather than a refusal.
func TestExecForCwdReadsTheInvocationFolder(t *testing.T) {
	r := execRepo(t)
	require.NoError(t, os.MkdirAll(r.Path("packages", "core", "src"), 0o755))
	require.NoError(t, os.MkdirAll(r.Path("docs"), 0o755))

	for name, tc := range map[string]struct {
		from string
		want string
	}{
		"a package folder":            {"packages/core", "level=core"},
		"below a package":             {"packages/core/src", "level=core"},
		"a space folder":              {"packages", "level=space"},
		"the monorepo root":           {".", "level=root"},
		"a folder inside neither":     {"docs", "level=root"},
		"a package declaring nothing": {"packages/api", "no script"},
	} {
		t.Run(name, func(t *testing.T) {
			res := r.CommandAt(tc.from, "exec", "which", "--for", "cwd")
			assert.Contains(t, res.Stdout, tc.want, "stderr:\n%s", res.Stderr)
		})
	}

	// Standing nowhere in particular widens to the top level rather than
	// failing, and says so, because a narrower answer is what was asked for.
	res := r.CommandAt("docs", "exec", "which", "--for", "cwd")
	require.Equal(t, 0, res.Code)
	assert.Contains(t, res.Stdout, "no package and no space",
		"the widening is reported, since the invocation asked for the folder")
}

func TestExecForCwdCarriesTheFoldersEnvironment(t *testing.T) {
	// The subject decides the environment as well as the text, so inferring it
	// from the folder has to move both. Anything less would make --for cwd a
	// different kind of subject from the named ones.
	r := execRepo(t)

	res := r.CommandAt("packages/core", "exec", "shared", "--for", "cwd", "--fallback")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "MSG=from-core")

	res = r.CommandAt("packages", "exec", "shared", "--for", "cwd", "--fallback")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "MSG=from-space")

	// And it reaches the release variables, which is the pairing that makes
	// `cd packages/core && dispat exec vars --for cwd --env both` work.
	res = r.CommandAt("packages/core", "exec", "vars", "--for", "cwd", "--fallback", "--env", "both")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "V=0.1.0")
	assert.Contains(t, res.Stdout, "P=core")

	// From a folder that is no package there are no release variables to give,
	// and that is settled after the folder is read rather than before.
	res = r.CommandAt("packages", "exec", "vars", "--for", "cwd", "--fallback", "--env", "both")
	assert.Equal(t, 1, res.Code, "a space has no version of its own to report")
	assert.Contains(t, res.Stdout, "needs a package")
}

func TestExecScriptFromCwd(t *testing.T) {
	// cwd works on --script-from too, and there it moves the lookup alone.
	r := execRepo(t)
	res := r.CommandAt("packages/core", "exec", "shared", "--for", "root", "--script-from", "cwd", "--fallback")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "MSG=from-root", "the environment stayed at the top level")
}

// TestExecRunsWhereItIsTold: --in decides the working directory, in the same
// vocabulary the subject is written in. Without it the script runs where the
// invocation stands, which is what every invocation did before the flag
// existed.
func TestExecRunsWhereItIsTold(t *testing.T) {
	r := harness.New(t)
	cfg := harness.BaseFile(2)
	cfg.Scripts = map[string]models.Script{"where": {"pwd"}}
	cfg.Spaces = map[string]models.SpaceConfig{"libs": {Path: "packages"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "api")
	require.NoError(t, os.MkdirAll(r.Path("build"), 0o755))
	// A folder actually called "root", to prove the reserved word can be
	// escaped the way a shell escapes it.
	require.NoError(t, os.MkdirAll(r.Path("root"), 0o755))

	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"a package":       {[]string{"--in", "pkg:core"}, r.Path("packages", "core")},
		"a space":         {[]string{"--in", "space:libs"}, r.Path("packages")},
		"the root":        {[]string{"--in", "root"}, r.Root},
		"a relative path": {[]string{"--in", "build"}, r.Path("build")},
		"an absolute path": {[]string{"--in", r.Path("packages", "api")},
			r.Path("packages", "api")},
		"a folder called root": {[]string{"--in", "./root"}, r.Path("root")},
		"cwd":                  {[]string{"--in", "cwd"}, r.Root},
		"nothing at all":       {nil, r.Root},
	} {
		t.Run(name, func(t *testing.T) {
			res := r.Command(append([]string{"exec", "where"}, tc.args...)...)
			require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
			assertSameFolder(t, tc.want, res.Stdout)
		})
	}
}

func TestExecInIsIndependentOfTheSubject(t *testing.T) {
	// The two flags answer different questions, so either can be given without
	// the other and both can disagree: core's environment, run at the root.
	r := execRepo(t)

	res := r.Command("exec", "vars", "--for", "pkg:core", "--fallback", "--in", "root")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "MSG=from-core", "the subject still decides the environment")

	// Standing in a package, --in root puts the script back at the top without
	// touching which config was found or which script was resolved.
	res = r.CommandAt("packages/core", "exec", "which", "--in", "root")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "level=root")
}

func TestExecOnFailureRunsInTheSameFolder(t *testing.T) {
	// The cleanup is part of the same invocation, so it belongs in the same
	// place: a handler that tidied up in a different folder from the script it
	// follows would be a trap.
	r := harness.New(t)
	cfg := harness.BaseFile(2)
	cfg.Scripts = map[string]models.Script{"boom": {"pwd; exit 7"}}
	cfg.Spaces = map[string]models.SpaceConfig{"libs": {Path: "packages"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")

	res := r.Command("exec", "boom", "--in", "pkg:core", "--on-failure", "pwd; exit 3")
	assert.Equal(t, 3, res.Code)
	assert.Equal(t, 2, strings.Count(res.Stdout, "packages/core"),
		"the script and its failure handler both ran in the named folder")
}

func TestExecFallbackWalksTheLayers(t *testing.T) {
	// --fallback is the resolution dispat run uses, so a name declared once at
	// the top level is reachable from anywhere, and a nearer level still wins.
	r := execRepo(t)

	res := r.Command("exec", "shared", "--for", "pkg:core")
	assert.Equal(t, 1, res.Code, "the exact mode does not reach the top level")

	res = r.Command("exec", "shared", "--for", "pkg:core", "--fallback")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "shared")

	res = r.Command("exec", "which", "--for", "pkg:core", "--fallback")
	require.Equal(t, 0, res.Code)
	assert.Contains(t, res.Stdout, "level=core", "the nearest level that has the name answers")

	res = r.Command("exec", "which", "--for", "pkg:api", "--fallback")
	require.Equal(t, 0, res.Code)
	assert.Contains(t, res.Stdout, "level=space", "api has none of its own, so its space answers")

	// A name nowhere in the chain reports the whole chain, which is what tells
	// a missing script from a misplaced one.
	res = r.Command("exec", "ghost", "--for", "pkg:core", "--fallback")
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stderr+res.Stdout, "nor in")
}

func TestExecEnvironmentFollowsTheSubject(t *testing.T) {
	// The declared env is always layered, file under space under package, and
	// it belongs to the subject rather than to the folder the command ran in.
	r := execRepo(t)

	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		// "shared" is declared at the top level alone, so every subject below
		// it needs --fallback to reach the text. What differs is the
		// environment, which is the point of the scenario.
		"top level": {[]string{"exec", "shared"}, "MSG=from-root"},
		"space":     {[]string{"exec", "shared", "--for", "space:libs", "--fallback"}, "MSG=from-space"},
		"package":   {[]string{"exec", "shared", "--for", "pkg:core", "--fallback"}, "MSG=from-core"},
	} {
		t.Run(name, func(t *testing.T) {
			res := r.Command(tc.args...)
			require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
			assert.Contains(t, res.Stdout, tc.want)
		})
	}
}

func TestExecScriptFromCrossesTextAndContext(t *testing.T) {
	// The one escape hatch: core's text against the space's context. If the
	// flag ever moved the environment too, the two would collapse into one
	// choice and the crossed form would be unsayable.
	r := execRepo(t)

	res := r.Command("exec", "which", "--for", "space:libs", "--script-from", "pkg:core")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "level=core", "the text comes from --script-from")

	res = r.Command("exec", "shared", "--for", "pkg:core", "--script-from", "root")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "MSG=from-core", "the environment stays with the subject")
}

func TestExecReachesTheReleaseVariablesOutsideARelease(t *testing.T) {
	// The reuse claim, and the reason the command exists: a declared script
	// that reads DISPAT_* can be run on its own, without running a release.
	r := execRepo(t)

	// The default scope computes no plan, so the release variables are absent
	// and only the declared env is there.
	res := r.Command("exec", "vars", "--for", "pkg:core", "--fallback")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "MSG=from-core")
	assert.Contains(t, res.Stdout, "V= ", "no plan was computed, so there is no version")

	// --env both is what a stage script sees, computed on demand.
	res = r.Command("exec", "vars", "--for", "pkg:core", "--fallback", "--env", "both")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "MSG=from-core", "both keeps the declared env")
	assert.Contains(t, res.Stdout, "V=0.1.0", "and adds the planned version")
	assert.Contains(t, res.Stdout, "P=core")
	assert.Contains(t, res.Stdout, "S=exec", "the stage a script sees names the command")

	// --env dispat is the release variables without the declared env.
	res = r.Command("exec", "vars", "--for", "pkg:core", "--fallback", "--env", "dispat")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "V=0.1.0")
	assert.Contains(t, res.Stdout, "MSG= ", "dispat alone drops the declared env")
}

func TestExecComputesNoPlanUnlessAsked(t *testing.T) {
	// The performance claim, and the reason --env has scopes at all: only
	// dispat and both read git. Taking the repository away is the sharpest
	// witness available, because a plan is then impossible: whatever still
	// works provably computed none, and whatever needs one now fails.
	r := execRepo(t)
	require.NoError(t, os.RemoveAll(r.Path(".git")))

	res := r.Command("exec", "which")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "level=root",
		"the default scope reads no history, so it works without a repository")

	// Reading the folder is discovery rather than history, so --for cwd stays
	// on the cheap side of that line: it walks the workspace, never git.
	res = r.CommandAt("packages/core", "exec", "which", "--for", "cwd")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "level=core",
		"inferring the subject from the folder computes no plan either")

	res = r.Command("exec", "which", "--for", "pkg:core", "--env", "both")
	assert.Equal(t, 1, res.Code, "asking for the release variables is what pays for the plan")
	assert.Contains(t, res.Stdout+res.Stderr, "git")
}

func TestExecPropagatesTheExitCode(t *testing.T) {
	r := harness.New(t)
	cfg := harness.BaseFile(2)
	cfg.Scripts = map[string]models.Script{"boom": {"exit 7"}}
	cfg.Spaces = map[string]models.SpaceConfig{"libs": {Path: "packages"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")

	res := r.Command("exec", "boom")
	assert.Equal(t, 7, res.Code, "the script's own code becomes the command's")

	res = r.Command("exec", "boom", "--on-failure", "echo cleaning up; exit 3")
	assert.Equal(t, 3, res.Code)
	assert.Contains(t, res.Stdout, "cleaning up")
}

func TestExecComposesInsideARunScript(t *testing.T) {
	// The in-flow use case: a run script calling exec to reuse another
	// declared script. The DISPAT_* variables the run already put in the
	// environment reach the inner script by inheritance, with no flag, which
	// is what makes a helper usable as a step inside a flow.
	r := harness.New(t)
	cfg := harness.BaseFile(2)
	cfg.Scripts = map[string]models.Script{
		"announce": {`echo announcing $DISPAT_PACKAGE at $DISPAT_VERSION`},
		"ci":       {r.DispatCommand("exec", "announce")},
	}
	cfg.Spaces = map[string]models.SpaceConfig{"libs": {Path: "packages"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first release")

	res := r.RunScriptOK("ci")
	assert.Contains(t, res.Stdout, "announcing core at 0.1.0",
		"the run's DISPAT_* variables reach the exec'd script through the process environment")
}

func TestExecIsReservedAndRefusesBadFlags(t *testing.T) {
	// `exec` is a command word like every other, and every way of asking it
	// wrongly is decided by the flags alone, so all of them exit 2.
	r := execRepo(t)

	for name, args := range map[string][]string{
		"no script": {"exec"},
		// A bare word is a folder, and --for names a level: the two flags that
		// take a subject refuse what only --in can use.
		"a folder as a subject":    {"exec", "which", "--for", "core"},
		"a path as a subject":      {"exec", "which", "--for", "./packages/core"},
		"unknown kind":             {"exec", "which", "--for", "group:libs"},
		"a kind naming nothing":    {"exec", "which", "--for", "pkg:"},
		"malformed script-from":    {"exec", "which", "--script-from", "core"},
		"malformed in":             {"exec", "which", "--in", "pkg:"},
		"unknown env scope":        {"exec", "which", "--env", "sideways"},
		"release vars, no package": {"exec", "which", "--env", "both"},
		"release vars for a space": {"exec", "which", "--for", "space:libs", "--env", "dispat"},
		// The pair --for replaced is gone rather than deprecated, so the old
		// spelling is an unknown flag.
		"the old --for-package": {"exec", "which", "--for-package", "core"},
		"the old --for-space":   {"exec", "which", "--for-space", "libs"},
	} {
		t.Run(name, func(t *testing.T) {
			res := r.Command(args...)
			assert.Equal(t, 2, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		})
	}

	// These were well formed and simply name something that is not there, which
	// is a runtime failure rather than a usage one.
	for name, args := range map[string][]string{
		"an unknown package":                    {"exec", "which", "--for", "pkg:ghost"},
		"an unknown space":                      {"exec", "which", "--for", "space:ghost"},
		"a missing folder":                      {"exec", "which", "--in", "nowhere"},
		"a package to run in that is not there": {"exec", "which", "--in", "pkg:ghost"},
	} {
		t.Run(name, func(t *testing.T) {
			res := r.Command(args...)
			assert.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		})
	}
	assert.Contains(t, r.Command("exec", "which", "--for", "pkg:ghost").Stdout, "ghost",
		"the message names what could not be found")
}

// TestExecForwardsArgumentsAfterTheDash: `dispat exec` runs one declared
// script once, so forwarding is unambiguous there in a way it is not for a
// sweep. The arguments are appended to the resolved command's text, which is
// what lets a script declared in the config take a value at the terminal
// without the config being edited.
//
// The second claim is the one worth pinning: `--on-failure` does not receive
// them. That script is about the failure rather than about the work, and a
// cleanup handed the arguments of the thing that just failed would be acting
// on a request nobody made of it.
func TestExecForwardsArgumentsAfterTheDash(t *testing.T) {
	r := harness.New(t)
	cfg := harness.BaseFile(2)
	cfg.Scripts = map[string]models.Script{
		"show": {`printf 'ARGS[%s]\n'`},
		// Ends in one program, which is what the appended arguments reach.
		"boom": {`sh -c 'printf "RAN[%s]\n" "$@"; exit 7' _`},
	}
	cfg.Spaces = map[string]models.SpaceConfig{"libs": {Path: "packages"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")

	res := r.Command("exec", "show", "--", "--verbose", "--out=dist")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "ARGS[--verbose]")
	assert.Contains(t, res.Stdout, "ARGS[--out=dist]")

	// One argument, not two.
	res = r.Command("exec", "show", "--", "my value")
	require.Equal(t, 0, res.Code)
	assert.Contains(t, res.Stdout, "ARGS[my value]")

	// Without a dash nothing is appended, and a bare word is still a usage
	// error: exec takes one script name and the subject is a flag.
	res = r.Command("exec", "show")
	require.Equal(t, 0, res.Code)
	assert.Contains(t, res.Stdout, "ARGS[]", "the command ran with nothing appended")
	assert.Equal(t, 2, r.Command("exec", "show", "extra").Code)

	// The failure handler runs, and runs without them.
	res = r.Command("exec", "boom", "--on-failure", `printf 'CLEAN[%s]\n'; exit 3`, "--", "--verbose")
	assert.Equal(t, 3, res.Code, "--on-failure still decides the exit code")
	assert.Contains(t, res.Stdout, "RAN[--verbose]", "the work got the arguments")
	assert.Contains(t, res.Stdout, "CLEAN[]", "the cleanup ran without them")
}
