# The status command

Run `dispat status` to see which packages would release and which versions they would get. It prints the dependency
graph and stops without running scripts, writing files, or creating tags. Use the same selection flags you plan to
pass to `dispat release`.

dispat computes the plan for the whole repository and narrows it exactly as a release would. This means the printed
graph shows exactly what `dispat release` will do with the same flags. See
[Releasing part of the graph](./release.md#releasing-part-of-the-graph) for selection rules and `--strict` refusals,
and [Exit codes](./README.md#exit-codes) for exit values.

Start here when a release refuses its plan. You can inspect the planning diagnostics and rerun the command as you
make corrections. Read [Diagnostic codes](../reference/plan-errors.md) to understand what
each error means.

## Flags

You can use these flags alongside the [global flags](./README.md#global-flags):

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

Turns a tolerated finding into a failure for `release` and `status`. dispat refuses the plan before anything is
published if it cannot release your selection as it stands. This happens if a package is waiting for its providers or
if you split a versioning group. See [Releasing part of the graph](./release.md#releasing-part-of-the-graph).

### `--require-release`

Forces `release` and `status` to exit `3` if the plan releases nothing, distinct from exit `1` for failures. Use this
for a CI stage that must publish something. dispat prints the graph first so you see the plan that explains the
refusal. Only packages this run will actually publish count, while held, withheld, or unselected packages do not. See
[Gating a pipeline on the plan](../reference/ci.md#gating-a-pipeline-on-the-plan).

### `--choreographer`

Adds a worker node for this invocation, written `name` or `name=endpoint`, beside the ones
[`execution.choreographers`](../configuration/execution.md#links-named-on-the-command-line) lists. You can repeat it. With
worker links, from the file or from the flag, `status` fixes and prints the plan digest a distributed run would carry,
in its `plan fixed` line, and it still takes no lock, resolves no remote, probes no node and assigns nothing. The
links are no part of the digest. A value that is neither a node name alone nor `name=endpoint` with both halves stated
exits `2`, and a link breaking a rule a configured link is held to exits `1` with `E225`.

### `--distribution-mode`

Selects `compilation` (default), `interpretation` or `hybrid`. `status` validates the chosen mode alongside the plan and
starts no return-channel listener or task. Unknown values are refused.
