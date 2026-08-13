# The status command

`dispat status` plans and prints the graph with computed version bumps, then exits. Nothing is executed, tagged or
written. It takes the release's own selection flags.

The plan is computed for the whole repository and narrowed exactly as a release would narrow it, so the graph shows
what `dispat release` with the same flags is about to do; see
[Releasing part of the graph](./release.md#releasing-part-of-the-graph) for the selection rules and what `--strict`
refuses. What `status` exits with is in [Exit codes](./README.md#exit-codes).

Because it computes the same plan a release does and touches nothing, it is also the first thing to reach for when a
release refuses to start: every diagnostic a release would raise appears here first, as many times as you care to run
it. [When there is no plan](../reference/plan-errors.md) walks through what each of them means.

## Flags

Beside the [global flags](./README.md#global-flags):

| Flag                  | Default     | Effect                                                                                                                                                                                                 |
|-----------------------|-------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `--package`, `-p`     |             | Every package-selecting command (`release`, `status`, `run`, `preview`, `changelog`, `autoversion`, `autowriter`, `autosubstitute`, `commit`, `github`, `compute`): narrow to the named packages. Repeatable and comma-separated, matched case-insensitively, `*` globs (`-p '*'` is every package); see [Choosing the packages](./run.md#choosing-the-packages).                     |
| `--space`, `-s`       |             | The same eleven commands: narrow to every package of the named spaces, with the same spellings. A standalone package belongs to no space; see [Choosing the packages](./run.md#choosing-the-packages).            |
| `--group`, `-g`       |             | The same eleven commands: narrow to every package of the named [versioning groups](../reference/releasing/versioning.md), with the same spellings. A group is a `versionGroups` entry or a space that versions as one, so it may cross spaces; see [Choosing the packages](./run.md#choosing-the-packages).            |
| `--strict`            |             | Turns a tolerated finding into a failure. `release` and `status`: a selection the plan cannot release as it stands (a package waiting for its providers, a split versioning group), refused before anything is published; see [Releasing part of the graph](./release.md#releasing-part-of-the-graph). |
| `--require-release`   |             | `release` and `status`: exit `1` when the plan releases nothing, for the CI stage whose point is that this run publishes something. The graph is printed first, so the refusal comes with the plan that explains it. Only packages this run will actually publish count, and a held, withheld or unselected one does not; see [Gating a pipeline on the plan](../reference/ci.md#gating-a-pipeline-on-the-plan). |
