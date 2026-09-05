package integration

// Area 35: spaces spanning several folders. A space's `path` accepts a list
// of folders: every direct sub-folder of every listed folder is a package of
// the space, each folder may carry its own space config file (merging in
// list order) and its own .dispatexclude, and the first folder is the
// space's primary one — the login script runs there and `dispat exec --in`
// resolves there. These scenarios drive the list form through the real
// binary: discovery and release across folders, the file merge order, the
// refusals, config resolution from inside a later folder, folder inference,
// and the combination with a versioning-none space.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models/v2"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// twoFolderConfig is this area's fixture: one "libs" space over pkgs/ and
// more/, with the standard build/publish pair and a login writing down where
// it ran.
func twoFolderConfig() models.File {
	cfg := harness.BaseFile(1)
	cfg.Scripts = map[string]models.Script{
		"build":   {"echo building"},
		"publish": {"echo publishing"},
		"login":   {"pwd >> login.cwd"},
		"where":   {"pwd"},
	}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {
			Path: models.PathList{"pkgs", "more"},
			Flow: &models.SpaceFlowConfig{Build: []string{"build"}, Publish: []string{"publish"}, Login: []string{"login"}},
		},
	}
	return cfg
}

// TestSpacePathsMultiFolderLifecycle: packages under every listed folder are
// the space's — one release covers both, the login runs once and in the
// first folder, `exec --in` resolves the first folder, and a second run
// converges.
func TestSpacePathsMultiFolderLifecycle(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(twoFolderConfig())
	r.SeedPackage("pkgs", "a")
	r.SeedPackage("more", "b")
	r.Commit("feat(a,b): one space, two folders")

	r.ReleaseOK()
	assert.True(t, r.HasTag("a@0.1.0"), "tags: %v", r.TagList())
	assert.True(t, r.HasTag("b@0.1.0"), "the second folder's package releases with the space; tags: %v", r.TagList())

	data, err := os.ReadFile(r.Path("pkgs", "login.cwd"))
	require.NoError(t, err, "the login ran in the first folder")
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	assert.Len(t, lines, 1, "one login for the space, not one per folder")
	assertRanIn(t, r.Path("pkgs"), r.Path("pkgs", "login.cwd"))

	res := r.Command("exec", "where", "--in", "space:libs")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	want, err := filepath.EvalSymlinks(r.Path("pkgs"))
	require.NoError(t, err)
	got, err := filepath.EvalSymlinks(strings.TrimSpace(res.Stdout))
	require.NoError(t, err)
	assert.Equal(t, want, got, "exec --in resolves the space's first folder")

	r.ReleaseOK()
	assert.Equal(t, 2, len(r.TagList()), "converged")
}

// TestSpacePathsSpaceFilesMergeInOrder: every listed folder may carry a
// space config file. They all load, in list order — a later file overrides
// an earlier one's value — and a dependency edge declared in any of them
// reaches the graph.
func TestSpacePathsSpaceFilesMergeInOrder(t *testing.T) {
	r := harness.New(t)
	cfg := twoFolderConfig()
	cfg.Scripts["kdump"] = models.Script{"echo $K >> ../../k.log"}
	r.WriteConfigModel(cfg)
	r.SeedPackage("pkgs", "a")
	r.SeedPackage("more", "b")
	r.SeedPackage("more", "c")
	r.WriteFile("pkgs/dispat.json",
		`{"env": {"K": "one"}, "dependencies": {"b": ["a"]}}`)
	r.WriteFile("more/dispat.json",
		`{"env": {"K": "two"}, "dependencies": {"c": ["b"]}}`)
	r.Commit("feat(a,b,c): a space file in each folder")

	res := r.StatusOK()
	line := harness.GraphLine(res.Events, "b")
	assert.Contains(t, line["dependsOn"], "a", "the first folder's file declared b -> a")
	line = harness.GraphLine(res.Events, "c")
	assert.Contains(t, line["dependsOn"], "b", "the second folder's file declared c -> b")

	r.RunScriptOK("kdump")
	data, err := os.ReadFile(r.Path("k.log"))
	require.NoError(t, err)
	for _, got := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		assert.Equal(t, "two", got, "the later folder's file overrides the earlier one's env")
	}
}

// TestSpacePathsRefusals: the folder list's own rules surface through the
// binary — a folder listed twice, and folders nesting one another.
func TestSpacePathsRefusals(t *testing.T) {
	r := harness.New(t)
	cfg := twoFolderConfig()
	libs := cfg.Spaces["libs"]
	libs.Path = models.PathList{"pkgs", "pkgs"}
	cfg.Spaces["libs"] = libs
	r.WriteConfigModel(cfg)
	r.SeedPackage("pkgs", "a")
	r.Commit("feat(a): bootstrap")

	res := r.Status()
	require.NotEqual(t, 0, res.Code)
	assert.Contains(t, loadError(res), "declared more than once")

	libs.Path = models.PathList{"pkgs", "pkgs/a"}
	cfg.Spaces["libs"] = libs
	r.WriteConfigModel(cfg)
	res = r.Status()
	require.NotEqual(t, 0, res.Code)
	assert.Contains(t, loadError(res), "overlap (one contains the other)")
}

// TestSpacePathsAscentFromSecondPath: config-file resolution from inside a
// later folder still finds the monorepo root, even when that folder carries
// a space file with a packages map — the shape that would read as a nested
// monorepo root if the list form of `path` were not recognised.
func TestSpacePathsAscentFromSecondPath(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(twoFolderConfig())
	r.SeedPackage("pkgs", "a")
	r.SeedPackage("more", "b")
	r.WriteFile("more/dispat.json", `{"packages": {"b": {"tagFormat": "b-v{version}"}}}`)
	r.Commit("feat(a,b): a space file in the second folder")

	res := r.CommandAt("more/b", "status")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	line := harness.GraphLine(res.Events, "a")
	assert.NotEmpty(t, line, "the root config was resolved: the other folder's package is visible")
}

// TestSpacePathsFilterAndLocate: --space covers every folder's packages, and
// standing in a later folder means the space exactly as standing in the
// first does.
func TestSpacePathsFilterAndLocate(t *testing.T) {
	r := harness.New(t)
	cfg := twoFolderConfig()
	cfg.Scripts["mark"] = models.Script{"echo ran >> ran.log"}
	r.WriteConfigModel(cfg)
	r.SeedPackage("pkgs", "a")
	r.SeedPackage("more", "b")
	r.Commit("feat(a,b): bootstrap")

	r.RunScriptOK("mark", "--space", "libs")
	assert.FileExists(t, r.Path("pkgs", "a", "ran.log"))
	assert.FileExists(t, r.Path("more", "b", "ran.log"), "--space reaches the second folder's packages")

	res := r.CommandAt("more", "run", "mark")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	data, err := os.ReadFile(r.Path("more", "b", "ran.log"))
	require.NoError(t, err)
	assert.Len(t, strings.Split(strings.TrimSpace(string(data)), "\n"), 2,
		"standing in the second folder infers the space")

	res = r.CommandAt("more/b", "run", "mark")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	dataA, err := os.ReadFile(r.Path("pkgs", "a", "ran.log"))
	require.NoError(t, err)
	assert.Len(t, strings.Split(strings.TrimSpace(string(dataA)), "\n"), 2,
		"standing inside a package narrows to that package: a did not run a third time")
}

// TestSpacePathsNoneCombined: the two features together — a versioning-none
// space spanning two folders runs scripts everywhere and never tags
// anything, while the releasable space next to it releases normally.
func TestSpacePathsNoneCombined(t *testing.T) {
	r := harness.New(t)
	cfg := harness.BaseFile(1)
	cfg.Scripts = map[string]models.Script{
		"build":   {"echo building"},
		"publish": {"echo publishing"},
		"mark":    {"echo ran >> ran.log"},
	}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: models.PathList{"packages"}, Flow: buildPublish()},
		"ops": {
			Path:       models.PathList{"tools", "sandboxes"},
			Versioning: models.VersioningNone,
			Flow:       buildPublish(),
		},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("tools", "smoke")
	r.SeedPackage("sandboxes", "probe")
	r.Commit("feat(core,smoke,probe): everything changes at once")

	r.ReleaseOK()
	assert.True(t, r.HasTag("core@0.1.0"), "tags: %v", r.TagList())
	assert.Equal(t, 1, len(r.TagList()), "nothing under either none folder is ever tagged")

	r.RunScriptOK("mark")
	assert.FileExists(t, r.Path("tools", "smoke", "ran.log"))
	assert.FileExists(t, r.Path("sandboxes", "probe", "ran.log"),
		"the none space's second folder runs scripts too")
}
