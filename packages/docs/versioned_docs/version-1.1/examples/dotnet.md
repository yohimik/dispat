# .NET packages

Projects packed and pushed to NuGet, with `<Version>` and every `PackageReference` between them written before
`dotnet pack` runs.

## The layout

```
src/core/Acme.Core.csproj    Acme.Core 1.2.0
src/web/Acme.Web.csproj      Acme.Web 0.4.1, references Acme.Core
dispat.json
```

Folder names become package names, and a dot in a configuration key reads as nesting, so keep the folders plain
(`core`, not `Acme.Core`). The NuGet id is read out of the project file, so the two are already connected; if a
project declares no `PackageId`, state the id under
[`manifestNames`](../configuration/packages.md#manifestnames).

## The configuration

```json title="dispat.json"
{
  "scripts": {
    "pack": "dotnet pack -c Release",
    "push": "dotnet nuget push \"bin/Release/*.nupkg\" --api-key $NUGET_API_KEY --source https://api.nuget.org/v3/index.json"
  },
  "spaces": {
    "src": {
      "path": "src",
      "flow": {"build": "pack", "publish": "push"},
      "autoVersion": {"enabled": true, "range": "exact"}
    }
  }
}
```

`range: exact` because NuGet resolves a bare version as "this or newer" already; writing `1.3.0` says what you mean
and keeps the diff readable.

## A release

```console
$ dispat compute --write
+ add     web -> core (dependencies)  src/web/Acme.Web.csproj dependencies "Acme.Core": "1.2.0"
+ initial core 1.2.0  src/core/Acme.Core.csproj declares 1.2.0; no release tag yet
+ initial web 0.4.1  src/web/Acme.Web.csproj declares 0.4.1; no release tag yet

applied 3 change(s) to dispat.json (previous copies carry the .backup suffix)

$ git commit -m "feat(core)^: add the resilience handler"
$ dispat status
12:41:35 INF ● changed baselineFromInitials=true bump=minor channel=stable dueToProviders=[] ownCommits=1 package=core reason=direct space=src version="1.2.0 -> 1.3.0"
12:41:35 INF ● changed baselineFromInitials=true bump=patch channel=stable dependsOn=["core"] dueToProviders=["core"] ownCommits=0 package=web reason="propagated from core" space=src version="0.4.1 -> 0.4.2"
12:41:35 INF release plan ready held=0 packages=2 releasing=2
```

The version stage then writes both files, and `dotnet pack` picks the versions straight out of them:

```console
$ dispat autoversion
12:41:35 INF manifest reconciled manifest=Acme.Core.csproj package=core ranges=0 stage=version versionWritten=true
12:41:35 INF manifest reconciled manifest=Acme.Web.csproj package=web ranges=1 stage=version versionWritten=true
12:41:35 INF auto-versioning finished failed=0 ran=2 skipped=0 stage=autoversion
```

## What dispat reads and writes

```console
$ dispat scanner
src/core/Acme.Core.csproj  nuget  Acme.Core@1.2.0
src/web/Acme.Web.csproj  nuget  Acme.Web@0.4.1
  dependencies  Acme.Core  1.2.0
  dependencies  Serilog    4.0.1
2 manifest(s), 2 dependency declaration(s)
```

The name is `PackageId`, falling back to `AssemblyName` and then to the file name. A `ProjectReference` is reported as
a link to that folder, which is how `compute` finds an edge even when no version is declared anywhere. Writes go to
the first `<Version>` and to each `PackageReference`, whether the version sits in an attribute or in a child element:

```console
$ dispat writer src/web/Acme.Web.csproj --set-version 0.5.0 --set Acme.Core=1.3.0
src/web/Acme.Web.csproj
  version written
  applied  dependencies  Acme.Core  1.3.0
1 manifest(s): 1 applied, 0 skipped, 0 missing
```

A version written as an MSBuild property, `$(AcmeCoreVersion)`, is reported as skipped and left alone. Four file
names are covered in all: `*.csproj`, `*.fsproj` and `*.vbproj`, plus `*.nuspec`, `Directory.Packages.props` and
`packages.config` for the projects that keep their versions in one of those.

## Central package management

With `ManagePackageVersionsCentrally`, no project file carries a version for its references. They all live in one
`Directory.Packages.props` at the repository root, which belongs to no package, so no package's version stage will
touch it. Reconcile it once per run instead, in the [`run.beforeAll` hook](../configuration/run-hooks.md), which fires
after planning and before any build:

```json title="dispat.json (central package management)"
{
  "scripts": {
    "pin-central": "dispat writer Directory.Packages.props --set Acme.Core=$DISPAT_WORKSPACE_CORE_VERSION",
    "pack": "dotnet pack -c Release",
    "push": "dotnet nuget push \"bin/Release/*.nupkg\" --api-key $NUGET_API_KEY --source https://api.nuget.org/v3/index.json"
  },
  "run": {"beforeAll": "pin-central"},
  "spaces": {
    "src": {
      "path": "src",
      "flow": {"build": "pack", "publish": "push"},
      "autoVersion": {"enabled": true, "range": "exact"}
    }
  }
}
```

`DISPAT_WORKSPACE_CORE_VERSION` is the version the `core` package will carry at the end of the run: its planned
version when it is releasing, its current one otherwise. Every workspace package has such a variable, so the script is
one `--set` per package you publish to your own feed. The whole run, with `pack` and `push` standing in for the real
commands:

```console
$ dispat
12:43:50 INF release started root=.
12:43:50 INF ● changed baselineFromInitials=true bump=minor channel=stable dueToProviders=[] ownCommits=1 package=core reason=direct space=src version="1.2.0 -> 1.3.0"
12:43:50 INF ● changed baselineFromInitials=true bump=patch channel=stable dependsOn=["core"] dueToProviders=["core"] ownCommits=0 package=web reason="propagated from core" space=src version="0.4.1 -> 0.4.2"
12:43:50 INF release plan ready held=0 packages=2 releasing=2
12:43:50 INF Directory.Packages.props stage=beforeAll
12:43:50 INF   applied  dependencies  Acme.Core  1.3.0 stage=beforeAll
12:43:50 INF 1 manifest(s): 1 applied, 0 skipped, 0 missing stage=beforeAll
12:43:50 INF manifest reconciled manifest=Acme.Core.csproj package=core ranges=0 stage=version version=1.3.0 versionWritten=true
12:43:50 INF version succeeded package=core stage=version version=1.3.0
12:43:50 INF build started package=core stage=build version=1.3.0
12:43:50 INF packing core 1.3.0 package=core stage=build version=1.3.0
12:43:50 INF build succeeded package=core stage=build version=1.3.0
12:43:50 INF publish started package=core stage=publish version=1.3.0
12:43:50 INF pushing core 1.3.0 package=core stage=publish version=1.3.0
12:43:50 INF manifest reconciled manifest=Acme.Web.csproj package=web ranges=0 stage=version version=0.4.2 versionWritten=true
12:43:50 INF version succeeded package=web stage=version version=0.4.2
12:43:50 INF build started package=web stage=build version=0.4.2
12:43:50 INF packing web 0.4.2 package=web stage=build version=0.4.2
12:43:50 INF build succeeded package=web stage=build version=0.4.2
12:43:50 INF published package=core stage=publish tag=core@1.3.0 version=1.3.0
12:43:50 INF publish started package=web stage=publish version=0.4.2
12:43:50 INF pushing web 0.4.2 package=web stage=publish version=0.4.2
12:43:50 INF published package=web stage=publish tag=web@0.4.2 version=0.4.2
12:43:50 INF summary channel=stable package=core status=published tag=core@1.3.0 took=0.4s version="1.2.0 -> 1.3.0"
12:43:50 INF summary channel=stable package=web status=published tag=web@0.4.2 took=0.5s version="0.4.1 -> 0.4.2"
12:43:50 INF done cancelled=0 failed=0 held=0 published=2 skipped=0 took=0.6s unchanged=0
```

`web` starts building while `core` is still publishing, and waits for it before its own publish. That ordering is the
only reason the versions it just wrote resolve.

## Worth knowing

- **Assembly versions are not package versions.** `dotnet pack` derives `AssemblyVersion` and `FileVersion` from
  `Version` unless you set them, which is usually what you want. Set them explicitly if your consumers bind strictly.
- **A pushed version is permanent.** Unlisting hides a package on NuGet, it does not free the number.
- **`--skip-duplicate` makes a re-run survivable.** After a failure partway through a run, the next run may re-push a
  package that already landed; that flag turns the conflict into a no-op.
- **Nothing above needs the .NET SDK to be reasoned about.** `dispat scanner` and `dispat writer` read and edit the
  project files directly, so a CI step can check them before any restore happens.

## See also

- [Run-level hooks](../configuration/run-hooks.md) for the seam the central pin uses.
- [Script environment variables](../reference/environment.md#workspace-data) for the workspace listing.
- [Manifest tools](../editing/manifests.md) for the scanner and writer on their own.
