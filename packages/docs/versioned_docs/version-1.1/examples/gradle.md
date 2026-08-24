# A Gradle library and its version catalog

You can release a published JVM library, an app that consumes it, and their shared `gradle/libs.versions.toml` file in
one run. They all move to the same new version together.

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

Three lines carry most of the weight. Each has a specific purpose.

**`manifestNames`.** Set this to connect your package folder to its Gradle coordinate. A Gradle build script declares
dependencies as coordinates like `com.acme:core`, but your package sits in a folder called `core`. Stating the
coordinate tells dispat to recognize a declaration anywhere in the repository as this package.

**`range: exact`.** Set this because a Gradle coordinate takes a plain version. The default policy writes `^1.3.0`.
That is npm's spelling, and it means nothing to Gradle.

**The `replace` rule.** Use a literal find-and-write because a top-level `version = "1.2.0"` in `build.gradle.kts` is a
plain Kotlin assignment. It is not a manifest field, so the parsing strategy has nothing to grip. The run fills in
`{previous}` and `{version}` automatically.

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

Look at `ranges=1` in the output for the app. This shows the coordinate in its `dependencies { }` block moving to
`com.acme:core:1.3.0`. The `file reconciled` log on the library means the `replace` rule wrote `version = "1.3.0"` into
its build script.

The catalog belongs to the repository rather than to any one package. It moves once during the `beforeAll` stage.

```toml
[versions]
acme-core = "1.3.0"
okhttp = "4.12.0"

[libraries]
acme-core = { module = "com.acme:core", version.ref = "acme-core" }
okhttp = { module = "com.squareup.okhttp3:okhttp", version.ref = "okhttp" }
```

Notice that the `[libraries]` entry remains untouched. dispat followed the `version.ref` to the `[versions]` table and
wrote the number where it actually lives. This is the only edit that preserves the reference.

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

**In a catalog**, dispat reads both spellings of a library: the `group:artifact:version` shorthand string and the table
form with `module` or `group` plus `name`. It resolves a version behind `version.ref` through `[versions]` on the way
in and writes it back there on the way out. Bundles and plugins are left alone because they are not dependencies.

**In a build script**, dispat treats a literal coordinate in the `dependencies { }` block as a dependency. A catalog
accessor like `libs.okhttp` is not a dependency here, because the version lives in the catalog where dispat edits it.
It reads a project dependency like `project(":core")` as a link to that module.

**Identity** comes from `applicationId`, falling back to `namespace`. dispat uses `versionName` as the version and
`versionCode` as a build counter. A library module that declares neither has no identity of its own, like `libs/core`
above, which is exactly why `manifestNames` is in the configuration.

## Worth knowing

- **`versionCode` is never written by a version write.** It is a counter rather than a version, so it moves once per
  build regardless of the release. The command to move it insists on an integer, so provide one when you run
  `dispat writer <file> --set-build "$GITHUB_RUN_NUMBER"`.
- **One `[versions]` entry shared by two libraries cannot take two different versions.** Give each library its own
  entry. If two edits land on the same `version.ref`, dispat refuses the write rather than silently picking one.
- **A version assembled from properties is left alone.** An assignment like `version = project.property("acmeVersion")`
  is an intentional indirection. Point a `replace` rule at `gradle.properties` instead.
- **The catalog is at the repository root**, outside every package folder. List it under
  [`commit.include`](../configuration/records.md#commit). This ensures the release commit carries the change made in
  `beforeAll`.

## See also

- [An Android app](./android.md) covers the app side. This includes `versionCode` and a bundle on the GitHub release.
- [Maven modules](./java.md) covers the other JVM build tool.
- [Replacing text across the monorepo](../editing/autoreplacer.md) explains how to handle versions no parser owns.
