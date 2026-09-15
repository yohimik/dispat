# dependencies

`dependencies` declares the consumer to provider relations the graph uses to order releases. Write it as an object
keyed by the consumer. List what each consumer depends on:

```yaml
dependencies:
  app: [core, utils]
  web: core            # one provider needs no array
```

Name the provider as a bare string when you only need to declare the edge. Write it as an object when you need to
configure more details:

```yaml
dependencies:
  app:
    - core
    - provider: utils
      keep: true
    - provider: tooling
      kind: devDependencies
    - provider: optional-runtime
      external: true
```

Set `kind` to name the manifest field the edge stands for. This can be `dependencies` (the default), `devDependencies`,
`peerDependencies` or `optionalDependencies`. Propagation follows or ignores the edge based on
`parser.propagation.kinds`, which defaults to every kind except `devDependencies`.

Set `keep: true` to mark an edge [`dispat compute`](../cli/compute.md) must never suggest removing. Use this for
deliberate relations no manifest declares, like a Docker base-image chain. The planner treats kept edges like any other
edge.

Set `external: true` when the provider belongs to a source configuration that is not always imported. While that
package is absent, dispat skips the edge and reports it; it does not invent a package. When the package is present, the
edge becomes ordinary: name validation, cycle checks, propagation, release ordering, failure blocking, version
reconciliation, `--consumers`, and script ordering all apply. `dispat compute` preserves the declaration in both
states. The option skips only the missing-provider error; an invalid consumer, `kind`, or other field still fails while
the provider is absent. A missing provider without `external: true` remains an error.

The consumer must exist, and a provider must exist unless its object sets `external: true`. dispat matches names
without regard to case, just like every other name-keyed part of the config. It rejects self-dependencies and cycles,
and ignores duplicates.

You cannot make a releasable package depend on a package in a
[`versioning: none` space](../reference/releasing/versioning.md#packages-that-never-release-none). The provider would
never have a version for auto-versioning to write, so dispat refuses the edge when the configuration loads. A `none`
package can consume releasable packages, and edges between two `none` packages work normally to order their script
runs.

## Where an edge can be written

You can write the same object at three levels. dispat merges every declaration into one graph. Nothing overrides
anything, so you declare an edge once wherever it reads best.

Write it at the **root** for any edge at all. Write it on a **space** for the edges that belong next to it. Write it on
a **package** in its [`packages` entry or in-folder file](./packages.md#package-dependencies) to list its own
providers.

```json
{
  "spaces": {
    "libs": {
      "path": "packages",
      "dependencies": {
        "web": ["core", { "provider": "utils", "keep": true }]
      }
    }
  }
}
```

An edge written on a space must **touch** it, meaning the consumer or the provider is one of that space's own packages.
This covers edges inside a space and cross-space edges. dispat refuses an edge between two packages of neither space,
because those belong in the root object.

Run [`dispat compute`](../cli/compute.md) to edit each edge where it was written. It applies corrections to an edge
declared on a space directly to that space. It writes a new edge where its consumer already declares its providers,
and to the root object when the consumer declares none, because it leaves space organization decisions to you.

You must use an object keyed by the consumer. An entry inside a consumer's list cannot name a `consumer` of its own.
The key it sits under already says which package the edge belongs to.
