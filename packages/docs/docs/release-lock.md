# The release lock

Two releases of the same repository at the same time is a problem no amount of care inside one run can solve. Both runs
read the same tags, both decide on the same next versions, both build and publish, and then both try to write the
result. One of them wins. The other has already published packages that nothing records.

It is easier to run into than it sounds. A merge to the main branch triggers a release, someone merges again a minute
later, and now two CI jobs are releasing at once. Somebody re-runs a job that looked stuck while the first attempt is
still going. Somebody releases from a laptop while a pipeline is doing the same thing.

So `dispat release` does not enter the race. Before it plans anything, it claims the repository, and if it cannot, it
stops.

## How the claim works

The claim is a git tag called `dispat-release-lock`, pushed to your remote. A tag push that is not forced is the one
operation git offers whose outcome depends on what another machine already did: if the name is taken, the push is
rejected. That rejection is the whole mechanism.

A release therefore goes like this:

1. Create the `dispat-release-lock` tag and push it. If the push is rejected for any reason, stop and exit `1`.
2. Do everything else: plan, build, publish, record, tag, push.
3. Delete the tag from the remote and from your clone.

Step 3 happens whatever step 2 did. A failed package, a guard refusing the run, a plan with nothing in it, a Ctrl-C
halfway through a build: the lock is given back on the way out either way.

## What a blocked run looks like

```console
$ dispat release
ERR unable to create the release lock tag error="pushing the release lock tag to origin: ... ! [rejected] dispat-release-lock -> dispat-release-lock (already exists)" remedy="another release may hold it; if you are sure nothing else is releasing, delete the tag on the remote (git push <remote> --delete dispat-release-lock) and run again" remote=origin tag=dispat-release-lock
```

Nothing was planned, nothing was built, nothing was published, and nothing was tagged. Waiting for the other run to
finish and running again is the whole recovery. The second run gets the repository, sees the versions the first one
released, and picks up from there.

## Clearing a lock that was left behind

A run that is killed outright cannot clean up after itself. `kill -9`, a CI runner being reclaimed mid-job, a laptop
losing power: the tag stays on the remote and every later release refuses.

Check first, because the tag looks exactly the same whether it was abandoned or is in use right now:

```sh
git ls-remote --tags origin dispat-release-lock   # is it there?
git fetch origin tag dispat-release-lock          # bring it here
git show dispat-release-lock                      # who wrote it, and when
```

The tag message names the host, the process and the moment the lock was taken:

```
dispat release lock

host ci-runner-7
pid 3412
at 2026-08-12T05:41:09.882374Z
```

If that run is genuinely gone, delete the tag and release again:

```sh
git push origin --delete dispat-release-lock
```

Deleting the lock is deliberately a decision you make rather than something dispat does for you after a timeout. dispat
cannot tell a dead run from a slow one, and a lock that expires on its own would let exactly the second release through
that the first is still in the middle of.

## Things worth knowing

**The lock needs a remote you can write to.** It is a push, so a repository with no remote configured cannot take one,
and neither can a CI job with a read-only token. In GitHub Actions that means `contents: write` on the release job. This
holds even if you never push anything else. See [dispat in CI](./ci.md).

**It has nothing to do with the release push.** `commit.push` decides whether the release commit and its tags go to the
remote. The lock is taken whether that is on or off, because two runs computing the same versions collide whether or not
either of them pushes.

**`commit.force` does not reach it.** Forcing applies to a run's own release tags, which are its records to rewrite. The
lock is a name other runs may hold, so its push is never forced. A repository that forces everything still stops at a
lock somebody else has.

**It covers the whole repository, not one package.** `dispat release -p core` and `dispat release -p web` started at the
same time are now serialised: the second waits for the first. They would otherwise write two release commits over each
other anyway.

**It is not a release tag.** `dispat-release-lock` carries no version, is never read back as one, and stays out of every
package's history even under a tag format broad enough to match the name.

## Turning it off

Either in the config file, for a repository that is always in this situation:

```yaml
unsafeDisableLock: true
```

or in the environment, for one invocation:

```sh
DISPAT_UNSAFE_DISABLE_LOCK=true dispat release
```

Either one is enough, and neither overrides the other: the lock is on only while both stay quiet. That is the default,
so a config file that never mentions the key gets the lock.

Both exist for repositories that have no remote to coordinate through at all: a scratch clone, a fixture, a local
experiment where the alternative is not an unguarded release but no release. They are spelled unsafe because they are.
With the lock off, nothing stops a second release starting beside the first.

Only a value that plainly reads as true switches the variable on (`true`, `TRUE`, `1`). Anything else, including a typo,
an empty value and an unset variable, leaves the lock in place. Releasing unguarded is not a state to end up in by
accident.
