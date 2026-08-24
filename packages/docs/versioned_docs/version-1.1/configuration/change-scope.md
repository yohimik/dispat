# What counts as a change

A release triggers when a commit modifies a package. dispat must decide which package a commit applies to. You can tell
it to ignore specific files so they never trigger a release.

## Two ways a commit reaches a package

**By name.** Name your packages directly by adding a scope to the commit header:

```
feat(core): a new export
```

This commit addresses `core` regardless of where its files live. The rules on this page do not change this behavior.

**By its files.** Omit the scope from your commit header to let dispat resolve the package from the changed files:

```
fix: correct the rounding
```

dispat maps each changed file to the package folder that contains it. A file always belongs to the innermost package
when you nest them. The commit then addresses those matched packages.

The settings below only restrict this second case. A commit that explicitly names a package always reaches it.

## `src`: only this folder is the package

Define `src` to specify the folder holding your actual source files. This path is relative to the package folder:

```json
{
  "spaces": {
    "libs": { "path": "packages", "src": "src" }
  }
}
```

A change to `packages/core/src/parser.ts` now counts as a change to `core`. Modifying `packages/core/docs/guide.md`
changes nothing.

Set `src` at the root, on a space, or on a single package. The nearest statement wins. dispat resolves this path
against each package's own folder, so a space-level `src: "src"` means `<space>/<package>/src` for every package in
that space.

Create the specified folder in every package that inherits this setting, because a missing folder fails the load. Fix a
failure by giving that specific package its own `src` value. This prevents a package from quietly owning no files and
never releasing.

## `ignore`: everything except these

Use `ignore` to work in reverse. The package stays whole, but the named files stop triggering releases:

```json
{
  "packages": {
    "core": { "ignore": ["docs/", "*.snap", "testdata/"] }
  }
}
```

A commit that only touches `packages/core/docs/guide.md` now releases nothing. A commit touching that file **and**
`packages/core/parser.ts` releases `core`. One valid file is enough to trigger the release.

You can also put these patterns in a `.dispatignore` file inside the package folder. Write one pattern per line:

```
# packages/core/.dispatignore
docs/
*.snap
testdata/
```

These two methods provide the exact same feature. Use the configuration key to keep your settings in one place. Use the
file to keep the rules next to the code they describe.

:::note
`.dispatignore` and [`.dispatexclude`](./spaces.md#dispatexclude) sound alike but do different jobs. `.dispatexclude`
decides **what is a package**, so a folder named in it is not a package at all. `.dispatignore` decides **what counts
as a change** to a package that already exists.
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

dispat ignores blank lines and lines starting with `#`. A pattern with no `/` matches files at any depth, while one
with a `/` counts from the folder that declared it. Escape an exclamation mark with `\!` if your filename actually
starts with one.

Order your patterns carefully because the last matching pattern wins:

```json
{ "ignore": ["docs/", "!docs/api.md"] }
```

This configuration ignores everything under `docs` except `docs/api.md`.

### The levels add up

Write patterns at the root, space, or package level. These patterns **accumulate** rather than replacing each other:

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

Every package in the repository ignores markdown files. Every package inside `libs` also ignores its fixtures. The
`core` package, and only `core`, counts `CHANGES.md` again.

The nearest level has the final say. The `core` package sits below the repository root, so its `!CHANGES.md` rule
overrules the `*.md` rule above it. This pattern belongs strictly to `core` and leaves its sibling packages unaffected.

dispat reads the configuration key first and the folder's `.dispatignore` file second. The file sitting in the folder
wins if the two disagree at the same level.

## What ignoring does not do

Ignoring a file only prevents a scopeless commit from addressing the package. It never changes what the package is or
how dispat releases it.

- A commit naming the package by scope releases it regardless of the touched files.
- The release commit still stages the entire package folder, including ignored files.
- Scripts still run in the package folder, and dispat still writes the changelog there.
- Manifest discovery and auto-versioning remain untouched, so dispat still finds a `package.json` sitting next to an
  ignored folder.

## A commit that reaches nothing

A scopeless commit addresses no package if you ignore every file it touches. dispat prints a warning rather than
staying silent:

```
W131  unit resolved to no package and is inert: fix: rewrite the guide
```

The tool releases nothing. Check the warning to see which commit was inert in case you expected a release.

## Choosing between them

Set `src` when a package has a single folder of actual source code and everything else is incidental. Set `ignore` when
you mix important files with files that do not matter. The two settings compose: `src` isolates the folder, and
`ignore` excludes files from what remains, with the patterns still counted from the package folder.
