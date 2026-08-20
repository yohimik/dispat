# Changelog

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
