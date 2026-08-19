# Unreal Engine

An Unreal plugin keeps its version in its `.uplugin`, a project keeps its in `Config/DefaultGame.ini`, and the Android
store fields live in `Config/DefaultEngine.ini`. dispat reads and writes all of them, and reads the plugin graph a
`.uproject` declares.

## The short version

A repository of plugins is a workspace like any other:

```json title="dispat.json"
{
  "packages": {
    "acmenet": {"path": "Plugins/AcmeNet", "autoVersion": {"enabled": true}},
    "acmeui": {"path": "Plugins/AcmeUI", "autoVersion": {"enabled": true}}
  }
}
```

Each `.uplugin` sits directly in its package folder, so the default manifest scope reaches it.

```console
$ git commit -m "feat(acmenet): reliable RPC batching"
$ dispat
12:50:45 INF ● changed bump=minor package=acmenet version="1.2.0 -> 1.3.0"
12:50:45 INF manifest updated manifest=AcmeNet.uplugin package=acmenet versionWritten=true
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

The name is the file's own base name rather than `FriendlyName`, because that is what other descriptors reference the
plugin by and what the folder on disk is called. It is the name a workspace can actually resolve.

`Version` is written back as a bare integer, because that is what the engine's build tool expects. Where a descriptor
already spells it as a string, dispat keeps that shape rather than reformatting somebody else's file on the way past.

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

A project descriptor declares no version of its own. What it declares is which plugins it enables, and those are
dependency edges: release `AcmeNet` first, then the project that uses it. `dispat compute` will suggest exactly that.

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

`ProjectVersion` is the version a packaged game reports at runtime, `VersionDisplayName` is what the Play Store
listing shows, and `StoreVersion` is the integer Google Play orders uploads by.

Unreal writes `ProjectVersion` with four components. dispat keeps whatever text you give it, because that text is what
the engine reads, and normalising it here would make the two disagree.

Both files are recognised only inside a `Config/` folder. A `DefaultEngine.ini` anywhere else is configuration, not a
manifest.

## Plugins have no versions, and that is fine

An Unreal descriptor names a plugin and stops there:

```json
"Plugins": [{ "Name": "AcmeNet", "Enabled": true }]
```

There is no version text, because the engine resolves a plugin by name against the project and the engine itself. So
asking dispat to write a range for one reports it **skipped**, not missing:

```console
$ dispat writer --set AcmeNet=1.3.0 Server.uproject
Server.uproject
1 manifest(s): 0 applied, 1 skipped, 0 missing
```

Skipped means the dependency is there and carries nothing a writer could change. Missing would mean the file does not
declare it at all, which is a real disagreement between you and the file, and only that fails `--strict`. Every
healthy Unreal descriptor is permanently in the skipped state, so warning about it would be noise.

A disabled plugin is still declared, and still an edge. A build that turns it back on needs it released.

## Build numbers for the stores

```console
$ dispat writer --set-build "$GITHUB_RUN_NUMBER" \
    Plugins/AcmeNet/AcmeNet.uplugin \
    Config/DefaultEngine.ini
```

The plugin's `Version` and the Android `StoreVersion` both move, and no version string does. Both are integers to the
tool that reads them, so a version string is refused before the file is opened.

## What dispat leaves alone

`EngineAssociation` pins the engine a project builds against. That is a toolchain choice rather than something the
project ships, so dispat never reads or writes it, on the same reasoning that leaves a Maven parent version alone.

`*.Build.cs` and `*.Target.cs` declare module dependencies in C#. They are source files, not manifests.

An array operation (`+ProjectVersion=`, `.ProjectVersion=`) is a different declaration from a plain assignment, and
Unreal resolves the two differently. dispat writes the plain one and leaves the operations alone.

## The folders dispat stays out of

`Binaries`, `Intermediate`, `Saved` and `DerivedDataCache` hold generated copies of the descriptors beside them.
dispat never descends into any of them.

## Where to go next

- [Games](./game.md) for a repository that grows past one project.
- [Unity](./unity.md) and [Godot](./godot.md) for the other engines.
- [compute](../cli/compute.md) for turning the plugin graph into config.
