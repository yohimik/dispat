# A Go module workspace

Several Go modules in one repository, released in dependency order. Go has no registry to publish to, so the tag *is*
the release, which makes this the shortest setup on the site.

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

There is no `publish` script. A Go module is fetched from a git tag, so once the tag exists the version is available;
`dispat commit --tag --push`, or the [release commit](../configuration/records.md) doing it for you, is the whole
publish. `autoVersion` keeps the `require` lines pointing at the versions this run produced, and `tidy` makes `go.sum`
follow.

## The tag format matters here

`{name}/v{version}` is not decoration. The go tool finds a module in a subdirectory only under a tag prefixed with that
subdirectory, so a module at `packages/core` must be tagged `packages/core/v0.1.0`. Name your packages after their
folders and the format above produces exactly that. dispat's own Go modules use
`"tagFormat": "pkg/{name}/v{version}"` for the same reason.

## Letting dispat read the graph

`go.mod` already says that `api` requires `core`. [`dispat compute`](../cli/compute.md) turns that into configuration
instead of you writing it out:

```console
$ dispat compute --write
+ add     api -> core (dependencies)  services/api/go.mod dependencies "github.com/acme/core": "v1.2.0"

applied 1 change(s) to dispat.json (previous copies carry the .backup suffix)
```

From then on the plan is ordered, and `api` waits for `core`:

```console
$ git commit -m "feat(core): typed client options"
$ git commit -m "fix(api): retry on 502"
$ dispat status
12:37:43 INF ● changed bump=minor channel=stable dueToProviders=[] ownCommits=1 package=core reason=direct space=libs version="0.0.0 -> 0.1.0"
12:37:43 INF ● changed bump=patch channel=stable dependsOn=["core"] dueToProviders=[] ownCommits=1 package=api reason=direct space=services version="0.0.0 -> 0.0.1"
12:37:43 INF release plan ready held=0 packages=2 releasing=2
```

## What dispat reads and writes

The scanner reads the module path as the name, every `require` as a dependency, and a relative `replace` as a link to
a folder. `go.mod` declares no version of its own, which is why the identity line below carries no `@version`:

```console
$ dispat scanner
packages/core/go.mod  gomod  github.com/acme/core
services/api/go.mod  gomod  github.com/acme/api
  dependencies  github.com/acme/core      v1.2.0
  dependencies  github.com/go-chi/chi/v5  v5.1.0
2 manifest(s), 2 dependency declaration(s)
```

Writing goes through the same module parser the go tool uses, so a `require` block keeps its grouping and its comments:

```console
$ dispat writer services/api/go.mod --set github.com/acme/core=v1.3.0
services/api/go.mod
  applied  dependencies  github.com/acme/core  v1.3.0
1 manifest(s): 1 applied, 0 skipped, 0 missing
```

Two rules are Go's rather than dispat's. Ranges do not exist in `go.mod`, so a `caret` or `tilde` policy still writes
the exact canonical `vX.Y.Z`. And a requirement that is not already there is never added, because adding one is
`go get`'s job and it has a `go.sum` to update.

## Building against the working tree

To compile a service against the copy of the library next door rather than a published version, write a `replace` and
take it away again afterwards. [`dispat autowriter`](../editing/autowriter.md) does both across every package the plan
covers:

```json title="dispat.json (the link bracket)"
{
  "scripts": {
    "link": "dispat autowriter --since all --sync-lock=false --link-local",
    "unlink": "dispat autowriter --since all --sync-lock=false --unlink-local",
    "verify": "dispat scanner --root-only --verify-unlinked"
  }
}
```

`replace` directives must never reach a tag: a consumer fetching the module would get a path that does not exist on
their machine. Run `verify` before the release and a leftover link fails the run with code `E215` instead of shipping.

## Worth knowing

- **Major versions past v1 change the module path.** `v2` means the path becomes `github.com/acme/core/v2`, in the
  module line and in every consumer's `require`. dispat writes the versions; moving the path is a source change you
  make in the commit that breaks the API.
- **`go mod tidy` runs after the version stage, not before.** That is what `syncLock` is for: the manifest is already
  reconciled when the command runs, so it only refreshes `go.sum`.
- **Indirect requirements are left alone.** The scanner keeps them apart from the module's own declarations, because
  the toolchain owns them.
- **Nothing publishes if the tag is not pushed.** Check `commit.push` in
  [release records](../configuration/records.md), or push the tags in the job that runs dispat.

## See also

- [Manifest tools](../editing/manifests.md) for the scanner and writer on their own.
- [`dispat compute`](../cli/compute.md) for deriving the graph and the starting versions.
- [Pipeline patterns](../reference/pipelines.md) for the working-tree link bracket in a CI job.
