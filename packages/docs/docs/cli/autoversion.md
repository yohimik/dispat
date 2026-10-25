# The autoversion command

Run `dispat autoversion` to bring manifest versions into line with the release plan. It updates the package's own
version and its declared workspace dependency ranges, then runs the space's `syncLock` scripts for packages whose
manifests changed. A package whose space enables
[`autoSign`](../configuration/autosign.md) leaves its own version to the sign stage, so the command writes its ranges
alone unless `--write-version` asks for the own version too.

Rewriting already-reconciled manifests changes nothing, so you can safely re-run this command. dispat skips a space
without an `autoVersion` block unless you force one with a policy flag. That flag makes dispat start from the defaults.

Pass `--only-updated` to narrow rewrites to declarations naming a package this run releases. This leaves a range that
fell behind an earlier release exactly as it is, instead of catching it up (`W197`).

Files are written inside each package's folder. The command uses the configured build concurrency limit.

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

These commands accept the [global flags](./README.md#global-flags) alongside the following:

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

### `--match`, `--write-version`

The default comes from config. Apply to `autoversion` only. Override the matching `autoVersion.*` policy for this
invocation.

### `--range`

The default comes from config. Apply to `autoversion` and `autowriter`. Override the
[`autoVersion.range`](../configuration/autoversion.md) write policy. This policy defines how dispat spells a reconciled
range (`caret`, `tilde`, `exact`, a `{version}` template, or a literal).

### `--manifests`

The default comes from config. Apply to `autoversion` and `autowriter`. Choose which of a package's manifests dispat
rewrites, passing `root` for manifests in the package folder or `all` for every manifest under it. `autoversion` also
accepts `none` to turn off its parsing strategy.

### `--only-updated`

Apply to `autoversion` and `autowriter`. Rewrite only the declarations naming a package this run updates. This leaves a
range that fell behind an earlier release exactly as it is.

### `--no-replace`

Apply to `autoversion` only. Skip the `autoVersion.replace` rules for this invocation.

### `--sync-lock`

The default is `true`. Apply to `autoversion` and `autowriter`. Run the `syncLock` scripts for packages whose manifests
changed. Pass `--sync-lock=false` to skip them.
