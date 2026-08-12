# The autoversion command

`dispat autoversion` runs the native manifest reconciliation of the version stage: declared workspace
ranges rewritten to the planned versions, own versions updated, and the space's `syncLock` scripts run for each
package whose manifests actually changed. Rewriting already-reconciled manifests changes nothing, so re-running is
safe. A space without an `autoVersion` block is skipped unless a policy flag forces one, which then starts from the
defaults. `--only-updated` narrows the rewrites to declarations naming a package this run releases, so a range that
had fallen behind a provider released earlier is left as it is instead of caught up (`W197`).

It writes inside each package's own folder and rides the build concurrency budget.

## The selection it shares

`dispat changelog`, `dispat autoversion`, `dispat commit` and `dispat github` expose the release pipeline's native
steps to custom flows: a stage script can run a step at the moment the flow needs it, and the release stage later
finds the work done and skips it. All four share the run command's [selection](./run.md#choosing-the-packages) *and* its
window: with no terms they cover every releasing package in dependency order, `--package`, `--space`, `--group` or the
invocation folder narrows that, `--since` replaces the window, `--consumers` expands it downstream, and `--on-error`
decides what a failed package does to its dependents. A term matching no package is an error; a *selected* package
that is not releasing is a logged no-op, so a flow never fails over a converged or held package — which also means a
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
| `--package`, `-p`     |             | Every package-selecting command (`release`, `status`, `run`, `preview`, `changelog`, `autoversion`, `commit`, `github`, `compute`): narrow to the named packages. Repeatable and comma-separated, matched case-insensitively, `*` globs (`-p '*'` is every package); see [Choosing the packages](./run.md#choosing-the-packages).                     |
| `--space`, `-s`       |             | The same nine commands: narrow to every package of the named spaces, with the same spellings. A standalone package belongs to no space; see [Choosing the packages](./run.md#choosing-the-packages).            |
| `--group`, `-g`       |             | The same nine commands: narrow to every package of the named [versioning groups](../releasing/versioning.md), with the same spellings. A group is a `versionGroups` entry or a space that versions as one, so it may cross spaces; see [Choosing the packages](./run.md#choosing-the-packages).            |
| `--since`             |             | The same six commands: cover the packages the commits since a git revision address, instead of the release window. `all` covers every package; see [the run command](./run.md).                |
| `--consumers`         |             | The same six commands: additionally cover every package that transitively depends on a selected one; see [the run command](./run.md).                                                          |
| `--on-error`          | `skip`      | Every sweeping command (`run`, `autowriter`, `autosubstitute`, `changelog`, `autoversion`, `commit`, `github`): what a failed package does to its dependents, `skip` (transitive) or `continue`. Either way the command exits `1` on any failure.                                         |
| `--range`, `--match`, `--write-version` | from config | `autoversion` only: override the matching `autoVersion.*` policy for the invocation.                             |
| `--manifests`         | from config | `autoversion` and `autowriter`: which of a package's manifests are rewritten, `root` (the ones in the package folder) or `all` (every manifest under it). `autoversion` also takes `none`, which turns its parsing strategy off. |
| `--only-updated`      |             | `autoversion` and `autowriter`: rewrite only the declarations naming a package this run updates, leaving a range that had fallen behind a provider released earlier as it is. |
| `--no-replace`        |             | `autoversion` only: skip the `autoVersion.replace` rules for this invocation.                                     |
| `--sync-lock`         | `true`      | `autoversion` and `autowriter`: run the syncLock scripts for packages whose manifests changed; `--sync-lock=false` skips them. |
