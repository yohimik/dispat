# The github command

Run `dispat github` to create a GitHub release for each covered package. It creates exactly what the release pipeline's
recorder would create: a release named after the package tag, or after
[`releaseName`](../configuration/records.md#your-own-words-around-an-entry), with the rendered changelog sections as
its body. If the repository already has the release, dispat skips it and logs `W224`. This means a repeated run, and
the release that follows it, converge instead of failing when the API rejects a duplicate tag.

You opt in exactly as you do for the recorder. dispat releases a package when its scripts export
[`DISPAT_EXPORT_GITHUB`](../reference/environment.md#script-outputs) or when
[`github.allPackages`](../configuration/records.md#github) covers it.

When you run this inside a stage script, dispat reads that export from its environment and attaches the listed files.
The stage provides this environment along with `DISPAT_PACKAGE` to identify the package. If you run the command by hand
with the variable exported, it covers every package your invocation selects. Without either opt-in, the command
publishes nothing and exits with `0`.

The command covers packages one at a time. A repository has one index and one HEAD, and a clear release order is worth
reading.

## The selection it shares

`dispat changelog`, `dispat autoversion`, `dispat commit` and `dispat github` expose the release pipeline's native
steps to custom flows. You can run a step from a stage script exactly when your flow needs it. The release stage later
sees the work is done and skips it.

All four commands share the run command's [selection](./run.md#choosing-the-packages) *and* its window. Run them with
no terms to cover every releasing package in dependency order. You can narrow this with `--package`, `--space`,
`--group`, or the invocation folder. You can also replace the window with `--since`, expand it downstream with
`--consumers`, and handle failures with `--on-error`.

A selection follows two rules. First, a term matching no package is an error. Second, a *selected* package that is not
releasing becomes a logged no-op, so your flow never fails over a converged or held package.

This second rule explains why a step run after `dispat commit --tag` covers nothing. You must use `--since all` to put
the tagged package back on the table.

These four command words are reserved. Like every command name, they win the `dispat <script>` shorthand over a
[script](../configuration/spaces.md#scripts-and-dispat-run) of the same name. This means `dispat commit` always runs
the command, but you can reach a script named commit by spelling it out as `dispat run commit`.

Every config value these commands consume is also a flag. You can override the config for a single run using the
options in the [flags table](#flags).

## Flags

Beside the [global flags](./README.md#global-flags):

| Flag                  | Default     | Effect                                                                                                                                                                                                 |
|-----------------------|-------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `--package`, `-p`     |             | Narrow to the named packages for every package-selecting command (`release`, `status`, `run`, `preview`, `changelog`, `autoversion`, `autowriter`, `autoreplacer`, `commit`, `github`, `compute`). This flag is repeatable and comma-separated. It matches case-insensitively and accepts `*` globs (`-p '*'` covers every package); see [Choosing the packages](./run.md#choosing-the-packages).                     |
| `--space`, `-s`       |             | Narrow the same eleven commands to every package in the named spaces. This accepts the same spellings and globs as the package flag. A standalone package belongs to no space; see [Choosing the packages](./run.md#choosing-the-packages).            |
| `--group`, `-g`       |             | Narrow the same eleven commands to every package in the named [versioning groups](../reference/releasing/versioning.md). This accepts the same spellings and globs. A group is a `versionGroups` entry or a space that versions as one, so it can cross spaces; see [Choosing the packages](./run.md#choosing-the-packages).            |
| `--since`             |             | Cover the packages modified by commits since a specific git revision. This applies to the same seven commands and replaces the release window. Pass `all` to cover every package; see [the run command](./run.md).                |
| `--consumers`         |             | Expand the selection downstream for the same seven commands. This covers every package that transitively depends on a selected one; see [the run command](./run.md).                                                          |
| `--on-error`          | `skip`      | Decide what a failed package does to its dependents for every sweeping command (`run`, `autowriter`, `autoreplacer`, `changelog`, `autoversion`, `commit`, `github`). Set this to `skip` (transitive) or `continue`. The command exits `1` on any failure regardless of this setting.                                         |
| `--owner`, `--repo`, `--api-url`, `--token-env` | from config | Override the matching `github.*` values for every package in the `github` command invocation.  |
| `--target`            |             | Push the commit to the remote before you use this. It tells the `github` command to create the tag at this commit or branch (`target_commitish`).   |
| `--release-name`      | from config | Override [`releaseName`](../configuration/records.md#your-own-words-around-an-entry) for the `changelog` and `github` commands. Environment variables like `$VAR` and `${VAR}` expand exactly as they do in the config. |
