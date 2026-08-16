# dispat <img alt="dispat logo" align="right" width="128" height="128" src="./imgs/logo.png" />

[![tests](https://github.com/yohimik/dispat/actions/workflows/tests.yml/badge.svg)](https://github.com/yohimik/dispat/actions/workflows/tests.yml)
[![coverage](https://img.shields.io/endpoint?style=flat&url=https%3A%2F%2Fraw.githubusercontent.com%2Fyohimik%2Fdispat%2Fbadges%2Fcoverage.json)](https://github.com/yohimik/dispat/actions/workflows/tests.yml)

**dispat** is a release tool for polyglot monorepos. It reads your conventional commits, works out which packages
changed and what their next semantic versions are, propagates those bumps to dependants, then builds and publishes
every package in dependency order, in parallel, writing changelogs, git tags and GitHub releases on the way out.

Polyglot is the point. A package is a folder and a stage is a shell command, so npm, Go, Cargo, Maven, .NET, Python,
Ruby, Dart, Docker, iOS and Android sit in one dependency graph and release together.

One binary, one config file. No daemon, no state file and no cache to operate.

```sh
# Linux and macOS
curl -fsSL https://raw.githubusercontent.com/yohimik/dispat/main/install.sh | sh
```

```sh
# ...or with wget
wget -qO- https://raw.githubusercontent.com/yohimik/dispat/main/install.sh | sh
```

```powershell
# Windows, in PowerShell
irm https://raw.githubusercontent.com/yohimik/dispat/main/install.ps1 | iex
```

```yaml
# ...or on GitHub Actions (pin @v1 once 1.0.0 is out; the v1 alias tracks the latest stable)
- uses: yohimik/dispat@main
- run: dispat --log-format json
```

Container images for every other CI system: `yohimik/dispat-alpine`, `-ubuntu`, `-debian`, `-dind`. See
[dispat in CI](https://yohimik.github.io/dispat/reference/ci).

## Why one more monorepo tool?

Every major monorepo tool can topologically sort a dependency graph: build everything in order, then publish
everything, or publish only what changed, one package at a time. Two situations break that model in practice:

1. **An error in the middle of a run.** Half the packages are published and half are not. Most tools either abort the
   whole run or plough on and leave you to reconstruct what shipped. Re-running tends to re-release what is already
   out, so you end up writing recovery scripts by hand.
2. **A consumer that can only be *built* once its provider is *published*.** A Node package can be built before its
   consumers publish, but a Docker image is often buildable only by pulling its base image from a registry, which means
   the provider has to be published first. Build everything then publish everything assumes every ecosystem behaves
   like npm, and mixed graphs break it.

Modern projects are exactly that mix: many packages on different infrastructure, npm next to Docker next to Go, wired
into one dependency graph. dispat is built for that case.

```console
$ dispat
12:04:05 INF ● changed bump=minor package=core version="1.2.3 -> 1.3.0"
12:04:05 INF ● changed bump=patch package=app dueToProviders=[core] version="0.8.1 -> 0.8.2"
12:04:05 INF release plan ready packages=3 releasing=2
12:04:05 INF published package=core tag=core@1.3.0
12:04:05 INF published package=app tag=app@0.8.2
12:04:05 INF done published=2 failed=0 skipped=0
```

- **Polyglot by construction.** Packages are folders and stages are plain shell commands, so any language, build
  system, registry, CI or cache plugs in with no integration work. On top of that dispat reads and rewrites twenty-three
  manifest formats across fifteen ecosystems: npm, Go, Cargo, Python, Composer, Maven, the .NET project and nuspec
  family, Dart, Ruby, Dockerfiles and compose files, and the mobile platforms (Info.plist, project.pbxproj, Podfile and
  .podspec on iOS; AndroidManifest.xml, Gradle build scripts and version catalogs on Android). The per-[space](https://yohimik.github.io/dispat/concepts)
  `isBuildWaitingPublish` option states whether a consumer's build needs its provider merely *built*, as node does, or
  already *published*, as docker does, so a four-level npm to docker chain schedules correctly with no extra work.
- **No task cache, because there is nothing to cache.** Most monorepo tools make unchanged work cheap by running it and
  short-circuiting on a cache hit, which buys you cache keys, a remote cache to operate, invalidation rules and a
  command to clear the cache when it gets one wrong. dispat computes which packages changed from git history and tags,
  and the ones that did not are simply absent from the plan: their scripts never start. Skipping work you never
  scheduled needs no cache, no state file and no daemon, and it cannot go stale. It also composes: because dispat
  caches nothing itself, whatever you already cache keeps working untouched inside the stage, whether that is BuildKit
  layers, an Nx, Turborepo or Bazel cache, ccache or the Gradle build cache. None of it can affect which versions get
  computed, in what order things publish, or what gets tagged.
- **Built around an error model, not a happy path.** A failure never aborts the run. The broken package's consumers are
  skipped unless they have changes of their own, and every unaffected subgraph keeps releasing. Failed and skipped
  consumers are not lost: the next run catches them up automatically, at the exact version they were originally owed,
  with no state file and no double release. Recovery is re-running.
- **The graph can come from the manifests themselves.** `dispat compute` reads the packages' project files
  (package.json, go.mod, Cargo.toml, pyproject.toml, composer.json, pom.xml, .csproj, pubspec.yaml, requirements files,
  Dockerfiles and compose files) and derives the consumer and provider graph from them, including an image chain read
  straight off the `FROM` lines. Suggestions can be previewed, confirmed one by one or applied wholesale, `--check`
  gates CI on a graph that has drifted, and `keep: true` marks deliberate relations no manifest declares. A space with
  an `autoVersion` block goes further: dispat rewrites its manifests at the version stage, reconciling declared ranges
  to end-of-run versions without disturbing their formatting, and `syncLock` scripts such as `npm install` regenerate
  lock files between version and build. Both libraries are commands of their own too: `dispat scanner` prints what a
  folder's manifests declare and `dispat writer` edits one in place, neither needing a config file or a git repository.
- **A release is treated as what it really is: a distributed transaction.** Publishing a graph of packages means
  irreversible writes across independent services (an npm registry, a Docker registry, GitHub) with no rollback to fall
  back on, so dispat handles it the way distributed systems do. Each package's leg commits by durably recording its own
  completion: the annotated git tag, written only once the publish succeeded. There are no state files and no registry
  queries, so nothing can drift from what actually happened. Recovery is deterministic replay, because the plan is a
  pure function of history, graph and configuration: a re-run recomputes the same transaction and executes only the
  legs whose record is missing. Completed work is never repeated, owed work is never lost, and the run converges however
  many times it is interrupted.

Could you wire the same thing up in a general-purpose task scheduler? With enough YAML and glue, probably. dispat
deliberately does less: release logic only, meaning build and publish to a registry with versioning, tagging and
changelogs around them. That focus is what keeps it easy to configure. One file, and a fixed set of script slots
(`version`, `build`, `publish`, `announce`, `login`, per-stage hooks, `onFail` and `onSkip`) that you fill with shell
commands. dispat supplies the ordering, the orchestration and the failure semantics.

## Inspiration

dispat stands on the shoulders of two things:

- **[Lerna](https://lerna.js.org/)**, and the workspaces of [npm](https://docs.npmjs.com/cli/using-npm/workspaces) and
  [pnpm](https://pnpm.io/workspaces) it grew up beside. Between them they proved that many packages in one repository
  can share a dependency graph, and that versioning and publishing all of them can be one command. dispat takes that
  idea beyond JavaScript and rebuilds it around an explicit dependency graph and an explicit error model.
- **[Conventional Commits](https://www.conventionalcommits.org/)**: commit messages as machine-readable release intent.
  dispat's parser, [`pkg/ccme`](./pkg/ccme), implements a strict superset of Conventional Commits 1.0.0 that adds the
  monorepo dimension: scopes as packages, propagation depth and prerelease channels.

## Where to go

- **[dispat](./services/dispat)**: the CLI itself. A terminal tour, the key features and the full documentation
  (getting started, an example per package manager, concepts, CLI, configuration, commit messages, script environment,
  architecture, coverage).
- **[ccme](./pkg/ccme)**: dispat's Conventional Commits extension as a standalone Go parser, with the vendored
  specification, the two-axis propagation grammar, performance notes and fuzzing.
- **[models](./pkg/models)**: the public configuration model, so external tooling can author dispat configs as typed
  values and marshal them to loadable files.
- **[manifest](./pkg/manifest)**: the shared manifest vocabulary (dependency kinds, manifest file-name rules, name
  normalisation) that keeps the reader and writer halves in exact agreement.
- **[scanner](./pkg/scanner)**: the manifest reader as a standalone Go library. package.json, go.mod, Cargo.toml,
  pyproject.toml, composer.json, pom.xml, the .NET project, nuspec and packages family, pubspec.yaml, Gemfile, .gemspec
  and requirements files, all parsed into one ecosystem-neutral shape, plus Dockerfiles and compose files and the
  mobile platforms: Info.plist, project.pbxproj, Podfile and .podspec on iOS, AndroidManifest.xml, Gradle version
  catalogs and build scripts on Android. This is the library behind `dispat compute`, auto-versioning and the
  `dispat scanner` command.
- **[writer](./pkg/writer)**: the manifest writer. Format-preserving, byte-precise in-place edits for **every** manifest
  the scanner reads, with atomic writes and validated output; the library behind auto-versioning and the
  `dispat writer` command.
- **[docker](./docker)**: the four container images, each a dispat package whose `docker-compose.yml` *is* its
  manifest, so the build and publish stages are nothing but `docker compose build` and `docker compose push`.
- **[Integration tests](./tests/integration)**: the black-box suite that compiles the real binary and drives it against
  disposable git repositories; setup, running, results and the test plan.
- **[docs](./packages/docs)**: the documentation site itself, released by dispat like any other package: how to run it
  locally, why its build is the link checker, and how a version snapshot and a deploy are cut.

## Projects using dispat

- **dispat itself**: this repository is a Go multi-module workspace released by the dispat binary built from its own
  checkout. One run rewrites the six modules' go.mod files to the released versions, regenerates their go.sum files,
  tags each module the way Go expects (`pkg/ccme/v1.0.0`, `services/dispat/v1.0.0`), and publishes a GitHub release per
  module with the cross-compiled binaries attached to the CLI's, which is what keeps
  `go install github.com/yohimik/dispat/services/dispat@latest` working.
- [webxash3d-fwgs](https://github.com/yohimik/webxash3d-fwgs): a WebAssembly port of the Xash3D-FWGS game engine. A
  real docker on docker on npm provider chain, four levels deep, with parallel builds from the engine package up to the
  modded server image.

## Community

Have questions or issues? Want to share a project you release with dispat?
Come and say hello: **[discord.gg/83PwVSCCmk](https://discord.gg/83PwVSCCmk)**.

Bugs and feature requests are welcome as [GitHub issues](https://github.com/yohimik/dispat/issues) too, whichever suits
you better.

## License

This project is licensed under the MIT License. See the [LICENSE](./LICENSE.md) file for more information.
