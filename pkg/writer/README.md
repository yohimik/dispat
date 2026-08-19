# writer

A deliberately lightweight manifest writer: format-preserving in-place edits where only the version text being changed
is replaced, and every other byte of the file (indentation, key order, comments) survives verbatim. The writing
counterpart of [`pkg/scanner`](../scanner), sharing its vocabulary through
[`pkg/manifest`](../manifest), and the library behind dispat's native auto-versioning. Every format the scanner reads
has a writer here, fenced by tests; each rewrite is byte-precise or it does not ship.

Every format writer reads and writes through one internal splicer, so the read cap, the splice, the proof that the
result still parses and the atomic write happen in one place for all of them. `Replace` is that same machinery with
the format knowledge taken away, for the versions no manifest holds.

```go
res, err := writer.Rewrite("packages/web/package.json", "1.3.0", []writer.Edit{
{Name: "@acme/core", Kind: "dependencies", Range: "^1.3.0"},
})
// res.Applied, res.Skipped, res.Missing, res.VersionWritten, res.Path

w := writer.New() // the same entry points behind one Writer value,
                  // mirroring scanner.Scanner, so a caller wiring the two
                  // halves together can fake the writes in tests
res, err = w.Rewrite("packages/web/package.json", "1.3.0", nil)
```

Writes are atomic, through a same-folder temp file, an fsync and a rename, and are skipped entirely when nothing
changed. Reads are capped at 16 MiB (`ErrManifestTooLarge`), and an unsupported path gives `ErrUnsupportedManifest`.

The result separates three outcomes. `Applied` holds the edits that changed the file. `Skipped` holds the ones whose
dependency is declared but whose version cannot be written, which is the normal state of a healthy manifest. `Missing`
holds the ones the manifest does not declare at all, which usually means the caller and the file disagree.

## Supported manifests

| File                                    | Rewrites dependency ranges                                 | Rewrites the own `version` field       | Manages a local redirect     |
|-----------------------------------------|------------------------------------------------------------|----------------------------------------|------------------------------|
| `package.json`                          | yes (byte-precise scalar splice)                           | yes                                    | yes (`file:` ranges)         |
| `go.mod`                                | yes (`golang.org/x/mod/modfile`)                           | no such field                          | yes (`replace`)              |
| `requirements*.txt`                     | yes (per-line splice, comments and CRLF preserved)         | no such field                          | no such directive            |
| `Dockerfile`                            | yes (the tag of a `FROM` or `COPY --from` image)           | no such field                          | no such directive            |
| `compose.yaml`                          | yes (the tag of a service's `image`)                       | yes (the image the file builds)        | no such directive            |
| `Info.plist`                            | none to write                                              | yes (`CFBundleShortVersionString`)     | no such directive            |
| `AndroidManifest.xml`                   | none to write                                              | yes (`android:versionName`)            | no such directive            |
| `project.pbxproj`                       | none to write                                              | yes (`MARKETING_VERSION`, all configs) | no such directive            |
| `libs.versions.toml`                    | yes (through `version.ref` into `[versions]`)              | no such field                          | no such directive            |
| `Podfile`                               | yes (per-line splice, quote style preserved)               | no such field                          | no such directive            |
| `*.podspec`                             | yes (per-line splice)                                      | yes (`s.version`)                      | no such directive            |
| `build.gradle`(`.kts`)                  | yes (the version segment of a literal coordinate)          | yes (`versionName`)                    | no such directive            |
| `Cargo.toml`                            | yes (plain values and inline-table `version` keys)         | yes (`[package] version`)              | yes (`path` keys)            |
| `pyproject.toml`                        | yes (PEP 508 array entries and Poetry tables)              | yes (`[project]`, else Poetry's)       | yes (Poetry `path`)          |
| `composer.json`                         | yes (`require`, `require-dev`)                             | yes, where one is declared             | no such directive            |
| `pom.xml`                               | yes (each `<dependency>`'s `<version>`)                    | yes (the project's own, not parent's)  | no such directive            |
| `*.csproj`/`.fsproj`/`.vbproj`          | yes (`Version` attribute and child element)                | yes (first `PropertyGroup`)            | no such directive            |
| `*.nuspec`                              | yes (each `<dependency>`'s version attribute)              | yes (`<metadata><version>`)            | no such directive            |
| `Directory.Packages.props`              | yes (each `PackageVersion`)                                | no such field                          | no such directive            |
| `packages.config`                       | yes (each `<package>`'s lower-case `version`)              | no such field                          | no such directive            |
| `pubspec.yaml`                          | yes (per-line scalar splice)                               | yes                                    | yes (`dependency_overrides`) |
| `Gemfile`                               | yes (per-line splice, quote style preserved)               | no such field                          | no such directive            |
| `*.gemspec`                             | yes (per-line splice)                                      | yes (`spec.version`)                   | no such directive            |
| `Packages/manifest.json`                | yes (byte-precise scalar splice, one field)                | no such field                          | no such directive            |
| `ProjectSettings/ProjectSettings.asset` | none to write                                              | yes (`bundleVersion`)                  | no such directive            |
| `project.godot`                         | none to write                                              | yes (`config/version`)                 | no such directive            |
| `plugin.cfg`                            | none to write                                              | yes (the addon's `version`)            | no such directive            |
| `export_presets.cfg`                    | none to write                                              | yes (every preset's store version)     | no such directive            |
| `*.uproject`                            | declared, never written (a plugin is named, not versioned) | no such field                          | no such directive            |
| `*.uplugin`                             | declared, never written (same reason)                      | yes (`VersionName`)                    | no such directive            |
| `Config/DefaultGame.ini`                | none to write                                              | yes (`ProjectVersion`)                 | no such directive            |
| `Config/DefaultEngine.ini`              | none to write                                              | yes (Android `VersionDisplayName`)     | no such directive            |
| `game.project`                          | none to write (a library is an archive URL)                | yes (`version`)                        | no such directive            |
| `project.json`/`gem.json`               | yes (the whole `Gem==1.0.0` literal)                       | yes, where one is declared             | no such directive            |

The last column is `Relink`, the other half of this package. Where `Rewrite` changes the version text a manifest
declares, `Relink` manages the directive that points a dependency at a folder in the same repository instead of at a
registry. Only five formats have such a directive to manage, which is why the rest of the column reads the way it does:
a NuGet package reference or a Maven coordinate names a package and nothing else, and there is no spelling for
"resolve this one locally" for `Relink` to add or remove. `SupportsLink` reports the same five at runtime.

A Docker image is named rather than located too. A reference points at a registry, so there is no redirect to manage:
building the image from a folder in this repository is what a compose file's `build:` says, and that is the author's
structure rather than a version dispat reconciles.

The game engines say it a third way. An Unreal plugin is resolved by name against the project and the engine, a Godot
addon and an O3DE gem sit in a folder the project already names, and a Defold library is an archive URL. There is no
redirect any of them could carry, and nothing local for `Relink` to point at.

**Every ecosystem the scanner reads now has a writer.** `TestEveryScannedEcosystemHasAWriter` is the list that says so,
and a format the scanner learns to read should fail it until it can be written too.

Every writer proves its output before a byte lands: `json.Valid` for the JSON formats, `modfile.Parse` for go.mod, a
re-parse for the XML formats, `toml.Unmarshal` for the TOML ones.

`project.pbxproj`, the Ruby manifests and the Gradle build scripts have no cheap grammar to re-parse against, so three
guards stand in for one. A replacement carrying any byte that could end a literal or open a block is refused outright.
The file's brace balance must come out unchanged. And the reader is run over the result, where it has to agree that
every splice landed where it was aimed.

The two Docker formats have no grammar to re-parse either, and they answer it with two guards. Nothing is written that
a registry would not accept as a tag, so the replacement can never carry a character that ends the value it sits in.
And the reader is run over the result, where every reference must read back as the tag it was aimed at.

The Docker formats decline three shapes of reference outright, and each comes back as `Skipped` rather than as an
error, because each is how a careful file is written:

| Reference                       | Why it is left alone                                                                     |
|---------------------------------|------------------------------------------------------------------------------------------|
| `FROM redis`                    | there is no tag to replace, and inventing one overrides the default the author chose      |
| `FROM redis@sha256:...`         | the digest is what gets pulled, so a new tag beside it would name a version nothing uses  |
| `FROM ${REGISTRY}/base:${TAG}`  | the value is resolved outside the file, and a literal would sever the indirection         |

A tag is also not a range. `^1.2.3` is not something a registry can resolve, so a caret policy writes the bare version
into a Docker manifest; a `{version}` template still passes through, which is how `{version}-alpine` is spelled.

## From the command line

`dispat writer <manifest>...` is this package with a report attached, and it needs no dispat config file and no git
repository:

```sh
dispat writer packages/web/package.json --set-version 1.3.0     # the own version
dispat writer packages/web/package.json --set @acme/core=^1.3.0 # a declared range
dispat writer services/api/go.mod --link github.com/acme/core=../core
dispat writer packages/web/package.json --set nope=1.0 --strict # exit 1 on a missing edit
```

`--set` takes `[kind:]name=range`, where the range starts after the first `=` and the kind prefix is only read as one
for the four dependency fields, so a Maven `group:artifact` coordinate keeps its colon. The full guide is
[Manifest tools](https://yohimik.github.io/dispat/editing/manifests/).

`dispat replacer <file>...` is `Replace` with the same report attached:

```sh
dispat replacer --replace 'com.acme:core:1.2.0=>com.acme:core:1.3.0' build.gradle README.md
dispat replacer --strict --replace 'stale-pattern=>x' Dockerfile   # exit 1 when it matched nothing
```

## Replacing literal text: `Replace`

Some versions do not live in a manifest: a coordinate a Gradle script assembles by hand, a base image in a Dockerfile,
the install line in a README. `Replace` is the format writer with the format knowledge taken away.

```go
res, err := writer.Replace("build.gradle", []writer.Replacement{
{Find: "com.acme:core:1.2.0", Write: "com.acme:core:1.3.0"},
})
// res.Applied, res.Skipped, res.Missing, res.Count, res.Path
```

It runs over any file at all, replaces every occurrence rather than the first, and applies its replacements in order,
each over what the one before it left. It shares the read cap and the atomic write with the rest of the package, and it
refuses a file that looks binary (`ErrBinaryFile`, decided by a NUL byte in the first 8 KiB) so a replacement never
lands in a PNG that happens to contain the version text. `ReplaceBytes` is the same work in memory, without touching
the caller's input.

Because it parses nothing it also cannot tell an intended match from an accidental one, so a replacement should carry
enough context to be unambiguous: `com.acme:core:1.2.0`, not `1.2.0`.

## Redirecting a dependency to a local folder

`go.mod` can point a dependency somewhere else, and a few other formats can too:

```go
res, err := writer.Relink("services/svc/go.mod", []writer.Link{
{Name: "github.com/acme/core", Path: "../../pkg/core"},
})
// res.Applied, res.Skipped, res.Missing, res.Path
```

An empty `Path` removes the redirect instead of adding one, which is what a release does before publishing: a local
link that ships to consumers gives them a module they cannot resolve.

| Format           | Directive                                      | How the redirect is spelled                       |
|------------------|------------------------------------------------|---------------------------------------------------|
| `go.mod`         | `replace`                                      | `replace acme/core => ../core`                    |
| `Cargo.toml`     | `[patch.crates-io]`                            | `core = { path = "../core" }`                     |
| `pubspec.yaml`   | `dependency_overrides`                         | `core:` with an indented `path: ../core` under it |
| `pyproject.toml` | `[tool.uv.sources]`                            | `core = { path = "../core" }`                     |
| `package.json`   | `overrides`, `resolutions` or `pnpm.overrides` | `"core": "file:../core"`                          |

`Link.Version` narrows the redirect to one required version, which only `go.mod` can express. The others key their
directive on the name alone and ignore it.

`SupportsLink` answers in advance whether a file has anywhere to put one. Every other format writes nothing and
reports each link in `Skipped`.

`Links` is the other direction: it enumerates the directives a file already carries, across every place the five
formats keep one, so a CI gate can prove no local link survived a build without knowing any names in advance.
`DropLinks` is `Links` followed by the matching removals: the shape of an "unlink whatever the build linked" step.

```go
links, err := writer.Links("services/svc/go.mod")   // what the file redirects today
res, err := writer.DropLinks("services/svc/go.mod") // remove them all
```

npm, Yarn and pnpm each name that map differently, so the field is chosen by reading the file rather than by guessing.
An existing `resolutions` or `pnpm.overrides` wins, then a `packageManager` field naming yarn or pnpm, and npm's
`overrides` is the default. All three read a `file:` specifier, which is the portable spelling, and the scanner reads it
back out as a local path.

One caveat is npm's alone: it refuses an override for a package the manifest depends on directly unless the two specs
match exactly, so overrides there are aimed at transitive dependencies. Yarn and pnpm have no such rule.

The test a format has to pass is narrow: it must point a **package at a local folder**, through a **separate,
package-keyed directive**. `Gemfile`, `Podfile`, `requirements*.txt`, Poetry tables and `.csproj` are out because their
redirect is part of a declaration rather than a directive to manage.

`pom.xml` has no redirect at all. Gradle can substitute in `resolutionStrategy` or `settings.gradle`, but only as
statements inside a closure, which is the one place this package will not write. Version catalogs, `Info.plist`,
`AndroidManifest.xml`, `project.pbxproj`, `.nuspec`, `packages.config` and `Directory.Packages.props` have nothing of
the kind.

One thing worth knowing: `composer.json` has a literal `"replace"` key and it does not mean this. It declares that the
package provides another one, which is how forks and metapackages tell Composer not to install the original. Writing a
redirect there would be wrong, so Composer is left alone.

The pyproject table is uv's, and only uv's. Poetry spells the same idea on the declaration and PEP 621 has no
equivalent, so a project not using uv gains a table its tooling will ignore.

## Writing the build counter

The version rewriters never touch a build counter: it is a monotonic count rather than a semantic version, and the two
move for different reasons. `SetBuild` is the separate write that moves it, in the nine places a format keeps one:
`CFBundleVersion`, `android:versionCode`, `CURRENT_PROJECT_VERSION` (every build configuration, like the marketing
version), Gradle's `versionCode`, the `+` suffix a pubspec version carries, Unity's `AndroidBundleVersionCode` and the
per-platform counters under `buildNumber` (every one of them, for the same reason), `version/code` in every Godot
export preset, a `.uplugin`'s `Version`, and the Android `StoreVersion` in `Config/DefaultEngine.ini`.

```go
res, err := writer.SetBuild("ios/App/Info.plist", "42")
// res.BuildWritten, res.Path
```

A counter the file does not declare is not created (the pubspec suffix, part of the version scalar, is the one
exception), a counter deferring to a build setting is left alone, and the Android and Gradle counters must be
integers. A format with no counter at all gives `ErrNoBuildCounter`.

## Behaviours worth reading first

Three behaviours are worth reading before turning these on.

A `[versions]` entry that several catalog libraries share **fans out**. Changing one coordinate changes every library
pinned to that ref, which follows from the file the author wrote. Two edits landing on one shared entry with different
text are refused with `ErrConflictingEdits` instead of letting the last one win.

`MARKETING_VERSION` is written to *every* build configuration, since leaving Debug and Release disagreeing is worse than
either alternative. A project holding two targets at different versions on purpose is the case this cannot serve.

A coordinate declared under several Gradle configurations is updated in all of them.

A value that defers to something outside the file is left alone rather than replaced with a literal. Overwriting it
would sever the indirection it exists for. That covers a Maven `${property}`, a Cargo `{ workspace = true }`, an MSBuild
or Xcode `$(...)` property, a nuspec `$version$` pack-time token, and the `spec.version = Acme::VERSION` constant nearly
every published gem uses to keep its library and its packaging in step.

A Maven `<parent>`'s version is never written either. It selects which POM this one inherits from, so moving it would
repoint the build instead of releasing the module.

Nothing is added where a declaration carries no version on purpose. A pod or gem with no requirement, one pinned to a
git revision or a path, a constraint spread across two literals (`gem 'pg', '>= 0.18', '< 2.0'`), and a catalog library
with no version are all reported in `Skipped`.

There is one exception, and it follows an older precedent. A bare PEP 508 entry gains its first specifier, in
`pyproject.toml` as in a requirements file, because a name with no specifier there is an unpinned dependency rather than
a choice.

## Requirements

Go 1.25 or later. Dependencies: `golang.org/x/mod`, `github.com/pelletier/go-toml/v2` (to re-parse a rewritten version
catalog before it is written) and the workspace's own `pkg/manifest`.

## Licence

MIT. See [LICENSE](./LICENSE).
