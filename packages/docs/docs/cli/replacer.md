# The replacer command

Run `dispat replacer <file>...` to change literal text across your files. Pass `--replace 'find=>write'` to swap every
occurrence of a string without parsing the file format, and repeat the flag to apply multiple replacements in order.
Use this for versions that live outside standard manifests, like a Gradle coordinate, a Helm chart, or a README
example.

The command exits `1` if a file cannot be read or looks binary. If a pattern matches nothing, the command succeeds
unless you pass `--strict`.

No configuration file, Git repository, or release plan is required. For a dependency declared in a supported
manifest, [`dispat writer`](./writer.md) can target the field directly. Use [`dispat scanner`](./scanner.md) to inspect
those declarations.

Positional paths resolve against `--root`. Pass `--log-format json` to swap the default listing for one event per file.

See [Replacing literal text](../editing/replacer.md) for examples and matching rules.

## Flags

These apply alongside the [global flags](./README.md#global-flags):

### `--replace`

Use with `replacer` and `autoreplacer` to replace literal text using `find=>write`. You can repeat this flag to apply
multiple replacements in order. See [The replacer](../editing/replacer.md).

### `--strict`

Turn a tolerated finding into a failure. For `scanner`, `writer`, and `replacer`, this fails the command if a manifest
fails to parse, an edit targets a missing declaration, or a `--replace` matches nothing.
