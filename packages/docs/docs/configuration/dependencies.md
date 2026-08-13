# dependencies

`dependencies` declares the consumer → provider relations the graph orders releases by. It is an object keyed by the
consumer, and each consumer lists what it depends on:

```yaml
dependencies:
  app: [core, utils]
  web: core            # one provider needs no array
```

A provider is a bare name when that is all there is to say about the edge. When it carries more, write it as an object:

```yaml
dependencies:
  app:
    - core
    - provider: utils
      keep: true
    - provider: tooling
      kind: devDependencies
```

`kind` names the manifest field the edge stands for: `dependencies` (the default), `devDependencies`,
`peerDependencies` or `optionalDependencies`. Propagation follows or ignores the edge according to
`parser.propagation.kinds`, whose default is every kind except `devDependencies`.

`keep: true` marks an edge [`dispat compute`](../cli/compute.md) must never suggest removing: a deliberate relation no
manifest declares, such as a Docker base-image chain. The planner treats kept edges like any other.

Both packages must exist, and their names are matched the way every other name-keyed part of the config is matched,
without regard to case. Self-dependencies and cycles are rejected; duplicates are ignored.

## Where an edge can be written

The same object can be written at three levels, and every declaration merges into one graph. Nothing overrides
anything: an edge is declared once, wherever it reads best.

At the **root**, for any edge at all. On a **space**, for the edges that belong next to it. On a **package**, in its
[`packages` entry or in-folder file](./packages.md#package-dependencies), where it lists its own providers.

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

An edge written on a space must **touch** it: its consumer or its provider is one of that space's own packages. That
covers the edges inside a space, and cross-space edges too, which belong to whichever of the two spaces you think of
as owning the relation. An edge between two packages of neither space is refused, because a reader looking for it
would have no space to look in. Those belong in the root object.

[`dispat compute`](../cli/compute.md) edits each edge where it was written, so a correction to an edge declared on a
space is applied there. New edges it discovers go to the root object instead: whether a space may hold a given edge
is a rule about the graph, and compute leaves that decision to you.

The object keyed by consumer is the only shape the key accepts. An entry inside a consumer's list may not name a
`consumer` of its own, because the key it sits under already says which package the edge belongs to.
