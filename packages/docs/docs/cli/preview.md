# The preview command

Run `dispat preview` to plan and print your pending release notes. dispat shows the breaking changes, features, fixes,
and provider updates that will go into the next changelog entry and GitHub release body. It covers every package with
pending changes in publish order.

You can narrow this list using
[`--package`, `--space`, `--group`, or the invocation folder](./run.md#choosing-the-packages). The preview respects
[release-notes windowing](../configuration/records.md#changelog), so a pending prerelease only shows its own changeset.
If nothing is pending, dispat prints `no pending changes` and names your selection.

## Choosing which body to preview

Changelogs and GitHub releases are configured separately and can say different things. Pass `--changelog` to print the
changelog entry body or `--github` to print the GitHub release body. Each output uses
[its own entry format](../configuration/records.md#entry-format-options-shared-by-changelog-and-github) with a specific
`header`, `footer`, and `releaseName`.

Pass both flags to print both bodies under one header per package. dispat labels them `--- changelog ---` and
`--- github release ---`. If you pass neither flag, dispat prints the changelog entry.

Run a preview to check your [channel filters](../configuration/records.md#choosing-which-releases-a-line-reaches)
before you release anything. The output renders for the specific channel the pending release is on.

A preview cannot show everything. The `### Release` block for a GitHub body in
[commit mode](../configuration/records.md#commit) relies on exported publishing data. Nothing has been published yet,
so this block is absent.

When a record is configured to write nothing, the preview tells you in its place:

```
## core@1.3.0-beta.0 (stable -> beta)

github release withheld: the channels do not admit beta
```

The reason will be either `disabled by config` or a restriction on the channels. Creating a GitHub release also depends
on the [`DISPAT_EXPORT_GITHUB` export](../reference/environment.md#script-outputs). No preview can know this export
value, so the note only covers the configured policy you can act on.

## Flags

You can also use the [global flags](./README.md#global-flags):

| Flag                  | Default     | Effect                                                                                                                                                                                                 |
|-----------------------|-------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `--changelog`         | `false`     | Print the changelog entry body. This is the default when you omit `--github`.                                                                                                                 |
| `--github`            | `false`     | Print the GitHub release body using the `github` entry format. Pass this with `--changelog` to print and label both.                                                                                    |
| `--package`, `-p`     |             | Narrow to the named packages for every package-selecting command (`release`, `status`, `run`, `preview`, `changelog`, `autoversion`, `autowriter`, `autoreplacer`, `commit`, `github`, `compute`). You can repeat the flag, separate names with commas, and use case-insensitive `*` globs (`-p '*'` matches every package). See [Choosing the packages](./run.md#choosing-the-packages).                     |
| `--space`, `-s`       |             | Narrow to every package in the named spaces for the same eleven commands, using the same spelling rules. A standalone package belongs to no space. See [Choosing the packages](./run.md#choosing-the-packages).            |
| `--group`, `-g`       |             | Narrow to every package in the named [versioning groups](../reference/releasing/versioning.md) for the same eleven commands. A group is a `versionGroups` entry or a space that versions as one, so it can cross spaces. See [Choosing the packages](./run.md#choosing-the-packages).            |
