# writer

Rewrites dependency manifests in place, format-preserving: only the version text being changed is replaced,
and every other byte of the file — indentation, key order, comments — survives verbatim. The writing
counterpart of [`pkg/scanner`](../scanner), and the library behind dispat's native auto-versioning.

```go
res, err := writer.Rewrite("packages/web/package.json", "1.3.0", []writer.Edit{
    {Name: "@acme/core", Kind: "dependencies", Range: "^1.3.0"},
})
// res.Applied, res.Missing, res.VersionWritten, res.Path
```

Writes are atomic (same-folder temp file, fsync, rename) and skipped entirely when nothing changed; a
rewritten `package.json` is re-validated as JSON before a single byte lands on disk. Reads are capped at
16 MiB (`ErrManifestTooLarge`); an unsupported path is `ErrUnsupportedManifest`.

## Supported manifests

| File | Rewrites dependency ranges | Rewrites the own `version` field |
|---|---|---|
| `package.json` | yes (byte-precise scalar splice) | yes |
| `go.mod` | yes (`golang.org/x/mod/modfile`) | no such field |
| `requirements*.txt` | yes (per-line splice, comments and CRLF preserved) | no such field |

## Deliberately out of scope

The other ecosystems the scanner reads — Cargo, pyproject, composer, Maven, NuGet, pub — are read-only:
their reconciliation belongs to a `flow.version` script. Only `package.json` has a writable own-version
field.

## Requirements

Go 1.25 or later. One dependency: `golang.org/x/mod`.

## Licence

MIT. See [LICENSE](../../LICENSE.md).
