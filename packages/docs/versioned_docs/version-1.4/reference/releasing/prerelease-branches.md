# Prerelease branches

You can run a branching model for prereleases where `main` releases the stable line and a beta branch releases a beta
train. The train later graduates through rc into a stable release. dispat has no branch configuration because channels
live in commit messages and tags, making a branch strategy a way of organizing those commits.

## Where the channel actually lives

Three facts decide everything below.

dispat reads a package's channel from its newest release tag, so `core@1.5.0-beta.2` is on `beta` and `core@1.4.2` is
on `stable` with no side file. Write a directive in a commit to move a package between channels. Use `%beta` to enter
the beta line and a transition such as `%beta>stable` to graduate it.

dispat reads tags from the commits reachable from `HEAD`. A tag on an unmerged branch is invisible to other branches,
which makes per-branch release lines work. A beta tag created on the beta branch does not move `main`'s view of the
package until you merge the branch.

The pending window is measured from the last **stable** tag. This means the plan on any branch always sees the whole
train since the last stable release.

Read the full channel rules in [Commit messages](../commits.md#channels-and-prereleases) and
[Concepts](../../concepts.md#prereleases-and-channels). The [`tagFormat`](../../configuration/versions.md#tagformat)
setting controls how a channel is spelled inside a tag.

## The setup

Set `main` as the stable line. Do your prerelease work on a branch, called `beta` here, and run releases from whichever
branch the work is on. Configure the [branch guard](../../configuration/run-hooks.md#the-branch-guard) to keep releases
off every other branch:

```json
{
  "run": { "allowBranch": ["main", "beta"] }
}
```

The guard applies to `dispat release` alone, and only when the run reaches the point of releasing. Commands like
`status` and `preview` work from any branch, so you can still inspect the plan on a feature branch. Check out a branch
in CI to release because a detached checkout matches no pattern, though patterns like `release/*` work and `*` crosses
`/`.

## The train on the branch

Put a risky rewrite out on a prerelease channel first. Declare the channel in the commit. The publish script sees it as
`$DISPAT_CHANNEL`, which you can use as the npm dist-tag so beta users opt in and `latest` stays untouched.

```console
$ git commit -m "feat(core)%beta: risky rewrite, try it on beta first"
$ dispat
12:04:05 INF ● changed bump=minor channel="stable -> beta" dueToProviders=[] ownCommits=1 package=core reason=direct space=libs version="0.1.0 -> 0.2.0-beta.0"
12:04:05 INF npm publish --tag beta package=core stage=publish version=0.2.0-beta.0
12:04:05 INF published package=core stage=publish tag=core@0.2.0-beta.0 version=0.2.0-beta.0
```

Ordinary commits keep the train rolling when feedback arrives:

```console
$ git commit -m "fix(core): beta feedback"
$ dispat
12:04:05 INF published package=core stage=publish tag=core@0.2.0-beta.1 version=0.2.0-beta.1
```

dispat recomputes the target version from the whole train on every run, so a breaking change arriving mid-train
correctly moves the target to `1.0.0-beta.N`. Graduate a finished train straight to stable with
`release(core)%beta>stable: graduate`, or take the longer road through rc described below. Whichever road it takes, the
graduation releases the accumulated work minus the prerelease marker, and nothing on the train is counted twice.

## Promoting beta to rc

Write a transition directive to move the train between prerelease channels the same way you graduate one:

```console
$ git commit --allow-empty -m "release(core)%beta>rc: promote to release candidate"
$ dispat
12:04:05 INF ● changed bump=minor channel="beta -> rc" dueToProviders=[] ownCommits=3 package=core reason=direct space=libs version="0.2.0-beta.1 -> 0.2.0-rc.0"
```

A transition matches the package's **baseline** channel, which makes it idempotent. Once the baseline says `rc`, a
`%beta>rc` directive proposes nothing, so re-running converges instead of minting `rc.1`. Use `%*>rc` to match any
prerelease, but the wildcard is a source only because there is no transition *to* a random prerelease.

You can run the promotion on the beta branch or on a dedicated `rc` branch. The tags travel with the commits. Merge
`beta` into an `rc` branch and run the transition there to get the exact same behavior as running it in place.

## Merging to main: the consequences

Merging the branch usually breaks a release tool. Here is exactly what dispat sees after you run `git merge beta` on
`main`.

**Every branch commit enters main's window, with its own message parsed.** The window includes every commit reachable
from `HEAD` since the last stable tag, instead of doing a first-parent walk. The `feat(core)%beta:` commits written on
the branch remain the exact same units they always were.

**Already-released prerelease work is discharged, not repeated.** A commit inside an existing prerelease tag still
counts toward the next version's bump, but it does not re-release the train. Nothing is published twice, a `Release-As`
is no longer in force, and a `cancel` can no longer discard it.

**The merge commit's own message is parsed too.** dispat has no special case for merge commits, so the parser ignores a
default `Merge branch 'beta'` subject for planning but honors a merge commit that carries scopes or directives. Write
your merge messages deliberately, because a scopeless conventional subject addresses the packages whose files the merge
brought in.

**Graduation is a directive, wherever you run it.** After the merge, `main`'s baseline for the package is still the
newest reachable tag, so the train is still on `rc` and a stable release does not happen by itself. Graduate it the
same way as any transition, and take the whole train's consumers along when there is one:

```console
$ git commit --allow-empty -m "release(core)%rc>stable: ship it"
$ dispat
12:04:05 INF ● changed bump=minor channel="rc -> stable" dueToProviders=[] ownCommits=4 package=core reason=direct space=libs version="0.2.0-rc.0 -> 0.2.0"
```

Run the canonical whole-train form `release(core)%rc>stable%%rc>stable++*` to graduate every consumer still on the
train. Read the transition semantics in [Commit messages](../commits.md#channels-and-prereleases).

## Guarding the pattern in CI

Use `run.allowBranch` as the release-side guard. On the CI side, run [`dispat if`](../../cli/if.md) to branch the
pipeline on the same fact without depending on the runner's shell. The condition reads the environment the command is
given, so export the branch name under whichever variable your provider supplies:

```sh
BRANCH=$GITHUB_REF_NAME dispat if 'BRANCH~release/*' --then 'dispat release' --else 'dispat status'
```

Read the [pipeline patterns](../pipelines.md) page for the full CI shapes this slots into. That page includes the plan
job that decides whether a release is worth starting at all.
