# Adopting dispat

Start by asking [`dispat compute`](../cli/compute.md) to read your manifests. It proposes dependency edges and starting
versions for you to review before the first release.

For a new project, follow the first section. If your project already ships, also check its starting versions, tag
format and changelog handling below. You can keep the release history you already have.

## Let the manifests declare the graph

Define a space, turn on `autoVersion`, and leave `dependencies` empty for now. The manifests will supply the edges:

```json
{
  "scripts": {
    "build": "npm ci && npm run build",
    "publish": "npm publish --access public"
  },
  "spaces": {
    "libs": {
      "path": "packages",
      "flow": {
        "build": "build",
        "publish": "publish"
      },
      "autoVersion": {
        "match": [
          "workspace:*"
        ]
      }
    }
  }
}
```

Write `"@acme/core": "workspace:*"` in `packages/web/package.json`. Run `dispat compute` to find the dependency edge
and see where it came from:

```console
$ dispat compute
+ add     web -> core (dependencies)  packages/web/package.json dependencies "@acme/core": "workspace:*"

1 suggestion(s); apply all with --write, choose with --interactive

$ dispat compute --write
+ add     web -> core (dependencies)  packages/web/package.json dependencies "@acme/core": "workspace:*"

applied 1 change(s) to dispat.json (previous copy at dispat.json.backup)

$ dispat compute --check
dependencies and baselines are in sync: 1 detected edge(s), 1 declared
```

Put `dispat compute --check` in CI next to your linters, because it exits non-zero whenever the config lags the
manifests. Now run `dispat` to release. The `autoVersion` block tells dispat to rewrite the manifests at the version
stage, before each build.

```console
$ dispat
12:04:05 INF manifest reconciled manifest=package.json package=core ranges=0 stage=version version=0.1.0 versionWritten=true
12:04:05 INF build succeeded package=core stage=build version=0.1.0
12:04:05 INF + core@0.1.0 package=core stage=publish version=0.1.0
12:04:05 INF manifest reconciled manifest=package.json package=web ranges=1 stage=version version=0.1.0 versionWritten=true
12:04:05 INF build succeeded package=web stage=build version=0.1.0
12:04:05 INF published package=core stage=publish tag=core@0.1.0 version=0.1.0
12:04:05 INF published package=web stage=publish tag=web@0.1.0 version=0.1.0

$ cat packages/web/package.json
{
  "name": "@acme/web",
  "version": "0.1.0",
  "dependencies": {"@acme/core": "^0.1.0", "left-pad": "1.3.0"}
}
```

The `workspace:*` range became `^0.1.0` and the `version` field advanced. `left-pad` is unchanged because its range
does not match the `match` globs. Other file content stays as it was. Name a `syncLock` script to regenerate a lock file
after the rewrite, and list the lock file under
`commit.include` if it lives at the repo root (details in [`autoVersion`](../configuration/autoversion.md) and
[the compute command](../cli/compute.md)).

## Adopt dispat in a repository that already ships versions

The previous recipe starts from zero, but most repositories already have versions like `1.4.2` in
`packages/core/package.json` and `2.1.0` in `packages/web/package.json`. dispat reads versions from git tags, so a
first run with no tags starts both packages at `0.0.0` and releases `0.0.1`. Run `dispat compute` to sort this out
before the first release.

Write a minimal config that describes your layout. Run compute and read the output:

```console
$ dispat compute
+ add     web -> core (dependencies)  packages/web/package.json dependencies "@acme/core": "workspace:*"
+ initial core 1.4.2  packages/core/package.json declares 1.4.2; no release tag yet
+ initial web 2.1.0  packages/web/package.json declares 2.1.0; no release tag yet

3 suggestion(s); apply all with --write, choose with --interactive
```

Each `add` line proposes a dependency edge. Each `initial` line proposes a starting version for a package with no
matching release tag. The source file appears beside the suggestion so you can check where a value came from.

dispat writes nothing to disk until you pass a flag. Run `dispat compute --write` to apply everything at once, or pass
`--interactive` to walk through the changes one at a time. This lets you accept the graph now and think about the
versions later:

```console
$ dispat compute --write
+ add     web -> core (dependencies)  packages/web/package.json dependencies "@acme/core": "workspace:*"
+ initial core 1.4.2  packages/core/package.json declares 1.4.2; no release tag yet
+ initial web 2.1.0  packages/web/package.json declares 2.1.0; no release tag yet

applied 3 change(s) to dispat.json (previous copies carry the .backup suffix)
```

The config now carries both the graph and the starting versions. You will find the previous version of the file saved
as `dispat.json.backup`:

```json title="dispat.json"
{
  "dependencies": {
    "web": ["core"]
  },
  "initials": {
    "core": "1.4.2",
    "web": "2.1.0"
  }
}
```

Run `dispat status` to check the result before releasing anything. This plans the release without touching the
repository. The plan now continues the history that your manifests knew about:

```console
$ dispat status
12:04:05 INF ● changed baselineFromInitials=true bump=minor channel=stable dueToProviders=[] ownCommits=1 package=core reason=direct space=libs version="1.4.2 -> 1.5.0"
12:04:05 INF ● changed baselineFromInitials=true bump=minor channel=stable dependsOn=["core"] dueToProviders=[] ownCommits=1 package=web reason=direct space=libs version="2.1.0 -> 2.2.0"
12:04:05 INF release plan ready held=0 packages=2 releasing=2
```

The `baselineFromInitials=true` field shows where each starting point came from. Tags exist after the first release,
and they win over the config entries from then on. Compute has nothing more to say about baselines once the tags take
over, but keep three things in mind:

- Edit any number you disagree with, or write it before running compute at all. dispat never touches an entry that
  already exists. Write `"core": "0.0.0"` to tell dispat a package really does start from zero.
- dispat skips packages that are already tagged. This makes compute safe to run in a repository that has been releasing
  with dispat for a year. Nothing is proposed for a package when the tags can answer the question.
- dispat ignores versions that state an intention rather than a release. This includes a Maven `1.0.0-SNAPSHOT` or two
  manifests in one package that disagree about the number (reported as `W225`). Those are decisions, and compute does
  not make decisions for you.

Read the full rules in [the compute command](../cli/compute.md) and
[`initials`](../configuration/versions.md#initials).

## Existing changelogs and history

You may already have a hand-written `CHANGELOG.md` and years of commits that do not follow the current convention.
Check these four details before your first release: where a new entry will go, how much history it will cover, how
old commit messages are handled, and whether an existing heading matches the planned tag.

### The file keeps everything above its first entry

If a changelog opens with dispat's configured title, dispat keeps that title and inserts the new entry one blank line
below it. Existing content follows unchanged.

If the file opens differently, everything before the first line beginning `## ` is treated as its preamble. This
can include front matter, a title, badges and introductory text. The preamble stays at the top; the new entry goes
below it, using the configured [entry seam](../configuration/records.md#the-seam-between-entries). dispat does not
add its own `# Changelog` title to that file.

```markdown
---
title: Releases
sidebar_position: 3
---

# Change Log            <- the preamble ends here, at the first entry heading

## core@1.5.0 (2026-08-31)   <- the new entry

### Features

- read the manifest before the lock file

## v1.4.2 (2024-01-01)  <- everything that was already there, where it was
```

Two details follow from recognising the title rather than the preamble. A file that *does* open with the configured
title has no preamble at all, so a badge row written between that title and the first entry stays where it is in the
file and ends up below the newest entry. And a hand-maintained `## Unreleased` heading is an entry heading like any
other, so new entries are written above it rather than into it.

A byte-order mark stays at the very top, ahead of everything, and a title terminated with CRLF is still recognised as
the title. Old content keeps its existing line endings; only the seam dispat writes uses `\n`. A second run finds
the existing entry and skips it rather than adding a duplicate.

The same writer serves [`dispat changelog`](../cli/changelog.md), so a flow that writes its entries from a stage script
adopts a file in exactly this way.

### The first release documents the whole history

A package with no matching release tag includes every conventional commit in its first changelog entry. For a
long-lived project, that can be a large entry.

To start from an existing release, set `tagFormat` to match its tags:

```yaml
tagFormat: "v{version}"
```

dispat then reads `v1.4.2` as the package's baseline, and the first entry documents only the commits after it. Where no
tag matches, [`initials`](../configuration/versions.md#initials) states the starting version and the window still spans
the history behind it.

### A history that predates the convention

Old commits may not follow the convention. With the default
[`commitErrors: "warn"`](../configuration/parser.md#commiterrors), dispat reports each unrecognised message, excludes
it from release input and continues. Setting `commitErrors: "error"` makes those messages block the release. Keep
the warning policy when adopting a history that you do not intend to rewrite.

Set [`parser.quiet: true`](../configuration/parser.md#quiet) to keep the findings out of the log. The diagnostics are
still counted, and the summary reports how many were hidden.

### When a hand-written heading matches the tag

dispat identifies an entry by its heading. A changelog whose headings were written in the shape your `tagFormat`
produces can therefore already carry the heading for the version this release is about to write. dispat reads it as an
entry that exists, leaves the file alone, and reports the skip as `W226`. The release itself goes through: only the
record is held.

dispat cannot tell whether the matching heading was written by a person or an earlier run, so it preserves the entry.
Only that version is affected; the next release writes its entry normally.

See [From one package to many](./one-to-many.md) to add deliverables while preserving existing package identities and release history.
