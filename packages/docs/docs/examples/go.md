# A Go module workspace

For Go modules, publishing means pushing the right Git tags. This example tests modules in dependency order and
updates their `require` versions before recording the release. Pay particular attention to the tag format: Go uses
it to find modules in subdirectories.

## The layout

```
packages/core/go.mod      module github.com/acme/monorepo/packages/core
services/api/go.mod       module github.com/acme/monorepo/services/api, requires core
dispat.json
```

## The configuration

This configuration shows the per-module tests and version updates. A complete release job also needs a way to resolve
new provider versions before their tags reach the remote. `go mod tidy` cannot fetch a tag that has not been pushed.
Choose that ordering for your project before using this configuration in CI. The
[local-link pattern below](#building-against-the-working-tree) covers tests against the checkout; it does not publish
the new tags.

```json title="dispat.json"
{
  "scripts": {
    "test": "go test ./...",
    "tidy": "go mod tidy"
  },
  "spaces": {
    "libs": {
      "path": "packages",
      "tagFormat": "packages/{name}/v{version}",
      "flow": {"build": "test"},
      "autoVersion": {"enabled": true, "syncLock": ["tidy"]}
    },
    "services": {
      "path": "services",
      "tagFormat": "services/{name}/v{version}",
      "flow": {"build": "test"},
      "autoVersion": {"enabled": true, "syncLock": ["tidy"]}
    }
  }
}
```

You do not write a `publish` script because a Go module is fetched directly from a git tag. Run
`dispat commit --tag --push` to finish the release, or let the [release commit](../configuration/records.md) do it for
you. The `autoVersion` field keeps the `require` lines pointing at the versions this run produced, and `tidy` updates
`go.sum` to match.

## The tag format matters here

The tag prefix must match the module's directory. In the `github.com/acme/monorepo` repository above, a module at
`packages/core` needs a tag such as `packages/core/v0.1.0`. The two spaces set different prefixes so both module
directories produce the right tags. See Go's [guide to module source layouts](https://go.dev/doc/modules/managing-source).

## Letting dispat read the graph

Your `go.mod` already says that `api` requires `core`. Run [`dispat compute`](../cli/compute.md) to turn that into
configuration instead of writing it out yourself:

```console
$ dispat compute --write
+ add     api -> core (dependencies)  services/api/go.mod dependencies "github.com/acme/monorepo/packages/core": "v1.2.0"

applied 1 change(s) to dispat.json (previous copies carry the .backup suffix)
```

The plan is ordered from then on, and `api` waits for `core`:

```console
$ git commit -m "feat(core): typed client options"
$ git commit -m "fix(api): retry on 502"
$ dispat status
12:37:43 INF ● changed bump=minor channel=stable dueToProviders=[] ownCommits=1 package=core reason=direct space=libs version="0.0.0 -> 0.1.0"
12:37:43 INF ● changed bump=patch channel=stable dependsOn=["core"] dueToProviders=[] ownCommits=1 package=api reason=direct space=services version="0.0.0 -> 0.0.1"
12:37:43 INF release plan ready held=0 packages=2 releasing=2
```

## What dispat reads and writes

The scanner reads the module path as the name, every `require` as a dependency, and a relative `replace` as a link to a
folder. A `go.mod` file declares no version of its own. This is why the identity line below carries no `@version`:

```console
$ dispat scanner
packages/core/go.mod  gomod  github.com/acme/monorepo/packages/core
services/api/go.mod  gomod  github.com/acme/monorepo/services/api
  dependencies  github.com/acme/monorepo/packages/core  v1.2.0
  dependencies  github.com/go-chi/chi/v5                 v5.1.0
2 manifest(s), 2 dependency declaration(s)
```

dispat writes through the same module parser the go tool uses. A `require` block keeps its grouping and its comments:

```console
$ dispat writer services/api/go.mod --set github.com/acme/monorepo/packages/core=v1.3.0
services/api/go.mod
  applied  dependencies  github.com/acme/monorepo/packages/core  v1.3.0
1 manifest(s): 1 applied, 0 skipped, 0 missing
```

`go.mod` uses exact versions, so even a `caret` or `tilde` policy writes `vX.Y.Z`. dispat updates existing requirements;
use `go get` to add a new one and update `go.sum`.

## Building against the working tree

For a local build, a `replace` directive can point the service at the library beside it. Remove that directive before
release. [`dispat autowriter`](../editing/autowriter.md) can add and remove the links across the packages in the plan:

```json title="dispat.json (the link bracket)"
{
  "scripts": {
    "link": "dispat autowriter --since all --sync-lock=false --link-local",
    "unlink": "dispat autowriter --since all --sync-lock=false --unlink-local",
    "verify": "dispat scanner --root-only --verify-unlinked"
  }
}
```

A local `replace` directive points to a path that will not exist on a consumer's machine. Run `verify` before release
to catch leftover links; a detected link fails with `E215`.

## Worth knowing

- **Major versions past v1 change the module path.** A `v2` release means the path becomes
  `github.com/acme/monorepo/packages/core/v2` in the module line and in every consumer's `require`. dispat writes the
  versions, but moving the path is a source change you make in the commit that breaks the API.
- **`go mod tidy` runs after the version stage, not before.** This is what `syncLock` is for. The manifest is already
  reconciled when the command runs, so it only refreshes `go.sum`.
- **Indirect requirements are left alone.** The scanner keeps them apart from the module's own declarations because the
  toolchain owns them.
- **Nothing publishes if the tag is not pushed.** Set `commit.push` in your
  [release records](../configuration/records.md), or push the tags in the job that runs dispat.

## Verify the published module

Before publication, use a clean consumer without local replacement directives against the packaged source to verify
the exact module path. Check proxy visibility separately after native release records exist. Multi-module notes and
GitHub release pages are additional outputs; a tag alone does not prove every output was finalized. See
[integration patterns](./release-integration.md#apply-the-pattern-to-your-ecosystem).

## Keep each module namespace explicit

Treat every `go.mod` as its own version namespace. A nested module commonly uses a path-prefixed tag and a distinct
proxy path; that prefix is part of the publication identity. A repository-wide tag is not a substitute.

A nested manifest is not proof that maintainers intend to release it independently. Record the declared release
policy before treating an absent module version as a failure. In a coordinated multi-module workflow, reconcile
drafts, assets and already-published module releases by exact tag OID. dispat can order those module records, but it
cannot infer release intent from the directory tree.

See [From one package to many](./one-to-many.md) to add deliverables while preserving existing package identities and release history.

## See also

- Read [Manifest tools](../editing/manifests.md) to use the scanner and writer on their own.
- Run [`dispat compute`](../cli/compute.md) to derive the graph and the starting versions.
- Check [Pipeline patterns](../reference/pipelines.md) for the working-tree link bracket in a CI job.
