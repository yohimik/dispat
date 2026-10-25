# The preview command

Run `dispat preview` to read the release notes before publishing. It shows the breaking changes, features, fixes,
and dependency updates planned for each package, in publish order. By default, it prints the next changelog entry;
use `--github` to preview the GitHub release body.

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

The GitHub body preview uses the publication limit. If the rendered body exceeds 120,000 characters, dispat cuts it
at a complete entry where possible, adds a line pointing to the remaining notes, and reports `W245`. This applies to
pending and `--version` previews. The changelog preview remains complete.

Run a preview to check your [channel filters](../configuration/records.md#choosing-which-releases-a-line-reaches)
before you release anything. The output renders for the specific channel the pending release is on.

The GitHub body's `### Release` block in [commit mode](../configuration/records.md#commit) needs data exported by
publish scripts. That data is not available during preview, so the block is omitted.

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

dispat renders the release the way the release itself did, including the GitHub body limit. It plans again with the
tag's commit as HEAD, so the changelogs and manifests read as they were released, and leaves out that tag, every
later release of the package, and every other release tag the same run wrote. The plan depends only on the history,
the configuration and the tags, so it is the plan the release was cut from. The bodies come from the same renderers
the release used. With
[release commits](../configuration/records.md#commit) on, the GitHub body carries the `### Release` block naming the
tag's commit.

The run's other packages are rendered as the release saw them. A run whose packages each make their own release commit,
for example with `dispat commit --tag` in their flow, lays those commits one after another, so its providers' tags sit
on commits below the consumer's. dispat walks down the first-parent line from the tag's commit while each commit is a
release commit: its message is `commit.messageFormat` rendered with the release tags it carries. The tags on those
commits belong to the same run, so they are left out too, and the providers plan again. The consumer's
`### Dependencies` section then lists the versions the run released. A provider that released in an earlier run stays,
because the release read that tag as history. A release commit whose message came from
`dispat commit --message-format` instead of the configuration is not recognised, and the walk stops there.

The changelog header carries the tag's date: the tagger date of an annotated tag, or the commit date of a lightweight
one, in UTC under the entry's `dateFormat`. The release dated its entry by its own clock when it wrote the entry,
shortly before it wrote the tag. The two dates differ only when midnight UTC fell between those two steps.

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
