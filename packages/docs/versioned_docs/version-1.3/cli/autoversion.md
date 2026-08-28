# The autoversion command

Run `dispat autoversion` to execute the native manifest reconciliation of the version stage. dispat rewrites declared
workspace ranges to the planned versions and updates the package's own version. It then runs the space's `syncLock`
scripts for each package whose manifests actually changed.

Rewriting already-reconciled manifests changes nothing, so you can safely re-run this command. dispat skips a space
without an `autoVersion` block unless you force one with a policy flag. That flag makes dispat start from the defaults.

Pass `--only-updated` to narrow rewrites to declarations naming a package this run releases. This leaves a range that
fell behind an earlier release exactly as it is, instead of catching it up (`W197`).

dispat writes inside each package's own folder. It rides the build concurrency budget.

## The selection it shares

Use `dispat changelog`, `dispat autoversion`, `dispat commit`, and `dispat github` to expose the release pipeline's
native steps to custom flows. A stage script can run a step exactly when the flow needs it. The release stage later
finds the work done and skips it.

These four commands share the run command's [selection](./run.md#choosing-the-packages) *and* its window. Run them with
no terms to cover every releasing package in dependency order. You can narrow this with `--package`, `--space`,
`--group`, or the invocation folder.

Pass `--since` to replace the window. Pass `--consumers` to expand it downstream. Use `--on-error` to decide what a
failed package does to its dependents.

A selection must follow two rules. First, a term matching no package is an error. Second, a *selected* package that is
not releasing is a logged no-op.

This means a flow never fails over a converged or held package. That second rule is also why a step run after
`dispat commit --tag` covers nothing. You must pass `--since all` to put the tagged package back on the table.

The four command words are reserved. Each wins the `dispat <script>` shorthand over a
[script](../configuration/spaces.md#scripts-and-dispat-run) of the same name, so `dispat commit` is always the command.
Run `dispat run commit` to reach the script instead.

Every config value the commands consume is also a flag. You can use these flags to override the config for a single
invocation. See the [flags table](#flags) for details.

## Flags

These commands accept the [global flags](./README.md#global-flags) alongside the following:

| Flag                  | Default     | Effect                                                                                                                                                                                                 |
|-----------------------|-------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `--package`, `-p`     |             | Narrow every package-selecting command (`release`, `status`, `run`, `preview`, `changelog`, `autoversion`, `autowriter`, `autoreplacer`, `commit`, `github`, `compute`) to the named packages. This flag is repeatable, comma-separated, and case-insensitive. It supports `*` globs, so `-p '*'` selects every package; see [Choosing the packages](./run.md#choosing-the-packages). |
| `--space`, `-s`       |             | Narrow the same eleven commands to every package in the named spaces. This uses the same spelling rules as packages. A standalone package belongs to no space; see [Choosing the packages](./run.md#choosing-the-packages). |
| `--group`, `-g`       |             | Narrow the same eleven commands to every package in the named [versioning groups](../reference/releasing/versioning.md). This uses the same spelling rules. A group is a `versionGroups` entry or a space that versions as one, so it can cross spaces; see [Choosing the packages](./run.md#choosing-the-packages). |
| `--since`             |             | Apply to the same seven commands. Cover the packages addressed by commits since a git revision, instead of the release window. Pass `all` to cover every package; see [the run command](./run.md). |
| `--consumers`         |             | Apply to the same seven commands. Cover every package that transitively depends on a selected one. See [the run command](./run.md). |
| `--on-error`          | `skip`      | Decide what a failed package does to its dependents in every sweeping command (`run`, `autowriter`, `autoreplacer`, `changelog`, `autoversion`, `commit`, `github`). Pass `skip` to skip transitively, or `continue` to proceed. The command exits `1` on any failure either way. |
| `--match`, `--write-version` | from config | Apply to `autoversion` only. Override the matching `autoVersion.*` policy for this invocation. |
| `--range`             | from config | Apply to `autoversion` and `autowriter`. Override the [`autoVersion.range`](../configuration/autoversion.md) write policy. This policy defines how dispat spells a reconciled range (`caret`, `tilde`, `exact`, a `{version}` template, or a literal). |
| `--manifests`         | from config | Apply to `autoversion` and `autowriter`. Choose which of a package's manifests dispat rewrites, passing `root` for manifests in the package folder or `all` for every manifest under it. `autoversion` also accepts `none` to turn off its parsing strategy. |
| `--only-updated`      |             | Apply to `autoversion` and `autowriter`. Rewrite only the declarations naming a package this run updates. This leaves a range that fell behind an earlier release exactly as it is. |
| `--no-replace`        |             | Apply to `autoversion` only. Skip the `autoVersion.replace` rules for this invocation. |
| `--sync-lock`         | `true`      | Apply to `autoversion` and `autowriter`. Run the `syncLock` scripts for packages whose manifests changed. Pass `--sync-lock=false` to skip them. |
