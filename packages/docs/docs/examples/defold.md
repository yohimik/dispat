# Defold projects

A Defold project’s own version can be written in `game.project`. Its library archive URLs need separate handling.
Use [From one package to many](./one-to-many.md) when the project gains extensions, a server or a website.

## A public input checked locally

[Monarch at `9540975`](https://github.com/britzl/monarch/blob/954097522e7c2d4a710464dd9e8b48fb309685ab/game.project)
declares the title `Monarch`, version `0.9`, and a `dependencies#0` URL for deftest. With dispat 1.10.0, the scanner
reported the title and version but no dependency edges. In a disposable copy, this command changed only the project
version to the test value `9.8.7`:

```sh
dispat writer game.project --set-version 9.8.7 --strict
dispat scanner . --root-only --strict --log-format json
```

Readback reported `Monarch@9.8.7`; the archive URL stayed unchanged, and repeating the edit changed no bytes.
The [public-manifest verifier](./open-source.md) reproduces this against the pinned file, with hashes before and after.
It did not run Defold’s editor, Bob, an extension build, or a store upload.

## Coordinate the project version

For a Defold project in `game/`, the graph and versioning part of a configuration is:

```json
{
  "packages": {
    "game": {"path": "game", "autoVersion": {"enabled": true}}
  },
  "initials": {"game": "0.9.0"}
}
```

This is a configuration fragment for version planning. Add your existing Defold build and publisher under `scripts`
and `flow`; it is not a complete release configuration. `initials` supplies a semantic baseline until there is a
matching release tag. In an established repository, preserve its real release baseline and tag format instead.

Run status and version reconciliation in a disposable checkout, then inspect the native artifact’s version. Ensure
`[project] version` already exists when choosing native rewriting. The writer changes existing values, not absent
keys, and this sample’s `0.9` manifest value is distinct from a three-part semantic release baseline.

## Libraries and release identity

If an extension built by another package is a release input, declare an explicit dependency with `keep: true`.
Update its archive URL with a targeted [replace rule](../configuration/autoversion.md) or version script that
preserves the repository, archive syntax and desired revision. Do not expect the scanner to derive this graph edge
or the dependency writer to rebuild an arbitrary URL.

The installable version of an extension may come from a Git tag while `game.project` describes its demo. Decide
which deliverable dispat is releasing before rewriting the demo’s version. Keep native build, artifact validation
and store publication in the existing pipeline. See [game development](./game.md), [Steam](./steam.md) and
[itch.io](./itch.md).
