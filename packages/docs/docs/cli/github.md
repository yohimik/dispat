# The github command

Run `dispat github` to create GitHub releases for the selected packages that have opted in. Each release uses the
package tag or configured [`releaseName`](../configuration/records.md#your-own-words-around-an-entry) as its name,
and the rendered changelog sections as its body. This is the same output as the release recorder.

An existing release is skipped with `W224`, so rerunning the command does not create a duplicate.

With [`github.draft`](../configuration/records.md#github), or with `--draft` on this command, the release is created
for a person to publish after reading it. It carries no tag ref until they do, so nothing that resolves a release by
its tag sees it meanwhile.

## Choose which packages get a GitHub release

The command uses the same opt-in rules as the release recorder. dispat releases a package when its scripts export
[`DISPAT_EXPORT_GITHUB`](../reference/environment.md#script-outputs) or when
[`github.allPackages`](../configuration/records.md#github) covers it.

When you run this inside a stage script, dispat reads that export from its environment and attaches the listed files.
The stage provides this environment along with `DISPAT_PACKAGE` to identify the package. If you run the command by hand
with the variable exported, it covers every package your invocation selects. Without either opt-in, the command
publishes nothing and exits with `0`.

Packages are processed one at a time, in release order.

## When a release cannot be created

A GitHub release follows a publication: by the time `dispat github` runs, `dispat commit --tag --push` has already
pushed the tag that records the version. So a release that cannot be created does not fail the command. dispat logs
it as an error under `E222`, closes with a warning that names every package whose release is missing, and exits `0`.
The stage script that called the command succeeds, the package stays published, and its consumers release.

Create what is missing afterwards with `dispat github --since all`, which skips every release that already exists
(`W224`); [repair GitHub metadata](../reference/releasing/recovery.md#repair-github-metadata-after-the-package-was-recorded)
describes the repair job. Pass `--ignore-errors=false` when a missing release must fail the command instead.

The flag covers a release that could not be created, for whatever reason: a refused request, a token that cannot be
read, or a verification GitHub rejected. A flag value, a selection or a plan that refuses the command before anything
is written, and an interrupted command, still exit non-zero.

## The shape of the body

The command and the release recorder produce the same body. Items have one blank line between them, with commit
bodies indented two spaces beneath their bullets. Dependency updates form a compact block without blank lines between
rows. Section endings are consistent whether or not the last item has a body.

The changelog file's [entry spacing](../configuration/records.md#the-seam-between-entries) does not apply to GitHub:
a release body contains a single entry.

GitHub refuses a body over 125,000 characters. dispat cuts a longer one to 120,000 characters at the last whole entry
that fits, and ends the sections with a line linking the package's changelog file at the release tag. The cut is
logged as `W245`, and the release is created. See
[a body over GitHub's limit](../configuration/records.md#github).

See [Release records](../configuration/records.md) for the entry format options the body is rendered under.

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
dependents. `continue` lets them run. Whether a failed release makes the command exit `1` is decided by
`--ignore-errors` below.

### `--ignore-errors`

The default is `true`: a release that could not be created is logged as an error (`E222`) and named in the closing
summary, and the command exits `0`; see [When a release cannot be created](#when-a-release-cannot-be-created).
`--ignore-errors=false` exits `1` instead. `dispat changelog` takes the same flag; `dispat commit` does not, because a
tag it could not create or push always fails it.

### `--owner`, `--repo`, `--api-url`, `--token-env`

The default comes from config. Override the matching `github.*` values for every package in the `github` command
invocation.

### `--target`

Push the commit to the remote before you use this. It tells the `github` command to create the tag at this commit or
branch (`target_commitish`).

### `--draft`

The default comes from config. Override [`github.draft`](../configuration/records.md#github) for every package in the
invocation. `--draft` creates the release for a person to publish, and `--draft=false` publishes straight away over a
configured draft.

### `--release-name`

The default comes from config. Override [`releaseName`](../configuration/records.md#your-own-words-around-an-entry) for
the `changelog` and `github` commands. Environment variables like `$VAR` and `${VAR}` expand exactly as they do in the
config.

### `--authors`

The default comes from config. Override
[`authors.placement`](../configuration/records.md#attributing-an-entry-to-its-authors) for the `changelog` and `github`
commands: `off`, `inline`, `section` or `both`. Any other value is refused before anything is planned.

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
