# A Gradle library and its version catalog

A published JVM library, an app that consumes it, and the `gradle/libs.versions.toml` both of them share, all moving
to the same new version in one run.

## The layout

```
gradle/libs.versions.toml      the shared version catalog, at the repository root
libs/core/build.gradle.kts     com.acme:core, published with maven-publish
apps/app/build.gradle.kts      the Android app that depends on it
dispat.json
```

## The configuration

```json title="dispat.json"
{
  "scripts": {
    "pin-catalog": "dispat writer gradle/libs.versions.toml --set com.acme:core=$DISPAT_WORKSPACE_CORE_VERSION",
    "publish-lib": "./gradlew :libs:core:publish -Pversion=$DISPAT_NEW_VERSION",
    "build-app": "./gradlew assembleRelease"
  },
  "run": {"beforeAll": "pin-catalog"},
  "spaces": {
    "libs": {
      "path": "libs",
      "flow": {"publish": "publish-lib"},
      "autoVersion": {
        "enabled": true,
        "manifests": "none",
        "replace": [
          {"files": ["build.gradle.kts"], "find": "version = \"{previous}\"", "write": "version = \"{version}\""}
        ]
      }
    },
    "apps": {
      "path": "apps",
      "flow": {"build": "build-app"},
      "autoVersion": {"enabled": true, "range": "exact"}
    }
  },
  "packages": {
    "core": {"manifestNames": ["com.acme:core"]}
  }
}
```

Three lines carry most of the weight, and each has a reason.

**`manifestNames`.** A Gradle build script declares dependencies as coordinates, `com.acme:core`, while your package is
a folder called `core`. Stating the coordinate connects the two, so a declaration anywhere in the repository is
recognised as this package.

**`range: exact`.** The default policy writes `^1.3.0`, which is npm's spelling and means nothing to Gradle. A
coordinate takes a plain version, so say so.

**The `replace` rule.** A `build.gradle.kts` that declares `version = "1.2.0"` at the top level is a plain Kotlin
assignment, not a manifest field, so the parsing strategy has nothing to grip. A literal find-and-write does the job,
with `{previous}` and `{version}` filled in by the run.

## A release

```console
$ git commit -m "feat(core)^: coroutine-friendly api"
$ dispat
12:46:52 INF release started root=.
12:46:52 INF ● changed baselineFromInitials=true bump=minor channel=stable dueToProviders=[] ownCommits=1 package=core reason=direct space=libs version="1.2.0 -> 1.3.0"
12:46:52 INF ● changed baselineFromInitials=true bump=patch channel=stable dependsOn=["core"] dueToProviders=["core"] ownCommits=0 package=app reason="propagated from core" space=apps version="0.4.1 -> 0.4.2"
12:46:52 INF release plan ready held=0 packages=2 releasing=2
12:46:52 INF gradle/libs.versions.toml stage=beforeAll
12:46:52 INF   applied  dependencies  com.acme:core  1.3.0 stage=beforeAll
12:46:52 INF 1 manifest(s): 1 applied, 0 skipped, 0 missing stage=beforeAll
12:46:52 INF file reconciled file=build.gradle.kts occurrences=1 package=core stage=version version=1.3.0
12:46:52 INF version succeeded package=core stage=version version=1.3.0
12:46:52 INF publish started package=core stage=publish version=1.3.0
12:46:52 INF ./gradlew :libs:core:publish -Pversion=1.3.0 package=core stage=publish version=1.3.0
12:46:52 INF manifest reconciled manifest=build.gradle.kts package=app ranges=1 stage=version version=0.4.2 versionWritten=true
12:46:52 INF version succeeded package=app stage=version version=0.4.2
12:46:52 INF build started package=app stage=build version=0.4.2
12:46:52 INF build succeeded package=app stage=build version=0.4.2
12:46:52 INF published package=core stage=publish tag=core@1.3.0 version=1.3.0
12:46:52 INF published package=app stage=publish tag=app@0.4.2 version=0.4.2
12:46:52 INF done cancelled=0 failed=0 held=0 published=2 skipped=0 took=0.5s unchanged=0
```

`ranges=1` on the app is the coordinate in its `dependencies { }` block moving to `com.acme:core:1.3.0`, and
`file reconciled` on the library is the `replace` rule writing `version = "1.3.0"` into its build script.

The catalog moves once, in `beforeAll`, because it belongs to the repository rather than to any one package:

```toml
[versions]
acme-core = "1.3.0"
okhttp = "4.12.0"

[libraries]
acme-core = { module = "com.acme:core", version.ref = "acme-core" }
okhttp = { module = "com.squareup.okhttp3:okhttp", version.ref = "okhttp" }
```

The `[libraries]` entry is untouched. dispat followed the `version.ref` to the `[versions]` table and wrote the number
where it actually lives, which is the only edit that does not break the reference.

## What dispat reads and writes

```console
$ dispat scanner
apps/app/build.gradle.kts  gradle  com.acme.app@0.4.1  build 41
  dependencies  com.acme:core  1.2.0
gradle/libs.versions.toml  gradle
  dependencies  com.acme:core                1.2.0
  dependencies  com.squareup.okhttp3:okhttp  4.12.0
libs/core/build.gradle.kts  gradle
3 manifest(s), 3 dependency declaration(s)
```

**In a catalog**, both spellings of a library are read: the `group:artifact:version` shorthand string and the table
form with `module` or `group` plus `name`. A version behind `version.ref` is resolved through `[versions]` on the way
in and written back there on the way out. Bundles and plugins are not dependencies and are left alone.

**In a build script**, a literal coordinate in the `dependencies { }` block is a dependency. A catalog accessor,
`libs.okhttp`, is not: the version is in the catalog, and that is where dispat edits it. A project dependency,
`project(":core")`, is read as a link to that module.

**Identity** comes from `applicationId`, falling back to `namespace`, with `versionName` as the version and
`versionCode` as a build counter. A library module that declares neither, like `libs/core` above, has no identity of
its own, which is exactly why `manifestNames` is in the configuration.

## Worth knowing

- **`versionCode` is never written by a version write.** It is a counter, not a version: it moves once per build,
  whatever the release decides. `dispat writer <file> --set-build "$GITHUB_RUN_NUMBER"` is the write that moves it,
  and it insists on an integer.
- **One `[versions]` entry shared by two libraries cannot take two different versions.** If two edits land on the
  same `version.ref`, the write is refused rather than silently picking one. Give each library its own entry.
- **A version assembled from properties is left alone.** `version = project.property("acmeVersion")` is an
  indirection somebody set up on purpose; point a `replace` rule at `gradle.properties` instead.
- **The catalog is at the repository root**, outside every package folder. List it under
  [`commit.include`](../configuration/records.md#commit) so the release commit carries the change made in `beforeAll`.

## See also

- [An Android app](./android.md) for the app side, including `versionCode` and a bundle on the GitHub release.
- [Maven modules](./java.md) for the other JVM build tool.
- [Replacing text across the monorepo](../editing/autoreplacer.md) for versions no parser owns.
