package integration

// Area 20: the autoreplacer command through the compiled binary. `dispat
// autoreplacer` is `dispat replacer` pointed at a selection instead of a list
// of files, and what only a real run can show about it lives here: that the
// globs select inside each covered package, that a {provider} pattern fans out
// across the workspace dependencies the binary just resolved, that --consumers
// reaches the packages the window left out, that a nested package's files are
// left to their owner, and that every outcome reaches the process exit code.

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// arpRepo is the fixture: core and web, web depending on core, each with a
// package.json so the manifest-name index has something to resolve, and a
// build.gradle in web carrying a coordinate no manifest writer can reach. That
// coordinate is the whole reason this command exists.
func arpRepo(t *testing.T) *harness.Repo {
	t.Helper()
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 2)
	cfg.Dependencies = []models.DependencyConfig{{Consumer: "web", Provider: "core"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "web")
	r.WriteFile("packages/core/package.json", arCoreJSON)
	r.WriteFile("packages/web/package.json", arWebJSON)
	r.WriteFile("packages/web/build.gradle",
		"dependencies {\n  implementation 'com.acme:core:0.0.0'\n}\n")
	r.WriteFile("packages/web/README.md", "web 0.0.0 uses @acme/core 0.0.0\n")
	r.Commit("feat(core,web): bootstrap")
	return r
}

func arpRead(t *testing.T, r *harness.Repo, parts ...string) string {
	t.Helper()
	body, err := os.ReadFile(r.Path(parts...))
	require.NoError(t, err)
	return string(body)
}

// TestAutoReplacerFansOutAcrossWorkspaceProviders: one pattern, rendered once
// per workspace package the covered package declares. Nothing names core on the
// command line: the fan-out found it in web's manifest.
func TestAutoReplacerFansOutAcrossWorkspaceProviders(t *testing.T) {
	r := arpRepo(t)

	res := r.Command("autoreplacer", "--since", "all", "--files", "*.gradle",
		"--sub", "com.acme:{provider}:{providerPrevious}=>com.acme:{provider}:{providerVersion}")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, arpRead(t, r, "packages", "web", "build.gradle"),
		"'com.acme:core:0.1.0'", "the coordinate followed the provider")
}

// TestAutoReplacerPackageScopedPatternRunsOnce: a --sub naming no provider is
// about the covered package itself, so it is rendered once and --only-updated
// leaves it alone.
func TestAutoReplacerPackageScopedPatternRunsOnce(t *testing.T) {
	r := arpRepo(t)

	res := r.Command("autoreplacer", "--since", "all", "--files", "README.md",
		"--sub", "{name} {previous}=>{name} {version}")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, arpRead(t, r, "packages", "web", "README.md"), "web 0.1.0",
		"the package's own version was written")
}

// TestAutoReplacerGlobsSelectWithinThePackage: a glob reaches only what it
// names, and only inside the package folder the sweep handed over.
func TestAutoReplacerGlobsSelectWithinThePackage(t *testing.T) {
	r := arpRepo(t)
	before := arpRead(t, r, "packages", "web", "README.md")

	res := r.Command("autoreplacer", "--since", "all", "--files", "*.gradle",
		"--sub", "0.0.0=>9.9.9")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, arpRead(t, r, "packages", "web", "build.gradle"), "9.9.9")
	assert.Equal(t, before, arpRead(t, r, "packages", "web", "README.md"),
		"a file no glob selected is untouched")
}

// TestAutoReplacerOnlyUpdatedNarrowsTheFanOut: with the providers of this run
// alone, a provider released earlier expands into nothing.
func TestAutoReplacerOnlyUpdatedNarrowsTheFanOut(t *testing.T) {
	r := arpRepo(t)
	// Release everything, then change only web: core is no longer updating.
	r.Command("commit", "--tag")
	r.WriteFile("packages/web/README.md", "web 0.1.0 uses @acme/core 0.1.0\n")
	r.Commit("feat(web): only web this time")

	res := r.Command("autoreplacer", "--only-updated", "--files", "*.gradle",
		"--sub", "com.acme:{provider}:{providerPrevious}=>com.acme:{provider}:{providerVersion}")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, arpRead(t, r, "packages", "web", "build.gradle"), "0.0.0",
		"core is not in this run, so its coordinate is left alone")
}

// TestAutoReplacerConsumersReachesThePackagesTheWindowLeftOut: the package
// holding a stale coordinate is usually the one nothing changed in, so it is
// outside the window by definition. --consumers is what reaches it.
func TestAutoReplacerConsumersReachesThePackagesTheWindowLeftOut(t *testing.T) {
	r := arpRepo(t)
	sub := "com.acme:{provider}:{providerPrevious}=>com.acme:{provider}:{providerVersion}"

	// Bring the coordinate up to date, then release, so the next run has a
	// genuine one-version gap to close.
	require.Equal(t, 0, r.Command("autoreplacer", "--since", "all", "--files", "*.gradle", "--sub", sub).Code)
	assert.Contains(t, arpRead(t, r, "packages", "web", "build.gradle"), "com.acme:core:0.1.0")
	r.Command("commit", "--tag")
	r.WriteFile("packages/core/extra.txt", "core moved\n")
	r.Commit("feat(core): only core changed")

	// Without --consumers the window covers core alone, and core has no gradle
	// file, so web's coordinate stays a version behind.
	res := r.Command("autoreplacer", "--files", "*.gradle", "--sub", sub)
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, arpRead(t, r, "packages", "web", "build.gradle"), "com.acme:core:0.1.0",
		"web is outside the window")

	res = r.Command("autoreplacer", "--consumers", "--files", "*.gradle", "--sub", sub)
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, arpRead(t, r, "packages", "web", "build.gradle"), "com.acme:core:0.2.0",
		"--consumers pulled web in and its coordinate followed")
}

// TestAutoReplacerConvergesUnderStrict: the probe is what tells "already
// reconciled" apart from "never matched". Without it every second run of a
// {previous}=>{version} pattern would report the pattern stale.
func TestAutoReplacerConvergesUnderStrict(t *testing.T) {
	r := arpRepo(t)
	args := []string{"autoreplacer", "--since", "all", "--strict", "--files", "README.md",
		"--sub", "{name} {previous}=>{name} {version}"}

	res := r.Command(args...)
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	first := arpRead(t, r, "packages", "web", "README.md")

	res = r.Command(args...)
	assert.Equal(t, 0, res.Code, "a converged re-run is clean, not stale; stdout:\n%s", res.Stdout)
	assert.Equal(t, first, arpRead(t, r, "packages", "web", "README.md"),
		"and it changed nothing")
}

// TestAutoReplacerOutcomesReachTheExitCode: every way this command can
// refuse, over the process boundary.
func TestAutoReplacerOutcomesReachTheExitCode(t *testing.T) {
	r := arpRepo(t)

	res := r.Command("autoreplacer", "--since", "all", "--strict", "--files", "*.gradle",
		"--sub", "nowhere-at-all=>x")
	assert.Equal(t, 1, res.Code, "a pattern no package carries is stale")
	assert.Contains(t, res.Stdout, "matched nothing")

	res = r.Command("autoreplacer", "--since", "all", "--files", "*.gradle",
		"--sub", "nowhere-at-all=>x")
	assert.Equal(t, 0, res.Code, "without --strict the same run is tolerated")

	assert.Equal(t, 2, r.Command("autoreplacer").Code, "nothing to write")
	assert.Equal(t, 2, r.Command("autoreplacer", "--sub", "a=>b").Code,
		"a sub with no files to look in selects nothing")
	assert.Equal(t, 2, r.Command("autoreplacer", "--files", "*.md").Code,
		"files with nothing to write")
	assert.Equal(t, 2, r.Command("autoreplacer", "--files", "*.md", "--sub", "no-separator").Code,
		"a malformed sub spec")
	assert.Equal(t, 2, r.Command("autoreplacer", "extra", "--files", "*.md", "--sub", "a=>b").Code,
		"packages are flags, not positional arguments")
}

// TestAutoReplacerLeavesANestedPackageToItsOwner: a package folder holding
// another package's folder must not rewrite that package's files. Its owner's
// own turn does, and without the guard the two would write one file from two
// goroutines.
func TestAutoReplacerLeavesANestedPackageToItsOwner(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 2)
	// inner is a package of its own, sitting inside outer's folder, so outer's
	// walk reaches its files and must decline them.
	cfg.Packages = map[string]models.PackageConfig{
		"inner": {Path: "packages/outer/inner"},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "outer")
	r.WriteFile("packages/outer/inner/main.txt", "inner\n")
	r.WriteFile("packages/outer/note.md", "pinned 0.0.0\n")
	r.WriteFile("packages/outer/inner/note.md", "pinned 0.0.0\n")
	r.Commit("feat(outer,inner): bootstrap")

	// The write half carries {name}, so the file records which package wrote
	// it. Without the guard, outer's walk would stamp its own name into
	// inner's file, or race inner for it.
	res := r.Command("autoreplacer", "--since", "all",
		"--files", "note.md", "--files", "**/note.md",
		"--sub", "pinned 0.0.0=>written by {name}")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, arpRead(t, r, "packages", "outer", "note.md"), "written by outer")
	assert.Contains(t, arpRead(t, r, "packages", "outer", "inner", "note.md"), "written by inner",
		"the nested package's own turn reached its file, and outer left it alone")
}
