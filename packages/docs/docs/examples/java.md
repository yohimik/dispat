# Maven modules

Keep your Java modules in one repository with a `pom.xml` for each. dispat versions them from commits and deploys them
in dependency order.

## The layout

```
libs/core/pom.xml         com.acme:core 1.2.0
services/api/pom.xml      com.acme:api 0.4.1, depends on core
dispat.json
```

## The configuration

```json title="dispat.json"
{
  "scripts": {
    "verify": "mvn -B verify",
    "deploy": "mvn -B deploy"
  },
  "spaces": {
    "libs": {
      "path": "libs",
      "flow": {"build": "verify", "publish": "deploy"},
      "autoVersion": {"enabled": true}
    },
    "services": {
      "path": "services",
      "flow": {"build": "verify", "publish": "deploy"},
      "autoVersion": {"enabled": true}
    }
  }
}
```

dispat writes the new version into each `pom.xml` during the version stage. It does this before `mvn` ever runs, so the
artifact Maven builds already carries the right coordinates. You do not need a `-Dversion` flag, `versions:set`, or a
`${revision}` property.

## A release

```console
$ dispat compute --write
+ add     api -> core (dependencies)  services/api/pom.xml dependencies "com.acme:core": "1.2.0"
+ initial api 0.4.1  services/api/pom.xml declares 0.4.1; no release tag yet
+ initial core 1.2.0  libs/core/pom.xml declares 1.2.0; no release tag yet

applied 3 change(s) to dispat.json (previous copies carry the .backup suffix)

$ git commit -m "feat(core)^: add the retry policy"
$ dispat status
12:41:17 INF ● changed baselineFromInitials=true bump=minor channel=stable dueToProviders=[] ownCommits=1 package=core reason=direct space=libs version="1.2.0 -> 1.3.0"
12:41:17 INF ● changed baselineFromInitials=true bump=patch channel=stable dependsOn=["core"] dueToProviders=["core"] ownCommits=0 package=api reason="propagated from core" space=services version="0.4.1 -> 0.4.2"
12:41:17 INF release plan ready held=0 packages=2 releasing=2
```

Write the folder names, `core` and `api`, as the scopes in your commits. Do not use the Maven coordinates. If you
prefer to write `feat(com.acme:core)`, set that coordinate in
[`manifestNames`](../configuration/packages.md#manifestnames) for the package.

## What dispat reads and writes

dispat identifies a Maven manifest by its `groupId:artifactId`. A module inherits the parent's group if it omits its
own `groupId`:

```console
$ dispat scanner
libs/core/pom.xml  maven  com.acme:core@1.2.0
services/api/pom.xml  maven  com.acme:api@0.4.1
  dependencies     com.acme:core                                1.2.0
  dependencies     com.fasterxml.jackson.core:jackson-databind  ${jackson.version}
  devDependencies  org.junit.jupiter:junit-jupiter              5.11.0
2 manifest(s), 3 dependency declaration(s)
```

dispat reports a `test` scope as a dev dependency, and an `optional` scope as an optional one. It reads a version
written as a property exactly as it is written. Writes will not touch that property:

```console
$ dispat writer services/api/pom.xml --set-version 0.5.0 --set com.acme:core=1.3.0 --set com.fasterxml.jackson.core:jackson-databind=2.18.1
services/api/pom.xml
  version written
  applied  dependencies  com.acme:core  1.3.0
  skipped  dependencies  com.fasterxml.jackson.core:jackson-databind  2.18.1
1 manifest(s): 1 applied, 1 skipped, 0 missing
```

You set up `${jackson.version}` as an indirection on purpose, so replacing it with a literal would break it. Skipped is
the healthy answer and never fails a release. The same rule protects `<parent><version>`, because dispat writes a
project's own `<version>` and never its parent's.

Look at the colon in `--set com.acme:core=1.3.0`. dispat reads a prefix before the colon as a manifest field only when
it matches `dependencies`, `devDependencies`, `peerDependencies` or `optionalDependencies`. This means Maven
coordinates keep their own colon and need no escaping.

## If your modules inherit their version from a parent

Many multi-module builds give each module its `<version>` through the parent instead of declaring one. dispat then has
nothing to write in the module. Put the parent's version in the version stage yourself:

```json title="dispat.json (parent-managed versions)"
{
  "scripts": {
    "set-version": "mvn -B versions:set -DnewVersion=$DISPAT_NEW_VERSION -DprocessAllModules=true -DgenerateBackupPoms=false"
  },
  "spaces": {
    "libs": {
      "path": "libs",
      "flow": {"version": "set-version", "build": "verify", "publish": "deploy"}
    }
  }
}
```

You can use a `flow.version` script and `autoVersion` together. The block reconciles what it can parse. The script then
runs and sees the already-reconciled files.

## Worth knowing

- **Maven Central is append-only and slow to sync.** A release spends that version if it fails after the deploy. Fix
  forward and let the next run take the next number.
- **Credentials live in `settings.xml`.** Write this file in a CI step before you run dispat. You can also write it in
  a [`flow.login`](./login.md) script so it happens once per space.
- **`SNAPSHOT` versions are outside all of this.** dispat computes release versions. Keep your snapshot publishes in a
  separate job that never runs `dispat`.
- **`mvn -B` matters.** A stage runs without a terminal. Maven hangs the run if it decides to be interactive.

## See also

- [A Gradle library and its version catalog](./gradle.md) for the other JVM build tool.
- [autoVersion](../configuration/autoversion.md) explains what the version stage reconciles.
- [Release steps](../reference/releasing/steps.md) shows how to run the pieces of a release by hand.
