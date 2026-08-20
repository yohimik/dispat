# Prerelease branches

How to run a branching model for prereleases, where `main` releases the stable line and a beta branch releases a beta
train that later graduates through rc into a stable release. dispat has no branch configuration: channels live in
commit messages and tags, and a branch strategy is a way of organizing those commits. This page maps the common model
onto that rule.

## Where the channel actually lives

Three facts decide everything below.

A package's channel is read from its newest release tag, so `core@1.5.0-beta.2` is on `beta` and `core@1.4.2` on
`stable`, with no side file and no configuration. A commit moves a package between channels with a directive: `%beta`
enters the beta line, and a transition such as `%beta>stable` graduates it.

Tags are read from the commits reachable from `HEAD`. A tag on an unmerged branch is invisible here, which is what
makes per-branch release lines work: a beta tag created on the beta branch does not move `main`'s view of the package
until the branch is merged.

And the pending window is measured from the last **stable** tag, so on any branch the plan always sees the whole train
since the last stable release.

The full channel rules live in [Commit messages](../commits.md#channels-and-prereleases) and
[Concepts](../../concepts.md#prereleases-and-channels); how a channel is spelled inside a tag is
[`tagFormat`](../../configuration/versions.md#tagformat)'s business.

## The setup

`main` is the stable line. Prerelease work happens on a branch, `beta` here, and releases run from whichever branch the
work is on. The [branch guard](../../configuration/run-hooks.md#the-branch-guard) keeps releases off every other
branch:

```json
{
  "run": { "allowBranch": ["main", "beta"] }
}
```

The guard applies to `dispat release` alone, and only when the run reaches the point of releasing: `status`, `preview`
and the step commands work from any branch, so a feature branch can still inspect its plan. A detached checkout
matches no pattern, `*` included, so CI that checks out a bare commit has to check out a branch to release. Patterns
like `release/*` work too, and `*` crosses `/`.

## The train on the branch

A risky rewrite goes out on a prerelease channel first. The channel is declared in the commit; the publish script sees
it as `$DISPAT_CHANNEL` (here used as the npm dist-tag, so beta users opt in and `latest` stays untouched).

```console
$ git commit -m "feat(core)%beta: risky rewrite, try it on beta first"
$ dispat
12:04:05 INF ● changed bump=minor channel="stable -> beta" dueToProviders=[] ownCommits=1 package=core reason=direct space=libs version="0.1.0 -> 0.2.0-beta.0"
12:04:05 INF npm publish --tag beta package=core stage=publish version=0.2.0-beta.0
12:04:05 INF published package=core stage=publish tag=core@0.2.0-beta.0 version=0.2.0-beta.0
```

Feedback arrives; ordinary commits keep the train rolling:

```console
$ git commit -m "fix(core): beta feedback"
$ dispat
12:04:05 INF published package=core stage=publish tag=core@0.2.0-beta.1 version=0.2.0-beta.1
```

The target version is recomputed from the whole train on every run, so a breaking change arriving mid-train moves the
target (to `1.0.0-beta.N`), which is exactly what it should do. A train that is done can graduate straight to stable
with `release(core)%beta>stable: graduate`; the sections below take the longer road through rc, which is where a
branch model earns its keep. Whichever road it takes, the graduation releases the betas' accumulated work minus the
prerelease marker, and nothing on the train is counted twice.

## Promoting beta to rc

A transition directive moves the train between prerelease channels the same way it graduates one:

```console
$ git commit --allow-empty -m "release(core)%beta>rc: promote to release candidate"
$ dispat
12:04:05 INF ● changed bump=minor channel="beta -> rc" dueToProviders=[] ownCommits=3 package=core reason=direct space=libs version="0.2.0-beta.1 -> 0.2.0-rc.0"
```

A transition matches the package's **baseline** channel, which is what makes it idempotent: once the baseline says
`rc`, a `%beta>rc` directive proposes nothing, so re-running converges instead of minting `rc.1`. `%*>rc` matches any
prerelease, and the wildcard is a source only: there is no transition *to* "some prerelease or other".

Whether the promotion happens on the beta branch or on a dedicated `rc` branch is a repository choice, not dispat's.
The tags travel with the commits, so merging `beta` into an `rc` branch and running the transition there behaves the
same as running it in place.

## Merging to main: the consequences

Merging the branch is the step where a branching model usually breaks a release tool. Here is exactly what dispat sees
after `git merge beta` on `main`.

**Every branch commit enters main's window, with its own message parsed.** The window is every commit reachable from
`HEAD` since the last stable tag, not a first-parent walk. The `feat(core)%beta:` commits written on the branch are
the same units they always were.

**Already-released prerelease work is discharged, not repeated.** A commit that an existing prerelease tag already
contains still counts toward the next version's bump, but it does not re-release the train: nothing is published
twice, a `Release-As` it carried is no longer in force, and a `cancel` can no longer discard it. The merged history
reads as one story whose shipped chapters stay shipped.

**The merge commit's own message is parsed too.** There is no special case for merge commits, so a default
`Merge branch 'beta'` subject is simply a message the parser diagnoses and ignores for planning, while a merge commit
that carries scopes or directives is honoured like any other. If you write something there, write it deliberately; a
scopeless conventional subject addresses the packages whose files the merge brought in.

**Graduation is a directive, wherever you run it.** After the merge, `main`'s baseline for the package is still the
newest tag, now reachable, so the train is still on `rc` and a stable release does not happen by itself. Graduate it
the same way as any transition, and take the whole train's consumers along when there is one:

```console
$ git commit --allow-empty -m "release(core)%rc>stable: ship it"
$ dispat
12:04:05 INF ● changed bump=minor channel="rc -> stable" dueToProviders=[] ownCommits=4 package=core reason=direct space=libs version="0.2.0-rc.0 -> 0.2.0"
```

The canonical whole-train form is `release(core)%rc>stable%%rc>stable++*`, which also graduates every consumer still
on the train. The transition semantics are in [Commit messages](../commits.md#channels-and-prereleases).

## Guarding the pattern in CI

`run.allowBranch` is the release-side guard. On the CI side, [`dispat if`](../../cli/if.md) branches the pipeline on
the same fact without depending on the runner's shell:

```sh
dispat if 'BRANCH~release/*' --then 'dispat release' --else 'dispat status'
```

The [pipeline patterns](../pipelines.md) page carries the full CI shapes this slots into, including the plan job that
decides whether a release is worth starting at all.
