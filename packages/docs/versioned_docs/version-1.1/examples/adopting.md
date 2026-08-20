# Adopting dispat

How to introduce dispat into a repository, whether it is brand new or has been shipping versions for years. Both halves
run through [`dispat compute`](../cli/compute.md), which reads what the manifests already declare instead of asking you
to transcribe it.

## Let the manifests declare the graph

Instead of maintaining `dependencies` by hand and writing a version-sync script, let dispat read both from the
manifests. One space, `autoVersion` turned on, and no `dependencies` yet:

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

`packages/web/package.json` declares `"@acme/core": "workspace:*"`. `dispat compute` finds the edge and shows where it
came from:

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

`--check` exits non-zero whenever the config lags the manifests, so put it in CI next to your linters. Now release:
the `autoVersion` block means dispat itself rewrites the manifests at the version stage, before each build.

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

Read the manifest: the `workspace:*` range became `^0.1.0` (only ranges matching the `match` globs are touched, so the
hand-pinned `left-pad` survived), and the `version` field advanced on its own. Only the version text changed; every
other byte of the file is exactly as it was. To regenerate a lock file after the rewrite, name a `syncLock`
script in the block and, if the lock file lives at the repo root, list it under `commit.include` so the release commit
carries it. Details: [`autoVersion`](../configuration/autoversion.md) and
[the compute command](../cli/compute.md).

## Adopt dispat in a repository that already ships versions

The recipe above starts from zero. Most repositories do not: `packages/core/package.json` already says `1.4.2`,
`packages/web/package.json` says `2.1.0`, and those numbers are on the registry. Nothing in a dispat config says so
yet, and versions live in git tags, so a first run would see no tags, start both packages at `0.0.0` and release
`0.0.1`. That is the one thing to sort out before the first release, and `dispat compute` does it for you.

Write the smallest config that describes the layout, then run compute and read what it found:

```console
$ dispat compute
+ add     web -> core (dependencies)  packages/web/package.json dependencies "@acme/core": "workspace:*"
+ initial core 1.4.2  packages/core/package.json declares 1.4.2; no release tag yet
+ initial web 2.1.0  packages/web/package.json declares 2.1.0; no release tag yet

3 suggestion(s); apply all with --write, choose with --interactive
```

Two kinds of line, from one read of the same files. The `add` line is the dependency graph, as above. Each `initial`
line is a starting point: the version that package is at today, which dispat needs written down because no release tag
carries it yet. The evidence after each line is the file it came from, so a number that looks wrong is one `cat` away
from being explained.

Nothing has been written at this point. `--write` applies the lot, and `--interactive` walks them one at a time if you
would rather take the graph now and think about the versions:

```console
$ dispat compute --write
+ add     web -> core (dependencies)  packages/web/package.json dependencies "@acme/core": "workspace:*"
+ initial core 1.4.2  packages/core/package.json declares 1.4.2; no release tag yet
+ initial web 2.1.0  packages/web/package.json declares 2.1.0; no release tag yet

applied 3 change(s) to dispat.json (previous copies carry the .backup suffix)
```

The config now carries both, and the previous version of the file is beside it as `dispat.json.backup`:

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

Check the result before releasing anything. `dispat status` plans without touching the repository, and the plan now
continues the history the manifests knew about:

```console
$ dispat status
12:04:05 INF ● changed baselineFromInitials=true bump=minor channel=stable dueToProviders=[] ownCommits=1 package=core reason=direct space=libs version="1.4.2 -> 1.5.0"
12:04:05 INF ● changed baselineFromInitials=true bump=minor channel=stable dependsOn=["core"] dueToProviders=[] ownCommits=1 package=web reason=direct space=libs version="2.1.0 -> 2.2.0"
12:04:05 INF release plan ready held=0 packages=2 releasing=2
```

`baselineFromInitials=true` is dispat saying where each starting point came from. After the first release the tags
exist, they win over the entries from then on, and compute has nothing more to say about baselines. Three things are
worth knowing while you are here:

- A number you disagree with is yours to change. Edit the entry, or write it before running compute at all: an entry
  that already exists is never touched, `"core": "0.0.0"` included, which is how you tell dispat a package really does
  start from zero.
- Packages that are already tagged are skipped, so this is safe to run in a repository that has been releasing with
  dispat for a year. Nothing is proposed for a package whose tags can answer the question.
- Versions that state an intention rather than a release are left alone: a Maven `1.0.0-SNAPSHOT`, or two manifests in
  one package that disagree about the number (reported as `W225`). Those are decisions, and compute does not make
  decisions for you.

Full rules: [the compute command](../cli/compute.md) and [`initials`](../configuration/versions.md#initials).
