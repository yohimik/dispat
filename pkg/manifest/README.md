# manifest

This package defines the shared vocabulary between [`pkg/scanner`](../scanner) (the manifest reader) and
[`pkg/writer`](../writer) (the manifest rewriter). It keeps the reading and writing halves of dispat in sync so they
never drift apart. Both sides use the same format list, spell dependency kinds the same way, and agree byte for byte on
what a Docker reference is.

The surface, by concern:

- **Formats.** `Format` names each of the thirty-five recognised manifest formats, `Formats` is the canonical list both
  halves prove they cover, and `FormatOf` maps a file name onto its format. `IsRequirementsFile` and `IsDockerfile`
  handle name rules that are too complex for a lookup table. Requirements files match by whole words, and Dockerfiles
  match `Dockerfile`, `Dockerfile.dev`, `api.Dockerfile`, and `Containerfile` while excluding prose extensions.
- **Kinds.** `Kind` is the dependency field a declaration came from, with the four constants spelled like the fields
  themselves and the zero value standing for plain `dependencies`. `ParseKind` reads a spelling from a config file or a
  command line, accepting `"dependencies"` as the long form of the zero value.
- **Names.** `NameWords` splits a file's base name into words, and `NormalizePyName` applies PEP 503 identically on
  both sides, which keeps `Acme_Core` and `acme-core` as one package.
- **Docker.** `ParseImageRef` reads an image reference into repository, tag, and digest with the tag's byte span, while
  `ValidTag` checks registry tag grammar. `DockerfileRefs` walks a Dockerfile's `FROM`, `COPY --from`, and
  `RUN --mount` instructions with stage-alias scoping, and `ComposeService` and `ComposeIdentity` decide which
  service's image names a compose file. These parsers live here rather than in the reader or the writer so both get
  byte-identical offsets.

```go
manifest.FormatOf("Dockerfile.dev")                 // FormatDockerfile, true
manifest.KindDevDependencies                        // the four dependency kinds, spelled like the fields
manifest.ParseKind("dependencies")                  // KindDependencies, true
manifest.IsRequirementsFile("requirements-dev.txt") // true
manifest.NormalizePyName("Acme_Core")               // "acme-core" (PEP 503)
manifest.ParseImageRef("ghcr.io/acme/api:1.2.0")    // repository, tag and the tag's span
```

The package has no dependencies and performs no I/O. It contains only models, pure functions, and the pure parsers
above. Nothing here touches a filesystem.

## Requirements

Go 1.25 or later.

## Licence

MIT. See [LICENSE](./LICENSE).
