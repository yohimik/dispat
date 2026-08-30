# Changelog

## services/dispat/v1.4.0 (2026-08-30)

### Features

- minified linux binaries ride the release
A release now carries dispat-tiny-linux-amd64 and
dispat-tiny-linux-arm64 beside the six it always has: the same source
and the same version stamp, built by the TinyGo fork at 0.42.0-net.4,
at roughly 60% of the bytes. They are additive downloads under names
of their own, so dispat-<os>-<arch>, which self-update and install.sh
resolve, is untouched.

The toolchain arrives as its release tarball by URL and against the
digest that release published, not through dispat install: a build of
dispat that needs a working dispat to start is a bootstrap cycle. The
checksum is the part install would have done, kept. Debian rather
than alpine, because the fork ships a glibc-linked LLVM and musl does
not run it; what comes out is static either way.

A TinyGo binary carries no Go build info, so the tiny pair is proven
by running it rather than by reading it back, which the smoke loop
now does for all four linux binaries. The spike stays the deep gate,
and internals/tinygo.md carries what those binaries can do.
- the loaded configuration says what it holds
The post-load debug line said which file was read and which folder
became the root, which answers "did it read the file I meant" and
nothing after it. A configuration that read as almost nothing — a $ref
resolving to an empty fragment, a spaces object under a key nobody
meant — still looks like a run that simply found no work.

So the line now counts what the loader made of the file: the package
entries it names, the scripts it binds and the webhooks it notifies,
across every level of the root file that may declare one. In-folder
files are deliberately not counted; they are read later, and a number
that grew afterwards would describe a configuration nobody had yet.
- first-party config decoder
The config language is a table now: one entry per key a file may write,
saying what writing it does. A key with no entry is a key the model has
no field for, so the unknown-key refusal every typo lands in is
structural rather than a setting somebody remembered to turn on.

What this replaces is a reflected decoder told its exceptions through
hooks that fire on a Go type and cannot see the key that produced it.
The hazard was never hypothetical: the conversion lifting a scalar into
a list splits it on commas, which is right for a list of script names
and wrong for a shell command, and the two were kept apart only by the
order the hooks were composed in. They are different setters here, so
the order that used to matter cannot exist.

Nothing calls it yet. fields_test.go reads the models' own json tags and
refuses any disagreement with the tables in either direction, and
decode_parity_test.go runs a corpus through both decoders and fails on
any difference the migration did not declare.
- the command that installs a tool is called install
The word says what the command leaves behind rather than how it gets
there: `dispat install <repo>` puts a verified release asset on PATH,
and download was the mechanism, not the outcome. The flags, the
behavior and the machinery are unchanged; the command word, the
package (internal/install), the error prefixes and the report texts
follow the new name, and every doc page moves with it.

`install` permanently shadows a run script of the same name, as every
command word does; a script called install stays reachable as `dispat
run install` and from flow sequences, which the example configs use it
in.
- download installs a tool from any github release
dispat download <repo> is self-update pointed at somebody else's
repository: the same listing walk, the same streamed download checked
against the published size and checksum, the same two renames that keep
what they replace. It needs no config file and no git repository.

The repository is named however it is at hand, and a host that is not
github.com derives a GitHub Enterprise endpoint. --asset says which of
the release's files is the binary, as a name or a glob, with {os},
{arch}, {version}, {tag} and {name} expanded; nothing is guessed, since
the wrong guess is installed globally and run. --bin-dir and --as say
where it goes and what it is called, defaulting to the ladder install.sh
climbs. --pipe hands the verified file to a command in that folder
instead, which is how an archive is unpacked and a release's own install
script is run.

The destination is hashed against the release's digest, so the command
is idempotent: --check gates on it and --force installs over it.
--rollback restores what the last download replaced. GITHUB_TOKEN is
sent to github.com alone, because the endpoint comes from an argument.

### Fixes

- a record line is read after it is decoded
A record line written as an object went through `return line,
decodeObject(item, at, entryLineFields(&line))`, which leaves the order
of copying line and running the call to the compiler. gc runs the call
first and returns the filled line; TinyGo copies line first and returns
it empty, so every object-form footer, header and fileTitle decoded to
nothing and the load failed with "line is required". Found by the
0.42.0-net.2 validation run; no assertion can catch it under gc, so the
sequencing comment is the guard.

### Dependencies

- models: 1.3.0 -> 1.4.0

## services/dispat/v1.3.1 (2026-08-28)

### Fixes

- catch-up records span the provider's movement
A catch-up picks up a provider that published in an earlier run, so by the
time its records are written the provider's own before-and-after have
collapsed onto the published version — and the changelog entry, the GitHub
release body and the DISPAT_UPDATED_* variables all said "1.3.0 -> 1.3.0",
a movement line with no movement, which is what the docs leg of the 1.3.0
release shipped. From is now what the package's previous release shipped
against, reconstructed off the provider's tags at the package's own
baseline, the same way a graduation spans its train; the step commands
inherit the span through the plan they recompute.

A ride catching up documents the movement it rode for by the same
reconstruction: its provider is not releasing and nothing propagated, so
the record loops found nothing at all, and the ride's entry stayed silent
about the one thing it existed to ship — where the same ride in a
single-run release names the provider's movement. An own-cause release's
manifest-only pickup stays out of the record, as it always has.

## services/dispat/v1.3.0 (2026-08-28)

### Features

- changelogs and github releases authors


### Dependencies

- models: 1.2.0 -> 1.3.0

## services/dispat/v1.2.0 (2026-08-27)

### Features

- external webhooks


### Dependencies

- models: 1.1.0 -> 1.2.0

## services/dispat/v1.1.1 (2026-08-26)

### Fixes

- window-only run selections without the script no-op


## services/dispat/v1.1.0 (2026-08-20)

### Features

- self-update prints changelog


### Dependencies

- models: 1.0.0 -> 1.1.0

## services/dispat/v1.0.2 (2026-08-19)

### Dependencies

- manifest: 1.1.0 -> 1.1.1
- scanner: 1.1.0 -> 1.1.1
- writer: 1.1.0 -> 1.1.1

## services/dispat/v1.0.1 (2026-08-19)

### Fixes

- manifest libraries updated providing unity, unreal, godot, o3de and defold manifests supported


### Dependencies

- manifest: 1.0.0 -> 1.1.0
- scanner: 1.0.0 -> 1.1.0
- writer: 1.0.0 -> 1.1.0

## services/dispat/v1.0.0 (2026-08-16)

### Breaking Changes

- commit to the 1.0 interfaces

- rename autosubstute to autoreplacer

- restore the 1.0 rc train

- replace the channel sigil with percent


### Features

- the release engine is ready for the stable line

- finalize the workspace for 1.0.0

- the installers explain PATH permanence and shadowing

- debug shows git mutations, trace shows starting scripts

- the run start and the lock answer at info

- warn a github step running before the run's tag

- step commands align to the run's environment

- release polyglot monorepos from conventional commits

- report the lock holder and age in the refusal

- trace scope, propagation and group derivation

- surface .dispatexclude exclusions at debug

- reconcile missing assets on an existing github release

- retry transient github lookups with backoff

- wire multi path spaces downstream

- accept a list of space paths

- report and guard none packages

- exclude none packages from the release plan

- reject releasable deps on none packages

- add versioning none mode

- expand the changed window before narrowing

- wire if changed and file conditions

- add changed selection lookup

- add resolved conditions

- exit 3 when --require-release finds nothing to release

- preview the changelog or github body

- choose the channels lines and records reach

- let a $ref name several files

- suppress the reverted changelog entries

- mark and render the corrected entries

- apply the Edits and Deletes corrections

- gate local links and dependency ranges from the scanner

- one location grammar for exec's subject, script source and folder

- load .env files

- write through a $ref in compute

- resolve $ref in config files

- trace what each script actually ran

- array of scripts, require release

- forward arguments after -- to run and exec scripts

- trace and debug logging for git, config and the plan

- add the autosubstitute command

- autowriter derives edits from the workspace

- change scope ignore rules

- root and space level flags

- space dependencies

- unsafeDisableLock config field

- lock the release command

- alias tags

- force tag writes and pushes

- consumer-keyed dependencies

- version component env vars

- if and exec shell helpers

- commit --tag-name

- allowBranch and behind-remote release guards

- self-update from the latest stable release

- autoreplace rewrites manifests across the selection

- select packages by versioning group

- release and status narrow to the package selection

- a package src path that narrows change detection

- per-command help, a platform in the version, and quiet parser diagnostics

- a github step command, and releases that skip themselves on a re-run

- prerelease opt-out for changelog and github records

- reconcile Docker image tags at the version stage

- fixedMajor and fixedMajorMinor versioning modes

- space packages entries and the space folder config layer

- space file model and .dispatignore over config names

- reconcile with replace rules

- select the autoversion strategy

- declare manifest names per package

- add the replacer command

- select packages with --package and --space

- expose the scanner and writer as commands

- resolve scripts per package across three levels

- changelog, commit and autoversion commands

- build release binaries in dispat flow

- run consumers

- package add

- per-package overrides, version groups, dispatignore

- preview all packages

- shared manifest module, package readmes

- compute auto version

- run since

- export

- preview, init, test, config


### Fixes

- exercise propagation out of the group driver

- exercise a group ride release

- exercise the release pipeline across every package

- the skip cascade reads the fresh changeset, not the train

- a graduation documents the train's provider movement

- config edits prepare every file before writing any

- self-update no longer claims an empty install path

- a record entry is never empty

- a catch-up on a train is still a catch-up

- status counts the fresh changeset, not the train

- a ride with train history still says no changes

- nested dependencies update again x3

- nested dependencies update againx2

- the reason names what forces the release

- a spent blast and a distant origin leave the records

- nested dependencies update again

- nested dependencies update

- dependencies update

- a wired record states the run's provider movements

- the module's go directive matches the workspace

- the installers walk the release listing past page one

- carry the license in every module

- the release commit names only what it records

- an explicit DISPAT_UPDATE_CHECK=1 waits for the answer

- warn when a commit.include path is missing

- refuse ambiguous initials under case-colliding names

- refuse an unselectable if --changed --consumers

- published log names the deferred tag

- pre-config errors respect the log flags

- scale the github upload timeout to the asset

- write changelogs through an atomic replace

- report a correction that reached nothing

- count references followed, not keys walked, when bounding an edit

- name the sparse member that decides a group's major

- run the login in the space folder

- keep alias tags out of the release commit subject

- sync manifests and changelogs for every updated provider

- log every W diagnostic at warn level

- 1.0.0 release blockers

- never abort a run after a release is out

- resolve the known groups once per invocation

- scope the group pin and channel conflicts to a moving group

- keep W222 for rules that actually reached a file

- step over an unreadable folder in a replace rule's walk

- run syncLock without a reconciling strategy

- load ancestry dag once, parallel tag reads

- scheduler fifo, script cancellation, launch determinism

- autoversion and compute correctness

- round 2 blockers

- ccme and models API freeze

- graceful shutdown, ancestry cache, scheduler guard

- 1.0.0 blockers

- badges

- git check


### Dependencies

- ccme: 1.0.0-rc.10 -> 1.0.0
- manifest: 1.0.0-rc.10 -> 1.0.0
- models: 1.0.0-rc.19 -> 1.0.0
- scanner: 1.0.0-rc.10 -> 1.0.0
- writer: 1.0.0-rc.10 -> 1.0.0

## services/dispat/v1.0.0-rc.19 (2026-08-16)

### Features

- the release engine is ready for the stable line


### Dependencies

- ccme: 1.0.0-rc.9 -> 1.0.0-rc.10
- manifest: 1.0.0-rc.9 -> 1.0.0-rc.10
- models: 1.0.0-rc.18 -> 1.0.0-rc.19
- scanner: 1.0.0-rc.9 -> 1.0.0-rc.10
- writer: 1.0.0-rc.9 -> 1.0.0-rc.10

## services/dispat/v1.0.0-rc.18 (2026-08-16)

### Dependencies

- models: 1.0.0-rc.17 -> 1.0.0-rc.18

## services/dispat/v1.0.0-rc.17 (2026-08-16)

### Fixes

- exercise propagation out of the group driver


### Dependencies

- models: 1.0.0-rc.16 -> 1.0.0-rc.17

## services/dispat/v1.0.0-rc.16 (2026-08-16)

### Dependencies

- writer: 1.0.0-rc.8 -> 1.0.0-rc.9
- manifest: 1.0.0-rc.8 -> 1.0.0-rc.9
- models: 1.0.0-rc.15 -> 1.0.0-rc.16
- scanner: 1.0.0-rc.8 -> 1.0.0-rc.9

## services/dispat/v1.0.0-rc.15 (2026-08-16)

### Dependencies

- ccme: 1.0.0-rc.8 -> 1.0.0-rc.9
- models: 1.0.0-rc.14 -> 1.0.0-rc.15

## services/dispat/v1.0.0-rc.14 (2026-08-16)

### Dependencies

- models: 1.0.0-rc.13 -> 1.0.0-rc.14

## services/dispat/v1.0.0-rc.13 (2026-08-16)

### Fixes

- exercise a group ride release


### Dependencies

- models: 1.0.0-rc.12 -> 1.0.0-rc.13

## services/dispat/v1.0.0-rc.12 (2026-08-16)

### Fixes

- exercise the release pipeline across every package


### Dependencies

- ccme: 1.0.0-rc.6 -> 1.0.0-rc.7
- manifest: 1.0.0-rc.6 -> 1.0.0-rc.7
- models: 1.0.0-rc.11 -> 1.0.0-rc.12
- scanner: 1.0.0-rc.6 -> 1.0.0-rc.7
- writer: 1.0.0-rc.6 -> 1.0.0-rc.7

## services/dispat/v1.0.0-rc.11 (2026-08-16)

### Features

- the installers explain PATH permanence and shadowing

- debug shows git mutations, trace shows starting scripts


### Fixes

- the skip cascade reads the fresh changeset, not the train

- a graduation documents the train's provider movement

- config edits prepare every file before writing any

- self-update no longer claims an empty install path

- a record entry is never empty

- a catch-up on a train is still a catch-up

- status counts the fresh changeset, not the train

- a ride with train history still says no changes


### Dependencies

- models: 1.0.0-rc.10 -> 1.0.0-rc.11

## services/dispat/v1.0.0-rc.10 (2026-08-16)

### Fixes

- nested dependencies update again x3


### Dependencies

- models: 1.0.0-rc.9 -> 1.0.0-rc.10

## services/dispat/v1.0.0-rc.9 (2026-08-16)

### Fixes

- nested dependencies update againx2


### Dependencies

- ccme: 1.0.0-rc.5 -> 1.0.0-rc.6
- manifest: 1.0.0-rc.5 -> 1.0.0-rc.6
- models: 1.0.0-rc.8 -> 1.0.0-rc.9
- scanner: 1.0.0-rc.5 -> 1.0.0-rc.6
- writer: 1.0.0-rc.5 -> 1.0.0-rc.6

## services/dispat/v1.0.0-rc.8 (2026-08-16)

### Fixes

- the reason names what forces the release

- a spent blast and a distant origin leave the records


### Dependencies

- writer: 1.0.0-rc.4 -> 1.0.0-rc.5
- manifest: 1.0.0-rc.4 -> 1.0.0-rc.5
- models: 1.0.0-rc.7 -> 1.0.0-rc.8
- scanner: 1.0.0-rc.4 -> 1.0.0-rc.5

## services/dispat/v1.0.0-rc.7 (2026-08-16)

### Dependencies

- ccme: 1.0.0-rc.4 -> 1.0.0-rc.5
- manifest: 1.0.0-rc.3 -> 1.0.0-rc.4
- models: 1.0.0-rc.6 -> 1.0.0-rc.7
- scanner: 1.0.0-rc.3 -> 1.0.0-rc.4
- writer: 1.0.0-rc.3 -> 1.0.0-rc.4

## services/dispat/v1.0.0-rc.6 (2026-08-16)

### Fixes

- nested dependencies update again

- nested dependencies update


### Dependencies

- ccme: 1.0.0-rc.3 -> 1.0.0-rc.4
- manifest: 1.0.0-rc.2 -> 1.0.0-rc.3
- models: 1.0.0-rc.5 -> 1.0.0-rc.6
- scanner: 1.0.0-rc.2 -> 1.0.0-rc.3
- writer: 1.0.0-rc.2 -> 1.0.0-rc.3

## services/dispat/v1.0.0-rc.5 (2026-08-16)

### Fixes

- dependencies update

- a wired record states the run's provider movements


### Dependencies

- ccme: 1.0.0-rc.2 -> 1.0.0-rc.3
- manifest: 1.0.0-rc.1 -> 1.0.0-rc.2
- models: 1.0.0-rc.4 -> 1.0.0-rc.5
- scanner: 1.0.0-rc.1 -> 1.0.0-rc.2
- writer: 1.0.0-rc.1 -> 1.0.0-rc.2

## services/dispat/v1.0.0-rc.4 (2026-08-16)

### Breaking Changes

- commit to the 1.0 interfaces


### Features

- the run start and the lock answer at info

- warn a github step running before the run's tag

- step commands align to the run's environment

- release polyglot monorepos from conventional commits


### Fixes

- the module's go directive matches the workspace

- the installers walk the release listing past page one

- carry the license in every module

- the release commit names only what it records

- an explicit DISPAT_UPDATE_CHECK=1 waits for the answer


### Dependencies

- ccme: 1.0.0-rc.1 -> 1.0.0-rc.2
- manifest: 1.0.0-rc.0 -> 1.0.0-rc.1
- models: 1.0.0-rc.1 -> 1.0.0-rc.4
- scanner: 1.0.0-rc.0 -> 1.0.0-rc.1
- writer: 1.0.0-rc.0 -> 1.0.0-rc.1

## services/dispat/v1.0.0-rc.3 (2026-08-09)

### Fixes

- load ancestry dag once, parallel tag reads


## services/dispat/v1.0.0-rc.2 (2026-08-09)

### Breaking Changes

- replace the channel sigil with percent


## services/dispat/v1.0.0-rc.1 (2026-08-09)

### Features

- changelog, commit and autoversion commands


## services/dispat/v1.0.0-rc.0 (2026-08-09)

### Breaking Changes

- initial release

