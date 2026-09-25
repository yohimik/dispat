# autoSign

Add an `autoSign` object to give a space's packages a sign stage that writes each package's own version. The sign
stage is the first stage of a package's release. It runs before the version stage (also called the propagate stage)
and before the build, and its native step writes the version the plan computed for the package into the package's own
manifests. The version stage after it then propagates the versions the package takes from its providers, and nothing
else.

Write this block on a space. You can also write it at the top level to set the default for every space, or on a single
package. Like [`autoVersion`](./autoversion.md), a level that states the block replaces what it inherits whole, and
`{"enabled": false}` switches an inherited block off for one level.

```yaml
spaces:
  js:
    path: packages
    autoSign:
      manifests: root
    autoPropagate:
      match: [ "workspace:*" ]
      syncLock: [ npm-install ]
scripts:
  npm-install: npm install --package-lock-only
```

Here the sign stage writes every package's own `version` field, the propagate stage rewrites the `workspace:*`
ranges, and the lock file is regenerated from the result before the build.

## The options

| Key         | Type   | Default | Effect                                                                                                                                                                                                                                                  |
|-------------|--------|---------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `enabled`   | bool   | `true`  | Set to false to turn the block off without deleting it. The minimal opt-in block is `{"enabled": true}` because the config loader prunes an empty `{}` object.                                                                                          |
| `manifests` | string | `root`  | Sets which manifests the sign stage scans: `root` for the manifests directly in the package folder, or `all` for every manifest under it, which reaches a format whose folder is part of its name, such as Unity's `ProjectSettings/ProjectSettings.asset`. Either way only the package's own manifests are written, so a nested example or fixture keeps its own version. `none` is refused: a sign stage that scans nothing writes nothing, and `enabled: false` already says so. |

The write is byte-precise and covers every format the scanner reads, exactly as the own-version write of `autoVersion`
does. A manifest whose own version disagrees with the baseline is reported as `W192` from the sign stage, and the
computed version is written over it.

## The sign stage

A package has a sign stage when its space enables `autoSign` or configures any of `flow.sign`, `flow.beforeSign` and
`flow.postSign`. A package with none of them has no sign stage at all: no task, no hooks, no events and no log lines,
so its release runs exactly as it always did.

The stage frame runs in this order, and every part of it gates the release:

1. `flow.beforeSign`, after `flow.beforeAll`, which runs at the package's first stage.
2. The native write, when `autoSign` is enabled.
3. The `flow.sign` scripts, which see the version already written. Use them for a version no manifest holds.
4. `flow.postSign`.

The sign stage is the package's first task, so it is the task that waits for the package's providers under their
[relation](./spaces.md#the-provider-relation), the way the version stage does when it comes first. It shares the build
budget with the version stage and the build. A failure anywhere in the frame fails the package at the stage named
`sign`: `DISPAT_FAILED_STAGE` and the webhook `failedStage` carry `sign`, a failing native write is logged as
`auto-signing failed`, and `revertOnFail` rolls the folder back. `DISPAT_STAGE` carries `sign`, `beforeSign` and
`postSign`. With [worker nodes](../distributed-execution.md) configured, the sign stage runs on the machine the release
was started on, because it writes the files every delegated build is snapshotted from.

## The sign stage owns the own version

Two stages never write the same field. When a package's `autoSign` is enabled, its `autoVersion` (or `autoPropagate`)
block writes dependency ranges and replace rules alone:

- `writeVersion` defaults to `false` beside an enabled `autoSign`.
- An explicit `writeVersion: true` beside it is refused when the configuration loads, with both keys named. This
  includes a `writeVersion: true` a package inherits from a level above it when the package enables `autoSign` itself,
  so restate the block without it there.
- Without `autoSign`, `autoVersion` writes both the ranges and the own version, exactly as it always has.

[`dispat autoversion`](../cli/autoversion.md) runs the version stage's reconciliation outside a release, so for a
package whose `autoSign` is enabled it writes the ranges alone. Pass `--write-version` to ask for the own version too.

## Lock files

`syncLock` lives on `autoVersion` (or `autoPropagate`), not on `autoSign`, so `autoSign` alone regenerates no lock
file. The lock scripts run after the version stage wherever the sign stage or the version stage changed a manifest, so
a release whose only change is the own version still regenerates its lock. When a space needs the lock regenerated but
has no ranges to reconcile, write `syncLock` on a block with the parsing strategy off. A block with neither strategy
has no change to key off, so it runs its lock scripts on every release:

```yaml
spaces:
  js:
    path: packages
    autoSign: { enabled: true }
    autoPropagate:
      manifests: none
      syncLock: [ npm-install ]
```
