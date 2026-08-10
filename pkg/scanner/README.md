# scanner

A deliberately lightweight manifest reader: thin per-format parsers turning dependency manifests into one
ecosystem-neutral shape: the package's declared identity (name, version) and its declared dependencies with their
ranges, manifest fields and local-path signals. No SBOM machinery, no lockfile resolution, no network. The goal is to
support **all package managers**; the shared vocabulary (dependency kinds, the file-name rules) lives in
[`pkg/manifest`](../manifest) so this reader and [`pkg/writer`](../writer) can never drift apart. It only reads;
rewriting is the writer's job. This is the library behind
`dispat compute` (deriving a monorepo's dependency graph from its manifests) and the executor's native auto-versioning.

```go
sc := scanner.New()
mans, err := sc.Scan(ctx, "packages/web") // every manifest under the folder
roots, err := sc.ScanRoot(ctx, "packages/web") // only the folder's own manifests
```

Both methods share one error contract: a manifest that fails to parse is skipped, its error joined into the returned
error, and the parsed manifests come back either way, so callers can report the problem and keep the partial result.
Reads are capped at 16 MiB per file (`ErrManifestTooLarge`); output order is deterministic.

## Supported manifests

| File                | Ecosystem | Reads                                                                                             |
|---------------------|-----------|---------------------------------------------------------------------------------------------------|
| `package.json`      | npm       | name, version, the four dependency fields, `file:`/`link:` local paths                            |
| `go.mod`            | gomod     | module path, direct requires, relative `replace` targets as local paths                           |
| `Cargo.toml`        | cargo     | name, version, `[dependencies]`/`[dev-dependencies]`/`[build-dependencies]`, renames, `path` keys |
| `pyproject.toml`    | python    | PEP 621, PEP 735 groups, Poetry, PEP 503 name normalisation                                       |
| `requirements*.txt` | python    | PEP 508 lines, continuations, editable local installs (`-e ./pkg`)                                |
| `composer.json`     | composer  | name, version, require/require-dev (platform requirements filtered)                               |
| `pom.xml`           | maven     | `groupId:artifactId` coordinates, scopes onto dependency kinds                                    |
| `*.csproj`          | nuget     | `PackageId`/`AssemblyName`, `PackageReference`, `ProjectReference` as local paths                 |
| `pubspec.yaml`      | pub       | name, version, dependencies, `dependency_overrides` folded onto their declarations                |

The mobile platforms are covered too. Four of these declare an identity and a version but no dependencies at all — they
feed auto-versioning rather than the dependency graph — and every Java-world coordinate is spelled `group:artifact`, so a
version-catalog entry, a build script's literal notation and a `pom.xml` dependency all name the same package.

| File                   | Ecosystem | Reads                                                                                        |
|------------------------|-----------|----------------------------------------------------------------------------------------------|
| `Info.plist`           | plist     | `CFBundleIdentifier`, `CFBundleShortVersionString`, `CFBundleVersion` as the build number    |
| `project.pbxproj`      | xcode     | `PRODUCT_BUNDLE_IDENTIFIER`, `MARKETING_VERSION`, `CURRENT_PROJECT_VERSION`, first config wins |
| `Podfile`              | cocoapods | `pod` declarations, `:path` local pods, a `…Tests` target's pods onto devDependencies         |
| `*.podspec`            | cocoapods | `name`, `version`, `dependency` declarations, including subspec and platform-scoped ones      |
| `AndroidManifest.xml`  | android   | `package`, `android:versionName`, `android:versionCode` as the build number                  |
| `libs.versions.toml`   | gradle    | `[libraries]` by Maven coordinate, `version.ref` resolved through `[versions]`                |
| `build.gradle`(`.kts`) | gradle    | `applicationId`/`namespace`, `versionName`, `versionCode`, literal coordinates, `project(…)`  |

`Manifest.BuildNumber` carries the monotonic counter these formats keep beside their marketing version
(`CFBundleVersion`, `android:versionCode`, `CURRENT_PROJECT_VERSION`). It is not a semantic version, and no writer
rewrites it.

Helpers shared by the CLI's two consumers: `NameIndex` (manifest name → owning package, root manifests first, ambiguous
names reported instead of guessed) and `ResolveLocalDir` (declared local path → owning package folder).

## Not read today

The goal is full coverage of every package manager; these known gaps are listed so nobody discovers them in production:
npm `workspaces`, `overrides` and
`resolutions`; Cargo `[workspace.dependencies]`, `[workspace.members]` and target-specific tables; Maven
`${property}` interpolation, parent-POM resolution, `<dependencyManagement>` and `<modules>`; Poetry multi-constraint
dependency lists; PEP 735 `include-group`; NuGet Central Package Management (`Directory.Packages.props`). Version text
is always kept verbatim, so name matching still carries the graph where the version is indirected.

The mobile formats are read by recognising the statement shapes that declare something, so anything a single file cannot
resolve is dropped rather than guessed at — which on modern Android projects is a great deal. Not read: version-catalog
accessors (`implementation libs.retrofit`), interpolated versions (`"…:$coreVersion"` and `#{...}` in a Podfile),
`ext` properties, and a `versionName` computed from a properties file. A Gradle `project(':core')` reference is recorded
by its last path segment with **no** local path — a project path is relative to the build's root, which one build file
does not reveal, and guessing at the folder could resolve to a real but unrelated package; `settings.gradle`
`projectDir` remapping is likewise invisible. `[plugins]` and `[bundles]` catalog tables are not dependencies. Subspec
dependencies are collected but not attributed to their subspec, and `.podspec.json` is a different (JSON) grammar. Only
the legacy pre-namespacing attributes are read from an `AndroidManifest.xml`, so a modern project correctly reads empty
there and declares its versions in `build.gradle` instead. Apple build-setting references are kept verbatim where a
version is expected, matching the Maven `${property}` rule, but a `$(PRODUCT_BUNDLE_IDENTIFIER)`-shaped *identifier*
reads as empty: every project spells it identically, and `NameIndex` would report the shared literal as an ambiguous
name. `Info.plist` is matched by exact name, so the legacy `MyApp-Info.plist` spelling is not recognised. Swift Package
Manager dependencies are out of scope by construction — `Package.swift` is executable Swift, not a manifest.

## Requirements

Go 1.25 or later.

## Licence

MIT. See [LICENSE](../../LICENSE.md).
