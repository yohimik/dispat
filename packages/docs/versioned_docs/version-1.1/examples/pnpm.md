# A pnpm workspace

Releasing a pnpm workspace takes the same shape as [an npm monorepo](./npm.md). You will notice three differences that
come from the package manager rather than from dispat. The lock file lives at the workspace root, internal ranges use
the `workspace:` protocol, and `pnpm publish` checks your git tree state.

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
  `pnpm-workspace.yaml`. Your build stage does not need `-C ../..`. Pass `--frozen-lockfile` to make the stage fail
  locally on a mismatched lock file, instead of quietly resolving something new mid-release.
- **`workspace:*` ranges stay as they are.** [`autoVersion`](../configuration/autoversion.md) writes each package's own
  `version` field. `pnpm publish` substitutes that version for the `workspace:` specifier when it packs, so your
  declared ranges never need rewriting. Set `range: "workspace:*"` to write this literal back verbatim and keep the
  protocol intact.
- **`pnpm-lock.yaml` is outside every package folder.** Use `syncLock` to regenerate it after the version stage, and
  add it to [`commit.include`](../configuration/records.md#commit) to put it in the release commit. Without this line,
  your release commit carries rewritten manifests and a stale lock file. The one-at-a-time
  [`syncLockConcurrency`](../configuration/autoversion.md) default matters here because every package in the workspace
  regenerates the *same* file.
- **`--no-git-checks`.** `pnpm publish` refuses to publish from a dirty working tree or an unapproved branch. A dispat
  run creates exactly that situation, because the version stage rewrites manifests and dispat creates the release
  commit after the publishes. Pass this flag to bypass the check and rely on the annotated tag as the record of what
  shipped.

Yarn workspaces work the same way. Put `yarn.lock` in `commit.include` and use `yarn npm publish` in the publish slot.
