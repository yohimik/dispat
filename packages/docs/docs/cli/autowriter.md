# The autowriter command

`dispat autowriter` is `dispat writer` pointed at a selection instead of a list of files: `--set-version`, `--set` and
`--link` mean exactly what they mean there, but the manifests are found by scanning each covered package and the
packages are the ones the plan and the window pick. It takes the same selection and window flags as the step commands,
so `--package`, `--space`, `--group`, `--since` and `--consumers` all read the same.

`--manifests root` (the default) edits the manifests sitting in each package folder, `--manifests all` every manifest
under it, leaving any that belongs to another package to that package. A range may be written as `{version}`, which
resolves to the planned version of the package the edit names, and `--set-version {version}` to the covered package's
own, written to its root manifests alone. `--only-updated` drops every edit naming a package this run does not
update, and `--strict` fails on an edit that matched no manifest anywhere, which is the cross-package reading of
missing: an edit absent from one manifest of twenty is the ordinary case.

`--set-local`, `--link-local` and `--unlink-local` derive the edits instead of taking them: every dependency a
manifest declares that names another package in the workspace has its range reconciled to that package's version
(spelled by `--range`), its local folder redirect written, or that redirect removed. A dependency named by `--set` or
`--link` keeps what the command line said. Derived links skip `package.json`, since npm refuses an override for a
directly declared dependency, and a local link must be removed with `--unlink-local` before publishing because no
release removes one.

In `go.mod` the link half also reaches an `// indirect` require, because Go honours `replace` in the main module's
`go.mod` alone: a provider reached only through another module still has to be redirected from the consumer's own
file. `--set-local` leaves those requires as it found them: the version there belongs to the toolchain. Do not run
`go work sync` or `go mod tidy` while links are in place: both drop the `go.sum` entries a local redirect makes
redundant, and unlinking needs them back.

A covered package with no manifest anything can write is a no-op; a selection in which none of them has one is an
error. The whole command, with worked examples, is in
[Editing across the monorepo](../cookbook/editing/autowriter.md).

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
| `--set-version`       |             | `writer` and `autowriter`: rewrite the manifest's own version field. For `autowriter`, `{version}` writes the covered package's planned version, and only its root manifests are touched. |
| `--set`               |             | `writer` and `autowriter`: set one dependency's declared range, `[kind:]name=range`; repeatable. For `autowriter`, `{version}` in the range is the planned version of the package the edit names. |
| `--link`              |             | `writer` and `autowriter`: point a dependency at a local folder, `name=path`; an empty path removes the redirect. Repeatable. |
| `--set-local`         |             | `autowriter` only: set every declared workspace dependency to its provider's version, spelled by `--range`. |
| `--link-local`        |             | `autowriter` only: point every declared workspace dependency at its folder, plus, in `go.mod`, the ones only an `// indirect` require names. The two cannot be combined with `--unlink-local`. |
| `--unlink-local`      |             | `autowriter` only: remove the local folder redirects `--link-local` writes. No release removes one, so run this before publishing. |
| `--range`             | from config | `autoversion` and `autowriter`: override the [`autoVersion.range`](../configuration/autoversion.md) write policy, which is how a reconciled range is spelled (`caret`, `tilde`, `exact`, a `{version}` template, or a literal). For `autowriter` it spells what `--set-local` derives. |
| `--manifests`         | from config | `autoversion` and `autowriter`: which of a package's manifests are rewritten, `root` (the ones in the package folder) or `all` (every manifest under it). `autoversion` also takes `none`, which turns its parsing strategy off. |
| `--only-updated`      |             | `autoversion` and `autowriter`: rewrite only the declarations naming a package this run updates, leaving a range that had fallen behind a provider released earlier as it is. |
| `--sync-lock`         | `true`      | `autoversion` and `autowriter`: run the syncLock scripts for packages whose manifests changed; `--sync-lock=false` skips them. |
| `--strict`            |             | Turns a tolerated finding into a failure. `autowriter`: an edit that matched no manifest anywhere; see [Editing across the monorepo](../cookbook/editing/autowriter.md#applied-skipped-and-missing-across-many-packages). |
