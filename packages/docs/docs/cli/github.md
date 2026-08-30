# The github command

Run `dispat github` to create a GitHub release for each covered package. It creates exactly what the release pipeline's
recorder would create: a release named after the package tag, or after
[`releaseName`](../configuration/records.md#your-own-words-around-an-entry), with the rendered changelog sections as
its body. If the repository already has the release, dispat skips it and logs `W224`. This means a repeated run, and
the release that follows it, converge instead of failing when the API rejects a duplicate tag.

With [`github.draft`](../configuration/records.md#github), or with `--draft` on this command, the release is created
for a person to publish after reading it. It carries no tag ref until they do, so nothing that resolves a release by
its tag sees it meanwhile.

You opt in exactly as you do for the recorder. dispat releases a package when its scripts export
[`DISPAT_EXPORT_GITHUB`](../reference/environment.md#script-outputs) or when
[`github.allPackages`](../configuration/records.md#github) covers it.

When you run this inside a stage script, dispat reads that export from its environment and attaches the listed files.
The stage provides this environment along with `DISPAT_PACKAGE` to identify the package. If you run the command by hand
with the variable exported, it covers every package your invocation selects. Without either opt-in, the command
publishes nothing and exits with `0`.

The command covers packages one at a time. A repository has one index and one HEAD, and a clear release order is worth
reading.

## The shape of the body

The body is rendered exactly as the release stage's recorder renders it, so a release created from a stage script and
one created by the release itself carry the same text. Items inside a section are separated by a single blank line, a
commit body is indented two spaces under the bullet it belongs to so that it stays part of that item, and every section
ends the same way whether or not its last line carried a body. The dependencies section is the one exception: its
lines are a table of movements and render as one tight block, with no blank lines between them. A release body is one
document with no entry beneath it, so the changelog file's
[entry seam](../configuration/records.md#the-seam-between-entries) does not apply here.

See [Release records](../configuration/records.md) for the entry format options the body is rendered under.

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
| `--draft`             | from config | Override [`github.draft`](../configuration/records.md#github) for every package in the invocation. `--draft` creates the release for a person to publish, and `--draft=false` publishes straight away over a configured draft. |
| `--release-name`      | from config | Override [`releaseName`](../configuration/records.md#your-own-words-around-an-entry) for the `changelog` and `github` commands. Environment variables like `$VAR` and `${VAR}` expand exactly as they do in the config. |
| `--authors`           | from config | Override [`authors.placement`](../configuration/records.md#attributing-an-entry-to-its-authors) for the `changelog` and `github` commands: `off`, `inline`, `section` or `both`. Any other value is refused before anything is planned. |
| `--authors-format`    | from config | Override `authors.format`: `fullname`, or `username` for the local part of the email address. |
| `--authors-commits`   | from config | Override `authors.commits`: `ccme` for the commits behind the entry's own lines, or `all` for every commit in the release window. |
| `--authors-include`   | from config | Override `authors.include`. This repeatable, comma-separated list of case-insensitive globs replaces the configured list whole, and each pattern is tried against the full name, the username and the email. |
| `--authors-exclude`   | from config | Override `authors.exclude`, with the same spellings. It is applied after `--authors-include` and wins. |
| `--authors-title`     | from config | Override `authors.title`, the heading of the authors section. |
