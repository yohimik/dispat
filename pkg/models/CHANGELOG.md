# Changelog

## pkg/models/v1.11.0-rc.4 (2026-09-23)

### Features

- declare run outputs at the root ([0b71280](https://github.com/yohimik/dispat/commit/0b71280ff30e1ad35ffd36aa42d73bc7c0b2fa2b)) (by yohimik, Claude Opus 5.5)
  A sweep that runs one script across many packages on worker nodes leaves
  its results on those machines unless the script says where it writes.
  `runOutputs` maps a script name to the folders its tasks write, relative
  to the root of each package's repository, so a distributed sweep can carry
  them back to the machine it was started on. It is a root-only key, like
  `execution`, and script names match case-insensitively.

- declare where a package's build and publish may run ([1051b28](https://github.com/yohimik/dispat/commit/1051b2818876f43c4cb92c561f573c25cb2a52de)) (by yohimik, Claude Opus 5)
  Some work must never leave the machine that holds the trust: a build that
  signs its artefact, a publish that logs in. runOnly says so per package,
  as one value for both stages or as a [build, publish] pair.

- let isBuildWaitingPublish describe the relation as an object ([0287f99](https://github.com/yohimik/dispat/commit/0287f9981f1b311ce5fae8ef4f1f3fbefa0d9443)) (by yohimik, Claude Opus 5)
  The key answered one question with a boolean. A provider a consumer never
  reads during its build, and only deploys after, needs a second answer, so
  the key additionally accepts an object naming what the build waits for and
  whether a failed provider outranks a consumer reason of its own.

- let a webhook format name the sending node and the worker ([5c30f13](https://github.com/yohimik/dispat/commit/5c30f137c5792a0e41a24c86cfb626e0f3e86f83)) (by yohimik, Claude Opus 5)
  A release that runs on several machines is read from one endpoint, so a
  template may name the process that sent the event and the node it is about.

- declare a package's build outputs and platforms ([4ecda14](https://github.com/yohimik/dispat/commit/4ecda14d8e29a77196281223b62a5685a0c0d3eb)) (by yohimik, Claude Opus 5)
  A build product is normally ignored by Git, so a checkout says nothing about
  it and a machine that did not run the build cannot learn what to ask for.
  `buildOutputs` is how a package says what its build leaves behind: literal
  paths, relative to the package folder, slash-separated, with the ignored
  files included on purpose. `buildPlatforms` says which machines may run that
  build, in the os/arch spelling a node reports about itself.

  Both keys sit on every level that carries a space-shaped key and replace
  whole, like the alias and webhook lists beside them, so the nearest statement
  is the whole statement and an explicit empty list is how a level opts out.

- declare execution roles, capacity and worker links ([25d8f9b](https://github.com/yohimik/dispat/commit/25d8f9b1064556f43f91986095fbed1504ae628f)) (by yohimik, Claude Opus 5)
  The `execution` object is the node-startup half of CCME section 28: which role
  a node plays, how much work it accepts, where its own mailbox is, and which
  worker nodes an orchestrator may delegate to. It is a pointer on File so that
  an absent key stays absent, and every accessor is nil-safe, which is what
  keeps a configuration that never heard of worker nodes on exactly the path it
  was already on.

- accept a versioning group's sharing axes ([3d65177](https://github.com/yohimik/dispat/commit/3d65177421bbbb31b329df714afb87935b9e1020)) (by yohimik)
  A versionGroups entry's `versioning` key now reads an object naming the
  three axes of a group's rule, `semver`, `counter` and `channels`, beside the
  mode it has always taken. Both shapes reach the same three values through
  one normaliser, and a group that states a mode is written back as one.

### Fixes

- say that an unstated isBlocking is false under a none relation ([5cca67c](https://github.com/yohimik/dispat/commit/5cca67c365b4340900339ed712d2b2a2a2ed8df1)) (by yohimik, Claude Opus 5)
  The doc comment still described the default the key carried before a
  `none` provider stopped skipping a consumer with work of its own, which
  is the opposite of what IsProviderBlocking answers.

- say what commit.force does to a release tag ([cc394c5](https://github.com/yohimik/dispat/commit/cc394c545a1b999cb6ded7d6c06cae407e358293)) (by yohimik, Claude Opus 5)
  The comment promised that a release tag sitting at another commit is left
  alone, which was true of this repository's tags and not of the remote's. It
  now says what the setting is: permission not to fail on a ref that is already
  there, on either side, and never permission to replace a published record.

- report a versioning object's key collisions in a fixed order ([a1bb8d2](https://github.com/yohimik/dispat/commit/a1bb8d23fc008ff63a9ebe916949f97b8e65839d)) (by yohimik, Claude Opus 5)
  The object form read its keys straight off the map, so an entry with more
  than one thing wrong with it could report a different one first on each run,
  and a pair of spellings of one axis could be named in either order. The
  generic decoder sorts its keys for exactly this reason; this one now does
  too.

- write the versioning-group comments in the repository's prose ([e1b83ab](https://github.com/yohimik/dispat/commit/e1b83ab6e4235ae18e5992c2fb20f05345385c84)) (by yohimik, Claude Opus 5)

### Authors

- yohimik
- Claude Opus 5.5


## pkg/models/v1.11.0-rc.3 (2026-09-20)

No changes: a version bump to keep the versioning group on one major and minor version.


## pkg/models/v1.11.0-rc.2 (2026-09-20)

### Fixes

- derive linked releases from repository topology ([35664e1](https://github.com/yohimik/dispat/commit/35664e1685e703c347233421f9faefdba8b1622a)) (by yohimik)
  Remove saga selectors and infer linked ownership from repository identity.
  Let compute choose minimal or star links while preserving existing edges.
  Reject cycles, invalid identities and incompatible topology, exclude disabled
  peers, and resolve link paths relative to the owning repository.

  Include integration regressions for topology, repair failures and exclusions.

### Authors

- yohimik


## pkg/models/v1.11.0-rc.0 (2026-09-15)

### Features

- describe polyrepo configuration and external dependencies ([d5c2ea1](https://github.com/yohimik/dispat/commit/d5c2ea1f85f463adfdd5626902e27381f8018220)) (by yohimik)
  Add opt-in composition, repository commit overrides and explicit history
  baseline tuples. Preserve optional external dependency annotations in
  configuration serialization.

### Authors

- yohimik


## pkg/models/v1.10.0 (2026-09-09)

No changes: a version bump to keep the versioning group on one major and minor version.


## pkg/models/v1.9.0 (2026-09-09)

No changes: a version bump to keep the versioning group on one major and minor version.


## pkg/models/v1.8.0 (2026-09-05)

### Fixes

- preserve configuration types (corrects 3e8b58492eea#1) ([f17f6ef](https://github.com/yohimik/dispat/commit/f17f6ef0d490abb150bf456ebb57a266ddcaece9)) (by yohimik)

### Dependencies

- [ccme](https://github.com/yohimik/dispat/releases/tag/pkg/ccme/v2.0.0): 1.0.0 -> 2.0.0

### Authors

- yohimik


## pkg/models/v1.7.0 (2026-09-02)

No changes: a version bump to keep the versioning group on one major and minor version.


## pkg/models/v1.6.0 (2026-08-31)

No changes: a version bump to keep the versioning group on one major and minor version.


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

