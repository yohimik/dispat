package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/ccme"
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
    run:
      build: build
      publish: publish
  apps:
    path: packages/apps
    run:
      build: build
      publish: publish
dependencies:
  - consumer: app
    provider: core
concurrency: 3
logLevel: info
logFormat: pretty
`

// writeRepo lays out a fake monorepo and returns its root.
func writeRepo(t *testing.T, cfgYAML string, pkgDirs ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, d := range pkgDirs {
		require.NoError(t, os.MkdirAll(filepath.Join(root, d), 0o755))
	}
	require.NoError(t, os.WriteFile(filepath.Join(root, "dispat.yaml"), []byte(cfgYAML), 0o644))
	return root
}

func TestLoadValid(t *testing.T) {
	root := writeRepo(t, validYAML, "packages/libs/core", "packages/apps/app")
	cfg, err := Load(filepath.Join(root, "dispat.yaml"), nil)
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
  libs: {path: pkgs, run: {build: build, publish: publish}}
concurrency: [4, 2]
`
	root := writeRepo(t, yml, "pkgs/core")
	cfg, err := Load(filepath.Join(root, "dispat.yaml"), nil)
	require.NoError(t, err)
	assert.Equal(t, 4, cfg.BuildConcurrency)
	assert.Equal(t, 2, cfg.PublishConcurrency)
}

func TestLoadDefaults(t *testing.T) {
	yml := `
scripts: {build: "echo b", publish: "echo p"}
spaces:
  libs: {path: pkgs, run: {build: build, publish: publish}}
`
	root := writeRepo(t, yml, "pkgs/core")
	cfg, err := Load(filepath.Join(root, "dispat.yaml"), nil)
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
	base := `
scripts: {build: "echo b"}
spaces:
  libs: {path: pkgs, run: {build: build}}
commitErrors: %s
`
	for _, tc := range []struct {
		value string
		want  string
		ok    bool
	}{
		{`"warn"`, CommitErrorsWarn, true},
		{`"error"`, CommitErrorsError, true},
		{`"fatal"`, "", false},
	} {
		root := writeRepo(t, fmt.Sprintf(base, tc.value), "pkgs/core")
		cfg, err := Load(filepath.Join(root, "dispat.yaml"), nil)
		if !tc.ok {
			require.Error(t, err, "commitErrors: %s", tc.value)
			assert.Contains(t, err.Error(), "commitErrors")
			continue
		}
		require.NoError(t, err, "commitErrors: %s", tc.value)
		assert.Equal(t, tc.want, cfg.CommitErrors)
	}
}

func TestLoadNonPackageScopes(t *testing.T) {
	yml := `
scripts: {build: "echo b"}
spaces:
  libs: {path: pkgs, run: {build: build}}
nonPackageScopes: [release, deps]
`
	root := writeRepo(t, yml, "pkgs/core")
	cfg, err := Load(filepath.Join(root, "dispat.yaml"), nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"release", "deps"}, cfg.NonPackageScopes)
}

func TestLoadTagFormatPerSpace(t *testing.T) {
	yml := `
scripts: {build: "echo b"}
tagFormat: "{name}@v{version}"
spaces:
  libs: {path: pkgs, run: {build: build}}
  services: {path: svc, run: {build: build}, tagFormat: "services/{name}@v{version}"}
`
	root := writeRepo(t, yml, "pkgs/core", "svc/api")
	cfg, err := Load(filepath.Join(root, "dispat.yaml"), nil)
	require.NoError(t, err)

	pkgs, _, err := Discover(cfg, root)
	require.NoError(t, err)

	byName := map[string]string{}
	for _, p := range pkgs {
		byName[p.Name] = p.Space.TagFormat
	}
	assert.Equal(t, "{name}@v{version}", byName["core"], "a space with no format inherits the repository's")
	assert.Equal(t, "services/{name}@v{version}", byName["api"], "and may override it")
}

func TestLoadTagFormatInvalid(t *testing.T) {
	for _, yml := range []string{
		`
scripts: {build: "echo b"}
tagFormat: "{name}"
spaces:
  libs: {path: pkgs, run: {build: build}}
`,
		`
scripts: {build: "echo b"}
spaces:
  libs: {path: pkgs, run: {build: build}, tagFormat: "{name}@{version}-{version}"}
`,
	} {
		root := writeRepo(t, yml, "pkgs/core")
		_, err := Load(filepath.Join(root, "dispat.yaml"), nil)
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
	root := writeRepo(t, validYAML)
	cfg, err := Load(filepath.Join(root, "dispat.yaml"),
		testFlags(t, "--concurrency", "4,2", "--log-level", "debug", "--log-format", "json"))
	require.NoError(t, err)
	assert.Equal(t, 4, cfg.BuildConcurrency, "explicit flag overrides config")
	assert.Equal(t, 2, cfg.PublishConcurrency, "explicit flag overrides config")
	assert.Equal(t, "debug", cfg.LogLevel, "explicit flag overrides config")
	assert.Equal(t, "json", cfg.LogFormat, "explicit flag overrides config")
}

func TestLoadLogFormatJSON(t *testing.T) {
	yml := `
scripts: {b: x}
spaces: {a: {path: p, run: {build: b, publish: b}}}
logFormat: json
`
	root := writeRepo(t, yml)
	cfg, err := Load(filepath.Join(root, "dispat.yaml"), nil)
	require.NoError(t, err)
	assert.Equal(t, "json", cfg.LogFormat)
}

func TestLoadFlagSingleValue(t *testing.T) {
	root := writeRepo(t, validYAML)
	cfg, err := Load(filepath.Join(root, "dispat.yaml"), testFlags(t, "--concurrency", "7"))
	require.NoError(t, err)
	assert.Equal(t, 7, cfg.BuildConcurrency)
	assert.Equal(t, 7, cfg.PublishConcurrency)
}

func TestLoadFlagDefaultsDoNotOverride(t *testing.T) {
	root := writeRepo(t, validYAML)
	cfg, err := Load(filepath.Join(root, "dispat.yaml"), testFlags(t))
	require.NoError(t, err)
	assert.Equal(t, 3, cfg.BuildConcurrency, "config wins over unset flag")
	assert.Equal(t, 3, cfg.PublishConcurrency, "config wins over unset flag")
	assert.Equal(t, "info", cfg.LogLevel, "config wins over unset flag")
	assert.Equal(t, "pretty", cfg.LogFormat, "config wins over unset flag")
}

func TestLoadScriptRefsCaseInsensitive(t *testing.T) {
	// Viper lowercases map keys; references must still resolve.
	yml := `
scripts: {buildAll: "echo b", publishAll: "echo p"}
spaces:
  libs: {path: pkgs, run: {build: buildAll, publish: publishAll}}
`
	root := writeRepo(t, yml, "pkgs/core")
	cfg, err := Load(filepath.Join(root, "dispat.yaml"), nil)
	require.NoError(t, err)
	pkgs, _, err := Discover(cfg, root)
	require.NoError(t, err)
	require.Len(t, pkgs, 1)
	assert.Equal(t, []string{"echo b"}, pkgs[0].Space.BuildScript)
	assert.Equal(t, []string{"echo p"}, pkgs[0].Space.PublishScript)
}

func TestLoadOptionalScripts(t *testing.T) {
	// Scripts are optional; a space may configure none, some or all of them.
	yml := `
scripts: {sync: "npm install"}
spaces:
  libs: {path: pkgs, run: {version: sync}}
`
	root := writeRepo(t, yml, "pkgs/core")
	cfg, err := Load(filepath.Join(root, "dispat.yaml"), nil)
	require.NoError(t, err)
	pkgs, _, err := Discover(cfg, root)
	require.NoError(t, err)
	require.Len(t, pkgs, 1)
	assert.Empty(t, pkgs[0].Space.BuildScript)
	assert.Empty(t, pkgs[0].Space.PublishScript)
	assert.Equal(t, []string{"npm install"}, pkgs[0].Space.VersionScript)
}

func TestLoadInitials(t *testing.T) {
	yml := validYAML + `
initials:
  core: 1.2.3
  legacy-pkg: "0.9.0"
`
	root := writeRepo(t, yml)
	cfg, err := Load(filepath.Join(root, "dispat.yaml"), nil)
	require.NoError(t, err)
	require.Len(t, cfg.InitialVersions, 2)
	// Compare rendered versions: the fields are uint64, and an untyped
	// constant in an interface argument arrives as int.
	assert.Equal(t, "1.2.3", cfg.InitialVersions["core"].String())
	assert.Equal(t, "0.9.0", cfg.InitialVersions["legacy-pkg"].String())
}

func TestLoadInitialsInvalidVersion(t *testing.T) {
	yml := validYAML + `
initials:
  core: "not-a-version"
`
	root := writeRepo(t, yml)
	_, err := Load(filepath.Join(root, "dispat.yaml"), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "initials")
	assert.Contains(t, err.Error(), "not-a-version")
}

func TestLoadInitialsAbsent(t *testing.T) {
	root := writeRepo(t, validYAML)
	cfg, err := Load(filepath.Join(root, "dispat.yaml"), nil)
	require.NoError(t, err)
	assert.Empty(t, cfg.InitialVersions)
}

func TestLoadChangelogGitHubDefaults(t *testing.T) {
	root := writeRepo(t, validYAML)
	cfg, err := Load(filepath.Join(root, "dispat.yaml"), nil)
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
	cfg, err := Load(filepath.Join(root, "dispat.yaml"), nil)
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
	cfg, err := Load(filepath.Join(root, "dispat.yaml"), nil)
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
	cfg, err := Load(filepath.Join(root, "dispat.yaml"), nil)
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
	cfg, err := Load(filepath.Join(root, "dispat.yaml"), nil)
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
	cfg, err := Load(filepath.Join(root, "dispat.yaml"), nil)
	require.NoError(t, err)
	assert.False(t, cfg.Commit.PushEnabled(), "push only applies when the commit is enabled")
}

func TestLoadShellOption(t *testing.T) {
	yml := validYAML + `
shell: ["bash", "-c"]
`
	root := writeRepo(t, yml)
	cfg, err := Load(filepath.Join(root, "dispat.yaml"), nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"bash", "-c"}, cfg.Shell)
}

func TestLoadShellEmptyInterpreter(t *testing.T) {
	yml := validYAML + `
shell: ["", "-c"]
`
	root := writeRepo(t, yml)
	_, err := Load(filepath.Join(root, "dispat.yaml"), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "shell")
}

func TestLoadJSONConfig(t *testing.T) {
	jsonCfg := `{
  "scripts": {"build": "echo b", "publish": "echo p"},
  "spaces": {"libs": {"path": "pkgs", "run": {"build": "build", "publish": "publish"}}},
  "concurrency": [4, 2],
  "github": {"enabled": false}
}`
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "pkgs", "core"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "dispat.json"), []byte(jsonCfg), 0o644))

	cfg, err := Load(filepath.Join(root, "dispat.json"), nil)
	require.NoError(t, err)
	assert.Equal(t, 4, cfg.BuildConcurrency)
	assert.Equal(t, 2, cfg.PublishConcurrency)
	assert.False(t, cfg.GitHub.IsEnabled())
	pkgs, _, err := Discover(cfg, root)
	require.NoError(t, err)
	require.Len(t, pkgs, 1)
	assert.Equal(t, "core", pkgs[0].Name)
}

func TestLoadErrors(t *testing.T) {
	cases := []struct {
		name, yml, wantErr string
	}{
		{"unknown field", "scripts: {b: x}\nspaces: {a: {path: p, typo: 1, run: {build: b, publish: b}}}", "invalid format"},
		{"no spaces", "scripts: {b: x}", "at least one space"},
		{"unknown script", "scripts: {b: x}\nspaces: {a: {path: p, run: {build: nope, publish: b}}}", "unknown script"},
		{"unknown version script", "scripts: {b: x}\nspaces: {a: {path: p, run: {version: nope}}}", "unknown script"},
		{"negative concurrency", "scripts: {b: x}\nspaces: {a: {path: p, run: {build: b, publish: b}}}\nconcurrency: -1", "concurrency"},
		{"too many concurrency values", "scripts: {b: x}\nspaces: {a: {path: p, run: {build: b, publish: b}}}\nconcurrency: [1, 2, 3]", "at most two"},
		{"bad level", "scripts: {b: x}\nspaces: {a: {path: p, run: {build: b, publish: b}}}\nlogLevel: loud", "logLevel"},
		{"pretty is not a level", "scripts: {b: x}\nspaces: {a: {path: p, run: {build: b, publish: b}}}\nlogLevel: pretty", "logLevel"},
		{"bad format", "scripts: {b: x}\nspaces: {a: {path: p, run: {build: b, publish: b}}}\nlogFormat: fancy", "logFormat"},
		{"self dependency", "scripts: {b: x}\nspaces: {a: {path: p, run: {build: b, publish: b}}}\ndependencies: [{consumer: x, provider: x}]", "itself"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := writeRepo(t, c.yml)
			_, err := Load(filepath.Join(root, "dispat.yaml"), nil)
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
	cfg, err := Load(filepath.Join(root, "dispat.yaml"), nil)
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
	root := writeRepo(t, validYAML,
		"packages/libs/core", "packages/apps/core", "packages/apps/app")
	cfg, err := Load(filepath.Join(root, "dispat.yaml"), nil)
	require.NoError(t, err)
	_, _, err = Discover(cfg, root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unique")
}

func TestDiscoverUnknownDependency(t *testing.T) {
	root := writeRepo(t, validYAML, "packages/libs/core", "packages/apps/other")
	cfg, err := Load(filepath.Join(root, "dispat.yaml"), nil)
	require.NoError(t, err)
	_, _, err = Discover(cfg, root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown consumer")
}

func TestLoadScriptArraysAndScalars(t *testing.T) {
	// Every script field accepts a single name or an array of names; a scalar
	// lifts into a one-element sequence and arrays keep their order.
	yml := `
scripts: {clean: "echo clean", compile: "echo compile", pub: "echo pub"}
spaces:
  libs:
    path: pkgs
    run:
      build: [clean, compile]
      publish: pub
`
	root := writeRepo(t, yml, "pkgs/core")
	cfg, err := Load(filepath.Join(root, "dispat.yaml"), nil)
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
	yml := `
scripts:
  auth: "npm login"
  hook: "echo hook"
  build: "echo build"
spaces:
  libs:
    path: pkgs
    run:
      build: build
      login: auth
      announce: [hook, build]
      beforeAll: hook
      beforeVersion: hook
      postVersion: [hook, build]
      beforeBuild: hook
      postBuild: hook
      beforePublish: hook
      postPublish: hook
      beforeAnnounce: hook
      postAnnounce: hook
      onFail: hook
      onSkip: [hook, build]
`
	root := writeRepo(t, yml, "pkgs/core")
	cfg, err := Load(filepath.Join(root, "dispat.yaml"), nil)
	require.NoError(t, err)
	pkgs, _, err := Discover(cfg, root)
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
	yml := `
scripts: {build: "echo b", notify: "echo notify", lint: "echo lint"}
spaces:
  libs: {path: pkgs, run: {build: build}}
run:
  beforeAll: lint
  postAll: notify
  beforeCommit: [lint, notify]
  afterCommit: notify
  postCommit: notify
  beforePush: notify
  afterPush: notify
`
	root := writeRepo(t, yml, "pkgs/core")
	cfg, err := Load(filepath.Join(root, "dispat.yaml"), nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"lint"}, cfg.Run.BeforeAll)
	assert.Equal(t, []string{"notify"}, cfg.Run.PostAll)
	assert.Equal(t, []string{"lint", "notify"}, cfg.Run.BeforeCommit)
	assert.Equal(t, []string{"echo lint", "echo notify"}, cfg.Commands(cfg.Run.BeforeCommit),
		"references resolve to the named commands in order")
	assert.Equal(t, []string{"echo notify"}, cfg.Commands(cfg.Run.AfterPush))
}

func TestLoadScriptReferenceErrors(t *testing.T) {
	cases := []struct {
		name, yml, wantErr string
	}{
		{"unknown login script",
			"scripts: {b: x}\nspaces: {a: {path: p, run: {build: b, login: nope}}}",
			"run.login references unknown script"},
		{"unknown hook script",
			"scripts: {b: x}\nspaces: {a: {path: p, run: {build: b, beforeBuild: nope}}}",
			"run.beforeBuild references unknown script"},
		{"unknown run hook script",
			"scripts: {b: x}\nspaces: {a: {path: p, run: {build: b}}}\nrun: {postAll: nope}",
			"run.postAll references unknown script"},
		{"unknown beforeAll run hook script",
			"scripts: {b: x}\nspaces: {a: {path: p, run: {build: b}}}\nrun: {beforeAll: nope}",
			"run.beforeAll references unknown script"},
		{"unknown script in an array",
			"scripts: {b: x}\nspaces: {a: {path: p, run: {build: [b, nope]}}}",
			"run.build references unknown script"},
		{"empty space script reference",
			"scripts: {b: x}\nspaces: {a: {path: p, run: {build: [\"\"]}}}",
			"empty script reference"},
		{"empty run hook reference",
			"scripts: {b: x}\nspaces: {a: {path: p, run: {build: b}}}\nrun: {beforePush: [\"\"]}",
			"empty script reference"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := writeRepo(t, c.yml)
			_, err := Load(filepath.Join(root, "dispat.yaml"), nil)
			require.Error(t, err)
			assert.Contains(t, err.Error(), c.wantErr)
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
		line := ""
		if versioning != "" {
			line = "\n    versioning: " + versioning
		}
		root := writeRepo(t, `
scripts:
  build: "echo build"
spaces:
  libs:
    path: packages/libs`+line+`
    run:
      build: build
`, "packages/libs/core")
		return Load(filepath.Join(root, "dispat.yaml"), nil)
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

func TestLoadRunScripts(t *testing.T) {
	// runScripts values are shell commands, not references into `scripts`, so
	// they need no scripts entry; keys are lowercased by viper and resolved
	// case-insensitively; an empty command is rejected.
	root := writeRepo(t, `
scripts:
  build: "echo build"
spaces:
  libs:
    path: packages/libs
    run:
      build: build
    runScripts:
      Lint: "echo linting"
      test: "go test ./..."
`, "packages/libs/core")
	cfg, err := Load(filepath.Join(root, "dispat.yaml"), nil)
	require.NoError(t, err)

	cmd, ok := cfg.Spaces["libs"].RunScript("LINT")
	require.True(t, ok, "run script names resolve case-insensitively")
	assert.Equal(t, "echo linting", cmd)
	_, ok = cfg.Spaces["libs"].RunScript("format")
	assert.False(t, ok)

	rootBad := writeRepo(t, `
scripts:
  build: "echo build"
spaces:
  libs:
    path: packages/libs
    run:
      build: build
    runScripts:
      lint: "  "
`, "packages/libs/core")
	_, err = Load(filepath.Join(rootBad, "dispat.yaml"), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `runScripts["lint"] is empty`)
}

func TestDiscoverCarriesVersioningAndRunScripts(t *testing.T) {
	root := writeRepo(t, `
scripts:
  build: "echo build"
spaces:
  libs:
    path: packages/libs
    versioning: fixed
    run:
      build: build
    runScripts:
      lint: "echo linting"
`, "packages/libs/core")
	cfg, err := Load(filepath.Join(root, "dispat.yaml"), nil)
	require.NoError(t, err)
	pkgs, _, err := Discover(cfg, root)
	require.NoError(t, err)
	require.Len(t, pkgs, 1)
	assert.Equal(t, "fixed", string(pkgs[0].Space.Versioning))
	assert.Equal(t, map[string]string{"lint": "echo linting"}, pkgs[0].Space.RunScripts)
}

func TestLoadRunScriptsEmptyNameRejected(t *testing.T) {
	root := writeRepo(t, `
scripts:
  build: "echo build"
spaces:
  libs:
    path: packages/libs
    run:
      build: build
    runScripts:
      "": "echo nameless"
`, "packages/libs/core")
	_, err := Load(filepath.Join(root, "dispat.yaml"), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty script name")
}

func TestLoadSelfDependencyRejected(t *testing.T) {
	root := writeRepo(t, `
scripts:
  build: "echo build"
spaces:
  libs:
    path: packages/libs
    run:
      build: build
dependencies:
  - consumer: core
    provider: core
`, "packages/libs/core")
	_, err := Load(filepath.Join(root, "dispat.yaml"), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot depend on itself")
}

func TestDiscoverMissingSpaceFolder(t *testing.T) {
	root := writeRepo(t, `
scripts:
  build: "echo build"
spaces:
  libs:
    path: does/not/exist
    run:
      build: build
`)
	cfg, err := Load(filepath.Join(root, "dispat.yaml"), nil)
	require.NoError(t, err, "the folder is a discovery concern, not a load one")
	_, _, err = Discover(cfg, root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `space "libs"`)
}

func TestDiscoverSkipsHiddenFoldersAndFiles(t *testing.T) {
	root := writeRepo(t, `
scripts:
  build: "echo build"
spaces:
  libs:
    path: packages/libs
    run:
      build: build
`, "packages/libs/core", "packages/libs/.hidden")
	require.NoError(t, os.WriteFile(filepath.Join(root, "packages", "libs", "notes.txt"), []byte("x"), 0o644))

	cfg, err := Load(filepath.Join(root, "dispat.yaml"), nil)
	require.NoError(t, err)
	pkgs, _, err := Discover(cfg, root)
	require.NoError(t, err)
	require.Len(t, pkgs, 1, "only real package folders count")
	assert.Equal(t, "core", pkgs[0].Name)
}

func TestDiscoverUnknownProvider(t *testing.T) {
	root := writeRepo(t, `
scripts:
  build: "echo build"
spaces:
  libs:
    path: packages/libs
    run:
      build: build
dependencies:
  - consumer: core
    provider: ghost
`, "packages/libs/core")
	cfg, err := Load(filepath.Join(root, "dispat.yaml"), nil)
	require.NoError(t, err)
	_, _, err = Discover(cfg, root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unknown provider package "ghost"`)
}

func TestLoadParserDefaults(t *testing.T) {
	// No parser object: the resolved config is the zero ccme.Config, which
	// ccme documents as the specification defaults — the parser dispat always
	// built.
	root := writeRepo(t, `
scripts:
  build: "echo build"
spaces:
  libs:
    path: packages/libs
    run:
      build: build
`, "packages/libs/core")
	cfg, err := Load(filepath.Join(root, "dispat.yaml"), nil)
	require.NoError(t, err)
	assert.Equal(t, ccme.Config{}, cfg.ParserConfig)
}

func TestLoadParserOptions(t *testing.T) {
	// Every knob maps onto the ccme configuration, with weak decoding letting
	// a numeric depth stand next to "all".
	root := writeRepo(t, `
scripts:
  build: "echo build"
spaces:
  libs:
    path: packages/libs
    run:
      build: build
parser:
  separator: "%%%"
  types:
    feat: minor
    fix: patch
    docs: patch
  strictTypes: true
  lenient: true
  maxDescriptionLength: 72
  propagation:
    bump: minor
    depth: 1
    channelDepth: all
    kinds: [dependencies, peerDependencies]
    channel: inherit
  limits:
    unitsPerMessage: 8
    scopeTermsPerUnit: 16
    messageBytes: 65536
  allowedChannels: [beta, rc]
`, "packages/libs/core")
	cfg, err := Load(filepath.Join(root, "dispat.yaml"), nil)
	require.NoError(t, err)

	pc := cfg.ParserConfig
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
	load := func(t *testing.T, parserYAML string) error {
		root := writeRepo(t, `
scripts:
  build: "echo build"
spaces:
  libs:
    path: packages/libs
    run:
      build: build
parser:
`+parserYAML, "packages/libs/core")
		_, err := Load(filepath.Join(root, "dispat.yaml"), nil)
		return err
	}

	for name, tc := range map[string]struct{ yaml, wantErr string }{
		"bad_type_bump": {"  types:\n    docs: huge\n", `types["docs"]: unknown bump "huge"`},
		// Viper lowercases map keys, so an uppercase name self-heals; a digit
		// survives lowercasing and must be rejected.
		"bad_type_name":  {"  types:\n    docs2: patch\n", "must consist of a-z only"},
		"bad_prop_bump":  {"  propagation:\n    bump: massive\n", "propagation.bump"},
		"bad_depth":      {"  propagation:\n    depth: -2\n", "propagation.depth"},
		"bad_kind":       {"  propagation:\n    kinds: [imports]\n", `unknown kind "imports"`},
		"bad_separator":  {"  separator: \"--\"\n", "at least three characters"},
		"bad_channel":    {"  allowedChannels: [latest]\n", "reserved"},
		"unknown_option": {"  colour: mauve\n", "invalid keys"},
	} {
		t.Run(name, func(t *testing.T) {
			err := load(t, tc.yaml)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}
