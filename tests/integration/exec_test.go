package integration_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

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
	cfg.Scripts = map[string]string{
		"which":  "echo level=root",
		"shared": "echo shared MSG=$MSG",
		"vars":   `echo "MSG=$MSG V=$DISPAT_VERSION P=$DISPAT_PACKAGE S=$DISPAT_STAGE"`,
	}
	cfg.Env = map[string]string{"MSG": "from-root", "ROOT_ONLY": "root"}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {
			Path:    "packages",
			Scripts: map[string]string{"which": "echo level=space"},
			Env:     map[string]string{"MSG": "from-space"},
			Packages: map[string]models.PackageConfig{
				"core": {
					Scripts: map[string]string{"which": "echo level=core"},
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

	res = r.Command("exec", "which", "--for-space", "libs")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "level=space")

	res = r.Command("exec", "which", "--for-package", "core")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "level=core")

	// api declares nothing of its own, so the exact mode has nothing to run
	// and says which level it read.
	res = r.Command("exec", "which", "--for-package", "api")
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

func TestExecFallbackWalksTheLayers(t *testing.T) {
	// --fallback is the resolution dispat run uses, so a name declared once at
	// the top level is reachable from anywhere, and a nearer level still wins.
	r := execRepo(t)

	res := r.Command("exec", "shared", "--for-package", "core")
	assert.Equal(t, 1, res.Code, "the exact mode does not reach the top level")

	res = r.Command("exec", "shared", "--for-package", "core", "--fallback")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "shared")

	res = r.Command("exec", "which", "--for-package", "core", "--fallback")
	require.Equal(t, 0, res.Code)
	assert.Contains(t, res.Stdout, "level=core", "the nearest level that has the name answers")

	res = r.Command("exec", "which", "--for-package", "api", "--fallback")
	require.Equal(t, 0, res.Code)
	assert.Contains(t, res.Stdout, "level=space", "api has none of its own, so its space answers")

	// A name nowhere in the chain reports the whole chain, which is what tells
	// a missing script from a misplaced one.
	res = r.Command("exec", "ghost", "--for-package", "core", "--fallback")
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
		"space":     {[]string{"exec", "shared", "--for-space", "libs", "--fallback"}, "MSG=from-space"},
		"package":   {[]string{"exec", "shared", "--for-package", "core", "--fallback"}, "MSG=from-core"},
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

	res := r.Command("exec", "which", "--for-space", "libs", "--script-from", "pkg:core")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "level=core", "the text comes from --script-from")

	res = r.Command("exec", "shared", "--for-package", "core", "--script-from", "root")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "MSG=from-core", "the environment stays with the subject")
}

func TestExecReachesTheReleaseVariablesOutsideARelease(t *testing.T) {
	// The reuse claim, and the reason the command exists: a declared script
	// that reads DISPAT_* can be run on its own, without running a release.
	r := execRepo(t)

	// The default scope computes no plan, so the release variables are absent
	// and only the declared env is there.
	res := r.Command("exec", "vars", "--for-package", "core", "--fallback")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "MSG=from-core")
	assert.Contains(t, res.Stdout, "V= ", "no plan was computed, so there is no version")

	// --env both is what a stage script sees, computed on demand.
	res = r.Command("exec", "vars", "--for-package", "core", "--fallback", "--env", "both")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "MSG=from-core", "both keeps the declared env")
	assert.Contains(t, res.Stdout, "V=0.1.0", "and adds the planned version")
	assert.Contains(t, res.Stdout, "P=core")
	assert.Contains(t, res.Stdout, "S=exec", "the stage a script sees names the command")

	// --env dispat is the release variables without the declared env.
	res = r.Command("exec", "vars", "--for-package", "core", "--fallback", "--env", "dispat")
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

	res = r.Command("exec", "which", "--for-package", "core", "--env", "both")
	assert.Equal(t, 1, res.Code, "asking for the release variables is what pays for the plan")
	assert.Contains(t, res.Stdout+res.Stderr, "git")
}

func TestExecPropagatesTheExitCode(t *testing.T) {
	r := harness.New(t)
	cfg := harness.BaseFile(2)
	cfg.Scripts = map[string]string{"boom": "exit 7"}
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
	cfg.Scripts = map[string]string{
		"announce": `echo announcing $DISPAT_PACKAGE at $DISPAT_VERSION`,
		"ci":       r.DispatCommand("exec", "announce"),
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
		"no script":                {"exec"},
		"two subjects":             {"exec", "which", "--for-package", "core", "--for-space", "libs"},
		"malformed script-from":    {"exec", "which", "--script-from", "core"},
		"unknown env scope":        {"exec", "which", "--env", "sideways"},
		"release vars, no package": {"exec", "which", "--env", "both"},
	} {
		t.Run(name, func(t *testing.T) {
			res := r.Command(args...)
			assert.Equal(t, 2, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		})
	}

	// An unknown subject is a runtime failure rather than a usage one: the
	// flags were well formed, the workspace simply has no such package.
	res := r.Command("exec", "which", "--for-package", "ghost")
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stderr+res.Stdout, "ghost")
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
	cfg.Scripts = map[string]string{
		"show": `printf 'ARGS[%s]\n'`,
		// Ends in one program, which is what the appended arguments reach.
		"boom": `sh -c 'printf "RAN[%s]\n" "$@"; exit 7' _`,
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
