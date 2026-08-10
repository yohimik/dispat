# writer

A deliberately lightweight manifest writer: format-preserving in-place edits where only the version text being changed
is replaced, and every other byte of the file (indentation, key order, comments) survives verbatim. The writing
counterpart of [`pkg/scanner`](../scanner), sharing its vocabulary through
[`pkg/manifest`](../manifest), and the library behind dispat's native auto-versioning. The goal is to support **all
package managers**; each ecosystem gains a writer once its rewrite can be made byte-precise.

```go
res, err := writer.Rewrite("packages/web/package.json", "1.3.0", []writer.Edit{
{Name: "@acme/core", Kind: "dependencies", Range: "^1.3.0"},
})
// res.Applied, res.Missing, res.VersionWritten, res.Path
```

Writes are atomic (same-folder temp file, fsync, rename) and skipped entirely when nothing changed; a rewritten
`package.json` is re-validated as JSON before a single byte lands on disk. Reads are capped at 16 MiB
(`ErrManifestTooLarge`); an unsupported path is `ErrUnsupportedManifest`.

## Supported manifests

| File                   | Rewrites dependency ranges                          | Rewrites the own `version` field    |
|------------------------|-----------------------------------------------------|-------------------------------------|
| `package.json`         | yes (byte-precise scalar splice)                    | yes                                 |
| `go.mod`               | yes (`golang.org/x/mod/modfile`)                    | no such field                       |
| `requirements*.txt`    | yes (per-line splice, comments and CRLF preserved)  | no such field                       |
| `Info.plist`           | none to write                                       | yes (`CFBundleShortVersionString`)  |
| `AndroidManifest.xml`  | none to write                                       | yes (`android:versionName`)         |
| `project.pbxproj`      | none to write                                       | yes (`MARKETING_VERSION`, all configs) |
| `libs.versions.toml`   | yes (through `version.ref` into `[versions]`)       | no such field                       |
| `Podfile`              | yes (per-line splice, quote style preserved)        | no such field                       |
| `*.podspec`            | yes (per-line splice)                               | yes (`s.version`)                   |
| `build.gradle`(`.kts`) | yes (the version segment of a literal coordinate)   | yes (`versionName`)                 |

Every writer proves its output before a byte lands: `json.Valid` for npm, `modfile.Parse` for go.mod, a re-parse for the
XML formats, `toml.Unmarshal` for the version catalog. `project.pbxproj`, the CocoaPods manifests and the Gradle build
scripts have no cheap grammar to re-parse against, so three guards stand in for one — a replacement carrying any byte
that could end a literal or open a block is refused outright, the file's brace balance must be unchanged, and the reader
is re-run over the result and must agree every splice landed where it was aimed.

## Not written today

The goal is a writer per scanner-supported ecosystem; the ones without one (Cargo, pyproject, composer, Maven, NuGet,
pub) are read-only, and their reconciliation belongs to a `flow.version` script.

Build numbers are read but never written. `CFBundleVersion`, `android:versionCode` and `CURRENT_PROJECT_VERSION` are
monotonic counters rather than semantic versions, and nothing upstream computes one; bumping them belongs to a
`flow.version` script.

Three behaviours are worth knowing before turning these on. A `[versions]` entry several catalog libraries share **fans
out** — changing one coordinate changes every library pinned to that ref, which follows from the file the author wrote —
and two edits landing on one shared entry with different text are refused with `ErrConflictingEdits` rather than letting
the last one win. `MARKETING_VERSION` is written to *every* build configuration, because leaving Debug and Release
disagreeing is worse than either alternative; a project deliberately holding two targets at different versions is the
case this cannot serve. And a coordinate declared under several Gradle configurations is updated in all of them.

Nothing is ever added, only replaced: a pod with no version requirement, a git- or path-pinned pod, a constraint spread
across two literals (`pod 'X', '>= 1.0', '< 2.0'`), a catalog library with no version, a computed `s.version =
Acme::VERSION` and an `$(MARKETING_VERSION)` build-setting reference are all reported missing and left exactly as they
are. Overwriting the last of those would silently sever the Xcode indirection the project relies on.

## Requirements

Go 1.25 or later. Dependencies: `golang.org/x/mod`, `github.com/pelletier/go-toml/v2` (to re-parse a rewritten version
catalog before it is written) and the workspace's own `pkg/manifest`.

## Licence

MIT. See [LICENSE](../../LICENSE.md).
