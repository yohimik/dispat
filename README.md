# dispat <img alt="dispat logo" align="right" width="128" height="128" src="./imgs/logo.png" />

[![tests](https://github.com/yohimik/dispat/actions/workflows/tests.yml/badge.svg)](https://github.com/yohimik/dispat/actions/workflows/tests.yml)
[![coverage](https://img.shields.io/endpoint?style=flat&url=https%3A%2F%2Fraw.githubusercontent.com%2Fyohimik%2Fdispat%2Fbadges%2Fcoverage.json)](https://github.com/yohimik/dispat/actions/workflows/tests.yml)

**dispat** releases monorepos. It reads conventional commits to track changed packages, computes their next semantic
versions (propagating bumps to dependants), and builds and publishes them in the right order, in parallel, with
changelogs, git tags and GitHub releases on the way out.

## Why one more monorepo tool?

Every major monorepo tool can topologically sort a dependency graph: build everything in order, then publish everything,
or publish only what changed, sequentially. Two situations break that model in practice:

1. **An error in the middle of a run.** Half the packages are published, half are not. Most tools either abort the whole
   run or plough on and leave you to reconstruct what shipped. Re-running tends to re-release things that are already
   out, or you end up writing recovery scripts by hand.
2. **A consumer that can only be *built* after its provider is *published*.** A Node package can be built before its
   consumers publish, but a Docker image is often buildable only by pulling its base image from a registry, which means
   the provider must already be published. "Build all, then publish all" assumes every ecosystem behaves like npm, and
   mixed graphs break it.

Modern projects are exactly that mix: many packages on different infrastructure (npm next to Docker next to Go) wired
into one dependency graph. dispat is built for that case:

- **Polyglot by construction.** Packages are just folders; build/publish/version steps are any shell commands. The
  per-space `isBuildWaitingPublish` option states whether a consumer's build needs the provider merely *built* (node)
  or already *published* (docker), so a four-level npm-to-docker chain schedules correctly out of the box.
- **Built around an error model, not a happy path.** A failure never aborts the run: the broken package's consumers are
  skipped (unless they have changes of their own) and every unaffected subgraph keeps releasing. Failed or skipped
  consumers are never lost. The next run catches them up automatically, at the exact version they were originally owed,
  with no state file and no double release. Recovery is just re-running.

Could you wire the same thing up in a general-purpose task scheduler? With enough YAML and glue, probably. dispat
deliberately does less: release logic only, meaning build and publish to a registry with versioning, tagging and
changelogs around them. That focus is what keeps it easy to configure: one file, a fixed set of script slots (`version`,
`build`, `publish`, `announce`, `login`, per-stage hooks, `onFail`/`onSkip`). You fill in shell commands; dispat
supplies the orchestration, ordering and failure semantics.

## Inspiration

dispat stands on the shoulders of two things:

- **[Lerna](https://lerna.js.org/)**, the original monorepo release manager, which proved that versioning and publishing
  many packages from one repository can be a single command. dispat takes that idea beyond JavaScript and rebuilds it
  around an explicit dependency graph and an explicit error model.
- **[Conventional Commits](https://www.conventionalcommits.org/)**: commit messages as machine-readable release intent.
  dispat's parser, [`pkg/ccme`](./pkg/ccme), implements a strict superset of Conventional Commits 1.0.0 that adds the
  monorepo dimension: scopes as packages, propagation depth, prerelease channels.

## Where to go

- **[dispat](./services/dispat)**: the CLI itself. A terminal tour, key features and the full documentation (getting
  started, concepts, CLI, configuration, commit messages, script environment, architecture, coverage).
- **[ccme](./pkg/ccme)**: dispat's Conventional Commits extension as a standalone Go parser: the vendored specification,
  the two-axis propagation grammar, performance notes and fuzzing.
- **[models](./pkg/models)**: the public configuration model, so external tooling can author dispat configs as typed
  values and marshal them to loadable files.
- **[Integration tests](./tests/integration)**: the black-box suite that compiles the real binary and drives it against
  disposable git repositories; setup, running, results and the test plan.

## Planned features

- **Per-package overrides within a space.** A package will be able to override its enclosing space's configuration
  (scripts, concurrency, `revertOnFail`, changelog/GitHub behavior, ...) for itself alone, so one-off exceptions no
  longer require carving a package out into its own space.
- **Computed dependency graph.** A command that analyzes packages' project files directly and derives the
  consumer/provider graph from them, so relations no longer have to be declared by hand in config; explicit overrides
  will be supported for cases the analysis can't or shouldn't infer.
- **Extendable config.** Configuration will be splittable across multiple files, so large monorepos don't have to keep
  every space and package declaration in one flat file.
- **Auto versioning for a broad range of languages.** Version bumps will be applied natively across package managers, so
  packages get automatic bump treatment without hand-rolled scripts.

## Projects using dispat (Real-world examples)

- [webxash3d-fwgs](https://github.com/yohimik/webxash3d-fwgs): WebAssembly port of the Xash3D-FWGS game engine. A real
  "docker depending on docker depending on npm" provider chain, four levels deep, with parallel builds from the engine
  package to the modded server image.

## License

This project is licensed under the MIT License. See the [LICENSE](./LICENSE.md) file for more information.
