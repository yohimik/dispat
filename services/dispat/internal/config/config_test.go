package config

// Configs are authored as typed models (config aliases the public pkg/models
// structs) and marshalled to JSON — one format everywhere, because the file
// formats themselves are viper's concern, smoke-tested once in
// TestLoadFormats. Raw shapes appear only where a marshaller cannot express
// what the test needs: an unknown key, and the weak-typing scalars (a bare
// `concurrency: 3`, a scalar script reference) that the typed slices cannot
// produce.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	toml "github.com/pelletier/go-toml/v2"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/ccme"
	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/services/dispat/internal/model"
	yaml "gopkg.in/yaml.v3"
)

// validConfig is the canonical valid configuration the option tests start
// from: two spaces, a dependency edge, one shared concurrency value.
func validConfig() File {
	return File{
		Scripts: map[string]string{"build": "echo build", "publish": "echo publish"},
		Spaces: map[string]SpaceConfig{
			"libs": {Path: "packages/libs", IsBuildWaitingPublish: true, RevertOnFail: true,
				Flow: &SpaceFlowConfig{Build: []string{"build"}, Publish: []string{"publish"}}},
			"apps": {Path: "packages/apps",
				Flow: &SpaceFlowConfig{Build: []string{"build"}, Publish: []string{"publish"}}},
		},
		Dependencies: []DependencyConfig{{Consumer: "app", Provider: "core"}},
		Concurrency:  []int{3},
		LogLevel:     "info",
		LogFormat:    "pretty",
	}
}

// minimalConfig is the smallest valid configuration: one space, one script.
// Run is pre-allocated so tests can set hooks on it directly; an empty object
// loads the same as an absent one.
func minimalConfig() File {
	return File{
		Scripts: map[string]string{"build": "echo b"},
		Spaces: map[string]SpaceConfig{
			"libs": {Path: "pkgs", Flow: &SpaceFlowConfig{Build: []string{"build"}}},
		},
		Run: &RunConfig{},
	}
}

// withLibs mutates the "libs" space of a config in place — the map-entry
// copy-out/copy-back that Go requires, hoisted out of every test that tweaks
// one field of the space.
func withLibs(cfg *File, mutate func(*SpaceConfig)) {
	libs := cfg.Spaces["libs"]
	mutate(&libs)
	cfg.Spaces["libs"] = libs
}

// writeModelRepo lays out a fake monorepo whose dispat.json is the marshalled
// model, and returns its root.
func writeModelRepo(t *testing.T, cfg File, pkgDirs ...string) string {
	t.Helper()
	data, err := json.MarshalIndent(cfg, "", "  ")
	require.NoError(t, err)
	return writeRawFile(t, string(data), pkgDirs...)
}

// writeRawRepo is writeModelRepo for the shapes the model cannot express (an
// unknown key, a weak-typed scalar): the config is a raw map, still written
// through the JSON marshaller rather than as a hand-formatted string.
func writeRawRepo(t *testing.T, cfg map[string]any, pkgDirs ...string) string {
	t.Helper()
	data, err := json.MarshalIndent(cfg, "", "  ")
	require.NoError(t, err)
	return writeRawFile(t, string(data), pkgDirs...)
}

func writeRawFile(t *testing.T, cfgJSON string, pkgDirs ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, d := range pkgDirs {
		require.NoError(t, os.MkdirAll(filepath.Join(root, d), 0o755))
	}
	require.NoError(t, os.WriteFile(filepath.Join(root, "dispat.json"), []byte(cfgJSON), 0o644))
	return root
}

// loadModel writes the model and loads it back, returning the outcome.
func loadModel(t *testing.T, cfg File, pkgDirs ...string) (*File, error) {
	t.Helper()
	root := writeModelRepo(t, cfg, pkgDirs...)
	return Load(filepath.Join(root, "dispat.json"), nil)
}

func TestLoadValid(t *testing.T) {
	cfg, err := loadModel(t, validConfig(), "packages/libs/core", "packages/apps/app")
	require.NoError(t, err)
	assert.Equal(t, 3, cfg.BuildConcurrency, "single value applies to build")
	assert.Equal(t, 3, cfg.PublishConcurrency, "single value applies to publish")
	assert.True(t, cfg.Spaces["libs"].IsBuildWaitingPublish)
	assert.True(t, cfg.Spaces["libs"].RevertOnFail)
	assert.False(t, cfg.Spaces["apps"].RevertOnFail, "revertOnFail defaults to false")
}

// TestLoadFormats smoke-tests each supported file format once: the same
// model-authored configuration must load identically from JSON, YAML and
// TOML. The YAML and TOML bodies are produced by real marshallers over the
// model's own key spellings (model -> JSON -> generic map -> format), so the
// test never hand-writes config text; everything beyond this — options,
// validation, errors — is exercised through JSON alone, because the formats
// are viper's concern, not this package's.
func TestLoadFormats(t *testing.T) {
	cfg := validConfig()
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
			require.NoError(t, os.MkdirAll(filepath.Join(root, "packages", "libs", "core"), 0o755))
			require.NoError(t, os.MkdirAll(filepath.Join(root, "packages", "apps", "app"), 0o755))
			path := filepath.Join(root, "dispat."+format)
			require.NoError(t, os.WriteFile(path, data, 0o644))

			loaded, err := Load(path, nil)
			require.NoError(t, err)
			assert.Equal(t, 3, loaded.BuildConcurrency)
			assert.True(t, loaded.Spaces["libs"].IsBuildWaitingPublish)
			assert.Equal(t, []string{"echo build"}, loaded.Commands(loaded.Spaces["libs"].Flow.Build))

			pkgs, _, err := Discover(loaded, root)
			require.NoError(t, err)
			names := make([]string, 0, len(pkgs))
			for _, p := range pkgs {
				names = append(names, p.Name)
			}
			assert.ElementsMatch(t, []string{"core", "app"}, names)
		})
	}
}

func TestLoadConcurrencyPair(t *testing.T) {
	cfg := minimalConfig()
	cfg.Concurrency = []int{4, 2}
	loaded, err := loadModel(t, cfg, "pkgs/core")
	require.NoError(t, err)
	assert.Equal(t, 4, loaded.BuildConcurrency)
	assert.Equal(t, 2, loaded.PublishConcurrency)
}

func TestLoadConcurrencyScalarWeakTyping(t *testing.T) {
	// `concurrency: 3` — a bare scalar rather than a list — lifts into the
	// slice through weak decoding. The model's []int cannot marshal a scalar,
	// so this is one of the raw-map shapes.
	root := writeRawRepo(t, map[string]any{
		"scripts": map[string]any{"b": "echo b"},
		"spaces": map[string]any{
			"libs": map[string]any{"path": "pkgs", "flow": map[string]any{"build": "b"}},
		},
		"concurrency": 3,
	}, "pkgs/core")
	cfg, err := Load(filepath.Join(root, "dispat.json"), nil)
	require.NoError(t, err)
	assert.Equal(t, 3, cfg.BuildConcurrency, "the scalar applies to build")
	assert.Equal(t, 3, cfg.PublishConcurrency, "and to publish")
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := loadModel(t, minimalConfig(), "pkgs/core")
	require.NoError(t, err)
	assert.GreaterOrEqual(t, cfg.BuildConcurrency, 1, "default build concurrency")
	assert.GreaterOrEqual(t, cfg.PublishConcurrency, 1, "default publish concurrency")
	assert.Equal(t, "info", cfg.LogLevel, "default logLevel")
	assert.Equal(t, "pretty", cfg.LogFormat, "default logFormat")
	assert.Equal(t, CommitErrorsWarn, cfg.CommitErrors,
		"§16 default: a unit-scoped error invalidates the unit, not the run")
	assert.Equal(t, []string{"release"}, cfg.NonPackageScopes,
		"dispat's own release-commit scope is exempt by default")
	assert.Equal(t, "{name}@{version}", cfg.TagFormat, "default tag format")
}

func TestLoadCommitErrors(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  string
		ok    bool
	}{
		{"warn", CommitErrorsWarn, true},
		{"error", CommitErrorsError, true},
		{"fatal", "", false},
	} {
		cfg := minimalConfig()
		cfg.CommitErrors = tc.value
		loaded, err := loadModel(t, cfg, "pkgs/core")
		if !tc.ok {
			require.Error(t, err, "commitErrors: %s", tc.value)
			assert.Contains(t, err.Error(), "commitErrors")
			continue
		}
		require.NoError(t, err, "commitErrors: %s", tc.value)
		assert.Equal(t, tc.want, loaded.CommitErrors)
	}
}

func TestLoadNonPackageScopes(t *testing.T) {
	cfg := minimalConfig()
	cfg.NonPackageScopes = []string{"release", "deps"}
	loaded, err := loadModel(t, cfg, "pkgs/core")
	require.NoError(t, err)
	assert.Equal(t, []string{"release", "deps"}, loaded.NonPackageScopes)
}

func TestLoadTagFormatPerSpace(t *testing.T) {
	cfg := File{
		Scripts:   map[string]string{"build": "echo b"},
		TagFormat: "{name}@v{version}",
		Spaces: map[string]SpaceConfig{
			"libs":     {Path: "pkgs", Flow: &SpaceFlowConfig{Build: []string{"build"}}},
			"services": {Path: "svc", Flow: &SpaceFlowConfig{Build: []string{"build"}}, TagFormat: "services/{name}@v{version}"},
		},
	}
	root := writeModelRepo(t, cfg, "pkgs/core", "svc/api")
	loaded, err := Load(filepath.Join(root, "dispat.json"), nil)
	require.NoError(t, err)

	pkgs, _, err := Discover(loaded, root)
	require.NoError(t, err)

	byName := map[string]string{}
	for _, p := range pkgs {
		byName[p.Name] = p.Space.TagFormat
	}
	assert.Equal(t, "{name}@v{version}", byName["core"], "a space with no format inherits the repository's")
	assert.Equal(t, "services/{name}@v{version}", byName["api"], "and may override it")
}

func TestLoadTagFormatInvalid(t *testing.T) {
	repoLevel := minimalConfig()
	repoLevel.TagFormat = "{name}"
	spaceLevel := minimalConfig()
	withLibs(&spaceLevel, func(s *SpaceConfig) {
		s.TagFormat = "{name}@{version}-{version}"
	})

	for _, cfg := range []File{repoLevel, spaceLevel} {
		_, err := loadModel(t, cfg, "pkgs/core")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "{version}")
	}
}

func testFlags(t *testing.T, args ...string) *pflag.FlagSet {
	t.Helper()
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	fs.IntSlice("concurrency", nil, "")
	fs.String("log-level", "", "")
	fs.String("log-format", "", "")
	require.NoError(t, fs.Parse(args))
	return fs
}

func TestLoadFlagOverrides(t *testing.T) {
	root := writeModelRepo(t, validConfig())
	cfg, err := Load(filepath.Join(root, "dispat.json"),
		testFlags(t, "--concurrency", "4,2", "--log-level", "debug", "--log-format", "json"))
	require.NoError(t, err)
	assert.Equal(t, 4, cfg.BuildConcurrency, "explicit flag overrides config")
	assert.Equal(t, 2, cfg.PublishConcurrency, "explicit flag overrides config")
	assert.Equal(t, "debug", cfg.LogLevel, "explicit flag overrides config")
	assert.Equal(t, "json", cfg.LogFormat, "explicit flag overrides config")
}

func TestLoadLogFormatJSON(t *testing.T) {
	cfg := minimalConfig()
	cfg.LogFormat = "json"
	loaded, err := loadModel(t, cfg)
	require.NoError(t, err)
	assert.Equal(t, "json", loaded.LogFormat)
}

func TestLoadFlagSingleValue(t *testing.T) {
	root := writeModelRepo(t, validConfig())
	cfg, err := Load(filepath.Join(root, "dispat.json"), testFlags(t, "--concurrency", "7"))
	require.NoError(t, err)
	assert.Equal(t, 7, cfg.BuildConcurrency)
	assert.Equal(t, 7, cfg.PublishConcurrency)
}

func TestLoadFlagDefaultsDoNotOverride(t *testing.T) {
	root := writeModelRepo(t, validConfig())
	cfg, err := Load(filepath.Join(root, "dispat.json"), testFlags(t))
	require.NoError(t, err)
	assert.Equal(t, 3, cfg.BuildConcurrency, "config wins over unset flag")
	assert.Equal(t, 3, cfg.PublishConcurrency, "config wins over unset flag")
	assert.Equal(t, "info", cfg.LogLevel, "config wins over unset flag")
	assert.Equal(t, "pretty", cfg.LogFormat, "config wins over unset flag")
}

func TestLoadScriptRefsCaseInsensitive(t *testing.T) {
	// Viper lowercases map keys; mixed-case references must still resolve.
	cfg := File{
		Scripts: map[string]string{"buildAll": "echo b", "publishAll": "echo p"},
		Spaces: map[string]SpaceConfig{
			"libs": {Path: "pkgs", Flow: &SpaceFlowConfig{
				Build: []string{"buildAll"}, Publish: []string{"publishAll"}}},
		},
	}
	root := writeModelRepo(t, cfg, "pkgs/core")
	loaded, err := Load(filepath.Join(root, "dispat.json"), nil)
	require.NoError(t, err)
	pkgs, _, err := Discover(loaded, root)
	require.NoError(t, err)
	require.Len(t, pkgs, 1)
	assert.Equal(t, []string{"echo b"}, pkgs[0].Space.BuildScript)
	assert.Equal(t, []string{"echo p"}, pkgs[0].Space.PublishScript)
}

func TestLoadOptionalScripts(t *testing.T) {
	// Scripts are optional; a space may configure none, some or all of them.
	cfg := File{
		Scripts: map[string]string{"sync": "npm install"},
		Spaces: map[string]SpaceConfig{
			"libs": {Path: "pkgs", Flow: &SpaceFlowConfig{Version: []string{"sync"}}},
		},
	}
	root := writeModelRepo(t, cfg, "pkgs/core")
	loaded, err := Load(filepath.Join(root, "dispat.json"), nil)
	require.NoError(t, err)
	pkgs, _, err := Discover(loaded, root)
	require.NoError(t, err)
	require.Len(t, pkgs, 1)
	assert.Empty(t, pkgs[0].Space.BuildScript)
	assert.Empty(t, pkgs[0].Space.PublishScript)
	assert.Equal(t, []string{"npm install"}, pkgs[0].Space.VersionScript)
}

func TestLoadInitials(t *testing.T) {
	cfg := validConfig()
	cfg.Initials = map[string]string{"core": "1.2.3", "scoped-pkg": "0.9.0"}
	loaded, err := loadModel(t, cfg)
	require.NoError(t, err)
	require.Len(t, loaded.InitialVersions, 2)
	// Compare rendered versions: the fields are uint64, and an untyped
	// constant in an interface argument arrives as int.
	assert.Equal(t, "1.2.3", loaded.InitialVersions["core"].String())
	assert.Equal(t, "0.9.0", loaded.InitialVersions["scoped-pkg"].String())
}

func TestLoadInitialsInvalidVersion(t *testing.T) {
	cfg := validConfig()
	cfg.Initials = map[string]string{"core": "not-a-version"}
	_, err := loadModel(t, cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "initials")
	assert.Contains(t, err.Error(), "not-a-version")
}

func TestLoadInitialsAbsent(t *testing.T) {
	cfg, err := loadModel(t, validConfig())
	require.NoError(t, err)
	assert.Empty(t, cfg.InitialVersions)
}

func TestLoadChangelogGitHubDefaults(t *testing.T) {
	cfg, err := loadModel(t, validConfig())
	require.NoError(t, err)
	assert.True(t, cfg.Changelog.IsEnabled(), "changelog defaults to enabled")
	assert.True(t, cfg.GitHub.IsEnabled(), "github defaults to enabled")
	assert.Empty(t, cfg.Changelog.File)
	assert.Empty(t, cfg.GitHub.Owner)
}

func TestLoadChangelogOptions(t *testing.T) {
	cfg := validConfig()
	cfg.Changelog = &ChangelogConfig{
		Enabled: models.Bool(false),
		File:    "HISTORY.md",
		Title:   "# History",
		EntryFormatConfig: EntryFormatConfig{
			DateFormat:        "02.01.2006",
			BreakingTitle:     "Breaking",
			FeaturesTitle:     "Added",
			FixesTitle:        "Fixed",
			DependenciesTitle: "Bumped",
		},
	}
	loaded, err := loadModel(t, cfg)
	require.NoError(t, err)
	assert.False(t, loaded.Changelog.IsEnabled())
	assert.Equal(t, "HISTORY.md", loaded.Changelog.File)
	assert.Equal(t, "# History", loaded.Changelog.Title)
	assert.Equal(t, "02.01.2006", loaded.Changelog.DateFormat)
	assert.Equal(t, "Breaking", loaded.Changelog.BreakingTitle)
	assert.Equal(t, "Added", loaded.Changelog.FeaturesTitle)
	assert.Equal(t, "Fixed", loaded.Changelog.FixesTitle)
	assert.Equal(t, "Bumped", loaded.Changelog.DependenciesTitle)
}

func TestLoadGitHubOptions(t *testing.T) {
	cfg := validConfig()
	cfg.GitHub = &GitHubConfig{
		Enabled: models.Bool(false), Owner: "acme", Repo: "mono",
		APIURL: "https://ghe.example.com/api/v3", TokenEnv: "GH_RELEASE_TOKEN",
		EntryFormatConfig: EntryFormatConfig{FeaturesTitle: "New"},
	}
	loaded, err := loadModel(t, cfg)
	require.NoError(t, err)
	assert.False(t, loaded.GitHub.IsEnabled())
	assert.Equal(t, "acme", loaded.GitHub.Owner)
	assert.Equal(t, "mono", loaded.GitHub.Repo)
	assert.Equal(t, "https://ghe.example.com/api/v3", loaded.GitHub.APIURL)
	assert.Equal(t, "GH_RELEASE_TOKEN", loaded.GitHub.TokenEnv)
	assert.Equal(t, "New", loaded.GitHub.FeaturesTitle)
}

func TestLoadCommitDefaults(t *testing.T) {
	cfg, err := loadModel(t, validConfig())
	require.NoError(t, err)
	assert.False(t, cfg.Commit.IsEnabled(), "commit defaults to disabled")
	assert.False(t, cfg.Commit.PushEnabled(), "push defaults to disabled")
	assert.Empty(t, cfg.Shell, "shell defaults to empty (runner falls back to /bin/sh -c)")
}

func TestLoadCommitOptions(t *testing.T) {
	cfg := validConfig()
	cfg.Commit = &CommitConfig{
		Enabled:       models.Bool(true),
		MessageFormat: "release: {packages} ({tags})",
		Push:          true,
		Remote:        "upstream",
	}
	loaded, err := loadModel(t, cfg)
	require.NoError(t, err)
	assert.True(t, loaded.Commit.IsEnabled())
	assert.Equal(t, "release: {packages} ({tags})", loaded.Commit.MessageFormat)
	assert.True(t, loaded.Commit.PushEnabled())
	assert.Equal(t, "upstream", loaded.Commit.Remote)
}

func TestLoadCommitPushWithoutCommitDisabled(t *testing.T) {
	cfg := validConfig()
	cfg.Commit = &CommitConfig{Push: true}
	loaded, err := loadModel(t, cfg)
	require.NoError(t, err)
	assert.False(t, loaded.Commit.PushEnabled(), "push only applies when the commit is enabled")
}

func TestLoadShellOption(t *testing.T) {
	cfg := validConfig()
	cfg.Shell = []string{"bash", "-c"}
	loaded, err := loadModel(t, cfg)
	require.NoError(t, err)
	assert.Equal(t, []string{"bash", "-c"}, loaded.Shell)
}

func TestLoadShellEmptyInterpreter(t *testing.T) {
	cfg := validConfig()
	cfg.Shell = []string{"", "-c"}
	_, err := loadModel(t, cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "shell")
}

func TestLoadErrors(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*File)
		wantErr string
	}{
		{"no spaces", func(c *File) { c.Spaces = nil }, "at least one space"},
		{"negative concurrency", func(c *File) { c.Concurrency = []int{-1} }, "concurrency"},
		{"too many concurrency values", func(c *File) { c.Concurrency = []int{1, 2, 3} }, "at most two"},
		{"bad level", func(c *File) { c.LogLevel = "loud" }, "logLevel"},
		{"pretty is not a level", func(c *File) { c.LogLevel = "pretty" }, "logLevel"},
		{"bad format", func(c *File) { c.LogFormat = "fancy" }, "logFormat"},
		{"self dependency", func(c *File) {
			c.Dependencies = []DependencyConfig{{Consumer: "x", Provider: "x"}}
		}, "itself"},
		{"missing space path", func(c *File) {
			withLibs(c, func(s *SpaceConfig) {
				s.Path = ""
			})
		}, "path is required"},
		{"autoVersion bad manifests", func(c *File) {
			withLibs(c, func(s *SpaceConfig) { s.AutoVersion = &AutoVersionConfig{Manifests: "some"} })
		}, "manifests"},
		{"autoVersion bad kind", func(c *File) {
			withLibs(c, func(s *SpaceConfig) { s.AutoVersion = &AutoVersionConfig{Kinds: []string{"bundledDependencies"}} })
		}, "kinds"},
		{"autoVersion bad match glob", func(c *File) {
			withLibs(c, func(s *SpaceConfig) { s.AutoVersion = &AutoVersionConfig{Match: []string{"[oops"}} })
		}, "match"},
		{"autoVersion bad nameMatch", func(c *File) {
			withLibs(c, func(s *SpaceConfig) { s.AutoVersion = &AutoVersionConfig{NameMatch: "fuzzy"} })
		}, "nameMatch"},
		{"autoVersion negative syncLockConcurrency", func(c *File) {
			withLibs(c, func(s *SpaceConfig) { s.AutoVersion = &AutoVersionConfig{SyncLockConcurrency: -1} })
		}, "syncLockConcurrency"},
		{"autoVersion replace without files", func(c *File) {
			withLibs(c, func(s *SpaceConfig) {
				s.AutoVersion = &AutoVersionConfig{Replace: []AutoVersionReplaceConfig{{Find: "a", Write: "b"}}}
			})
		}, "replace[0]: files is required"},
		{"autoVersion replace with an empty glob", func(c *File) {
			withLibs(c, func(s *SpaceConfig) {
				s.AutoVersion = &AutoVersionConfig{Replace: []AutoVersionReplaceConfig{{Files: []string{""}, Find: "a", Write: "b"}}}
			})
		}, "replace[0]: files: empty glob"},
		{"autoVersion replace with a bad glob", func(c *File) {
			withLibs(c, func(s *SpaceConfig) {
				s.AutoVersion = &AutoVersionConfig{Replace: []AutoVersionReplaceConfig{{Files: []string{"[oops"}, Find: "a", Write: "b"}}}
			})
		}, "replace[0]: files: invalid pattern"},
		{"autoVersion replace without find", func(c *File) {
			withLibs(c, func(s *SpaceConfig) {
				s.AutoVersion = &AutoVersionConfig{Replace: []AutoVersionReplaceConfig{{Files: []string{"*.md"}, Write: "b"}}}
			})
		}, "replace[0]: find is required"},
		{"autoVersion replace without write", func(c *File) {
			withLibs(c, func(s *SpaceConfig) {
				s.AutoVersion = &AutoVersionConfig{Replace: []AutoVersionReplaceConfig{{Files: []string{"*.md"}, Find: "a"}}}
			})
		}, "replace[0]: write is required"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := minimalConfig()
			c.mutate(&cfg)
			_, err := loadModel(t, cfg)
			require.Error(t, err)
			assert.Contains(t, err.Error(), c.wantErr)
		})
	}
}

// setBuild rewires the minimal config's one build reference.
func setBuild(c *File, ref string) {
	libs := c.Spaces["libs"]
	libs.Flow.Build = []string{ref}
	c.Spaces["libs"] = libs
}

func TestLoadUnknownKeyRejected(t *testing.T) {
	// UnmarshalExact rejects unknown keys, catching config typos early. An
	// unknown key is the shape the typed model cannot express, so the config
	// is a raw map.
	root := writeRawRepo(t, map[string]any{
		"scripts": map[string]any{"b": "echo b"},
		"spaces": map[string]any{
			"libs": map[string]any{"path": "pkgs", "typo": 1, "flow": map[string]any{"build": "b"}},
		},
	})
	_, err := Load(filepath.Join(root, "dispat.json"), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid format")
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nope.yaml"), nil)
	assert.Error(t, err)
}

func TestResolveFile(t *testing.T) {
	root := t.TempDir()
	touch := func(name string) {
		require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte("{}"), 0o644))
	}

	// No candidate exists anywhere: the error says so and names every name
	// tried.
	_, _, err := ResolveFile(root, "dispat.json", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no dispat config file found")
	assert.Contains(t, err.Error(), "any parent directory")
	for _, name := range defaultFileNames {
		assert.Contains(t, err.Error(), name, "the error must name every candidate")
	}

	// The fallback order is json, yaml, yml, toml: creating an earlier name
	// takes precedence over every later one already present. A config found
	// in root itself keeps root as the monorepo root.
	touch("dispat.toml")
	path, resolvedRoot, err := ResolveFile(root, "dispat.json", false)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "dispat.toml"), path)
	assert.Equal(t, root, resolvedRoot)
	touch("dispat.yml")
	path, _, _ = ResolveFile(root, "dispat.json", false)
	assert.Equal(t, filepath.Join(root, "dispat.yml"), path)
	touch("dispat.yaml")
	path, _, _ = ResolveFile(root, "dispat.json", false)
	assert.Equal(t, filepath.Join(root, "dispat.yaml"), path)
	touch("dispat.json")
	path, _, _ = ResolveFile(root, "dispat.json", false)
	assert.Equal(t, filepath.Join(root, "dispat.json"), path)

	// An explicit name is used as-is — no existence check, no fallback, no
	// ascent — so a typo fails at load instead of silently loading a
	// different file.
	path, resolvedRoot, err = ResolveFile(root, "custom.yaml", true)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "custom.yaml"), path)
	assert.Equal(t, root, resolvedRoot)
}

func TestResolveFileAscendsParents(t *testing.T) {
	// The config sits at the monorepo top; resolution started from a nested
	// package folder must find it and report the top as the monorepo root —
	// what lets the CLI run from inside a package.
	top := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(top, "dispat.json"), []byte("{}"), 0o644))
	nested := filepath.Join(top, "packages", "core", "internal")
	require.NoError(t, os.MkdirAll(nested, 0o755))

	path, resolvedRoot, err := ResolveFile(nested, "dispat.json", false)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(top, "dispat.json"), path)
	assert.Equal(t, top, resolvedRoot)

	// A closer config shadows the ancestor's: the nearest one wins.
	mid := filepath.Join(top, "packages")
	require.NoError(t, os.WriteFile(filepath.Join(mid, "dispat.yaml"), []byte("{}"), 0o644))
	path, resolvedRoot, err = ResolveFile(nested, "dispat.json", false)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(mid, "dispat.yaml"), path)
	assert.Equal(t, mid, resolvedRoot)

	// An explicit --config never ascends.
	_, resolvedRoot, err = ResolveFile(nested, "dispat.json", true)
	require.NoError(t, err)
	assert.Equal(t, nested, resolvedRoot)
}

func TestDiscover(t *testing.T) {
	root := writeModelRepo(t, validConfig(),
		"packages/libs/core", "packages/libs/utils", "packages/apps/app")
	cfg, err := Load(filepath.Join(root, "dispat.json"), nil)
	require.NoError(t, err)
	pkgs, deps, err := Discover(cfg, root)
	require.NoError(t, err)
	require.Len(t, pkgs, 3)

	names := make([]string, 0, len(pkgs))
	for _, p := range pkgs {
		names = append(names, p.Name)
		require.NotNil(t, p.Space, "package %s", p.Name)
		assert.NotEmpty(t, p.Dir, "package %s", p.Name)
		assert.NotEmpty(t, p.Space.BuildScript, "package %s", p.Name)
	}
	assert.ElementsMatch(t, []string{"core", "utils", "app"}, names)

	require.Len(t, deps, 1)
	assert.Equal(t, "app", deps[0].Consumer)
	assert.Equal(t, "core", deps[0].Provider)
}

func TestDiscoverDuplicatePackage(t *testing.T) {
	root := writeModelRepo(t, validConfig(),
		"packages/libs/core", "packages/apps/core", "packages/apps/app")
	cfg, err := Load(filepath.Join(root, "dispat.json"), nil)
	require.NoError(t, err)
	_, _, err = Discover(cfg, root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unique")
}

func TestDiscoverUnknownDependency(t *testing.T) {
	root := writeModelRepo(t, validConfig(), "packages/libs/core", "packages/apps/other")
	cfg, err := Load(filepath.Join(root, "dispat.json"), nil)
	require.NoError(t, err)
	_, _, err = Discover(cfg, root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown consumer")
}

func TestLoadScriptArraysAndScalars(t *testing.T) {
	// Every script field accepts a single name or an array of names; a scalar
	// lifts into a one-element sequence and arrays keep their order. The
	// scalar spelling is a weak-typing shape the model's []string cannot
	// marshal, so this config is a raw map.
	root := writeRawRepo(t, map[string]any{
		"scripts": map[string]any{"clean": "echo clean", "compile": "echo compile", "pub": "echo pub"},
		"spaces": map[string]any{
			"libs": map[string]any{"path": "pkgs", "flow": map[string]any{
				"build":   []string{"clean", "compile"},
				"publish": "pub",
			}},
		},
	}, "pkgs/core")
	cfg, err := Load(filepath.Join(root, "dispat.json"), nil)
	require.NoError(t, err)
	pkgs, _, err := Discover(cfg, root)
	require.NoError(t, err)
	require.Len(t, pkgs, 1)
	assert.Equal(t, []string{"echo clean", "echo compile"}, pkgs[0].Space.BuildScript,
		"array form resolves in configuration order")
	assert.Equal(t, []string{"echo pub"}, pkgs[0].Space.PublishScript,
		"scalar form lifts into a one-element sequence")
}

func TestLoadLoginAndHookScripts(t *testing.T) {
	cfg := File{
		Scripts: map[string]string{"auth": "npm login", "hook": "echo hook", "build": "echo build"},
		Spaces: map[string]SpaceConfig{
			"libs": {Path: "pkgs", Flow: &SpaceFlowConfig{
				Build:          []string{"build"},
				Login:          []string{"auth"},
				Announce:       []string{"hook", "build"},
				BeforeAll:      []string{"hook"},
				BeforeVersion:  []string{"hook"},
				PostVersion:    []string{"hook", "build"},
				BeforeBuild:    []string{"hook"},
				PostBuild:      []string{"hook"},
				BeforePublish:  []string{"hook"},
				PostPublish:    []string{"hook"},
				BeforeAnnounce: []string{"hook"},
				PostAnnounce:   []string{"hook"},
				OnFail:         []string{"hook"},
				OnSkip:         []string{"hook", "build"},
			}},
		},
	}
	root := writeModelRepo(t, cfg, "pkgs/core")
	loaded, err := Load(filepath.Join(root, "dispat.json"), nil)
	require.NoError(t, err)
	pkgs, _, err := Discover(loaded, root)
	require.NoError(t, err)
	require.Len(t, pkgs, 1)
	sp := pkgs[0].Space
	assert.Equal(t, []string{"npm login"}, sp.LoginScript)
	assert.Equal(t, []string{"echo hook", "echo build"}, sp.AnnounceScript)
	assert.Equal(t, []string{"echo hook"}, sp.BeforeAnnounceScript)
	assert.Equal(t, []string{"echo hook"}, sp.PostAnnounceScript)
	assert.Equal(t, []string{"echo hook"}, sp.OnFailScript)
	assert.Equal(t, []string{"echo hook", "echo build"}, sp.OnSkipScript)
	assert.Equal(t, []string{"echo hook"}, sp.BeforeAllScript)
	assert.Equal(t, []string{"echo hook"}, sp.BeforeVersionScript)
	assert.Equal(t, []string{"echo hook", "echo build"}, sp.PostVersionScript)
	assert.Equal(t, []string{"echo hook"}, sp.BeforeBuildScript)
	assert.Equal(t, []string{"echo hook"}, sp.PostBuildScript)
	assert.Equal(t, []string{"echo hook"}, sp.BeforePublishScript)
	assert.Equal(t, []string{"echo hook"}, sp.PostPublishScript)
}

func TestLoadRunHooks(t *testing.T) {
	cfg := File{
		Scripts: map[string]string{"build": "echo b", "notify": "echo notify", "lint": "echo lint"},
		Spaces: map[string]SpaceConfig{
			"libs": {Path: "pkgs", Flow: &SpaceFlowConfig{Build: []string{"build"}}},
		},
		Run: &RunConfig{
			BeforeAll:    []string{"lint"},
			PostAll:      []string{"notify"},
			BeforeCommit: []string{"lint", "notify"},
			AfterCommit:  []string{"notify"},
			PostCommit:   []string{"notify"},
			BeforePush:   []string{"notify"},
			AfterPush:    []string{"notify"},
		},
	}
	loaded, err := loadModel(t, cfg, "pkgs/core")
	require.NoError(t, err)
	assert.Equal(t, []string{"lint"}, loaded.Run.BeforeAll)
	assert.Equal(t, []string{"notify"}, loaded.Run.PostAll)
	assert.Equal(t, []string{"lint", "notify"}, loaded.Run.BeforeCommit)
	assert.Equal(t, []string{"echo lint", "echo notify"}, loaded.Commands(loaded.Run.BeforeCommit),
		"references resolve to the named commands in order")
	assert.Equal(t, []string{"echo notify"}, loaded.Commands(loaded.Run.AfterPush))
}

// TestLoadScriptReferenceErrors covers the references that resolve at the
// repository level: the run hooks run once at the root, with no package in
// scope, so the top-level scripts are the whole scope and Load can check them.
func TestLoadScriptReferenceErrors(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*File)
		wantErr string
	}{
		{"unknown run hook script", func(c *File) {
			c.Run.PostAll = []string{"nope"}
		}, "run.postAll references unknown script"},
		{"unknown beforeAll run hook script", func(c *File) {
			c.Run.BeforeAll = []string{"nope"}
		}, "run.beforeAll references unknown script"},
		{"empty run hook reference", func(c *File) {
			c.Run.BeforePush = []string{""}
		}, "empty script reference"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := minimalConfig()
			c.mutate(&cfg)
			_, err := loadModel(t, cfg)
			require.Error(t, err)
			assert.Contains(t, err.Error(), c.wantErr)
		})
	}
}

// TestDiscoverScriptReferenceErrors covers the references that resolve in a
// package's scope — every flow entry and autoVersion.syncLock. They cannot be
// checked at load: the levels below the space, which a package may be the
// only one to define, are only known once packages are discovered.
func TestDiscoverScriptReferenceErrors(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*File)
		wantErr string
	}{
		{"unknown build script", func(c *File) { setBuild(c, "nope") },
			"flow.build references unknown script"},
		{"unknown version script", func(c *File) {
			withLibs(c, func(s *SpaceConfig) { s.Flow.Version = []string{"nope"} })
		}, "flow.version references unknown script"},
		{"unknown login script", func(c *File) {
			withLibs(c, func(s *SpaceConfig) { s.Flow.Login = []string{"nope"} })
		}, "flow.login references unknown script"},
		{"unknown hook script", func(c *File) {
			withLibs(c, func(s *SpaceConfig) { s.Flow.BeforeBuild = []string{"nope"} })
		}, "flow.beforeBuild references unknown script"},
		{"unknown script in an array", func(c *File) {
			withLibs(c, func(s *SpaceConfig) { s.Flow.Build = []string{"build", "nope"} })
		}, "flow.build references unknown script"},
		{"empty space script reference", func(c *File) { setBuild(c, "") },
			"empty script reference"},
		{"autoVersion unknown syncLock script", func(c *File) {
			withLibs(c, func(s *SpaceConfig) { s.AutoVersion = &AutoVersionConfig{SyncLock: []string{"nope"}} })
		}, "autoVersion.syncLock references unknown script"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := minimalConfig()
			c.mutate(&cfg)
			root := writeModelRepo(t, cfg, "pkgs/core")
			loaded, err := Load(filepath.Join(root, "dispat.json"), nil)
			require.NoError(t, err, "a reference is not a load-time error")
			_, _, err = Discover(loaded, root)
			require.Error(t, err)
			assert.Contains(t, err.Error(), c.wantErr)
			assert.Contains(t, err.Error(), `package "core"`,
				"the error names the package whose scope the name had to resolve in")
		})
	}
}

func TestExampleConfigsAreValid(t *testing.T) {
	// The annotated examples double as documentation; they must always load.
	for _, f := range []string{"dispat.example.json", "dispat.example.yaml"} {
		_, err := Load(filepath.Join("..", "..", f), nil)
		require.NoError(t, err, f)
	}
}

func TestLoadVersioning(t *testing.T) {
	// The three modes load and normalize case-insensitively; the default is
	// independent; an unknown value is rejected with the valid set named.
	load := func(t *testing.T, versioning string) (*File, error) {
		t.Helper()
		cfg := minimalConfig()
		withLibs(&cfg, func(s *SpaceConfig) {
			s.Versioning = versioning
		})
		return loadModel(t, cfg, "pkgs/core")
	}

	for raw, want := range map[string]string{
		"":            VersioningIndependent,
		"independent": VersioningIndependent,
		"fixed":       VersioningFixed,
		"Fixed":       VersioningFixed,
		"fixedSparse": VersioningFixedSparse,
		"fixedsparse": VersioningFixedSparse,
	} {
		cfg, err := load(t, raw)
		require.NoError(t, err, "versioning %q", raw)
		assert.Equal(t, want, cfg.Spaces["libs"].Versioning, "versioning %q", raw)
	}

	_, err := load(t, "locked")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unknown versioning "locked"`)
	assert.Contains(t, err.Error(), "fixedSparse", "the message names the valid values")
}

func TestLoadSpaceScripts(t *testing.T) {
	// A space's scripts hold shell commands, the same shape as the file's own;
	// keys are lowercased by viper and resolved case-insensitively; an empty
	// command is rejected.
	cfg := minimalConfig()
	withLibs(&cfg, func(s *SpaceConfig) {
		s.Scripts = map[string]string{"Lint": "echo linting", "test": "go test ./..."}
	})
	loaded, err := loadModel(t, cfg, "pkgs/core")
	require.NoError(t, err)

	cmd, ok := loaded.Spaces["libs"].Script("LINT")
	require.True(t, ok, "script names resolve case-insensitively")
	assert.Equal(t, "echo linting", cmd)
	_, ok = loaded.Spaces["libs"].Script("format")
	assert.False(t, ok)

	bad := minimalConfig()
	withLibs(&bad, func(s *SpaceConfig) {
		s.Scripts = map[string]string{"lint": "  "}
	})
	_, err = loadModel(t, bad, "pkgs/core")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `scripts["lint"] is empty`)
}

func TestDiscoverCarriesVersioningAndScripts(t *testing.T) {
	// The discovered space's Scripts is the package's effective map: the
	// file's scripts underneath the space's own.
	cfg := minimalConfig()
	withLibs(&cfg, func(s *SpaceConfig) {
		s.Versioning = "fixed"
		s.Scripts = map[string]string{"lint": "echo linting"}
	})
	root := writeModelRepo(t, cfg, "pkgs/core")
	loaded, err := Load(filepath.Join(root, "dispat.json"), nil)
	require.NoError(t, err)
	pkgs, _, err := Discover(loaded, root)
	require.NoError(t, err)
	require.Len(t, pkgs, 1)
	assert.Equal(t, "fixed", string(pkgs[0].Space.Versioning))
	assert.Equal(t, map[string]string{"build": "echo b", "lint": "echo linting"},
		pkgs[0].Space.Scripts)
}

// TestScriptValuesAreCheckedAtEveryLevel: one map, one set of rules. A
// nameless entry or a name bound to no command is rejected wherever it sits,
// so a package cannot smuggle in what a space may not hold.
func TestScriptValuesAreCheckedAtEveryLevel(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*File)
		wantErr string
	}{
		{"top-level empty name", func(c *File) { c.Scripts[""] = "echo nameless" },
			"empty script name"},
		{"top-level empty command", func(c *File) { c.Scripts["blank"] = "  " },
			`scripts["blank"] is empty`},
		{"space empty name", func(c *File) {
			withLibs(c, func(s *SpaceConfig) { s.Scripts = map[string]string{"": "echo nameless"} })
		}, "empty script name"},
		{"space empty command", func(c *File) {
			withLibs(c, func(s *SpaceConfig) { s.Scripts = map[string]string{"blank": "  "} })
		}, `scripts["blank"] is empty`},
		{"package empty command", func(c *File) {
			c.Packages = map[string]PackageConfig{"core": {Scripts: map[string]string{"blank": "  "}}}
		}, `scripts["blank"] is empty`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := minimalConfig()
			c.mutate(&cfg)
			root := writeModelRepo(t, cfg, "pkgs/core")
			loaded, err := Load(filepath.Join(root, "dispat.json"), nil)
			if err == nil {
				// A package's map only exists once its layers are merged.
				_, _, err = Discover(loaded, root)
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), c.wantErr)
		})
	}
}

// TestFlowResolvesThroughEveryLevel: a flow entry names a script, and the
// package it runs for is what resolves the name — its own scripts first, then
// its space's, then the file's. The same name at several levels resolves to
// the most local command.
func TestFlowResolvesThroughEveryLevel(t *testing.T) {
	cfg := minimalConfig()
	cfg.Scripts["deploy"] = "echo top"
	withLibs(&cfg, func(s *SpaceConfig) {
		s.Scripts = map[string]string{"build": "echo space-build"}
		s.Flow.Publish = []string{"deploy"}
	})
	cfg.Packages = map[string]PackageConfig{
		"core": {Scripts: map[string]string{"build": "echo core-build"}},
	}
	root := writeModelRepo(t, cfg, "pkgs/core", "pkgs/app")
	loaded, err := Load(filepath.Join(root, "dispat.json"), nil)
	require.NoError(t, err)
	pkgs, _, err := Discover(loaded, root)
	require.NoError(t, err)

	byName := map[string]*model.Package{}
	for _, p := range pkgs {
		byName[p.Name] = p
	}
	require.Len(t, byName, 2)
	assert.Equal(t, []string{"echo core-build"}, byName["core"].Space.BuildScript,
		"the package's own script wins")
	assert.Equal(t, []string{"echo space-build"}, byName["app"].Space.BuildScript,
		"the space's script wins over the file's")
	assert.Equal(t, []string{"echo top"}, byName["app"].Space.PublishScript,
		"a name no lower level defines falls through to the file's scripts")
}

// TestFlowRefNeedsThePackageScope: a name defined only in another space, or
// only in another package, was never in scope, and the error says so against
// the package whose scope failed.
func TestFlowRefNeedsThePackageScope(t *testing.T) {
	t.Run("defined only in another space", func(t *testing.T) {
		cfg := minimalConfig()
		withLibs(&cfg, func(s *SpaceConfig) { s.Flow.Publish = []string{"ship"} })
		cfg.Spaces["apps"] = SpaceConfig{Path: "apps", Flow: &SpaceFlowConfig{},
			Scripts: map[string]string{"ship": "echo shipping"}}
		root := writeModelRepo(t, cfg, "pkgs/core", "apps/web")
		loaded, err := Load(filepath.Join(root, "dispat.json"), nil)
		require.NoError(t, err)
		_, _, err = Discover(loaded, root)
		require.Error(t, err)
		assert.Contains(t, err.Error(), `package "core"`)
		assert.Contains(t, err.Error(), `flow.publish references unknown script "ship"`)
		assert.Contains(t, err.Error(), "no scripts entry in the package, its space or the top level")
	})

	t.Run("defined only in another package", func(t *testing.T) {
		cfg := minimalConfig()
		withLibs(&cfg, func(s *SpaceConfig) { s.Flow.Publish = []string{"ship"} })
		cfg.Packages = map[string]PackageConfig{
			"core": {Scripts: map[string]string{"ship": "echo shipping"}},
		}
		root := writeModelRepo(t, cfg, "pkgs/core", "pkgs/app")
		loaded, err := Load(filepath.Join(root, "dispat.json"), nil)
		require.NoError(t, err)
		_, _, err = Discover(loaded, root)
		require.Error(t, err)
		assert.Contains(t, err.Error(), `package "app"`, "core resolves it, app does not")
	})

	t.Run("every package supplies its own", func(t *testing.T) {
		// The counterpart: a space flow entry no level above defines is fine
		// as long as every package defines it, which is why the check is per
		// package and not per space.
		cfg := minimalConfig()
		withLibs(&cfg, func(s *SpaceConfig) { s.Flow.Publish = []string{"ship"} })
		cfg.Packages = map[string]PackageConfig{
			"core": {Scripts: map[string]string{"ship": "echo core-ship"}},
			"app":  {Scripts: map[string]string{"ship": "echo app-ship"}},
		}
		root := writeModelRepo(t, cfg, "pkgs/core", "pkgs/app")
		loaded, err := Load(filepath.Join(root, "dispat.json"), nil)
		require.NoError(t, err)
		pkgs, _, err := Discover(loaded, root)
		require.NoError(t, err)
		require.Len(t, pkgs, 2)
		for _, p := range pkgs {
			assert.Equal(t, []string{"echo " + p.Name + "-ship"}, p.Space.PublishScript)
		}
	})
}

// TestSyncLockResolvesThroughThePackageScope: autoVersion.syncLock is a
// script reference like any flow entry, so it resolves — and is checked — in
// the package's scope too.
func TestSyncLockResolvesThroughThePackageScope(t *testing.T) {
	cfg := minimalConfig()
	withLibs(&cfg, func(s *SpaceConfig) {
		s.Scripts = map[string]string{"tidy": "go mod tidy"}
		s.AutoVersion = &AutoVersionConfig{Enabled: models.Bool(true), SyncLock: []string{"tidy"}}
	})
	root := writeModelRepo(t, cfg, "pkgs/core")
	loaded, err := Load(filepath.Join(root, "dispat.json"), nil)
	require.NoError(t, err)
	pkgs, _, err := Discover(loaded, root)
	require.NoError(t, err)
	require.Len(t, pkgs, 1)
	require.NotNil(t, pkgs[0].Space.AutoVersion)
	assert.Equal(t, []string{"go mod tidy"}, pkgs[0].Space.AutoVersion.SyncLock)

	bad := minimalConfig()
	withLibs(&bad, func(s *SpaceConfig) {
		s.AutoVersion = &AutoVersionConfig{Enabled: models.Bool(true), SyncLock: []string{"tidy"}}
	})
	badRoot := writeModelRepo(t, bad, "pkgs/core")
	loaded, err = Load(filepath.Join(badRoot, "dispat.json"), nil)
	require.NoError(t, err)
	_, _, err = Discover(loaded, badRoot)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `autoVersion.syncLock references unknown script "tidy"`)
}

func TestDiscoverMissingSpaceFolder(t *testing.T) {
	cfg := minimalConfig()
	withLibs(&cfg, func(s *SpaceConfig) {
		s.Path = "does/not/exist"
	})
	root := writeModelRepo(t, cfg)
	loaded, err := Load(filepath.Join(root, "dispat.json"), nil)
	require.NoError(t, err, "the folder is a discovery concern, not a load one")
	_, _, err = Discover(loaded, root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `space "libs"`)
}

func TestDiscoverSkipsHiddenFoldersAndFiles(t *testing.T) {
	root := writeModelRepo(t, minimalConfig(), "pkgs/core", "pkgs/.hidden")
	require.NoError(t, os.WriteFile(filepath.Join(root, "pkgs", "notes.txt"), []byte("x"), 0o644))

	cfg, err := Load(filepath.Join(root, "dispat.json"), nil)
	require.NoError(t, err)
	pkgs, _, err := Discover(cfg, root)
	require.NoError(t, err)
	require.Len(t, pkgs, 1, "only real package folders count")
	assert.Equal(t, "core", pkgs[0].Name)
}

func TestDiscoverUnknownProvider(t *testing.T) {
	cfg := minimalConfig()
	cfg.Dependencies = []DependencyConfig{{Consumer: "core", Provider: "ghost"}}
	root := writeModelRepo(t, cfg, "pkgs/core")
	loaded, err := Load(filepath.Join(root, "dispat.json"), nil)
	require.NoError(t, err)
	_, _, err = Discover(loaded, root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unknown provider package "ghost"`)
}

func TestLoadParserDefaults(t *testing.T) {
	// No parser object: the resolved config is the zero ccme.Config, which
	// ccme documents as the specification defaults — the parser dispat always
	// built.
	cfg, err := loadModel(t, minimalConfig(), "pkgs/core")
	require.NoError(t, err)
	assert.Equal(t, ccme.Config{}, cfg.ResolvedParser)
}

func TestLoadParserOptions(t *testing.T) {
	// Every knob maps onto the ccme configuration, with weak decoding letting
	// a numeric depth stand next to "all".
	cfg := minimalConfig()
	cfg.Parser = &ParserConfig{
		Separator:            "%%%",
		Types:                map[string]string{"feat": "minor", "fix": "patch", "docs": "patch"},
		StrictTypes:          true,
		Lenient:              true,
		MaxDescriptionLength: 72,
		Propagation: &ParserPropagationConfig{
			Bump:         "minor",
			Depth:        "1",
			ChannelDepth: "all",
			Kinds:        []string{"dependencies", "peerDependencies"},
			Channel:      "inherit",
		},
		Limits:          &ParserLimitsConfig{UnitsPerMessage: 8, ScopeTermsPerUnit: 16, MessageBytes: 65536},
		AllowedChannels: []string{"beta", "rc"},
	}
	loaded, err := loadModel(t, cfg, "pkgs/core")
	require.NoError(t, err)

	pc := loaded.ResolvedParser
	assert.Equal(t, "%%%", pc.Separator)
	assert.Equal(t, map[string]ccme.Bump{"feat": ccme.BumpMinor, "fix": ccme.BumpPatch, "docs": ccme.BumpPatch}, pc.Types)
	assert.True(t, pc.StrictTypes)
	assert.True(t, pc.Lenient)
	assert.Equal(t, 72, pc.MaxDescriptionLength)
	assert.Equal(t, ccme.PropagateMinor, pc.Propagation.Bump)
	assert.Equal(t, ccme.Depth(1), pc.Propagation.Depth)
	assert.Equal(t, ccme.DepthAll, pc.Propagation.ChannelDepth)
	assert.Equal(t, []ccme.DependencyKind{ccme.KindDependencies, ccme.KindPeerDependencies}, pc.Propagation.Kinds)
	assert.Equal(t, ccme.ChannelInherit, pc.Propagation.Channel)
	assert.Equal(t, ccme.Limits{UnitsPerMessage: 8, ScopeTermsPerUnit: 16, MessageBytes: 65536}, pc.Limits)
	assert.Equal(t, []string{"beta", "rc"}, pc.AllowedChannels)
}

func TestLoadParserInvalidValues(t *testing.T) {
	cases := []struct {
		name    string
		parser  ParserConfig
		wantErr string
	}{
		{"bad_type_bump", ParserConfig{Types: map[string]string{"docs": "huge"}},
			`types["docs"]: unknown bump "huge"`},
		// Viper lowercases map keys, so an uppercase name self-heals; a digit
		// survives lowercasing and must be rejected.
		{"bad_type_name", ParserConfig{Types: map[string]string{"docs2": "patch"}},
			"must consist of a-z only"},
		{"bad_prop_bump", ParserConfig{Propagation: &ParserPropagationConfig{Bump: "massive"}},
			"propagation.bump"},
		{"bad_depth", ParserConfig{Propagation: &ParserPropagationConfig{Depth: "-2"}},
			"propagation.depth"},
		{"bad_kind", ParserConfig{Propagation: &ParserPropagationConfig{Kinds: []string{"imports"}}},
			`unknown kind "imports"`},
		{"bad_separator", ParserConfig{Separator: "--"}, "at least three characters"},
		{"bad_channel", ParserConfig{AllowedChannels: []string{"latest"}}, "reserved"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := minimalConfig()
			cfg.Parser = &c.parser
			_, err := loadModel(t, cfg, "pkgs/core")
			require.Error(t, err)
			assert.Contains(t, err.Error(), c.wantErr)
		})
	}

	t.Run("unknown_option", func(t *testing.T) {
		// An unknown parser key is the unknown-key shape again: raw map.
		root := writeRawRepo(t, map[string]any{
			"scripts": map[string]any{"b": "echo b"},
			"spaces": map[string]any{
				"libs": map[string]any{"path": "pkgs", "flow": map[string]any{"build": "b"}},
			},
			"parser": map[string]any{"colour": "mauve"},
		}, "pkgs/core")
		_, err := Load(filepath.Join(root, "dispat.json"), nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid keys")
	})
}

func TestAutoVersionResolution(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		// The minimal opt-in block enables everything at its documented
		// defaults: all four kinds, root manifests only, exact name matching,
		// writeVersion on, no syncLock. ({"enabled": true} rather than {}:
		// viper prunes empty objects while flattening, so a bare {} is
		// indistinguishable from an absent key by decode time.)
		cfg := minimalConfig()
		libs := cfg.Spaces["libs"]
		libs.AutoVersion = &AutoVersionConfig{Enabled: models.Bool(true)}
		cfg.Spaces["libs"] = libs
		root := writeModelRepo(t, cfg, "pkgs/core")
		loaded, err := Load(filepath.Join(root, "dispat.json"), nil)
		require.NoError(t, err)
		pkgs, _, err := Discover(loaded, root)
		require.NoError(t, err)
		av := pkgs[0].Space.AutoVersion
		require.NotNil(t, av)
		assert.Equal(t, model.ScopeRoot, av.Manifests, "manifests defaults to root")
		assert.Empty(t, av.Replace, "no replace rules means the replacing strategy is off")
		assert.True(t, av.Reconciles())
		assert.False(t, av.NameSubstring)
		assert.True(t, av.WriteVersion, "writeVersion defaults on")
		assert.Empty(t, av.SyncLock)
		assert.Zero(t, av.SyncLockConcurrency)
		for _, k := range []model.DepKind{model.KindDependencies, model.KindDevDependencies,
			model.KindPeerDependencies, model.KindOptionalDependencies} {
			assert.True(t, av.Kinds[k], "empty kinds means all four: %s", k)
		}
		assert.Nil(t, av.Only, "empty only means every provider")
	})

	t.Run("customised", func(t *testing.T) {
		cfg := minimalConfig()
		cfg.Scripts["lock"] = "npm install --package-lock-only"
		libs := cfg.Spaces["libs"]
		libs.AutoVersion = &AutoVersionConfig{
			Manifests:           "all",
			Kinds:               []string{"dependencies", "peerDependencies"},
			Only:                []string{"core"},
			NameMatch:           "substring",
			Match:               []string{"workspace:*"},
			Range:               "exact",
			WriteVersion:        models.Bool(false),
			SyncLock:            []string{"lock"},
			SyncLockConcurrency: 2,
		}
		cfg.Spaces["libs"] = libs
		root := writeModelRepo(t, cfg, "pkgs/core")
		loaded, err := Load(filepath.Join(root, "dispat.json"), nil)
		require.NoError(t, err)
		pkgs, _, err := Discover(loaded, root)
		require.NoError(t, err)
		av := pkgs[0].Space.AutoVersion
		require.NotNil(t, av)
		assert.Equal(t, model.ScopeAll, av.Manifests)
		assert.True(t, av.NameSubstring)
		assert.False(t, av.WriteVersion)
		assert.Equal(t, []string{"workspace:*"}, av.Match)
		assert.Equal(t, "exact", av.Range)
		assert.Equal(t, []string{"npm install --package-lock-only"}, av.SyncLock,
			"syncLock names resolve through the scripts map")
		assert.Equal(t, 2, av.SyncLockConcurrency)
		assert.True(t, av.Kinds[model.KindDependencies])
		assert.True(t, av.Kinds[model.KindPeerDependencies])
		assert.False(t, av.Kinds[model.KindDevDependencies], "unlisted kinds excluded")
		assert.Equal(t, map[string]bool{"core": true}, av.Only)
	})

	t.Run("disabled_block_is_inert", func(t *testing.T) {
		// enabled:false yields no AutoVersion at all — and its `only` list is
		// not validated, because a disabled block is dormant configuration.
		cfg := minimalConfig()
		libs := cfg.Spaces["libs"]
		libs.AutoVersion = &AutoVersionConfig{
			Enabled: models.Bool(false),
			Only:    []string{"no-such-package"},
		}
		cfg.Spaces["libs"] = libs
		root := writeModelRepo(t, cfg, "pkgs/core")
		loaded, err := Load(filepath.Join(root, "dispat.json"), nil)
		require.NoError(t, err)
		pkgs, _, err := Discover(loaded, root)
		require.NoError(t, err)
		assert.Nil(t, pkgs[0].Space.AutoVersion)
	})
}

func TestCommitIncludeValidation(t *testing.T) {
	for _, tc := range []struct {
		name, path, wantErr string
	}{
		{"absolute", "/etc/passwd", "repository-relative"},
		{"escapes_root", "../outside.txt", "escapes the repository root"},
		{"empty", "", "repository-relative"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := minimalConfig()
			cfg.Commit = &CommitConfig{Enabled: models.Bool(true), Include: []string{tc.path}}
			_, err := loadModel(t, cfg, "pkgs/core")
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}

	cfg := minimalConfig()
	cfg.Commit = &CommitConfig{Enabled: models.Bool(true), Include: []string{"package-lock.json", "locks/go.work.sum"}}
	loaded, err := loadModel(t, cfg, "pkgs/core")
	require.NoError(t, err)
	assert.Equal(t, []string{"package-lock.json", "locks/go.work.sum"}, loaded.Commit.Include)
}

func TestStringKeyMap(t *testing.T) {
	m, ok := stringKeyMap(map[string]any{"a": 1})
	require.True(t, ok)
	assert.Equal(t, map[string]any{"a": 1}, m)

	m, ok = stringKeyMap(map[any]any{"a": 1, "b": "x"})
	require.True(t, ok, "YAML's map shape converts when every key is a string")
	assert.Equal(t, map[string]any{"a": 1, "b": "x"}, m)

	_, ok = stringKeyMap(map[any]any{1: "x"})
	assert.False(t, ok, "a non-string key refuses the whole map")

	_, ok = stringKeyMap([]any{"not", "a", "map"})
	assert.False(t, ok)
}

// TestAutoVersionStrategies: the two strategies resolve independently, and a
// block with neither still resolves — that is how a space asks for syncLock
// and nothing else.
func TestAutoVersionStrategies(t *testing.T) {
	resolve := func(t *testing.T, av *AutoVersionConfig) *model.AutoVersion {
		t.Helper()
		cfg := minimalConfig()
		cfg.Scripts["lock"] = "go mod tidy"
		libs := cfg.Spaces["libs"]
		libs.AutoVersion = av
		cfg.Spaces["libs"] = libs
		root := writeModelRepo(t, cfg, "pkgs/core")
		loaded, err := Load(filepath.Join(root, "dispat.json"), nil)
		require.NoError(t, err)
		pkgs, _, err := Discover(loaded, root)
		require.NoError(t, err)
		return pkgs[0].Space.AutoVersion
	}

	t.Run("replace only", func(t *testing.T) {
		av := resolve(t, &AutoVersionConfig{
			Manifests: "none",
			Replace: []AutoVersionReplaceConfig{
				{Files: []string{"*.gradle"}, Find: "{provider}:{providerPrevious}", Write: "{provider}:{providerVersion}"},
			},
		})
		require.NotNil(t, av)
		assert.Equal(t, model.ScopeNone, av.Manifests)
		require.Len(t, av.Replace, 1)
		assert.Equal(t, []string{"*.gradle"}, av.Replace[0].Files)
		assert.Equal(t, "{provider}:{providerPrevious}", av.Replace[0].Find)
		assert.Equal(t, "{provider}:{providerVersion}", av.Replace[0].Write)
		assert.True(t, av.Reconciles(), "replace rules are work to do")
	})

	t.Run("syncLock only", func(t *testing.T) {
		av := resolve(t, &AutoVersionConfig{Manifests: "none", SyncLock: []string{"lock"}})
		require.NotNil(t, av, "a block with neither strategy still resolves")
		assert.False(t, av.Reconciles())
		assert.Equal(t, []string{"go mod tidy"}, av.SyncLock)
	})

	t.Run("both", func(t *testing.T) {
		av := resolve(t, &AutoVersionConfig{
			Manifests: "all",
			Replace:   []AutoVersionReplaceConfig{{Files: []string{"README.md"}, Find: "{previous}", Write: "{version}"}},
		})
		assert.Equal(t, model.ScopeAll, av.Manifests)
		assert.Len(t, av.Replace, 1)
		assert.True(t, av.Reconciles())
	})

	t.Run("disabled", func(t *testing.T) {
		assert.Nil(t, resolve(t, &AutoVersionConfig{Enabled: models.Bool(false), Manifests: "none"}))
	})
}
