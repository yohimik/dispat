# dispat <img alt="dispat logo" align="right" width="128" height="128" src="../../imgs/logo.png" />

The dispat command line tool is a single Go binary that plans and runs releases of a monorepo, a polyrepo or a single
package from conventional commits. It runs a release as a saga, so an interrupted one recovers by being run again.
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

$ dispat                            # re-run after checking any publish interrupted before its tag
done  published=0  unchanged=4
```

The output above is abridged. Before your first release, edit the starter config to list your package folders and set
their build and publish commands. If `api` fails to build in that run, `core` and `utils` still ship, dispat exits
non-zero, and the next run releases `api` at the exact version it was owed.

Recorded publishes recover forward on the next run. If a publish process was killed before dispat wrote its tag,
check that destination before retrying. [Concepts](https://dispat.dev/concepts/) explains the full failure model.

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
$ dispat install acme/tool --asset 'tool-{os}-{arch}'    # install a tool from any GitHub release, verified
$ git commit -m "feat(core)%beta: try it out"
$ dispat                            # releases core@1.6.0-beta.0; graduate later with release(core)%stable:
```

## Key features

- **Build and publish in dependency order.** Dispat schedules each package after the packages it needs. A package that
  uses another is its consumer; the package it needs is its provider. Independent work runs in parallel, with separate
  limits for builds and publishes. Set `isBuildWaitingPublish` when a build needs its provider's published artifact,
  as Docker image builds often do.
- **Choose which packages release.** A `feat(core):` commit releases `core`. Add `^` to include direct consumers,
  `^^` to include every downstream consumer, or `+N` to reach up to N dependency edges away. The commit records your
  choice for review alongside the code.
- **Recover unfinished releases.** Dispat writes a Git tag after a successful publish. If a package fails, independent
  packages can still finish. The next run uses those tags to plan unfinished work. A successful publish whose tag was
  never written remains ambiguous: check its destination before retrying, or use a publisher that safely accepts
  repeated requests. See [recovery behavior](https://dispat.dev/reference/releasing/recovery/).
- **Manage prerelease versions in Git.** A prerelease lets you publish a version for testing before marking it stable.
  Use `%beta` to start one and `%beta>stable` to graduate it. `Release-As: none` holds a package, `Release-As: auto`
  resumes releases, and `Release-As: 2.0.0` selects an exact version. See
  [release controls](https://dispat.dev/reference/commits/#release-control) for the full syntax.
- **Keep your existing tools.** Configure shell commands for each package's build and publish stages. Dispat reads
  dependency manifests across Go, npm, Python, Cargo, Docker, mobile, and game projects. `dispat compute` can derive
  the graph and starting versions from these files. Your existing build caches continue to work inside those commands.
  [Aqua tool pins](https://dispat.dev/next/editing/manifests/#aqua) are supported in the unreleased version.
- **Run release steps separately.** Generate changelogs, create tags, write GitHub releases, or make release commits with
  standalone commands. Preview the plan with `dispat status`, select a subset with `dispat release -p core`, and use
  `dispat if`, `exec`, `autowriter`, or `autoreplacer` to compose your own workflow. The
  [release lock](https://dispat.dev/reference/releasing/release-lock/) coordinates concurrent release runs.


## Documentation

Start with [Getting started](https://dispat.dev/getting-started/), then dip into the references as
needed:

| Document                                                                                             | Contents                                                                                                                                                                                   |
|------------------------------------------------------------------------------------------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| [Getting started](https://dispat.dev/getting-started/)                                  | Install, first config, commit convention, commands, CI setup.                                                                                                                              |
| [Concepts](https://dispat.dev/concepts/)                                                | The mental model: baselines, propagation, trains, catch-up, the pipeline.                                                                                                                  |
| [Beside other release tools](https://dispat.dev/comparison/)                            | lerna, nx, release-please and changesets on the same failure, and the experiments that observed it.                                                                                        |
| [Examples](https://dispat.dev/examples/)                                                | A complete setup per package manager, each with real terminal output: npm, pnpm, Docker, Android, the mixed graph, adoption.                                                               |
| [Manifest tools](https://dispat.dev/editing/manifests/)                        | `dispat scanner` and `dispat writer`: reading and editing manifests on their own.                                                                                                          |
| [Editing across the monorepo](https://dispat.dev/editing/autowriter/)          | `dispat autowriter`: one manifest edit applied to every package the plan selects, or edits derived from the workspace.                                                                     |
| [Replacing across the monorepo](https://dispat.dev/editing/autoreplacer/) | `dispat autoreplacer`: literal text replaced in every package the plan selects.                                                                                                          |
| [The replacer](https://dispat.dev/editing/replacer/)                           | `dispat replacer` and `autoVersion.replace`: literal text for the versions no manifest holds.                                                                                              |
| [API reference](https://dispat.dev/api/)                                                | The three surfaces dispat exposes: the command line, the configuration file, and the Go packages.                                                                                          |
| [CLI](https://dispat.dev/cli/)                                                          | Every command, flag and exit code.                                                                                                                                                         |
| [dispat if](https://dispat.dev/cli/if/)                                                 | Branching on a variable, a file or the changed packages inside a configured script.                                                                                                        |
| [dispat for](https://dispat.dev/cli/for/)                                               | Running one shell command per item of a list, under whichever shell the repository configured.                                                                                             |
| [dispat exec](https://dispat.dev/cli/exec/)                                             | Running one declared script on its own.                                                                                                                                                    |
| [Naming a place](https://dispat.dev/cli/locations/)                                     | The `pkg:`, `space:`, `root` and `cwd` values `--for`, `--script-from` and `--in` share.                                                                                                    |
| [Configuration file](https://dispat.dev/configuration/)                                 | Top-level options, script sequences, run-level hooks; links the pages below.                                                                                                               |
| [Spaces](https://dispat.dev/configuration/spaces/)                                      | Space options, stages and hooks in execution order, versioning modes and groups, `scripts`, the space's `packages` map, the space configuration file, `.dispatexclude`. |
| [Packages](https://dispat.dev/configuration/packages/)                                  | Per-package overrides and the ladder that orders them, `src`, standalone packages, package dependencies, in-folder config files.                                                           |
| [What counts as a change](https://dispat.dev/configuration/change-scope/)               | `src` and `ignore`: which of a package's files make a scopeless commit address it, and the `.dispatignore` file.                                                                           |
| [Tags and baselines](https://dispat.dev/configuration/versions/)                        | `tagFormat` and `initials`.                                                                                                                                                                |
| [Alias tags](https://dispat.dev/configuration/alias-tags/)                              | Extra moving tags beside the release tag, and why an alias is never read back as a baseline.                                                                                               |
| [Release records](https://dispat.dev/configuration/records/)                            | Changelogs, GitHub releases, your own header and footer lines, the channels each record reaches, the release commit.                                                                       |
| [Commit parsing options](https://dispat.dev/configuration/parser/)                      | `commitErrors`, `nonPackageScopes`, the `parser` object, quieting the parser's diagnostics.                                                                                                |
| [dependencies](https://dispat.dev/configuration/dependencies/)                          | Declaring the consumer and provider relations the graph orders releases by, and the three levels one can be written at.                                                                                     |
| [autoVersion](https://dispat.dev/configuration/autoversion/)                            | Native manifest rewriting at the version stage: the parsing and replacing strategies, the write policy, and `syncLock`.                                                                    |
| [Script sequences](https://dispat.dev/configuration/scripts/)                           | `scripts` as named commands, one or several per name, how a `flow` name resolves package first, and what runs when.                                                                        |
| [Run-level hooks](https://dispat.dev/configuration/run-hooks/)                          | The seven hooks that observe the whole run rather than one package, where they execute, and why the step commands fire none of them.                                                       |
| [Webhooks](https://dispat.dev/configuration/webhooks/)                                  | The HTTP endpoints a release run notifies of its progress: the events, the signed payloads, and why a dead endpoint can never fail a release.                                              |
| [Static env](https://dispat.dev/configuration/env/)                                     | The `env` objects that add fixed variables to every script, and how the levels layer.                                                                                                      |
| [custom](https://dispat.dev/configuration/custom/)                                      | The free-form object dispat carries without ever reading it, for your own tooling.                                                                                                         |
| [Go packages](https://dispat.dev/go/)                                                   | The five modules dispat is built from, each importable on its own.                                                                                                                         |
| [ccme](https://dispat.dev/go/ccme/)                                                     | The commit parser as a library: entry points, configuration, diagnostics, bulk parsing.                                                                                                    |
| [scanner](https://dispat.dev/go/scanner/)                                               | The manifest reader as a library: what it reads, what comes back, the partial-result contract.                                                                                             |
| [writer](https://dispat.dev/go/writer/)                                                 | The manifest writer as a library: rewriting, replacing, relinking and build counters.                                                                                                      |
| [Commit messages](https://dispat.dev/reference/commits/)                                | Scope sets, directives, footers, channels, release control.                                                                                                                                |
| [Correcting a record](https://dispat.dev/reference/corrections/)                        | `Edits` and `Deletes`: restating or discarding a release record a pushed commit message got wrong, and what a revert does to the changelog.                                                 |
| [Script environment](https://dispat.dev/reference/environment/)                         | Every `DISPAT_*` variable, the listings, script outputs.                                                                                                                                   |
| [Shared versions](https://dispat.dev/reference/releasing/versioning/)                   | Packages that hold part of their version in common: the seven modes, what moves a group, sparse behaviour, versioning groups.                                                              |
| [Release steps](https://dispat.dev/reference/releasing/steps/)                          | The step commands from scratch: what they are for, why they are safe to repeat, and a worked flow.                                                                                         |
| [Partial releases](https://dispat.dev/reference/releasing/partial-releases/)            | Releasing part of the monorepo: the selection flags on `release` and `status`, why the order can hold a package back, and `--strict`.                                                      |
| [Prerelease branches](https://dispat.dev/reference/releasing/prerelease-branches/)      | A stable main and a beta branch: the train, the rc promotion, and exactly what merging to main does.                                                                                       |
| [The release lock](https://dispat.dev/reference/releasing/release-lock/)                | Why two releases at once are refused rather than raced, and how to clear a lock a killed run left behind.                                                                                  |
| [Recovering from a failed run](https://dispat.dev/reference/releasing/recovery/)        | A run that failed in the middle, and the re-run that finishes exactly what it still owed.                                                                                                  |
| [Diagnostic codes](https://dispat.dev/reference/plan-errors/)                           | Every code dispat can report: the three ways a run refuses to start, each error that aborts a plan, and what to do about it.                                                                |
| [dispat in CI](https://dispat.dev/reference/ci/)                                        | The GitHub Action, the container images, the install script: getting dispat onto a runner, and gating a pipeline on the plan with `--require-release`.                                      |
| [Pipeline patterns](https://dispat.dev/reference/pipelines/)                            | Testing only what a commit changed, the local-link bracket, and the gated release pipeline with `-p`/`-s`/`-g` passed from CI.                                                              |
| [Updating dispat](https://dispat.dev/reference/self-update/)                            | `dispat self-update`: how the binary replaces itself, what it reads out about the release, the backup and the rollback, the update notice.                                                 |
| [The install command](https://dispat.dev/cli/install/)                                  | `dispat install`: installing a tool from any GitHub repository's releases, choosing the asset and the destination, piping an archive, the idempotence gate, and a private repository.      |
| [Architecture](https://dispat.dev/internals/architecture/)                              | Modules, algorithms, execution model, design decisions, testing.                                                                                                                           |
| [Test coverage](https://dispat.dev/internals/coverage/)                                 | What the suite reaches, per package, measured by the release that published the page.                                                                                                      |
| [Test results](https://dispat.dev/internals/test-results/)                              | What the suite did: the counts, the timings, the race pass, and what each area covers.                                                                                                     |
| [Release experiments](https://dispat.dev/internals/experiments/)                        | The published binary against a registry that refuses an upload and a branch that moves mid-release, beside lerna, nx and changesets in the same fixture.                                     |
| [Integration tests](../../tests/integration)                                                         | The black-box suite itself: setup, running, and the test plan.                                                                                                                             |

See [`dispat.example.json`](./dispat.example.json) and [`dispat.example.yaml`](./dispat.example.yaml) to inspect every
configuration option in a single annotated file.

## Testing

dispat verifies its failure semantics across two independent layers run by [CI](../../.github/workflows/tests.yml) on
every push. First, unit tests evaluate logic against in-memory fakes. Second, a black-box
[integration suite](../../tests/integration) builds the binary and runs it against disposable git repositories,
asserting on git state, JSON logs, and nanosecond-resolution execution timelines.

Every release calculates fresh test metrics and publishes them directly to the docs site. You can review code reach in
[test coverage](https://dispat.dev/internals/coverage/), execution counts and timings in
[test results](https://dispat.dev/internals/test-results/), and verified scenarios in the
[test plan](../../tests/integration/docs/test-plan.md).

## On the roadmap

These planned features outline future development. Nothing described below exists yet, and current releases do not
depend on them.

**Managing the tools a release needs.** [`dispat install`](https://dispat.dev/cli/install/) already fetches one tool
from any GitHub release, verifies it, and installs it, which is the same engine
[`dispat self-update`](https://dispat.dev/cli/self-update/) replaces dispat with. A setup step therefore needs no
package manager and no second downloader.

What is still missing is the declarative half. Your configuration cannot yet list the tools a stage needs, so each one
is a line in a setup script rather than a pinned entry dispat resolves and installs on its own, and a tool published
anywhere other than a GitHub release still has to be fetched by hand.

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
modules, four container images, and the [documentation site](https://dispat.dev/), each with its own tag,
changelog, and GitHub release. The release workflow lives in [release.yml](../../.github/workflows/release.yml),
following the structure described in the [CI reference](https://dispat.dev/reference/ci/).

See the [repository README](../../README.md#projects-using-dispat) for the full list of projects. If you release a
project with dispat, share it on Discord to get it listed.

## Community

Join the community to ask questions, report issues, or share projects:
**[discord.gg/83PwVSCCmk](https://discord.gg/83PwVSCCmk)**.

You can also submit bug reports and feature requests directly through
[GitHub issues](https://github.com/yohimik/dispat/issues).
