# scanner: the manifest reader

`github.com/yohimik/dispat/pkg/scanner` reads dependency manifests into one ecosystem-neutral shape: the package's
declared identity, its declared dependencies, their ranges and any local-path signals. It is a dependency manifest
parser for Go, covering twenty-three formats across fifteen ecosystems, and it only reads. Rewriting is
[the writer's](./writer.md) job.

There is no SBOM machinery here, no lockfile resolution and no network. The recognised formats are fixed at build time,
and fence tests hold the reader and the writer to the same list.

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

The `Scanner` interface exists so a caller wiring the reader and the writer together can substitute a fake in tests;
the package-level functions are the same work without one.

Both entry points share an error contract worth relying on: a manifest that fails to parse is skipped, its error is
joined into the returned error, and the manifests that did parse come back regardless. A caller can report the problem
and keep the partial result rather than losing a whole scan to one malformed file.

Reads are capped at 16 MiB per file, which comes back as `ErrManifestTooLarge`, and the output order is deterministic.

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

`BuildNumber` is the monotonic counter the mobile formats keep beside their marketing version, such as
`CFBundleVersion` or `android:versionCode`. It is not a semantic version, so no version write ever moves it.

`Indirect` holds requirements a manifest records as transitive bookkeeping rather than as its own declarations. Only
`go.mod` draws that distinction. Keeping the two apart lets a caller reconcile ranges without touching a version the
toolchain owns.

`Dropped` names entries the manifest declared but the parser could not coerce into a dependency, one line each. These
are not errors: the manifest parsed, and the caller decides whether the drops are worth reporting.

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

Several formats are matched by name rather than extension. A Dockerfile matches `Dockerfile`, `Dockerfile.dev`,
`api.Dockerfile` and Podman's `Containerfile`. A requirements file matches by whole words, so `dev-requirements.txt`
counts while `old-requirements-notes.txt` reads as prose.

Docker has no version field, so identity comes from the images a file names. A compose service declaring both a `build`
section and a tagged `image` is producing that image, and failing that the reader takes the tagged repository the most
services name. A file that only wires third-party services together declares no identity, which is the honest answer
rather than a guess.

The exact fields each format contributes, and the shapes deliberately left unread, are in the
[package README](https://github.com/yohimik/dispat/blob/main/pkg/scanner/README.md).

## Helpers for callers building a graph

Three exported helpers do the work the CLI needs on top of a scan. `NameIndex` maps a manifest name onto its owning
package, preferring stated names, then root manifests, then nested ones, and reporting a same-rank collision instead of
guessing. `ResolveLocalDir` turns a declared local path into the package folder it points at. `SkipDir` names the
folders a workspace walk never enters, exported so a caller walking a package for its own reasons stays out of the same
places.

## The same work from the command line

[`dispat scanner`](../cli/scanner.md) is this package with a listing attached, and it needs no configuration file and
no git repository:

```sh
dispat scanner packages/web              # every manifest under the folder
dispat scanner packages/web --root-only  # only the folder's own
dispat scanner --log-format json         # one JSON object per manifest
```

[`dispat compute`](../cli/compute.md) is the reader used for its intended purpose: deriving a monorepo's dependency
graph and starting versions from the manifests already on disk.

## Further reading

- [Manifest tools](../editing/manifests.md) is the guide to the reader and the writer together.
- The full API is on
  [pkg.go.dev](https://pkg.go.dev/github.com/yohimik/dispat/pkg/scanner) and the source is
  [on GitHub](https://github.com/yohimik/dispat/tree/main/pkg/scanner).
