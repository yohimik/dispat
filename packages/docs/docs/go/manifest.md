# manifest: the shared vocabulary

The `github.com/yohimik/dispat/pkg/manifest` package provides the vocabulary shared by
[the manifest reader](./scanner.md) and [the manifest writer](./writer.md). This ensures both halves never drift apart.
They range over the same format list, spell dependency kinds the same way, and agree exactly on what a Docker reference
is.

The package contains models, pure functions, and parsers. It has no dependencies and performs no I/O. Nothing here
touches your filesystem.

```sh
go get github.com/yohimik/dispat/pkg/manifest
```

## The surface, by concern

**Formats.** The `Format` type names each of the thirty-five recognised manifest formats, and `Formats` provides the
canonical list both halves prove they cover. `FormatOf` maps a file name onto its format. `IsRequirementsFile` matches
requirements files by whole words, and `IsDockerfile` matches `Dockerfile`, `Dockerfile.dev`, `api.Dockerfile`, and
`Containerfile` while excluding prose extensions.

**Kinds.** The `Kind` type identifies the dependency field a declaration came from. It has four constants spelled like
the fields themselves, with the zero value standing for plain `dependencies`. `ParseKind` reads a spelling from a
configuration file or a command line, and it accepts `"dependencies"` as the long form of the zero value.

**Names.** The `NameWords` function splits a file's base name into words. `NormalizePyName` applies PEP 503 identically
on both sides. This normalization keeps `Acme_Core` and `acme-core` recognized as a single package.

**Docker.** `ParseImageRef` reads an image reference into a repository, tag, and digest, recording the tag's byte span.
`ValidTag` provides the registry tag grammar, while `DockerfileRefs` walks a Dockerfile's `FROM`, `COPY --from`, and
`RUN --mount` instructions with stage-alias scoping. `ComposeService` and `ComposeIdentity` decide which service's
image names a compose file, and these parsers live here so both halves get byte-identical offsets.

## In use

```go
manifest.FormatOf("Dockerfile.dev")                 // FormatDockerfile, true
manifest.KindDevDependencies                        // the four dependency kinds, spelled like the fields
manifest.ParseKind("dependencies")                  // KindDependencies, true
manifest.IsRequirementsFile("requirements-dev.txt") // true
manifest.NormalizePyName("Acme_Core")               // "acme-core" (PEP 503)
manifest.ParseImageRef("ghcr.io/acme/api:1.2.0")    // repository, tag and the tag's span
```

The byte span is the detail that makes the reader and writer work together. The reader records where a version sits,
and the writer splices exactly that range. A rewrite changes only the version text and leaves everything around it
untouched.

## Further reading

- The [scanner](./scanner.md) and [writer](./writer.md) packages build on this vocabulary.
- Read the full API on [pkg.go.dev](https://pkg.go.dev/github.com/yohimik/dispat/pkg/manifest) and view the source
  [on GitHub](https://github.com/yohimik/dispat/tree/main/pkg/manifest).
