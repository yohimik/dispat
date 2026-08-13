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
that failure model is the point of the tool; [Concepts](https://yohimik.github.io/dispat/concepts) explains it.

A few more moves:

```sh
$ dispat preview                    # pending release notes, all packages (-p core: just one)
$ dispat scanner packages/core      # what that folder's manifests declare; no config or git needed
$ dispat writer packages/core/package.json --set @acme/utils=^2.0.0   # one format-preserving edit
$ dispat autowriter --set @acme/utils='^{version}' --since all       # ...the same edit in every package
$ dispat autowriter --set-local --since all                          # ...or every workspace range, worked out
$ dispat replacer --sub 'com.acme:core:1.2.0=>com.acme:core:1.3.0' build.gradle   # literal text, no parsing
$ dispat run build --since all -p core   # try the build script in packages/core with the real DISPAT_* env
$ dispat release -g platform        # release one versioning group; -s libs releases one space
$ dispat lint                       # run a script in every changed package that has it, in graph order
$ dispat self-update                # replace this binary with the latest release; --rollback puts the old one back
$ git commit -m "feat(core)%beta: try it out"
$ dispat                            # releases core@1.6.0-beta.0; graduate later with release(core)%stable:
```

## Key features

- **Releases the graph, not a list.** Consumer/provider ordering, parallel builds and publishes with separate
  concurrency budgets, and `isBuildWaitingPublish` for ecosystems (like Docker) where a consumer can only build after
  its provider is published.
- **Blast radius written in the commit.** `feat(core):` releases `core` alone; `^` reaches direct consumers, `^^`
  the transitive closure, `+N` exactly N edges. Nothing is released on a guess.
- **Self-healing runs.** Failures don't stop the world and are never lost: a broken package skips only its true
  dependants while the rest keeps releasing, and the next run catches the skipped ones up at the exact version they were
  owed. No state files, no double releases, no repair scripts: re-running *is* the recovery.
- **Release control from commits.** `%beta` starts a prerelease train and `%beta>stable` graduates it;
  `Release-As: none` holds a package and `Release-As: auto` resumes it; `Release-As: 2.0.0` pins an exact version and
  `cancel(pkg)` discards pending work. It is all written in commits, so release decisions are reviewed and versioned
  like code. [Details](https://yohimik.github.io/dispat/reference/commits#release-control).
- **Polyglot by construction: any language, any registry, any tooling.** Stages are shell commands fed a rich
  [`DISPAT_*` environment](https://yohimik.github.io/dispat/reference/environment), scripts pass values to each other through `$DISPAT_OUTPUT`, and
  release state lives in git tags, so every build system, CI and caching layer works from inside a script unchanged.
  dispat reads and rewrites fifteen manifest families: npm, Go, Cargo, Python, Composer, Maven, the .NET
  project/nuspec family, Dart, Ruby, Dockerfiles and compose files, and the mobile platforms (Info.plist,
  project.pbxproj, Podfile and .podspec on iOS; AndroidManifest.xml, Gradle build scripts and version catalogs on
  Android). `dispat compute` derives the dependency graph, and the baseline each package starts from, out of the
  packages' own manifests, so adopting dispat in a repository that already ships versions takes one command. An
  `autoVersion` space has dispat rewrite its manifests natively at the version stage; native rewriting of *dependency
  ranges* covers `package.json` and `go.mod`, and other ecosystems reconcile theirs from a `flow.version` script.
- **Release records built in, safe by design.** Per-package changelogs, annotated tags, GitHub releases and an optional
  release commit plus push, all customisable per package. `dispat status` dry-runs the whole plan, credentials are
  verified before any work, and nothing is ever published against an unpublished dependency. Two releases of one
  repository at once are refused rather than raced: a run claims the repository with a
  [release lock](https://yohimik.github.io/dispat/reference/releasing/release-lock) before it plans anything.
- **Release part of the monorepo when you need to.** `dispat release --package core` (or `--space libs`, or
  `--group platform` for a whole version group, or just the folder you are standing in) releases a subset at exactly
  the versions a full release would have given it. Publish
  order still rules: a package whose provider is releasing and unselected waits for the next run instead of shipping
  ahead of it, and `--strict` refuses a selection that cannot go out cleanly before anything is built.
  [Details](https://yohimik.github.io/dispat/reference/releasing/partial-releases).
- **Edit every package at once.** `dispat autowriter` applies one manifest edit across every package the plan selects,
  finding each package's manifests itself: bump a shared dependency everywhere, or derive the edits from the workspace
  with `--set-local` and `--link-local`. `dispat autosubstitute` does the same for literal text, so hand-written
  coordinates in READMEs, badges and install snippets follow a release too. Both take the same selection flags as
  `dispat run`, and `dispat scanner`, `dispat writer` and `dispat replacer` expose the same libraries for one folder or
  one file, needing no config and no git repository.
  [Details](https://yohimik.github.io/dispat/cookbook/editing/autowriter).
- **Every release step is also a command.** `dispat changelog`, `autoversion`, `commit` and `github` run one thing the
  release normally does, at the moment your own flow needs it, and the release stage then finds the work done and skips
  it. `dispat if` branches on an environment variable and `dispat exec` runs one declared script once, so a custom
  pipeline can be assembled from the same pieces without giving up the ordering.
  [Details](https://yohimik.github.io/dispat/reference/releasing/steps).

## Documentation

Start with [Getting started](https://yohimik.github.io/dispat/getting-started), then dip into the references as needed:

| Document                                                                        | Contents                                                                                            |
|---------------------------------------------------------------------------------|-----------------------------------------------------------------------------------------------------|
| [Getting started](https://yohimik.github.io/dispat/getting-started)             | Install, first config, commit convention, commands, CI setup.                                       |
| [Cookbook](https://yohimik.github.io/dispat/cookbook)                           | Ready-made setups with real terminal output: npm, Docker, Android, failure recovery, beta channels. |
| [Concepts](https://yohimik.github.io/dispat/concepts)                           | The mental model: baselines, propagation, trains, catch-up, the pipeline.                           |
| [Shared versions](https://yohimik.github.io/dispat/reference/releasing/versioning)                  | Packages that hold part of their version in common: the seven modes, what moves a group, sparse behaviour, versioning groups. |
| [Release steps](https://yohimik.github.io/dispat/reference/releasing/steps)                         | The step commands from scratch: what they are for, why they are safe to repeat, and a worked flow.  |
| [Partial releases](https://yohimik.github.io/dispat/reference/releasing/partial-releases)           | Releasing part of the monorepo: the selection flags on `release` and `status`, why the order can hold a package back, and `--strict`. |
| [The release lock](https://yohimik.github.io/dispat/reference/releasing/release-lock)               | Why two releases at once are refused rather than raced, and how to clear a lock a killed run left behind. |
| [CLI](https://yohimik.github.io/dispat/cli)                                     | Every command, flag and exit code.                                                                  |
| [dispat if](https://yohimik.github.io/dispat/cli/if)                             | Branching on a variable inside a configured script.                                                 |
| [dispat exec](https://yohimik.github.io/dispat/cli/exec)                        | Running one declared script on its own.                                                             |
| [Configuration file](https://yohimik.github.io/dispat/configuration)            | Top-level options, script sequences, run-level hooks; links the pages below.                        |
| [Spaces](https://yohimik.github.io/dispat/configuration/spaces)                 | Space options, stages and hooks, versioning modes and groups, `scripts`, the space's `packages` map and `dependencies`, the space configuration file, `.dispatexclude`. |
| [Packages](https://yohimik.github.io/dispat/configuration/packages)             | Per-package overrides and the ladder that orders them, `src`, standalone packages, package dependencies, in-folder config files. |
| [What counts as a change](https://yohimik.github.io/dispat/configuration/change-scope) | `src` and `ignore`: which of a package's files make a scopeless commit address it, and the `.dispatignore` file. |
| [Tags and baselines](https://yohimik.github.io/dispat/configuration/versions)   | `tagFormat` and `initials`.                                                                         |
| [Alias tags](https://yohimik.github.io/dispat/configuration/alias-tags)         | Extra moving tags beside the release tag, and why an alias is never read back as a baseline.        |
| [Release records](https://yohimik.github.io/dispat/configuration/records)       | Changelogs, GitHub releases, your own header and footer lines, holding prereleases back, the release commit. |
| [Commit parsing options](https://yohimik.github.io/dispat/configuration/parser) | `commitErrors`, `nonPackageScopes`, the `parser` object, quieting the parser's diagnostics.         |
| [dependencies](https://yohimik.github.io/dispat/configuration/dependencies)     | Declaring the consumer/provider relations the graph orders releases by, at the root or inside a space. |
| [Script sequences](https://yohimik.github.io/dispat/configuration/scripts)      | `scripts` as named commands, how a `flow` name resolves package first, and what runs when.          |
| [Run-level hooks](https://yohimik.github.io/dispat/configuration/run-hooks)     | The seven hooks that observe the whole run rather than one package, and where they execute.         |
| [Static env](https://yohimik.github.io/dispat/configuration/env)                | The `env` objects that add fixed variables to every script, and how the levels layer.               |
| [custom](https://yohimik.github.io/dispat/configuration/custom)                 | The free-form object dispat carries without ever reading it, for your own tooling.                  |
| [Manifest tools](https://yohimik.github.io/dispat/cookbook/editing/manifests)                    | `dispat scanner` and `dispat writer`: reading and editing manifests on their own.                   |
| [Editing across the monorepo](https://yohimik.github.io/dispat/cookbook/editing/autowriter)      | `dispat autowriter`: one manifest edit applied to every package the plan selects, or edits derived from the workspace. |
| [Substituting across the monorepo](https://yohimik.github.io/dispat/cookbook/editing/autosubstitute) | `dispat autosubstitute`: literal text replaced in every package the plan selects.               |
| [The replacer](https://yohimik.github.io/dispat/cookbook/editing/replacer)                       | `dispat replacer` and `autoVersion.replace`: literal text for the versions no manifest holds.       |
| [Commit messages](https://yohimik.github.io/dispat/reference/commits)                     | Scope sets, directives, footers, channels, release control.                                         |
| [Script environment](https://yohimik.github.io/dispat/reference/environment)              | Every `DISPAT_*` variable, the listings, script outputs.                                            |
| [dispat in CI](https://yohimik.github.io/dispat/reference/ci)                             | The GitHub Action, the container images, the install script: getting dispat onto a runner.          |
| [Updating dispat](https://yohimik.github.io/dispat/reference/self-update)                 | `dispat self-update`: how the binary replaces itself, the backup and the rollback, the update notice. |
| [Architecture](https://yohimik.github.io/dispat/internals/architecture)                   | Modules, algorithms, execution model, design decisions, testing.                                    |
| [Test coverage](https://yohimik.github.io/dispat/internals/coverage)                      | The per-package statement coverage table and how to reproduce it.                                   |
| [Integration tests](../../tests/integration)                                    | The black-box suite: setup, running, results; links the test plan.                                  |

[`dispat.example.json`](./dispat.example.json) and [`dispat.example.yaml`](./dispat.example.yaml) show every option in
one annotated file.

## Testing

The failure semantics above are the tool's main promise, so they are tested at two independent layers (over 500 test
functions plus fuzzing, run by [CI](../../.github/workflows/tests.yml) on every push): unit tests against in-memory
fakes, and a black-box [integration suite](../../tests/integration) that compiles the real binary and drives it against
disposable git repositories, asserting on git state, JSON logs and nanosecond-resolution execution timelines
([results](../../tests/integration/docs/test-results.md), [test plan](../../tests/integration/docs/test-plan.md)).
Together they hold **95.6%** workspace statement coverage
([per-package table](https://yohimik.github.io/dispat/internals/coverage), [test inventory](https://yohimik.github.io/dispat/internals/architecture#testing)).

## Community

Have questions or issues? Want to share a project you release with dispat?
Come and say hello: **[discord.gg/83PwVSCCmk](https://discord.gg/83PwVSCCmk)**.

Bugs and feature requests are welcome as [GitHub issues](https://github.com/yohimik/dispat/issues) too, whichever suits
you better.
