# From one package to many

Start with one library, application, game or service. Add a CLI, container image, documentation site or SDK when
you need it. Each independently released deliverable becomes another package in the same configuration, with its
own commands, release records and dependencies.

You can keep each package’s folder and published tags while the graph grows. The package name, path and tag format
are part of its identity: preserve them when adding neighbours. A space can share commands later without requiring
all packages to use the same language or version.

## Start with one deliverable

Follow [A single package](./single-package.md) to configure its real build and publisher. For the graph alone,
a library in `core/` starts with:

```json title="dispat.json: graph only"
{
  "packages": {"core": {"path": "core"}},
  "initials": {"core": "1.2.0"}
}
```

These examples isolate package layout and release planning. Keep your existing `scripts`, `flow`, version edits
and record settings alongside them before running a release. A configuration with no build/publish stages does
not ship an artifact. Packages must occupy folders below the repository root.

Once `core@1.2.0` exists as a release record, that tag supplies the baseline. Adding other packages does not reset
it to zero or require moving its source. For an existing project with a different tag format, keep that format and
follow [Adopting dispat](./adopting.md).

## Add the next deliverables

Suppose the library gains a CLI, a container that installs the CLI, generated documentation, and an independent
marketing site. Add four package entries:

```json title="dispat.json: expanded graph"
{
  "packages": {
    "core": {"path": "core"},
    "cli": {
      "path": "cli",
      "isBuildWaitingPublish": true,
      "dependencies": [{"provider": "core", "keep": true}]
    },
    "image": {
      "path": "image",
      "dependencies": [{"provider": "cli", "keep": true}]
    },
    "docs": {
      "path": "docs",
      "dependencies": [{"provider": "core", "keep": true}]
    },
    "site": {"path": "site"}
  },
  "initials": {
    "core": "1.2.0",
    "cli": "0.1.0",
    "image": "0.1.0",
    "docs": "0.1.0",
    "site": "0.1.0"
  }
}
```

The `core` entry is unchanged. Each new entry keeps the build and publish tools appropriate to its artifact.
`keep: true` preserves these deliberately declared edges when computing dependencies from manifests. It does
not itself request a release or propagation.

The CLI is marked `isBuildWaitingPublish` because this example’s image downloads its released binary. The flag
belongs to the provider: the image waits for CLI publication before building. If a consumer uses a local build
output instead, choose its boundary accordingly. If the CLI itself fetches core from a registry during its build,
core also needs a publication boundary. See [build boundaries](./release-integration.md#choose-the-build-boundary).

## Release only the intended consumers

A commit `fix(core)^^: exercise consumers` requests propagation to all transitive consumers. In this graph that
reaches two dependency levels:

| Package | Planned version | Why |
| --- | --- | --- |
| core | 1.2.0 → 1.2.1 | Its own fix. |
| cli | 0.1.0 → 0.1.1 | Direct consumer of core. |
| image | 0.1.0 → 0.1.1 | Consumer through cli. |
| docs | 0.1.0 → 0.1.1 | Direct consumer of core. |
| site | 0.1.0, unchanged | No dependency or change requesting a release. |

Without propagation, the core fix releases core alone. Use `dispat status` to inspect the graph before the release
job runs. Independent versions let a library, application and site evolve at different rates; use an explicit
[shared version policy](../reference/releasing/versioning.md) only when they must share a version.

## Keep recovery aligned with deliverables

One package can publish several files, but its publisher must reconcile partial uploads before reporting success.
Give a destination its own package when it needs an independent release record and retry boundary. Do not split
packages merely because they use different commands; choose boundaries that make completion meaningful.

Adding a package does not require moving existing source or resetting tags. Changes to shared build scripts and
root configuration still need explicit release attribution; they are not automatically changes inside every
package folder. Run the full required suite and retain package and artifact checks during releases.

Use a [space](../configuration/spaces.md) when several packages share scripts. If the deliverables live in separate
repositories, a [control repository](../control-repository.md) can supply the same graph with pinned submodules.

## Apply the pattern to your project

| Starting point | Possible next deliverables | Worked examples |
| --- | --- | --- |
| Library | CLI, API docs, language bindings | [Go](./go.md), [Cargo](./rust.md), [npm](./npm.md) |
| Python package | Other distributions, worker image, service | [Python](./python.md), [Pants and pip](./python-pants.md) |
| Application or service | Container, chart, deployment, site | [Docker](./docker.md), [Helm](./helm.md), [Terraform](./terraform.md), [sites](./pages.md) |
| Mobile app | Shared library, backend, release assets | [Apple](./apple.md), [Android](./android.md), [Flutter](./flutter.md) |
| Game | Dedicated server, modding SDK, launcher, website | [Game development](./game.md), [Steam](./steam.md), [itch.io](./itch.md) |
