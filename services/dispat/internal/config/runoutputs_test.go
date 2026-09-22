package config

// The sweep's output roots, from the three sides the build keys are tested
// from: what a file may write, where it may write it, and the questions that
// need every package to be known.

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// runOutputsAt returns the minimal fixture with one script's roots stated at
// the root.
func runOutputsAt(roots ...string) File {
	cfg := minimalConfig()
	cfg.RunOutputs = map[string][]string{"tests": roots}
	return cfg
}

// TestRunOutputsLoadAsWritten: a root file states a script's roots, and a
// sweep finds them by the script's name in any case.
func TestRunOutputsLoadAsWritten(t *testing.T) {
	root := writeModelRepo(t, runOutputsAt("coverage", "reports/junit/"), "pkgs/core")

	loaded, err := Load(filepath.Join(root, "dispat.json"), nil)
	require.NoError(t, err)
	_, _, _, err = DiscoverPackages(loaded, root)
	require.NoError(t, err)

	roots, ok := loaded.FindRunOutputs("TESTS")
	require.True(t, ok)
	assert.Equal(t, []string{"coverage", "reports/junit/"}, roots,
		"the roots are kept as the file wrote them; a sweep cleans them where it resolves them")
}

// TestRunOutputsShapeRefusals: every shape a root may not have, refused as the
// file loads with the key path named and the execution code attached.
func TestRunOutputsShapeRefusals(t *testing.T) {
	for name, row := range map[string]struct {
		roots []string
		want  string
	}{
		"an empty path":                   {[]string{""}, "names no folder"},
		"a path holding a NUL byte":       {[]string{"coverage\x00"}, "NUL byte"},
		"a path written with backslashes": {[]string{`coverage\unit`}, "slash-separated"},
		"a path naming a drive":           {[]string{"C:/coverage"}, "colon"},
		"an absolute path":                {[]string{"/srv/coverage"}, "relative to the repository root"},
		"a path leaving the repository":   {[]string{"../coverage"}, "leaves the repository"},
		"a path climbing back out":        {[]string{"coverage/../../x"}, "leaves the repository"},
		"the repository root itself":      {[]string{"."}, "names the repository root itself"},
		"a path naming repository metadata": {
			[]string{".git/coverage"}, "repository metadata"},
		"the same root written twice": {
			[]string{"coverage", "coverage/"}, "one root, or one inside the other"},
		"a root inside another": {
			[]string{"coverage", "coverage/unit"}, "one root, or one inside the other"},
		"two spellings of one root": {
			[]string{"coverage", "COVERAGE"}, "one root, or one inside the other"},
	} {
		t.Run(name, func(t *testing.T) {
			err := refuseBuildConfig(t, runOutputsAt(row.roots...), "pkgs/core")
			require.Error(t, err)
			assert.Contains(t, err.Error(), `runOutputs["tests"]`)
			assert.Contains(t, err.Error(), row.want)
			assert.Equal(t, DiagnosticExecution, DiagnosticCode(err))
		})
	}
}

// TestRunOutputsScriptNamesFoldTogetherOnce: two script names that are one
// name after case folding are two answers to one lookup, which the loader
// refuses as it does for `scripts`.
func TestRunOutputsScriptNamesFoldTogetherOnce(t *testing.T) {
	cfg := minimalConfig()
	cfg.RunOutputs = map[string][]string{"tests": {"coverage"}, "Tests": {"reports"}}
	err := refuseBuildConfig(t, cfg, "pkgs/core")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "runOutputs")
}

// TestRunOutputsAreRootOnly: a space, a package entry, a space folder's file
// and a package folder's file all reject the key as unknown, because a sweep
// is the entry's invocation and never a property of one package.
func TestRunOutputsAreRootOnly(t *testing.T) {
	stated := map[string]any{"tests": []any{"coverage"}}
	t.Run("a space", func(t *testing.T) {
		root := writeRawRepo(t, map[string]any{
			"scripts": map[string]any{"build": "echo b"},
			"spaces": map[string]any{"libs": map[string]any{
				"path": "pkgs", "flow": map[string]any{"build": []any{"build"}}, "runOutputs": stated}},
		}, "pkgs/core")
		_, err := Load(filepath.Join(root, "dispat.json"), nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "runOutputs")
	})
	t.Run("a package entry", func(t *testing.T) {
		root := writeRawRepo(t, map[string]any{
			"scripts": map[string]any{"build": "echo b"},
			"spaces": map[string]any{"libs": map[string]any{
				"path": "pkgs", "flow": map[string]any{"build": []any{"build"}}}},
			"packages": map[string]any{"core": map[string]any{"runOutputs": stated}},
		}, "pkgs/core")
		_, err := Load(filepath.Join(root, "dispat.json"), nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "runOutputs")
	})
	t.Run("a space folder's own file", func(t *testing.T) {
		root := writeModelRepo(t, minimalConfig(), "pkgs/core")
		writeSpaceRaw(t, root, "pkgs", map[string]any{"runOutputs": stated})
		loaded, err := Load(filepath.Join(root, "dispat.json"), nil)
		require.NoError(t, err)
		_, _, _, err = DiscoverPackages(loaded, root)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "runOutputs")
	})
	t.Run("a package folder's own file", func(t *testing.T) {
		root := writeModelRepo(t, minimalConfig(), "pkgs/core")
		writePackageRaw(t, root, "pkgs/core", map[string]any{"runOutputs": stated})
		loaded, err := Load(filepath.Join(root, "dispat.json"), nil)
		require.NoError(t, err)
		_, _, _, err = DiscoverPackages(loaded, root)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "runOutputs")
	})
}

// TestRunOutputRootsStayClearOfPackagesAndBuildOutputs: the questions that
// need every package to be known, asked at discovery so that `dispat status`
// reports them before any sweep writes anything.
func TestRunOutputRootsStayClearOfPackagesAndBuildOutputs(t *testing.T) {
	for name, row := range map[string]struct {
		roots        []string
		buildOutputs []string
		want         string
	}{
		"a package folder": {
			roots: []string{"pkgs/core"}, want: `is or holds the folder of package "core"`},
		"a folder holding a package": {
			roots: []string{"pkgs"}, want: `is or holds the folder of package "core"`},
		"another spelling of a package folder": {
			roots: []string{"PKGS/core"}, want: `is or holds the folder of package "core"`},
		"a package's build output root": {
			roots: []string{"pkgs/core/dist"}, buildOutputs: []string{"dist"},
			want: `overlaps the build output "dist" of package "core"`},
		"a folder inside a build output root": {
			roots: []string{"pkgs/core/dist/coverage"}, buildOutputs: []string{"dist"},
			want: `overlaps the build output "dist" of package "core"`},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := runOutputsAt(row.roots...)
			cfg.BuildOutputs = row.buildOutputs
			err := refuseBuildConfig(t, cfg, "pkgs/core")
			require.Error(t, err)
			assert.Contains(t, err.Error(), row.want)
			assert.Equal(t, DiagnosticExecution, DiagnosticCode(err))
		})
	}
}

// TestRunOutputRootsMayLieInsideAPackageFolder: the rule is that a root is not
// a package's folder and does not hold one. A folder inside a package, and a
// folder of a repository whose root is itself a package, are ordinary roots.
func TestRunOutputRootsMayLieInsideAPackageFolder(t *testing.T) {
	for name, roots := range map[string][]string{
		"a folder beside the packages":   {"coverage"},
		"a folder inside a package":      {"pkgs/core/coverage"},
		"a sibling of a build output":    {"pkgs/core/dist-coverage"},
		"several folders for one script": {"coverage", "reports"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := runOutputsAt(roots...)
			cfg.BuildOutputs = []string{"dist"}
			assert.NoError(t, refuseBuildConfig(t, cfg, "pkgs/core"))
		})
	}
}

// TestRunOutputRootsResolveAgainstEachPackagesRepository: in a composed
// workspace a root is resolved against the repository that owns each swept
// package, because that is the checkout its task writes in. A root naming a
// package folder of one repository is refused, and the same spelling is an
// ordinary folder of another.
func TestRunOutputRootsResolveAgainstEachPackagesRepository(t *testing.T) {
	workspace := t.TempDir()
	packageIn := func(repository string) *model.Package {
		return &model.Package{
			Name: repository + "-core", Dir: filepath.Join(workspace, repository, "core"),
			RepoRoot: filepath.Join(workspace, repository), Space: &model.Space{},
		}
	}
	pkgs := []*model.Package{packageIn("one"), packageIn("two")}

	err := checkRunOutputRoots(map[string][]string{"tests": {"core"}}, pkgs, workspace)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `package "one-core" (core)`,
		"the refusal names the folder relative to the repository it was resolved against")
	assert.NoError(t, checkRunOutputRoots(map[string][]string{"tests": {"coverage"}}, pkgs, workspace))
	assert.NoError(t, checkRunOutputRoots(nil, pkgs, workspace), "declaring nothing checks nothing")
	assert.Equal(t, []string{workspace}, collectRepositoryRoots(nil, workspace),
		"a workspace with no package still has the repository it was read from")
}
