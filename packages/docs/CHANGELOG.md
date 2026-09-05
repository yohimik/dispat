# Changelog

## packages/docs/v1.8.3 (2026-09-05)

### Fixes

- quiet scene loading ([2ee1d10](https://github.com/yohimik/dispat/commit/2ee1d10680769997510ab451b96b685e7a8ef0e7)) (by yohimik)

### Authors

- yohimik


## packages/docs/v1.8.2 (2026-09-05)

### Fixes

- stabilize captions and page flow ([d3235f0](https://github.com/yohimik/dispat/commit/d3235f000ee83df92a29df9a6e6325fb916ec6cc)) (by yohimik)

- refine demos and navigation ([33f88c1](https://github.com/yohimik/dispat/commit/33f88c12de7c5cdd2de67d1440ddc438c038cd84)) (by yohimik)

- remove hero logo row ([0a34d3b](https://github.com/yohimik/dispat/commit/0a34d3baf94b797023d1b2d0c0b52530ee19bee3)) (by yohimik)

- align demos and mobile menus ([2c71b52](https://github.com/yohimik/dispat/commit/2c71b520b46552e8df1bb7ccaf8ab52b7593c483)) (by yohimik)

- keep progress heading clear ([45a1fd6](https://github.com/yohimik/dispat/commit/45a1fd680de782869f52ed4441ba163ab2296076)) (by yohimik)

- fit silent mobile demos ([f770220](https://github.com/yohimik/dispat/commit/f770220f5746ce3322c93f9de37bee1ed1f12568)) (by yohimik)

- keep terminal rows intact ([7d3e018](https://github.com/yohimik/dispat/commit/7d3e018df77fe88c1685953267877f6cfbdc0578)) (by yohimik)

- clarify live demo stories ([984e461](https://github.com/yohimik/dispat/commit/984e461d5b5b2ec344ff018e6b22a65eadd79b49)) (by yohimik)

- unify sidebar categories ([0e5482f](https://github.com/yohimik/dispat/commit/0e5482f3ff87afd9d76a8d06a5f9c898246fa556)) (by yohimik)

### Authors

- yohimik


## packages/docs/v1.8.1 (2026-09-05)

### Fixes

- preserve versioned evidence ([c89a2b0](https://github.com/yohimik/dispat/commit/c89a2b0b484cbb38fcf29c583b613681a98cb6ba)) (by yohimik)

- stabilize landing demos ([d45d331](https://github.com/yohimik/dispat/commit/d45d331257cf85931765901a99d483a7cffa1d26)) (by yohimik)

### Authors

- yohimik


## packages/docs/v1.8.0 (2026-09-05)

### Features

- refresh guides and live demos ([3419488](https://github.com/yohimik/dispat/commit/3419488c1ef374518858386ee94e8060bd354b83)) (by yohimik)

### Fixes

- include live demo build inputs ([1efa6a4](https://github.com/yohimik/dispat/commit/1efa6a404dab623178cfab0668d2a4a48a67ef4b)) (by yohimik)

### Dependencies

- [dispat](https://github.com/yohimik/dispat/releases/tag/services/dispat/v1.8.0): 1.7.2 -> 1.8.0
- [dispat-alpine](https://github.com/yohimik/dispat/releases/tag/docker/dispat-alpine/v1.8.0): 1.7.2 -> 1.8.0

### Authors

- yohimik


## packages/docs/v1.7.3 (2026-09-04)

### Fixes

- the description names the saga polyglot release tool ([d9d906b](https://github.com/yohimik/dispat/commit/d9d906be9b1d3941f1d3555014ca9bf01a7cfb86)) (by yohimik)
  The one-line description is the repository's everywhere it is said: the
  README, the site's tagline, title and metadata, the web manifest, the
  announcement card and its captions, and the CLI's README. The landing
  page names the saga pattern as the run's core and the distributed-systems
  patterns beside it (forward recovery, the tag store as ledger, idempotent
  reruns with exactly-once delivery, compare-and-swap mutual exclusion,
  topological ordering), says which repository shapes it releases, and
  carries the keywords and structured data for monorepo and polyrepo
  searches. The comparison page's experiments section describes the
  harness as it runs now.

  Beside it: the tools module is covered to 98.8% (the verbs, the runners
  and the error arms, behind small writer seams), the twelve experiment
  cells are enumerated once by a `cells` script that both the release
  sweep and the workflow matrix read, and the docs build guard also
  requires every frozen version's sidebar to list the measured pages.

### Authors

- yohimik


## packages/docs/v1.7.2 (2026-09-03)

### Dependencies

- [dispat](https://github.com/yohimik/dispat/releases/tag/services/dispat/v1.7.2): 1.7.1 -> 1.7.2
- [dispat-alpine](https://github.com/yohimik/dispat/releases/tag/docker/dispat-alpine/v1.7.2): 1.7.1 -> 1.7.2


## packages/docs/v1.7.1 (2026-09-02)

### Dependencies

- [dispat](https://github.com/yohimik/dispat/releases/tag/services/dispat/v1.7.1): 1.7.0 -> 1.7.1


## packages/docs/v1.7.0 (2026-09-02)

### Features

- install the conventional asset ([18460f9](https://github.com/yohimik/dispat/commit/18460f9e575d2d5ef61bbee34621fb29fde02470)) (by yohimik, Claude Fable 5)
  A release carrying more than one file was refused unless --asset named
  one, which made the flag mandatory for almost every real repository,
  dispat's own included: its releases carry six binaries and a checksum
  file.

  Without --asset dispat now looks for the name most projects publish
  under, the repository's own name and the platform, with the extension
  selfupdate.AssetName appends on Windows. The name is matched exactly and
  never as a glob, so a bare invocation installs what the release decided
  rather than whichever near-miss sorted first, and a release that follows
  no convention is refused as before, with the name that was tried added
  to the listing so the reader knows what to answer.

  The single-asset shortcut and every explicit --asset path are unchanged.

### Fixes

- finish a conflicted release ([5b8da48](https://github.com/yohimik/dispat/commit/5b8da4826aa2eb6c4a24d89e4bd328b9b24751f3)) (by yohimik, Claude Fable 5)
  A recovery whose merge conflicted aborted and failed the run, which
  leaves a release that has already published with its commit and tags
  nowhere but the local clone. It completes instead.

  This release's side wins every conflicting file, because that is the
  tree the tag names and taking the other side would publish content the
  release never saw; everything the arriving commits changed that did not
  conflict is in the merge as it is on the clean path. Their side is
  pushed to a branch of its own, release-conflicts/ followed by what the
  leg released and a UTC timestamp, plain and never forced, so the work is
  kept rather than dropped. Both records name the conflicting files and
  that branch: the GitHub body through a note block, the changelog through
  the merge commit, since the release commit is tagged and must not be
  amended. W243 says the same thing in the log, and the run exits 0.

  The tag invariant is untouched, and the republish guard and the bounded
  retry cover this path's pushes too. E224 is now only for the recovery
  machinery failing: the quarantine branch refused, the settled merge
  uncommittable, or a merge that stopped for something other than content.

  The e2e walk gains the two cycles this is about, and the key-features
  walk keeps the private install; both are what the release build runs
  against the bytes it exports.

- keep a released tag from moving ([96cdb2b](https://github.com/yohimik/dispat/commit/96cdb2b720309d6ca8173252e0fa5501e1b87707)) (by yohimik, Claude Fable 5)
  Four ways the mid-release recovery was wrong.

  It re-pushed with commit.force, which defaults on, so a checkout stale
  enough to have re-planned an already published version would force-move
  that published tag: the push it recovers from never reached a tag ref,
  so nothing stood between the two. The remote's tags are now read first
  and the run stops, naming the tag. Aliases stay movable, because moving
  them is what every release does.

  It fired on "[rejected]" alone, and the simultaneous-push race prints
  "[remote rejected]", so the phrase it was meant to recognise was
  unreachable. The gate now matches both, and every git invocation asks
  for the C locale so a translated checkout cannot defeat the match. The
  sentinel also stopped leading the message: git's own words are what a
  reader of a failed release needs first.

  The merge was refused outright by a repository configured merge.ff=only,
  and the abort that followed failed too. It is now made with --no-ff,
  which also pins the first-parent shape the recovery documents.

  It gave up if a commit landed between its own pull and its own push,
  which is the very surprise it exists to absorb. It now goes round up to
  three times, capturing the release commit once so the tag it names never
  moves whichever round lands.

- record the release commit ([d3299f9](https://github.com/yohimik/dispat/commit/d3299f9673ba568a68fcf7f857507a43a46a70b1)) (by yohimik, Claude Fable 5)
  The GitHub release is created after the push, and after a mid-release
  recovery HEAD is the merge by then, so the "commit" line in its body and
  its target_commitish named the merge rather than the release the record
  is about.

  The recovery already reads the release commit before merging, since the
  merge message names it. It now hands that back, and the finalize phase
  prefers it over HEAD when stamping the releasers. A run with no recovery
  sets nothing and reads HEAD exactly as before.

- read past a package's own alias ([4edbab5](https://github.com/yohimik/dispat/commit/4edbab5dca43d159b45fe05d297c1c7cb55c0804)) (by yohimik, Claude Fable 5)
  A single-package repository releasing as "v1.4.2" could not declare the
  "v1" a GitHub composite is consumed through: the load-time check refused
  any alias whose name matched a package's tagFormat, and "v1" matches
  "v{version}" on the prefix alone. The refusal existed because the
  baseline reader would take the alias for the newest release tag and read
  no version out of it, leaving the package looking unreleased.

  Both halves now ask what a name can be read back as rather than what it
  looks like. The check refuses an alias only when it parses as a release
  tag, so "v1.4.2" beside "v{version}" stays refused and "v1" becomes
  legal. The tag listing drops a name that carries no version when one of
  the package's own alias formats could have written it, which is precise
  enough to leave a mistyped release tag like "v1.0.0.0" in place, where
  the initials fallback still measures the window from it.

  Recognising an alias is a matcher rather than a prefix test, so {major},
  {minor} and {patch} capture one number and stop at a separator. Both
  readers of a baseline go through the same filter: the planner and the
  compute command's manifest baselines.

### Dependencies

- [dispat](https://github.com/yohimik/dispat/releases/tag/services/dispat/v1.7.0): 1.6.0 -> 1.7.0

### Authors

- yohimik
- Claude Fable 5


## packages/docs/v1.6.0 (2026-08-31)

### Dependencies

- [dispat](https://github.com/yohimik/dispat/releases/tag/services/dispat/v1.6.0): 1.5.0 -> 1.6.0


## packages/docs/v1.5.0 (2026-08-31)

No changes.


## packages/docs/v1.4.0 (2026-08-30)

### Features

- the download command has a page
The command page covers naming a repository, choosing the file and the
destination, the pipe for an asset that is not a binary, the idempotence
gate, the tag prefix, and the rollback. The CI guide gains the section
it belongs in, since a runner that already has dispat needs no second
downloader, and the self-update guide points at the other half of the
same engine. Mirrored into version-1.3, which is what the site serves.

The roadmap entry that described this as future work now describes what
is left of it: the declarative half, where a configuration lists the
tools a stage needs.

### Fixes

- the download variables reach the canonical listings
DISPAT_BIN_DIR joins the switches dotenv.md names as the variables
dispat reads for itself, and the environment reference points at the
two variables the pipe hands its command, so the page that promises
every DISPAT_* variable keeps that promise.

### Dependencies

- dispat: 1.3.1 -> 1.4.0

## packages/docs/v1.3.2 (2026-08-28)

### Dependencies

- dispat: 1.3.0 -> 1.3.1

## packages/docs/v1.3.1 (2026-08-28)

### Fixes

- the footer columns wrap through a grid
The flex row either held every column or crushed them, and wrapping under
space-between flung a partial row to the edges. The auto-fit grid degrades
from three columns through two to a single stack, every column keeping the
same measure as its neighbours at every step; infima's row margins and col
padding are neutralised since the grid gap owns the spacing now.
- the projects grid is sized by its content
The section reused the two-column features grid, so the single project the
README lists today rendered beside an empty half. auto-fit lets one card
take the row and packs future additions into columns on their own.

## packages/docs/v1.3.0 (2026-08-28)

### Dependencies

- dispat: 1.2.0 -> 1.3.0

## packages/docs/v1.2.0 (2026-08-27)

### Dependencies

- dispat: 1.1.1 -> 1.2.0

## packages/docs/v1.1.10 (2026-08-26)

### Dependencies

- dispat: 1.1.0 -> 1.1.1

## packages/docs/v1.1.9 (2026-08-26)

### Fixes

- logo transparency


## packages/docs/v1.1.8 (2026-08-26)

### Fixes

- color scheme


## packages/docs/v1.1.7 (2026-08-26)

### Fixes

- logo colors


## packages/docs/v1.1.6 (2026-08-26)

### Fixes

- match logo


## packages/docs/v1.1.5 (2026-08-25)

### Dependencies

- infra: 0.0.0 -> 0.0.1

## packages/docs/v1.1.4 (2026-08-25)

### Fixes

- minified styles


## packages/docs/v1.1.3 (2026-08-25)

### Fixes

- slides added on landing page


## packages/docs/v1.1.2 (2026-08-24)

### Fixes

- embed the demo animations


## packages/docs/v1.1.1 (2026-08-24)

### Fixes

- improve readability


## packages/docs/v1.1.0 (2026-08-20)

### Dependencies

- dispat: 1.0.2 -> 1.1.0

## packages/docs/v1.0.12 (2026-08-20)

### Fixes

- a control repository for many repositories
The pattern that gets the graph back without merging anyone's code: one
small repository holding every configuration, linking the product
repositories in as git submodules. A pointer bump is a commit touching one
path, so dispat reads it as a change to that package and the fleet plans,
orders and propagates as a single monorepo, while nobody in the linked
repositories touches a dispat file.

Both layouts get a section: a wrapper folder, where the changelog lands
somewhere the release commit can stage it, and the submodule mounted as the
package, where it does not. Both build models too, building in the control
repository and triggering each repository's own pipeline. Plus the sync
script that turns upstream subjects into --- separated units, the parser
settings that implies, the cross-repository edges compute derives on its
own, and what to watch for.

Every transcript is a real run against throwaway fleets built for it.

monorepo.md and the FAQ point at it, since the first says outright that
dispat cannot span repositories.

## packages/docs/v1.0.11 (2026-08-19)

### Fixes

- wire game engines


### Dependencies

- dispat: 1.0.1 -> 1.0.2

## packages/docs/v1.0.10 (2026-08-19)

### Dependencies

- dispat: 1.0.0 -> 1.0.1

## packages/docs/v1.0.9 (2026-08-18)

### Fixes

- one repository or many, and a tidier examples index
A page for the decision itself: what a monorepo and a repository each
actually cost, which of those costs dispat removes (ordering, per-package
cadence, CI that scales with the diff), where it does not help (there is no
graph across repositories), and how to move either direction without losing
published versions.

The examples index loses its duplicated "start here" table for a two-line
reading path, and the five repository-shaping pages become a group of their
own in both sidebars instead of sitting loose after the ecosystems.

## packages/docs/v1.0.8 (2026-08-18)

### Fixes

- what dispat buys a game project as it grows
The game example now opens with the case for adopting it: one version
number across the engine file, the tag, the Steam build and the itch
upload; changelogs and announcements written from the commits; a patch
that takes the same path as a release. Ends on the reason a single game
binary is worth setting up this way, which is that it does not stay a
single binary once a landing page, an SDK and a server arrive.

The examples index carries the same point in its lead.

## packages/docs/v1.0.7 (2026-08-18)

### Fixes

- examples per ecosystem, game stores and other CI providers
Seventeen worked examples: one per manifest ecosystem, so all 23 formats
the scanner and writer cover are demonstrated somewhere; a game repository
that starts as one package and grows; Steam and itch.io publishing; and the
release job on GitLab CI, CircleCI, Jenkins, Buildkite and Azure Pipelines.

Corrects three pages that still said manifest rewriting only covered
package.json, go.mod and requirements*.txt, and three writer transcripts
that printed "replace" and "patch" where the CLI prints "link".

## packages/docs/v1.0.6 (2026-08-17)

### Fixes

- why one more monorepo tool update


## packages/docs/v1.0.5 (2026-08-17)

### Fixes

- title updated


## packages/docs/v1.0.4 (2026-08-16)

### Fixes

- the faq reaches the 1.0 line


## packages/docs/v1.0.3 (2026-08-16)

### Fixes

- the questions the first users actually ask


## packages/docs/v1.0.2 (2026-08-16)

### Fixes

- projects using dispat carry the first stable run's numbers


## packages/docs/v1.0.1 (2026-08-16)

### Fixes

- announce the stable release


## packages/docs/v1.0.0 (2026-08-16)

### Breaking Changes

- commit to the 1.0 interfaces

- restore the 1.0 rc train


### Features

- the documentation covers the stable surface

- finalize the workspace for 1.0.0

- anchor the palette on dark pine

- the documentation site, versioned and deployed by dispat

- read projects using dispat onto the landing page

- exit 3 when --require-release finds nothing to release

- split the docs and api sidebars

- muted pine palette

- build the landing page from both READMEs

- read the README and the test report into the site

- array of scripts, require release

- forward arguments after -- to run and exec scripts

- trace and debug logging for git, config and the plan

- show the tests and coverage badges on the landing page

- list ccme beside the manifest libraries instead of in the footer

- document the standalone manifest scanner and writer on the landing page


### Fixes

- exercise a caret that reaches nobody

- exercise a partial-mode member release mid-train

- exercise the release pipeline across every package

- nested dependencies update againx2

- nested dependencies update again

- nested dependencies update

- dependencies update

- the diagnostic registry names the step alignment codes

- the tile color joins the pine palette

- wrap long inline code instead of overflowing a phone

- span the last feature card only on an odd count

- let the footer columns wrap on mobile

- pin the pwa reload popup theme alias

- keep the install blocks inside a phone's viewport

- keep the landing page inside a phone's viewport

- sync manifests and changelogs for every updated provider

- 1.0.0 release blockers

- centre the landing page text

- give the commit parser its own row in the libraries grid

- close the prerelease-train code fence in concepts

- serve a landing page at the site root instead of a 404


### Dependencies

- dispat: 1.0.0-rc.19 -> 1.0.0

## packages/docs/v1.0.0-rc.19 (2026-08-16)

### Features

- the documentation covers the stable surface


### Dependencies

- dispat: 1.0.0-rc.18 -> 1.0.0-rc.19

## packages/docs/v1.0.0-rc.18 (2026-08-16)

### Fixes

- exercise a caret that reaches nobody


### Dependencies

- dispat: 1.0.0-rc.17 -> 1.0.0-rc.18

## packages/docs/v1.0.0-rc.17 (2026-08-16)

### Dependencies

- dispat: 1.0.0-rc.16 -> 1.0.0-rc.17

## packages/docs/v1.0.0-rc.16 (2026-08-16)

### Dependencies

- dispat: 1.0.0-rc.15 -> 1.0.0-rc.16

## packages/docs/v1.0.0-rc.15 (2026-08-16)

### Dependencies

- dispat: 1.0.0-rc.14 -> 1.0.0-rc.15

## packages/docs/v1.0.0-rc.14 (2026-08-16)

### Fixes

- exercise a partial-mode member release mid-train


### Dependencies

- dispat: 1.0.0-rc.13 -> 1.0.0-rc.14

## packages/docs/v1.0.0-rc.13 (2026-08-16)

### Dependencies

- dispat: 1.0.0-rc.12 -> 1.0.0-rc.13

## packages/docs/v1.0.0-rc.12 (2026-08-16)

### Fixes

- exercise the release pipeline across every package


### Dependencies

- dispat: 1.0.0-rc.11 -> 1.0.0-rc.12

## packages/docs/v1.0.0-rc.11 (2026-08-16)

### Dependencies

- dispat: 1.0.0-rc.10 -> 1.0.0-rc.11

## packages/docs/v1.0.0-rc.10 (2026-08-16)

### Dependencies

- dispat: 1.0.0-rc.9 -> 1.0.0-rc.10

## packages/docs/v1.0.0-rc.9 (2026-08-16)

### Fixes

- nested dependencies update againx2


### Dependencies

- dispat: 1.0.0-rc.8 -> 1.0.0-rc.9

## packages/docs/v1.0.0-rc.8 (2026-08-16)

### Dependencies

- dispat: 1.0.0-rc.7 -> 1.0.0-rc.8

## packages/docs/v1.0.0-rc.7 (2026-08-16)

### Dependencies

- ccme: 1.0.0-rc.4 -> 1.0.0-rc.5
- dispat: 1.0.0-rc.6 -> 1.0.0-rc.7

## packages/docs/v1.0.0-rc.6 (2026-08-16)

### Fixes

- nested dependencies update again

- nested dependencies update


### Dependencies

- dispat: 1.0.0-rc.5 -> 1.0.0-rc.6

## packages/docs/v1.0.0-rc.5 (2026-08-16)

### Fixes

- dependencies update


### Dependencies

- dispat: 1.0.0-rc.4 -> 1.0.0-rc.5

## packages/docs/v1.0.0-rc.4 (2026-08-16)

### Breaking Changes

- commit to the 1.0 interfaces


### Features

- anchor the palette on dark pine

- the documentation site, versioned and deployed by dispat


### Fixes

- the diagnostic registry names the step alignment codes

- the tile color joins the pine palette


### Dependencies

- dispat: 1.0.0-rc.3 -> 1.0.0-rc.4
