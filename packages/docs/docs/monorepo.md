# One repository or many

Whether to keep everything in one repository or give each project its own is a decision dispat does not make for you.
This page is what actually differs between the two, which of the differences dispat removes, and how to move from one
to the other later without losing the versions you have already published.

The short version: dispat works either way. It exists because the release side of a monorepo is the part that is
genuinely harder, and it is also perfectly happy in a repository holding one thing.

## The two shapes

**One repository.** Every project is a folder. One checkout, one history, one CI configuration, one place to change
something that crosses two projects.

**A repository each.** Every project is its own repository with its own history, its own permissions and its own
pipeline. Projects talk to each other only through published versions.

Both are legitimate, and plenty of teams run a mixture: a repository per product, each holding the several packages
that product is made of.

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

dispat is aimed at exactly three rows of that table.

**Release ordering.** In one repository, dispat reads the dependency graph and releases in topological order: a
library publishes before the service that consumes it, and packages with no relation publish in parallel. Nobody
schedules that by hand.

**Independent cadence in one repository.** Each package keeps its own version, its own tag, its own changelog. One
commit can release one package, or several, or the whole repository. That is what makes a monorepo stop meaning
"everything ships at once", which is the objection most people actually have to the shape.
[Shared versions](./reference/releasing/versioning.md) covers the opposite choice, for packages that should move
together.

**CI cost.** A monorepo only gets expensive if every job does everything.
[`dispat run --since`](./cli/run.md) and [`dispat if --changed`](./cli/if.md#changed-packages) run a script only in the
packages a change reached, dependants included, so the pipeline scales with the diff rather than with the repository.

What dispat does not do is make a monorepo out of separate repositories. Its graph is the packages in one checkout.

## Where dispat does not help: across repositories

If two projects live in different repositories, dispat cannot order their releases or propagate a version from one to
the other. There is no graph spanning them, because there is no single history to read.

That is not a gap to work around so much as the definition of the choice: separate repositories mean the only
connection is a published version, and picking that version up is the consumer's own decision. In practice a bot such
as Renovate or Dependabot opens the pull request, and dispat in the consumer's repository then releases it like any
other change. The [reconciliation pickup](./configuration/autoversion.md#picking-up-providers-released-without-you)
handles the same situation inside one repository automatically, which is a fair summary of what the shape buys you.

## dispat in a single-project repository

Nothing about dispat requires more than one package. A repository whose whole deliverable is one thing declares one
standalone entry and gets automatic versioning, changelogs, tags and releases from its commits.
[A single package](./examples/single-package.md) is that setup in full.

Two details are different at that size, and both are small:

- The tag can stay plain. `tagFormat: "v{version}"` gives `v1.4.0` rather than `app@1.4.0`, because there is no second
  package to disambiguate from.
- The package still lives in a folder. `path` must name a directory inside the repository, so the code sits in `src`,
  `app` or whatever you already build from, while the config, the changelog and the tags live at the root.

A fleet of small repositories each running dispat is a perfectly ordinary way to use it. Every repository gets its own
[release lock](./reference/releasing/release-lock.md), since the lock is a tag on that repository's own remote, so
they never wait for each other.

## Moving from many repositories to one

The work is mostly git, and dispat's part of it is one command.

1. Bring the histories together however you prefer, `git subtree add` or a plain copy, so each project ends up in its
   own folder.
2. Write a config naming the folders. [Keeping configuration beside the code](./examples/layout.md) shows the layout
   where each space and package holds its own file, which keeps a big repository readable.
3. Run [`dispat compute --write`](./cli/compute.md). It reads the manifests and writes the `dependencies` edges
   between the packages, and the `initials` each package starts from, so the versions already published are not lost.
4. Check `dispat status` before releasing anything. It reports what a release would do and changes nothing.

The versions carry over because a baseline is a tag or an `initials` entry, and both survive the move.
[Adopting dispat](./examples/adopting.md) is the same procedure written out with transcripts.

## Moving from one repository to many

The reverse is no harder. Extract the folder into its own repository, keep the tags for that package if the history
came with it, and state `initials` if it did not. The package's next release continues from the version it was on
rather than starting again at zero.

What you lose in the split is the ordering and the propagation between the split-off project and what remains, which
is the same trade the table above describes. What you gain is the independence.

## Choosing

Some questions that decide it faster than a general argument:

- **Do changes routinely touch two projects at once?** If yes, one repository saves you the coordination every time.
- **Do the projects release on genuinely unrelated schedules, for unrelated audiences?** If yes, separate repositories
  cost you little.
- **Does anybody need access to one project and not the others?** That is the strongest argument for separate
  repositories, and the hardest to work around.
- **Is the repository large enough that a full checkout is a problem?** Measure before deciding; it is usually later
  than people expect.
- **Are you splitting to make releases simpler?** That is the one reason worth re-examining, because it is the part
  dispat already handles.

## Where to go next

- [A game, from one package to many](./examples/game.md) walks the growth in one repository: one deliverable today,
  four more later, without restructuring.
- [Examples](./examples/README.md) has a complete setup per ecosystem, most of them a handful of lines.
- [Concepts](./concepts.md) explains versions, propagation and channels, which is the vocabulary the rest uses.
