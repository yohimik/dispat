# One repository or many

dispat does not decide whether you keep everything in one repository or give each project its own. This page explains
the differences between the two setups. It covers which differences dispat removes and how you can move between them
without losing your published versions.

dispat works either way. It exists because the release side of a monorepo is genuinely harder, and it is perfectly
happy in a repository holding one thing.

## The two shapes

**One repository.** Every project is a folder. You get one checkout, one history, and one CI configuration. You have
one place to change something that crosses two projects.

**A repository each.** Every project is its own repository with its own history, permissions, and pipeline. Projects
talk to each other only through published versions.

Both are legitimate, and plenty of teams run a mixture. They use a repository per product, and each repository holds
the several packages that make up the product.

## What each one costs

| | One repository | A repository each |
|---|---|---|
| A change crossing two projects | one commit, one review, one CI run | one pull request per repository, merged in the right order |
| Knowing what works with what | the checkout is a tested combination | you find out when someone installs the published versions |
| Release ordering | the tool's problem, if the tool understands the graph | yours, by hand or by waiting |
| Access control | mostly all or nothing | per repository, naturally |
| Checkout and CI cost | grows with the whole repository unless jobs are selective | small, always |
| Release pipelines to maintain | one | one per repository |
| Independent cadence | needs per-package versions | free |

Read that table as the honest version of the argument. A monorepo trades convenience across projects for size and
coupling. Separate repositories trade coordination cost for independence.

## Which of those costs dispat removes

dispat targets exactly three rows of that table.

**Release ordering.** dispat reads the dependency graph in one repository and releases in topological order. A library
publishes before the service that consumes it, and packages with no relation publish in parallel. You do not schedule
that by hand.

**Independent cadence in one repository.** Each package keeps its own version, tag, and changelog. One commit can
release one package, several packages, or the whole repository, which stops a monorepo from meaning that everything
ships at once. [Shared versions](./reference/releasing/versioning.md) covers the opposite choice for packages that
should move together.

**CI cost.** A monorepo only gets expensive if every job does everything. Run [`dispat run --since`](./cli/run.md) and
[`dispat if --changed`](./cli/if.md#changed-packages) to execute a script only in the packages a change reached. This
includes dependants, so the pipeline scales with the diff rather than with the repository.

dispat does not make a monorepo out of separate repositories. Its graph is the packages in one checkout.

## Where dispat does not help: across repositories

dispat cannot order releases or propagate a version between two projects in different repositories. There is no graph
spanning them because there is no single history to read.

Separate repositories mean the only connection is a published version, and picking that version up is the consumer's
own decision. This is the definition of the choice rather than a gap to work around. A bot like Renovate opens a pull
request for dispat to release, and the
[reconciliation pickup](./configuration/autoversion.md#picking-up-providers-released-without-you) handles this
situation automatically inside one repository.

You can get the graph back without merging anything by using a repository that holds only the configuration and links
the others as git submodules. dispat runs in this single checkout to order releases and propagate versions across every
linked repository while the code stays where it is.
[A control repository for many repositories](./control-repository.md) explains this pattern and its costs.

## dispat in a single-project repository

dispat does not require more than one package. A repository delivering one thing declares one standalone entry to get
automatic versioning, changelogs, tags, and releases from its commits. [A single package](./examples/single-package.md)
shows this setup in full.

Two details are different at that size:

- Set `tagFormat: "v{version}"` to get `v1.4.0` rather than `app@1.4.0`. You do not need the prefix because there is no
  second package to disambiguate from.
- You must set `path` to a directory inside the repository. The code sits in `src`, `app`, or whatever you already
  build from, while the config, changelog, and tags live at the root.

A fleet of small repositories each running dispat is a completely ordinary setup. Every repository gets its own
[release lock](./reference/releasing/release-lock.md). The lock is a tag on that repository's own remote, so they never
wait for each other.

## Moving from many repositories to one

The work is mostly git, and the dispat portion is one command.

1. Bring the histories together using `git subtree add` or a plain copy so each project ends up in its own folder.
2. Write a config naming the folders. [Keeping configuration beside the code](./examples/layout.md) shows a layout
   where each space and package holds its own file to keep a big repository readable.
3. Run [`dispat compute --write`](./cli/compute.md). It reads the manifests and writes the `dependencies` edges between
   the packages, alongside the `initials` each package starts from. This ensures you do not lose the versions you
   already published.
4. Run `dispat status` before you release anything. It reports what a release would do and changes nothing.

The versions carry over because a baseline is a tag or an `initials` entry. Both survive the move.
[Adopting dispat](./examples/adopting.md) shows the same procedure with transcripts.

## Moving from one repository to many

The reverse is no harder. Extract the folder into its own repository, and keep the tags for that package if the history
came with it, or state `initials` if it did not. The package's next release continues from the version it was on rather
than starting at zero.

You lose the ordering and the propagation between the split-off project and what remains. This is the same trade the
table above describes. You gain independence.

## Choosing

These questions decide the shape faster than a general argument:

- **Do changes routinely touch two projects at once?** If yes, one repository saves you the coordination every time.
- **Do the projects release on genuinely unrelated schedules, for unrelated audiences?** If yes, separate repositories
  cost you little.
- **Does anybody need access to one project and not the others?** This is the strongest argument for separate
  repositories. It is also the hardest to work around.
- **Is the repository large enough that a full checkout is a problem?** Measure before deciding. It usually happens
  later than people expect.
- **Are you splitting to make releases simpler?** This is the one reason worth re-examining. dispat already handles the
  release complexity.

## Where to go next

- [A game, from one package to many](./examples/game.md) walks through growth in one repository. You start with one
  deliverable today and add four more later without restructuring.
- [Examples](./examples/README.md) provides a complete setup per ecosystem. Most of them are a handful of lines.
- [Concepts](./concepts.md) explains versions, propagation, and channels. The rest of the documentation uses this
  vocabulary.
