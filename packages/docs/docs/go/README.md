# Go packages

The six Go modules below can be used independently of the dispat CLI. Import them to parse commit messages, load
configuration, or read and edit dependency manifests in your own tools. None requires a Git repository or network
access.

The rest of the CLI lives under `services/dispat/internal` and cannot be imported from another Go module.

## The packages

| Module | Import path | What it does |
|--------|-------------|--------------|
| [ccme](./ccme.md) | `github.com/yohimik/dispat/pkg/ccme` | Parses commit messages in the Conventional Commits Monorepo Extension format |
| [config](./config.md) | `github.com/yohimik/dispat/pkg/config` | Loads JSON, YAML and TOML configuration without reflection |
| [scanner](./scanner.md) | `github.com/yohimik/dispat/pkg/scanner` | Reads dependency manifests into one ecosystem-neutral shape |
| [writer](./writer.md) | `github.com/yohimik/dispat/pkg/writer` | Rewrites those manifests in place, byte for byte |
| [manifest](./manifest.md) | `github.com/yohimik/dispat/pkg/manifest` | The vocabulary the reader and the writer share |
| [models](./models.md) | `github.com/yohimik/dispat/pkg/models` | The typed model of the configuration file |

## How they fit together

Choose the packages for the work you need:

- **Read and edit manifests:** `scanner` reads the files, `writer` edits them, and both use `manifest` for format names,
  dependency kinds, and byte positions.
- **Parse commit messages:** `ccme` works on message text and uses only the standard library.
- **Generate dispat configurations:** `models` provides the structs. It imports `ccme` for parser settings.
- **Load your own configuration:** `config` works with your structs and does not depend on dispat's configuration model.

You can combine these packages, but importing one only brings in the modules it depends on.

## Installing

Each module is versioned and tagged separately. They use the layout Go expects for a multi-module repository. Further
CCME parser releases are on hold, and its unsuffixed module path still resolves to the published v1.0.0 parser. The
immutable `pkg/ccme/v2.0.0` Git tag remains part of the repository's history; it does not make 2.0.0
fetchable from the unsuffixed Go module path:

```sh
go get github.com/yohimik/dispat/pkg/ccme@v1.0.0
go get github.com/yohimik/dispat/pkg/config
go get github.com/yohimik/dispat/pkg/scanner
go get github.com/yohimik/dispat/pkg/writer
go get github.com/yohimik/dispat/pkg/manifest
go get github.com/yohimik/dispat/pkg/models
```

Taking one package does not pull the others in unless it needs them. None of them pulls in the CLI.

## The same work from the command line

You can try the manifest tools on your own files before using the Go API. These commands need no configuration file
or Git repository:

- [`dispat scanner`](../cli/scanner.md) prints what a folder's manifests declare.
- [`dispat writer`](../cli/writer.md) applies manifest edits in place.
- [`dispat replacer`](../cli/replacer.md) replaces literal text in any file.

See [Manifest tools](../editing/manifests.md) for examples and supported formats.
