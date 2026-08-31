# The commit command

Run `dispat commit` to create a release commit for each covered package. dispat stages the package folder along with
any `commit.include` paths, then writes a commit message using `commit.messageFormat` to insert the package's name and
tag.

If a package has nothing to stage, dispat cleanly skips it.

Pass `--tag` to create an annotated release tag at the new commit. dispat skips the tag and logs `W223` if it already
exists at that exact commit, but fails if the tag points anywhere else.

Add `--push` to push the branch to your remote once all packages finish. If you also pass `--tag`, dispat pushes the
tags with force, so a tag the remote already carries is replaced rather than left as it is. Pass `--no-force` to skip
those tags instead; see [`commit.force`](../configuration/records.md#force).

Run this command inside a release stage script to access the `DISPAT_OUTPUT` environment variable. dispat exports each
package's commit as `PACKAGE_<KEY>`, pinning the outer run's tag and GitHub release to it.

dispat processes packages one at a time. A repository only has one index and one HEAD, and a sequential release order
makes the history easier to read.

## The selection it shares

The `dispat changelog`, `dispat autoversion`, `dispat commit`, and `dispat github` commands expose native release
pipeline steps to your custom flows. Run a step inside a stage script exactly when your flow needs it. When the release
stage runs later, it sees the completed work and skips it.

All four commands share the run command's [selection](./run.md#choosing-the-packages) *and* its release window. Run
them without flags to cover every releasing package in dependency order. Use `--package`, `--space`, `--group`, or your
current folder to narrow that list.

Pass `--since` to replace the time window entirely. Use `--consumers` to expand the selection downstream, and
`--on-error` to decide what happens to dependent packages if one fails.

Selection follows two rules. If a term matches no package, the command fails. If a *selected* package is not releasing,
dispat logs a no-op.

This ensures your flow never fails over a converged or held package. This second rule explains why a step run after
`dispat commit --tag` covers nothing. You must pass `--since all` to put the tagged package back into the selection.

These four command words are reserved. Like all built-in commands, they win the `dispat <script>` shorthand over a
custom [script](../configuration/spaces.md#scripts-and-dispat-run) with the same name. This means `dispat commit`
always triggers the native command.

Type `dispat run commit` to run your custom script instead.

Every config value these commands use is also available as a flag. Pass the flag to override the config for a single
run, as shown in [Flags](#flags).

## Flags

These flags apply alongside the [global flags](./README.md#global-flags):

### `--package`, `-p`

Pass this to narrow every package-selecting command (`release`, `status`, `run`, `preview`, `changelog`, `autoversion`,
`autowriter`, `autoreplacer`, `commit`, `github`, `compute`) to the named packages. You can repeat the flag or separate
names with commas. dispat matches names case-insensitively and supports `*` globs, so `-p '*'` selects every package.
See [Choosing the packages](./run.md#choosing-the-packages).

### `--space`, `-s`

Pass this to narrow the same eleven commands to every package in the named spaces. This flag uses the same spelling
rules as `--package`. Standalone packages belong to no space. See
[Choosing the packages](./run.md#choosing-the-packages).

### `--group`, `-g`

Pass this to narrow the same eleven commands to every package in the named
[versioning groups](../reference/releasing/versioning.md). This flag uses the same spelling rules. A group is a
`versionGroups` entry or a space that versions as one, meaning it can cross spaces. See
[Choosing the packages](./run.md#choosing-the-packages).

### `--since`

Pass this to the same seven commands to cover packages modified by commits since a specific git revision. This
overrides the release window. Pass `all` to cover every package. See [the run command](./run.md).

### `--consumers`

Pass this to the same seven commands to also cover every package that transitively depends on a selected one. See
[the run command](./run.md).

### `--on-error`

The default is `skip`. Pass `skip` or `continue` to every sweeping command (`run`, `autowriter`, `autoreplacer`,
`changelog`, `autoversion`, `commit`, `github`) to decide what a failed package does to its dependents. The `skip`
option is transitive. The command exits `1` on any failure regardless of this setting.

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
