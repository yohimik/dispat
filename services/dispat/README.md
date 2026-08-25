# dispat <img alt="dispat logo" align="right" width="128" height="128" src="../../imgs/logo.png" />

The dispat command line tool is a single Go binary that plans and runs monorepo releases from conventional commits.
Read the [repository README](../../README.md) to learn why dispat exists, or follow this guide to start using it.

## In the terminal

Point one config file at your packages, write your commits as usual, and let dispat handle the release logic:

```sh
$ go install github.com/yohimik/dispat/services/dispat@latest
$ dispat init                       # starter dispat.json (--format yaml/toml)
$ dispat compute --write            # derive the graph and starting versions from the manifests

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

The output above is abridged. Before your first release, edit the starter config to list your package folders and set
their build and publish commands. If `api` fails to build in that run, `core` and `utils` still ship, dispat exits
non-zero, and the next run releases `api` at the exact version it was owed.

Runs are self-healing, and [Concepts](https://yohimik.github.io/dispat/concepts/) explains this failure model in depth.

Run these commands for common daily tasks:

```sh
$ dispat preview                    # pending release notes, all packages (-p core: just one)
$ dispat scanner packages/core      # what that folder's manifests declare; no config or git needed
$ dispat writer packages/core/package.json --set @acme/utils=^2.0.0   # one format-preserving edit
$ dispat autowriter --set @acme/utils='^{version}' --since all       # ...the same edit in every package
$ dispat autowriter --set-local --since all                          # ...or every workspace range, worked out
$ dispat replacer --replace 'com.acme:core:1.2.0=>com.acme:core:1.3.0' build.gradle   # literal text, no parsing
$ dispat run build --since all -p core   # try the build script in packages/core with the real DISPAT_* env
$ dispat release -g platform        # release one versioning group; -s libs releases one space
$ dispat lint                       # run a script in every changed package that has it, in graph order
$ dispat self-update                # replace this binary with the latest release; --rollback puts the old one back
$ git commit -m "feat(core)%beta: try it out"
$ dispat                            # releases core@1.6.0-beta.0; graduate later with release(core)%stable:
```

## Key features

- **Releases the graph, not a list.** dispat manages consumer and provider ordering across your workspace. It runs
  builds and publishes in parallel with separate concurrency budgets, and supports `isBuildWaitingPublish` for
  ecosystems like Docker where a consumer must wait for its provider to publish before building.
- **Blast radius written in the commit.** A `feat(core):` commit releases `core` alone, `^` bumps its direct consumers,
  `^^` reaches the full transitive closure, and `+N` reaches up to N edges away. You control the exact blast radius
  directly from git history.
- **Self-healing runs, because a release is a distributed transaction.** Publishing a graph performs irreversible
  writes across external registries without rollbacks, so each package commits its leg by creating an annotated git tag
  only after publishing succeeds. If a package fails, dispat skips only its dependants while unrelated packages
  continue shipping. Re-running *is* the recovery because the plan is a pure function of git history, the dependency
  graph, and your configuration, so the next run executes only the unfinished legs without state files or repair
  scripts. Read more in [Details](https://yohimik.github.io/dispat/internals/architecture/).
- **Release control from commits.** Use `%beta` to start a prerelease train and `%beta>stable` to graduate it. Set
  `Release-As: none` to hold a package, `Release-As: auto` to resume releases, `Release-As: 2.0.0` to pin a version, or
  `cancel(pkg)` to drop pending changes. Because you write these directives into commit messages, release decisions are
  reviewed and tracked like code. Read more in
  [Details](https://yohimik.github.io/dispat/reference/commits/#release-control).
- **Polyglot by construction: any language, any registry, any tooling.** Stages execute shell commands populated with a
  rich [`DISPAT_*` environment](https://yohimik.github.io/dispat/reference/environment/), storing release state
  entirely in git tags. dispat reads and rewrites thirty-five manifest formats across twenty ecosystems, from npm to
  `go.mod` to `Podfile` and game project files in Unity, Godot, Unreal, Defold, and O3DE, so `dispat compute` derives
  your graph and starting versions automatically. Unchanged packages stay out of the plan entirely, letting external
  caches like BuildKit, Turborepo, Nx, Bazel, or Gradle operate inside stages without affecting versioning or tags.
- **Every release step is also a command, with the records built in.** Generate per-package changelogs, annotated tags,
  GitHub releases, or release commits individually with standalone commands like `dispat changelog`, `commit`, or
  `github`. When the main release stage runs later, it detects completed records and skips them. You can dry-run plans
  with `dispat status`, prevent concurrent runs using a
  [release lock](https://yohimik.github.io/dispat/reference/releasing/release-lock/), release subsets using
  `dispat release -p core` (or `-s libs`, `-g platform`), and assemble custom workflows with `dispat if`, `exec`,
  `autowriter`, and `autoreplacer`. Read more in
  [Details](https://yohimik.github.io/dispat/reference/releasing/steps/).

## Documentation

Start with [Getting started](https://yohimik.github.io/dispat/getting-started/), then dip into the references as
needed:

| Document                                                                                             | Contents                                                                                                                                                                                   |
|------------------------------------------------------------------------------------------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| [Getting started](https://yohimik.github.io/dispat/getting-started/)                                  | Install, first config, commit convention, commands, CI setup.                                                                                                                              |
| [Concepts](https://yohimik.github.io/dispat/concepts/)                                                | The mental model: baselines, propagation, trains, catch-up, the pipeline.                                                                                                                  |
| [Examples](https://yohimik.github.io/dispat/examples/)                                                | A complete setup per package manager, each with real terminal output: npm, pnpm, Docker, Android, the mixed graph, adoption.                                                               |
| [Manifest tools](https://yohimik.github.io/dispat/editing/manifests/)                        | `dispat scanner` and `dispat writer`: reading and editing manifests on their own.                                                                                                          |
| [Editing across the monorepo](https://yohimik.github.io/dispat/editing/autowriter/)          | `dispat autowriter`: one manifest edit applied to every package the plan selects, or edits derived from the workspace.                                                                     |
| [Replacing across the monorepo](https://yohimik.github.io/dispat/editing/autoreplacer/) | `dispat autoreplacer`: literal text replaced in every package the plan selects.                                                                                                          |
| [The replacer](https://yohimik.github.io/dispat/editing/replacer/)                           | `dispat replacer` and `autoVersion.replace`: literal text for the versions no manifest holds.                                                                                              |
| [API reference](https://yohimik.github.io/dispat/api/)                                                | The three surfaces dispat exposes: the command line, the configuration file, and the Go packages.                                                                                          |
| [CLI](https://yohimik.github.io/dispat/cli/)                                                          | Every command, flag and exit code.                                                                                                                                                         |
| [dispat if](https://yohimik.github.io/dispat/cli/if/)                                                 | Branching on a variable, a file or the changed packages inside a configured script.                                                                                                        |
| [dispat exec](https://yohimik.github.io/dispat/cli/exec/)                                             | Running one declared script on its own.                                                                                                                                                    |
| [Naming a place](https://yohimik.github.io/dispat/cli/locations/)                                     | The `pkg:`, `space:`, `root` and `cwd` values `--for`, `--script-from` and `--in` share.                                                                                                    |
| [Configuration file](https://yohimik.github.io/dispat/configuration/)                                 | Top-level options, script sequences, run-level hooks; links the pages below.                                                                                                               |
| [Spaces](https://yohimik.github.io/dispat/configuration/spaces/)                                      | Space options, stages and hooks in execution order, versioning modes and groups, `scripts`, the space's `packages` map, the space configuration file, `.dispatexclude`. |
| [Packages](https://yohimik.github.io/dispat/configuration/packages/)                                  | Per-package overrides and the ladder that orders them, `src`, standalone packages, package dependencies, in-folder config files.                                                           |
| [What counts as a change](https://yohimik.github.io/dispat/configuration/change-scope/)               | `src` and `ignore`: which of a package's files make a scopeless commit address it, and the `.dispatignore` file.                                                                           |
| [Tags and baselines](https://yohimik.github.io/dispat/configuration/versions/)                        | `tagFormat` and `initials`.                                                                                                                                                                |
| [Alias tags](https://yohimik.github.io/dispat/configuration/alias-tags/)                              | Extra moving tags beside the release tag, and why an alias is never read back as a baseline.                                                                                               |
| [Release records](https://yohimik.github.io/dispat/configuration/records/)                            | Changelogs, GitHub releases, your own header and footer lines, the channels each record reaches, the release commit.                                                                       |
| [Commit parsing options](https://yohimik.github.io/dispat/configuration/parser/)                      | `commitErrors`, `nonPackageScopes`, the `parser` object, quieting the parser's diagnostics.                                                                                                |
| [dependencies](https://yohimik.github.io/dispat/configuration/dependencies/)                          | Declaring the consumer and provider relations the graph orders releases by, and the three levels one can be written at.                                                                                     |
| [autoVersion](https://yohimik.github.io/dispat/configuration/autoversion/)                            | Native manifest rewriting at the version stage: the parsing and replacing strategies, the write policy, and `syncLock`.                                                                    |
| [Script sequences](https://yohimik.github.io/dispat/configuration/scripts/)                           | `scripts` as named commands, one or several per name, how a `flow` name resolves package first, and what runs when.                                                                        |
| [Run-level hooks](https://yohimik.github.io/dispat/configuration/run-hooks/)                          | The seven hooks that observe the whole run rather than one package, where they execute, and why the step commands fire none of them.                                                       |
| [Static env](https://yohimik.github.io/dispat/configuration/env/)                                     | The `env` objects that add fixed variables to every script, and how the levels layer.                                                                                                      |
| [custom](https://yohimik.github.io/dispat/configuration/custom/)                                      | The free-form object dispat carries without ever reading it, for your own tooling.                                                                                                         |
| [Go packages](https://yohimik.github.io/dispat/go/)                                                   | The five modules dispat is built from, each importable on its own.                                                                                                                         |
| [ccme](https://yohimik.github.io/dispat/go/ccme/)                                                     | The commit parser as a library: entry points, configuration, diagnostics, bulk parsing.                                                                                                    |
| [scanner](https://yohimik.github.io/dispat/go/scanner/)                                               | The manifest reader as a library: what it reads, what comes back, the partial-result contract.                                                                                             |
| [writer](https://yohimik.github.io/dispat/go/writer/)                                                 | The manifest writer as a library: rewriting, replacing, relinking and build counters.                                                                                                      |
| [Commit messages](https://yohimik.github.io/dispat/reference/commits/)                                | Scope sets, directives, footers, channels, release control.                                                                                                                                |
| [Correcting a record](https://yohimik.github.io/dispat/reference/corrections/)                        | `Edits` and `Deletes`: restating or discarding a release record a pushed commit message got wrong, and what a revert does to the changelog.                                                 |
| [Script environment](https://yohimik.github.io/dispat/reference/environment/)                         | Every `DISPAT_*` variable, the listings, script outputs.                                                                                                                                   |
| [Shared versions](https://yohimik.github.io/dispat/reference/releasing/versioning/)                   | Packages that hold part of their version in common: the seven modes, what moves a group, sparse behaviour, versioning groups.                                                              |
| [Release steps](https://yohimik.github.io/dispat/reference/releasing/steps/)                          | The step commands from scratch: what they are for, why they are safe to repeat, and a worked flow.                                                                                         |
| [Partial releases](https://yohimik.github.io/dispat/reference/releasing/partial-releases/)            | Releasing part of the monorepo: the selection flags on `release` and `status`, why the order can hold a package back, and `--strict`.                                                      |
| [Prerelease branches](https://yohimik.github.io/dispat/reference/releasing/prerelease-branches/)      | A stable main and a beta branch: the train, the rc promotion, and exactly what merging to main does.                                                                                       |
| [The release lock](https://yohimik.github.io/dispat/reference/releasing/release-lock/)                | Why two releases at once are refused rather than raced, and how to clear a lock a killed run left behind.                                                                                  |
| [Recovering from a failed run](https://yohimik.github.io/dispat/reference/releasing/recovery/)        | A run that failed in the middle, and the re-run that finishes exactly what it still owed.                                                                                                  |
| [Diagnostic codes](https://yohimik.github.io/dispat/reference/plan-errors/)                           | Every code dispat can report: the three ways a run refuses to start, each error that aborts a plan, and what to do about it.                                                                |
| [dispat in CI](https://yohimik.github.io/dispat/reference/ci/)                                        | The GitHub Action, the container images, the install script: getting dispat onto a runner, and gating a pipeline on the plan with `--require-release`.                                      |
| [Pipeline patterns](https://yohimik.github.io/dispat/reference/pipelines/)                            | Testing only what a commit changed, the local-link bracket, and the gated release pipeline with `-p`/`-s`/`-g` passed from CI.                                                              |
| [Updating dispat](https://yohimik.github.io/dispat/reference/self-update/)                            | `dispat self-update`: how the binary replaces itself, what it reads out about the release, the backup and the rollback, the update notice.                                                 |
| [Architecture](https://yohimik.github.io/dispat/internals/architecture/)                              | Modules, algorithms, execution model, design decisions, testing.                                                                                                                           |
| [Test coverage](https://yohimik.github.io/dispat/internals/coverage/)                                 | What the suite reaches, per package, measured by the release that published the page.                                                                                                      |
| [Test results](https://yohimik.github.io/dispat/internals/test-results/)                              | What the suite did: the counts, the timings, the race pass, and what each area covers.                                                                                                     |
| [Integration tests](../../tests/integration)                                                         | The black-box suite itself: setup, running, and the test plan.                                                                                                                             |

See [`dispat.example.json`](./dispat.example.json) and [`dispat.example.yaml`](./dispat.example.yaml) to inspect every
configuration option in a single annotated file.

## Testing

dispat verifies its failure semantics across two independent layers run by [CI](../../.github/workflows/tests.yml) on
every push. First, unit tests evaluate logic against in-memory fakes. Second, a black-box
[integration suite](../../tests/integration) builds the binary and runs it against disposable git repositories,
asserting on git state, JSON logs, and nanosecond-resolution execution timelines.

Every release calculates fresh test metrics and publishes them directly to the docs site. You can review code reach in
[test coverage](https://yohimik.github.io/dispat/internals/coverage/), execution counts and timings in
[test results](https://yohimik.github.io/dispat/internals/test-results/), and verified scenarios in the
[test plan](../../tests/integration/docs/test-plan.md).

## On the roadmap

These planned features outline future development. Nothing described below exists yet, and current releases do not
depend on them.

**Managing the tools a release needs.** Stage scripts currently assume required CLI tools already exist on the runner
or host machine. You must install them using CI setup steps, Dockerfile layers, package managers, or setup scripts.

The goal is to let your configuration declare required CLI tools so dispat can fetch them from a **GitHub release** or
a **URL you provide**, pinned to an exact version. dispat already uses this logic internally for
[`dispat self-update`](https://yohimik.github.io/dispat/cli/self-update/), which resolves releases, verifies checksums,
and installs binaries. Future releases will extend that engine to external tools.

**A native package in every ecosystem.** dispat currently ships as a standalone static binary, requiring manual
installation outside of standard language managers. The goal is to distribute wrapper packages across native
ecosystems, such as an **npm package** that you can add as a development dependency, pin in your lockfile, and run with
`npx dispat`. Go projects already support this workflow through `go install`.

Each package wrapper will also handle ecosystem-specific workflows, such as npm publish expectations around
`package.json` and workspace structures. Because dispat already reads and rewrites these manifests, native wrappers
will fit smoothly into your existing project conventions.

## Projects using dispat

dispat releases its own monorepo: every release of this repository is planned and published by the binary built from
that checkout. The first stable release shipped eleven packages at once: this CLI across six platform targets, five Go
modules, four container images, and the [documentation site](https://yohimik.github.io/dispat/), each with its own tag,
changelog, and GitHub release. The release workflow lives in [release.yml](../../.github/workflows/release.yml),
following the structure described in the [CI reference](https://yohimik.github.io/dispat/reference/ci/).

See the [repository README](../../README.md#projects-using-dispat) for the full list of projects. If you release a
project with dispat, share it on Discord to get it listed.

## Community

Join the community to ask questions, report issues, or share projects:
**[discord.gg/83PwVSCCmk](https://discord.gg/83PwVSCCmk)**.

You can also submit bug reports and feature requests directly through
[GitHub issues](https://github.com/yohimik/dispat/issues).
