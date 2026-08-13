# The release command

`dispat release` plans, prints the graph, then runs version/build/publish for every changed package, records releases
and tags. It is what a bare `dispat` does.

## Releasing part of the graph

`dispat release` and `dispat status` read the same [selection](./run.md#choosing-the-packages) every other command does:
`--package`, `--space`, `--group`, or the package or space folder the command was invoked from. The plan is computed for the
whole repository and narrowed afterwards, so a selection decides *what* is released and never *at which version*:
`dispat release -p core` releases core at exactly the version a full release would have given it.

One rule is the selection's own, and it comes from publish order. A selected package whose provider is releasing in
the same plan and is *not* selected is **withheld**: releasing it first would ship release notes crediting a provider
version that does not exist yet (§19.2). It is reported as `W230` with the providers it waits for, the rest of the
selection still releases, and the next run releases it. The rule is transitive, and a provider that is unchanged or
held is nothing to wait for.

A [versioning group](../reference/releasing/versioning.md) is the softer case: a selection that takes only part of one releases and warns
(`W231`). Nothing goes out of order, and the members left behind are ridden up to the group's version by the next run
(`W210`), so the split is temporary and needs no operator. Naming the group itself, `dispat release -g platform`,
takes every member at once and so can never split it.

`--strict` refuses both, before anything is built, published or tagged: either the selection goes out as written or
nothing does. On `status` it exits `1` for the same selections, which makes it a gate to put in front of a release
job. The graph is printed either way, so a refusal always comes with the plan that explains it.

The full guide, with worked output, is [Partial releases](../reference/releasing/partial-releases.md).

## The release lock

Before it plans anything, `dispat release` claims the repository: it pushes a `dispat-release-lock` tag to the remote
(`commit.remote`, by default `origin`) and deletes it when the run ends, however the run ends. A second release started
while the first is running cannot push that tag, so it is refused with exit `1` before it builds, publishes or tags
anything.

The lock is taken on every release, whether or not `commit.push` is enabled, so the release job needs write access to
the remote either way. [`unsafeDisableLock: true`](../configuration/README.md) in the config, or
`DISPAT_UNSAFE_DISABLE_LOCK=true` in the environment, switches it off, which is what a repository with no remote to
coordinate through needs. No other command takes it.

The full guide, including how to clear a lock a killed run left behind, is [The release lock](../reference/releasing/release-lock.md).

## Flags

Beside the [global flags](./README.md#global-flags):

| Flag                  | Default     | Effect                                                                                                                                                                                                 |
|-----------------------|-------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `--package`, `-p`     |             | Every package-selecting command (`release`, `status`, `run`, `preview`, `changelog`, `autoversion`, `autowriter`, `autosubstitute`, `commit`, `github`, `compute`): narrow to the named packages. Repeatable and comma-separated, matched case-insensitively, `*` globs (`-p '*'` is every package); see [Choosing the packages](./run.md#choosing-the-packages).                     |
| `--space`, `-s`       |             | The same eleven commands: narrow to every package of the named spaces, with the same spellings. A standalone package belongs to no space; see [Choosing the packages](./run.md#choosing-the-packages).            |
| `--group`, `-g`       |             | The same eleven commands: narrow to every package of the named [versioning groups](../reference/releasing/versioning.md), with the same spellings. A group is a `versionGroups` entry or a space that versions as one, so it may cross spaces; see [Choosing the packages](./run.md#choosing-the-packages).            |
| `--strict`            |             | Turns a tolerated finding into a failure. `release` and `status`: a selection the plan cannot release as it stands (a package waiting for its providers, a split versioning group), refused before anything is published; see [Releasing part of the graph](./release.md). |
| `--require-release`   |             | `release` and `status`: exit `1` when the plan releases nothing, for the CI stage whose point is that this run publishes something. The plan is computed *before* the [release lock](#the-release-lock), so a run that would publish nothing never takes the tag and never runs `beforeAll`. Only packages this run will actually publish count, and a held, withheld or unselected one does not; see [Gating a pipeline on the plan](../reference/ci.md#gating-a-pipeline-on-the-plan). |
