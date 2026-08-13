package integration

// Area 9: the script frames. Every stage dispat runs sits inside a frame of
// hooks, and the frames nest: nine per-package hooks around the version,
// build and publish stages, the announce frame after a publish, the two
// outcome scripts a failure or a skip fires, the once-per-space login gate,
// and the run-level bracket around the whole thing.
//
// The claim these tests exist for is *authority*, not mere ordering. A hook
// before the point of no return may fail its package; one after it may only
// warn, because the release is already out and reporting it as failed would
// revert a folder, skip consumers and un-publish nothing. The same split
// decides where revertOnFail applies and where a login failure lands. None of
// it is observable without running real processes in a real order, which is
// why it lives here rather than in a unit suite.

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

// TestHooksLoginOncePerSpaceAcrossSpaces: two spaces referencing the exact
// same login script text still log in once *each* — configuration.md is
// explicit that credentials belong to the space, not the script. The cli
// package's own end-to-end test covers only the single-space case (two
// packages, one login); this is the shape that would catch a login gate
// accidentally keyed by script text instead of by space.
func TestHooksLoginOncePerSpaceAcrossSpaces(t *testing.T) {
	r := harness.New(t)
	cfg := harness.BaseFile(2)
	cfg.Scripts = map[string]models.Script{
		"build":   {"echo building"},
		"publish": {"echo publishing"},
		"login":   {r.TsmarkScript("login.log", "$DISPAT_SPACE", 0)},
	}
	withLogin := &models.SpaceFlowConfig{Build: []string{"build"}, Publish: []string{"publish"}, Login: []string{"login"}}
	cfg.Spaces = map[string]models.SpaceConfig{
		"spaceA": {Path: "packages/a", Flow: withLogin},
		"spaceB": {Path: "packages/b", Flow: withLogin},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages/a", "a1")
	r.SeedPackage("packages/a", "a2")
	r.SeedPackage("packages/b", "b1")
	r.Commit("feat(a1,a2,b1): bootstrap three packages across two spaces")

	r.ReleaseOK()

	tl := r.Timeline("login.log")
	require.Len(t, tl, 2, "one login per space — not per publish, not one for the whole run: %v", tl)
	names := map[string]bool{}
	for _, iv := range tl {
		names[iv.Label] = true
	}
	// viper lowercases map keys, so $DISPAT_SPACE reports "spacea"/"spaceb"
	// even though the config wrote "spaceA"/"spaceB".
	assert.True(t, names["spacea"] && names["spaceb"], "each space logs in under its own name: %v", tl)
}

// assertRanIn checks that a script recording its working directory with `pwd`
// ran in want. Both sides go through EvalSymlinks first: a shell's `pwd`
// reports the resolved path, and macOS puts the test's temporary directory
// behind the /var -> /private/var symlink, so the two spellings of one folder
// would otherwise differ.
func assertRanIn(t *testing.T, want, cwdFile string) {
	t.Helper()
	got, err := os.ReadFile(cwdFile)
	require.NoError(t, err, "the script left no record of its working directory")
	resolved, err := filepath.EvalSymlinks(want)
	require.NoError(t, err)
	assert.Equal(t, resolved, strings.TrimSpace(string(got)))
}

// TestHooksLoginRunsInTheSpaceFolder: the login is the one script that
// belongs to the space rather than to a package, so the folder it runs in has
// to be the space's own — not the folder of whichever member's publish
// happened to reach the gate first. A login script reading a local file (a
// netrc, a credentials JSON, a certificate) sees the same folder on every
// run only if that is true.
//
// The script writes its working directory into a file *in* that directory,
// so where the file lands is the assertion.
func TestHooksLoginRunsInTheSpaceFolder(t *testing.T) {
	r := harness.New(t)
	cfg := harness.BaseFile(2)
	cfg.Scripts = map[string]models.Script{
		"build":   {"echo building"},
		"publish": {"echo publishing"},
		"login":   {"pwd > login-cwd.txt"},
	}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: "packages/libs", Flow: &models.SpaceFlowConfig{
			Build: []string{"build"}, Publish: []string{"publish"}, Login: []string{"login"}}},
	}
	r.WriteConfigModel(cfg)
	// Two members, so the gate is genuinely raced: with the folder taken from
	// a member, either one could have supplied it.
	r.SeedPackage("packages/libs", "a")
	r.SeedPackage("packages/libs", "b")
	r.Commit("feat(a,b): two members racing one login")

	r.ReleaseOK()

	assertRanIn(t, r.Path("packages", "libs"), r.Path("packages", "libs", "login-cwd.txt"))
	// Neither member's folder may hold one: that is the failure mode where the
	// winner of the race decided the directory.
	assert.NoFileExists(t, r.Path("packages", "libs", "a", "login-cwd.txt"))
	assert.NoFileExists(t, r.Path("packages", "libs", "b", "login-cwd.txt"))
}

// TestHooksLoginOfAStandalonePackageRunsInItsOwnFolder: a standalone package
// is its own space, so the space folder the login runs in is the package's
// own folder. The parent it happens to sit in belongs to nobody — it may hold
// unrelated packages, or nothing at all — and a login running there would
// read the wrong credentials file or none.
//
// The login reaches the package through the root `flow`, which is the only
// route it has: flow.login cannot be written on a package entry.
func TestHooksLoginOfAStandalonePackageRunsInItsOwnFolder(t *testing.T) {
	r := harness.New(t)
	cfg := harness.BaseFile(1)
	cfg.Scripts = map[string]models.Script{
		"build":   {"echo building"},
		"publish": {"echo publishing"},
		"login":   {"pwd > login-cwd.txt"},
	}
	cfg.Flow = &models.SpaceFlowConfig{
		Build: []string{"build"}, Publish: []string{"publish"}, Login: []string{"login"}}
	cfg.Packages = map[string]models.PackageConfig{"cli": {Path: "tools/cli"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("tools", "cli")
	r.Commit("feat(cli): a standalone package with a login")

	r.ReleaseOK()
	require.True(t, r.HasTag("cli@0.1.0"), "tags: %v", r.TagList())

	assertRanIn(t, r.Path("tools", "cli"), r.Path("tools", "cli", "login-cwd.txt"))
	assert.NoFileExists(t, r.Path("tools", "login-cwd.txt"),
		"the package's parent is not a space folder and the login has no business in it")
}

// TestHooksLoginFailureIsolatedToItsSpace: a failing login fails every
// publish in *its* space — none of them could have succeeded without it —
// but must not touch an unrelated space's publishes.
func TestHooksLoginFailureIsolatedToItsSpace(t *testing.T) {
	r := harness.New(t)
	cfg := harness.BaseFile(2)
	cfg.Scripts = map[string]models.Script{
		"build":      {"echo building"},
		"publish":    {"echo publishing"},
		"bad-login":  {"exit 1"},
		"good-login": {"echo ok"},
	}
	cfg.Spaces = map[string]models.SpaceConfig{
		"broken": {Path: "packages/broken", Flow: &models.SpaceFlowConfig{
			Build: []string{"build"}, Publish: []string{"publish"}, Login: []string{"bad-login"}}},
		"fine": {Path: "packages/fine", Flow: &models.SpaceFlowConfig{
			Build: []string{"build"}, Publish: []string{"publish"}, Login: []string{"good-login"}}},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages/broken", "b1")
	r.SeedPackage("packages/broken", "b2")
	r.SeedPackage("packages/fine", "f1")
	r.Commit("feat(b1,b2,f1): bootstrap both spaces")

	res := r.Release()
	require.Equal(t, 1, res.Code, "the broken space's login failure must fail its packages\nstdout:\n%s", res.Stdout)
	assert.Zero(t, r.TagCount("b1@"))
	assert.Zero(t, r.TagCount("b2@"))
	assert.True(t, r.HasTag("f1@0.1.0"), "the unrelated space must still publish; tags: %v", r.TagList())
}

// TestHooksOnFailAndOnSkipOutcomeScripts covers the outcome scripts end to
// end in one failing run over three packages: provider fails its publish,
// consumer is skipped because of it, bystander publishes. onFail must run
// once for provider with the failure specifics (DISPAT_FAILED_STAGE,
// DISPAT_ERROR), onSkip once for consumer with the blame
// (DISPAT_BLOCKED_BY), and neither for the package that published. onFail
// is wired as a two-command sequence whose first command fails, proving the
// warn-only rule the docs state: the rest of the sequence still runs, and
// the failing outcome script changes nothing about the run's outcome.
func TestHooksOnFailAndOnSkipOutcomeScripts(t *testing.T) {
	r := harness.New(t)
	cfg := harness.BaseFile(1)
	cfg.Scripts = map[string]models.Script{
		"build":       {"echo building"},
		"publish":     {`[ "$DISPAT_PACKAGE" != "provider" ]`},
		"boom":        {"exit 1"},
		"record-fail": {`env | grep '^DISPAT_' > "../../onfail-$DISPAT_PACKAGE.env"`},
		"record-skip": {`env | grep '^DISPAT_' > "../../onskip-$DISPAT_PACKAGE.env"`},
	}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: "packages", Flow: &models.SpaceFlowConfig{
			Build:   []string{"build"},
			Publish: []string{"publish"},
			OnFail:  []string{"boom", "record-fail"},
			OnSkip:  []string{"record-skip"},
		}},
	}
	cfg.Dependencies = []models.DependencyConfig{{Consumer: "consumer", Provider: "provider"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "provider")
	r.SeedPackage("packages", "consumer")
	r.SeedPackage("packages", "bystander")
	r.Commit("feat(provider,bystander)^: provider will fail its publish")

	res := r.Release()
	require.Equal(t, 1, res.Code, "provider's publish failure must fail the run\nstdout:\n%s", res.Stdout)
	assert.True(t, r.HasTag("bystander@0.1.0"), "the unrelated package still publishes; tags: %v", r.TagList())
	assert.Zero(t, r.TagCount("provider@"))
	assert.Zero(t, r.TagCount("consumer@"))

	// onFail ran for the failed provider — reaching record-fail even though
	// boom, the sequence's first command, exited 1 (warn-only sequences run
	// to the end) — with the failure specifics in its environment.
	failEnv, err := os.ReadFile(r.Path("onfail-provider.env"))
	require.NoError(t, err, "onFail must run for the failed package")
	assert.Contains(t, string(failEnv), "DISPAT_STAGE=onFail")
	assert.Contains(t, string(failEnv), "DISPAT_FAILED_STAGE=publish")
	assert.Contains(t, string(failEnv), "DISPAT_ERROR=")
	assert.Contains(t, string(failEnv), "DISPAT_PACKAGE=provider")

	// onSkip ran for the blocked consumer, naming the provider to blame.
	skipEnv, err := os.ReadFile(r.Path("onskip-consumer.env"))
	require.NoError(t, err, "onSkip must run for the skipped package")
	assert.Contains(t, string(skipEnv), "DISPAT_STAGE=onSkip")
	assert.Contains(t, string(skipEnv), "DISPAT_BLOCKED_BY=provider")

	// Neither outcome script fires for any other combination: a skipped
	// package did not fail, a failed one was not skipped, and a published
	// one was neither.
	assert.NoFileExists(t, r.Path("onskip-provider.env"))
	assert.NoFileExists(t, r.Path("onfail-consumer.env"))
	assert.NoFileExists(t, r.Path("onfail-bystander.env"))
	assert.NoFileExists(t, r.Path("onskip-bystander.env"))
}

// TestHooksRevertOnFailAppliesAfterVersionStageOnSkip covers the
// documented but easy-to-miss half of revertOnFail: "the same rollback runs
// when a package is skipped after its version stage already modified
// files." The consumer's version script dirties its folder; the provider's
// *publish* then fails (its build succeeded, so with isBuildWaitingPublish
// at its default the consumer's version and build stages have already run);
// the consumer is skipped at its own publish — after real damage — and
// revertOnFail must still clean its folder up.
func TestHooksRevertOnFailAppliesAfterVersionStageOnSkip(t *testing.T) {
	r := harness.New(t)
	cfg := harness.BaseFile(1)
	cfg.Scripts = map[string]models.Script{
		"build":        {"echo building"},
		"fail-publish": {"exit 1"},
		"mutate":       {"echo dirty >> main.txt && echo extra > extra.txt"},
		"publish":      {"echo publishing"},
	}
	cfg.Spaces = map[string]models.SpaceConfig{
		"provider": {Path: "packages/provider", Flow: &models.SpaceFlowConfig{
			Build: []string{"build"}, Publish: []string{"fail-publish"}}},
		"consumer": {Path: "packages/consumer", RevertOnFail: models.Bool(true), Flow: &models.SpaceFlowConfig{
			Version: []string{"mutate"}, Build: []string{"build"}, Publish: []string{"publish"}}},
	}
	cfg.Dependencies = []models.DependencyConfig{{Consumer: "consumer", Provider: "provider"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages/provider", "provider")
	r.SeedPackage("packages/consumer", "consumer")
	r.Commit("feat(provider)^: reaches its one consumer, but fails to publish")

	res := r.Release()
	require.Equal(t, 1, res.Code, "provider's publish failure must fail the run\nstdout:\n%s", res.Stdout)
	assert.Zero(t, r.TagCount("provider@"))
	assert.Zero(t, r.TagCount("consumer@"))
	assert.True(t, harness.HasCodeForPackage(res.Events, "W194", "consumer"), "consumer must be reported blocked")

	dir := r.Path("packages", "consumer", "consumer")
	data, err := os.ReadFile(filepath.Join(dir, "main.txt"))
	require.NoError(t, err)
	assert.Equal(t, "consumer\n", string(data), "the version script's edit to the tracked file must be reverted")
	assert.NoFileExists(t, filepath.Join(dir, "extra.txt"), "the version script's untracked file must be removed")
}

// TestHooksScriptOutputsCarryAcrossStagesAndHooks pins the whole
// DISPAT_OUTPUT accumulation contract through the real binary, hooks
// included: a beforeBuild *hook* export reaches the build and publish, the
// build's export reaches the publish, and on the failing package the onFail
// outcome script receives both the hook's export and what the failing build
// exported right before dying.
func TestHooksScriptOutputsCarryAcrossStagesAndHooks(t *testing.T) {
	r := harness.New(t)
	cfg := harness.BaseFile(1)
	cfg.Scripts = map[string]models.Script{
		"hook-export": {`echo "HOOK_MARK=hook-$DISPAT_PACKAGE" >> "$DISPAT_OUTPUT"`},
		"build": {`if [ "$DISPAT_PACKAGE" = "bad" ]; then` +
			` echo "BUILD_MARK=pre-fail" >> "$DISPAT_OUTPUT"; exit 1; fi;` +
			` echo "BUILD_MARK=built" >> "$DISPAT_OUTPUT"`},
		"publish":     {`env | grep '^DISPAT_OUTPUT' | sort > "../../publish-$DISPAT_PACKAGE.env"`},
		"record-fail": {`env | grep '^DISPAT_OUTPUT' | sort > "../../onfail-$DISPAT_PACKAGE.env"`},
	}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: "packages", Flow: &models.SpaceFlowConfig{
			BeforeBuild: []string{"hook-export"},
			Build:       []string{"build"},
			Publish:     []string{"publish"},
			OnFail:      []string{"record-fail"},
		}},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "good")
	r.SeedPackage("packages", "bad")
	r.Commit("feat(good,bad): one will fail its build")

	res := r.Release()
	require.Equal(t, 1, res.Code, "bad's build failure must fail the run\nstdout:\n%s", res.Stdout)
	assert.True(t, r.HasTag("good@0.1.0"), "the independent good package still publishes")
	assert.Zero(t, r.TagCount("bad@"))

	// The hook's export and the build's export both reached good's publish.
	pub, err := os.ReadFile(r.Path("publish-good.env"))
	require.NoError(t, err)
	assert.Contains(t, string(pub), "DISPAT_OUTPUT_HOOK_MARK=hook-good\n", "a hook export carries forward")
	assert.Contains(t, string(pub), "DISPAT_OUTPUT_BUILD_MARK=built\n", "a stage export carries forward")
	assert.Contains(t, string(pub), "DISPAT_OUTPUTS=HOOK_MARK BUILD_MARK\n", "the listing names both, in export order")

	// The failed package's onFail sees the hook's export and what the build
	// exported before it died.
	fail, err := os.ReadFile(r.Path("onfail-bad.env"))
	require.NoError(t, err, "onFail must run for the failed package")
	assert.Contains(t, string(fail), "DISPAT_OUTPUT_HOOK_MARK=hook-bad\n")
	assert.Contains(t, string(fail), "DISPAT_OUTPUT_BUILD_MARK=pre-fail\n",
		"a failed script still surrenders what it exported before failing")
}

// TestHooksRunLevelHooks: the run-level hook frame end to end against a
// real remote — every hook fires in phase order in the monorepo root, postAll
// sees the run outcome and the workspace listing, and a quiet second run
// keeps the commit and push hooks off because their phases never happen.
func TestHooksRunLevelHooks(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Scripts["hook"] = models.Script{"echo $DISPAT_STAGE >> hooks.log"}
	cfg.Scripts["dump"] = models.Script{"env | grep '^DISPAT_' > postall.env"}
	cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true), Push: true}
	cfg.Run = &models.RunConfig{
		BeforeAll:    []string{"hook"},
		PostAll:      []string{"hook", "dump"},
		BeforeCommit: []string{"hook"},
		AfterCommit:  []string{"hook"},
		PostCommit:   []string{"hook"},
		BeforePush:   []string{"hook"},
		AfterPush:    []string{"hook"},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")

	r.AddBareRemote()
	r.Commit("feat(core): first release")

	r.ReleaseOK()

	data, err := os.ReadFile(r.Path("hooks.log"))
	require.NoError(t, err)
	assert.Equal(t, "beforeAll\npostAll\nbeforeCommit\nafterCommit\npostCommit\nbeforePush\nafterPush\n",
		string(data), "the hooks bracket their phases in order")

	env, err := os.ReadFile(r.Path("postall.env"))
	require.NoError(t, err)
	assert.Contains(t, string(env), "DISPAT_PUBLISHED_PACKAGES=CORE")
	assert.Contains(t, string(env), "DISPAT_RESULT_CORE_STATUS=published")
	assert.Contains(t, string(env), "DISPAT_RESULT_CORE_NEW_VERSION=0.1.0")
	assert.Contains(t, string(env), "DISPAT_WORKSPACE_CORE_VERSION=0.1.0")
	assert.Contains(t, string(env), "DISPAT_FAILED_PACKAGES=\n")
	assert.Contains(t, string(env), "DISPAT_STAGE=postAll")

	// A second run releases nothing: postAll still reports the (empty) run,
	// but the commit and push hooks are no-ops without a publish.
	r.Remove("hooks.log")
	r.ReleaseOK()
	data, err = os.ReadFile(r.Path("hooks.log"))
	require.NoError(t, err)
	assert.Equal(t, "beforeAll\npostAll\n", string(data),
		"commit and push hooks must not run when nothing published")
	env, err = os.ReadFile(r.Path("postall.env"))
	require.NoError(t, err)
	assert.Contains(t, string(env), "DISPAT_UNPLANNED_PACKAGES=CORE",
		"a package with nothing to release is reported as unplanned")
}

// TestHooksRunLevelHookFailureSemantics: one flowing scenario for the two
// failure modes of the run-level hooks. A failing warn-only hook (postAll)
// does not fail the run and the rest of its sequence still executes; the
// gating beforeAll, by contrast, aborts the run before any release work.
func TestHooksRunLevelHookFailureSemantics(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Scripts["boom"] = models.Script{"exit 1"}
	cfg.Scripts["hook"] = models.Script{"echo $DISPAT_STAGE >> hooks.log"}
	cfg.Run = &models.RunConfig{PostAll: []string{"boom", "hook"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first release")

	res := r.ReleaseOK() // a failing run hook must not fail the run
	assert.Contains(t, res.Stdout, "postAll script failed (not fatal)")
	assert.True(t, r.HasTag("core@0.1.0"), "the release went out regardless; tags: %v", r.TagList())
	data, err := os.ReadFile(r.Path("hooks.log"))
	require.NoError(t, err)
	assert.Equal(t, "postAll\n", string(data), "the sequence continued past the failure")

	// Rewire beforeAll to the failing script: the next release must abort
	// before any work, leaving the pending fix unreleased and untagged.
	cfg.Run = &models.RunConfig{BeforeAll: []string{"boom"}}
	r.WriteConfigModel(cfg)
	r.Commit("fix(core): never released")

	res = r.Release()
	require.Equal(t, 1, res.Code, "a failing beforeAll must abort the run\nstdout:\n%s", res.Stdout)
	assert.Contains(t, res.Stdout, "beforeAll hook failed")
	assert.False(t, r.HasTag("core@0.1.1"), "nothing may be tagged after the gate refused; tags: %v", r.TagList())
}

// hookLog builds the scripts map wiring every per-package stage hook (all
// nine), the three stages and announce to one appending log line each, so a
// scenario can read back exactly what fired and in which order.
func hookLog() map[string]models.Script {
	names := []string{
		"beforeAll", "beforeVersion", "postVersion", "beforeBuild", "postBuild",
		"beforePublish", "postPublish", "beforeAnnounce", "postAnnounce",
		"build", "publish", "version", "announce",
	}
	scripts := make(map[string]models.Script, len(names))
	for _, n := range names {
		scripts[n] = models.Script{"echo " + n + ":$DISPAT_PACKAGE >> ../../hooks.log"}
	}
	return scripts
}

// hookFlow references every hookLog script from its flow slot.
func hookFlow() *models.SpaceFlowConfig {
	return &models.SpaceFlowConfig{
		Build: []string{"build"}, Publish: []string{"publish"}, Version: []string{"version"},
		Announce:      []string{"announce"},
		BeforeAll:     []string{"beforeAll"},
		BeforeVersion: []string{"beforeVersion"}, PostVersion: []string{"postVersion"},
		BeforeBuild: []string{"beforeBuild"}, PostBuild: []string{"postBuild"},
		BeforePublish: []string{"beforePublish"}, PostPublish: []string{"postPublish"},
		BeforeAnnounce: []string{"beforeAnnounce"}, PostAnnounce: []string{"postAnnounce"},
	}
}

// hookSequence returns hooks.log's entries for one package, in file order.
func hookSequence(t *testing.T, r *harness.Repo, pkg string) []string {
	t.Helper()
	data, err := os.ReadFile(r.Path("hooks.log"))
	require.NoError(t, err)
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if name, p, ok := strings.Cut(line, ":"); ok && p == pkg {
			out = append(out, name)
		}
	}
	return out
}

// TestHooksAllStageHooksFireInOrder exercises the full per-package hook
// frame — every one of the nine hooks plus the announce stage — on a
// provider/consumer pair. The provider runs the plain frame; the consumer,
// bumped because of the provider, additionally runs the version stage with
// its two hooks. Order is asserted per package: the frame's shape is a
// per-package promise, interleaving across packages is the scheduler's
// business.
func TestHooksAllStageHooksFireInOrder(t *testing.T) {
	r := harness.New(t)
	cfg := harness.BaseFile(1)
	cfg.Scripts = hookLog()
	cfg.Spaces = map[string]models.SpaceConfig{"libs": {Path: "packages", Flow: hookFlow()}}
	cfg.Dependencies = []models.DependencyConfig{{Consumer: "app", Provider: "core"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "app")
	r.Commit("feat(core)^: propagate to the consumer")

	r.ReleaseOK()
	assert.Equal(t, []string{
		"beforeAll", "beforeBuild", "build", "postBuild",
		"beforePublish", "publish", "postPublish",
		"beforeAnnounce", "announce", "postAnnounce",
	}, hookSequence(t, r, "core"), "the provider's frame")
	assert.Equal(t, []string{
		"beforeAll", "beforeVersion", "version", "postVersion",
		"beforeBuild", "build", "postBuild",
		"beforePublish", "publish", "postPublish",
		"beforeAnnounce", "announce", "postAnnounce",
	}, hookSequence(t, r, "app"), "the consumer adds the version stage inside the same frame")
}

// TestHooksStageHookAuthoritySplit pins the documented split: hooks up to
// beforePublish gate the release (their failure fails the package), while
// postPublish and the whole announce frame observe a release that is already
// out and may only warn.
func TestHooksStageHookAuthoritySplit(t *testing.T) {
	t.Run("post_publish_and_announce_only_warn", func(t *testing.T) {
		r := harness.New(t)
		cfg := harness.BaseFile(1)
		cfg.Scripts = map[string]models.Script{
			"build": {echoBuild}, "publish": {"echo publishing"},
			"boom": {"exit 1"},
		}
		cfg.Spaces = map[string]models.SpaceConfig{"libs": {Path: "packages", Flow: &models.SpaceFlowConfig{
			Build: []string{"build"}, Publish: []string{"publish"},
			PostPublish: []string{"boom"}, BeforeAnnounce: []string{"boom"},
			Announce: []string{"boom"}, PostAnnounce: []string{"boom"},
		}}}
		r.WriteConfigModel(cfg)
		r.SeedPackage("packages", "core")
		r.Commit("feat(core): ship it")

		r.ReleaseOK() // exit 0 despite four failing warn-only sequences
		assert.True(t, r.HasTag("core@0.1.0"),
			"the release is out; observers failing must not unreport it: %v", r.TagList())
	})

	t.Run("gating_hooks_fail_the_package", func(t *testing.T) {
		r := harness.New(t)
		cfg := harness.BaseFile(1)
		cfg.Scripts = map[string]models.Script{
			"build": {echoBuild}, "publish": {"echo publishing"},
			"boom": {"exit 1"}, "onfail": {"echo onFail:$DISPAT_FAILED_STAGE >> ../../hooks.log"},
		}
		cfg.Spaces = map[string]models.SpaceConfig{"libs": {Path: "packages", Flow: &models.SpaceFlowConfig{
			Build: []string{"build"}, Publish: []string{"publish"},
			PostBuild: []string{"boom"}, OnFail: []string{"onfail"},
		}}}
		r.WriteConfigModel(cfg)
		r.SeedPackage("packages", "core")
		r.Commit("feat(core): doomed")

		res := r.Release()
		assert.NotZero(t, res.Code, "a failing gating hook fails the run")
		assert.Empty(t, r.TagList(), "nothing may be tagged")
		data, err := os.ReadFile(r.Path("hooks.log"))
		require.NoError(t, err)
		assert.Contains(t, string(data), "onFail:build",
			"onFail observes the failure with the stage that carried it")
	})
}

// TestHooksRunLevelHooksAreTheReleasesOwn pins the boundary the run-level
// hooks live on: they belong to `dispat release`'s phases, and the step
// commands do not fire them.
//
// The distinction is not arbitrary. A run hook exists to give an operator a
// seam into a moment dispat chooses: inside a release, dispat decides when the
// commit happens, so it offers `beforeCommit`. A flow that calls
// `dispat commit` itself already owns that moment and brackets it by writing
// the line before and the line after.
//
// Firing them from the step command would also break the "once per run"
// promise the whole `run` object rests on. `beforePublish` runs per package,
// and the documented flow nests `dispat commit --tag --push` there, so a
// release of N packages would fire each commit hook N times from inside
// itself and once more from its own finalize, with no way for the script to
// tell which firing it was looking at.
func TestHooksRunLevelHooksAreTheReleasesOwn(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Scripts["hook"] = models.Script{"echo $DISPAT_STAGE >> hooks.log"}
	cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true), Push: true}
	cfg.Run = &models.RunConfig{
		BeforeAll:    []string{"hook"},
		PostAll:      []string{"hook"},
		BeforeCommit: []string{"hook"},
		AfterCommit:  []string{"hook"},
		PostCommit:   []string{"hook"},
		BeforePush:   []string{"hook"},
		AfterPush:    []string{"hook"},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.AddBareRemote()
	r.Commit("feat(core): first release")
	// Something for the commit to stage, so the phase runs in full rather
	// than taking the clean-folder no-op.
	r.WriteFile("packages/core/generated.txt", "written by a version stage\n")

	// The step command does the whole commit phase: the release commit, the
	// annotated tag and the push.
	res := r.Command("commit", "--tag", "--push")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	require.True(t, r.HasTag("core@0.1.0"), "the work really happened; tags: %v", r.TagList())
	assert.Contains(t, r.Git("log", "-1", "--format=%s"), "chore(release): core@0.1.0")

	assert.NoFileExists(t, r.Path("hooks.log"),
		"a step command fires no run-level hook, not even the ones bracketing what it just did")

	// The same configuration, through the command the hooks belong to: every
	// one of them fires, so the silence above is about the invocation and not
	// about the config being wrong.
	r.CommitEmpty("feat(core): more work")
	r.ReleaseOK()

	data, err := os.ReadFile(r.Path("hooks.log"))
	require.NoError(t, err, "the release fires them")
	assert.Equal(t, "beforeAll\npostAll\nbeforeCommit\nafterCommit\npostCommit\nbeforePush\nafterPush\n",
		string(data), "each exactly once, bracketing its own phase")
}
