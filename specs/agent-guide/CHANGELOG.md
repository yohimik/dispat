# Changelog

## specs/agent-guide/v1.11.0-rc.6 (2026-09-25)

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

### Authors

- yohimik
- Claude Opus 5.5


## specs/agent-guide/v1.11.0-rc.5 (2026-09-23)

### Fixes

- retain authenticated coordination ownership through cancellation races ([7dceee9](https://github.com/yohimik/dispat/commit/7dceee974ac2937c6d5d64e3cec5c76df0822ed7)) (by yohimik)

- preserve uncertain publication and bound worker state ([e92ca16](https://github.com/yohimik/dispat/commit/e92ca16f0cbbee47a18a45b41f085f217e3e45da)) (by yohimik)

- bound transports and fence worker state ownership ([792522e](https://github.com/yohimik/dispat/commit/792522e7a8ad723724dd53fbb967d492d2ceaad9)) (by yohimik)

- harden fleet release recovery and shared version policies ([dbf10c7](https://github.com/yohimik/dispat/commit/dbf10c73478d0bb9f298d4b1ec19cd6c8b177005)) (by yohimik)

### Authors

- yohimik


## specs/agent-guide/v1.11.0-rc.4 (2026-09-23)

### Fixes

- keep the guide's tests on the machine with release authority ([768f2ed](https://github.com/yohimik/dispat/commit/768f2ed0ca62c05c4c867a7050fca3de59df89ef)) (by yohimik, Claude Fable 5.1)
  The guide's test suite performs releases on throwaway repositories, and a
  worker refuses release initiation by design, so a sweep that placed the
  suite on a worker failed it with E226. Every stage of the package now runs
  on the orchestrator.

- say that a sweep uses the pool and takes no lock ([4f65527](https://github.com/yohimik/dispat/commit/4f65527990454eaafa27841fda0ea120eecf1462)) (by yohimik, Claude Opus 5.5)
  An agent reading a distributed repository is told that `--worker` adds
  links for one invocation, and that `dispat run` places its tasks on the
  same pool, carries the declared `runOutputs` back, and takes no release
  lock, so it neither waits for a release nor stops one.

- describe a distributed release ([bede09b](https://github.com/yohimik/dispat/commit/bede09bd29412bdb946b214bc3ce411bfbc2be9d)) (by yohimik, Claude Opus 5)
  What to check before a run that delegates work, how to read the per-task
  summary, what an unknown publication outcome means, and why a retained lock
  is an operator's decision.

- say how a group's sharing rule limits a directive's reach ([e1b83ab](https://github.com/yohimik/dispat/commit/e1b83ab6e4235ae18e5992c2fb20f05345385c84)) (by yohimik)
  With independent channels only the packages a directive names enter or leave
  a train, so an agent reviewing version-group effects has to name every
  package it intends to move and confirm the result before releasing.

### Authors

- yohimik
- Claude Fable 5.1


## specs/agent-guide/v1.11.0-rc.3 (2026-09-20)

No changes: a version bump to keep the versioning group on one major and minor version.


## specs/agent-guide/v1.11.0-rc.2 (2026-09-20)

No changes: a version bump to keep the versioning group on one major and minor version.


## specs/agent-guide/v1.11.0-rc.1 (2026-09-20)

### Fixes

- document safe topology computation and graph repair ([35664e1](https://github.com/yohimik/dispat/commit/35664e1685e703c347233421f9faefdba8b1622a)) (by yohimik)

- clarify release intent and safe fleet recovery ([2f591b0](https://github.com/yohimik/dispat/commit/2f591b0139339d13f20c4718056d14be6e0dea98)) (by yohimik)

### Authors

- yohimik


## specs/agent-guide/v1.11.0-rc.0 (2026-09-15)

### Fixes

- clarify polyrepository configuration and release behavior ([88cf2ca](https://github.com/yohimik/dispat/commit/88cf2cae8b0355be0f5e185fc450f0c1949e4502)) (by yohimik, Codex (gpt-5.6-sol))
  Document owner-local folder inputs and parser diagnostics, direct channel
  precedence, and cancellation of hooks during durable release recording.

- document composed repository releases ([7e38ae1](https://github.com/yohimik/dispat/commit/7e38ae131734159acb7011a835219f765c46a983)) (by yohimik, Codex (gpt-5.6-sol))
  Describe central and imported configuration, external edges, source ownership,
  locks, exact pin handoff, diagnostics and partial publication recovery.

### Authors

- yohimik
- Codex (gpt-5.6-sol)


## specs/agent-guide/v1.10.3 (2026-09-11)

### Fixes

- define release intent and publication boundaries ([f56f86e](https://github.com/yohimik/dispat/commit/f56f86e056e6cb5895b33618d3a1e37d29700cba)) (by yohimik)
  Confirm the operator-approved package scope and inspect dispat status before
  releasing. Finish validation before publication and proceed directly to records.

### Authors

- yohimik


## specs/agent-guide/v1.10.2 (2026-09-10)

### Fixes

- clarify independent documentation patch versions ([9a48f92](https://github.com/yohimik/dispat/commit/9a48f929deb4ad0cf1601443822d7241c302dad9)) (by yohimik)
  Use the latest compatible documentation and guide patches without matching
  the CLI patch. Keep explanations of existing behavior on the current
  major/minor line and verify document package intent before releasing.

### Authors

- yohimik


## specs/agent-guide/v1.10.1 (2026-09-10)

### Fixes

- correct agent release interruption and integration guidance ([9411b5c](https://github.com/yohimik/dispat/commit/9411b5c4d6526c5d7423e0a658813925da7d679b)) (by yohimik)
  Interrupt invalid releases immediately when requirements change, inspect
  partial publication, and validate corrections before restarting.

  Summarize the important cross-project integration findings in six rows
  with references to the detailed ecosystem guidance.

- strengthen release integration checks ([fb422ca](https://github.com/yohimik/dispat/commit/fb422ca45b15247e03f32dec71dad27e33921ca6)) (by yohimik)
  Document native wrapper validation, minimum engine compatibility, host and
  plugin checks, immutable download inputs, and downstream patch evidence.
  Add a short integration checklist with references to the agent guide.

- guide dependent release propagation choices ([85294cc](https://github.com/yohimik/dispat/commit/85294cc4eb18232878b4018ba78fdb23545c6e76)) (by yohimik)

- clarify agent commit and release workflow guidance ([45fd18f](https://github.com/yohimik/dispat/commit/45fd18fea5d0dbdae5c922a4cb5117d720973c65)) (by yohimik)

### Authors

- yohimik


## specs/agent-guide/v1.10.0 (2026-09-09)

### Features

- diagnose messages ([1bb3b39](https://github.com/yohimik/dispat/commit/1bb3b397e4749461c1102869a56b1ceb600636ae)) (by yohimik)

### Authors

- yohimik


## specs/agent-guide/v1.9.0 (2026-09-09)

No changes: a version bump to keep the versioning group on one major and minor version.


## specs/agent-guide/v1.8.1 (2026-09-07)

### Fixes

- explain platform shell configs ([a767e93](https://github.com/yohimik/dispat/commit/a767e93205c45689b2f9fe6e6447f48ab6d7ba47)) (by yohimik)

### Authors

- yohimik


## specs/agent-guide/v1.8.0 (2026-09-06)

No changes: a version bump to keep the versioning group on one major and minor version.


## specs/agent-guide/v1.0.0 (2026-09-06)

No changes: a version set by Release-As.
