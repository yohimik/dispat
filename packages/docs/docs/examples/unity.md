# Unity

A Unity release uses the project's existing build method and store uploader. dispat updates the version in
`ProjectSettings/ProjectSettings.asset` and reads dependencies from `Packages/manifest.json`. The example below
connects a Unity build to an itch.io upload.

## The short version

```json title="dispat.json"
{
  "scripts": {
    "build": "Unity -quit -batchmode -projectPath . -executeMethod Builder.Build",
    "publish": "butler push Build acme/game:windows --userversion $DISPAT_NEW_VERSION"
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

The scripts run inside `game`, so `.` is the Unity project and `Build` is its output directory. Configure
`Builder.Build` to write the upload there.

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

Unity adds a tag directive and class ID to the YAML settings file:

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

dispat reads this format line by line rather than relying on a generic YAML parser. It reads settings one level deep
so a nested mapping is not mistaken for the project's version.

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

The scanner retains all three forms. The `file:` entry identifies the linked folder, which lets dispat connect the
embedded package to the workspace graph. Use propagation when a release of `com.acme.core` should also release the
project that consumes it.

The manifest declares no name and no version of its own. It lists what the project consumes.

## Build numbers for the stores

Google Play and the App Store order uploads by an integer counter. They ignore the version players see. Run
`--set-build` to write that counter and nothing else:

```console
$ dispat writer --set-build "$GITHUB_RUN_NUMBER" game/ProjectSettings/ProjectSettings.asset
```

The command updates every supported counter in the file. If you export several platforms from one settings file,
check all their counters before uploading.

Pass an integer. A version string is rejected before the file is changed.

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

`Library/PackageCache` contains manifests for resolved third-party packages. To avoid treating generated or cached
files as workspace packages, dispat skips `Library/`, `Temp`, `Logs`, `UserSettings`, `MemoryCaptures` and `Builds`.

Rename any source folder called `Library` or `Builds`. dispat will not find manifests inside them.

## Validate every distribution format

A UPM package, a Unity package export, and a NuGet package can carry the same source but have different publication
steps. Keep the native UPM manifest, test each artifact as a consumer would, and reconcile successful destinations
before retrying. See [integration findings](./release-integration.md).

Check identity at all three boundaries: the `package.json` version, the release tag, and the version embedded in each
download name or registry record. A project may publish one source version as a Git-installable UPM package, a
`.unitypackage`, and a NuGet package. Success at one destination says nothing about the others, so reconcile each
publication before a retry and install each fresh artifact in a clean consumer project. A NuGet integration tool does
not necessarily publish itself to NuGet; follow the declared distribution policy.

Android exports can include native libraries from packages, plugins and middleware. Apply the engine-independent
[Android alignment check](./android.md#inspect-every-native-wrapper-for-16-kb-alignment) to every 64-bit `.so` in the
final artifact, including wrappers. Keep ELF, APK ZIP and device checks separate.

## Where to go next

- Read [From one package to many](./one-to-many.md) when your repository grows past one project.
- See [Godot](./godot.md) and [Unreal](./unreal.md) for the other engines.
- Read [Auto-versioning](../configuration/autoversion.md) to see what `manifests` and `range` do in full.
