# writer

A deliberately lightweight manifest writer: format-preserving in-place edits where only the version text being changed
is replaced, and every other byte of the file (indentation, key order, comments) survives verbatim. The writing
counterpart of [`pkg/scanner`](../scanner), sharing its vocabulary through
[`pkg/manifest`](../manifest), and the library behind dispat's native auto-versioning. The goal is to support **all
package managers**; each ecosystem gains a writer once its rewrite can be made byte-precise.

```go
res, err := writer.Rewrite("packages/web/package.json", "1.3.0", []writer.Edit{
{Name: "@acme/core", Kind: "dependencies", Range: "^1.3.0"},
})
// res.Applied, res.Skipped, res.Missing, res.VersionWritten, res.Path
```

Writes are atomic, through a same-folder temp file, an fsync and a rename, and are skipped entirely when nothing
changed. Reads are capped at 16 MiB (`ErrManifestTooLarge`), and an unsupported path gives `ErrUnsupportedManifest`.

The result separates three outcomes. `Applied` holds the edits that changed the file. `Skipped` holds the ones whose
dependency is declared but whose version cannot be written, which is the normal state of a healthy manifest. `Missing`
holds the ones the manifest does not declare at all, which usually means the caller and the file disagree.

## Supported manifests

| File                   | Rewrites dependency ranges                          | Rewrites the own `version` field    |
|------------------------|-----------------------------------------------------|-------------------------------------|
| `package.json`         | yes (byte-precise scalar splice)                    | yes                                 |
| `go.mod`               | yes (`golang.org/x/mod/modfile`)                    | no such field                       |
| `requirements*.txt`    | yes (per-line splice, comments and CRLF preserved)  | no such field                       |
| `Info.plist`           | none to write                                       | yes (`CFBundleShortVersionString`)  |
| `AndroidManifest.xml`  | none to write                                       | yes (`android:versionName`)         |
| `project.pbxproj`      | none to write                                       | yes (`MARKETING_VERSION`, all configs) |
| `libs.versions.toml`   | yes (through `version.ref` into `[versions]`)       | no such field                       |
| `Podfile`              | yes (per-line splice, quote style preserved)        | no such field                       |
| `*.podspec`            | yes (per-line splice)                               | yes (`s.version`)                   |
| `build.gradle`(`.kts`) | yes (the version segment of a literal coordinate)   | yes (`versionName`)                 |
| `Cargo.toml`           | yes (plain values and inline-table `version` keys)  | yes (`[package] version`)           |
| `pyproject.toml`       | yes (PEP 508 array entries and Poetry tables)       | yes (`[project]`, else Poetry's)    |
| `composer.json`        | yes (`require`, `require-dev`)                      | yes, where one is declared          |
| `pom.xml`              | yes (each `<dependency>`'s `<version>`)             | yes (the project's own, not parent's) |
| `*.csproj`/`.fsproj`/`.vbproj` | yes (`Version` attribute and child element) | yes (first `PropertyGroup`)      |
| `*.nuspec`             | yes (each `<dependency>`'s version attribute)       | yes (`<metadata><version>`)         |
| `Directory.Packages.props` | yes (each `PackageVersion`)                     | no such field                       |
| `packages.config`      | yes (each `<package>`'s lower-case `version`)       | no such field                       |
| `pubspec.yaml`         | yes (per-line scalar splice)                        | yes                                 |
| `Gemfile`              | yes (per-line splice, quote style preserved)        | no such field                       |
| `*.gemspec`            | yes (per-line splice)                               | yes (`spec.version`)                |

**Every ecosystem the scanner reads now has a writer.** `TestEveryScannedEcosystemHasAWriter` is the list that says so,
and a format the scanner learns to read should fail it until it can be written too.

Every writer proves its output before a byte lands: `json.Valid` for the JSON formats, `modfile.Parse` for go.mod, a
re-parse for the XML formats, `toml.Unmarshal` for the TOML ones.

`project.pbxproj`, the Ruby manifests and the Gradle build scripts have no cheap grammar to re-parse against, so three
guards stand in for one. A replacement carrying any byte that could end a literal or open a block is refused outright.
The file's brace balance must come out unchanged. And the reader is run over the result, where it has to agree that
every splice landed where it was aimed.

## Replacing a dependency with a local folder

`go.mod` can point a dependency somewhere else, and a few other formats can too:

```go
res, err := writer.Replace("services/svc/go.mod", []writer.Replacement{
{Name: "github.com/acme/core", Path: "../../pkg/core"},
})
// res.Applied, res.Skipped, res.Missing, res.Path
```

An empty `Path` removes the redirect instead of adding one, which is what a release does before publishing: a local
replace that ships to consumers gives them a module they cannot resolve.

| Format | Directive | How the redirect is spelled |
|---|---|---|
| `go.mod` | `replace` | `replace acme/core => ../core` |
| `Cargo.toml` | `[patch.crates-io]` | `core = { path = "../core" }` |
| `pubspec.yaml` | `dependency_overrides` | `core:` with an indented `path: ../core` under it |
| `pyproject.toml` | `[tool.uv.sources]` | `core = { path = "../core" }` |
| `package.json` | `overrides`, `resolutions` or `pnpm.overrides` | `"core": "file:../core"` |

`Replacement.Version` narrows the redirect to one required version, which only `go.mod` can express. The others key
their directive on the name alone and ignore it.

`SupportsReplace` answers in advance whether a file has anywhere to put one. Every other format writes nothing and
reports each replacement in `Skipped`.

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

## Not written today

Build numbers are read but never written. `CFBundleVersion`, `android:versionCode` and `CURRENT_PROJECT_VERSION` are
monotonic counters rather than semantic versions, and nothing upstream computes one; bumping them belongs to a
`flow.version` script.

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

MIT. See [LICENSE](../../LICENSE.md).
