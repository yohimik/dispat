# Run-level hooks

Seven hooks observe the run as a whole rather than any one package. They live in the **top-level `run` object** and
execute in the **monorepo root**. Each is a no-op when unconfigured:

```yaml
run:
  beforeAll: check-preconditions
  postAll: notify
```

| Hook               | Runs                                                                              |
|--------------------|-----------------------------------------------------------------------------------|
| `run.beforeAll`    | Once, after planning and verification, before the task graph starts.              |
| `run.postAll`      | Once, after the whole task graph finishes, even when nothing released.            |
| `run.beforeCommit` | Before the release commit. `commit` mode only, and only when something published. |
| `run.afterCommit`  | After the release commit succeeded.                                               |
| `run.postCommit`   | After the release commit **and** the tags.                                        |
| `run.beforePush`   | Before the push. `commit.push` mode only.                                         |
| `run.afterPush`    | After the push succeeded.                                                         |

`run.beforeAll` is the one **gating** run hook. It fires before any release work begins. A failure aborts the run with
exit `1` before dispat builds, publishes, or tags anything. Every other run hook only **warns** on failure. These run
after the work they observe, so dispat executes every command in the sequence even if an earlier one fails. The "after"
hooks only run when their bracketed operation succeeds.

All seven hooks receive `DISPAT_STAGE` naming the hook and the
[workspace listing](../reference/environment.md#workspace-data). `run.postAll` and later hooks also receive the
[run outcome listing](../reference/environment.md#run-outcome-data). This data reports which packages published,
failed, were skipped, or were never planned to release. `run.beforeAll` fires before any outcome exists.

## They belong to `dispat release`

These seven fire for `dispat release` and nothing else. The [step commands](../reference/releasing/steps.md) do not
fire them. For example, `dispat commit --tag --push` makes the release commit, writes tags, and pushes without ever
triggering `run.beforeCommit` or `run.afterPush`.

This design is deliberate. A run hook gives you a seam into a moment **dispat** chooses. Inside a release, dispat
decides when the commit happens, so it offers `beforeCommit`. A script that calls `dispat commit` already owns that
moment. You bracket the commit by writing the line before and the line after:

```yaml
scripts:
  notify-before: ./notify.sh starting
  commit: dispat commit --tag --push
  notify-after: ./notify.sh done
spaces:
  libs:
    flow:
      beforePublish: [notify-before, commit, notify-after]
```

The `run` object also means *once per run*. The `flow.beforePublish` hook executes **per package**, and nesting step
commands there is the [recommended pattern](../reference/releasing/steps.md). A release of ten packages would fire each
commit hook ten times from inside itself and once more from its own finalize. Nothing in the environment would
distinguish one firing from another.

## The branch guard

Configure `run.allowBranch` to guard your releases. A release run refuses to start unless your checked-out branch
matches one of your listed patterns:

```yaml
run:
  allowBranch: [main, release/*]
```

A run from `main` proceeds normally. A run from `feature/tryout` stops with exit `1` and prints a message naming both
the branch and the patterns. This happens before any verification, hooks, or release work, leaving the repository
exactly as dispat found it.

A `*` matches any run of characters, including separators. This means `release/*` matches `release/v2/hotfix`. A
detached HEAD has no branch name, so it matches nothing, even a pattern as broad as `*`.

The guard restricts releasing, not looking. Read-only commands like `status`, `preview`, and `compute` work on any
branch. The [step commands](../reference/releasing/steps.md) are also unguarded. They are built to run inside a release
stage that this guard has already cleared.

Leave the list unset to release from any branch. This is the default behavior. It suits single-branch repositories and
disposable clones.

### Releasing from a stale checkout

A second guard needs no configuration. When you set the finalize phase to push ([`commit.push`](./records.md#commit)),
dispat checks your checkout against the remote branch. It refuses to run if your local branch is behind:

```console
$ dispat
ERR refusing to release  error="the checkout is behind origin/main; pull before releasing"
```

dispat computes the plan from the tags your local clone can see. If someone else released in the meantime, your
checkout plans versions that may already exist. The push at the end would fail after everything was built, published,
and tagged. Failing at the start costs nothing. Failing at the end costs a release.

Run `git pull` and try again. dispat ignores this check for a detached HEAD or a new branch the remote does not have
yet. The check uses `ls-remote`. Set `commit.verify: false` to turn off this check if your remote rejects `ls-remote`
but accepts pushes.
