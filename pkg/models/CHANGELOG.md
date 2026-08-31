# Changelog

## pkg/models/v1.5.0 (2026-08-31)

### Features

- sections, commit refs and record link fields ([267f1e3](https://github.com/yohimik/dispat/commit/267f1e3c5b9a9b7d5c735327b4b0c10cb73edb6f)) (by yohimik, Claude Fable 5)
  The record models gain the four options an entry needs to say more than it
  could: `sections`, which states the whole render order of an entry and the
  custom sections in it; `commitRefs`, which names the commit behind an entry
  line; `dependencyLink`, which points a dependency line at the release it moved
  to; and `noChangesText`, for the sentence an entry with nothing to group
  carries. The changelog file gains `entrySpacing`, the blank-line count between
  one entry and the next, with EntrySpacingOrDefault and the Int helper beside
  it for the tri-state pointer the other options already use.

  Every default is the behaviour that was already there, so a configuration
  saying none of this records exactly what it recorded before. SectionConfig
  carries both shapes a list element comes in, a built-in named by key and a
  custom section claiming commit types, told apart by whether types are stated.

### Fixes

- record rendering hardened after review ([24cf9b8](https://github.com/yohimik/dispat/commit/24cf9b83f7c7146227c013c705877fe737d118e4)) (by yohimik, Claude Fable 5)
  A review pass over the new record features closed what it found. An entry
  whose file title renders empty for the release keeps the file's own head
  instead of writing above it, and a preamble containing a fenced example is
  split after the fence rather than inside it. An auto link declines when the
  step environment aligned the updates without their tags, when the resolved
  API URL points outside github.com, and when the repository pair is only half
  configured; the half pair is no longer completed from the environment, and
  the completion itself moved from the renderer to the recorders, so rendering
  no longer reads the process environment at all. A record link inherited from
  a broader layer can be declined with the value off. A no-changes text that
  expands to nothing falls back to the built-in line and says so as W241, and
  one carrying a thematic break on any line is refused at load. The auto base
  is derived once per entry rather than once per line.

### Authors

- yohimik
- Claude Fable 5


## pkg/models/v1.4.0 (2026-08-30)

### Features

- github draft config option
github.draft creates every GitHub release as a draft, so a human reads
the rendered notes before the release goes out. It is a tri-state
boolean defaulting to false and overridable through the whole layering
(root, space, space file, package) exactly like github.allPackages: a
repository can hold every release back while one package publishes
straight away, or the other way round.

A draft carries no tag ref until it is published, so nothing that looks
a release up by its tag sees it meanwhile.

The resolved GitHubSpec carries the flag and its Key() encodes it:
packages sharing a key share one releaser, so a draft setting left out
of the key would leak one package's policy onto a sibling.

## pkg/models/v1.3.0 (2026-08-28)

### Features

- authors in release records
New entry-format `authors` object, shared by `changelog` and `github` and
riding the full configuration ladder, attributes a release record to the
people who wrote it. The identity is git's own, the commit author plus its
Co-authored-by trailers, so no forge is asked and the attribution costs no
API call. Placement is off by default and can be an inline "(by ...)" suffix
per entry line, a section of its own, or both; authors render as full names
or as usernames; the section counts either the commits behind the entry's
own lines or every commit in the window, which is what reaches work that
carried no release record; and include/exclude globs filter the list. All six
keys have flags on `dispat changelog` and `dispat github`, and all six join
the GitHub releaser key so that two packages configured differently cannot
share one releaser and one another's bodies. The DISPAT_* script variables
stay description-only by contract.

`dispat release` now takes the release lock unconditionally, then verifies
the remote, then plans. The `--require-release` pre-plan exception is gone:
whether there is work to do is not known until after planning, and planning
is the thing the lock exists to serialise, so an empty run round-trips the
lock and still exits 3. `dispat status --require-release` remains the
lock-free probe a CI gate should call. The behind-remote check moves ahead of
the plan for the same reason it exists, since a plan built on a stale
checkout recomputes versions somebody else has already published.

## pkg/models/v1.2.0 (2026-08-27)

No changes: a version bump to keep the versioning group on one major and minor version.

## pkg/models/v1.1.0 (2026-08-20)

No changes: a version bump to keep the versioning group on one major and minor version.

## pkg/models/v1.0.0 (2026-08-16)

### Breaking Changes

- commit to the 1.0 interfaces

- drop the dependency edge array form

- restore the 1.0 rc train


### Features

- the public configuration model is stable

- finalize the workspace for 1.0.0

- the dispat configuration schema as a Go module

- accept a list of space paths

- add versioning none mode

- choose the channels lines and records reach

- resolve $ref in config files

- array of scripts, require release

- an updateCheck option

- a package src path that narrows change detection

- prerelease opt-out for changelog and github records

- fixedMajor and fixedMajorMinor versioning modes

- space file model and .dispatignore over config names

- declare manifest names per package

- changelog, commit and autoversion commands

- run consumers

- package add

- per-package overrides, version groups, dispatignore

- shared manifest module, package readmes

- compute auto version

- export

- versioning modes, run command, script outputs, parser config, public models


### Fixes

- exercise the release pipeline across every package

- nested dependencies update againx2

- nested dependencies update again

- nested dependencies update

- dependencies update

- carry the license in every module

- autoversion and compute correctness

- ccme and models API freeze

- graceful shutdown, ancestry cache, scheduler guard

- 1.0.0 blockers


### Dependencies

- ccme: 1.0.0-rc.10 -> 1.0.0

## pkg/models/v1.0.0-rc.19 (2026-08-16)

### Features

- the public configuration model is stable


### Dependencies

- ccme: 1.0.0-rc.9 -> 1.0.0-rc.10

## pkg/models/v1.0.0-rc.18 (2026-08-16)

No changes: a version bump to keep the versioning group on one major and minor version.

## pkg/models/v1.0.0-rc.17 (2026-08-16)

No changes: a version bump to keep the versioning group on one major and minor version.

## pkg/models/v1.0.0-rc.16 (2026-08-16)

No changes: a version bump to keep the versioning group on one major and minor version.

## pkg/models/v1.0.0-rc.15 (2026-08-16)

### Dependencies

- ccme: 1.0.0-rc.8 -> 1.0.0-rc.9

## pkg/models/v1.0.0-rc.14 (2026-08-16)

No changes: a version bump to keep the versioning group on one major and minor version.

## pkg/models/v1.0.0-rc.13 (2026-08-16)

No changes: a version bump to keep the versioning group on one major and minor version.

## pkg/models/v1.0.0-rc.12 (2026-08-16)

### Fixes

- exercise the release pipeline across every package


### Dependencies

- ccme: 1.0.0-rc.6 -> 1.0.0-rc.7

## pkg/models/v1.0.0-rc.11 (2026-08-16)

No changes: a version bump to keep the versioning group on one major and minor version.

## pkg/models/v1.0.0-rc.10 (2026-08-16)

No changes: a version bump to keep the versioning group on one major and minor version.

## pkg/models/v1.0.0-rc.9 (2026-08-16)

### Fixes

- nested dependencies update againx2


### Dependencies

- ccme: 1.0.0-rc.5 -> 1.0.0-rc.6

## pkg/models/v1.0.0-rc.8 (2026-08-16)

No changes: a version bump to keep the versioning group on one major and minor version.

## pkg/models/v1.0.0-rc.7 (2026-08-16)

### Dependencies

- ccme: 1.0.0-rc.4 -> 1.0.0-rc.5

## pkg/models/v1.0.0-rc.6 (2026-08-16)

### Fixes

- nested dependencies update again

- nested dependencies update


### Dependencies

- ccme: 1.0.0-rc.3 -> 1.0.0-rc.4

## pkg/models/v1.0.0-rc.5 (2026-08-16)

### Fixes

- dependencies update


### Dependencies

- ccme: 1.0.0-rc.2 -> 1.0.0-rc.3

## pkg/models/v1.0.0-rc.4 (2026-08-16)

### Breaking Changes

- commit to the 1.0 interfaces


### Features

- the dispat configuration schema as a Go module


### Fixes

- carry the license in every module


### Dependencies

- ccme: 1.0.0-rc.1 -> 1.0.0-rc.2

## pkg/models/v1.0.0-rc.1 (2026-08-09)

### Features

- changelog, commit and autoversion commands


## pkg/models/v1.0.0-rc.0 (2026-08-09)

### Breaking Changes

- initial release

