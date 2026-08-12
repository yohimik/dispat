# Run-level hooks

Seven hooks observe the run as a whole rather than any one package. They live in the **top-level `run` object**, run in
the **monorepo root**, and each is a no-op when not configured:

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

`run.beforeAll` is the one **gating** run hook: it fires before any release work, when failing it can still stop
everything, so it does, fail-fast, aborting the run with exit `1` before anything is built, published or tagged. Every
other run hook only **warns** on failure (a warn-only sequence: every command runs even when an earlier one failed),
because it runs after the work it observes. The "after" hooks additionally only run when the operation they bracket
succeeded: a hook observing a commit or push that never happened would be reporting a lie.

All seven receive `DISPAT_STAGE` naming the hook and the [workspace listing](../reference/environment.md#workspace-data);
`run.postAll` and everything after it additionally receive the
[run outcome listing](../reference/environment.md#run-outcome-data) reporting which packages published, failed, were skipped or
were never planned to release (`run.beforeAll` fires before any outcome exists).

## The branch guard

`run.allowBranch` is not a hook but a guard. When you set it, a release run refuses to start unless the branch you have
checked out matches one of the patterns you listed:

```yaml
run:
  allowBranch: [main, release/*]
```

That is the whole feature. A run from `main` proceeds; a run from `feature/tryout` stops with exit `1` and a message
naming both the branch and the patterns, before any verification, any hook, and any release work. Nothing is built,
nothing is tagged, and the repository is left exactly as it was found.

A `*` matches any run of characters, separators included, so `release/*` reaches `release/v2/hotfix`. A detached HEAD
has no branch name, so it matches nothing, including a pattern as broad as `*`.

The guard is about releasing, not about looking. Read-only commands (`status`, `preview`, `compute`) work on any branch,
which is what you want on a pull request. The [step commands](../releasing/steps.md) are not guarded either: they are built to run
inside a release stage that this guard has already cleared.

Leave the list unset to release from any branch. That suits single-branch repositories and disposable clones, and it is
the default.

### Releasing from a stale checkout

There is a second guard, and it needs no configuration. When the finalize phase is set to push
([`commit.push`](./records.md#commit)), dispat checks that your checkout is not behind the branch it would push to, and
refuses if it is:

```console
$ dispat
ERR refusing to release  error="the checkout is behind origin/main; pull before releasing"
```

The reason is that the plan is computed from the tags this clone can see. If someone else has released in the meantime,
your checkout is planning versions that may already exist, and the push at the end would be rejected anyway, after
everything was built, published and tagged. Failing at the start costs nothing; failing at the end costs a release.

`git pull` and run again. Two cases are deliberately not treated as behind: a branch the remote does not have yet,
because the first push is what creates it, and a detached HEAD, where there is no branch to compare. The check is an
`ls-remote`, so `commit.verify: false`, the escape hatch for remotes that reject `ls-remote` but accept pushes, turns
it off along with the reachability check.
