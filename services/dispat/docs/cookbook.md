# Cookbook

Complete, copy-ready setups for the most common stacks, each with the config, the scripts people actually
write, and the terminal output of a real run. Every transcript on this page was produced by running dispat
against a throwaway repository; only timestamps and durations are normalized. Script output lines (the
`npm`/`docker` lines) come from your own commands, so yours will differ.

If a term is new, [Concepts](concepts.md) defines all of them in a few minutes of reading.

- [An npm package](#an-npm-package)
- [A Docker image chain](#a-docker-image-chain)
- [An Android app](#an-android-app)
- [npm and Docker in one graph](#npm-and-docker-in-one-graph)
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
      "flow": { "build": "build", "publish": "publish" }
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
[release records](configuration/records.md)) and the release is done.

One thing to know about versions: your `package.json` version field does not drive anything. dispat computes
the version from commits and tags, and hands it to your scripts as `$DISPAT_NEW_VERSION`. A typical build
script therefore stamps it in before packing:

```sh
npm version "$DISPAT_NEW_VERSION" --no-git-tag-version && npm ci && npm run build
```

## A Docker image chain

Docker is the case that breaks "build everything, then publish everything": an image that starts `FROM` your
base image can only be *built* after the base image is *pushed* to the registry. The per-space
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
      "flow": { "build": "build", "publish": "publish" }
    }
  },
  "dependencies": [
    { "consumer": "app", "provider": "base" }
  ]
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

Read the order: `app`'s build starts only after `docker push` of `base` finished, because the flag told the
scheduler that this space's consumers need their providers *published*, not merely built. Without the flag,
`app`'s build would have run in parallel with `base`'s push (that is the right setting for npm, where a
consumer builds against the local workspace). In a Dockerfile, pin the base by the version dispat provides:

```dockerfile
ARG BASE_VERSION
FROM registry.example.com/base:${BASE_VERSION}
```

and pass it in the build script with `--build-arg BASE_VERSION=$DISPAT_UPDATED_BASE_NEW_VERSION` (falling
back to `$DISPAT_WORKSPACE_BASE_VERSION` when base is not part of this run; see
[the script environment](environment.md)).

## An Android app

Gradle projects fit the same two slots. The version travels through environment variables into Gradle
properties, and the "publish" of an app is whatever your delivery is: a Play upload, an artifact repository,
an APK attached to a GitHub release.

```json
{
  "scripts": {
    "build": "../../gradlew -p . assembleRelease -PversionName=$DISPAT_NEW_VERSION",
    "publish": "../../gradlew -p . publishReleaseBundle -PversionName=$DISPAT_NEW_VERSION"
  },
  "spaces": {
    "apps": {
      "path": "android",
      "flow": { "build": "build", "publish": "publish" }
    }
  }
}
```

Two Gradle-specific notes:

- Scripts run inside the package folder (here `android/<app>`), so a Gradle wrapper at the repository root
  sits two levels up: `../../gradlew`.
- `versionCode` must be a monotonically increasing integer. A simple derivation from the semantic version:
  `-PversionCode=$((MAJOR * 10000 + MINOR * 100 + PATCH))` computed in the script from `$DISPAT_NEW_VERSION`.

To attach the built bundle to the GitHub release instead, export it as an asset from the build script:

```sh
echo "DISPAT_EXPORT_GITHUB=$PWD/app/build/outputs/bundle/release/app-release.aab" >> "$DISPAT_OUTPUT"
```

## npm and Docker in one graph

The polyglot case dispat exists for: a TypeScript library, a service that uses it, and a Docker image that
ships the service. Each space brings its own scripts and its own ordering rule; the dependency edges connect
them into one graph.

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
      "flow": { "build": "npm-build", "publish": "npm-publish" }
    },
    "images": {
      "path": "images",
      "isBuildWaitingPublish": true,
      "flow": { "build": "img-build", "publish": "img-publish" }
    }
  },
  "dependencies": [
    { "consumer": "service", "provider": "sdk" },
    { "consumer": "service-image", "provider": "service" }
  ]
}
```

Here `sdk` and `service` live under `packages/` (npm), `service-image` under `images/` (Docker). A
`feat(sdk)^^: ...` commit releases all three: `service` builds as soon as `sdk` has *built* (npm space,
no waiting flag), while `service-image` waits until `service` is *published*, because the image's build
pulls the published package. You declare intent once per space; the scheduler works out the rest per run.

## Registry login, once per space

Authentication belongs to the space, not to any one package. A `login` entry runs once before the space's
first publish; every other publish of the space waits for it, and if it fails, every publish of the space
fails (none of them could have succeeded). The login runs in the space folder, so a space-local config file
is always found at the same place.

```json
{
  "scripts": {
    "docker-login": "echo \"$REGISTRY_TOKEN\" | docker login registry.example.com -u ci --password-stdin"
  },
  "spaces": {
    "images": {
      "path": "images",
      "isBuildWaitingPublish": true,
      "flow": { "build": "img-build", "publish": "img-publish", "login": "docker-login" }
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
`$DISPAT_OUTPUT_<NAME>`. See [Script outputs](environment.md#script-outputs).

## Recovering from a failed run

The scenario every release tool has to answer for: a run of several packages fails in the middle. Here
`core` and its consumer `app` release together; `app`'s tests break its build after `core` already
published.

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

The run exits non-zero, `core@0.1.0` is out and tagged, `app` is not. There is no state file to repair and
no version to reconcile by hand. Fix the tests and run the same command again:

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

The second run recomputed the same plan from history and configuration, saw `core@0.1.0` already recorded,
and executed only the missing half. `W193` is the marker to look for: it says `app` is releasing at exactly
the version it was owed from the earlier run, not because of any new commit. `core` is not re-released,
however many times you run this. If instead a *provider* fails, its consumers are skipped and reported with
`W194`, and the same re-run catches them up once the provider is fixed.

An interrupted run (Ctrl-C, a killed CI job) follows the same rule: packages whose publish completed keep
their record, everything else is reported as `cancelled`, and the next run picks up exactly the remainder.

## A beta channel: try, iterate, graduate

A risky rewrite goes out on a prerelease channel first. The channel is declared in the commit; the publish
script sees it as `$DISPAT_CHANNEL` (here used as the npm dist-tag, so beta users opt in and `latest` stays
untouched).

```console
$ git commit -m "feat(core)@beta: risky rewrite, try it on beta first"
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

The target version is recomputed from the whole train on every run, so a breaking change arriving mid-train
moves the target (to `1.0.0-beta.N`), which is exactly what it should do. When it is ready:

```console
$ git commit --allow-empty -m "release(core)@beta>stable: graduate"
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

The graduation releases `0.2.0`: the betas' accumulated work, minus the prerelease marker. Nothing on the
train is counted twice. The full channel rules, including bringing consumers onto a train with `++N` and
graduating a whole train at once, are in [Commit messages](commits.md) and
[Versions and channels](configuration/versions.md).
