# Defold projects

A Defold project’s own version can be written in `game.project`. Its library archive URLs need separate handling.
Use [From one package to many](./one-to-many.md) when the project gains extensions, a server or a website.

## Version and dependency boundaries

The writer can change an existing project version:

```sh
dispat writer game.project --set-version 1.0.0 --strict
dispat scanner . --root-only --strict --log-format json
```

Archive URLs need a targeted replacement or version script. Scanning and writing `game.project` does not run Defold's
editor or Bob, build extensions, or upload to a store. Keep those native checks in the configured build and publish
commands.

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
keys. A short native version such as `0.9` may also differ from a three-part semantic release baseline.

## Libraries and release identity

If an extension built by another package is a release input, declare an explicit dependency with `keep: true`.
Update its archive URL with a targeted [replace rule](../configuration/autoversion.md) or version script that
preserves the repository, archive syntax and desired revision. Do not expect the scanner to derive this graph edge
or the dependency writer to rebuild an arbitrary URL.

The installable version of an extension may come from a Git tag while `game.project` describes its demo. Decide
which deliverable dispat is releasing before rewriting the demo’s version. Keep native build, artifact validation
and store publication in the existing pipeline. See [game development](./game.md), [Steam](./steam.md) and
[itch.io](./itch.md).
