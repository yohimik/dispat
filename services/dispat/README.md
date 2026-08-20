# dispat <img alt="dispat logo" align="right" width="128" height="128" src="../../imgs/logo.png" />

The dispat command line tool: a single Go binary that plans and runs monorepo releases from your conventional commits.
What the tool is and why it exists is in the [repository README](../../README.md); this page is about using it.

## In the terminal

Point one config file at your packages, write commits as usual, and dispat works out the rest:

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

(Output abridged. The starter config still needs two things from you: which folders hold your packages, and the build
and publish commands to run in them.) If `api`'s build had failed, `core` and `utils` would still have shipped, the run
would exit non-zero, and the next run would release `api` at the exact version it was owed. Runs are self-healing, and
that failure model is the point of the tool; [Concepts](https://yohimik.github.io/dispat/concepts/) explains it.

A few more moves:

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

- **Releases the graph, not a list.** Consumer and provider ordering, parallel builds and publishes with separate
  concurrency budgets, and `isBuildWaitingPublish` for the ecosystems, Docker among them, where a consumer can only
  build once its provider is published.
- **Blast radius written in the commit.** `feat(core):` releases `core` alone, `^` reaches its direct consumers, `^^`
  the transitive closure, and `+N` exactly N edges. Nothing is released on a guess.
- **Self-healing runs, because a release is a distributed transaction.** Publishing a graph means irreversible writes
  across independent services with no rollback to fall back on, so each package's leg commits by durably recording its
  own completion: the annotated git tag, written only once the publish succeeded. A broken package skips only its true
  dependants while everything else keeps releasing, and re-running *is* the recovery: the plan is a pure function of
  history, graph and configuration, so the next run recomputes the same transaction and executes only the legs whose
  record is missing. No state files, no double releases and no repair scripts.
  [Details](https://yohimik.github.io/dispat/internals/architecture/).
- **Release control from commits.** `%beta` starts a prerelease train and `%beta>stable` graduates it,
  `Release-As: none` holds a package and `Release-As: auto` resumes it, `Release-As: 2.0.0` pins an exact version and
  `cancel(pkg)` discards pending work. It is written in commits, so release decisions are reviewed and versioned like
  code. [Details](https://yohimik.github.io/dispat/reference/commits/#release-control).
- **Polyglot by construction: any language, any registry, any tooling.** Stages are shell commands fed a rich
  [`DISPAT_*` environment](https://yohimik.github.io/dispat/reference/environment/), release state lives in git tags,
  and dispat reads and rewrites thirty-five manifest formats across twenty ecosystems, npm to `go.mod` to `Podfile` to
  the project files Unity, Godot, Unreal, Defold and O3DE keep a game's version in, so `dispat compute` can derive the
  dependency graph and each package's starting version from the repository you already have. And because an unchanged package is simply not in the plan, there is no task cache to manage, clear or
  distrust: BuildKit layers, an Nx, Turborepo or Bazel cache and the Gradle build cache all keep working inside the
  stage, and none of them can change what is versioned, ordered or tagged.
- **Every release step is also a command, with the records built in.** Per-package changelogs, annotated tags, GitHub
  releases and an optional release commit, each also runnable alone (`dispat changelog`, `commit`, `github`) with the
  release stage finding the work done and skipping it. `dispat status` dry-runs the whole plan, a
  [release lock](https://yohimik.github.io/dispat/reference/releasing/release-lock/) refuses two releases of one
  repository at once, and `dispat release -p core` (or `-s libs`, `-g platform`) ships a subset at exactly the versions
  a full release would have given it, with `dispat if`, `exec`, `autowriter` and `autoreplacer` as the glue a custom
  pipeline is assembled from. [Details](https://yohimik.github.io/dispat/reference/releasing/steps/).

## Documentation

Start with [Getting started](https://yohimik.github.io/dispat/getting-started/), then dip into the references as needed:

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

[`dispat.example.json`](./dispat.example.json) and [`dispat.example.yaml`](./dispat.example.yaml) show every option in
one annotated file.

## Testing

The failure semantics above are the tool's main promise, so they are tested at two independent layers, both run by
[CI](../../.github/workflows/tests.yml) on every push: unit tests against in-memory fakes, and a black-box
[integration suite](../../tests/integration) that compiles the real binary and drives it against disposable git
repositories, asserting on git state, JSON logs and nanosecond-resolution execution timelines.

No number about the suite is written here. Every release measures them and publishes them with the site it releases:
what the tests reach is in [test coverage](https://yohimik.github.io/dispat/internals/coverage/), what they did is in
[test results](https://yohimik.github.io/dispat/internals/test-results/), and the claim-by-claim matrix behind the
integration suite is its [test plan](../../tests/integration/docs/test-plan.md).

## On the roadmap

Written down so the shape of it is not a surprise. Nothing described here exists yet, and nothing in the first release
depends on it.

**Managing the tools a release needs.** Today every stage script assumes its tools are already on the machine, so
getting them there is somebody else's problem: a CI setup step, a Dockerfile layer, a package manager, or a `curl` line
at the top of a script. That is the one part of a release pipeline dispat has no opinion about, and it is where "works
on my machine" tends to survive.

The plan is to let a repository declare the CLI tools its stages need and have dispat fetch them, from a **GitHub
release** or from a **URL you provide**, pinned to the version the config names. The machinery already exists, since
that is what [`dispat self-update`](https://yohimik.github.io/dispat/cli/self-update/) does for dispat's own binary
today: resolving a release, checking the download against the published checksum, and putting the result somewhere the
next command can find it. Pointing it at other tools is the feature.

**A native package in every ecosystem.** dispat ships as a static binary, which asks a repository that already has a
package manager to install its release tool some other way. The plan is to distribute dispat through the ecosystems it
releases, as a thin package per ecosystem wrapping the same binary: an **npm package** a JavaScript repository adds as a
development dependency, pins in the lockfile it already reviews and runs with `npx dispat`, and the equivalent wherever
else a repository expects its tools to come from its own package manager. Go already works this way, since `go install`
is that ecosystem's version of the idea.

Each wrapper is also where what belongs to one ecosystem rather than to releasing in general can live: the npm one
carries what an npm publish expects around a `package.json` and a workspace, the next one carries its own equivalent.
dispat already reads and rewrites those manifests, so this is about meeting each ecosystem where its users are, not
about teaching dispat a new format.

## Projects using dispat

dispat's own monorepo is the reference deployment: since the very first release, every release of this repository has
been planned and published by the dispat built from the same checkout. The first stable run cut eleven packages in one
release: this CLI with six static binaries (Linux, macOS and Windows, on amd64 and arm64), the six Go modules, four
container images and the [documentation site](https://yohimik.github.io/dispat/), each with its own tag, changelog
entry and GitHub release. The workflow behind it lives in the repository
([release.yml](../../.github/workflows/release.yml)) and follows the same shape the
[CI reference](https://yohimik.github.io/dispat/reference/ci/) documents for any repository; the full list of projects
is in the [repository README](../../README.md#projects-using-dispat).

Releasing a project with dispat? Share it on the Discord below and it can be listed here.

## Community

Have questions or issues? Want to share a project you release with dispat?
Come and say hello: **[discord.gg/83PwVSCCmk](https://discord.gg/83PwVSCCmk)**.

Bugs and feature requests are welcome as [GitHub issues](https://github.com/yohimik/dispat/issues) too, whichever suits
you better.
