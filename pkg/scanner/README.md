# scanner

A deliberately lightweight manifest reader: thin per-format parsers turning dependency manifests into one
ecosystem-neutral shape: the package's declared identity (name, version) and its declared dependencies with their
ranges, manifest fields and local-path signals. No SBOM machinery, no lockfile resolution, no network. The goal is to
support **all package managers**; the shared vocabulary (dependency kinds, the file-name rules) lives in [
`pkg/manifest`](../manifest) so this reader and [`pkg/writer`](../writer) can never drift apart. It only reads;
rewriting is the writer's job. This is the library behind
`dispat compute` (deriving a monorepo's dependency graph from its manifests) and the executor's native auto-versioning.

```go
sc := scanner.New()
mans, err := sc.Scan(ctx, "packages/web") // every manifest under the folder
roots, err := sc.ScanRoot(ctx, "packages/web") // only the folder's own manifests
```

Both methods share one error contract: a manifest that fails to parse is skipped, its error joined into the returned
error, and the parsed manifests come back either way, so callers can report the problem and keep the partial result.
Reads are capped at 16 MiB per file (`ErrManifestTooLarge`); output order is deterministic.

## Supported manifests

| File                | Ecosystem | Reads                                                                                             |
|---------------------|-----------|---------------------------------------------------------------------------------------------------|
| `package.json`      | npm       | name, version, the four dependency fields, `file:`/`link:` local paths                            |
| `go.mod`            | gomod     | module path, direct requires, relative `replace` targets as local paths                           |
| `Cargo.toml`        | cargo     | name, version, `[dependencies]`/`[dev-dependencies]`/`[build-dependencies]`, renames, `path` keys |
| `pyproject.toml`    | python    | PEP 621, PEP 735 groups, Poetry, PEP 503 name normalisation                                       |
| `requirements*.txt` | python    | PEP 508 lines, continuations, editable local installs (`-e ./pkg`)                                |
| `composer.json`     | composer  | name, version, require/require-dev (platform requirements filtered)                               |
| `pom.xml`           | maven     | `groupId:artifactId` coordinates, scopes onto dependency kinds                                    |
| `*.csproj`          | nuget     | `PackageId`/`AssemblyName`, `PackageReference`, `ProjectReference` as local paths                 |
| `pubspec.yaml`      | pub       | name, version, dependencies, `dependency_overrides` folded onto their declarations                |

Helpers shared by the CLI's two consumers: `NameIndex` (manifest name → owning package, root manifests first, ambiguous
names reported instead of guessed) and `ResolveLocalDir` (declared local path → owning package folder).

## Not read today

The goal is full coverage of every package manager; these known gaps are listed so nobody discovers them in production:
npm `workspaces`, `overrides` and
`resolutions`; Cargo `[workspace.dependencies]`, `[workspace.members]` and target-specific tables; Maven
`${property}` interpolation, parent-POM resolution, `<dependencyManagement>` and `<modules>`; Poetry multi-constraint
dependency lists; PEP 735 `include-group`; NuGet Central Package Management (`Directory.Packages.props`). Version text
is always kept verbatim, so name matching still carries the graph where the version is indirected.

## Requirements

Go 1.25 or later.

## Licence

MIT. See [LICENSE](../../LICENSE.md).
