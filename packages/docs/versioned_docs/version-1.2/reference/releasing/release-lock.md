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

The claim is a git tag called `dispat-release-lock` that dispat pushes to your remote. An unforced tag push is the only
git operation that depends on what another machine already did. If the name is taken, git rejects the push. That
rejection acts as the lock.

A release happens in three steps:

1. Create the `dispat-release-lock` tag and push it. Stop and exit `1` if the push is rejected for any reason.
2. Do everything else: plan, build, publish, record, tag, push.
3. Delete the tag from the remote and from your clone.

Step 3 happens no matter what step 2 did. A failed package, a guard refusing the run, or an empty plan all trigger
cleanup. dispat gives the lock back on the way out even if you press Ctrl-C halfway through a build.

## What a blocked run looks like

```console
$ dispat release
ERR unable to create the release lock tag error="pushing the release lock tag to origin: ... ! [rejected] dispat-release-lock -> dispat-release-lock (already exists)" remedy="another release may hold it; if you are sure nothing else is releasing, delete the tag on the remote (git push <remote> --delete dispat-release-lock) and run again" remote=origin tag=dispat-release-lock
```

Nothing was planned, built, published, or tagged. Wait for the other run to finish and run your command again. The
second run gets the repository, sees the versions the first run released, and picks up from there.

## Clearing a lock that was left behind

A run killed outright cannot clean up after itself. The tag stays on the remote if a CI runner is reclaimed mid-job, a
laptop loses power, or you use `kill -9`. Every later release then refuses to run.

Check the remote first. The tag looks exactly the same whether it is abandoned or in use right now:

```sh
git ls-remote --tags origin dispat-release-lock   # is it there?
git fetch origin tag dispat-release-lock          # bring it here
git show dispat-release-lock                      # who wrote it, and when
```

The tag message names the host, the process, and the moment the lock was taken:

```
dispat release lock

host ci-runner-7
pid 3412
at 2026-08-12T05:41:09.882374Z
```

Confirm that run is genuinely gone. Then delete the tag and release again:

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
