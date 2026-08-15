package release

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/ccme"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// The replacing strategy runs against real files too: these tests lay out a
// package, wire the rules into the space and assert on the bytes left behind.

// avReplaceSpace is a space with the parsing strategy off and the given rules
// on, which is how a Gradle or Makefile project reconciles.
func avReplaceSpace(rules ...model.ReplaceRule) *model.Space {
	return avSpace(&model.AutoVersion{
		Manifests: model.ScopeNone,
		Kinds:     allKinds(),
		Replace:   rules,
	})
}

func TestReplaceRuleRewritesProviderCoordinates(t *testing.T) {
	// A Gradle module: nothing here parses as a manifest, so the parsing
	// strategy has no way in and the coordinate is reconciled literally.
	root := t.TempDir()
	space := avReplaceSpace(model.ReplaceRule{
		Files: []string{"*.gradle"},
		Find:  "com.acme:{provider}:{providerPrevious}",
		Write: "com.acme:{provider}:{providerVersion}",
	})
	seedFile(t, root, "core/build.gradle", "// core\n")
	seedFile(t, root, "web/build.gradle",
		"dependencies {\n  implementation 'com.acme:core:1.0.0'\n  testImplementation 'com.acme:core:1.0.0'\n}\n")
	p := avPlan(root, space, "core", "web")
	p.Providers["web"] = []string{"core"}

	res := newExecutor(execSpec{Runner: &fakeRunner{}, Build: 2, Publish: 2}).Run(context.Background(), p)
	require.Equal(t, StatusPublished, res["web"].Status, "%v", res["web"].Err)
	web := fileText(t, root, "web/build.gradle")
	assert.Equal(t,
		"dependencies {\n  implementation 'com.acme:core:1.0.1'\n  testImplementation 'com.acme:core:1.0.1'\n}\n",
		web, "every occurrence, and nothing else in the file")
}

func TestReplaceRuleWritesTheOwnVersion(t *testing.T) {
	// A rule mentioning no provider is rendered once, for the package itself.
	root := t.TempDir()
	space := avReplaceSpace(model.ReplaceRule{
		Files: []string{"*.md", "Dockerfile"},
		Find:  "{name}:{previous}",
		Write: "{name}:{version}",
	})
	seedFile(t, root, "web/README.md", "Install web:1.0.0 today.\n")
	seedFile(t, root, "web/Dockerfile", "FROM registry/web:1.0.0\n")
	seedFile(t, root, "web/notes.txt", "web:1.0.0 is not selected\n")
	p := avPlan(root, space, "web")

	res := newExecutor(execSpec{Runner: &fakeRunner{}, Build: 1, Publish: 1}).Run(context.Background(), p)
	require.Equal(t, StatusPublished, res["web"].Status, "%v", res["web"].Err)
	assert.Equal(t, "Install web:1.0.1 today.\n", fileText(t, root, "web/README.md"))
	assert.Equal(t, "FROM registry/web:1.0.1\n", fileText(t, root, "web/Dockerfile"))
	assert.Equal(t, "web:1.0.0 is not selected\n", fileText(t, root, "web/notes.txt"),
		"a file no glob selects is never opened")
}

func TestReplaceRuleGlobsCrossFolders(t *testing.T) {
	// "*" matches any run of characters, separators included, the same
	// semantics autoVersion.match already documents. The folders a workspace
	// walk never enters stay out of reach whatever the glob says.
	root := t.TempDir()
	space := avReplaceSpace(model.ReplaceRule{
		Files: []string{"*.txt"},
		Find:  "{previous}",
		Write: "{version}",
	})
	seedFile(t, root, "web/top.txt", "1.0.0\n")
	seedFile(t, root, "web/deep/nested/inner.txt", "1.0.0\n")
	seedFile(t, root, "web/node_modules/dep/vendored.txt", "1.0.0\n")
	seedFile(t, root, "web/build/generated.txt", "1.0.0\n")
	seedFile(t, root, "web/.cache/hidden.txt", "1.0.0\n")
	p := avPlan(root, space, "web")

	res := newExecutor(execSpec{Runner: &fakeRunner{}, Build: 1, Publish: 1}).Run(context.Background(), p)
	require.Equal(t, StatusPublished, res["web"].Status, "%v", res["web"].Err)
	assert.Equal(t, "1.0.1\n", fileText(t, root, "web/top.txt"))
	assert.Equal(t, "1.0.1\n", fileText(t, root, "web/deep/nested/inner.txt"))
	for _, rel := range []string{
		"web/node_modules/dep/vendored.txt", "web/build/generated.txt", "web/.cache/hidden.txt",
	} {
		assert.Equal(t, "1.0.0\n", fileText(t, root, rel), "%s must never be entered", rel)
	}
}

func TestReplaceRuleOnlyFilterNarrowsProviders(t *testing.T) {
	root := t.TempDir()
	space := avReplaceSpace(model.ReplaceRule{
		Files: []string{"deps.txt"},
		Find:  "{provider}@{providerPrevious}",
		Write: "{provider}@{providerVersion}",
	})
	space.AutoVersion.Only = map[string]bool{"core": true}
	seedFile(t, root, "core/deps.txt", "")
	seedFile(t, root, "tools/deps.txt", "")
	seedFile(t, root, "web/deps.txt", "core@1.0.0\ntools@1.0.0\n")
	p := avPlan(root, space, "core", "tools", "web")
	p.Providers["web"] = []string{"core", "tools"}

	res := newExecutor(execSpec{Runner: &fakeRunner{}, Build: 3, Publish: 3}).Run(context.Background(), p)
	require.Equal(t, StatusPublished, res["web"].Status, "%v", res["web"].Err)
	assert.Equal(t, "core@1.0.1\ntools@1.0.0\n", fileText(t, root, "web/deps.txt"),
		"only core is in the policy's only list")
}

func TestReplaceRuleFailedProviderFallsBackToBaseline(t *testing.T) {
	// The same rule the parsing strategy follows: a provider whose build
	// failed never shipped its planned version, so the file must name the
	// baseline. The find text is the baseline too, so nothing changes at all.
	root := t.TempDir()
	space := avReplaceSpace(model.ReplaceRule{
		Files: []string{"deps.txt"},
		Find:  "{provider}@{providerPrevious}",
		Write: "{provider}@{providerVersion}",
	})
	seedFile(t, root, "core/deps.txt", "")
	seedFile(t, root, "web/deps.txt", "core@1.0.0\n")
	p := avPlan(root, space, "core", "web")
	p.Providers["web"] = []string{"core"}

	r := &fakeRunner{fail: map[string]bool{"build " + filepath.Join(root, "core"): true}}
	res := newExecutor(execSpec{Runner: r, Build: 1, Publish: 1}).Run(context.Background(), p)
	require.Equal(t, StatusFailed, res["core"].Status)
	require.Equal(t, StatusPublished, res["web"].Status, "%v", res["web"].Err)
	assert.Equal(t, "core@1.0.0\n", fileText(t, root, "web/deps.txt"),
		"failed provider: the baseline, not the planned version")
}

func TestReplaceRuleMatchedNothingWarnsW222(t *testing.T) {
	root := t.TempDir()
	space := avReplaceSpace(model.ReplaceRule{
		Files: []string{"*.txt"},
		Find:  "typo-{previous}",
		Write: "typo-{version}",
	})
	seedFile(t, root, "web/notes.txt", "nothing the rule looks for\n")
	p := avPlan(root, space, "web")

	var logBuf bytes.Buffer
	e := newExecutor(execSpec{Runner: &fakeRunner{}, Build: 1, Publish: 1})
	e.Log = syncedLog(&logBuf)
	res := e.Run(context.Background(), p)
	require.Equal(t, StatusPublished, res["web"].Status, "%v", res["web"].Err)
	assert.Contains(t, logBuf.String(), plan.CodeReplaceRuleMatchedNothing)
	assert.Contains(t, logBuf.String(), "typo-1.0.0")
}

func TestReplaceRuleIsQuietOnAnAlreadyReconciledFile(t *testing.T) {
	// Re-running is safe and must stay silent: the text the rule looks for is
	// gone after the first pass, and only the probe can tell that apart from
	// a pattern that never matched.
	root := t.TempDir()
	space := avReplaceSpace(model.ReplaceRule{
		Files: []string{"*.txt"},
		Find:  "web@{previous}",
		Write: "web@{version}",
	})
	seedFile(t, root, "web/notes.txt", "web@1.0.1 already\n")
	p := avPlan(root, space, "web")

	var logBuf bytes.Buffer
	e := newExecutor(execSpec{Runner: &fakeRunner{}, Build: 1, Publish: 1})
	e.Log = syncedLog(&logBuf)
	res := e.Run(context.Background(), p)
	require.Equal(t, StatusPublished, res["web"].Status, "%v", res["web"].Err)
	assert.Equal(t, "web@1.0.1 already\n", fileText(t, root, "web/notes.txt"))
	assert.NotContains(t, logBuf.String(), plan.CodeReplaceRuleMatchedNothing,
		"an already-reconciled file is not a stale rule")
}

func TestReplaceRuleWarnsW203OverAPrereleaseProvider(t *testing.T) {
	// web is stable, core is releasing on a prerelease train: the file now
	// names a moving version, which is the same glance-worthy state the
	// parsing strategy reports.
	root := t.TempDir()
	space := avReplaceSpace(model.ReplaceRule{
		Files: []string{"deps.txt"},
		Find:  "{provider}@{providerPrevious}",
		Write: "{provider}@{providerVersion}",
	})
	seedFile(t, root, "core/deps.txt", "")
	seedFile(t, root, "web/deps.txt", "core@1.0.0\n")
	p := avPlan(root, space, "core", "web")
	p.Providers["web"] = []string{"core"}
	core := p.Releases["core"]
	core.Channel = "beta"
	core.Next = ccme.Version{Major: 1, Patch: 1, Prerelease: []string{"beta", "1"}}

	var logBuf bytes.Buffer
	e := newExecutor(execSpec{Runner: &fakeRunner{}, Build: 2, Publish: 2})
	e.Log = syncedLog(&logBuf)
	res := e.Run(context.Background(), p)
	require.Equal(t, StatusPublished, res["web"].Status, "%v", res["web"].Err)
	assert.Equal(t, "core@1.0.1-beta.1\n", fileText(t, root, "web/deps.txt"))
	logs := logBuf.String()
	assert.Contains(t, logs, plan.CodeStableOverPrerelease, "W203")
	assert.Equal(t, 1, strings.Count(logs, plan.CodeStableOverPrerelease), "said once per provider")
}

func TestReplaceRuleWarnsW197ForAProviderOutsideTheRun(t *testing.T) {
	// core is not releasing, so a rule naming {providerPrevious} would be a
	// no-op; a rule finding literal text still moves the file, and the
	// catch-up is worth saying out loud because nothing in this run explains
	// where the new version came from.
	root := t.TempDir()
	space := avReplaceSpace(model.ReplaceRule{
		Files: []string{"deps.txt"},
		Find:  "core@0.9.0",
		Write: "core@{providerVersion}",
	})
	seedFile(t, root, "web/deps.txt", "core@0.9.0\n")
	p := avPlan(root, space, "web")
	p.Releases["core"] = &plan.Release{
		Pkg:         &model.Package{Name: "core", Dir: filepath.Join(root, "core"), Space: space},
		Current:     ccme.Version{Major: 1},
		Baseline:    ccme.Version{Major: 1},
		HasBaseline: true,
	}
	p.Providers["web"] = []string{"core"}

	var logBuf bytes.Buffer
	e := newExecutor(execSpec{Runner: &fakeRunner{}, Build: 1, Publish: 1})
	e.Log = syncedLog(&logBuf)
	res := e.Run(context.Background(), p)
	require.Equal(t, StatusPublished, res["web"].Status, "%v", res["web"].Err)
	assert.Equal(t, "core@1.0.0\n", fileText(t, root, "web/deps.txt"))
	logs := logBuf.String()
	assert.Contains(t, logs, plan.CodeRangeCatchUp, "W197: a provider released outside this run")
	assert.Equal(t, 1, strings.Count(logs, plan.CodeRangeCatchUp), "said once per provider")
}

func TestReplaceRuleWritesAFileOnceForSeveralRules(t *testing.T) {
	// Two rules selecting one file must not read or write it twice: the walk
	// gathers the rules per file and one replacement call carries them all.
	root := t.TempDir()
	space := avReplaceSpace(
		model.ReplaceRule{Files: []string{"app.env"}, Find: "SELF={previous}", Write: "SELF={version}"},
		model.ReplaceRule{Files: []string{"*.env"}, Find: "DEP={providerPrevious}", Write: "DEP={providerVersion}"},
	)
	seedFile(t, root, "core/app.env", "")
	seedFile(t, root, "web/app.env", "SELF=1.0.0\nDEP=1.0.0\n")
	p := avPlan(root, space, "core", "web")
	p.Providers["web"] = []string{"core"}

	res := newExecutor(execSpec{Runner: &fakeRunner{}, Build: 2, Publish: 2}).Run(context.Background(), p)
	require.Equal(t, StatusPublished, res["web"].Status, "%v", res["web"].Err)
	assert.Equal(t, "SELF=1.0.1\nDEP=1.0.1\n", fileText(t, root, "web/app.env"))
}

func TestReplaceRuleSkipsBinaryAndOversizedFiles(t *testing.T) {
	// A glob reaching a PNG is ordinary; failing the release over it is not.
	root := t.TempDir()
	space := avReplaceSpace(model.ReplaceRule{
		Files: []string{"*"},
		Find:  "{previous}",
		Write: "{version}",
	})
	seedFile(t, root, "web/notes.txt", "1.0.0\n")
	seedFile(t, root, "web/logo.png", "\x89PNG\x00\x00 1.0.0")
	p := avPlan(root, space, "web")

	res := newExecutor(execSpec{Runner: &fakeRunner{}, Build: 1, Publish: 1}).Run(context.Background(), p)
	require.Equal(t, StatusPublished, res["web"].Status, "%v", res["web"].Err)
	assert.Equal(t, "1.0.1\n", fileText(t, root, "web/notes.txt"))
	assert.Equal(t, "\x89PNG\x00\x00 1.0.0", fileText(t, root, "web/logo.png"), "a binary file is left alone")
}

func TestReplaceRunsAfterTheManifestStrategy(t *testing.T) {
	// A package may use both: the manifests are reconciled first, then the
	// literal rules run over whatever else the release has to keep in step.
	root := t.TempDir()
	space := avSpace(&model.AutoVersion{
		Manifests:    model.ScopeRoot,
		Kinds:        allKinds(),
		WriteVersion: true,
		Replace: []model.ReplaceRule{{
			Files: []string{"README.md"},
			Find:  "@acme/web@{previous}",
			Write: "@acme/web@{version}",
		}},
	})
	seedFile(t, root, "core/package.json", `{"name": "@acme/core", "version": "1.0.0"}`)
	seedFile(t, root, "web/package.json",
		`{"name": "@acme/web", "version": "1.0.0", "dependencies": {"@acme/core": "workspace:*"}}`)
	seedFile(t, root, "web/README.md", "npm i @acme/web@1.0.0\n")
	p := avPlan(root, space, "core", "web")

	res := newExecutor(execSpec{Runner: &fakeRunner{}, Build: 2, Publish: 2}).Run(context.Background(), p)
	require.Equal(t, StatusPublished, res["web"].Status, "%v", res["web"].Err)
	assert.Contains(t, fileText(t, root, "web/package.json"), `"@acme/core": "^1.0.1"`)
	assert.Contains(t, fileText(t, root, "web/package.json"), `"version": "1.0.1"`)
	assert.Equal(t, "npm i @acme/web@1.0.1\n", fileText(t, root, "web/README.md"))
}

func TestReplaceRuleFailureFailsTheVersionStage(t *testing.T) {
	// The folder is read-only, so the replacement cannot write its temp
	// file: the native step's error fails the package at the version stage.
	root := t.TempDir()
	space := avReplaceSpace(model.ReplaceRule{
		Files: []string{"notes.txt"},
		Find:  "{previous}",
		Write: "{version}",
	})
	seedFile(t, root, "a/notes.txt", "1.0.0\n")
	dir := filepath.Join(root, "a")
	require.NoError(t, os.Chmod(dir, 0o555))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	res := newExecutor(execSpec{Runner: &fakeRunner{}, Build: 1, Publish: 1}).
		Run(context.Background(), avPlan(root, space, "a"))
	require.Equal(t, StatusFailed, res["a"].Status)
	assert.Equal(t, "version", res["a"].FailedStage, "a replace rule fails the version stage")
	require.Error(t, res["a"].Err)
}

func TestReplaceRulePropagatesCancellation(t *testing.T) {
	root := t.TempDir()
	space := avReplaceSpace(model.ReplaceRule{
		Files: []string{"notes.txt"},
		Find:  "{previous}",
		Write: "{version}",
	})
	seedFile(t, root, "web/notes.txt", "1.0.0\n")
	p := avPlan(root, space, "web")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	changed, err := autoVersionPackagesCtx(ctx, p, []string{"web"}, nil, syncedLog(&bytes.Buffer{}))
	require.Error(t, err)
	assert.Empty(t, changed)
	assert.Equal(t, "1.0.0\n", fileText(t, root, "web/notes.txt"),
		"nothing may be written after the cancellation")
}

func TestReplaceRuleStepsOverAnUnreadableFolder(t *testing.T) {
	// A folder the walk cannot enter is reported and stepped over, the way a
	// scan treats a sub-tree it cannot read: the files it *can* reach are
	// still reconciled, and the release is not failed over a folder no rule
	// was ever going to reach.
	if os.Getuid() == 0 {
		t.Skip("permission checks are meaningless as root")
	}
	root := t.TempDir()
	space := avReplaceSpace(model.ReplaceRule{
		Files: []string{"*.txt"},
		Find:  "{previous}",
		Write: "{version}",
	})
	seedFile(t, root, "web/reachable.txt", "1.0.0\n")
	seedFile(t, root, "web/locked/inner.txt", "1.0.0\n")
	locked := filepath.Join(root, "web", "locked")
	require.NoError(t, os.Chmod(locked, 0o000))
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	p := avPlan(root, space, "web")

	var logBuf bytes.Buffer
	e := newExecutor(execSpec{Runner: &fakeRunner{}, Build: 1, Publish: 1})
	e.Log = syncedLog(&logBuf)
	res := e.Run(context.Background(), p)
	require.Equal(t, StatusPublished, res["web"].Status, "%v", res["web"].Err)
	assert.Equal(t, "1.0.1\n", fileText(t, root, "web/reachable.txt"))
	assert.Contains(t, logBuf.String(), "folder skipped")
}

func TestReplaceRuleFailsOnAnUnreadablePackageFolder(t *testing.T) {
	// The package's own folder is different: if that cannot be walked, the
	// rules cannot be said to have run at all.
	if os.Getuid() == 0 {
		t.Skip("permission checks are meaningless as root")
	}
	root := t.TempDir()
	space := avReplaceSpace(model.ReplaceRule{
		Files: []string{"*.txt"},
		Find:  "{previous}",
		Write: "{version}",
	})
	seedFile(t, root, "a/notes.txt", "1.0.0\n")
	dir := filepath.Join(root, "a")
	require.NoError(t, os.Chmod(dir, 0o000))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	res := newExecutor(execSpec{Runner: &fakeRunner{}, Build: 1, Publish: 1}).
		Run(context.Background(), avPlan(root, space, "a"))
	require.Equal(t, StatusFailed, res["a"].Status)
	assert.Equal(t, "version", res["a"].FailedStage)
}

func TestReplaceRuleWithNoProvidersSelectsNothing(t *testing.T) {
	// A provider-scoped rule in a package with no providers expands into
	// nothing, so its globs must select nothing either. The file it would
	// have reached is left exactly as it was, and the sibling rule that did
	// expand still does its job.
	root := t.TempDir()
	space := avReplaceSpace(
		model.ReplaceRule{Files: []string{"deps.txt"}, Find: "{provider}@{providerPrevious}", Write: "{provider}@{providerVersion}"},
		model.ReplaceRule{Files: []string{"own.txt"}, Find: "web@{previous}", Write: "web@{version}"},
	)
	seedFile(t, root, "web/deps.txt", "core@1.0.0\n")
	seedFile(t, root, "web/own.txt", "web@1.0.0\n")
	p := avPlan(root, space, "web") // no p.Providers entry at all

	var logBuf bytes.Buffer
	e := newExecutor(execSpec{Runner: &fakeRunner{}, Build: 1, Publish: 1})
	e.Log = syncedLog(&logBuf)
	res := e.Run(context.Background(), p)
	require.Equal(t, StatusPublished, res["web"].Status, "%v", res["web"].Err)
	assert.Equal(t, "core@1.0.0\n", fileText(t, root, "web/deps.txt"),
		"a rule that expanded into nothing must not reach its files")
	assert.Equal(t, "web@1.0.1\n", fileText(t, root, "web/own.txt"))
	assert.NotContains(t, logBuf.String(), plan.CodeReplaceRuleMatchedNothing,
		"a rule with no providers to expand for is not a stale rule")
}

func TestReplaceRuleSelectingNoFileIsNotStale(t *testing.T) {
	// One space-wide rule over "README.md" is the ordinary way to keep every
	// README that exists in step. A package without one has nothing for the
	// rule to say, so it must stay quiet: warning per package would drown the
	// warning that matters. A rule that *did* reach a file and found nothing
	// still warns.
	root := t.TempDir()
	space := avReplaceSpace(
		model.ReplaceRule{Files: []string{"README.md"}, Find: "{name}@{previous}", Write: "{name}@{version}"},
		model.ReplaceRule{Files: []string{"notes.txt"}, Find: "typo-{previous}", Write: "typo-{version}"},
	)
	seedFile(t, root, "web/notes.txt", "nothing the second rule looks for\n") // no README.md
	p := avPlan(root, space, "web")

	var logBuf bytes.Buffer
	e := newExecutor(execSpec{Runner: &fakeRunner{}, Build: 1, Publish: 1})
	e.Log = syncedLog(&logBuf)
	res := e.Run(context.Background(), p)
	require.Equal(t, StatusPublished, res["web"].Status, "%v", res["web"].Err)

	logs := logBuf.String()
	assert.Equal(t, 1, strings.Count(logs, plan.CodeReplaceRuleMatchedNothing),
		"only the rule that reached a file and found nothing is reported")
	assert.Contains(t, logs, "typo-1.0.0")
	assert.NotContains(t, logs, "web@1.0.0", "the rule that reached no file says nothing")
}
