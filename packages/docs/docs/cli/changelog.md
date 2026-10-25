# The changelog command

Run `dispat changelog` to write pending changelog entries before the normal release step. It uses the same content
and formatting as the release recorder. Existing entries are skipped with `W226`, so the later release step does not
write them again.

To include the changelog in the tagged commit, run this command before `dispat commit`, for example in a
`beforePublish` script.

Entries are written inside each package's folder, using the configured build concurrency limit.

An entry that cannot be written does not fail the command. dispat logs it as an error under `E222`, closes with a
warning that names every package whose entry is missing, and exits `0`, so a flow that writes its changelog inside the
publish stage never reports a published package as failed over it. Write what is missing with a later
`dispat changelog --since all`, which skips the entries that already exist (`W226`). Pass `--ignore-errors=false` when
a missing entry must fail the command instead. A flag value, a selection or a plan that refuses the command before
anything is written, and an interrupted command, still exit non-zero.

## The shape of what it writes

Entries use the same layout whether this command or the release recorder writes them:

- Items in a section have one blank line between them.
- A commit body is indented two spaces beneath its bullet.
- Dependency updates form one compact block, without blank lines between rows.
- Entries in the file are separated by [`changelog.entrySpacing`](../configuration/records.md#the-seam-between-entries)
  blank lines, two by default.

Section endings are consistent whether or not the last item includes a commit body.

This command renders exactly what the release stage's recorder renders, so an entry written from a stage script and one
written by the release itself are the same bytes. See [Release records](../configuration/records.md) for the entry
format options and [Existing changelogs and history](../examples/adopting.md#existing-changelogs-and-history) for what
happens to a file that already had content.

## The selection it shares

`changelog`, `autoversion`, `commit`, and `github` let you run individual release steps from a custom flow or stage
script. The later release step recognises completed work and skips it.

These commands use the run command's [package selection](./run.md#choosing-the-packages) and release window. By
default, they process releasing packages in dependency order. Narrow the selection with `--package`, `--space`,
`--group`, or your current folder. Use `--since` to change the window, `--consumers` to include dependent packages,
and `--on-error` to choose how failures affect them.

A name that matches no package is an error. A selected package with no release to perform is a logged no-op. After
`dispat commit --tag`, the tagged package is no longer in the release window; pass `--since all` to include it again.

Built-in command names take precedence over script names. For example, `dispat commit` runs this built-in step;
`dispat run commit` runs a [script](../configuration/spaces.md#scripts-and-dispat-run) named `commit`.
Use the flags below to override configuration for this invocation.

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

### `--since`

Use commits since a Git revision to choose packages, replacing the default release window. Pass `all` to include
every package. See [how the run command selects packages](./run.md).

### `--consumers`

Also include packages that depend on the selected packages, directly or indirectly. See
[how the run command expands a selection](./run.md).

### `--on-error`

Choose what happens to dependent packages when a package fails. The default, `skip`, skips its direct and indirect
dependents. `continue` lets them run. Whether a failed entry makes the command exit `1` is decided by
`--ignore-errors` below.

### `--ignore-errors`

The default is `true`: an entry that could not be written is logged as an error (`E222`) and named in the closing
summary, and the command exits `0`. `--ignore-errors=false` exits `1` instead. `dispat github` takes the same flag;
`dispat commit` does not, because a tag it could not create or push always fails it.

### `--file`, `-f`, `--file-title`, `--date-format`

The default comes from config. Override the matching `changelog.*` values for every package in a `changelog`
invocation. Use `--file-title` to state the whole title as one line.

### `--release-name`

The default comes from config. Override [`releaseName`](../configuration/records.md#your-own-words-around-an-entry) for
a `changelog` or `github` invocation. Variables like `$VAR` and `${VAR}` expand exactly as they do in the config.

### `--authors`

The default comes from config. Override
[`authors.placement`](../configuration/records.md#attributing-an-entry-to-its-authors) for a `changelog` or `github`
invocation: `off`, `inline`, `section` or `both`. Any other value is refused before anything is planned.

### `--authors-format`

The default comes from config. Override `authors.format`: `fullname`, or `username` for the local part of the email
address.

### `--authors-commits`

The default comes from config. Override `authors.commits`: `ccme` for the commits behind the entry's own lines, or
`all` for every commit in the release window.

### `--authors-include`

The default comes from config. Override `authors.include`. This repeatable, comma-separated list of case-insensitive
globs replaces the configured list whole, and each pattern is tried against the full name, the username and the email.

### `--authors-exclude`

The default comes from config. Override `authors.exclude`, with the same spellings. It is applied after
`--authors-include` and wins.

### `--authors-title`

The default comes from config. Override `authors.title`, the heading of the authors section.
