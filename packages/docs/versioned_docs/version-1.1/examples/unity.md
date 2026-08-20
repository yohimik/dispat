# Unity

A Unity project keeps its version in `ProjectSettings/ProjectSettings.asset` and its dependencies in
`Packages/manifest.json`. dispat reads and writes both, so a Unity game releases the same way an npm package does: one
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

`manifests: all` is what reaches `ProjectSettings/ProjectSettings.asset`, because it sits one folder down from the
package rather than directly in it. Nothing else is needed. Commit a feature, run `dispat`, and the version in the
settings file, the git tag and the changelog all say the same thing.

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

The file is YAML, but not YAML any library will parse: Unity writes its own tag directive and tags the document with a
class id.

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

A conforming YAML parser refuses `!u!129`, which would mean refusing every real Unity project, so dispat reads the
file by line instead. Only the settings one level in are read, so the nested mappings below them cannot leak a version
into the answer.

### `Packages/manifest.json`

Every entry is a dependency, in whichever of the three forms Unity accepts:

```json
{
  "dependencies": {
    "com.unity.textmeshpro": "3.0.6",
    "com.acme.core": "file:../../packages/core",
    "com.acme.tools": "https://github.com/acme/tools.git#v1.2.3"
  }
}
```

All three are kept exactly as written. The `file:` form also tells dispat the folder it points at, which is what makes
an embedded package part of the workspace graph: release `com.acme.core` and the project consuming it releases after.

The manifest declares no name and no version of its own. It says what the project consumes, not what it is.

## Build numbers for the stores

Google Play and the App Store order uploads by an integer counter, not by the version players see. `--set-build`
writes that counter and nothing else:

```console
$ dispat writer --set-build "$GITHUB_RUN_NUMBER" game/ProjectSettings/ProjectSettings.asset
```

Every counter in the file moves, not the first one. A project shipping to Steam, the App Store and Google Play from
one settings file has three, and stamping one of them would upload two builds the stores order wrongly.

The counter must be an integer, because that is what the stores parse. A version string is refused before the file is
opened rather than written and discovered at upload time.

A version write never touches a counter, and a build write never touches the version. They move for different reasons,
so they are separate commands.

## Unity packages

A UPM package is an npm package: same `package.json`, same fields, same registry protocol. dispat reads it with its
npm reader, so a repository of Unity packages needs nothing special.

One thing is worth setting. UPM resolves an exact version and nothing else, so a caret range would leave a project
that will not open:

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

Ranges written into `Packages/manifest.json` are always pinned, whatever the policy says, for the same reason.

## What dispat leaves alone

`ProjectSettings/ProjectVersion.txt` pins the editor version. That is a toolchain choice rather than something the
project ships, so dispat never reads or writes it.

`Packages/packages-lock.json` is a lock file. dispat does not write lock files in any ecosystem; the `syncLock` hook
is where you run the tool that owns yours.

## The folders dispat stays out of

`Library/` is Unity's `node_modules`. Its `PackageCache` holds a real `package.json` for every resolved package, and a
scan that read them would report a few hundred third-party packages as members of your workspace. dispat never
descends into it, nor into `Temp`, `Logs`, `UserSettings`, `MemoryCaptures` or `Builds`.

If your repository has a source folder named `Library` or `Builds`, move it or rename it: manifests inside will not be
found.

## Where to go next

- [Games](./game.md) for a repository that grows past one project.
- [Godot](./godot.md) and [Unreal](./unreal.md) for the other engines.
- [Auto-versioning](../configuration/autoversion.md) for what `manifests` and `range` do in full.
