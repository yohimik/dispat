# The github command

`dispat github` creates each covered package's GitHub release, exactly what the release pipeline's own recorder
would create: the release named after the package tag (or after
[`releaseName`](../configuration/records.md#your-own-words-around-an-entry)), its body the rendered changelog
sections. A release the
repository already carries is a skip (`W224`), so a repeated invocation, and the release that follows one, converge
instead of failing on the API's duplicate-tag rejection.

The opt-in is the one the recorder uses: a package is released when its scripts exported
[`DISPAT_EXPORT_GITHUB`](../reference/environment.md#script-outputs), or when
[`github.allPackages`](../configuration/records.md#github) covers it. Run inside a stage script, the command reads that
export out of its own environment (the stage handed it over, along with `DISPAT_PACKAGE` naming whose it is) and
attaches the files it lists. Run by hand with the variable exported, it covers every package the invocation selects.
Without either opt-in the command publishes nothing, and says so with exit `0`.

It covers its packages one at a time, since a repository has one index and one HEAD, and a release order is worth reading.

## The selection it shares

`dispat changelog`, `dispat autoversion`, `dispat commit` and `dispat github` expose the release pipeline's native
steps to custom flows: a stage script can run a step at the moment the flow needs it, and the release stage later
finds the work done and skips it. All four share the run command's [selection](./run.md#choosing-the-packages) *and* its
window: with no terms they cover every releasing package in dependency order, `--package`, `--space`, `--group` or the
invocation folder narrows that, `--since` replaces the window, `--consumers` expands it downstream, and `--on-error`
decides what a failed package does to its dependents. A term matching no package is an error; a *selected* package
that is not releasing is a logged no-op, so a flow never fails over a converged or held package. That also means a
step run after `dispat commit --tag` covers nothing until `--since all` puts the tagged package back on the table.
The four command words are reserved: like every command name, each wins
the `dispat <script>` shorthand over a [script](../configuration/spaces.md#scripts-and-dispat-run) of the same name, so
`dispat commit` is always the command. Spelling it out as `dispat run commit` still reaches the script.

Every config value the commands consume is also a flag that overrides it for the invocation, listed in the
[flags table](#flags).

## Flags

Beside the [global flags](./README.md#global-flags):

| Flag                  | Default     | Effect                                                                                                                                                                                                 |
|-----------------------|-------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `--package`, `-p`     |             | Every package-selecting command (`release`, `status`, `run`, `preview`, `changelog`, `autoversion`, `autowriter`, `autoreplacer`, `commit`, `github`, `compute`): narrow to the named packages. Repeatable and comma-separated, matched case-insensitively, `*` globs (`-p '*'` is every package); see [Choosing the packages](./run.md#choosing-the-packages).                     |
| `--space`, `-s`       |             | The same eleven commands: narrow to every package of the named spaces, with the same spellings. A standalone package belongs to no space; see [Choosing the packages](./run.md#choosing-the-packages).            |
| `--group`, `-g`       |             | The same eleven commands: narrow to every package of the named [versioning groups](../reference/releasing/versioning.md), with the same spellings. A group is a `versionGroups` entry or a space that versions as one, so it may cross spaces; see [Choosing the packages](./run.md#choosing-the-packages).            |
| `--since`             |             | The same seven commands: cover the packages the commits since a git revision address, instead of the release window. `all` covers every package; see [the run command](./run.md).                |
| `--consumers`         |             | The same seven commands: additionally cover every package that transitively depends on a selected one; see [the run command](./run.md).                                                          |
| `--on-error`          | `skip`      | Every sweeping command (`run`, `autowriter`, `autoreplacer`, `changelog`, `autoversion`, `commit`, `github`): what a failed package does to its dependents, `skip` (transitive) or `continue`. Either way the command exits `1` on any failure.                                         |
| `--owner`, `--repo`, `--api-url`, `--token-env` | from config | `github`: override the matching `github.*` values for every package of the invocation.  |
| `--target`            |             | `github` only: create the tag at this commit or branch (`target_commitish`). Only safe once the commit is on the remote.   |
| `--release-name`      | from config | `changelog` and `github`: override [`releaseName`](../configuration/records.md#your-own-words-around-an-entry) for the invocation. `$VAR` and `${VAR}` expand as they do in the config. |
