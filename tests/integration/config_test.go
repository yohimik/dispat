package integration

// Area 4: configuration, login scripts, and the cases a config-loading unit
// test cannot witness. Each test targets a behaviour only observable
// through the running binary: flag precedence changing *runtime* behaviour,
// a custom shell actually being invoked, login counted per space rather
// than per script text, a login failure's blast radius, nonPackageScopes
// replacing rather than extending its default, a fused prerelease tag
// format read back across runs, revertOnFail reaching a package skipped
// after its version stage already ran, the onFail/onSkip outcome scripts,
// and the GitHub prerelease flag following a real channel transition.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestConfigUnknownKeyIsRejected: viper's UnmarshalExact rejects unknown
// keys rather than silently ignoring them — the one config mistake that is
// otherwise invisible until a script that should have run never does. The
// legacy case matters as much as the typo: the space script keys moved into
// the nested `run` object, so a config still written in the old flat shape
// (`buildScript` on the space) must fail loudly instead of releasing with
// no scripts at all.
func TestConfigUnknownKeyIsRejected(t *testing.T) {
	for name, cfg := range map[string]string{
		"top_level_typo": `{
  "scripts": {"build": "echo building", "publish": "echo publishing"},
  "spaces": {"libs": {"path": "packages", "run": {"build": "build", "publish": "publish"}}},
  "conncurrency": 4
}`,
		"legacy_flat_space_keys": `{
  "scripts": {"build": "echo building", "publish": "echo publishing"},
  "spaces": {"libs": {"path": "packages", "buildScript": "build", "publishScript": "publish"}}
}`,
	} {
		t.Run(name, func(t *testing.T) {
			r := harness.New(t)
			r.WriteConfig(cfg)
			r.SeedPackage("packages", "core")
			r.Commit("feat(core): first release")

			res := r.Status()
			assert.Equal(t, 1, res.Code,
				"an unknown key must fail config loading, not be silently ignored\nstdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		})
	}
}

// TestConfigConcurrencyFlagOverridesFile proves flag precedence at runtime,
// not just at parse time: the file pins a serial budget, --concurrency
// raises it, and only a real measured overlap tells the two apart.
func TestConfigConcurrencyFlagOverridesFile(t *testing.T) {
	names := packageNames(4, "pkg")
	r := harness.New(t)
	r.WriteConfig(fmt.Sprintf(`{
  "scripts": {"build": %q, "publish": "echo publishing"},
  "spaces": {"libs": {"path": "packages", "run": {"build": "build", "publish": "publish"}}},
  %s
}`, r.TsmarkScript("build.log", "$DISPAT_PACKAGE", 150*time.Millisecond), harness.Base("1")))
	seedIndependentPackages(r, names)

	r.ReleaseOK("--concurrency", fmt.Sprintf("%d,%d", len(names), len(names)))

	tl := r.Timeline("build.log")
	require.Len(t, tl, len(names))
	harness.AssertConcurrencyBudget(t, tl, len(names))
}

// TestConfigCustomShellIsUsed proves the "shell" option actually changes
// the interpreter scripts run under, not merely that it is accepted: a
// bashism invalid under the default /bin/sh -c must succeed once "shell"
// names bash.
func TestConfigCustomShellIsUsed(t *testing.T) {
	const bash = "/bin/bash"
	if _, err := os.Stat(bash); err != nil {
		t.Skip("bash not available at /bin/bash")
	}
	r := harness.New(t)
	r.WriteConfig(fmt.Sprintf(`{
  "scripts": {"build": "arr=(a b c); echo ${arr[1]} > shellcheck.txt", "publish": "echo publishing"},
  "spaces": {"libs": {"path": "packages", "run": {"build": "build", "publish": "publish"}}},
  "shell": ["%s", "-c"],
  %s
}`, bash, harness.Base("1")))
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first release")

	r.ReleaseOK()
	data, err := os.ReadFile(r.Path("packages", "core", "shellcheck.txt"))
	require.NoError(t, err)
	assert.Equal(t, "b\n", string(data))
}

// TestConfigLoginOncePerSpaceAcrossSpaces: two spaces referencing the exact
// same login script text still log in once *each* — configuration.md is
// explicit that credentials belong to the space, not the script. The cli
// package's own end-to-end test covers only the single-space case (two
// packages, one login); this is the shape that would catch a login gate
// accidentally keyed by script text instead of by space.
func TestConfigLoginOncePerSpaceAcrossSpaces(t *testing.T) {
	r := harness.New(t)
	r.WriteConfig(fmt.Sprintf(`{
  "scripts": {
    "build": "echo building",
    "publish": "echo publishing",
    "login": %q
  },
  "spaces": {
    "spaceA": {"path": "packages/a", "run": {"build": "build", "publish": "publish", "login": "login"}},
    "spaceB": {"path": "packages/b", "run": {"build": "build", "publish": "publish", "login": "login"}}
  },
  %s
}`, r.TsmarkScript("login.log", "$DISPAT_SPACE", 0), harness.Base("2")))
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

// TestConfigLoginFailureIsolatedToItsSpace: a failing login fails every
// publish in *its* space — none of them could have succeeded without it —
// but must not touch an unrelated space's publishes.
func TestConfigLoginFailureIsolatedToItsSpace(t *testing.T) {
	r := harness.New(t)
	r.WriteConfig(`{
  "scripts": {
    "build": "echo building",
    "publish": "echo publishing",
    "bad-login": "exit 1",
    "good-login": "echo ok"
  },
  "spaces": {
    "broken": {"path": "packages/broken", "run": {"build": "build", "publish": "publish", "login": "bad-login"}},
    "fine": {"path": "packages/fine", "run": {"build": "build", "publish": "publish", "login": "good-login"}}
  },
  ` + harness.Base("2") + `
}`)
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

// TestConfigOnFailAndOnSkipOutcomeScripts covers the outcome scripts end to
// end in one failing run over three packages: provider fails its publish,
// consumer is skipped because of it, bystander publishes. onFail must run
// once for provider with the failure specifics (DISPAT_FAILED_STAGE,
// DISPAT_ERROR), onSkip once for consumer with the blame
// (DISPAT_BLOCKED_BY), and neither for the package that published. onFail
// is wired as a two-command sequence whose first command fails, proving the
// warn-only rule the docs state: the rest of the sequence still runs, and
// the failing outcome script changes nothing about the run's outcome.
func TestConfigOnFailAndOnSkipOutcomeScripts(t *testing.T) {
	r := harness.New(t)
	r.WriteConfig(`{
  "scripts": {
    "build": "echo building",
    "publish": "[ \"$DISPAT_PACKAGE\" != \"provider\" ]",
    "boom": "exit 1",
    "record-fail": "env | grep '^DISPAT_' > \"../../onfail-$DISPAT_PACKAGE.env\"",
    "record-skip": "env | grep '^DISPAT_' > \"../../onskip-$DISPAT_PACKAGE.env\""
  },
  "spaces": {"libs": {"path": "packages", "run": {
    "build": "build",
    "publish": "publish",
    "onFail": ["boom", "record-fail"],
    "onSkip": "record-skip"
  }}},
  "dependencies": [{"consumer": "consumer", "provider": "provider"}],
  ` + harness.Base("1") + `
}`)
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

// TestConfigNonPackageScopesReplacesDefault: setting nonPackageScopes
// *replaces* the built-in ["release"] default rather than extending it — an
// easy thing to assume the wrong way, since most config keys layer defaults
// underneath. The custom scope becomes exempt; "release" stops being one.
func TestConfigNonPackageScopesReplacesDefault(t *testing.T) {
	r := harness.New(t)
	r.WriteConfig(`{
  "scripts": {"build": "echo building", "publish": "echo publishing"},
  "spaces": {"libs": {"path": "packages", "run": {"build": "build", "publish": "publish"}}},
  "nonPackageScopes": ["infra"],
  ` + harness.Base("1") + `
}`)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first release")
	r.ReleaseOK()

	// The custom scope is exempt: a commit naming only it is inert, not an
	// unknown-package error.
	r.CommitEmpty("chore(infra): touch up the pipeline config")
	res := r.StatusOK()
	assert.False(t, harness.HasCode(res.Events, "E130"), "infra is exempt by explicit configuration")

	// "release" is no longer exempt, because setting the key replaced the
	// built-in default rather than adding to it.
	r.CommitEmpty("chore(release): pretend to be dispat's own commit")
	res = r.StatusOK()
	assert.True(t, harness.HasCode(res.Events, "E130"),
		"release must no longer be exempt once nonPackageScopes was set explicitly")
}

// TestConfigFusedPrereleaseTagFormatRoundTrips exercises a tagFormat fusing
// channel and counter with no separator — configuration.md documents the
// split at the letter/digit boundary ("beta0" -> "beta"/"0") — across three
// runs, so later runs must *read back* what earlier ones wrote rather than
// merely render it once.
func TestConfigFusedPrereleaseTagFormatRoundTrips(t *testing.T) {
	r := harness.New(t)
	r.WriteConfig(`{
  "scripts": {"build": "echo building", "publish": "echo publishing"},
  "spaces": {"libs": {"path": "packages", "run": {"build": "build", "publish": "publish"}}},
  "tagFormat": "{name}@v{version}-{channel}{counter}",
  ` + harness.Base("1") + `
}`)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core)@beta: start the train")
	r.ReleaseOK()
	require.True(t, r.HasTag("core@v0.1.0-beta0"), "tags: %v", r.TagList())

	// A run with no new commits must read v0.1.0-beta0 back and find
	// nothing pending — the format round-trips, not just renders.
	r.ReleaseOK()
	assert.Equal(t, 1, r.TagCount("core@"), "a released prerelease must not re-release itself")

	// A new commit continues the counter, which only works if the previous
	// run parsed "beta0" back into channel "beta", counter "0".
	r.CommitEmpty("fix(core): tweak")
	r.ReleaseOK()
	assert.True(t, r.HasTag("core@v0.1.0-beta1"), "tags: %v", r.TagList())
}

// TestConfigRevertOnFailAppliesAfterVersionStageOnSkip covers the
// documented but easy-to-miss half of revertOnFail: "the same rollback runs
// when a package is skipped after its version stage already modified
// files." The consumer's version script dirties its folder; the provider's
// *publish* then fails (its build succeeded, so with isBuildWaitingPublish
// at its default the consumer's version and build stages have already run);
// the consumer is skipped at its own publish — after real damage — and
// revertOnFail must still clean its folder up.
func TestConfigRevertOnFailAppliesAfterVersionStageOnSkip(t *testing.T) {
	r := harness.New(t)
	r.WriteConfig(`{
  "scripts": {
    "build": "echo building",
    "fail-publish": "exit 1",
    "mutate": "echo dirty >> main.txt && echo extra > extra.txt",
    "publish": "echo publishing"
  },
  "spaces": {
    "provider": {"path": "packages/provider", "run": {"build": "build", "publish": "fail-publish"}},
    "consumer": {"path": "packages/consumer", "revertOnFail": true, "run": {"version": "mutate", "build": "build", "publish": "publish"}}
  },
  "dependencies": [{"consumer": "consumer", "provider": "provider"}],
  ` + harness.Base("1") + `
}`)
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

// TestConfigGithubReleasePrereleaseFlagFollowsChannel exercises the GitHub
// release recorder through a real train and its graduation, end to end
// rather than in isolation: the same package's releases must flip
// `prerelease` true then false as its channel actually changes.
func TestConfigGithubReleasePrereleaseFlagFollowsChannel(t *testing.T) {
	type ghRelease struct {
		TagName    string `json:"tag_name"`
		Prerelease bool   `json:"prerelease"`
	}
	var mu sync.Mutex
	var releases []ghRelease
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.Method {
		case http.MethodGet: // upfront verification
			w.WriteHeader(http.StatusOK)
		case http.MethodPost:
			var rel ghRelease
			require.NoError(t, json.NewDecoder(req.Body).Decode(&rel))
			mu.Lock()
			releases = append(releases, rel)
			mu.Unlock()
			w.WriteHeader(http.StatusCreated)
		}
	}))
	defer srv.Close()

	r := harness.New(t)
	r.WriteConfig(fmt.Sprintf(`{
  "scripts": {"build": "echo building", "publish": "echo publishing"},
  "spaces": {"libs": {"path": "packages", "run": {"build": "build", "publish": "publish"}}},
  "concurrency": 1,
  "logLevel": "info",
  "logFormat": "json",
  "github": {"enabled": true, "owner": "acme", "repo": "mono", "apiUrl": %q, "tokenEnv": "DISPAT_IT_TOKEN"}
}`, srv.URL))
	t.Setenv("DISPAT_IT_TOKEN", "tkn")
	r.SeedPackage("packages", "core")
	r.Commit("feat(core)@beta: start the train")

	r.ReleaseOK()
	r.CommitEmpty("release(core)@stable: graduate")
	r.ReleaseOK()

	require.Len(t, releases, 2)
	assert.Equal(t, "core@0.1.0-beta.0", releases[0].TagName)
	assert.True(t, releases[0].Prerelease, "the beta release must be marked a prerelease")
	assert.Equal(t, "core@0.1.0", releases[1].TagName)
	assert.False(t, releases[1].Prerelease, "the graduated release must not be")
}
