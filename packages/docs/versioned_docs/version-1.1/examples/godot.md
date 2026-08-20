# Godot

A Godot project keeps its version in `project.godot`, an addon keeps its in `plugin.cfg`, and the versions the stores
see live in `export_presets.cfg`. dispat reads and writes all three.

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

`project.godot` sits directly in the package folder, so the default manifest scope reaches it. Commit a feature, run
`dispat`, and the version in the project file, the git tag and the changelog all agree.

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

`config/name` is the package's name and `config/version` is its version. Godot declares no dependencies of its own,
addons are vendored into `addons/` and carry their own manifests, so this file feeds versioning rather than the
dependency graph.

Godot does not create `config/version` until somebody fills it in, and a project versioned by its git tags never will.
An absent key reads as an empty version, and dispat never creates one: a project that declares no version has decided
where its version lives.

Godot 3 and Godot 4 spell these two keys the same way, so both parse.

### `addons/*/plugin.cfg`

```ini
[plugin]

name="Acme Tools"
version="1.2.0"
script="plugin.gd"
```

This is the closest thing the Godot ecosystem has to a package manifest, so a repository of addons is a workspace like
any other: one package per addon, each with its own version and changelog.

Only the `[plugin]` section is read, so a file of the same name belonging to something else reads as nothing rather
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

`version/name`, `application/short_version` and `application/version` are the versions the stores show, and
`version/code` is the integer Google Play orders builds by.

Every preset is written, not the first one. A project exporting to three stores that stamped one of them would ship
two stale version strings and nothing would say so.

The file is frequently kept out of version control, because a preset can name a signing keystore. Its absence is
normal and is never an error.

## Build numbers for the stores

```console
$ dispat writer --set-build "$GITHUB_RUN_NUMBER" game/export_presets.cfg
```

`version/code` moves in every preset, and every version string stays where it was. Godot parses the counter as an
integer, so a version string is refused before the file is opened rather than written and discovered at upload time.

## What dispat will not splice

Godot writes some values as GDScript expressions:

```ini
config/features=PackedStringArray("4.3")
```

A value carrying a bracket is left alone. Godot also spreads array and dictionary literals across several lines, and a
splice inside one of those leaves a file the editor cannot load. No version or counter dispat writes has a bracket in
it, so refusing costs nothing worth having.

A value that could not survive as one value is refused outright rather than written: a quote would close the literal,
a bracket could read as a section header on the next parse, and a semicolon would comment out the rest of the line.

## Where to go next

- [Games](./game.md) for a repository that grows past one project.
- [Unity](./unity.md) and [Unreal](./unreal.md) for the other engines.
- [Auto-versioning](../configuration/autoversion.md) for what the `autoVersion` block does in full.
