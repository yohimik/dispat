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

## Previewing a past release

After a release nothing is pending, so a plain preview has nothing to show. Pass `--version` to render the notes of a
release that already happened. The value is a version of the selected package, its full tag, or `latest`:

```sh
dispat preview -p dispat --version 1.11.0-rc.8 --github
dispat preview --version services/dispat/v1.11.0-rc.8 --github
dispat preview -p dispat --version --github
```

The bare `--version` means `latest`, the package's highest release. A full tag is read under the package's tag format,
and an [alias tag](../configuration/alias-tags.md) is read through the release tag on the same commit. A version or
`latest` belongs to no package by itself, so the selection must cover exactly one package. A full tag names its own
package and needs no selection. dispat refuses a version or tag that names no release, a tag of a package outside the
selection, and a version beside a selection of several packages.

dispat renders the release the way the release itself did. It plans again with the tag's commit as HEAD, leaving out
that tag, every later release of the package, and the other packages' release tags on the same commit, which the same
run wrote. The plan depends only on the history, the configuration and the tags, so it is the plan the release was cut
from. The bodies come from the same renderers the release used. With [release commits](../configuration/records.md#commit)
on, the GitHub body carries the `### Release` block naming the tag's commit.

dispat reads the current checkout's configuration. If the configuration changed after the release, the notes can
differ from what the release wrote. When the plan computes a different version, dispat warns and renders the notes
under the released version. A note that the push recovery added to the body is not in the history, so it is absent.
`--version` works in a single repository and is refused in a composed workspace.

Use it to rebuild a release page that was never created. Release run 40177824466 tagged `services/dispat/v1.11.0-rc.8`,
and its release page was never created. The command below prints the body that release would have sent, ready to paste into a
new release for that tag:

```sh
dispat preview -p dispat --version 1.11.0-rc.8 --github
```

See [Repair GitHub metadata](../reference/releasing/recovery.md#repair-github-metadata-after-the-package-was-recorded)
for the rest of that repair.

## Flags

You can also use the [global flags](./README.md#global-flags):

### `--changelog`

The default is `false`. Print the changelog entry body. This is the default when you omit `--github`.

### `--github`

The default is `false`. Print the GitHub release body using the `github` entry format. Pass this with `--changelog` to
print and label both.

### `--version`

The default is empty, which previews the pending notes. Render the notes of a past release instead: a version of the
selected package, its full tag, or `latest`. A bare `--version` means `latest`. See
[Previewing a past release](#previewing-a-past-release). On every other command, `--version` prints the dispat version.

### `--package`, `-p`

Narrow to the named packages for every package-selecting command (`release`, `status`, `run`, `preview`, `changelog`,
`autoversion`, `autowriter`, `autoreplacer`, `commit`, `github`, `compute`). You can repeat the flag, separate names
with commas, and use case-insensitive `*` globs (`-p '*'` matches every package). See
[Choosing the packages](./run.md#choosing-the-packages).

### `--space`, `-s`

Narrow to every package in the named spaces for the same eleven commands, using the same spelling rules. A standalone
package belongs to no space. See [Choosing the packages](./run.md#choosing-the-packages).

### `--group`, `-g`

Narrow to every package in the named [versioning groups](../reference/releasing/versioning.md) for the same eleven
commands. A group is a `versionGroups` entry or a space that versions as one, so it can cross spaces. See
[Choosing the packages](./run.md#choosing-the-packages).
