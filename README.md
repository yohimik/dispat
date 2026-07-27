# monorel

Builds and publishes the changed packages of a monorepo, driven by conventional commits and a single config at the repo root.

## How it works

The tool loads `monorel.yaml` or `monorel.json`, discovers packages (every direct sub-folder of a space is a package, named after the folder), and builds a dependency graph from the configured consumer/provider relations. For each package it finds the latest `pkg@MAJOR.MINOR.PATCH` git tag (or falls back to the first commit) and scans commit subjects since then. `fix(pkg): …` demands a patch, `feat(pkg): …` a minor, `BREAKING CHANGE(pkg): …` or any `type(pkg)!: …` a major; anything else is ignored. A consumer of one or more changed providers gets a single patch bump, unless its own commits demand more — the higher bump always wins.

It then prints the whole project graph with changed packages highlighted, and runs build + publish scripts inside each changed package folder with bounded parallelism: `concurrency` accepts either a single value for both stages or a `[build, publish]` pair, and each stage has its own independent budget (e.g. `[4, 2]` allows 4 parallel builds and 2 parallel publishes at the same time). Publish always waits for the package's own build and for its providers' publishes. When a provider's space sets `isBuildWaitingPublish: true`, consumers may not even start building until that provider is published. On failure the package is marked failed and its consumers are skipped — unless a consumer has changes of its own (or another successfully published provider), in which case it proceeds. Each successful publish prepends an entry to the package's `CHANGELOG.md` and creates an annotated tag `pkg@version` (push tags from CI). A summary is printed at the end; the exit code is 1 if anything failed.

## Usage

```sh
go mod tidy   # fetch zerolog, viper, pflag and testify, generate go.sum
go build -o monorel .
./monorel --root /path/to/monorepo --config monorel.yaml   # release (default command)
./monorel status --root /path/to/monorepo                   # only print the graph and new versions
```

Commands: `release` (default) runs the full build + publish pipeline; `status` stops after printing the project graph with the computed version bumps — nothing is built, published or tagged.

All flags are optional: `--root` defaults to `.`, `--config` to `monorel.yaml`; `--concurrency` (e.g. `--concurrency 4,2` for build,publish or a single value for both) and `--log-level` override the corresponding config values (viper precedence: explicit flag > config file > flag default). Configuration is loaded with viper, so besides YAML the config may also be JSON or TOML — the format is inferred from the file extension. Viper matches keys case-insensitively (map keys are lowercased), so script and space names are case-insensitive too. See `monorepo.example.yaml` for the full format. `logLevel: pretty` gives colored console output; any concrete level (`trace`…`error`) switches to JSON lines for CI log ingestion.

## Design

- `internal/config` — viper-based loading (`UnmarshalExact` rejects unknown keys), pflag bindings, validation, package discovery.
- `internal/conventional` — minimal conventional-commits subject parser, no regular expressions.
- `internal/semver` — strict `MAJOR.MINOR.PATCH` parsing and bumping, no regular expressions.
- `internal/graph` — Kahn topological sort with a name-ordered heap, O((V+E) log V), deterministic output, cycle detection.
- `internal/gitx` — `Git` interface plus a CLI implementation shelling out to `git` (tags list, log ranges, annotated tags).
- `internal/plan` — computes each package's next version and the processing order.
- `internal/release` — the parallel executor: a task graph of build/publish pairs, a worker budget of `concurrency`, skip propagation on failure, per-line script log streaming.
- `internal/changelog` — renders and prepends `CHANGELOG.md` entries.
- `internal/cli` — wiring, graph printout, summary, exit codes.

Interfaces (`gitx.Git`, `script.Runner`, `release.Tagger`, `release.ChangelogWriter`) decouple the executor and planner from git, the shell and the filesystem; the unit tests (testify assertions) exercise every package with in-memory fakes:

```sh
go test ./...
```
