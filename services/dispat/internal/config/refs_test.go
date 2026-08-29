package config

// The loader every config file arrives through: which formats it reads, what
// it makes of a file that says nothing, and the copy that keeps the decode's
// key-lowercasing away from the tree the env pass reads.
//
// Configs are authored as typed models and marshalled, as everywhere else in
// this package; raw bytes appear only where the point is a file no marshaller
// would produce.

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeFile writes one file into dir and returns its path.
func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	return path
}

// TestReadTreeFormats: the three formats dispat reads all parse into the same
// tree, with their keys spelled as the file wrote them.
func TestReadTreeFormats(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct{ name, body string }{
		{"dispat.json", `{"env": {"MiXed": "v"}}`},
		{"dispat.yaml", "env:\n  MiXed: v\n"},
		{"dispat.yml", "env:\n  MiXed: v\n"},
		{"dispat.toml", "[env]\nMiXed = \"v\"\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tree, err := readTree(writeFile(t, dir, tc.name, tc.body))
			require.NoError(t, err)
			assert.Equal(t, map[string]any{"env": map[string]any{"MiXed": "v"}}, tree.root)
		})
	}
}

// TestReadTreeUnknownFormatIsRefused: the extension names the parser, so a
// file in a format dispat has none for is refused by name rather than guessed
// at. This is the one thing an explicit `--config dispat.ini` can be told.
func TestReadTreeUnknownFormatIsRefused(t *testing.T) {
	path := writeFile(t, t.TempDir(), "dispat.ini", "[env]\nMiXed = v\n")

	_, err := readTree(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot read "+path)
	assert.Contains(t, err.Error(), "dispat reads json, yaml and toml config files")
}

// TestReadTreeMalformedFileIsReported: a file the parser chokes on names
// itself, because "cannot read this file" is the first thing to know and the
// parser's own message follows it.
func TestReadTreeMalformedFileIsReported(t *testing.T) {
	path := writeFile(t, t.TempDir(), "dispat.json", "{not json")

	_, err := readTree(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot read "+path)
}

// TestReadTreeEmptyFileIsAnEmptyObject: an empty file is a config that says
// nothing, which validation answers ("at least one space or package") far
// better than the parser could.
func TestReadTreeEmptyFileIsAnEmptyObject(t *testing.T) {
	tree, err := readTree(writeFile(t, t.TempDir(), "dispat.yaml", ""))
	require.NoError(t, err)
	assert.Empty(t, tree.root)

	_, err = Load(writeFile(t, t.TempDir(), "dispat.yaml", ""), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one space or package")
}

// TestReadTreeTopLevelMustBeAnObject: a config file is an object of keys. A
// document that is a list has no key to read, and saying so beats an
// unknown-key error about a key nobody wrote.
func TestReadTreeTopLevelMustBeAnObject(t *testing.T) {
	path := writeFile(t, t.TempDir(), "dispat.yaml", "- one\n- two\n")

	_, err := readTree(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "the top level is not an object")
}

// TestLowerTreeLeavesTheTreeAlone: the lowered view folds every key of the map
// it is handed, all the way down and inside lists too. It is therefore a copy:
// the tree is what the env pass reads case-exactly, and a config whose env
// survived the decode is the proof it was not shared.
func TestLowerTreeLeavesTheTreeAlone(t *testing.T) {
	tree := &tree{root: map[string]any{
		"spaces": map[string]any{"Libs": map[string]any{
			"env":       map[string]any{"MiXed": "v"},
			"aliasTags": []any{map[string]any{"Format": "latest"}},
		}},
	}}

	raw := lowerTree(tree, nil)
	assert.Equal(t, map[string]any{"spaces": map[string]any{"libs": map[string]any{
		"env":       map[string]any{"mixed": "v"},
		"aliastags": []any{map[string]any{"format": "latest"}},
	}}}, raw, "the lowered view folds every key, lists included")

	assert.Equal(t, map[string]any{"spaces": map[string]any{"Libs": map[string]any{
		"env":       map[string]any{"MiXed": "v"},
		"aliasTags": []any{map[string]any{"Format": "latest"}},
	}}}, tree.root, "the tree keeps every key as the file spelled it")
}

// TestLowerTreeFoldsEveryKindOfMap: the lowered view is what makes the config
// language case-insensitive, so it has to reach every map a parser can
// produce. A yaml mapping with a non-string key parses as a generic map. A list
// of strings is the flag overlay's, and is copied rather than shared so that
// nothing here can write through into the flag set. Anything else is a value no
// parser builds, and passes through as it came: this is the contract the
// reflected deep copy used to cover, narrowed to the containers that exist.
func TestLowerTreeFoldsEveryKindOfMap(t *testing.T) {
	strs := []string{"one", "two"}
	typed := map[string][]string{"Keep": {"As-Is"}}
	tree := &tree{root: map[string]any{
		"Generic": map[any]any{"MiXed": "v", 1: "one", true: "yes"},
		"List":    []any{map[any]any{"Nested": "v"}},
		"Strings": strs,
		"Typed":   typed,
	}}

	raw := lowerTree(tree, nil)
	assert.Equal(t, map[string]any{"mixed": "v", "1": "one", "true": "yes"}, raw["generic"],
		"a generic map becomes a string-keyed one, folded, so the decode meets one kind of map")
	assert.Equal(t, []any{map[string]any{"nested": "v"}}, raw["list"])
	assert.Equal(t, strs, raw["strings"], "a list of strings arrives as it was written")
	assert.Equal(t, typed, raw["typed"], "a typed container keeps its own keys")

	raw["strings"].([]string)[0] = "changed"
	assert.Equal(t, "one", strs[0], "and the flag overlay's list is copied rather than shared")
}

// TestSettingsPrunesEmptyObjectsAndSplitsPaths pins the two rules the decode
// input carries that the lowered tree does not: an object with no keys is not
// a key at all, which is why an opt-in block written as a bare {} says nothing
// rather than enabling itself at its defaults; and a key spelled with the
// delimiter is the levels it names. Both are load-bearing — the config tests
// author their opt-in blocks around the first — so they are pinned here rather
// than left to be rediscovered.
func TestSettingsPrunesEmptyObjectsAndSplitsPaths(t *testing.T) {
	out := settings(map[string]any{
		"kept":    map[string]any{"leaf": "v"},
		"empty":   map[string]any{},
		"hollow":  map[string]any{"inner": map[string]any{}},
		"a.b":     "dotted",
		"list":    []any{},
		"nothing": nil,
	})

	assert.Equal(t, map[string]any{"leaf": "v"}, out["kept"])
	assert.NotContains(t, out, "empty", "an object with no keys is not a key")
	assert.NotContains(t, out, "hollow", "nor is one holding only empty objects")
	assert.Equal(t, map[string]any{"b": "dotted"}, out["a"], "the delimiter is a level")
	assert.Equal(t, []any{}, out["list"], "an empty list is a value, not an absence")
	assert.Contains(t, out, "nothing", "a key written with no value is still a key")
}

// TestLowerTreeFlagOverridesOnlyWhenPassed pins the one thing a flag binding
// ever meant: a flag the caller actually passed replaces what the file says,
// and a flag left alone carries its default into nothing at all. The two cases
// are one test because the bug they guard against is the pair being confused —
// an unset --log-level clobbering a configured logLevel with "" looks like a
// config that was never read.
func TestLowerTreeFlagOverridesOnlyWhenPassed(t *testing.T) {
	newFlags := func() *pflag.FlagSet {
		fs := pflag.NewFlagSet("dispat", pflag.ContinueOnError)
		fs.IntSlice("concurrency", nil, "")
		fs.String("log-level", "", "")
		fs.String("log-format", "", "")
		return fs
	}
	configured := func() *tree {
		return &tree{root: map[string]any{
			"concurrency": []any{4, 2},
			"logLevel":    "debug",
			"logFormat":   "json",
		}}
	}

	t.Run("unset", func(t *testing.T) {
		raw := lowerTree(configured(), newFlags())
		assert.Equal(t, []any{4, 2}, raw["concurrency"])
		assert.Equal(t, "debug", raw["loglevel"])
		assert.Equal(t, "json", raw["logformat"])
	})

	t.Run("passed", func(t *testing.T) {
		fs := newFlags()
		require.NoError(t, fs.Parse([]string{"--concurrency", "1,3", "--log-level", "warn"}))

		raw := lowerTree(configured(), fs)
		// A list flag hands over its elements: its printed form is "[1,3]",
		// which no list field could be weakly typed out of.
		assert.Equal(t, []string{"1", "3"}, raw["concurrency"])
		assert.Equal(t, "warn", raw["loglevel"])
		assert.Equal(t, "json", raw["logformat"], "a flag nobody passed leaves its key alone")
	})

	t.Run("through_the_loader", func(t *testing.T) {
		cfg := minimalConfig()
		cfg.LogLevel = "debug"
		cfg.Concurrency = []int{4, 2}
		root := writeModelRepo(t, cfg, "pkgs/core")

		loaded, err := Load(filepath.Join(root, "dispat.json"), newFlags())
		require.NoError(t, err)
		assert.Equal(t, "debug", loaded.LogLevel, "an unset flag does not clobber the file")
		assert.Equal(t, 4, loaded.BuildConcurrency)
		assert.Equal(t, 2, loaded.PublishConcurrency)

		fs := newFlags()
		require.NoError(t, fs.Parse([]string{"--log-level", "warn", "--concurrency", "1,3"}))
		overridden, err := Load(filepath.Join(root, "dispat.json"), fs)
		require.NoError(t, err)
		assert.Equal(t, "warn", overridden.LogLevel)
		assert.Equal(t, 1, overridden.BuildConcurrency)
		assert.Equal(t, 3, overridden.PublishConcurrency)
	})
}

// TestRefReplacesTheValue: the whole point, at three levels of nesting and in
// every position a value can be. A referenced document becomes the value
// wholesale, whether it is an object, a list or a single value.
func TestRefReplacesTheValue(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "spaces.json", `{"libs": {"path": "pkgs", "flow": {"$ref": "./flow.yaml"}}}`)
	writeFile(t, dir, "flow.yaml", "build:\n  - build\n")
	writeFile(t, dir, "shell.json", `["/bin/bash", "-c"]`)
	writeFile(t, dir, "tagformat.yaml", "'{name}-{version}'\n")
	path := writeFile(t, dir, "dispat.json", `{
		"scripts": {"build": "echo b"},
		"spaces": {"$ref": "./spaces.json"},
		"shell": {"$ref": "./shell.json"},
		"tagFormat": {"$ref": "./tagformat.yaml"}
	}`)

	cfg, err := Load(path, nil)
	require.NoError(t, err)
	assert.Equal(t, PathList{"pkgs"}, cfg.Spaces["libs"].Path)
	assert.Equal(t, []string{"build"}, cfg.Spaces["libs"].Flow.Build, "an object through two files")
	assert.Equal(t, []string{"/bin/bash", "-c"}, cfg.Shell, "a referenced list")
	assert.Equal(t, "{name}-{version}", cfg.TagFormat, "a referenced single value")
	assert.Equal(t, []string{
		path,
		filepath.Join(dir, "shell.json"),
		filepath.Join(dir, "spaces.json"),
		filepath.Join(dir, "flow.yaml"),
		filepath.Join(dir, "tagformat.yaml"),
	}, cfg.SourceFiles, "every file read, in the order it was read")
}

// TestRefLoadsTheSameConfigurationAsInline: a configuration split across files
// is the configuration it was split out of, which is the claim that matters
// more than any single key.
func TestRefLoadsTheSameConfigurationAsInline(t *testing.T) {
	cfg := minimalConfig()
	cfg.Env = map[string]string{"MiXed": "v"}
	inline, err := loadModel(t, cfg, "pkgs/core")
	require.NoError(t, err)

	dir := t.TempDir()
	writeJSON(t, filepath.Join(dir, "spaces.json"), cfg.Spaces)
	writeFile(t, dir, "env.yaml", "MiXed: v\n")
	split := writeFile(t, dir, "dispat.json", `{
		"scripts": {"build": "echo b"},
		"run": {},
		"spaces": {"$ref": "./spaces.json"},
		"env": {"$ref": "./env.yaml"}
	}`)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "pkgs", "core"), 0o755))

	loaded, err := Load(split, nil)
	require.NoError(t, err)
	loaded.SourceFiles, inline.SourceFiles = nil, nil
	assert.Equal(t, inline, loaded)
	assert.Equal(t, map[string]string{"MiXed": "v"}, loaded.Env,
		"a referenced env object keeps its key case like an inline one")
}

// TestRefResolvesAgainstTheFileThatWroteIt: a relative reference is relative to
// its own file, not to the monorepo root, which is what lets a folder of
// fragments be moved as a folder. Proven with the same file name sitting in
// both places, holding different things.
func TestRefResolvesAgainstTheFileThatWroteIt(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "flow.yaml", "build:\n  - wrong\n")
	writeFile(t, dir, "cfg/flow.yaml", "build:\n  - right\n")
	writeFile(t, dir, "cfg/spaces.json", `{"libs": {"path": "pkgs", "flow": {"$ref": "./flow.yaml"}}}`)
	path := writeFile(t, dir, "dispat.json", `{
		"scripts": {"build": "echo b"},
		"spaces": {"$ref": "./cfg/spaces.json"}
	}`)

	cfg, err := Load(path, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"right"}, cfg.Spaces["libs"].Flow.Build)
}

// TestRefAbsolutePath: an absolute path is used as written, for the fragment
// that lives outside the repository.
func TestRefAbsolutePath(t *testing.T) {
	shared := writeFile(t, t.TempDir(), "scripts.json", `{"build": "echo shared"}`)
	dir := t.TempDir()
	path := writeFile(t, dir, "dispat.json", `{
		"scripts": {"$ref": "`+filepath.ToSlash(shared)+`"},
		"spaces": {"libs": {"path": "pkgs", "flow": {"build": ["build"]}}}
	}`)

	cfg, err := Load(path, nil)
	require.NoError(t, err)
	assert.Equal(t, Script{"echo shared"}, cfg.Scripts["build"])
}

// TestRefSiblingKeysOverride: a reference brings in a base and the keys beside
// it settle the rest, so one shared fragment serves the places that agree with
// it and the places that agree with most of it.
func TestRefSiblingKeysOverride(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "space.yaml", "path: pkgs\nflow:\n  build:\n    - build\nversioning: fixed\n")
	writeFile(t, dir, "independent.json", `"independent"`)
	path := writeFile(t, dir, "dispat.json", `{
		"scripts": {"build": "echo b"},
		"spaces": {
			"libs": {"$ref": "./space.yaml"},
			"apps": {"$ref": "./space.yaml", "Versioning": {"$ref": "./independent.json"}}
		}
	}`)

	cfg, err := Load(path, nil)
	require.NoError(t, err)
	assert.Equal(t, "fixed", cfg.Spaces["libs"].Versioning, "the fragment as it is")
	assert.Equal(t, "independent", cfg.Spaces["apps"].Versioning,
		"the sibling wins, however the two files spell the key, and may be a reference itself")
	assert.Equal(t, PathList{"pkgs"}, cfg.Spaces["apps"].Path, "everything it did not override survives")
}

// TestRefSiblingsNeedAnObject: keys beside a reference override the object it
// brought in, so a fragment that is not an object leaves them nothing to
// override and the file is refused rather than half-applied.
func TestRefSiblingsNeedAnObject(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell.json", `["/bin/bash", "-c"]`)
	writeFile(t, dir, "format.json", `"{name}@{version}"`)

	list := writeFile(t, dir, "dispat.json", `{"shell": {"$ref": "./shell.json", "extra": 1}}`)
	_, err := Load(list, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "the file is a list, so the keys beside the $ref have nothing to override")

	single := writeFile(t, dir, "dispat.yaml", `{"tagFormat": {"$ref": "./format.json", "extra": 1}}`)
	_, err = Load(single, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "the file is a single value, so the keys beside the $ref have nothing to override")
}

// TestRefTheDocumentItself: a config file may be nothing but a reference, which
// is how a repository keeps its real configuration somewhere other than the
// name dispat looks for.
func TestRefTheDocumentItself(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "cfg/real.yaml", "scripts:\n  build: echo b\nspaces:\n  libs:\n    path: pkgs\n    flow:\n      build: [build]\n")
	path := writeFile(t, dir, "dispat.json", `{"$ref": "./cfg/real.yaml"}`)

	cfg, err := Load(path, nil)
	require.NoError(t, err)
	assert.Equal(t, PathList{"pkgs"}, cfg.Spaces["libs"].Path)
}

// TestRefInsideCustom: `custom` is free-form data dispat never reads, and a
// reference means there what it means everywhere else. One rule, no exception
// to remember.
func TestRefInsideCustom(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "team.json", `{"team": "platform"}`)
	path := writeFile(t, dir, "dispat.json", `{
		"scripts": {"build": "echo b"},
		"spaces": {"libs": {"path": "pkgs", "flow": {"build": ["build"]}}},
		"custom": {"owners": {"$ref": "./team.json"}}
	}`)

	cfg, err := Load(path, nil)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"owners": map[string]any{"team": "platform"}}, cfg.Custom)
}

// TestRefTheSameFileTwiceIsNotACycle: a fragment used in two places is read
// twice and lands in both, because only a file already being read is a cycle.
func TestRefTheSameFileTwiceIsNotACycle(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "flow.yaml", "build:\n  - build\n")
	path := writeFile(t, dir, "dispat.json", `{
		"scripts": {"build": "echo b"},
		"spaces": {
			"libs": {"path": "pkgs", "flow": {"$ref": "./flow.yaml"}},
			"apps": {"path": "apps", "flow": {"$ref": "./flow.yaml"}}
		}
	}`)

	cfg, err := Load(path, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"build"}, cfg.Spaces["libs"].Flow.Build)
	assert.Equal(t, []string{"build"}, cfg.Spaces["apps"].Flow.Build)
}

// TestRefCycles: a file that reaches itself is refused with the way it got
// there, however many files the loop runs through.
func TestRefCycles(t *testing.T) {
	t.Run("a file referencing itself", func(t *testing.T) {
		dir := t.TempDir()
		path := writeFile(t, dir, "dispat.json", `{"spaces": {"$ref": "./dispat.json"}}`)

		_, err := Load(path, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "$ref cycle: "+path+" (spaces) -> "+path)
		assert.Contains(t, err.Error(), "a file cannot reference itself, directly or through another")
	})

	t.Run("through two more files", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "a.json", `{"libs": {"$ref": "./b.json"}}`)
		writeFile(t, dir, "b.json", `{"path": "pkgs", "flow": {"$ref": "./dispat.json"}}`)
		path := writeFile(t, dir, "dispat.json", `{"spaces": {"$ref": "./a.json"}}`)

		_, err := Load(path, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "$ref cycle: "+
			path+" (spaces) -> "+filepath.Join(dir, "a.json")+" (libs) -> "+
			filepath.Join(dir, "b.json")+" (flow) -> "+path)
	})

	t.Run("two names for one file", func(t *testing.T) {
		dir := t.TempDir()
		path := writeFile(t, dir, "dispat.json", `{"spaces": {"$ref": "./cfg/../dispat.json"}}`)

		_, err := Load(path, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "$ref cycle")
	})
}

// TestRefNestingIsCapped: a chain nobody wrote on purpose stops at a depth
// rather than at a stack overflow. Each file references the next, so the cap
// is what ends it.
func TestRefNestingIsCapped(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i <= maxRefDepth+1; i++ {
		writeFile(t, dir, fmt.Sprintf("f%d.json", i), fmt.Sprintf(`{"next": {"$ref": "./f%d.json"}}`, i+1))
	}
	path := writeFile(t, dir, "dispat.json", `{"spaces": {"$ref": "./f0.json"}}`)

	_, err := Load(path, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), fmt.Sprintf("$ref nesting is more than %d files deep", maxRefDepth))
}

// TestRefFailures: everything a reference can get wrong, each named where it
// was written and with what it pointed at.
func TestRefFailures(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct{ name, body, want string }{
		{"missing file", `{"env": {"$ref": "./absent.json"}}`,
			`env: $ref "./absent.json": cannot read`},
		{"unreadable format", `{"env": {"$ref": "./env.ini"}}`,
			"dispat reads json, yaml and toml config files"},
		{"empty file", `{"env": {"$ref": "./empty.yaml"}}`,
			`env: $ref "./empty.yaml": the file is empty, so the key would have no value`},
		{"not a string", `{"env": {"$ref": 7}}`,
			"env: $ref must name another config file"},
		{"empty string", `{"env": {"$ref": "  "}}`,
			"env: $ref must name another config file"},
		{"deep inside a list", `{"aliasTags": [{"$ref": "./absent.json"}]}`,
			`aliasTags[0]: $ref "./absent.json": cannot read`},
		{"the document itself", `{"$ref": "./absent.json"}`,
			`the document: $ref "./absent.json": cannot read`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeFile(t, dir, "env.ini", "A = 1\n")
			writeFile(t, dir, "empty.yaml", "")
			path := writeFile(t, dir, "dispat.json", tc.body)

			_, err := Load(path, nil)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

// TestRefListMergesObjects: a reference may name several files, which are read
// in order and merged key by key. The last file to write a key wins it, in its
// own spelling, so a shared fragment can be adjusted by the ones after it.
func TestRefListMergesObjects(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "base.yaml", "MiXed: base\nKept: base\n")
	writeFile(t, dir, "extra.json", `{"mixed": "extra", "Added": "extra"}`)
	path := writeFile(t, dir, "dispat.json", `{
		"scripts": {"build": "echo b"},
		"spaces": {"libs": {"path": "pkgs", "flow": {"build": ["build"]}}},
		"env": {"$ref": ["./base.yaml", "./extra.json"]}
	}`)

	cfg, err := Load(path, nil)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"mixed": "extra", "Kept": "base", "Added": "extra"}, cfg.Env,
		"the later file wins the key it shares, and only its spelling survives")
	assert.Equal(t, []string{path, filepath.Join(dir, "base.yaml"), filepath.Join(dir, "extra.json")},
		cfg.SourceFiles, "every file the reference named, in the order it was read")
}

// TestRefListConcatenatesLists: files that hold lists are added one after
// another, which is what lets a common block of lines be extended in place
// rather than copied.
func TestRefListConcatenatesLists(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell.json", `["/bin/bash"]`)
	writeFile(t, dir, "flags.yaml", "- -e\n- -c\n")
	writeFile(t, dir, "common.yaml", "- shared line\n")
	writeFile(t, dir, "release.json", `[{"line": "release line", "package": ["core"]}]`)
	path := writeFile(t, dir, "dispat.json", `{
		"scripts": {"build": "echo b"},
		"spaces": {"libs": {"path": "pkgs", "flow": {"build": ["build"]}}},
		"shell": {"$ref": ["./shell.json", "./flags.yaml"]},
		"changelog": {"footer": {"$ref": ["./common.yaml", "./release.json"]}}
	}`)

	cfg, err := Load(path, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"/bin/bash", "-e", "-c"}, cfg.Shell, "in the order the files are named")
	require.Len(t, cfg.Changelog.Footer, 2, "the shorthands of both files expand as they always do")
	assert.Equal(t, []string{"shared line"}, cfg.Changelog.Footer[0].Line)
	assert.Equal(t, []string{"core"}, cfg.Changelog.Footer[1].Package)
}

// TestRefListOfOneIsTheSameReference: a list holding one file is the reference
// naming that file, written the long way. Refusing it would make the list form
// a trap for anything that generates configuration.
func TestRefListOfOneIsTheSameReference(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "scripts.json", `{"build": "echo shared"}`)
	path := writeFile(t, dir, "dispat.json", `{
		"scripts": {"$ref": ["./scripts.json"]},
		"spaces": {"libs": {"path": "pkgs", "flow": {"build": ["build"]}}}
	}`)

	cfg, err := Load(path, nil)
	require.NoError(t, err)
	assert.Equal(t, Script{"echo shared"}, cfg.Scripts["build"])
}

// TestRefListMixedKindsAreRefused: merging asks the files to agree on what they
// hold. Two kinds of answer have no merge between them, and neither has a
// single value with anything, so the files are named rather than guessed at.
func TestRefListMixedKindsAreRefused(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "object.json", `{"build": "echo b"}`)
	writeFile(t, dir, "list.json", `["build"]`)
	writeFile(t, dir, "single.json", `"echo b"`)

	for _, tc := range []struct{ name, body, want string }{
		{"an object then a list", `{"scripts": {"$ref": ["./object.json", "./list.json"]}}`,
			`$ref: "./object.json" holds an object and "./list.json" holds a list`},
		{"a list then an object", `{"shell": {"$ref": ["./list.json", "./object.json"]}}`,
			`$ref: "./list.json" holds a list and "./object.json" holds an object`},
		{"a single value", `{"scripts": {"$ref": ["./single.json", "./object.json"]}}`,
			`$ref: "./single.json" holds a single value and "./object.json" holds an object`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeFile(t, dir, "dispat.json", tc.body)

			_, err := Load(path, nil)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
			assert.Contains(t, err.Error(), "the files of one $ref must all hold objects, or all hold lists")
		})
	}
}

// TestRefListSiblingKeysOverrideTheMerge: the keys beside a reference are the
// nearest layer of all, so they settle what the files merged to. They still
// need an object to override, and a merge that produced a list says so.
func TestRefListSiblingKeysOverrideTheMerge(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "base.yaml", "path: pkgs\nversioning: fixed\nflow:\n  build:\n    - build\n")
	writeFile(t, dir, "extra.json", `{"versioning": "independent"}`)
	writeFile(t, dir, "one.json", `["/bin/bash"]`)
	writeFile(t, dir, "two.json", `["-c"]`)

	path := writeFile(t, dir, "dispat.json", `{
		"scripts": {"build": "echo b"},
		"spaces": {"libs": {"$ref": ["./base.yaml", "./extra.json"], "Versioning": "fixed"}}
	}`)
	cfg, err := Load(path, nil)
	require.NoError(t, err)
	assert.Equal(t, "fixed", cfg.Spaces["libs"].Versioning, "the sibling outranks every file the $ref named")
	assert.Equal(t, PathList{"pkgs"}, cfg.Spaces["libs"].Path, "everything it did not override survives the merge")

	list := writeFile(t, dir, "dispat.yaml", `{"shell": {"$ref": ["./one.json", "./two.json"], "extra": 1}}`)
	_, err = Load(list, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(),
		"$ref: the files merge to a list, so the keys beside the $ref have nothing to override")
}

// TestRefListFailures: what a list of files can get wrong, each named where it
// was written. A file the list names is read exactly as a single file is, so
// its own refusals arrive unchanged.
func TestRefListFailures(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct{ name, body, want string }{
		{"no files at all", `{"env": {"$ref": []}}`,
			"env: $ref names no files: the list must hold at least one path"},
		{"a file that is not named", `{"env": {"$ref": ["./env.yaml", 7]}}`,
			"env: $ref[1] must name another config file"},
		{"a name that is blank", `{"env": {"$ref": ["./env.yaml", "  "]}}`,
			"env: $ref[1] must name another config file"},
		{"neither a file nor a list", `{"env": {"$ref": 7}}`,
			"env: $ref must name another config file, or a list of them"},
		{"a file that is missing", `{"env": {"$ref": ["./env.yaml", "./absent.json"]}}`,
			`env: $ref "./absent.json": cannot read`},
		{"a file that is empty", `{"env": {"$ref": ["./env.yaml", "./empty.yaml"]}}`,
			`env: $ref "./empty.yaml": the file is empty, so the key would have no value`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeFile(t, dir, "env.yaml", "MiXed: v\n")
			writeFile(t, dir, "empty.yaml", "")
			path := writeFile(t, dir, "dispat.json", tc.body)

			_, err := Load(path, nil)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

// TestRefListCycleNamesThePathThatClosedIt: every file a reference names is
// followed on its own, so a cycle through the second of them is the cycle it
// is, reported by the way it got there.
func TestRefListCycleNamesThePathThatClosedIt(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "first.yaml", "MiXed: v\n")
	writeFile(t, dir, "second.json", `{"deeper": {"$ref": "./dispat.json"}}`)
	path := writeFile(t, dir, "dispat.json", `{"env": {"$ref": ["./first.yaml", "./second.json"]}}`)

	_, err := Load(path, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "$ref cycle: "+path+" (env) -> "+
		filepath.Join(dir, "second.json")+" (deeper) -> "+path)
}

// TestRefListMergesFilesThatSplitFurther: a file a list names may be split
// itself. Each is resolved whole before it is merged, so a fragment's own
// references are followed against its own folder and a shared sub-fragment
// used by two of them is read for each, as it is anywhere else.
func TestRefListMergesFilesThatSplitFurther(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "cfg/base.json", `{"libs": {"path": "pkgs", "flow": {"$ref": "./flow.yaml"}}}`)
	writeFile(t, dir, "cfg/extra.json", `{"apps": {"path": "apps", "flow": {"$ref": "./flow.yaml"}}}`)
	writeFile(t, dir, "cfg/flow.yaml", "build:\n  - build\n")
	path := writeFile(t, dir, "dispat.json", `{
		"scripts": {"build": "echo b"},
		"spaces": {"$ref": ["./cfg/base.json", "./cfg/extra.json"]}
	}`)

	cfg, err := Load(path, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"build"}, cfg.Spaces["libs"].Flow.Build)
	assert.Equal(t, []string{"build"}, cfg.Spaces["apps"].Flow.Build)
	assert.Equal(t, []string{
		path,
		filepath.Join(dir, "cfg", "base.json"),
		filepath.Join(dir, "cfg", "flow.yaml"),
		filepath.Join(dir, "cfg", "extra.json"),
		filepath.Join(dir, "cfg", "flow.yaml"),
	}, cfg.SourceFiles, "each fragment is resolved whole before the next is read")
}

// TestRefListResolvesEachAgainstItsOwnFolder: the files of one reference may
// live in different folders, and each is resolved against the file that named
// it, as every reference is.
func TestRefListResolvesEachAgainstItsOwnFolder(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "cfg/spaces.json", `{"$ref": ["./base.json", "../overlay.json"]}`)
	writeFile(t, dir, "cfg/base.json", `{"libs": {"path": "pkgs", "flow": {"build": ["build"]}}}`)
	writeFile(t, dir, "overlay.json", `{"apps": {"path": "apps", "flow": {"build": ["build"]}}}`)
	path := writeFile(t, dir, "dispat.json", `{
		"scripts": {"build": "echo b"},
		"spaces": {"$ref": "./cfg/spaces.json"}
	}`)

	cfg, err := Load(path, nil)
	require.NoError(t, err)
	assert.Equal(t, PathList{"pkgs"}, cfg.Spaces["libs"].Path)
	assert.Equal(t, PathList{"apps"}, cfg.Spaces["apps"].Path)
}

// TestRefKeepsPathsRepositoryRelative: a reference moves text, not meaning. A
// space's path written in a fragment one folder down still points where it
// would have pointed inline, because it is the monorepo root it is resolved
// against.
func TestRefKeepsPathsRepositoryRelative(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "cfg/spaces.json", `{"libs": {"path": "pkgs", "flow": {"build": ["build"]}}}`)
	path := writeFile(t, dir, "dispat.json", `{
		"scripts": {"build": "echo b"},
		"spaces": {"$ref": "./cfg/spaces.json"}
	}`)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "pkgs", "core"), 0o755))

	cfg, err := Load(path, nil)
	require.NoError(t, err)
	pkgs, _, _, err := Discover(cfg, dir)
	require.NoError(t, err)
	require.Len(t, pkgs, 1)
	assert.Equal(t, filepath.Join(dir, "pkgs", "core"), pkgs[0].Dir)
}

// TestRefInFolderConfigFiles: a space folder's file and a package folder's file
// are read the same way the root config is, so they may be split too.
func TestRefInFolderConfigFiles(t *testing.T) {
	cfg := minimalConfig()
	root := writeModelRepo(t, cfg, "pkgs/core")
	writeFile(t, root, "pkgs/env.yaml", "SPACE_KEY: s\n")
	writeFile(t, root, "pkgs/dispat.json", `{"env": {"$ref": "./env.yaml"}}`)
	writeFile(t, root, "pkgs/core/scripts.json", `{"build": "echo core"}`)
	writeFile(t, root, "pkgs/core/dispat.json", `{"scripts": {"$ref": "./scripts.json"}}`)

	loaded, err := Load(filepath.Join(root, "dispat.json"), nil)
	require.NoError(t, err)
	pkgs, _, _, err := Discover(loaded, root)
	require.NoError(t, err)
	require.Len(t, pkgs, 1)
	assert.Equal(t, []string{"SPACE_KEY=s"}, pkgs[0].Space.Env,
		"the space folder's referenced env, with its case")
	assert.Equal(t, Script{"echo core"}, pkgs[0].Space.Scripts["build"],
		"the package folder's referenced scripts")
}

// TestRefRefusalsSurviveAReference: a folder file may not declare what only a
// monorepo root may, and writing it in a referenced fragment is the same
// mistake, refused the same way.
func TestRefRefusalsSurviveAReference(t *testing.T) {
	cfg := minimalConfig()
	root := writeModelRepo(t, cfg, "pkgs/core")
	writeFile(t, root, "pkgs/nested.json", `{"other": {"path": "elsewhere"}}`)
	writeFile(t, root, "pkgs/dispat.json", `{"spaces": {"$ref": "./nested.json"}}`)

	loaded, err := Load(filepath.Join(root, "dispat.json"), nil)
	require.NoError(t, err)
	_, _, _, err = Discover(loaded, root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "declares spaces")
}

// TestCloneValueCopiesEveryContainer: the parsers in use produce string-keyed
// maps, generic maps and lists, the flag overlay adds a list of strings, and
// every one of them is copied rather than shared. A value of any other type is
// passed through, which is what the clone now says instead of reflecting over
// containers nothing builds.
func TestCloneValueCopiesEveryContainer(t *testing.T) {
	original := map[string]any{
		"generic": map[any]any{1: map[string]any{"deep": "v"}},
		"list":    []any{map[string]any{"deep": "v"}},
		"strings": []string{"one"},
		"nils":    []any{nil},
		"scalar":  "v",
	}
	clone := cloneTree(original)
	require.Equal(t, original, clone)

	clone["generic"].(map[any]any)[1].(map[string]any)["deep"] = "changed"
	clone["list"].([]any)[0].(map[string]any)["deep"] = "changed"
	clone["strings"].([]string)[0] = "changed"

	assert.Equal(t, "v", original["generic"].(map[any]any)[1].(map[string]any)["deep"])
	assert.Equal(t, "v", original["list"].([]any)[0].(map[string]any)["deep"])
	assert.Equal(t, "one", original["strings"].([]string)[0])
}

// TestRefEmptyFileInEveryFormat: "this file is empty" is one answer across the
// three formats, even though TOML spells an empty document as an empty table
// and the other two as no value at all.
func TestRefEmptyFileInEveryFormat(t *testing.T) {
	for _, name := range []string{"empty.json", "empty.yaml", "empty.toml"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			body := ""
			if name == "empty.json" {
				body = "null" // an empty .json file is not a document at all
			}
			writeFile(t, dir, name, body)
			path := writeFile(t, dir, "dispat.json", `{"env": {"$ref": "./`+name+`"}}`)

			_, err := Load(path, nil)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "the file is empty, so the key would have no value")
		})
	}
}

// TestResolveEditNestingIsCapped: the loader refuses a cycle long before an
// edit is collected, so the writer's own bound only has to stop rather than
// explain. It counts the references followed, not the keys walked.
func TestResolveEditNestingIsCapped(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i <= maxRefDepth+1; i++ {
		writeFile(t, dir, fmt.Sprintf("f%d.json", i), fmt.Sprintf(`{"next": {"$ref": "./f%d.json"}}`, i+1))
	}
	path := writeFile(t, dir, "dispat.json", `{"packages": {"$ref": "./f0.json"}}`)

	// Every key of the path crosses one more reference, which is the only way
	// to nest this far: a key path dispat itself builds is three keys long.
	keyPath := []string{"packages"}
	for i := 0; i <= maxRefDepth+1; i++ {
		keyPath = append(keyPath, "next")
	}
	_, _, err := ResolveEdit(path, keyPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "$ref nesting is more than 32 files deep")

	// A path of many keys through one file is not nesting, and is not capped.
	deep := map[string]any{}
	node := deep
	keys := make([]string, 0, maxRefDepth+2)
	for i := 0; i <= maxRefDepth+1; i++ {
		key := fmt.Sprintf("k%d", i)
		child := map[string]any{}
		node[key] = child
		node = child
		keys = append(keys, key)
	}
	writeJSON(t, filepath.Join(dir, "deep.json"), deep)
	file, inner, err := ResolveEdit(filepath.Join(dir, "deep.json"), keys)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "deep.json"), file)
	assert.Equal(t, keys, inner)
}
