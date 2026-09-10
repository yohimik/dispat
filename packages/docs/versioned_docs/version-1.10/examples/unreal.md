# Unreal Engine

An Unreal plugin stores its version in its `.uplugin` file. A project keeps its version in `Config/DefaultGame.ini`,
and the Android store fields live in `Config/DefaultEngine.ini`. dispat reads and writes all of these files, and it
reads the plugin graph declared by a `.uproject`.

## The short version

Treat a repository of plugins as a standard workspace. Define your packages in `dispat.json`:

```json title="dispat.json"
{
  "packages": {
    "acmenet": {"path": "Plugins/AcmeNet", "autoVersion": {"enabled": true}},
    "acmeui": {"path": "Plugins/AcmeUI", "autoVersion": {"enabled": true}}
  }
}
```

Place each `.uplugin` directly in its package folder so the default manifest scope finds it. Commit your changes and
run `dispat` to bump the versions.

```console
$ git commit -m "feat(acmenet): reliable RPC batching"
$ dispat
12:50:45 INF ● changed bump=minor package=acmenet version="1.2.0 -> 1.3.0"
12:50:45 INF manifest reconciled manifest=AcmeNet.uplugin package=acmenet ranges=0 stage=version version=1.3.0 versionWritten=true
12:50:45 INF published package=acmenet tag=acmenet@1.3.0 version=1.3.0
```

## What dispat reads

### `*.uplugin`

```json
{
  "FileVersion": 3,
  "Version": 7,
  "VersionName": "1.2.0",
  "FriendlyName": "Acme Networking",
  "Plugins": [
    { "Name": "ChaosVehiclesPlugin", "Enabled": true }
  ]
}
```

| Field | Read as | Written by |
|-------|---------|------------|
| the file's own name | the package's name | never |
| `VersionName` | the version | `--set-version`, and auto-versioning |
| `Version` | the build counter | `--set-build` |
| `Plugins[].Name` | dependencies, with no version | never |

dispat uses the file's base name as the package name instead of `FriendlyName`. Other descriptors reference the plugin
by this base name, and it matches the folder on disk. A workspace resolves this name directly.

dispat writes `Version` back as a bare integer because the engine's build tool expects that format. If a descriptor
already formats the version as a string, dispat keeps that shape. It avoids reformatting existing files.

### `*.uproject`

```json
{
  "FileVersion": 3,
  "EngineAssociation": "5.4",
  "Plugins": [
    { "Name": "AcmeNet", "Enabled": true }
  ]
}
```

A project descriptor declares no version of its own. It declares which plugins it enables, and dispat treats these as
dependency edges. Release `AcmeNet` first, then release the project that uses it, which `dispat compute` suggests
automatically.

### `Config/DefaultGame.ini` and `Config/DefaultEngine.ini`

```ini
[/Script/EngineSettings.GeneralProjectSettings]
ProjectName=Acme Game
ProjectVersion=1.2.0.0
```

```ini
[/Script/AndroidRuntimeSettings.AndroidRuntimeSettings]
StoreVersion=7
VersionDisplayName=1.2.0
```

`ProjectVersion` sets the version a packaged game reports at runtime. `VersionDisplayName` controls what the Play Store
listing shows. `StoreVersion` provides the integer Google Play uses to order uploads.

Unreal writes `ProjectVersion` with four components. dispat keeps whatever text you provide because the engine reads
that exact text. Normalising the text would cause disagreements with the engine.

Put both files inside a `Config/` folder so dispat recognises them. A `DefaultEngine.ini` anywhere else acts as
configuration, not a manifest.

## Plugins have no versions, and that is fine

An Unreal descriptor names a plugin and stops there:

```json
"Plugins": [{ "Name": "AcmeNet", "Enabled": true }]
```

The descriptor lacks version text because the engine resolves a plugin by name against the project and the engine
itself. Ask dispat to write a version range for a plugin, and it reports the file as **skipped**, not missing:

```console
$ dispat writer --set AcmeNet=1.3.0 Server.uproject
Server.uproject
1 manifest(s): 0 applied, 1 skipped, 0 missing
```

A skipped dependency exists but carries no version text for a writer to change. A missing dependency means the file
does not declare it at all, creating a disagreement that fails a `--strict` run. Every healthy Unreal descriptor stays
permanently in the skipped state, so warnings here would create noise.

A disabled plugin remains declared and still acts as a dependency edge. A build that turns the plugin back on requires
a released version.

## Build numbers for the stores

```console
$ dispat writer --set-build "$GITHUB_RUN_NUMBER" \
    Plugins/AcmeNet/AcmeNet.uplugin \
    Config/DefaultEngine.ini
```

Run this command to bump the plugin's `Version` and the Android `StoreVersion` without changing any version strings.
Both fields require integers. dispat refuses a version string before it even opens the file.

## What dispat leaves alone

`EngineAssociation` pins the engine a project builds against. This represents a toolchain choice rather than a shipped
artifact. dispat never reads or writes it, just as it ignores a Maven parent version.

`*.Build.cs` and `*.Target.cs` declare module dependencies in C#. dispat ignores them because they are source files,
not manifests.

An array operation like `+ProjectVersion=` or `.ProjectVersion=` acts as a different declaration from a plain
assignment. Unreal resolves the two differently. dispat writes the plain assignment and leaves the array operations
alone.

## The folders dispat stays out of

The `Binaries`, `Intermediate`, `Saved`, and `DerivedDataCache` folders hold generated copies of the descriptors.
dispat never descends into any of them.

## Where to go next

- Read [From one package to many](./one-to-many.md) for a repository that grows past one project.
- Check [Unity](./unity.md) and [Godot](./godot.md) for other game engines.
- Use [compute](../cli/compute.md) to turn the plugin graph into configuration.

## Test the packaged plugin

Check the native library inventory inside each engine/platform archive, then install that archive into a clean test
project. Source-tree builds can pass while the distributed ZIP lacks a library. See the
[Cesium packaging case](./release-integration.md#test-the-distributed-artifact).

For Android packages, run every bundled 64-bit `.so` through the shared
[alignment check](./android.md#inspect-every-native-wrapper-for-16-kb-alignment), including middleware and plugin
wrappers.

Treat the source tag and a precompiled plugin archive as different distribution promises. `VersionName` can match the
tag while an archive filename and `EngineVersion` limit that binary to one Unreal release. Verify those values and the
native library inventory together; source compatibility with other engine versions does not prove that a packaged
archive exists for them.

For example, [SocketIOClient-Unreal 2.11.0](https://github.com/getnamo/SocketIOClient-Unreal/releases/tag/v2.11.0)
lists a UE 5.7 archive, while [UEGitPlugin 3.16](https://github.com/ProjectBorealis/UEGitPlugin/releases/tag/3.16)
provides a source release. Validate the project's declared destination, then retain any required Unreal build,
licensing and marketplace steps in the configured scripts.

## Test the oldest supported engine

Build the packaged plugin against the oldest engine version its guide claims to support. Steam Audio 4.8.1's
[released guide](https://github.com/ValveSoftware/steam-audio/blob/v4.8.1/unreal/doc/getting-started.rst)
says Unreal Engine 4.27 or later, while [issue #573](https://github.com/ValveSoftware/steam-audio/issues/573)
reports a 4.27.2 build stopping at a missing `EditorAssetSubsystem.h`. The official plugin archive contains that
include; a build on a newer engine does not validate the stated minimum.

Keep this compatibility build in the native scripts before publishing each plugin archive. dispat can update its
version fields and order the stages; engine API compatibility still needs the actual engine/compiler check.

## A public repository to compare

[SocketIOClient Unreal plugin at `f3e63dc`](https://github.com/getnamo/SocketIOClient-Unreal/blob/f3e63dcf578095f897c977d991c3fbb9aa501b94/SocketIOClient.uplugin): The plugin declares a literal `VersionName` and a separate integer `Version`. The local check changed `VersionName` while preserving the integer. Decide the build-counter policy separately, and retain Unreal’s native plugin packaging and target validation.

See the [21-ecosystem audit](./open-source.md) for pinned inputs, reproducible checks and their limits.
