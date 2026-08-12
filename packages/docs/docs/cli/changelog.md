# The changelog command

`dispat changelog` writes each covered package's pending changelog entry, exactly what the release
stage's recorder would write. An entry that already exists in the file is a skip (`W222`), and the same check makes
the release stage skip entries this command already wrote. That is the point of running it early: a changelog written
in a `beforePublish` script, before `dispat commit`, lands inside the tagged commit.

It writes inside each package's own folder and rides the build concurrency budget.

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
| `--package`, `-p`     |             | Every package-selecting command (`release`, `status`, `run`, `preview`, `changelog`, `autoversion`, `autowriter`, `autosubstitute`, `commit`, `github`, `compute`): narrow to the named packages. Repeatable and comma-separated, matched case-insensitively, `*` globs (`-p '*'` is every package); see [Choosing the packages](./run.md#choosing-the-packages).                     |
| `--space`, `-s`       |             | The same eleven commands: narrow to every package of the named spaces, with the same spellings. A standalone package belongs to no space; see [Choosing the packages](./run.md#choosing-the-packages).            |
| `--group`, `-g`       |             | The same eleven commands: narrow to every package of the named [versioning groups](../releasing/versioning.md), with the same spellings. A group is a `versionGroups` entry or a space that versions as one, so it may cross spaces; see [Choosing the packages](./run.md#choosing-the-packages).            |
| `--since`             |             | The same seven commands: cover the packages the commits since a git revision address, instead of the release window. `all` covers every package; see [the run command](./run.md).                |
| `--consumers`         |             | The same seven commands: additionally cover every package that transitively depends on a selected one; see [the run command](./run.md).                                                          |
| `--on-error`          | `skip`      | Every sweeping command (`run`, `autowriter`, `autosubstitute`, `changelog`, `autoversion`, `commit`, `github`): what a failed package does to its dependents, `skip` (transitive) or `continue`. Either way the command exits `1` on any failure.                                         |
| `--file`, `--file-title`, `--date-format` | from config | `changelog` only: override the matching `changelog.*` values for every package of the invocation. `--file-title` states the whole title as one line. |
| `--release-name`      | from config | `changelog` and `github`: override [`releaseName`](../configuration/records.md#your-own-words-around-an-entry) for the invocation. `$VAR` and `${VAR}` expand as they do in the config. |
