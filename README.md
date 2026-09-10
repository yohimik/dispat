# dispat <img alt="dispat logo" align="right" width="128" height="128" src="./imgs/logo.png" />

[![coverage](https://img.shields.io/endpoint?style=flat&url=https%3A%2F%2Fraw.githubusercontent.com%2Fyohimik%2Fdispat%2Fbadges%2Fcoverage.json)](https://github.com/yohimik/dispat/actions/workflows/tests.yml)

**dispat** reads your conventional commits, works out the next versions, and builds and publishes changed packages in
dependency order. Preview the release with `dispat status` before running it.

Use your existing build and publish commands across Go, npm, Cargo, Python, Docker, and other tools. dispat works with
one package, a [monorepo](https://dispat.dev/monorepo/), or several repositories joined through a
[control repository](https://dispat.dev/control-repository/).

Install the binary, run `dispat init` in your Git repository, and edit the generated `dispat.json` for your packages.
The [setup guide](https://dispat.dev/getting-started/) walks through your first release.

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

## Why one more release tool?

dispat brings version planning, builds, publication, and recovery into one dependency graph. It is especially useful
when a release crosses toolchains or destinations, and completing it requires more than running a build in order.

- **Deep dependency chains across toolchains.** Release `base image → runtime image → application image`, or
  `Go module → Go module → binary → Docker image → derived image`, with the same scheduler. Each provider can require
  its consumers to wait for publication before building: set `isBuildWaitingPublish: true` when a consumer needs a
  version fetched from a registry. Local workspace builds can instead use the provider's build output. The graph
  carries these requirements through every level. See the [Go](https://dispat.dev/examples/go/) and
  [Docker](https://dispat.dev/examples/docker/) examples.
- **Saga-style release recovery.** Publication consists of separate external writes. dispat records each successful
  package publication with a Git tag and uses those records to plan unfinished work on a rerun. A package failure
  leaves completed publications in place while unaffected graph branches can continue. If an upload succeeds before
  its record exists, inspect that destination before retrying. Publish scripts must reconcile their own partial
  writes; there is no automatic rollback across registries. See the
  [recovery experiments](https://dispat.dev/internals/experiments/).
- **Useful for a single-package project.** One package can still ship a binary, a container, release notes, and a
  website. Use the same versioning, channels, and recovery model without introducing a monorepo. Give independently
  recoverable deliverables their own package records, or make a multi-destination publish script resume each pending
  destination. See [single-package releases](https://dispat.dev/examples/single-package/).
- **Monorepos and polyrepos use the same release model.** A monorepo declares packages and their dependencies in one
  checkout. A [control repository](https://dispat.dev/control-repository/) can assemble separate repositories through
  pinned Git submodules and coordinate them with one graph. Its pointer-update commits carry release intent, and its
  history holds the release records; the linked repositories keep their own histories.
- **Your manifests and commands remain the inputs.** dispat reads supported project manifests, can derive dependency
  edges with `dispat compute`, and reconciles versions through `autoVersion`. Build and publish stages use your shell
  commands across Go, npm, Cargo, Python, Docker, mobile projects, and game engines. Configure the toolchain's checks,
  credentials, and artifact validation in those stages. [Commit messages](https://dispat.dev/reference/commits/)
  provide version intent and release notes.
- **Plan only the work the release needs.** Git history and release tags determine the changed packages and affected
  consumers. Unchanged packages stay outside the release plan. BuildKit layers, Go's build cache, and other existing
  caches can speed up the selected stages; dispat does not require a separate task-cache service. Preview the package
  versions and scope with `dispat status` before releasing through CI.

```console
$ dispat status
12:04:05 INF ● changed bump=minor package=core version="1.2.3 -> 1.3.0"
12:04:05 INF ● changed bump=patch package=app dueToProviders=[core] version="0.8.1 -> 0.8.2"
12:04:05 INF release plan ready packages=3 releasing=2
```

The same configuration supplies version, build, publish, and announcement stages, with hooks for project-specific
work. dispat coordinates their order and release records; your existing tools produce and publish the artifacts.

## Inspiration

dispat draws on tools and ideas that make complex work easier to inspect, compose, and recover:

- **Linux and Git** guided the CLI design: focused commands, explicit inputs, useful exit codes, and tools that work
  together. The shell tools used on [Linux](https://www.kernel.org/) inspired
  [`dispat if`](https://dispat.dev/cli/if/) and [`dispat for`](https://dispat.dev/cli/for/), which expose familiar
  conditional and looping control flow as commands. [Git](https://git-scm.com/) also supplies the history and release
  records that let you inspect how a release was planned and what it completed.
- **Database recovery and sagas** inspired the approach to reliable releases: record completed work, coordinate
  concurrent runs, and recover after partial failure. Garcia-Molina and Salem's
  [*Sagas* (1987)](https://www.cs.princeton.edu/research/techreps/598) describes long transactions made of smaller,
  independently committed steps. dispat applies that structure to publishing: it writes a Git tag after each
  successful publish and uses those records to plan unfinished work. A
  [release lock](https://dispat.dev/reference/releasing/release-lock/) coordinates concurrent runs, without a separate
  release database or lock service. If a publisher succeeds before its tag is written, check the destination before
  retrying. See [recovery behavior](https://dispat.dev/concepts/#failure-and-recovery).
- **[Lerna](https://lerna.js.org/)**, and the workspaces of [npm](https://docs.npmjs.com/cli/using-npm/workspaces) and
  [pnpm](https://pnpm.io/workspaces) it grew up beside. Between them they proved that many packages in one repository
  can share a dependency graph. They also proved that versioning and publishing all of them can be one command. dispat
  takes that idea beyond JavaScript and rebuilds it around an explicit dependency graph and an explicit error model.
- **[Conventional Commits](https://www.conventionalcommits.org/)**: commit messages as machine-readable release intent.
  The dispat parser, [`pkg/ccme`](./pkg/ccme), implements a strict superset of Conventional Commits 1.0.0 that adds the
  monorepo dimension. It treats scopes as packages and handles propagation depth and prerelease channels.

## Where to go

- **[Agent work guide](./specs/agent-guide/README.md)**: if you are a coding agent working with dispat, start with
  `dispat --version` and read the versioned guide linked by `dispat --help`. Use the latest published guide patch
  on that CLI's major/minor release line, not the guide from another release line or the default branch. If you
  cannot check for newer patches, use the pinned guide from help. Keep configuration and API references pinned
  to the installed CLI release, and follow the guide's CI/CD release workflow.
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
- **[writer](./pkg/writer)**: the manifest writer. It makes targeted, validated edits for the supported writable
  manifests and preserves their supported structure and formatting. Format-specific tools such as the Go formatter
  may normalize the file. Writes are atomic. This is the library behind auto-versioning and the `dispat writer`
  command.
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
  Every package received its own tag, changelog entry, and GitHub release. The release also announces itself: its
  `announce` stage renders a card from the changelog it just wrote and posts it to Instagram and LinkedIn with
  [crier](https://github.com/yohimik/crier), from the same Actions run, which
  [the announcement page](https://dispat.dev/internals/announce/) describes.
- **[crier](https://github.com/yohimik/crier)**: a single-package repository, one Go module and one binary, which
  renders HTML templates to images and video and posts them to fourteen social platforms. It shows that dispat needs no
  monorepo. Its first release was a breaking change on the `rc` channel, and eighteen release candidates followed in
  two days, each with its own changelog entry and its own GitHub release flagged as a prerelease. One transition commit
  then graduated the train to 1.0.0: the stable version is computed over the whole train rather than counted by hand,
  and the graduation collects every candidate's notes into the one entry stable readers see. This is the train and
  graduation flow that Kubernetes runs its own releases through, here driven by conventional commits alone. A moving
  `v1` alias tag is scoped to the stable channel, so `uses: yohimik/crier@v1` never picks up a candidate. crier also
  shows how a release integrates with dispat: the `announce` stage of the run renders a card from the release-notes
  variables dispat sets and posts it from the binary the run just built, and its [release-changelog
  example](https://github.com/yohimik/crier/tree/main/examples/release-changelog) wires the same card into any dispat
  release as one more publish step. It installs with `dispat install yohimik/crier`.

## Community

Bring your questions, issues, or projects you release with dispat to the community. Come and say hello:
**[discord.gg/83PwVSCCmk](https://discord.gg/83PwVSCCmk)**.

You can also open bugs and feature requests as [GitHub issues](https://github.com/yohimik/dispat/issues), whichever
suits you better.

## License

Official dispat release binaries are available under [MIT](./LICENSE), including the dispat executable in the
official container images. Third-party components retain their own license terms.

Source files that reference or incorporate the GPL-covered CCME specification, algorithms or proofs are licensed
under GPL-3.0-or-later and carry an explicit SPDX notice. The [CCME specification](./specs/ccme-spec) and its
accompanying material are also GPL-3.0-or-later. The [CCME parser](./pkg/ccme) remains [MIT](./pkg/ccme/LICENSE),
including its specification references. Other source files remain MIT unless they have a separate license notice.

The MIT grant for official binaries does not offer an MIT alternative for GPL-designated source files.
See [LICENSE](./LICENSE) for the scope of each grant. Previously distributed copies retain their original licenses.
