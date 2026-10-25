# The release command

Run `dispat release` to publish the pending release. It prints the plan, runs each changed package's version, build,
and publish steps, then records the releases and tags. Running `dispat` without a command does the same thing.

Use [`dispat status`](./status.md) to inspect the plan first and [`dispat preview`](./preview.md) to read the pending
release notes.

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
group releases and prints a warning (`W231`). Nothing goes out of order, and the next run makes the group whole: a
member left behind releases the work the group has already published at the version that carries it, and a member with
no pending work of its own rides up to it (`W234`). The split is temporary and needs no operator.

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

Before hooks or version writers run, dispat protects paths that the release could change destructively. With release
commits enabled, it refuses staged, unstaged, or untracked changes in selected package folders and `commit.include`
paths so the automatic commit cannot absorb earlier work. It applies the same check to each selected package whose
`revertOnFail` setting could reset its folder. Commit or stash those protected paths, then run the release again.

When both release commits and `revertOnFail` are disabled, dispat leaves writer output in the working tree and performs
no automatic Git reset. Existing changelog or manifest edits can then be input to the release, including output kept
from an interrupted attempt.

## Flags

Beside the [global flags](./README.md#global-flags):

### `--package`, `-p`

Select named packages. Repeat the flag or separate names with commas. Names are case-insensitive and accept `*`
globs; quote them so the shell does not expand them first. For example, `-p core,web` selects two packages and `-p '*'`
selects every package. See [Choosing the packages](./run.md#choosing-the-packages).

### `--space`, `-s`

Select every package in the named spaces. This accepts the same lists and globs as `--package`. Standalone packages
do not belong to a space, so `-s '*'` does not include them. See
[Choosing the packages](./run.md#choosing-the-packages).

### `--group`, `-g`

Select every package in a named [versioning group](../reference/releasing/versioning.md), using the same lists and
globs as `--package`. A group may be a `versionGroups` entry or a space that versions together, and may span several
spaces. See [Choosing the packages](./run.md#choosing-the-packages).

### `--strict`

Turns a tolerated finding into a failure. For `release` and `status`, this refuses a selection the plan cannot release
as it stands, like a package waiting for its providers or a split versioning group. The command fails before anything
is published; read [Releasing part of the graph](./release.md).

### `--require-release`

Exits `release` and `status` with `3` when the plan releases nothing, which helps gate a CI stage that exists purely to
publish something. On `release` the [lock](#the-release-lock) is still taken first and given straight back, because
whether there is work to do is not known until after planning; `dispat status --require-release` answers the same
question without touching the remote and is what a CI gate should call. Only packages this run actually publishes
count, so held, withheld, or unselected packages do not; see
[Gating a pipeline on the plan](../reference/ci.md#gating-a-pipeline-on-the-plan).

### `--worker`

Adds a worker node for this run, written `name` or `name=endpoint`, beside the ones
[`execution.workers`](../configuration/execution.md#links-named-on-the-command-line) lists, which is how a pipeline
names a machine it created a minute before the release. A name alone reaches the repository being released, and an
endpoint names another mailbox. You can repeat it. The link is held to every rule a configured one is, the file has
to name `execution.secretEnv`, and a process running under a task's authority or a node whose file says
`role: worker` refuses the flag with `E226`. See [Distributed execution](../distributed-execution.md).
