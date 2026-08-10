package integration

// Goal 15: the standalone step commands. `dispat changelog`, `dispat commit`
// and `dispat autoversion` expose the pipeline's native steps to custom
// flows, and the release stage skips work they already did: a pre-written
// changelog entry is a W222 skip, a pre-created tag at the release commit a
// W223 skip. The central claim is the ordering fix the commands exist for:
// a changelog written by a stage script before the per-package commit lands
// inside the tagged tree.

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

func TestStandaloneChangelogWritesAndIsIdempotent(t *testing.T) {
	r := singlePackageRepo(t, echoBuild)
	r.Commit("feat(core): first feature")

	res := r.Command("changelog", "core")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	log := r.Path("packages", "core", "CHANGELOG.md")
	data, err := os.ReadFile(log)
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(string(data), "## core@0.1.0 ("))
	assert.Contains(t, string(data), "first feature")
	assert.Empty(t, r.TagList(), "the changelog command releases nothing")

	// A second invocation is a W222 skip and changes nothing.
	res = r.Command("changelog", "core")
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
	assert.Equal(t, 1, r.Command("changelog", "ghost").Code)
	assert.Equal(t, 0, r.Command("changelog", "core").Code, "converged package: a logged no-op")
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
	res := r.Command("commit", "core", "--tag", "--push", "--name", "release bot", "--email", "bot@dispat.test")
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
	res = r.Command("commit", "core", "--tag")
	require.Equal(t, 0, res.Code)
	assert.Equal(t, 1, r.TagCount("core@"))
}

func TestStandaloneCommitExportsPinWhenDispatOutputSet(t *testing.T) {
	r := singlePackageRepo(t, echoBuild)
	r.Commit("feat(core): pinned feature")
	out := r.Path("outputs.txt")

	res := r.CommandEnv([]string{"DISPAT_OUTPUT=" + out}, "commit", "core")
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
	res := r.Command("changelog", "core", "--file", "HISTORY.md", "--title", "# History", "--date-format", "2006")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	data, err := os.ReadFile(r.Path("packages", "core", "HISTORY.md"))
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(string(data), "# History\n"))
	assert.Contains(t, string(data), "## core@0.1.0 (2026)", "the overridden date layout applies")
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

	res := r.Command("commit", "core", "--message-format", "release: {packages} at {tags}", "--include", "shared.lock")
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
	res := r.Command("changelog", "core")
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
	res := r.Command("commit", "core", "--tag", "--push")
	assert.Equal(t, 1, res.Code, "pushing without a remote fails loudly")
	assert.Equal(t, 1, r.TagCount("core@"), "the local work before the push still happened")
}
