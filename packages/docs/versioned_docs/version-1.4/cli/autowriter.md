# The autowriter command

Run `dispat autowriter` to apply `dispat writer` across a selection of packages instead of a list of files. The
`--set-version`, `--set`, and `--link` flags mean exactly what they mean there. dispat finds the manifests by scanning
each covered package that the plan and the window pick.

You can pass the same selection and window flags as the step commands. The `--package`, `--space`, `--group`,
`--since`, and `--consumers` flags all read the same.

Pass `--manifests root` (the default) to edit the manifests sitting in each package folder. Pass `--manifests all` to
edit every manifest under it. This leaves any manifest belonging to another package to that package.

You can write a range as `{version}` to resolve to the planned version of the package the edit names. Pass
`--set-version {version}` to write the covered package's own planned version to its root manifests alone.

Pass `--only-updated` to drop every edit naming a package this run does not update. Pass `--strict` to fail on an edit
that matches no manifest anywhere. This is the cross-package reading of missing, because an edit absent from one
manifest out of twenty is the ordinary case.

Pass `--set-local`, `--link-local`, or `--unlink-local` to derive the edits instead of taking them. dispat finds every
dependency a manifest declares that names another package in the workspace. It reconciles the range to that package's
version (spelled by `--range`), writes its local folder redirect, or removes that redirect.

A dependency named by `--set` or `--link` keeps what you typed on the command line. Derived links skip `package.json`
because npm refuses an override for a directly declared dependency. No release removes a local link, so run
`--unlink-local` to remove them before you publish.

In `go.mod` files, the link half also reaches an `// indirect` require. Go honours `replace` only in the main module's
`go.mod` file, so a provider reached only through another module still needs a redirect from the consumer's own file.
The `--set-local` flag leaves those requires as it found them. The version there belongs to the toolchain.

Do not run `go work sync` or `go mod tidy` while links are in place. Both commands drop the `go.sum` entries a local
redirect makes redundant. Unlinking needs those entries back.

A covered package with no writable manifest is a no-op. The command fails if your selection contains no packages with a
writable manifest. Read [Editing across the monorepo](../editing/autowriter.md) for worked examples of the whole
command.

## Flags

You can use the [global flags](./README.md#global-flags) alongside these options:

| Flag                  | Default     | Effect                                                                                                                                                                                                 |
|-----------------------|-------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `--package`, `-p`     |             | Narrows package-selecting commands (`release`, `status`, `run`, `preview`, `changelog`, `autoversion`, `autowriter`, `autoreplacer`, `commit`, `github`, `compute`) to the named packages. You can repeat this comma-separated flag, which matches case-insensitively and accepts `*` globs (`-p '*'` selects every package). Read [Choosing the packages](./run.md#choosing-the-packages) for more details. |
| `--space`, `-s`       |             | Narrows the same eleven commands to every package in the named spaces. It uses the same spellings, and a standalone package belongs to no space. Read [Choosing the packages](./run.md#choosing-the-packages) for more details. |
| `--group`, `-g`       |             | Narrows the same eleven commands to every package in the named [versioning groups](../reference/releasing/versioning.md) using the same spellings. A group is a `versionGroups` entry or a space that versions as one, so it can cross spaces. Read [Choosing the packages](./run.md#choosing-the-packages) for more details. |
| `--since`             |             | Instructs the same seven commands to cover the packages addressed by commits since a git revision, instead of the release window. Pass `all` to cover every package. Read [the run command](./run.md) for more details. |
| `--consumers`         |             | Instructs the same seven commands to additionally cover every package that transitively depends on a selected one. Read [the run command](./run.md) for more details. |
| `--on-error`          | `skip`      | Tells every sweeping command (`run`, `autowriter`, `autoreplacer`, `changelog`, `autoversion`, `commit`, `github`) what a failed package does to its dependents. Pass `skip` to skip them transitively, or `continue` to process them. The command exits `1` on any failure either way. |
| `--set-version`       |             | Tells `writer` and `autowriter` to rewrite the manifest's own version field. Pass `{version}` to `autowriter` to write the covered package's planned version. This touches only its root manifests. |
| `--set`               |             | Tells `writer` and `autowriter` to set one dependency's declared range. Format this as `[kind:]name=range` and repeat the flag as needed. For `autowriter`, writing `{version}` in the range inserts the planned version of the package the edit names. |
| `--link`              |             | Tells `writer` and `autowriter` to point a dependency at a local folder. Format this repeatable flag as `name=path`. Pass an empty path to remove the redirect. |
| `--set-local`         |             | Tells `autowriter` to set every declared workspace dependency to its provider's version. The `--range` flag spells this version. |
| `--link-local`        |             | Tells `autowriter` to point every declared workspace dependency at its folder. In `go.mod` files, this includes dependencies named only by an `// indirect` require. You cannot combine `--link-local` with `--unlink-local`. |
| `--unlink-local`      |             | Tells `autowriter` to remove the local folder redirects that `--link-local` writes. No release removes these automatically. Run this command before you publish. |
| `--range`             | from config | Tells `autoversion` and `autowriter` to override the [`autoVersion.range`](../configuration/autoversion.md) write policy. This policy defines how a reconciled range is spelled (`caret`, `tilde`, `exact`, a `{version}` template, or a literal). For `autowriter`, this spells the version that `--set-local` derives. |
| `--manifests`         | `root` here | Tells `autoversion` and `autowriter` which of a package's manifests to rewrite. Pass `root` to rewrite the ones in the package folder, or `all` to rewrite every manifest under it. The `autoversion` command also accepts `none` to turn off its parsing strategy, and takes its default from the config; `autowriter` reads no config for this and always defaults to `root`. |
| `--only-updated`      |             | Tells `autoversion` and `autowriter` to rewrite only the declarations naming a package this run updates. This leaves a range alone if it fell behind a provider released earlier. |
| `--sync-lock`         | `true`      | Tells `autoversion` and `autowriter` to run the syncLock scripts for packages whose manifests changed. Pass `--sync-lock=false` to skip them. |
| `--strict`            |             | Turns a tolerated finding into a failure. For `autowriter`, this fails the command if an edit matches no manifest anywhere. Read [Editing across the monorepo](../editing/autowriter.md#applied-skipped-and-missing-across-many-packages) for more details. |
