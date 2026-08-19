# manifest: the shared vocabulary

`github.com/yohimik/dispat/pkg/manifest` is the vocabulary shared by [the manifest reader](./scanner.md) and
[the manifest writer](./writer.md). It exists so the two halves can never drift apart: both range over the same format
list, spell dependency kinds the same way, and agree byte for byte on what a Docker reference is.

There is no I/O here and no dependencies. The package is models, pure functions and a few parsers, and nothing in it
touches a filesystem.

```sh
go get github.com/yohimik/dispat/pkg/manifest
```

## The surface, by concern

**Formats.** `Format` names each of the thirty-five recognised manifest formats, `Formats` is the canonical list both
halves prove they cover, and `FormatOf` maps a file name onto its format. `IsRequirementsFile` and `IsDockerfile` are
the two naming rules too wordy for a lookup table: requirements files match by whole words, and Dockerfiles match
`Dockerfile`, `Dockerfile.dev`, `api.Dockerfile` and `Containerfile` while excluding prose extensions.

**Kinds.** `Kind` is the dependency field a declaration came from, with four constants spelled like the fields
themselves and the zero value standing for plain `dependencies`. `ParseKind` reads a spelling from a configuration file
or a command line, accepting `"dependencies"` as the long form of the zero value.

**Names.** `NameWords` splits a file's base name into words, and `NormalizePyName` applies PEP 503 identically on both
sides, which is what keeps `Acme_Core` and `acme-core` one package.

**Docker.** `ParseImageRef` reads an image reference into repository, tag and digest, including the tag's byte span.
`ValidTag` is the registry tag grammar. `DockerfileRefs` walks a Dockerfile's `FROM`, `COPY --from` and `RUN --mount`
instructions with stage-alias scoping. `ComposeService` and `ComposeIdentity` decide which service's image names a
compose file. These parsers live here rather than in the reader or the writer precisely so both get byte-identical
offsets.

## In use

```go
manifest.FormatOf("Dockerfile.dev")                 // FormatDockerfile, true
manifest.KindDevDependencies                        // the four dependency kinds, spelled like the fields
manifest.ParseKind("dependencies")                  // KindDependencies, true
manifest.IsRequirementsFile("requirements-dev.txt") // true
manifest.NormalizePyName("Acme_Core")               // "acme-core" (PEP 503)
manifest.ParseImageRef("ghcr.io/acme/api:1.2.0")    // repository, tag and the tag's span
```

The byte span is the detail that makes the pair work. The reader records where a version sits, and the writer splices
exactly that range, so a rewrite changes the version text and leaves everything around it untouched.

## Further reading

- [scanner](./scanner.md) and [writer](./writer.md) are the two packages built on this vocabulary.
- The full API is on
  [pkg.go.dev](https://pkg.go.dev/github.com/yohimik/dispat/pkg/manifest) and the source is
  [on GitHub](https://github.com/yohimik/dispat/tree/main/pkg/manifest).
