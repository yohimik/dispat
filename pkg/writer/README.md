# writer

A deliberately lightweight manifest writer: format-preserving in-place edits where only the version text
being changed is replaced, and every other byte of the file — indentation, key order, comments — survives
verbatim. The writing counterpart of [`pkg/scanner`](../scanner), sharing its vocabulary through
[`pkg/manifest`](../manifest), and the library behind dispat's native auto-versioning. The goal is to
support **all package managers**; each ecosystem gains a writer once its rewrite can be made byte-precise.

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

## Not written today

The goal is a writer per scanner-supported ecosystem; the ones without one — Cargo, pyproject, composer,
Maven, NuGet, pub — are read-only, and their reconciliation belongs to a `flow.version` script. Only
`package.json` has a writable own-version field.

## Requirements

Go 1.25 or later. Dependencies: `golang.org/x/mod` and the workspace's own `pkg/manifest`.

## Licence

MIT. See [LICENSE](../../LICENSE.md).
