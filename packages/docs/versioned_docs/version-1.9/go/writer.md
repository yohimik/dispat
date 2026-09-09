# writer: the manifest writer

Use `github.com/yohimik/dispat/pkg/writer` to bump versions in `package.json`, `go.mod`, `Cargo.toml`, and every other
format [the scanner](./scanner.md) reads. The package updates files in place and preserves their format. It replaces
only the version text and leaves every other byte verbatim, including indentation, key order, and comments.

Every format writer goes through one internal splicer. This means the read cap, the splice, the proof that the result
still parses, and the atomic write happen in the same place. Writes go through a same-folder temporary file, an fsync,
and a rename, but dispat skips the write entirely if nothing changed.

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

Read the result to see three distinct outcomes instead of just checking the error. `Applied` holds the edits that
changed the file. `Skipped` holds edits where the dependency exists but the version cannot be written, which is the
normal state of a healthy manifest.

`Missing` holds edits the manifest does not declare at all. This usually means your caller and the file disagree about
what is installed.

An unsupported path returns `ErrUnsupportedManifest`. Reads are capped at 16 MiB, and a larger file returns
`ErrManifestTooLarge`.

## What is never written

A value that defers to something outside the file is left alone rather than replaced with a literal. Overwriting it
would sever the indirection it exists for. This covers a Maven `${property}`, a Cargo `{ workspace = true }`, an
MSBuild or Xcode `$(...)` property, a nuspec `$version$` pack-time token, and the `spec.version = Acme::VERSION`
constant nearly every published gem uses.

A Maven `<parent>` version is never written either. It selects which POM this one inherits from, so moving it would
repoint the build rather than release the module. Nothing is added where a declaration carries no version on purpose,
such as a gem pinned to a git revision.

Every writer proves its output before a byte lands. It re-parses the result with `json.Valid`, `modfile.Parse`, a
re-parse for the XML formats, or `toml.Unmarshal`.

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

It runs over any file and replaces every occurrence rather than the first. It applies replacements in order, each over
what the one before it left.

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

Pass an empty `Path` to remove the redirect instead of adding one. A release removes redirects before publishing
because a local link ships consumers a module they cannot resolve.

Five formats have such a directive. These are `go.mod` (`replace`), `Cargo.toml` (`[patch.crates-io]`), `pubspec.yaml`
(`dependency_overrides`), `pyproject.toml` (`[tool.uv.sources]`), and `package.json` (`overrides`, `resolutions`, or
`pnpm.overrides`). The `package.json` directive is chosen by reading the file rather than guessing.

Call `SupportsLink` to report the same five formats at runtime. Every other format writes nothing and reports each link
in `Skipped`.

Call `Links` to read the other direction and enumerate the directives a file already carries. This lets a CI gate prove
no local link survived a build without knowing any names in advance. Call `DropLinks` to run `Links` followed by the
matching removals:

```go
links, err := writer.Links("services/svc/go.mod")
res, err := writer.DropLinks("services/svc/go.mod")
```

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

A counter the file does not declare is not created. A format with no counter at all gives `ErrNoBuildCounter`.

## The same work from the command line

Use [`dispat writer`](../cli/writer.md) and [`dispat replacer`](../cli/replacer.md) to run this package with a report
attached. Neither command needs a configuration file or a git repository:

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
