# dispat <img alt="dispat logo" align="right" width="128" height="128" src="./imgs/logo.png" />

[![tests](https://github.com/yohimik/dispat/actions/workflows/tests.yml/badge.svg)](https://github.com/yohimik/dispat/actions/workflows/tests.yml)
[![coverage](https://img.shields.io/endpoint?style=flat&url=https%3A%2F%2Fraw.githubusercontent.com%2Fyohimik%2Fdispat%2Fbadges%2Fcoverage.json)](https://github.com/yohimik/dispat/actions/workflows/tests.yml)

**dispat** is a release tool for polyglot monorepos. It reads your conventional commits to find which packages changed
and calculates their next semantic versions. Then it propagates those bumps to dependants and builds every package in
dependency order. Finally, dispat publishes them in parallel, writing changelogs, git tags, and GitHub releases on the
way out.

Polyglot is the point. A package is a folder and a stage is a shell command. This means npm, Go, Cargo, Maven, .NET,
Python, Ruby, Dart, Docker, iOS, and Android sit in one dependency graph and release together.

You install one binary and write one config file. You run no daemon, manage no state file, and operate no cache.

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
# ...or on GitHub Actions
- uses: yohimik/dispat@v1
- run: dispat --log-format json
```

Pull container images for any other CI system: `yohimik/dispat-alpine`, `-ubuntu`, `-debian`, and `-dind`. See
[dispat in CI](https://dispat.dev/reference/ci/).

<p align="center">
  <img src="imgs/demo-release.gif" width="830"
       alt="An animated dependency graph of four packages across npm, Go, and Docker: commits decide the blast radius, builds and publishes run in dependency order in parallel, a failed build stays contained while its consumer is skipped, and a re-run finishes exactly what the first run still owed">
</p>

## Why one more monorepo tool?

Every major monorepo tool can topologically sort a dependency graph to build everything in order. Turborepo, Nx, Bazel,
Pants, Buck2, and moon schedule work across such a graph. Lerna, changesets, Rush, Melos, cargo-release,
semantic-release, the npm, pnpm, and yarn workspace commands, and the Maven and Gradle reactors turn one into a
release.

As of dispat 1.0.0 in August 2026, the split is always the same. The language-agnostic tools stop before the release,
and the tools that publish are built around a single ecosystem. Only nx release and release-please reach further, but
neither goes all the way.

Two situations break that model in practice:

1. **An error in the middle of a run.** Half the packages are published and half are not. Most tools either abort the
   whole run or carry on and leave you to reconstruct what shipped. Where recovery exists, it is a registry query like
   `lerna publish from-package`. A registry answers only whether a version is already there, never what the run still
   owes. This is also only one ecosystem's answer. There is no equivalent for a Docker tag, a GitHub release, or a
   Maven deploy that simply rejects the version it has. Recovery ends up being a script you write.
2. **A consumer that can only be *built* once its provider is *published*.** A Node package can be built before its
   providers publish. A Docker image is often buildable only by pulling its base image from a registry, which means the
   provider has to be published first. Building everything and then publishing everything assumes every ecosystem
   behaves like npm, and mixed graphs break it.

Modern projects are exactly that mix. You wire many packages on different infrastructure into one dependency graph,
placing npm next to Docker next to Go. dispat is built for that case.

```console
$ dispat
12:04:05 INF ● changed bump=minor package=core version="1.2.3 -> 1.3.0"
12:04:05 INF ● changed bump=patch package=app dueToProviders=[core] version="0.8.1 -> 0.8.2"
12:04:05 INF release plan ready packages=3 releasing=2
12:04:05 INF published package=core tag=core@1.3.0
12:04:05 INF published package=app tag=app@0.8.2
12:04:05 INF done published=2 failed=0 skipped=0
```

- **Polyglot by construction.** Packages are folders and stages are plain shell commands. This means any language,
  build system, registry, CI, or cache plugs in with no integration work. On top of that, dispat reads and rewrites
  thirty-five manifest formats across twenty ecosystems. It supports npm, Go, Cargo, Python, Composer, Maven, the .NET
  project and nuspec family, Dart, Ruby, Dockerfiles, and compose files. It also supports mobile platforms (Info.plist,
  project.pbxproj, Podfile, and .podspec on iOS; AndroidManifest.xml, Gradle build scripts, and version catalogs on
  Android) and game engines (Unity, Godot, Unreal, Defold, and O3DE), which keep their versions in files no package
  manager understands. Set the per-[space](https://dispat.dev/concepts/) `isBuildWaitingPublish` option
  to state whether a consumer's build needs its provider merely *built*, as node does, or already *published*, as
  docker does. This schedules a four-level npm to docker chain correctly with no extra work.
- **No task cache, because there is nothing to cache.** Most monorepo tools make unchanged work cheap by running it and
  short-circuiting on a cache hit. This buys you cache keys, a remote cache to operate, invalidation rules, and a
  command to clear the cache when it gets one wrong. dispat computes which packages changed from git history and tags.
  Packages that did not change are absent from the plan, so their scripts never start. Skipping work you never
  scheduled needs no cache, no state file, and no daemon, and it cannot go stale. This approach also composes. Because
  dispat caches nothing itself, whatever you already cache keeps working untouched inside the stage. This includes
  BuildKit layers, an Nx, Turborepo or Bazel cache, ccache, or the Gradle build cache. None of it can affect which
  versions get computed, in what order things publish, or what gets tagged.
- **Built around an error model, not a happy path.** A failure never aborts the run. dispat skips the broken package's
  consumers unless they have changes of their own, and every unaffected subgraph keeps releasing. Failed and skipped
  consumers are not lost. The next run catches them up automatically at the exact version they were originally owed,
  with no state file and no double release. Recovery is re-running.
- **The graph can come from the manifests themselves.** Run `dispat compute` to read the packages' project files
  (package.json, go.mod, Cargo.toml, pyproject.toml, composer.json, pom.xml, .csproj, pubspec.yaml, requirements files,
  Dockerfiles, and compose files). dispat derives the consumer and provider graph from them, including an image chain
  read straight off the `FROM` lines. You can preview suggestions, confirm them one by one, or apply them wholesale.
  Pass `--check` to gate CI on a graph that has drifted. Set `keep: true` to mark deliberate relations no manifest
  declares. A space with an `autoVersion` block goes further. dispat rewrites its manifests at the version stage,
  reconciling declared ranges to end-of-run versions without disturbing their formatting. Run `syncLock` scripts like
  `npm install` to regenerate lock files between version and build. Both libraries are commands of their own too. Run
  `dispat scanner` to print what a folder's manifests declare, and run `dispat writer` to edit one in place. Neither
  needs a config file or a git repository.
- **A release is treated as what it really is: a distributed transaction.** Publishing a graph of packages means
  irreversible writes across independent services (an npm registry, a Docker registry, GitHub) with no rollback to fall
  back on. dispat handles this the way distributed systems do. Each package's leg commits by durably recording its own
  completion as an annotated git tag, written only once the publish succeeds. There are no state files and no registry
  queries, so nothing can drift from what actually happened. Recovery is deterministic replay, because the plan is a
  pure function of history, graph, and configuration. A re-run recomputes the same transaction and executes only the
  legs whose record is missing. Completed work is never repeated, owed work is never lost, and the run converges
  however many times it is interrupted.

You could probably wire the same thing up in a general-purpose task scheduler with enough YAML and glue. dispat
deliberately does less. It handles release logic only, meaning build and publish to a registry with versioning,
tagging, and changelogs around them.

That focus is what keeps it easy to configure. You write one file and fill a fixed set of script slots (`version`,
`build`, `publish`, `announce`, `login`, per-stage hooks, `onFail`, and `onSkip`) with shell commands. dispat supplies
the ordering, the orchestration, and the failure semantics.

## Inspiration

dispat stands on the shoulders of two things:

- **[Lerna](https://lerna.js.org/)**, and the workspaces of [npm](https://docs.npmjs.com/cli/using-npm/workspaces) and
  [pnpm](https://pnpm.io/workspaces) it grew up beside. Between them they proved that many packages in one repository
  can share a dependency graph. They also proved that versioning and publishing all of them can be one command. dispat
  takes that idea beyond JavaScript and rebuilds it around an explicit dependency graph and an explicit error model.
- **[Conventional Commits](https://www.conventionalcommits.org/)**: commit messages as machine-readable release intent.
  The dispat parser, [`pkg/ccme`](./pkg/ccme), implements a strict superset of Conventional Commits 1.0.0 that adds the
  monorepo dimension. It treats scopes as packages and handles propagation depth and prerelease channels.

## Where to go

- **[dispat](./services/dispat)**: the CLI itself. Read a terminal tour, the key features, and the full documentation.
  This includes getting started, an example per package manager, concepts, CLI, configuration, commit messages, script
  environment, architecture, and coverage.
- **[ccme](./pkg/ccme)**: the dispat Conventional Commits extension as a standalone Go parser. It includes the vendored
  specification, the two-axis propagation grammar, performance notes, and fuzzing.
- **[models](./pkg/models)**: the public configuration model. External tooling uses this to author dispat configs as
  typed values and marshal them to loadable files.
- **[config](./pkg/config)**: the configuration loader as a standalone Go library. It parses JSON, YAML, and TOML into
  one tree, composes files through `$ref`, finds the file a command was run beneath, and decodes through setter tables
  with no reflection at all, which is what lets it link under TinyGo. This is the library behind dispat's own config
  reading.
- **[manifest](./pkg/manifest)**: the shared manifest vocabulary. It defines dependency kinds, manifest file-name
  rules, and name normalisation. This keeps the reader and writer halves in exact agreement.
- **[scanner](./pkg/scanner)**: the manifest reader as a standalone Go library. It parses package.json, go.mod,
  Cargo.toml, pyproject.toml, composer.json, pom.xml, the .NET project, nuspec and packages family, pubspec.yaml,
  Gemfile, .gemspec, and requirements files into one ecosystem-neutral shape. It also reads Dockerfiles, compose files,
  and the mobile platforms (Info.plist, project.pbxproj, Podfile, and .podspec on iOS; AndroidManifest.xml, Gradle
  version catalogs, and build scripts on Android). This is the library behind `dispat compute`, auto-versioning, and
  the `dispat scanner` command.
- **[writer](./pkg/writer)**: the manifest writer. It performs format-preserving, byte-precise in-place edits for
  **every** manifest the scanner reads. It provides atomic writes and validated output. This is the library behind
  auto-versioning and the `dispat writer` command.
- **[docker](./docker)**: the four container images. Each is a dispat package whose `docker-compose.yml` *is* its
  manifest. The build stage runs `docker compose build`, and the publish stage runs `docker compose build --push`.
- **[infra](./infra)**: the Google Cloud footprint that serves [dispat.dev](https://dispat.dev), written only from CI
  through the dispat release: Terraform plans in the build stage, applies in the publish stage, and each applied state
  is an `infra/v*` tag.
- **[Integration tests](./tests/integration)**: the black-box suite that compiles the real binary and drives it against
  disposable git repositories. Read about setup, running, results, and the test plan.
- **[docs](./packages/docs)**: the documentation site itself, released by dispat like any other package. Learn how to
  run it locally, why its build is the link checker, and how a version snapshot and a deploy are cut.

## Projects using dispat

- **dispat itself**: this repository is a polyglot Go, npm, and Docker workspace. It is released by the dispat binary
  built from its own checkout, and it has been since the very first release. The first stable run cut eleven packages
  in one release. It rewrote the go.mod files of six Go modules to the released versions, regenerated go.sum files, and
  tagged each module the way Go expects (`pkg/ccme/v1.0.0`, `services/dispat/v1.0.0`). This keeps
  `go install github.com/yohimik/dispat/services/dispat@latest` working. It also attached six cross-compiled binaries
  to the CLI's GitHub release, published the four container images, and released the versioned documentation site.
  Every package received its own tag, changelog entry, and GitHub release.

## Community

Bring your questions, issues, or projects you release with dispat to the community. Come and say hello:
**[discord.gg/83PwVSCCmk](https://discord.gg/83PwVSCCmk)**.

You can also open bugs and feature requests as [GitHub issues](https://github.com/yohimik/dispat/issues), whichever
suits you better.

## License

This project is licensed under the MIT License. See the [LICENSE](./LICENSE) file for more information.

The CCME specification, [pkg/ccme/SPEC.md](./pkg/ccme/SPEC.md), is licensed under GPL-3.0-or-later, and dispat uses GPL
licensed materials from CCME. See [pkg/ccme/LICENSE-SPEC](./pkg/ccme/LICENSE-SPEC). The parser implementing it stays
under MIT along with the rest of the project.
