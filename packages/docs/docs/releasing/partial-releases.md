# Releasing part of the monorepo

A normal `dispat` release takes every package that changed and releases all of them. That is usually what you want.
Sometimes it is not. You may want to ship a hotfix in one library today and leave the rest of the queue for the
weekly release. You may have one package whose publish target is down. You may simply want to try the pipeline on a
single package first.

For those cases, `dispat release` and `dispat status` take the same three selection flags every other dispat command
takes:

```sh
dispat release --package core        # release core and nothing else
dispat release -p core,web           # release both
dispat release --space libs          # release every package of the libs space
dispat release --group platform      # release every package of the platform version group
dispat status -p core                # see what that release would do, without doing it
```

`--package` (short `-p`) names packages, `--space` (short `-s`) names spaces, and `--group` (short `-g`) names
[version groups](./versioning.md). All three are repeatable, all three accept comma separated lists, all three match
without caring about upper or lower case, and all three accept `*` as a wildcard (`-p '@acme/*'`). Quote the wildcard,
or your shell will try to expand it first. The full rules are in
[Choosing the packages](../cli/run.md#choosing-the-packages), and they are the same rules `dispat run` uses.

If you pass no flags at all, the folder you are standing in is the selection. Run `dispat release` inside
`packages/core` and you release core. Run it at the top of the repository and you release everything, exactly as
before.

A term that matches no package is an error. Releasing nothing because of a typo is the one outcome nobody wants.

## The versions do not change

This is the part worth trusting. dispat always computes the plan for the whole repository first, and narrows it
afterwards. Your selection decides *what gets released*, never *what version it gets*.

So `dispat release -p core` releases core at exactly the version a full release would have given it. Nothing about a
package's version depends on which other packages happened to go out with it.

## Providers go first, so some packages have to wait

Packages are released in dependency order for a reason. If `web` depends on `core` and both have changes waiting,
then releasing `web` first would publish a version of `web` whose release notes credit a `core` update that does not
exist yet. Anyone installing `web` would get the old `core`.

dispat will not do that. If you select a package whose provider is also releasing but was left out of the selection,
the package is held back:

```console
$ dispat release -p web
WRN selected package cannot be released before its providers, leaving it for the next run code=W230 package=web waitingFor=["core"]
INF ⊘ withheld until its providers release package=web
INF release plan ready packages=2 releasing=0 deselected=2
```

Nothing was released, and the message says why and what to do about it. You have two ways forward, and both are
fine:

- add the provider to the selection, `dispat release -p core,web`, and both go out in order;
- release `core` on its own now and let `web` go out on a later run. `web` keeps its pending changes, picks up the
  new `core` when it does release, and nothing is lost.

The rule follows the whole chain. If `app` depends on `web` and `web` depends on `core`, then selecting `app` and
`web` without `core` holds both of them back, because `web` cannot go either.

A provider only holds a package back when that provider is really releasing in this run. A provider with nothing
pending, or one held by a `Release-As: none` directive, is nothing to wait for, so selecting the consumer alone works
normally.

## Shared version groups get a warning instead

Packages in a [shared version group](./versioning.md) (`fixed` and its relatives) all move to one version together.
If your selection takes only part of a group, dispat releases the members you asked for and warns you:

```console
$ dispat release -p one
WRN selection releases part of a versioning group; the rest catches up on the next run code=W231 group=libs releasing=["one"] leftBehind=["two"]
```

Nothing here is published out of order, so this is a warning rather than a refusal. The group's shared version is
briefly untrue: `one` is at 0.2.0 and `two` is still at 0.1.0. The next full release puts that right on its own,
because a member that has fallen behind its group is released to catch up (that is the `W210` ride you may already
have seen). You do not have to do anything.

If your repository would rather never be in that state, you have two options. Name the group instead of its members,
`dispat release -g libs`, which releases every member at once and so cannot split anything. Or use `--strict`, which
refuses the split outright.

## `--strict`: all or nothing

Add `--strict` and dispat refuses the whole run if the selection cannot be released cleanly. A package the order
holds back, or a version group split in two, becomes an error instead of a warning, and the refusal happens before
anything at all is built, published or tagged:

```console
$ dispat release -p web,site --strict
WRN selected package cannot be released before its providers, leaving it for the next run code=W230 package=web waitingFor=["core"]
ERR refusing to release error="the selection cannot be released as it stands and --strict is set"
$ echo $?
1
```

Note what did *not* happen: `site` was perfectly releasable, and it was not released either. That is the point of
`--strict`. Either the selection goes out exactly as written, or nothing does, which is what makes a narrowed
release safe to run unattended in CI.

The plan is still printed before the refusal, so you can see what was refused and why.

## Checking first with `status`

`dispat status` takes the same flags and narrows the same plan, so it is the release seen in advance:

```sh
dispat status -p core            # what "dispat release -p core" would do
dispat status -p web --strict    # exit 1 if that selection could not be released cleanly
```

It still prints every package in the graph, because showing you the whole picture is its job. Each line tells you
where that package stands:

| Line                                  | Meaning                                                        |
|---------------------------------------|-----------------------------------------------------------------|
| `● changed`                           | releasing, with the new version on the same line                |
| `⊝ not selected`                      | it would have released, and your selection left it out          |
| `⊘ withheld until its providers release` | you asked for it, and the release order cannot reach it yet  |
| `‖ held (Release-As: none)`           | a commit directive is holding it back, nothing to do with you   |
| `unchanged`                           | nothing pending for it at all                                   |

Without `--strict`, `status` keeps its usual promise of exiting 0 even when it has something to warn about. With
`--strict`, a selection that could not be released cleanly exits 1, which makes it a useful gate to put in front of
a release job.

## What a narrowed release writes

Everything a release records follows the packages that actually went out, and nothing else:

- tags are created only for released packages;
- changelog entries are written only for released packages;
- GitHub releases are created only for released packages;
- the release commit, if you use one, stages only the released packages' folders and names only their tags in its
  message.

A package that was left out is untouched. Its pending changes stay pending, and the next run releases it at the
version it was always owed.

## A worked example

Three packages: `core`, `web` (which depends on `core`) and `site`. All three have changes waiting.

```console
$ dispat release -p core
INF release narrowed to the selection selection=core releasing=["core"]
INF ● changed package=core version="1.2.0 -> 1.3.0"
INF ⊝ not selected package=web
INF ⊝ not selected package=site
INF done published=1 deselected=2 unchanged=0
```

`core@1.3.0` is out. `web` and `site` are exactly where they were. Later, with nothing new committed:

```console
$ dispat release
INF ● changed package=web version="0.4.0 -> 0.5.0" reason="direct"
INF ● changed package=site version="2.0.0 -> 2.1.0" reason="direct"
INF done published=2 unchanged=1
```

`web` picks up the `core` release as part of its own, `site` goes out, and `core` is quiet because it has nothing
left to release. The repository ends up in the same place a single full release would have put it, just in two
steps.

## Quick reference

| You want                                     | Command                                  |
|----------------------------------------------|-------------------------------------------|
| Release one package                          | `dispat release -p core`                  |
| Release a package and its provider           | `dispat release -p core,web`              |
| Release one space                            | `dispat release -s libs`                  |
| Release a whole version group                | `dispat release -g platform`              |
| Release the package folder you are in        | `dispat release` (from inside the folder) |
| See it first                                 | `dispat status -p core`                   |
| Fail instead of releasing part of it         | add `--strict`                            |

Related reading: [CLI reference](../cli/README.md), [Release steps](./steps.md) for running one stage of a release by hand,
and [Shared versions](./versioning.md) for what a version group promises.
