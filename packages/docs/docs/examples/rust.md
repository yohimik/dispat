# A Cargo workspace

Crates in one workspace, versioned from commits and published to crates.io in dependency order, with the `path`
dependencies between them rewritten to real versions before anything is uploaded.

## The layout

```
crates/core/Cargo.toml    acme-core 1.2.0
crates/app/Cargo.toml     acme-app 0.4.1, depends on acme-core
Cargo.lock                the workspace lock, at the root
dispat.json
```

## The configuration

```json title="dispat.json"
{
  "scripts": {
    "test": "cargo test",
    "publish": "cargo publish --allow-dirty"
  },
  "spaces": {
    "crates": {
      "path": "crates",
      "flow": {"build": "test", "publish": "publish"},
      "autoVersion": {"enabled": true, "range": "caret"}
    }
  },
  "commit": {"include": ["Cargo.lock"]}
}
```

`--allow-dirty` is not carelessness. The version stage has just written the new version into `Cargo.toml`, so the
working tree is dirty by design when the publish runs; the release commit at the end of the run is what records those
edits. `Cargo.lock` lives at the workspace root, outside every package folder, so it is named under `commit.include`
to reach the same commit.

## Starting from the versions already in the files

Cargo manifests declare their versions, so [`dispat compute`](../cli/compute.md) can derive both halves of the setup at
once: the edges between crates, and the baseline each crate starts from.

```console
$ dispat compute --write
+ add     app -> core (dependencies)  crates/app/Cargo.toml dependencies "acme-core": "1.2.0"
+ initial app 0.4.1  crates/app/Cargo.toml declares 0.4.1; no release tag yet
+ initial core 1.2.0  crates/core/Cargo.toml declares 1.2.0; no release tag yet

applied 3 change(s) to dispat.json (previous copies carry the .backup suffix)
```

The plan then reads like the workspace does:

```console
$ git commit -m "feat(core)!: rename the client builder"
$ git commit -m "fix(app): flush the writer on drop"
$ dispat status
12:38:37 INF ● changed baselineFromInitials=true bump=major channel=stable dueToProviders=[] ownCommits=1 package=core reason=direct space=crates version="1.2.0 -> 2.0.0"
12:38:37 INF ● changed baselineFromInitials=true bump=patch channel=stable dependsOn=["core"] dueToProviders=[] ownCommits=1 package=app reason=direct space=crates version="0.4.1 -> 0.4.2"
12:38:37 INF release plan ready held=0 packages=2 releasing=2
```

`core` publishes first because `app` declares it. Current cargo waits for a published crate to appear in the registry
index before it exits, so by the time `app` uploads, the version it names is fetchable.

## What dispat reads and writes

A `path` dependency is reported with an arrow, since it is the strongest evidence that two folders belong to one
workspace:

```console
$ dispat scanner
crates/app/Cargo.toml  cargo  acme-app@0.4.1
  dependencies     acme-core  1.2.0  -> ../core
  dependencies     serde      1.0.210
  dependencies     tokio      
  devDependencies  tempfile   3.13.0
crates/core/Cargo.toml  cargo  acme-core@1.2.0
  dependencies  serde  1.0.210
2 manifest(s), 5 dependency declaration(s)
```

Writes land in the version field and in the `version` key of a dependency, whether it is written as a bare string or
as an inline table. Anything inherited from the workspace is skipped, because its version is not in this file:

```console
$ dispat writer crates/app/Cargo.toml --set-version 0.5.0 --set acme-core=^1.3.0 --set tokio=1.41.0
crates/app/Cargo.toml
  version written
  applied  dependencies  acme-core  ^1.3.0
  skipped  dependencies  tokio  1.41.0
1 manifest(s): 1 applied, 1 skipped, 0 missing
```

`tokio` is declared `{ workspace = true }`. Skipped is the correct outcome there: the version lives in the root
`[workspace.dependencies]`, and writing a literal into the member would break the inheritance somebody set up on
purpose. The same applies to `version.workspace = true` on the package itself, so a workspace that inherits its
version needs an [`autoVersion.replace`](../configuration/autoversion.md) rule aimed at the root manifest instead.

A dependency carrying only a `path` and no version keeps working locally but cannot be published, since crates.io
requires a version for every dependency. Declare both, as `acme-core = { version = "1.2.0", path = "../core" }`, and
dispat keeps the version half current.

## Building against the crate next door

`dispat writer --link` writes a `[patch.crates-io]` entry, and the empty form takes it away again:

```sh
dispat autowriter --since all --link-local     # before the build
dispat autowriter --since all --unlink-local   # before the publish
dispat scanner --verify-unlinked               # fails with E215 if one is left behind
```

## Worth knowing

- **Yanking is not a rollback.** If a publish fails halfway through a run, fix forward and release again; a version
  number on crates.io is spent whatever happens next. [Recovering from a failed run](../reference/releasing/recovery.md)
  explains what re-running does and does not repeat.
- **`0.x` versions are their own compatibility rule.** Cargo treats `0.4` and `0.5` as incompatible, so a `feat` on a
  pre-1.0 crate is a breaking change for its consumers even though dispat calls it a minor. Reach the consumers
  deliberately with `^` in the commit when that matters.
- **Keep `cargo publish` last.** Anything that can fail, `cargo test` and `cargo package` above all, belongs in the
  build stage, where failing costs nothing.

## See also

- [autoVersion](../configuration/autoversion.md) for the rewriting policy and `syncLock`.
- [Manifest tools](../editing/manifests.md) for the scanner and the writer used on their own.
- [Shared versions](../reference/releasing/versioning.md) if the crates should move as one version.
