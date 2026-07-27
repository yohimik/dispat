package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const validYAML = `
scripts:
  build: "echo build"
  publish: "echo publish"
spaces:
  libs:
    path: packages/libs
    isBuildWaitingPublish: true
    revertOnFail: true
    buildScript: build
    publishScript: publish
  apps:
    path: packages/apps
    buildScript: build
    publishScript: publish
dependencies:
  - consumer: app
    provider: core
concurrency: 3
logLevel: pretty
`

// writeRepo lays out a fake monorepo and returns its root.
func writeRepo(t *testing.T, cfgYAML string, pkgDirs ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, d := range pkgDirs {
		require.NoError(t, os.MkdirAll(filepath.Join(root, d), 0o755))
	}
	require.NoError(t, os.WriteFile(filepath.Join(root, "monorel.yaml"), []byte(cfgYAML), 0o644))
	return root
}

func TestLoadValid(t *testing.T) {
	root := writeRepo(t, validYAML, "packages/libs/core", "packages/apps/app")
	cfg, err := Load(filepath.Join(root, "monorel.yaml"), nil)
	require.NoError(t, err)
	assert.Equal(t, 3, cfg.BuildConcurrency, "single value applies to build")
	assert.Equal(t, 3, cfg.PublishConcurrency, "single value applies to publish")
	assert.True(t, cfg.Spaces["libs"].IsBuildWaitingPublish)
	assert.True(t, cfg.Spaces["libs"].RevertOnFail)
	assert.False(t, cfg.Spaces["apps"].RevertOnFail, "revertOnFail defaults to false")
}

func TestLoadConcurrencyPair(t *testing.T) {
	yml := `
scripts: {build: "echo b", publish: "echo p"}
spaces:
  libs: {path: pkgs, buildScript: build, publishScript: publish}
concurrency: [4, 2]
`
	root := writeRepo(t, yml, "pkgs/core")
	cfg, err := Load(filepath.Join(root, "monorel.yaml"), nil)
	require.NoError(t, err)
	assert.Equal(t, 4, cfg.BuildConcurrency)
	assert.Equal(t, 2, cfg.PublishConcurrency)
}

func TestLoadDefaults(t *testing.T) {
	yml := `
scripts: {build: "echo b", publish: "echo p"}
spaces:
  libs: {path: pkgs, buildScript: build, publishScript: publish}
`
	root := writeRepo(t, yml, "pkgs/core")
	cfg, err := Load(filepath.Join(root, "monorel.yaml"), nil)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, cfg.BuildConcurrency, 1, "default build concurrency")
	assert.GreaterOrEqual(t, cfg.PublishConcurrency, 1, "default publish concurrency")
	assert.Equal(t, "pretty", cfg.LogLevel, "default logLevel")
}

func testFlags(t *testing.T, args ...string) *pflag.FlagSet {
	t.Helper()
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	fs.IntSlice("concurrency", nil, "")
	fs.String("log-level", "", "")
	require.NoError(t, fs.Parse(args))
	return fs
}

func TestLoadFlagOverrides(t *testing.T) {
	root := writeRepo(t, validYAML)
	cfg, err := Load(filepath.Join(root, "monorel.yaml"),
		testFlags(t, "--concurrency", "4,2", "--log-level", "debug"))
	require.NoError(t, err)
	assert.Equal(t, 4, cfg.BuildConcurrency, "explicit flag overrides config")
	assert.Equal(t, 2, cfg.PublishConcurrency, "explicit flag overrides config")
	assert.Equal(t, "debug", cfg.LogLevel, "explicit flag overrides config")
}

func TestLoadFlagSingleValue(t *testing.T) {
	root := writeRepo(t, validYAML)
	cfg, err := Load(filepath.Join(root, "monorel.yaml"), testFlags(t, "--concurrency", "7"))
	require.NoError(t, err)
	assert.Equal(t, 7, cfg.BuildConcurrency)
	assert.Equal(t, 7, cfg.PublishConcurrency)
}

func TestLoadFlagDefaultsDoNotOverride(t *testing.T) {
	root := writeRepo(t, validYAML)
	cfg, err := Load(filepath.Join(root, "monorel.yaml"), testFlags(t))
	require.NoError(t, err)
	assert.Equal(t, 3, cfg.BuildConcurrency, "config wins over unset flag")
	assert.Equal(t, 3, cfg.PublishConcurrency, "config wins over unset flag")
	assert.Equal(t, "pretty", cfg.LogLevel, "config wins over unset flag")
}

func TestLoadScriptRefsCaseInsensitive(t *testing.T) {
	// Viper lowercases map keys; references must still resolve.
	yml := `
scripts: {buildAll: "echo b", publishAll: "echo p"}
spaces:
  libs: {path: pkgs, buildScript: buildAll, publishScript: publishAll}
`
	root := writeRepo(t, yml, "pkgs/core")
	cfg, err := Load(filepath.Join(root, "monorel.yaml"), nil)
	require.NoError(t, err)
	pkgs, _, err := cfg.Discover(root)
	require.NoError(t, err)
	require.Len(t, pkgs, 1)
	assert.Equal(t, "echo b", pkgs[0].Space.BuildScript)
	assert.Equal(t, "echo p", pkgs[0].Space.PublishScript)
}

func TestLoadOptionalScripts(t *testing.T) {
	// Scripts are optional; a space may configure none, some or all of them.
	yml := `
scripts: {sync: "npm install"}
spaces:
  libs: {path: pkgs, versionScript: sync}
`
	root := writeRepo(t, yml, "pkgs/core")
	cfg, err := Load(filepath.Join(root, "monorel.yaml"), nil)
	require.NoError(t, err)
	pkgs, _, err := cfg.Discover(root)
	require.NoError(t, err)
	require.Len(t, pkgs, 1)
	assert.Empty(t, pkgs[0].Space.BuildScript)
	assert.Empty(t, pkgs[0].Space.PublishScript)
	assert.Equal(t, "npm install", pkgs[0].Space.VersionScript)
}

func TestLoadInitials(t *testing.T) {
	yml := validYAML + `
initials:
  core: 1.2.3
  legacy-pkg: "0.9.0"
`
	root := writeRepo(t, yml)
	cfg, err := Load(filepath.Join(root, "monorel.yaml"), nil)
	require.NoError(t, err)
	require.Len(t, cfg.InitialVersions, 2)
	assert.Equal(t, 1, cfg.InitialVersions["core"].Major)
	assert.Equal(t, 3, cfg.InitialVersions["core"].Patch)
	assert.Equal(t, 9, cfg.InitialVersions["legacy-pkg"].Minor)
}

func TestLoadInitialsInvalidVersion(t *testing.T) {
	yml := validYAML + `
initials:
  core: "not-a-version"
`
	root := writeRepo(t, yml)
	_, err := Load(filepath.Join(root, "monorel.yaml"), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "initials")
	assert.Contains(t, err.Error(), "not-a-version")
}

func TestLoadInitialsAbsent(t *testing.T) {
	root := writeRepo(t, validYAML)
	cfg, err := Load(filepath.Join(root, "monorel.yaml"), nil)
	require.NoError(t, err)
	assert.Empty(t, cfg.InitialVersions)
}

func TestLoadChangelogGitHubDefaults(t *testing.T) {
	root := writeRepo(t, validYAML)
	cfg, err := Load(filepath.Join(root, "monorel.yaml"), nil)
	require.NoError(t, err)
	assert.True(t, cfg.Changelog.IsEnabled(), "changelog defaults to enabled")
	assert.True(t, cfg.GitHub.IsEnabled(), "github defaults to enabled")
	assert.Empty(t, cfg.Changelog.File)
	assert.Empty(t, cfg.GitHub.Owner)
}

func TestLoadChangelogOptions(t *testing.T) {
	yml := validYAML + `
changelog:
  enabled: false
  file: HISTORY.md
  title: "# History"
  dateFormat: "02.01.2006"
  breakingTitle: "Breaking"
  featuresTitle: "Added"
  fixesTitle: "Fixed"
  dependenciesTitle: "Bumped"
`
	root := writeRepo(t, yml)
	cfg, err := Load(filepath.Join(root, "monorel.yaml"), nil)
	require.NoError(t, err)
	assert.False(t, cfg.Changelog.IsEnabled())
	assert.Equal(t, "HISTORY.md", cfg.Changelog.File)
	assert.Equal(t, "# History", cfg.Changelog.Title)
	assert.Equal(t, "02.01.2006", cfg.Changelog.DateFormat)
	assert.Equal(t, "Breaking", cfg.Changelog.BreakingTitle)
	assert.Equal(t, "Added", cfg.Changelog.FeaturesTitle)
	assert.Equal(t, "Fixed", cfg.Changelog.FixesTitle)
	assert.Equal(t, "Bumped", cfg.Changelog.DependenciesTitle)
}

func TestLoadGitHubOptions(t *testing.T) {
	yml := validYAML + `
github:
  enabled: false
  owner: acme
  repo: mono
  apiUrl: https://ghe.example.com/api/v3
  tokenEnv: GH_RELEASE_TOKEN
  featuresTitle: "New"
`
	root := writeRepo(t, yml)
	cfg, err := Load(filepath.Join(root, "monorel.yaml"), nil)
	require.NoError(t, err)
	assert.False(t, cfg.GitHub.IsEnabled())
	assert.Equal(t, "acme", cfg.GitHub.Owner)
	assert.Equal(t, "mono", cfg.GitHub.Repo)
	assert.Equal(t, "https://ghe.example.com/api/v3", cfg.GitHub.APIURL)
	assert.Equal(t, "GH_RELEASE_TOKEN", cfg.GitHub.TokenEnv)
	assert.Equal(t, "New", cfg.GitHub.FeaturesTitle)
}

func TestLoadCommitDefaults(t *testing.T) {
	root := writeRepo(t, validYAML)
	cfg, err := Load(filepath.Join(root, "monorel.yaml"), nil)
	require.NoError(t, err)
	assert.False(t, cfg.Commit.IsEnabled(), "commit defaults to disabled")
	assert.False(t, cfg.Commit.PushEnabled(), "push defaults to disabled")
	assert.Empty(t, cfg.Shell, "shell defaults to empty (runner falls back to /bin/sh -c)")
}

func TestLoadCommitOptions(t *testing.T) {
	yml := validYAML + `
commit:
  enabled: true
  messageFormat: "release: {packages} ({tags})"
  push: true
  remote: upstream
`
	root := writeRepo(t, yml)
	cfg, err := Load(filepath.Join(root, "monorel.yaml"), nil)
	require.NoError(t, err)
	assert.True(t, cfg.Commit.IsEnabled())
	assert.Equal(t, "release: {packages} ({tags})", cfg.Commit.MessageFormat)
	assert.True(t, cfg.Commit.PushEnabled())
	assert.Equal(t, "upstream", cfg.Commit.Remote)
}

func TestLoadCommitPushWithoutCommitDisabled(t *testing.T) {
	yml := validYAML + `
commit:
  push: true
`
	root := writeRepo(t, yml)
	cfg, err := Load(filepath.Join(root, "monorel.yaml"), nil)
	require.NoError(t, err)
	assert.False(t, cfg.Commit.PushEnabled(), "push only applies when the commit is enabled")
}

func TestLoadShellOption(t *testing.T) {
	yml := validYAML + `
shell: ["bash", "-c"]
`
	root := writeRepo(t, yml)
	cfg, err := Load(filepath.Join(root, "monorel.yaml"), nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"bash", "-c"}, cfg.Shell)
}

func TestLoadShellEmptyInterpreter(t *testing.T) {
	yml := validYAML + `
shell: ["", "-c"]
`
	root := writeRepo(t, yml)
	_, err := Load(filepath.Join(root, "monorel.yaml"), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "shell")
}

func TestLoadJSONConfig(t *testing.T) {
	jsonCfg := `{
  "scripts": {"build": "echo b", "publish": "echo p"},
  "spaces": {"libs": {"path": "pkgs", "buildScript": "build", "publishScript": "publish"}},
  "concurrency": [4, 2],
  "github": {"enabled": false}
}`
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "pkgs", "core"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "monorel.json"), []byte(jsonCfg), 0o644))

	cfg, err := Load(filepath.Join(root, "monorel.json"), nil)
	require.NoError(t, err)
	assert.Equal(t, 4, cfg.BuildConcurrency)
	assert.Equal(t, 2, cfg.PublishConcurrency)
	assert.False(t, cfg.GitHub.IsEnabled())
	pkgs, _, err := cfg.Discover(root)
	require.NoError(t, err)
	require.Len(t, pkgs, 1)
	assert.Equal(t, "core", pkgs[0].Name)
}

func TestLoadErrors(t *testing.T) {
	cases := []struct {
		name, yml, wantErr string
	}{
		{"unknown field", "scripts: {b: x}\nspaces: {a: {path: p, buildScript: b, publishScript: b, typo: 1}}", "invalid format"},
		{"no spaces", "scripts: {b: x}", "at least one space"},
		{"unknown script", "scripts: {b: x}\nspaces: {a: {path: p, buildScript: nope, publishScript: b}}", "unknown script"},
		{"unknown version script", "scripts: {b: x}\nspaces: {a: {path: p, versionScript: nope}}", "unknown script"},
		{"negative concurrency", "scripts: {b: x}\nspaces: {a: {path: p, buildScript: b, publishScript: b}}\nconcurrency: -1", "concurrency"},
		{"too many concurrency values", "scripts: {b: x}\nspaces: {a: {path: p, buildScript: b, publishScript: b}}\nconcurrency: [1, 2, 3]", "at most two"},
		{"bad level", "scripts: {b: x}\nspaces: {a: {path: p, buildScript: b, publishScript: b}}\nlogLevel: loud", "logLevel"},
		{"self dependency", "scripts: {b: x}\nspaces: {a: {path: p, buildScript: b, publishScript: b}}\ndependencies: [{consumer: x, provider: x}]", "itself"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := writeRepo(t, c.yml)
			_, err := Load(filepath.Join(root, "monorel.yaml"), nil)
			require.Error(t, err)
			assert.Contains(t, err.Error(), c.wantErr)
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nope.yaml"), nil)
	assert.Error(t, err)
}

func TestDiscover(t *testing.T) {
	root := writeRepo(t, validYAML,
		"packages/libs/core", "packages/libs/utils", "packages/apps/app")
	cfg, err := Load(filepath.Join(root, "monorel.yaml"), nil)
	require.NoError(t, err)
	pkgs, deps, err := cfg.Discover(root)
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
	root := writeRepo(t, validYAML,
		"packages/libs/core", "packages/apps/core", "packages/apps/app")
	cfg, err := Load(filepath.Join(root, "monorel.yaml"), nil)
	require.NoError(t, err)
	_, _, err = cfg.Discover(root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unique")
}

func TestDiscoverUnknownDependency(t *testing.T) {
	root := writeRepo(t, validYAML, "packages/libs/core", "packages/apps/other")
	cfg, err := Load(filepath.Join(root, "monorel.yaml"), nil)
	require.NoError(t, err)
	_, _, err = cfg.Discover(root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown consumer")
}
