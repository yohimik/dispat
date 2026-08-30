# scanner: the manifest reader

`github.com/yohimik/dispat/pkg/scanner` reads dependency manifests into one ecosystem-neutral shape. You get the
package's declared identity, its declared dependencies, their ranges, and any local-path signals. It is a dependency
manifest parser for Go covering thirty-five formats across twenty ecosystems.

It only reads. Rewriting is [the writer's](./writer.md) job. This package has no SBOM machinery, no lockfile
resolution, and no network access.

The recognised formats are fixed at build time. Fence tests hold the reader and the writer to the same list.

```sh
go get github.com/yohimik/dispat/pkg/scanner
```

## Reading a folder

```go
sc := scanner.New()
mans, err := sc.Scan(ctx, "packages/web")     // every manifest under the folder
roots, err := sc.ScanRoot(ctx, "packages/web") // only the folder's own manifests

mans, err = scanner.Scan(ctx, "packages/web") // the package-level conveniences
```

Use the `Scanner` interface to substitute a fake in tests when you wire the reader and the writer together. Call the
package-level functions to do the same work without an interface.

Both entry points share an error contract worth relying on. A manifest that fails to parse is skipped, and its error
joins the returned error. The manifests that did parse come back regardless.

You can report the problem and keep the partial result rather than losing a whole scan to one malformed file. Reads are
capped at 16 MiB per file. A larger file returns `ErrManifestTooLarge`, and the output order is deterministic.

## What comes back

```go
type Manifest struct {
	Path, Name, Version, BuildNumber string
	Ecosystem                        Ecosystem
	Deps, Indirect                   []DeclaredDep
	Dropped                          []string
	Root                             bool
}
```

Three of these fields carry more meaning than their names suggest.

`BuildNumber` is the monotonic counter the mobile formats keep beside their marketing version. Examples include
`CFBundleVersion` or `android:versionCode`. It is not a semantic version, so no version write ever moves it.

`Indirect` holds requirements a manifest records as transitive bookkeeping rather than as its own declarations. Only
`go.mod` draws that distinction. Keeping the two apart lets you reconcile ranges without touching a version the
toolchain owns.

`Dropped` names entries the manifest declared but the parser could not coerce into a dependency. Each entry takes one
line. These are not errors because the manifest parsed, and you decide whether the drops are worth reporting.

## The formats it reads

| Ecosystem | Manifests |
|-----------|-----------|
| npm | `package.json` |
| Go | `go.mod` |
| Cargo | `Cargo.toml` |
| Python | `pyproject.toml`, requirements files |
| Composer | `composer.json` |
| Maven | `pom.xml` |
| NuGet | `*.csproj`, `*.fsproj`, `*.vbproj`, `*.nuspec`, `Directory.Packages.props`, `packages.config` |
| pub | `pubspec.yaml` |
| RubyGems | `Gemfile`, `*.gemspec` |
| Docker | `Dockerfile`, `Containerfile`, compose files |
| plist, Xcode | `Info.plist`, `project.pbxproj` |
| CocoaPods | `Podfile`, `*.podspec` |
| Android, Gradle | `AndroidManifest.xml`, `libs.versions.toml`, `build.gradle`, `build.gradle.kts` |
| Unity | `Packages/manifest.json`, `ProjectSettings/ProjectSettings.asset` |
| Godot | `project.godot`, `plugin.cfg`, `export_presets.cfg` |
| Unreal | `*.uproject`, `*.uplugin`, `Config/DefaultGame.ini`, `Config/DefaultEngine.ini` |
| Defold | `game.project` |
| O3DE | `project.json`, `gem.json` |

Several formats are matched by name rather than extension. A Dockerfile matches `Dockerfile`, `Dockerfile.dev`,
`api.Dockerfile`, and Podman's `Containerfile`. A requirements file matches by whole words, so `dev-requirements.txt`
counts while `old-requirements-notes.txt` reads as prose.

Docker has no version field, so identity comes from the images a file names. A compose service declaring both a `build`
section and a tagged `image` is producing that image. Failing that, the reader takes the tagged repository the most
services name.

A file that only wires third-party services together declares no identity. This is the honest answer rather than a
guess.

The exact fields each format contributes are in the
[package README](https://github.com/yohimik/dispat/blob/main/pkg/scanner/README.md). This document also lists the
shapes deliberately left unread.

## Helpers for callers building a graph

Four exported helpers do the work the CLI needs on top of a scan. `NameIndex` maps a manifest name onto its owning
package. It prefers stated names, then root manifests, then nested ones, and reports a same-rank collision instead of
guessing.

`ResolveLocalDir` turns a declared local path into the package folder it points at. `SkipDir` and `SkipWorkspaceDir`
name the folders a walk never enters. Exporting these lets you stay out of the same places when walking a package for
your own reasons.

They differ by the folders a game engine generates. `SkipDir` is the dependency trees, virtual environments, and build
output every walk avoids. A tool replacing literal text should follow `SkipDir` because a version string under `Build/`
is still a version string.

`SkipWorkspaceDir` adds `Library`, `PackageCache`, `Temp`, `Logs`, `UserSettings`, `MemoryCaptures`, `Binaries`,
`Intermediate`, `Saved`, `DerivedDataCache`, and `Builds`. This is what `Scan` follows. Unity's `Library/PackageCache`
holds a real `package.json` per resolved package, so a scan entering it would report hundreds of third-party packages
as workspace members.

## The same work from the command line

Run [`dispat scanner`](../cli/scanner.md) to use this package with a listing attached. It needs no configuration file
and no git repository:

```sh
dispat scanner packages/web              # every manifest under the folder
dispat scanner packages/web --root-only  # only the folder's own
dispat scanner --log-format json         # one JSON object per manifest
```

Run [`dispat compute`](../cli/compute.md) to use the reader for its intended purpose. This derives a monorepo's
dependency graph and starting versions from the manifests already on disk.

## Further reading

- [Manifest tools](../editing/manifests.md) is the guide to the reader and the writer together.
- The full API is on [pkg.go.dev](https://pkg.go.dev/github.com/yohimik/dispat/pkg/scanner) and the source is
  [on GitHub](https://github.com/yohimik/dispat/tree/main/pkg/scanner).
