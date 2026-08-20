# Maven modules

Java modules in one repository, each with its own `pom.xml`, versioned from commits and deployed in dependency order.

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

The version stage writes the new version into each `pom.xml` before `mvn` ever runs, so the artifact Maven builds
already carries the right coordinates. No `-Dversion` flag, no `versions:set`, no `${revision}` property.

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

The scopes in your commits are the folder names, `core` and `api`, not the Maven coordinates. If you would rather
write `feat(com.acme:core)`, state that coordinate under [`manifestNames`](../configuration/packages.md#manifestnames)
for the package.

## What dispat reads and writes

A Maven manifest is identified by `groupId:artifactId`, and a module that omits its `groupId` inherits the parent's:

```console
$ dispat scanner
libs/core/pom.xml  maven  com.acme:core@1.2.0
services/api/pom.xml  maven  com.acme:api@0.4.1
  dependencies     com.acme:core                                1.2.0
  dependencies     com.fasterxml.jackson.core:jackson-databind  ${jackson.version}
  devDependencies  org.junit.jupiter:junit-jupiter              5.11.0
2 manifest(s), 3 dependency declaration(s)
```

`test` scope is reported as a dev dependency, and `optional` as an optional one. A version written as a property is
read exactly as it is written, and it is the one thing writes will not touch:

```console
$ dispat writer services/api/pom.xml --set-version 0.5.0 --set com.acme:core=1.3.0 --set com.fasterxml.jackson.core:jackson-databind=2.18.1
services/api/pom.xml
  version written
  applied  dependencies  com.acme:core  1.3.0
  skipped  dependencies  com.fasterxml.jackson.core:jackson-databind  2.18.1
1 manifest(s): 1 applied, 1 skipped, 0 missing
```

`${jackson.version}` is an indirection somebody set up on purpose, and replacing it with a literal would break it.
Skipped is the healthy answer, and it never fails a release. The same rule protects `<parent><version>`: dispat writes
a project's own `<version>` and never its parent's.

Note the colon in `--set com.acme:core=1.3.0`. A prefix before the colon is read as a manifest field only when it is
one of `dependencies`, `devDependencies`, `peerDependencies` or `optionalDependencies`, so Maven coordinates keep
their own colon and need no escaping.

## If your modules inherit their version from a parent

Many multi-module builds give each module `<version>` through the parent instead of declaring one. dispat then has
nothing to write in the module, so put the parent's version in the version stage yourself:

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

A `flow.version` script and `autoVersion` can coexist: the block reconciles what it can parse, then the script runs
and sees the already-reconciled files.

## Worth knowing

- **Maven Central is append-only and slow to sync.** A release that fails after the deploy has spent that version.
  Fix forward and let the next run take the next number.
- **Credentials live in `settings.xml`.** In CI, write it in a step before dispat, or in a
  [`flow.login`](./login.md) script so it happens once per space.
- **`SNAPSHOT` versions are outside all of this.** dispat computes release versions; if you also publish snapshots
  from every commit, keep that as a separate job that never runs `dispat`.
- **`mvn -B` matters.** A stage runs without a terminal, and a Maven that decides to be interactive will hang the run.

## See also

- [A Gradle library and its version catalog](./gradle.md) for the other JVM build tool.
- [autoVersion](../configuration/autoversion.md) for what the version stage reconciles.
- [Release steps](../reference/releasing/steps.md) for running the pieces of a release by hand.
