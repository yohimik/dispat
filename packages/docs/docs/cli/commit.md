# The commit command

The commit command has two uses. Pass `-m` or another authoring flag to create a source commit after checking its
CCME message. Run it without authoring flags to create the per-package release commits described below.

To check a string without staging files or creating a commit, use [`dispat diagnostics`](./diagnostics.md).

## Write a source commit

Stage the files you intend to commit, then pass a message:

```sh
git add packages/core/api.go
dispat commit -m "feat(core): add streaming"
```

dispat uses the repository's [parser settings](../configuration/parser.md) to check the message before Git creates the commit. An error in any unit rejects the whole message. Warnings are shown but do not block creation unless an existing parser option makes them errors. This check validates syntax; use `dispat status` afterwards to inspect scopes, versions, and the release plan.

A message can also come from a file or Git's editor:

```sh
dispat commit --file message.txt
dispat commit --edit
dispat commit --amend -m "fix(core): close the stream"
```

`-C`/`--reuse-message` and `-c`/`--reedit-message` can reuse a named commit's message; the latter opens the editor before validation.

Authoring mode uses Git's commit operation. It does not run package release scripts, select releasing packages, create release tags, or push. Git controls staging, identity, signing, and hooks. As with `git commit --amend`, an explicit amend request replaces the current commit. Review your index before running it.

dispat validates after an existing `commit-msg` hook has run and after applying the selected cleanup policy. `--no-verify` and `-n` are refused because they would skip this check. Calling Git directly still bypasses dispat, so keep required history validation in CI.

Use full long-option names. Supported Git options cover message sources, editing and amending, identity and signing, staging and pathspecs, trailers, cleanup, and dry-run output. Unknown options and abbreviated long options are refused before Git runs. A dry run displays Git's proposed changes but does not validate or create a commit.

For comment cleanup, set an explicit `core.commentChar`; `auto` is not supported by the validation wrapper. The raw message file is limited to 16 MiB, and the parser's configured message limit applies to the cleaned text. Temporary hook wrappers and parser settings are removed when the command finishes. Existing hooks remain repository-trusted executable code.

Do not combine authoring flags with release-step flags such as `--tag`, `--push`, `--package`, or `--message-format`. Put dispat's global options before `commit` to distinguish them from Git options:

```sh
dispat --config dispat.yaml commit -m "fix(core): close the stream"
```

## Create release commits

Run `dispat commit` to create a release commit for each covered package. dispat stages the package folder along with
any `commit.include` paths, then writes a commit message using `commit.messageFormat` to insert the package's name and
tag.

If a package has nothing to stage, dispat cleanly skips it.

Pass `--tag` to create an annotated release tag at the new commit. dispat skips the tag and logs `W223` if it already
exists at that exact commit, but fails if the tag points anywhere else.

Outside a release stage script, `--tag` refuses to tag a provider on the release commit of a consumer it still owes
unless the same invocation tags that consumer after it
([`E201`](../reference/plan-errors.md#after-the-plan-before-any-releasing)). dispat checks this before it commits or
tags anything. Cover both packages (`--package <provider>,<consumer>`), or commit first and tag the provider alone.
Inside a release stage script the run that started the script has already checked its whole selection.

Add `--push` to push the branch to your remote once all packages finish. If you also pass `--tag`, dispat pushes the
tags with force, so a tag the remote already carries is replaced rather than left as it is. Pass `--no-force` to skip
those tags instead; see [`commit.force`](../configuration/records.md#force).

Run this command inside a release stage script to access the `DISPAT_OUTPUT` environment variable. dispat exports each
package's commit as `PACKAGE_<KEY>`, pinning the outer run's tag and GitHub release to it.

dispat processes packages one at a time. A repository only has one index and one HEAD, and a sequential release order
makes the history easier to read.

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

These flags apply alongside the [global flags](./README.md#global-flags):

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
dependents. `continue` lets them run. Either way, any package failure makes the command exit `1`.

### `--tag`

Pass this to `commit` to create an annotated release tag at the resulting commit. dispat skips an identical existing
tag. If a tag exists at a different commit, dispat leaves it alone and reports `E221`.

### `--push`

Pass this to `commit` to push the branch. If you also pass `--tag`, dispat pushes the tags too.

### `--no-force`

Pass this to `commit` to turn [`commit.force`](../configuration/records.md#force) off for this run. dispat leaves any
tag the repository or remote already carries exactly as it is.

### `--name`, `--email`

The default comes from config. Pass these to `commit` to override the `commit.name` and `commit.email` committer
identity.

### `--remote`

The default comes from config. Pass this to `commit` to override the `commit.remote` push target.

### `--tag-name`

The default is computed by dispat. Pass this to `commit` to name the annotated tag yourself instead of letting dispat
compute it. You can pass `$DISPAT_TAG` from a release stage. This works for one package only.

### `--message-format`

The default comes from config. Pass this to `commit` to override the `commit.messageFormat` template.

### `--include`

The default comes from config. Pass this to `commit` to override the `commit.include` extra staged paths.
