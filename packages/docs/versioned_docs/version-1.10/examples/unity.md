# Unity

A Unity project keeps its version in `ProjectSettings/ProjectSettings.asset` and its dependencies in
`Packages/manifest.json`. dispat reads and writes both. A Unity game releases the same way an npm package does: one
command, one version, one tag.

## The short version

```json title="dispat.json"
{
  "scripts": {
    "build": "Unity -quit -batchmode -projectPath game -executeMethod Builder.Build",
    "publish": "butler push game/Build acme/game:windows --userversion $DISPAT_NEW_VERSION"
  },
  "packages": {
    "game": {
      "path": "game",
      "flow": {"build": "build", "publish": "publish"},
      "autoVersion": {"enabled": true, "manifests": "all"}
    }
  },
  "initials": {"game": "0.1.0"}
}
```

Use `manifests: all` to reach `ProjectSettings/ProjectSettings.asset`. The file sits one folder down from the package
rather than directly in it. Commit a feature and run `dispat`. The settings file, the git tag, and the changelog will
all show the same version.

```console
$ git commit -m "feat(game): co-op lobby"
$ dispat
12:50:45 INF ● changed bump=minor package=game version="0.1.0 -> 0.2.0"
12:50:45 INF manifest reconciled manifest=ProjectSettings/ProjectSettings.asset package=game ranges=0 stage=version version=0.2.0 versionWritten=true
12:50:45 INF published package=game tag=game@0.2.0 version=0.2.0
```

## What dispat reads

### `ProjectSettings/ProjectSettings.asset`

| Field | Read as | Written by |
|-------|---------|------------|
| `productName` | the package's name | never |
| `bundleVersion` | the version | `--set-version`, and auto-versioning |
| `AndroidBundleVersionCode` | the build counter | `--set-build` |
| `buildNumber` (per platform) | the build counter, when there is no Android one | `--set-build` |

The file is YAML, but standard libraries cannot parse it. Unity writes its own tag directive and tags the document with
a class id.

```yaml
%YAML 1.1
%TAG !u! tag:unity3d.com,2011:
--- !u!129 &1
PlayerSettings:
  productName: Acme Game
  bundleVersion: 1.2.0
  buildNumber:
    Standalone: 7
    iPhone: 7
  AndroidBundleVersionCode: 7
```

A conforming YAML parser refuses `!u!129`. dispat reads the file by line instead, so it can parse any real Unity
project. It only reads settings one level deep, so nested mappings cannot leak a version into the answer.

### `Packages/manifest.json`

Every entry is a dependency. dispat accepts all three forms Unity uses:

```json
{
  "dependencies": {
    "com.unity.textmeshpro": "3.0.6",
    "com.acme.core": "file:../../packages/core",
    "com.acme.tools": "https://github.com/acme/tools.git#v1.2.3"
  }
}
```

dispat keeps all three exactly as written. The `file:` form tells dispat which folder it points at. This makes an
embedded package part of the workspace graph. Release `com.acme.core`, and the project consuming it releases next.

The manifest declares no name and no version of its own. It lists what the project consumes.

## Build numbers for the stores

Google Play and the App Store order uploads by an integer counter. They ignore the version players see. Run
`--set-build` to write that counter and nothing else:

```console
$ dispat writer --set-build "$GITHUB_RUN_NUMBER" game/ProjectSettings/ProjectSettings.asset
```

Every counter in the file moves. A project shipping to Steam, the App Store, and Google Play from one settings file has
three counters. Stamping only one of them would upload two builds with the wrong store order.

Pass an integer for the counter. The stores require it, so dispat enforces it. Passing a version string fails before
the file opens, saving you from an upload error later.

A version write never touches a counter. A build write never touches the version. They move for different reasons, so
you use separate commands for each.

## Unity packages

A UPM package is an npm package. It uses the same `package.json`, the same fields, and the same registry protocol.
dispat reads it with its npm reader. A repository of Unity packages needs no special setup.

Set the range policy to exact. UPM resolves an exact version and nothing else. A caret range leaves a project that
fails to open:

```json title="dispat.json"
{
  "packages": {
    "core": {
      "path": "packages/core",
      "autoVersion": {"enabled": true, "range": "exact"}
    }
  }
}
```

Ranges written into `Packages/manifest.json` are always pinned. dispat ignores the policy here to keep the project
working.

## What dispat leaves alone

`ProjectSettings/ProjectVersion.txt` pins the editor version. This is a toolchain choice. dispat never reads or writes
it.

`Packages/packages-lock.json` is a lock file. dispat does not write lock files in any ecosystem. Run the tool that owns
yours in the `syncLock` hook.

## The folders dispat stays out of

`Library/` is Unity's `node_modules`. Its `PackageCache` holds a real `package.json` for every resolved package, which
would flood your workspace with third-party packages if scanned. dispat never descends into `Library/`, `Temp`, `Logs`,
`UserSettings`, `MemoryCaptures`, or `Builds`.

Rename any source folder called `Library` or `Builds`. dispat will not find manifests inside them.

## Where to go next

- Read [Games](./game.md) when your repository grows past one project.
- See [Godot](./godot.md) and [Unreal](./unreal.md) for the other engines.
- Read [Auto-versioning](../configuration/autoversion.md) to see what `manifests` and `range` do in full.
