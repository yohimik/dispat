---
description: Recover a stopped release safely by using recorded tags and checking any publish whose outcome is ambiguous.
---

# Recovering from a failed run

Before retrying, check which part of the release failed. A new `dispat` run reads the durable tags, skips recorded
packages, and plans the remaining work. A package may already have a tag while its GitHub release is still missing,
so those two failures need different repairs.

With `commit.push` on, dispat attempts the remote tag or receipt before it runs optional recorders or lets a dependent
publish. A stopped CI job can still leave a publication without a record if it stops during the publish command or
before that remote write completes. A failed remote record write leaves the package published, blocks dependent
publication, and makes the run exit nonzero.

There is one interval no record can describe: a publish command may succeed and the process may stop before dispat
hears of it. The next run meets that version at its destination, and adopts it when its identity matches what the run
built (`W196`) or refuses it when it does not (`E198`). dispat does not claim exactly-once delivery across an arbitrary
shell command, and that check is what stands in for it.

## A run stopped between a publication and its record

What the remote holds after a stop depends on the commit policy.

| Policy | What reaches the remote after each publish | What a stopped run leaves |
|--------|---------------------------------------------|---------------------------|
| `commit.enabled` off, `commit.push` on | A receipt first, then the release tag and its aliases; a successful tag push consumes the receipt | Tags for packages whose record push completed; a rejected tag push retains its receipt for a retry |
| `commit.enabled` on, `commit.push` on | A publication receipt, `refs/dispat/receipts/<tag>`, after a successful receipt push | Receipts for packages whose record push completed; a stop before it can leave a published package unrecorded |
| `commit.push` off | Nothing; CI pushes | Tags in the runner's checkout only |

A receipt names the planned head, any exported tag target, the package, the version and the `INTEGRITY` the package
exported. It is not a release record, and no tag listing reads it. The next release reads the receipts under its lock,
before anything runs, and settles each one:

- **On the same commit, at the same version.** The run publishes nothing for it. If the new build exports the same
  `INTEGRITY` as the receipt, it warns `W246` and finishes the release commit and tag. If that identity differs or is
  absent, it refuses the record with `E198` and retains the receipt. If the receipt carried no identity, dispat tags
  the receipt's original commit and replans, as with an earlier commit.
- **On an earlier planned head.** Commits landed after the stopped run planned. The run writes the tag at the
  receipt's tag target, pushes it, warns `W246`, and replans, so new commits can release as the next version. Without
  an exported target, the tag names the planned head rather than a later release commit. A changelog entry the stopped
  run never pushed may be missing.
- **Its tag already exists.** In tag-only mode, local and remote peeled targets must match the receipt's tag target;
  a matching local-only tag is pushed first. A conflicting tag retains the receipt. In release-commit mode, the final
  branch, any new tags and receipt deletion are one atomic remote update. A same-checkout retry uses a local marker
  written before the tag to prove its exact receipt and target; a local tag without that marker fails closed.
- **On a commit the checkout's head does not reach.** It cannot affect this plan and is left alone, with a warning.

In release-commit mode, finalize atomically pushes the branch and tags while deleting their receipts. An unsupported
atomic push leaves the receipts and records untouched. A lost response is checked against the remote branch, tags and
receipts. A prepared same-checkout retry can resume the exact atomic push, including a clean merge of remote work that
landed meanwhile. Before any marker or tag exists, retry records the receipt's original target and replans. A fresh
checkout also works: it rebuilds on the receipt's planned head or records the original target after the head moved.
The stopped run's changelog and manifest edits may remain in its working tree; inspect them before unrelated edits.

This retry rule assumes authoritative release tags are not rewritten. A verified matching remote tag is a stable
precondition, so an existing tag need not be written again when the branch and exact receipt deletion are updated
atomically. Git can omit an unchanged tag from a push; that omission does not compare or fence the tag against a
concurrent forced rewrite. A tag observed at another target is refused, and recovery does not claim to handle a
rewrite between verification and finalization.

With `commit.push` off, the remote learns of a release only when CI pushes, and a run stopped before that push leaves
its tags in the runner's checkout. The next run meets those publications at their destinations instead.

### When the destination already holds the version

A publication that no record describes, because the run stopped inside the publish command or the record was lost
anyway, is planned again at the same version. The destination refuses the upload. dispat then asks whether what it
holds is the artefact this run built, through the space's
[`publish.adoptPublished`](../../configuration/spaces.md#publish) verify script, or for npm through the registry's
`dist.integrity` when the build exported `INTEGRITY`:

```console
12:04:05 WRN the destination already held this version as this run's artefact; adopted it as published code=W196 package=ui tag=ui@0.1.0
```

The package is recorded as published and nothing is uploaded again. When the destination holds a different artefact,
or its identity cannot be established, the package fails instead:

```console
12:04:05 ERR publish refused: the destination holds this version as another artefact code=E198 package=core
```

The run starts no further publication after `E198`, and packages waiting to publish are reported as cancelled. A
mismatch means something other than this repository's release published that version: a hand publish, another branch,
or a credential somebody else holds. Find out which before anything else. Do not tag the version by hand. Recover
forward with a new version through the reviewed workflow, as the npm section below describes.

A destination that does not hold the version, or that could not be asked, leaves the publish failure as it was. With
`adoptPublished: false`, every refused upload is an ordinary publish failure.

Removing a release page or Git tag does not recall artifacts already downloaded or cached by package proxies.
After an authorized withdrawal, verify each publication destination separately and account for consumers that may
still have the withdrawn bytes. A removed remote reference alone is not evidence that a publication was undone.

Choose the section that matches the last completed operation:

| What happened | Start here |
| --- | --- |
| A package failed while its provider published | [Dependent build failure](#continue-after-a-dependent-build-fails) |
| A provider failed | [Provider failure](#continue-after-a-provider-fails) |
| The run was interrupted | [Interruption and recording](#recover-an-interrupted-run) |
| The retry finds changed files | [Working-tree cleanup](#what-a-failed-run-leaves-in-the-working-tree) |
| The package and tag exist, but the GitHub release does not | [Repair GitHub metadata](#repair-github-metadata-after-the-package-was-recorded) |
| npm accepted the package, but there is no release tag | [npm publication recovery](#recover-an-npm-publication-without-a-release-tag) |
| A push was rejected after publication | [Concurrent pushes](#when-somebody-pushes-while-the-release-runs) |

Treat npm's version and dispat's release tag as separate records. If npm accepts a tarball and the run stops before
the Git tag reaches the remote, do not create the missing Git tag or npm dist-tag by hand. First inspect the exact npm
version, the workflow revision, and the tarball integrity outside the publish script.

If a workflow is interrupted after npm reports success and before Git records are written, and the build exported the
tarball's `INTEGRITY`, the next run adopts the version when npm's `dist.integrity` matches the tarball it built
(`W196`), and refuses it with `E198` when it does not. A build is reproducible only when the tarball's bytes are, so a
rebuilt tarball whose integrity differs is refused. A project whose publisher runs only `npm publish` and exports no
integrity leaves the decision to the workflow operator; rerunning the same publish command is unsafe. npm does not permit a name and version combination to be reused after
publication, including after an unpublish. Recover forward with a new patch version through the reviewed workflow, while keeping any independent
binary or provider pin unchanged when its bytes did not change. For example, reserve `1.10.0` and release `1.10.1`
rather than trying to recreate `1.10.0`.

Keep artifact validation before publication: build and inspect one tarball, verify its package name, version, and digest,
exercise that exact file, then give the same path to npm. The publish stage can then remain one upload operation. If the
entire package and all its versions are unpublished, npm also requires waiting 24 hours before publishing another
version of that package. Use npm's authenticated procedure and follow its
[unpublish policy](https://docs.npmjs.com/policies/unpublish/) as a separate administrative action; removal does not
make its version reusable.

The 24-hour restriction applies to every new version under the removed package name. Advancing the patch version
does not bypass it. A registry E409 indicating that the package is still being processed can occur during this
interval; inspect the package's `time.unpublished.time` and wait until the restriction has expired before retrying CI.

After a successful publication, independent installation checks may need to wait for registry metadata to become
available. Poll for the exact version with a bounded deadline before installing it. Keep that readiness check in a
post-release job, after release records, so a delayed registry response cannot turn an accepted upload into a failed
publish stage. This short readiness wait does not replace the 24-hour restriction after complete removal.

### Publish the next patch before removing the previous version

If the package is still published, an alternative is to keep its name and publish the next patch through the reviewed
workflow first. For example, publish `1.10.1`, verify its release records and installation, then unpublish only
`1.10.0` using `npm unpublish <package-name>@1.10.0`, if npm's
[unpublish policy](https://docs.npmjs.com/policies/unpublish/) permits it. Keep `1.10.1` published: removing only the
previous version avoids complete package removal and its 24-hour restriction, without renaming the package.

Exact dependency pins and lockfiles that still require `1.10.0` may fail to install after removal; update those
consumers to the replacement patch. If removal is not permitted or would disrupt consumers, deprecate only the old
version with a message directing users to the patch. The removed version remains unusable for future publications.
If all versions were already unpublished, wait out the restriction described above before publishing the patch.

## Continue after a dependent build fails

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

The run exits nonzero. `core@0.1.0` is tagged, while `app` still has work to release. Fix the tests and rerun dispat:

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
run, while `core` is never re-released.

## Continue after a provider fails

If the *provider* fails instead, a consumer whose only reason to release was that provider is skipped and reported with
`W194`, so you can catch it up with a re-run once you fix the provider.

By default, a consumer with changes of its own can still release. Its manifests must refer to the provider's previous,
published version. Before publication, dispat restores those dependency ranges if the version stage had already
written the provider's planned version.

The provider's propagated change is still pending for that consumer. When the provider finally publishes, the consumer
releases again to include it:

```console
$ dispat
12:04:05 INF ● changed bump=minor dueToProviders=[] ownCommits=1 package=core reason=direct version="0.1.0 -> 0.2.0"
12:04:05 INF ● changed bump=patch dependsOn=["core"] dueToProviders=["core"] ownCommits=0 package=app reason="propagated from core" version="0.2.0 -> 0.2.1"
12:04:05 INF done cancelled=0 failed=0 held=0 published=2 skipped=0 unchanged=0
```

The `app` package has no commits of its own this time. It is in the plan because the contribution `core` propagated to
it has still not been delivered by any release of `core`, and its `0.2.1` release is what delivers it: the range in its
manifest moves to `^0.2.0` and the two packages are in step again. If `core` fails a second time, `app` is skipped with
`W194` instead, because by then the failure is the only reason it is in the plan at all.

The provider's successful run need not include the consumer, but it must not release the provider alone on the
consumer's own release commit. If `app` published its own fix at the commit where `core` failed, and you now run
`dispat release --package core` on that same commit, dispat refuses before anything runs
([`E201`](../plan-errors.md#after-the-plan-before-any-releasing)): both tags would sit on one commit, and nothing could
later tell that `app` came first. Release both in one run with `dispat release --package core,app`, or commit first and
then release `core` alone. The provider's tag then lands past `app`'s release, and the next full run's `dispat status`
shows `app` as a `W193` catch-up from the newly published `core`, even though `app`'s earlier tag already contains the
original propagation commit. Run the full release through CI to deliver that catch-up; no new release-intent commit is
needed.

The same state can arise without a selection: the run releases `core` and then `app` on `app`'s own release commit,
and `app` fails after `core` published. dispat reports that as `E201` as well, because no later plan can find the
debt, and names the remedy: an empty commit `release(app)` with the footer `Release-As: <version>`, the version the
failed run planned for `app`. The next release ships `app` at that version and picks `core` up.

If the consumer already built against the provider's planned version, changing its manifest is not enough: the built
artifact may contain that version too. dispat blocks the consumer with `W194`. The next run rebuilds it against the
provider's published version.

## Recover an interrupted run

An interrupted run follows the same rule. If you press Ctrl-C or a CI job dies, packages with a completed publish keep
their record. dispat reports everything else as `cancelled`, and your next run recomputes the remaining plan.

After an ordinary cancellation, dispat gives completed publishes up to five minutes, counted from the interrupt, to
finish their commit, tags, push, and GitHub release record. The same holds whenever the interrupt arrives: during the
build of another package, during `postAll`, during a commit or push hook, or while the release push is on the wire.
A user hook that is running when the interrupt arrives is stopped, and no later hook starts, while the commit, the
tags and the push it brackets go on. A run that is never interrupted has no such limit. A timeout is reported as a
recording failure (`E223`), with the local tags and commits left available for inspection. The run still exits
non-zero and its closing webhook says `interrupted`.

A fleet released from a [control repository](../../control-repository.md#source-history-mode) recovers by the same
rule, with the records spread across the repositories that own them. Each successful package is tagged in its owner
after publishing, already recorded successes survive a failure elsewhere, consumers of a failed provider stay blocked,
and the next run reconstructs the remaining plan from those source tags. A recording error is reported separately and
never turns an unrecorded publish into a success. The control-repository page states what to do when a source tag is
durable but the control checkpoint failed.

If release commits are enabled, commit or stash pre-existing changes in selected package folders and `commit.include`
paths before retrying; dispat refuses them so its automatic commit cannot capture unrelated work. It likewise refuses
pre-existing changes only in selected package folders where `revertOnFail` could reset them. With both features off,
writer output left by the earlier run remains valid input to the retry.

A consumer that failed while its provider released keeps its old dependency range. On the consumer's next release,
the version stage updates that range to the published provider version (`W197`). This is
[the auto-version reconciliation](../../configuration/autoversion.md#picking-up-providers-released-without-you), not a
release triggered by the provider.

## What a failed run leaves in the working tree

With release commits enabled, a package that is skipped after an earlier stage of it ran has its folder restored
before the run ends, whether or not `revertOnFail` is set. The run proved that folder clean when it started, so every
change in it names a version that did not publish, and the release commit does not stage it. Tracked files are
restored from HEAD and untracked files are removed, exactly as `revertOnFail` does, so the retry starts without
refusing that folder over pre-existing changes. Two folders are left as the run left them, because restoring them
would reach files another package owns: a package rooted at the repository root, and a package whose folder holds
another package's folder.

A package that failed keeps the [`revertOnFail`](../../configuration/spaces.md#space-options) rule: its folder is
restored only when the setting is on, so you can inspect what its stages wrote. The one exception is a release whose
shared files have to be re-synchronized, described below, which restores the failed package's tracked files and keeps
its untracked ones. Without release commits nothing is restored by default, because nothing proved the folders clean
before the run.

In a fleet, the same rule applies to each repository that makes release commits, and the folder is restored inside
the repository that owns it.

### Shared files of the release commit

A [`commit.include`](../../configuration/records.md#commit) path such as a root lock file is shared by every package.
A whole-workspace regenerator in a [`syncLock`](../../configuration/autoversion.md#the-options) script writes every
package's manifest version into it, including the planned version of a package that later fails or is skipped. When a
package whose version or `syncLock` stage ran does not publish while another package does, dispat therefore
re-synchronizes the shared paths before the release commit:

1. The tracked files of every such package are restored to HEAD, so that its manifests name the version that exists.
   Its untracked files stay for inspection.
2. The `commit.include` paths are restored to HEAD. Every published package's folder and changelog file keep what the
   run wrote.
3. The `syncLock` scripts of every published package whose `syncLock` stage ran commands run again, one package at a
   time in dependency order. Their environment lists each package that did not publish at its previous version with
   `DISPAT_WORKSPACE_<KEY>_RELEASING=false`, and leaves it out of `DISPAT_UPDATED_*`.

The release commit then records only versions that published. When every package that prepared its release files
published, none of this happens.

If a script of step 3 fails, or the run is interrupted before step 3 finishes, dispat restores the `commit.include`
paths to HEAD again, because a regenerator stopped halfway can leave a file half written, and makes the release commit
without them. The published packages are still committed and tagged. The run reports `E223` with the paths and the
packages whose scripts regenerate them. Run those scripts yourself and commit the paths, for example with the script
name the space's `autoVersion.syncLock` lists:

```sh
dispat run npm-install --package web --since all
git add pnpm-lock.yaml
git commit -m "chore(release): regenerate the lock file"
```

The re-synchronization covers a single repository history. A fleet commits each package's `commit.include` paths into
that package's own release commit while the run is still going, so no later step can take a package's write back out
of a sibling's commit. In a fleet, check a shared file that a package which did not publish may have written, and
regenerate it before the next release.

## Repair GitHub metadata after the package was recorded

A GitHub API error can happen after a successful publish, source tag, and (in a fleet) control checkpoint. An ordinary
`dispat release` retry sees the source tag and skips that package: it does not republish it, but it also does not
recreate the missing GitHub release. Verify the exact published version and source tag, the control checkpoint if one
was required, and the absence of the GitHub release before repairing only the metadata.

A flow that creates the release from its own publish stage, with `dispat github` after `dispat commit --tag --push`,
reaches the same state without failing. The tag is already pushed when the release is refused, so `dispat github`
logs the refusal as `E222`, closes with a warning naming the package in `failedPackages`, and exits `0`: the package
is reported published and its consumers release. A `dispat changelog` in the same stage treats an entry it cannot write
the same way. Search the run's log for `E222` or `finished with failures` to find what to repair. Only a tag that
`dispat commit` could not create or push fails the stage and skips the consumers, because without it the next run
cannot tell that the version shipped. A flow that wants a missing record to fail the package passes
`--ignore-errors=false`; see [the github command](../../cli/github.md#when-a-release-cannot-be-created).

Run the standalone GitHub step in a reviewed CI repair job using the original run's configuration, credentials,
`DISPAT_PACKAGE`, `DISPAT_NEW_VERSION`, and `DISPAT_TAG`. The `--since all` window includes the already-tagged package.
For a package covered by `github.allPackages`, the CI command can be:

```sh
DISPAT_PACKAGE=core DISPAT_NEW_VERSION=0.1.0 DISPAT_TAG=core@0.1.0 \
  dispat github --package core --since all
```

Use the actual package, version, and tag from the failed run. If the original GitHub release was opted in through
`DISPAT_EXPORT_GITHUB`, restore that variable and make its original attachment files available to the CI job as well.
Check that the GitHub release names the existing tag and contains the intended assets. Do not run a new publication,
create or move a tag, or choose a new version to repair metadata; do not run this production repair from a local
workstation. An existing GitHub release is skipped by the standalone step (`W224`).

To see the notes before the repair, or to create the release page by hand, rebuild them from the commits with
[`dispat preview --version`](../../cli/preview.md#previewing-a-past-release). Later releases do not change the
result, because the preview plans again at the tag's commit:

```sh
dispat preview --package core --version 0.1.0 --github
```

The output is the body the release would have sent. The preview reads the current configuration, so check that the
`github` entry format has not changed since the release.
## Recover an npm publication without a release tag

Treat npm's version and dispat's release tag as separate records. If npm accepts a tarball and the run stops before
the Git tag reaches the remote, do not create the missing Git tag or npm dist-tag by hand. First inspect the exact npm
version, the workflow revision, and the tarball integrity outside the publish script.

If a workflow is interrupted after npm reports success and before Git records are written, inspect npm separately.
A project whose publisher intentionally runs only `npm publish` leaves that decision to the workflow operator;
rerunning the same publish command is unsafe. npm does not permit a name and version combination to be reused after
publication, including after an unpublish. Recover forward with a new patch version through the reviewed workflow, while keeping any independent
binary or provider pin unchanged when its bytes did not change. For example, reserve `1.10.0` and release `1.10.1`
rather than trying to recreate `1.10.0`.

Keep artifact validation before publication: build and inspect one tarball, verify its package name, version, and digest,
exercise that exact file, then give the same path to npm. The publish stage can then remain one upload operation. If the
entire package and all its versions are unpublished, npm also requires waiting 24 hours before publishing another
version of that package. Use npm's authenticated procedure and follow its
[unpublish policy](https://docs.npmjs.com/policies/unpublish/) as a separate administrative action; removal does not
make its version reusable.

The 24-hour restriction applies to every new version under the removed package name. Advancing the patch version
does not bypass it. A registry E409 indicating that the package is still being processed can occur during this
interval; inspect the package's `time.unpublished.time` and wait until the restriction has expired before retrying CI.

After a successful publication, independent installation checks may need to wait for registry metadata to become
available. Poll for the exact version with a bounded deadline before installing it. Keep that readiness check in a
post-release job, after release records, so a delayed registry response cannot turn an accepted upload into a failed
publish stage. This short readiness wait does not replace the 24-hour restriction after complete removal.

### Publish the next patch before removing the previous version

If the package is still published, an alternative is to keep its name and publish the next patch through the reviewed
workflow first. For example, publish `1.10.1`, verify its release records and installation, then unpublish only
`1.10.0` using `npm unpublish <package-name>@1.10.0`, if npm's
[unpublish policy](https://docs.npmjs.com/policies/unpublish/) permits it. Keep `1.10.1` published: removing only the
previous version avoids complete package removal and its 24-hour restriction, without renaming the package.

Exact dependency pins and lockfiles that still require `1.10.0` may fail to install after removal; update those
consumers to the replacement patch. If removal is not permitted or would disrupt consumers, deprecate only the old
version with a message directing users to the patch. The removed version remains unusable for future publications.
If all versions were already unpublished, wait out the restriction described above before publishing the patch.

## When somebody pushes while the release runs

The [behind-remote guard](./release-lock.md) closes before the plan is computed, so it cannot cover a commit pushed
after the run has started. Such a commit reaches the release at the very end, as a rejected push, by which point the
packages have already published: refusing there would leave a released package with no commit, no tag and nothing on
the remote.

dispat pulls the branch, merges the new commits with its release commit, and pushes the merge. The original release
commit and its tags are preserved. A successful recovery exits `0` and reports the merge:

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

Another commit can arrive between the pull and the retrying push. dispat retries this recovery up to three times and
warns on each attempt. The release tag continues to point to the original release commit, never to a recovery merge.

The warning is there because the branch the release went out on is not the branch the run was planned against. Read it
as a prompt to check that the merge is the history you wanted.

dispat stops before merging anything when the remote already carries a release tag this run is about to push:

```console
12:04:05 ERR push failed code=E224 error="commits landed on origin/main during the release, and origin already carries core@1.2.0: this checkout planned a version that is already published, so pushing again would move a released tag. Pull and run again" remote=origin
```

That is a checkout whose tags were stale enough for the plan to recompute a version somebody else has already released.
Pull and run again instead: the plan then reads the tag that exists and releases the version after it. Moving aliases
are not part of the check, because moving them is what every release does.

A checkout that stale is normally refused much earlier. Before it plans anything, a release that pushes reads the
release records of the remote it records to, under its lock, and stops with `E196` when the remote holds one this
checkout does not (see [plan errors](../plan-errors.md)); the remedy is `git fetch --tags`. This check is the last
line, for a record that arrived while the run was working. Whichever of them reports it, no release tag is ever
replaced on the remote: the push creates the name or leaves what is there alone.

### When what landed conflicts

If an arriving commit conflicts with the release changes, dispat still records the published release and preserves
the conflicting work for you to resolve:

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
