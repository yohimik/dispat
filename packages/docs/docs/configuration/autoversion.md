# autoVersion

With an `autoVersion` object present, dispat keeps a space's files in sync with the released versions itself. No
`flow.version` script is required; one may still run afterwards and sees the already-reconciled files.

The block is written on a space (or at the top level, where it becomes every space's default, or on a single package
that needs its own). Everything below describes one such block.

There are two strategies, and they are independent. You can use either, both, or neither.

The **parsing** strategy is the default one described below. dispat scans the package's manifests, matches each
declared dependency against the workspace, and rewrites the declaration to the provider's end-of-run version. A
dependency matches in one of three ways: by manifest name, by a name the configuration states through
[`manifestNames`](./packages.md#manifestnames), or by a declared local path such as `file:`, a relative `replace` or
`path =`. The end-of-run version is the planned version when the provider is releasing and has not failed, and its
baseline otherwise. `manifests: none` turns it off.

The **replacing** strategy is the `replace` list: literal find-and-write over whatever files its globs select, parsing
nothing, for the versions no manifest holds (a Gradle coordinate, a README example, a CI workflow). It has
[a page of its own](../cookbook/editing/replacer.md).

A package may use both, in which case its manifests are reconciled first. A block using neither still schedules a
version task, which is how a space asks for [`syncLock`](#the-options) and nothing else.

The rewrite is byte-precise: only the version text changes, and formatting, key order and comments survive. Writers
exist for `package.json`, `go.mod` and `requirements*.txt` (only the matching line's specifier changes; spelling,
spacing and comments survive). The other ecosystems (Cargo, pyproject, Composer, Maven, .NET, Dart) still feed
`compute` and the graph, but their rewriting is `flow.version`'s job.

Two consequences worth knowing before turning it on. First, the reconciliation rule (§9.4 of the
[commit specification](https://github.com/yohimik/dispat/blob/main/pkg/ccme/SPEC.md)) covers *every* workspace dependency, including providers released
by earlier runs, so an auto-versioning space runs a version task for **every** releasing package, even one whose
providers are all quiet this run. Second, a rewriting failure fails the version stage, and
`revertOnFail` rolls the half-edited folder back.

## The options

| Key                   | Type             | Default  | Effect                                                                                                                                                                                                                                                                                                                                                                                                                                                                                             |
|-----------------------|------------------|----------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `enabled`             | bool             | `true`   | Turns the block off without deleting it. The minimal opt-in block is `{"enabled": true}`: a completely empty `{}` object is pruned by the config loader and reads as absent.                                                                                                                                                                                                                                                                                                                       |
| `manifests`           | string           | `root`   | The parsing strategy's scope. `root`: only manifests directly in the package folder; `all`: every manifest found under it (dependency, virtual-env and build-output folders such as `node_modules`, `vendor`, `dist` and `venv`, plus every dot-folder, are never entered); `none`: the parsing strategy is off, leaving `replace` and `syncLock` as the whole of the version stage.                                                                                                              |
| `replace`             | array of objects | none     | The replacing strategy: literal text substitutions applied to the files each rule's globs select, parsing nothing. Each entry takes `files` (globs relative to the package folder), `find` and `write`, all required; `find` and `write` are templates over `{name}`, `{version}`, `{previous}`, `{provider}`, `{providerVersion}` and `{providerPrevious}`, and a rule naming a provider is applied once per provider. See [the replacer](../cookbook/editing/replacer.md).                                        |
| `kinds`               | array of strings | all four | Restrict rewriting to the named manifest fields (`dependencies`, `devDependencies`, `peerDependencies`, `optionalDependencies`).                                                                                                                                                                                                                                                                                                                                                                   |
| `only`                | array of strings | all      | Restrict rewriting to declarations of the named provider packages; every name must be a discovered package.                                                                                                                                                                                                                                                                                                                                                                                        |
| `nameMatch`           | string           | `exact`  | How a declared name finds its workspace package when no manifest declares that name and no local path matches: `exact` (such declarations are simply not workspace dependencies) or `substring`; see the note below the table.                                                                                                                                                                                                                                                                     |
| `match`               | array of globs   | any      | Rewrite only declared ranges matching one of the globs, e.g. `["workspace:*"]`, so a range pinned by hand is never overridden. `*` matches any run of characters (slashes included, same as scope globs), so `file:../core` is matched by `file:*` and by `*`.                                                                                                                                                                                                                                     |
| `range`               | string           | `caret`  | The write policy: `caret` (`^1.2.3`), `tilde` (`~1.2.3`), `exact` (`1.2.3`), a `{version}` template (`>={version}`), or any other literal written verbatim (`workspace:*`). Ecosystems with their own version spelling override the keywords: `go.mod` always receives exact canonical `vX.Y.Z`, Python files always receive `==X.Y.Z`; templates and literals pass through everywhere.                                                                                                            |
| `writeVersion`        | bool             | `true`   | Also write the package's own new version into its manifest's version field (§12.4). Applies to the package's **root** manifests only: a nested manifest (an example, a fixture) keeps its own version even under `manifests: all`.                                                                                                                                                                                                                                                                 |
| `syncLock`            | array of names   | none     | Script names, resolved per package like every other name (see [script sequences](./scripts.md)), run inside the package folder after its files were reconciled and before its build: the slot for `npm install` and friends, so lock files follow the manifests. Skipped for a package whose version stage changed nothing, so a quiet release does not regenerate locks for no reason. A block that configures neither strategy has no such change to key off, so its scripts run every release instead of never: that is how a space asks for lock regeneration alone. A lock file living at the repo root is outside every package folder: list it under [`commit.include`](./records.md#commit) so the release commit carries it. That is optional for npm and Yarn workspaces and unavoidable for pnpm, whose `pnpm-lock.yaml` is always the workspace root's. |
| `syncLockConcurrency` | int              | `1`      | Run-wide cap on simultaneously running `syncLock` scripts. Shared lock files corrupt under parallel writers, hence the serial default; when spaces disagree, the smallest configured value wins.                                                                                                                                                                                                                                                                                                   |

Under `nameMatch: substring`, a declared name whose last `/`- or `:`-separated segment equals a package's folder name
also matches: package `app` matches `@core/app`, `com.acme:app` or a bare `app` line, even when the `app` package has no
parseable manifest of its own. It is opt-in because it can false-positive; a third-party `@types/app` would match a
package named `app`.

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

The same reconciliation is callable outside a release as [`dispat autoversion`](../cli/autoversion.md). Flags override
the block's policy for that invocation: `--manifests none` turns the parsing strategy off, and `--no-replace` skips the
rules. A custom flow uses it to reconcile at the moment it needs, and the version stage later finds nothing left to
rewrite.

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

Four warnings narrate what the rewrite did that the commit log alone cannot explain. `W192`: the manifest's declared own
version disagreed with the baseline (tags are authoritative; the computed version is written over it). `W197`: a range
was caught up to a provider released outside this run (the reconciliation rule's catch-up case). `W203`: a stable
release now ranges over a prerelease provider. And `W221`: a rewritten dependency has no configured `dependencies` edge
behind it, so nothing orders this package after that provider or skips it when the provider fails; the written version
is optimistic about a publish still in flight. `dispat compute` derives the missing edge.

The replacing strategy raises `W197` and `W203` on the same terms, plus one of its own. `W222`: a replace rule found
its text in none of the files it selected, which usually means a mistyped template or a stale glob. Re-running a
release does not trigger it, because dispat checks whether the file already reads the way the rule wants before
deciding the rule is stale.
