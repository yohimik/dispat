# The commit command

`dispat commit` creates each covered package's release commit: the package folder staged together with
the `commit.include` paths, the message rendered from `commit.messageFormat` with that one package's name and tag.
A package with nothing to stage is a clean no-op. With `--tag`, the annotated release tag is created at the resulting
commit; a tag that already exists there is a skip (`W223`), while a tag at any other commit is an error. With
`--push`, the branch is pushed once after all packages, and with `--tag` the tags too, skipping any already on the
remote. When the command runs inside a release stage script (the environment carries `DISPAT_OUTPUT`), each package's
commit is exported as `PACKAGE_<KEY>`, pinning the outer run's tag and GitHub release to it.

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
| `--package`, `-p`     |             | Every package-selecting command (`release`, `status`, `run`, `preview`, `changelog`, `autoversion`, `autowriter`, `autosubstitute`, `commit`, `github`, `compute`): narrow to the named packages. Repeatable and comma-separated, matched case-insensitively, `*` globs (`-p '*'` is every package); see [Choosing the packages](./run.md#choosing-the-packages).                     |
| `--space`, `-s`       |             | The same eleven commands: narrow to every package of the named spaces, with the same spellings. A standalone package belongs to no space; see [Choosing the packages](./run.md#choosing-the-packages).            |
| `--group`, `-g`       |             | The same eleven commands: narrow to every package of the named [versioning groups](../releasing/versioning.md), with the same spellings. A group is a `versionGroups` entry or a space that versions as one, so it may cross spaces; see [Choosing the packages](./run.md#choosing-the-packages).            |
| `--since`             |             | The same seven commands: cover the packages the commits since a git revision address, instead of the release window. `all` covers every package; see [the run command](./run.md).                |
| `--consumers`         |             | The same seven commands: additionally cover every package that transitively depends on a selected one; see [the run command](./run.md).                                                          |
| `--on-error`          | `skip`      | Every sweeping command (`run`, `autowriter`, `autosubstitute`, `changelog`, `autoversion`, `commit`, `github`): what a failed package does to its dependents, `skip` (transitive) or `continue`. Either way the command exits `1` on any failure.                                         |
| `--tag`               |             | `commit` only: also create the annotated release tag at the resulting commit; an identical existing tag is skipped, and one at a different commit is left alone and reported (`E211`).        |
| `--push`              |             | `commit` only: push the branch, and with `--tag` the tags.                         |
| `--no-force`          |             | `commit` only: turn [`commit.force`](../configuration/records.md#force) off for this invocation, leaving a tag the repository or the remote already carries as it is.                         |
| `--name`, `--email`   | from config | `commit` only: override the `commit.name` / `commit.email` committer identity.                                             |
| `--remote`            | from config | `commit` only: override the `commit.remote` push target.                                                                   |
| `--tag-name`          | computed    | `commit` only: name the annotated tag instead of computing it; pass `$DISPAT_TAG` from a release stage. One package only.   |
| `--message-format`    | from config | `commit` only: override the `commit.messageFormat` template.                                                               |
| `--include`           | from config | `commit` only: override the `commit.include` extra staged paths.                                                           |
