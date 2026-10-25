# writer: the manifest writer

Use `github.com/yohimik/dispat/pkg/writer` to bump versions in `package.json`, `go.mod`, `Cargo.toml`, and every other
format [the scanner](./scanner.md) reads. Most formats use targeted byte edits that preserve indentation, key order
and comments. Go modules use the Go module formatter.

All format writers share the same read limit, validation, and atomic-write path. Changed files are written through a
temporary file in the same folder, followed by an fsync and rename. If the result is unchanged, no write occurs.

```sh
go get github.com/yohimik/dispat/pkg/writer
```

## Rewriting a manifest

```go
res, err := writer.Rewrite("packages/web/package.json", "1.3.0", []writer.Edit{
	{Name: "@acme/core", Kind: "dependencies", Range: "^1.3.0"},
})
// res.Applied, res.Skipped, res.Missing, res.VersionWritten, res.Path
```

Check the result as well as the error:

- `Applied`: edits that changed the file.
- `Skipped`: the dependency exists, but the writer cannot change its version. This is expected for values supplied
  through a property or another file.
- `Missing`: the manifest does not declare the dependency. Check the dependency name and the file you passed.

An unsupported path returns `ErrUnsupportedManifest`. Reads are capped at 16 MiB, and a larger file returns
`ErrManifestTooLarge`.

## What is never written

Values supplied by a property, workspace, or another file are left unchanged. Replacing one with a literal would
break that connection. This covers a Maven `${property}`, a Cargo `{ workspace = true }`, an
MSBuild or Xcode `$(...)` property, a nuspec `$version$` pack-time token, and the `spec.version = Acme::VERSION`
constant nearly every published gem uses.

A Maven `<parent>` version is never written either. It selects which POM this one inherits from, so moving it would
repoint the build rather than release the module. Nothing is added where a declaration carries no version on purpose,
such as a gem pinned to a git revision.

Before writing, the package validates the edited content with `json.Valid`, `modfile.Parse`, an XML parser, or
`toml.Unmarshal`, as appropriate for the format.

Formats with no cheap grammar to re-parse against are guarded three ways instead. This applies to `project.pbxproj`,
the Ruby manifests, and the Gradle build scripts. The writer refuses a replacement carrying any byte that could end a
literal or open a block, the file's brace balance must come out unchanged, and the reader must agree that every splice
landed where it was aimed.

## Replacing literal text

Some versions do not live in a manifest at all, like a coordinate a Gradle script assembles by hand or an install line
in a README. Call `Replace` to use the same machinery with the format knowledge taken away.

```go
res, err := writer.Replace("build.gradle", []writer.Replacement{
	{Find: "com.acme:core:1.2.0", Write: "com.acme:core:1.3.0"},
})
// res.Applied, res.Skipped, res.Missing, res.Count, res.Path
```

Each replacement changes every match. Replacements run in order, so a later replacement sees the result of the
earlier ones.

It refuses a file that looks binary so a replacement never lands inside a PNG that happens to contain the version text.
This returns `ErrBinaryFile` and is decided by a NUL byte in the first 8 KiB. Call `ReplaceBytes` to do the same work
in memory.

Because it parses nothing, it cannot tell an intended match from an accidental one. Provide enough context in a
replacement to make it unambiguous. Pass `com.acme:core:1.2.0`, not `1.2.0`.

## Redirecting a dependency to a local folder

Call `Relink` to manage the directive that points a dependency at a folder in the same repository instead of at a
registry:

```go
res, err := writer.Relink("services/svc/go.mod", []writer.Link{
	{Name: "github.com/acme/core", Path: "../../pkg/core"},
})
```

Pass an empty `Path` to remove a local redirect instead of adding one. Configure an unlink step before publishing
when your build creates temporary redirects; release execution does not remove them automatically.

Five formats have such a directive. These are `go.mod` (`replace`), `Cargo.toml` (`[patch.crates-io]`), `pubspec.yaml`
(`dependency_overrides`), `pyproject.toml` (`[tool.uv.sources]`), and `package.json` (`overrides`, `resolutions`, or
`pnpm.overrides`). The `package.json` directive is chosen by reading the file rather than guessing.

Call `IsLinkSupported` to report the same five formats at runtime. Every other format writes nothing and reports each
link in `Skipped`.

Call `Links` to read the other direction and enumerate the directives a file already carries. This lets a CI gate prove
no local link survived a build without knowing any names in advance. Call `DropLinks` to run `Links` followed by the
matching removals:

```go
links, err := writer.Links("services/svc/go.mod")
res, err := writer.DropLinks("services/svc/go.mod")
```

In `pubspec.yaml`, only an override with its own `path` field is a local redirect. Version constraints and Git,
hosted or SDK overrides remain intact, including a `path` nested inside a Git source. In `package.json`, a selected
override container must be an object; a malformed container is refused without changing the file.

In `package.json`, linking a name repoints every `file:` or `link:` copy of it across `overrides`, `resolutions` and
`pnpm.overrides`, and removing it deletes every such copy, while a registry override of the same name in another field
stays as written. A name with no local copy gets one `file:` entry in the chosen field, which replaces a registry
override of that name in that field. Removing a name that only a registry override declares changes nothing and reports
the name in `Missing`.

## Writing the build counter

The version rewriters never touch a build counter. A counter is a monotonic count rather than a semantic version, and
the two move for different reasons. Call `SetBuild` to run the separate write that moves it:

```go
res, err := writer.SetBuild("ios/App/Info.plist", "42")
// res.BuildWritten, res.Path
```

This covers the nine places a format keeps a counter. These include `CFBundleVersion`, `android:versionCode`,
`CURRENT_PROJECT_VERSION`, Gradle's `versionCode`, and the `+` suffix a pubspec version carries. It also covers Unity's
`AndroidBundleVersionCode`, the per-platform counters under `buildNumber`, `version/code` in every Godot export preset,
a `.uplugin`'s `Version`, and the Android `StoreVersion` in `Config/DefaultEngine.ini`.

An absent counter is left absent, except for a pubspec version's `+` suffix, which can be appended to the existing
version. A format with no counter at all returns `ErrNoBuildCounter`.

## The same work from the command line

Use [`dispat writer`](../cli/writer.md) and [`dispat replacer`](../cli/replacer.md) to make these edits from the
terminal and print a report. Neither command needs a configuration file or a git repository:

```sh
dispat writer packages/web/package.json --set-version 1.3.0
dispat writer services/api/go.mod --link github.com/acme/core=../core
dispat replacer --replace 'com.acme:core:1.2.0=>com.acme:core:1.3.0' build.gradle
```

Inside a release, [`autoVersion`](../configuration/autoversion.md) applies the same writer to the planned versions at
the version stage.

## Further reading

- Read [Manifest tools](../editing/manifests.md) for a guide to the reader and the writer together.
- Read the full API on [pkg.go.dev](https://pkg.go.dev/github.com/yohimik/dispat/pkg/writer).
- View the source [on GitHub](https://github.com/yohimik/dispat/tree/main/pkg/writer).
