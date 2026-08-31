# Splitting the file with `$ref`

One config file describes the whole monorepo, so in a big repository that file gets long. Use `$ref` to move any part
of it into a separate file. Write an object holding a single `$ref` key anywhere a value belongs, and dispat will use
that file's content as the value:

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

The two files act exactly like one file with the spaces written inline. Nothing else changes. dispat checks the same
keys, catches the same typos, and prints the same errors.

## Where the path points

Write a relative path to resolve it against the folder of the file that wrote it, not against the monorepo root. This
means `cfg/spaces.yaml` above can say `$ref: ./flow.yaml` and get `cfg/flow.yaml`. You can move the whole `cfg` folder
somewhere else without editing anything inside it.

Write an absolute path to reach a file shared from outside the repository. dispat uses absolute paths exactly as
written.

**Paths inside a referenced file still mean what they would mean inline.** A space's `path`, a package's `path`, `src`,
and the changelog's `file` resolve from the monorepo root or from the package folder, while only `$ref` itself is
relative to the file it is written in. In the example above, `path: packages/libs` points at `packages/libs` under the
monorepo root, not at `cfg/packages/libs`.

## What a referenced file can hold

A referenced file can hold anything a value can be. This includes an object, a list, or a single value:

```yaml
shell:
  $ref: ./cfg/shell.json     # ["/bin/bash", "-c"]
tagFormat:
  $ref: ./cfg/tag-format.yaml  # '{name}@v{version}'
```

The file can be JSON, YAML, or TOML. The extension decides how dispat reads it. A YAML config can pull in a JSON
fragment, and a JSON config can pull in a YAML fragment.

Do not reference an empty file. This causes an error because a `$ref` expects a value. An empty file is almost always a
mistake.

## Merging several files

Name a list of files in a `$ref` to combine them into a single value. dispat reads them in the order they are written.
Use this to write a shared block once and adjust it where needed:

```yaml
# dispat.yaml
scripts:
  $ref:
    - ./cfg/scripts-common.yaml
    - ./cfg/scripts-local.yaml
```

Objects merge key by key. The last file to write a key wins. In the example above, `cfg/scripts-local.yaml` only needs
to hold the scripts it changes, because everything else comes from the common file.

Lists join end to end. This helps when you build the record lines around a changelog entry or a GitHub release:

```yaml
changelog:
  footer:
    $ref:
      - ./cfg/footer-common.yaml   # the lines every repository writes
      - ./cfg/footer-extra.yaml    # the ones this one adds
```

Every file of one `$ref` must hold an object, or every file must hold a list, because a single value cannot merge with
anything. Mixing types causes an error that names both files and their contents. Naming no files is an error, and
naming one file means exactly what naming that file directly means.

Keys written beside the reference win over the merged result, exactly as they do for a single file. The merge itself is
not a deep merge. A key an object holds replaces that key whole, exactly as
[overriding](#overriding-part-of-a-fragment) does.

## References inside references

A referenced file can hold references of its own. These resolve against its own folder. Nesting is capped at 32
levels, which no honest configuration reaches, and dispat reads a file twice if you use it in two places.

Do not create a loop. dispat stops and prints the path if a file reads itself, directly or through others:

```
config: $ref cycle: dispat.yaml (spaces) -> cfg/spaces.yaml (libs.flow) -> dispat.yaml;
a file cannot reference itself, directly or through another
```

Each step names the file. The key that pointed onwards is in brackets.

## Overriding part of a fragment

Write keys beside a `$ref` to win over the file it names. This lets one shared fragment serve places that agree with
all of it and places that agree with most of it:

```yaml
spaces:
  libs:
    $ref: ./cfg/space.yaml
  apps:
    $ref: ./cfg/space.yaml
    versioning: independent    # everything else comes from the fragment
```

The override replaces the key it names whole. It is not a deep merge. An object written beside a reference replaces
that object rather than blending into it.

Overriding only works when the referenced file holds an object. dispat prints an error if the file holds a list or a
single value, because there is nothing for the extra keys to override.

## Where references can go

Put a reference in every property, at every depth. This includes inside `custom`. The document itself can be a
reference, which lets a repository keep its real configuration somewhere else:

```json
{"$ref": "./cfg/dispat.yaml"}
```

Folder config files work the same way. You can split the config files that a space folder or a package folder carries.

## Editing a split config

Run [`dispat compute --write`](../cli/compute.md) to write into the file that actually holds the key. If your
`packages` map lives in a fragment, dispat rewrites the fragment. The reference in the root config stays as it is, and
the backup copy sits beside the file that changed.

dispat will not write a key that comes from more than one file at once, which happens when a key is composed from a
fragment *and* the keys beside the reference, or when a key merges from a `$ref` naming several files. dispat refuses
and says what to change because it has no reason to prefer one file over another. For a composed key, write it beside
the `$ref` or leave the reference as the whole value, and for a merged one, write it beside the `$ref` or point the
reference at a single file.

## Seeing what was read

Pass `--log-level debug` to see how many files the configuration was made of. Pass `--log-level trace` to name each
one:

```
DBG configuration loaded config=dispat.yaml configFiles=3 root=/repo spaces=2
TRC configuration file read file=dispat.yaml
TRC configuration file read file=/repo/cfg/spaces.yaml
TRC configuration file read file=/repo/cfg/flow.yaml
```

Check this first when a split config does not behave. A `$ref` naming the wrong fragment looks exactly like a key
nobody wrote.

When a value is wrong, the error names the key path (`spaces["libs"]: ...`) rather than the file the text was written
in. Use the trace above to find that file.
