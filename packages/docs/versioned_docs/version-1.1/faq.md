# FAQ

Read the most common questions, their short answers, and links to the full details. You can ask anything not covered
here on [Discord](https://discord.gg/83PwVSCCmk).

## What does dispat need before the first release?

Give dispat a git repository with full history, one configuration file naming the package folders and the build and
publish commands, and commits written as conventional commits. There is no runtime to install and no state file,
because the tags are the whole memory. Follow the four steps in [Getting started](./getting-started.md).

## Why did a package I did not touch release?

Run `dispat status` to see the exact cause in the `reason` field before anything runs. The package might share a
[version group](./reference/releasing/versioning.md) with something that moved (`fixed group versioning`, reported as
`W234`), or a provider's commit carried a propagation marker such as `feat(core)^` (`propagated from core`). The third
cause is a provider publishing in an earlier run where this package failed, so the current run discharges that debt
(`catch-up from core`, reported as `W193`).

## Why does `status` show fewer packages when I run it inside a package folder?

Standing inside a package folder narrows the invocation to that package, exactly as `--package` does. This narrowed
selection can withhold a package when its providers sit outside the selection (`W230`). Run dispat from the repository
root for the whole plan, or pass `--strict` to refuse a selection that costs anything, as explained in
[Partial releases](./reference/releasing/partial-releases.md).

## Why did my manifest move to a provider version I did not release in this run?

This is the auto-version
[reconciliation pickup](./configuration/autoversion.md#picking-up-providers-released-without-you). When a provider
releases without you, your package's next release resolves every declared range to the provider's current published
version, reported as `W197`. Lock-file scripts under `syncLock` only make the lock follow the manifest, and the
changelog omits the pickup because the provider's own release documented it.

## Why does a changelog entry say "No changes"?

A record entry is never empty. A release with nothing for its notes to group states its cause instead, such as a
version bump keeping a version group aligned, a version set by `Release-As`, a channel transition, or pending work
cancelled out by its own reverts. Read [Records](./configuration/records.md#changelog) for the exact lines.

## How do I run a prerelease line, and how do I end it?

Start the train with a channel directive on a commit, where `feat(core)%rc:` releases `1.3.0-rc.0` and later work
continues it to `rc.1`. End it with a transition like `release(core)%rc>stable:` to graduate the train and collect
every prerelease entry into the one entry stable readers see. If a graduation is refused as backwards (`E185`), pin the
graduation itself with `Release-As`, and check [Commits](./reference/commits.md) for all directives.

## A release failed halfway. What do I do?

Run dispat again, because there is no repair command and no state to clean. Packages that published keep their records,
while failed ones and their dependants are skipped or failed. The next run releases exactly what is still owed at the
versions it was owed, labelled as catch-up, as shown in
[Recovering from a failed run](./reference/releasing/recovery.md).

## Can I release only part of the monorepo?

Pass `--package`, `--space`, or `--group` to narrow any command, or stand in a folder to imply the narrowing. The plan
reports what the selection costs, and a split version group catches up on the next run (`W231`, `W234`). Pass
`--strict` to turn any cost into a refusal, and read [Partial releases](./reference/releasing/partial-releases.md) for
the full guide.

## Can dispat release packages that live in different repositories?

dispat cannot do this on its own, because the plan comes from one checkout with no graph spanning repositories and
nothing to order them by. You can work around this by giving dispat a single checkout that holds every configuration
and links the other repositories in as git submodules. Moving a submodule forward is a commit dispat reads like any
other, so releases across the whole fleet get ordered, versioned, and changelogged from one place, as explained in
[A control repository for many repositories](./control-repository.md).

## How do I stop something from releasing?

Use `Release-As: none` to hold a package without discarding anything, and `Release-As: auto` to resume it at everything
accumulated. Write a `Deletes:` footer to discard one commit's record, or use `cancel(scope)` to erase the pending
ledger for a scope. These corrections reach only work that has not shipped, because released history is never
retracted, and [Correcting a record](./reference/corrections.md) covers them all.

## Does dispat ever rewrite git history?

dispat never rewrites history. It only adds a release commit, tags, and changelog entries prepended to a file. You
correct wrong records forward with `Edits:` and `Deletes:` footers in new commits, so the audit trail survives its own
mistakes.

## Which languages and package managers does it work with?

dispat works with any language, because a package is a folder and a stage is a shell command, so Go, npm, pnpm, Cargo,
Maven, Python, Docker, and the rest all sit in one dependency graph. Native manifest rewriting covers all thirty-five
formats dispat reads, from `package.json`, `go.mod`, `pom.xml`, `Cargo.toml`, `*.csproj`, `pubspec.yaml`, and compose
files to the project files Unity, Godot, Unreal, Defold, and O3DE keep a version in. The
[replace strategy](./configuration/autoversion.md) covers a version living somewhere no parser owns, such as a Gradle
coordinate or a Helm chart, and the [examples](./examples/README.md) show one setup per ecosystem.

## Can I start with one package and add more later?

A repository with one deliverable declares one standalone [`packages` entry](./examples/single-package.md) and no
spaces, and nothing has to be restructured when you add more later. Adding a landing page, a docs site, or an SDK is
one more entry each, plus an edge if they depend on one another. The tags already published stay the baselines
everything counts from, as shown in [A game, from one package to many](./examples/game.md).

## Do I have to use GitHub?

dispat only needs `git`, a POSIX shell, and the full history. GitHub releases are one opt-in recorder, and the
composite action is one of three ways to install the binary. Read
[The release job on other providers](./reference/ci-providers.md) for setups on GitLab CI, CircleCI, Jenkins,
Buildkite, and Azure Pipelines.

## How do I pin the dispat version in CI?

Pass a `version` input to the [GitHub action](./reference/ci.md#the-github-action) or pass `--version` to the
[install script](./getting-started.md#install). The container images also ship one CLI version per image tag. Both the
action and the script resolve the latest stable release when you leave the version unpinned.
