# A Go module workspace

Keep several Go modules in one repository, and release them in dependency order. Go has no registry to publish to, so
the tag *is* the release. This makes a Go workspace the shortest setup on the site.

## The layout

```
packages/core/go.mod      module github.com/acme/core
services/api/go.mod       module github.com/acme/api, requires core
dispat.json
```

## The configuration

```json title="dispat.json"
{
  "tagFormat": "{name}/v{version}",
  "scripts": {
    "test": "go test ./...",
    "tidy": "go mod tidy"
  },
  "spaces": {
    "libs": {
      "path": "packages",
      "flow": {"build": "test"},
      "autoVersion": {"enabled": true, "syncLock": ["tidy"]}
    },
    "services": {
      "path": "services",
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

The `{name}/v{version}` format is not decoration. The go tool finds a module in a subdirectory only under a tag
prefixed with that subdirectory, so a module at `packages/core` must be tagged `packages/core/v0.1.0`. Name your
packages after their folders to produce exactly that format, just as dispat uses `"tagFormat": "pkg/{name}/v{version}"`
for its own modules.

## Letting dispat read the graph

Your `go.mod` already says that `api` requires `core`. Run [`dispat compute`](../cli/compute.md) to turn that into
configuration instead of writing it out yourself:

```console
$ dispat compute --write
+ add     api -> core (dependencies)  services/api/go.mod dependencies "github.com/acme/core": "v1.2.0"

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
packages/core/go.mod  gomod  github.com/acme/core
services/api/go.mod  gomod  github.com/acme/api
  dependencies  github.com/acme/core      v1.2.0
  dependencies  github.com/go-chi/chi/v5  v5.1.0
2 manifest(s), 2 dependency declaration(s)
```

dispat writes through the same module parser the go tool uses. A `require` block keeps its grouping and its comments:

```console
$ dispat writer services/api/go.mod --set github.com/acme/core=v1.3.0
services/api/go.mod
  applied  dependencies  github.com/acme/core  v1.3.0
1 manifest(s): 1 applied, 0 skipped, 0 missing
```

Two rules belong to Go rather than dispat. Ranges do not exist in `go.mod`, so a `caret` or `tilde` policy still writes
the exact canonical `vX.Y.Z`. dispat never adds a requirement that is not already there, because adding one is
`go get`'s job and it has a `go.sum` to update.

## Building against the working tree

Write a `replace` directive to compile a service against the copy of the library next door rather than a published
version, and take it away again afterwards. Run [`dispat autowriter`](../editing/autowriter.md) to do both across every
package the plan covers:

```json title="dispat.json (the link bracket)"
{
  "scripts": {
    "link": "dispat autowriter --since all --sync-lock=false --link-local",
    "unlink": "dispat autowriter --since all --sync-lock=false --unlink-local",
    "verify": "dispat scanner --root-only --verify-unlinked"
  }
}
```

A `replace` directive must never reach a tag, because a consumer fetching the module would get a path that does not
exist on their machine. Run `verify` before the release. A leftover link fails the run with code `E215` instead of
shipping.

## Worth knowing

- **Major versions past v1 change the module path.** A `v2` release means the path becomes `github.com/acme/core/v2` in
  the module line and in every consumer's `require`. dispat writes the versions, but moving the path is a source change
  you make in the commit that breaks the API.
- **`go mod tidy` runs after the version stage, not before.** This is what `syncLock` is for. The manifest is already
  reconciled when the command runs, so it only refreshes `go.sum`.
- **Indirect requirements are left alone.** The scanner keeps them apart from the module's own declarations because the
  toolchain owns them.
- **Nothing publishes if the tag is not pushed.** Set `commit.push` in your
  [release records](../configuration/records.md), or push the tags in the job that runs dispat.

## See also

- Read [Manifest tools](../editing/manifests.md) to use the scanner and writer on their own.
- Run [`dispat compute`](../cli/compute.md) to derive the graph and the starting versions.
- Check [Pipeline patterns](../reference/pipelines.md) for the working-tree link bracket in a CI job.
