# Changelog

## services/dispat/v1.11.0-rc.8 (2026-10-25)

### Fixes

- forget the packages memo with the discovery it was read from ([079f03f](https://github.com/yohimik/dispat/commit/079f03f1311b89f0f27a5e8bfbeac3366f5379e3)) (by yohimik, Claude Fable 5.1)
  forgetDiscovery reset the remembered workspace walk after compute
  rewrote the files it was read from, but not the packages memo built from
  the same walk, so the two answers of one App could disagree after a
  compute that wrote. Both are forgotten together now.

- list the control history as commits only once a window reads it ([298f31e](https://github.com/yohimik/dispat/commit/298f31e5cecfb4d4e230fe119f98fbc16b8a8afa)) (by yohimik, Claude Fable 5.1)
  615f9882 listed the control inventory as commits whenever its parent
  graph was indexed, so a run that reads checkpoints and no control window
  paid for a listing nothing read. The listing is now built on the first
  window read and shared by every window after it.

  BenchmarkControlCheckpointPersistentSnapshots and BenchmarkControlWindows,
  -benchmem -benchtime 20x -cpu 1, same machine:

                                       B/op      allocs/op
    169cf108 (before 615f9882)         463280    8032      checkpoints
    615f9882                           709040    8033      checkpoints
    this commit                        463280    8032      checkpoints
    169cf108 (before 615f9882)             64    12356637  windows
    615f9882                               64    6725402   windows
    this commit                            64    6725404   windows

- repair an answered-work record changed outside or cut by a crash ([f771956](https://github.com/yohimik/dispat/commit/f77195625efb0460b14da0ab582ffac811950abc)) (by yohimik, Claude Opus 5.5)
  Review blocker on dda1be5f. Appending over the closing brace trusted that
  seen.json was still the file this process last wrote, and a process killed
  mid-append left an object without its closing brace. Either way the next
  `dispat worker` refused to start until someone deleted the record, which
  gives up replay protection; the old whole-file rewrite healed both.

  An append now checks the opened file first: the same file (identity),
  the same size and modification time this process last left it at, and a
  closing brace where the append starts. Anything else is a change from
  outside and the claim rewrites the file whole through a temporary file
  and a rename, as every claim did before the append. LoadSeenSet reads a
  file that ends inside its object, with nothing before the end that a
  reader would refuse, as the members before the cut: the member being
  written belonged to a Record that never returned, so its attempt never
  ran, and dropping it is safe. A file broken anywhere else, or one that
  ends at its opening brace (no append can leave that), is still refused.
  The next claim rewrites the file whole. A release older than the append
  cannot read a cut file; the docs say so.

  Record's comment no longer claims a record is never half there, and the
  no-op for an already-known triple says why it is correct.

  Failure behaviour that differs from the base on purpose: a claim succeeds
  where only the temporary file or the folder is unwritable and seen.json
  itself is writable, because an append needs neither (dda1be5f).

  Tests: TestSeenSetRecordRepairsAFileChangedOutside (emptied in place,
  replaced by a file of its size, edited in place to its size, cut short)
  fails on dda1be5f..80227e39 in three of its four cases.
  TestSeenSetLoadReadsARecordCutByACrash cuts an append at every byte and
  fails there too. TestExecutionWorkerReadsAnAnsweredWorkRecordACrashCut
  (integration, EXE-52) fails with the binaries of 1bfc3967 and c7b14d32.

  testreport bench, ./internal/execution, -bench '^BenchmarkSeenSetRecord'
  -benchmem -benchtime=10000x, darwin/arm64 Apple M5, shared machine:
  allocs/op and B/op are stable, ns/op indicative. The after column is this
  commit (tree a77c7ff9); the cost is the two fstat calls and the one-byte
  read that make the append safe.

  | benchmark     | revision  | allocs/op | B/op    | ns/op   |
  |---------------|-----------|-----------|---------|---------|
  | Record        | 80227e39  | 19        | 2370    | 17500   |
  | Record        | this      | 21        | 2787    | 27659   |
  | RecordPruning | c7b14d32  | 5022      | 1279911 | 1236039 |
  | RecordPruning | 80227e39  | 30        | 3707    | 17878   |
  | RecordPruning | this      | 32        | 4123    | 27917   |

- ask git for a repository's index path once per run ([11e01c7](https://github.com/yohimik/dispat/commit/11e01c7719ead344b0f152b1b2d9a5bd2be0c6a7)) (by yohimik, Claude Opus 5.5)
  Every snapshot capture ran `git rev-parse --git-path index` before
  copying the index, under the exclusive snapshot guard. Where a
  repository keeps its index does not move during a run (the folder, the
  git directory and the process environment are fixed), so the capture
  memory now keeps the answer per repository folder. A failed lookup is
  not kept and is asked again, and the index is still copied fresh on
  every capture: the copy is the input the snapshot is taken from, which
  is why the rest of the finding stays declined.

  Finding: "Every snapshot capture re-asks the index path and copies the
  whole index inside the exclusive guard" (snapshot.go:127), refuted 0/3
  on size and, for reusing the index, on equivalence; all three votes name
  the index-path cache as the part that changes no output.

  BenchmarkSnapshotCaptureUnchanged, a small repository, -benchtime=200x,
  darwin/arm64 (Apple M5), shared machine: git invocations and allocations
  are the stable figures, ns/op is indicative only. snapshot.go is
  unchanged from c7b14d32 at the before revision.

  | metric             | before (dda1be5f) | after (this commit) |
  |--------------------|-------------------|---------------------|
  | git-invocations/op | 3                 | 2                   |
  | allocs/op          | 304               | 213                 |
  | B/op               | 96624             | 75231               |
  | ns/op              | 81328612          | 37833511            |

  TestSnapshotAsksForTheIndexOncePerRepository pins the two processes of
  a repeated capture and fails on the previous code with three.

- append to the answered-work record instead of rewriting it ([3d7e075](https://github.com/yohimik/dispat/commit/3d7e0758fe42ceb18d97c94bd34b7a6b0199c590)) (by yohimik, Claude Opus 5.5)
  SeenSet.Record scanned every entry to prune, marshalled the whole map
  with sorted keys and replaced the file on every claim: O(S log S) time
  and O(S) bytes written per claim, S being the attempts accepted in the
  48-hour horizon. A claim now appends its record, and the records of the
  triples it pruned, over the file's closing brace in one positioned write;
  the file is rewritten through a temporary file and a rename only when it
  holds twice the live records, after a load, or when an append fails.
  Pruning pops an expiry heap instead of scanning the map.

  The file stays one JSON object from triple to time, so the current
  reader (and an older dispat) reads it: a repeated name keeps its last
  value, which is the newest time, and a pruned triple is appended with
  the zero time, which every horizon drops. That keeps a reload equal to
  the old rewritten file even when the wall clock is stepped back before
  a restart.

  Equivalence: IsSeen answers what the whole scan answered after every
  call. Times with a monotonic reading and times without one wait in
  separate heaps, because Before compares them by different clocks, and a
  cutoff without a monotonic reading filters the monotonic heap whole. A
  randomized comparison against the old algorithm (300 seeds x 400 records,
  clocks stepping back and forth, mixed monotonic and wall-only times, and
  reloads at random horizons) agreed on every IsSeen answer and every
  reloaded entry.

  Finding: "SeenSet.Record rewrites the whole answered-work record on
  every claim" (state.go:460).

  BenchmarkSeenSetRecord, 10000 answered attempts, -benchtime=2000x,
  darwin/arm64 (Apple M5), shared machine: allocations are the stable
  figure, ns/op is indicative only. The after column includes the one
  rewrite a loaded record pays on its first claim.

  | metric    | before (c7b14d32) | after (this commit) |
  |-----------|-------------------|---------------------|
  | allocs/op | 11028             | 38                  |
  | B/op      | 3783854           | 6244                |
  | ns/op     | 2793414           | 49721               |

  TestSeenSetRecordAppendsInPlace fails on the previous code (the file
  was replaced on every claim); TestSeenSetFileStaysWithinTwiceItsLiveRecords
  pins the file's size to twice the live records over a week of claims.
  TestSeenSetRecordPrunesWithoutRestart now checks the pruned triple
  through a reload instead of its absence from the raw file.

  The file crosses the process boundary, so two integration tests follow
  it. TestExecutionWorkerPrunesAnsweredWorkWhileServing reads the expired
  triple as forgotten (its last value is the zero time) rather than
  absent, and TestExecutionClaimWithoutDurableReplayRecordCannotPublish
  blocks the in-place append as well as the temporary file, since a
  claim no longer needs the temporary file to persist; the test plan
  rows say so.

- install a validated output set from the objects its validation read ([9ea4b7a](https://github.com/yohimik/dispat/commit/9ea4b7af9a8ef2f94855510bb7c2430e906ab706)) (by yohimik, Claude Opus 5.5)
  Validation walks the set's tree with one ls-tree and discarded each
  entry's object id; installation then ran a second ls-tree of the same
  immutable tree into a path->oid map, and built a third map of every
  entry path only to learn which declared roots an entry names. The
  validating walk now records the object of each entry, aligned with the
  manifest's entries, ValidateOutputs returns them, and the orchestrator's
  admission and the worker's input installation hand them to
  InstallOutputs. The roots check costs the roots instead of the entries.
  An install without validated objects (the sweep merge) lists the tree as
  before.

  Equivalence: a passing validation proves a one-to-one match between the
  tree's entry names and the manifest's paths, so the recorded ids equal
  the second listing's; the loops that write files or diagnostics still
  run over the manifest's slices, and every refusal keeps its place.

  Finding: "Install re-lists the output tree that validation just walked,
  and builds a third O(E) map" (install.go:147). Outside the group's files
  this touches manifest.go (ValidateOutputs) and taskoutputs.go (the
  worker's caller).

  BenchmarkAdmitOutputsFromAnotherNode, 1000-file sets, -benchtime=30x,
  darwin/arm64 (Apple M5), shared machine: git invocations and allocations
  are the stable figures, ns/op is indicative only.

  | metric             | before (e1a44c23) | after (this commit) |
  |--------------------|-------------------|---------------------|
  | git-invocations/op | 4                 | 3                   |
  | allocs/op          | 34137             | 32017               |
  | B/op               | 5356225           | 4748740             |
  | ns/op              | 254252692         | 279043119           |

  TestAdmittingAnotherNodesSetListsItsTreeOnce pins the three processes
  and fails on the previous code with four.

- keep only the name and totals of an admitted output set ([aa6bdcb](https://github.com/yohimik/dispat/commit/aa6bdcb7d1f6040a9eed923e24dc9aaa681c744f)) (by yohimik, Claude Opus 5.5)
  The run's record of admitted output sets kept each whole manifest,
  every entry of every package included, until the run ended. Nothing
  reads the entries after admission: validation and installation both
  run inside admitOutputs, and every later reader (input resolution,
  relays, local inputs, publication inputs, the task summary) uses only
  the task, package, digest, output tree and the two totals. The record
  now keeps exactly those, so it costs O(P) words instead of
  O(sum of entries) for the run's life.

  Finding: "Admitted output manifests are retained whole, entries
  included, for the life of the run" (admission.go:166).

  BenchmarkAdmitOutputsFromAnotherNode, 1000-file sets, -benchtime=30x,
  darwin/arm64 (Apple M5), shared machine: allocations and retained bytes
  are the stable figures, ns/op is indicative only. retained-B/op is the
  heap still held per admitted set after the admission returned, so a
  value near zero is within the measurement's noise.

  | metric             | before (c7b14d32) | after (this commit) |
  |--------------------|-------------------|---------------------|
  | retained-B/op      | 214635            | -4139 (~0)          |
  | git-invocations/op | 4                 | 4                   |
  | allocs/op          | 34137             | 34137               |
  | B/op               | 5309435           | 5356225             |
  | ns/op              | 330162739         | 254252692           |

- warn about unmatched initials entries in key order ([2cc1f24](https://github.com/yohimik/dispat/commit/2cc1f247fe358bd54c5a14e6211da102f4e66e07)) (by yohimik, Claude Opus 5.5)
  Review note (pre-existing): initialVersions and workspaceInitialVersions
  warned once per initials entry naming no package while ranging over the
  InitialVersions map, so with two or more such entries the order of the
  warning lines followed Go's randomised map iteration. The unmatched keys
  are now collected and sorted before they are reported. The entries that do
  match are applied as before, and nothing extra is done when every entry
  matches.

  TestUnmatchedInitialsWarnInKeyOrder (eight unmatched keys, single history
  and composed workspace) failed 6 of 6 subtests over three runs on the old
  code.

  BenchmarkPolyrepoInitialVersionIndexes (every entry matches, -benchtime
  200x, darwin/arm64; allocations are the stable figure on a shared machine):

  | size  | 76cc7dbe allocs/op | B/op    | this commit allocs/op | B/op    |
  |-------|--------------------|---------|-----------------------|---------|
  | 100   | 226                | 40673   | 226                   | 40682   |
  | 1000  | 2050               | 686254  | 2050                  | 686322  |
  | 10000 | 20196              | 5573188 | 20196                 | 5573223 |

- let compute read the workspace walk its invocation made ([41388d0](https://github.com/yohimik/dispat/commit/41388d0bcc31be696703d78f31041363fbb0299e)) (by yohimik, Claude Opus 5.5)
  Review suggestion on dda4dcbe: Compute still ran its own
  config.DiscoverWorkspacePackages walk, and its baseline reading walked
  again through a.packages(). Both are the unvalidated discovery a.discovery()
  already remembers per App, so Compute now reads that one walk. It keeps the
  same packages, declared edges and error ("package discovery failed"), and
  nothing in compute writes into the packages or the declared list; discovery
  logs nothing, so no log line changes.

  Once compute has written, the remembered walk no longer matches disk, so
  Compute forgets it (forgetDiscovery) after applying, the way
  alignDeclaredList keeps a.cfg in step. A later Compute or planOptions on
  the same App then walks the rewritten files, as each did on c7b14d32; the
  CLI exits after compute, so no command's output changes. The memoised
  packages() answer is left as it was, as c7b14d32 left it. Without the
  forget step the converging second runs of TestComputeWriteAppliesAndBacksUp,
  TestComputeWriteRewritesTheRootObject,
  TestComputeWriteKeepsEdgesWhereTheConsumerDeclaresThem and
  TestComputeWriteEditsASpaceObjectInPlace fail.

  BenchmarkComputePreview (fresh App per op, 200 untagged packages, preview
  only, -benchtime 20x, darwin/arm64). Allocations are the stable figure; the
  machine is shared with other agents, so ns/op is indicative only (two runs
  at each of the last two revisions are shown).

  | revision           | ns/op                  | B/op    | allocs/op |
  |--------------------|------------------------|---------|-----------|
  | c7b14d32 (base)    | 470060156              | 8098561 | 52197     |
  | 7dbfe26b (before)  | 35296962 / 139088235   | 4036767 | 30749     |
  | this commit        | 71133819 / 113595546   | 3083942 | 25019     |

  The base column also carries the per-package tag reads cd09e746 removed.
  TestComputeReadsTheDiscoveredWorkspace fails on 7dbfe26b: a package folder
  added after the invocation's walk is found by Compute's second walk.

- read each package's own tags when compute's inventory fails ([d23cc65](https://github.com/yohimik/dispat/commit/d23cc651955b12627c2fedf098e50148508a9998)) (by yohimik, Claude Opus 5.5)
  Review of cd09e746 found that a failed repository tag inventory was handed
  to every candidate of that repository. The warning "cannot read the release
  tags; no baseline suggested" then carried the unfiltered inventory command
  instead of the package's own `git tag --list ... <glob>`, and one malformed
  inventory record withheld every package's baseline where before only the
  packages whose glob reached it lost theirs.

  When the inventory read fails and the context is still live, each candidate
  now asks its own per-package listing, exactly the call compute made before
  the inventory. The answers, the error texts and the set of withheld
  packages are the old ones; the extra reads happen only on that error path,
  so the success path keeps its one inventory per repository.

  TestComputeWithholdsBaselinesWhenReleaseTagsCannotBeRead pinned the old
  per-package reading (2 tag listings). It now asserts 3 (one inventory, then
  one listing per package) and that each package's warning names its own
  glob. On cd09e746 it fails with 1 listing and the inventory command in both
  errors.

- order a task's output providers by rank, not by the whole plan ([77fb130](https://github.com/yohimik/dispat/commit/77fb130bb36738ab77725828c0cc061b5e96059a)) (by yohimik, Claude Opus 5.5)
  resolveOutputProviders filtered each task's provider closure by walking
  the whole plan order, so every Inputs call paid O(P) however small the
  closure, O(P^2) over a distributed run's tasks. The plan order's ranks are
  read once per dispatch, and each call sorts its own closure by them: the
  same providers in the same plan order, for O(|C| log |C|) per call. The
  per-name memo the finding proposed is not added: it removes only the two to
  four calls per task (a constant, as the complexity vote showed) at the cost
  of a mutex-guarded map shared by the dispatch goroutines.

  Finding: "Distributed run recomputes each task's transitive provider closure
  three times per task" (execution_dispatch.go:67), acted on as the complexity
  vote re-derived it: the term that did not belong was the plan-order scan.

  BenchmarkResolveOutputProviders (2,000 packages in chains of ten, every
  package asked once), -benchtime 20x, same machine (darwin/arm64, shared with
  other jobs: allocations are the stable figure, time is indicative; the
  added bytes are the rank index, built once per plan):

  | metric    | c7b14d32 (before) | this commit (after) |
  |-----------|-------------------|---------------------|
  | ns/op     | 71161690          | 4239190             |
  | allocs/op | 2400              | 2409                |
  | B/op      | 462937            | 571887              |

- bucket declared edges once when compute rewrites lists ([d50a7b5](https://github.com/yohimik/dispat/commit/d50a7b56bcd3fb0d04d57f27078663b17fbc41c4)) (by yohimik, Claude Opus 5.5)
  collectDepEdits rebuilt every touched dependency list by scanning the whole
  merged declaration list and building each entry's list key again, once per
  touched list: L x E key strings. One pass over the declarations now hands
  each entry to its own list's edit, if that list is touched, so each list's
  surviving entries come out in declared order as before and the edits, their
  order and the rendered lists are unchanged.

  Finding: "compute --write rebuilds every touched dependency list by
  rescanning all declared edges" (computedeps.go:384).

  BenchmarkCollectDepEditsPerPackageLists (300 packages, ten providers each in
  their own folder file, one removal per package), -benchtime 20x, same
  machine (darwin/arm64, shared with other jobs: allocations are the stable
  figure, time is indicative):

  | metric    | c7b14d32 (before) | this commit (after) |
  |-----------|-------------------|---------------------|
  | allocs/op | 935078            | 37511               |
  | B/op      | 133283165         | 3784767             |
  | ns/op     | 57866190          | 6967590             |

- read a changelog once per entry and scan it without splitting ([481133d](https://github.com/yohimik/dispat/commit/481133d9e96ac9ce0c923608586b6e53209eecb8)) (by yohimik, Claude Opus 5.5)
  The changelog step read each package's CHANGELOG.md twice, once through
  HasEntryFor to learn whether Record would skip and once inside Record, and
  each IsEntryPresent copied the file into a string and split it into a slice
  of every line. The step now asks FileWriter.RecordEntry, which is Record
  reporting whether it wrote, so the file is read once; HasEntryFor had no
  other caller and is gone. IsEntryPresent finds the marker at the head or
  after a newline in the bytes as read, which is the line-by-line answer
  (TestHasEntryMatchesTheLineByLineReading checks it, a newline in the tag
  included), and TestIsEntryPresentDoesNotCopyTheFile pins its allocation
  below 1 KiB on a 2 MB file (the old code allocates about 4.6 MB). The
  rewrite is assembled in one buffer sized for the file instead of a doubling
  builder copied into a byte slice. The skip decision, the W226 line, the
  "changelog written" line and the bytes written are unchanged.

  Finding: "`dispat changelog` reads and line-splits each CHANGELOG.md twice
  per package" (changelog.go:144). Outside the group's files:
  services/dispat/internal/changelog/changelog.go (RecordEntry,
  IsEntryPresent, the rewrite buffer).

  Benchmarks on a 2 MB changelog, -benchtime 50x, same machine (darwin/arm64,
  shared with other jobs: allocations and bytes are the stable figures, time
  is indicative):

  | benchmark / metric                          | c7b14d32 (before) | this commit (after) |
  |---------------------------------------------|-------------------|---------------------|
  | ChangelogStepSkip B/op                      | 13525851          | 2112640             |
  | ChangelogStepSkip allocs/op                 | 120               | 97                  |
  | ChangelogStepSkip ns/op                     | 5224854           | 136845              |
  | IsEntryPresentAbsent B/op                   | 4653174           | 16                  |
  | IsEntryPresentAbsent allocs/op              | 2                 | 1                   |
  | IsEntryPresentAbsent ns/op                  | 1949603           | 903193              |
  | RecordWritesAboveALargeChangelog B/op       | 13085042          | 6325892             |
  | RecordWritesAboveALargeChangelog allocs/op  | 132               | 125                 |
  | RecordWritesAboveALargeChangelog ns/op      | 6079542           | 6664843             |

- resolve --since from the inputs the plan was computed from ([6e9f96b](https://github.com/yohimik/dispat/commit/6e9f96b8fcc877f3f7f6a4f19ef5d5acf2fd5e63)) (by yohimik, Claude Opus 5.5)
  sincePackages assembled the planner inputs a second time to ask
  PackagesChangedSince which packages rev..HEAD addresses: another
  discovery, another `git remote get-url` per source repository in a composed
  workspace, and a second pass of the initials warnings. It now reads the
  inputs plan() kept (plannedOptions), assembling them only when no plan has
  been computed. PackagesChangedSince reads the same packages, edges, parser
  and histories, so the selection is unchanged; the duplicate uncoded
  "initials entry matches no discovered package" warning and the second set
  of workspace debug lines are no longer written.
  TestSincePackagesReadTheInputsThePlanUsed fails on the old code (the
  warning is written twice). The fixture moves to tagfixture_test.go.

  Finding: "--since re-runs discovery and polyrepo record resolution although
  plannedOptions already holds them" (selection.go:412).

  BenchmarkSincePackages (64 packages, --since HEAD~1 after planning),
  -benchtime 50x, same machine (darwin/arm64, shared with other jobs:
  allocations are the stable figure, time is indicative). The before column
  includes the second discovery walk the previous commit already removed:

  | metric    | c7b14d32 (before) | this commit (after) |
  |-----------|-------------------|---------------------|
  | allocs/op | 2245              | 328                 |
  | B/op      | 424163            | 97888               |
  | ns/op     | 13734824          | 9833641             |

- walk the workspace once per invocation ([3cf76ed](https://github.com/yohimik/dispat/commit/3cf76edc9a7a4ef2bfa52d92a9cab4b462a7cc8a)) (by yohimik, Claude Opus 5.5)
  Every command that asked for the packages and then planned walked the
  workspace twice: packages() discovered it for the step mask, the release
  records, exec and `dispat run`'s script guard, and planOptions discovered it
  again through config.DiscoverWorkspacePlan. `dispat run`'s guard walked it a
  third way, through config.DiscoverPackages directly. Discovery now runs once
  per App (DiscoverWorkspacePackages, the unvalidated walk); packages() and
  planOptions each judge the declared dependencies from it with the new
  config.ValidatePlanDependencies, so each keeps the answer and the error it
  had. A composed workspace's plan resolves record targets into copies, so the
  packages every other caller is handed stay as discovery found them.
  TestPlanOptionsReuseTheDiscoveredWorkspace fails on the old code (the second
  walk misses a folder removed in between).

  Findings: "Workspace discovery walk is performed twice per invocation on
  common command paths" (app.go:289) and "`dispat run`'s typo guard walks
  package discovery a second time" (run.go:237), with the design the
  equivalence vote set out: memoise the unvalidated walk, validate per caller,
  copy before resolveRepositoryRecords writes.

  BenchmarkDiscoveryPerInvocation (200 packages; packages() then planOptions
  on a fresh App), -benchtime 50x, same machine (darwin/arm64, shared with
  other jobs: allocations are the stable figure, time is indicative):

  | metric    | c7b14d32 (before) | this commit (after) |
  |-----------|-------------------|---------------------|
  | allocs/op | 11472             | 5739                |
  | B/op      | 1927712           | 969992              |
  | ns/op     | 3357046           | 1514480             |

- read compute's release tags from one inventory per repository ([d657896](https://github.com/yohimik/dispat/commit/d65789638e3eac4e2e6873e2000e02673a2d28fd)) (by yohimik, Claude Opus 5.5)
  compute's baseline half asked git for each candidate package's tags with
  its own `git tag --list --merged HEAD <glob>`, one process and one
  reachability walk per package. It now reads one inventory per repository
  through TagsForPackages, the reader the planner uses (§13.2), and applies
  each package's alias filter and stable-baseline choice to its share as
  before. TestBaselineReasonsReadOneInventoryPerRepository pins two git
  processes per repository and checks every package's list, order included,
  against its per-package `git tag --list`.

  Finding: "The compute command lists tags once per package with a
  reachability walk each" (computeinitials.go:269), and its duplicate
  "compute re-reads the whole tag inventory once per package".

  BenchmarkBaselineReasons (64 released packages, 2 tags each), -benchtime 20x,
  same machine (darwin/arm64, shared with other jobs: allocations and git
  processes are the stable figures, time is indicative):

  | metric            | c7b14d32 (before) | this commit (after) |
  |-------------------|-------------------|---------------------|
  | GitInvocations/op | 65                | 2                   |
  | allocs/op         | 8497              | 1752                |
  | B/op              | 1519506           | 214848              |
  | ns/op             | 220252012         | 33871404            |

- read the worker's root configuration once ([45dd714](https://github.com/yohimik/dispat/commit/45dd7146d6f5c45f81f221696a84e99e92cd50af)) (by yohimik, Claude Opus 5.5)
  Review blocker on 2e5d2d19 (dispatch.go:993): the read-once change
  covered the control path only. `dispat worker` still handed the path to
  runWorker, whose LoadNode read and parsed the file, $refs included, that
  the ascent had already parsed.

  LoadNode becomes LoadResolvedNode, which takes the ResolvedFile and
  decodes its tree, and runWorker takes the ResolvedFile. It is the same
  argument as for LoadResolvedControl: the same loader built the tree,
  nothing mutates it, and an explicit --config or a file the ascent could
  not parse carries no tree and is read at load, so the error is the same.
  LoadNode had no other caller; TestModeExampleConfigsAreValid loads the
  worker example through LoadResolvedNode with a path alone.
  TestWorkerReadsItsConfigurationOnce runs `dispat worker` under worker
  authority, which stops it right after the load, and counts the loader's
  file-read events: 1, where the previous commit reads 2 and fails.
  TestResolveAndLoadNodeReadsEachFileOnce pins 2 reads for a root file
  with one $ref fragment and checks the result equals a plain load.

  go run github.com/yohimik/dispat/tools/testreport bench <name> --
  ./internal/config -run '^$' -bench BenchmarkResolveAndLoadNode -benchmem
  -benchtime 200x -count 1, from services/dispat, Apple M5. The benchmark's
  helper follows the CLI's call sequence: ResolveConfigFile then LoadNode
  of the path at df074bec, ResolveConfigFile then LoadResolvedNode here.
  Files read and allocations are the stable figures; the machine is
  shared, so the timings are indicative only.

  | per resolve and load | df074bec (before) | this commit (after) |
  |---|---|---|
  | files read | 4 | 2 |
  | allocs/op | 686 | 518 |
  | B/op | 45,326 | 35,112 |
  | ns/op (indicative) | 88,120 | 52,743 |

- index a packages map only when it has entries and is asked ([5a05274](https://github.com/yohimik/dispat/commit/5a05274ade13273705a9edd7e40664d0706c8f5b)) (by yohimik, Claude Opus 5.5)
  Review suggestion on c18aa88b (discover.go): newDiscoveryMode indexed
  the root packages map for every discovery, ResolvedSpaceConfigs
  included, which never asks it about a package; every empty map got an
  index map of its own; and every package name was folded up front even
  when each map it met was empty or held its exact spelling.

  The root index is now built on the first package lookup, an empty map
  gets no index, the space-file index list is nil when no space file holds
  an entry, and a name is folded only when a map with entries misses its
  exact spelling. Every lookup returns what it did: a lookup in an empty
  map finds nothing, Fold is pure so folding later gives the same key, and
  nothing writes a packages map's keys during discovery, so an index built
  at the first lookup holds the keys one built at the start would.
  TestPackageEntriesFindIsFoldLookup now also asks an empty map.

  go run github.com/yohimik/dispat/tools/testreport bench <name> --
  ./internal/config -run '^$' -bench 'PackageLayers|ResolvedSpaceConfigs'
  -benchmem -benchtime 200x -count 1, from services/dispat, Apple M5. The
  machine is shared: allocations are the stable figure, timings and the
  small B/op differences of BenchmarkPackageLayers (temporary folder
  names) are indicative. The reference column is the BASE sources with
  discover_bench_test.go from df074bec copied in. With entries in every
  map the index is the time win of c18aa88b and stays.

  | allocs/op (B/op) | c7b14d32 + df074bec bench | df074bec (before) | this commit (after) |
  |---|---|---|---|
  | PackageLayersWithoutEntries, 100 | 221 (28,003) | 225 (28,180) | 221 (28,048) |
  | PackageLayersWithoutEntries, 500 | 621 (79,316) | 625 (79,519) | 621 (79,294) |
  | ResolvedSpaceConfigs, 100 | 104 (11,137) | 108 (16,106) | 104 (11,154) |
  | ResolvedSpaceConfigs, 500 | 104 (11,138) | 108 (52,302) | 104 (11,154) |
  | PackageLayers, 500 | 9,452 (1,041,765) | 9,465 (1,063,910) | 9,464 (1,070,008) |
  | ns/op PackageLayers, 500 | 2,780,171 | 537,439 | 526,567 |

- index package folders only when an output root is declared ([293abd7](https://github.com/yohimik/dispat/commit/293abd70751c6bc64b22bf59bbd7c78fd65354bd)) (by yohimik, Claude Opus 5.5)
  Review blocker on b977c803 (buildoutputs.go:306): the containment
  checks built the package-folder index before they knew any root existed,
  so a workspace declaring no buildOutputs, this repository among them,
  folded every package folder and sorted them for nothing: 904 allocations
  at 500 packages where the code before b977c803 made none. The sweep
  check did the same for runOutputs whose scripts list no root.

  checkBuildOutputRootsHoldNoPackage now returns before the index when
  there is no root, and checkRunOutputRoots returns when no script lists a
  root rather than only when the map is empty. Both are what the loops
  would have answered: with nothing to hold against the packages, neither
  can refuse. The run check now also skips the repository-root walk it
  used to make in that case, so it allocates less than before b977c803.
  The folder index and the overlap sort use slices.SortFunc, which needs
  no reflection swapper; the overlap order is total (folded spelling, then
  index) and the folder runs are read by minimum position, so neither
  answer depends on the sort algorithm. TestDiscoveryCheckAllocationBudget
  pins both checks at 0 allocations with no root declared; on the previous
  commit they measure 904 and 906 and fail.

  go run github.com/yohimik/dispat/tools/testreport bench <name> --
  ./internal/config -run '^$' -bench
  'Undeclared|CheckBuildOutputRoots|CheckRunOutputRoots' -benchmem
  -benchtime 200x -count 1, from services/dispat, Apple M5. The machine is
  shared, so allocations and bytes are the stable figures and the timings
  are indicative only. The reference column is the BASE sources with
  discover_bench_test.go from df074bec copied in.

  | allocs/op (B/op) | c7b14d32 + df074bec bench | df074bec (before) | this commit (after) |
  |---|---|---|---|
  | undeclared build, 100 pkgs | 0 (0) | 104 (5,984) | 0 (0) |
  | undeclared build, 500 pkgs | 0 (0) | 904 (53,984) | 0 (0) |
  | undeclared run, 100 pkgs | 2 (32) | 106 (6,016) | 0 (0) |
  | undeclared run, 500 pkgs | 2 (32) | 906 (54,016) | 0 (0) |
  | CheckBuildOutputRoots, 500 | - | 2,922 (282,409) | 2,916 (282,272) |
  | CheckRunOutputRoots, 500 | - | 2,420 (206,592) | 2,417 (206,496) |
  | ns/op undeclared build, 500 | 368 | 54,479 | 362 |

- read the root configuration once per command ([0dbc7e0](https://github.com/yohimik/dispat/commit/0dbc7e0bd8e983389b15c55acf3a9c9eca5a7b27)) (by yohimik, Claude Opus 5.5)
  Finding: the CLI resolved the configuration file with ResolveFile, whose
  ascent parses every file it classifies, and then loaded the chosen path
  with LoadControl, which read and parsed it again together with every
  file its $refs pull in (config.go:453).

  ResolveConfigFile now keeps the tree the ascent parsed, and
  LoadResolvedControl decodes it instead of reading the file. An explicit
  --config and a file the ascent could not parse carry no tree, so they
  are read at load exactly as before and a broken file fails with the
  same error. The decoded configuration is the same: the same loader,
  formats and $ref rules built the tree, and nothing mutates a tree once
  read. ResolveFile, Load and LoadControl keep their signatures. Only the
  loader's debug and trace events for that file are now written once.
  TestResolveAndLoadControlReadsEachFileOnce pins two file reads (four
  before) and compares the result with Load of the file;
  TestLoadResolvedControlReportsAnUnreadableFile pins the error.

  go run github.com/yohimik/dispat/tools/testreport bench <name> --
  ./internal/config -run '^$' -bench BenchmarkResolveAndLoadControl
  -benchmem -benchtime 200x -count 1, from services/dispat, Apple M5. The
  benchmark's helper follows the CLI's call sequence, so it calls
  ResolveFile and LoadControl before and the new pair after. The machine
  was under a load average near 28: files read and allocations are the
  stable figures, the timings are not comparable.

  | per resolve and load | a8be7908 (before) | this commit (after) |
  |---|---|---|
  | files read | 4 | 2 |
  | allocs/op | 686 | 518 |
  | B/op | 45,296 | 35,110 |
  | ns/op | 82,348 | 336,979 |

- index each packages map by folded key for package lookups ([6a84a03](https://github.com/yohimik/dispat/commit/6a84a03fb62eac6023f415d3ab3e22561fb48769)) (by yohimik, Claude Opus 5.5)
  Finding: packageLayers (discover.go:448) asked the root packages map,
  the space's map and every space file's map for each package through
  FoldLookup, which scans the whole map with EqualFold on every miss: O(P *
  K) comparisons per map, and every package misses the maps that do not
  hold its entry.

  Discovery now indexes each map once by the folded key, the root map
  when discovery starts and the space and space-file maps when the space
  is resolved, and folds the package name once per package. The exact key
  is still tried first. Loading refuses two keys of one map that fold
  together, and Fold matches EqualFold, so the index returns the one key
  the scan would: the entry is consumed, labelled and edited under the
  same spelling. TestPackageEntriesFindIsFoldLookup compares the index
  with models.FoldLookup over exact, re-capitalised and unicode spellings
  (final sigma, Kelvin sign, dotted I, micro sign) and missing names.

  The claim is time: the index adds one map per non-empty packages map,
  which the allocation column shows (+14 allocations, +29 KB at 500
  packages).

  go run github.com/yohimik/dispat/tools/testreport bench <name> --
  ./internal/config -run '^$' -bench BenchmarkPackageLayers -benchmem
  -benchtime 20x -count 1, from services/dispat, Apple M5. The machine is
  shared, so timings are indicative.

  | packages | a710f850 (before) | this commit (after) |
  |---|---|---|
  | 100 ns/op | 402,688 | 291,348 |
  | 100 allocs/op | 2,016 | 2,030 |
  | 100 B/op | 208,034 | 212,034 |
  | 500 ns/op | 4,196,677 | 968,958 |
  | 500 allocs/op | 9,450 | 9,464 |
  | 500 B/op | 1,041,108 | 1,070,320 |

- fold package folders once for the output containment checks ([d607cd6](https://github.com/yohimik/dispat/commit/d607cd6c23a419ec863ee504840a83899270ab94)) (by yohimik, Claude Opus 5.5)
  Finding: checkBuildOutputRootsHoldNoPackage (buildoutputs.go:308) folded
  every package's folder again for every declared build output root, and
  isFolderWithin built root+separator on every call: O(R * P) folds and
  concatenations, R = P under a root-level buildOutputs. checkRunOutputRoot
  repeated the per-package fold for every sweep root of every repository.

  The package folders are now folded once and sorted. The folders equal to
  a root and the folders under root+separator are each one contiguous run
  in byte order, found by binary search, and the refusal names the
  smallest package position among them, skipping the root's own package:
  the package the scan in order returned first. The sweep root checks
  share one index across every root. isFolderWithin compares without
  allocating. TestPackageFolderIndexIsEveryPackageAsked compares the index
  and isFolderWithin with the scan and predicate as written, over a root's
  lexical neighbours (dist-old, dist.d), repeated folders and case
  spellings; alloc_test.go pins both checks at 500 packages.

  go run github.com/yohimik/dispat/tools/testreport bench <name> --
  ./internal/config -run '^$' -bench
  'BenchmarkCheckBuildOutputRoots|BenchmarkCheckRunOutputRoots' -benchmem
  -benchtime 20x -count 1, from services/dispat, Apple M5. The machine is
  shared, so allocations are the stable figure and timings are
  indicative. The build-root column was taken at a710f850 and the sweep
  column with the sources of a710f850 (neither file changed in f83ed275).

  | benchmark | before | after (this commit) |
  |---|---|---|
  | CheckBuildOutputRoots 100 allocs/op | 20,116 | 520 |
  | CheckBuildOutputRoots 100 B/op | 834,398 | 53,160 |
  | CheckBuildOutputRoots 100 ns/op | 922,579 | 48,929 |
  | CheckBuildOutputRoots 500 allocs/op | 700,128 | 2,922 |
  | CheckBuildOutputRoots 500 B/op | 32,940,016 | 282,679 |
  | CheckBuildOutputRoots 500 ns/op | 26,610,248 | 273,425 |
  | CheckRunOutputRoots 100 allocs/op | 714 | 418 |
  | CheckRunOutputRoots 100 B/op | 49,568 | 39,552 |
  | CheckRunOutputRoots 100 ns/op | 52,925 | 20,240 |
  | CheckRunOutputRoots 500 allocs/op | 4,316 | 2,420 |
  | CheckRunOutputRoots 500 B/op | 283,808 | 206,863 |
  | CheckRunOutputRoots 500 ns/op | 180,631 | 105,990 |

- ask only the packages that could read an alias tag ([2870533](https://github.com/yohimik/dispat/commit/287053397ce86bb5e349a4fa113ac8505135c757)) (by yohimik, Claude Opus 5.5)
  Finding: the alias-tag readability check (config.go:2227) tried every
  package's release format against every rendered alias, O(P^2 * A)
  ParseVersion calls at about four allocations each, and recompiled the
  alias template on every render.

  Each distinct alias format is now compiled once, and the rendered tags
  are looked up in a gitx.ReleaseReaderIndex: packages keyed by the
  rendered literal-and-name run their format opens with, or closes with
  when the name follows the version, and a format that never spells the
  name answered by its first package. Those runs are byte-exact tests in
  every shape a tag is read in, so the index only skips packages whose
  parse would fail, and it reports the smallest package position that
  parses, which is the package the loop used to return first. The error
  text and the duplicate-alias loop are unchanged.

  TestReleaseReaderIndexIsEveryReaderTried compares the index with every
  reader tried in order over formats spelling the name in the lead, in
  the tail, in the middle and not at all; alloc_test.go pins the check at
  500 packages (2,011,317 allocations before, 6,526 now).

  go run github.com/yohimik/dispat/tools/testreport bench <name> --
  ./internal/config -run '^$' -bench BenchmarkCheckAliasTagsAreWriteOnly
  -benchmem -benchtime 20x -count 1, from services/dispat, Apple M5. The
  machine is shared, so allocations are the stable figure and timings
  are indicative.

  | packages | a710f850 (before) | this commit (after) |
  |---|---|---|
  | 100 allocs/op | 82,114 | 1,164 |
  | 100 B/op | 4,586,385 | 45,848 |
  | 100 ns/op | 2,419,515 | 37,258 |
  | 500 allocs/op | 2,011,351 | 6,526 |
  | 500 B/op | 112,553,836 | 272,976 |
  | 500 ns/op | 67,089,221 | 171,412 |

- stream the rev-list answer into a commit graph with one edge array ([400be3e](https://github.com/yohimik/dispat/commit/400be3ec0bec8c5a22981b75d7f6b95c788beffc)) (by yohimik, Claude Opus 5.5)
  Finding: "commitGraph construction allocates per commit and buffers the whole
  rev-list output" (services/dispat/internal/gitx/gitx.go:1308).

  commitDAG buffered `git rev-list --parents HEAD` in a growing bytes.Buffer,
  copied it into a string, and read it twice, allocating a field slice and a
  parent slice per commit. The answer now streams through gitStream.stdout into
  a builder that reads each line as it arrives: an id is indexed when it is
  first named, as a commit or as a parent, every edge goes into one array with
  a span per commit, and the ids that never had a line of their own (parents a
  shallow clone does not hold) leave the index at the end, so they still go to
  merge-base. The first line naming a commit with parents is still the one
  kept. Indices are no longer dense in line order, which no answer depends on.
  A differential run of 3,000 generated histories, lines shuffled, split into
  random writes, with duplicate root lines and absent parents, gave the same
  index membership and the same isAncestor answer and known flag for every
  pair as the previous two-pass reader. The git invocation and its trace line
  are unchanged; the trace still counts its output bytes.

  BenchmarkLoadCommitGraph, 50,000 commits fed in 64 KiB writes as a pipe
  delivers them, from the bytes git writes to the graph a handle keeps,
  -benchtime=50x -count=3, Apple M5, shared machine: allocations and bytes are
  the stable figures, timings indicative.

  | revision                  | ns/op           | B/op       | allocs/op |
  |---------------------------|-----------------|------------|-----------|
  | 514d4b7d (before)         | 10.15-11.11 ms  | 30,217,856 | 150,295   |
  | this commit (after)       | 5.65-5.73 ms    | 9,071,311  | 50,342    |

  TestAllocationBudget pins the load of 10,000 commits at 10,134 allocations;
  the previous reader makes 30,097 and fails it.

- read a push's porcelain answer once rather than once per ref ([ab4de6b](https://github.com/yohimik/dispat/commit/ab4de6b141bdba5a1ccd6fcaaf16b333ea9e8ddc)) (by yohimik, Claude Opus 5.5)
  Finding: "Porcelain push output is rescanned once per ref, quadratic in refs
  per push" (services/dispat/internal/gitx/transport.go:707).

  PushReleaseRefs and DeleteRemoteBranchesLease looked every ref up with
  findPushStatus, which scans the whole answer, so a push of R refs cost
  O(R^2) line scans; PushReleaseRefs has no batch cap. Both now read the answer
  once into a report by destination ref, keeping the first line of a ref
  mentioned twice exactly as the scan did, and look each ref up in it. The
  outcomes keep the caller's ref order and every error is raised at the same
  ref. pushLeased asks about one ref and keeps the scan. The single-rune flag
  test is the same test spelled without a rune slice. A differential run of
  300,000 generated answers (duplicate refs, multi-byte, invalid and two-rune
  flags) gave the report and the scan identical answers for every ref.

  BenchmarkPushStatusLookup, 1,000 refs in one answer, -benchtime=50x
  -count=3, Apple M5, shared machine: allocations and bytes are the stable
  figures, timings indicative.

  | revision                  | ns/op              | B/op   | allocs/op |
  |---------------------------|--------------------|--------|-----------|
  | 514d4b7d (before)         | 8,991,338-9,761,922 | 0     | 0         |
  | this commit (after)       | 36,162-41,377      | 98,432 | 6         |

  The report is O(R) transient memory, 98 KB for 1,000 refs, in exchange for
  O(R) rather than O(R^2) time. TestAllocationBudget pins its 6 allocations.

- parse the control gitlink history with a cursor and one copy per commit header ([e4471ea](https://github.com/yohimik/dispat/commit/e4471ea5fc47d23405c9c65417701db8b772a4bc)) (by yohimik, Claude Opus 5.5)
  Finding: "Control history parse materialises a field slice and a map bucket
  per commit" (services/dispat/internal/gitx/control_history.go:72).

  parseControlGitlinkHistory no longer splits the whole `git log -z` answer
  into a slice of every field: a cursor walks the NUL-separated fields in place
  and reads them in the same order with the same framing rules and the same
  errors. The kept header strings of a commit share one copy, the raw metadata
  is split without a slice, Files and the commit list are allocated once at
  their size, the two ids of a gitlink share one copy, and Gitlinks is nil for
  a commit without a transition. The planner's two readers (a range and a
  comma-ok lookup in plan/history.go) behave the same on a nil map. A
  differential run of 300,000 generated inputs, malformed ones included,
  against the previous parser gave identical commits and identical error texts.

  BenchmarkParseControlGitlinkHistory, 20,000 commits (12.7 MB input),
  -benchtime=50x -count=3, Apple M5, shared machine: allocations and bytes are
  the stable figures, timings indicative.

  | revision                  | ns/op           | B/op       | allocs/op |
  |---------------------------|-----------------|------------|-----------|
  | 514d4b7d (before)         | 15.03-15.08 ms  | 42,941,712 | 380,022   |
  | this commit (after)       | 14.24-16.27 ms  | 20,960,213 | 180,000   |

  Retained after the parse: 0.42x of the input, down from 0.54x.
  TestAllocationBudget pins 9 allocations per commit; the previous parser
  makes 19 and fails it.

- index only the exports a large merge folds ([7234870](https://github.com/yohimik/dispat/commit/72348701552cf83355861a45da8a0e392d250b28)) (by yohimik, Claude Opus 5.5)
  Review suggestion on the finding "MergeOutputs scans the release's
  output list once per merged name" (release/outputs.go:92). The index of
  7507185d held every output of the release and every export, so a merge
  of a few exports onto a long list built a map of the whole list, and
  BenchmarkMergeOutputs rose 31-48% in bytes over the scan it replaced.

  The index now holds the exports alone: each name's last export. One
  pass over the release overrides the first copy of each exported name,
  then the names it did not carry are appended where their first export
  stands, one at a time, so the list and its capacity are the scan's.
  Onto a release with no outputs, which is how dispat run hands a
  consumer its provider's list, the fold of the exports is the list. Time
  stays O(|outputs| + |exports|) and the transient memory is O(|exports|).

  outputScanLimit drops from 12 to 7, read from the new
  BenchmarkMergeOutputsCrossover (a 6-export point is added here): from
  8 exports the index is no slower onto 100 outputs and faster onto 1000,
  below that the scan is faster, and at 8 or fewer neither allocates.

  go test -bench -benchmem through testreport bench, Apple M5, shared
  machine at load 3.8, the two columns run back to back. Allocations and
  bytes are the stable figures, timings indicative. 33e4a6cb is that tree
  with this commit's benchmark file.

  BenchmarkMergeOutputs, -benchtime 20x:

  | case                 | 33e4a6cb                        | this commit                    |
  |----------------------|---------------------------------|--------------------------------|
  | chain-1-exports-5    | 1154 ns, 2896 B, 6 allocs       | 1542 ns, 2896 B, 6 allocs      |
  | chain-300-exports-5  | 6.57 ms, 41451022 B, 4828       | 6.34 ms, 41451362 B, 4832      |
  | chain-300-exports-20 | 47.75 ms, 245575023 B, 11252    | 40.10 ms, 205835612 B, 8883    |

  chain-1 runs the scan in both columns, so its time is noise.

  BenchmarkMergeOutputsCrossover, index rows, -benchtime 20000x:

  | case                  | 33e4a6cb               | this commit        | scan            |
  |-----------------------|------------------------|--------------------|-----------------|
  | outputs-100, 4        | 1425 ns, 3496 B, 3     | 681 ns, 0 B, 0     | 397 ns, 0 B     |
  | outputs-100, 8        | 1389 ns, 3496 B, 3     | 850 ns, 0 B, 0     | 868 ns, 0 B     |
  | outputs-100, 12       | 1408 ns, 3496 B, 3     | 1166 ns, 456 B, 3  | 1329 ns, 0 B    |
  | outputs-100, 24       | 1542 ns, 6568 B, 3     | 1830 ns, 936 B, 3  | 2769 ns, 0 B    |
  | outputs-1000, 4       | 12395 ns, 54608 B, 5   | 5757 ns, 0 B, 0    | 3567 ns, 0 B    |
  | outputs-1000, 8       | 12482 ns, 54608 B, 5   | 6168 ns, 0 B, 0    | 7903 ns, 0 B    |
  | outputs-1000, 12      | 12568 ns, 54608 B, 5   | 7523 ns, 456 B, 3  | 12487 ns, 0 B   |
  | outputs-1000, 24      | 12645 ns, 54608 B, 5   | 9508 ns, 936 B, 3  | 28977 ns, 0 B   |

  Tested alternatives, same machine: folding the exports into a list
  first and applying it (more bytes per merge), and marking merged names
  instead of deleting them (no measurable difference).

  TestMergeOutputsIndexHoldsOnlyTheExports pins that the index allocates
  the same onto 100 outputs as onto 10000 (it fails on 33e4a6cb: 3
  against 33). TestMergeOutputsIndexAnswersAsTheScanOnDrawnLists compares
  both ways on 200 drawn lists with names repeated in the release and in
  the exports, including a release with no outputs.

- let shouldSkip allocate only once a provider is dead ([54ca66d](https://github.com/yohimik/dispat/commit/54ca66dd4d75bca11641c0934194b26ac35741b7)) (by yohimik, Claude Opus 5.5)
  Follow-up to the review blocker on the admission path
  (release/stagerelation.go:312). Every admission concatenated the
  package's declared providers with its reached ones and made a dead-set
  map sized for the reached list, although nearly every admission finds
  nothing dead and reads neither. shouldSkip now walks the two lists in
  the same order without joining them and makes the map on the first
  dead provider. The answers are the same: same providers, same order,
  same first record-blocked, last bad and last blocking provider, and
  hasCauseOfItsOwn gets the same set whenever it is asked.

  BenchmarkAdmission, go test -bench -benchmem -benchtime 20000x through
  testreport bench, Apple M5, shared machine at load 2.4, the two columns
  run back to back. Allocations are the stable figure, timings
  indicative.

  | case                 | a735d8a0              | this commit       |
  |----------------------|-----------------------|-------------------|
  | gaps-1               | 84 ns, 16 B, 1        | 80 ns, 0 B, 0     |
  | gaps-3               | 94 ns, 48 B, 1        | 88 ns, 0 B, 0     |
  | gaps-8               | 161 ns, 128 B, 1      | 139 ns, 0 B, 0    |
  | gaps-8-after-a-death | 2368 ns, 10035 B, 4   | 1729 ns, 10 B, 0  |

  Against the base code (c7b14d32, 2194 ns, 10024 B, 4 allocs on every
  shape), every admission is now cheaper in time and memory, with or
  without a death. The 10 B after a death is the one-time listing of the
  consumer's closure amortised over the iterations.

  TestPublishOrderAdmitsWithoutAllocatingWhileEveryProviderLives pins
  zero allocations for an admission over live declared and reached
  providers; it fails on a735d8a0.

- ask the skip cascade nothing until a package is dead ([20dc389](https://github.com/yohimik/dispat/commit/20dc389d90deb33579cf1a9696faddaa68f34803)) (by yohimik, Claude Opus 5.5)
  Review blocker on the finding "reachedProviders retains the per-consumer
  closure for the whole run and shouldSkip rescans it on every task"
  (release/executor.go:470, stagerelation.go:312). The lazy closure of
  8c52d013 kept the memory win but made every admission walk each carrying
  gap's memoised list without deduplicating, so a consumer whose gaps
  overlap paid the sum of those lists on every task of every run, which
  is slower than the deduplicated list the base code read.

  The run now records, under mu, that a releasing package failed, was
  skipped or had its records blocked, at the six places a result becomes
  one of those. Until then resolveReachedProviders answers nil without
  looking at the graph, and the closure memo is no longer walked before
  the tasks run: a run without a failure never walks it. Once a package
  is dead, a consumer's closure is listed with Indirect, sorted exactly as
  before, and kept, so each later admission reads the same list the base
  code read. isAnyReachedDead, resolveCarriers and newCascadeReach are
  gone. shouldSkip passes over a live provider, so before any death the
  nil list answers what the full list did.

  BenchmarkAdmission, go test -bench -benchmem -benchtime 20000x through
  testreport bench, Apple M5, shared machine at load 3.6, the three
  columns run back to back. Allocations are the stable figure, timings
  indicative. c7b14d32 and 3fa3bb3d are those trees with this
  benchmark added (only its setup differs, to build each revision's run).

  | case                 | c7b14d32 (base)      | 3fa3bb3d            | this commit          |
  |----------------------|----------------------|---------------------|----------------------|
  | gaps-1               | 2287 ns, 10024 B, 4  | 1239 ns, 16 B, 1    | 54 ns, 16 B, 1       |
  | gaps-3               | 2150 ns, 10024 B, 4  | 3601 ns, 48 B, 1    | 69 ns, 48 B, 1       |
  | gaps-8               | 2157 ns, 10024 B, 4  | 10308 ns, 128 B, 1  | 121 ns, 128 B, 1     |
  | gaps-8-after-a-death | 2194 ns, 10024 B, 4  | 10603 ns, 128 B, 1  | 2366 ns, 10035 B, 4  |

  After a death the admission costs what the base code's did. The bytes
  are shouldSkip's own, which the next commit takes on.

  BenchmarkTaskGraph -benchtime 50x, same machine, 3fa3bb3d against this
  commit: the cascade memo is no longer walked while the graph is built.

  | case      | 3fa3bb3d                       | this commit                    |
  |-----------|--------------------------------|--------------------------------|
  | hub-100   | 0.26 ms, 455244 B, 1816 allocs | 0.26 ms, 433166 B, 1796 allocs |
  | hub-300   | 0.93 ms, 1615398 B, 5073 allocs | 0.86 ms, 1530120 B, 5048 allocs |
  | chain-300 | 0.50 ms, 613594 B, 2490 allocs | 0.52 ms, 591793 B, 2487 allocs |

  These are also the quiet-load timings for 8c52d013, whose message
  carries timings taken at load average 46.

  TestPublishOrderListsTheReachedClosureOnlyOnceAProviderDied now pins
  that nothing is walked or listed before a death (it fails on 3fa3bb3d,
  which walked every consumer's carriers up front) and records the death
  through settleFailure. TestPublishOrderSkipsAReachedConsumerBehindIncompleteRecords
  covers the record-blocked site through the executor. Removing the flag
  from settleFailure or markPublished makes these tests fail. The two
  panic sites stop the run, so the cascade never reads them; they set the
  flag all the same. TestPublishOrderCascadeAnswersAsTheFullClosure
  still compares against the closure listed up front on 60 random
  workspaces.

- list the skip cascade's reached closure only after a death ([4e7b346](https://github.com/yohimik/dispat/commit/4e7b34676bf782c98e75e8ecb9de86c247880ebc)) (by yohimik, Claude Opus 5.5)
  Finding "reachedProviders retains the per-consumer closure for the
  whole run" (release/executor.go:470). Before any task ran, every
  changed consumer's closure over the publication order was unioned,
  sorted and kept for the run: n * m names for n consumers behind one
  unreleased package over m providers, which after the pass-through
  nodes is the whole of what stays quadratic. shouldSkip passes over a
  provider that lives, so the closure only changes its answer once a
  provider in it failed, was skipped or had its records blocked. The
  memoised walk is now taken up front and only read afterwards; an
  admission asks whether anything it reaches is dead, and lists the
  closure, sorted exactly as before, only when something is, once per
  consumer, under the run's lock. The blocker, the W194 sentence and
  DISPAT_BLOCKED_BY are unchanged: TestPublishOrderCascadeAnswersAsTheFull
  Closure compares both answers over 60 drawn workspaces with mixed
  outcomes, and TestPublishOrderListsTheReachedClosureOnlyOnceAProvider
  Died pins that a run where everything lives lists nothing.

  BenchmarkTaskGraph (build the graph and consume it), go test -bench
  -benchmem -benchtime 50x through testreport bench, Apple M5. The machine
  was shared and loaded (load average 46 for the last column), so the
  allocations are the figure to read and the timings are indicative only.

  | case      | f055a944 (base code)  | c47a5b4c (pass-through) | this commit          |
  |-----------|-----------------------|-------------------------|----------------------|
  | hub-100   | 4.11 MB, 5543 allocs  | 1.58 MB, 3518 allocs    | 455.1 kB, 1816 allocs |
  | hub-300   | 32.50 MB, 19986 allocs | 12.52 MB, 11675 allocs | 1.62 MB, 5073 allocs |
  | chain-300 | 685.7 kB, 2789 allocs | 635.3 kB, 2492 allocs   | 614.0 kB, 2490 allocs |
  | time      | 5.56 / 54.0 / 0.99 ms | 0.73 / 4.94 / 0.49 ms   | 1.58 / 15.7 / 12.4 ms (loaded) |

- order through unreleased packages with pass-through nodes ([2fdd3bd](https://github.com/yohimik/dispat/commit/2fdd3bd156bb3eda05b8354bac94e2f35b226794)) (by yohimik, Claude Opus 5.5)
  Finding "Indirect orderings materialised as one task edge per
  (consumer, reached provider) pair" (release/stagerelation.go:132). The
  task graph stated one build edge and one publish edge from every changed
  provider a consumer reaches through packages this run does not release,
  so n consumers behind one unreleased hub over m providers cost 2nm
  scheduler edges, plus a memoised union and a sort per consumer for the
  build index. Each such package now gets a pass-through node per
  ordering, as the build readiness row of the cost table prices it: the
  node waits for the stage of every changed provider it carries the
  ordering from and for the nodes behind it, and its consumers wait for
  the node, one edge per dependency edge.

  The scheduler learns pass-through nodes: never handed out or counted,
  completed in place the moment their last dependency is. The hand-out
  order, which reaches the log and the event stream, is the literal
  graph's exactly. Done numbers every edge of a scheduler that has
  pass-through nodes and hands each readied node out at the number of the
  edge that readied it; the consumer edges are stated in the consumer's
  turn, where the literal edges stood, and the providers' tasks those
  edges registered early are registered at the same place, so the initial
  frontier keeps its insertion order. A graph without pass-through nodes
  keeps the old code path and pays nothing. TestTaskGraphHandsOutTheLiteral
  Order* drains the new graph and the literal one side by side under
  seven completion orders on diamonds, chains through non-building
  packages, the relation and publication fixtures, a hub with and without
  a publish group, and 60 random workspaces, and requires the same
  batches in the same order.

  go test -bench -benchmem through testreport bench, Apple M5, shared
  machine: allocations are the stable figure, timings indicative.

  BenchmarkTaskGraph (build the graph and consume it), -benchtime 50x:

  | case      | f055a944 (base code)            | this commit                   |
  |-----------|---------------------------------|-------------------------------|
  | hub-100   | 5.56 ms, 4.11 MB, 5543 allocs   | 0.73 ms, 1.58 MB, 3518 allocs |
  | hub-300   | 54.0 ms, 32.50 MB, 19986 allocs | 4.94 ms, 12.52 MB, 11675 allocs |
  | chain-300 | 0.99 ms, 685.7 kB, 2789 allocs  | 0.49 ms, 635.3 kB, 2492 allocs |

  BenchmarkTopoSort (the planner's scheduler use), -benchtime 500x:
  f055a944 466 us, 620377 B, 6074 allocs; this commit 375 us, 620376 B,
  6074 allocs.

  What remains of hub-300 is the per-consumer closure the skip cascade
  keeps (finding "reachedProviders retains the per-consumer closure").

- render the settled workspace listing once per closing phase ([7f82f4c](https://github.com/yohimik/dispat/commit/7f82f4c6966e3aa90e7d78c4856416205da6d0eb)) (by yohimik, Claude Opus 5.5)
  Finding "SettledEnv re-renders the whole workspace listing for every
  package that re-runs syncLock" (release/env.go:337). The listing names
  no package and the closing phase reads it over one settled run, yet
  every package's environment rebuilt it: 4P+1 concatenated strings, the
  key map and the owners JSON, P_sync times. SettledEnvs renders it once
  and each package's environment copies it, as a release run already
  does with WorkspaceEnv. The environment each syncLock script receives
  is unchanged byte for byte: its inputs are the plan and the results,
  which the closing loop never writes, and the listing's only diagnostic
  already went to a no-op logger.

  BenchmarkSettledEnv (300 published packages, every one re-running
  syncLock), go test -bench -benchmem -benchtime 50x through testreport
  bench, Apple M5, shared machine: allocations are the stable figure,
  timings indicative.

  | revision              | time/op  | bytes/op | allocs/op |
  |-----------------------|----------|----------|-----------|
  | f055a944 (base code)  | 48.0 ms  | 44.65 MB | 647116    |
  | this commit           | 1.30 ms  | 6.85 MB  | 16211     |

  Touches services/dispat/internal/app/include_sync.go, the one caller.

- index the outputs a large merge folds onto a release ([6c8867a](https://github.com/yohimik/dispat/commit/6c8867a548e2815aa1fd51e8f90b4e1e45a8c69e)) (by yohimik, Claude Opus 5.5)
  Finding "MergeOutputs scans the release's output list once per merged
  name" (release/outputs.go:92). Every incoming export scanned the
  release's whole list, so a merge cost |exports| x |outputs|, and dispat
  run, which merges each provider's accumulated list into its consumer,
  paid O(P^3 k^2) comparisons down a chain of P packages exporting k
  names. A merge of more than 12 exports now looks names up in an index
  built for that one call, holding the first position of each name and
  taking every name it appends, so the resulting list is element for
  element the scan's. A stage's own few exports keep the scan.

  BenchmarkMergeOutputs, go test -bench -benchmem -benchtime 5x through
  testreport bench, Apple M5, shared machine: allocations are the stable
  figure, timings indicative.

  | case                  | f055a944 (base code)         | this commit                  |
  |-----------------------|------------------------------|------------------------------|
  | chain-1-exports-5     | 900 ns, 2896 B, 6 allocs     | 3125 ns, 2896 B, 6 allocs    |
  | chain-300-exports-5   | 151.8 ms, 31.6 MB, 3704 allocs | 6.6 ms, 41.5 MB, 4830 allocs |
  | chain-300-exports-20  | 3373 ms, 165.7 MB, 4755 allocs | 47.9 ms, 245.5 MB, 11250 allocs |

  The added bytes are the per-call index, garbage once the call returns;
  the retained outputs are unchanged. The one-package case runs the same
  code as before, so its time difference is machine noise.

- answer a shallower walk from the deeper one kept for its source ([7b56ab6](https://github.com/yohimik/dispat/commit/7b56ab68760bd04931616d7683ac393a7a94281b)) (by yohimik, Claude Opus 5.5)
  Review suggestion on 0deb7525: a bounded single-source ask whose source
  has no unbounded walk kept was walked again for every new depth and kept
  once per depth, although a deeper bounded walk from the same source over
  the same kinds already held the answer as its prefix.

  The bounded walks now sit in a cache of their own, one per source and
  kinds: the deepest one asked. A shallower ask reads its prefix through
  withinDepth, which already serves the prefix of an unbounded walk. A
  deeper ask walks to its own depth and replaces the kept walk while the
  budget allows it, and otherwise the shallower one stays. The prefix is
  exact for the reason 0deb7525 relies on. walkLiteral is a FIFO
  breadth-first walk, so every level k target is listed before any level
  k+1 target, with the same origin and level whatever the cut. Cutting at
  d' >= d expands every level below d, so the targets up to level d are
  the same, in the same order. TestCachedWalkIsTheLiteralWalk still checks
  random asks over depths 1, 2, 3 and unbounded against walkLiteral.
  TestShallowerAskReadsTheDeeperBoundedWalk pins 2 targets walked for a
  depth-two ask followed by a depth-one ask. The parent walks 3.

  Same machine, -benchtime=5x, through testreport bench. walkTargets,
  allocations and bytes are the stable figures. Timings are indicative,
  because the machine is shared:

  | benchmark                 | metric         | aab12ddd   | this commit |
  |---------------------------|----------------|------------|-------------|
  | ComputeMixedDepthChain/1024 | walkTargets/op | 3,068    | 2,045       |
  |                           | allocs/op      | 75,280     | 74,248      |
  |                           | B/op           | 12,680,380 | 12,336,158  |
  |                           | ns/op          | 5,504,725  | 5,447,167   |
  | ComputeMixedDepthChain/4096 | walkTargets/op | 12,284   | 8,189       |
  |                           | allocs/op      | 300,205    | 296,078     |
  |                           | B/op           | 51,941,785 | 50,565,000  |
  |                           | ns/op          | 26,352,842 | 22,758,125  |
  | ComputeShallowChain/4096  | walkTargets/op | 4,095      | 4,095       |
  |                           | allocs/op      | 181,259    | 181,262     |

  The ShallowChain allocations grow by 1 at 1,024 packages and by 3 at
  4,096. That is the second map, made with the cache and grown beside the
  first.

- drop the attribution's rank sets once releases are attributed ([c684a01](https://github.com/yohimik/dispat/commit/c684a01db99a4fc4728fac697f75c11c8841ab3c)) (by yohimik, Claude Opus 5.5)
  Review suggestion on ca4dcba7: the composed attribution keeps one rank
  bitset per shared window (about C/8 bytes each), the control
  repository's rank set, the merge scratch and the per-commit author ids
  on the computation, and nothing reads them after collectReleaseAuthors.
  They stayed live through the provider updates, the owed boundaries and
  the plan's emission. collectReleaseAuthors now releases them when the
  last release is attributed. The authorCandidates comment now says it is
  the one commitSet written after it is built. TestReleaseAuthorsDropTheirScratch
  fails on the parent.

  At 50,000 commits and 93 windows that is about 0.6 MiB of rank sets and
  0.2 MiB of author ids freed early. Total bytes allocated cannot move, and
  did not. The sampled peak is indicative. The allocation deltas of 1 to 8
  are run-to-run noise: MergedHistory composed measured 134,473 at
  ca4dcba7 and 134,477 at 7bc931ff, which share this path.

  Same machine, -benchtime=3x, through testreport bench. Allocations and
  bytes are the stable figures. Timings and the sampled peak heap are
  indicative, because the machine is shared:

  | benchmark                      | metric       | 7bc931ff    | this commit |
  |--------------------------------|--------------|-------------|-------------|
  | ComputeRealHistory composed/50k| peakHeap_MiB | 53.01       | 46.72       |
  |                                | retained_MiB | 3.366       | 3.367       |
  |                                | allocs/op    | 363,661     | 363,669     |
  |                                | B/op         | 86,476,906  | 86,541,616  |
  |                                | ns/op        | 489,380,472 | 497,019,570 |
  | ComputeRealHistory composed/10k| peakHeap_MiB | 9.559       | 8.797       |
  |                                | allocs/op    | 78,611      | 78,612      |
  | ComputeMergedHistory composed  | peakHeap_MiB | 34.57       | 34.04       |
  |                                | allocs/op    | 134,477     | 134,481     |
  |                                | B/op         | 42,716,357  | 42,753,197  |

  ca4dcba7's MergedHistory composed BEFORE column was taken at 0548960b.
  Retaken at its parent, -benchtime=5x on the same machine, from a git
  archive of bc7c967e and in the worktree at ca4dcba7:

  | ComputeMergedHistory composed | bc7c967e   | ca4dcba7   |
  |-------------------------------|------------|------------|
  | allocs/op                     | 134,213    | 134,473    |
  | B/op                          | 42,632,742 | 42,676,312 |
  | peakHeap_MiB                  | 34.65      | 33.81      |
  | ns/op                         | 46,363,650 | 35,070,933 |

- keep only the link evidence subjects a boundary looks up ([889f8fd](https://github.com/yohimik/dispat/commit/889f8fd58e30b3e2832b614aa835e55bd911f491)) (by yohimik, Claude Opus 5.5)
  Finding (contested on size alone, and the reviewer's equivalence
  blocker on 63476f57): linkEvidence.index kept the subject of every
  release tag of every package for the whole plan, although a boundary
  only looks up a package's stable and newest tag.

  The read is unchanged. Every tag's commit still goes to the one subject
  read per repository, so a commit the repository cannot log still stops
  the plan with the same error, and the debug line still counts every
  subject read. Only the subjects of the stable and newest tags are
  interned and kept. resolve looks a subject up under the repository name
  the index stored it under and a boundary tag's commit, and every such
  key is kept, so every lookup gets the answer it got before.
  TestLinkEvidenceRefusesAnUnloggableOlderRelease fails on 63476f57, where
  the older release's commit was not read.
  TestLinkEvidenceKeepsOnlyTheBoundarySubjects pins four revisions read and
  two subjects kept; it fails on the parent, which keeps four.

  Same machine, -benchtime=5x, through testreport bench. Revisions,
  allocations and bytes are the stable figures. Timings are indicative,
  because the machine is shared:

  | benchmark                         | metric          | 36a0ad9d  | this commit |
  |-----------------------------------|-----------------|-----------|-------------|
  | LinkEvidenceIndex (64 x 256 tags) | revisions/op    | 16,384    | 16,384      |
  |                                   | subjects kept*  | 16,384    | 64          |
  |                                   | allocs/op       | 33,127    | 361         |
  |                                   | B/op            | 7,254,288 | 3,342,552   |
  |                                   | ns/op           | 5,684,608 | 3,978,842   |

  * not a benchmark metric: one per indexed revision before, one per
  package here (stable and newest are the same tag in this workload), as
  TestLinkEvidenceKeepsOnlyTheBoundarySubjects pins on a smaller line.

- read link evidence subjects for every release tag again ([bb27084](https://github.com/yohimik/dispat/commit/bb270840cf30e1326afbb45eb09534df85fbf3f2)) (by yohimik, Claude Opus 5.5)
  63476f57 asked the subject read about the stable and newest tag of each
  package only. That is not the same work made cheaper. CommitSubjects is
  one `git log --no-walk` over every revision it is given, and each one has
  to resolve. A release tag whose commit the repository cannot log (a
  pruned or shallow object, or a tag that peels to a non-commit) used to
  stop the plan with "reading repository X release subjects". With the
  smaller read set it planned instead, and the debug line counted fewer
  revisions. CCME section 17.2 holds an optimised plan to the literal
  reading on every input, and that includes the inputs it refuses, so the
  read set goes back to every tag. The next commit keeps the saving that
  is equivalent: it retains only the subjects a boundary looks up.

  Reverts: 63476f574ee51f527216ab5be5a8754069dc844f

- attribute a composed window from its windows, not the union ([6d8e8ed](https://github.com/yohimik/dispat/commit/6d8e8ed2fe447f30314dbfac49cd9e0cb7b95b79)) (by yohimik, Claude Opus 5.5)
  Finding (contested on equivalence): in a composed workspace
  collectWindowAuthors visited every commit of the union for each window
  identity, O(identities x C), where the single-history path visits the
  window alone. The refuting vote showed why merging the package's
  windows is not enough: a source package's control-repository keys are
  decided by the control rule alone, so a fresh control window from an
  older published boundary holds commits the rule rejects, and the rule
  admits control commits no window holds.

  The attribution now visits a superset of what inWindow can admit: the
  commits of the shared windows the package was attached from, each
  window's ranks built once and shared by every package released at it,
  plus, for a package outside the control repository, every control
  commit of the union. Each candidate, in rank order, still has to pass
  the same test the union pass applied (the repository is reachable and
  inWindow admits the key), so the commits attributed, their order, the
  ancestry questions asked of control commits and the lists are those of
  the pass over the union. TestComposedAttributionVisitsTheWindowsNotTheUnion
  compares every package's lists with that pass on a merged history and
  on the vote's case, a source package whose provider lives in the
  control repository with a published control boundary older than its
  stable one; dropping the inWindow test fails it there. It also pins the
  commits examined below half of a union pass per package. The window and
  control rank sets and the merge scratch are computation fields
  (plan.go); mergedGit now reports its commits' authors.

  Same machine; AuthorScans, allocations and bytes are the stable
  figures, timings and the sampled peak heap indicative:

  | benchmark (-benchtime)              | metric         | bc7c967e    | this commit |
  |-------------------------------------|----------------|-------------|-------------|
  | ComputeRealHistory composed/50k (3x)| AuthorScans/op | 741,056     | 214,965     |
  |                                     | allocs/op      | 363,505     | 363,672     |
  |                                     | B/op           | 86,356,648  | 86,534,528  |
  |                                     | ns/op          | 518,890,153 | 503,022,597 |
  | ComputeMergedHistory composed (5x)  | ns/op          | 44,018,800* | 32,635,233  |
  |                                     | B/op           | 42,490,524* | 42,686,545  |

  * taken at 0548960b; the commits since change no composed path there.

  214,965 is what the same repository planned as one history examines
  (shape=bounded). The cost is a rank set per shared window, about 6 KiB
  each at 50,000 commits, held until the plan is computed.

- identify each commit's author once per plan ([0e726e0](https://github.com/yohimik/dispat/commit/0e726e083438338e9a13619e98521e5eaa888f3d)) (by yohimik, Claude Opus 5.5)
  Finding (contested on size alone): the window attribution built an
  Author per visited commit, folded its email into the dedup key and
  probed two string-keyed maps it allocated per window identity, so the
  same commit's author was folded and hashed again for every window that
  holds it.

  authorOf interns the folded key, the same Author.key, to a plan-wide id
  the first time a commit is attributed and keeps it by commit rank; each
  attribution then marks the ids it has listed in two generation-stamped
  arrays shared by every attribution of the plan, so starting one costs
  neither a clear nor an allocation. Two commits get one id exactly when
  their keys are equal, the Author listed is still the visited commit's
  own spelling, the visiting order is unchanged and containedInBaseline is
  still asked on every visit, so WindowAuthors and FreshWindowAuthors are
  the same lists. TestWindowAttributionFoldsEachCommitsAuthorOnce pins a
  second visit of a commit with a mixed-case address at zero allocations
  (Fold allocated on every visit before). The id table and the marks are
  new fields of the computation in plan.go.

  Same machine, -benchtime=3x; allocations and bytes are the stable
  figures, timings indicative (shared machine):

  | ComputeRealHistory/50k | metric    | 27298330    | this commit |
  |------------------------|-----------|-------------|-------------|
  | whole                  | allocs/op | 895,443     | 894,826     |
  |                        | B/op      | 195,200,277 | 195,232,285 |
  |                        | ns/op     | 749,790,222 | 703,598,736 |
  | bounded                | allocs/op | 259,830     | 259,217     |
  |                        | B/op      | 52,190,312  | 52,068,213  |
  |                        | ns/op     | 482,813,195 | 435,295,569 |
  | composed               | allocs/op | 364,118     | 363,505     |
  |                        | B/op      | 86,497,426  | 86,356,648  |
  |                        | ns/op     | 558,856,444 | 518,890,153 |

  AuthorScans/op is unchanged (192,366, 214,965 and 741,056): the visits
  are the same, each is cheaper. The composed scan of the whole union per
  identity is a separate finding.

- hand releases only the unit marks they name ([166747f](https://github.com/yohimik/dispat/commit/166747f5b29a48fdcd7f9b1d7ef71dcdeaf8a57f)) (by yohimik, Claude Opus 5.5)
  Finding (contested on size alone): finalise gave every release the
  computation's unitAuthors and unitCommits maps, which hold every valid
  unit of the union, so a returned plan kept every parsed message of the
  history alive (49 of 53 MiB at 50,000 commits) although its readers only
  look units up by keys a release already holds.

  After the versions and the window attribution, shareUnitMarks builds one
  pair of maps holding the units some release names in Units, Corrects or
  SuppressedNotes, copied from the computation's, and hands that pair to
  every release. AuthorsFor, UnitCommit, the digest and the changelog read
  the maps by those keys alone and never range them, so every answer is
  the same; the computation keeps its own maps for the fixed-group
  re-measure. TestReleasesKeepOnlyTheUnitsTheyName pins a three-commit
  history at one kept unit (the old maps held three).

  The parse results themselves are not held longer than needed: a one-unit
  message's Result is the same allocation as its unit, which the plan
  keeps, so releasing it earlier than resolve does would free nothing.

  Same machine, -benchtime=3x; retained heap, allocations and bytes are
  the stable figures, timings and the sampled peak heap indicative:

  | ComputeRealHistory/50k | metric       | de0d1ce2 | 63476f57-era* | this commit |
  |------------------------|--------------|----------|---------------|-------------|
  | whole                  | retained_MiB | 53.15    | 53.15         | 4.18        |
  | bounded                | retained_MiB | 12.69    | 12.69         | 3.50        |
  | composed               | retained_MiB | 12.34    | -             | 3.37        |

  * retained_MiB was 53.15 / 12.69 at every commit of this series before
  this one (measured at 22d2a15d, ab94449e and 59856de3).

- read link evidence subjects for the boundary tags only ([38077c5](https://github.com/yohimik/dispat/commit/38077c56b50be1bbd8f1efb5887ee450b413c6f2)) (by yohimik, Claude Opus 5.5)
  Finding (contested on size alone): linkEvidence.index passed the commit
  of every release tag of every package to one subject read per
  repository and kept every subject for the plan, although boundaries are
  asked only for a package's stable and newest tags.

  index now asks about those two tags' commits per package. A repository
  is read exactly when it was before, once whenever one of its packages
  has a tag, so LinkReads and the missing-reader error are unchanged, and
  every subject resolve looks up is still indexed. The only observable
  difference is the debug line's revision count, and that a broken commit
  behind a release tag no boundary uses no longer fails the read, which is
  the literal reading's answer. TestLinkEvidenceReadsOnlyTheBoundaryTags
  pins a four-tag line at two revisions in one read.

  Same machine, -benchtime=5x; revisions, allocations and bytes are the
  stable figures, timings indicative:

  | benchmark                         | metric       | de0d1ce2  | this commit |
  |-----------------------------------|--------------|-----------|-------------|
  | LinkEvidenceIndex (64 x 256 tags) | revisions/op | 16,384    | 64          |
  |                                   | allocs/op    | 33,125    | 153         |
  |                                   | B/op         | 7,252,204 | 27,208      |
  |                                   | ns/op        | 4,551,508 | 16,775      |

- index the fleet's link peers once per plan ([589f320](https://github.com/yohimik/dispat/commit/589f3207770e2dd3adac551fa1eb719892b3753c)) (by yohimik, Claude Opus 5.5)
  Finding (contested on size alone): linkRoute's breadth-first search
  called linkNeighbours for every repository it dequeued, and each call
  sorted every history name, scanned every history's links and sorted the
  result: O(Q^2 log Q) per route, and projectSince routes to every
  repository.

  The peers of every repository are now listed in one pass over the
  histories in name order, the first time a route is asked for, and kept
  on the computation (a new linkPeers field in plan.go): a history's own
  links in name order, and the name of every other history whose links
  name it, met in history order, each name kept in the spelling met first
  and the list ordered by folded name. That is what the scan found for
  each repository, so every route, and every diagnostic that names a hop,
  is unchanged; TestLinkPeersAreTheScanOfEveryHistory checks the index
  against the scan over 300 random fleets with one-sided links, case
  variants and self links. A route is then a search over the index.

  Same machine, -benchtime=5x; allocations and bytes are the stable
  figures, timings indicative:

  | benchmark                    | metric    | de0d1ce2   | this commit |
  |------------------------------|-----------|------------|-------------|
  | LinkRoute/repositories=16    | allocs/op | 780        | 323         |
  |                              | B/op      | 57,753     | 22,113      |
  |                              | ns/op     | 163,833    | 39,200      |
  | LinkRoute/repositories=64    | allocs/op | 12,756     | 3,255       |
  |                              | B/op      | 2,896,860  | 397,089     |
  |                              | ns/op     | 10,043,650 | 227,683     |

  The app's planLinks and config.Workspace.LinkRoute have the same shape
  and are outside this change.

- join each diagnostic's digest sort key once ([5c640db](https://github.com/yohimik/dispat/commit/5c640db278e325f969025714731995e2789acdd3)) (by yohimik, Claude Opus 5.5)
  Finding (contested on size alone): canonicalDiagnostics sorted with a
  comparator that joined the four key fields of both operands on every
  comparison, about 2 D log D short-lived strings per digest.

  Each diagnostic's key is joined once beside it and sort.Slice compares
  the stored keys: the same comparison results in the same sequence, so
  the same permutation and the same canonical document, ties and fields
  holding the separator included. TestCanonicalDiagnosticsSortByTheJoinedKey
  checks the order against the per-comparison definition over 300 random
  lists with duplicates and NUL-bearing fields, and pins 64 diagnostics at
  72 allocations (measured 69).

  Same machine, -benchtime=5x; allocations and bytes are the stable
  figures, timings indicative:

  | benchmark                         | metric    | de0d1ce2   | this commit |
  |-----------------------------------|-----------|------------|-------------|
  | CanonicalDiagnostics/5000         | allocs/op | 126,956    | 5,005       |
  |                                   | B/op      | 6,421,512  | 969,240     |
  |                                   | ns/op     | 4,155,183  | 787,667     |
  | CanonicalDiagnostics/50000        | allocs/op | 1,688,282  | 50,008      |
  |                                   | B/op      | 84,240,552 | 9,612,304   |
  |                                   | ns/op     | 56,636,808 | 11,412,733  |

- read a group member's direct contributions off its units ([740df4b](https://github.com/yohimik/dispat/commit/740df4b38649932e55d93eef5c276c9b25937a16)) (by yohimik, Claude Opus 5.5)
  Finding (contested on size alone): directBumps appended a
  groupContrib{key, bump} per admitted (unit, package) for every package,
  under exactly the guards that append the unit to rel.Units, although
  only the fixed-group aggregate (groupFresh) reads it and the same pairs
  are rel.Units' commits (unitCommits) and bumps.

  groupFresh now walks rel.Units, reading each unit's commit from
  unitCommits and its bump from the unit: the same (commit, bump) sequence
  in the same order, so the same ancestry questions are asked and the
  group's version is unchanged. The ownContribs map and the groupContrib
  type are gone (fixedgroup.go, plan.go). The fixed-group suites pass
  unchanged.

  Same machine, -benchtime=5x; allocations and bytes are the stable
  figures, timings indicative:

  | benchmark                         | metric    | 59856de3   | this commit |
  |-----------------------------------|-----------|------------|-------------|
  | ComputeGroup/fixed (200 members)  | allocs/op | 3,690      | 3,685       |
  |                                   | B/op      | 746,100    | 735,108     |
  | ComputeGroup/fixedSparse          | allocs/op | 2,075      | 2,071       |
  |                                   | B/op      | 628,947    | 619,041     |
  | ComputeMergedHistory single       | allocs/op | 78,012     | 77,489      |
  |                                   | B/op      | 17,498,083 | 17,411,212  |
  | ComputeMergedHistory composed     | allocs/op | 134,710    | 134,187     |

  The parent column was taken in this worktree with the change stashed;
  the base revision has no separate figure for ComputeGroup.

- resolve scope-sets without filtered copies of their terms ([32c1b16](https://github.com/yohimik/dispat/commit/32c1b169bc8f37f7a91cc2acf568abd22a79b93e)) (by yohimik, Claude Opus 5.5)
  Findings: scope-set resolution allocates per unit, one map and two
  slices of it needlessly (scope.go:64, contested on size alone), and the
  scope-set accessors and report strings allocate per unit (scope.go:51,
  refuted on size and on the equivalence of a single-pass sketch).

  ScopeSet.Includes and Excludes each copied the terms into a new slice,
  three times per resolution and once more in the files pre-pass
  (isScopeSetDerived in files.go). Resolution now reads the terms in two
  passes over the written set, inclusions and then exclusions, which is
  the order the copies gave: every inclusion is still expanded before any
  exclusion, so W134 lists the inclusions' empty globs first whatever
  order the terms were written in, the property the refuting vote showed a
  single pass would break. TestScopeSetExpandsInclusionsBeforeExclusions
  pins that order. reportScope builds its location text only when there
  is a diagnostic to carry it. The exclusion set stays a local the
  compiler keeps on the stack, so nothing else allocates.
  TestScopeSetResolutionAllocatesOnlyItsResult pins a three-term
  resolution at 2 allocations (the old code made 5).

  Same machine; allocations and bytes are the stable figures, timings and
  the sampled peak heap indicative:

  | benchmark (-benchtime)              | metric    | de0d1ce2    | ab94449e    | this commit |
  |-------------------------------------|-----------|-------------|-------------|-------------|
  | ComputeRealHistory whole/50k (3x)   | allocs/op | 1,321,552   | 1,029,763   | 895,773     |
  |                                     | B/op      | 228,552,176 | 203,435,200 | 194,858,202 |
  | ComputeRealHistory bounded/50k (3x) | allocs/op | 359,262     | 291,277     | 260,174     |
  |                                     | B/op      | 59,745,450  | 53,838,024  | 51,847,040  |
  | ComputeMergedHistory single (5x)    | allocs/op | 117,515     | 87,825      | 78,013      |
  | ComputeMergedHistory composed (5x)  | allocs/op | 174,211     | 144,520     | 134,715     |

- allocate a one-unit commit's unit records together ([2367e63](https://github.com/yohimik/dispat/commit/2367e63bf14691ee25473a9e05259bbdef72f85a)) (by yohimik, Claude Opus 5.5)
  Finding (contested on size alone): resolveCommit made three slices of
  len(units) per commit, scope-sets, bump axes and channel axes, and
  resolved the channel axis of every non-cancel unit although at depth 0
  with no scope footer that record is inert and every reader skips it.

  A one-unit commit, nearly every commit, now takes its three records from
  one allocation; others make the three slices as before. The channel axis
  is resolved only when its depth is set or a Propagate-Scope or
  Propagate-Channel-Scope footer is written, the cases where resolution
  can produce a live record or a diagnostic; otherwise the zero record,
  inert by depth 0, stands in. The readers in propagate.go and history.go
  test inert() before anything else, so the plan is unchanged.
  TestOneUnitCommitAllocatesItsRecordsOnce pins the one allocation (the
  old code made three).

  Same machine; allocations and bytes are the stable figures, timings and
  the sampled peak heap indicative:

  | benchmark (-benchtime)              | metric    | de0d1ce2    | 22d2a15d    | this commit |
  |-------------------------------------|-----------|-------------|-------------|-------------|
  | ComputeRealHistory whole/50k (3x)   | allocs/op | 1,321,552   | 1,129,757   | 1,029,763   |
  |                                     | B/op      | 228,552,176 | 203,834,029 | 203,435,200 |
  | ComputeRealHistory bounded/50k (3x) | allocs/op | 359,262     | 314,442     | 291,277     |
  |                                     | B/op      | 59,745,450  | 53,931,080  | 53,838,024  |
  | ComputeMergedHistory single (5x)    | allocs/op | 117,515     | 97,588      | 87,825      |
  | ComputeMergedHistory composed (5x)  | allocs/op | 174,211     | 154,288     | 144,520     |

- build a kind set once per configured kind list ([d70f8ab](https://github.com/yohimik/dispat/commit/d70f8aba238036666ca4737ead3e7a3f7e9a1593)) (by yohimik, Claude Opus 5.5)
  Finding (contested on size alone): unitPropagation and
  unitChannelPropagation each called kindSet(d.Kinds) for every unit,
  building a fresh map, although Directives.Kinds aliases the parser's one
  configured list, so every unit of a parser got an identical map and kept
  it in rec.propagations and rec.channelPropagations for the plan.

  sharedKindSet builds the set once per list, keyed by where the list
  lives and its length, and hands every unit the same read-only map. Every
  reader only looks kinds up or, in kindsCacheKey, lists them sorted, so
  walks, cache keys and the plan are unchanged; a nil list stays every
  kind and a present empty one stays none. TestSharedKindSetIsBuiltOncePerList
  pins a second unit's lookup at zero allocations. The memo is a new field
  of the computation in plan.go.

  Same machine; allocations, bytes and heap are the stable figures,
  timings indicative:

  | benchmark (-benchtime)          | metric       | de0d1ce2    | this commit |
  |---------------------------------|--------------|-------------|-------------|
  | ComputeRealHistory whole/50k (3x)   | allocs/op    | 1,321,552   | 1,129,757   |
  |                                 | B/op         | 228,552,176 | 203,834,029 |
  |                                 | peakHeap_MiB | 134.0       | 124.6       |
  | ComputeRealHistory bounded/50k (3x) | allocs/op    | 359,262     | 314,442     |
  |                                 | B/op         | 59,745,450  | 53,931,080  |
  |                                 | peakHeap_MiB | 33.11       | 27.48       |
  | ComputeMergedHistory single (5x)    | allocs/op    | 117,515     | 97,588      |
  |                                 | B/op         | 20,857,641  | 18,162,926  |
  | ComputeMergedHistory composed (5x)  | allocs/op    | 174,211     | 154,288     |
  |                                 | B/op         | 45,923,768  | 43,275,216  |

  The earlier commits of this series change the walks only, which these
  histories make few of: 91704a44's MergedHistory figures (116,108 and
  172,805 allocs/op) put this commit's share at about 18,500 allocations
  per plan there.

- order a release's contributions by counting providers ([1265348](https://github.com/yohimik/dispat/commit/1265348cc099c93b2cabb927925c902923be7edf)) (by yohimik, Claude Opus 5.5)
  Finding (contested on size alone): every release's Sources was
  sort.SliceStable'd by provider, O(Z log Z) comparisons and O(Z log^2 Z)
  swaps, and dedupeProviders then built a map and sorted the distinct
  providers again for DueTo.

  Contributions already in provider order, the common case of one
  provider, are kept as they are and only their distinct providers listed.
  Otherwise the contributions are counted per provider, the k distinct
  providers sorted once, and each contribution placed after those of the
  providers before it, in the order it was made: O(Z + k log k), and the
  sorted provider list is DueTo. That is the order a stable sort by
  provider gives, ties included; TestContributionsOrderAsAStableSortByProvider
  checks it against sort.SliceStable on 500 random lists and pins the
  one-provider case at one allocation. The propagation unit tests move to
  propagate_test.go beside it.

  Same machine, -benchtime=5x; allocations and bytes are the stable
  figures, timings indicative:

  | benchmark                    | metric    | de0d1ce2   | 15a3aa04   | this commit |
  |------------------------------|-----------|------------|------------|-------------|
  | ComputeDependencyGraph/256   | allocs/op | 7,145      | 7,114      | 6,858       |
  | ComputeDependencyGraph/4096  | B/op      | 23,448,048 | 21,974,227 | 21,875,992  |
  |                              | allocs/op | 109,499    | 109,412    | 105,317     |
  | ComputeMergedHistory/single  | allocs/op | 117,515    | -          | 116,108     |
  | ComputeMergedHistory/composed| allocs/op | 174,211    | -          | 172,805     |

  The MergedHistory column at de0d1ce2 includes the earlier walk commits'
  share of the difference; it was not taken at 15a3aa04.

- answer a one-source unit's reaching set without a map ([e799e67](https://github.com/yohimik/dispat/commit/e799e67193e15e967a86afe3833eb5e4d3b14416)) (by yohimik, Claude Opus 5.5)
  Finding (contested on size alone): for a unit over one package
  reachingSources built a map with one entry per walked target, every
  entry the same one-element list, which the target loop then looked up
  once per target.

  The answer is now a reachingSet: the one sorted source list for every
  target when the unit has a single source, the per-target map otherwise.
  Every target receives the same list it did, so admissions, Sources
  records and W208 are unchanged; the multi-source path is the same walk
  per source. TestOneSourceReachesEveryTargetWithoutAnIndex pins the
  one-source answer at one allocation, the source's name.

  Same machine, -benchtime=5x; allocations and bytes are the stable
  figures, timings indicative:

  | benchmark                   | metric    | de0d1ce2      | b18704ca   | this commit |
  |-----------------------------|-----------|---------------|------------|-------------|
  | ComputeDependencyGraph/4096 | B/op      | 23,448,048    | 22,367,833 | 21,974,227  |
  |                             | allocs/op | 109,499       | 109,430    | 109,412     |
  | ComputeChainTopology/4096   | B/op      | 1,698,456,033 | 40,338,465 | 38,699,353  |
  |                             | allocs/op | 4,609,883     | 254,966    | 246,774     |

- queue a literal walk's targets in its own output ([be76820](https://github.com/yohimik/dispat/commit/be76820de07a580ed695c5e0d16e4264f42f86da)) (by yohimik, Claude Opus 5.5)
  Finding (contested on size alone): walkLiteral kept an origin map and a
  second queue slice beside its output, five hashed map operations and
  two appends per discovered target, although every queued entry already
  carries its origin and the queue after the sources is exactly the
  output in output order.

  The walk now expands the sources and then reads its own output by index
  as the queue, crediting each target with the source of the entry it was
  reached from. The same targets are discovered in the same order with the
  same source and level, and the trace line names the same origin.
  TestLiteralWalkAllocatesItsOutputAndSeenSet pins a 63-target chain walk
  at 20 allocations (measured 16; the old code made 89).

  Same machine, -benchtime=5x; allocations and bytes are the stable
  figures, timings indicative:

  | benchmark                  | metric    | de0d1ce2      | 0deb7525   | this commit |
  |----------------------------|-----------|---------------|------------|-------------|
  | ComputeChainTopology/4096  | B/op      | 1,698,456,033 | 40,730,585 | 40,338,465  |
  |                            | allocs/op | 4,609,883     | 263,156    | 254,966     |
  | ComputeShallowChain/4096   | B/op      | 1,696,541,068 | 38,127,096 | 37,735,004  |
  |                            | allocs/op | 4,589,440     | 242,665    | 234,476     |
  | ComputeDependencyGraph/4096| B/op      | 23,448,048    | 23,448,024 | 22,367,833  |
  |                            | allocs/op | 109,499       | 109,499    | 109,430     |

  The walk cache commit before this one removed the quadratic walks, so
  what remains here is the constant on the walks still made.

- walk a bounded single-source ask to its own depth ([d5faf32](https://github.com/yohimik/dispat/commit/d5faf328f255e6f13c52087bab27d60160efbbfc)) (by yohimik, Claude Opus 5.5)
  Finding (contested on size alone): a depth-bounded request from one
  source computed and cached that source's unbounded reach and returned
  its prefix, so P units written '^' on a chain cost Theta(P^2) targets
  where the literal reading reaches P - 1 (appendix B.7a's cost
  workload). compose took the same route for each of its sources.

  A bounded ask now reads the prefix of the source's unbounded walk only
  when that walk is already kept; otherwise it walks to its own depth and
  keeps the result under the source, depth and kinds, charged to the same
  budget. A breadth-first walk cut off at depth d lists levels 0..d in the
  order the unbounded walk does, so every answer is the literal walk;
  TestCachedWalkIsTheLiteralWalk checks every route against walkLiteral
  and TestBoundedWalkWalksItsOwnDepth pins one walked target for a '^'
  ask on a chain. The spill flag, which only routed bounded asks after
  the budget was exceeded, is gone.

  Same machine, -benchtime=5x; allocations, bytes and walkTargets are the
  stable figures, timings indicative (the machine is shared):

  | benchmark                    | metric         | de0d1ce2      | this commit |
  |------------------------------|----------------|---------------|-------------|
  | ComputeShallowChain/1024     | walkTargets/op | 523,776       | 1,023       |
  |                              | B/op           | 203,698,360   | 9,412,268   |
  |                              | allocs/op      | 621,077       | 60,890      |
  |                              | peakHeap_MiB   | 52.06         | 6.32        |
  |                              | ns/op          | 73,985,983    | 5,902,892   |
  | ComputeShallowChain/4096     | walkTargets/op | 4,196,055     | 4,095       |
  |                              | B/op           | 1,696,541,068 | 38,127,096  |
  |                              | allocs/op      | 4,589,440     | 242,665     |
  |                              | peakHeap_MiB   | 361.3         | 26.62       |
  |                              | ns/op          | 569,861,675   | 15,864,750  |
  | ComputeChainTopology/1024    | B/op           | 204,299,246   | 10,008,918  |
  |                              | allocs/op      | 626,202       | 66,010      |
  |                              | peakHeap_MiB   | 54.10         | 6.96        |
  | ComputeChainTopology/4096    | B/op           | 1,698,456,033 | 40,730,585  |
  |                              | allocs/op      | 4,609,883     | 263,156     |
  |                              | peakHeap_MiB   | 344.9         | 25.44       |
  |                              | ns/op          | 569,956,708   | 17,302,542  |

  de0d1ce2 is the base revision 3d5129a1 with the benchmarks and the
  walkTargets counter added. BenchmarkComputeDependencyGraph is unchanged
  (109,499 allocs/op at 4096 before and after).

- read control windows off one listing of the control history ([615f988](https://github.com/yohimik/dispat/commit/615f9882d6786c92ec7d12ba63e16c2340cefb8d)) (by yohimik, Claude Opus 5.5)
  Finding (refuted on size, the fix small and safe): each control window
  re-walks the control graph and copies the remaining history as
  gitx.Commit values.

  The control inventory is listed as commits once, when its parent graph
  is indexed, and a control window is that listing with the boundary's
  ancestor closure left out, the same closure walk as before, which load
  reads in place as it reads a union walk's windows. No window copies the
  history any more; the whole-history control window, which every plan
  with control intent loads, was such a copy and is now the listing
  itself. Windows, their order and their keys are unchanged; the window
  tests read the same commits through a test helper that applies the
  exclusion.

  BenchmarkControlWindows (20,000 control commits, 64 windows; the loop
  now takes each window's size off its exclusion set where it took the
  copy's length), -benchtime=20x, same machine (timings indicative):

  | metric    | c7b14d32   | this commit |
  |-----------|------------|-------------|
  | B/op      | 12,357,469 | 6,726,218   |
  | allocs/op | 80,265     | 80,266      |
  | ns/op     | 8,729,992  | 5,810,906   |

  BenchmarkComputeFleetSnapshot repositories=16 is unchanged by this
  commit (13,361,364 B/op, 114,296 allocs/op): its control history is 256
  commits.

  The walk per boundary stays: CCME 13.11 accepts a walk per boundary,
  and the boundaries are few.

- read composed union windows in place and intern keys unbuilt ([169cf10](https://github.com/yohimik/dispat/commit/169cf1083bb44818607e7b40559e9c868c364df8)) (by yohimik, Claude Opus 5.5)
  Finding (refuted on size alone, the fix small and safe): composed
  pending windows are materialised as per-boundary commit copies and
  qualified-key strings.

  A window its repository's union walk read is now indexed straight off
  the walk, skipping what its boundary excludes, instead of being copied
  into a listing of its own first. A commit's qualified key is spelled
  into a buffer the window index reuses and looked up there; only a key
  no window has interned yet is built as a string. Most commits of a
  window are in another window of the same repository, so a window costs
  its key list and membership set and nothing per commit it shares. The
  keys, their order and every window are unchanged.
  TestOverlappingWindowsShareTheirKeys pins a second window over 400
  indexed commits at 13 allocations (the old code made 413).

  Same machine, shared with other agents: allocations, bytes and heap are
  the stable figures, timings indicative.

  BenchmarkComputeRealHistory, shape=composed, -benchtime=3x:

  | commits | metric       | c7b14d32    | previous commit | this commit |
  |---------|--------------|-------------|-----------------|-------------|
  | 10000   | allocs/op    | 205,276     | 166,390         | 97,757      |
  | 10000   | B/op         | 36,234,026  | 30,608,658      | 18,519,394  |
  | 10000   | peakHeap_MiB | 13.56       | 12.10           | 12.15       |
  | 50000   | allocs/op    | 1,083,257   | 885,977         | 463,567     |
  | 50000   | B/op         | 193,292,736 | 166,824,170     | 94,098,901  |
  | 50000   | peakHeap_MiB | 78.61       | 70.49           | 45.73       |
  | 50000   | retained_MiB | 12.43       | 12.34           | 12.34       |

  BenchmarkComputeMergedHistory composed=true and FleetSnapshot,
  -benchtime=20x:

  | case                        | metric    | c7b14d32    | this commit |
  |-----------------------------|-----------|-------------|-------------|
  | MergedHistory composed=true | allocs/op | 520,468     | 174,212     |
  | MergedHistory composed=true | B/op      | 105,900,652 | 45,937,552  |
  | FleetSnapshot repos=16      | allocs/op | 118,867     | 114,296     |

  The membership maps themselves stay: replacing them with rank sets
  changes the composed windows' type for every reader of windowRefs.

- answer the cancellation predicates from per-package masks ([a06a356](https://github.com/yohimik/dispat/commit/a06a3560666cc329b148e6c3074240ae227f21fb)) (by yohimik, Claude Opus 5.5)
  Finding: cancelledFor scans every cancel per query and asks containment
  first: an ancestry question on every query, then a scope lookup and a
  closure call per cancel until the first match.

  When every barrier's closure is its ancestor set in the marker index,
  collectCancels now indexes the cancels once: per package a mask of the
  cancels naming it, and per commit a row of the cancels whose barrier it
  is behind, transposed from the barriers' sets 64 by 64. A query is then
  two lookups and a word of bits per 64 cancels, the lowest set bit
  naming the cancel the loop would have flagged, and containment in the
  baseline is asked only of a commit some cancel reaches. Any barrier the
  index does not answer for keeps the loop for every query. The cancel
  set and its scopes are settled before collectCancels and unchanged
  after it (applyCorrections), so the index never goes stale; the flags,
  and with them W170, are those of the loop.
  TestCancelIndexNamesTheFirstCancelTheLoopNames compares the index with
  the loop for every commit and package over random DAGs with up to 150
  cancels.

  BenchmarkComputeCancelledHistory, -benchtime=20x, same machine, two
  runs each at the previous commit and at this one taken back to back at
  a load average near 7 (the c7b14d32 column was taken earlier under
  another load, and lacks the earlier commits' savings):

  | metric    | c7b14d32   | previous commit     | this commit         |
  |-----------|------------|---------------------|---------------------|
  | ns/op     | 22,362,671 | 8,804,165 8,424,004 | 8,440,727 8,069,450 |
  | B/op      | 45,762,708 | 20,919,525          | 20,961,295          |
  | allocs/op | 118,361    | 117,420             | 117,425             |

  The predicates were about 3% of a plan here; the index costs a word per
  commit per 64 cancels, 42 KB over this union.

- copy only what LocalGitx's commits do not already own ([6778841](https://github.com/yohimik/dispat/commit/6778841baf1dcb3d593f663985f1a02c4e4e11bb)) (by yohimik, Claude Opus 5.5)
  Findings: cloneHistoryCommit re-copies every commit field gitx already
  detached, and the control inventory served from the control index is
  cloned a second time into the window index.

  The canonical copy of a composed window's commit now copies what the
  Git implementation does not already own. LocalGitx's control history
  parser copies every field it keeps, so those commits are kept as
  parsed. Its log copies each commit out of git's output as one record,
  so a commit's text fields, the ones a plan keeps for its lifetime, are
  copied each into a string of its own, and its parents and paths are
  kept as listed: copying nothing at all measured 0.8 MiB more retained
  at 50,000 commits, because a kept message or author then kept its
  whole record. Any other implementation's commits are copied whole as
  before, and the repository and root names are copied once per window
  instead of once per commit. The values are byte-identical and nothing
  writes into a commit's parents or paths. TestLocalCommitsAreKeptAsListed
  pins the allocations of indexing a real repository's window at 7 per
  commit (measured 6.2; the old code made 10.2).

  Same machine, shared with other agents: allocations, bytes and heap are
  the stable figures, timings indicative.

  BenchmarkComputeRealHistory, shape=composed, -benchtime=3x:

  | commits | metric       | c7b14d32    | previous commit | this commit |
  |---------|--------------|-------------|-----------------|-------------|
  | 10000   | allocs/op    | 205,276     | 174,734         | 166,390     |
  | 10000   | B/op         | 36,234,026  | 31,033,600      | 30,608,658  |
  | 10000   | peakHeap_MiB | 13.56       | 13.70           | 12.10       |
  | 10000   | retained_MiB | 2.535       | 2.536           | 2.521       |
  | 50000   | allocs/op    | 1,083,257   | 932,450         | 885,977     |
  | 50000   | B/op         | 193,292,736 | 169,130,160     | 166,824,170 |
  | 50000   | peakHeap_MiB | 78.61       | 73.89           | 70.49       |
  | 50000   | retained_MiB | 12.43       | 12.43           | 12.34       |

  With fakes, which are copied whole, only the per-window names change
  (-benchtime=20x):

  | case                          | metric    | c7b14d32 | this commit |
  |-------------------------------|-----------|----------|-------------|
  | FleetSnapshot repos=4         | allocs/op | 32,912   | 31,386      |
  | FleetSnapshot repos=16        | allocs/op | 118,867  | 114,293     |
  | MergedHistory composed=true   | allocs/op | 520,468  | 510,223     |

- mark every provider's released commits in one marker pass ([b47475d](https://github.com/yohimik/dispat/commit/b47475d52fa8a8d873f3b1c0c11b6fce1b4be161)) (by yohimik, Claude Opus 5.5)
  Finding: lazily registered markers run one marker pass each. Measured,
  every single-marker pass of the real-history and merged-history
  benchmarks comes from releaseCommits, which marked one provider's
  in-union release commits per pass: 28 single-marker passes per plan at
  50,000 commits, 55 in the merged history, each a walk of the union and
  a C-word mask.

  The first delivery question now lists the released commits of every
  package that provides for another and marks them in one call, which
  mark() splits into batches of 512. A package asked about that provides
  for nobody is still listed, and marked, on its own. Which bit a marker
  takes decides nothing (ancestry.go), and the sets are the same, so
  every answer is unchanged; the markers retained for providers no
  question reaches are one C-bit set each, and only a budget past two
  thousand markers over a million commits would send a question to git,
  whose answer for a repository with trusted parents is the same.
  TestReleaseCommitsMarkEveryProviderInOnePass fails on the old code: the
  next provider's release was not indexed by the first question.

  Same machine, shared with other agents (load average 17 to 50):
  allocations and bytes are the stable figures, timings indicative.

  BenchmarkComputeMergedHistory, -benchtime=20x:

  | case           | metric       | c7b14d32    | previous commit | this commit |
  |----------------|--------------|-------------|-----------------|-------------|
  | composed=false | B/op         | 45,697,463  | 23,058,063      | 20,854,313  |
  | composed=false | allocs/op    | 118,455     | 117,527         | 117,512     |
  | composed=false | peakHeap_MiB | 37.85       | 16.41           | 12.66       |
  | composed=true  | B/op         | 105,900,652 | 105,859,979     | 103,675,752 |

  BenchmarkComputeRealHistory, -benchtime=3x:

  | shape/commits  | metric | c7b14d32    | this commit |
  |----------------|--------|-------------|-------------|
  | whole/10000    | B/op   | 49,066,925  | 46,771,565  |
  | whole/50000    | B/op   | 239,831,776 | 228,543,549 |
  | bounded/50000  | B/op   | 88,028,960  | 59,744,573  |
  | composed/50000 | B/op   | 193,292,736 | 169,130,160 |

  The bounded and composed columns include the earlier commits' savings
  (62,396,866 and 171,747,666 B/op before this one).

- answer a composed union's own tags behind it from the index ([fb7ba39](https://github.com/yohimik/dispat/commit/fb7ba3936776ad882614036732b1cac2e0a02d75)) (by yohimik, Claude Opus 5.5)
  Finding: the composed marker index never records boundaries behind the
  union, so the owed-window decision's question about a provider release
  in the union against a consumer baseline outside it went to git, whose
  first answer loads the whole commit DAG.

  indexRepositoryAncestry now records, as the single-history index does,
  every package's own stable and published release tag that the union
  does not hold as behind it. A tag is reachable from HEAD and a window
  is closed under descendants, so no commit of the union is an ancestor
  of such a tag, which is git's answer. Boundaries in other repositories
  are pins and tuples and stay unknown, and markedAncestor reads the
  record only for a question within one repository, so a question across
  repositories keeps its own path (the control repository's
  observation). TestComposedOwnTagsBehindTheUnionAskNoGit fails on the old
  code with one git question and asks none now.

  BenchmarkComputeRealHistory, shape=composed, -benchtime=3x, same machine
  (shared with other agents at a load average near 50: git calls, output
  bytes and allocations are the stable figures, timings are not):

  | commits | metric            | c7b14d32    | this commit |
  |---------|-------------------|-------------|-------------|
  | 10000   | gitcalls/op       | 11          | 10          |
  | 10000   | gitOutputBytes/op | 1,399,284   | 567,271     |
  | 10000   | B/op              | 36,234,026  | 31,290,205  |
  | 10000   | allocs/op         | 205,276     | 174,680     |
  | 50000   | gitcalls/op       | 11          | 10          |
  | 50000   | gitOutputBytes/op | 7,076,766   | 2,916,537   |
  | 50000   | B/op              | 193,292,736 | 171,747,666 |
  | 50000   | allocs/op         | 1,083,257   | 932,462     |
  | 50000   | peakHeap_MiB      | 78.61       | 67.68       |

  The composed shape's git output is now within 41 bytes of the bounded
  single-history shape's (2,916,496 bytes at 50,000 commits).

- transpose the marker pass 64 by 64 bits at a time ([528a1e5](https://github.com/yohimik/dispat/commit/528a1e509fd3c29ed5352e135270d7db9a441190)) (by yohimik, Claude Opus 5.5)
  Finding: the marker-pass transpose enumerates every set bit, one scalar
  step per (commit, marker) ancestor pair, which on a chain with nested
  markers is about C*m/2 steps against the pass's own C*ceil(m/64) words.

  The rows are now transposed into the markers' columns a 64-by-64 block
  at a time, six rounds of shifts and masks per block, a block with no bit
  set skipped, and each column's size taken by popcount. The sets are the
  same bits with the same sizes: TestTransposeBlockSwapsRowsAndColumns
  checks the block transpose bit by bit, and
  TestAncestryIndexAgreesWithAWalk checks every set against a walk,
  including a batch past one word.

  BenchmarkMarkerPass (new, 50,000-commit chain, markers spread along it;
  it runs unchanged at c7b14d32), -benchtime=20x -count=3, same machine.
  The machine was shared with other agents at a load average near 50, so
  the timings are indicative; the allocations are unchanged by design.

  | markers | metric    | c7b14d32 (3 runs)          | this commit (3 runs)      |
  |---------|-----------|----------------------------|---------------------------|
  | 72      | ns/op     | 5,299,817 8,157,590 8,217,831 | 1,213,308 5,579,508 6,002,644 |
  | 72      | allocs/op | 164                        | 164                       |
  | 512     | ns/op     | 59,203,044 75,949,367 63,612,167 | 4,082,946 17,729,885 25,005,771 |
  | 512     | allocs/op | 1,052                      | 1,052                     |

- rank union walks a word at a time and read owed windows as one ranked list ([0ba6c95](https://github.com/yohimik/dispat/commit/0ba6c950b55fe438240b7367d5971023e1ca656e)) (by yohimik, Claude Opus 5.5)
  Findings: legacy union windows are built by one closure call per rank
  per boundary (windows.go), and owed windows copy the whole union walk
  once per boundary before buildUnion discards all but the first-seen
  commits (owed.go readUnionLists, reported twice).

  A window is now the union minus its boundary's marker bits, built a
  word at a time (commitSetWithout), O(C/64) per window where a test per
  rank was O(C). The union is ranked from the windows by one helper,
  rankByWindows, which both paths share: the longest window first, then
  what each shorter one adds in walk order, ties in boundary order,
  visiting a word of commits at a time, and the walk itself when the
  longest window is all of it. The owed windows no longer materialise a
  []gitx.Commit per boundary: buildUnion gets the one ranked list and the
  statistics and the debug log take each window's length from its
  exclusion set. Ranks, the union's order and every window are unchanged:
  TestRankByWindowsIsBuildUnionsRanking compares the ranking with
  buildUnion over per-boundary listings, TestCommitSetWithoutIsTheComplement
  compares the windows with a rank-by-rank build, and
  TestMergedHistoryPlansAsReadOneByOne compares whole plans read through
  the union walk with plans read a window per boundary over a merged
  history whose windows are not nested and whose owed windows reach past
  the union.

  Same machine, shared with other agents: allocations are the stable
  figure, timings indicative only (the unchanged shapes moved as much).

  BenchmarkComputeMergedHistory, -benchtime=20x:

  | case           | metric       | c7b14d32    | this commit |
  |----------------|--------------|-------------|-------------|
  | composed=false | B/op         | 45,697,463  | 23,058,071  |
  | composed=false | allocs/op    | 118,455     | 117,523     |
  | composed=false | peakHeap_MiB | 37.85       | 14.86       |
  | composed=false | ns/op        | 15,869,383  | 18,678,267  |
  | composed=true  | B/op         | 105,900,652 | 105,821,280 |
  | composed=true  | allocs/op    | 520,468     | 519,582     |

  BenchmarkComputeRealHistory, -benchtime=3x:

  | shape/commits  | metric       | c7b14d32   | this commit |
  |----------------|--------------|------------|-------------|
  | bounded/10000  | B/op         | 16,607,741 | 12,642,018  |
  | bounded/10000  | peakHeap_MiB | 7.149      | 6.415       |
  | bounded/50000  | B/op         | 88,028,960 | 62,396,866  |
  | bounded/50000  | allocs/op    | 359,707    | 359,282     |
  | bounded/50000  | peakHeap_MiB | 44.98      | 33.21       |
  | whole/50000    | B/op         | 239,831,776| 239,780,736 |
  | composed/50000 | B/op         | 193,292,736| 193,313,602 |

  The composed shapes read their windows per repository (history.go) and
  are untouched here.

- list the owed pairs in one pass instead of a walk per provider ([fefd02d](https://github.com/yohimik/dispat/commit/fefd02d94d57cf6bc25c167f2968c17b5d2ec89a)) (by yohimik, Claude Opus 5.5)
  Finding: listOwedPairs runs one unbounded walk per released provider,
  Theta(sum |reach(P)|), quadratic on a dependency chain, for an answer of
  a few distinct baselines per provider.

  The pairs are now read off one pass over the plan order, consumers
  first. For each package the pass keeps the distinct baselines its walk
  meets, in the order the walk first meets them: the walk's queue is
  first-in first-out, so a baseline is first met one level below the
  least level a direct consumer's own walk meets it at, in the first such
  consumer, where that consumer's walk first meets it. The cost is
  O(P + E*B) for B distinct baselines, one list family per provider
  repository in a fleet. The pairs, their consumers and their order are
  unchanged; TestOwedPairsAreTheLiteralWalksPairs compares them with the
  literal walks on 3,000 random DAGs in one history and in fleets, and
  TestOwedPairsAreLinearOnAChain pins the listing at 2 allocations per
  package (measured 1,046 for 1,024 packages; the walks allocated
  563,294). The debug log's "pairs" now counts the distinct pairs.

  BenchmarkComputeChainTopology, -benchtime=3x, same machine (shared with
  other agents: allocations are the stable figure, timings indicative):

  | packages | metric       | c7b14d32      | this commit   |
  |----------|--------------|---------------|---------------|
  | 1024     | ns/op        | 92,965,611    | 72,041,347    |
  | 1024     | B/op         | 204,560,237   | 204,306,408   |
  | 1024     | allocs/op    | 632,365       | 626,212       |
  | 1024     | peakHeap_MiB | 49.02         | 51.72         |
  | 4096     | ns/op        | 1,377,247,625 | 555,683,569   |
  | 4096     | B/op         | 3,438,576,216 | 1,698,849,362 |
  | 4096     | allocs/op    | 8,937,420     | 4,609,913     |
  | 4096     | peakHeap_MiB | 387.3         | 361.9         |

  At 1,024 packages the units' own single-source walks, which the owed
  listing used to pre-fill the walk cache with, now compute the same
  unbounded walks, so the total barely moves there; that cost belongs to
  the propagation walks (propagate.go).

### Dependencies

- [config](https://github.com/yohimik/dispat/releases/tag/pkg/config/v1.1.0-rc.0): 1.0.2-rc.1 -> 1.1.0-rc.0
- [manifest](https://github.com/yohimik/dispat/releases/tag/pkg/manifest/v1.2.2-rc.2): 1.2.2-rc.1 -> 1.2.2-rc.2
- [scanner](https://github.com/yohimik/dispat/releases/tag/pkg/scanner/v1.2.2-rc.2): 1.2.2-rc.1 -> 1.2.2-rc.2
- [writer](https://github.com/yohimik/dispat/releases/tag/pkg/writer/v1.2.2-rc.2): 1.2.2-rc.1 -> 1.2.2-rc.2

### Authors

- yohimik
- Claude Fable 5.1


## services/dispat/v1.11.0-rc.7 (2026-10-24)

### Features

- add an optional tests stage and name the version stage by its key ([45c5768](https://github.com/yohimik/dispat/commit/45c57689121e31c15e86746dba32edab5d299ca4)) (by yohimik, Claude Opus 5.5)
  A release can run a tests stage between the build and the publish:
  `flow.tests`, `flow.beforeTests` and `flow.postTests` name its scripts and
  hooks on every level a flow is stated. Its job is checking what the build
  produced, so a package whose artefact fails is never published: the
  failure is the `tests` stage, revertOnFail and onFail apply, and its
  consumers are blocked as after a failed build. The build's exports reach
  it and its own reach the publish. It shares the build budget, and with
  worker nodes it is placed under the build's runOnly value, preferring
  the node that built the package, and given the package's admitted
  outputs. A package that configures none of the three has no tests
  stage, and its release runs exactly as before.

  The stages are data: one ordered table of descriptors (sign, version,
  syncLock, build, tests, publish) states each stage's name and hook
  spelling, its budget, where its frame may run, what a node installs
  first, whether its outputs are admitted and whether its commands wait
  for authorization. The task graph chains the stages a package has in
  that order, and the executor reaches a coordinator through one Stage
  call carrying the descriptor in place of one method per stage. Build
  and publish assignments keep their kinds and formats; any other stage
  travels under the generic kind `stage`, whose assignment names it.

  The version stage now runs under the name of the key that configured
  it. Under flow.propagate or autoPropagate it is `propagate` in
  DISPAT_STAGE, DISPAT_FAILED_STAGE, the failed stage, the events, the
  webhooks, the log and its hooks' names (beforePropagate,
  postPropagate); under the version keys it stays `version`. The stage's
  entry decides first, then the native block, then the hooks.

  The plan digest carries the tests commands and the stage's name, so its
  schema is dispat-plan-digest/3.

### Fixes

- name the agent guide the AI agent skill (agent work guide) ([07ed726](https://github.com/yohimik/dispat/commit/07ed726f4c2bd757c01f24477a859d8a7f5fc253)) (by yohimik, Claude Opus 5.5)
  The guide is the skill an AI agent follows to inspect, test, release and
  recover a dispat repository. Its title, the READMEs, CONTRIBUTING, the
  CLI reference page, the requirement matrix and `dispat --help` now give
  it that full name at their first mention, and the agent skill after it.

### Dependencies

- [models](https://github.com/yohimik/dispat/releases/tag/pkg/models/v1.11.0-rc.6): 1.11.0-rc.5 -> 1.11.0-rc.6

### Authors

- yohimik
- Claude Opus 5.5


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

