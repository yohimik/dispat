# One repository or many

You can use dispat with one package, a monorepo, or linked repositories. Choose the layout that fits how your team
changes, reviews, and grants access to the code. Release automation can help with coordination, but it does not remove
those tradeoffs.

## The two shapes

**One repository:** projects live in folders within a shared checkout and history. A change spanning several projects
can be reviewed together. Packages can still have separate versions and release schedules.

**Separate repositories:** projects have their own histories, permissions, and pipelines. Consumers usually pick up
changes through published dependency versions. You can also link repositories when their releases need coordination.

You can mix these approaches—for example, one repository per product, with several packages inside each product.

## What each one costs

| Concern | One repository | Separate repositories |
| --- | --- | --- |
| Changes across projects | Can share a commit and review | Need coordinated changes across repositories |
| Testing a compatible set | Test projects from the same checkout | Assemble and test the intended source or published versions |
| Release ordering | One declared dependency graph can order the work | Independent pipelines need coordination or dependency updates |
| Access control | Usually managed at repository level | Can be different for each repository |
| Checkout and CI work | May grow with the shared history; selective jobs help | Each job checks out its own project and any linked sources |
| Pipeline maintenance | Shared setup can serve several packages | Each repository needs a setup, even if it uses a common template |
| Release cadence | Independent or shared package versions | Usually independent; linked releases can coordinate them |

## Which of those costs dispat removes

dispat handles the release work described by its configuration:

- **Dependency ordering.** Providers publish before their consumers publish. Independent branches can run in parallel.
  The dependency settings also determine whether a consumer's build waits for a provider's build or publication.
- **Package versions.** Each package can have its own version, tags, and changelog. Use
  [shared versions](./reference/releasing/versioning.md) when packages should move together.
- **Selecting work.** [`dispat run --since`](./cli/run.md) and
  [`dispat if --changed`](./cli/if.md#changed-packages) help a pipeline run scripts for affected packages, including
  dependants as described by the selection rules. Existing build caches can still speed up those scripts.

An ordinary local run sees the graph in its checkout. A control repository or choreographed fleet can combine linked
repositories into that graph.

## Joining repositories through a control checkout

A [control repository](./control-repository.md) holds the shared configuration and links source repositories as Git
submodules. The source histories stay separate, while one release run coordinates the combined graph.

Choose which history carries release intent:

- **Source-history mode:** dispat reads each linked repository's commits and tags at the pinned source snapshot.
- **Pointer-history mode:** commits in the control repository describe source pointer updates and their release intent.

Without a combined graph, independent runs do not order each other's releases. A consumer must adopt a published
dependency update through its own workflow. Within a configured graph, dispat's
[reconciliation pickup](./configuration/autoversion.md#picking-up-providers-released-without-you) handles versions
published by a provider in an earlier run.

## Joining repositories as peers

A [choreographed fleet](./choreographed-repositories.md) coordinates linked repositories without an extra control
repository. Each peer declares a `repository` identity, can list neighbours in `repositories`, and keeps its own
configuration and release records.

Two-sided submodule links connect the peers. A run started in any peer follows those links and composes the fleet's
release graph. Commit scopes are interpreted within the repository that contains the commit; a peer's commit cannot
address the whole fleet as if it were one local workspace.

Choose this arrangement when ownership should stay with the individual projects. Choose a control repository when a
central release policy and entry point are useful.

## dispat in a single-project repository

One standalone package entry is enough for automatic version planning and release recording. You do not need a space.
The [single-package example](./examples/single-package.md) shows a complete setup.

Two settings often help:

- `tagFormat: "v{version}"` produces tags such as `v1.4.0`. A package prefix is optional when there is only one package.
- `path: "."` makes the repository root the package folder. Scripts run there, alongside the manifest, source, and
  changelog. If the package lives in `src`, `lib`, or another subdirectory, use that path instead. The
  [root-manifest example](./examples/single-package.md#one-root-manifest) shows how to run npm at the root while
  attributing source changes to a subdirectory.

Independent repositories running separate releases each use their own
[release lock](./reference/releasing/release-lock.md). Linked repositories participating in one coordinated release
also follow that setup's fleet-lock rules.

## Moving from many repositories to one

1. Move each project into its own folder. Decide whether to preserve its Git history—for example with `git subtree
   add`—or copy the files and record its starting version explicitly.
2. Configure the package folders. [Configuration beside the code](./examples/layout.md) shows how to split a large
   configuration across local files.
3. Run [`dispat compute`](./cli/compute.md) to review inferred dependencies and `initials` from the manifests. Use
   `--write` when you are ready to save them.
4. Check `dispat status` before releasing. Confirm package names, starting versions, and the proposed dependency order.

A package needs a usable release tag or an `initials` entry to continue from its published version. Moving files alone
does not preserve that information. [Adopting dispat](./examples/adopting.md) walks through the setup with transcripts.

## Moving from one repository to many

Extract the package folder into its new repository. If you preserve its history, keep the relevant release tags. If
you do not, configure `initials` so planning starts from the version already published.

Independent releases no longer share ordering or propagation across the split. A control repository or choreographed
fleet can restore that coordination by linking the sources and declaring the cross-repository dependencies. Review the
extra checkout, permissions, and locking requirements before choosing a linked setup.

## Choosing

Use these questions to narrow the choice:

- **Do changes often span projects?** A shared repository makes those changes easier to review together.
- **Do projects need different access permissions?** Separate repositories usually give you a clearer boundary.
- **Do projects release together or independently?** Either is possible in one repository; linked repositories need an
  explicit coordination setup when their releases depend on each other.
- **Is checkout or CI time already a problem?** Measure where the time goes. Selective jobs may help without moving code.
- **Who owns the shared release process?** That helps decide between local releases, a control repository, and peers.

## Where to go next

- [From one package to many](./examples/one-to-many.md): add deliverables without renaming existing packages.
- [Examples](./examples/README.md): find a setup for your ecosystem or publish target.
- [Concepts](./concepts.md): understand versions, dependency propagation, and prerelease channels.
