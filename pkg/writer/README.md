# writer

Use dispat's manifest writer to update version declarations in place. Most formats use byte edits that preserve
surrounding indentation, key order, and comments. Go modules use the Go module formatter. This library serves as the writing
counterpart to [`pkg/scanner`](../scanner), shares its vocabulary through [`pkg/manifest`](../manifest), and powers
native auto-versioning in dispat.

Every format writer reads and writes through a single internal splicer. That splicer enforces the read cap, performs
the splice, proves the output parses, and executes the atomic write. `Replace` uses that same engine without
format-specific rules to edit versions outside standard manifests.

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

All writes are atomic: dispat writes to a temporary file in the same folder, runs an fsync, and renames the file over
the original. If nothing changed, dispat skips the write entirely. Manifest reads are capped at 16 MiB
(`ErrManifestTooLarge`), and unsupported file paths return `ErrUnsupportedManifest`.

Inspect the returned result to track three distinct outcomes. `Applied` lists the edits that modified the file on disk.
`Skipped` contains declared dependencies whose versions cannot be updated, while `Missing` lists entries that the
manifest does not declare.

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
| Aqua YAML                               | yes (literal package pins)                                | no such field                          | no such directive            |

For Aqua, the writer updates exact pins in either `name@version` or a separate `version` field. It leaves
`version_expr`, `go_version_file`, `import`, and `import_dir` entries alone and explains the skip in its result. It
does not evaluate expressions, fetch registry metadata, install packages, or rewrite checksums. Pass
`--manifest-format aqua` when an imported Aqua file has an arbitrary filename.

The Aqua writer accepts block mappings with plain or one-line quoted scalars and preserves comments, quotes, order,
and CRLF. It atomically refuses flow-style package maps, anchors or aliases in the package tree, and block or multiline
name and version scalars because those forms do not have an unambiguous byte span to replace. The scanner can still
read valid YAML aliases.

Use `Relink` to manage the directive that points a dependency at a folder in your repository instead of a registry.
Only five formats support this directive, while formats like NuGet and Maven name packages without any local redirect
syntax. Call `SupportsLink` at runtime to check whether a format supports these directives.

Docker images are named by registry coordinates rather than file paths. You do not manage redirects for Docker
references because a compose file's `build:` key already defines local build paths.

Game engines handle local references differently. Unreal plugins resolve by name, Godot addons and O3DE gems live in
directories named by the project, and Defold libraries point at archive URLs, so none of them use `Relink`.

**Every ecosystem the scanner reads now has a writer.** The suite `TestEveryScannedEcosystemHasAWriter` enforces this
coverage, failing whenever the scanner learns a format that lacks a writer.

Every writer verifies its output before writing bytes to disk. dispat runs `json.Valid` for JSON, `modfile.Parse` for
`go.mod`, full re-parsing for XML, and `toml.Unmarshal` for TOML.

For `project.pbxproj`, Ruby manifests, and Gradle scripts, dispat uses three structural guards in place of full grammar
parsing. It refuses replacements that contain bytes capable of ending literals or opening blocks, checks that brace
balance remains identical, and re-scans the file to verify that each splice landed accurately.

For Docker formats, dispat enforces two guards. It rejects any replacement string that a registry would refuse as a
tag, preventing syntax breaks in the enclosing value, and re-reads the output to confirm each reference matches the
intended tag.

The Docker rewriters skip three reference patterns outright, returning them as `Skipped`:

| Reference                       | Why it is left alone                                                                     |
|---------------------------------|------------------------------------------------------------------------------------------|
| `FROM redis`                    | there is no tag to replace, and inventing one overrides the default the author chose      |
| `FROM redis@sha256:...`         | the digest is what gets pulled, so a new tag beside it would name a version nothing uses  |
| `FROM ${REGISTRY}/base:${TAG}`  | the value is resolved outside the file, and a literal would sever the indirection         |

Docker tags do not support ranges like `^1.2.3`. When given a caret policy, dispat writes the bare version into the
Docker manifest while still preserving templates such as `{version}-alpine`.

## From the command line

Run `dispat writer <manifest>...` to rewrite manifests and print a summary report without needing a repository or
configuration file:

```sh
dispat writer packages/web/package.json --set-version 1.3.0     # the own version
dispat writer packages/web/package.json --set @acme/core=^1.3.0 # a declared range
dispat writer services/api/go.mod --link github.com/acme/core=../core
dispat writer packages/web/package.json --set nope=1.0 --strict # exit 1 on a missing edit
```

Pass `--set [kind:]name=range` to specify dependency updates. The range begins after the first `=`, and dispat
interprets the prefix as a kind only for standard dependency fields, preserving colons in Maven coordinates. For
complete details, see [Manifest tools](https://dispat.dev/editing/manifests/).

Run `dispat replacer <file>...` to execute `Replace` across arbitrary files and inspect the report:

```sh
dispat replacer --replace 'com.acme:core:1.2.0=>com.acme:core:1.3.0' build.gradle README.md
dispat replacer --strict --replace 'stale-pattern=>x' Dockerfile   # exit 1 when it matched nothing
```

## Replacing literal text: `Replace`

Use `Replace` when versions live outside standard manifests, such as coordinates assembled in Gradle scripts,
Dockerfile base images, or README install lines. It strips away format-specific logic to perform literal text
replacements across your files.

```go
res, err := writer.Replace("build.gradle", []writer.Replacement{
{Find: "com.acme:core:1.2.0", Write: "com.acme:core:1.3.0"},
})
// res.Applied, res.Skipped, res.Missing, res.Count, res.Path
```

Pass any text file to `Replace` to update every matching occurrence in order, applying each replacement on top of
previous edits. It uses the same read limits and atomic write guarantees as manifest rewrites, and it rejects binary
files (`ErrBinaryFile`) if it finds a NUL byte within the first 8 KiB. Call `ReplaceBytes` to run these replacements
purely in memory without touching disk.

Provide enough surrounding context to make each match unambiguous, such as `com.acme:core:1.2.0` instead of `1.2.0`.
Because `Replace` does not parse syntax, it cannot distinguish between intended targets and accidental substrings.

## Redirecting a dependency to a local folder

Call `Relink` on supported formats like `go.mod` to redirect a dependency to a local folder:

```go
res, err := writer.Relink("services/svc/go.mod", []writer.Link{
{Name: "github.com/acme/core", Path: "../../pkg/core"},
})
// res.Applied, res.Skipped, res.Missing, res.Path
```

Pass an empty `Path` to remove an existing redirect. Always clear local redirects before publishing releases so
consumers do not receive unresolvable local paths.

| Format           | Directive                                      | How the redirect is spelled                       |
|------------------|------------------------------------------------|---------------------------------------------------|
| `go.mod`         | `replace`                                      | `replace acme/core => ../core`                    |
| `Cargo.toml`     | `[patch.crates-io]`                            | `core = { path = "../core" }`                     |
| `pubspec.yaml`   | `dependency_overrides`                         | `core:` with an indented `path: ../core` under it |
| `pyproject.toml` | `[tool.uv.sources]`                            | `core = { path = "../core" }`                     |
| `package.json`   | `overrides`, `resolutions` or `pnpm.overrides` | `"core": "file:../core"`                          |

Set `Link.Version` to restrict the redirect to a specific required version, which only `go.mod` supports. Other formats
match on name alone and ignore this field.

Call `SupportsLink` to verify whether a file format supports redirect directives. Unsupported formats make no changes
on disk and report each link in `Skipped`.

Call `Links` to inspect all active redirects across the five supported formats, which allows CI pipelines to verify
that no local links remain. Use `DropLinks` to discover and remove all active redirects in a single step.

```go
links, err := writer.Links("services/svc/go.mod")   // what the file redirects today
res, err := writer.DropLinks("services/svc/go.mod") // remove them all
```

In `package.json`, dispat selects the redirect field by inspecting your manifest. It prioritises existing `resolutions`
or `pnpm.overrides` fields, checks `packageManager` for yarn or pnpm, and defaults to npm's `overrides`. All three
managers accept `file:` specifiers, which the scanner reads as local paths.

Be aware of npm's override rule: npm rejects overrides for direct dependencies unless the target specifier matches
exactly, making it suitable primarily for transitive dependencies. Yarn and pnpm impose no such restriction.

To support `Relink`, a format must point a **package at a local folder** through a **separate, package-keyed
directive**. Formats like `Gemfile`, `Podfile`, `requirements*.txt`, Poetry tables, and `.csproj` are unsupported
because their local paths are embedded directly inside dependency declarations.

`pom.xml` provides no redirect mechanism. Gradle can redirect dependencies inside `resolutionStrategy` or
`settings.gradle`, but only through closure statements that dispat does not modify. Files like version catalogs,
`Info.plist`, `AndroidManifest.xml`, `project.pbxproj`, `.nuspec`, `packages.config`, and `Directory.Packages.props`
offer no redirect directives.

Do not use `composer.json` for redirects. Its `"replace"` key declares that a package provides an alternative
implementation, such as for forks, rather than redirecting paths on disk.

The `pyproject.toml` table applies only to uv. Poetry defines local dependencies directly on declarations, and PEP 621
lacks redirect syntax, so non-uv tools will ignore this table.

## Writing the build counter

Version rewriters do not modify build counters, which track monotonic increments rather than semantic versions. Call
`SetBuild` to update counters across the nine supported formats: `CFBundleVersion`, `android:versionCode`,
`CURRENT_PROJECT_VERSION` across all build configurations, Gradle's `versionCode`, pubspec `+` suffixes, Unity's
`AndroidBundleVersionCode` and per-platform `buildNumber` entries, Godot export preset `version/code` keys, `.uplugin`
`Version` fields, and Android `StoreVersion` in `Config/DefaultEngine.ini`.

```go
res, err := writer.SetBuild("ios/App/Info.plist", "42")
// res.BuildWritten, res.Path
```

`SetBuild` leaves missing counters untouched, except for pubspec version suffixes, and ignores counters that point to
external build settings. Android and Gradle counters require integer values, and calling `SetBuild` on formats without
counter support returns `ErrNoBuildCounter`.

## Behaviours worth reading first

Review these three manifest update behaviours before applying edits.

When multiple catalog dependencies share a single `[versions]` entry, updating that entry **fans out** across all
linked coordinates. If two concurrent edits attempt to set different versions for the same shared reference, dispat
aborts with `ErrConflictingEdits`.

dispat updates `MARKETING_VERSION` across *every* build configuration to keep Debug and Release targets synchronized.
You cannot use this workflow if your project deliberately assigns different version numbers to different targets.

dispat updates a coordinate across every Gradle configuration where it appears.

dispat preserves indirect values that reference external definitions rather than replacing them with literal strings.
This rule protects Maven `${property}` references, Cargo `{ workspace = true }` entries, MSBuild and Xcode `$(...)`
properties, nuspec `$version$` tokens, and Ruby `spec.version = Acme::VERSION` constants.

dispat never modifies a Maven `<parent>` version. Changing the parent version alters project inheritance rather than
updating the module version.

dispat skips unversioned declarations instead of adding new version constraints. It marks unpinned pods or gems, git
and path dependencies, multi-literal constraints like `gem 'pg', '>= 0.18', '< 2.0'`, and unversioned catalog entries
as `Skipped`.

As an exception, dispat adds a version specifier to bare PEP 508 entries in `pyproject.toml` and requirements files. In
those ecosystems, an entry without a specifier represents an unpinned dependency rather than an intentional version
exclusion.

## Requirements

Go 1.25 or later. Dependencies: `golang.org/x/mod`, `github.com/pelletier/go-toml/v2` (to re-parse a rewritten version
catalog before it is written) and the workspace's own `pkg/manifest`.

## Licence

MIT. See [LICENSE](./LICENSE).
