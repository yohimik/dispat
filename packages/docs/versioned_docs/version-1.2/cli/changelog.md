# The changelog command

Run `dispat changelog` to write the pending changelog entry for each covered package, exactly as the release stage's
recorder would write it. If an entry already exists, dispat skips it and logs `W226`, and this same check tells the
release stage to skip entries you already wrote. Run this command early so a changelog written in a `beforePublish`
script, before `dispat commit`, lands inside the tagged commit.

dispat writes these entries inside each package's own folder. The command rides your build concurrency budget.

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
