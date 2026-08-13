# The scanner command

`dispat scanner [folder]` walks the folder (`--root-only` stays out of sub-folders) and prints each manifest's
identity, ecosystem and dependency declarations. A manifest that fails to parse is reported while the rest are still
listed, and `--strict` turns that into exit `1`.

`dispat scanner`, `dispat writer` and `dispat replacer` expose the manifest libraries directly: the first prints what a
folder's manifests declare, the second edits a declaration in place while preserving the file's formatting, and the
third replaces literal text in any file at all. All three need no config file, no git repository and no release plan,
so they work on any checkout. Positional paths resolve against `--root`, and `--log-format json` swaps each command's
listing for one event per file.

The full guide, with worked examples and the format list, is [Manifest tools](../cookbook/editing/manifests.md).

## Flags

Beside the [global flags](./README.md#global-flags):

| Flag                  | Default     | Effect                                                                                                                                                                                                 |
|-----------------------|-------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `--root-only`         |             | `scanner` only: read the folder's own manifests without descending into sub-folders.                                       |
| `--strict`            |             | Turns a tolerated finding into a failure. `scanner`, `writer` and `replacer`: a manifest that failed to parse, an edit the manifest does not declare, or a `--sub` that matched nothing. |
