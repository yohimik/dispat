# autoVersion

Add an `autoVersion` object to let dispat keep a space's files in sync with released versions. You do not need a
`flow.version` script. If you run one anyway, it sees the already-reconciled files.

Write this block on a space. You can also write it at the top level to set the default for every space, or on a single
package that needs its own rules. Everything below describes one such block.

You can use the parsing strategy, the replacing strategy, both, or neither. They are independent.

The **parsing** strategy is the default. dispat scans your package manifests, matches each declared dependency against
the workspace, and rewrites the declaration to the provider's end-of-run version. A dependency matches by manifest
name, by a name you configure with [`manifestNames`](./packages.md#manifestnames), or by a declared local path like
`file:`, a relative `replace` or `path =`. Set `manifests: none` to turn this off, otherwise dispat uses the planned
version when the provider is releasing successfully and the baseline version otherwise.

The **replacing** strategy uses the `replace` list to do literal find-and-write over files selected by globs. It parses
nothing. Use this for versions no manifest holds, like a Gradle coordinate or a README example, and read more on
[a page of its own](../editing/replacer.md).

Configure both strategies to reconcile manifests first and replace text second. A block using neither strategy still
schedules a version task. This lets a space ask for [`syncLock`](#the-options) and nothing else.

The rewrite is byte-precise, meaning only the version text changes while your formatting, key order, and comments
survive. The parsing strategy covers all formats the scanner reads, including `package.json`, `go.mod`, `Cargo.toml`,
`pyproject.toml`, `requirements*.txt`, `composer.json`, `pom.xml`, the .NET project and package files, `pubspec.yaml`,
the Ruby, CocoaPods, Xcode, Android, Gradle and Docker files, and the Unity, Godot, Unreal, Defold and O3DE project
files. A game engine needs no `replace` rule for its own version because files like `project.godot` and
`ProjectSettings/ProjectSettings.asset` are manifests, and you can find the full list in
[Manifest tools](../editing/manifests.md#supported-formats).

A writer never replaces an indirection with a literal. dispat skips a Maven `${property}`, a Cargo
`{ workspace = true }`, an MSBuild `$(Version)`, or an Xcode `$(MARKETING_VERSION)` and leaves them exactly as they are
because the indirection is deliberate. Use `replace` or a `flow.version` script to edit the file where the number
actually lives.

Know two consequences before you turn this on. First, the reconciliation rule (§9.4 of the
[commit specification](https://github.com/yohimik/dispat/blob/main/pkg/ccme/SPEC.md)) covers *every* workspace
dependency, including providers released by earlier runs, so an auto-versioning space runs a version task for **every**
releasing package even when its providers are quiet. Second, a rewriting failure fails the version stage, and
`revertOnFail` rolls the half-edited folder back.

### Picking up providers released without you

When a provider releases alone, its consumers do not move and their manifests keep the old version until each consumer
releases for a reason of its own. That next release's version stage reconciles the range to the provider's current
released version, even though the provider releases nothing in that run. The parsing strategy performs this pickup by
resolving every declared provider against the plan, while lock-file scripts under `syncLock` like `npm install` or
`go mod tidy` run afterwards to make the lock follow the manifest without choosing versions.

You see this pickup as `W197` in the log and in the release commit's manifest diff, but it does not appear in the
consumer's [changelog entry](./records.md#changelog) because the provider's own release already documented the change.
Hold a provider back with the `match` filter or an explicit [`dependencies` range](./dependencies.md) if a consumer
must not pick it up yet. Run [`dispat autoversion`](../cli/autoversion.md) to trigger this pickup standalone and
reconcile a lagging manifest without releasing.

## The options

| Key                   | Type             | Default  | Effect                                                                                                                                                                                                                                                                                                                                                                                                                                                                                             |
|-----------------------|------------------|----------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `enabled`             | bool             | `true`   | Set to false to turn the block off without deleting it. The minimal opt-in block is `{"enabled": true}` because the config loader prunes an empty `{}` object.                                                                                                                                                                                                                                                                                                                                     |
| `manifests`           | string           | `root`   | Sets the parsing strategy's scope to `root` for manifests directly in the package folder, or `all` for every manifest found under it. dispat never enters dot-folders or output folders like `node_modules`, `vendor`, `dist`, and `venv`. Set to `none` to turn the parsing strategy off and leave `replace` and `syncLock` as the whole version stage.                                                                                                                                         |
| `replace`             | array of objects | none     | Configures the replacing strategy to apply literal text replacements to the files your globs select, parsing nothing. Provide `files` (globs relative to the package folder) alongside `find` and `write` templates over `{name}`, `{version}`, `{previous}`, `{provider}`, `{providerVersion}`, and `{providerPrevious}`. A rule naming a provider applies once per provider, and you can read more in [the replacer](../editing/replacer.md).                                                  |
| `kinds`               | array of strings | all four | Restricts rewriting to the named manifest fields. Pass fields like `dependencies`, `devDependencies`, `peerDependencies`, or `optionalDependencies`.                                                                                                                                                                                                                                                                                                                                               |
| `only`                | array of strings | all      | Restricts rewriting to declarations of the named provider packages. Every name must be a discovered package.                                                                                                                                                                                                                                                                                                                                                                                       |
| `nameMatch`           | string           | `exact`  | Controls how a declared name finds its workspace package when no manifest declares that name and no local path matches. Use `exact` to treat these declarations as external dependencies, or `substring` to match them. See the note below the table.                                                                                                                                                                                                                                              |
| `match`               | array of globs   | any      | Rewrites only declared ranges matching one of your globs, like `["workspace:*"]`. This prevents dispat from overriding a range you pinned by hand. The `*` character matches any run of characters including slashes, so `file:*` and `*` both match `file:../core`.                                                                                                                                                                                                                               |
| `range`               | string           | `caret`  | Sets the write policy to `caret` (`^1.2.3`), `tilde` (`~1.2.3`), `exact` (`1.2.3`), a `{version}` template (`>={version}`), or any literal written verbatim (`workspace:*`). Ecosystems with their own version spelling override the keywords, so `go.mod` always receives exact canonical `vX.Y.Z` and Python files always receive `==X.Y.Z`. Templates and literals pass through everywhere.                                                                                                     |
| `writeVersion`        | bool             | `true`   | Writes the package's own new version into its manifest's version field (§12.4). This applies to the package's **root** manifests only. A nested manifest like an example or fixture keeps its own version even under `manifests: all`.                                                                                                                                                                                                                                                             |
| `syncLock`            | array of names   | none     | Runs script names inside the package folder after files reconcile and before the build, resolving per package like every other name (see [script sequences](./scripts.md)). Use this for scripts like `npm install` so lock files follow the manifests, though dispat skips this for a package whose version stage changed nothing unless you configure neither strategy. List a repo root lock file under [`commit.include`](./records.md#commit) so the release commit carries it, which is optional for npm and Yarn workspaces but unavoidable for pnpm. |
| `syncLockConcurrency` | int              | `1`      | Caps simultaneously running `syncLock` scripts across the run. Shared lock files corrupt under parallel writers, which is why the default is serial. The smallest configured value wins when spaces disagree.                                                                                                                                                                                                                                                                                      |

Set `nameMatch: substring` to match a declared name when its last `/`- or `:`-separated segment equals a package's
folder name. A package named `app` matches `@core/app`, `com.acme:app`, or a bare `app` line, even when the `app`
package has no parseable manifest of its own. You must opt in because it can false-positive, like matching a
third-party `@types/app` to your `app` package.

```yaml
spaces:
  js:
    path: packages
    autoVersion:
      match: [ "workspace:*" ]      # only ranges the workspace manages
      range: caret                # write ^<new version>
      syncLock: [ npm-install ]     # scripts.npm-install: "npm install"
scripts:
  npm-install: npm install --package-lock-only
  # pnpm's equivalent, resolving into pnpm-lock.yaml without touching
  # node_modules:
  pnpm-lock: pnpm install --lockfile-only
```

Run [`dispat autoversion`](../cli/autoversion.md) to call the same reconciliation outside a release. Pass flags like
`--manifests none` to turn the parsing strategy off, or `--no-replace` to skip the rules. Call this in a custom flow to
reconcile exactly when you need, so the version stage later finds nothing left to rewrite.

Three shapes cover most repositories:

```yaml
spaces:
  js:                            # manifests dispat can parse
    path: packages
    autoVersion:
      match: [ "workspace:*" ]
      syncLock: [ npm-install ]

  android:                       # nothing here parses as a manifest
    path: modules
    autoVersion:
      manifests: none
      replace:
        - files: [ "*.gradle" ]
          find:  "com.acme:{provider}:{providerPrevious}"
          write: "com.acme:{provider}:{providerVersion}"

  go:                            # nothing to reconcile, one script to run
    path: services
    autoVersion:
      manifests: none
      syncLock: [ go-mod-tidy ]
```

Four warnings narrate what the rewrite did when the commit log alone cannot explain it: `W192` means the manifest's
declared own version disagreed with the baseline, so dispat writes the authoritative computed version over it. `W197`
means a range caught up to a provider released outside this run, and `W203` warns that a stable release now ranges over
a prerelease provider. `W221` means a rewritten dependency has no configured `dependencies` edge behind it to order
this package after that provider, so the written version is optimistic about an in-flight publish and `dispat compute`
derives the missing edge.

The replacing strategy raises `W197` and `W203` on the same terms, plus `W222` when a replace rule finds its text in
none of the selected files. This usually means a mistyped template or a stale glob. Re-running a release does not
trigger this warning because dispat checks if the file already reads the way the rule wants before deciding the rule is
stale.
