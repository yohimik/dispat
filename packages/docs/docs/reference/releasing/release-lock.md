# The release lock

Running two releases of the same repository at the same time causes problems. Both runs read the same tags and decide
on the same next versions. Both build and publish, but only one writes the result. The other run publishes packages
that nothing records.

This happens easily. A merge to the main branch triggers a release, and another merge a minute later starts a second CI
job. You might re-run a stuck job while the first attempt is still going, or release from a laptop while a pipeline
runs.

`dispat release` avoids this race entirely. It claims the repository before planning anything. If dispat cannot claim
the repository, it stops.

## How the claim works

The claim is a git tag called `dispat-release-lock` that dispat pushes to your remote. Each attempt first creates a
unique annotated-tag object, remembers its object ID, and offers that immutable object under the shared remote name.
The push is not forced. If the name is taken, git rejects the push and that rejection acts as the lock. Using the
object ID matters when two processes share one checkout: neither process can retarget the object the other is pushing.

A release happens in five steps:

1. Create the `dispat-release-lock` tag and push it. Stop and exit `1` with `E336` if the push is rejected for any
   reason. A push that reports a failure is read back from the remote before it is believed, as
   [a push whose answer was lost](#a-push-or-a-delete-whose-answer-was-lost) describes.
2. Check that this checkout is not behind the remote, when `commit.push` and `commit.verify` are both on: the branch it
   has checked out is compared with the remote's tip of that branch. In a fleet every participating repository with
   both settings on is checked the same way. A plan built from a stale checkout recomputes versions somebody else has
   already published, so the check runs before the plan exists and under the lock that keeps its answer from going
   stale. A detached fleet source has no branch to compare; the branch a release commit is pushed to, when
   `commit.branch` names another one, is compared once the plan says which repositories record. A single repository
   that pushes refuses a detached HEAD with `E337` before it takes the lock at all.
3. Read the remote's release tags once, under the same conditions, and compare them with this checkout's. The lock
   decides who releases; it does not decide what the releasing run knows, and a checkout that is level with the branch
   can still be missing every record another run wrote. A tag the remote holds on a commit this run's head reaches and
   this checkout does not have is `E196`; the same version named at two commits is `E191`. Both stop the run before
   anything is planned, and the remedy is `git fetch --tags`, because a run that refreshed its records after reading
   them would be planning from something nothing checked.
4. Do everything else: plan, build, publish, record, tag, push. Before each publish command, read the lock back from
   the remote. A remote that carries another object under the name, or none, belongs to another run now: the
   publication is refused with `E336` before its command starts, and no later publication of the run starts either.
   A read that fails is tried again, three reads in all, each bounded, and a remote that answers none of them is
   refused the same way, because the run cannot show that it still owns the repository. `commit.verify: false`
   skips this read with a warning, for a remote that rejects `ls-remote` and accepts pushes. A refusal for a lock
   that is another run's now carries the opposite remedy to a refusal at step 1: that lock is not this run's to
   delete, so let its run finish, check what this run published, and run again.
5. Delete the remote tag only if it still points to this run's object, then remove the local attempt tag.

The last step happens no matter what the ones before it did. A failed package, a guard refusing the run, or an empty
plan all trigger cleanup. Cleanup is detached from a cancelled release context and bounded to 30 seconds. If another owner has
replaced the remote ref, the expected-object lease rejects the delete and preserves that owner's lock. The rejected
delete is read back once. No lock on the remote means the delete landed and only its answer was lost, which is a lock
given back. The other owner's object on the remote means this run cannot show it held the exclusion until it ended, so
the run fails with `E336` and a remedy that says not to delete that lock: let its run finish, check what this run
published, and run again.

If any repository's lock cannot be returned, dispat reports `E336` and exits nonzero. A publication that already
succeeded remains published and is still counted that way in the summary; the closing `release.finished` webhook says
`failed`, or `interrupted` if the run was cancelled. A refusal before execution emits no completion webhook. In a fleet, cleanup continues in reverse order so one damaged repository does not strand the locks of
the remaining repositories too.

The claim is unconditional. No flag moves the plan ahead of it, because whether there is work to do is not known
until after planning, and planning is the thing the lock exists to serialise. A run that turns out to have nothing to
publish therefore takes the lock, gives it straight back, and exits as it otherwise would. To ask whether a release
would publish anything without touching the lock, run `dispat status --require-release`, which plans without ever
writing to the remote.

## What a blocked run looks like

```console
$ dispat release
ERR unable to create the release lock tag code=E336 error="pushing the release lock tag to origin: ... ! [rejected] dispat-release-lock -> dispat-release-lock (already exists)" remedy="another release may hold it; if you are sure nothing else is releasing, delete the tag on the remote (git push <remote> --delete dispat-release-lock) and run again" remote=origin tag=dispat-release-lock
```

Nothing was planned, built, published, or tagged. Wait for the other run to finish and run your command again. The
second run gets the repository, sees the versions the first run released, and picks up from there.

## A push or a delete whose answer was lost

A push can land on the remote and still report a failure, because the connection broke before the answer came back.
A refusal and a lost answer look the same from here, so dispat reads the lock back from the remote before it decides,
on a bound of its own that an interrupt does not cut short:

| The remote carries | Outcome |
| --- | --- |
| This attempt's own object | The push landed. The run owns the lock, says so with a warning, and proceeds. |
| Another object | Another run holds the lock. The run is refused and names that run when the tag says who it is. |
| No lock | The push did not land. The run is refused. |
| Nothing, because every read failed | The push may have landed. dispat deletes it under a lease on this attempt's object, which can only remove this attempt's own lock, and refuses the run with `E336`. The refusal names the attempt and the object, so a lock the delete could not reach is recognisable: its message carries that `attempt` line and nobody holds it. |

The same holds for the delete that gives the lock back. A delete that reports a failure is read back once. No lock on
the remote means it was given back, which is a debug line and a successful cleanup. Another object means another run
holds the lock now: this run's lock is already gone, and the other one is left alone. Only this run's own object, or
a read that failed, is a lock left behind: `E336`, a non-zero exit, and the remedy for clearing it.

## Clearing a lock that was left behind

A run killed outright cannot clean up after itself. The tag stays on the remote if a CI runner is reclaimed mid-job, a
laptop loses power, or you use `kill -9`. Every later release then refuses to run.

Check the remote first. The tag looks exactly the same whether it is abandoned or in use right now:

```sh
git ls-remote --tags origin dispat-release-lock   # is it there?
git fetch origin tag dispat-release-lock          # bring it here
git show dispat-release-lock                      # who wrote it, and when
```

The tag message names the host, the process, the moment the lock was taken and the attempt that took it:

```
dispat release lock

host ci-runner-7
pid 3412
at 2026-08-12T05:41:09.882374Z
attempt dispat-release-lock-attempt-9d2c4e7a1b3f5d6e8a0c2b4d6f8e1a3c
run 6f1a9f0d2b90c8f96f1a9f0d2b90c8f9
```

The `run` line is present only when the release delegates work to [worker nodes](../../distributed-execution.md). It
is the run id every coordination branch of that run carries, and it is the place dispat records it so that a run
which ended holding its lock can be found from the lock. A later run refused by this lock quotes the same facts,
including the run.

Confirm that run is genuinely gone. A lock that names a run is cleared in the order
[a lock a distributed run retained](#a-lock-a-distributed-run-retained) gives, because a worker of that run may hold
a publication it was authorized to start. Any other lock is cleared by deleting the tag and releasing again:

```sh
git push origin --delete dispat-release-lock
```

Deleting the lock is a decision you make. dispat does not time out and delete the lock for you. It cannot tell a dead
run from a slow one, so an expiring lock would let a second release run over the first.

## Things worth knowing

**The lock needs a remote you can write to.** A repository with no remote configured cannot take a lock, and neither
can a CI job with a read-only token. You need `contents: write` on the release job in GitHub Actions. This holds even
if you never push anything else, so check [dispat in CI](../ci.md).

**It has nothing to do with the release push.** The `commit.push` setting decides whether the release commit and its
tags go to the remote. dispat takes the lock whether that setting is on or off. Two runs computing the same versions
collide whether or not either of them pushes.

**`commit.force` does not reach it.** Forcing applies to a run's own release tags, which are its records to rewrite.
The lock is a name other runs may hold, so dispat never forces the lock push. A repository that forces everything still
stops at a lock somebody else holds.

**It covers the whole repository, not one package.** Running `dispat release -p core` and `dispat release -p web` at
the same time serialises the runs. The second waits for the first. They would otherwise write two release commits over
each other.

**It is not a release tag.** The `dispat-release-lock` tag carries no version and dispat never reads it back as one. It
stays out of every package's history even under a tag format broad enough to match the name.

**A fleet is locked repository by repository.** A release run from a
[control repository](../../control-repository.md#keeping-the-plan-fixed-during-release) takes the lock in every
participating repository, in repository-name order, and gives them back in reverse. The lock covers the whole combined
workspace, so independent per-repository locks do not make two fleet runs safe.

## Turning it off

Turn the lock off in the config file for a repository that is always in this situation:

```yaml
unsafeDisableLock: true
```

Or turn it off in the environment for one invocation:

```sh
DISPAT_UNSAFE_DISABLE_LOCK=true dispat release
```

Either setting is enough, and neither overrides the other. The lock is on only while both stay quiet. That is the
default, so a config file that never mentions the key gets the lock.

These settings exist for repositories that have no remote to coordinate through at all. This includes a scratch clone,
a fixture, or a local experiment where the alternative is no release. They are spelled unsafe because they are. With
the lock off, nothing stops a second release starting beside the first.

Only a value that plainly reads as true switches the variable on (`true`, `TRUE`, `1`). Anything else leaves the lock
in place, including a typo, an empty value, or an unset variable. Releasing unguarded is not a state you want to end up
in by accident.

A run with the lock off says so. One `W331` warning names every repository releasing without a lock and which setting
asked for it, so a fleet that meant to bypass one repository can see that it bypassed all of them. See
[diagnostic codes](../plan-errors.md#polyrepository-snapshot-and-recording-diagnostics).

**[Distributed execution](../../distributed-execution.md) refuses both switches.** A run that configures
`execution.workers` and would release without the remote lock stops with `E225`, naming the repositories and the
setting that asked for it. The bypass exists for a repository with no remote to coordinate through; a run that
dispatches work to other machines is the opposite situation, because every node it reaches writes through a remote
and the lock is the only thing that stops a second run authorizing the same publication from somewhere else.

## A lock a distributed run retained

A distributed run leaves one thing behind on purpose. When it authorized a publication on a worker node and the node
never reported back, the outcome of that publication cannot be established from here: the registry may hold the
version or it may not, and the publisher may still be running. The run reports `E228`, fails that package, blocks its
dependents and exits non-zero. If the node never acknowledged the withdrawal either, the release lock of the
repository it was publishing into is **retained** rather than given back, because handing that repository to the next
run would be handing over an exclusion that does not exclude.

The log line says so, names the run and points here:

```
ERR release lock retained code=E228 category=publication-unknown run=6f1a9f0d2b90c8f96f1a9f0d2b90c8f9 tag=dispat-release-lock
```

The uncertain publication's authorization ref is retained even when its node acknowledged after starting publish.
Other coordination refs may already have been cleaned up. The evidence is in the remaining run refs, which live on the
repository's own remote unless a worker link named another endpoint, and the order of the steps matters:

```sh
git fetch origin tag dispat-release-lock && git show dispat-release-lock   # the `run` line names the run
git ls-remote --heads origin 'dispat-worker-*'                             # the coordination refs
```

1. Find the branches of the run: the messages on each one (`dispat/assignment.json` and the ones after it) carry the
   run id in their `run` field. Among them, find the branch of the publication: it carries an authorization (`go`)
   with no result beside it.
2. Confirm on that node that the publisher has stopped. A machine that is gone is confirmation; a machine still
   running the publish command is not.
3. Check the registry for the version the package was publishing. That, and not the tags, is what says whether the
   publication happened.
4. Delete the run's coordination refs, after verifying that their current tips still belong to that run's
   authenticated chain. Investigate a changed or unauthenticated tip instead of deleting it as residue.
5. Only then delete the lock tag, exactly as for an abandoned lock:

```sh
git push origin --delete dispat-release-lock
```

Then run the release again. A run may end with a lock retained and no release record at all, so the next run plans
whatever is still owed and publishes it, which is the supported recovery. dispat never clears this lock for you and
never retries the publication inside the run that lost it.
