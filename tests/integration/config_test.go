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
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestConfigUnknownKeyIsRejected: viper's UnmarshalExact rejects unknown
// keys rather than silently ignoring them — the one config mistake that is
// otherwise invisible until a script that should have run never does. An
// unknown key is exactly the shape the typed model cannot express, so this
// one config is authored as a raw map[string]any.
func TestConfigUnknownKeyIsRejected(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigRaw(map[string]any{
		"scripts": map[string]any{"build": "echo building", "publish": "echo publishing"},
		"spaces": map[string]any{
			"libs": map[string]any{"path": "packages", "flow": map[string]any{"build": "build", "publish": "publish"}},
		},
		"conncurrency": 4,
	})
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first release")

	res := r.Status()
	assert.Equal(t, 1, res.Code,
		"an unknown key must fail config loading, not be silently ignored\nstdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
}

// TestConfigFileFallbackResolution: without --config the binary resolves the
// first of dispat.json, dispat.yaml, dispat.yml, dispat.toml that exists —
// the names `dispat init` writes under its formats — and with none present it
// fails with an error naming what it tried. JSON is valid YAML, so the typed
// model marshalled to JSON serves as the yaml-named config.
func TestConfigFileFallbackResolution(t *testing.T) {
	r := harness.New(t)
	data, err := json.MarshalIndent(libsConfig(echoBuild, 1), "", "  ")
	require.NoError(t, err)
	r.WriteFile("dispat.yaml", string(data))
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first release")

	res := r.Status()
	assert.Equal(t, 0, res.Code,
		"dispat.yaml must be found without --config\nstdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)

	require.NoError(t, os.Remove(r.Path("dispat.yaml")))
	res = r.Status()
	assert.Equal(t, 1, res.Code, "no config file at all must fail the run")
	assert.Contains(t, res.Stderr, "no dispat config file found",
		"the error must say what is missing rather than fail obscurely")
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
// merely render it once. A second space overriding the repository format
// with the normative one pins that a tag format is a per-space property: the
// fused spelling must not bleed onto the other space's tags.
func TestConfigFusedPrereleaseTagFormatRoundTrips(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.TagFormat = "{name}@v{version}-{channel}{counter}"
	cfg.Spaces["apps"] = models.SpaceConfig{Path: "apps", Flow: buildPublish(),
		TagFormat: "{name}@{version}"}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("apps", "web")
	r.Commit("feat(core)%beta: start the train\n---\nfeat(web)%beta: ride along in the other space")
	r.ReleaseOK()
	require.True(t, r.HasTag("core@v0.1.0-beta0"), "tags: %v", r.TagList())
	require.True(t, r.HasTag("web@0.1.0-beta.0"),
		"the space override keeps the normative spelling; tags: %v", r.TagList())

	// A run with no new commits must read v0.1.0-beta0 back and find
	// nothing pending — the format round-trips, not just renders.
	r.ReleaseOK()
	assert.Equal(t, 1, r.TagCount("core@"), "a released prerelease must not re-release itself")
	assert.Equal(t, 1, r.TagCount("web@"), "both formats must round-trip")

	// A new commit continues the counter, which only works if the previous
	// run parsed "beta0" back into channel "beta", counter "0".
	r.CommitEmpty("fix(core): tweak")
	r.ReleaseOK()
	assert.True(t, r.HasTag("core@v0.1.0-beta1"), "tags: %v", r.TagList())
	assert.Equal(t, 1, r.TagCount("web@"), "the other space had no new work")
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
	cfg.Spaces = map[string]models.SpaceConfig{"libs": {Path: "packages", Flow: &models.SpaceFlowConfig{
		Build: []string{"build"}, Publish: []string{"publish"}}}}
	cfg.GitHub = &models.GitHubConfig{
		Enabled: models.Bool(true), Owner: "acme", Repo: "mono",
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
	srv, bodies := githubFake(t)

	r := harness.New(t)
	r.WriteConfigModel(githubConfig(srv.URL))
	t.Setenv("DISPAT_IT_TOKEN", "tkn")
	r.SeedPackage("packages", "core")
	r.Commit("feat(core)%beta: start the train")

	r.ReleaseOK()
	r.CommitEmpty("release(core)%stable: graduate")
	r.ReleaseOK()

	releases := decodeAll[ghRelease](t, bodies())
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
		if githubTagProbe(w, req, nil) {
			return
		}
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
	cfg.Spaces = map[string]models.SpaceConfig{"libs": {Path: "packages", Flow: &models.SpaceFlowConfig{
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
	cfg.Parser = &models.ParserConfig{
		Types:       map[string]string{"feat": "minor", "fix": "patch", "docs": "patch"},
		StrictTypes: true,
		Propagation: &models.ParserPropagationConfig{Depth: "1"},
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
	bad.Parser = &models.ParserConfig{Types: map[string]string{"docs": "huge"}}
	r.WriteConfigModel(bad)
	badRes := r.Status()
	assert.Equal(t, 1, badRes.Code, "an invalid parser option must fail the load\nstdout:\n%s\nstderr:\n%s",
		badRes.Stdout, badRes.Stderr)
}

// TestConfigCommitErrorsPolicy: a commit whose scope names no package is a
// unit-scoped error (§16). Under the default "warn" the unit contributes
// nothing and the run releases the rest; under "error" the release is
// refused outright — while `status` still exits 0 either way, because seeing
// the plan is the point of the operation.
func TestConfigCommitErrorsPolicy(t *testing.T) {
	t.Run("warn", func(t *testing.T) {
		r := singlePackageRepo(t, echoBuild)
		r.Commit("feat(core): real work")
		r.CommitEmpty("fix(nosuch): typo in the scope")

		res := r.ReleaseOK()
		assert.True(t, harness.HasCode(res.Events, "E130"), "the diagnostic is reported even when tolerated")
		assert.True(t, r.HasTag("core@0.1.0"), "the sibling work still releases; tags: %v", r.TagList())
	})
	t.Run("error", func(t *testing.T) {
		r := harness.New(t)
		cfg := libsConfig(echoBuild, 1)
		cfg.CommitErrors = "error"
		r.WriteConfigModel(cfg)
		r.SeedPackage("packages", "core")
		r.Commit("feat(core): real work")
		r.CommitEmpty("fix(nosuch): typo in the scope")

		status := r.StatusOK()
		assert.True(t, harness.HasCode(status.Events, "E130"), "status reports the plan it would refuse")

		res := r.Release()
		require.Equal(t, 1, res.Code, "commitErrors=error must refuse the release\nstdout:\n%s", res.Stdout)
		assert.True(t, harness.HasCode(res.Events, "E130"))
		assert.Empty(t, r.TagList(), "nothing may be released under a refused plan")
	})
}

// TestConfigInitialsBaselines: the initials map seeds the baseline of a
// package whose latest tag is missing or unparseable. The unparseable-tag
// half is the subtle one: the pre-last tag must NOT be used — the version
// comes from initials while the pending window is still measured from the
// broken tag, so already released commits are not counted twice — and an
// initials key naming no discovered package only warns.
func TestConfigInitialsBaselines(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Initials = map[string]string{"core": "1.0.0", "ghost": "5.0.0"}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")

	// A feat, then an unparseable tag over it, then a fix: only the fix is
	// pending, and it bumps on top of the configured 1.0.0.
	r.Commit("feat(core): released under the broken tag")
	r.Git("tag", "-a", "core@0.1.0.broken", "-m", "broken tag")
	r.CommitEmpty("fix(core): repair")

	res := r.ReleaseOK()
	assert.True(t, r.HasTag("core@1.0.1"),
		"initials 1.0.0 + only the fix since the unparseable tag; tags: %v", r.TagList())
	assert.Contains(t, res.Stdout, "baselineFromInitials")
	assert.Contains(t, res.Stdout, "initials entry matches no discovered package",
		"the ghost key warns instead of failing the run")

	// Converged: the new tag is parseable, initials no longer apply to core.
	r.CommitEmpty("fix(core): once more")
	r.ReleaseOK()
	assert.True(t, r.HasTag("core@1.0.2"), "the next bump reads the real tag back; tags: %v", r.TagList())
}

// TestConfigRunLevelHooks: the run-level hook frame end to end against a
// real remote — every hook fires in phase order in the monorepo root, postAll
// sees the run outcome and the workspace listing, and a quiet second run
// keeps the commit and push hooks off because their phases never happen.
func TestConfigRunLevelHooks(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Scripts["hook"] = "echo $DISPAT_STAGE >> hooks.log"
	cfg.Scripts["dump"] = "env | grep '^DISPAT_' > postall.env"
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

// TestConfigRunLevelHookFailureSemantics: one flowing scenario for the two
// failure modes of the run-level hooks. A failing warn-only hook (postAll)
// does not fail the run and the rest of its sequence still executes; the
// gating beforeAll, by contrast, aborts the run before any release work.
func TestConfigRunLevelHookFailureSemantics(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Scripts["boom"] = "exit 1"
	cfg.Scripts["hook"] = "echo $DISPAT_STAGE >> hooks.log"
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

// TestConfigFormatsSmoke: one minimal end-to-end smoke per supported config
// format — the same monorepo releases through the binary under a
// dispat.json, a dispat.yaml and a dispat.toml (the binary's own starter).
// The json leg also pins that `status` reports without tagging. Everything
// deeper about the formats is the config loader's unit territory
// (TestLoadFormats there); resolution order is TestConfigFileFallbackResolution.
func TestConfigFormatsSmoke(t *testing.T) {
	t.Run("json", func(t *testing.T) {
		r := singlePackageRepo(t, echoBuild)
		r.Commit("feat(core): first release")
		r.StatusOK()
		assert.Empty(t, r.TagList(), "status must not tag")
		r.ReleaseOK()
		assert.True(t, r.HasTag("core@0.1.0"), "tags: %v", r.TagList())
	})
	t.Run("yaml", func(t *testing.T) {
		r := harness.New(t)
		data, err := json.MarshalIndent(libsConfig(echoBuild, 1), "", "  ")
		require.NoError(t, err)
		r.WriteFile("dispat.yaml", string(data)) // JSON is valid YAML
		r.SeedPackage("packages", "core")
		r.Commit("feat(core): first release")
		r.ReleaseOK()
		assert.True(t, r.HasTag("core@0.1.0"), "tags: %v", r.TagList())
	})
	t.Run("toml", func(t *testing.T) {
		// The starter leaves GitHub at its enabled default; blank the env so
		// a CI runner's GITHUB_* variables can never reach the real API.
		t.Setenv("GITHUB_REPOSITORY", "")
		t.Setenv("GITHUB_TOKEN", "")
		r := harness.New(t)
		res := r.Command("init", "--format", "toml") // the binary's own TOML starter
		require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
		r.SeedPackage("packages", "core")
		r.Commit("feat(core): first release")
		r.ReleaseOK()
		assert.True(t, r.HasTag("core@0.1.0"), "tags: %v", r.TagList())
	})
}

// TestConfigResolutionAscendsToTheMonorepoRoot: without --config, resolution
// climbs from --root through its parents until a config file is found, and
// the config's own directory becomes the effective monorepo root — so the
// CLI works from inside a package folder. The release invoked from
// packages/core must behave exactly as one invoked from the top: same tags,
// same changelog placement, converging second run included.
func TestConfigResolutionAscendsToTheMonorepoRoot(t *testing.T) {
	r := singlePackageRepo(t, echoBuild)
	r.Commit("feat(core): first release")

	res := r.CommandAt("packages/core", "status")
	require.Equal(t, 0, res.Code,
		"status must find the root config from inside the package\nstdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Empty(t, r.TagList(), "status must not tag")

	res = r.CommandAt("packages/core", "release")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.True(t, r.HasTag("core@0.1.0"),
		"the tag lands in the monorepo's repository, not a nested one; tags: %v", r.TagList())
	assert.FileExists(t, r.Path("packages", "core", "CHANGELOG.md"),
		"the changelog lands under the resolved root")

	res = r.CommandAt("packages/core", "release")
	require.Equal(t, 0, res.Code, "a converged release from the subfolder is a clean no-op")
	assert.Equal(t, 1, r.TagCount("core@"))

	// An explicit --config stays anchored to --root: it must not ascend.
	res = r.CommandAt("packages/core", "status", "--config", "dispat.json")
	assert.Equal(t, 1, res.Code, "an explicit --config must fail from the subfolder, not fall back")
}

// TestConfigGitRepositoryGuard: a config without a git repository around it —
// and an `init` pointed outside one — both fail with one clear error before
// any work, instead of a raw git failure halfway through planning. This is
// the one test that must not use harness.New, which git-inits its repository;
// it drives the built binary against a bare temp directory instead.
func TestConfigGitRepositoryGuard(t *testing.T) {
	dispat, _ := harness.Build(t)
	root := t.TempDir() // a config, packages, but no .git
	data, err := json.MarshalIndent(libsConfig(echoBuild, 1), "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "packages", "core"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "dispat.json"), data, 0o644))

	out, runErr := exec.Command(dispat, "status", "--root", root).CombinedOutput()
	require.Error(t, runErr, "status without a repository must fail\n%s", out)
	assert.Contains(t, string(out), "not a git repository root",
		"the guard names the problem instead of surfacing a raw git error")

	out, runErr = exec.Command(dispat, "init", "--root", t.TempDir()).CombinedOutput()
	require.Error(t, runErr, "init outside a repository root must fail\n%s", out)
	assert.Contains(t, string(out), "not a git repository root")
}

// hookLog builds the scripts map wiring every per-package stage hook (all
// nine), the three stages and announce to one appending log line each, so a
// scenario can read back exactly what fired and in which order.
func hookLog() map[string]string {
	names := []string{
		"beforeAll", "beforeVersion", "postVersion", "beforeBuild", "postBuild",
		"beforePublish", "postPublish", "beforeAnnounce", "postAnnounce",
		"build", "publish", "version", "announce",
	}
	scripts := make(map[string]string, len(names))
	for _, n := range names {
		scripts[n] = "echo " + n + ":$DISPAT_PACKAGE >> ../../hooks.log"
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

// TestConfigAllStageHooksFireInOrder exercises the full per-package hook
// frame — every one of the nine hooks plus the announce stage — on a
// provider/consumer pair. The provider runs the plain frame; the consumer,
// bumped because of the provider, additionally runs the version stage with
// its two hooks. Order is asserted per package: the frame's shape is a
// per-package promise, interleaving across packages is the scheduler's
// business.
func TestConfigAllStageHooksFireInOrder(t *testing.T) {
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

// TestConfigStageHookAuthoritySplit pins the documented split: hooks up to
// beforePublish gate the release (their failure fails the package), while
// postPublish and the whole announce frame observe a release that is already
// out and may only warn.
func TestConfigStageHookAuthoritySplit(t *testing.T) {
	t.Run("post_publish_and_announce_only_warn", func(t *testing.T) {
		r := harness.New(t)
		cfg := harness.BaseFile(1)
		cfg.Scripts = map[string]string{
			"build": echoBuild, "publish": "echo publishing",
			"boom": "exit 1",
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
		cfg.Scripts = map[string]string{
			"build": echoBuild, "publish": "echo publishing",
			"boom": "exit 1", "onfail": "echo onFail:$DISPAT_FAILED_STAGE >> ../../hooks.log",
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

// TestConfigDispatignoreSelectsTheConfigFile: a folder holding two config
// files says which one is real by naming the other in its .dispatignore, and
// the rule holds at each of the three places a config file can sit — the
// repository root, a space folder and a package folder. Every choice is
// proved by a tag only that file's tagFormat could produce.
func TestConfigDispatignoreSelectsTheConfigFile(t *testing.T) {
	writeYAML := func(r *harness.Repo, path string, value any) {
		t.Helper()
		data, err := json.MarshalIndent(value, "", "  ")
		require.NoError(t, err)
		r.WriteFile(path, string(data)) // JSON is valid YAML
	}

	t.Run("repository root", func(t *testing.T) {
		r := harness.New(t)
		decoy := libsConfig(echoBuild, 1)
		decoy.TagFormat = "json-{name}@{version}"
		r.WriteConfigModel(decoy)
		real := libsConfig(echoBuild, 1)
		real.TagFormat = "yaml-{name}@{version}"
		writeYAML(r, "dispat.yaml", real)
		r.WriteFile(".dispatignore", "# the json file is generated\ndispat.json\n")
		r.SeedPackage("packages", "core")
		r.Commit("feat(core): first release")

		r.ReleaseOK()
		assert.True(t, r.HasTag("yaml-core@0.1.0"), "tags: %v", r.TagList())
	})

	t.Run("space folder", func(t *testing.T) {
		r := harness.New(t)
		r.WriteConfigModel(libsConfig(echoBuild, 1))
		r.SeedPackage("packages", "core")
		spaceFile(t, r, "packages", models.SpaceFile{TagFormat: "json-{name}@{version}"})
		writeYAML(r, "packages/dispat.yaml", models.SpaceFile{TagFormat: "yaml-{name}@{version}"})
		r.WriteFile("packages/.dispatignore", "dispat.json\n")
		r.Commit("feat(core): first release")

		r.ReleaseOK()
		assert.True(t, r.HasTag("yaml-core@0.1.0"), "tags: %v", r.TagList())
	})

	t.Run("package folder", func(t *testing.T) {
		r := harness.New(t)
		r.WriteConfigModel(libsConfig(echoBuild, 1))
		r.SeedPackage("packages", "core")
		packageFile(t, r, "packages/core", models.PackageConfig{TagFormat: "json-{name}@{version}"})
		writeYAML(r, "packages/core/dispat.yaml", models.PackageConfig{TagFormat: "yaml-{name}@{version}"})
		r.WriteFile("packages/core/.dispatignore", "dispat.json\n")
		r.Commit("feat(core): first release")

		r.ReleaseOK()
		assert.True(t, r.HasTag("yaml-core@0.1.0"), "tags: %v", r.TagList())
	})
}

// TestConfigResolutionAscendsPastASpaceFile: a space folder's file declares
// packages, which is also what a monorepo of standalone packages declares.
// Run from inside such a space — and from the space folder itself — the CLI
// must still resolve to the root above, because that root claims the folder.
func TestConfigResolutionAscendsPastASpaceFile(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(libsConfig(echoBuild, 1))
	r.SeedPackage("packages", "core")
	spaceFile(t, r, "packages", models.SpaceFile{
		Packages: map[string]models.PackageConfig{"core": {TagFormat: "space-{name}@{version}"}},
	})
	r.Commit("feat(core): first release")

	for _, from := range []string{"packages/core", "packages"} {
		res := r.CommandAt(from, "status")
		require.Equal(t, 0, res.Code,
			"status from %s must find the root config\nstdout:\n%s\nstderr:\n%s", from, res.Stdout, res.Stderr)
	}

	res := r.CommandAt("packages/core", "release")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.True(t, r.HasTag("space-core@0.1.0"),
		"the space file's entry applied and the tag landed in the monorepo's repository: %v", r.TagList())
}

// TestConfigSpaceLayerRejections: what the new layers may not say, each one
// failing the load before any work rather than being half-applied.
func TestConfigSpaceLayerRejections(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(r *harness.Repo)
		want  string
	}{
		{
			name: "a space packages entry cannot set path",
			setup: func(r *harness.Repo) {
				cfg := libsConfig(echoBuild, 1)
				cfg.Spaces = map[string]models.SpaceConfig{
					"libs": {Path: "packages", Flow: buildPublish(), Packages: map[string]models.PackageConfig{
						"core": {Path: "elsewhere"},
					}},
				}
				r.WriteConfigModel(cfg)
			},
			want: "path cannot be set",
		},
		{
			name: "a space file cannot set path",
			setup: func(r *harness.Repo) {
				r.WriteConfigModel(libsConfig(echoBuild, 1))
				r.WriteFile("packages/dispat.json", `{"path": "elsewhere"}`)
			},
			want: "path cannot be set in a space folder's config file",
		},
		{
			name: "a space file cannot declare spaces",
			setup: func(r *harness.Repo) {
				r.WriteConfigModel(libsConfig(echoBuild, 1))
				r.WriteFile("packages/dispat.json", `{"spaces": {"inner": {"path": "pkgs"}}}`)
			},
			want: "monorepo root of its own",
		},
		{
			name: "a package entry cannot hold packages",
			setup: func(r *harness.Repo) {
				r.WriteConfigRaw(map[string]any{
					"scripts": map[string]any{"build": echoBuild, "publish": "echo publishing"},
					"spaces": map[string]any{"libs": map[string]any{"path": "packages",
						"flow": map[string]any{"build": "build", "publish": "publish"}}},
					"packages": map[string]any{
						"core": map[string]any{"packages": map[string]any{"inner": map[string]any{}}},
					},
				})
			},
			want: "cannot be set on a package entry",
		},
		{
			name: "a space packages key must match a folder of that space",
			setup: func(r *harness.Repo) {
				cfg := libsConfig(echoBuild, 1)
				cfg.Spaces = map[string]models.SpaceConfig{
					"libs": {Path: "packages", Flow: buildPublish(), Packages: map[string]models.PackageConfig{
						"ghost": {TagFormat: "v{version}"},
					}},
				}
				r.WriteConfigModel(cfg)
			},
			// The error travels as a JSON log field, so the assertion stays
			// clear of the quotes the encoder escapes.
			want: "matches no folder of space",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := harness.New(t)
			r.SeedPackage("packages", "core")
			tc.setup(r)
			r.Commit("feat(core): first release")

			res := r.Status()
			require.Equal(t, 1, res.Code,
				"the config must be refused\nstdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.Contains(t, res.Stdout+res.Stderr, tc.want)
			assert.Empty(t, r.TagList(), "nothing may be released from a refused config")
		})
	}
}

// TestConfigParserQuiet: parser.quiet hides the commit-message parser's own
// findings from the log for a repository whose history predates the
// convention. It hides lines and nothing else — the diagnostics are still
// counted, the summary says how many went unprinted, a hidden error still
// refuses the release under commitErrors "error", and the planner's own
// diagnostics are never hidden, because they explain release outcomes a
// reader of the commit log cannot account for.
func TestConfigParserQuiet(t *testing.T) {
	// seed returns a repository whose history carries one parser error (an
	// unknown type under strictTypes, E140) and one planner error (a scope
	// naming no package, E130), plus real work so a plan exists.
	seed := func(t *testing.T, quiet bool, commitErrors string) *harness.Repo {
		t.Helper()
		r := harness.New(t)
		cfg := libsConfig(echoBuild, 1)
		cfg.CommitErrors = commitErrors
		cfg.Parser = &models.ParserConfig{
			Quiet:       quiet,
			StrictTypes: true,
			Types:       map[string]string{"feat": "minor", "fix": "patch"},
		}
		r.WriteConfigModel(cfg)
		r.SeedPackage("packages", "core")
		r.Commit("feat(core): real work")
		r.CommitEmpty("wat(core): a type nobody declared")
		r.CommitEmpty("fix(nosuch): a scope naming no package")
		return r
	}

	loud := seed(t, false, "").StatusOK()
	require.True(t, harness.HasCode(loud.Events, "E140"), "the parser finding prints by default")
	require.True(t, harness.HasCode(loud.Events, "E130"), "so does the planner's")

	quiet := seed(t, true, "").StatusOK()
	assert.False(t, harness.HasCode(quiet.Events, "E140"), "parser.quiet hides the parser's finding")
	assert.True(t, harness.HasCode(quiet.Events, "E130"),
		"a planner finding explains a release outcome and is never hidden")
	assert.Contains(t, quiet.Stdout, `"hidden":1`,
		"a hidden diagnostic is still counted, and the count says how many")

	// The flag overrides the config in both directions.
	r := seed(t, true, "")
	shown := r.Command("status", "--quiet-parser=false")
	require.Equal(t, 0, shown.Code, "stderr:\n%s", shown.Stderr)
	assert.True(t, harness.HasCode(shown.Events, "E140"), "--quiet-parser=false brings the findings back")

	r = seed(t, false, "")
	hushed := r.Command("status", "--quiet-parser")
	require.Equal(t, 0, hushed.Code, "stderr:\n%s", hushed.Stderr)
	assert.False(t, harness.HasCode(hushed.Events, "E140"), "--quiet-parser hides them for one invocation")

	// Display only: a hidden error still refuses the release.
	blocked := seed(t, true, "error").Release()
	assert.Equal(t, 1, blocked.Code, "a hidden error still blocks under commitErrors=error")
	assert.False(t, harness.HasCode(blocked.Events, "E140"))
	assert.Contains(t, blocked.Stdout, "refusing to release",
		"the refusal itself is never hidden, or the run would stop for no stated reason")
}

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
	cfg.Scripts["hook-probe"] = `printf '%s|%s' "$ROOT_V" "$SHARED" > hook_env.txt`
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
	cfg.Scripts["login"] = `printf '%s|%s' "$REGISTRY" "$DISPAT_SPACE" > ../login_env.txt`
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

// TestConfigCustomObjectIsIgnored: `custom` is a free-form object at every
// level that dispat never reads. It exists so a repository's own tooling can
// keep data in the config file without tripping the unknown-key guard, so the
// whole claim is that a release behaves exactly as if it were absent.
func TestConfigCustomObjectIsIgnored(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Custom = map[string]any{"team": "platform", "budget": 3, "nested": map[string]any{"a": true}}
	libs := cfg.Spaces["libs"]
	libs.Custom = map[string]any{"owner": "libs-team"}
	cfg.Spaces["libs"] = libs
	cfg.Packages = map[string]models.PackageConfig{"core": {Custom: map[string]any{"tier": "one"}}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): custom data")

	r.ReleaseOK()
	assert.True(t, r.HasTag("core@0.1.0"), "tags: %v", r.TagList())
}
