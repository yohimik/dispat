package integration

// Goal 15: the standalone step commands. `dispat changelog`, `dispat commit`
// and `dispat autoversion` expose the pipeline's native steps to custom
// flows, and the release stage skips work they already did: a pre-written
// changelog entry is a W222 skip, a pre-created tag at the release commit a
// W223 skip. The central claim is the ordering fix the commands exist for:
// a changelog written by a stage script before the per-package commit lands
// inside the tagged tree.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

func TestStandaloneChangelogWritesAndIsIdempotent(t *testing.T) {
	r := singlePackageRepo(t, echoBuild)
	r.Commit("feat(core): first feature")

	res := r.Command("changelog", "--package", "core")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	log := r.Path("packages", "core", "CHANGELOG.md")
	data, err := os.ReadFile(log)
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(string(data), "## core@0.1.0 ("))
	assert.Contains(t, string(data), "first feature")
	assert.Empty(t, r.TagList(), "the changelog command releases nothing")

	// A second invocation is a W222 skip and changes nothing.
	res = r.Command("changelog", "--package", "core")
	require.Equal(t, 0, res.Code)
	assert.True(t, harness.HasCodeForPackage(res.Events, "W222", "core"))
	after, err := os.ReadFile(log)
	require.NoError(t, err)
	assert.Equal(t, string(data), string(after), "a repeated write is byte-identical")

	// The release finds the entry present and skips its own write: still
	// exactly one header, and the release itself proceeds normally.
	r.ReleaseOK()
	final, err := os.ReadFile(log)
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(string(final), "## core@0.1.0 ("))
	assert.Equal(t, 1, r.TagCount("core@"))

	// An unknown package is an error; a known but unreleasing one is not.
	assert.Equal(t, 1, r.Command("changelog", "--package", "ghost").Code)
	assert.Equal(t, 0, r.Command("changelog", "--package", "core").Code, "converged package: a logged no-op")
}

func TestStandaloneStepsInsideAReleaseFlow(t *testing.T) {
	// The dogfood flow: beforePublish runs the nested `dispat changelog` and
	// `dispat commit --tag`, so the changelog lands inside the tagged commit
	// (the fix under test), and the outer run's own recorder and tagging
	// find the work done: W222 and W223, zero errors.
	r := singlePackageRepo(t, echoBuild)
	cfg := libsConfig(echoBuild, 1)
	cfg.Scripts["step-changelog"] = r.DispatCommand("changelog")
	cfg.Scripts["step-commit"] = r.DispatCommand("commit", "--tag")
	cfg.Spaces["libs"] = models.SpaceConfig{Path: "packages", Flow: &models.SpaceFlowConfig{
		Build: []string{"build"}, Publish: []string{"publish"},
		BeforePublish: []string{"step-changelog", "step-commit"},
	}}
	r.WriteConfigModel(cfg)
	r.Commit("feat(core): flowing feature")

	res := r.ReleaseOK()
	assert.True(t, harness.HasCodeForPackage(res.Events, "W222", "core"), "the recorder skipped the pre-written entry")
	assert.True(t, harness.HasCode(res.Events, "W223"), "tagging skipped the pre-created tag")
	assert.Equal(t, 1, r.TagCount("core@"))

	// THE fix: the tagged tree contains the changelog entry, because the
	// stage script wrote it before the per-package commit was pinned.
	shown := r.Git("show", "core@0.1.0:packages/core/CHANGELOG.md")
	assert.Contains(t, shown, "## core@0.1.0 (")
	assert.Contains(t, shown, "flowing feature")

	// The per-package release commit carries the default message and is what
	// the tag points at.
	assert.Equal(t, "chore(release): core@0.1.0", r.Git("log", "-1", "--format=%s", "core@0.1.0^{commit}"))

	// Convergence: the next run releases nothing and creates nothing new.
	quiet := r.ReleaseOK()
	assert.Equal(t, 1, r.TagCount("core@"))
	assert.False(t, harness.HasCode(quiet.Events, "W223"), "a converged run tags nothing, so it skips nothing")
}

func TestStandaloneCommitPushAndNothingToCommit(t *testing.T) {
	r := singlePackageRepo(t, echoBuild)
	r.Commit("feat(core): pushable feature")
	remote := r.AddBareRemote()
	r.Git("push", "-q", "origin", "HEAD:main")

	// A clean folder commits nothing but --tag still records the release at
	// HEAD, and --push delivers branch and tag with the configured identity.
	res := r.Command("commit", "--package", "core", "--tag", "--push", "--name", "release bot", "--email", "bot@dispat.test")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Equal(t, 1, r.TagCount("core@"))
	assert.Equal(t, "tag", r.Git("cat-file", "-t", "core@0.1.0"), "the release tag is annotated")
	assert.Equal(t, "release bot", r.Git("for-each-ref", "--format=%(taggername)", "refs/tags/core@0.1.0"))
	remoteTags := r.Git("ls-remote", "--tags", remote)
	assert.Contains(t, remoteTags, "core@0.1.0")

	// Re-running converges before any git work: the tag advanced the
	// baseline, so the package is no longer releasing and the command is a
	// clean no-op. (The W223 tag-exists skip belongs to the in-flow case,
	// where the outer run's plan predates the tag.)
	res = r.Command("commit", "--package", "core", "--tag")
	require.Equal(t, 0, res.Code)
	assert.Equal(t, 1, r.TagCount("core@"))
}

func TestStandaloneCommitExportsPinWhenDispatOutputSet(t *testing.T) {
	r := singlePackageRepo(t, echoBuild)
	r.Commit("feat(core): pinned feature")
	out := r.Path("outputs.txt")

	res := r.CommandEnv([]string{"DISPAT_OUTPUT=" + out}, "commit", "--package", "core")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	data, err := os.ReadFile(out)
	require.NoError(t, err)
	head := r.Git("rev-parse", "HEAD")
	assert.Contains(t, string(data), "PACKAGE_CORE="+head,
		"the commit pin reaches the outer run through DISPAT_OUTPUT")
}

func TestStandaloneCommitFolderNarrowing(t *testing.T) {
	// Invoked from inside a package folder the command narrows to it; from
	// the root it covers every releasing package in dependency order.
	r := linkedRepo(t, "core", "app", echoBuild)
	r.Commit("feat(core,app): both change")

	res := r.CommandAt("packages/app", "commit", "--tag")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Equal(t, 1, r.TagCount("app@"), "narrowed to the invoking folder's package")
	assert.Equal(t, 0, r.TagCount("core@"))

	res = r.Command("commit", "--tag")
	require.Equal(t, 0, res.Code)
	assert.Equal(t, 1, r.TagCount("core@"), "from the root, every releasing package")
}

func TestStandaloneAutoversionReconcilesAndSyncLocks(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Scripts["mark-lock"] = "echo locked >> ../../lock.log"
	cfg.Spaces["libs"] = models.SpaceConfig{Path: "packages", Flow: buildPublish(),
		AutoVersion: &models.AutoVersionConfig{SyncLock: []string{"mark-lock"}}}
	cfg.Dependencies = []models.DependencyConfig{{Consumer: "web", Provider: "core"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.WriteFile("packages/core/package.json", `{"name": "core", "version": "0.0.0"}`)
	r.SeedPackage("packages", "web")
	r.WriteFile("packages/web/package.json", `{"name": "web", "version": "0.0.0", "dependencies": {"core": "^0.0.0"}}`)
	r.Commit("feat(core,web): bootstrap")

	res := r.Command("autoversion")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	web, err := os.ReadFile(r.Path("packages", "web", "package.json"))
	require.NoError(t, err)
	assert.Contains(t, string(web), `"core": "^0.1.0"`, "the range reconciled to the planned version")
	assert.Contains(t, string(web), `"version": "0.1.0"`, "the own version advanced")
	lock, err := os.ReadFile(r.Path("lock.log"))
	require.NoError(t, err)
	assert.Equal(t, 2, strings.Count(string(lock), "locked"), "syncLock ran once per changed package")

	// Idempotence: nothing left to rewrite, no syncLock re-run.
	res = r.Command("autoversion")
	require.Equal(t, 0, res.Code)
	lock, err = os.ReadFile(r.Path("lock.log"))
	require.NoError(t, err)
	assert.Equal(t, 2, strings.Count(string(lock), "locked"), "unchanged manifests regenerate nothing")
	assert.Empty(t, r.TagList(), "autoversion releases nothing")
}

func TestStandaloneChangelogOverrideFlags(t *testing.T) {
	// Every config value the command consumes is also a flag overriding it
	// for the invocation.
	r := singlePackageRepo(t, echoBuild)
	r.Commit("feat(core): flagged feature")
	res := r.Command("changelog", "--package", "core", "--file", "HISTORY.md",
		"--file-title", "# History", "--date-format", "2006", "--release-name", "${DISPAT_TAG} shipped")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	data, err := os.ReadFile(r.Path("packages", "core", "HISTORY.md"))
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(string(data), "# History\n"))
	assert.Contains(t, string(data), "## core@0.1.0 (2026)", "the overridden date layout applies")
	assert.Contains(t, string(data), "### core@0.1.0 shipped", "the flagged release name is interpolated")
	assert.NoFileExists(t, r.Path("packages", "core", "CHANGELOG.md"))
}

func TestStandaloneAutoversionPolicyFlags(t *testing.T) {
	// Policy flags override the space's block for the invocation, and force
	// the defaults onto a space that has no block at all.
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1) // no autoVersion block on the space
	cfg.Dependencies = []models.DependencyConfig{{Consumer: "web", Provider: "core"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.WriteFile("packages/core/package.json", `{"name": "core", "version": "0.0.0"}`)
	r.SeedPackage("packages", "web")
	r.WriteFile("packages/web/package.json", `{"name": "web", "version": "0.0.0", "dependencies": {"core": "^0.0.0"}}`)
	r.Commit("feat(core,web): bootstrap")

	// Without flags, the no-block space reconciles nothing.
	res := r.Command("autoversion")
	require.Equal(t, 0, res.Code)
	web, err := os.ReadFile(r.Path("packages", "web", "package.json"))
	require.NoError(t, err)
	assert.Contains(t, string(web), `"core": "^0.0.0"`, "no policy, no rewrite")

	// A policy flag forces the defaults plus the override: tilde ranges,
	// no own-version write.
	res = r.Command("autoversion", "--range", "tilde", "--write-version=false", "--sync-lock=false")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	web, err = os.ReadFile(r.Path("packages", "web", "package.json"))
	require.NoError(t, err)
	assert.Contains(t, string(web), `"core": "~0.1.0"`, "the overridden range policy applies")
	assert.Contains(t, string(web), `"version": "0.0.0"`, "own-version write disabled by flag")

	// --manifests is the one policy flag with a closed set of values, so it is
	// also the one that can be misspelled. Both spellings are accepted and
	// anything else is a usage error, checked after the config loads.
	for _, value := range []string{"root", "all"} {
		res = r.Command("autoversion", "--manifests", value, "--sync-lock=false")
		assert.Equal(t, 0, res.Code, "--manifests %s: stderr:\n%s", value, res.Stderr)
	}
	res = r.Command("autoversion", "--manifests", "several")
	assert.Equal(t, 2, res.Code, "an unknown --manifests value is a usage error")
}

func TestStandaloneCommitMessageAndIncludeFlags(t *testing.T) {
	r := singlePackageRepo(t, echoBuild)
	r.WriteFile("shared.lock", "regenerated\n")
	r.Commit("feat(core): flagged commit")
	r.WriteFile("shared.lock", "regenerated again\n") // dirty include path
	r.WriteFile("packages/core/generated.txt", "artifact\n")

	res := r.Command("commit", "--package", "core", "--message-format", "release: {packages} at {tags}", "--include", "shared.lock")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Equal(t, "release: core at core@0.1.0", r.Git("log", "-1", "--format=%s"))
	shown := r.Git("show", "--stat", "--format=", "HEAD")
	assert.Contains(t, shown, "shared.lock", "the include path is staged alongside the folder")
	assert.Contains(t, shown, "generated.txt")
}

func TestStandaloneChangelogRespectsDisabledConfig(t *testing.T) {
	r := singlePackageRepo(t, echoBuild)
	cfg := libsConfig(echoBuild, 1)
	cfg.Changelog = &models.ChangelogConfig{Enabled: models.Bool(false)}
	r.WriteConfigModel(cfg)
	r.Commit("feat(core): quiet feature")
	res := r.Command("changelog", "--package", "core")
	require.Equal(t, 0, res.Code, "a disabled changelog is a clean no-op")
	assert.NoFileExists(t, r.Path("packages", "core", "CHANGELOG.md"))
}

func TestStandaloneAutoversionFlagOverridesExistingBlock(t *testing.T) {
	// A flag override starts from the space's own block, replacing only the
	// flagged field: the block's writeVersion stays in force while the range
	// policy changes for the invocation.
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Spaces["libs"] = models.SpaceConfig{Path: "packages", Flow: buildPublish(),
		AutoVersion: &models.AutoVersionConfig{}}
	cfg.Dependencies = []models.DependencyConfig{{Consumer: "web", Provider: "core"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.WriteFile("packages/core/package.json", `{"name": "core", "version": "0.0.0"}`)
	r.SeedPackage("packages", "web")
	r.WriteFile("packages/web/package.json", `{"name": "web", "version": "0.0.0", "dependencies": {"core": "^0.0.0"}}`)
	r.Commit("feat(core,web): bootstrap")

	res := r.Command("autoversion", "--range", "exact", "--sync-lock=false")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	web, err := os.ReadFile(r.Path("packages", "web", "package.json"))
	require.NoError(t, err)
	assert.Contains(t, string(web), `"core": "0.1.0"`, "exact range from the flag")
	assert.Contains(t, string(web), `"version": "0.1.0"`, "the block's writeVersion default still applies")
}

func TestStandaloneCommitPushWithoutRemoteFails(t *testing.T) {
	r := singlePackageRepo(t, echoBuild)
	r.Commit("feat(core): unpushable")
	res := r.Command("commit", "--package", "core", "--tag", "--push")
	assert.Equal(t, 1, res.Code, "pushing without a remote fails loudly")
	assert.Equal(t, 1, r.TagCount("core@"), "the local work before the push still happened")
}

// TestStandaloneGithubPublishesFromAStageScript: the github step command in
// an announce stage. The build stage exports DISPAT_EXPORT_GITHUB with the
// files to attach; the announce script runs `dispat github`, which reads
// that export out of its own environment (the stage handed it over), creates
// the release for the one package DISPAT_PACKAGE names, and attaches the
// files. This is the flow the command exists for: the release goes out from
// the flow's own stage rather than at the end of the run.
func TestStandaloneGithubPublishesFromAStageScript(t *testing.T) {
	type upload struct{ name, body string }
	type ghRelease struct {
		TagName string `json:"tag_name"`
		Body    string `json:"body"`
	}
	var mu sync.Mutex
	var uploads []upload
	var created [][]byte
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case githubTagProbe(w, req, nil):
		case req.Method == http.MethodGet:
			w.WriteHeader(http.StatusOK)
		case req.URL.Path == "/uploads":
			body, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			uploads = append(uploads, upload{name: req.URL.Query().Get("name"), body: string(body)})
			w.WriteHeader(http.StatusCreated)
		default:
			data, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			created = append(created, data)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"upload_url": "` + srv.URL + `/uploads{?name,label}"}`))
		}
	}))
	defer srv.Close()

	r := harness.New(t)
	cfg := harness.BaseFile(1)
	cfg.Scripts = map[string]string{
		"build": `echo binary-bytes > app.bin` +
			` && echo "DISPAT_EXPORT_GITHUB=$PWD/app.bin" >> "$DISPAT_OUTPUT"`,
		"publish":  "echo publishing",
		"announce": "dispat github",
	}
	cfg.Spaces = map[string]models.SpaceConfig{"libs": {Path: "packages", Flow: &models.SpaceFlowConfig{
		Build: []string{"build"}, Publish: []string{"publish"}, Announce: []string{"announce"}}}}
	// The run's own recorder stays off, so every release seen here came from
	// the step command inside the stage.
	cfg.GitHub = &models.GitHubConfig{
		Enabled: models.Bool(false), Owner: "acme", Repo: "mono",
		APIURL: srv.URL, TokenEnv: "DISPAT_IT_TOKEN",
	}
	cfg.Packages = map[string]models.PackageConfig{
		"core": {GitHub: &models.GitHubConfig{Enabled: models.Bool(true)}},
	}
	r.WriteConfigModel(cfg)
	t.Setenv("DISPAT_IT_TOKEN", "tkn")
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "utils")
	r.Commit("feat(core,utils): first release of both")

	r.ReleaseOK()

	releases := decodeAll[ghRelease](t, created)
	require.Len(t, releases, 1, "only the package whose policy is enabled is released")
	assert.Equal(t, "core@0.1.0", releases[0].TagName)
	assert.Contains(t, releases[0].Body, "### Features")
	assert.Contains(t, releases[0].Body, "first release of both")
	require.Len(t, uploads, 1, "the export named one file to attach")
	assert.Equal(t, "app.bin", uploads[0].name)
	assert.Equal(t, "binary-bytes\n", uploads[0].body)
}

// TestStandaloneGithubSelection: the github command selects packages exactly
// like the other step commands — an unknown term is an error, a package the
// plan is not releasing is a logged no-op — and without an opt-in it
// publishes nothing at all, the same rule the run's recorder follows.
func TestStandaloneGithubSelection(t *testing.T) {
	srv, bodies := githubFake(t)

	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.GitHub = &models.GitHubConfig{
		Enabled: models.Bool(true), Owner: "acme", Repo: "mono",
		APIURL: srv.URL, TokenEnv: "DISPAT_IT_TOKEN",
	}
	r.WriteConfigModel(cfg)
	t.Setenv("DISPAT_IT_TOKEN", "tkn")
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "quiet")
	r.Commit("feat(core): only core has work")

	// No export and no allPackages: nothing has opted in, so nothing is
	// published, and that is a success rather than an error.
	res := r.Command("github", "--package", "core")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Empty(t, bodies(), "without an opt-in the command publishes nothing")

	// The export in the caller's own environment is the opt-in.
	res = r.CommandEnv([]string{"DISPAT_EXPORT_GITHUB="}, "github", "--package", "core")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Len(t, bodies(), 1, "the exported opt-in publishes the release")

	// A package the plan is not releasing is a logged no-op, not a failure.
	res = r.CommandEnv([]string{"DISPAT_EXPORT_GITHUB="}, "github", "--package", "quiet")
	assert.Equal(t, 0, res.Code)
	assert.Len(t, bodies(), 1)
	assert.Contains(t, res.Stdout, "outside the window, nothing to do")

	// An unknown term is an error, and a positional argument a usage error.
	assert.Equal(t, 1, r.Command("github", "--package", "ghost").Code)
	assert.Equal(t, 2, r.Command("github", "core").Code)
}

// TestStandaloneGithubFailures: the github step's error paths. A prerelease
// held back by github.prerelease publishes nothing and says so; a package
// whose token cannot be resolved fails the command outright rather than
// silently publishing nothing, because a step the flow asked for must not
// pass quietly when it could not run; and an API that rejects the up-front
// verification fails before any release is created.
func TestStandaloneGithubFailures(t *testing.T) {
	t.Run("a prerelease held back", func(t *testing.T) {
		srv, bodies := githubFake(t)
		r := harness.New(t)
		cfg := libsConfig(echoBuild, 1)
		cfg.GitHub = &models.GitHubConfig{
			Enabled: models.Bool(true), AllPackages: models.Bool(true), Prerelease: models.Bool(false),
			Owner: "acme", Repo: "mono", APIURL: srv.URL, TokenEnv: "DISPAT_IT_TOKEN",
		}
		r.WriteConfigModel(cfg)
		t.Setenv("DISPAT_IT_TOKEN", "tkn")
		r.SeedPackage("packages", "core")
		r.Commit("feat(core)%beta: a beta")

		res := r.Command("github", "--package", "core")
		assert.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
		assert.Empty(t, bodies(), "github.prerelease false holds the beta back here too")
		assert.Contains(t, res.Stdout, "github.prerelease is false", "the skip states its reason")
	})

	t.Run("no token", func(t *testing.T) {
		srv, bodies := githubFake(t)
		r := harness.New(t)
		cfg := libsConfig(echoBuild, 1)
		cfg.GitHub = &models.GitHubConfig{
			Enabled: models.Bool(true), AllPackages: models.Bool(true),
			Owner: "acme", Repo: "mono", APIURL: srv.URL, TokenEnv: "DISPAT_IT_MISSING_TOKEN",
		}
		r.WriteConfigModel(cfg)
		r.SeedPackage("packages", "core")
		r.Commit("feat(core): first release")

		res := r.Command("github", "--package", "core")
		assert.Equal(t, 1, res.Code, "an unresolvable target fails the step it was asked for")
		assert.Contains(t, res.Stdout+res.Stderr, "DISPAT_IT_MISSING_TOKEN")
		assert.Empty(t, bodies())
	})

	t.Run("verification refused", func(t *testing.T) {
		var created int
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if req.Method == http.MethodPost {
				created++
			}
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message":"Bad credentials"}`))
		}))
		defer srv.Close()

		r := harness.New(t)
		cfg := libsConfig(echoBuild, 1)
		cfg.GitHub = &models.GitHubConfig{
			Enabled: models.Bool(true), AllPackages: models.Bool(true),
			Owner: "acme", Repo: "mono", APIURL: srv.URL, TokenEnv: "DISPAT_IT_TOKEN",
		}
		r.WriteConfigModel(cfg)
		t.Setenv("DISPAT_IT_TOKEN", "wrong")
		r.SeedPackage("packages", "core")
		r.Commit("feat(core): first release")

		res := r.Command("github", "--package", "core")
		assert.Equal(t, 1, res.Code)
		assert.Contains(t, res.Stdout+res.Stderr, "github verification failed")
		assert.Zero(t, created, "verification fails before any release is created")
	})

	t.Run("the api rejects the creation", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			switch {
			case githubTagProbe(w, req, nil):
			case req.Method == http.MethodGet:
				w.WriteHeader(http.StatusOK)
			default:
				w.WriteHeader(http.StatusUnprocessableEntity)
				_, _ = w.Write([]byte(`{"message":"Validation Failed"}`))
			}
		}))
		defer srv.Close()

		r := harness.New(t)
		cfg := libsConfig(echoBuild, 1)
		cfg.GitHub = &models.GitHubConfig{
			Enabled: models.Bool(true), AllPackages: models.Bool(true),
			Owner: "acme", Repo: "mono", APIURL: srv.URL, TokenEnv: "DISPAT_IT_TOKEN",
		}
		r.WriteConfigModel(cfg)
		t.Setenv("DISPAT_IT_TOKEN", "tkn")
		r.SeedPackage("packages", "core")
		r.Commit("feat(core): first release")

		res := r.Command("github", "--package", "core")
		assert.Equal(t, 1, res.Code)
		assert.Contains(t, res.Stdout+res.Stderr, "github release failed")
	})
}

// TestStandaloneStepsTakeTheWindowFlags: the step commands cover packages the
// way `dispat run` does, so --since, --consumers and --on-error mean here what
// they mean there.
//
// The pitfall this pins is the one every flow meets sooner or later: a step
// command plans afresh every time, so once `dispat commit --tag` has tagged a
// package the recomputed plan no longer releases it, and the next step over
// the same package covers nothing at all. --since all is what puts it back on
// the table.
func TestStandaloneStepsTakeTheWindowFlags(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 2)
	cfg.Dependencies = []models.DependencyConfig{{Consumer: "web", Provider: "core"}}
	r.WriteConfigModel(cfg)
	r.Commit("chore: set the repository up")
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "web")
	r.Commit("feat(core,web): bootstrap")

	// --since picks the window: what the last commit addressed.
	res := r.Command("changelog", "--since", "HEAD~1")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.FileExists(t, r.Path("packages", "core", "CHANGELOG.md"))
	assert.FileExists(t, r.Path("packages", "web", "CHANGELOG.md"))

	// --consumers reaches web from core.
	r.Remove("packages/web/CHANGELOG.md")
	res = r.Command("changelog", "--package", "core", "--consumers")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.FileExists(t, r.Path("packages", "web", "CHANGELOG.md"), "the consumer was pulled in")

	// --on-error parses and is validated on every sweeping command.
	assert.Equal(t, 0, r.Command("changelog", "--on-error", "continue").Code)
	assert.Equal(t, 2, r.Command("changelog", "--on-error", "explode").Code)

	// The pitfall: commit --tag, and the package stops being on the window.
	res = r.Command("commit", "--package", "core", "--tag")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	require.True(t, r.HasTag("core@0.1.0"), "tags: %v", r.TagList())

	res = r.Command("autoversion", "--package", "core")
	require.Equal(t, 0, res.Code, "a package with nothing pending is a no-op, not a failure")
	assert.Contains(t, res.Stdout, "outside the window, nothing to do")

	res = r.Command("autoversion", "--package", "core", "--since", "all")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.NotContains(t, res.Stdout, "outside the window",
		"--since all puts the released package back on the table")
}
