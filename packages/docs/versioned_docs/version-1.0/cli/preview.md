# The preview command

`dispat preview` plans, then prints the pending release notes: the breaking-changes/features/fixes sections plus
provider updates that the next release's changelog entry and GitHub release body would carry. It covers every package
that has something pending, in publish order, narrowed by
[`--package` / `--space` / `--group` or the invocation folder](./run.md#choosing-the-packages). It follows the
[release-notes windowing](../configuration/records.md#changelog), so a pending prerelease previews only its own
changeset. Prints `no pending changes` when nothing is, naming the selection when there was one.

## Choosing which body to preview

The two records are configured separately, so they can say different things. `--changelog` prints the changelog entry
body and `--github` the GitHub release body, each under
[its own entry format](../configuration/records.md#entry-format-options-shared-by-changelog-and-github): its own
`header`, `footer` and `releaseName`. Together they print both under one header per package, labelled
`--- changelog ---` and `--- github release ---`. Naming neither prints the changelog entry.

This is where you check what the
[channel filters](../configuration/records.md#choosing-which-releases-a-line-reaches) actually do before releasing
anything, since a preview renders for the channel the pending release is on.

Two things a preview cannot show. The `### Release` block a GitHub body carries in [commit
mode](../configuration/records.md#commit) is built from what publishing exported, and nothing has been published, so
it is absent. And when a record would write nothing at all, the preview says so in its place:

```
## core@1.3.0-beta.0 (stable -> beta)

github release withheld: the channels do not admit beta
```

The reason is either `disabled by config` or the channels the record is restricted to. Whether a GitHub release is
created also depends on the [`DISPAT_EXPORT_GITHUB` export](../reference/environment.md#script-outputs), which no
preview can know; the note covers the configured policy, which is the part you can act on.

## Flags

Beside the [global flags](./README.md#global-flags):

| Flag                  | Default     | Effect                                                                                                                                                                                                 |
|-----------------------|-------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `--changelog`         | `false`     | Print the changelog entry body. The default when neither this nor `--github` is given.                                                                                                                 |
| `--github`            | `false`     | Print the GitHub release body, under the `github` entry format. Beside `--changelog`, both are printed and labelled.                                                                                    |
| `--package`, `-p`     |             | Every package-selecting command (`release`, `status`, `run`, `preview`, `changelog`, `autoversion`, `autowriter`, `autoreplacer`, `commit`, `github`, `compute`): narrow to the named packages. Repeatable and comma-separated, matched case-insensitively, `*` globs (`-p '*'` is every package); see [Choosing the packages](./run.md#choosing-the-packages).                     |
| `--space`, `-s`       |             | The same eleven commands: narrow to every package of the named spaces, with the same spellings. A standalone package belongs to no space; see [Choosing the packages](./run.md#choosing-the-packages).            |
| `--group`, `-g`       |             | The same eleven commands: narrow to every package of the named [versioning groups](../reference/releasing/versioning.md), with the same spellings. A group is a `versionGroups` entry or a space that versions as one, so it may cross spaces; see [Choosing the packages](./run.md#choosing-the-packages).            |
