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

## Merging several files

A `$ref` can name a list of files instead of one. Dispat reads them in the order they are written and combines them
into a single value. This is how a block that several places need is written once and adjusted where it has to be:

```yaml
# dispat.yaml
scripts:
  $ref:
    - ./cfg/scripts-common.yaml
    - ./cfg/scripts-local.yaml
```

Objects merge key by key, and the last file to write a key is the one that wins. In the example above,
`cfg/scripts-local.yaml` needs to hold only the scripts it changes; everything else comes from the common file.

Lists are joined end to end, which is what makes this useful for the record lines around a changelog entry or a GitHub
release:

```yaml
changelog:
  footer:
    $ref:
      - ./cfg/footer-common.yaml   # the lines every repository writes
      - ./cfg/footer-extra.yaml    # the ones this one adds
```

The files have to agree on what they hold. Every file of one `$ref` must hold an object, or every file must hold a
list; a single value cannot be merged with anything. Mixing them is an error naming both files and what each of them
holds. A list naming no files at all is an error too, and a list naming one file means exactly what naming that file
directly means.

Keys written beside the reference still win over the merged result, the same way they win over a single file. The
merge itself is not a deep merge: a key an object holds replaces that key whole, exactly as
[overriding](#overriding-part-of-a-fragment) does.

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

The one thing it will not do is write a key that comes from more than one file at once. That happens two ways: a key
composed from a fragment *and* the keys beside the reference, and a key merged from a `$ref` naming several files. In
both cases there is more than one file the new value could go in and no reason to prefer one, so dispat refuses and
says what to change. For a composed key, write it beside the `$ref` or leave the reference as the whole value; for a
merged one, write it beside the `$ref` or point the reference at a single file.

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
