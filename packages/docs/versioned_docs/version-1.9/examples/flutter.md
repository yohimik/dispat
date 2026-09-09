# A Flutter app and its packages

Build a Flutter app from Dart packages published to pub.dev. dispat manages the app's build number separately from its
version.

## The layout

```
packages/core/pubspec.yaml    acme_core 1.2.0
apps/app/pubspec.yaml         acme_app 0.4.1+41, depends on acme_core
dispat.json
```

`0.4.1+41` is two numbers in one string. The version is `0.4.1`, and `+41` is the build number the stores use to tell
one upload from another. dispat treats them as what they are. The release moves the version, and a separate write moves
the counter.

## The configuration

```json title="dispat.json"
{
  "scripts": {
    "set-build": "dispat writer pubspec.yaml --set-build \"$GITHUB_RUN_NUMBER\"",
    "build-app": "flutter build appbundle --build-name $DISPAT_NEW_VERSION",
    "publish-pkg": "dart pub publish --force"
  },
  "spaces": {
    "packages": {
      "path": "packages",
      "flow": {"publish": "publish-pkg"},
      "autoVersion": {"enabled": true}
    },
    "apps": {
      "path": "apps",
      "flow": {"beforeBuild": "set-build", "build": "build-app"},
      "autoVersion": {"enabled": true}
    }
  }
}
```

You define two spaces because the two halves ship differently. You publish a package to pub.dev, and you build an app
into a bundle for a store. They still sit in one dependency graph, so dispat reconciles the app's `acme_core`
constraint before it builds.

The `set-build` script runs in `flow.beforeBuild`, after the version stage writes the new version and before the bundle
is produced. That order matters. The version write preserves the existing build number, and this script then replaces
it with the CI run number.

## A release

```console
$ git commit -m "feat(core)^: offline cache"
$ dispat status
12:45:22 INF ● changed baselineFromInitials=true bump=minor channel=stable dueToProviders=[] ownCommits=1 package=core reason=direct space=packages version="1.2.0 -> 1.3.0"
12:45:22 INF ● changed baselineFromInitials=true bump=patch channel=stable dependsOn=["core"] dueToProviders=["core"] ownCommits=0 package=app reason="propagated from core" space=apps version="0.4.1 -> 0.4.2"
12:45:22 INF release plan ready held=0 packages=2 releasing=2

$ dispat autoversion
12:45:22 INF manifest reconciled manifest=pubspec.yaml package=core ranges=0 stage=version versionWritten=true
12:45:22 WRN manifest version disagrees with the baseline; writing the computed version baseline=0.4.1 code=W192 manifest=pubspec.yaml manifestVersion=0.4.1+41 package=app stage=version
12:45:22 INF manifest reconciled manifest=pubspec.yaml package=app ranges=1 stage=version versionWritten=true
12:45:22 INF auto-versioning finished failed=0 ran=2 skipped=0 stage=autoversion
```

The `W192` code says the manifest and the tags disagreed, and the tags won. You see this on every Flutter app whose
version carries a build suffix, because `0.4.1+41` is not the same string as the baseline `0.4.1`. Nothing is lost. The
counter survives the write.

```yaml title="apps/app/pubspec.yaml, after the version stage"
name: acme_app
version: 0.4.2+41

environment:
  sdk: ">=3.5.0 <4.0.0"

dependencies:
  acme_core: ^1.3.0
  http: ^1.2.2
  flutter:
    sdk: flutter

dev_dependencies:
  test: ^1.25.8
```

## What dispat reads and writes

```console
$ dispat scanner
apps/app/pubspec.yaml  pub  acme_app@0.4.1+41  build 41
  dependencies     acme_core  ^1.2.0
  dependencies     flutter    
  dependencies     http       ^1.2.2
  devDependencies  test       ^1.25.8
packages/core/pubspec.yaml  pub  acme_core@1.2.0
2 manifest(s), 4 dependency declaration(s)
```

The identity is `name` and `version`, with the `+N` suffix reported separately as `build 41`. dispat reads
`dependencies` and `dev_dependencies`, and it folds `dependency_overrides` in. A dependency written as a block rather
than a string has no version to show and none to write. This applies to `flutter: {sdk: flutter}` or a `git:` entry, so
they appear with an empty range and dispat skips them during writes.

Run these commands for local development against a neighbouring package. dispat writes and removes the same
`dependency_overrides` for you:

```sh
dispat autowriter --since all --link-local     # path overrides in
dispat autowriter --since all --unlink-local   # and back out before publishing
dispat scanner --verify-unlinked               # E215 if one survived
```

A `dependency_overrides` block reaching pub.dev is the classic Flutter monorepo accident. The gate above is the cheap
way never to have that conversation.

## Worth knowing

- **The store build number must only ever increase.** The `$GITHUB_RUN_NUMBER` variable is monotonic per workflow and
  costs nothing. A value derived from the version is fine too, as long as it never goes backwards. Android additionally
  requires an integer, and dispat refuses a value that is not one before touching the file.
- **`dart pub publish` is final.** You can retract a published version for 7 days, but you can never replace it.
- **`--force` skips the confirmation prompt.** A stage has no terminal, so an interactive publish hangs the run.
- **`pubspec_overrides.yaml` is not read.** dispat writes overrides into the pubspec itself. This is the file both the
  tooling and the reader agree on.

## See also

- [An Android app](./android.md) for the Gradle side of a mobile release.
- [An iOS app and a CocoaPods library](./apple.md) for the Apple side.
- [autoVersion](../configuration/autoversion.md) for what the version stage reconciles.
