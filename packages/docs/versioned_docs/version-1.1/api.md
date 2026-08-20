# API reference

Everything dispat exposes to something other than a human reader: the command line, the configuration file, and the
five Go packages the CLI is built from. There is no server and no HTTP endpoint anywhere in the project, so these three
surfaces are the whole of it.

Each surface is stable in its own way. The command line is what a CI pipeline calls, so its flags and exit codes are
the contract. The configuration file is what a repository commits, so its keys and defaults are the contract. The Go
packages are imported by other programs, so their exported types and functions are the contract.

## The command line

[CLI reference](./cli/README.md) documents every command and every flag, one page per command, with the exit codes and
the global flags on the index page.

Two commands are worth knowing before the rest: [`dispat status`](./cli/status.md) plans a release and prints it
without doing anything, and [`dispat release`](./cli/release.md) is the same plan carried out. Everything else either
narrows those two or performs one of their steps on its own.

## The configuration file

[Configuration reference](./configuration/README.md) documents every key of `dispat.json`, `dispat.yaml` and
`dispat.toml`, including how the file is discovered, what each default is, and how a package overrides its space.

## The Go packages

[Go packages](./go/README.md) documents the five modules dispat is assembled from. Each is importable on its own, with
no dispat binary, no git repository and no network involved:

| Package | What it does |
|---------|--------------|
| [`pkg/ccme`](./go/ccme.md) | Parses commit messages in the Conventional Commits Monorepo Extension format |
| [`pkg/scanner`](./go/scanner.md) | Reads dependency manifests across twenty ecosystems into one shape |
| [`pkg/writer`](./go/writer.md) | Rewrites those manifests in place, preserving every byte it does not change |
| [`pkg/manifest`](./go/manifest.md) | The vocabulary the reader and the writer share |
| [`pkg/models`](./go/models.md) | The typed configuration model, so tooling can author config files as values |

## The formats around them

Three more pages describe formats rather than surfaces, and belong here for the same reason:

- [Commit messages](./reference/commits.md) is the format dispat reads release intent from.
- [Script environment](./reference/environment.md) is the set of `DISPAT_*` variables every stage script receives.
- [Diagnostic codes](./reference/plan-errors.md) is the numbered list of everything dispat can report, with what each
  code means and what to do about it.
