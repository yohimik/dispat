# The autosubstitute command

`dispat autosubstitute` is `dispat replacer` pointed at a selection instead of a list of files: `--sub 'find=>write'`
means exactly what it means there, and `--files` says which of each covered package's files to look in, as globs
relative to that package's folder. Both are repeatable, and each package folder is walked once however many globs
there are.

A `--sub` carrying `{provider}`, `{providerVersion}` or `{providerPrevious}` is rendered once per workspace package the
covered package declares, so one pattern reaches every hand-written coordinate without naming a dependency. `{name}`,
`{version}` and `{previous}` render the covered package itself. `--only-updated` narrows the fan-out to the providers
this run releases.

The packages carrying these coordinates are usually the consumers of what just changed, and the window covers only what
the commits touched, so `--consumers` is what reaches them. `--strict` fails on a `--sub` that matched nothing in any
covered package. The whole command is in
[Substituting text across the monorepo](../cookbook/editing/autosubstitute.md).

## Flags

Beside the [global flags](./README.md#global-flags):

| Flag                  | Default     | Effect                                                                                                                                                                                                 |
|-----------------------|-------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `--package`, `-p`     |             | Every package-selecting command (`release`, `status`, `run`, `preview`, `changelog`, `autoversion`, `autowriter`, `autosubstitute`, `commit`, `github`, `compute`): narrow to the named packages. Repeatable and comma-separated, matched case-insensitively, `*` globs (`-p '*'` is every package); see [Choosing the packages](./run.md#choosing-the-packages).                     |
| `--space`, `-s`       |             | The same eleven commands: narrow to every package of the named spaces, with the same spellings. A standalone package belongs to no space; see [Choosing the packages](./run.md#choosing-the-packages).            |
| `--group`, `-g`       |             | The same eleven commands: narrow to every package of the named [versioning groups](../cookbook/releasing/versioning.md), with the same spellings. A group is a `versionGroups` entry or a space that versions as one, so it may cross spaces; see [Choosing the packages](./run.md#choosing-the-packages).            |
| `--since`             |             | The same seven commands: cover the packages the commits since a git revision address, instead of the release window. `all` covers every package; see [the run command](./run.md).                |
| `--consumers`         |             | The same seven commands: additionally cover every package that transitively depends on a selected one; see [the run command](./run.md).                                                          |
| `--on-error`          | `skip`      | Every sweeping command (`run`, `autowriter`, `autosubstitute`, `changelog`, `autoversion`, `commit`, `github`): what a failed package does to its dependents, `skip` (transitive) or `continue`. Either way the command exits `1` on any failure.                                         |
| `--sub`               |             | `replacer` and `autosubstitute`: replace literal text, `find=>write`; repeatable and applied in order. See [The replacer](../cookbook/editing/replacer.md). |
| `--files`             |             | `autosubstitute` only: which files of each covered package to rewrite, as globs relative to its folder; repeatable. |
| `--only-updated`      |             | `autosubstitute`: narrows the fan-out to the providers this run releases. |
| `--strict`            |             | Turns a tolerated finding into a failure. `autosubstitute`: a `--sub` that matched nothing in any covered package. |
