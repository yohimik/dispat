# The scanner command

`dispat scanner [folder]` walks the folder (`--root-only` stays out of sub-folders) and prints each manifest's
identity, ecosystem and dependency declarations. A manifest that fails to parse is reported while the rest are still
listed, and `--strict` turns that into exit `1`.

`dispat scanner`, `dispat writer` and `dispat replacer` expose the manifest libraries directly: the first prints what a
folder's manifests declare, the second edits a declaration in place while preserving the file's formatting, and the
third replaces literal text in any file at all. All three need no config file, no git repository and no release plan,
so they work on any checkout. Positional paths resolve against `--root`, and `--log-format json` swaps each command's
listing for one event per file.

The full guide, with worked examples and the format list, is [Manifest tools](../editing/manifests.md).

## Flags

Beside the [global flags](./README.md#global-flags):

| Flag                  | Default     | Effect                                                                                                                                                                                                 |
|-----------------------|-------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `--root-only`         |             | `scanner` only: read the folder's own manifests without descending into sub-folders.                                       |
| `--verify-unlinked`   |             | Exit `1` when any manifest still carries a local-link directive. The scope is exactly what `--link-local` can inject: a go.mod filesystem `replace`, a Cargo `[patch.crates-io]` or uv `[tool.uv.sources]` path entry, a pubspec `dependency_overrides` path, an npm `file:`/`link:` override. Each finding is an error event with code `E215`. |
| `--verify-linked`     |             | The inverse: exit `1` when no manifest in the selection carries such a directive (code `E216`), which is how a pipeline proves its link step actually landed. Cannot be combined with `--verify-unlinked`. |
| `--forbid-range`      |             | Exit `1` for every declared dependency range matching the pattern, literal text with `*` as a wildcard; repeatable, one error event per finding (code `E217`). `--forbid-range 'workspace:*'` gates placeholder ranges before a publish. |
| `--require-range`     |             | Exit `1` when no declared dependency range matches the pattern (code `E218`); repeatable, each pattern asked on its own. The same pattern cannot be forbidden and required at once. |
| `--strict`            |             | Turns a tolerated finding into a failure. `scanner`, `writer` and `replacer`: a manifest that failed to parse, an edit the manifest does not declare, or a `--replace` that matched nothing. |
