# The release command

Run `dispat release` to plan your release and print the graph. The command runs version, build, and publish steps for
every changed package, then records releases and tags. Running a bare `dispat` does exactly the same thing.

## Releasing part of the graph

Pass `--package`, `--space`, or `--group` to `dispat release` and `dispat status` to read the same
[selection](./run.md#choosing-the-packages) every other command uses. You can also run the command from a package or
space folder to select it.

dispat computes the plan for the whole repository and narrows it afterwards, so a selection decides *what* is released
and never *at which version*. Running `dispat release -p core` releases core at exactly the version a full release
would give it.

Selections follow one specific rule based on publish order. A selected package is **withheld** if its provider is
releasing in the same plan and is *not* selected. Releasing the package first would ship release notes crediting a
provider version that does not exist yet (§19.2).

dispat reports this as `W230` alongside the providers it waits for. The rest of the selection still releases, and the
next run releases the withheld package. This rule is transitive, and a provider that is unchanged or held is nothing to
wait for.

A [versioning group](../reference/releasing/versioning.md) is a softer case. A selection that takes only part of a
group releases and prints a warning (`W231`). Nothing goes out of order, and the members left behind ride up to the
group's version on the next run (`W234`), so the split is temporary and needs no operator.

Pass the group name directly with `dispat release -g platform` to select every member at once and avoid splitting it.

Pass `--strict` to refuse both cases before anything is built, published, or tagged. Either the selection goes out
exactly as written, or nothing does. Running `status` with this flag exits `1` for the same selections, making it a
useful gate to put in front of a release job.

dispat prints the graph either way, so a refusal always comes with the plan that explains it.

Read [Partial releases](../reference/releasing/partial-releases.md) for the full guide and worked output.

## The release lock

`dispat release` claims the repository before it plans anything by pushing a `dispat-release-lock` tag to the remote
(`commit.remote`, by default `origin`). It deletes this tag when the run ends, no matter how the run ends. If a second
release starts while the first is running, it cannot push the tag and fails with exit `1` before building, publishing,
or tagging anything.

dispat takes the lock on every release, whether or not `commit.push` is enabled, so your release job needs write access
to the remote either way. Set [`unsafeDisableLock: true`](../configuration/README.md) in the config or
`DISPAT_UNSAFE_DISABLE_LOCK=true` in the environment to switch the lock off. A repository with no remote to coordinate
through needs this setting, and no other command takes the lock.

Read [The release lock](../reference/releasing/release-lock.md) for the full guide, including how to clear a lock left
behind by a killed run.

## Flags

Beside the [global flags](./README.md#global-flags):

| Flag                  | Default     | Effect                                                                                                                                                                                                 |
|-----------------------|-------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `--package`, `-p`     |             | Narrows the eleven package-selecting commands (`release`, `status`, `run`, `preview`, `changelog`, `autoversion`, `autowriter`, `autoreplacer`, `commit`, `github`, `compute`) to the named packages. This flag is repeatable, comma-separated, matches case-insensitively, and accepts `*` globs, where `-p '*'` is every package. Read [Choosing the packages](./run.md#choosing-the-packages). |
| `--space`, `-s`       |             | Narrows the same eleven commands to every package in the named spaces using the same spellings. A standalone package belongs to no space. Read [Choosing the packages](./run.md#choosing-the-packages). |
| `--group`, `-g`       |             | Narrows the same eleven commands to every package in the named [versioning groups](../reference/releasing/versioning.md) using the same spellings. A group is a `versionGroups` entry or a space that versions as one, so it can cross spaces. Read [Choosing the packages](./run.md#choosing-the-packages). |
| `--strict`            |             | Turns a tolerated finding into a failure. For `release` and `status`, this refuses a selection the plan cannot release as it stands, like a package waiting for its providers or a split versioning group. The command fails before anything is published; read [Releasing part of the graph](./release.md). |
| `--require-release`   |             | Exits `release` and `status` with `3` when the plan releases nothing, which helps gate a CI stage that exists purely to publish something. dispat computes the plan *before* taking the [release lock](#the-release-lock), so a run that publishes nothing never takes the tag and never runs `beforeAll`. Only packages this run actually publishes count, so held, withheld, or unselected packages do not; see [Gating a pipeline on the plan](../reference/ci.md#gating-a-pipeline-on-the-plan). |
