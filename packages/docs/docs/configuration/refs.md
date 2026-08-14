# Splitting the file with `$ref`

One config file describes the whole monorepo, and in a big repository that file gets long. `$ref` lets you move any
part of it into a file of its own.

Anywhere a value belongs, you can write an object holding a single `$ref` key that names another file. Dispat reads
that file and uses its content as the value:

```yaml
# dispat.yaml
scripts:
  build: npm run build
spaces:
  $ref: ./cfg/spaces.yaml
```

```yaml
# cfg/spaces.yaml
libs:
  path: packages/libs
  flow:
    build: [build]
apps:
  path: packages/apps
  flow:
    build: [build]
```

The two files together mean exactly what one file with the spaces written inline would mean. Nothing else changes:
the same keys are checked, the same typos are caught, the same errors come out.

## Where the path points

A relative path is resolved against the folder of the file that wrote it, not against the monorepo root. So
`cfg/spaces.yaml` above can itself say `$ref: ./flow.yaml` and get `cfg/flow.yaml`, and you can move the whole `cfg`
folder somewhere else without editing anything inside it.

An absolute path is used as written, which is how you reach a file shared from outside the repository.

**Paths inside a referenced file still mean what they would mean inline.** A space's `path`, a package's `path`, `src`
and the changelog's `file` are all resolved the way they always are: from the monorepo root, or from the package
folder. Only `$ref` itself is relative to the file it is written in. In the example above, `path: packages/libs`
points at `packages/libs` under the monorepo root, not at `cfg/packages/libs`.

## What a referenced file can hold

Anything a value can be. An object, as above, but also a list or a single value:

```yaml
shell:
  $ref: ./cfg/shell.json     # ["/bin/bash", "-c"]
tagFormat:
  $ref: ./cfg/tag-format.yaml  # '{name}@v{version}'
```

The file can be JSON, YAML or TOML, whatever the file referencing it is. The extension decides how it is read, so a
YAML config can pull in a JSON fragment and the other way around.

An empty file is an error. A `$ref` says "the value is over there", so a file with nothing in it is almost always a
mistake rather than an intention.

## References inside references

A referenced file may hold references of its own, resolved against its own folder. There is no limit worth thinking
about, and a file used in two places is simply read twice.

What is refused is a loop. If a file ends up reading itself, directly or through others, dispat stops and prints the
path it took:

```
config: $ref cycle: dispat.yaml (spaces) -> cfg/spaces.yaml (libs.flow) -> dispat.yaml;
a file cannot reference itself, directly or through another
```

Each step names the file and, in brackets, the key that pointed onwards.

## Overriding part of a fragment

Keys written beside a `$ref` win over the file it names. That is what lets one shared fragment serve places that agree
with all of it and places that agree with most of it:

```yaml
spaces:
  libs:
    $ref: ./cfg/space.yaml
  apps:
    $ref: ./cfg/space.yaml
    versioning: independent    # everything else comes from the fragment
```

The override replaces the key it names, whole. It is not a deep merge, so an object written beside a reference
replaces that object rather than blending into it.

Overriding only works when the referenced file holds an object. If it holds a list or a single value there is nothing
for the extra keys to override, and dispat says so.

## Where references can go

Every property, at every depth, including inside `custom`. The document itself can be one too, which is how a
repository keeps its real configuration somewhere other than the name dispat looks for:

```json
{"$ref": "./cfg/dispat.yaml"}
```

Folder config files, the ones a space folder or a package folder can carry, are read the same way and can be split
the same way.

## Editing a split config

[`dispat compute --write`](../cli/compute.md) writes into the file that actually holds the key. If your `packages` map
lives in a fragment, the fragment is what gets rewritten, the reference in the root config stays as it is, and the
backup copy sits beside the file that changed.

The one thing it will not do is write a key that is composed from a fragment *and* the keys beside the reference,
because there are two files it could go in and no reason to prefer one. Dispat refuses and tells you to write that key
beside the `$ref`, or to leave the reference as the whole value.

## Seeing what was read

`--log-level debug` reports how many files the configuration was made of, and `--log-level trace` names each one:

```
DBG configuration loaded config=dispat.yaml configFiles=3 root=/repo spaces=2
TRC configuration file read file=dispat.yaml
TRC configuration file read file=/repo/cfg/spaces.yaml
TRC configuration file read file=/repo/cfg/flow.yaml
```

That is the first thing to check when a split config does not behave: a `$ref` naming the wrong fragment looks exactly
like a key nobody wrote.

One limitation worth knowing: when a value is wrong, the error names the key path (`spaces["libs"]: ...`) rather than
the file the text was written in. The trace above is how you find that file.
