# scanner

A deliberately lightweight manifest reader: thin per-format parsers turning dependency manifests into one
ecosystem-neutral shape: the package's declared identity (name, version) and its declared dependencies with their
ranges, manifest fields and local-path signals. No SBOM machinery, no lockfile resolution, no network. The recognised
formats are fixed at build time, twenty-three of them across fifteen ecosystems, and fence tests hold the reader and
the writer to the same list; the shared vocabulary (dependency kinds, the file-name rules) lives in
[`pkg/manifest`](../manifest) so this reader and [`pkg/writer`](../writer) can never drift apart. It only reads;
rewriting is the writer's job. This is the library behind
`dispat compute` (deriving a monorepo's dependency graph from its manifests) and the executor's native auto-versioning.

```go
sc := scanner.New()
mans, err := sc.Scan(ctx, "packages/web") // every manifest under the folder
roots, err := sc.ScanRoot(ctx, "packages/web") // only the folder's own manifests

mans, err = scanner.Scan(ctx, "packages/web") // the package-level conveniences
roots, err = scanner.ScanRoot(ctx, "packages/web")
```

Both methods share one error contract: a manifest that fails to parse is skipped, its error joined into the returned
error, and the parsed manifests come back either way, so callers can report the problem and keep the partial result.
Reads are capped at 16 MiB per file (`ErrManifestTooLarge`); output order is deterministic.

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

A Dockerfile is matched by name rather than by extension, so `Dockerfile`, `Dockerfile.dev`, `api.Dockerfile` and
Podman's `Containerfile` all count. A compose file is matched by name too: `compose.yaml`, `compose.yml`,
`docker-compose.yaml`, `docker-compose.yml` and the `.override.` variant of each, which is the set the Compose
specification itself loads. A requirements file is matched by whole words rather than a glob: a `.txt` whose base name
starts or ends with the word `requirements` counts, so `dev-requirements.txt` and `requirements-test.txt` land in
devDependencies while `requirements-latest.txt` stays a plain requirements file and `old-requirements-notes.txt` is
prose, not a manifest.

Docker has no version field, so the reader takes the identity from the images the file names. The rule, in order: the
service that declares both a `build` section and a tagged `image` is producing that image here, which is as close to
"this is my package" as compose gets; failing that, the tagged repository the most services name. Ties go to the lowest
service name, because a YAML mapping decodes in no order worth trusting and the answer has to come from the data. A
compose file that only wires third-party services together declares no identity at all, which is the honest answer
rather than a guess. A Dockerfile never declares one: what it builds is named on the command line, not in the file.

The name a Docker manifest declares is an image repository (`ghcr.io/acme/api`, not `api`), so a package usually
either states `manifestNames` or leans on the substring name matching, whose last-segment rule maps the two onto each
other.

The mobile platforms are covered too. Four of these declare an identity and a version but no dependencies at all, so
they feed auto-versioning rather than the dependency graph. Every Java-world coordinate is spelled `group:artifact`,
which means a version-catalog entry, a build script's literal notation and a `pom.xml` dependency all name the same
package.

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
(`CFBundleVersion`, `android:versionCode`, `CURRENT_PROJECT_VERSION`, Gradle's `versionCode`, a pubspec version's `+`
suffix). It is not a semantic version, so no version write ever moves it; the writer's `SetBuild` is the one entry
point that does.

`Manifest.Dropped` names the entries a manifest declared but the parser could not coerce into a dependency, one line
each (`service db: not a mapping`). They are not errors: the manifest parsed, and the caller decides whether the drops
are worth reporting. The shapes a format reads selectively by design are not dropped entries; those are listed under
[Not read today](#not-read-today).

`Manifest.Indirect` carries the requirements a manifest records as transitive bookkeeping rather than as its own
declarations. Only `go.mod` has the distinction and only its parser fills the field; a requirement in `Deps` never
appears there as well, and an indirect require that a relative `replace` pins locally counts as a declaration and stays
in `Deps`. Keeping the two apart is what lets a caller reconcile ranges without touching a version the toolchain owns,
while still being able to redirect a module reached only transitively, which a Go build needs, since it honours
`replace` in the main module alone.

Helpers shared by the CLI's two consumers: `NameIndex` (manifest name → owning package, stated names first, then root
manifests, then nested ones, with a same-rank collision reported instead of guessed), `ResolveLocalDir` (declared local
path → owning package folder) and `SkipDir` (the folder names a workspace walk never enters, exported so a caller
walking a package for other reasons stays out of the same places).

`Owner.Names` is how a package with no readable identity joins the index: a Gradle module or a Makefile project
declares nothing a parser here can read, so the caller states the names it answers to and they outrank anything a file
declares.

## From the command line

`dispat scanner [folder]` is this package with a listing attached, and it needs no dispat config file and no git
repository:

```sh
dispat scanner packages/web              # every manifest under the folder
dispat scanner packages/web --root-only  # only the folder's own
dispat scanner --log-format json         # one JSON object per manifest
dispat scanner --strict                  # exit 1 if any manifest failed to parse
```

The full guide is [Manifest tools](https://yohimik.github.io/dispat/editing/manifests).

## Not read today

These gaps are written down so nobody meets them for the first time in production.

Not read: npm `workspaces`, `overrides` and `resolutions`; Cargo `[workspace.dependencies]`, `[workspace.members]` and
target-specific tables; Maven `${property}` interpolation, parent-POM resolution, `<dependencyManagement>` and
`<modules>`; Poetry multi-constraint dependency lists; PEP 735 `include-group`; `Directory.Build.props` and NuGet lock
files; Bundler's alternative `gems.rb` spelling and its `Gemfile.lock`.

A `.nuspec` packed from a project is a template. NuGet fills in its `$id$` and `$version$` tokens at pack time, so a
token version is kept as written and a token identifier reads as empty. An Xcode `$(PRODUCT_BUNDLE_IDENTIFIER)` is
treated the same way.

Version text is always kept as written, so name matching still carries the graph when the version is indirected. The
`Acme::VERSION` constant nearly every gemspec assigns its version from reads as no version at all, because the number
lives in a Ruby source file rather than the manifest.

The mobile formats are read by recognising the statement shapes that declare something. Anything a single file cannot
resolve is dropped instead of guessed at, and on a modern Android project that is a great deal.

Not read: version-catalog accessors (`implementation libs.retrofit`), interpolated versions (`"…:$coreVersion"`, and
`#{...}` in a Podfile), `ext` properties, and a `versionName` computed from a properties file.

A Gradle `project(':core')` reference is recorded by its last path segment with **no** local path. A project path is
relative to the build's root, which one build file does not reveal, so guessing at the folder could land on a real but
unrelated package. `settings.gradle` `projectDir` remapping is invisible for the same reason.

`[plugins]` and `[bundles]` catalog tables are not dependencies. Subspec dependencies are collected but not attributed
to their subspec, and `.podspec.json` is a different grammar in JSON. Only the legacy pre-namespacing attributes are
read from an `AndroidManifest.xml`, so a modern project correctly reads empty there and declares its versions in
`build.gradle` instead.

Apple build-setting references are kept as written where a version is expected, matching the Maven `${property}` rule.
A `$(PRODUCT_BUNDLE_IDENTIFIER)`-shaped *identifier* reads as empty instead: every project spells it identically, and
`NameIndex` would report the shared literal as an ambiguous name. `Info.plist` is matched by exact name, so the legacy
`MyApp-Info.plist` spelling is not recognised.

### Whole ecosystems not read

Some package managers have no reader here at all. Each entry states what reading it would take, so the cost of closing
a gap is known before anyone starts.

- Swift Package Manager: `Package.swift` is executable Swift rather than a manifest, the same objection that keeps
  the statement-shape readers above conservative.
- Helm: `Chart.yaml` is plain YAML with a `version`, an `appVersion` and a `dependencies` list, the cheapest gap in
  this list to close.
- Elixir: `mix.exs` is executable Elixir, readable only the way the Ruby files are read, by recognising the statement
  shapes that declare something.
- Deno: `deno.json` is plain JSON whose imports map carries versioned specifiers.
- Conan: `conanfile.txt` is a flat list; `conanfile.py` is executable Python and shares Swift PM's objection.

## Requirements

Go 1.25 or later.

## Licence

MIT. See [LICENSE](../../LICENSE.md).
