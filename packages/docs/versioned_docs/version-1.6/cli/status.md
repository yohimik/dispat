# The status command

Run `dispat status` to plan and print the graph with its computed version bumps. dispat exits without executing,
tagging, or writing anything. You can pass the same selection flags that a release takes.

dispat computes the plan for the whole repository and narrows it exactly as a release would. This means the printed
graph shows exactly what `dispat release` will do with the same flags. See
[Releasing part of the graph](./release.md#releasing-part-of-the-graph) for selection rules and `--strict` refusals,
and [Exit codes](./README.md#exit-codes) for exit values.

Run this command first when a release refuses to start. Every diagnostic a release would raise appears here, and you
can run it safely as many times as you need. Read [Diagnostic codes](../reference/plan-errors.md) to understand what
each error means.

## Flags

You can use these flags alongside the [global flags](./README.md#global-flags):

### `--package`, `-p`

Narrows the plan to the named packages. You can repeat this flag, pass a comma-separated list, and use `*` globs
(`-p '*'` selects every package). dispat matches names case-insensitively across every package-selecting command
(`release`, `status`, `run`, `preview`, `changelog`, `autoversion`, `autowriter`, `autoreplacer`, `commit`, `github`,
`compute`). See [Choosing the packages](./run.md#choosing-the-packages).

### `--space`, `-s`

Narrows the plan to every package in the named spaces. You can use the same spelling rules and globs as `--package`
across the same eleven commands. A standalone package belongs to no space. See
[Choosing the packages](./run.md#choosing-the-packages).

### `--group`, `-g`

Narrows the plan to every package in the named [versioning groups](../reference/releasing/versioning.md). You can use
the same spelling rules across the same eleven commands. A group is a `versionGroups` entry or a space that versions as
one, so it can cross spaces. See [Choosing the packages](./run.md#choosing-the-packages).

### `--strict`

Turns a tolerated finding into a failure for `release` and `status`. dispat refuses the plan before anything is
published if it cannot release your selection as it stands. This happens if a package is waiting for its providers or
if you split a versioning group. See [Releasing part of the graph](./release.md#releasing-part-of-the-graph).

### `--require-release`

Forces `release` and `status` to exit `3` if the plan releases nothing, distinct from exit `1` for failures. Use this
for a CI stage that must publish something. dispat prints the graph first so you see the plan that explains the
refusal. Only packages this run will actually publish count, while held, withheld, or unselected packages do not. See
[Gating a pipeline on the plan](../reference/ci.md#gating-a-pipeline-on-the-plan).
