# Adopting dispat

Introduce dispat into a new repository or one that has shipped versions for years. Run
[`dispat compute`](../cli/compute.md) to read what your manifests already declare, which saves you from transcribing
everything by hand.

## Let the manifests declare the graph

Let dispat read your `dependencies` from the manifests instead of maintaining them by hand. Define a space, turn on
`autoVersion`, and leave `dependencies` empty for now:

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

Check the manifest to see that the `workspace:*` range became `^0.1.0` and the `version` field advanced. The
hand-pinned `left-pad` survived because dispat only touches ranges matching the `match` globs, leaving every other byte
of the file exactly as it was. Name a `syncLock` script to regenerate a lock file after the rewrite, and list it under
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

dispat prints two kinds of line from reading the same files. The `add` line defines the dependency graph, while each
`initial` line defines a starting version that dispat needs written down because no release tag carries it yet. The
evidence after each line shows the source file, so you can run `cat` to explain any wrong number.

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
