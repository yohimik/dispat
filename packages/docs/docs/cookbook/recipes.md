# Recipes

Complete, copy-ready setups for the most common stacks, each with the config, the shell scripts its stages run, and the
terminal output of a real run. Every transcript on this page was produced by running dispat against a throwaway
repository; only timestamps and durations are normalized. Script output lines (the
`npm`/`docker` lines) come from your own commands, so yours will differ.

If a term is new, [Concepts](../concepts.md) defines all of them in a few minutes of reading.

- [An npm package](#an-npm-package)
- [A single package, no monorepo](#a-single-package-no-monorepo)
- [Let the manifests declare the graph](#let-the-manifests-declare-the-graph)
- [Adopt dispat in a repository that already ships versions](#adopt-dispat-in-a-repository-that-already-ships-versions)
- [A Docker image chain](#a-docker-image-chain)
- [An Android app](#an-android-app)
- [npm and Docker in one graph](#npm-and-docker-in-one-graph)
- [A pnpm workspace](#a-pnpm-workspace)
- [Registry login, once per space](#registry-login-once-per-space)
- [Recovering from a failed run](#recovering-from-a-failed-run)
- [A beta channel: try, iterate, graduate](#a-beta-channel-try-iterate-graduate)

## An npm package

The smallest real setup: one space of npm packages, a build and a publish command.

```json
{
  "scripts": {
    "build": "npm ci --silent && npm run build --silent",
    "publish": "npm publish --access public"
  },
  "spaces": {
    "libs": {
      "path": "packages",
      "flow": {
        "build": "build",
        "publish": "publish"
      }
    }
  }
}
```

Commit work with the package name in the scope, then look at the plan before releasing anything:

```console
$ git commit -m "feat(logger): first version of the logger"
$ dispat status
12:04:05 INF ● changed bump=minor channel=stable dueToProviders=[] ownCommits=1 package=logger reason=direct space=libs version="0.0.0 -> 0.1.0"
12:04:05 INF release plan ready held=0 packages=1 releasing=1
```

`status` changes nothing; it shows what a release would do. The release itself:

```console
$ dispat
12:04:05 INF ● changed bump=minor channel=stable dueToProviders=[] ownCommits=1 package=logger reason=direct space=libs version="0.0.0 -> 0.1.0"
12:04:05 INF release plan ready held=0 packages=1 releasing=1
12:04:05 INF build started package=logger stage=build version=0.1.0
12:04:05 INF added 42 packages in 1s package=logger stage=build version=0.1.0
12:04:05 INF build succeeded package=logger stage=build version=0.1.0
12:04:05 INF publish started package=logger stage=publish version=0.1.0
12:04:05 INF + logger@0.1.0 package=logger stage=publish version=0.1.0
12:04:05 INF published package=logger stage=publish tag=logger@0.1.0 version=0.1.0
12:04:05 INF summary channel=stable package=logger status=published tag=logger@0.1.0 took=1.2s version="0.0.0 -> 0.1.0"
12:04:05 INF done cancelled=0 failed=0 held=0 published=1 skipped=0 took=1.2s unchanged=0

$ git tag
logger@0.1.0
```

The annotated tag is the record that the publish happened. Push it (or let dispat push it, see
[release records](../configuration/records.md)) and the release is done.

One thing to know about versions: your `package.json` version field does not drive anything. dispat computes the version
from commits and tags, and hands it to your scripts as `$DISPAT_NEW_VERSION`. A typical build script therefore stamps it
in before packing:

```sh
npm version "$DISPAT_NEW_VERSION" --no-git-tag-version && npm ci && npm run build
```

## A single package, no monorepo

dispat is built for monorepos, but nothing requires more than one package. A repository whose whole deliverable is a
single package skips `spaces` entirely and declares one standalone `packages` entry pointing at the folder the code
lives in:

```json
{
  "scripts": {
    "build": "npm ci && npm run build",
    "publish": "npm publish --access public"
  },
  "packages": {
    "app": {
      "path": "src",
      "flow": { "build": "build", "publish": "publish" }
    }
  }
}
```

One rule to know: `path` must name a folder inside the repository, so the package cannot be the repository root itself.
Keeping the deliverable in a subfolder (`src`, `app`, whatever you already build from) is all it takes; the config, the
changelog and the tags live at the root, the scripts run inside the folder.

Commits scoped with the package name drive it, and a commit with no scope counts too when it touches files inside the
folder:

```console
$ git commit -m "feat(app): first version"
$ dispat status
09:31:07 INF ● changed bump=minor channel=stable dueToProviders=[] ownCommits=1 package=app reason=direct space=app version="0.0.0 -> 0.1.0"
09:31:07 INF release plan ready held=0 packages=1 releasing=1
```

Everything else on this page works unchanged with one package: channels, the changelog, GitHub releases, hooks,
`dispat run`. If the repository grows a second deliverable later, add another entry (or a space) and declare the edge
between them; the single-package setup is just the smallest case of the general one.

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

## A Docker image chain

Docker is the case that breaks "build everything, then publish everything": an image that starts `FROM` your base image
can only be *built* after the base image is *pushed* to the registry. The per-space
`isBuildWaitingPublish` flag states exactly that.

```json
{
  "scripts": {
    "build": "docker build -t registry.example.com/$DISPAT_PACKAGE:$DISPAT_NEW_VERSION .",
    "publish": "docker push registry.example.com/$DISPAT_PACKAGE:$DISPAT_NEW_VERSION"
  },
  "spaces": {
    "images": {
      "path": "images",
      "isBuildWaitingPublish": true,
      "flow": {
        "build": "build",
        "publish": "publish"
      }
    }
  },
  "dependencies": {
    "app": ["base"]
  }
}
```

A change to the base image, with a caret so it reaches the images built on top of it:

```console
$ git commit -m "feat(base)^: harden the base image"
$ dispat
12:04:05 INF ● changed bump=minor channel=stable dueToProviders=[] ownCommits=1 package=base reason=direct space=images version="0.0.0 -> 0.1.0"
12:04:05 INF ● changed bump=patch channel=stable dependsOn=["base"] dueToProviders=["base"] ownCommits=0 package=app reason="propagated from base" space=images version="0.0.0 -> 0.0.1"
12:04:05 INF release plan ready held=0 packages=2 releasing=2
12:04:05 INF build started package=base stage=build version=0.1.0
12:04:05 INF docker build -t registry.example.com/base:0.1.0 . package=base stage=build version=0.1.0
12:04:05 INF build succeeded package=base stage=build version=0.1.0
12:04:05 INF publish started package=base stage=publish version=0.1.0
12:04:05 INF docker push registry.example.com/base:0.1.0 package=base stage=publish version=0.1.0
12:04:05 INF published package=base stage=publish tag=base@0.1.0 version=0.1.0
12:04:05 INF version succeeded package=app stage=version version=0.0.1
12:04:05 INF build started package=app stage=build version=0.0.1
12:04:05 INF docker build -t registry.example.com/app:0.0.1 . package=app stage=build version=0.0.1
12:04:05 INF build succeeded package=app stage=build version=0.0.1
12:04:05 INF publish started package=app stage=publish version=0.0.1
12:04:05 INF docker push registry.example.com/app:0.0.1 package=app stage=publish version=0.0.1
12:04:05 INF published package=app stage=publish tag=app@0.0.1 version=0.0.1
12:04:05 INF summary channel=stable package=base status=published tag=base@0.1.0 took=1.2s version="0.0.0 -> 0.1.0"
12:04:05 INF summary channel=stable package=app status=published tag=app@0.0.1 took=1.2s version="0.0.0 -> 0.0.1"
12:04:05 INF done cancelled=0 failed=0 held=0 published=2 skipped=0 took=1.2s unchanged=0
```

Read the order: `app`'s build starts only after `docker push` of `base` finished, because the flag told the scheduler
that this space's consumers need their providers *published*, not merely built. Without the flag,
`app`'s build would have run in parallel with `base`'s push (that is the right setting for npm, where a consumer builds
against the local workspace).

That leaves the base image's version in `app`'s Dockerfile. There are two ways to keep it current, and the first is
usually the one you want.

**Let dispat write the tag.** dispat reads Dockerfiles, so the `FROM` line is a declared dependency like any other and
an `autoVersion` block reconciles it at the version stage:

```json
{
  "spaces": {
    "images": {
      "path": "images",
      "isBuildWaitingPublish": true,
      "autoVersion": {},
      "flow": { "build": "build", "publish": "publish" }
    }
  }
}
```

```dockerfile
FROM registry.example.com/base:0.1.0
```

After the run above, that line reads `registry.example.com/base:0.2.0` and every other byte of the file is untouched.
The base's package must answer to the repository name for the two to connect (`"packages": {"base": {"manifestNames":
["registry.example.com/base"]}}`), because an image is called `registry.example.com/base` while the folder is called
`base`. With that in place `dispat compute` will even propose the `app -> base` edge for you, straight off the `FROM`
line, so the `dependencies` block above need not be written by hand. The details are in
[manifests](./editing/manifests.md#docker).

**Or pass it as a build argument.** When you would rather the Dockerfile stay version-free:

```dockerfile
ARG BASE_VERSION
FROM registry.example.com/base:${BASE_VERSION}
```

dispat never rewrites an interpolated reference, so this one is left alone by design. Pass it in the build script with
`--build-arg BASE_VERSION=$DISPAT_UPDATED_BASE_NEW_VERSION`, which is set whenever base moved in this run, however it
moved. Use `$DISPAT_WORKSPACE_BASE_VERSION` when you want the version base carries whether or not it is releasing; see
[the script environment](../reference/environment.md).

**A worked example, in this repository.** dispat's own
[container images](https://github.com/yohimik/dispat/tree/main/docker) are four packages set up this way, and they go
one step further: each one's `docker-compose.yml` *is* its manifest, so the version lives there and the whole of the
build and publish stages is `docker compose build` and `docker compose push`. The same file shows both halves of this
section: a rewritten literal (`image:`) beside an interpolated reference dispat leaves alone (the channel tag).

## An Android app

Gradle projects fit the same two slots. The version travels through environment variables into Gradle properties, and
the "publish" of an app is whatever your delivery is: a Play upload, an artifact repository, an APK attached to a GitHub
release.

```json
{
  "scripts": {
    "build": "../../gradlew -p . assembleRelease -PversionName=$DISPAT_NEW_VERSION",
    "publish": "../../gradlew -p . publishReleaseBundle -PversionName=$DISPAT_NEW_VERSION"
  },
  "spaces": {
    "apps": {
      "path": "android",
      "flow": {
        "build": "build",
        "publish": "publish"
      }
    }
  }
}
```

Two Gradle-specific notes:

- Scripts run inside the package folder (here `android/<app>`), so a Gradle wrapper at the repository root sits two
  levels up: `../../gradlew`.
- `versionCode` must be a monotonically increasing integer. A simple derivation from the semantic version:
  `-PversionCode=$((MAJOR * 10000 + MINOR * 100 + PATCH))` computed in the script from `$DISPAT_NEW_VERSION`.

To attach the built bundle to the GitHub release instead, export it as an asset from the build script:

```sh
echo "DISPAT_EXPORT_GITHUB=$PWD/app/build/outputs/bundle/release/app-release.aab" >> "$DISPAT_OUTPUT"
```

## npm and Docker in one graph

The polyglot case dispat exists for: a TypeScript library, a service that uses it, and a Docker image that ships the
service. Each space brings its own scripts and its own ordering rule; the dependency edges connect them into one graph.

```json
{
  "scripts": {
    "npm-build": "npm ci && npm run build",
    "npm-publish": "npm publish --access public",
    "img-build": "docker build -t registry.example.com/$DISPAT_PACKAGE:$DISPAT_NEW_VERSION .",
    "img-publish": "docker push registry.example.com/$DISPAT_PACKAGE:$DISPAT_NEW_VERSION"
  },
  "spaces": {
    "libs": {
      "path": "packages",
      "flow": {
        "build": "npm-build",
        "publish": "npm-publish"
      }
    },
    "images": {
      "path": "images",
      "isBuildWaitingPublish": true,
      "flow": {
        "build": "img-build",
        "publish": "img-publish"
      }
    }
  },
  "dependencies": {
    "service": ["sdk"],
    "service-image": ["service"]
  }
}
```

Here `sdk` and `service` live under `packages/` (npm), `service-image` under `images/` (Docker). A
`feat(sdk)^^: ...` commit releases all three: `service` builds as soon as `sdk` has *built* (npm space, no waiting
flag), while `service-image` waits until `service` is *published*, because the image's build pulls the published
package. You declare intent once per space; the scheduler works out the rest per run.

## A pnpm workspace

Same shape as [An npm package](#an-npm-package), with three differences that come from the package manager rather than
from dispat: the lock file is the workspace root's, internal ranges use the `workspace:` protocol, and `pnpm publish`
has an opinion about the state of your git tree.

```json
{
  "scripts": {
    "pnpm-build": "pnpm install --frozen-lockfile && pnpm run build",
    "pnpm-publish": "pnpm publish --access public --no-git-checks",
    "pnpm-lock": "pnpm install --lockfile-only"
  },
  "spaces": {
    "libs": {
      "path": "packages",
      "autoVersion": {
        "enabled": true,
        "range": "workspace:*",
        "syncLock": [
          "pnpm-lock"
        ]
      },
      "flow": {
        "build": "pnpm-build",
        "publish": "pnpm-publish"
      }
    }
  },
  "commit": {
    "enabled": true,
    "include": [
      "pnpm-lock.yaml"
    ]
  }
}
```

- **The install runs from the package folder and installs the workspace anyway.** pnpm looks upward for
  `pnpm-workspace.yaml`, so a build stage does not need `-C ../..`. Locally, `--frozen-lockfile` is worth stating even
  though CI turns it on by default: it makes the stage fail on a lock file that no longer matches the manifests instead
  of quietly resolving something new mid-release.
- **`workspace:*` ranges stay as they are.** [`autoVersion`](../configuration/autoversion.md) writes each package's
  own `version` field, and `pnpm publish` substitutes that version for the `workspace:` specifier when it packs, so the
  declared ranges never need rewriting. `range: "workspace:*"` is a literal, written back verbatim, which keeps the
  protocol intact if a range is rewritten at all.
- **`pnpm-lock.yaml` is outside every package folder.** `syncLock` regenerates it after the version stage, and
  [`commit.include`](../configuration/records.md#commit) is what puts it in the release commit. Without that line the
  release commit carries rewritten manifests and a stale lock file. The one-at-a-time
  [`syncLockConcurrency`](../configuration/autoversion.md) default matters here for the same reason: every package in
  the workspace regenerates the *same* file.
- **`--no-git-checks`.** `pnpm publish` refuses to publish from a branch that is not the release branch or from a dirty
  working tree. A dispat run is exactly that situation: the version stage has just rewritten manifests, and the release
  commit is created after the publishes. The check has nothing to add: the annotated tag is the record of what shipped.

Yarn workspaces work the same way with `yarn.lock` in `commit.include` and `yarn npm publish` in the publish slot.

## Registry login, once per space

Authentication belongs to the space, not to any one package. A `login` entry runs once before the space's first publish;
every other publish of the space waits for it, and if it fails, every publish of the space fails (none of them could
have succeeded). The login runs in the space folder, so a space-local config file is always found at the same place.

```json
{
  "scripts": {
    "docker-login": "echo \"$REGISTRY_TOKEN\" | docker login registry.example.com -u ci --password-stdin"
  },
  "spaces": {
    "images": {
      "path": "images",
      "isBuildWaitingPublish": true,
      "flow": {
        "build": "img-build",
        "publish": "img-publish",
        "login": "docker-login"
      }
    }
  }
}
```

For npm the same slot typically writes an `.npmrc`:

```sh
echo "//registry.npmjs.org/:_authToken=$NPM_TOKEN" >> ~/.npmrc
```

A login script can also pass values forward (a short-lived token, say) by appending
`DISPAT_OUTPUT_<NAME>=value` lines to `$DISPAT_OUTPUT`; the space's publish scripts then read
`$DISPAT_OUTPUT_<NAME>`. See [Script outputs](../reference/environment.md#script-outputs).

## Keeping a space's exceptions inside its folder

A space that lives on its own (a sub-team's area, a vendored tree, anything you would rather not edit the root config
for) can carry its own configuration file. Drop a `dispat.json` into the space folder; its top-level object is the
space, and it overrides what the root file says field by field.

```json title="dispat.json (root)"
{
  "scripts": { "build": "npm run build", "publish": "npm publish" },
  "spaces": {
    "libs": { "path": "packages", "flow": { "build": "build", "publish": "publish" } }
  }
}
```

```json title="packages/dispat.json"
{
  "tagFormat": "libs/{name}@{version}",
  "packages": {
    "legacy": { "revertOnFail": true }
  }
}
```

Now every package of `libs` tags as `libs/<name>@<version>`, `legacy` rolls its folder back when it fails, and the root
config never had to learn either fact. The space still has to exist at the root, because that is where its `path` and
its name live; the file only adjusts it. Commands run from inside the space keep resolving to the monorepo root, so
`cd packages/legacy && dispat status` works as before.

The same entries can go in the root file instead, under the space rather than at the top level:

```json
{
  "spaces": {
    "libs": {
      "path": "packages",
      "packages": { "legacy": { "revertOnFail": true } }
    }
  }
}
```

Use whichever keeps the exception where you will look for it. When both name the same package, the nearer one wins; the
full order is [the override ladder](../configuration/packages.md#the-override-ladder).

## Two config files in one folder

Migrating from JSON to YAML, or generating one config while hand-writing another, leaves two files where dispat expects
to choose one. Without a hint it takes the first name in its list (`dispat.json`, then `dispat.yaml`, `dispat.yml`,
`dispat.toml`), which during a migration is usually the file you are trying to retire.

Name the one to skip in a `.dispatexclude` next to it:

```
# dispat.json is generated; the checked-in config is dispat.yaml
dispat.json
```

That works in the repository root, in a space folder and in a package folder, and always applies to that folder alone,
so a migration can move one folder at a time. An explicit `--config dispat.json` still loads the excluded file: the flag
is exact, which is what makes it usable for a side-by-side comparison of the two.

## Recovering from a failed run

The scenario every release tool has to answer for: a run of several packages fails in the middle. Here
`core` and its consumer `app` release together; `app`'s tests break its build after `core` already published.

```console
$ git commit -m "feat(core)^: new API, reaching the app"
$ dispat
12:04:05 INF ● changed bump=minor channel=stable dueToProviders=[] ownCommits=1 package=core reason=direct space=libs version="0.0.0 -> 0.1.0"
12:04:05 INF ● changed bump=patch channel=stable dependsOn=["core"] dueToProviders=["core"] ownCommits=0 package=app reason="propagated from core" space=libs version="0.0.0 -> 0.0.1"
12:04:05 INF release plan ready held=0 packages=2 releasing=2
12:04:05 INF build started package=core stage=build version=0.1.0
12:04:05 INF build succeeded package=core stage=build version=0.1.0
12:04:05 INF publish started package=core stage=publish version=0.1.0
12:04:05 INF version succeeded package=app stage=version version=0.0.1
12:04:05 INF build started package=app stage=build version=0.0.1
12:04:05 INF tests failed package=app stage=build version=0.0.1
12:04:05 ERR build script failed error="exit status 1" package=app stage=build version=0.0.1
12:04:05 INF published package=core stage=publish tag=core@0.1.0 version=0.1.0
12:04:05 INF summary channel=stable package=core status=published tag=core@0.1.0 took=1.2s version="0.0.0 -> 0.1.0"
12:04:05 ERR summary error="build: exit status 1" channel=stable failedStage=build package=app status=failed took=1.2s version="0.0.0 -> 0.0.1"
12:04:05 INF done cancelled=0 failed=1 held=0 published=1 skipped=0 took=1.2s unchanged=0

$ git tag
core@0.1.0
```

The run exits non-zero, `core@0.1.0` is out and tagged, `app` is not. There is no state file to repair and no version to
reconcile by hand. Fix the tests and run the same command again:

```console
$ dispat
12:04:05 WRN catch-up release at 0.0.1: discharging work already published by core@0.1.0 code=W193 package=app
12:04:05 INF plan diagnostics errors=0 warnings=1
12:04:05 INF unchanged channel=stable package=core space=libs version=0.1.0
12:04:05 INF ↻ catch-up bump=patch channel=stable dependsOn=["core"] dueToProviders=["core"] ownCommits=0 package=app reason="catch-up from core" space=libs version="0.0.0 -> 0.0.1"
12:04:05 INF version succeeded package=app stage=version version=0.0.1
12:04:05 INF build succeeded package=app stage=build version=0.0.1
12:04:05 INF published package=app stage=publish tag=app@0.0.1 version=0.0.1
12:04:05 INF done cancelled=0 failed=0 held=0 published=1 skipped=0 took=1.2s unchanged=1
```

The second run recomputed the same plan from history and configuration, saw `core@0.1.0` already recorded, and executed
only the missing half. `W193` is the marker to look for: it says `app` is releasing at exactly the version it was owed
from the earlier run, not because of any new commit. `core` is not re-released, however many times you run this. If
instead a *provider* fails, its consumers are skipped and reported with
`W194`, and the same re-run catches them up once the provider is fixed.

An interrupted run (Ctrl-C, a killed CI job) follows the same rule: packages whose publish completed keep their record,
everything else is reported as `cancelled`, and the next run picks up exactly the remainder.

## Fixing a commit message after it is pushed

A commit went in as a breaking feature and it was not one. The message is already on the remote, so amending it is off
the table, and the release it would cause is a major nobody wants.

```console
$ git log --oneline -1
b9fb604 feat(core)!: rewrite internals

$ dispat status
12:04:05 INF unchanged channel=stable package=app space=libs version=0.1.0
12:04:05 INF ● changed bump=major channel=stable ownCommits=1 package=core reason=direct space=libs version="0.1.0 -> 1.0.0"
12:04:05 INF release plan ready held=0 packages=2 releasing=1
```

Write the message you should have written, and point it at the one you did. The commit needs no files of its own:

```console
$ git commit --allow-empty -m "fix(core): rewrite internals

The change is a refactor with a defensive fix, not a breaking feature.

Edits: b9fb604"

$ dispat status
12:04:05 INF unchanged channel=stable package=app space=libs version=0.1.0
12:04:05 INF ● changed bump=patch channel=stable corrected=1 ownCommits=1 package=core reason=direct space=libs version="0.1.0 -> 0.1.1"
12:04:05 INF release plan ready held=0 packages=2 releasing=1
```

`core` is back to a patch, and `corrected=1` says one entry in this release replaces an earlier record. The changelog
carries the new description and names what it stands in for:

```markdown
## core@0.1.1

### Fixes

- rewrite internals (corrects b9fb60428723)
The change is a refactor with a defensive fix, not a breaking feature.
```

Two things decide whether this works. The correction has to point at an **earlier** commit, and the record has to be
**unreleased**. If `core@1.0.0` had already gone out, the correction would be a no-op and dispat would say so with
`W209` rather than quietly doing nothing.

If the record should not exist at all rather than say something else, use `Deletes:` and a type that bumps nothing:

```console
$ git commit --allow-empty -m "chore(core): that release note was invented

Deletes: b9fb604"
```

The full set of rules, including correcting a record for one package out of several and undoing a correction you got
wrong, is in [Correcting a release record](../reference/corrections.md).

## A beta channel: try, iterate, graduate

A risky rewrite goes out on a prerelease channel first. The channel is declared in the commit; the publish script sees
it as `$DISPAT_CHANNEL` (here used as the npm dist-tag, so beta users opt in and `latest` stays untouched).

```console
$ git commit -m "feat(core)%beta: risky rewrite, try it on beta first"
$ dispat
12:04:05 INF ● changed bump=minor channel="stable -> beta" dueToProviders=[] ownCommits=1 package=core reason=direct space=libs version="0.1.0 -> 0.2.0-beta.0"
12:04:05 INF npm publish --tag beta package=core stage=publish version=0.2.0-beta.0
12:04:05 INF published package=core stage=publish tag=core@0.2.0-beta.0 version=0.2.0-beta.0
```

Feedback arrives; ordinary commits keep the train rolling:

```console
$ git commit -m "fix(core): beta feedback"
$ dispat
12:04:05 INF published package=core stage=publish tag=core@0.2.0-beta.1 version=0.2.0-beta.1
```

The target version is recomputed from the whole train on every run, so a breaking change arriving mid-train moves the
target (to `1.0.0-beta.N`), which is exactly what it should do. When it is ready:

```console
$ git commit --allow-empty -m "release(core)%beta>stable: graduate"
$ dispat
12:04:05 INF ● changed bump=minor channel="beta -> stable" dueToProviders=[] ownCommits=2 package=core reason=direct space=libs version="0.2.0-beta.1 -> 0.2.0"
12:04:05 INF npm publish --tag stable package=core stage=publish version=0.2.0
12:04:05 INF published package=core stage=publish tag=core@0.2.0 version=0.2.0

$ git tag
core@0.1.0
core@0.2.0
core@0.2.0-beta.0
core@0.2.0-beta.1
```

The graduation releases `0.2.0`: the betas' accumulated work, minus the prerelease marker. Nothing on the train is
counted twice. The full channel rules, including bringing consumers onto a train with `++N` and graduating a whole train
at once, are in [Commit messages](../reference/commits.md#channels-and-prereleases) and
[Concepts](../concepts.md#prereleases-and-channels); how a channel is spelled inside a tag is
[`tagFormat`](../configuration/versions.md#tagformat)'s business.
