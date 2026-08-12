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
without regard to case. Self-dependencies and cycles are rejected; duplicates are ignored. The same object can be
written on a space, for the edges that belong next to it (see
[the space's `dependencies`](./spaces.md#the-spaces-dependencies)), and a package can list its own providers in its
`packages` entry or in-folder file (see [package dependencies](./packages.md#package-dependencies)). Every
declaration merges into one list.

The object keyed by consumer is the only shape the key accepts. An entry inside a consumer's list may not name a
`consumer` of its own, because the key it sits under already says which package the edge belongs to.
