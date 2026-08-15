# Recovering from a failed run

What to do when a release run fails partway: nothing. The scenario every release tool has to answer for is a run of
several packages failing in the middle, and dispat's answer is that the same command, run again, finishes exactly what
the first run still owed.

The scenario every release tool has to answer for: a run of several packages fails in the middle. Here
`core` and its consumer `app` release together; `app`'s tests break its build after `core` already published.

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

The run exits non-zero, `core@0.1.0` is out and tagged, `app` is not. There is no state file to repair and no version to
reconcile by hand. Fix the tests and run the same command again:

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

The second run recomputed the same plan from history and configuration, saw `core@0.1.0` already recorded, and executed
only the missing half. `W193` is the marker to look for: it says `app` is releasing at exactly the version it was owed
from the earlier run, not because of any new commit. `core` is not re-released, however many times you run this. If
instead a *provider* fails, its consumers are skipped and reported with
`W194`, and the same re-run catches them up once the provider is fixed.

An interrupted run (Ctrl-C, a killed CI job) follows the same rule: packages whose publish completed keep their record,
everything else is reported as `cancelled`, and the next run picks up exactly the remainder.

The properties that make this safe, and why no state file is involved, are in
[Concepts](../../concepts.md#catch-up-failed-consumers-are-never-lost). What each diagnostic code means is in
[Diagnostic codes](../plan-errors.md).
