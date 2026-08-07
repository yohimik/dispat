package integration

// Area 4: configuration, login scripts, and the cases a config-loading unit
// test cannot witness. Each test targets a behaviour only observable
// through the running binary: flag precedence changing *runtime* behaviour,
// a custom shell actually being invoked, login counted per space rather
// than per script text, a login failure's blast radius, nonPackageScopes
// replacing rather than extending its default, a fused prerelease tag
// format read back across runs, revertOnFail reaching a package skipped
// after its version stage already ran, the onFail/onSkip outcome scripts,
// the GitHub prerelease flag following a real channel transition, and the
// build-exported release attachments uploaded as GitHub assets.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	models "github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestConfigUnknownKeyIsRejected: viper's UnmarshalExact rejects unknown
// keys rather than silently ignoring them — the one config mistake that is
// otherwise invisible until a script that should have run never does. The
// legacy case matters as much as the typo: the space script keys moved into
// the nested `run` object, so a config still written in the old flat shape
// (`buildScript` on the space) must fail loudly instead of releasing with
// no scripts at all. These are exactly the shapes the typed model cannot
// express, so they are authored as raw map[string]any.
func TestConfigUnknownKeyIsRejected(t *testing.T) {
	for name, cfg := range map[string]map[string]any{
		"top_level_typo": {
			"scripts": map[string]any{"build": "echo building", "publish": "echo publishing"},
			"spaces": map[string]any{
				"libs": map[string]any{"path": "packages", "run": map[string]any{"build": "build", "publish": "publish"}},
			},
			"conncurrency": 4,
		},
		"legacy_flat_space_keys": {
			"scripts": map[string]any{"build": "echo building", "publish": "echo publishing"},
			"spaces": map[string]any{
				"libs": map[string]any{"path": "packages", "buildScript": "build", "publishScript": "publish"},
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := harness.New(t)
			r.WriteConfigRaw(cfg)
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
	r.WriteConfigModel(libsConfig(r.TsmarkScript("build.log", "$DISPAT_PACKAGE", 150*time.Millisecond), 1))
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
	cfg := libsConfig("arr=(a b c); echo ${arr[1]} > shellcheck.txt", 1)
	cfg.Shell = []string{bash, "-c"}
	r.WriteConfigModel(cfg)
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
	cfg := harness.BaseFile(2)
	cfg.Scripts = map[string]string{
		"build":   "echo building",
		"publish": "echo publishing",
		"login":   r.TsmarkScript("login.log", "$DISPAT_SPACE", 0),
	}
	withLogin := models.SpaceRunConfig{Build: []string{"build"}, Publish: []string{"publish"}, Login: []string{"login"}}
	cfg.Spaces = map[string]models.SpaceConfig{
		"spaceA": {Path: "packages/a", Run: withLogin},
		"spaceB": {Path: "packages/b", Run: withLogin},
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

// TestConfigLoginFailureIsolatedToItsSpace: a failing login fails every
// publish in *its* space — none of them could have succeeded without it —
// but must not touch an unrelated space's publishes.
func TestConfigLoginFailureIsolatedToItsSpace(t *testing.T) {
	r := harness.New(t)
	cfg := harness.BaseFile(2)
	cfg.Scripts = map[string]string{
		"build":      "echo building",
		"publish":    "echo publishing",
		"bad-login":  "exit 1",
		"good-login": "echo ok",
	}
	cfg.Spaces = map[string]models.SpaceConfig{
		"broken": {Path: "packages/broken", Run: models.SpaceRunConfig{
			Build: []string{"build"}, Publish: []string{"publish"}, Login: []string{"bad-login"}}},
		"fine": {Path: "packages/fine", Run: models.SpaceRunConfig{
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
	cfg := harness.BaseFile(1)
	cfg.Scripts = map[string]string{
		"build":       "echo building",
		"publish":     `[ "$DISPAT_PACKAGE" != "provider" ]`,
		"boom":        "exit 1",
		"record-fail": `env | grep '^DISPAT_' > "../../onfail-$DISPAT_PACKAGE.env"`,
		"record-skip": `env | grep '^DISPAT_' > "../../onskip-$DISPAT_PACKAGE.env"`,
	}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: "packages", Run: models.SpaceRunConfig{
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

// TestConfigNonPackageScopesReplacesDefault: setting nonPackageScopes
// *replaces* the built-in ["release"] default rather than extending it — an
// easy thing to assume the wrong way, since most config keys layer defaults
// underneath. The custom scope becomes exempt; "release" stops being one.
func TestConfigNonPackageScopesReplacesDefault(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.NonPackageScopes = []string{"infra"}
	r.WriteConfigModel(cfg)
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
	cfg := libsConfig(echoBuild, 1)
	cfg.TagFormat = "{name}@v{version}-{channel}{counter}"
	r.WriteConfigModel(cfg)
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
	cfg := harness.BaseFile(1)
	cfg.Scripts = map[string]string{
		"build":        "echo building",
		"fail-publish": "exit 1",
		"mutate":       "echo dirty >> main.txt && echo extra > extra.txt",
		"publish":      "echo publishing",
	}
	cfg.Spaces = map[string]models.SpaceConfig{
		"provider": {Path: "packages/provider", Run: models.SpaceRunConfig{
			Build: []string{"build"}, Publish: []string{"fail-publish"}}},
		"consumer": {Path: "packages/consumer", RevertOnFail: true, Run: models.SpaceRunConfig{
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

// githubConfig returns a config whose GitHub recorder points at the given
// fake API server, with the token read from DISPAT_IT_TOKEN. The publish
// script exports an empty DISPAT_EXPORT_GITHUB — the recorder acts only on
// packages that opted in.
func githubConfig(apiURL string) models.File {
	cfg := harness.BaseFile(1)
	cfg.Scripts = map[string]string{
		"build":   "echo building",
		"publish": `echo "DISPAT_EXPORT_GITHUB=" >> "$DISPAT_OUTPUT"`,
	}
	cfg.Spaces = map[string]models.SpaceConfig{"libs": {Path: "packages", Run: models.SpaceRunConfig{
		Build: []string{"build"}, Publish: []string{"publish"}}}}
	cfg.GitHub = models.GitHubConfig{
		Enabled: harness.Bool(true), Owner: "acme", Repo: "mono",
		APIURL: apiURL, TokenEnv: "DISPAT_IT_TOKEN",
	}
	return cfg
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
	r.WriteConfigModel(githubConfig(srv.URL))
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

// TestConfigGithubReleaseAttachments exercises the whole script-output and
// attachment path through the real binary: the build script exports
// DISPAT_EXPORT_GITHUB (two files) — opting the package into a GitHub
// release — plus an ordinary output into $DISPAT_OUTPUT, the publish and
// announce scripts must see them again (the export under its full name, the
// output as DISPAT_OUTPUT_*), and the created GitHub release must receive
// both files as assets at the endpoint the release itself advertised
// (upload_url).
func TestConfigGithubReleaseAttachments(t *testing.T) {
	type upload struct {
		name, body string
	}
	var mu sync.Mutex
	var uploads []upload
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.Method == http.MethodGet:
			w.WriteHeader(http.StatusOK)
		case req.URL.Path == "/uploads":
			body, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			mu.Lock()
			uploads = append(uploads, upload{name: req.URL.Query().Get("name"), body: string(body)})
			mu.Unlock()
			w.WriteHeader(http.StatusCreated)
		default: // release creation: advertise this server as the asset endpoint
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"upload_url": "` + srv.URL + `/uploads{?name,label}"}`))
		}
	}))
	defer srv.Close()

	r := harness.New(t)
	cfg := githubConfig(srv.URL)
	cfg.Scripts = map[string]string{
		"build": `echo binary-bytes > app.bin && echo docs-bytes > docs.txt` +
			` && echo "DISPAT_EXPORT_GITHUB=$PWD/app.bin $PWD/docs.txt" >> "$DISPAT_OUTPUT"` +
			` && echo "BUILD_FLAVOUR=release" >> "$DISPAT_OUTPUT"`,
		"publish":  `echo "publish: $DISPAT_OUTPUTS / $DISPAT_EXPORT_GITHUB" > ../../publish-env.txt`,
		"announce": `echo "announce: $DISPAT_OUTPUT_BUILD_FLAVOUR" > ../../announce-env.txt`,
	}
	cfg.Spaces = map[string]models.SpaceConfig{"libs": {Path: "packages", Run: models.SpaceRunConfig{
		Build: []string{"build"}, Publish: []string{"publish"}, Announce: []string{"announce"}}}}
	r.WriteConfigModel(cfg)
	t.Setenv("DISPAT_IT_TOKEN", "tkn")
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first release with artefacts")

	r.ReleaseOK()

	// The ordinary output reached the later stages as DISPAT_OUTPUT_*; the
	// GitHub export travelled under its full name and stayed out of the
	// DISPAT_OUTPUTS listing.
	pubEnv, err := os.ReadFile(r.Path("publish-env.txt"))
	require.NoError(t, err)
	assert.Contains(t, string(pubEnv), "publish: BUILD_FLAVOUR / ")
	assert.Contains(t, string(pubEnv), "/app.bin")
	assert.Contains(t, string(pubEnv), "/docs.txt")
	annEnv, err := os.ReadFile(r.Path("announce-env.txt"))
	require.NoError(t, err)
	assert.Contains(t, string(annEnv), "announce: release")

	// Both files landed on the release as assets, named after their files.
	mu.Lock()
	defer mu.Unlock()
	require.Len(t, uploads, 2, "uploads: %v", uploads)
	byName := map[string]string{}
	for _, u := range uploads {
		byName[u.name] = u.body
	}
	assert.Equal(t, "binary-bytes\n", byName["app.bin"])
	assert.Equal(t, "docs-bytes\n", byName["docs.txt"])
}

// TestConfigScriptOutputsCarryAcrossStagesAndHooks pins the whole
// DISPAT_OUTPUT accumulation contract through the real binary, hooks
// included: a beforeBuild *hook* export reaches the build and publish, the
// build's export reaches the publish, and on the failing package the onFail
// outcome script receives both the hook's export and what the failing build
// exported right before dying.
func TestConfigScriptOutputsCarryAcrossStagesAndHooks(t *testing.T) {
	r := harness.New(t)
	cfg := harness.BaseFile(1)
	cfg.Scripts = map[string]string{
		"hook-export": `echo "HOOK_MARK=hook-$DISPAT_PACKAGE" >> "$DISPAT_OUTPUT"`,
		"build": `if [ "$DISPAT_PACKAGE" = "bad" ]; then` +
			` echo "BUILD_MARK=pre-fail" >> "$DISPAT_OUTPUT"; exit 1; fi;` +
			` echo "BUILD_MARK=built" >> "$DISPAT_OUTPUT"`,
		"publish":     `env | grep '^DISPAT_OUTPUT' | sort > "../../publish-$DISPAT_PACKAGE.env"`,
		"record-fail": `env | grep '^DISPAT_OUTPUT' | sort > "../../onfail-$DISPAT_PACKAGE.env"`,
	}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: "packages", Run: models.SpaceRunConfig{
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

// TestConfigParserOptions: the top-level `parser` object reconfigures the
// commit-message parser end to end. A custom type table makes `docs` release
// a patch, the configured default propagation depth carries it to the direct
// consumer with no caret written, strictTypes turns an unknown type into an
// error (E140) that commitErrors' default policy tolerates, and an invalid
// parser value fails the load rather than the first release.
func TestConfigParserOptions(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Dependencies = []models.DependencyConfig{{Consumer: "app", Provider: "core"}}
	cfg.Parser = models.ParserConfig{
		Types:       map[string]string{"feat": "minor", "fix": "patch", "docs": "patch"},
		StrictTypes: true,
		Propagation: models.ParserPropagationConfig{Depth: "1"},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "app")

	// docs is release-worthy under the custom table, and the configured
	// default depth reaches app without a caret anywhere in the message.
	r.Commit("docs(core): documentation now ships")
	r.ReleaseOK()
	assert.True(t, r.HasTag("core@0.0.1"), "docs bumps patch under the custom table; tags: %v", r.TagList())
	assert.True(t, r.HasTag("app@0.0.1"),
		"the configured propagation depth carries the bump with no caret; tags: %v", r.TagList())

	// strictTypes: an unknown type is an error, not a shrug — reported, and
	// under the default commitErrors policy the unit just contributes nothing.
	r.CommitEmpty("wat(core): a type nobody declared")
	res := r.StatusOK()
	assert.True(t, harness.HasCode(res.Events, "E140"), "strictTypes raises E140 for the unknown type")
	r.ReleaseOK()
	assert.Equal(t, 1, r.TagCount("core@"), "the invalid unit releases nothing")

	// A bad parser value is a load error: exit 1 before any planning.
	bad := libsConfig(echoBuild, 1)
	bad.Parser = models.ParserConfig{Types: map[string]string{"docs": "huge"}}
	r.WriteConfigModel(bad)
	badRes := r.Status()
	assert.Equal(t, 1, badRes.Code, "an invalid parser option must fail the load\nstdout:\n%s\nstderr:\n%s",
		badRes.Stdout, badRes.Stderr)
}
