# The changelog command

Run `dispat changelog` to write the pending changelog entry for each covered package, exactly as the release stage's
recorder would write it. If an entry already exists, dispat skips it and logs `W226`, and this same check tells the
release stage to skip entries you already wrote. Run this command early so a changelog written in a `beforePublish`
script, before `dispat commit`, lands inside the tagged commit.

dispat writes these entries inside each package's own folder. The command rides your build concurrency budget.

## The shape of what it writes

One entry has one shape, whichever door writes it. Items inside a section are separated by a single blank line, a
commit body is indented two spaces under the bullet it belongs to so that it stays part of that item, and every section
ends the same way whether or not its last line carried a body. The dependencies section is the one exception: its
lines are a table of movements and render as one tight block, with no blank lines between them. In the file, the new entry is separated from the entry
below it by [`changelog.entrySpacing`](../configuration/records.md#the-seam-between-entries) blank lines, two by
default.

This command renders exactly what the release stage's recorder renders, so an entry written from a stage script and one
written by the release itself are the same bytes. See [Release records](../configuration/records.md) for the entry
format options and [Existing changelogs and history](../examples/adopting.md#existing-changelogs-and-history) for what
happens to a file that already had content.

## The selection it shares

`dispat changelog`, `dispat autoversion`, `dispat commit`, and `dispat github` expose the release pipeline's native
steps to custom flows. You can run a step from a stage script at the exact moment your flow needs it. The release stage
later finds the work done and skips it.

All four commands share the run command's [selection](./run.md#choosing-the-packages) *and* its window. Run them with
no terms to cover every releasing package in dependency order. You can narrow this list with `--package`, `--space`,
`--group`, or your invocation folder, adjust the window with `--since` or `--consumers`, and handle failures with
`--on-error`.

Two rules govern what a selection may contain. A term matching no package causes an error, but a *selected* package
that is not releasing becomes a logged no-op. This means your flow never fails over a converged or held package, which
is why a step run after `dispat commit --tag` covers nothing until `--since all` puts the tagged package back on the
table.

The four command words are reserved. Each command name wins the `dispat <script>` shorthand over a
[script](../configuration/spaces.md#scripts-and-dispat-run) of the same name, so `dispat commit` always runs the
command. You can still reach your script by spelling it out as `dispat run commit`.

Every config value these commands consume is also a flag. You can use these flags to override the config for a single
invocation. See the [flags table](#flags) for details.

## Flags

Beside the [global flags](./README.md#global-flags):

| Flag                  | Default     | Effect                                                                                                                                                                                                 |
|-----------------------|-------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `--package`, `-p`     |             | Narrow every package-selecting command (`release`, `status`, `run`, `preview`, `changelog`, `autoversion`, `autowriter`, `autoreplacer`, `commit`, `github`, `compute`) to the named packages. This repeatable, comma-separated flag matches case-insensitively and accepts `*` globs, so `-p '*'` selects every package. See [Choosing the packages](./run.md#choosing-the-packages). |
| `--space`, `-s`       |             | Narrow the same eleven commands to every package of the named spaces. This flag uses the same spellings as the package flag, and a standalone package belongs to no space. See [Choosing the packages](./run.md#choosing-the-packages). |
| `--group`, `-g`       |             | Narrow the same eleven commands to every package of the named [versioning groups](../reference/releasing/versioning.md). This flag uses the same spellings, and a group is a `versionGroups` entry or a space that versions as one, so it may cross spaces. See [Choosing the packages](./run.md#choosing-the-packages). |
| `--since`             |             | Cover the packages addressed by commits since a git revision for the same seven commands. This replaces the default release window. Pass `all` to cover every package, and see [the run command](./run.md) for details. |
| `--consumers`         |             | Tell the same seven commands to additionally cover every package that transitively depends on a selected one. See [the run command](./run.md). |
| `--on-error`          | `skip`      | Decide what a failed package does to its dependents during every sweeping command (`run`, `autowriter`, `autoreplacer`, `changelog`, `autoversion`, `commit`, `github`). Set this to `skip` for transitive skipping or `continue`. The command always exits `1` on any failure. |
| `--file`, `-f`, `--file-title`, `--date-format` | from config | Override the matching `changelog.*` values for every package in a `changelog` invocation. Use `--file-title` to state the whole title as one line. |
| `--release-name`      | from config | Override [`releaseName`](../configuration/records.md#your-own-words-around-an-entry) for a `changelog` or `github` invocation. Variables like `$VAR` and `${VAR}` expand exactly as they do in the config. |
| `--authors`           | from config | Override [`authors.placement`](../configuration/records.md#attributing-an-entry-to-its-authors) for a `changelog` or `github` invocation: `off`, `inline`, `section` or `both`. Any other value is refused before anything is planned. |
| `--authors-format`    | from config | Override `authors.format`: `fullname`, or `username` for the local part of the email address. |
| `--authors-commits`   | from config | Override `authors.commits`: `ccme` for the commits behind the entry's own lines, or `all` for every commit in the release window. |
| `--authors-include`   | from config | Override `authors.include`. This repeatable, comma-separated list of case-insensitive globs replaces the configured list whole, and each pattern is tried against the full name, the username and the email. |
| `--authors-exclude`   | from config | Override `authors.exclude`, with the same spellings. It is applied after `--authors-include` and wins. |
| `--authors-title`     | from config | Override `authors.title`, the heading of the authors section. |
