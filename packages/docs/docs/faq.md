# FAQ

The questions that come up most often, each with the short answer and the page that carries the long one. For anything
not covered here, the community lives on [Discord](https://discord.gg/83PwVSCCmk).

## What does dispat need before the first release?

A git repository with full history, one configuration file naming the package folders and the build and publish
commands, and commits written as conventional commits. There is no runtime to install and no state file: the tags are
the whole memory. [Getting started](./getting-started.md) walks the four steps.

## Why did a package I did not touch release?

Three legitimate reasons, and the plan names each one in its `reason` field. The package shares a
[version group](./reference/releasing/versioning.md) with something that moved (`fixed group versioning`, reported as
`W234`); a provider's commit carried a propagation marker such as `feat(core)^` (`propagated from core`); or a
provider published in an earlier run whose leg for this package failed, and the run is discharging that debt
(`catch-up from core`, reported as `W193`). `dispat status` shows the reason before anything runs.

## Why does `status` show fewer packages when I run it inside a package folder?

Standing inside a package folder narrows the invocation to that package, exactly as `--package` would. A narrowed
selection can also withhold a package whose providers are outside the selection (`W230`). Run dispat from the
repository root for the whole plan, or pass `--strict` to refuse a selection that costs anything.
[Partial releases](./reference/releasing/partial-releases.md) has the details.

## Why did my manifest move to a provider version I did not release in this run?

That is the auto-version
[reconciliation pickup](./configuration/autoversion.md#picking-up-providers-released-without-you): when a provider
released without you, your package's next release resolves every declared range to the provider's current published
version, reported as `W197`. Lock-file scripts under `syncLock` choose no versions; they only make the lock follow the
manifest. The changelog deliberately does not list the pickup, because the provider's own release documented it.

## Why does a changelog entry say "No changes"?

A record entry is never empty, so a release with nothing for its notes to group states its cause instead: a version
bump keeping a version group aligned, a version set by `Release-As`, a channel transition, or pending work its own
reverts cancelled out. [Records](./configuration/records.md#changelog) lists the exact lines.

## How do I run a prerelease line, and how do I end it?

A channel directive on a commit starts the train: `feat(core)%rc:` releases `1.3.0-rc.0`, later work continues it to
`rc.1`, and each prerelease entry documents only its own changeset. A transition ends it: `release(core)%rc>stable:`
graduates the train, and the stable entry collects the whole train into the one entry stable readers see. If a
graduation is refused as backwards (`E185`, typically after an exact pin raised the train), pin the graduation itself
with `Release-As`. [Commits](./reference/commits.md) covers the directives.

## A release failed halfway. What do I do?

Usually nothing but re-run. Packages that published keep their records, the failed ones and their dependants are
skipped or failed, and the next run releases exactly what is still owed at the versions it was owed, labelled as
catch-up. There is no repair command and no state to clean.
[Recovering from a failed run](./reference/releasing/recovery.md) shows a full sequence.

## Can I release only part of the monorepo?

Yes: `--package`, `--space` and `--group` narrow any command, and standing in a folder implies the narrowing. The plan
reports what the selection costs, a split version group catches up on the next run (`W231`, `W234`), and `--strict`
turns any cost into a refusal. [Partial releases](./reference/releasing/partial-releases.md) is the guide.

## Can dispat release packages that live in different repositories?

Not on its own: the plan comes from one checkout, so there is no graph spanning repositories and nothing to order
them by. The way around it is to give dispat a checkout that contains them all. One small repository holds every
configuration and links the others in as git submodules, and moving a submodule forward is a commit dispat reads like
any other, so releases across the whole fleet get ordered, versioned and changelogged from one place.
[A control repository for many repositories](./control-repository.md) is the pattern, the two layouts it comes in and
what it costs.

## How do I stop something from releasing?

The ladder, from softest to hardest: `Release-As: none` holds a package without discarding anything, and
`Release-As: auto` resumes it at everything accumulated; `Deletes:` discards one commit's record; `cancel(scope)`
erases the pending ledger for a scope. All of them reach only work that has not shipped: released history is never
retracted. [Correcting a record](./reference/corrections.md) covers the corrections.

## Does dispat ever rewrite git history?

No. dispat only adds: a release commit, tags, changelog entries prepended to a file. Records that are wrong are
corrected forward with `Edits:` and `Deletes:` footers in new commits, so the audit trail survives its own mistakes.

## Which languages and package managers does it work with?

Any: a package is a folder and a stage is a shell command, so Go, npm, pnpm, Cargo, Maven, Python, Docker and the rest
all sit in one dependency graph. Native manifest rewriting covers all thirty-five formats dispat reads, from
`package.json` and `go.mod` to `pom.xml`, `Cargo.toml`, `*.csproj`, `pubspec.yaml`, compose files and the game engine
project files Unity, Godot, Unreal, Defold and O3DE keep a version in; a version living somewhere no parser owns, such
as a Gradle coordinate or a Helm chart, is covered by the
[replace strategy](./configuration/autoversion.md). The [examples](./examples/README.md) show one setup per
ecosystem, and the table there says which page covers your manifest.

## Can I start with one package and add more later?

Yes, and nothing has to be restructured when you do. A repository with one deliverable declares one standalone
[`packages` entry](./examples/single-package.md) and no spaces. Adding a landing page, a docs site or an SDK later is
one more entry each, plus an edge if they depend on one another; the tags already published stay the baselines
everything counts from. [A game, from one package to many](./examples/game.md) walks both halves of that.

## Do I have to use GitHub?

No. dispat needs `git`, a POSIX shell and the full history. GitHub releases are one opt-in recorder, and the composite
action is one of three ways to install the binary. [The release job on other providers](./reference/ci-providers.md)
has GitLab CI, CircleCI, Jenkins, Buildkite and Azure Pipelines.

## How do I pin the dispat version in CI?

The [GitHub action](./reference/ci.md#the-github-action) takes a `version` input, the
[install script](./getting-started.md#install) takes `--version`, and the container images ship one CLI version per
image tag. With nothing pinned, both resolve the latest stable release.
