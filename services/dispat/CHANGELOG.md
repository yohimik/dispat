# Changelog

## services/dispat/v1.11.0-rc.6 (2026-09-25)

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

- name the linked peer whose beforeAll hook refused a release ([c79f732](https://github.com/yohimik/dispat/commit/c79f7326f1e2b5bea199a57f80e2b27f6426860d)) (by yohimik, Claude Opus 5.5)
  In a linked fleet every peer's run.beforeAll gates the release. When the
  entry's hook failed the run logged "beforeAll hook failed, refusing to
  release", but when a peer's hook failed the release exited 1 with no
  error line at all, leaving nothing in the log to say why. A peer's
  failure is now logged the same way, as an error that names the
  repository whose hook failed. Nothing is built or tagged, as before.

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

- refuse a changed-files listing that names a commit not asked about ([bd81c7a](https://github.com/yohimik/dispat/commit/bd81c7a2819ba0c847ced5e83c6eb7aac67fa15c)) (by yohimik, Claude Opus 5.5)
  A commit that names no package takes its packages from the files it
  changed, and dispat asks Git for those files of every such commit in one
  listing. A listing was accepted whenever it held as many records as
  commits were asked about, so a record naming some other commit left an
  asked commit with no files: it derived no package, W131 called it inert,
  and its fix never released. The listing is now refused as malformed, and
  status and release stop before planning, when a record names a commit
  that was not asked about, when a commit is listed twice, or when an asked
  commit has no record; the error names the commit.

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

- name an ambiguous release-lock destination E336 in one repository ([043ca59](https://github.com/yohimik/dispat/commit/043ca597f1b6f64fd9c427258e11016549a84fec)) (by yohimik, Claude Opus 5.5)
  A repository whose release remote pushes to more than one URL cannot hold a
  release lock that coordinates anything, and dispat refuses the release before
  any package work. A fleet named that refusal E336, as the release-lock page
  promises for any lock that cannot be taken, but a single repository logged it
  with no code. The single-repository refusal now carries E336 on its log line
  and in its error, like the fleet's, and the error still says that the lock
  needs exactly one push destination.

- count both paths of a moved file and every name unquoted in derived scopes ([f55b9f3](https://github.com/yohimik/dispat/commit/f55b9f3bba66b6962ba43f25b54a515042c3b5f4)) (by yohimik, Claude Opus 5.5)
  A unit with no scope-set addresses the packages owning the paths its commit
  changed (CCME §6.2). The list came from `git log --name-only` under the
  operator's rename detection, which names a moved file by its new path alone,
  so a file moved from one package to another released only the package it
  reached, against §6.2 and its vector 29. Git also quotes a path holding a
  letter outside ASCII or a tab, so such a file belonged to no package and a
  scopeless unit touching only it was inert (W131). The list is now read with
  rename detection off, which gives both paths of a move, and separated by NUL
  bytes, which gives every path as the repository records it; the root
  commit's paths are asked for outright rather than left to `log.showRoot`.
  The plan of a history without such commits is unchanged.

- write each release tag without first listing the package's tags ([3583297](https://github.com/yohimik/dispat/commit/3583297553c92780006d6df3b687232f770d578d)) (by yohimik, Claude Opus 5.5)
  Writing a release tag listed every tag of the package that HEAD reaches,
  sorted, with a reachability walk per tag, and only then wrote the tag: two
  git processes per releasing package, and a listing that grows with every
  release the package has made. The write is create-only, so it is itself the
  existence check. The tag is now written first, and only a refused write
  looks the name up, as the one exact ref HEAD reaches. The outcomes are the
  ones the listing gave: a tag at the release commit is skipped (W223), one
  HEAD reaches elsewhere is left alone (E221) with or without force, one HEAD
  cannot reach is rewritten under force and is the write's failure without,
  and a write refused with no tag of that name anywhere is reported as it is
  and not retried.

  The integration test of a failing post-publication tag listing now proves
  the listing is not made at all: the executor reads no tag inventory before
  its write.

  BenchmarkCreateReleaseTags (64 packages with 30 releases each, one new tag
  per package per iteration), Apple M5 Pro, darwin/arm64, go1.26.5,
  -benchtime 3x, through `testreport bench`, run back to back; columns
  e73d72ce -> this commit:

      gitcalls/tag   ns/tag            B/op              allocs/op
      2 -> 1         58.7 ms -> 8.2 ms  5.04 MB -> 1.37 MB  33,750 -> 8,457

- index the control history's parents once per plan ([e73d72c](https://github.com/yohimik/dispat/commit/e73d72ceaca9bfb086a46eec3215643938637b29)) (by yohimik, Claude Opus 5.5)
  A composed workspace reads the control repository's pending windows from
  the one control inventory, and every distinct control boundary rebuilt a
  map of the whole inventory's parents and walked the boundary's ancestors
  into another map. The parent graph is now indexed by position once, when
  the inventory is read, and each window's excluded commits are a bitset
  walked over it. Windows and plans are unchanged.

  BenchmarkControlWindows (a 20,000-commit control history, 64 distinct
  boundaries), Apple M5 Pro, darwin/arm64, go1.26.5, -benchtime 3x, through
  `testreport bench`, run back to back; columns 8b421058 -> this commit:

      ns/op               B/op                allocs/op
      143.0 ms -> 11.4 ms  368.8 MB -> 12.4 MB  73,536 -> 80,274

- read the owed windows in one history walk ([8b42105](https://github.com/yohimik/dispat/commit/8b421058f4fd67edabaca0c7c2b17dfa60c2c08f)) (by yohimik, Claude Opus 5.5)
  The owed windows of §13.3 were read one `git log <tag>..HEAD` per
  boundary, each a separate process listing mostly the same commits, in a
  single history and in each repository of a composed workspace. They are
  now read like the ordinary windows: one union walk over all of their
  boundaries, each window recovered from it by the marker pass of CCME
  §13.11 in the order git lists it. Once an owed window is the whole
  history no further one is read, since it holds them all. And in a single
  history, a consumer on a prerelease train no longer makes the planner load
  the whole commit graph to learn that a provider release outside the pending
  union is behind its baseline: the release is behind every boundary the
  union was read from, among them the consumer's own stable release, which
  its baseline reaches, and the marker index says so. Plans are unchanged.

  BenchmarkComputeRealHistory, Apple M5 Pro, darwin/arm64, go1.26.5,
  -benchtime 3x, through `testreport bench`, run back to back; columns
  43c8cbc9 -> this commit:

      shape/commits    gitcalls/op  gitOutputBytes/op     peakHeap_MiB   ns/op
      bounded/10000    26 -> 9      5,825,134 -> 567,230      18.5 -> 6.4    428 ms -> 144 ms
      bounded/50000    30 -> 9      36,948,138 -> 2,916,496   125.4 -> 35.2  1547 ms -> 439 ms
      composed/10000   27 -> 11     5,825,175 -> 1,399,284    14.9 -> 14.3   447 ms -> 223 ms
      composed/50000   31 -> 11     36,948,179 -> 7,076,766   81.3 -> 82.3   1620 ms -> 742 ms

- bound the single-source walk cache and list only distinct owed pairs ([f86878d](https://github.com/yohimik/dispat/commit/f86878d91a573b1ae70cf7e6d313801f2a222954)) (by yohimik, Claude Opus 5.5)
  CCME §13.11 bounds what the planner's graph caches retain, and only the
  cache of multi-source walks honoured it: the unbounded walk from every
  single package was kept whatever its size, which over a dependency chain
  is a quadratic number of targets. Both tiers are now charged to the one
  budget; a walk past it is still answered in full and not kept, and once
  the budget has turned one away a bounded walk is taken to its own depth
  rather than read as a prefix of an unbounded one. The owed windows of
  §13.3 listed every (provider, consumer) pair before keeping the first of
  each (provider, baseline); they now keep only those while listing. Plans
  are unchanged.

  BenchmarkComputeChainTopology, Apple M5 Pro, darwin/arm64, go1.26.5,
  -benchtime 3x, through `testreport bench`, run back to back; columns
  c592e39e -> this commit:

      packages   peakHeap_MiB    B/op                ns/op
      1024       117.6 -> 47.8   342 MB -> 205 MB    127 ms -> 95 ms
      4096       1742 -> 393.5   5458 MB -> 3439 MB  2087 ms -> 1579 ms

- attribute authors only for releases that have an entry to render ([c592e39](https://github.com/yohimik/dispat/commit/c592e39e225fd4f4bdb72a826d59c12a46d8e550)) (by yohimik, Claude Opus 5.5)
  Planning collected the window authors of every package, releasing or not,
  and each distinct window scanned the whole pending union to find its own
  commits. The attribution is read only by the release records, the preview
  and a step's aligned plan, and each of those renders a changed package that
  versions at all. The authors are now collected for those packages alone,
  after the versions and fixed-group rides are settled, and a single
  history's window is read from its own set of commits in history order
  rather than by testing every commit of the union. A held package is still
  changed and keeps its authors, because its preview shows its entry. The
  releases' authors are unchanged; a package with nothing to release, and a
  versioning none package, carry none.

  On this repository the plan is identical; the debug log loses the five
  "release authors collected" lines of the four none packages and the one
  unchanged package, and the remaining lines follow the fixed-group lines.

  BenchmarkComputeRealHistory, Apple M5 Pro, darwin/arm64, go1.26.5,
  -benchtime 3x, through `testreport bench`; columns 16966fea -> this commit:

      shape/commits   AuthorScans/op        ns/op
      whole/10000     630000 -> 37783       189 ms -> 195 ms
      whole/50000     3250000 -> 192366     919 ms -> 883 ms
      bounded/10000   133308 -> 35929       426 ms -> 455 ms
      bounded/50000   741056 -> 214965      1569 ms -> 2171 ms

  The timings moved with other work on the machine (load average near 11):
  run back to back in one session, bounded/10000 measured 601 and 636 ms
  before and 571 and 577 ms after. The scan counts are exact.

- stream the history read instead of buffering git's whole output ([16966fe](https://github.com/yohimik/dispat/commit/16966feab55ef6279937dd5fbd331ae70118d2ad)) (by yohimik, Claude Opus 5.5)
  The planner's history read collected git's whole output in one buffer,
  copied it into a string, split it into records and kept sub-slices of that
  string, so every commit a plan kept held the entire output alive with it
  for the whole run, and a read of several overlapping windows kept several.
  The read now parses git's output as it arrives, one record at a time in a
  buffer bounded by the largest record, and every field a commit keeps is a
  copy of its own. The changed files of a commit are dropped once its derived
  packages are known, since nothing after scope resolution reads them. Plans
  are unchanged.

  BenchmarkComputeRealHistory and BenchmarkParseCommits, Apple M5 Pro,
  darwin/arm64, go1.26.5, -benchtime 3x, through `testreport bench`; columns
  6e9766ba -> this commit (ParseCommits before: 62b3e8f4, whose parser
  6e9766ba left as it was):

      benchmark        B/op               peakHeap_MiB    retained_MiB   ns/op
      whole/10000      52.4 MB -> 49.2 MB   30.3 -> 30.5    11.0 -> 11.0   195 ms -> 189 ms
      whole/50000      252 MB -> 241 MB    149 -> 139     52.9 -> 53.2   895 ms -> 919 ms
      bounded/10000    53.6 MB -> 42.7 MB   19.2 -> 18.3    2.76 -> 2.59   456 ms -> 426 ms
      bounded/50000    351 MB -> 279 MB    126 -> 120     13.5 -> 12.7   1748 ms -> 1569 ms
      ParseCommits     39.7 MB -> 54.0 MB   n/a             20.3 -> 20.1   15.5 ms -> 15.4 ms

  The history now carries no paths, so a single read keeps nearly all of what
  it reads and retention moves little; the gain is the buffer and the record
  list no longer held at once, and the overlapping owed-window reads of the
  bounded shape. The in-memory parse, which only tests use, allocates more
  because it now copies what it keeps.

- read changed files only for the commits whose scope derives from them ([6e9766b](https://github.com/yohimik/dispat/commit/6e9766bac20a3d3cc11e40d207ba5d8887cab7d3)) (by yohimik, Claude Opus 5.5)
  Planning read every pending commit with the paths it changed, which makes
  git diff each of those commits against its parent. Only a unit that leaves
  its scope to the files (no scope-set, exclusions alone, or ".", in the
  header or a Propagate-Scope footer) ever looks at them. The history is now
  read without paths, and the paths of the commits that need them are read
  afterwards in one git process. That read uses git log over exactly those
  commits, so the lists are the ones the history read gave: a merge's changes
  against its first parent, and a renamed file under its new name alone. A
  package without a stable tag, such as every versioning none package, still
  makes the pending union the whole history, and plans are unchanged.

  On this repository `dispat status` diffs 362 commits instead of 1,451, and
  ten runs take 1.03 to 1.07 s instead of 1.54 to 1.68 s. Its JSON output at
  the default, debug and trace levels is identical apart from one new debug
  line naming the commits whose files were read.

  BenchmarkComputeRealHistory, Apple M5 Pro, darwin/arm64, go1.26.5,
  -benchtime 3x, through `testreport bench`; columns 62b3e8f4 -> this commit:

      shape/commits   commitsDiffed/op   ns/op                 gitcalls/op
      whole/10000     10000 -> 3216      193.9 ms -> 195.3 ms  3 -> 4
      whole/50000     50000 -> 16504     924.1 ms -> 894.5 ms  3 -> 4
      bounded/10000   33318 -> 677       688.6 ms -> 456.3 ms  25 -> 26
      bounded/50000   218841 -> 3803     3181 ms -> 1748 ms    29 -> 30

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

- treat author spellings that differ only by case as one author ([30ff0e9](https://github.com/yohimik/dispat/commit/30ff0e9b08d59ede570c49ea4df65be845a1e049)) (by yohimik, Claude Opus 5.5)
  Release records deduplicate and filter authors with the same Unicode fold
  every other name comparison in dispat uses, rather than with lowercasing.
  Two spellings of one identity are now one author exactly when they are equal
  ignoring case: a name or address with a final sigma, a micro sign or a long s
  no longer splits into two authors, and a dotted capital I is no longer merged
  with a plain i. An include or exclude pattern such as `*ς` reaches a name that
  ends in a capital sigma. The changelog section deduplicates through the
  planner's own function, so the two cannot disagree about who one person is.

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

- authenticate the image builds' release lookup and wait out a rate limit ([ef5a0a3](https://github.com/yohimik/dispat/commit/ef5a0a329cf2d0d96fbfda70e4a525502c6acf4a)) (by yohimik, Claude Opus 5.5)
  The four images' fetch stages ran install.sh anonymously, so every image
  build shared the runner address's small hourly quota, and install.sh read
  any refused lookup, a spent rate limit included, as "no release for TAG".
  The fetch stages now install curl and mount the build's GITHUB_TOKEN as the
  github_token secret, which the compose files declare from the environment and
  the docker space exports on every compose call (empty means anonymous, as
  before). install.sh and install.ps1 wait out a rate-limited 403 or 429 for up
  to three attempts, as retry-after or x-ratelimit-reset asks and at most a
  minute each, call a release missing only on a 404, and otherwise report the
  HTTP status with GitHub's own message.

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

- keep credentials out of worker refusals and redacted remotes ([2c6b18f](https://github.com/yohimik/dispat/commit/2c6b18ff0eb1b51d73cd72485a9790532054eaa2)) (by yohimik, Claude Opus 5.5)
  A run whose worker link reaches the release remote no longer writes that
  remote's name as it is configured. `commit.remote` may be a URL rather than
  a remote's name, and the refusal of a push URL no mailbox may be, like the
  debug line saying where a link reaches, printed it raw, token included.

  Redaction itself no longer lets a malformed URL through. A value with a
  scheme that Go cannot parse, such as a password holding a stray `%` or `#`,
  was returned unchanged; it now has everything up to its last `@` cut, and its
  query and fragment, as an endpoint already does.

  The coordinator's test-only ownership setter is gone; its tests use the gate
  a release hands the coordinator.

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

- accept a live pin equal to the control pin ([9b814d5](https://github.com/yohimik/dispat/commit/9b814d537e2d8cb5cb054414502f98bae2a250be)) (by yohimik, Claude Opus 5.5)
  A nested command in a composed workspace validates each source checkout
  against control HEAD and the live pins of the release around it. A checkout
  sitting exactly at the revision control pins was accepted only when two reads
  of the live pin agreed, so while the enclosing release kept publishing
  revisions the command read ten times and refused a correctly pinned checkout
  with E330. Such a checkout needs nothing from the run and is accepted on the
  first read again; a checkout past the control pin still needs a stable live
  pin that admits it.

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

- report every error the owed-consumer check meets ([7d0f45b](https://github.com/yohimik/dispat/commit/7d0f45bb05d90dfc556675ee19d3f219a82db6a2)) (by yohimik, Claude Opus 5.5)
  After a run publishes a provider, E201 checks that no consumer it still owes
  was left at or ahead of the provider's release. That check read any failure
  to resolve the provider's tag as a tag never written, and a failed ancestry
  comparison only as a warning, so a cancelled run or a Git error could skip it
  and exit 0 with a debt no later plan can find. Only a tag known to be absent
  is skipped now; every other failure is a critical naming the consumer and the
  provider.

  Before publication, a composed workspace compared the head a provider would be
  tagged at with the consumer's baseline by equality, while that baseline is a
  recorded pin or tuple which need not be behind the head. The check now asks
  whether the head is behind the baseline, as the check after publication does,
  so a provider tagged behind a consumer's pin is refused with E201 too, and a
  head that cannot be read or compared stops the release instead of admitting
  it.

- keep a fixed group's planned version across failed catch-ups ([f4aecdd](https://github.com/yohimik/dispat/commit/f4aecdd30e9935ec392f314df8737656b2c35160)) (by yohimik, Claude Opus 5.5)
  A shared-version group member that got ahead of a failed provider is owed that
  provider's release, and the group moves once to deliver it. When that
  catch-up then failed, or the member sat out a run that released the rest of
  the group, every later run counted the same debt again: the group moved to a
  new version each time, the members that had already published re-released
  with nothing new (W234), and the owed member landed one version later than it
  was planned at. The group's version already accounts for a debt its member
  still owes when the member is behind that version, so the member now catches
  up at the version it was planned at (G3) and nobody rides again. A member
  that holds the group's version itself still moves the group, since that
  version carries the commit but never the provider's release.

- catch up a prerelease consumer that overtook a provider's failed release ([19b47cc](https://github.com/yohimik/dispat/commit/19b47cc6657e6fcab71a0e61f22f76b8904bda3c)) (by yohimik, Claude Opus 5.5)
  A consumer that released a prerelease on a change of its own while its
  provider's publish failed kept the provider's old version for ever. Its train
  window still held the shared commit, and the planner read a commit its train
  carried as already delivered, so the consumer was never owed the provider's
  release: a provider-only run then published on the consumer's release commit
  without E201, and every later prerelease stayed on the old version with no
  W193. The train published the commit, not the provider's version, so such a
  consumer is now owed exactly as a stable one is: it releases its next
  prerelease after the provider, with the provider in its record, a
  provider-only run on its release commit is refused with E201, and a provider
  shipped alone later leaves a W193 catch-up. What the train published still
  counts toward its version, and a cancel of the consumer discards only the
  owed catch-up.

- let a worker lose a stale state-lock race under the TinyGo build ([cee16f5](https://github.com/yohimik/dispat/commit/cee16f5fa3ad84ef6f4810be868ebcf96cc43451)) (by yohimik, Claude Opus 5.5)
  Two workers taking over one stale worker.lock at once race on renaming it
  aside, and the loser's rename fails because the file is already gone. The
  takeover read that with os.IsNotExist, which in the TinyGo runtime does not
  look into the *os.LinkError a rename returns, so the TinyGo binary reported a
  raw rename error instead of settling the claim (a refusal naming the owner,
  or the folder when the winner never wrote). errors.Is(err, fs.ErrNotExist)
  reads the same answer in both runtimes. The TinyGo acceptance gate caught it
  in TestExecutionWorkerConcurrentStaleStateClaimHasOneOwner.

- say in the help that --require-release exits 3 ([d76e97c](https://github.com/yohimik/dispat/commit/d76e97c2231f620493bd275d1fdd8fb88917d84d)) (by yohimik, Claude Opus 5.5)
  The flag list said release and status exit 1 when the plan releases nothing;
  both exit 3, as their own descriptions and the documentation say, so a
  pipeline can tell "nothing to do" from a failure.

- leave a coordination branch alone when a stopping poll cut its fetch ([c3cbe21](https://github.com/yohimik/dispat/commit/c3cbe2114b8e63e177ec6b6086295c382067280d)) (by yohimik, Claude Opus 5.5)
  A worker or orchestrator stopped by a signal while a poll was fetching read
  the interrupted fetch as a branch nobody can fetch: it retried the branches
  one by one, quarantined each, and warned W244 that a coordination branch was
  left alone for the run. A fetch the stopping poll cut short says nothing about
  the branch, so the poll now returns the cancellation, quarantines nothing and
  reads the branch again next time. This also removes the spurious W244 that
  made TestExecutionTerminalMessageRacesWithdrawalLease fail about one run in
  five.

- say in the help that --config takes an absolute path ([d1b26de](https://github.com/yohimik/dispat/commit/d1b26de432ed6a6ff129f571fa7c2c31cd3d3fb5)) (by yohimik, Claude Opus 5.5)
  The flag has accepted an absolute path for a while, and the reference says
  so; the help still called it a name relative to --root.

- name a repository-local versioning group by its repository ([7204ab0](https://github.com/yohimik/dispat/commit/7204ab03a1ea3538f4dfa7300a8cf537851a1696)) (by yohimik, Claude Opus 5.5)
  A diagnostic about a versioning group local to one repository of a composed
  workspace printed the planner's internal identity, the repository and the
  group joined by a NUL, as in versioning group "web\x00platform". It now names
  the group as its author wrote it and the repository it belongs to, as in
  versioning group "platform" of repository "web"; a group of a single history
  reads exactly as before.

- mask a remote's password in two more places it was written ([5abae21](https://github.com/yohimik/dispat/commit/5abae2198cbb6e5c66c57b7d30cd89705e15dd97)) (by yohimik, Claude Opus 5.5)
  An HTTP request that its context or timeout ended reported its full URL,
  password included, in an error that reaches the log; it now reports the URL
  with the password masked, as net/http does. A remote written in the scp-like
  form with a password, user:password@host:path, is not a URL to Go's parser
  and passed through the redaction of git's arguments and of every remote a
  log line names; its user half is now replaced, while a bare account such as
  git@host:path and a refspec or a tag name that merely contains an @ stay as
  written. The documentation comment of the scp-form endpoint check sits on
  its function again.

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

- check the lock before the first worker probe ([4d8da55](https://github.com/yohimik/dispat/commit/4d8da551260cfb0e6a65247046fe95ea9502607f)) (by yohimik, Claude Opus 5.5)
  A distributed release planned under its locks and then probed every worker
  node before it asked the remote whether it still held those locks, so a run
  that lost its lock while it planned still pushed a probe branch to every
  mailbox. The coordinator now asks the run's ownership gate before the first
  probe: a lost lock refuses the run with E336 before any branch is offered.
  A sweep holds no lock, so its preflight asks nothing.

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

- retry a lock read before calling the lock lost ([8733c32](https://github.com/yohimik/dispat/commit/8733c32afcaf3d28c5b86e37a2d812a00b4daa57)) (by yohimik, Claude Opus 5.5)
  A distributed release asks the remote, before every assignment and every
  publication authorization, whether it still holds its release lock. One
  failed read counted as a lost lock, and a read that never answered held
  every later check of the run behind it. Each read is now bounded at fifteen
  seconds, and a failed read is read again, up to three reads one and then two
  seconds apart, with each retry logged at warn level. A remote that shows
  another lock object, or none, is still a loss at once. A remote that no read
  reached is a lock the run cannot show it owns: new effects stop exactly as
  they do for a loss, the lost line names the reason `unverified` instead of
  `lost`, and the lock, still the run's own, is given back. A cancelled lookup
  is not a loss.

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

- move a shared-version group as one when a member got ahead of its provider ([bfc8832](https://github.com/yohimik/dispat/commit/bfc883203fb5b9f29db9389b1e7a2a2ec4c7e8a5)) (by yohimik, Claude Opus 5.5)
  A member of a fixed versioning group that released on a change of its own
  while its provider's publish failed is still owed the provider's version, and
  the run that publishes the provider plans it again. The group measured its
  members' pending work against the commit of the tag holding the group's
  version, which is exactly where that owed contribution sits, so it treated the
  contribution as work the group had already versioned: the member caught up
  alone at the next patch while the rest of the group stayed on the old version
  until a later run rode them up. An owed contribution is no longer masked, so
  the whole group moves to the version the catch-up needs in one run.

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

### Dependencies

- [config](https://github.com/yohimik/dispat/releases/tag/pkg/config/v1.0.2-rc.1): 1.0.2-rc.0 -> 1.0.2-rc.1
- [writer](https://github.com/yohimik/dispat/releases/tag/pkg/writer/v1.2.2-rc.1): 1.2.2-rc.0 -> 1.2.2-rc.1
- [manifest](https://github.com/yohimik/dispat/releases/tag/pkg/manifest/v1.2.2-rc.1): 1.2.2-rc.0 -> 1.2.2-rc.1
- [models](https://github.com/yohimik/dispat/releases/tag/pkg/models/v1.11.0-rc.5): 1.11.0-rc.4 -> 1.11.0-rc.5
- [scanner](https://github.com/yohimik/dispat/releases/tag/pkg/scanner/v1.2.2-rc.1): 1.2.2-rc.0 -> 1.2.2-rc.1

### Authors

- yohimik
- Claude Opus 5.5


## services/dispat/v1.11.0-rc.5 (2026-09-23)

### Fixes

- isolate TinyGo fixtures from reporting-tool dependencies ([03ea15f](https://github.com/yohimik/dispat/commit/03ea15fdc0342d20e3dcf01a7fb9cde87b1bb513)) (by yohimik)

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

- [config](https://github.com/yohimik/dispat/releases/tag/pkg/config/v1.0.2-rc.0): 1.0.1 -> 1.0.2-rc.0
- [manifest](https://github.com/yohimik/dispat/releases/tag/pkg/manifest/v1.2.2-rc.0): 1.2.1 -> 1.2.2-rc.0
- [scanner](https://github.com/yohimik/dispat/releases/tag/pkg/scanner/v1.2.2-rc.0): 1.2.1 -> 1.2.2-rc.0
- [writer](https://github.com/yohimik/dispat/releases/tag/pkg/writer/v1.2.2-rc.0): 1.2.1 -> 1.2.2-rc.0

### Authors

- yohimik


## services/dispat/v1.11.0-rc.4 (2026-09-23)

### Features

- carry a sweep's declared outputs back to the orchestrator ([204ba42](https://github.com/yohimik/dispat/commit/204ba42f3b1a13bd3f50eabc7bfc8f55a699c3bb)) (by yohimik, Claude Opus 5.5)
  A sweep task that ran on another machine left what it wrote there.
  `runOutputs`, read from the entry configuration alone, now names the
  folders a script's tasks write, relative to the root of each package's
  repository. After a delegated task succeeds, the node captures those roots
  under the manifest and the transfer ceilings every output set is held to;
  a root the script did not write is admitted as empty, a deliberate
  difference from a build output root. The orchestrator verifies each set
  through the one validator and records it beside the sets already admitted.

  Every task of a sweep writes into one root, so the orchestrator merges
  instead of replacing: each file is staged and verified, then moved into
  place file by file, replacing the file of its own path and keeping every
  other file of the root. A set is installed all or nothing, and a move that
  fails puts back what it already moved. The merge waits until every task has
  answered: two tasks writing one path with different bytes fail the sweep
  with E227 naming the path and both tasks, and neither task's set is merged,
  so the root holds neither file. Identical bytes are merged once. A task
  placed on the orchestrator writes into the checkout directly and captures
  nothing.

- run script sweeps on worker nodes ([9d74ac8](https://github.com/yohimik/dispat/commit/9d74ac870487dbb6045990ee1a096c0127474230)) (by yohimik, Claude Opus 5.5)
  `dispat run` had no distributed path: a sweep ran every package's script
  in this checkout whatever `execution.workers` said. With worker links,
  from the file or from `--worker`, a sweep now runs the way a distributed
  release runs its task graph. The refusals come first and are a release's:
  a worker, or a process under worker authority, may not start one, and a
  lock bypass beside worker links is refused with E225. The plan is fixed
  and named, every link is probed, and each package's task goes through the
  coordinator with the new kind `run`, placed by the package's own `runOnly`
  as its build would be, the orchestrator last under `both`.

  A sweep task carries the script's commands and nothing around them, the
  computed environment with the static pairs unresolved, and the package's
  input closure; it installs nothing from other tasks. The node runs it
  with DISPAT_STAGE=run:<script> and returns its exports, which are merged
  onto the release so a consumer on another machine reads its providers'.

  A sweep takes no release lock: it records nothing and authorizes no effect,
  so its messages are bound to a generation drawn from its own run identity,
  and a release of the same repositories may run beside it. A distributed
  sweep ends with the summary a distributed release prints, one line per
  task with its node, its computation and its exports count. Without links
  nothing changes, except that a package whose `runOnly` pins its build to a
  worker is refused with E225 instead of being run on this machine.

- name a worker on the command line ([4de5d48](https://github.com/yohimik/dispat/commit/4de5d480c45ecb3e1fef20cb3edd389413f48401)) (by yohimik, Claude Opus 5.5)
  A pipeline that creates a worker machine a minute before the run cannot
  write it into the committed file. `--worker name=endpoint`, repeatable on
  release, run and status, adds one link to the entry configuration's
  `execution.workers` before anything is validated, so a link stated on the
  command line is held to every rule a configured one is: the node name, the
  credential-free endpoint, the folded uniqueness against the file's own
  links and the signing secret the file has to name. A refusal names the
  value the operator typed, never its endpoint.

  A malformed value is a usage error. Under worker authority, or on a node
  whose file says `role: worker`, the flag is refused with E226: a worker
  never dispatches to a pool. The links are no part of the plan digest.

- read and check the run outputs a configuration declares ([235bd00](https://github.com/yohimik/dispat/commit/235bd00006ea5008b3257b8b1719ca63a39f7040)) (by yohimik, Claude Opus 5.5)
  `runOutputs` is read from the root file and refused anywhere else as an
  unknown key. Every root is held where the file loads to the shape a build
  output root is held to, worded for a path relative to the repository root,
  and one script's list holds no root twice or one inside another. Once every
  package is known, a root that is or holds a package folder, or that overlaps
  a package's declared build output root, is refused at discovery, so `dispat
  status` reports it. In a composed workspace the entry's roots are resolved
  against the repository of every swept package. Every refusal carries E225.

  The path rules the two keys share are one function now; the build outputs'
  own refusals read exactly as before.

- report computed, admitted, published and recorded outcomes apart ([71eb3e7](https://github.com/yohimik/dispat/commit/71eb3e78dc7b11b1f72ddd18d931a5f3661157ff)) (by yohimik, Claude Opus 5)
  A completed task is not a released package, so a distributed run now prints one
  line per task in plan order with the four outcomes in four columns, the
  machine each frame ran on including this one, the providers it prepared without
  releasing them, the dependents it never attempted and the publications it
  cannot account for. A totals line counts each of them separately, and an
  execution metrics line states what the run cost without comparing it to
  anything.

- start no new effect after the lock is lost, and retain the one exclusion that has to survive ([93b4038](https://github.com/yohimik/dispat/commit/93b4038d02b4cf68b911bd17a3b5807cae8dd18e)) (by yohimik, Claude Opus 5)
  Ownership was verified before the plan was fixed and never again, so every
  assignment written afterwards was a new effect started on a check that was
  minutes old. It is asked again of the remote before each one, cached for a few
  seconds so a fan-out costs one query per owning repository, and a loss ends the
  attempts already in flight as an interrupt does. A run that authorized a
  publication it cannot account for now leaves that repository locked, with the
  order of recovery named instead of an instruction to delete a tag.

- hold capacity until acknowledged cancellation ([2875bf6](https://github.com/yohimik/dispat/commit/2875bf6d603bc947634951209403f9479d61a72f)) (by yohimik)
  A wait that elapsed says what the run stopped expecting and nothing about the
  machine at the other end, so a node slot now comes back only on a result, on
  an acknowledged withdrawal, or on a coordination ref this run revoked before
  anybody claimed it. An assignment that merely queued behind another run is
  revoked and placed again under an attempt of its own, so queue time is no
  longer charged as run time, and every assignment carries the deadline its node
  enforces on its own clock.

- withhold reauthorization while a publication outcome is unknown ([2875bf6](https://github.com/yohimik/dispat/commit/2875bf6d603bc947634951209403f9479d61a72f)) (by yohimik, Claude Opus 5)
  An authorized publisher that never reported is withdrawn and asked what it had
  got to. A node that stopped before its publish command began leaves an outcome
  the run knows; one that stopped inside it, or never answered, leaves one nobody
  here can establish, so the package fails at publish with no tag, no record and
  no second attempt under that authorization, and the repository it was
  publishing into is remembered as one whose exclusion has to survive the run.

- revalidate relevant inputs and lock ownership before authorizing a publication ([6c0b32a](https://github.com/yohimik/dispat/commit/6c0b32a81a057a1458c7ae3f2cc2650d54cff9d6)) (by yohimik, Claude Opus 5)
  A distributed run asks three things in the moment between a packages
  beforePublish hook and its publish command: whether it still holds the
  owning repositorys lock, whether the fleet is still the one the plan was
  computed over, and whether anything the artefact was built from moved
  after the build consumed it.

- publish from workers under orchestrator authorization ([b2db70c](https://github.com/yohimik/dispat/commit/b2db70cb96a8b1a074b0925f6fe62489ccecb4d2)) (by yohimik, Claude Opus 5)
  A publish stage of a space with no login script and an explicit runOnly
  worker value now runs on a node, behind the ready and go handshake: the
  node installs the packages own admitted outputs, runs the beforePublish
  hook and waits, and the run authorizes the command once, after it has
  revalidated what only it can revalidate.

- prepare unreleased providers without adding releases ([a2544c6](https://github.com/yohimik/dispat/commit/a2544c699272710d0e9b0aa7f43d53efd675ee97)) (by yohimik, Claude Opus 5)
  A consumer compiles against the folder its provider builds whether or not
  this run releases the provider, so a run that delegates builds now builds
  such a provider once, under the environment dispat run gives the same
  package, and admits its outputs through the path every other build output
  takes. Nothing about a release is invented for it: no version, no tag, no
  changelog, no record, no event and no plan entry.

- place each stage on the orchestrator or a worker as runOnly says ([779a2a9](https://github.com/yohimik/dispat/commit/779a2a93670f3ec4e9e4da725635efacfe13e414)) (by yohimik, Claude Opus 5)
  The orchestrator joins its own pool as a node of last resort: a build it
  takes captures, describes and admits its outputs through the path a
  worker's result travels, so a consumer cannot tell the two apart.

- let consumers build beside a provider and publish after it ([33ebac3](https://github.com/yohimik/dispat/commit/33ebac351b3b5c2f9d3fc147eeb427c70bc16388)) (by yohimik, Claude Opus 5)
  The provider relation resolves onto two questions rather than one flag:
  what a consumer's version and build stage waits for, and whether a
  provider that failed outranks a consumer reason of its own. A deploy-order
  provider states `none` and its consumers build beside it while their
  publications still follow.

- transport dependent build outputs between workers ([ef07597](https://github.com/yohimik/dispat/commit/ef07597002caba1ee824c59e2162a5ffcbe12206)) (by yohimik, Claude Opus 5)
  A node that finished a build captures its declared outputs into the same
  compare-and-swap push that reports the task, so the report and the bytes
  it describes travel together. The orchestrator admits the set against the
  roots the plan declares, installs it into its own checkout so that the
  stages which stay here still see the files, and names the admitted
  manifest to every consumer in the closure; when a consumer reads a
  different mailbox the object is relayed onto it as an immutable branch.
  A consuming node verifies the manifest it fetched against the digest it
  was assigned and installs every root of every input before its first
  command, so a prerequisite that cannot be retrieved and verified fails
  the task rather than becoming a build against whatever was there.

- bind declared build outputs to a verified manifest ([117911e](https://github.com/yohimik/dispat/commit/117911e16b5a55e9efafb7346b07effa1e8697ce)) (by yohimik, Claude Opus 5)
  Source synchronization alone does not make a provider's ignored dist
  folder available to a consumer on another machine, so a declared output
  set is captured into a tree of its own and described by a manifest bound
  to the run, the plan, the task attempt and the states the build
  consumed. One validator holds that description to every rule, so the
  orchestrator that admits a set and the node that consumes it cannot
  disagree about what a usable set is, and installation assembles the whole
  set before it replaces a declared root.

- let a task raise webhook events from its worker ([086db23](https://github.com/yohimik/dispat/commit/086db23f2d98d1f79218e1fdda7d358c1036c97b)) (by yohimik, Claude Opus 5)
  Reporting progress writes no release ref and starts no release, so a build
  script may raise its own events wherever the build was placed. Refusing the
  word under worker authority made moving a build to another machine a change
  in what a repository's receivers hear, which a placement decision may not be.

- name the node on every log line and webhook event of a distributed run ([52b1d78](https://github.com/yohimik/dispat/commit/52b1d780109535cfbbc46b96c12e3c16cedba91c)) (by yohimik, Claude Opus 5)
  A release spread over several machines is read from several logs at once, so
  every process taking part says who it is: the run logger is derived once,
  where the configuration is first known, and the dispatcher stamps the same
  two names on every event that leaves it. A line or event about another node
  names that node in a field of its own, which is what the orchestrator side of
  gates 6 and 7 now uses. A repository that states no execution object writes
  and delivers what it always did.

- let a standalone package live at the repository root ([95f7660](https://github.com/yohimik/dispat/commit/95f76602f44f40d41d8635e94eb5cdfb0e01c7a1)) (by yohimik, Claude Opus 5)
  A `packages` entry may name "." so a single-package repository declares its
  one package as the repository itself. The root configuration file is not
  merged in as that package's own folder layer, the invocation folder no
  longer infers it as a selection, and revertOnFail is refused on it.

- delegate build stages to workers from a prepared snapshot ([4223a57](https://github.com/yohimik/dispat/commit/4223a5745eb9d707887165d37ed9c08266a8b8a5)) (by yohimik, Claude Opus 5)
  Every build frame of a distributed run is executed on a worker node, from
  a commit whose parent is the planned head and whose tree is the working
  state as it stands. The version and syncLock frames stay here, bracketed
  by the guard that keeps their writes out of a snapshot somebody is taking.

- preflight every worker before dispatch ([9a3ebce](https://github.com/yohimik/dispat/commit/9a3ebcea17d33388330dc02bc16981ca7bd7348d)) (by yohimik, Claude Opus 5)
  A release that delegates work asks every configured node what it is once the
  plan is fixed and before the first hook: the protocol version, the platform,
  the capacity and the transfer ceilings have to admit the work this plan would
  place on the pool, and a node that cannot answer fails the run with nothing
  published and nothing tagged. The coordinator owns every ref the run creates
  and closes them before the locks go back.

- add the worker command and its signed git mailbox ([c1620b1](https://github.com/yohimik/dispat/commit/c1620b16d0341b2294b8836faa95fd3a6452ef87)) (by yohimik, Claude Opus 5)
  A serving node is started with `dispat worker`: it reads the work addressed
  to it from a git repository used as a mailbox, answers a probe with what it
  is, and refuses everything else with a stable reason and no echo of what it
  refused. One goroutine owns the poll, the memo, the object cache and the
  record of what has already been answered.

  Every message is authenticated by an HMAC over the kind of message and the
  exact document bytes. The kind has to be inside the code, because which
  message a document is comes from the name of the file it travels under:
  without it, anybody able to push to the mailbox could publish an authentic
  withdrawal as an authorization without ever holding the secret. Every message
  after the first also names the commit it answers, so an authentic message
  cannot be replayed at another position of its branch.

- derive an ownership generation from the held release locks ([187456f](https://github.com/yohimik/dispat/commit/187456fe59bbc3e4a3d4e1bd08a1bfc9cb07b75d)) (by yohimik, Claude Opus 5)
  Publication is authorized under the ownership the orchestrator still holds
  (CCME 28.6), so ownership has to be a question with a remote answer and a name
  of its own. IsHeld compares the tag object the run pushed with the one the
  remote advertises, the generation is a sha256 over the sorted locks, and the
  acquired locks are kept so both can be asked later.

- add compare-and-swap transport plumbing ([1c916c6](https://github.com/yohimik/dispat/commit/1c916c6322af2cbdde08db64c1b6511d327f6681)) (by yohimik, Claude Opus 5)
  A coordination branch moves by one leased push, so the remote decides who won
  and no reader has to trust a check it made a moment earlier. A rejection is
  told from a transport failure by the porcelain status flags rather than by
  git's wording. git prints those flags on standard output and still exits
  non-zero, so the leased pushes read a failed command's output through the
  streaming runner. Every other caller keeps the contract it was written
  against: a failed invocation returns nothing beside its error, which matters
  because rev-parse echoes an unknown argument on its way to failing.

- fix a plan digest over semantic input ([1a914e9](https://github.com/yohimik/dispat/commit/1a914e9be4b5362c4c716763d4266a03c2daa1e0)) (by yohimik, Claude Opus 5)
  The orchestrator of a distributed run has to name the plan it fixed so every
  node can tell which release it is taking part in (CCME 28.3), and the name has
  to follow what is released rather than where the release runs (17.2). The
  digest is a sha256 over a canonical document that serializes no map, and it is
  computed and reported only where workers are configured.

- refuse a release under worker authority or a lock bypass with workers ([731782c](https://github.com/yohimik/dispat/commit/731782c77d378eb41ed17051486aaee44bc0ebdf)) (by yohimik, Claude Opus 5)
  A release is started by an orchestrator, so the two states that are not one
  refuse before the first lock is pushed: a node whose configured role is worker,
  and a process executing somebody else's task, which is a marker the worker's
  task runner puts in the environment of every command it starts. The marker
  reaches a build script the way every other variable does, so the command line
  refuses the release and the commands that write a native release ref however
  deeply they are nested, and leaves the helpers a build legitimately runs alone.

  A run that delegates work is held to two further rules, and both are answered
  before anything is dispatched. The unsafe lock bypass exists for a repository
  with no remote to coordinate through, and a run reaching other machines is the
  opposite situation, so the configured switch, the environment switch and a peer
  that states one for itself each refuse the run rather than warn it, naming the
  repositories and the setting the warning would have named. The variable naming
  the signing secret has to hold something, and the refusal names the variable
  rather than reading it.

  Every refusal carries the numbered code dispat has always printed and, beside
  it, the machine-readable outcome class the specification requires, so a CI job
  can switch on the class without learning the numbers. The six classes and the
  codes this profile owns are declared together for that reason. Execution
  settings an imported or linked peer states are reported ignored once per run
  and are never read: any peer may be another run's entry, and a checkout that
  travels to another machine does not get to say what that machine may do.

- resolve build outputs and platforms through the ladder ([8385235](https://github.com/yohimik/dispat/commit/8385235bdb801f2f0818008ce855fdd42c049bc9)) (by yohimik, Claude Opus 5)
  The two keys have a line in all four field tables and fold through the same
  merge every space-shaped key does, so a package folder's own file outranks
  the space folder's file, which outranks the root file's space entry, and a
  stated list replaces the inherited one rather than adding to it. What a
  package ended up with lands on its resolved space, and `dispat status
  --log-level debug` names both lists on the package's resolved line when it
  has them, which is the one place a reader can check the ladder without
  starting a release.

  Every rule is one E225 refusal naming the key path. An entry has to be a
  relative slash-separated path inside the package folder, with no `..`, no
  `.git`, no backslash, no colon and no NUL, and no entry may be another
  entry's folder, because a root travels whole. A platform is os/arch and may
  not be stated twice.

  The last rule needs every package to be known: folders may nest, so two
  packages can come to claim one folder, and its contents would then be
  attributed to whichever build finished last. Discovery resolves every
  declared root against its own package folder and refuses an equal, a
  containing or a contained pair, naming both packages and both paths. The
  comparison folds case, because a checkout on a case-insensitive filesystem
  would merge the two.

  A root is installed by replacing the folder it names, so two more shapes are
  refused for what installing them would destroy: the package folder itself as a
  root, and a root that holds another package folder, whether or not that nested
  package declares anything.

- validate execution settings at load ([1a06f66](https://github.com/yohimik/dispat/commit/1a06f6664568c5738d0870f31a4360c6b6caa382)) (by yohimik, Claude Opus 5)
  The key has a line in the root object table alone, so a space, a package or a
  folder file refuses it as an unknown key: a checkout that travels to another
  machine must not be able to tell that machine what role it plays. Every rule
  it is held to is one E225 refusal naming the key path, before any lock, plan
  or command: the role vocabulary, a capacity below one, the node names, the
  credential-free mailbox addresses, the variable naming the signing secret,
  and the bounds a distributed run waits and transfers under. A refused address
  is named with its user half, query and fragment taken off, because those are
  the three places a credential would sit and the refusal is the one place the
  value would otherwise be written into a log.

  The settings are read from the entry configuration alone. An imported or
  linked peer may state its own object, because any peer may be another run
  entry, and it is validated with that file and never consulted. Nothing reads
  the settings yet.

- share a version prefix without sharing counter and channel ([3d65177](https://github.com/yohimik/dispat/commit/3d65177421bbbb31b329df714afb87935b9e1020)) (by yohimik, Claude Opus 5)
  A versioning group held its members to one prerelease counter and one
  channel, which is right when the version is a badge of the release itself
  and wrong when the group shares only a prefix: a retry after a partial
  publication burned a counter for members that had nothing to add, and
  ending one member's train ended everybody's.

  The two axes are now selectable per group, and the rule is one sentence: the
  group engages when a part of the version it shares moves. With a counter of
  its own each member continues from its own baseline, so a retry plans the
  failed run's versions for the legs that failed and nothing for the ones that
  published; with a channel of its own only the packages a directive names
  enter or leave a train, while a movement of the shared prefix still takes
  every member, each on its own line. Both axes default to the behaviour they
  replace, so no group's plan changes until it opts in.

### Fixes

- read back a created release whose notes are longer than an error message ([18dc54f](https://github.com/yohimik/dispat/commit/18dc54f9a4d6dae884611e89fae4bb6628924793)) (by yohimik, Claude Fable 5.1)
  GitHub created the candidate's release and echoed its 63,627 bytes of notes,
  and the client refused the answer as larger than the 64 KiB bound sized for
  an error message, failing a publication the server had already performed
  and leaving the release without its binaries. Reading one release, created
  or looked up, is now bounded at 1 MiB, above the 125,000 characters of notes
  GitHub accepts plus their escaping and the release's metadata.

- carry the exit status of a command that failed on a node into the run's message ([ff002eb](https://github.com/yohimik/dispat/commit/ff002eb7f97eb3a4955e0cb5eaa806b9a1e32345)) (by yohimik, Claude Fable 5.1)
  A node reported a failed frame with an exit field nobody filled, so the run
  said "exit 0" of every script that failed on a worker, and the operator had
  to open the node's log to learn the status. The node now reads the status
  off the command's error, through whatever wrapping the sequence added, and
  the run names it; a failure that was not a command's, an input, a deadline
  or a refusal, names none.

- show a sweep's run outputs and a named worker in the example files ([1de3ede](https://github.com/yohimik/dispat/commit/1de3edec4c32b54da09ff276cbb56ecd4713377e)) (by yohimik, Claude Opus 5.5)
  The main example pair declares a `test` script whose reports land under
  the repository's `coverage/`, and `runOutputs` naming that folder, with a
  comment saying what a distributed sweep does with it. The orchestrator
  example says that a pipeline can name a worker it created with `--worker`.

- admit a build's outputs under the attempt that produced them ([f096061](https://github.com/yohimik/dispat/commit/f096061da8068d2a68de196a141e5746772e15c5)) (by yohimik, Claude Opus 5.5)
  A task whose assignment queued unclaimed is revoked and placed again as
  its second or third attempt, and the node binds the outputs it captures to
  that attempt. The orchestrator held every admitted set to attempt 1, so the
  build that finally ran was refused as `output-identity` and its package
  failed although nothing about it was wrong; a prepared provider placed
  again failed its consumers the same way. The admission now expects the
  attempt that answered.

- attribute a unit's sources to a dependent within the unit's depth of it ([e461d7d](https://github.com/yohimik/dispat/commit/e461d7d9b7869b7f7d231ae21625e95382edafc1)) (by yohimik, Claude Fable 5.1)
  The bump axis credited the unit's whole source set to every dependent the
  walk reached, so a consumer two edges from one source and one from another
  was owed by both, and one that consumes only one of the packages a unit was
  written over was told it releases because of the other. §9.2 attributes
  from(d) = {P in sources : dist(P, d) <= depth}, §13.4a's reaching(u, D), and
  draws the owed set from it; a source the target does not depend on within the
  unit's depth owes it nothing, whatever delivered says of the pair. A consumer
  that ui had delivered a shared commit to was released again on core's
  account, two edges away.

  The reaching set is read off the cached per-source walks the composed walk is
  built from; a unit over one package reaches every target from it and pays
  nothing. owedSources takes the set in name order and answers a subset in that
  order. TestMultiScopeSourceOutsideTheManifestsStaysOutOfUpdates pinned the
  old attribution and now expects the provenance the specification states.

- say in the candidate notes how a proceeding consumer is caught up and where it is not ([e8ce18b](https://github.com/yohimik/dispat/commit/e8ce18b38833ca0e6ca425f58d40e56a0e94aeed)) (by yohimik, Claude Fable 5.1)
  The caption gains the card's catch-up paragraph, which fits its limit, and
  both gain the departure the specification's history entry names: an app that
  released past a failed library is caught up only when it is in the run where
  the library publishes, until owed windows and E201 land.

- capture build outputs as the build wrote them, whatever the checkout's attributes say ([9f649f8](https://github.com/yohimik/dispat/commit/9f649f88172d02fe4031b5be6bf5b9591e9c031f)) (by yohimik, Claude Fable 5.1)
  Build outputs were captured with `git add`, which applies the checkout's
  `.gitattributes` to whatever it stages: a `text` attribute rewrote every
  CRLF pair inside three libraries of a project that marks its whole tree as
  text, and the consumers installed corrupted binaries that the project's own
  integrity check refused. A build output is not source. The forced capture
  now hashes every file with no filter or conversion, records links and
  nested repositories as git would, and fills the temporary index by hand;
  the source snapshot keeps git's conversion, which is what a checkout of it
  expects.

- let a publishing node answer what it refused and read a withdrawal of an unread authorization ([80e9aab](https://github.com/yohimik/dispat/commit/80e9aabe6e290e488c43b18bda0430e0c03efc95)) (by yohimik, Claude Fable 5.1)
  A node that refused an authorization, a withdrawal or a stray message
  leased its failed result over the ready commit although the branch had
  moved past it, so the result could never be written and the run waited out
  its task deadline; for an authorization the run had written itself, such as
  one the node read after its expiry, the run then reported an outcome it
  could not establish and retained the release lock for a command that never
  started. The refusal is now reported on top of the message it refused.

  A withdrawal the run wrote on top of an authorization the node had not read
  yet was refused as a replay, because the node compared it with the ready
  commit it was waiting at. That is the ordinary shape of a run interrupted
  within one poll interval of authorizing. The authorization beneath the
  withdrawal is now read and verified, expired or not, and the withdrawal is
  acknowledged; the fence before the command reads a withdrawal that landed
  after the authorization the same way.

- qualify a tag commit without a branch that nothing can take ([729ef32](https://github.com/yohimik/dispat/commit/729ef327826c53c9163c8c309af241ae75b37456)) (by yohimik, Claude Opus 5)

- name the dead-pickup lookup apart from the field it fills ([773f9dc](https://github.com/yohimik/dispat/commit/773f9dceea8db2dd1aea4a95ca5b62bfd5fb4774)) (by yohimik, Claude Opus 5)
  A field on the task context and a method promoted onto it from the run
  shared one name, so which of the two a reader sees depended on the
  receiver they were looking at.

- write this change's comments and rows without em dashes ([e786124](https://github.com/yohimik/dispat/commit/e78612479c41366d56162b4061fddfb9834e8217)) (by yohimik)

- reconcile a proceeding consumer to what its providers published ([e598046](https://github.com/yohimik/dispat/commit/e598046519a7cd7488d82c9d14737ab2faebaf51)) (by yohimik, Claude Opus 5)
  A consumer that proceeds past a provider which died after its version
  stage had manifests naming a version nobody will ever publish. Its
  reconciliation is redone before its publish, against the providers that
  are still alive, and a consumer whose build already embedded the planned
  version is blocked with W194 rather than silently rebuilt. The blocking
  rule itself is read as the specification writes it: every admitted cause
  of the release, not any own reason.

- admit a provider's unit for a consumer until a release of the provider delivered it ([47c2cb2](https://github.com/yohimik/dispat/commit/47c2cb2e367496aa5d4ace1b63b32558f75bcd16)) (by yohimik, Claude Opus 5)
  The bump axis asked only whether the dependent had released past the
  commit, so a consumer that proceeded on a reason of its own while a
  provider failed or was held was never planned again: it kept the
  provider's previous version for ever and reported nothing. Admission now
  follows delivery, for the sources that still owe the dependent a version.

- describe the execution keys in the example configuration ([a38be82](https://github.com/yohimik/dispat/commit/a38be821ed3c3a0c7900cf4f450196c74ac5e5c5)) (by yohimik, Claude Opus 5)
  The orchestrator and worker examples were written while the profile was being
  built. Publication is delegated only by an explicit runOnly, a build under
  `both` may run on either machine, and the transfer window and the task
  deadline bound what the finished code bounds.

- re-read the branch when a withdrawal loses its lease ([8eeaad4](https://github.com/yohimik/dispat/commit/8eeaad4e8300fbe6acee01dbed6e24494fa218bb)) (by yohimik)
  Both parties advance one branch under expected-old checks, so a withdrawal
  written against the object the run last saw loses to a node that moved the
  branch meanwhile, and that is the ordinary case: a claim reaches the poller up
  to one poll interval after the node wrote it, and an interrupt inside that
  window was leased against the assignment and wrote nothing at all. The branch
  is asked where it is, once, and the withdrawal is written against that; an
  attempt already at its terminal message needs none.

- read the branch before calling a publication outcome unknown ([75d0d5b](https://github.com/yohimik/dispat/commit/75d0d5b26beb260360c87e7ec491ae66d82dc664)) (by yohimik)
  An authorization this run could not write was classed by the shape of the
  push failure, so a push that never left the machine was treated as one whose
  answer was lost: the package became an unknown outcome and its repository was
  left locked although no node could have been told anything. The branch is
  asked instead, which is what answers the question: a tip still carrying the
  ready commit carries no authorization, and only a branch that moved or cannot
  be read is the case the mark before the push exists for.

- write a withdrawal on a context the interrupt did not cancel ([349ddb2](https://github.com/yohimik/dispat/commit/349ddb2eb8887db2121c4f72056fc3da10091d70)) (by yohimik)
  The withdrawal of an in-flight attempt went out on the caller context, which
  for the commonest reason to withdraw one is already cancelled: an interrupted
  run wrote no cancellation at all, so the node kept building, its slot was held
  for nothing and a publisher was left with no answer to the one question that
  has to be answered before a lock goes back. Both the withdrawal and the wait
  for its acknowledgement are now bounded by the run cancel wait and detached.

- take over a stale node lock without taking a fresh one ([178bc01](https://github.com/yohimik/dispat/commit/178bc01e4f1d7db73ce9700b2110b2d7cb74c26c)) (by yohimik)
  Two processes that read the same stale worker state lock could interleave a
  remove and a create, so the second removed the first fresh claim and both
  believed they owned the folder, each holding half the record of what had been
  answered. The stale lock is renamed aside and what was renamed is examined: a
  live claim is put straight back and its owner reported, which is what the
  refusal always promised and what a plain remove could not deliver.

- keep the orchestrator own capacity and retry a raced capture ([22aa718](https://github.com/yohimik/dispat/commit/22aa718768c237f5856024e7fe998355500988dc)) (by yohimik, Claude Opus 5)
  Two conditions on the machine that owns the run. A frame placed here held a
  lease that could be leaked, so a local frame that ended in anything but a
  reported result took this node out of its own pool and with it every frame
  only this node may run. And the capture of a prepared input state stages the
  repository and then hashes it, so a changelog the recorder renames into place
  while another package is dispatched failed the capture and the innocent
  package with it.

- authenticate a cancellation and find a result below the tip ([d337838](https://github.com/yohimik/dispat/commit/d3378382c22f43b537989fd859082fba4939a832)) (by yohimik, Claude Opus 5)
  Two messages of a coordination branch were believed for the word in their
  commit tree rather than for their signature. An attempt whose result was
  buried under one foreign commit waited out the whole task deadline and took
  its node out of the pool, and a publisher acted on any tip that said cancel,
  so whoever could write into a mailbox could refuse every publication of every
  run. The chain under a tip is read for the step that answers the attempt, and
  a withdrawal is held to the rules an authorization is held to.

- keep a node polling past an unreadable branch and a long task ([4092628](https://github.com/yohimik/dispat/commit/40926281a93283389babe41c554306baa7fe06d1)) (by yohimik, Claude Opus 5)
  A serving node stopped seeing its work in three ways. One ref that lists and
  does not fetch failed the whole batched fetch and with it every other branch of
  that tick. A prepared input state or a relayed result in the node own namespace
  was read as work and reported as an assignment nobody could read. And the idle
  clock was a timer reset on progress, so a task that outlasted the timeout was
  followed by an immediate stop and a node that had just started could stop
  before it had served anything.

- give a result that carries build outputs the transfer window to report ([f0c9e95](https://github.com/yohimik/dispat/commit/f0c9e9594fc87dda325aa323a1a82de53bd23cca)) (by yohimik, Claude Fable 5.1)
  A finished task reported on a context bounded by the thirty seconds a small
  document needs, and a result carrying a build's outputs is the push of those
  outputs: a 1.7 GB install tree was killed mid-push on a real run and the task
  never reported. Such a result now travels under the configured
  transfer.timeout; a result carrying nothing keeps the short bound, so a node
  asked to stop is not held for the transfer window by a report with nothing
  to transfer.

- let a consumer with work of its own proceed under a none relation ([ae635e5](https://github.com/yohimik/dispat/commit/ae635e5a4fb5a1ce6cbf5a5b09e5bb5ee68f0142)) (by yohimik, Claude Fable 5.1)
  The repository owner's rule: a consumer with a change of its own proceeds
  past a provider that failed or was skipped. isBlocking was true unless
  stated under a none relation, which skipped such a consumer; it is now
  false unless stated there as under build, an explicit opt-in to the
  unconditional skip, and stays true under publish, where the consumer's build
  takes the provider's publish as its input and no work of the consumer's own
  can stand in for an input that never existed.

- read a coordination branch again when reading it failed ([c8fec92](https://github.com/yohimik/dispat/commit/c8fec924f1350e688e1fe2fefd54051182bcdc3f)) (by yohimik, Claude Opus 5)
  A poll remembers every branch whose objects it fetched, so that an
  unchanged branch is not read twice. The memo was recorded before the
  caller had made anything of those objects, so a local git failure while
  resolving the chain or reading the message counted as having dealt with
  the branch: a tip that never moved again was never offered a second
  time, and one failed read cost the whole task deadline on either side.
  Such a branch is now reconsidered, while a message this protocol refuses
  still is not, because reading it again reaches the same decision.

- materialize a task's repositories outside in ([7c0d2be](https://github.com/yohimik/dispat/commit/7c0d2be05b185e951e64da081d5ba07291e72879)) (by yohimik, Claude Opus 5)
  A composed workspace hands a node one worktree per repository of the
  package's input closure, and the closure named the repository that owns
  the package first. In a linked fleet the peer holding the entry's own
  checkout is not that repository, so the nested checkout was created
  first and the entry's worktree was then refused the folder the nested
  one had just made: every delegated build of a package a peer owns failed
  with E227. The order is the layout's now, shallowest path first.

- admit no output set from a publication ([12869b0](https://github.com/yohimik/dispat/commit/12869b0d2dd955bb7e2cc3b50d410e041cf02f40)) (by yohimik, Claude Opus 5)
  A publish task describes no outputs of its own, so running its result
  through the build admission asked a publisher for a set this run had
  already admitted from the build and refused the publication for not
  having one. An authorization is also never issued twice, and the second
  request is refused without a second withdrawal.

- report a preparation once it has an outcome ([f5348ed](https://github.com/yohimik/dispat/commit/f5348edf6cfecec8c5f2c60cabc6ddf99f34006d)) (by yohimik, Claude Opus 5)
  The line that opened a preparation said the provider had been built before
  its first command ran, and the record it leaves is a decision with two
  branches, which belongs in a function that returns from each of them.

- count only declared providers as a release reason ([7bec6e5](https://github.com/yohimik/dispat/commit/7bec6e525ad542ff4b1ccfd30079d6888435e641)) (by yohimik, Claude Opus 5)
  A provider reached through a package this run does not release hands
  the consumer no version to pick up, so its publication must not stand
  in for a declared provider that failed (SPEC 19.3, 19.5).

- order publications through packages that are not in the plan ([8b76917](https://github.com/yohimik/dispat/commit/8b769178295c6c9e0fdb3540317c1ca4743df619)) (by yohimik)
  The publish order was taken over the subgraph the plan induces, so a
  consumer and a provider it reaches only through a package with nothing
  to release were mutually unordered and the consumer could publish
  first, against a version nobody had published (SPEC 19.2).

- block a consumer behind a failed provider it reaches through an unreleasing package ([8b76917](https://github.com/yohimik/dispat/commit/8b769178295c6c9e0fdb3540317c1ca4743df619)) (by yohimik, Claude Opus 5)
  The skip cascade read the direct providers alone, so a provider that
  failed left the consumers behind an unreleasing package unblocked
  (SPEC 19.3). Both halves are one walk over the whole graph, and
  ordering without blocking would publish the consumer after a provider
  that failed.

- run a build placed on the orchestrator outside the snapshot guard ([5db2c43](https://github.com/yohimik/dispat/commit/5db2c4362673c5b4303e08445b1b95a98417ee0a)) (by yohimik, Claude Fable 5.1)
  A build frame the orchestrator kept held the shared side of the snapshot
  guard for as long as the build ran, so every dispatch that needed a fresh
  snapshot waited for the longest local build, and a waiting capture held
  every other reader off behind it. The orchestrator takes a build exactly
  when the workers are busy, which is when the next dispatch is about to be
  needed, so the run serialised where it was meant to overlap. The guard
  exists for frames that write the tracked files snapshots are made of; a
  build writes outputs no snapshot carries, and now holds its pool slot alone.

- carry no build outputs to a consumer across a none relation ([1f70e72](https://github.com/yohimik/dispat/commit/1f70e72dca49ff8dacccb70cffadc437a9b06a06)) (by yohimik, Claude Fable 5.1)
  A build task's inputs were the declared outputs of its whole provider
  closure. A provider whose relation is none declares that its consumers'
  builds read nothing it builds, and such a consumer may be building while the
  provider still is, so the closure now ends at that provider and at whatever
  lies behind it on that path. A provider another path still reaches stays an
  input.

- name the resolved relation in the comment that pointed at the old field ([43ea5ad](https://github.com/yohimik/dispat/commit/43ea5add342e9906bf35ca2bb4575a48b990e1af)) (by yohimik, Claude Opus 5)

- order builds through packages that do not build ([e49d3c6](https://github.com/yohimik/dispat/commit/e49d3c6d2a659b1f65334b37f9558fd23c9b2542)) (by yohimik, Claude Opus 5)
  Build order was taken over the subgraph the plan induces, so a provider
  reached only through a package with nothing to release was unordered
  against its consumer. It is now taken over the whole dependency graph and
  restricted to the packages that build, as the publish order is, and a
  `none` hop ends the constraint of every path through it.

- refuse an output link whose target climbs after it descended ([d4e49eb](https://github.com/yohimik/dispat/commit/d4e49ebd5dbae90cb1fe70004a9aa13b1b4f7496)) (by yohimik, Claude Fable 5.1)
  A link target was held to its root lexically, and `sub/../x` reads as `x`
  beside the link. When `sub` is itself a link to a folder higher up, the same
  target resolves above the root the set travelled in, so the lexical answer
  and the file system's differ. A target may now step up only before it steps
  into a name, which is the shape every tool that writes relative links
  produces, and the refusal is the existing link-escape one.

- refuse an output manifest larger than the run allows ([d4e2f18](https://github.com/yohimik/dispat/commit/d4e2f18a59a0a5673473d1933302621c7a46cd53)) (by yohimik, Claude Opus 5)
  A description a reader could not read looks exactly like a node that
  never answered, so the ceiling is applied where the manifest is written
  and the run is told which rule was broken instead of waiting out the task
  deadline. The label a refused frame carries names the two parts of a
  delegated stage that are not commands, so an operator reads what failed
  rather than that something did.

- wait for a starting worker to write its state lock before reading it as stale ([134bf12](https://github.com/yohimik/dispat/commit/134bf129b6af06c69edf0a0882a25606e0fd79df)) (by yohimik, Claude Fable 5.1)
  The lock file is created and then filled with the owning process id, which
  are two operations. A second worker arriving between them read the empty
  file as a lock nobody holds, removed it and claimed the folder, so two nodes
  could serve one state folder with half the record of answered work each. An
  empty lock is now polled for up to a second, and only one that stays empty,
  or that names no process, is replaced.

- read the fleet shape once for the groups and the union-find ([3259267](https://github.com/yohimik/dispat/commit/32592673148a78f4a57a505b83dfe94748fc5c9e)) (by yohimik, Claude Opus 5)
  Both were derived from the links separately, so the centre pass needed a
  guard against a pair the union-find had already joined and no black-box test
  could reach it. One reading of the pairs makes a group and a set the same
  thing.

- keep only the centre fallbacks a composed fleet can reach ([b23c441](https://github.com/yohimik/dispat/commit/b23c441998ecd2b834d89fbc0cc9b1deae78ffd5)) (by yohimik, Claude Opus 5)
  Measure a centre by eccentricity from the two ends of a longest route rather
  than by reading that route back, and drop the join fallback that would need
  two groups with links, which one entry can never compose. Both were branches
  no black-box test could reach.

- join linked groups at their centres in the minimal topology ([83e912f](https://github.com/yohimik/dispat/commit/83e912f5c88b3c1a5c7b53a71425c061c439b0dd)) (by yohimik, Claude Opus 5)
  Every completion of the fleet forest adds the same number of links, so the
  count cannot choose between two proposals. The longest route can, and section
  27.9 charges link evidence and settlement by it. Join each group at its
  centre, hanging every other centre off the centre of the group with the
  greatest radius.

- drop two guards the comparison cannot reach ([623c61b](https://github.com/yohimik/dispat/commit/623c61b0c2699fcce5e55c05c6dd2d23ec83c436)) (by yohimik)
  A release-tag format map is never empty where the records are read, and a
  commit id that is no commit is absent from the head's history for the same
  reason every other absent one is. Neither arm changed an answer, and an arm no
  run can reach is an arm no test can hold to anything.

- compare only the records a release could plan again ([fa4f298](https://github.com/yohimik/dispat/commit/fa4f298e876402346fb1d30461072a982f7d733d)) (by yohimik)
  A name with a release tag's shape and no version in it records no release,
  and the planner already reads neither a baseline nor a duplicate out of one,
  so the comparison skips it rather than refusing a checkout that lacks it.

- let the planner report a workspace it cannot discover ([fa4f298](https://github.com/yohimik/dispat/commit/fa4f298e876402346fb1d30461072a982f7d733d)) (by yohimik)
  The comparison was the first thing on the release path to ask for the
  workspace, so a configuration error arrived as a refusal to release instead
  of the discovery failure it is. It now settles which stores it would read
  before discovering anything, which also spares a run that records nowhere the
  walk, and leaves a discovery error to the planner that reports it.

- warn when commit.verify leaves the records uncompared ([fa4f298](https://github.com/yohimik/dispat/commit/fa4f298e876402346fb1d30461072a982f7d733d)) (by yohimik, Claude Opus 5)
  The setting excuses the read; it cannot make the run read as though the
  records had been compared, because a checkout missing one the remote holds
  can then publish that version a second time.

- create a release tag on the remote and never replace one ([0f3b14e](https://github.com/yohimik/dispat/commit/0f3b14ef7487f1c95943946aa665a34349fb6823)) (by yohimik, Claude Opus 5)
  A release tag travelled with the rest of the push under commit.force, so a
  tag the remote already carried was overwritten: a published record moved onto
  whatever commit the pushing clone had planned. Each record now travels leased
  against its own absence, which is one operation rather than a read and a
  write, and the remote decides. A name it already holds at this release's
  commit is the retry of a write whose answer was lost and is reported as
  already recorded; a name it holds elsewhere is left exactly where it is and
  reported as E221, beside the local tag case it has always been. Only an alias
  declared moving is still forced, which is what it is for.

- plan a release from the remote's records as read under the lock ([8638567](https://github.com/yohimik/dispat/commit/8638567c86475c6e2679e573f2beee2b7d89fdab)) (by yohimik, Claude Opus 5)
  The lock is taken before the plan, but the plan reads its release tags from
  the local clone, so a checkout made before another run recorded, or made with
  no tags at all, plans a version that is already published and publishes it a
  second time. Under the locks and before the plan is computed, the engine now
  compares the release records of every store this run writes to with the ones
  it is about to plan from: a record on a commit the planned head reaches that
  the checkout lacks is E196, the same name at another commit is E191, and a
  record off that history cannot change this plan. Nothing is repaired and
  nothing is fetched, in the single repository and in every participating
  repository of a composed workspace alike.

- say in the orchestrator example where a publish runs ([223eed8](https://github.com/yohimik/dispat/commit/223eed82b6b1c1154ecbf5c5791c98ee69919d1e)) (by yohimik, Claude Fable 5.1)
  A space that logs in publishes on the node a release was started on, so the
  login and the credentials it uses never leave that machine and nothing about
  them reaches a mailbox. The example claimed the login ran on the worker that
  published. It now states the rule, and that a publish runs on a worker only
  where a package asks for it.

- show every way of running a release in a full example file ([b959f45](https://github.com/yohimik/dispat/commit/b959f45b8610eaa3a1f88f8688f0abefe65097d3)) (by yohimik, Claude Fable 5.1)
  The annotated example pair describes one repository released on one machine,
  and the ways of running a release that cannot share a file with it had no
  example to copy. Each now has one that loads as written: a control repository
  over several source repositories, one peer of a linked fleet, a node that
  delegates its builds and publishes to worker nodes, and a worker node. A test
  loads each under the loader its command uses, so an example cannot drift away
  from the configuration language.

  The main pair gains the keys it never showed: the build outputs and platforms
  a package declares, the update check, the changelog's entry spacing,
  dependency links and commit references, the release commit's force, the
  GitHub recorder's allPackages, a webhook's method, and the parser's quiet
  flag and trailer lists. The YAML and JSON files still load to one
  configuration.

- offer queued work again and tell an input state from a message ([c31f16d](https://github.com/yohimik/dispat/commit/c31f16d41e975777ac0d6fa2666c485b1b8decc1)) (by yohimik, Claude Opus 5)
  A node that was full left the assignment where it was and remembered the
  branch as seen, so the work waited for a push nobody was going to make.
  The branch is forgotten instead, and a commit carrying nothing of the
  protocol is the prepared input state it is rather than a rejected message.

- index ancestry, scope globs and propagation walks once per plan ([c68d8bd](https://github.com/yohimik/dispat/commit/c68d8bdefbc59159449c12198453ae7dbdb2c365)) (by yohimik)
  Planning asked the same questions again for every commit, every unit and
  every package, and each of them now has an index. Ancestry among the
  union's commits is one marker pass, a bit per boundary, so a cancel
  barrier or a correction target costs a bit test rather than a walk of
  the history. A glob term is resolved once per pattern and ownership
  class, and a glob ending in a star reads a contiguous run of the folded
  names. File ownership probes the prefixes of a path instead of comparing
  it against every package. A propagation walk is computed once per source
  set and edge kinds, and a bounded walk is a prefix of the unbounded one.
  The repository inputs of a release are closed over the condensed
  components of the dependency and version-group graph. A unit's
  propagation scope is resolved once for both axes.

  The pending windows are read as a single union walk where the Git
  implementation offers it, and every window is recovered from it by
  ancestry. Three fault tests are re-aimed at the calls that still happen,
  because ancestry inside a window is now read off the parent lists the
  window already carries, and only a commit behind the baseline still puts
  the question to the repository graph.

- graduate a train entered by the channel-entry patch ([c68d8bd](https://github.com/yohimik/dispat/commit/c68d8bdefbc59159449c12198453ae7dbdb2c365)) (by yohimik, Claude Fable 5.1)
  A train entered by that patch carries no bump of its own, so graduation
  computed the stable baseline itself, below the core the train was
  published under, and the run aborted with E185. The same patch that let
  the train in lets it out, at the core it carried. A train a whole minor
  above its stable baseline is still not what one patch explains.

- read every pending window in one history walk ([7f21e0c](https://github.com/yohimik/dispat/commit/7f21e0cdf8308bd9a6d794d771c57c79088676ad)) (by yohimik, Claude Fable 5.1)
  A plan needs one window per distinct baseline. Read one at a time, the
  messages and changed paths of the commits those windows share are read
  and parsed once per window, and the windows are nested or overlapping
  views of one history. CommitsSinceAny is the optional capability that
  reads their union in a single walk, bounded by the merge bases of the
  boundaries, and declines a boundary the head does not descend from
  rather than answering an unrecoverable listing.

  Ancestry is indexed with it. The commit graph now gives every commit a
  dense index and keeps a boundary's ancestor set as a bitset, so the
  questions planning asks thousands of times cost a bit test rather than a
  walk of the ancestry each time.

  The alias formats of a workspace are indexed by the literal text they
  open with, so an unparsed tag is tried against the aliases that could
  have written it rather than against every package's.

- match globs in linear time ([f7b90a1](https://github.com/yohimik/dispat/commit/f7b90a1b2e57bb88817be2474032ef35a082022a)) (by yohimik, Claude Fable 5.1)
  The two-pointer walk restarted one byte after its last star, so a
  pattern such as a star followed by a long run cost the product of the
  pattern and the subject. Since a star is the only metacharacter, the
  pattern is its literal segments: a prefix test, a suffix test, and the
  leftmost occurrence of each segment between them, found by
  Knuth-Morris-Pratt so the bound holds on hostile input too. That is the
  linear cost 18.3 asks for.

- bring a releasing laggard to the prefix under its own counter ([b2b5b14](https://github.com/yohimik/dispat/commit/b2b5b14fceb8f553c6099106c0d294da8a30e3d7)) (by yohimik)
  The floor lifts a member's target to the part the group shares, except for
  one member: a member on stable takes no floor while the group's line is a
  prerelease, because it must not be the first to publish that core as stable.
  That member's own release therefore stayed on its old line, a whole shared
  minor below the rest of the group, which is the invariant a versioning group
  exists for. The alignment pass now brings it to the prefix instead, on the
  line's channel and at its own counter, the same way a laggard with nothing
  pending joins.

  Two functions the new code replaced are gone with it: the group rule's floor
  accessor, superseded by the per-member one, and nextPrerelease, whose one
  caller now names the target core itself.

- write the pin rule's comment without em dashes ([bdf7e47](https://github.com/yohimik/dispat/commit/bdf7e47e239601e1d0ed4aff06640bdebabb7d1d)) (by yohimik, Claude Opus 5)

- keep a member's own pin from breaking a moving group ([d13ccd2](https://github.com/yohimik/dispat/commit/d13ccd2a9b33aee59dc47b0ff3304db247e021bb)) (by yohimik, Claude Opus 5)
  A pin naming a version inside the prefix the group is leaving is the one the
  group deliberately did not take: fixedGroupPin leaves such a pin to the
  member, because it asks for nothing of the group's. Versioning that member
  by its own computation therefore applied the pin and left it on the old
  prefix while every other member moved, which is the one thing a versioning
  group exists to prevent. A pinned member now takes the group's version
  whatever the channel axis says, exactly as it always has where the channel
  is shared.

- describe the versioning-group file by the rule it now carries ([cdfbaf8](https://github.com/yohimik/dispat/commit/cdfbaf8274a130c84630838950ee432a1b99343b)) (by yohimik, Claude Opus 5)
  The file's own narrative still said a group engages when the shared prefix
  moves and that assignment is the same either way, which is the default and
  no longer the whole rule. It now names the two further axes, points at the
  value object that owns the decision, and says what an assigned member takes
  when its channel is its own.

- show the versioning object in the annotated example configs ([e1b83ab](https://github.com/yohimik/dispat/commit/e1b83ab6e4235ae18e5992c2fb20f05345385c84)) (by yohimik)
  The annotated configs are where an operator reads what a key accepts, and
  `versionGroups` accepted only the scalar there. Both now carry a second group
  stating all three axes.

- preview no entry for a package that never releases ([b41ea23](https://github.com/yohimik/dispat/commit/b41ea23e3e0dedd2ffce21820e2e7ccbb9a134a7)) (by yohimik)
  A changed `versioning: none` package was previewed as `## name@0.0.0
  (stable)`, a header built from the placeholder it carries in the plan and a
  body of notes no changelog will ever receive. It reads as a release that is
  about to happen. The preview now takes the early-out the plan graph already
  takes for the same package.

- keep a never-released provider out of the replacer's fan-out ([5a0f007](https://github.com/yohimik/dispat/commit/5a0f0075653b5985785594d1f2af3ee2345e17b4)) (by yohimik)
  The manifest-derived fan-out expanded a rule over every workspace package a
  manifest named, a `versioning: none` one included. Such a package has no
  version, and the 0.0.0 it carries in the plan is a placeholder, so a pattern
  rendered over it rewrote a real pinned coordinate down to that placeholder
  and then reported a catch-up (W197) from a provider that is never released.
  The parsing strategy and `autowriter --set-local` already refuse it; this is
  the third place that has to.

  The same function read a provider's version from one answer and its channel
  from another, so a held provider's withheld prerelease raised W203 against a
  consumer picking up the provider's published stable version. Both now come
  from the version the rules actually render.

- record the version a withheld provider actually carries ([f34f1fd](https://github.com/yohimik/dispat/commit/f34f1fd75d6ade8b5b93e6c4c68a81ebdb9989a4)) (by yohimik, Claude Opus 5)
  A provider's entry in a consumer's Updates named the version the provider
  computed rather than the one it ends the run with. For a held provider those
  differ: the plan reports the withheld version so an operator can see what
  lifting the hold would release, and no tag ever carries it. The consumer's
  changelog dependency line, its GitHub release body, DISPAT_UPDATED_* and
  DISPAT_DEPENDENCIES all named it, with a tag link that leads nowhere, while
  native auto-versioning wrote the published version into the same run's
  manifests. One release cannot have two answers.

  A provider that is not releasing now carries its published version, which
  collapses the entry onto the catch-up shape the record already knows how to
  reconstruct a From for.

- floor a versioning group member at its group's line ([5f6712c](https://github.com/yohimik/dispat/commit/5f6712c6576d5c3d8438b390d2ea662cf4b659fc)) (by yohimik, Claude Opus 5)
  A member's own window need not carry the work that set its group's core. A
  rider carries none of it, and a leg that failed after its neighbours
  published carries only part of it, so the member's own computation lands
  below a position the group already holds: a half-finished graduation retried
  E185 instead of finishing the train, and a rider versioned on its own hit
  E195 on a core the group had already reached.

  The group now hands each member the core of its line before the member is
  versioned, and the computation is raised to it before the guards read the
  result. Both guards keep their meaning, because the floor never reaches past
  the line: a tag nothing in the group explains still fails, and so does a
  channel switch that would go backwards.

- read a versioning group's channel from its movers only ([fb94226](https://github.com/yohimik/dispat/commit/fb942263a10ad07191c967fbe5d019aae74ad4e1)) (by yohimik, Claude Opus 5)
  A member resting where its own tags put it proposed that channel to its
  group's aggregate, so a sparse member that never joined a train graduated
  the whole group, and a rider left behind by a failed graduation dragged the
  group back onto the train it had already left. A channel is derived from a
  baseline (§11.1) and a proposal is a directive, so only a member that is
  itself moving between channels now contributes one.

### Authors

- yohimik
- Claude Fable 5.1


## services/dispat/v1.11.0-rc.3 (2026-09-20)

### Features

- polyrepo saga orchestration and choreography ([97b1f03](https://github.com/yohimik/dispat/commit/97b1f032b14c140fc2b3bb5b4870c0bc383cbb37)) (by yohimik)

### Fixes

- let the TinyGo acceptance gate name the one test it leaves out ([026f8ad](https://github.com/yohimik/dispat/commit/026f8ad3748b61499e438deee6430e80f37bfc3c)) (by yohimik, Claude Fable 5.1)
  The gate refuses any skipped test case, and
  TestGoInstallBuildUsesTheGoToolchainForUpdates skips under a prebuilt TinyGo
  run: it builds a dispat of its own with `go install` and never touches the
  binary being accepted. Both arrived together, and the suite hanging on an
  interrupt test hid the conflict until that was fixed; then the CLI build
  failed with nothing but "false" after all 3126 tests had passed.

  The gate now leaves that test out by name with -skip, so the rule stays
  absolute for every other test, and when it does refuse a run it prints which
  tests failed or skipped.

- catch the CLI's go.mod up to the released pkg modules ([c97a432](https://github.com/yohimik/dispat/commit/c97a432311eacff09a576a1485b3e0277e566cf2)) (by yohimik, Claude Fable 5.1)
  The partial release published scanner and writer 1.2.1, which require
  pkg/manifest v1.2.1, while the CLI still required v1.2.0. Inside the link
  bracket the build runs with -mod=readonly, so every CLI build on main stopped
  at "updates to go.mod needed". The CLI now requires config 1.0.1 and manifest,
  scanner and writer 1.2.1, the versions that run released, reconciled with
  `dispat autoversion` and tidied. models stays at its published rc.0: its next
  version is only planned, and the release writes it.

- never reconcile a declaration that names a versioning none package ([d282cfc](https://github.com/yohimik/dispat/commit/d282cfc4553dcbcf1d65c6d6a4d06e54406053f8)) (by yohimik)
  A versioning "none" package is never released, so it has no version to write.
  The configured edge into one is refused at load, but a manifest can still name
  the package, and auto-versioning "caught it up" to the 0.0.0 placeholder the
  plan carries: a go.mod requiring a published v1.0.0 left the version stage
  requiring v0.0.0, with a W197 for a catch-up that never happened.
  `dispat autowriter --set-local` derived the same range. Both now leave the
  declaration as written; --link-local still links the folder.

- announce with one crier command named by the channel ([8a956d7](https://github.com/yohimik/dispat/commit/8a956d7196bae8b4e4cef5a78c26ece156c93601)) (by yohimik)
  The announce script is `crier publish --config
  "announce/$DISPAT_CHANNEL/crier.yaml"` behind the ANNOUNCE switch, and nothing
  builds data first: both configurations read the stage's environment. A
  release candidate still reads only the version. A stable release reads the
  release-notes groups dispat generated, one entry per line, so notes.py is
  gone; its card prints each group as written and its caption carries a group
  only while it is short, which keeps the worst case inside every platform's
  limit. The stable release link now names the released version.

  The cli-released and cli-version step outputs move to the postPublish hook, so
  they never wait on a social network.

- end an HTTP request when its context does ([5f9c7a6](https://github.com/yohimik/dispat/commit/5f9c7a6c3e6612b6ff52fe613b8bce6164b1a1ad)) (by yohimik)
  Every HTTP call now goes through httpx.Do, which returns when the request's
  context ends or the client's Timeout elapses even if the transport notices
  neither. The TinyGo build's HTTP client reads the response on the calling
  goroutine and consults no context and no timeout, so a server that accepted a
  request and never answered held a tiny binary for good, and an interrupt could
  not free it.

  That is what TestStandaloneGitHubInterruptDrainsTheInFlightRequest met: against
  dispat-tiny-linux-* it sat for 51 minutes on both architectures until go test's
  60 minute alarm, which took the Build and Native ARM64 export jobs from about
  20 minutes to over an hour. Built with the same fork release, the test now
  passes in 0.01s.

- announce from the dispat config instead of a shell script ([9b4cf64](https://github.com/yohimik/dispat/commit/9b4cf6485e1effb1cd7d99b0cf29de4273b37b04)) (by yohimik)
  Replace announce.sh with a short `dispat if` chain in the package's announce
  script: nothing is posted outside the announce stage or without ANNOUNCE, and
  the channel picks its crier configuration.

  A release candidate now announces only what a person wrote, with every link
  to that candidate and no generated notes. The words live in rc/notes.yaml,
  which rc/crier.yaml pulls in as its caption with a $ref, and are repeated on
  the card; the only value taken from the release is the version. Discord
  receives the links and the lede and leaves the long paragraphs to the
  attached pictures, which keeps the caption inside its 2000 character limit.

  A stable release keeps the notes dispat generated. notes.py replaces notes.sh
  and prints the same document in half the code.

  publish.yaml holds only the three destinations, all enabled; each channel
  writes its caption beside the reference. The release workflow gates the ping
  and the announcement on one ANNOUNCE switch, pings both configurations
  without flags, and puts the verified crier on PATH.

- give RC and stable announcements their own crier configurations ([13cf8af](https://github.com/yohimik/dispat/commit/13cf8afb6becf678fc4ef7de06d84115c0ffe3ef)) (by yohimik)
  Add rc/crier.yaml and stable/crier.yaml. Both take their whole publish
  section from the shared publish.yaml, so the two channels cross-post to the
  same destinations, and each owns its card and its music. announce.sh selects
  the configuration from the channel notes.sh reads off the version, and the
  release gate pings both.

  Keep a channel's fixed text in two committed files, announcement.md and
  links.md, with the version written as ${DISPAT_NEW_VERSION}. The RC links name
  this exact candidate. Captions print the links, the notes and the changelog;
  the RC card repeats the same notes and prints the links on its cover.

- restore the full-suite release gate ([85fb4e8](https://github.com/yohimik/dispat/commit/85fb4e8dae0c649398ff24633893767cbd58dd9b)) (by yohimik)
  Cover release recovery, locking, topology, configuration, manifest and installation edge cases. Reject nonregular manifest files before opening them, report unavailable Git during release startup, and correct the coverage gate assertion.

  Validated all 17 package test jobs, 1,315 integration tests with and without race detection, coverage freshness checks, and repository checks. Integration coverage: 23,826/25,073 statements (95.03%); combined coverage: 97.3%.

- derive linked releases from repository topology ([35664e1](https://github.com/yohimik/dispat/commit/35664e1685e703c347233421f9faefdba8b1622a)) (by yohimik)
  Remove saga selectors and infer linked ownership from repository identity.
  Let compute choose minimal or star links while preserving existing edges.
  Reject cycles, invalid identities and incompatible topology, exclude disabled
  peers, and resolve link paths relative to the owning repository.

  Include integration regressions for topology, repair failures and exclusions.

- separate RC and stable release announcements ([de8b1f9](https://github.com/yohimik/dispat/commit/de8b1f98785fb5f9cdb030b5cf3ce271d8b6bcf4)) (by yohimik)
  Move the shared publisher and media into services/dispat/announce. Select
  channel-specific copy, introduce the saga RC announcement, and pin install
  commands to the announced version. Update release and replay workflows,
  Docker contexts, and offline announcement checks for the new layout.

### Dependencies

- [config](https://github.com/yohimik/dispat/releases/tag/pkg/config/v1.0.1): 1.0.0 -> 1.0.1
- [manifest](https://github.com/yohimik/dispat/releases/tag/pkg/manifest/v1.2.1): 1.2.0 -> 1.2.1
- [scanner](https://github.com/yohimik/dispat/releases/tag/pkg/scanner/v1.2.1): 1.2.0 -> 1.2.1
- [writer](https://github.com/yohimik/dispat/releases/tag/pkg/writer/v1.2.1): 1.2.0 -> 1.2.1
- [models](https://github.com/yohimik/dispat/releases/tag/pkg/models/v1.11.0-rc.3): 1.11.0-rc.2 -> 1.11.0-rc.3

### Authors

- yohimik
- Claude Fable 5.1


## services/dispat/v1.11.0-rc.0 (2026-09-15)

### Features

- release composed repositories with guarded source records ([3b751b5](https://github.com/yohimik/dispat/commit/3b751b58e27beea1d9ea0c6f6aac45d887aa9775)) (by yohimik, Codex (gpt-5.6-sol))
  Plan from each repository history and preserve the accepted source heads.
  Acquire ordered fleet locks, guard publication inputs, record and push in the
  owning repository, and share exact native pins with nested workspace commands.
  Keep optional control checkpoints and source-local policies explicit.

- share verified source pins with concurrent nested commands ([074d1dc](https://github.com/yohimik/dispat/commit/074d1dc069a48fd9e4a4826ba2cffad0a275ed48)) (by yohimik, Codex (gpt-5.6-sol))
  Bind temporary per-run pin coordination to the exact workspace and source
  identities. Publish atomic owner records, bound file reads, preserve full
  commit exports, and remove transient coordination at run completion.

- coordinate repository publication and critical source records ([26f4a16](https://github.com/yohimik/dispat/commit/26f4a162f880fe2a025e38ad0c5c8c7ed499eb40)) (by yohimik, Codex (gpt-5.6-sol))
  Order publications sharing a repository without blocking independent
  repositories or converting sibling order into dependency failure edges.
  Preserve published results and block consumer closure after required record
  failures. Carry workspace ownership to nested commands and diagnostic codes
  to failed-package observers.

- guard repository mutations and release tag snapshots ([52075ac](https://github.com/yohimik/dispat/commit/52075ace92c3e2506551c9a7084d0755bf9f28ae)) (by yohimik, Codex (gpt-5.6-sol))
  Serialize native Git mutations by common Git directory and compare exact
  package tag refs before publication. Share compiled prefix dispatch between
  tag inventory and snapshot readers without retaining command buffers.

- plan releases across repository-owned Git histories ([685b6cf](https://github.com/yohimik/dispat/commit/685b6cf1ee7548d74e287a74b7cba5e6a2dc3b27)) (by yohimik, Codex (gpt-5.6-sol))
  Read source tags and commit windows under the control gitlink snapshot.
  Keep scope, corrections and ancestry repository-local while propagating
  through the combined graph. Require explicit boundaries when normal
  release checkpoints do not prove prior consumption, and causal control
  intent when incomparable source directives conflict.

  Share canonical commit payloads, persistent gitlink snapshots and distinct
  window memberships; retain existing single-repository diagnostics.

- compose repository configurations under a control root ([db6208d](https://github.com/yohimik/dispat/commit/db6208d976e1c92b3bddc3060f3cb8a4845d1059)) (by yohimik, Codex (gpt-5.6-sol))
  Load explicit source configs or central package definitions with repository
  ownership, local defaults, external dependencies and nested script context.
  Route selectors and config computation through the composed workspace and
  resolve source record destinations independently from the control environment.

  Index initial versions and repository aliases once per operation, bound tag
  query workers, and validate overlapping package scopes with sorted directory
  prefixes. Cover config precedence, ownership, imported scripts and writeback.

  Verify the isolated staged milestone across app, config, CLI, Git, changelog
  and model suites; benchmark the new indexes through 10,000 packages.
  Multi-history release planning and recording follow in subsequent commits.

- validate optional external dependency providers ([d5c2ea1](https://github.com/yohimik/dispat/commit/d5c2ea1f85f463adfdd5626902e27381f8018220)) (by yohimik, Codex (gpt-5.6-sol))
  Accept the polyrepo configuration surface and keep absent external providers
  out of the active graph while validating consumers and dependency kinds.
  Exercise absent and included providers and malformed dependency kinds.

### Fixes

- preserve repository ownership throughout composed releases ([683ab0e](https://github.com/yohimik/dispat/commit/683ab0ead51d3df4a7b27b655bb978a31a4bebee)) (by yohimik, Codex (gpt-5.6-sol))
  Keep folder inputs, parser diagnostics and nested-step tag masks local to
  their declaring repository. Honor direct channel intent, stop user hooks
  on interruption, order boundary errors and free temporary planning indexes.

- retain compact repository input closures ([7ced500](https://github.com/yohimik/dispat/commit/7ced5004126d95ae3748047b73873315994e8d2a)) (by yohimik, Codex (gpt-5.6-sol))
  Carry owner, provider, shared-group and applicable control history into
  publication validation as interned immutable bitsets. Share sparse provider
  lists during planning and discard temporary name lists before the returned
  plan retains its ancestry callback.

- reject stale workspace pin readers ([bb54e30](https://github.com/yohimik/dispat/commit/bb54e30c151a64b631eefdf456f47382c0719d0f)) (by yohimik, Codex (gpt-5.6-sol))
  Fail when a cached reader loses its coordinator or the directory is replaced.
  Cover malformed, oversized, symlinked and non-regular coordination files while
  preserving an absent owner pin inside a valid run.

- retain source pins from composition through planning ([55d4308](https://github.com/yohimik/dispat/commit/55d43080a1f14d4f226046f2726e51a1a9249333)) (by yohimik, Codex (gpt-5.6-sol))
  Compare source HEAD with the captured control revision and resolve live pins
  under the source Git mutation lock. Reject control identity collisions before
  building repository ownership, including every case variant.

- reject packages owned by unlisted nested repositories ([7213ef0](https://github.com/yohimik/dispat/commit/7213ef0ee1ba9ef9ae86be2f8f763f39400420b9)) (by yohimik, Codex (gpt-5.6-sol))
  Memoize existing Git-root ancestry during workspace discovery so package
  and source paths cannot silently use a different repository history.
  Preserve the existing requirement that configured source folders exist.

- preserve config provenance and exact repository ownership ([4c14f03](https://github.com/yohimik/dispat/commit/4c14f0380cf1a71dd4ea59cd21e89b610fdade0e)) (by yohimik, Codex (gpt-5.6-sol))
  Resolve imported paths from each declaring reference file, canonicalize
  checkout ownership, and retain the logical gitlink path for history and
  recording. Validate exact repository identities and report structured
  composition diagnostics. Accept only exact owner-qualified commit exports
  when a nested command reuses its enclosing workspace.

- index shared tag inventories without retaining raw buffers ([d9304c7](https://github.com/yohimik/dispat/commit/d9304c746e3026df315619f6dd288e4f2fe92422)) (by yohimik, Codex (gpt-5.6-sol))
  Parse each ref once and dispatch matching package formats through a prefix
  index. Preserve tag ordering, peeling, malformed records and custom format
  overlap while detaching retained fields from the raw inventory allocation.

- preserve absent external providers during config computation ([1745be8](https://github.com/yohimik/dispat/commit/1745be8f3d575e757b6f94957b2729bb36756e5e)) (by yohimik, Codex (gpt-6-astra))
  Keep optional providers outside the workspace when rewriting dependency
  lists, without hiding missing consumers or stale edges to present providers.
  Retain external annotations through kind corrections and dependency additions.

  Cover JSON and YAML root, space and package declarations, repeated computation,
  and the existing TOML manual-edit fallback. Regressions fail before the fix.

### Dependencies

- [models](https://github.com/yohimik/dispat/releases/tag/pkg/models/v1.11.0-rc.0): 1.10.0 -> 1.11.0-rc.0

### Authors

- yohimik
- Codex (gpt-5.6-sol)


## services/dispat/v1.10.0 (2026-09-09)

### Features

- diagnose messages ([1bb3b39](https://github.com/yohimik/dispat/commit/1bb3b397e4749461c1102869a56b1ceb600636ae)) (by yohimik)

### Dependencies

- [models](https://github.com/yohimik/dispat/releases/tag/pkg/models/v1.10.0): 1.9.0 -> 1.10.0

### Authors

- yohimik


## services/dispat/v1.9.0 (2026-09-09)

### Features

- validate source commits ([4c22611](https://github.com/yohimik/dispat/commit/4c22611dc02c6a88e39fae48f9b3184cdb3ae2fe)) (by yohimik)

### Fixes

- distinguish command arguments ([8402051](https://github.com/yohimik/dispat/commit/8402051e3842fec1ab3604d31d396c051778da1b)) (by yohimik)

### Dependencies

- [models](https://github.com/yohimik/dispat/releases/tag/pkg/models/v1.9.0): 1.8.0 -> 1.9.0

### Authors

- yohimik


## services/dispat/v1.8.2 (2026-09-06)

### Fixes

- pin release references ([8ac4766](https://github.com/yohimik/dispat/commit/8ac47666e4de3213f06727c7dda88cd520c39fa5)) (by yohimik)
  Pin help to the installed CLI's documentation snapshot. Keep agent guides on
  the CLI major/minor line with independent patches and same-line update advice.
  Correct CCME algorithm cost bounds and unsafe optimization guidance.

### Authors

- yohimik


## services/dispat/v1.8.1 (2026-09-06)

### Fixes

- link agent and reference guides ([a90fcb7](https://github.com/yohimik/dispat/commit/a90fcb723b53345857fabb1dd127f61b602f1f57)) (by yohimik)
  Add common agent guidance and configuration/API links to root help.
  Correct the release lock ordering described by --require-release help.

- update TinyGo toolchain ([4bb91d6](https://github.com/yohimik/dispat/commit/4bb91d6506173f05946013b809e6bc8ccacdd0ca)) (by yohimik)

### Authors

- yohimik


## services/dispat/v1.8.0 (2026-09-05)

### Features

- support Aqua manifests ([18f3e2c](https://github.com/yohimik/dispat/commit/18f3e2c2891ad3959a02535f6b6d942a5c0fd326)) (by yohimik)

### Fixes

- retain parser type identities ([f17f6ef](https://github.com/yohimik/dispat/commit/f17f6ef0d490abb150bf456ebb57a266ddcaece9)) (by yohimik)

- preserve bootstrap workspace ([48c55c6](https://github.com/yohimik/dispat/commit/48c55c6e1e38a48dd5f4fa9a9e4c202709553ee3)) (by yohimik)

- share baseline commit windows ([d7ca8be](https://github.com/yohimik/dispat/commit/d7ca8befd185f7da3ea3fddd240de340f53c6f3f)) (by yohimik)

- share release tag inventory ([7c5d5da](https://github.com/yohimik/dispat/commit/7c5d5da7990a780506da962d3ac3100f1497462c)) (by yohimik)

- announce in one command ([c8b8123](https://github.com/yohimik/dispat/commit/c8b812398136a6d5709b74d259f5925fc3af6558)) (by yohimik)

- post photo announcements ([26d2223](https://github.com/yohimik/dispat/commit/26d222364f2a8459b9b84fb2575b3726db54574b)) (by yohimik)

- retain distinct authors ([d949e3d](https://github.com/yohimik/dispat/commit/d949e3de59e4b2e5264461e1cf7c934719aa0650)) (by yohimik)

- share commit windows ([06e5890](https://github.com/yohimik/dispat/commit/06e5890b9c0b4a2928e8bfaf41d5a9832640a54c)) (by yohimik)

- keep locks TinyGo compatible ([42ab519](https://github.com/yohimik/dispat/commit/42ab5198483896c4ef48a289f5c687bddf7b4756)) (by yohimik)

- harden install checks and logs ([23cf03d](https://github.com/yohimik/dispat/commit/23cf03d3d5524459fdf1b8d912778482d16fc7c1)) (by yohimik)

- protect release ownership ([c4e6c62](https://github.com/yohimik/dispat/commit/c4e6c6299967d1fd8a2e33aa14d8fb44584df9e6)) (by yohimik)

- bound release HTTP work ([b743446](https://github.com/yohimik/dispat/commit/b7434468680a2468539f4821ffff305ba4ca6c6b)) (by yohimik)

### Dependencies

- [ccme](https://github.com/yohimik/dispat/releases/tag/pkg/ccme/v2.0.0): 1.0.0 -> 2.0.0
- [manifest](https://github.com/yohimik/dispat/releases/tag/pkg/manifest/v1.2.0): 1.1.1 -> 1.2.0
- [models](https://github.com/yohimik/dispat/releases/tag/pkg/models/v1.8.0): 1.7.0 -> 1.8.0
- [scanner](https://github.com/yohimik/dispat/releases/tag/pkg/scanner/v1.2.0): 1.1.1 -> 1.2.0
- [writer](https://github.com/yohimik/dispat/releases/tag/pkg/writer/v1.2.0): 1.1.1 -> 1.2.0

### Authors

- yohimik


## services/dispat/v1.7.2 (2026-09-03)

### Fixes

- the release announces itself and ships its experiments ([e1ac8c5](https://github.com/yohimik/dispat/commit/e1ac8c514e68679a1ad9d881386fcaa443e8e054)) (by yohimik)
  The release experiments run inside the release, as the docs package's
  beforeBuild hook against the image the run has just published, and the
  site's new release experiments page is built from what they recorded;
  the records land in coverage/experiments and never under the harness.
  tools/testreport reads the cells into the report and renders the job
  summary, replacing summary.py. Every finding of the harness review is
  fixed: the masked exit codes, the vacuous gating assert, the observer
  that fetched into the clone under test, the shim's marker and log, the
  proxy's deny rule and chunked uploads, the pinned fixture dates.

  The release announces itself on Instagram and LinkedIn with crier, from
  the announce folder at the root: a paginated card, the anthem clip, the
  stories and the LinkedIn reel with an album fallback, with a ping job
  ahead of the release and a replay workflow beside it.

  scripts/install-tools.sh pins crier and the TinyGo fork in one place;
  the tiny toolchain stage, the spike and the darwin script install
  through it, and a shellcheck gate sweeps every script.

### Authors

- yohimik


## services/dispat/v1.7.1 (2026-09-02)

### Fixes

- the tiny binaries build on the fork at 0.43.0-net.1 ([cf86355](https://github.com/yohimik/dispat/commit/cf86355d56962502c266d531d78dbe1c6004ec66)) (by yohimik, Claude Fable 5.1)
  The two dispat-tiny-linux binaries the release attaches move to the
  TinyGo fork's 0.43.0-net.1, net.4's content rebased on upstream 0.42.0,
  with the checksums the release Dockerfile verifies the toolchain
  against. The spike's base image follows upstream to 0.42.0 and its
  verdict is re-read there: a host netdev exists now and the tcp, http
  and dns layers pass, but crypto/tls is still the offload stub that puts
  plaintext on the wire, so the answer stays no and the fork stays the
  only toolchain that speaks TLS. The unit-test stage runs one package at
  a time under a bound, and internals/tinygo.md records all of it.

  The README's projects section gains crier, a single-package repository
  released through an eighteen-candidate rc train and one graduation, and
  the landing page reads it from there.

### Authors

- yohimik
- Claude Fable 5.1


## services/dispat/v1.7.0 (2026-09-02)

### Features

- install from a private github repository ([d198be3](https://github.com/yohimik/dispat/commit/d198be39909b61cb183ecdc74bd5057c6614b5b1)) (by yohimik, Claude Fable 5)
  A token now authenticates the whole install path rather than only the
  release listing: dispat install and dispat self-update fetch a private
  repository's assets from the asset API endpoint, install.sh, install.ps1
  and the GitHub action do the same, and a release carrying several files
  installs the one named after the repository without --asset.

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

- install scripts reach a private repository ([036d897](https://github.com/yohimik/dispat/commit/036d89711c420958068bd5b41749e347bc1cb053)) (by yohimik, Claude Fable 5)
  The bootstrap scripts authenticated the releases API and then fetched
  the binary from the public download URL with no headers, which for a
  private repository is a sign-in page served under a 200: the install
  failed on its checksum with nothing saying why.

  Both scripts now read the asset's own REST endpoint out of the release
  and, when a token was given, download from there with the octet-stream
  Accept and the bearer credential. The credential must not reach the
  object storage that endpoint redirects to, and each downloader is
  handled on its terms: curl drops Authorization across a change of host
  by itself, wget is stopped at the redirect and the location refetched
  bare, and PowerShell, which forwards the header on 5.1, is stopped the
  same way. busybox wget can do neither, so a token sent through it is
  refused rather than leaked. Without a token nothing changes.

  install.sh is now executed against a fake API, once per downloader, so
  the endpoint choice and the credential's boundary are proven rather than
  read; install.ps1 has no interpreter in the test image and is held to
  the same shape by a textual cross-check.

- download release assets with the listing's token ([8368390](https://github.com/yohimik/dispat/commit/836839032048b5056b00ab8bfec6f06f31622a40)) (by yohimik, Claude Fable 5)
  A repository the listing needed a token for serves its assets only
  through the asset API endpoint — the public download URL answers with a
  sign-in page. When the source carries a token, install and self-update
  now fetch the asset from its API endpoint with the same credential;
  without one, nothing changes.

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

- fall back to the public URL, and read wget's answer ([f5b4760](https://github.com/yohimik/dispat/commit/f5b4760ae4211a46863d41735a366e3ba7d8bce7)) (by yohimik, Claude Fable 5)
  Three ways the authenticated download was wrong.

  An asset endpoint that refuses ended the install. A token that reads a
  repository's listing and not its assets is a real shape, and before the
  endpoint existed that install simply worked, so all three downloaders
  now try the public URL once more with no credential; the size and the
  digest still decide what lands, and when both addresses fail the refusal
  names the status the endpoint gave.

  install.sh threw away wget's exit status, so a refusal that wrote no
  file read as success. The status is kept and decides, except when a
  Location came back, which is the one answer an exit code cannot
  distinguish from a 404. The busybox probe is now the option's own exit
  code rather than a search of help text nobody promised.

  Both awk walks started at the top of the release, so a release titled
  after its own asset made the url walk print the author's account URL.
  They start at the assets array instead.

  install.ps1 caught an exception Windows PowerShell 5.1 never raises: it
  returns the refused redirect rather than throwing. The response is now
  asked for and inspected, with the 7.x exception path kept beside it.

- recognise every alias tag, whoever wrote it ([9248d6d](https://github.com/yohimik/dispat/commit/9248d6d54b42537116ee73a0da18f794fbde009e)) (by yohimik, Claude Fable 5)
  Two ways a moving alias still poisoned a baseline.

  The filter only knew the listing package's own aliases, but an alias
  belongs to whoever writes it and lands in whichever listing its shape
  matches: one package's "v1" sits in another's "v{version}" listing
  looking exactly like a release nobody can parse, and that package's
  baseline collapsed to its initials from the first alias onwards. The
  filter is now built from every package's formats, compiled once.

  The matcher assumed the version class was digits and dots regardless of
  the format, so an alias spelling a plain {version} did not recognise its
  own prerelease renders and poisoned its own package. It now reads the
  class off the format as the release-tag matcher does, walks the reduced
  shapes a prerelease-spelling format renders, and requires the text it
  captured for {version} to be a version: without that, anything the class
  allows would pass and a genuinely malformed tag would go out with it.

- record the release commit ([d3299f9](https://github.com/yohimik/dispat/commit/d3299f9673ba568a68fcf7f857507a43a46a70b1)) (by yohimik, Claude Fable 5)
  The GitHub release is created after the push, and after a mid-release
  recovery HEAD is the merge by then, so the "commit" line in its body and
  its target_commitish named the merge rather than the release the record
  is about.

  The recovery already reads the release commit before merging, since the
  merge message names it. It now hands that back, and the finalize phase
  prefers it over HEAD when stamping the releasers. A run with no recovery
  sets nothing and reads HEAD exactly as before.

- merge what landed during a release instead of rebasing ([5b74cd5](https://github.com/yohimik/dispat/commit/5b74cd5d08f60b0ad8a0bf7dda13a8a3d8aaa00e)) (by yohimik, Claude Fable 5)
  The recovery from a push somebody pushed under replayed the release
  commit on top of what arrived, which rewrote it: the tags had to be
  moved onto the replacement, a release pinned to a commit its own scripts
  made could not be recovered at all, and the commits that arrived ended
  up inside the window the release closed, so nothing ever released them.

  It now merges instead. Nothing the run made is rewritten, so the release
  commit keeps its identity and its tags keep naming it: the tagged tree
  still carries the changelog entries and version rewrites the release
  recorded, which is what anything resolving the tag reads. Only the
  branch tip changes, into a merge whose first parent is the release
  commit and whose second is what arrived.

  That leaves the arriving commits outside the tag's ancestry, which is
  where they belong: the next run plans them and releases them with
  records of their own. The merge commit is a chore(release), so the scope
  nonPackageScopes exempts keeps it from naming a package, and it carries
  the release commit and its tags in its body so the join can be audited.

  The merge is made from the branch rather than onto the fetched tip.
  Either parent order reads the same to the planner, which finds an
  exempted scope on the merge either way, but this one is a single command
  that a single command undoes: a conflict aborts back to exactly the tree
  the run had, rather than mid-merge or on a detached HEAD.

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

- recover a release push somebody pushed under ([91551ef](https://github.com/yohimik/dispat/commit/91551efe4b27f3d8381b57e8a55ec1d2fef8350c)) (by yohimik, Claude Fable 5)
  The behind-remote guard closes before the plan is computed, so a commit
  pushed while the run is working reaches the finalize push as a
  rejection. The packages have already published by then, and the run
  ended with the release commit and its tags nowhere but the local clone,
  which nothing goes back for.

  The push now tells a branch that moved apart from a remote nobody could
  reach, and the first is recovered: the branch is pulled, the release
  commit replayed on top of what landed, the tags moved onto the replayed
  commit and the push retried. The tags therefore only ever reach the
  remote on the commit that reached it too. W242 reports the recovery,
  because the release went out on a tree that is not the one it was
  planned against.

  A replay that conflicts, and a release whose tag is pinned to a commit
  its own scripts made, are both refused with an error naming that commits
  landed during the release: the rebase is undone, no tag is pushed, and
  the lock is given back as on every other way out.

  The commit.verify guard scenario asserted the old failure and now
  asserts the recovery: what that guard protects is the plan, not the
  push.

- say what the release token now unlocks ([a754978](https://github.com/yohimik/dispat/commit/a7549788fb95f4c76856fe3107a0b9ac8aee1fc9)) (by yohimik, Claude Fable 5)
  The token stopped being only a rate-limit lever when the asset download
  started using it, but the godoc on Source.Token, the comment behind the
  GITHUB_TOKEN fallback and the download's own commentary all still said
  so. They now say that a public repository needs no token and a private
  one needs it for both the listing and the asset.

  The download also had no record of the choice it makes. It now logs the
  asset, the address it went to and whether the request was authenticated,
  at debug level and never the credential itself, so a failed private
  install can be told apart from a public one that went the usual way.

- enumerate release pages on PowerShell 7.6 ([c5ebc04](https://github.com/yohimik/dispat/commit/c5ebc0436209d048797163609a642a31273183cd)) (by yohimik, Claude Fable 5)
  Invoke-RestMethod stopped enumerating JSON arrays, so a page arrived as
  one Object[] and the tag filter became member enumeration over every
  tag — where a shorter tag from another package made Substring throw.
  Seen as a hard failure in every Windows 'Set up dispat' action step.

### Dependencies

- [models](https://github.com/yohimik/dispat/releases/tag/pkg/models/v1.7.0): 1.6.0 -> 1.7.0

### Authors

- yohimik
- Claude Fable 5


## services/dispat/v1.6.0 (2026-08-31)

### Features

- dispat for runs a script once per item ([2aa6053](https://github.com/yohimik/dispat/commit/2aa60534c97080afd303c944339bbface82123cd)) (by yohimik, Claude Fable 5)
  The third shell helper, beside if and exec. A POSIX `for x in ...; do
  ...; done` copied into a configured script is the one construct that
  breaks the moment `shell` names something else, and a loop is what a
  script reaches for the instant it has more than one thing to do.

  The list comes from exactly one source: positional items, which need no
  configuration at all; -p, -s or -g, which iterate over the packages the
  terms name, over the spaces themselves, or over the versioning groups
  themselves; or --changed, --unchanged and a bare --since, which iterate
  over the window every sweeping command covers and over its complement.
  Under a window source the same three selection flags stop being the
  source and become the narrowing they are everywhere else, exactly as
  they compose for `dispat if --changed`.

  Every iteration exports DISPAT_ITEM, DISPAT_INDEX and DISPAT_TOTAL,
  appended last so nothing an item or an enclosing run carries can shadow
  them, plus the release environment's own names for what the item is:
  DISPAT_PACKAGE, DISPAT_SPACE, DISPAT_DIR and DISPAT_GROUP. A script
  therefore moves between a release stage and a loop unchanged.

  Like `dispat if`, the command conditionally requires configuration: a
  literal list reads nothing and starts no update check, and every source
  that asks about the monorepo defers to the configured phase, where the
  loop body also picks up the configured shell -- which is the whole point
  of the command.

- the release environment names the versioning group ([a06b54f](https://github.com/yohimik/dispat/commit/a06b54fcbb338428c13a913a21081e5f7cbc44c6)) (by yohimik, Claude Fable 5)
  DISPAT_GROUP is the package's third address, beside its name and its
  space: a script that has to know which packages move together with this
  release could not derive it from the other two, since a group may span
  spaces and a space may version independently. It is rendered from the
  package's own group -- a declared versionGroups entry, or the space's
  name where a space versions as one -- and left unset rather than empty
  for an independently versioned package, the same unset-not-empty
  convention DISPAT_COUNTER keeps.

- the step environment carries each update's tag ([ed151aa](https://github.com/yohimik/dispat/commit/ed151aa3812eaca304f224f93146864ac82573a3)) (by yohimik, Claude Fable 5)
  A nested step command that aligns its updates to the run rebuilt them
  from the environment without their tags, and the renderer rightly
  declines an auto dependency link it cannot spell, so an aligned record
  rendered plain lines where the run's own record linked them. The
  listing now writes DISPAT_UPDATED_<KEY>_TAG beside the name and the
  versions, the step reader carries it back, and absence stays legal: a
  parent run predating the variable leaves the tag empty and the decline
  applies as before. Tags alone are never drift, because a tag mismatch
  is not a movement mismatch and either side of the alignment rendered
  its tags through the same formats.

### Fixes

- the loop speaks at the level if does ([a710926](https://github.com/yohimik/dispat/commit/a710926db91b69bc2159fade9867d73435576fdf)) (by yohimik, Claude Fable 5)
  The shell helpers are glue, and if says everything at debug: a chosen
  branch, an empty result, nothing at info. for's summary and its empty
  list said the same kinds of things one level louder, so a quiet pipeline
  gained lines its scripts did not write. Both drop to debug; the
  --require-items refusal stays an error, because a refusal is not
  narration.

- every source package of a unit reaches DueTo ([c7bfcb5](https://github.com/yohimik/dispat/commit/c7bfcb57978e41752f703c187a38b49ef3c5cf44)) (by yohimik, Claude Fable 5)
  A unit written over several packages propagated its bump correctly but
  credited only the package the traversal happened to arrive from: the
  walk visits a target once, and the first source out of the queue was
  the one recorded. A consumer of all of them was told it releases
  because of one, and the miscredit was not cosmetic. A catch-up record
  reaches a provider that is not releasing only through the attribution,
  so the dependencies section listed one movement and silently dropped
  the rest, and a release explained by a releasing source was labelled a
  catch-up whenever the credited source happened to be the one already
  shipped. Section 9.2 attributes the whole source set, prov[d] |=
  sources, and the plan now records one contribution per source package.

### Dependencies

- [models](https://github.com/yohimik/dispat/releases/tag/pkg/models/v1.6.0): 1.5.0 -> 1.6.0

### Authors

- yohimik
- Claude Fable 5


## services/dispat/v1.5.0 (2026-08-31)

### Features

- sections, links and refs on release records ([b14b1f3](https://github.com/yohimik/dispat/commit/b14b1f3ae83f1963a76c80eea8d8de9c4ddb3992)) (by yohimik, Claude Fable 5)
  An entry could say four things it had no way to say. It can now.

  `sections` states the whole render order of an entry, the built-ins and
  sections of its own together, and a custom section claims commit types out of
  the bump-keyed grouping. A section may declare the bump its types carry, which
  merges into the parser's one type table, so declaring the section that renders
  `add` is what makes an `add` commit release at all. Breaking always wins the
  grouping: letting `add(x)!:` sit under "Added" would hide the one thing a
  reader scans an entry for. A built-in the list omits is appended after the
  listed ones rather than dropped, because a section removed in silence takes
  released work out of the record with it. The bump belongs to the root file,
  since the parser is built once for the repository, and a folder's own config
  file is refused with the reason rather than left with a section nothing reaches.

  `dependencyLink` links a dependency line to the release the provider moved to,
  and `commitRefs` names the commit behind each entry line. Both take a URL
  template or `auto`, which derives github.com's own URLs from the package's
  owner and repo, and both fall back to the plain text rather than to a link that
  leads nowhere: a record is published and permanent, and there is no later run
  in which a broken link comes out right. The provider's tag is rendered by the
  plan through the provider's own tagFormat, and a unit the planner has no sha
  for is left unreferenced and reported once per release under W240.

  `noChangesText` replaces the sentence an entry with nothing to group carries.
  It falls back to the built-in sentences when it expands to nothing, because an
  entry that renders empty reads as a broken write.

  The renderer moves into sections.go with one change of shape that is a fix
  rather than an option: a commit body is indented two spaces under its bullet,
  so the paragraphs after the first stay inside the list item instead of ending
  it, and every section now closes on exactly one newline whether or not its last
  line carried a body.

- unknown config keys hint at self-update ([947c3e5](https://github.com/yohimik/dispat/commit/947c3e56af8d37274bb8231ee8cc52b84cb46229)) (by yohimik, Claude Fable 5)
  A key the loader has no field for is usually a typo, and pkg/config already
  says so. The cause it cannot know about is a configuration written for a newer
  dispat than the one reading it: the key is real, in a schema this binary
  predates, and the file is right while the binary is behind. An operator who
  reads only "unknown key" spends the next minutes hunting a spelling mistake
  that is not there.

  Every place dispat surfaces a config load failure, the root file and a folder's
  own package or space file alike, now appends the other explanation and names
  the command that answers it, `dispat self-update --check`. The hint is dispat's
  rather than the library's, because pkg/config knows nothing about dispat's
  releases or how one updates, and it wraps rather than replaces, so errors.Is
  and errors.As still reach what the loader reported.

### Fixes

- record rendering hardened after review ([24cf9b8](https://github.com/yohimik/dispat/commit/24cf9b83f7c7146227c013c705877fe737d118e4)) (by yohimik, Claude Fable 5)
  A review pass over the new record features closed what it found. An entry
  whose file title renders empty for the release keeps the file's own head
  instead of writing above it, and a preamble containing a fenced example is
  split after the fence rather than inside it. An auto link declines when the
  step environment aligned the updates without their tags, when the resolved
  API URL points outside github.com, and when the repository pair is only half
  configured; the half pair is no longer completed from the environment, and
  the completion itself moved from the renderer to the recorders, so rendering
  no longer reads the process environment at all. A record link inherited from
  a broader layer can be declined with the value off. A no-changes text that
  expands to nothing falls back to the built-in line and says so as W241, and
  one carrying a thematic break on any line is refused at load. The auto base
  is derived once per entry rather than once per line.

- the dependencies section stays a tight list ([de93ba8](https://github.com/yohimik/dispat/commit/de93ba8a80c9543bb55475119e42e681232bc6ab)) (by yohimik, Claude Fable 5)
  The loose joining the sections gained for their bullet-and-body items had
  reached the dependencies list, and a multi-provider entry rendered with a
  blank line between movements. The section is a table: its lines never carry
  bodies, and every changelog written so far renders them as one block, so the
  tight joining is restored there alone. The command pages state the exception.

- entry spacing and adopted changelog preambles ([7e71494](https://github.com/yohimik/dispat/commit/7e714946cc4c09b0d3d899610fe54b7260b823e6)) (by yohimik, Claude Fable 5)
  Two things the changelog writer got wrong, both of them about bytes it did not
  write.

  The seam between one entry and the next was whatever the entry above it
  happened to end with, so a release whose last section was a dependencies list
  spaced differently from one that ended on a bullet with a body, and a file
  recorded the shape of each release rather than one rule. The writer now closes
  an entry on exactly one newline and writes exactly `changelog.entrySpacing`
  blank lines below it, two by default. Only the seam is written: the entries
  underneath keep the spacing and the line endings they were published with.

  Adoption was the sharper one. A file that does not open with the title dispat
  renders was prepended to, which published a second H1 over the file's own and
  pushed YAML front matter off the head of the file, where it stops being front
  matter at all. Everything above the first entry heading is now the file's
  preamble: it stays at the top, the new entry goes in below it, and dispat's own
  title is never written into a file that already has one. A byte-order mark is
  carried through at the very head and cut before the title match and the
  entry-exists check, and a title terminated with CRLF is matched line by line,
  because a title the strip fails to see is a title written twice.

### Dependencies

- [config](https://github.com/yohimik/dispat/releases/tag/pkg/config/v1.0.0): 0.0.0 -> 1.0.0
- [models](https://github.com/yohimik/dispat/releases/tag/pkg/models/v1.5.0): 1.4.0 -> 1.5.0

### Authors

- yohimik
- Claude Fable 5


## services/dispat/v1.4.0 (2026-08-30)

### Features

- minified linux binaries ride the release
A release now carries dispat-tiny-linux-amd64 and
dispat-tiny-linux-arm64 beside the six it always has: the same source
and the same version stamp, built by the TinyGo fork at 0.42.0-net.4,
at roughly 60% of the bytes. They are additive downloads under names
of their own, so dispat-<os>-<arch>, which self-update and install.sh
resolve, is untouched.

The toolchain arrives as its release tarball by URL and against the
digest that release published, not through dispat install: a build of
dispat that needs a working dispat to start is a bootstrap cycle. The
checksum is the part install would have done, kept. Debian rather
than alpine, because the fork ships a glibc-linked LLVM and musl does
not run it; what comes out is static either way.

A TinyGo binary carries no Go build info, so the tiny pair is proven
by running it rather than by reading it back, which the smoke loop
now does for all four linux binaries. The spike stays the deep gate,
and internals/tinygo.md carries what those binaries can do.
- the loaded configuration says what it holds
The post-load debug line said which file was read and which folder
became the root, which answers "did it read the file I meant" and
nothing after it. A configuration that read as almost nothing — a $ref
resolving to an empty fragment, a spaces object under a key nobody
meant — still looks like a run that simply found no work.

So the line now counts what the loader made of the file: the package
entries it names, the scripts it binds and the webhooks it notifies,
across every level of the root file that may declare one. In-folder
files are deliberately not counted; they are read later, and a number
that grew afterwards would describe a configuration nobody had yet.
- first-party config decoder
The config language is a table now: one entry per key a file may write,
saying what writing it does. A key with no entry is a key the model has
no field for, so the unknown-key refusal every typo lands in is
structural rather than a setting somebody remembered to turn on.

What this replaces is a reflected decoder told its exceptions through
hooks that fire on a Go type and cannot see the key that produced it.
The hazard was never hypothetical: the conversion lifting a scalar into
a list splits it on commas, which is right for a list of script names
and wrong for a shell command, and the two were kept apart only by the
order the hooks were composed in. They are different setters here, so
the order that used to matter cannot exist.

Nothing calls it yet. fields_test.go reads the models' own json tags and
refuses any disagreement with the tables in either direction, and
decode_parity_test.go runs a corpus through both decoders and fails on
any difference the migration did not declare.
- the command that installs a tool is called install
The word says what the command leaves behind rather than how it gets
there: `dispat install <repo>` puts a verified release asset on PATH,
and download was the mechanism, not the outcome. The flags, the
behavior and the machinery are unchanged; the command word, the
package (internal/install), the error prefixes and the report texts
follow the new name, and every doc page moves with it.

`install` permanently shadows a run script of the same name, as every
command word does; a script called install stays reachable as `dispat
run install` and from flow sequences, which the example configs use it
in.
- download installs a tool from any github release
dispat download <repo> is self-update pointed at somebody else's
repository: the same listing walk, the same streamed download checked
against the published size and checksum, the same two renames that keep
what they replace. It needs no config file and no git repository.

The repository is named however it is at hand, and a host that is not
github.com derives a GitHub Enterprise endpoint. --asset says which of
the release's files is the binary, as a name or a glob, with {os},
{arch}, {version}, {tag} and {name} expanded; nothing is guessed, since
the wrong guess is installed globally and run. --bin-dir and --as say
where it goes and what it is called, defaulting to the ladder install.sh
climbs. --pipe hands the verified file to a command in that folder
instead, which is how an archive is unpacked and a release's own install
script is run.

The destination is hashed against the release's digest, so the command
is idempotent: --check gates on it and --force installs over it.
--rollback restores what the last download replaced. GITHUB_TOKEN is
sent to github.com alone, because the endpoint comes from an argument.

### Fixes

- a record line is read after it is decoded
A record line written as an object went through `return line,
decodeObject(item, at, entryLineFields(&line))`, which leaves the order
of copying line and running the call to the compiler. gc runs the call
first and returns the filled line; TinyGo copies line first and returns
it empty, so every object-form footer, header and fileTitle decoded to
nothing and the load failed with "line is required". Found by the
0.42.0-net.2 validation run; no assertion can catch it under gc, so the
sequencing comment is the guard.

### Dependencies

- models: 1.3.0 -> 1.4.0

## services/dispat/v1.3.1 (2026-08-28)

### Fixes

- catch-up records span the provider's movement
A catch-up picks up a provider that published in an earlier run, so by the
time its records are written the provider's own before-and-after have
collapsed onto the published version — and the changelog entry, the GitHub
release body and the DISPAT_UPDATED_* variables all said "1.3.0 -> 1.3.0",
a movement line with no movement, which is what the docs leg of the 1.3.0
release shipped. From is now what the package's previous release shipped
against, reconstructed off the provider's tags at the package's own
baseline, the same way a graduation spans its train; the step commands
inherit the span through the plan they recompute.

A ride catching up documents the movement it rode for by the same
reconstruction: its provider is not releasing and nothing propagated, so
the record loops found nothing at all, and the ride's entry stayed silent
about the one thing it existed to ship — where the same ride in a
single-run release names the provider's movement. An own-cause release's
manifest-only pickup stays out of the record, as it always has.

## services/dispat/v1.3.0 (2026-08-28)

### Features

- changelogs and github releases authors


### Dependencies

- models: 1.2.0 -> 1.3.0

## services/dispat/v1.2.0 (2026-08-27)

### Features

- external webhooks


### Dependencies

- models: 1.1.0 -> 1.2.0

## services/dispat/v1.1.1 (2026-08-26)

### Fixes

- window-only run selections without the script no-op


## services/dispat/v1.1.0 (2026-08-20)

### Features

- self-update prints changelog


### Dependencies

- models: 1.0.0 -> 1.1.0

## services/dispat/v1.0.2 (2026-08-19)

### Dependencies

- manifest: 1.1.0 -> 1.1.1
- scanner: 1.1.0 -> 1.1.1
- writer: 1.1.0 -> 1.1.1

## services/dispat/v1.0.1 (2026-08-19)

### Fixes

- manifest libraries updated providing unity, unreal, godot, o3de and defold manifests supported


### Dependencies

- manifest: 1.0.0 -> 1.1.0
- scanner: 1.0.0 -> 1.1.0
- writer: 1.0.0 -> 1.1.0

## services/dispat/v1.0.0 (2026-08-16)

### Breaking Changes

- commit to the 1.0 interfaces

- rename autosubstute to autoreplacer

- restore the 1.0 rc train

- replace the channel sigil with percent


### Features

- the release engine is ready for the stable line

- finalize the workspace for 1.0.0

- the installers explain PATH permanence and shadowing

- debug shows git mutations, trace shows starting scripts

- the run start and the lock answer at info

- warn a github step running before the run's tag

- step commands align to the run's environment

- release polyglot monorepos from conventional commits

- report the lock holder and age in the refusal

- trace scope, propagation and group derivation

- surface .dispatexclude exclusions at debug

- reconcile missing assets on an existing github release

- retry transient github lookups with backoff

- wire multi path spaces downstream

- accept a list of space paths

- report and guard none packages

- exclude none packages from the release plan

- reject releasable deps on none packages

- add versioning none mode

- expand the changed window before narrowing

- wire if changed and file conditions

- add changed selection lookup

- add resolved conditions

- exit 3 when --require-release finds nothing to release

- preview the changelog or github body

- choose the channels lines and records reach

- let a $ref name several files

- suppress the reverted changelog entries

- mark and render the corrected entries

- apply the Edits and Deletes corrections

- gate local links and dependency ranges from the scanner

- one location grammar for exec's subject, script source and folder

- load .env files

- write through a $ref in compute

- resolve $ref in config files

- trace what each script actually ran

- array of scripts, require release

- forward arguments after -- to run and exec scripts

- trace and debug logging for git, config and the plan

- add the autosubstitute command

- autowriter derives edits from the workspace

- change scope ignore rules

- root and space level flags

- space dependencies

- unsafeDisableLock config field

- lock the release command

- alias tags

- force tag writes and pushes

- consumer-keyed dependencies

- version component env vars

- if and exec shell helpers

- commit --tag-name

- allowBranch and behind-remote release guards

- self-update from the latest stable release

- autoreplace rewrites manifests across the selection

- select packages by versioning group

- release and status narrow to the package selection

- a package src path that narrows change detection

- per-command help, a platform in the version, and quiet parser diagnostics

- a github step command, and releases that skip themselves on a re-run

- prerelease opt-out for changelog and github records

- reconcile Docker image tags at the version stage

- fixedMajor and fixedMajorMinor versioning modes

- space packages entries and the space folder config layer

- space file model and .dispatignore over config names

- reconcile with replace rules

- select the autoversion strategy

- declare manifest names per package

- add the replacer command

- select packages with --package and --space

- expose the scanner and writer as commands

- resolve scripts per package across three levels

- changelog, commit and autoversion commands

- build release binaries in dispat flow

- run consumers

- package add

- per-package overrides, version groups, dispatignore

- preview all packages

- shared manifest module, package readmes

- compute auto version

- run since

- export

- preview, init, test, config


### Fixes

- exercise propagation out of the group driver

- exercise a group ride release

- exercise the release pipeline across every package

- the skip cascade reads the fresh changeset, not the train

- a graduation documents the train's provider movement

- config edits prepare every file before writing any

- self-update no longer claims an empty install path

- a record entry is never empty

- a catch-up on a train is still a catch-up

- status counts the fresh changeset, not the train

- a ride with train history still says no changes

- nested dependencies update again x3

- nested dependencies update againx2

- the reason names what forces the release

- a spent blast and a distant origin leave the records

- nested dependencies update again

- nested dependencies update

- dependencies update

- a wired record states the run's provider movements

- the module's go directive matches the workspace

- the installers walk the release listing past page one

- carry the license in every module

- the release commit names only what it records

- an explicit DISPAT_UPDATE_CHECK=1 waits for the answer

- warn when a commit.include path is missing

- refuse ambiguous initials under case-colliding names

- refuse an unselectable if --changed --consumers

- published log names the deferred tag

- pre-config errors respect the log flags

- scale the github upload timeout to the asset

- write changelogs through an atomic replace

- report a correction that reached nothing

- count references followed, not keys walked, when bounding an edit

- name the sparse member that decides a group's major

- run the login in the space folder

- keep alias tags out of the release commit subject

- sync manifests and changelogs for every updated provider

- log every W diagnostic at warn level

- 1.0.0 release blockers

- never abort a run after a release is out

- resolve the known groups once per invocation

- scope the group pin and channel conflicts to a moving group

- keep W222 for rules that actually reached a file

- step over an unreadable folder in a replace rule's walk

- run syncLock without a reconciling strategy

- load ancestry dag once, parallel tag reads

- scheduler fifo, script cancellation, launch determinism

- autoversion and compute correctness

- round 2 blockers

- ccme and models API freeze

- graceful shutdown, ancestry cache, scheduler guard

- 1.0.0 blockers

- badges

- git check


### Dependencies

- ccme: 1.0.0-rc.10 -> 1.0.0
- manifest: 1.0.0-rc.10 -> 1.0.0
- models: 1.0.0-rc.19 -> 1.0.0
- scanner: 1.0.0-rc.10 -> 1.0.0
- writer: 1.0.0-rc.10 -> 1.0.0

## services/dispat/v1.0.0-rc.19 (2026-08-16)

### Features

- the release engine is ready for the stable line


### Dependencies

- ccme: 1.0.0-rc.9 -> 1.0.0-rc.10
- manifest: 1.0.0-rc.9 -> 1.0.0-rc.10
- models: 1.0.0-rc.18 -> 1.0.0-rc.19
- scanner: 1.0.0-rc.9 -> 1.0.0-rc.10
- writer: 1.0.0-rc.9 -> 1.0.0-rc.10

## services/dispat/v1.0.0-rc.18 (2026-08-16)

### Dependencies

- models: 1.0.0-rc.17 -> 1.0.0-rc.18

## services/dispat/v1.0.0-rc.17 (2026-08-16)

### Fixes

- exercise propagation out of the group driver


### Dependencies

- models: 1.0.0-rc.16 -> 1.0.0-rc.17

## services/dispat/v1.0.0-rc.16 (2026-08-16)

### Dependencies

- writer: 1.0.0-rc.8 -> 1.0.0-rc.9
- manifest: 1.0.0-rc.8 -> 1.0.0-rc.9
- models: 1.0.0-rc.15 -> 1.0.0-rc.16
- scanner: 1.0.0-rc.8 -> 1.0.0-rc.9

## services/dispat/v1.0.0-rc.15 (2026-08-16)

### Dependencies

- ccme: 1.0.0-rc.8 -> 1.0.0-rc.9
- models: 1.0.0-rc.14 -> 1.0.0-rc.15

## services/dispat/v1.0.0-rc.14 (2026-08-16)

### Dependencies

- models: 1.0.0-rc.13 -> 1.0.0-rc.14

## services/dispat/v1.0.0-rc.13 (2026-08-16)

### Fixes

- exercise a group ride release


### Dependencies

- models: 1.0.0-rc.12 -> 1.0.0-rc.13

## services/dispat/v1.0.0-rc.12 (2026-08-16)

### Fixes

- exercise the release pipeline across every package


### Dependencies

- ccme: 1.0.0-rc.6 -> 1.0.0-rc.7
- manifest: 1.0.0-rc.6 -> 1.0.0-rc.7
- models: 1.0.0-rc.11 -> 1.0.0-rc.12
- scanner: 1.0.0-rc.6 -> 1.0.0-rc.7
- writer: 1.0.0-rc.6 -> 1.0.0-rc.7

## services/dispat/v1.0.0-rc.11 (2026-08-16)

### Features

- the installers explain PATH permanence and shadowing

- debug shows git mutations, trace shows starting scripts


### Fixes

- the skip cascade reads the fresh changeset, not the train

- a graduation documents the train's provider movement

- config edits prepare every file before writing any

- self-update no longer claims an empty install path

- a record entry is never empty

- a catch-up on a train is still a catch-up

- status counts the fresh changeset, not the train

- a ride with train history still says no changes


### Dependencies

- models: 1.0.0-rc.10 -> 1.0.0-rc.11

## services/dispat/v1.0.0-rc.10 (2026-08-16)

### Fixes

- nested dependencies update again x3


### Dependencies

- models: 1.0.0-rc.9 -> 1.0.0-rc.10

## services/dispat/v1.0.0-rc.9 (2026-08-16)

### Fixes

- nested dependencies update againx2


### Dependencies

- ccme: 1.0.0-rc.5 -> 1.0.0-rc.6
- manifest: 1.0.0-rc.5 -> 1.0.0-rc.6
- models: 1.0.0-rc.8 -> 1.0.0-rc.9
- scanner: 1.0.0-rc.5 -> 1.0.0-rc.6
- writer: 1.0.0-rc.5 -> 1.0.0-rc.6

## services/dispat/v1.0.0-rc.8 (2026-08-16)

### Fixes

- the reason names what forces the release

- a spent blast and a distant origin leave the records


### Dependencies

- writer: 1.0.0-rc.4 -> 1.0.0-rc.5
- manifest: 1.0.0-rc.4 -> 1.0.0-rc.5
- models: 1.0.0-rc.7 -> 1.0.0-rc.8
- scanner: 1.0.0-rc.4 -> 1.0.0-rc.5

## services/dispat/v1.0.0-rc.7 (2026-08-16)

### Dependencies

- ccme: 1.0.0-rc.4 -> 1.0.0-rc.5
- manifest: 1.0.0-rc.3 -> 1.0.0-rc.4
- models: 1.0.0-rc.6 -> 1.0.0-rc.7
- scanner: 1.0.0-rc.3 -> 1.0.0-rc.4
- writer: 1.0.0-rc.3 -> 1.0.0-rc.4

## services/dispat/v1.0.0-rc.6 (2026-08-16)

### Fixes

- nested dependencies update again

- nested dependencies update


### Dependencies

- ccme: 1.0.0-rc.3 -> 1.0.0-rc.4
- manifest: 1.0.0-rc.2 -> 1.0.0-rc.3
- models: 1.0.0-rc.5 -> 1.0.0-rc.6
- scanner: 1.0.0-rc.2 -> 1.0.0-rc.3
- writer: 1.0.0-rc.2 -> 1.0.0-rc.3

## services/dispat/v1.0.0-rc.5 (2026-08-16)

### Fixes

- dependencies update

- a wired record states the run's provider movements


### Dependencies

- ccme: 1.0.0-rc.2 -> 1.0.0-rc.3
- manifest: 1.0.0-rc.1 -> 1.0.0-rc.2
- models: 1.0.0-rc.4 -> 1.0.0-rc.5
- scanner: 1.0.0-rc.1 -> 1.0.0-rc.2
- writer: 1.0.0-rc.1 -> 1.0.0-rc.2

## services/dispat/v1.0.0-rc.4 (2026-08-16)

### Breaking Changes

- commit to the 1.0 interfaces


### Features

- the run start and the lock answer at info

- warn a github step running before the run's tag

- step commands align to the run's environment

- release polyglot monorepos from conventional commits


### Fixes

- the module's go directive matches the workspace

- the installers walk the release listing past page one

- carry the license in every module

- the release commit names only what it records

- an explicit DISPAT_UPDATE_CHECK=1 waits for the answer


### Dependencies

- ccme: 1.0.0-rc.1 -> 1.0.0-rc.2
- manifest: 1.0.0-rc.0 -> 1.0.0-rc.1
- models: 1.0.0-rc.1 -> 1.0.0-rc.4
- scanner: 1.0.0-rc.0 -> 1.0.0-rc.1
- writer: 1.0.0-rc.0 -> 1.0.0-rc.1

## services/dispat/v1.0.0-rc.3 (2026-08-09)

### Fixes

- load ancestry dag once, parallel tag reads


## services/dispat/v1.0.0-rc.2 (2026-08-09)

### Breaking Changes

- replace the channel sigil with percent


## services/dispat/v1.0.0-rc.1 (2026-08-09)

### Features

- changelog, commit and autoversion commands


## services/dispat/v1.0.0-rc.0 (2026-08-09)

### Breaking Changes

- initial release

