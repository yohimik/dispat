# The preview command

`dispat preview` plans, then prints the pending release notes: the breaking-changes/features/fixes sections plus
provider updates that the next release's changelog entry and GitHub release body would carry. It covers every package
that has something pending, in publish order, narrowed by
[`--package` / `--space` / `--group` or the invocation folder](./run.md#choosing-the-packages). It follows the
[release-notes windowing](../configuration/records.md#changelog), so a pending prerelease previews only its own
changeset. Prints `no pending changes` when nothing is — naming the selection when there was one.

## Flags

Beside the [global flags](./README.md#global-flags):

| Flag                  | Default     | Effect                                                                                                                                                                                                 |
|-----------------------|-------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `--package`, `-p`     |             | Every package-selecting command (`release`, `status`, `run`, `preview`, `changelog`, `autoversion`, `commit`, `github`, `compute`): narrow to the named packages. Repeatable and comma-separated, matched case-insensitively, `*` globs (`-p '*'` is every package); see [Choosing the packages](./run.md#choosing-the-packages).                     |
| `--space`, `-s`       |             | The same nine commands: narrow to every package of the named spaces, with the same spellings. A standalone package belongs to no space; see [Choosing the packages](./run.md#choosing-the-packages).            |
| `--group`, `-g`       |             | The same nine commands: narrow to every package of the named [versioning groups](../releasing/versioning.md), with the same spellings. A group is a `versionGroups` entry or a space that versions as one, so it may cross spaces; see [Choosing the packages](./run.md#choosing-the-packages).            |
