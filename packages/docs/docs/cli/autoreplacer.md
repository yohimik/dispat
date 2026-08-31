# The autoreplacer command

Run `dispat autoreplacer` to apply `dispat replacer` to a selection of packages instead of a list of files. Pass
`--replace 'find=>write'` to define the text replacement, and pass `--files` to specify which files to search using
globs relative to the package folder. You can repeat both flags, and dispat walks each package folder exactly once
regardless of how many globs you provide.

Include `{provider}`, `{providerVersion}`, or `{providerPrevious}` in your `--replace` string to render the pattern
once for each workspace package the covered package declares. This lets one pattern update every hand-written
coordinate without explicitly naming a dependency, while `{name}`, `{version}`, and `{previous}` render the covered
package itself. Pass `--only-updated` to narrow the fan-out to only the providers this run releases.

The packages holding these coordinates usually consume the packages that just changed. The release window only covers
what the commits touched, so pass `--consumers` to reach those consuming packages. Pass `--strict` to fail the command
if a `--replace` pattern matched nothing in any covered package, and read about the whole command in
[Replacing text across the monorepo](../editing/autoreplacer.md).

## Flags

This command also accepts the [global flags](./README.md#global-flags):

### `--package`, `-p`

Narrow package-selecting commands (`release`, `status`, `run`, `preview`, `changelog`, `autoversion`, `autowriter`,
`autoreplacer`, `commit`, `github`, `compute`) to the named packages. You can repeat this comma-separated flag, and
dispat matches names case-insensitively. It supports `*` globs, so `-p '*'` selects every package; see
[Choosing the packages](./run.md#choosing-the-packages).

### `--space`, `-s`

Narrow the same eleven commands to every package in the named spaces. This uses the same spelling rules as the package
flag. A standalone package belongs to no space; see [Choosing the packages](./run.md#choosing-the-packages).

### `--group`, `-g`

Narrow the same eleven commands to every package in the named
[versioning groups](../reference/releasing/versioning.md). This uses the same spelling rules. A group is a
`versionGroups` entry or a space that versions as one, so it can cross spaces; see
[Choosing the packages](./run.md#choosing-the-packages).

### `--since`

For the same seven commands, cover the packages the commits since a git revision address instead of the release window.
Pass `all` to cover every package. See [the run command](./run.md).

### `--consumers`

For the same seven commands, additionally cover every package that transitively depends on a selected package. See
[the run command](./run.md).

### `--on-error`

The default is `skip`. Control what a failed package does to its dependents during sweeping commands (`run`,
`autowriter`, `autoreplacer`, `changelog`, `autoversion`, `commit`, `github`). Set this to `skip` (transitive) or
`continue`. The command exits `1` on any failure either way.

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
