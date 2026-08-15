// Goal 32: a reference naming several files. One shared fragment plus the
// files that adjust it, merged in the order they are named, is what lets a
// common block of scripts or record lines be written once and used from
// several places without copying it.
//
// The configurations here go through WriteConfigRaw: `$ref` is a shape the
// typed model deliberately cannot express, which is the same reason the
// single-file reference tests in config_test.go are written that way.

package integration

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// rawSplitConfig is the base a scenario here starts from: the raw spelling of
// harness.BaseFile plus the canonical one-space flow, with the keys a scenario
// splits across files left to it.
func rawSplitConfig() map[string]any {
	return map[string]any{
		"logLevel":    "info",
		"logFormat":   "json",
		"github":      map[string]any{"enabled": false},
		"updateCheck": false,
		"scripts":     map[string]any{"build": echoBuild, "publish": "echo publishing"},
		"spaces": map[string]any{
			"libs": map[string]any{"path": "packages",
				"flow": map[string]any{"build": []string{"build"}, "publish": []string{"publish"}}},
		},
	}
}

// TestMultiRefMergesObjectFragments: a common fragment and the file that
// adjusts it become one object, the later file winning the keys it writes.
// Every file read is on the record, which is how a configuration split several
// ways still answers "where did that come from".
func TestMultiRefMergesObjectFragments(t *testing.T) {
	r := harness.New(t)
	r.WriteFile("cfg/scripts-common.yaml", "build: echo building\npublish: echo publishing\n")
	r.WriteFile("cfg/scripts-local.json", `{"build": "echo building locally"}`)
	cfg := rawSplitConfig()
	cfg["scripts"] = map[string]any{"$ref": []string{"./cfg/scripts-common.yaml", "./cfg/scripts-local.json"}}
	r.WriteConfigRaw(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first release")

	res := r.ReleaseOK()
	assert.True(t, r.HasTag("core@0.1.0"), "tags: %v", r.TagList())
	assert.Contains(t, res.Stdout, "building locally", "the later file wins the key it shares")
	assert.Contains(t, res.Stdout, "publishing", "and the key only the first file wrote survives")

	trace := r.Status("--log-level", "trace")
	require.Equal(t, 0, trace.Code, "stderr:\n%s", trace.Stderr)
	for _, file := range []string{"dispat.json", "cfg/scripts-common.yaml", "cfg/scripts-local.json"} {
		assert.Contains(t, trace.Stdout, file, "every file the reference named is traced")
	}
}

// TestMultiRefConcatenatesLineFragments: the case the feature exists for. A
// shared block of record lines lives in one file, the lines a repository adds
// in another, and the entry carries both in the order they are named. The
// fragments are in different formats because the format a fragment is written
// in changes nothing.
func TestMultiRefConcatenatesLineFragments(t *testing.T) {
	r := harness.New(t)
	r.WriteFile("cfg/footer-common.yaml", "- Released by dispat.\n")
	r.WriteFile("cfg/footer-extra.json", `[{"line": "Ask #core-team about core.", "package": ["core"]}]`)
	cfg := rawSplitConfig()
	cfg["changelog"] = map[string]any{
		"footer": map[string]any{"$ref": []string{"./cfg/footer-common.yaml", "./cfg/footer-extra.json"}},
	}
	r.WriteConfigRaw(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "utils")
	r.Commit("feat(core,utils): first release")
	r.ReleaseOK()

	core, err := os.ReadFile(r.Path("packages/core/CHANGELOG.md"))
	require.NoError(t, err)
	assert.Contains(t, string(core), "Released by dispat.", "the shared block")
	assert.Contains(t, string(core), "Ask #core-team about core.", "and the line the second file added")
	assert.Less(t, strings.Index(string(core), "Released by dispat."),
		strings.Index(string(core), "Ask #core-team about core."),
		"in the order the files are named")

	utils, err := os.ReadFile(r.Path("packages/utils/CHANGELOG.md"))
	require.NoError(t, err)
	assert.Contains(t, string(utils), "Released by dispat.")
	assert.NotContains(t, string(utils), "#core-team",
		"a merged line keeps the filters it was written with")
}

// TestMultiRefSiblingOverrideAndEnvCase: the keys beside a reference outrank
// every file it named, and an environment variable merged out of two fragments
// reaches a script spelled exactly as the file spelled it. Key case is the
// thing a merge could plausibly lose, since the loader hands viper a
// lowercased copy of the tree.
func TestMultiRefSiblingOverrideAndEnvCase(t *testing.T) {
	r := harness.New(t)
	r.WriteFile("cfg/env-common.yaml", "SharedKey: shared\nOverridden: from the first file\n")
	r.WriteFile("cfg/env-local.json", `{"LocalKey": "local", "Overridden": "from the second file"}`)
	cfg := rawSplitConfig()
	cfg["scripts"] = map[string]any{
		"build":   "echo [$SharedKey][$LocalKey][$Overridden][$Beside]",
		"publish": "echo publishing",
	}
	cfg["env"] = map[string]any{
		"$ref":   []string{"./cfg/env-common.yaml", "./cfg/env-local.json"},
		"Beside": "from the config file",
	}
	r.WriteConfigRaw(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first release")

	res := r.ReleaseOK()
	assert.Contains(t, res.Stdout, "[shared][local][from the second file][from the config file]",
		"every fragment's keys arrive with their case, the later file wins, and the sibling outranks both")
}

// TestMultiRefRefusals: what a list of files can get wrong. Each is a
// configuration error, so each stops the run where every configuration error
// stops it: before a tag, a commit or a script.
func TestMultiRefRefusals(t *testing.T) {
	cases := []struct {
		name  string
		ref   any
		wants []string
	}{
		{"no files at all", []string{},
			[]string{"scripts: $ref names no files"}},
		{"a name that is not a file", []any{"./cfg/a.json", 7},
			[]string{"scripts: $ref[1] must name another config file"}},
		{"files that hold different kinds", []string{"./cfg/a.json", "./cfg/list.json"},
			[]string{"holds an object", "holds a list",
				"the files of one $ref must all hold objects, or all hold lists"}},
		{"a file that is missing", []string{"./cfg/a.json", "./cfg/absent.json"},
			[]string{"cfg/absent.json", "cannot read"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := harness.New(t)
			r.WriteFile("cfg/a.json", `{"build": "echo building > built.txt"}`)
			r.WriteFile("cfg/list.json", `["build"]`)
			cfg := rawSplitConfig()
			cfg["scripts"] = map[string]any{"$ref": tc.ref}
			r.WriteConfigRaw(cfg)
			r.SeedPackage("packages", "core")
			r.Commit("feat(core): first release")

			res := r.Release()
			assert.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.Contains(t, res.Stderr, "config:", "a configuration error, named as one")
			for _, want := range tc.wants {
				assert.Contains(t, res.Stderr, want)
			}
			assert.Empty(t, r.TagList(), "nothing released")
			assert.NoFileExists(t, r.Path("built.txt"), "no script ran")
		})
	}
}

// TestMultiRefCycleNamesThePath: every file a reference names is followed on
// its own, so a cycle closed by the second of them is refused with the path
// that closed it rather than with a stack overflow.
func TestMultiRefCycleNamesThePath(t *testing.T) {
	r := harness.New(t)
	r.WriteFile("cfg/first.json", `{"build": "echo building"}`)
	r.WriteFile("cfg/second.json", `{"publish": {"$ref": "../dispat.json"}}`)
	cfg := rawSplitConfig()
	cfg["scripts"] = map[string]any{"$ref": []string{"./cfg/first.json", "./cfg/second.json"}}
	r.WriteConfigRaw(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first release")

	res := r.Release()
	assert.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stderr, "$ref cycle")
	assert.Contains(t, res.Stderr, "cfg/second.json", "the file that closed it is named")
	assert.Empty(t, r.TagList(), "nothing released")
}

// TestMultiRefComputeWriteRefuses: a key merged from several files is held by
// no one of them, so `compute --write` refuses instead of picking a file, and
// leaves every file exactly as it found it. A list naming one file is that
// reference written the long way, and is written through as one.
func TestMultiRefComputeWriteRefuses(t *testing.T) {
	r := harness.New(t)
	r.WriteFile("cfg/packages-a.json", `{"web": {"dependencies": ["ghost"]}}`)
	r.WriteFile("cfg/packages-b.json", `{"core": {}}`)
	cfg := rawSplitConfig()
	cfg["packages"] = map[string]any{"$ref": []string{"./cfg/packages-a.json", "./cfg/packages-b.json"}}
	r.WriteConfigRaw(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "ghost")
	r.SeedPackage("packages", "web")
	r.WriteFile("packages/web/package.json", `{"name": "@acme/web", "dependencies": {"@acme/core": "workspace:*"}}`)
	r.WriteFile("packages/core/package.json", `{"name": "@acme/core"}`)
	r.Commit("feat(core,ghost,web): bootstrap")

	before, err := os.ReadFile(r.Path("cfg", "packages-a.json"))
	require.NoError(t, err)

	res := r.Command("compute", "--write")
	assert.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout+res.Stderr, "cannot be rewritten in place")
	assert.Contains(t, res.Stdout+res.Stderr, "point the $ref at a single file", "the ways out are named")

	after, err := os.ReadFile(r.Path("cfg", "packages-a.json"))
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "the fragment was left as it was")
	assert.NoFileExists(t, r.Path("cfg", "packages-a.json.backup"), "and nothing was backed up")

	// The same configuration through a list naming one file: the edit lands
	// in the file it names, as it does through a plain reference.
	cfg["packages"] = map[string]any{"$ref": []string{"./cfg/packages-a.json"}}
	r.WriteConfigRaw(cfg)

	single := r.Command("compute", "--write")
	require.Equal(t, 0, single.Code, "stdout:\n%s\nstderr:\n%s", single.Stdout, single.Stderr)
	fragment, err := os.ReadFile(r.Path("cfg", "packages-a.json"))
	require.NoError(t, err)
	assert.Contains(t, string(fragment), "core", "the detected edge landed in the fragment")
	assert.NotContains(t, string(fragment), "ghost", "and the stale one was removed there")

	root, err := os.ReadFile(r.Path("dispat.json"))
	require.NoError(t, err)
	assert.Contains(t, string(root), "cfg/packages-a.json", "the reference survived the write")
}
