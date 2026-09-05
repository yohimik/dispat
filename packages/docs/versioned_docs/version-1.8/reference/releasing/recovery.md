# Recovering from a failed run

Run `dispat` again to recover from a failed release run. Tags written after successful publishes let the next plan
skip recorded packages and continue with their pending consumers.

There is one interval a tag cannot describe: a publish command may succeed and the process may stop before dispat
writes its tag. If a run is killed during that interval, inspect that package's registry or destination before you
retry. dispat does not claim exactly-once delivery across an arbitrary shell command.

In this example, `core` and its consumer `app` release together. The tests for `app` break its build after `core`
already published.

```console
$ git commit -m "feat(core)^: new API, reaching the app"
$ dispat
12:04:05 INF ● changed bump=minor channel=stable dueToProviders=[] ownCommits=1 package=core reason=direct space=libs version="0.0.0 -> 0.1.0"
12:04:05 INF ● changed bump=patch channel=stable dependsOn=["core"] dueToProviders=["core"] ownCommits=0 package=app reason="propagated from core" space=libs version="0.0.0 -> 0.0.1"
12:04:05 INF release plan ready held=0 packages=2 releasing=2
12:04:05 INF build started package=core stage=build version=0.1.0
12:04:05 INF build succeeded package=core stage=build version=0.1.0
12:04:05 INF publish started package=core stage=publish version=0.1.0
12:04:05 INF version succeeded package=app stage=version version=0.0.1
12:04:05 INF build started package=app stage=build version=0.0.1
12:04:05 INF tests failed package=app stage=build version=0.0.1
12:04:05 ERR build script failed error="exit status 1" package=app stage=build version=0.0.1
12:04:05 INF published package=core stage=publish tag=core@0.1.0 version=0.1.0
12:04:05 INF summary channel=stable package=core status=published tag=core@0.1.0 took=1.2s version="0.0.0 -> 0.1.0"
12:04:05 ERR summary error="build: exit status 1" channel=stable failedStage=build package=app status=failed took=1.2s version="0.0.0 -> 0.0.1"
12:04:05 INF done cancelled=0 failed=1 held=0 published=1 skipped=0 took=1.2s unchanged=0

$ git tag
core@0.1.0
```

The run exits with a non-zero status, leaving `core@0.1.0` tagged while `app` is not. You do not need to repair a state
file or reconcile versions by hand. Fix the tests and run the command again:

```console
$ dispat
12:04:05 WRN catch-up release at 0.0.1: discharging work already published by core@0.1.0 code=W193 package=app
12:04:05 INF plan diagnostics errors=0 warnings=1
12:04:05 INF unchanged channel=stable package=core space=libs version=0.1.0
12:04:05 INF ↻ catch-up bump=patch channel=stable dependsOn=["core"] dueToProviders=["core"] ownCommits=0 package=app reason="catch-up from core" space=libs version="0.0.0 -> 0.0.1"
12:04:05 INF version succeeded package=app stage=version version=0.0.1
12:04:05 INF build succeeded package=app stage=build version=0.0.1
12:04:05 INF published package=app stage=publish tag=app@0.0.1 version=0.0.1
12:04:05 INF done cancelled=0 failed=0 held=0 published=1 skipped=0 took=1.2s unchanged=1
```

The second run recomputes the plan from history and configuration, sees `core@0.1.0` is already recorded, and executes
only the missing half. The `W193` marker confirms `app` is releasing at the exact version it was owed from the earlier
run, while `core` is never re-released. If a *provider* fails instead, dispat skips its consumers and reports them with
`W194`, so you can catch them up with a re-run once you fix the provider.

An interrupted run follows the same rule. If you press Ctrl-C or a CI job dies, packages with a completed publish keep
their record. dispat reports everything else as `cancelled`, and your next run recomputes the remaining plan.

After an ordinary cancellation, dispat gives completed publishes up to five minutes to finish their commit, tags,
push, and GitHub release record. It skips further user hooks during this detached finalization. A timeout is reported
as a recording failure, with the local tags and commits left available for inspection.

If release commits are enabled, commit or stash pre-existing changes in selected package folders and `commit.include`
paths before retrying; dispat refuses them so its automatic commit cannot capture unrelated work. It likewise refuses
pre-existing changes only in selected package folders where `revertOnFail` could reset them. With both features off,
writer output left by the earlier run remains valid input to the retry.

Catch-up has a manifest half as well as a release half. A consumer that failed to release beside its provider keeps its
old range until its own next release, when the version stage reconciles the range to the provider's published version
(`W197`). This pickup is
[the auto-version reconciliation](../../configuration/autoversion.md#picking-up-providers-released-without-you), not a
release triggered by the provider.

## When somebody pushes while the release runs

The [behind-remote guard](./release-lock.md) closes before the plan is computed, so it cannot cover a commit pushed
after the run has started. Such a commit reaches the release at the very end, as a rejected push, by which point the
packages have already published: refusing there would leave a released package with no commit, no tag and nothing on
the remote.

dispat therefore recovers rather than refuses, and it does so by joining the two rather than choosing between them. It
pulls the branch and merges what landed with its release commit, leaving that commit exactly as it was, and pushes the
merge. The run exits `0` and says what it did:

```console
12:04:05 WRN pulled the branch during the release to sync changes that landed while it ran; the release tags point at the tree that was planned and the release commit was merged on top branch=main code=W242 remote=origin
```

Nothing dispat made is rewritten. The release commit keeps its identity and its tags keep naming it, so the tagged tree
is still the one the release recorded: the changelog entries it wrote and the version rewrites it made are inside it,
and anything resolving the tag gets what was published. Only the branch tip changes, into a merge whose first parent is
the release commit and whose second is what arrived. The merge itself is a `chore(release)` commit, and
[`nonPackageScopes`](../../configuration/parser.md#nonpackagescopes) exempts that scope, so it names no package.

The commit that arrived is outside the tag's ancestry, which is where it belongs. It was not in this run's plan, it is
not in this run's records, and the next run plans it and releases it on its own terms, with an entry of its own.

The window this recovers from is still open while it recovers, so another clone can land a commit between the pull and
the push that follows it. dispat answers that the same way, up to three rounds in all; the warning is reported once per
round. What never moves across the rounds is the tag: it names the commit the run planned, not the merge above it.

The warning is there because the branch the release went out on is not the branch the run was planned against. Read it
as a prompt to check that the merge is the history you wanted.

dispat stops before merging anything when the remote already carries a release tag this run is about to push:

```console
12:04:05 ERR push failed code=E224 error="commits landed on origin/main during the release, and origin already carries core@1.2.0: this checkout planned a version that is already published, so pushing again would move a released tag. Pull and run again" remote=origin
```

That is a checkout whose tags were stale enough for the plan to recompute a version somebody else has already released.
The recovery would push the tag again, and [`commit.force`](../../configuration/records.md#force) is on by default, so
it would move a published ref. Pull and run again instead: the plan then reads the tag that exists and releases the
version after it. Moving aliases are not part of the check, because moving them is what every release does.

### When what landed conflicts

Sometimes the commits that arrived changed the same content the release did. The release has already published by
then, so stopping would still leave it nowhere but the local clone. dispat completes it and hands you the conflict
instead:

```console
12:04:05 WRN commits landed on the branch during the release and changed the same content; this release's side was kept and theirs was pushed to a branch of its own to be reconciled branch=main code=W243 keptAt=release-conflicts/core-0.1.0-20260902-053012 paths=["packages/core/CHANGELOG.md"] remote=origin
```

Three things happen, and the run then exits `0`:

- **This release's side wins every conflicting file.** It is the tree that was planned, built, published and tagged,
  and the tag already names it, so taking the other side would publish content the release never saw. Everything the
  arriving commits changed that did *not* conflict is in the merge exactly as it is on the clean path.
- **The other side is kept.** dispat pushes the foreign tip to a branch of its own, named
  `release-conflicts/<package>-<version>...-<UTC timestamp>`: every package this leg released with its version, then
  when it happened, so the name says what it belongs to and cannot collide. The branch is never forced, and a name
  that somehow exists already stops the run rather than being overwritten.
- **Both records say so.** The changelog entry and the GitHub release body carry a note naming the conflicting files
  and that branch.

The changelog note lives in the merge commit rather than in the release commit, because the release commit is tagged
and a tag whose commit is amended names nothing. The tree the tag points at therefore carries the entry *without* the
note; the branch carries it with one. The GitHub release body has no such constraint, since it is written after the
push.

What is left is a job for a person. Two versions of the same content exist, one on the branch and one on the
quarantine branch, and no rule dispat could follow decides which should survive. Note also that the arriving commits
sit outside the tag's ancestry, so the next run releases them as it would any other commit: it will release the parts
of their work that did merge, while the conflicting hunks are still only on the quarantine branch. Reconcile the two
sides before or after that release, whichever suits, but do not leave the branch unread.

dispat stops with `E224` only when the recovery machinery itself fails: the quarantine branch cannot be pushed, the
settled merge cannot be committed, or the merge stopped for a reason that is not conflicting content. The run then
exits non-zero, no tag reaches the remote, the working tree is left out of the merge dispat started, and the
[release lock](./release-lock.md) is given back as it is on every other way out.

Read [Concepts](../../concepts.md#catch-up-failed-consumers-are-never-lost) to understand the properties that make this
safe and why dispat needs no state file. You can look up each diagnostic code in [Diagnostic codes](../plan-errors.md).
