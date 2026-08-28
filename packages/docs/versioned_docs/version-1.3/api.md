# API reference

This section covers everything dispat exposes to machines. The project has no server and no HTTP endpoint, so it relies
entirely on three stable surfaces.

Each surface provides a stable contract. The command line flags and exit codes are the contract for your CI pipeline.
The configuration keys and defaults are the contract for your repository, and the exported types and functions are the
contract for your custom programs.

## The command line

Read the [CLI reference](./cli/README.md) for a list of every command and flag. The index page details the exit codes
and global flags.

Run [`dispat status`](./cli/status.md) to plan a release and print it without doing anything. Run
[`dispat release`](./cli/release.md) to carry out that exact plan. Every other command narrows these two or performs a
single step.

## The configuration file

Check the [Configuration reference](./configuration/README.md) to understand every key in `dispat.json`, `dispat.yaml`
and `dispat.toml`. This page explains how dispat discovers the file and what each default is. It also shows how a
package overrides its space.

## The Go packages

Read [Go packages](./go/README.md) for details on the five modules dispat uses. You can import each module on its own.
They do not require a dispat binary, a git repository, or network access.

| Package | What it does |
|---------|--------------|
| [`pkg/ccme`](./go/ccme.md) | Parses commit messages in the Conventional Commits Monorepo Extension format |
| [`pkg/scanner`](./go/scanner.md) | Reads dependency manifests across twenty ecosystems into one shape |
| [`pkg/writer`](./go/writer.md) | Rewrites those manifests in place, preserving every byte it does not change |
| [`pkg/manifest`](./go/manifest.md) | Provides the vocabulary the reader and the writer share |
| [`pkg/models`](./go/models.md) | Exposes the typed configuration model so tooling can author config files as values |

## The formats around them

Three more pages describe formats instead of surfaces. They belong in this section because machines read them.

- [Commit messages](./reference/commits.md) defines the format dispat reads release intent from.
- [Script environment](./reference/environment.md) lists the `DISPAT_*` variables every stage script receives.
- [Diagnostic codes](./reference/plan-errors.md) numbers everything dispat can report, explaining what each code means
  and what you should do about it.
