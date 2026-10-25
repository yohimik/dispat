# The autoreplacer command

Use `dispat autoreplacer` when the same text edit needs to reach several packages. Set the replacement with
`--replace 'find=>write'` and choose files with `--files`, using globs relative to each package folder. Both flags can
be repeated. Each package folder is scanned once, even when several globs match it.

Include `{provider}`, `{providerVersion}`, or `{providerPrevious}` in your `--replace` string to render the pattern
once for each workspace package the covered package declares. This lets one pattern update every hand-written
coordinate without explicitly naming a dependency, while `{name}`, `{version}`, and `{previous}` render the covered
package itself. Pass `--only-updated` to narrow the fan-out to only the providers this run releases.

## Include packages that use the changed dependency

A dependency coordinate often lives in a package that did not change itself. Add `--consumers` to include packages
that depend on the changed ones. Use `--strict` if every replacement pattern must match somewhere in the selection.
See [Replacing text across the monorepo](../editing/autoreplacer.md) for examples.

## Flags

This command also accepts the [global flags](./README.md#global-flags):

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

### `--replace`

Replace literal text using `find=>write` for `replacer` and `autoreplacer`. You can repeat this flag, and dispat
applies the replacements in order. See [The replacer](../editing/replacer.md).

### `--files`

Specify which files of each covered package to rewrite during `autoreplacer`. Provide globs relative to the package
folder. You can repeat this flag.

### `--only-updated`

Narrow the `autoreplacer` fan-out to the providers this run releases.

### `--strict`

Turn a tolerated finding into a failure. For `autoreplacer`, this fails the command if a `--replace` pattern matched
nothing in any covered package.
