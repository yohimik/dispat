package config

// The static env objects: the layering, the refusals, and the exact-case
// re-read that exists because viper lowercases every map key it reads.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	toml "github.com/pelletier/go-toml/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	yaml "gopkg.in/yaml.v3"
)

// envOf loads the config, discovers the packages and returns the named
// package's resolved static env — the flattened, sorted pairs a script will
// receive. It is the end of the pipeline every layering test checks.
func envOf(t *testing.T, cfg File, pkg string, pkgDirs ...string) []string {
	t.Helper()
	root := writeModelRepo(t, cfg, pkgDirs...)
	loaded, err := Load(filepath.Join(root, "dispat.json"), nil)
	require.NoError(t, err)
	pkgs, _, _, err := Discover(loaded, root)
	require.NoError(t, err)
	for _, p := range pkgs {
		if p.Name == pkg {
			return p.Space.Env
		}
	}
	t.Fatalf("package %q not discovered", pkg)
	return nil
}

func TestEnvPairs(t *testing.T) {
	// Sorted, so two runs of one configuration build the same environment in
	// the same order; nil for an empty map, so an unconfigured repository adds
	// no entries at all rather than an empty slice that reads as "configured".
	assert.Nil(t, EnvPairs(nil))
	assert.Nil(t, EnvPairs(map[string]string{}))
	assert.Equal(t, []string{"A=1", "B=2", "a=3"},
		EnvPairs(map[string]string{"B": "2", "a": "3", "A": "1"}))
	// A value may hold anything, "=" included: only the first splits.
	assert.Equal(t, []string{"K=a=b"}, EnvPairs(map[string]string{"K": "a=b"}))
}

func TestMergeEnv(t *testing.T) {
	base := map[string]string{"KEEP": "base", "OVER": "base"}
	over := map[string]string{"OVER": "local", "NEW": "local"}
	merged := MergeEnv(base, over)
	assert.Equal(t, map[string]string{"KEEP": "base", "OVER": "local", "NEW": "local"}, merged)
	// The inputs are left alone: a space's map is shared by every package of
	// the space, so merging one package's layer must not reach the others.
	assert.Equal(t, map[string]string{"KEEP": "base", "OVER": "base"}, base)

	// An empty overlay means "this layer says nothing", which has to leave the
	// base as it was rather than replace it with a copy.
	assert.Equal(t, base, MergeEnv(base, nil))
	assert.Equal(t, over, MergeEnv(nil, over))
}

func TestValidateEnvRefusals(t *testing.T) {
	cases := map[string]struct {
		env  map[string]string
		want string
	}{
		"empty key":       {map[string]string{"": "v"}, "contains an empty key"},
		"equals in key":   {map[string]string{"A=B": "v"}, "must not contain '='"},
		"reserved prefix": {map[string]string{"DISPAT_VERSION": "v"}, "reserved DISPAT_ prefix"},
		"reserved lower":  {map[string]string{"dispat_version": "v"}, "reserved DISPAT_ prefix"},
		"case collision":  {map[string]string{"Path": "a", "PATH": "b"}, "collide case-insensitively"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			err := validateEnv("env", c.env)
			require.Error(t, err)
			assert.Contains(t, err.Error(), c.want)
		})
	}

	// The keys a shell can actually carry are accepted, oddities included.
	require.NoError(t, validateEnv("env", map[string]string{
		"MiXed_Case": "1", "lower": "2", "WITH_DIGITS9": "3", "EMPTY": "",
	}))
	require.NoError(t, validateEnv("env", nil))
}

// TestValidateEnvReportsTheFirstMistakeDeterministically: keys are checked in
// sorted order, so a config with several mistakes always names the same one
// and an error message is reproducible.
func TestValidateEnvReportsTheFirstMistakeDeterministically(t *testing.T) {
	env := map[string]string{"ZZZ=bad": "v", "AAA": "ok", "DISPAT_X": "v"}
	for range 20 {
		err := validateEnv("env", env)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "reserved DISPAT_ prefix",
			"DISPAT_X sorts before ZZZ=bad, so it is always the reported one")
	}
}

// TestEnvKeyCaseSurvivesEveryFormat: viper lowercases map keys, so the env
// objects are re-read with the format's own parser. All three formats the
// config loader accepts must come back with the spelling the file used.
func TestEnvKeyCaseSurvivesEveryFormat(t *testing.T) {
	cfg := minimalConfig()
	cfg.Env = map[string]string{"MiXed_Case": "root", "lower": "l", "UPPER": "u"}
	withLibs(&cfg, func(s *SpaceConfig) { s.Env = map[string]string{"SpaceKey": "s"} })

	base, err := json.Marshal(cfg)
	require.NoError(t, err)
	var tree map[string]any
	require.NoError(t, json.Unmarshal(base, &tree))

	marshallers := map[string]func() ([]byte, error){
		"json": func() ([]byte, error) { return json.MarshalIndent(cfg, "", "  ") },
		"yaml": func() ([]byte, error) { return yaml.Marshal(tree) },
		"toml": func() ([]byte, error) { return toml.Marshal(tree) },
	}
	for format, marshal := range marshallers {
		t.Run(format, func(t *testing.T) {
			data, err := marshal()
			require.NoError(t, err)
			root := t.TempDir()
			require.NoError(t, os.MkdirAll(filepath.Join(root, "pkgs", "core"), 0o755))
			path := filepath.Join(root, "dispat."+format)
			require.NoError(t, os.WriteFile(path, data, 0o644))

			loaded, err := Load(path, nil)
			require.NoError(t, err)
			assert.Equal(t, map[string]string{"MiXed_Case": "root", "lower": "l", "UPPER": "u"},
				loaded.Env, "top-level env keys keep their spelling")
			assert.Equal(t, map[string]string{"SpaceKey": "s"}, loaded.Spaces["libs"].Env,
				"a space's env keys keep theirs too")
		})
	}
}

// TestEnvValuesAreWeaklyTyped: the second parse must agree with viper's weak
// decoding, so a bare number or boolean means the same thing whichever pass
// read it. Large floats go through strconv rather than %v, which would render
// them in scientific notation the file never wrote.
func TestEnvValuesAreWeaklyTyped(t *testing.T) {
	root := writeRawRepo(t, map[string]any{
		"scripts": map[string]any{"build": "echo b"},
		"spaces": map[string]any{
			"libs": map[string]any{"path": "pkgs", "flow": map[string]any{"build": "build"}},
		},
		"env": map[string]any{
			"COUNT": 3,
			"BIG":   1234567890123,
			"RATIO": 1.5,
			"FLAG":  true,
			"NOTES": nil,
		},
	}, "pkgs/core")

	loaded, err := Load(filepath.Join(root, "dispat.json"), nil)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"COUNT": "3", "BIG": "1234567890123", "RATIO": "1.5", "FLAG": "true", "NOTES": "",
	}, loaded.Env)
}

// TestEnvLayersMergeMostLocalWins walks every layer the root config file can
// hold, including the space-nested packages entry that is easy to forget: the
// restore pass has to reach it, or its keys arrive lowercased.
func TestEnvLayersMergeMostLocalWins(t *testing.T) {
	cfg := minimalConfig()
	cfg.Env = map[string]string{"RootOnly": "root", "Shared": "root", "Deep": "root"}
	withLibs(&cfg, func(s *SpaceConfig) {
		s.Env = map[string]string{"Shared": "space", "Deep": "space"}
		s.Packages = map[string]PackageConfig{
			"core": {Env: map[string]string{"Deep": "space-entry"}},
		}
	})
	cfg.Packages = map[string]PackageConfig{
		"core": {Env: map[string]string{"Shared": "root-entry"}},
	}

	env := envOf(t, cfg, "core", "pkgs/core")
	assert.Equal(t, []string{"Deep=space-entry", "RootOnly=root", "Shared=root-entry"}, env,
		"most local wins per key, and every key keeps its spelling")
}

// TestEnvFromFolderConfigFiles: the two in-folder layers, whose env is read
// back from the file they came from rather than from the root config.
func TestEnvFromFolderConfigFiles(t *testing.T) {
	cfg := minimalConfig()
	cfg.Env = map[string]string{"Root": "root", "Shared": "root"}
	root := writeModelRepo(t, cfg, "pkgs/core")

	// The space folder's own file, and its packages entry for core.
	spaceFile := SpaceFile{
		Env:      map[string]string{"Shared": "space-file", "SpaceFile": "yes"},
		Packages: map[string]PackageConfig{"core": {Env: map[string]string{"SpaceEntry": "yes"}}},
	}
	writeJSON(t, filepath.Join(root, "pkgs", "dispat.json"), spaceFile)
	// The package folder's own file: the most local layer of all.
	writeJSON(t, filepath.Join(root, "pkgs", "core", "dispat.json"),
		PackageConfig{Env: map[string]string{"Shared": "pkg-file", "PkgFile": "yes"}})

	loaded, err := Load(filepath.Join(root, "dispat.json"), nil)
	require.NoError(t, err)
	pkgs, _, _, err := Discover(loaded, root)
	require.NoError(t, err)
	require.Len(t, pkgs, 1)
	assert.Equal(t, []string{
		"PkgFile=yes", "Root=root", "Shared=pkg-file", "SpaceEntry=yes", "SpaceFile=yes",
	}, pkgs[0].Space.Env)
}

// TestEnvRefusedAtEveryLayer: the same validation reaches every level, and
// each error names the level it came from so the operator knows where to look.
func TestEnvRefusedAtEveryLayer(t *testing.T) {
	bad := map[string]string{"DISPAT_TAG": "mine"}

	t.Run("top level", func(t *testing.T) {
		cfg := minimalConfig()
		cfg.Env = bad
		_, err := loadModel(t, cfg, "pkgs/core")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "env: key \"DISPAT_TAG\" uses the reserved")
	})

	t.Run("space", func(t *testing.T) {
		cfg := minimalConfig()
		withLibs(&cfg, func(s *SpaceConfig) { s.Env = bad })
		_, err := loadModel(t, cfg, "pkgs/core")
		require.Error(t, err)
		assert.Contains(t, err.Error(), `space "libs": env: key "DISPAT_TAG"`)
	})

	t.Run("package entry", func(t *testing.T) {
		cfg := minimalConfig()
		cfg.Packages = map[string]PackageConfig{"core": {Env: bad}}
		root := writeModelRepo(t, cfg, "pkgs/core")
		loaded, err := Load(filepath.Join(root, "dispat.json"), nil)
		require.NoError(t, err, "a package layer is validated during discovery")
		_, _, _, err = Discover(loaded, root)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "env: key \"DISPAT_TAG\"")
	})
}

// TestEnvRestorerReadsEveryLevel: a config carrying env at four levels is read
// and parsed once, and each level is a lookup in the tree that parse produced.
func TestEnvRestorerReadsEveryLevel(t *testing.T) {
	cfg := minimalConfig()
	cfg.Env = map[string]string{"A": "1"}
	withLibs(&cfg, func(s *SpaceConfig) {
		s.Env = map[string]string{"B": "2"}
		s.Packages = map[string]PackageConfig{"core": {Env: map[string]string{"C": "3"}}}
	})
	cfg.Packages = map[string]PackageConfig{"core": {Env: map[string]string{"D": "4"}}}
	root := writeModelRepo(t, cfg, "pkgs/core")

	tree, err := readTree(filepath.Join(root, "dispat.json"))
	require.NoError(t, err)
	r := envRestorerOf(tree)
	assert.Equal(t, map[string]string{"A": "1"}, r.envAt("env"))
	assert.Equal(t, map[string]string{"B": "2"}, r.envAt("spaces", "libs", "env"))
	assert.Equal(t, map[string]string{"C": "3"}, r.envAt("spaces", "libs", "packages", "core", "env"))
	assert.Equal(t, map[string]string{"D": "4"}, r.envAt("packages", "core", "env"))
	// A path that is not there, and one whose value is not an object.
	assert.Nil(t, r.envAt("nope"))
	assert.Nil(t, r.envAt("spaces", "missing", "env"))
	assert.Nil(t, r.envAt("scripts", "build", "env"))
}

// TestEnvRestorerMatchesNamesCaseInsensitively: the caller looks entries up by
// the lowercased keys viper produced, while the tree came from the raw file,
// where a space may be spelled "Libs".
func TestEnvRestorerMatchesNamesCaseInsensitively(t *testing.T) {
	root := writeRawRepo(t, map[string]any{
		"scripts": map[string]any{"build": "echo b"},
		"spaces": map[string]any{
			"Libs": map[string]any{
				"path": "pkgs",
				"flow": map[string]any{"build": "build"},
				"env":  map[string]any{"SpaceKey": "s"},
			},
		},
	}, "pkgs/core")

	loaded, err := Load(filepath.Join(root, "dispat.json"), nil)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"SpaceKey": "s"}, loaded.Spaces["libs"].Env,
		"the space is keyed lowercase but spelled Libs in the file")
}

// writeJSON marshals a model into a config file at path.
func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0o644))
}

// TestEnvWeakValuesAgreeAcrossFormats: each parser hands back its own Go type
// for a number — JSON a float64, YAML an int, TOML an int64 — so the three
// have to be rendered identically or the same configuration would export
// different values depending on the file extension.
func TestEnvWeakValuesAgreeAcrossFormats(t *testing.T) {
	tree := map[string]any{
		"scripts": map[string]any{"build": "echo b"},
		"spaces": map[string]any{
			"libs": map[string]any{"path": "pkgs", "flow": map[string]any{"build": "build"}},
		},
		"env": map[string]any{
			"COUNT": 3,
			"BIG":   1234567890123,
			"RATIO": 1.5,
			"FLAG":  true,
			"TEXT":  "plain",
		},
	}
	want := map[string]string{
		"COUNT": "3", "BIG": "1234567890123", "RATIO": "1.5", "FLAG": "true", "TEXT": "plain",
	}

	marshallers := map[string]func() ([]byte, error){
		"json": func() ([]byte, error) { return json.MarshalIndent(tree, "", "  ") },
		"yaml": func() ([]byte, error) { return yaml.Marshal(tree) },
		"toml": func() ([]byte, error) { return toml.Marshal(tree) },
	}
	for format, marshal := range marshallers {
		t.Run(format, func(t *testing.T) {
			data, err := marshal()
			require.NoError(t, err)
			root := t.TempDir()
			require.NoError(t, os.MkdirAll(filepath.Join(root, "pkgs", "core"), 0o755))
			path := filepath.Join(root, "dispat."+format)
			require.NoError(t, os.WriteFile(path, data, 0o644))

			loaded, err := Load(path, nil)
			require.NoError(t, err)
			assert.Equal(t, want, loaded.Env)
		})
	}
}

// TestWeakEnvStringFallsBackForUnexpectedShapes: a value that is not a scalar
// at all — a list, say — still has to become something rather than panic, so
// the renderer ends in a plain fallback.
func TestWeakEnvStringFallsBackForUnexpectedShapes(t *testing.T) {
	assert.Equal(t, "[a b]", weakEnvString([]any{"a", "b"}))
	assert.Equal(t, "", weakEnvString(nil))
}
