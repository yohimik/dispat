# The scanner command

Run `dispat scanner [folder]` to walk a directory and print each manifest's identity, ecosystem, and dependency
declarations. Pass `--root-only` to stay out of sub-folders. A manifest that fails to parse reports a warning but
leaves the rest of the list intact, and `--strict` turns that failure into an error and an exit `1`.

The `dispat scanner`, `dispat writer`, and `dispat replacer` commands expose the manifest libraries directly. The first
prints what a folder's manifests declare, the second edits a declaration in place while preserving formatting, and the
third replaces literal text anywhere. You can run them on any checkout because they need no configuration file, git
repository, or release plan.

Positional paths resolve against `--root`. Pass `--log-format json` to swap each command's listing for one event per
file.

See [Manifest tools](../editing/manifests.md) for the full guide, worked examples, and the format list.

## Flags

These apply alongside the [global flags](./README.md#global-flags):

| Flag                  | Default     | Effect                                                                                                                                                                                                 |
|-----------------------|-------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `--root-only`         |             | Read the folder's own manifests without descending into sub-folders. This applies to `scanner` only. |
| `--verify-unlinked`   |             | Exit `1` when any manifest still carries a local-link directive. This checks for exactly what `--link-local` injects: a go.mod filesystem `replace`, a Cargo `[patch.crates-io]` or uv `[tool.uv.sources]` path entry, a pubspec `dependency_overrides` path, or an npm `file:`/`link:` override. dispat emits each finding as an error event with code `E215`. |
| `--verify-linked`     |             | Exit `1` when no manifest in the selection carries a local-link directive. Use this in a pipeline to prove your link step actually landed. dispat emits code `E216` on failure, and you cannot combine this flag with `--verify-unlinked`. |
| `--forbid-range`      |             | Exit `1` for every declared dependency range that matches your pattern. The pattern takes literal text with `*` as a wildcard, and you can repeat the flag. Pass `--forbid-range 'workspace:*'` to gate placeholder ranges before a publish, which emits one `E217` error event per finding. |
| `--require-range`     |             | Exit `1` when no declared dependency range matches your pattern. You can repeat the flag to require multiple patterns, and dispat evaluates each one on its own to emit code `E218` on failure. You cannot forbid and require the same pattern at once. |
| `--strict`            |             | Turn a tolerated finding into a failure. This applies to a manifest that fails to parse in `scanner`, an edit the manifest does not declare in `writer`, or a `--replace` that matches nothing in `replacer`. |
