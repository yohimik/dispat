# Godot

Godot has three places to check: `project.godot` for the project version, `plugin.cfg` for an addon, and
`export_presets.cfg` for store versions. dispat reads and writes all three. This example exports a Linux build and
publishes it to itch.io.

## The short version

```json title="dispat.json"
{
  "scripts": {
    "export": "godot --headless --export-release Linux build/acme-game.x86_64",
    "publish": "butler push build acme/game:linux --userversion $DISPAT_NEW_VERSION"
  },
  "packages": {
    "game": {
      "path": "game",
      "flow": {"build": "export", "publish": "publish"},
      "autoVersion": {"enabled": true}
    }
  },
  "initials": {"game": "0.1.0"}
}
```

The default manifest scope automatically finds `project.godot` because it sits directly in the package folder. Commit a
feature and run `dispat` to bump the version. The project file, the git tag, and the changelog will all agree.

```console
$ git commit -m "feat(game): co-op mode"
$ dispat
12:50:45 INF ● changed bump=minor package=game version="0.1.0 -> 0.2.0"
12:50:45 INF manifest reconciled manifest=project.godot package=game ranges=0 stage=version version=0.2.0 versionWritten=true
12:50:45 INF published package=game tag=game@0.2.0 version=0.2.0
```

## What dispat reads

### `project.godot`

```ini
[application]

config/name="Acme Game"
config/version="1.2.0"
config/features=PackedStringArray("4.3")
```

dispat reads `config/name` as the package's name and `config/version` as its version. Godot declares no dependencies of
its own. Addons are vendored into `addons/` and carry their own manifests, so this file feeds versioning rather than
the dependency graph.

Godot does not create `config/version` until you fill it in. If it is absent, dispat reads an empty version and leaves
the file that way. Add the key first if you want dispat to update it.

Godot 3 and Godot 4 spell these two keys the same way, so both parse correctly.

### `addons/*/plugin.cfg`

```ini
[plugin]

name="Acme Tools"
version="1.2.0"
script="plugin.gd"
```

For a repository of addons, give each addon a package folder containing its `plugin.cfg`. Each can then have its own
version and changelog.

dispat reads only the `[plugin]` section. A file of the same name belonging to something else reads as nothing rather
than as a wrong answer.

### `export_presets.cfg`

```ini
[preset.0]

name="Android"

[preset.0.options]

version/code=7
version/name="1.2.0"

[preset.1]

name="iOS"

[preset.1.options]

application/short_version="1.2.0"
application/version="1.2.0"
```

The stores show `version/name`, `application/short_version`, and `application/version`. Google Play orders builds by
the `version/code` integer.

dispat updates every preset so exports for different stores do not retain stale version strings.

You might keep this file out of version control because a preset can name a signing keystore. Its absence is normal and
is never an error.

## Build numbers for the stores

```console
$ dispat writer --set-build "$GITHUB_RUN_NUMBER" game/export_presets.cfg
```

This updates `version/code` in every preset and leaves the version strings unchanged. The counter must be an integer;
dispat rejects a version string before changing the file.

## What dispat will not splice

Godot writes some values as GDScript expressions:

```ini
config/features=PackedStringArray("4.3")
```

Values containing brackets are left unchanged because they can be array or dictionary expressions spanning several
lines. Editing part of one could make the file unreadable by the editor.

The writer also rejects replacement values containing quotes, brackets or semicolons. Those characters could close
a string, introduce a section header or comment out the rest of the line.

## Validate the addon that users install

Match the addon `plugin.cfg` version, the source tag and the packaged native-library inventory. Separate addon, demo
and source archives are different artifact promises; none by itself proves marketplace publication.

Keep Godot/ABI/platform checks in the native build scripts, and test a clean project using the distributed addon.
For Android exports, inspect every bundled 64-bit `.so` with the shared
[alignment check](./android.md#inspect-every-native-wrapper-for-16-kb-alignment), including addon and middleware
wrappers.

For a GitHub asset publisher, propagate each failed upload and reconcile completed files before retrying. A script
that starts by recreating an existing commit or tag may never reach unfinished uploads on its second invocation.

## Where to go next

- [From one package to many](./one-to-many.md) for a repository that grows past one project.
- [Unity](./unity.md) and [Unreal](./unreal.md) for the other engines.
- [Auto-versioning](../configuration/autoversion.md) for what the `autoVersion` block does in full.
