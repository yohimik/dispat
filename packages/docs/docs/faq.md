---
description: Answers to common questions about setting up dispat, understanding a release plan, recovering from failures, and working across repositories.
toc_max_heading_level: 2
---

# FAQ

Start with the question closest to what you are trying to do. Each answer links to the guide or reference with the
full details. If you are looking at a warning or error, use the [diagnostic code reference](./reference/plan-errors.md).

- [Getting started](#getting-started): requirements, package support, and your first project.
- [Understanding the plan](#understanding-the-plan): unexpected releases, version changes, and prereleases.
- [Recovering and correcting](#recovering-and-correcting): failed runs, held packages, and release records.
- [Repositories and workers](#repositories-and-workers): linking projects and sharing build work.
- [Running in CI](#running-in-ci): providers and version pinning.
- [Design choices](#design-choices): spaces, submodules, and the single binary.

## Getting started

### What does dispat need before the first release?

You need a Git repository with its full history and tags, plus a dispat configuration naming your packages. Add the
build and publish commands those packages need; stages without a configured command still participate in release
ordering. Conventional Commits tell dispat which versions to release. The native binary does not need a language
runtime, but your commands still need their usual tools and credentials.

Run `dispat status` to inspect the plan before you publish anything. [Getting started](./getting-started.md) walks
through installation, configuration, and that first preview.

### Can I start with one package and add more later?

Yes. Declare one standalone [`packages` entry](./examples/single-package.md); you do not need a space. Add another
entry when you add a website, SDK, or other deliverable, and declare any dependencies between them.

Existing release tags still provide the starting versions. You do not have to rename the first package or restart its
history. See [From one package to many](./examples/one-to-many.md).

### Which languages and package managers does it work with?

The build and publish stages run your shell commands, so packages written in different languages can share a release
plan. For example, one run can build a Go binary, publish an npm library, and push a Docker image.

Automatic manifest reading and version updates have a defined support list. Check
[Manifest tools](./editing/manifests.md) for your format and its limitations. For a version stored outside a supported
manifest, the [replace strategy](./configuration/autoversion.md) can update a configured text pattern. The
[examples](./examples/README.md) show complete setups by ecosystem and delivery target.

### Which commands are safe to use for a preview?

`dispat status` shows planned packages, versions, and reasons without changing files or publishing. `dispat preview`
shows pending release notes. `dispat compute` proposes dependencies and starting versions; adding `--write` applies
those proposals to your configuration.

Running `dispat` with no command starts the release pipeline. See the [CLI overview](./cli/README.md) when choosing a
command for a script or CI job.

## Understanding the plan

### Why did a package I did not touch release?

Check the package's `reason` in `dispat status`. Common reasons are:

| Reason | What happened | Where to read more |
| --- | --- | --- |
| `fixed group versioning` (`W234`) | Another member moved a version shared by the group. | [Shared versions](./reference/releasing/versioning.md) |
| `propagated from core` | A dependency's commit explicitly included consumers, for example `feat(core)^`. | [Commit directives](./reference/commits.md) |
| `catch-up from core` (`W193`) | A dependency published in an earlier run, but this package did not receive that provider release. | [Recovery](./reference/releasing/recovery.md) |

A *provider* is a package another package depends on. A *consumer* is the package using it. Those terms appear in the
plan and throughout the reference pages.

### Why does `status` show fewer packages when I run it inside a package folder?

The current folder selects that package, much like `--package`. Run from the repository root to see the whole plan.

A limited selection may also leave a package waiting when its providers are outside the selection (`W230`). Use
`--strict` if you want dispat to refuse a selection that leaves required work out. See
[Partial releases](./reference/releasing/partial-releases.md).

### Can I release only part of the monorepo?

Yes. Use `--package`, `--space`, or `--group`, or run from the package or space folder. Preview the same selection with
`dispat status` first.

The plan tells you when the selection leaves out dependencies or splits a shared version group. A split group can
catch up on a later run (`W231`, `W234`). Add `--strict` to stop instead of accepting those compromises. See
[Partial releases](./reference/releasing/partial-releases.md).

### Why did my manifest move to a provider version I did not release in this run?

The provider already published that version in an earlier run. When your package next releases, auto-versioning updates
its declared dependency ranges to the provider's current published version. The reconciliation log reports this as `W197`.

This is called [reconciliation pickup](./configuration/autoversion.md#picking-up-providers-released-without-you).
`syncLock` scripts then update lockfiles to match the manifest. The pickup does not add a changelog entry because the
provider's release notes already describe that change.

### Why does a changelog entry say "No changes"?

A package can need a release without having new commits to list. It may be following a shared version group, using a
version set by `Release-As`, changing prerelease channels, or finishing a set of changes cancelled out by reverts.

The entry states that reason rather than leaving an empty heading. [Records](./configuration/records.md#changelog)
lists the messages you can expect.

### How do I run a prerelease line, and how do I end it?

Add a channel directive to a commit. For example, `feat(core)%rc:` starts an `rc` prerelease; when the target version is
`1.3.0`, the first tag is `1.3.0-rc.0`. Later releases increment the counter while that target stays the same.

Use a transition such as `release(core)%rc>stable:` to publish the stable version. Its release notes collect the
prerelease changes into one entry. If graduation would move backwards (`E185`), review the planned version and use
`Release-As` on the graduation when an explicit version is needed. See [Commit messages](./reference/commits.md) and
[Prerelease branches](./reference/releasing/prerelease-branches.md).

## Recovering and correcting

### A release failed halfway. What do I do?

Start with the failed package's log and check what reached the publish destination.

1. If the build failed, fix it and review the next plan with `dispat status`.
2. If publishing failed, check the registry or destination. An upload may have succeeded before dispat could record
   its release tag. Resolve that partial publication before retrying; the publisher's commands must handle their own
   partial writes.
3. Run dispat again when the failed step is ready to retry. Recorded publications remain in place, and the planner
   uses the current history and tags to find the remaining work.

There is no automatic rollback across registries. A rerun also is not a frozen copy of the previous plan: new commits
can change what it needs to release. [Recovering from a failed run](./reference/releasing/recovery.md) covers the
common cases, including an npm upload without a release tag.

### How do I stop something from releasing?

Use a `Release-As: none` footer to hold a package. Its pending changes remain available; a later `Release-As: auto`
resumes planning from the accumulated changes.

If you want to remove pending release-note metadata instead, use `Deletes:` for a particular commit or `cancel(scope)`
for the whole scope. These affect unreleased metadata, not the code changes in those commits. See
[Correcting a record](./reference/corrections.md) before choosing between a hold, a correction, and a cancellation.

### Does dispat ever rewrite git history?

Release recording adds commits, tags, and changelog entries; it does not rewrite published source history. Use `Edits:`
and `Deletes:` in a new commit to correct pending release records while retaining the original commit in Git.

Those footers do not retract a version that has already shipped. For that, follow the package registry's recovery
process and publish a correction. See [Correcting a record](./reference/corrections.md) and
[Recovery](./reference/releasing/recovery.md).

### A previous run left a release lock. Can I remove it?

First confirm the previous run and any workers have stopped. The lock may be protecting work that is still running,
especially after a lost network connection. Do not delete it merely because another run cannot start.

The [release lock guide](./reference/releasing/release-lock.md#clearing-a-lock-that-was-left-behind) explains how to
inspect and clear an abandoned lock. Distributed runs have
[additional recovery checks](./reference/releasing/release-lock.md#a-lock-a-distributed-run-retained).

## Repositories and workers

### Can dispat release packages that live in different repositories?

Yes. Choose one of two ways to assemble their dependency graph:

| Setup | Who owns the configuration and records? | Use it when… |
| --- | --- | --- |
| [Control repository](./control-repository.md) | A central repository defines the combined release policy. Records live there in pointer-history mode, or in each source repository with `polyrepo: true`. | You want one place to coordinate the projects. |
| [Choreographed fleet](./choreographed-repositories.md) | Each peer owns its configuration and records. A run can start in any peer and follow the repository links. | You want to coordinate releases without adding a central repository. |

Both use Git submodule links to assemble a checkout and one combined release plan. Separate, unrelated dispat runs do
not coordinate their dependency graphs automatically.

### Can the builds of one release run on several machines?

Yes. Configure [`execution`](./configuration/execution.md) and run `dispat worker` on the worker machines. The machine
that starts the release remains the orchestrator: it owns the plan, locks, authorizations, and release records.

Workers execute assigned stages, which can include build, test, and publish. Verified output files travel to workers
that need them, so a consumer can use a provider's `dist` directory without committing it. Coordination uses Git
branches, by default in the repository being released. You can configure a separate coordination repository. Workers
poll it and do not need inbound network access.

Start with [Distributed execution](./distributed-execution.md), including its security section before sharing signing
credentials. A release without configured workers runs locally.

### Why doesn't choreography trigger CI jobs in each repository?

Choreography determines which repositories own configuration and release records. It does not require a separate CI
job for each repository. One invocation follows the links, plans the combined graph, and coordinates its release.

This gives cancellation, locking, and recovery one coordinator. Dispatching a chain of CI jobs would also require
provider-specific permissions and a way to stop every downstream job while tracking what each had already published.

Use the [choreography CI setup](./choreographed-repositories.md#ci-checkout) to run that invocation. If you need more
build machines, use [workers](./distributed-execution.md) within the same release.

## Running in CI

### Do I have to use GitHub?

No. dispat needs Git history and the tools your configured shell commands use. GitHub releases are an optional recorder,
and the GitHub Action is one installation method.

[Other CI providers](./reference/ci-providers.md) has setups for GitLab CI, CircleCI, Jenkins, Buildkite, and Azure
Pipelines. Each runner needs full history and tags, repository permissions, and credentials for your publish targets.

### How do I pin the dispat version in CI?

Set the `version` input on the [GitHub Action](./reference/ci.md#the-github-action), or pass `--version` to the
[install script](./getting-started.md#install). Containers also have version-specific image tags.

Without a version, the action and install script choose the latest stable release. Pin a version when you want to
review upgrades before they affect the release job.

## Design choices

### What is a space, and why that name?

A **space** groups packages that share settings, such as build and publish commands. If several libraries follow the
same release process, put them in a space and configure that process once.

The name is short for *workspace*. A single package does not need one. See [Spaces](./configuration/spaces.md) for
folder discovery, shared settings, and package overrides.

### Why use Git submodules to link repositories?

A release needs both a repository's configuration and a known revision of its source. A submodule link provides the
repository path and URL through `.gitmodules`; the parent commit records the source revision as a gitlink. The linked
checkout contains that repository's dispat configuration.

Using those Git records avoids a second checkout manifest that could drift from them. dispat configuration then
specifies which repositories participate and how their packages release. See [Control repositories](./control-repository.md)
and [Choreographed fleets](./choreographed-repositories.md).

### Why aren't scanner and writer separate command-line programs?

They are subcommands: [`dispat scanner`](./cli/scanner.md) and [`dispat writer`](./cli/writer.md). One binary gives local
scripts and CI the same command names and one CLI version to install and pin.

If you are building your own tool, the [Go packages](./go/README.md) are independently importable. You do not need to
run the dispat CLI to use the scanner or writer as a library.

---

Still have a question? Ask on [Discord](https://discord.gg/83PwVSCCmk), or
[open an issue](https://github.com/yohimik/dispat/issues) with the command, dispat version, and relevant output.
