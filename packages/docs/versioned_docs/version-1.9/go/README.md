# Go packages

dispat is assembled from six Go modules, and you can import each one on its own. Parsing commit messages, reading
configuration files and rewriting dependency manifests are problems older than releases. These modules are packages
first and dispat internals second, so none of them needs the CLI, a git repository, or a network.

Everything else the CLI does lives under `services/dispat/internal`. Go makes this unreachable from outside the module.
The six packages below are the whole of the importable surface.

## The packages

| Module | Import path | What it does |
|--------|-------------|--------------|
| [ccme](./ccme.md) | `github.com/yohimik/dispat/pkg/ccme/v2` | Parses commit messages in the Conventional Commits Monorepo Extension format |
| [config](./config.md) | `github.com/yohimik/dispat/pkg/config` | Loads JSON, YAML and TOML configuration without reflection |
| [scanner](./scanner.md) | `github.com/yohimik/dispat/pkg/scanner` | Reads dependency manifests into one ecosystem-neutral shape |
| [writer](./writer.md) | `github.com/yohimik/dispat/pkg/writer` | Rewrites those manifests in place, byte for byte |
| [manifest](./manifest.md) | `github.com/yohimik/dispat/pkg/manifest` | The vocabulary the reader and the writer share |
| [models](./models.md) | `github.com/yohimik/dispat/pkg/models` | The typed model of the configuration file |

## How they fit together

The dependency shape is deliberately shallow, and `ccme` depends on nothing outside the standard library. Both
`scanner` and `writer` import `manifest` to keep them covering exactly the same formats. `models` imports `ccme`
because a resolved configuration carries a parser configuration inside it. `config` imports neither: it is a loader
for any Go program's configuration, and it knows nothing about dispat's own model.

That leaves two natural pairs and one that stands alone. `scanner` and `writer` are the manifest halves, where one
reads a version and the other writes it, and both agree byte for byte on where the value sits because `manifest`
decides for them. `ccme` and `models` are the input halves, where one reads the commit messages and the other
describes the file configuring how they are read. `config` is what reads that file: `models` says what a
configuration means, and `config` says how a configuration is loaded at all.

## Installing

Each module is versioned and tagged separately. They use the layout Go expects for a multi-module repository. For
example, the `github.com/yohimik/dispat/pkg/ccme/v2` module is released by the `pkg/ccme/v2.0.0` tag rather than a
repository-wide version:

```sh
go get github.com/yohimik/dispat/pkg/ccme/v2
go get github.com/yohimik/dispat/pkg/config
go get github.com/yohimik/dispat/pkg/scanner
go get github.com/yohimik/dispat/pkg/writer
go get github.com/yohimik/dispat/pkg/manifest
go get github.com/yohimik/dispat/pkg/models
```

Taking one package does not pull the others in unless it needs them. None of them pulls in the CLI.

## The same work from the command line

Three of the packages have a command that wraps the package with a report attached. You do not need a configuration
file or a git repository to run them:

- [`dispat scanner`](../cli/scanner.md) prints what a folder's manifests declare.
- [`dispat writer`](../cli/writer.md) applies manifest edits in place.
- [`dispat replacer`](../cli/replacer.md) replaces literal text in any file.

Run these commands to see what a package does with your own files before you import it.
