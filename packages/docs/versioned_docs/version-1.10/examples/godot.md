# Godot

A Godot project keeps its version in `project.godot`. An addon keeps its version in `plugin.cfg`. The versions the
stores see live in `export_presets.cfg`, and dispat reads and writes all three.

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

Godot does not create `config/version` until you fill it in, and a project versioned by its git tags never will. An
absent key reads as an empty version. dispat never creates one, because a project that declares no version has decided
where its version lives.

Godot 3 and Godot 4 spell these two keys the same way, so both parse correctly.

### `addons/*/plugin.cfg`

```ini
[plugin]

name="Acme Tools"
version="1.2.0"
script="plugin.gd"
```

This file is the closest thing the Godot ecosystem has to a package manifest. A repository of addons acts as a
workspace like any other. You get one package per addon, and each gets its own version and changelog.

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

dispat writes to every preset, not just the first one. A project exporting to three stores that stamped only one would
ship two stale version strings, and nothing would warn you.

You might keep this file out of version control because a preset can name a signing keystore. Its absence is normal and
is never an error.

## Build numbers for the stores

```console
$ dispat writer --set-build "$GITHUB_RUN_NUMBER" game/export_presets.cfg
```

Run this command and `version/code` moves in every preset, but every version string stays exactly where it was. Godot
parses the counter as an integer. dispat refuses a version string before opening the file, so you never write a bad
value and discover it at upload time.

## What dispat will not splice

Godot writes some values as GDScript expressions:

```ini
config/features=PackedStringArray("4.3")
```

dispat leaves a value carrying a bracket alone. Godot spreads array and dictionary literals across several lines, and a
splice inside one of those leaves a file the editor cannot load. No version or counter dispat writes has a bracket in
it, so refusing these values costs nothing.

dispat outright refuses a value that could not survive as a single value. A quote would close the literal, and a
bracket could read as a section header on the next parse. A semicolon would comment out the rest of the line.

## Where to go next

- [Games](./game.md) for a repository that grows past one project.
- [Unity](./unity.md) and [Unreal](./unreal.md) for the other engines.
- [Auto-versioning](../configuration/autoversion.md) for what the `autoVersion` block does in full.
