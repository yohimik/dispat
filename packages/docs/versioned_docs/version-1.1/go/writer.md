# writer: the manifest writer

`github.com/yohimik/dispat/pkg/writer` bumps versions in `package.json`, `go.mod`, `Cargo.toml` and every other format
[the scanner](./scanner.md) reads, in place and format-preserving: only the version text being changed is replaced, and
every other byte of the file survives verbatim, including indentation, key order and comments.

Every format writer goes through one internal splicer, so the read cap, the splice, the proof that the result still
parses and the atomic write happen in the same place for all of them. Writes go through a same-folder temporary file,
an fsync and a rename, and are skipped entirely when nothing changed.

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

The result separates three outcomes, and the distinction is the reason to read it rather than just the error. `Applied`
holds the edits that changed the file. `Skipped` holds edits whose dependency is declared but whose version cannot be
written, which is the normal state of a healthy manifest. `Missing` holds edits the manifest does not declare at all,
which usually means the caller and the file disagree about what is installed.

An unsupported path gives `ErrUnsupportedManifest`, and reads are capped at 16 MiB (`ErrManifestTooLarge`).

## What is never written

A value that defers to something outside the file is left alone rather than replaced with a literal, because
overwriting it would sever the indirection it exists for. That covers a Maven `${property}`, a Cargo
`{ workspace = true }`, an MSBuild or Xcode `$(...)` property, a nuspec `$version$` pack-time token, and the
`spec.version = Acme::VERSION` constant nearly every published gem uses.

A Maven `<parent>` version is never written either: it selects which POM this one inherits from, so moving it would
repoint the build rather than release the module. Nothing is added where a declaration carries no version on purpose,
such as a gem pinned to a git revision.

Every writer proves its output before a byte lands, re-parsing the result with `json.Valid`, `modfile.Parse`, a
re-parse for the XML formats or `toml.Unmarshal`. The formats with no cheap grammar to re-parse against, meaning
`project.pbxproj`, the Ruby manifests and the Gradle build scripts, are guarded three ways instead: a replacement
carrying any byte that could end a literal or open a block is refused, the file's brace balance must come out
unchanged, and the reader has to agree that every splice landed where it was aimed.

## Replacing literal text

Some versions do not live in a manifest at all: a coordinate a Gradle script assembles by hand, an install line in a
README. `Replace` is the same machinery with the format knowledge taken away.

```go
res, err := writer.Replace("build.gradle", []writer.Replacement{
	{Find: "com.acme:core:1.2.0", Write: "com.acme:core:1.3.0"},
})
// res.Applied, res.Skipped, res.Missing, res.Count, res.Path
```

It runs over any file, replaces every occurrence rather than the first, and applies replacements in order, each over
what the one before it left. It refuses a file that looks binary (`ErrBinaryFile`, decided by a NUL byte in the first
8 KiB) so a replacement never lands inside a PNG that happens to contain the version text. `ReplaceBytes` is the same
work in memory.

Because it parses nothing, it cannot tell an intended match from an accidental one. A replacement should carry enough
context to be unambiguous: `com.acme:core:1.2.0`, not `1.2.0`.

## Redirecting a dependency to a local folder

`Relink` manages the directive that points a dependency at a folder in the same repository instead of at a registry:

```go
res, err := writer.Relink("services/svc/go.mod", []writer.Link{
	{Name: "github.com/acme/core", Path: "../../pkg/core"},
})
```

An empty `Path` removes the redirect instead of adding one, which is what a release does before publishing: a local
link that ships to consumers gives them a module they cannot resolve.

Five formats have such a directive: `go.mod` (`replace`), `Cargo.toml` (`[patch.crates-io]`), `pubspec.yaml`
(`dependency_overrides`), `pyproject.toml` (`[tool.uv.sources]`) and `package.json` (`overrides`, `resolutions` or
`pnpm.overrides`, chosen by reading the file rather than guessing). `SupportsLink` reports the same five at runtime, and
every other format writes nothing and reports each link in `Skipped`.

`Links` reads the other direction, enumerating the directives a file already carries, so a CI gate can prove no local
link survived a build without knowing any names in advance. `DropLinks` is `Links` followed by the matching removals:

```go
links, err := writer.Links("services/svc/go.mod")
res, err := writer.DropLinks("services/svc/go.mod")
```

## Writing the build counter

The version rewriters never touch a build counter, since it is a monotonic count rather than a semantic version and the
two move for different reasons. `SetBuild` is the separate write that moves it:

```go
res, err := writer.SetBuild("ios/App/Info.plist", "42")
// res.BuildWritten, res.Path
```

It covers the nine places a format keeps one: `CFBundleVersion`, `android:versionCode`, `CURRENT_PROJECT_VERSION`,
Gradle's `versionCode`, the `+` suffix a pubspec version carries, Unity's `AndroidBundleVersionCode` and the
per-platform counters under `buildNumber`, `version/code` in every Godot export preset, a `.uplugin`'s `Version`, and
the Android `StoreVersion` in `Config/DefaultEngine.ini`. A counter the file does not declare is not created, and a
format with no counter at all gives `ErrNoBuildCounter`.

## The same work from the command line

[`dispat writer`](../cli/writer.md) and [`dispat replacer`](../cli/replacer.md) are this package with a report
attached, and neither needs a configuration file or a git repository:

```sh
dispat writer packages/web/package.json --set-version 1.3.0
dispat writer services/api/go.mod --link github.com/acme/core=../core
dispat replacer --replace 'com.acme:core:1.2.0=>com.acme:core:1.3.0' build.gradle
```

Inside a release, [`autoVersion`](../configuration/autoversion.md) is the same writer applied to the planned versions
at the version stage.

## Further reading

- [Manifest tools](../editing/manifests.md) is the guide to the reader and the writer together.
- The full API is on
  [pkg.go.dev](https://pkg.go.dev/github.com/yohimik/dispat/pkg/writer) and the source is
  [on GitHub](https://github.com/yohimik/dispat/tree/main/pkg/writer).
