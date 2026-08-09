# manifest

The vocabulary shared by [`pkg/scanner`](../scanner) (the manifest reader) and [`pkg/writer`](../writer)
(the manifest rewriter): the dependency-field kinds a manifest declares, the file-name rules that decide what counts as
a manifest, and the name normalisation both sides must apply identically. It exists so the reading and writing halves of
dispat's manifest support can never drift apart.

```go
manifest.KindDevDependencies                        // the four dependency kinds, spelled like the fields
manifest.IsRequirementsFile("requirements-dev.txt") // true
manifest.NormalizePyName("Acme_Core") // "acme-core" (PEP 503)
```

No dependencies, no I/O: models and pure functions only.

## Requirements

Go 1.25 or later.

## Licence

MIT. See [LICENSE](../../LICENSE.md).
