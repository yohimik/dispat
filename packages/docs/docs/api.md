# API reference

Use these references when writing a configuration file, a CI script, or a tool that uses dispat's libraries. If you
are setting up a project for the first time, start with [Getting started](./getting-started.md).

| You need to… | Start here |
| --- | --- |
| Choose a command or check its flags and exit codes | [Command line](./cli/README.md) |
| Set package paths, dependencies, or release behavior | [Configuration](./configuration/README.md) |
| Parse commits or read and update manifests in a Go program | [Go packages](./go/README.md) |
| Write a commit that carries release intent | [Commit messages](./reference/commits.md) |
| Use package versions and release data in a script | [Script environment](./reference/environment.md) |
| Understand a warning or failed plan | [Diagnostic codes](./reference/plan-errors.md) |

## The command line

[`dispat status`](./cli/status.md) computes and displays a release plan without changing files or publishing.
[`dispat release`](./cli/release.md) computes a plan and runs the release. With unchanged inputs, they make the same
version decisions; a status preview does not freeze the inputs for a later run.

Other commands run scripts, edit manifests, or perform individual release steps. The [CLI index](./cli/README.md)
groups them by task and lists the shared flags and exit codes.

## The configuration file

The [Configuration reference](./configuration/README.md) explains file discovery, supported formats, defaults, and
how a package overrides shared settings. Use JSON, YAML, or TOML for the same configuration model.

For releases spanning repositories, choose the setup before looking up its fields:

- A [control repository](./control-repository.md#source-history-mode) uses `polyrepo`, `configs`,
  `repositoryOverrides`, and `repositoryBaselines`. Provider objects can declare `external`, and `commit.branch`
  controls the recording branch. The CLI equivalents include `--polyrepo` and repeatable `--configs` flags.
- A [choreographed fleet](./choreographed-repositories.md) starts with a `repository` identity. Its optional
  `repositories` roster lists peers as `{name, url, path, branch}` entries. `--polyrepo=false` limits one invocation
  to the local repository.

The [models package](./go/models.md) exposes these settings as Go types.

## The Go packages

Each of the six modules is independently importable. You do not need the dispat executable to use them in your own
program. Their individual pages explain inputs, outputs, and error handling.

| Package | What it does |
| --- | --- |
| [`pkg/ccme`](./go/ccme.md) | Parses commit messages in the Conventional Commits Monorepo Extension format |
| [`pkg/scanner`](./go/scanner.md) | Reads supported manifests into a common representation |
| [`pkg/writer`](./go/writer.md) | Updates supported manifests while preserving formatting where the format allows |
| [`pkg/manifest`](./go/manifest.md) | Defines the format names and types shared by scanner and writer |
| [`pkg/models`](./go/models.md) | Represents dispat configuration as typed Go values |
| [`pkg/config`](./go/config.md) | Loads JSON, YAML, and TOML configuration, including local file references |

## The formats around them

[Commit messages](./reference/commits.md) describe release intent. [Script environment variables](./reference/environment.md)
pass the resulting plan to your commands. [Diagnostic codes](./reference/plan-errors.md) explain why a plan or run
could not proceed as expected.

Keep the documentation version matched to the CLI or library version you use. Select an older release in the version
menu when maintaining a pinned installation.
