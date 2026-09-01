# Recovering from a failed run

Run `dispat` again to recover from a failed release run. dispat finishes exactly what the first run still owed.

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
their record. dispat reports everything else as `cancelled`, and your next run picks up exactly the remainder.

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

dispat therefore recovers rather than refuses. It pulls the branch, replays its release commit on top of what landed,
moves the release tags onto the replayed commit, and pushes again. The run exits `0` and says what it did:

```console
12:04:05 WRN pulled the branch during the release to sync changes that landed while it ran; the release commit was replayed on top of them branch=main code=W242 remote=origin
```

The warning is there because the release went out on a tree that is not the one the run was planned against. What was
planned is still what was released: the commit that arrived was not in the plan, and it is not in the changelog entries
or the version numbers this run wrote. It sits underneath the release commit, and therefore inside the window the
release just closed, so it carries no entry of its own and will not release anything on the next run. Read the
`W242` warning as a prompt to check whether that commit needed a release, and cut one for it if it did.

Tags are only ever pushed on the commit that was pushed. If the replay conflicts with what landed, dispat has nothing
to decide on its own and stops:

```console
12:04:05 ERR push failed code=E224 error="commits landed on origin/main during the release and could not be merged with it: ..." remote=origin
```

The run exits non-zero, no tag reaches the remote, the working tree is left out of the rebase dispat started, and the
[release lock](./release-lock.md) is given back as it is on every other way out. Merge the two sides yourself, then
push the release commit and its tags.

A release whose tag is pinned to a commit its own scripts exported is never replayed, because replaying rewrites that
commit too. dispat reports the same failure and leaves the merge to you.

Read [Concepts](../../concepts.md#catch-up-failed-consumers-are-never-lost) to understand the properties that make this
safe and why dispat needs no state file. You can look up each diagnostic code in [Diagnostic codes](../plan-errors.md).
