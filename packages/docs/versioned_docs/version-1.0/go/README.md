# Go packages

dispat is assembled from five Go modules, and each one is importable on its own. Parsing commit messages and reading
and rewriting dependency manifests are problems older than releases, so they are packages first and dispat internals
second: none of them needs the CLI, a git repository or a network.

Everything else the CLI does lives under `services/dispat/internal`, which Go makes unreachable from outside the
module. The five packages below are the whole of the importable surface.

## The packages

| Module | Import path | What it does |
|--------|-------------|--------------|
| [ccme](./ccme.md) | `github.com/yohimik/dispat/pkg/ccme` | Parses commit messages in the Conventional Commits Monorepo Extension format |
| [scanner](./scanner.md) | `github.com/yohimik/dispat/pkg/scanner` | Reads dependency manifests into one ecosystem-neutral shape |
| [writer](./writer.md) | `github.com/yohimik/dispat/pkg/writer` | Rewrites those manifests in place, byte for byte |
| [manifest](./manifest.md) | `github.com/yohimik/dispat/pkg/manifest` | The vocabulary the reader and the writer share |
| [models](./models.md) | `github.com/yohimik/dispat/pkg/models` | The typed model of the configuration file |

## How they fit together

The dependency shape is deliberately shallow. `ccme` depends on nothing outside the standard library. `manifest`
depends on nothing and is imported by both `scanner` and `writer`, which is what keeps the reader and the writer
covering exactly the same formats. `models` imports `ccme`, because a resolved configuration carries a parser
configuration inside it.

That leaves two natural pairs. `scanner` and `writer` are the manifest halves: one reads a version, the other writes
it, and both agree byte for byte on where the value sits because `manifest` decides for them. `ccme` and `models` are
the input halves: one reads the commit messages, the other describes the file that configures how they are read.

## Installing

Each module is versioned and tagged separately, in the layout Go expects for a multi-module repository, so a tag reads
`pkg/ccme/v1.0.0` rather than a repository-wide version:

```sh
go get github.com/yohimik/dispat/pkg/ccme
go get github.com/yohimik/dispat/pkg/scanner
go get github.com/yohimik/dispat/pkg/writer
go get github.com/yohimik/dispat/pkg/manifest
go get github.com/yohimik/dispat/pkg/models
```

Taking one package does not pull the others in unless it needs them, and none of them pulls in the CLI.

## The same work from the command line

Three of the packages have a command that is the package with a report attached, and those commands need no
configuration file and no git repository:

- [`dispat scanner`](../cli/scanner.md) prints what a folder's manifests declare.
- [`dispat writer`](../cli/writer.md) applies manifest edits in place.
- [`dispat replacer`](../cli/replacer.md) replaces literal text in any file.

They are the quickest way to see what a package does with your own files before importing it.
