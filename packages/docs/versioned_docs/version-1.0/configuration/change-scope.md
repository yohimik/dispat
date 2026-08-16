# What counts as a change

A release happens because a commit says a package changed. This page is about how dispat decides which package a
commit is talking about, and how to tell it that some files should not count.

## Two ways a commit reaches a package

**By name.** A commit whose header carries a scope names its packages outright:

```
feat(core): a new export
```

That addresses `core` wherever its files are. Nothing on this page changes that.

**By its files.** A commit with no scope in the header is resolved from what it touched:

```
fix: correct the rounding
```

dispat looks at the changed files, finds the package each one belongs to, and the commit addresses those packages. A
file belongs to the package whose folder it sits in, and when packages are nested, to the innermost one.

Everything below narrows the second case only. A commit that names a package always reaches it.

## `src`: only this folder is the package

`src` says which folder holds the files that matter. It is relative to the package folder:

```json
{
  "spaces": {
    "libs": { "path": "packages", "src": "src" }
  }
}
```

Now a change to `packages/core/src/parser.ts` is a change to `core`, and a change to `packages/core/docs/guide.md` is
not a change to anything.

`src` can be written at the root (every package), on a space (its packages) or on one package, and the nearest
statement wins. Wherever it is written it is resolved against each package's own folder, so a space-level `src: "src"`
means `<space>/<package>/src` for every package of that space.

Every package it reaches must actually have the folder. A package without it fails the load, and the fix is to give
that package a `src` of its own. The alternative would be a package that quietly owns no files and stops releasing,
which is the kind of thing nobody notices for a month.

## `ignore`: everything except these

`ignore` works the other way round. The package stays whole, and named files stop counting:

```json
{
  "packages": {
    "core": { "ignore": ["docs/", "*.snap", "testdata/"] }
  }
}
```

A commit touching only `packages/core/docs/guide.md` now releases nothing. A commit touching that file **and**
`packages/core/parser.ts` releases `core`, because one file that counts is enough.

The same patterns can go in a `.dispatignore` file in the folder, one per line:

```
# packages/core/.dispatignore
docs/
*.snap
testdata/
```

The two are the same feature. Use the key when you want the configuration in one place, and the file when you want it
next to what it describes.

:::note
`.dispatignore` and [`.dispatexclude`](./spaces.md#dispatexclude) sound alike and do different jobs.
`.dispatexclude` decides **what is a package**: a folder named in it is not one at all. `.dispatignore` decides **what
counts as a change** to a package that already exists.
:::

### Patterns

| Pattern | Matches |
|---------|---------|
| `docs/` | the `docs` folder and everything under it, at any depth |
| `docs/api/` | that one folder, counted from where the pattern was written |
| `*.snap` | any file ending in `.snap`, at any depth |
| `README.md` | any file with that name, at any depth |
| `docs/*` | everything under `docs`, since `*` crosses folder boundaries |
| `!CHANGES.md` | re-includes that file after a broader pattern excluded it |

Blank lines and lines starting with `#` are ignored. A pattern with no `/` matches at any depth; one with a `/` is
counted from the folder that declared it. Write `\!` for a file whose name really does start with an exclamation mark.

The last pattern that matches decides, so order matters:

```json
{ "ignore": ["docs/", "!docs/api.md"] }
```

Everything under `docs` stops counting except `docs/api.md`.

### The levels add up

Patterns can be written at three levels, and they **accumulate** rather than replace:

```json
{
  "ignore": ["*.md"],
  "spaces": {
    "libs": { "path": "packages", "ignore": ["fixtures/"] }
  },
  "packages": {
    "core": { "ignore": ["!CHANGES.md"] }
  }
}
```

Every package in the repository ignores markdown. Every package of `libs` also ignores its fixtures. And `core`, and
only `core`, counts `CHANGES.md` again.

The nearer level has the last word, which is what makes the re-inclusion work: `core` sits below the repository, so
its `!CHANGES.md` overrules the `*.md` above it. Its siblings are unaffected, because that pattern belongs to `core`.

Within one level the config key is read first and the folder's `.dispatignore` second, so the file sitting in the
folder wins where the two disagree.

## What ignoring does not do

Ignoring a file changes one thing: whether it makes a scopeless commit address the package. It does not change what
the package is or how it is released.

- A commit naming the package by scope releases it, whatever it touched.
- The release commit still stages the whole package folder, ignored files included.
- Scripts still run in the package folder, and the changelog is still written there.
- Manifest discovery and auto-versioning are untouched, so a `package.json` next to an ignored folder is still found.

## A commit that reaches nothing

If every file a scopeless commit touched is ignored, the commit addresses no package. That is the point, and dispat
says so rather than staying silent:

```
W131  unit resolved to no package and is inert: fix: rewrite the guide
```

Nothing is released, and the warning tells you which commit was inert in case it was not meant to be.

## Choosing between them

Reach for `src` when a package has one folder of real sources and everything else is incidental. Reach for `ignore`
when the files that matter and the files that do not are mixed together. They compose: `src` picks the folder, and
`ignore` excludes from what is left, with the patterns still counted from the package folder.
