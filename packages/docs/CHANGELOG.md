# Changelog

## packages/docs/v1.11.0-rc.5 (2026-09-25)

### Features

- add an optional sign stage that writes each package's own version ([13b321c](https://github.com/yohimik/dispat/commit/13b321ccd6f60ccc7fc2f0b9721e9b311119a429)) (by yohimik, Claude Opus 5.5)
  A release can run a sign stage before the propagate (version) stage and the
  build. `autoSign` enables its native step, which writes the version the plan
  computed for each package into the package's own manifests (`manifests: root`
  by default, or `all` to reach a format such as Unity's
  ProjectSettings/ProjectSettings.asset), and `flow.sign`, `flow.beforeSign` and
  `flow.postSign` name its scripts and hooks. The stage is the package's first
  task: beforeAll runs before it, it waits for the providers the way the version
  stage does when it comes first, it shares the build budget, and it stays on the
  orchestrator when worker nodes are configured. A failure there is reported as
  the `sign` stage, logged as "auto-signing failed" for the native step, and
  reverted under revertOnFail.

  The sign stage owns the own version. Beside an enabled autoSign, autoVersion
  (or autoPropagate) writes dependency ranges and replace rules alone:
  writeVersion defaults to false, an explicit `writeVersion: true` is refused at
  load, and `dispat autoversion` writes the ranges only unless --write-version
  asks otherwise. W192 is reported by the stage that writes the version, and
  syncLock still runs after a change only the sign stage made.

  A package whose configuration names neither autoSign nor a sign entry has no
  sign stage: no task, no hooks, no events, and its release runs exactly as
  before. The plan digest carries the sign commands and the autoSign policy, so
  its schema is dispat-plan-digest/2.

- accept propagate as the version stage's name and autoPropagate as autoVersion's ([d0a281f](https://github.com/yohimik/dispat/commit/d0a281fb8a8356ee4a4357180b7befb595feb628)) (by yohimik, Claude Opus 5.5)
  The version stage is also the propagate stage: it propagates the versions a
  package takes from its providers into its files. The configuration accepts
  that name beside the existing one. `autoPropagate` is `autoVersion` with the
  same options, and `flow.propagate`, `flow.beforePropagate` and
  `flow.postPropagate` are `flow.version`, `flow.beforeVersion` and
  `flow.postVersion`.

  Each pair is one setting. A level stating either spelling replaces what a
  level above stated under the other, and one object stating both spellings of
  a pair is refused when the configuration loads, at the root, in a space entry,
  in a space folder's file and in every package layer. Messages about the block
  name the key the file wrote. The stage keeps its runtime name whichever key
  configured it: DISPAT_STAGE, DISPAT_FAILED_STAGE, the webhook fields and the
  log say `version`. A configuration that uses neither new key loads and runs
  exactly as before.

- let a worker find its mailbox from the repository it runs in ([fdb7133](https://github.com/yohimik/dispat/commit/fdb7133b9cf0b54749484e1177ce6e55e0bec6c5)) (by yohimik, Claude Opus 5.5)
  `dispat worker` no longer requires `execution.endpoint`. A worker started in
  a checkout of the repository being released, with none stated, reads its
  work from that checkout's release remote, `commit.remote` or `origin`, at the
  push URL Git resolves for it, which is where an orchestrator's link with no
  endpoint sends the work. A pool that coordinates through the repository
  itself then names its mailbox nowhere.

  The resolution is strict. A folder that is not a Git repository, a remote
  that is not configured or pushes to more than one URL, and a push URL that
  carries a credential or cannot be a mailbox are refused with E225 before the
  worker opens its state folder, naming both remedies: state
  `execution.endpoint`, or start the worker in a checkout of that repository.
  The push URL is held to the rules of an endpoint by the same check an
  orchestrator's link is held to.

- reach workers through the repository being released by default ([b1674f1](https://github.com/yohimik/dispat/commit/b1674f109f588ae16997b711c3abc7341ee1ccac)) (by yohimik, Claude Opus 5.5)
  A worker link now needs only the node's name. A link with no endpoint, in
  execution.workers or as `--worker name` on release, run and status, reaches
  the remote the release takes its lock on, at the push URL Git resolves for
  it, which in a composed workspace is the entry repository's, and the worker
  names that repository as its own endpoint. A pool needs no mailbox
  repository of its own, and a snapshot sends only what the remote does not
  already hold. An endpoint a link states still names another mailbox. The
  push URL is held to the endpoint rules when a run that dispatches starts,
  before any lock: one that carries a credential is refused with E225, naming
  the link and the remote and never the credential, and a release checks it
  again at the destination its lock was taken on. `status --worker` resolves
  nothing. A `--worker` value that is neither a node name alone nor
  name=endpoint with both halves stated is a usage error and is never echoed.
  The configuration tests that refused a link with no endpoint now accept it.
  The docs describe the repository as the mailbox and what follows from it:
  whatever travels is readable by whoever can read the repository, and the
  host's branch and tag rules keep worker credentials away from release
  branches, release tags and the lock tag.

### Fixes

- refuse a pushing release from a detached HEAD before it starts ([4659f22](https://github.com/yohimik/dispat/commit/4659f221c4a00d9741a3fc0fd53bcb26c56e5e10)) (by yohimik, Claude Opus 5.5)
  A single repository pushes its release commit as the branch it has
  checked out. With commit.push on and a detached HEAD there is no such
  branch, and the run went on to take the lock, build and publish every
  package, and only then failed its push with E224, leaving a published
  release whose commit and tags never reached the remote. The release now
  refuses a detached HEAD with E337 at its entry, before the lock, any hook
  or any package work, and the error says to check out a branch (for
  example actions/checkout with a ref). commit.branch does not change this
  in a single repository; it names a fleet source's branch.

  The recovery merge after a rejected push no longer carries its own
  detached-HEAD refusal, and the behind-remote check no longer skips a
  detached HEAD: neither can meet one now. The unit test that expected the
  behind check to pass a detached HEAD is replaced by one for the refusal,
  and the recovery test that fails the run's branch reads counts the entry
  check's read before the one it fails.

- announce how much faster planning is on long histories ([787f1b6](https://github.com/yohimik/dispat/commit/787f1b682f025110bb2589a1cb225409b211b478)) (by yohimik, Claude Opus 5.5)
  The candidate's announcement, and the stable draft beside it, say what the
  planning work bought in one measured sentence: on the 50,000-commit
  benchmark, planning takes 0.4 seconds instead of 2.9. The cards carry the
  same words, and their previews are rendered again with the seed each
  release's announcement uses, so they show the post as it goes out.

- report a release tag the remote declined as a failed push, not E221 ([10eb970](https://github.com/yohimik/dispat/commit/10eb9707fd56be5b5b401ec19f93b9f5b38e4bc2)) (by yohimik, Claude Opus 5.5)
  A release tag is pushed create-only, and a refusal is read back from the
  remote to tell a record at another commit (E221) from this run's own write
  arriving twice. A remote that declined the tag for a reason of its own, a
  hook, a tag rule or a missing permission, holds no tag by that name, and
  that answer was read as a record at another commit: the run reported E221
  naming an empty commit, which sends the operator looking for a release
  nobody made, and then logged that it had pushed the release commit and
  tags. Such a refusal is now the push failing: E224 names the tag and the
  reason the remote gave, the other refs of the same push are still reported,
  and the package stays published. Once the remote accepts the tag, push it
  from the checkout that holds it.

- refuse conflicting boundaries two peers of a linked fleet state ([0cf0af8](https://github.com/yohimik/dispat/commit/0cf0af8b89192387bf784dd523da746cb427b801)) (by yohimik, Claude Opus 5.5)
  A linked fleet merges the repositoryBaselines of every peer, and the merge
  kept the first tuple for a consumer, release tag and repository and dropped
  any later one without reading it. Two peers stating one boundary at different
  commits therefore planned from whichever tuple the entry repository's own file
  held: a run from one peer released a catch-up that a run from the other peer
  did not, and no E333 was reported from either.

  Every peer's tuple is resolved. Tuples that resolve to one commit are
  one boundary and are kept once; tuples that resolve to different commits are
  E333 naming both peers and both revisions, from whichever peer the run starts
  in. A duplicate within one file keeps its own refusal.

- refuse an install asset pattern that can never expand before any request ([4537f25](https://github.com/yohimik/dispat/commit/4537f252b118eb3b75d8880441095c198e208493)) (by yohimik, Claude Opus 5.5)
  `dispat install --asset 'tool-{arch64}'` asked the release API for the
  release, then failed with exit 1 naming the placeholder, while the install
  page promises that a mistake in the command line exits 2 before any request
  is made. Whether a pattern names only the placeholders dispat knows, and
  closes every brace, does not depend on the release, so such a pattern is now
  refused as a usage mistake: exit 2, the placeholder named, nothing asked.

- name the pipe-bound test by the name it carries in the run goal ([50a7eed](https://github.com/yohimik/dispat/commit/50a7eedb1dcd3be172372eefe4d16a1897289cb8)) (by yohimik, Claude Opus 5.5)
  The TinyGo notes point readers at the integration test that bounds a script
  whose child keeps the output pipes open. That test now lives in the run goal
  as TestRunScriptThatLeavesAChildHoldingTheOutputPipes, and the page names it
  so.

- announce the sign stage and what this candidate hardens ([c281459](https://github.com/yohimik/dispat/commit/c28145989a7def6f7bbdd53557747e661b2e8842)) (by yohimik, Claude Opus 5.5)
  The candidate's caption and card now say what it ships besides the pick-up
  work: an optional sign stage that writes each package's own version, the
  version stage's second name propagate beside autoPropagate, records that
  still land when an interrupt reaches the release commit, skipped packages
  that leave no edits or lockfile entries behind, a prerelease app that went
  ahead of a failed library catching up, and worker nodes that find their
  mailbox in their own checkout. The links name the sign stage's page, the
  previews show this candidate, and the stable draft mentions the new stage.
  Both captions stay inside Discord's and Instagram's limits.

- push the first snapshots of different nodes at once ([226f22c](https://github.com/yohimik/dispat/commit/226f22c7074a60018b123c90f994554c9cee8972)) (by yohimik, Claude Opus 5.5)
  The orchestrator held one lock across the push of every input state, so the
  first state a run sent to one node waited for the push of the same state to
  every other node. With two mailboxes that take 200 ms to accept a push, a
  run's first dispatch to two nodes waited about 565 ms instead of about 300 ms.
  The pushes to different nodes now travel at once. Two dispatches that need one
  state on one node still share a single push, and a dispatch that waited for a
  push that failed pushes the state itself.

- bound each poll of a worker's mailbox ([cbe3e83](https://github.com/yohimik/dispat/commit/cbe3e83745b7411510622c9357813401ab8395d5)) (by yohimik, Claude Opus 5.5)
  A worker polled its mailbox on the goroutine that claims work with no
  deadline of its own, so a listing or a fetch that hung on a dead connection
  stopped the node until the process was restarted. Each poll is now bounded by
  execution.transfer.timeout, the window the operator allowed for moving a
  task's inputs, which is what a poll fetches. A poll that runs out of time is
  reported as a warning and the next poll asks again, without reopening the
  cache.

- keep git maintenance out of a worker's polls ([5b1f5cc](https://github.com/yohimik/dispat/commit/5b1f5cccc3757d9c9a279a722daf3f84f7f0542b)) (by yohimik, Claude Opus 5.5)
  Every fetch a worker's poll made started git's automatic maintenance in the
  foreground, so a busy worker could stall on a repack in the middle of a poll,
  and the objects of closed coordination branches stayed in its cache: each
  task left its messages and fetched inputs behind, 18 objects and 136 KiB per
  probe with a 64 KiB input state in the measured case, growing without bound.
  A worker now turns git's automatic maintenance off in its cache, and compacts
  the cache itself after a minute with nothing claimed and nothing in flight,
  once per idle stretch and never while a task runs. The cache now stays at the
  size of what the open branches reach.

- start a worker with an empty coordination cache ([5698442](https://github.com/yohimik/dispat/commit/5698442f70e444a4183ddee1da604fbcbf188acd)) (by yohimik, Claude Opus 5.5)
  A worker removes the coordination refs it fetched when their branches close,
  but it only knows the refs its own process fetched. A worker that was killed
  or restarted left the refs of its earlier process in its cache for ever, and
  each one kept a task's inputs or an output set on disk. A worker now removes
  every coordination ref in its cache the first time it opens the cache, before
  its first poll. It does this only in its own bare cache, never in a checkout
  where another dispat process may be using its refs.

- refuse a space file stating both versioning and versionGroup ([951d3f4](https://github.com/yohimik/dispat/commit/951d3f4fa55b8bf74ace51027d3fb0581c28befc)) (by yohimik, Claude Opus 5.5)
  A space folder's own configuration file that stated both `versioning` and
  `versionGroup` loaded without a word: the merge folds the two into one axis
  and kept the group, so the versioning the file wrote was silently dropped.
  Every other layer already refuses the pair as a contradiction. The file is
  now checked before it is merged, and the error names the file.

- recover self-update backups a crash left parked and refuse a blocked slot before downloading ([144c870](https://github.com/yohimik/dispat/commit/144c870e0d762cbc330f6cc4f17a3f5f831592e9)) (by yohimik, Claude Opus 5.5)
  An update parks the previous rollback copy in a staging directory until the
  new binary is in place. A process killed in between left that copy there for
  good, so `self-update --rollback` reported that there was no backup, and a
  download staged beside the binary kept its 15 MiB. The next update, rollback
  or restore now puts a parked copy older than an hour back in the backup's
  place, drops it when a newer backup already holds that place, and removes a
  staged download of the same age; younger leftovers belong to an update that
  may still be running.

  A folder, link or other special file where the backup is kept is now named
  with its remedy (move or remove it, then re-run) before anything is
  downloaded, and `self-update --check` reports the same obstruction. A first
  install, which writes nothing there, no longer refuses because of it. A
  rollback, including `dispat install --rollback`, refuses to move anything but
  a file, or a link to one, into the tool's place, and `--rollback --check`
  says so rather than offering a restore.

- keep versions that did not publish out of the release commit's shared files ([dd240aa](https://github.com/yohimik/dispat/commit/dd240aa9f15e68e83c97efab2b97f3868ee9d34d)) (by yohimik, Claude Opus 5.5)
  When a package whose version or syncLock stage ran does not publish while
  another package does, a single history re-synchronizes the commit.include
  paths before the release commit. The unpublished packages' tracked files and
  the include paths return to HEAD, leaving every published folder and
  changelog alone, and the published packages' syncLock scripts run again with
  the unpublished packages listed at their previous versions and left out of
  DISPAT_UPDATED_*. A whole-workspace regenerator such as pnpm install
  otherwise left a failed or skipped package's planned version in a root lock
  file, and the release commit recorded a version that never published.

  If a script run for the re-synchronization fails, or the run is interrupted
  during it, the include paths return to HEAD and stay out of the release
  commit, E223 names the paths and the remedy, and the published packages are
  still committed and tagged. A run in which every prepared package published
  is unchanged. A fleet commits include paths into each source commit while
  the run is going, so the re-synchronization covers a single history, and the
  recovery guide describes the fleet case.

- restore a skipped consumer's folder in commit mode ([eeeac8d](https://github.com/yohimik/dispat/commit/eeeac8d0aaf73366fbd4ec59de9748072baf77f9)) (by yohimik, Claude Opus 5.5)
  In commit mode a package skipped after its version stage ran has its folder
  restored whether or not revertOnFail is set. The run proves every releasing
  folder clean before it starts and the release commit never stages a skipped
  folder, so its edits named a version that did not publish and made the next
  release refuse to start over pre-existing local changes.

  The repository root and a folder holding another package's folder keep their
  edits, because a restore there would reach another package's files. A failed
  package keeps the revertOnFail rule, and a run without release commits is
  unchanged. In a fleet the policy of the repository that owns the folder
  decides. Reverts in one checkout take the repository's mutation lock in turn,
  so skipped siblings no longer race on Git's index lock, and in a run that
  delegates work a restore holds the snapshot guard, so no node is handed a
  folder half restored.

- let a restarted worker reclaim a state folder its own process id names ([46fdded](https://github.com/yohimik/dispat/commit/46fdded74b62bbf7f9cb313f897c47979246ee10)) (by yohimik, Claude Opus 5.5)
  A worker restarted in a container is process 1 every time, so the
  `worker.lock` its crashed predecessor left names the restarted process
  itself. The lock read that id as a live owner, since the process asking is
  running, and every restart was refused with E225 until somebody deleted the
  file, although the documentation promises that a crashed worker restarts
  without that. A process has not written its claim when it reads the lock, so
  its own id there can only be an earlier process's, and the folder is now
  taken over as from any process that is gone.

  The state tests that stood in for a second live process with the test's own
  id now use the process that started the test, which is alive and is another
  process.

- bound and contain coordination cleanup and task panics ([20fdf8f](https://github.com/yohimik/dispat/commit/20fdf8f6e15ad1648ca973bd5407c30d5ea8fe65)) (by yohimik, Claude Opus 5.5)
  A distributed run's cleanup now ends within a bound whatever its mailbox
  does. Revoking an attempt that passed its deadline waited on a detached
  context with no deadline, and closing the run's coordination branches did
  the same, so a mailbox that hung after preflight kept the release, and every
  lock it gives back afterwards, waiting for ever. The revocation now waits at
  most `timeouts.cancel`, and the close at most `timeouts.cancel` and never
  less than 30 seconds; the branches a hung mailbox kept are reported with
  W244, and the refs the run fetched are removed on a bound of their own.

  A failure inside dispat's handling of what a mailbox carries no longer ends
  the process. A panic reading one branch quarantines that branch and the poll
  goes on; one during a probe fails that node's preflight; one in a node's task
  reports a failure when its command never started and nothing when it did;
  and one in a delegated publication after the run authorized it is settled as
  an unanswered publication, never as a failure.

- stop holding the mailbox across network transfers and fetch only this run's branches ([d07b0b2](https://github.com/yohimik/dispat/commit/d07b0b2f0625cc23e1ac7a3cf2b3bc31ab97d9c3)) (by yohimik, Claude Opus 5.5)
  A distributed run no longer serializes its mailbox behind one git call. The
  mailbox held one lock across every push, fetch and listing, so a node pushing
  a large result blocked its own polls, withdrawals and the read of another
  task's publication authorization, which expires two minutes after it is
  written, and a waiter could not leave when its context ended. The lock now
  guards the mailbox's memo alone; the writes to its own transport refs are
  ordered by a wait that ends with the caller's context, and pushes, listings,
  object writes and reads run at once.

  An orchestrator polling a mailbox other runs share fetches only the branches
  of its own attempts, instead of every moved branch addressed to the node, and
  does not remember the others, so another run's output trees never reach the
  repository being released. The refs a process fetched are removed when their
  branch disappears from the remote and, on close, all of them, on a bounded
  context of their own.

- keep what a publisher reported when its authorization is withdrawn ([acd4678](https://github.com/yohimik/dispat/commit/acd46786c0fc95b7aa82bcbfa672fb34a7d003cb)) (by yohimik, Claude Opus 5.5)
  A run that withdraws a delegated publication, because its authorization was
  refused, lost, left unanswered or overtaken by an interrupt or a lost lock,
  now reads what the node already wrote in the withdrawal's place. The node's
  own success result is a publication and is recorded as usual, which is also
  how a publication authorized before a lost lock is still recorded; its own
  failure is an ordinary publish failure. Before, any terminal message found
  there was read as "stopped after the command started", so a publisher that
  had succeeded, or failed cleanly, was reported as E228 and kept its lock.

  An acknowledgement found there is read for its phase and command flag rather
  than assumed. A publisher withdrawn because its authorization was refused
  whose acknowledgement says the command had started, with no result, is now
  reported as E228 instead of as a publication withheld.

- tell a refused coordination push from a lost one ([d9606e9](https://github.com/yohimik/dispat/commit/d9606e9bdd7b2f3a8cab788de1b5a8bfced09362)) (by yohimik, Claude Opus 5.5)
  A distributed run now tells the three answers a coordination push can get
  apart. A lease the remote refused and an update a server rule or hook
  declined never landed; a `[remote failure]`, an unnamed refusal or a push
  that reported nothing may have. Before, every refusal read as a lost lease
  and every missing answer as a failure, so a claim, a ready or an assignment
  whose push applied but whose response was lost stranded the task until
  `timeouts.task`, raised a false E228 or left a branch nobody closed.

  A push that did not report success is settled by reading the branch and
  never by pushing again: the message on the tip, or on the tip's first-parent
  chain, landed; a refused message the branch does not carry did not; anything
  else is unknown after three reads. Every branch the orchestrator creates is
  recorded for cleanup before it is pushed. An assignment whose create stays
  unknown is revoked under a lease on itself and offered again when the branch
  is gone. A node adopts its own claim when it surfaces, and reports a result
  a server rule refused once more without its outputs, as a failure naming
  `transfer-refused`, so the run hears an answer long before its deadline. A
  lease over a branch that is already gone closes it.

  TestExecutionLostAuthorizationResponseRetainsExclusion changes by design:
  an authorization found on the branch, or under the result the node wrote on
  top of it, landed, so the "visible" row now publishes, records the package
  and gives the lock back. A deleted or rewound branch still proves nothing and
  stays E228. A cancel test wrote an "earlier" withdrawal identical to the one
  the run writes in the same second; it now issues it a minute earlier, since
  an identical object on the chain is one that landed.

- contain a panicking task so its run records and unlocks ([176cd31](https://github.com/yohimik/dispat/commit/176cd318c8e61cb919a97e34d31b835f442e01ed)) (by yohimik, Claude Opus 5.5)
  A panic inside one package's task ended the whole process: whatever else was
  in flight, the records of what had already published and the release lock
  all went with it, and the next run met a lock nobody would give back.

  A task that panics is now contained where it starts. A package whose publish
  had not returned success fails at its stage with the internal error as its
  reason; a package that had published stays published, with the panic as a
  critical of its record and its consumers blocked. The rest of the graph stops
  as it does on an interrupt, while the run itself goes on to postAll, its
  records and its unlock, and exits 1. No onFail or onSkip script runs for it,
  and every lock a task can hold is now given back through defer, so the
  containment cannot wait on a lock the panic left behind.

- let only the control configuration unlock an orchestrated source ([043b51b](https://github.com/yohimik/dispat/commit/043b51b06fd1687d2c754d32df707e3c033d6a74)) (by yohimik, Claude Opus 5.5)
  A source of an orchestrated fleet whose own imported configuration set
  unsafeDisableLock released without its remote lock, although an orchestrated
  fleet runs under the control configuration's policy. One source could take
  the fleet's exclusion apart for itself, and a distributed run was refused for
  a bypass its control configuration never asked for.

  Only the control configuration or DISPAT_UNSAFE_DISABLE_LOCK now releases an
  orchestrated source without its lock. A source whose own configuration sets
  the key is locked like any other, and one warning line names the ignored
  setting and the repositories; it is not W331, which names repositories that
  release without a lock. A peer of a choreographed fleet keeps its own setting.

- settle a release lock push or delete whose answer was lost ([9532a0b](https://github.com/yohimik/dispat/commit/9532a0be315b754469123be0f34bd141a75ce145)) (by yohimik, Claude Opus 5.5)
  A lock push that reported a failure was settled by one read of the lock
  tag's message. When that read failed as well, a push that had in fact
  landed left a lock nobody owned, and every later release was refused until
  somebody deleted it by hand. On the way out, any failed delete was E336 with
  the advice to delete the tag, even when the delete had landed and only its
  answer was lost, or when the lock on the remote belonged to another run by
  then, which that advice would hand to a third run.

  A failed push is now read back from the remote object by object: this
  attempt's object is a lock this run owns, another object is a refusal naming
  its holder, no lock is a push that did not land, and a remote that answers no
  read gets the push removed under a lease on this attempt's own object and an
  E336 refusal naming the attempt and the object, so a lock the delete could
  not reach is recognisable. A failed delete is read back once: no lock is a
  clean release; another run's lock is left alone and fails the run with E336
  and a remedy that says not to delete it, because the run cannot show it held
  the exclusion to its end; this run's own object or a read that failed is E336
  with the remedy that clears it. A publication refused because the lock is
  another run's now carries the same do-not-delete remedy, and every refusal to
  take the lock now carries E336. Giving the lock back still fits a fleet's 30
  seconds per repository.

  TestReleaseLockCleanupPreservesAReplacedLock keeps its E336 and now asserts
  the do-not-delete remedy instead of the advice to delete the tag.

- close coordination branches within a bound before the locks go back ([e33a7a2](https://github.com/yohimik/dispat/commit/e33a7a2565c2eefd75e450529c9d1e2e1c9555fe)) (by yohimik, Claude Opus 5.5)
  A distributed run closes the coordination branches it created on its way
  out, and that close pushed to every mailbox with no deadline. On an early
  return it ran before the release locks were given back, so a mailbox push
  that never answered held every lock of the run for as long as it hung; on
  normal completion it ran after the locks were already given back, although
  nothing of a distributed run should outlive the exclusion it ran under.

  The close now has two minutes, detached from the run's cancellation, and the
  closing phase closes the coordinator explicitly just before it gives the
  locks back. The deferred close that covers every earlier return does nothing
  once that has happened. A close that runs out of time is the W244 warning a
  branch that could not be closed has always been, and never costs the locks.

- record completed releases when an interrupt reaches the closing phase ([b19c0a7](https://github.com/yohimik/dispat/commit/b19c0a70fb4feb7490cfc847cf0db9c66e153b5d)) (by yohimik, Claude Opus 5.5)
  An interrupt that arrived after the packages published, during postAll or
  the finalize phase, reached the release commit, the tags and the push: the
  run decided once, at the start of its closing phase, whether it had been
  interrupted, and ran the records on its own live context when it had not.
  A Ctrl-C during postAll, a commit hook or the release push therefore left
  published packages with no release commit, no tag and no push, which the
  next run releases again.

  The records now run on a context of their own that the run's cancellation
  never reaches and that gets five minutes from the moment the run is
  interrupted; an uninterrupted run is not bounded by it. The operator's hooks
  stay on the live run, in finalize exactly as in a fleet's source records: a
  hook running when the interrupt arrives is stopped and no later hook starts,
  while the commit, the tags and the push go on. Whether the run was
  interrupted is read once the records and the lock cleanup are done, so the
  closing webhook says interrupted whenever the interrupt came. The record
  written after each publish is bounded by the same five minutes, so a remote
  that stops answering cannot hold the run and its lock for good.

- name a fixed group by its authored name in every diagnostic ([e8b3d64](https://github.com/yohimik/dispat/commit/e8b3d64e9201774847f5ec2a516827bef5ecb4c4)) (by yohimik, Claude Opus 5.5)
  A diagnostic raised against a whole versioning group local to one repository
  of a polyrepository workspace carried the planner's internal identity as its
  package, the repository and the group joined by a NUL, so the log line read
  package=group:web followed by an invisible byte and the group name. The
  package field now names the group as its author wrote it and the repository
  it belongs to, as in group:platform of repository web; a group of a single
  history still reads group:<name>. The same change drops an unreachable
  fallback from the planner.

- refuse a standalone tag that would release a provider on a consumer's release commit ([adf3346](https://github.com/yohimik/dispat/commit/adf3346fe0d1594de198d4929cca78ffe6704dd5)) (by yohimik, Claude Opus 5.5)
  A hand-built pipeline that ran `dispat commit --tag --package core` on the
  release commit of a consumer core still owed tagged core there with no
  refusal, although `dispat release` refuses the same selection with E201: both
  tags then sit on one commit, the consumer reads as served, and it keeps the
  provider's old version with nothing left to find the debt. Outside a release
  stage script, `dispat commit --tag` now refuses such a provider before it
  commits or tags anything, unless the same invocation tags the consumer after
  it, and names the consumer, the provider and both remedies. A nested
  invocation inside a release stage is unchanged, since the run that started
  it has already checked its whole selection.

- keep each entry of the experiment tables whole ([e68f073](https://github.com/yohimik/dispat/commit/e68f07307e647db3dd49bf46ce2a835397d4216b)) (by yohimik, Claude Opus 5.5)
  A release experiments table now breaks a line only between two entries of
  its code cells, a tool, a step or a package's final state, and never
  inside one. With the Catch-up column beside them, the page's inline code
  otherwise broke anywhere, splitting `consistent` and `changesets` across
  lines; on a narrow screen the table scrolls within its own box instead.

- describe the propagation experiment as the campaign runs it ([4eae7a9](https://github.com/yohimik/dispat/commit/4eae7a99896b78731c23c95cbeb928dc14956e9d)) (by yohimik, Claude Opus 5.5)
  The release experiments page and the comparison with other release tools
  describe the propagation experiment as it runs: every tool builds where it
  documents a build and meets the fault with its own documented release and
  recovery, with nothing published by hand, and the comparison states what
  each tool's catch-up took as the records show it. The page explains how to
  read the Catch-up column, names the five dispat-only cells, and says that a
  cell whose fault never fired is not published. Neither page describes a
  provider published by hand any longer, nor the counts that came from it.

- show what each release experiment's catch-up took ([36a4251](https://github.com/yohimik/dispat/commit/36a4251131f08de1cd34c749e5cc0164e8c5e4e3)) (by yohimik, Claude Opus 5.5)
  The release experiments page gains a Catch-up column whenever the campaign
  measured one: how many runs and commands finishing a release took once its
  fault was removed, how many of them were done by hand, and whether the
  release converged, in the words the job summary uses. The summary above the
  tables counts the experiments and release tools from the recorded cells
  instead of stating them, and says how many of dispat's measured catch-ups
  finish in one run of one command with no manual step. Records written
  before the harness measured a catch-up, and the archives that hold them,
  render as they did.

- announce the candidate that closes the pick-up gap ([f5bae0e](https://github.com/yohimik/dispat/commit/f5bae0ec5a1d2ee0a08a6e8c3b9a550e1e8cf362)) (by yohimik, Claude Opus 5.5)
  The release candidate caption and card now say what this candidate ships: an
  app that sat out the run where its library shipped still picks the library up,
  a library released alone on the app's own release commit is refused, a new
  experiment counts what each release tool needs to catch up, and worker nodes
  use the repository itself as their mailbox. The stable draft says the same
  about pick-up and the mailbox, and the announcement page names dispat in
  lower case.

- correct when execution refusals happen and who sends stage events ([81af8bd](https://github.com/yohimik/dispat/commit/81af8bdd7e4d67cb5b7380b2ad0aa752c28c04ca)) (by yohimik, Claude Opus 5.5)
  The distributed execution page still described an ownership answer cached for
  five seconds and a single failed lock read counted as a loss; it now says the
  lock is read before the first probe, every assignment and every publication,
  local or delegated, and that a failed read is bounded and retried before the
  run stops. Several pages and the E225 comment said every E225 refusal happens
  before any lock, plan or command, which is wrong for a node that fails
  preflight, an unsatisfiable buildPlatforms and a stage pinned to a worker with
  no worker links: those are refused once the plan is fixed, before any hook or
  stage, and a release gives its locks back. The page said a delegated stage
  raises its events from the worker; the orchestrator raises them and names the
  node in `worker`, and only a `dispat trigger` inside a delegated script is
  sent from the worker. The webhook page now lists the `role`, `node` and
  `worker` format tokens and says what the three fields carry.

- describe the release's worker on the repository mailbox ([5a5eb03](https://github.com/yohimik/dispat/commit/5a5eb036be08056bef1ed0383c3a89679b65423e)) (by yohimik, Claude Opus 5.5)
  The CI page described dispat's own release worker as a machine serving a
  mailbox of its own, seeded from the repository and reached over ssh. It now
  describes what the release does: the job names the machine alone, so the
  coordination branches go to the repository itself, and the machine reaches
  the repository with a GitHub App token minted for the run, confined by the
  host's branch and tag rules, never with the job's GITHUB_TOKEN. It also
  states the two risks that remain: a task on the machine can read the token
  from the worker's environment, and `contents: write` also covers GitHub
  Releases.

- check fleet remotes before planning ([4f74333](https://github.com/yohimik/dispat/commit/4f74333a725c3254ecb1a35a5ca15a9d43fac6d1)) (by yohimik, Claude Opus 5.5)
  A release in a composed workspace planned first and only then checked that
  the repositories it selected could reach their remotes and were not behind
  them, so a stale participant produced a plan computed from outdated tags,
  and hooks, builds and planning time were spent before the refusal. A single
  history has always made this check before its plan. Every participating
  repository that pushes with commit.verify on is now checked before anything
  is planned: its remote must answer and the branch it has checked out must
  not be behind it. A detached checkout has no branch to compare and is not
  refused for it. After the plan, each selected repository settles the branch
  its release commit is pushed to, refuses an invalid name with E337, and
  reads that branch from the remote only when it is not the one already
  compared, so no branch is read twice. The standalone commit step still makes
  both checks together. One unit test asserted that a stale checkout had
  already settled its push branch; it now asserts the branch that was compared
  and that no push branch was settled. The release lock and choreographed
  repositories pages describe when each check runs.

- give back a lock when the authorization push was refused ([d7aa56a](https://github.com/yohimik/dispat/commit/d7aa56a94de75ccabaea72ebcf68d1daa1093eb7)) (by yohimik, Claude Opus 5.5)
  An authorization push that the remote refused was reported as a publication
  whose outcome is unknown: the package failed with E228 and the repository's
  release lock was left on the remote for an operator. A refused push, a
  porcelain rejection of its lease, proves that the branch never took the
  authorization, so no node can have read it. The run now withdraws the waiting
  publisher, as it does when it refuses an authorization itself, fails the
  package at the pre-publish check and gives the lock back. The attempt stays
  marked authorized, so no second authorization follows, and a push that got no
  answer at all is still E228 with the lock retained.

- check the lock before every publication, local or distributed ([56103b1](https://github.com/yohimik/dispat/commit/56103b1b952ecbfa74832175020bff9fd85e0585)) (by yohimik, Claude Opus 5.5)
  Only a release that delegated work to worker nodes asked, before a
  publication, whether it still held its release lock. A release that ran
  everything on one machine took the lock before planning and never read it
  again, so a run whose lock another run had taken over published anyway.
  Every release that holds a lock now opens one ownership gate right after the
  locks are taken, and every publication, local or delegated, passes it: a
  remote that carries another lock object or none refuses the publication with
  E336 and the native-recording-or-lock category before its command starts,
  and a distributed run's assignments borrow the same gate, so one loss is one
  decision. A repository with `commit.verify: false` skips the read with one
  warning, as its records comparison does, and a release with worker links
  refuses that setting with E225, because it reads its lock back before every
  assignment. The release lock, records and execution pages say so.

- name the run in the release lock ([1ffb9af](https://github.com/yohimik/dispat/commit/1ffb9af8d07f1c6ba7d2cf0d8ffb650776f09ef3)) (by yohimik, Claude Opus 5.5)
  A distributed release writes its run id into the release lock tag, on a
  `run` line after the attempt, and a later run refused by that lock names the
  run beside the host and the process that hold it. CCME §28.6 requires an
  abandoned run to be findable from its lock: the run id is what every
  coordination branch of that run carries, so an operator reading a lock that
  was left behind can tell which branches to settle before removing it. A
  release that delegates nothing has no run id and writes the message it
  always wrote. The release lock page names the line as the documented place.

- own a worker state folder by its process id file again ([ae3c294](https://github.com/yohimik/dispat/commit/ae3c2942ef5224fa105c3aeee332125ab0a6a7ee)) (by yohimik, Claude Opus 5.5)
  dispat worker takes no operating-system lock on worker.lock any more. The
  file holds the serving process's id, as it did before rc.5: a worker that
  finds a live id refuses with E225, a worker that finds the id of a process
  that is gone renames the lock aside, checks what it renamed and claims the
  folder, and a normal stop removes the lock. Two changes close the window in
  which workers started together could each finish a takeover they read as a
  win. Every claim settles for one second and is trusted only if worker.lock
  still names its process; any other claimant is refused with the E225 it
  would have met a moment later. A serving worker also reads worker.lock again
  before it claims each assignment, and when another process's id is written
  there it leaves the assignment for that process, stops through its ordinary
  exit path (reason disowned) and exits non-zero with E225. Release removes the
  lock only while it names the releasing process. The answered-work retention
  of 48 hours, pruning on write and complete writes stay as they are, and
  oversized lock content is still refused.

  Output staging no longer probes device identity. An admitted set is staged in
  the checkout's private Git directory; when the final rename into the checkout
  fails, as it does for a linked worktree whose Git directory is on another
  filesystem, the set is staged again in the hidden folder beside the outermost
  checkout and renamed from there, and a second failure is reported as before.
  No build-tagged files remain in the execution package for either.

  Tests: the PID-file takeover tests are restored, with new tests for the
  settle, the per-claim check and the owner-only release; the kernel-lock inode
  test is removed. The concurrent stale-claim integration test keeps "exactly
  one serves" without its inode assertion, and a new integration test proves a
  serving worker stops once another process's id is in its lock. Staging is
  tested with a scripted rename failure and, where a second filesystem exists,
  across a real mount boundary.

- serialise Git transactions without operating-system locks ([25d0223](https://github.com/yohimik/dispat/commit/25d0223717e73ff6f09ff34e5e5a567e9525203b)) (by yohimik, Claude Opus 5.5)
  dispat takes no file lock in the Git common directory any more, so no
  dispat-mutation.lock file is created and nothing depends on how Windows or
  macOS implement byte-range locks. Native Git transactions (release commits,
  tags, pushes, control checkpoints, fleet-link settlements, snapshot checks
  and fleet lock bookkeeping) are serialised per repository within one process
  by an in-process lock keyed by the canonical Git common directory: several
  repositories are taken in sorted order, linked worktrees of one repository
  once, a waiter stops when its context ends, and a release is given back once
  in reverse order. Other processes are not excluded: Git's own index and ref
  lock files refuse a conflicting writer, every transaction re-proves the
  revision it records before it writes, and releases are serialised by the
  remote release lock.

  A nested dispat command that validates an inherited live pin no longer
  queues behind the outer release. It reads the pin, the source HEAD and the
  pin again, and accepts only a HEAD that two agreeing reads admit, reading
  again up to ten times a quarter of a second apart before the existing E330
  refusal. An interrupt ends that wait.

  The cross-process gitx test is replaced by in-process tests: one repository
  serialises, linked worktrees share one lock, opposite orders never deadlock,
  a cancelled waiter takes nothing, and release is idempotent. Tests that
  damaged the lock file are re-aimed at the Git-level checks that now carry
  their claims: a failing common-directory lookup before planning, a source
  commit after publication, a control commit before the checkpoint, a source
  commit between the release commit and its tag, and a source remote that
  refuses to give back its release lock. The composition interrupt test now
  interrupts the stable pin read. TestComposeWorkspaceRejectsUnpinnedSource
  expects two pin reads, which the documented stable read requires.

- name crier 1.1.1 and what each announcement post carries ([ed68eac](https://github.com/yohimik/dispat/commit/ed68eacabb877154eba25f134c9541084f48f636)) (by yohimik, Claude Opus 5.5)
  The CI reference names the crier release the repository installs, 1.1.1.
  The announcement's audio notes say that the recordings score the cover clip
  that opens the Instagram feed post and stories, and that every post carries
  only the channel's card and its hand-written notes, never changelog text.

- refuse a provider release that would hide its consumer's debt ([bed0508](https://github.com/yohimik/dispat/commit/bed0508bda6d397c81638b4bfb25509f49a811b0)) (by yohimik, Claude Opus 5.5)
  Two releases on one commit cannot be ordered afterwards. When a consumer
  released a change of its own on the commit where its provider failed,
  releasing the provider alone on that same commit would tag both there: the
  consumer would read as served, and no later plan could find what it is still
  owed. Such a release is now refused with E201 before any hook, build or
  publish. The error names the consumer, the provider and the commit, and both
  remedies: release the consumer in the same run (--package <provider>,<consumer>),
  or commit first and release the provider alone, after which the next run
  catches the consumer up. `dispat status` shows the same error and exits 0. In
  commit mode the refusal is conservative, because a run cannot know before it
  publishes whether its release commit will be empty.

  When a run releases the provider and then the consumer on that commit and the
  consumer fails, the run now ends with a critical E201 naming the one remedy
  left: an empty commit `release(<consumer>)` with the footer
  `Release-As: <version>`, the version the run planned for the consumer.

  A run whose stages run on worker nodes refuses and picks a consumer up exactly
  as a local run does. The deferred release experiment commits before releasing
  its provider alone, as the refusal asks.

- keep a debt visible to a consumer that sat out its provider's release ([fbf38bc](https://github.com/yohimik/dispat/commit/fbf38bc16d77dc3afa4adfa9e7dbf054358caa0c)) (by yohimik, Claude Opus 5.5)
  A consumer that released a change of its own while its provider's publish
  failed or was held is still owed the provider's version. Planning now finds
  that debt even when the provider shipped later in a run the consumer sat out:
  for every provider and every consumer it reaches, the plan also reads the
  history after the newest provider release the consumer's own release reached,
  so the next full run catches the consumer up (W193) at the version it was
  owed, with no new commit and no provider republish. The extra history is read
  only where a consumer actually got ahead of its provider. It works for any
  release tag, lightweight or annotated, and for a consumer that was held while
  its provider shipped.

  Release tags no longer carry the provider receipt of 1.11.0-rc.5: a tag's
  message is `release <tag>` again, the tag inventory reads three fields, and
  publish steps no longer receive DISPAT_PROVIDER_RECEIPT, so a publish step
  sees the same environment on the orchestrator and on a worker. Tags rc.5
  wrote with a `dispat-seen-v1:` payload remain ordinary release tags whose
  message is not read, and a malformed payload no longer stops planning. A
  release no longer refuses a wide dependency fan-in for the size of a receipt.

- say how to write a symlinked config and that --config takes absolute paths ([992379a](https://github.com/yohimik/dispat/commit/992379a9607e516c13c01e14e62907816a0be80d)) (by yohimik, Claude Opus 5.5)
  The compute page told a reader whose configuration is a symlink to point
  --config at the real file, which cannot help when the link is a $ref
  fragment, because --config names the root file alone. The page says that
  --write refuses a symlinked root file or fragment alike, and that
  replacing the link with the file it points to, or editing that file by
  hand, applies the suggestions. The CLI reference says --config takes an
  absolute path as well as one relative to --root. The comparison and
  experiment pages spell the name dispat in lower case, and lines over the
  page width in the edited pages are rewrapped.

- keep each linked peer's version groups in its own repository ([bb0d049](https://github.com/yohimik/dispat/commit/bb0d0491fe4810f7835c066e5547eed1761088f1)) (by yohimik, Claude Opus 5.5)
  Linked peers of an identity-linked fleet shared one case-insensitive
  version-group namespace. Two peers with a fixed space called libs, one at
  3.4.0 and the other at 1.2.0, became one group, so the second peer's
  packages jumped to 3.4.x with "No changes: a version bump to keep the
  versioning group on one version", and two peers stating different
  policies for one group name refused every fleet command. Each peer is an
  ordinary repository-local root (CCME §27.3, §27.11): the groups it
  declares and the implicit groups of its shared spaces belong to it, a
  same-named group in another peer releases on its own version, and an
  unqualified --group still selects the matching group of every peer.

  A versionGroup is checked against the peer's own configuration when it
  loads, so a peer that referenced another peer's group needs its own
  declaration again. The linked-fleet page, the versioning reference and
  the agent guide describe repository-local groups, and the fleet tests
  that asserted the shared namespace are replaced by one that holds each
  peer's groups to its own repository.

- reset TinyGo rollback retention and expose acceptance failures ([ad62412](https://github.com/yohimik/dispat/commit/ad62412ff79631548fd5a617a0c31d8854ab0790)) (by yohimik)

- bind every receipt and acknowledge cancellation that wins the result lease ([612a58b](https://github.com/yohimik/dispat/commit/612a58b5295015a13203cae8f4648f551a460913)) (by yohimik)

- authenticate queued transitions and settle concurrent withdrawal safely ([f694114](https://github.com/yohimik/dispat/commit/f6941140a99209e851ecf889c54b9aa8cfd023e4)) (by yohimik)

- retain authenticated coordination ownership through cancellation races ([7dceee9](https://github.com/yohimik/dispat/commit/7dceee974ac2937c6d5d64e3cec5c76df0822ed7)) (by yohimik)

- preserve complete writes and reconcile release identities and late claims ([7fb8a84](https://github.com/yohimik/dispat/commit/7fb8a84693d4472e31a73d1cdc10f5db64578b88)) (by yohimik)

- settle worker cleanup races and report failed output merges ([96c435e](https://github.com/yohimik/dispat/commit/96c435e24f2de9a181bbf13b4c2bf49036d29cc0)) (by yohimik)

- isolate distributed outputs and preserve recovery state ([fe7c42f](https://github.com/yohimik/dispat/commit/fe7c42fa606781166be077e9322ff5e173295658)) (by yohimik)

- admit durable receipts and retain fleet command ownership ([f2f1d96](https://github.com/yohimik/dispat/commit/f2f1d965679a22cfe00adb8170e37ee9bd60bdb7)) (by yohimik)

- keep Git maintenance attached and stream tag receipts ([dd79291](https://github.com/yohimik/dispat/commit/dd7929143c99a16f3d920fe99e220d79be8331a0)) (by yohimik)

- preserve uncertain publication and bound worker state ([e92ca16](https://github.com/yohimik/dispat/commit/e92ca16f0cbbee47a18a45b41f085f217e3e45da)) (by yohimik)

- bound transports and fence worker state ownership ([792522e](https://github.com/yohimik/dispat/commit/792522e7a8ad723724dd53fbb967d492d2ceaad9)) (by yohimik)

- harden fleet release recovery and shared version policies ([dbf10c7](https://github.com/yohimik/dispat/commit/dbf10c73478d0bb9f298d4b1ec19cd6c8b177005)) (by yohimik)

### Dependencies

- [dispat](https://github.com/yohimik/dispat/releases/tag/services/dispat/v1.11.0-rc.6): 1.11.0-rc.5 -> 1.11.0-rc.6
- [dispat-alpine](https://github.com/yohimik/dispat/releases/tag/docker/dispat-alpine/v1.11.0-rc.6): 1.11.0-rc.5 -> 1.11.0-rc.6
- [cli](https://github.com/yohimik/dispat/releases/tag/packages/cli/v1.11.0-rc.5): 1.11.0-rc.4 -> 1.11.0-rc.5

### Authors

- yohimik
- Claude Opus 5.5


## packages/docs/v1.11.0-rc.4 (2026-09-23)

### Features

- document distributed execution across worker nodes ([8f9cf6a](https://github.com/yohimik/dispat/commit/8f9cf6a05276834a34229931cec25eb4cf0ef03d)) (by yohimik, Claude Opus 5)
  The orchestrator and worker roles, the Git mailbox transport, build outputs
  as inputs, publishing under an authorization, the locks a run may retain, and
  what the arrangement exposes and how to contain it.

- say that a none relation carries no build outputs to another machine ([bf82653](https://github.com/yohimik/dispat/commit/bf82653a3b064f80a64f2988a004edb94696edb0)) (by yohimik, Claude Opus 5)

- document the three provider relations ([4bb7d2c](https://github.com/yohimik/dispat/commit/4bb7d2c29d456ccf619a614da096a38fc2813603)) (by yohimik, Claude Opus 5)

- describe worker nodes on kubernetes and why cpu autoscaling does not fit ([4d1d438](https://github.com/yohimik/dispat/commit/4d1d438f301256c22f08c5b42e740f14e4c19d29)) (by yohimik, Claude Fable 5.1)
  A worker polls Git, exits when idle and finishes its claimed tasks on
  SIGTERM, so it runs as an Indexed Job for the length of one release, and the
  cluster adds machines for its pending pods. The page says why an autoscaler
  on worker CPU adds nothing: the pool is fixed at the preflight, CPU does not
  show waiting work, the reaction is slower than a release, and scaling down
  kills tasks. It gives the manifest, how large a pool is worth having, the
  isolation a pool that runs assigned commands needs, and what dispat does not
  do yet.

### Fixes

- say the integration passes run in six shards ([15904c9](https://github.com/yohimik/dispat/commit/15904c97b960e272fe33bbe7d8cf2df5af8fc52a)) (by yohimik, Claude Opus 5.5)
  The test results page describes how CI runs the integration suite, and it
  now says that each pass is six processes over an exact split of the
  suite's tests, kept as one log and one row, and that the time the row
  shows for each package is the longest shard's rather than the sum of six.

- describe the machine the release's full-suite job creates for itself ([dcfe0f1](https://github.com/yohimik/dispat/commit/dcfe0f1c631ea4e3d291046686b0bbdadcedbe98)) (by yohimik, Claude Fable 5.1)

- document script sweeps on worker nodes and the --worker flag ([7d23ac1](https://github.com/yohimik/dispat/commit/7d23ac1619bf6d1ea68429006dbc623d650477f6)) (by yohimik, Claude Opus 5.5)
  Distributed execution gains a section on running scripts on workers: what
  a sweep task carries and what it leaves behind, that placement follows
  `runOnly`, that a sweep takes no release lock and so neither waits for nor
  excludes a release, and how `runOutputs` folders come back merged, with a
  disagreeing path leaving neither file. `--worker` is documented beside
  `execution.workers` and on the release, run and status pages; `runOutputs`
  joins the root options and the where-a-setting-lives matrix; the error
  codes, the environment table, the architecture's out-of-scope table, the
  CI pipeline and the Kubernetes example say what changed.

- say how a consumer that proceeded past its provider is caught up ([475f3d4](https://github.com/yohimik/dispat/commit/475f3d443070e11ffba380422c2968bca83ae87b)) (by yohimik)
  Admission by delivery in concepts.md, the recovery walkthrough for a
  failed provider, and the provider relation's manifest rule, which the
  table also had backwards for `none` after the default flipped.

- say which provider publication is a release reason ([23d269a](https://github.com/yohimik/dispat/commit/23d269ac6eb952a75d2823705bd68c46ec39c004)) (by yohimik, Claude Opus 5)

- say that publish order and blocking hold through packages that are not releasing ([320160d](https://github.com/yohimik/dispat/commit/320160d9e620303db94fcf63cf813645ba0741f5)) (by yohimik, Claude Opus 5)

- say where a non-blocking relation can publish a version nobody published ([7d89713](https://github.com/yohimik/dispat/commit/7d89713431b6ed80a8a0edc2242443297e982a38)) (by yohimik, Claude Opus 5)

- say how the minimal topology chooses its links ([36ed709](https://github.com/yohimik/dispat/commit/36ed709344efaf60d779397ee36dae2a3dd5756f)) (by yohimik, Claude Opus 5)
  Among the proposals of fewest links the computation joins the groups at their
  centres, so the longest route between two repositories stays as short as the
  existing links allow. Evidence and settlement work grow with that route.

- say that commit.verify leaves the records uncompared ([274f8a2](https://github.com/yohimik/dispat/commit/274f8a2f7e2160e0bfa3357282346a3b5000c236)) (by yohimik, Claude Opus 5)

- document the release record comparison and the create-only tag push ([228c57d](https://github.com/yohimik/dispat/commit/228c57d6b3dd429ad2c7f260964ab22a79d4db30)) (by yohimik, Claude Opus 5)
  The lock page gains the step it was missing: the remote's release tags are
  read under the lock and compared with the checkout's, so a clone that is
  level with the branch and missing a record is refused rather than planning
  that version again. E196 and E191 gain that second condition with the fetch
  that resolves it, the CI pages say a checkout made without tags is refused
  and how to bring them, and the recovery page stops explaining the old force
  push. What commit.force means is restated where it is described: permission
  not to fail on a ref that is already there, never permission to replace a
  published record, with the remote's answer stated in full beside it.

- keep the single-package example sections whole ([e7bc2bd](https://github.com/yohimik/dispat/commit/e7bc2bda61cb6b917ff3a9a166ebed17f148b144)) (by yohimik, Claude Opus 5)
  The repository-as-the-package section landed inside the subfolder layout it
  follows, splitting that section from its own example.

- say what a repository rooted package is in a fleet ([b969420](https://github.com/yohimik/dispat/commit/b9694203178d141e2bab7315b13e04fbb25a81ef)) (by yohimik, Claude Opus 5)

- document the repository root as a space and a package folder ([5f2b7ec](https://github.com/yohimik/dispat/commit/5f2b7ece12ed2137a5511eca129c45f2fe00b224)) (by yohimik, Claude Opus 5)
  A space may be rooted at the repository, and a standalone entry may name it
  too, so the pages that said a package path cannot be "." now describe what
  such a package owns and the two settings its folder cannot honour.

- say that no axis excuses falling behind the shared part ([b2b5b14](https://github.com/yohimik/dispat/commit/b2b5b14fceb8f553c6099106c0d294da8a30e3d7)) (by yohimik, Claude Opus 5)

- document a versioning group's counter and channel axes ([e1b83ab](https://github.com/yohimik/dispat/commit/e1b83ab6e4235ae18e5992c2fb20f05345385c84)) (by yohimik)
  Shared versions explained the depth and said nothing about the other two
  parts of a group's rule, so the page's statements about a train ("later work
  takes all of them to beta.1", "a graduation on any one member ends the train
  for all") read as the only behaviour there is rather than as what a shared
  counter and a shared channel do. A new section works through each setting
  with the retry and the graduation it changes, and the two safety rules a ride
  always obeys.

  Space options gains the object form, the axis table and the combination that
  is refused. The architecture note rewrites the engagement rule around "a
  shared part moves" and adds the member target floor, the resting-channel rule
  and alignment under an independent counter. The `none` bullet names
  autoreplacer beside auto-versioning and `autowriter --set-local`, which now
  leave a never-released provider's declaration alone as well.

- admit settlement heads and tighten fleet cost bounds ([a7b35ab](https://github.com/yohimik/dispat/commit/a7b35abcbbabca01fef68fbc22418f9099460f64)) (by yohimik, Claude Fable 5.1)
  A settlement moves heads before the revalidation point of §27.2, the
  consumer's own included, and §27.2 admitted only a native record step, so a
  literal reading refused every settled consumer with E330. The exact
  revision a settlement wrote is now an admitted transition, which is what the
  engine already does; vector 28 of §27.12 and the docs page say so.

  The cost model is corrected where it was loose or silent. The publication
  input closure is one condensation and one bitset union per edge, not one
  traversal per repository. A fresh window is the meet of two single-boundary
  windows, so reachability classes count distinct boundary commits and never
  (stable, fresh) pairs, and a window is keyed by the repository it ranges over
  and not by the owner of the package reading it (vector 29). The linked peer
  topology gains the rows it never had: link evidence and settlement, with a
  star bounding every settlement at two commits.

  No release plan changes.

### Dependencies

- [dispat](https://github.com/yohimik/dispat/releases/tag/services/dispat/v1.11.0-rc.4): 1.11.0-rc.3 -> 1.11.0-rc.4

### Authors

- yohimik
- Claude Opus 5.5


## packages/docs/v1.11.0-rc.3 (2026-09-20)

### Fixes

- say that a versioning none provider is never picked up ([d282cfc](https://github.com/yohimik/dispat/commit/d282cfc4553dcbcf1d65c6d6a4d06e54406053f8)) (by yohimik, Claude Fable 5.1)

- remove delayed label ([4811866](https://github.com/yohimik/dispat/commit/481186681cf2f055d902d21c33766253b43641f8)) (by yohimik)

- describe the single announce command and the postPublish outputs ([8a956d7](https://github.com/yohimik/dispat/commit/8a956d7196bae8b4e4cef5a78c26ece156c93601)) (by yohimik, Claude Fable 5.1)

- explain what a space is and where the name comes from ([c45f7eb](https://github.com/yohimik/dispat/commit/c45f7ebd1832bfc4e69f3ade551ae799b1ed5ab6)) (by yohimik, Claude Fable 5.1)

- describe the script-free announcement flow ([9b4cf64](https://github.com/yohimik/dispat/commit/9b4cf6485e1effb1cd7d99b0cf29de4273b37b04)) (by yohimik, Claude Fable 5.1)
  Document the dispat if chain, the hand-written release candidate, the
  generated stable notes, caption limits, ANNOUNCE and replays.

- document channel configurations, notes and links ([13cf8af](https://github.com/yohimik/dispat/commit/13cf8afb6becf678fc4ef7de06d84115c0ffe3ef)) (by yohimik, Claude Fable 5.1)
  Describe the per-channel crier configurations, the shared cross-posting, and
  how RC notes and links are duplicated in the description and the picture.

- explain topology selection and linked release ownership ([35664e1](https://github.com/yohimik/dispat/commit/35664e1685e703c347233421f9faefdba8b1622a)) (by yohimik)
  Update configuration and command references, examples and RC announcements.

- explain polyrepo design choices and announcement channels ([de8b1f9](https://github.com/yohimik/dispat/commit/de8b1f98785fb5f9cdb030b5cf3ce271d8b6bcf4)) (by yohimik)
  Explain submodule links, choreography execution and cancellation, and the
  single CLI approach for scanner and writer. Document channel-specific
  announcements and preserve historical preview links.

- document parser imports and Go naming conventions ([2f591b0](https://github.com/yohimik/dispat/commit/2f591b0139339d13f20c4718056d14be6e0dea98)) (by yohimik)
  Clarify Is and Are predicates, New and new constructors, and conversion
  methods named after their target type.

### Dependencies

- [dispat](https://github.com/yohimik/dispat/releases/tag/services/dispat/v1.11.0-rc.3): 1.11.0-rc.0 -> 1.11.0-rc.3
- [cli](https://github.com/yohimik/dispat/releases/tag/packages/cli/v1.11.0-rc.3): 1.11.0-rc.0 -> 1.11.0-rc.3
- [dispat-alpine](https://github.com/yohimik/dispat/releases/tag/docker/dispat-alpine/v1.11.0-rc.3): 1.11.0-rc.0 -> 1.11.0-rc.3

### Authors

- yohimik
- Claude Fable 5.1


## packages/docs/v1.11.0-rc.0 (2026-09-15)

### Fixes

- clarify polyrepository configuration and release behavior ([88cf2ca](https://github.com/yohimik/dispat/commit/88cf2cae8b0355be0f5e185fc450f0c1949e4502)) (by yohimik, Codex (gpt-5.6-sol))
  Document owner-local folder inputs and parser diagnostics, direct channel
  precedence, and cancellation of hooks during durable release recording.

- document composed repository releases ([7e38ae1](https://github.com/yohimik/dispat/commit/7e38ae131734159acb7011a835219f765c46a983)) (by yohimik, Codex (gpt-5.6-sol))
  Describe central and imported configuration, external edges, source ownership,
  locks, exact pin handoff, diagnostics and partial publication recovery.

### Dependencies

- [dispat](https://github.com/yohimik/dispat/releases/tag/services/dispat/v1.11.0-rc.0): 1.10.0 -> 1.11.0-rc.0
- [cli](https://github.com/yohimik/dispat/releases/tag/packages/cli/v1.11.0-rc.0): 1.10.3 -> 1.11.0-rc.0
- [dispat-alpine](https://github.com/yohimik/dispat/releases/tag/docker/dispat-alpine/v1.11.0-rc.0): 1.10.0 -> 1.11.0-rc.0

### Authors

- yohimik
- Codex (gpt-5.6-sol)


## packages/docs/v1.10.14 (2026-09-13)

### Fixes

- explain root changelog paths and release commit inclusion ([cc66610](https://github.com/yohimik/dispat/commit/cc666103070be3d51425a1a49825163a9ff4556a)) (by yohimik, Codex (gpt-6-astra))
  Keep the CLI example and current/1.10 solo and record references aligned.
  Distinguish package-relative changelog paths from repository-relative
  commit includes.

- explain single-root npm release configuration ([3f7174d](https://github.com/yohimik/dispat/commit/3f7174dc71fcb32bd5146ccc4d79bf729c0c3fd9)) (by yohimik, Codex (gpt-6-astra))
  Add the existing-runtime recipe and synchronize current and 1.10 docs.
  Explain version-stage scheduling, versioning from 0.0.0, explicit initial
  release versions, root-file scopes and recovery for parent manifest changes.

### Dependencies

- [cli](https://github.com/yohimik/dispat/releases/tag/packages/cli/v1.10.3): 1.10.2 -> 1.10.3

### Authors

- yohimik
- Codex (gpt-6-astra)


## packages/docs/v1.10.13 (2026-09-13)

### Fixes

- document same-name npm patch recovery ([4d597ec](https://github.com/yohimik/dispat/commit/4d597ec4d0a638f0ef0a39beef619144bfaf7a03)) (by yohimik)
  Publish and verify the next patch before removing only the previous version.
  Update current and 1.10 recovery guidance with policy and consumer constraints.

### Authors

- yohimik


## packages/docs/v1.10.12 (2026-09-12)

### Fixes

- streamline docs and build checks ([87d7085](https://github.com/yohimik/dispat/commit/87d70851a2fc1ebbd8742960fb2d1a4d2b90f151)) (by yohimik)

- drop verification ([9e7b320](https://github.com/yohimik/dispat/commit/9e7b32009bb048e5a53f75e0dcf454a719bcd1cd)) (by yohimik)

### Authors

- yohimik


## packages/docs/v1.10.11 (2026-09-11)

### Fixes

- clarify npm installation and recovery ([8dc9f82](https://github.com/yohimik/dispat/commit/8dc9f82d7a11d3aa8078d809792c3ddfd30693fb)) (by yohimik, Codex (gpt-5.6-sol))
  Keep README and current and 1.10 documentation aligned with script approvals
  and global repair. Explain saga recovery and link the agent work guide.

### Dependencies

- [cli](https://github.com/yohimik/dispat/releases/tag/packages/cli/v1.10.2): 1.10.1 -> 1.10.2

### Authors

- yohimik
- Codex (gpt-5.6-sol)


## packages/docs/v1.10.10 (2026-09-11)

### Fixes

- document @dispat/bin installation and recovery ([40c8500](https://github.com/yohimik/dispat/commit/40c850094c95c5447827bd10213cb373a1f78d1c)) (by yohimik, Codex (gpt-6-astra))
  Update npm installation and repair examples in current and 1.10 documentation.
  Distinguish metadata propagation from the package removal waiting period.

- document npm installation and recovery ([f56f86e](https://github.com/yohimik/dispat/commit/f56f86e056e6cb5895b33618d3a1e37d29700cba)) (by yohimik)
  Explain installation and recovery after an unrecorded npm publication in
  current and 1.10 documentation.

- document npm installation ([a2895ab](https://github.com/yohimik/dispat/commit/a2895ab364abbe93bb52fd7c0cbe57ef917b36bd)) (by yohimik)
  Explain npm installation, verified native downloads, script approval,
  repair, and npm-managed updates in current and 1.10 documentation.

### Dependencies

- [cli](https://github.com/yohimik/dispat/releases/tag/packages/cli/v1.10.0): 0.0.0 -> 1.10.0

### Authors

- yohimik
- Codex (gpt-6-astra)


## packages/docs/v1.10.9 (2026-09-11)

### Fixes

- distribute the CLI through npm ([597e106](https://github.com/yohimik/dispat/commit/597e1064a2651f93132e678f4f4ed33c1d5bfc13)) (by yohimik)

### Authors

- yohimik


## packages/docs/v1.10.8 (2026-09-11)

### Fixes

- add landing page download counter ([04f1bb4](https://github.com/yohimik/dispat/commit/04f1bb4648b7a9bbab304e5952374b7bb051c572)) (by yohimik)

### Authors

- yohimik


## packages/docs/v1.10.7 (2026-09-10)

### Fixes

- verify public ecosystem examples and generalize package growth ([4b9aefd](https://github.com/yohimik/dispat/commit/4b9aefd95155865c630c44752586eda1579034d8)) (by yohimik)

### Authors

- yohimik


## packages/docs/v1.10.6 (2026-09-10)

### Fixes

- share Android native library checks across game engines ([9a48f92](https://github.com/yohimik/dispat/commit/9a48f929deb4ad0cf1601443822d7241c302dad9)) (by yohimik)
  Link engine integrations to the shared packaged-library alignment check.
  Keep the verified FMOD wrapper finding separate from claims about other
  engines, and update the current and 1.10 documentation together.

### Authors

- yohimik


## packages/docs/v1.10.5 (2026-09-10)

### Fixes

- clarify verified release recovery and metadata checks ([65566d9](https://github.com/yohimik/dispat/commit/65566d927ba0e048c07baa5c65275cfab5add17f)) (by yohimik)
  Explain pending destination recovery, independent tag verification, and
  Changesets publication gating with linked reproductions and CI evidence.
  Distinguish reused successful jobs from repeated execution, and record
  the corrected Apple framework metadata case in the 1.10 documentation.

### Authors

- yohimik


## packages/docs/v1.10.4 (2026-09-10)

### Fixes

- strengthen release integration checks ([fb422ca](https://github.com/yohimik/dispat/commit/fb422ca45b15247e03f32dec71dad27e33921ca6)) (by yohimik)
  Document native wrapper validation, minimum engine compatibility, host and
  plugin checks, immutable download inputs, and downstream patch evidence.
  Add a short integration checklist with references to the agent guide.

### Authors

- yohimik


## packages/docs/v1.10.3 (2026-09-10)

### Fixes

- add consumer checks from verified release failures ([d9b34b6](https://github.com/yohimik/dispat/commit/d9b34b64989fd992479b81ecb5c3a2598a98c905)) (by yohimik)
  Document release-plan completeness, publisher trust across maintained lines,
  published POM/AAR dependencies, crate contents, vendored fixes and bounded
  queue regressions. Link reproducible upstream evidence and distinguish
  consumer failures from planned publication ordering.

  Update the current examples and the served 1.10 snapshot together. Preserve
  native publisher constraints and distinguish implemented release recovery
  from CCME 3 explicit rollback requirements.

### Authors

- yohimik


## packages/docs/v1.10.2 (2026-09-10)

### Fixes

- document release integration across all 21 ecosystems ([a67d844](https://github.com/yohimik/dispat/commit/a67d8441757d027db67ec2bd082e5d1e2cc069e8)) (by yohimik)
  Add verified release and registry findings with practical integration checks
  for native packages, mobile and game engines, Docker chains and Aqua pins.
  Explain single-package recovery, Changesets authoring and pending-record
  corrections without rebasing shared history. Cover complete skill trees,
  versioned specifications and TeX documentation as release artifacts.

  Update the current examples and the served 1.10 snapshot together, preserving
  native publishers, deliberate version policies and documented recovery limits.

### Authors

- yohimik


## packages/docs/v1.10.1 (2026-09-09)

### Fixes

- lowercase dispat branding ([bb4fe1f](https://github.com/yohimik/dispat/commit/bb4fe1fe36b6f813a25f2a87fd48cc2326148b20)) (by yohimik)

### Authors

- yohimik


## packages/docs/v1.10.0 (2026-09-09)

### Dependencies

- [dispat](https://github.com/yohimik/dispat/releases/tag/services/dispat/v1.10.0): 1.9.0 -> 1.10.0
- [dispat-alpine](https://github.com/yohimik/dispat/releases/tag/docker/dispat-alpine/v1.10.0): 1.9.0 -> 1.10.0


## packages/docs/v1.9.0 (2026-09-09)

### Dependencies

- [dispat](https://github.com/yohimik/dispat/releases/tag/services/dispat/v1.9.0): 1.8.2 -> 1.9.0
- [dispat-alpine](https://github.com/yohimik/dispat/releases/tag/docker/dispat-alpine/v1.9.0): 1.8.2 -> 1.9.0


## packages/docs/v1.8.7 (2026-09-07)

### Fixes

- credit CLI and database influences ([8db82c6](https://github.com/yohimik/dispat/commit/8db82c612c59fd7849ecc6895c941aabd2658456)) (by yohimik)

### Authors

- yohimik


## packages/docs/v1.8.6 (2026-09-06)

### Dependencies

- [dispat](https://github.com/yohimik/dispat/releases/tag/services/dispat/v1.8.2): 1.8.1 -> 1.8.2
- [dispat-alpine](https://github.com/yohimik/dispat/releases/tag/docker/dispat-alpine/v1.8.2): 1.8.1 -> 1.8.2


## packages/docs/v1.8.5 (2026-09-06)

### Dependencies

- [dispat](https://github.com/yohimik/dispat/releases/tag/services/dispat/v1.8.1): 1.8.0 -> 1.8.1
- [dispat-alpine](https://github.com/yohimik/dispat/releases/tag/docker/dispat-alpine/v1.8.1): 1.8.0 -> 1.8.1


## packages/docs/v1.8.4 (2026-09-05)

### Fixes

- restore mobile navigation ([ab05286](https://github.com/yohimik/dispat/commit/ab05286210c277207f6f6671a8ec511f483bdd60)) (by yohimik)

### Authors

- yohimik


## packages/docs/v1.8.3 (2026-09-05)

### Fixes

- quiet scene loading ([2ee1d10](https://github.com/yohimik/dispat/commit/2ee1d10680769997510ab451b96b685e7a8ef0e7)) (by yohimik)

### Authors

- yohimik


## packages/docs/v1.8.2 (2026-09-05)

### Fixes

- stabilize captions and page flow ([d3235f0](https://github.com/yohimik/dispat/commit/d3235f000ee83df92a29df9a6e6325fb916ec6cc)) (by yohimik)

- refine demos and navigation ([33f88c1](https://github.com/yohimik/dispat/commit/33f88c12de7c5cdd2de67d1440ddc438c038cd84)) (by yohimik)

- remove hero logo row ([0a34d3b](https://github.com/yohimik/dispat/commit/0a34d3baf94b797023d1b2d0c0b52530ee19bee3)) (by yohimik)

- align demos and mobile menus ([2c71b52](https://github.com/yohimik/dispat/commit/2c71b520b46552e8df1bb7ccaf8ab52b7593c483)) (by yohimik)

- keep progress heading clear ([45a1fd6](https://github.com/yohimik/dispat/commit/45a1fd680de782869f52ed4441ba163ab2296076)) (by yohimik)

- fit silent mobile demos ([f770220](https://github.com/yohimik/dispat/commit/f770220f5746ce3322c93f9de37bee1ed1f12568)) (by yohimik)

- keep terminal rows intact ([7d3e018](https://github.com/yohimik/dispat/commit/7d3e018df77fe88c1685953267877f6cfbdc0578)) (by yohimik)

- clarify live demo stories ([984e461](https://github.com/yohimik/dispat/commit/984e461d5b5b2ec344ff018e6b22a65eadd79b49)) (by yohimik)

- unify sidebar categories ([0e5482f](https://github.com/yohimik/dispat/commit/0e5482f3ff87afd9d76a8d06a5f9c898246fa556)) (by yohimik)

### Authors

- yohimik


## packages/docs/v1.8.1 (2026-09-05)

### Fixes

- preserve versioned evidence ([c89a2b0](https://github.com/yohimik/dispat/commit/c89a2b0b484cbb38fcf29c583b613681a98cb6ba)) (by yohimik)

- stabilize landing demos ([d45d331](https://github.com/yohimik/dispat/commit/d45d331257cf85931765901a99d483a7cffa1d26)) (by yohimik)

### Authors

- yohimik


## packages/docs/v1.8.0 (2026-09-05)

### Features

- refresh guides and live demos ([3419488](https://github.com/yohimik/dispat/commit/3419488c1ef374518858386ee94e8060bd354b83)) (by yohimik)

### Fixes

- include live demo build inputs ([1efa6a4](https://github.com/yohimik/dispat/commit/1efa6a404dab623178cfab0668d2a4a48a67ef4b)) (by yohimik)

### Dependencies

- [dispat](https://github.com/yohimik/dispat/releases/tag/services/dispat/v1.8.0): 1.7.2 -> 1.8.0
- [dispat-alpine](https://github.com/yohimik/dispat/releases/tag/docker/dispat-alpine/v1.8.0): 1.7.2 -> 1.8.0

### Authors

- yohimik


## packages/docs/v1.7.3 (2026-09-04)

### Fixes

- the description names the saga polyglot release tool ([d9d906b](https://github.com/yohimik/dispat/commit/d9d906be9b1d3941f1d3555014ca9bf01a7cfb86)) (by yohimik)
  The one-line description is the repository's everywhere it is said: the
  README, the site's tagline, title and metadata, the web manifest, the
  announcement card and its captions, and the CLI's README. The landing
  page names the saga pattern as the run's core and the distributed-systems
  patterns beside it (forward recovery, the tag store as ledger, idempotent
  reruns with exactly-once delivery, compare-and-swap mutual exclusion,
  topological ordering), says which repository shapes it releases, and
  carries the keywords and structured data for monorepo and polyrepo
  searches. The comparison page's experiments section describes the
  harness as it runs now.

  Beside it: the tools module is covered to 98.8% (the verbs, the runners
  and the error arms, behind small writer seams), the twelve experiment
  cells are enumerated once by a `cells` script that both the release
  sweep and the workflow matrix read, and the docs build guard also
  requires every frozen version's sidebar to list the measured pages.

### Authors

- yohimik


## packages/docs/v1.7.2 (2026-09-03)

### Dependencies

- [dispat](https://github.com/yohimik/dispat/releases/tag/services/dispat/v1.7.2): 1.7.1 -> 1.7.2
- [dispat-alpine](https://github.com/yohimik/dispat/releases/tag/docker/dispat-alpine/v1.7.2): 1.7.1 -> 1.7.2


## packages/docs/v1.7.1 (2026-09-02)

### Dependencies

- [dispat](https://github.com/yohimik/dispat/releases/tag/services/dispat/v1.7.1): 1.7.0 -> 1.7.1


## packages/docs/v1.7.0 (2026-09-02)

### Features

- install the conventional asset ([18460f9](https://github.com/yohimik/dispat/commit/18460f9e575d2d5ef61bbee34621fb29fde02470)) (by yohimik, Claude Fable 5)
  A release carrying more than one file was refused unless --asset named
  one, which made the flag mandatory for almost every real repository,
  dispat's own included: its releases carry six binaries and a checksum
  file.

  Without --asset dispat now looks for the name most projects publish
  under, the repository's own name and the platform, with the extension
  selfupdate.AssetName appends on Windows. The name is matched exactly and
  never as a glob, so a bare invocation installs what the release decided
  rather than whichever near-miss sorted first, and a release that follows
  no convention is refused as before, with the name that was tried added
  to the listing so the reader knows what to answer.

  The single-asset shortcut and every explicit --asset path are unchanged.

### Fixes

- finish a conflicted release ([5b8da48](https://github.com/yohimik/dispat/commit/5b8da4826aa2eb6c4a24d89e4bd328b9b24751f3)) (by yohimik, Claude Fable 5)
  A recovery whose merge conflicted aborted and failed the run, which
  leaves a release that has already published with its commit and tags
  nowhere but the local clone. It completes instead.

  This release's side wins every conflicting file, because that is the
  tree the tag names and taking the other side would publish content the
  release never saw; everything the arriving commits changed that did not
  conflict is in the merge as it is on the clean path. Their side is
  pushed to a branch of its own, release-conflicts/ followed by what the
  leg released and a UTC timestamp, plain and never forced, so the work is
  kept rather than dropped. Both records name the conflicting files and
  that branch: the GitHub body through a note block, the changelog through
  the merge commit, since the release commit is tagged and must not be
  amended. W243 says the same thing in the log, and the run exits 0.

  The tag invariant is untouched, and the republish guard and the bounded
  retry cover this path's pushes too. E224 is now only for the recovery
  machinery failing: the quarantine branch refused, the settled merge
  uncommittable, or a merge that stopped for something other than content.

  The e2e walk gains the two cycles this is about, and the key-features
  walk keeps the private install; both are what the release build runs
  against the bytes it exports.

- keep a released tag from moving ([96cdb2b](https://github.com/yohimik/dispat/commit/96cdb2b720309d6ca8173252e0fa5501e1b87707)) (by yohimik, Claude Fable 5)
  Four ways the mid-release recovery was wrong.

  It re-pushed with commit.force, which defaults on, so a checkout stale
  enough to have re-planned an already published version would force-move
  that published tag: the push it recovers from never reached a tag ref,
  so nothing stood between the two. The remote's tags are now read first
  and the run stops, naming the tag. Aliases stay movable, because moving
  them is what every release does.

  It fired on "[rejected]" alone, and the simultaneous-push race prints
  "[remote rejected]", so the phrase it was meant to recognise was
  unreachable. The gate now matches both, and every git invocation asks
  for the C locale so a translated checkout cannot defeat the match. The
  sentinel also stopped leading the message: git's own words are what a
  reader of a failed release needs first.

  The merge was refused outright by a repository configured merge.ff=only,
  and the abort that followed failed too. It is now made with --no-ff,
  which also pins the first-parent shape the recovery documents.

  It gave up if a commit landed between its own pull and its own push,
  which is the very surprise it exists to absorb. It now goes round up to
  three times, capturing the release commit once so the tag it names never
  moves whichever round lands.

- record the release commit ([d3299f9](https://github.com/yohimik/dispat/commit/d3299f9673ba568a68fcf7f857507a43a46a70b1)) (by yohimik, Claude Fable 5)
  The GitHub release is created after the push, and after a mid-release
  recovery HEAD is the merge by then, so the "commit" line in its body and
  its target_commitish named the merge rather than the release the record
  is about.

  The recovery already reads the release commit before merging, since the
  merge message names it. It now hands that back, and the finalize phase
  prefers it over HEAD when stamping the releasers. A run with no recovery
  sets nothing and reads HEAD exactly as before.

- read past a package's own alias ([4edbab5](https://github.com/yohimik/dispat/commit/4edbab5dca43d159b45fe05d297c1c7cb55c0804)) (by yohimik, Claude Fable 5)
  A single-package repository releasing as "v1.4.2" could not declare the
  "v1" a GitHub composite is consumed through: the load-time check refused
  any alias whose name matched a package's tagFormat, and "v1" matches
  "v{version}" on the prefix alone. The refusal existed because the
  baseline reader would take the alias for the newest release tag and read
  no version out of it, leaving the package looking unreleased.

  Both halves now ask what a name can be read back as rather than what it
  looks like. The check refuses an alias only when it parses as a release
  tag, so "v1.4.2" beside "v{version}" stays refused and "v1" becomes
  legal. The tag listing drops a name that carries no version when one of
  the package's own alias formats could have written it, which is precise
  enough to leave a mistyped release tag like "v1.0.0.0" in place, where
  the initials fallback still measures the window from it.

  Recognising an alias is a matcher rather than a prefix test, so {major},
  {minor} and {patch} capture one number and stop at a separator. Both
  readers of a baseline go through the same filter: the planner and the
  compute command's manifest baselines.

### Dependencies

- [dispat](https://github.com/yohimik/dispat/releases/tag/services/dispat/v1.7.0): 1.6.0 -> 1.7.0

### Authors

- yohimik
- Claude Fable 5


## packages/docs/v1.6.0 (2026-08-31)

### Dependencies

- [dispat](https://github.com/yohimik/dispat/releases/tag/services/dispat/v1.6.0): 1.5.0 -> 1.6.0


## packages/docs/v1.5.0 (2026-08-31)

No changes.


## packages/docs/v1.4.0 (2026-08-30)

### Features

- the download command has a page
The command page covers naming a repository, choosing the file and the
destination, the pipe for an asset that is not a binary, the idempotence
gate, the tag prefix, and the rollback. The CI guide gains the section
it belongs in, since a runner that already has dispat needs no second
downloader, and the self-update guide points at the other half of the
same engine. Mirrored into version-1.3, which is what the site serves.

The roadmap entry that described this as future work now describes what
is left of it: the declarative half, where a configuration lists the
tools a stage needs.

### Fixes

- the download variables reach the canonical listings
DISPAT_BIN_DIR joins the switches dotenv.md names as the variables
dispat reads for itself, and the environment reference points at the
two variables the pipe hands its command, so the page that promises
every DISPAT_* variable keeps that promise.

### Dependencies

- dispat: 1.3.1 -> 1.4.0

## packages/docs/v1.3.2 (2026-08-28)

### Dependencies

- dispat: 1.3.0 -> 1.3.1

## packages/docs/v1.3.1 (2026-08-28)

### Fixes

- the footer columns wrap through a grid
The flex row either held every column or crushed them, and wrapping under
space-between flung a partial row to the edges. The auto-fit grid degrades
from three columns through two to a single stack, every column keeping the
same measure as its neighbours at every step; infima's row margins and col
padding are neutralised since the grid gap owns the spacing now.
- the projects grid is sized by its content
The section reused the two-column features grid, so the single project the
README lists today rendered beside an empty half. auto-fit lets one card
take the row and packs future additions into columns on their own.

## packages/docs/v1.3.0 (2026-08-28)

### Dependencies

- dispat: 1.2.0 -> 1.3.0

## packages/docs/v1.2.0 (2026-08-27)

### Dependencies

- dispat: 1.1.1 -> 1.2.0

## packages/docs/v1.1.10 (2026-08-26)

### Dependencies

- dispat: 1.1.0 -> 1.1.1

## packages/docs/v1.1.9 (2026-08-26)

### Fixes

- logo transparency


## packages/docs/v1.1.8 (2026-08-26)

### Fixes

- color scheme


## packages/docs/v1.1.7 (2026-08-26)

### Fixes

- logo colors


## packages/docs/v1.1.6 (2026-08-26)

### Fixes

- match logo


## packages/docs/v1.1.5 (2026-08-25)

### Dependencies

- infra: 0.0.0 -> 0.0.1

## packages/docs/v1.1.4 (2026-08-25)

### Fixes

- minified styles


## packages/docs/v1.1.3 (2026-08-25)

### Fixes

- slides added on landing page


## packages/docs/v1.1.2 (2026-08-24)

### Fixes

- embed the demo animations


## packages/docs/v1.1.1 (2026-08-24)

### Fixes

- improve readability


## packages/docs/v1.1.0 (2026-08-20)

### Dependencies

- dispat: 1.0.2 -> 1.1.0

## packages/docs/v1.0.12 (2026-08-20)

### Fixes

- a control repository for many repositories
The pattern that gets the graph back without merging anyone's code: one
small repository holding every configuration, linking the product
repositories in as git submodules. A pointer bump is a commit touching one
path, so dispat reads it as a change to that package and the fleet plans,
orders and propagates as a single monorepo, while nobody in the linked
repositories touches a dispat file.

Both layouts get a section: a wrapper folder, where the changelog lands
somewhere the release commit can stage it, and the submodule mounted as the
package, where it does not. Both build models too, building in the control
repository and triggering each repository's own pipeline. Plus the sync
script that turns upstream subjects into --- separated units, the parser
settings that implies, the cross-repository edges compute derives on its
own, and what to watch for.

Every transcript is a real run against throwaway fleets built for it.

monorepo.md and the FAQ point at it, since the first says outright that
dispat cannot span repositories.

## packages/docs/v1.0.11 (2026-08-19)

### Fixes

- wire game engines


### Dependencies

- dispat: 1.0.1 -> 1.0.2

## packages/docs/v1.0.10 (2026-08-19)

### Dependencies

- dispat: 1.0.0 -> 1.0.1

## packages/docs/v1.0.9 (2026-08-18)

### Fixes

- one repository or many, and a tidier examples index
A page for the decision itself: what a monorepo and a repository each
actually cost, which of those costs dispat removes (ordering, per-package
cadence, CI that scales with the diff), where it does not help (there is no
graph across repositories), and how to move either direction without losing
published versions.

The examples index loses its duplicated "start here" table for a two-line
reading path, and the five repository-shaping pages become a group of their
own in both sidebars instead of sitting loose after the ecosystems.

## packages/docs/v1.0.8 (2026-08-18)

### Fixes

- what dispat buys a game project as it grows
The game example now opens with the case for adopting it: one version
number across the engine file, the tag, the Steam build and the itch
upload; changelogs and announcements written from the commits; a patch
that takes the same path as a release. Ends on the reason a single game
binary is worth setting up this way, which is that it does not stay a
single binary once a landing page, an SDK and a server arrive.

The examples index carries the same point in its lead.

## packages/docs/v1.0.7 (2026-08-18)

### Fixes

- examples per ecosystem, game stores and other CI providers
Seventeen worked examples: one per manifest ecosystem, so all 23 formats
the scanner and writer cover are demonstrated somewhere; a game repository
that starts as one package and grows; Steam and itch.io publishing; and the
release job on GitLab CI, CircleCI, Jenkins, Buildkite and Azure Pipelines.

Corrects three pages that still said manifest rewriting only covered
package.json, go.mod and requirements*.txt, and three writer transcripts
that printed "replace" and "patch" where the CLI prints "link".

## packages/docs/v1.0.6 (2026-08-17)

### Fixes

- why one more monorepo tool update


## packages/docs/v1.0.5 (2026-08-17)

### Fixes

- title updated


## packages/docs/v1.0.4 (2026-08-16)

### Fixes

- the faq reaches the 1.0 line


## packages/docs/v1.0.3 (2026-08-16)

### Fixes

- the questions the first users actually ask


## packages/docs/v1.0.2 (2026-08-16)

### Fixes

- projects using dispat carry the first stable run's numbers


## packages/docs/v1.0.1 (2026-08-16)

### Fixes

- announce the stable release


## packages/docs/v1.0.0 (2026-08-16)

### Breaking Changes

- commit to the 1.0 interfaces

- restore the 1.0 rc train


### Features

- the documentation covers the stable surface

- finalize the workspace for 1.0.0

- anchor the palette on dark pine

- the documentation site, versioned and deployed by dispat

- read projects using dispat onto the landing page

- exit 3 when --require-release finds nothing to release

- split the docs and api sidebars

- muted pine palette

- build the landing page from both READMEs

- read the README and the test report into the site

- array of scripts, require release

- forward arguments after -- to run and exec scripts

- trace and debug logging for git, config and the plan

- show the tests and coverage badges on the landing page

- list ccme beside the manifest libraries instead of in the footer

- document the standalone manifest scanner and writer on the landing page


### Fixes

- exercise a caret that reaches nobody

- exercise a partial-mode member release mid-train

- exercise the release pipeline across every package

- nested dependencies update againx2

- nested dependencies update again

- nested dependencies update

- dependencies update

- the diagnostic registry names the step alignment codes

- the tile color joins the pine palette

- wrap long inline code instead of overflowing a phone

- span the last feature card only on an odd count

- let the footer columns wrap on mobile

- pin the pwa reload popup theme alias

- keep the install blocks inside a phone's viewport

- keep the landing page inside a phone's viewport

- sync manifests and changelogs for every updated provider

- 1.0.0 release blockers

- centre the landing page text

- give the commit parser its own row in the libraries grid

- close the prerelease-train code fence in concepts

- serve a landing page at the site root instead of a 404


### Dependencies

- dispat: 1.0.0-rc.19 -> 1.0.0

## packages/docs/v1.0.0-rc.19 (2026-08-16)

### Features

- the documentation covers the stable surface


### Dependencies

- dispat: 1.0.0-rc.18 -> 1.0.0-rc.19

## packages/docs/v1.0.0-rc.18 (2026-08-16)

### Fixes

- exercise a caret that reaches nobody


### Dependencies

- dispat: 1.0.0-rc.17 -> 1.0.0-rc.18

## packages/docs/v1.0.0-rc.17 (2026-08-16)

### Dependencies

- dispat: 1.0.0-rc.16 -> 1.0.0-rc.17

## packages/docs/v1.0.0-rc.16 (2026-08-16)

### Dependencies

- dispat: 1.0.0-rc.15 -> 1.0.0-rc.16

## packages/docs/v1.0.0-rc.15 (2026-08-16)

### Dependencies

- dispat: 1.0.0-rc.14 -> 1.0.0-rc.15

## packages/docs/v1.0.0-rc.14 (2026-08-16)

### Fixes

- exercise a partial-mode member release mid-train


### Dependencies

- dispat: 1.0.0-rc.13 -> 1.0.0-rc.14

## packages/docs/v1.0.0-rc.13 (2026-08-16)

### Dependencies

- dispat: 1.0.0-rc.12 -> 1.0.0-rc.13

## packages/docs/v1.0.0-rc.12 (2026-08-16)

### Fixes

- exercise the release pipeline across every package


### Dependencies

- dispat: 1.0.0-rc.11 -> 1.0.0-rc.12

## packages/docs/v1.0.0-rc.11 (2026-08-16)

### Dependencies

- dispat: 1.0.0-rc.10 -> 1.0.0-rc.11

## packages/docs/v1.0.0-rc.10 (2026-08-16)

### Dependencies

- dispat: 1.0.0-rc.9 -> 1.0.0-rc.10

## packages/docs/v1.0.0-rc.9 (2026-08-16)

### Fixes

- nested dependencies update againx2


### Dependencies

- dispat: 1.0.0-rc.8 -> 1.0.0-rc.9

## packages/docs/v1.0.0-rc.8 (2026-08-16)

### Dependencies

- dispat: 1.0.0-rc.7 -> 1.0.0-rc.8

## packages/docs/v1.0.0-rc.7 (2026-08-16)

### Dependencies

- ccme: 1.0.0-rc.4 -> 1.0.0-rc.5
- dispat: 1.0.0-rc.6 -> 1.0.0-rc.7

## packages/docs/v1.0.0-rc.6 (2026-08-16)

### Fixes

- nested dependencies update again

- nested dependencies update


### Dependencies

- dispat: 1.0.0-rc.5 -> 1.0.0-rc.6

## packages/docs/v1.0.0-rc.5 (2026-08-16)

### Fixes

- dependencies update


### Dependencies

- dispat: 1.0.0-rc.4 -> 1.0.0-rc.5

## packages/docs/v1.0.0-rc.4 (2026-08-16)

### Breaking Changes

- commit to the 1.0 interfaces


### Features

- anchor the palette on dark pine

- the documentation site, versioned and deployed by dispat


### Fixes

- the diagnostic registry names the step alignment codes

- the tile color joins the pine palette


### Dependencies

- dispat: 1.0.0-rc.3 -> 1.0.0-rc.4
