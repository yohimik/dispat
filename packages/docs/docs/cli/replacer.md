# The replacer command

`dispat replacer <file>...` applies each `--sub 'find=>write'` to each named file, in the order given and to every
occurrence, parsing nothing. It is the tool for the versions no manifest holds: a Gradle coordinate, a Helm chart,
a README example. A pattern that matched nothing anywhere fails only under `--strict`; a file that cannot be read, or
that looks binary, exits `1`.

`dispat scanner`, `dispat writer` and `dispat replacer` expose the manifest libraries directly: the first prints what a
folder's manifests declare, the second edits a declaration in place while preserving the file's formatting, and the
third replaces literal text in any file at all. All three need no config file, no git repository and no release plan,
so they work on any checkout. Positional paths resolve against `--root`, and `--log-format json` swaps each command's
listing for one event per file.

The replacer has [a page of its own](../cookbook/editing/replacer.md).

## Flags

Beside the [global flags](./README.md#global-flags):

| Flag                  | Default     | Effect                                                                                                                                                                                                 |
|-----------------------|-------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `--sub`               |             | `replacer` and `autoreplacer`: replace literal text, `find=>write`; repeatable and applied in order. See [The replacer](../cookbook/editing/replacer.md). |
| `--strict`            |             | Turns a tolerated finding into a failure. `scanner`, `writer` and `replacer`: a manifest that failed to parse, an edit the manifest does not declare, or a `--sub` that matched nothing. |
