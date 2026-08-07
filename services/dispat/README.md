# dispat <img alt="dispat logo" align="right" width="128" height="128" src="../../imgs/logo.png" />

The dispat command line tool: a single Go binary that plans and runs monorepo releases from your conventional
commits. What the tool is and why it exists is in the [repository README](../../README.md); this page is about using
it.

## In the terminal

Point one config file at your packages, write commits as usual, and dispat works out the rest:

```sh
$ go install github.com/yohimik/dispat@latest
$ dispat init                       # starter dispat.json (--format yaml/toml)

$ git log --oneline -2
9f3c2a1 feat(core)^: add streaming api      # ^ = also bump core's direct consumers
b82d47e fix(utils): close file handle leak

$ dispat status                     # dry run: the full plan, nothing touched
● changed   package=core   bump=minor  version=1.4.2 -> 1.5.0
● changed   package=api    bump=patch  version=0.8.2 -> 0.8.3   dueToProviders=[core]
● changed   package=utils  bump=patch  version=2.0.3 -> 2.0.4
  unchanged package=docs   version=1.1.0
release plan ready  packages=4  releasing=3

$ dispat                            # release: build + publish in graph order, in parallel
published  package=utils  tag=utils@2.0.4
published  package=core   tag=core@1.5.0
published  package=api    tag=api@0.8.3    # waited for core's publish
done  published=3

$ dispat                            # re-running is always safe
done  published=0  unchanged=4
```

(Output abridged.) If `api`'s build had failed, `core` and `utils` would still have shipped, the run would exit
non-zero, and the next run would release `api` at the exact version it was owed. Runs are self-healing, and that
failure model is the point of the tool; [Concepts](./docs/concepts.md) explains it.

A few more moves:

```sh
$ dispat preview core               # what core's next changelog entry will say
$ dispat test build core            # try the build script in packages/core with the real DISPAT_* env
$ dispat lint                       # run a script in every changed package, in graph order
$ git commit -m "feat(core)@beta: try it out"
$ dispat                            # releases core@1.6.0-beta.0; graduate later with release(core)@stable:
```

## Key features

- **Releases the graph, not a list.** Consumer/provider ordering, parallel builds and publishes with separate
  concurrency budgets, and `isBuildWaitingPublish` for ecosystems (like Docker) where a consumer can only build
  after its provider is published.
- **Blast radius written in the commit.** `feat(core):` releases `core` alone; `^` reaches direct consumers, `^^`
  the transitive closure, `+N` exactly N edges. Nothing is released on a guess.
- **Self-healing runs.** Failures don't stop the world and are never lost: a broken package skips only its true
  dependants while the rest keeps releasing, and the next run catches the skipped ones up at the exact version they
  were owed. No state files, no double releases, no repair scripts: re-running *is* the recovery.
- **Prerelease trains.** `@beta` starts a line, `@beta>stable` graduates it; channels live in the tags, so trains
  survive a fresh clone.
- **Release control from commits.** `Release-As: none` holds a package: work keeps accumulating but nothing ships
  until `Release-As: auto` resumes it at everything it is owed. `Release-As: 2.0.0` pins an exact version (guarded
  against going backwards or overshooting), and `cancel(pkg)` discards pending work for good. It is all written in
  commits, so release decisions are reviewed and versioned like code. [Details](./docs/commits.md#release-control).
- **Any language, any registry.** Stages are shell commands fed a rich [`DISPAT_*` environment](./docs/environment.md);
  scripts pass values to each other through `$DISPAT_OUTPUT`, up to attaching build artefacts to GitHub releases.
- **Release records built in.** Per-package changelogs, annotated tags, GitHub releases, an optional single release
  commit plus push; all customisable or disableable.
- **Safe by design.** `dispat status` dry-runs the whole plan; credentials are verified before any work; failed
  packages can roll their folders back (`revertOnFail`); nothing is ever published against an unpublished
  dependency.
- **Built for scale.** One static binary whose only runtime dependency is git; O((V+E) log V) planning and exactly
  one bounded `git tag`/`git log` query pair per package, deterministic everywhere.

## Documentation

Start with [Getting started](./docs/getting-started.md), then dip into the references as needed:

| Document                                                  | Contents                                                                     |
|-----------------------------------------------------------|-------------------------------------------------------------------------------|
| [Getting started](./docs/getting-started.md)              | Install, first config, commit convention, commands, CI setup.                |
| [Concepts](./docs/concepts.md)                            | The mental model: baselines, propagation, trains, catch-up, the pipeline.    |
| [CLI](./docs/cli.md)                                      | Every command, flag and exit code.                                           |
| [Configuration file](./docs/configuration/README.md)      | Top-level options, script sequences, run-level hooks; links the pages below. |
| [Spaces](./docs/configuration/spaces.md)                  | Space options, stages and hooks, versioning modes, run scripts.              |
| [Tags and baselines](./docs/configuration/versions.md)    | `tagFormat` and `initials`.                                                  |
| [Release records](./docs/configuration/records.md)        | Changelogs, GitHub releases, the release commit.                             |
| [Commit parsing options](./docs/configuration/parser.md)  | `commitErrors`, `nonPackageScopes`, the `parser` object.                     |
| [Commit messages](./docs/commits.md)                      | Scope sets, directives, footers, channels, release control.                  |
| [Script environment](./docs/environment.md)               | Every `DISPAT_*` variable, the listings, script outputs.                     |
| [Architecture](./docs/architecture.md)                    | Modules, algorithms, execution model, design decisions, testing.             |
| [Unit test coverage](./docs/coverage.md)                  | The per-package statement coverage table and how to reproduce it.            |
| [Integration tests](../../tests/integration)              | The black-box suite: setup, running, results; links the test plan.           |

[`dispat.example.json`](./dispat.example.json) and [`dispat.example.yaml`](./dispat.example.yaml) show every option
in one annotated file.

## Testing

The failure semantics above are the tool's main promise, so they are tested at two independent layers (over 450 test
functions plus fuzzing, run by [CI](../../.github/workflows/ci.yml) on every push): unit tests holding **94.8%**
workspace statement coverage ([per-package table](./docs/coverage.md), [test inventory](./docs/architecture.md#testing)),
and a black-box [integration suite](../../tests/integration) that compiles the real binary and drives it against
disposable git repositories, asserting on git state, JSON logs and nanosecond-resolution execution timelines
([results](../../tests/integration/docs/test-results.md), [test plan](../../tests/integration/docs/test-plan.md)).
