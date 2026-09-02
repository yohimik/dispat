# The replacer command

Run `dispat replacer <file>...` to change literal text across your files. Pass `--replace 'find=>write'` to swap every
occurrence of a string without parsing the file format, and repeat the flag to apply multiple replacements in order.
Use this for versions that live outside standard manifests, like a Gradle coordinate, a Helm chart, or a README
example.

The command exits `1` if a file cannot be read or looks binary. If a pattern matches nothing, the command succeeds
unless you pass `--strict`.

The `dispat scanner`, `dispat writer`, and `dispat replacer` commands expose the manifest libraries directly.
`dispat scanner` prints what a folder's manifests declare, `dispat writer` edits a declaration in place while
preserving formatting, and `dispat replacer` changes literal text anywhere. You do not need a config file, a git
repository, or a release plan to run them on any checkout.

Positional paths resolve against `--root`. Pass `--log-format json` to swap the default listing for one event per file.

The replacer has [a page of its own](../editing/replacer.md).

## Flags

These apply alongside the [global flags](./README.md#global-flags):

### `--replace`

Use with `replacer` and `autoreplacer` to replace literal text using `find=>write`. You can repeat this flag to apply
multiple replacements in order. See [The replacer](../editing/replacer.md).

### `--strict`

Turn a tolerated finding into a failure. For `scanner`, `writer`, and `replacer`, this fails the command if a manifest
fails to parse, an edit targets a missing declaration, or a `--replace` matches nothing.
