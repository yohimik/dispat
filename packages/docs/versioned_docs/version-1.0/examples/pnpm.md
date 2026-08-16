# A pnpm workspace

Releasing a pnpm workspace: the same shape as [an npm monorepo](./npm.md), with three differences that come from the
package manager rather than from dispat. The lock file is the workspace root's, internal ranges use the `workspace:`
protocol, and `pnpm publish` has an opinion about the state of your git tree.

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
