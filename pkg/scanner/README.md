# scanner

The `scanner` package is a lightweight manifest reader. It parses dependency manifests into one ecosystem-neutral
shape: the package's declared identity (name and version) and its declared dependencies with their ranges, manifest
fields, and local-path signals. It does not resolve lockfiles, build SBOMs, or make network calls.

The parser recognizes thirty-five formats across twenty ecosystems at build time. Fence tests keep the reader and the
writer aligned on the same list. Shared rules and dependency kinds live in [`pkg/manifest`](../manifest) so this reader
and [`pkg/writer`](../writer) never drift apart. `scanner` only reads manifests; use `writer` to update them.

This package powers `dispat compute` when it derives your dependency graph, and it drives native auto-versioning.

```go
sc := scanner.New()
mans, err := sc.Scan(ctx, "packages/web") // every manifest under the folder
roots, err := sc.ScanRoot(ctx, "packages/web") // only the folder's own manifests

mans, err = scanner.Scan(ctx, "packages/web") // the package-level conveniences
roots, err = scanner.ScanRoot(ctx, "packages/web")
```

Both methods share an error contract. If a manifest fails to parse, dispat skips it, joins its error into the returned
error, and returns all successfully parsed manifests. This lets you report problems without discarding partial results.
Reads are capped at 16 MiB per file (`ErrManifestTooLarge`), and output order is always deterministic.

## Supported manifests

| File                | Ecosystem | Reads                                                                                             |
|---------------------|-----------|---------------------------------------------------------------------------------------------------|
| `package.json`      | npm       | name, version, the four dependency fields, `file:`/`link:` local paths                            |
| `go.mod`            | gomod     | module path, direct requires, indirect ones apart in `Indirect`, relative `replace` targets as local paths |
| `Cargo.toml`        | cargo     | name, version, `[dependencies]`/`[dev-dependencies]`/`[build-dependencies]`, renames, `path` keys |
| `pyproject.toml`    | python    | PEP 621, PEP 735 groups, Poetry, PEP 503 name normalisation                                       |
| requirements files  | python    | PEP 508 lines, continuations, editable local installs (`-e ./pkg`)                                |
| `composer.json`     | composer  | name, version, require/require-dev (platform requirements filtered)                               |
| `pom.xml`           | maven     | `groupId:artifactId` coordinates, scopes onto dependency kinds                                    |
| `*.csproj`          | nuget     | `PackageId`/`AssemblyName` (the file's base name when both are absent), `<Version>`, `PackageReference`, `ProjectReference` as local paths |
| `*.fsproj`/`*.vbproj` | nuget   | the same SDK-style schema, since F# and VB projects share it                                      |
| `*.nuspec`          | nuget     | `id`, `version`, dependencies flat or inside targetFramework groups                                |
| `Directory.Packages.props` | nuget | Central Package Management: every `PackageVersion` the repository pins                        |
| `packages.config`   | nuget     | the legacy list, with `developmentDependency` onto devDependencies                                 |
| `pubspec.yaml`/`.yml` | pub     | name, version (its `+` suffix as the build number), dependencies, `dependency_overrides` folded onto their declarations |
| `Gemfile`           | rubygems  | `gem` declarations, `:path` local gems, development and test groups onto devDependencies (a group is tracked by line, so a block that never closes scopes every later declaration) |
| `*.gemspec`         | rubygems  | name, version, `add_dependency`/`add_runtime_dependency`/`add_development_dependency`             |
| `Dockerfile`, `Containerfile` | docker | every `FROM`, `COPY --from` and `RUN --mount=…,from=` image; stage aliases and `scratch` excluded |
| `compose.yaml`      | docker    | the image the file builds as its identity, every other service's image as a dependency            |

dispat matches a Dockerfile by name rather than extension. `Dockerfile`, `Dockerfile.dev`, `api.Dockerfile`, and
Podman's `Containerfile` all match. Compose files match by name as well: `compose.yaml`, `compose.yml`,
`docker-compose.yaml`, `docker-compose.yml`, and their `.override.` variants.

Requirements files match whole words rather than globs. Any `.txt` file whose base name starts or ends with
`requirements` counts. This puts `dev-requirements.txt` and `requirements-test.txt` into devDependencies, keeps
`requirements-latest.txt` as a runtime manifest, and ignores files like `old-requirements-notes.txt`.

Docker files have no version field, so dispat derives their identity from image names. In a Compose file, dispat first
looks for a service that declares both a `build` block and a tagged `image`. If none exists, it uses the tagged
repository named by the most services, breaking ties with the alphabetically lowest service name. A Compose file that
only wires third-party images together declares no identity, and Dockerfiles never declare an identity because their
target tag is set on the command line.

Because Docker manifests declare full image repositories like `ghcr.io/acme/api`, you can map them to packages using
`manifestNames` or rely on dispat's last-segment name matching.

Mobile platforms are supported as well. Three mobile formats declare an identity and a version without dependencies, so
they drive auto-versioning rather than the dependency graph. Every Java coordinate uses the `group:artifact` format,
aligning version catalogs, build script literals, and `pom.xml` dependencies to the same package name.

| File                   | Ecosystem | Reads                                                                                        |
|------------------------|-----------|----------------------------------------------------------------------------------------------|
| `Info.plist`           | plist     | `CFBundleIdentifier`, `CFBundleShortVersionString`, `CFBundleVersion` as the build number    |
| `project.pbxproj`      | xcode     | `PRODUCT_BUNDLE_IDENTIFIER`, `MARKETING_VERSION`, `CURRENT_PROJECT_VERSION`, first config wins |
| `Podfile`              | cocoapods | `pod` declarations, `:path` local pods, a `…Tests` target's pods onto devDependencies         |
| `*.podspec`            | cocoapods | `name`, `version`, `dependency` declarations, including subspec and platform-scoped ones      |
| `AndroidManifest.xml`  | android   | `package`, `android:versionName`, `android:versionCode` as the build number                  |
| `libs.versions.toml`   | gradle    | `[libraries]` by Maven coordinate, `version.ref` resolved through `[versions]`                |
| `build.gradle`(`.kts`) | gradle    | `applicationId`/`namespace`, `versionName`, `versionCode`, literal coordinates, `project(…)`  |

Game engines are also supported under the engine's name because the engine resolves its own manifests. Several formats
store versions in non-standard files or declare identities without dependencies, feeding auto-versioning instead of the
dependency graph.

| File                                    | Ecosystem | Reads                                                                                    |
|-----------------------------------------|-----------|--------------------------------------------------------------------------------------------|
| `Packages/manifest.json`                | unity     | the flat dependency map, registry versions, `file:` folders as local paths; no identity of its own |
| `ProjectSettings/ProjectSettings.asset` | unity     | `productName`, `bundleVersion`, `AndroidBundleVersionCode` and `buildNumber` as the build number |
| `project.godot`                         | godot     | the `[application]` name and `config/version`, which an unversioned project leaves empty  |
| `plugin.cfg`                            | godot     | one addon's `[plugin]` name and version                                                  |
| `export_presets.cfg`                    | godot     | the first preset's name, its store version, and `version/code` as the build number       |
| `*.uproject`                            | unreal    | the file's base name as the identity and every enabled plugin as a dependency, never a version |
| `*.uplugin`                             | unreal    | the same, plus `VersionName` and `Version` as the build number                           |
| `Config/DefaultGame.ini`                | unreal    | `ProjectName` and `ProjectVersion`                                                        |
| `Config/DefaultEngine.ini`              | unreal    | the Android `VersionDisplayName`, and `StoreVersion` as the build number                  |
| `game.project`                          | defold    | `title` and `version`; a library is an archive URL, so no dependencies are read           |
| `project.json`/`gem.json`               | o3de      | the project or gem name, `version`, and the `Gem==1.0.0` dependency specifiers            |

`Manifest.BuildNumber` holds the monotonic build counter stored alongside marketing versions, such as
`CFBundleVersion`, `android:versionCode`, `CURRENT_PROJECT_VERSION`, Gradle's `versionCode`, a pubspec `+` suffix,
Unity's `buildNumber`, or a `.uplugin` `Version`. Because this is not a semantic version, normal version bumps do not
touch it. Only the writer's `SetBuild` method updates this value.

`Manifest.Dropped` lists entries that the parser could not convert into dependencies, formatted with one line per entry
(such as `service db: not a mapping`). These are not parse errors, and your calling code decides whether to log or
ignore them. Unread syntax that dispat ignores by design does not appear here; see [Not read today](#not-read-today)
for those rules.

`Manifest.Indirect` holds transitive bookkeeping requirements rather than direct package declarations. Only `go.mod`
uses this field. A dependency in `Deps` never appears in `Indirect`, though an indirect require pinned locally by a
relative `replace` directive counts as a declaration and stays in `Deps`. Separating them allows you to reconcile
version ranges without touching toolchain versions while still redirecting transitive modules in the root Go module.

The scanner exposes shared helper functions:
- `NameIndex`: maps manifest names to owning packages, prioritizing explicit names, then root manifests, then nested
  files, and reporting same-rank collisions.
- `ResolveLocalDir`: resolves a declared local path to its owning package folder.
- `SkipDir`: lists dependency directories, virtual environments, build outputs, and hidden folders to ignore when
  walking trees.
- `SkipWorkspaceDir`: includes everything in `SkipDir` plus engine-generated folders, matching the walk rules in
  `Scan`.

Use `Owner.Names` to register packages that lack readable manifest identities, such as Makefile projects or certain
Gradle modules. Stated names in `Owner.Names` take precedence over any identity read from disk.

## From the command line

Run `dispat scanner [folder]` to inspect parsed manifests directly from your terminal. It runs without a git repository
or a dispat configuration file:

```sh
dispat scanner packages/web              # every manifest under the folder
dispat scanner packages/web --root-only  # only the folder's own
dispat scanner --log-format json         # one JSON object per manifest
dispat scanner --strict                  # exit 1 if any manifest failed to parse
```

The full guide is [Manifest tools](https://yohimik.github.io/dispat/editing/manifests/).

## Not read today

These limitations are documented so you do not encounter unexpected behavior in production.

dispat does not read npm `workspaces`, `overrides`, or `resolutions`; Cargo `[workspace.dependencies]`,
`[workspace.members]`, or target-specific tables; Maven `${property}` interpolation, parent-POM resolution,
`<dependencyManagement>`, or `<modules>`; Poetry multi-constraint dependency lists; PEP 735 `include-group`;
`Directory.Build.props` and NuGet lock files; or Bundler `gems.rb` and `Gemfile.lock`.

A `.nuspec` generated from a project is a template. NuGet resolves `$id$` and `$version$` tokens during packaging, so
dispat preserves a token version as written and parses a token identifier as empty. Xcode
`$(PRODUCT_BUNDLE_IDENTIFIER)` values behave the same way.

Version strings are preserved exactly as written, allowing name matching to build the graph even when versions are
indirect. If a gemspec assigns its version from a constant like `Acme::VERSION`, dispat reads no version because the
value lives in a Ruby source file.

Mobile manifests are read by matching declared statement structures. Anything that cannot be resolved within a single
file is dropped rather than guessed.

dispat does not read Gradle version-catalog accessors (`implementation libs.retrofit`), interpolated version strings
(`"…:$coreVersion"` or `#{...}` in Podfiles), `ext` properties, or `versionName` values loaded from property files.

A Gradle `project(':core')` reference is recorded using its last path segment with **no** local path. Project paths are
relative to the root build file, which cannot be determined from a single submodule file. For the same reason,
`settings.gradle` `projectDir` remaps are not tracked.

Catalog `[plugins]` and `[bundles]` tables are not parsed as dependencies. Podspec subspec dependencies are parsed
together without subspec attribution, and `.podspec.json` is not supported. `AndroidManifest.xml` only parses legacy
pre-namespacing attributes, so modern projects will read empty and should declare their versions in `build.gradle`.

Apple build-setting references remain unexpanded when parsed as versions, matching Maven `${property}` handling. A
`$(PRODUCT_BUNDLE_IDENTIFIER)`-shaped *identifier* reads as empty to prevent `NameIndex` from colliding on identical
literal strings across projects. `Info.plist` files require an exact name match, so names like `MyApp-Info.plist` are
ignored.

### Whole ecosystems not read

Some package managers have no reader implemented. Each item below notes the parsing model required:

- Swift Package Manager: `Package.swift` is executable Swift code rather than static data.
- Helm: `Chart.yaml` is standard YAML containing `version`, `appVersion`, and `dependencies` fields.
- Elixir: `mix.exs` is executable Elixir code that requires statement-shape pattern matching.
- Deno: `deno.json` is standard JSON with versioned specifiers in its imports map.
- Conan: `conanfile.txt` is a flat text list, while `conanfile.py` is executable Python code.

## Requirements

Go 1.25 or later.

## Licence

MIT. See [LICENSE](./LICENSE).
