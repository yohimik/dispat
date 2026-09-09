# A Cargo workspace

Keep your crates in one workspace. dispat versions them from commits and publishes them to crates.io in dependency
order. The tool rewrites the `path` dependencies between your crates to real versions before it uploads anything.

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

Pass `--allow-dirty` to your publish script. The version stage writes the new version into `Cargo.toml` right before
the publish runs, so your working tree is dirty by design. The release commit at the end of the run records those
edits.

Include `Cargo.lock` under `commit.include` to reach the same commit. The file lives at the workspace root, outside
every package folder.

## Starting from the versions already in the files

Run [`dispat compute`](../cli/compute.md) to derive the edges between crates and the baseline each crate starts from.
dispat can read both halves of the setup at once because Cargo manifests declare their versions.

```console
$ dispat compute --write
+ add     app -> core (dependencies)  crates/app/Cargo.toml dependencies "acme-core": "1.2.0"
+ initial app 0.4.1  crates/app/Cargo.toml declares 0.4.1; no release tag yet
+ initial core 1.2.0  crates/core/Cargo.toml declares 1.2.0; no release tag yet

applied 3 change(s) to dispat.json (previous copies carry the .backup suffix)
```

Run `dispat status` to see a plan that matches your workspace.

```console
$ git commit -m "feat(core)!: rename the client builder"
$ git commit -m "fix(app): flush the writer on drop"
$ dispat status
12:38:37 INF ● changed baselineFromInitials=true bump=major channel=stable dueToProviders=[] ownCommits=1 package=core reason=direct space=crates version="1.2.0 -> 2.0.0"
12:38:37 INF ● changed baselineFromInitials=true bump=patch channel=stable dependsOn=["core"] dueToProviders=[] ownCommits=1 package=app reason=direct space=crates version="0.4.1 -> 0.4.2"
12:38:37 INF release plan ready held=0 packages=2 releasing=2
```

dispat publishes `core` first because `app` declares it. Cargo waits for a published crate to appear in the registry
index before it exits. The version `app` names is fetchable by the time it uploads.

## What dispat reads and writes

Run `dispat scanner` to see your dependencies. dispat reports a `path` dependency with an arrow, because a path is the
strongest evidence that two folders belong to one workspace.

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

Run `dispat writer` to update your manifests. dispat writes to the version field and the `version` key of a dependency.
It handles bare strings and inline tables. The tool skips anything inherited from the workspace because the version is
not in the file.

```console
$ dispat writer crates/app/Cargo.toml --set-version 0.5.0 --set acme-core=^1.3.0 --set tokio=1.41.0
crates/app/Cargo.toml
  version written
  applied  dependencies  acme-core  ^1.3.0
  skipped  dependencies  tokio  1.41.0
1 manifest(s): 1 applied, 1 skipped, 0 missing
```

The manifest declares `tokio` as `{ workspace = true }`. Skipped is the correct outcome here. The version lives in the
root `[workspace.dependencies]`, and writing a literal into the member breaks the inheritance you set up.

The same applies to `version.workspace = true` on the package itself. Point an
[`autoVersion.replace`](../configuration/autoversion.md) rule at the root manifest if your workspace inherits its
version.

crates.io requires a version for every dependency, so a dependency carrying only a `path` fails to publish. Declare
both a version and a path, like `acme-core = { version = "1.2.0", path = "../core" }`. dispat keeps the version half
current.

## Building against the crate next door

Run `dispat writer --link` to write a `[patch.crates-io]` entry. The empty form takes it away again.

```sh
dispat autowriter --since all --link-local     # before the build
dispat autowriter --since all --unlink-local   # before the publish
dispat scanner --verify-unlinked               # fails with E215 if one is left behind
```

## Worth knowing

- **Yanking is not a rollback.** Fix forward and release again if a publish fails halfway through a run. A version
  number on crates.io is spent whatever happens next. Read
  [Recovering from a failed run](../reference/releasing/recovery.md) to see what re-running does and does not repeat.
- **`0.x` versions are their own compatibility rule.** Cargo treats `0.4` and `0.5` as incompatible. A `feat` on a
  pre-1.0 crate is a breaking change for its consumers even though dispat calls it a minor. Reach the consumers
  deliberately with `^` in the commit when that matters.
- **Keep `cargo publish` last.** Put anything that can fail in the build stage, where failing costs nothing. This
  applies to `cargo test` and `cargo package` above all.

## See also

- Read [autoVersion](../configuration/autoversion.md) for the rewriting policy and `syncLock`.
- Read [Manifest tools](../editing/manifests.md) for the scanner and the writer used on their own.
- Read [Shared versions](../reference/releasing/versioning.md) if the crates should move as one version.
