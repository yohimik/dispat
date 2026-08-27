# Releasing part of the monorepo

A normal `dispat` release takes every package that changed and releases all of them. You usually want this. Sometimes
you need to ship a hotfix in one library today and leave the rest for the weekly release. A publish target might be
down, or you might want to try the pipeline on a single package first.

Pass one of three selection flags to `dispat release` or `dispat status` to narrow the run. Every other dispat command
takes these same flags:

```sh
dispat release --package core        # release core and nothing else
dispat release -p core,web           # release both
dispat release --space libs          # release every package of the libs space
dispat release --group platform      # release every package of the platform version group
dispat status -p core                # see what that release would do, without doing it
```

The `--package` (short `-p`) flag names packages, `--space` (short `-s`) names spaces, and `--group` (short `-g`) names
[version groups](./versioning.md). These flags are repeatable and accept comma separated lists. They ignore case and
accept `*` as a wildcard (`-p '@acme/*'`). Quote the wildcard so your shell does not expand it first. The full rules
are in [Choosing the packages](../../cli/run.md#choosing-the-packages), and they match what `dispat run` uses.

Pass no flags to select the folder you are standing in. Run `dispat release` inside `packages/core` to release core
alone. Run it at the top of the repository to release everything.

A term that matches no package is an error. dispat stops before releasing nothing because of a typo.

## The versions do not change

dispat always computes the plan for the whole repository first and narrows it afterwards. Your selection decides *what
gets released*, never *what version it gets*.

Run `dispat release -p core` to release core at exactly the version a full release would have given it. A package's
version never depends on which other packages go out with it.

## Providers go first, so some packages have to wait

Packages are released in dependency order so consumers get the right updates. If `web` depends on `core` and both have
changes waiting, releasing `web` first publishes a version of `web` that credits a missing `core` update. Anyone
installing `web` gets the old `core`.

dispat prevents this. Select a package whose provider is also releasing but was left out of the selection, and the
package is held back:

```console
$ dispat release -p web
WRN selected package cannot be released before its providers, leaving it for the next run code=W230 package=web waitingFor=["core"]
INF ⊘ withheld until its providers release package=web
INF release plan ready packages=2 releasing=0 deselected=2
```

dispat releases nothing. The message says why and what to do about it. You have two ways forward:

- add the provider to the selection with `dispat release -p core,web` so both go out in order;
- release `core` on its own now and let `web` go out on a later run. The `web` package keeps its pending changes and
  picks up the new `core` when it releases.

The rule follows the whole dependency chain. If `app` depends on `web` and `web` depends on `core`, selecting `app` and
`web` without `core` holds both of them back. The `web` package cannot go either.

A provider only holds a package back when that provider is releasing in this run. A provider with nothing pending is
nothing to wait for. A provider held by a `Release-As: none` directive also lets you select the consumer alone.

## Shared version groups get a warning instead

Packages in a [shared version group](./versioning.md) (`fixed` and its relatives) all move to one version together.
Select only part of a group, and dispat releases the members you asked for and warns you:

```console
$ dispat release -p one
WRN selection releases part of a versioning group; the rest catches up on the next run code=W231 group=libs releasing=["one"] leftBehind=["two"]
```

Nothing publishes out of order, so this is a warning rather than a refusal. The group's shared version is briefly
untrue with `one` at 0.2.0 and `two` still at 0.1.0. The next full release fixes this automatically. A member that
falls behind its group releases to catch up. This triggers the `W234` warning you might have seen.

You have two options to avoid this state entirely. Name the group instead of its members with `dispat release -g libs`
to release every member at once. Or pass `--strict` to refuse the split outright.

## `--strict`: all or nothing

Add `--strict` to make dispat refuse the whole run if the selection cannot release cleanly. A package held back by
dependency order becomes an error instead of a warning. A version group split in two also becomes an error.

This refusal happens before anything builds, publishes, or tags:

```console
$ dispat release -p web,site --strict
WRN selected package cannot be released before its providers, leaving it for the next run code=W230 package=web waitingFor=["core"]
ERR refusing to release error="the selection cannot be released as it stands and --strict is set"
$ echo $?
1
```

Notice that `site` was perfectly releasable, but it did *not* release either. Either the selection goes out exactly *as
written*, or nothing does. This guarantee makes a narrowed release safe to run unattended in CI.

dispat still prints the plan before the refusal so you can see what failed and why.

## Checking first with `status`

Run `dispat status` with the same flags to narrow the same plan. This shows you the release in advance:

```sh
dispat status -p core            # what "dispat release -p core" would do
dispat status -p web --strict    # exit 1 if that selection could not be released cleanly
```

The command still prints every package in the graph to show you the whole picture. Each line tells you where that
package stands:

| Line                                  | Meaning                                                        |
|---------------------------------------|-----------------------------------------------------------------|
| `● changed`                           | releases with the new version printed on the same line          |
| `⊝ not selected`                      | it would release, but your selection left it out                |
| `⊘ withheld until its providers release` | you asked for it, but the release order cannot reach it yet  |
| `‖ held (Release-As: none)`           | a commit directive holds it back                                |
| `∅ script-only (versioning: none)`    | its space [never releases](./versioning.md#packages-that-never-release-none) and runs scripts instead |
| `unchanged`                           | nothing pending for it                                          |

Without `--strict`, `status` exits 0 even when it prints a warning. Pass `--strict` to make a blocked selection exit 1.
This makes the command a useful gate to put in front of a release job.

Pass `--require-release` to gate the pipeline on a different question. The `--strict` flag asks whether the selection
can go out *as written*. The `--require-release` flag asks whether it goes out *at all*, and exits 3 when the narrowed
plan releases nothing.

Only `● changed` counts as releasing. The run will not publish `⊝ not selected`, `⊘ withheld`, `‖ held`,
`∅ script-only`, or `unchanged` packages. Both flags work on `release` and `status`, and both refuse before anything
builds. Check [Gating a pipeline on the plan](../ci.md#gating-a-pipeline-on-the-plan) for details.

## What a narrowed release writes

A release records only the packages that actually went out:

- dispat creates tags only for released packages.
- dispat writes changelog entries only for released packages.
- dispat creates GitHub releases only for released packages.
- the release commit stages only the released packages' folders and names only their tags in its message.

A package left out of the selection remains untouched. Its pending changes stay pending. The next run releases it at
the version it was always owed.

## A worked example

Imagine three packages named `core`, `web`, and `site`. The `web` package depends on `core`, and all three have changes
waiting.

```console
$ dispat release -p core
INF release narrowed to the selection selection=core releasing=["core"]
INF ● changed package=core version="1.2.0 -> 1.3.0"
INF ⊝ not selected package=web
INF ⊝ not selected package=site
INF done published=1 deselected=2 unchanged=0
```

The `core@1.3.0` release is out. The `web` and `site` packages sit exactly where they were. Run a full release later
with nothing new committed:

```console
$ dispat release
INF ● changed package=web version="0.4.0 -> 0.5.0" reason="direct"
INF ● changed package=site version="2.0.0 -> 2.1.0" reason="direct"
INF done published=2 unchanged=1
```

The `web` package picks up the `core` release as part of its own. The `site` package goes out, and `core` stays quiet
because it has nothing left to release. The repository ends up in the exact place a single full release would put it,
just in two steps.

## Quick reference

| You want                                     | Command                                   |
|----------------------------------------------|-------------------------------------------|
| Release one package                          | `dispat release -p core`                  |
| Release a package and its provider           | `dispat release -p core,web`              |
| Release one space                            | `dispat release -s libs`                  |
| Release a whole version group                | `dispat release -g platform`              |
| Release the package folder you are in        | `dispat release` (from inside the folder) |
| See the plan first                           | `dispat status -p core`                   |
| Fail instead of releasing a partial group    | add `--strict`                            |
| Fail when the run releases nothing           | add `--require-release`                   |

Read the [CLI reference](../../cli/README.md) for all flags. Check [Release steps](./steps.md) to run one stage of a
release by hand. Read [Shared versions](./versioning.md) to see what a version group promises.
