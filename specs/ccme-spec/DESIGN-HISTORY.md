# CCME design history

## 2026-09-06: CCME 3.0.0 design disclosure

The project author, Semen Fediukovich (yohimik), proposed publishing two specification additions ahead of their implementation:

- Git remains the default version-control backend. Shareable trusted shell commands can adapt other backends through defined request/response formats while preserving complete history, deterministic ordering, immutable release records and ownership-safe locks.
- An explicit `rollback(scope)` commit directive requests withdrawal of an identified published version. A package or space provides the rollback handler. Missing handlers are preflight errors; completion is recorded separately without deleting release history or reusing versions.

The normative contracts are [VCS-PROTOCOL.md](./VCS-PROTOCOL.md) and [ROLLBACK.md](./ROLLBACK.md). The versioned release tag and Git history identify the exact disclosed text. This dated entry records the design and authorship statement; it is not a claim that dispat implements either feature, that the design has been experimentally validated, or that publication grants exclusive rights.

The parser remains on its existing implementation line under an explicit release hold. The current runtime and its measurements must continue to cite the specification/behavior they actually implement. Future implementation and experimental validation should be recorded as separate dated entries.


## 2026-09-09: Authoring validation boundary

The specification clarifies the relationship between optional commit-message validation and CCME 3's VCS adapters.
Creating a source revision is distinct from writing an immutable release record. Git-specific authoring arguments are
not adapter requests, and validation must account for native editor, hook and cleanup effects. This is an informative
clarification, with no grammar, release-plan, adapter capability or full-engine conformance change. It does not claim
that external adapter support or rollback has been implemented.


## 2026-09-21: Distributed execution profile draft

[Section 28](./SPEC.md#28-distributed-task-and-release-execution) defines an optional execution profile for single,
specified and discovered Git histories. One orchestrator acquires every participating repository's lock before
planning; workers execute identity-bound tasks under the same configuration schema without release-initiation
authority. Shared manifest and lockfile preparation precedes parallel builds. Verified output bytes, including
ignored JS build products, move between dependent tasks through temporary Git branches or referenced immutable
bundles. Transport commits remain outside native release ancestry; ordinary source records preserve recovery.

The profile specifies capacity limits, publication fencing and reconciliation, output admission and conformance cases.
It is unimplemented and unmeasured: this draft does not establish a performance improvement, execute its conformance
vectors, change the message grammar or lift the parser hold. Implementation and experimental validation require separate
evidence. Version markers remain owned by the specification release process. The entry of 2026-09-22 records the
implementation.


## 2026-09-21: Record authority under the lock

An audit of the release transaction found that taking the release lock before planning serializes runs without
isolating them: a checkout that lacks release records its remote already holds plans a released version again, under
a lock it holds legitimately. [Section 13.2](./SPEC.md#132-load-tags) now requires a write-capable run to compare the
authoritative store's release records with its planning input under the lock, as `E196` or `E191`, and
[section 19.1](./SPEC.md#191-tagging) makes a release tag create-only. The Git mapping of
[VCS-PROTOCOL.md](./VCS-PROTOCOL.md#5-git-default-mapping) states how the built-in driver restores the snapshot
condition Git's push does not give it. Section 28 gains the same rule for an orchestrator, the treatment of a record
whose publication was authorized before a lock was lost, the evidence an operator needs before removing an abandoned
lock, and an optional time bound on publication authorizations. Section 13.11 gains informative cost rows for the
execution profile, and section 27.11 states which of the equally small link proposals a minimal topology should
prefer. No message grammar changes.


## 2026-09-22: Distributed execution implemented

The dispat release engine implements the execution profile of [section
28](./SPEC.md#28-distributed-task-and-release-execution) in every history mode. Its transport is the Git mailbox of
section 28.4: an orchestrator and its workers share a Git repository, every message is a signed tree on a branch named
`dispat-worker-<node>-<date>-<kind>-<random>`, every transition is one compare-and-swap ref update, and a worker polls;
nothing connects to a worker. The node settings are `execution.role`, `execution.concurrency`, `execution.workers`
(orchestrator), `execution.name` and `execution.endpoint` (worker), `execution.secretEnv`, `execution.timeouts` and
`execution.transfer`. A package or space declares what its build leaves behind (`buildOutputs`), where its outputs run
(`buildPlatforms`) and where its stages may be placed (`runOnly: both`, `worker` or `orchestrator`, one value or a build
and publish pair). The orchestrator is one more node of the pool, chosen last. Builds and publications run on workers; a
publication is delegated only by an explicit `runOnly`; a space that logs in to its registry publishes on the
orchestrator, and a file asking otherwise is refused at load. A package placed on the orchestrator is exempt from the
workers' platform check. A provider the run does not release is prepared once per run under the non-release environment
when a consumer reads its outputs. Outputs travel as result trees on the branches (section 28.5); there is no bundle
service, and vector 17 is the mode in use. Every log line and webhook event names its sending node. Failures carry the
six categories of section 28.9 beside the engine's own codes `E225` to `E229` and `W244`; the existing `E220` to `E222`,
`E335` and `E336` carry `native-recording-or-lock` as their class; the field is attached where a distributed run loses
its lock, and not yet at the older recording and lock sites, which is a departure to be closed by attaching it there.

Not implemented, so the conditional vectors do not apply: reuse of verified outputs across runs (vector 20) and the
rollback profile of section 26 (vector 26). Departures from the profile as written: the platform check of section 28.2
runs before dispatch for the packages a run releases and, for a prepared provider, at its placement, where an
unsatisfiable platform fails the provider's consumers. Coordination and recording were measured on Linux nodes against
the LLVM monorepo: a probe answers in about 4 seconds, an assignment is claimed 3 seconds after it is written on a warm
node and 37 seconds on a cold one, and a result is noticed within 5 seconds. A cold worker fetches the snapshot branch
before it can read its first assignment, so its first claim comes minutes after a warm worker's; placement takes the
first free slot, so a pool of unequal nodes waits for its slowest. The first transport of a snapshot that descends from
the planned head carries that repository's whole history, 7.3 million objects for LLVM, and takes 17 to 20 minutes from
a small orchestrator, and it is paid again on every run because cleanup removes every ref. That cost belongs to a
mailbox that starts empty: a mailbox that already holds the repository's objects, the repository's own remote for one,
receives no history, which is what the engine does where the remote is used as the mailbox. Section 28.4 now also
permits a durable mailbox ref or a history-free snapshot, and neither is implemented. Two same-hardware pairs were
measured on 2026-09-22 on the Linux kernel v7.3-rc4 (commit 93f51579, fork branch codex/kernel-parity at c699bf3d,
dispat f0c9e959), each once with one c3-standard-22 node alone (Xeon Platinum 8481C, 11 cores) and once with that node
plus one t2d-standard-8 worker, under the same recipe and per-cell settings: the hexagon group (defconfig, tinyconfig,
allnoconfig) took 122.62 s alone and 92.22 s with the worker building allnoconfig, 95.89 s on a second paired trial; the
m68k_nommu group (five cells) took 138.01 s alone and 115.48 s with the worker building one cell, 115.61 s on a second
paired trial. Kernel sources were warm and compiled outputs cold on every run; a paired release moved about 512 KB of
exported source and no build outputs, and paid the 3 to 8 s of preflight a single node never pays; every run produced
the same revision, toolchain identities and case lines under the recipe's own comparison. The single node ran once per
group, so variability was not measured; the worker is a slower machine of another family, so each pair is one node
against that node plus a second, slower one; and the single-node m68k_nommu figure includes 58 to 66 s of source-lease
contention on two of its cells, the same five cells having measured 128.17 s outside the engine. What the pairs show is
the placement of independent cells on a second node, which is all these groups have to distribute; they do not exercise
the transfer of dependent outputs that section 28.7 names as the profile's required gain. That fixture is the LLVM pair,
measured on 2026-09-22 on the fork yohimik/llvm-project, branch dispat-gce at d8fb8408 (the same tree as the
orchestrator's 6c1a5fcd, whose revision the single-node products embed), recipe .ci/dispat/build.py with LLVM_FAST=1,
LLVM_SKIP_CHECKS=1, 14 build jobs, 2 link jobs and an uncompressed bundle, five packages from 24.0.0 to 24.1.0: llvm for
X86 and llvm-aarch64 from the same sources, lld and polly consuming llvm's outputs, and a bundle of the three X86 trees
built on the orchestrator with no declared outputs; Ubuntu 24.04.5, g++ 13.3.0, cmake 3.28.3, ninja 1.11.1, dispat
1.11.0-rc.4+main.272772cb. One repetition each, by GNU time from invocation to exit: on one n2-custom-14 node alone (7
cores, two build slots) 41 min 12 s, the two heavy builds sharing the node for about 39 min, lld and polly 2 min 16 s,
the bundle 18 s; on an e2-standard-4 orchestrator with two such workers, the mailbox being the orchestrator's bare
origin, 27 min 49 s: llvm in 22 min 8 s on one worker and llvm-aarch64 in 23 min 22 s on the other, each including its
result push, then lld in 1 min 47 s and polly in 2 min 13 s on the opposite workers once llvm's outputs had reached
them, and the bundle in 32 s on the orchestrator. Sources were warm on every node and outputs cold on both sides. llvm's
declared outputs, 1.4 GB in 2647 files, were pushed once and fetched by two consumers and the orchestrator,
llvm-aarch64's about the same, lld's 420 MB and polly's 22 MB likewise; the bundle's 1.85 GB never travelled. The bundle
step verified every provider's receipt against the installed tree, so the transported bytes are what the workers built,
and the two sides' products differ only in what the recipe embeds about its checkout: the compiled-in revision and the
build-id derived from it, the repository string, and RPATH and source paths; libLLVMCore.a, which embeds none of these,
is byte-identical, and byte identity throughout needs LLVM_APPEND_VC_REV off, a file prefix map and a relative RPATH,
which this recipe does not set. What the pair shows is the profile's required mechanism at work on a real dependent
graph: lld and polly consumed llvm's outputs on nodes that never built llvm. It is one repetition per side, so
variability is unmeasured; the distributed side had two build nodes against one; and the two heavy builds are one
project for two targets, a fair matrix but not two projects. The seven other attempts were no measurement: two failed on
the fork's fingerprint of umask-dependent modes, since corrected to the executable bit and link targets; one planned
from the initial versions because the clone lacked its tags; one was interrupted by a fixture change; one failed its
bundle on an orchestrator without a C++ compiler; one failed its bundle on a transport defect, captured outputs passing
through Git's text conversion, which rewrote CRLF pairs inside three libraries, fixed on dispat's main at 9f649f88 by
hashing and indexing captured outputs with no filter; and one is the negative case section 28.7 predicts: a fixture of
one heavy package with two small consumers took 26 min 47 s distributed against 20 min 16 s on one node, because moving
a 1.4 GB install tree to consumers that build in two minutes cannot pay for itself. No speedup of the profile or of the
engine is claimed beyond what these pairs measured. Sections 28.1, 28.2, 28.4, 28.5, 28.6, 28.8 and 28.9 gained the
rules this implementation showed to be missing: local execution captured like a worker's, unclaimed work outside
capacity, an unreadable tip not counted as seen, outermost-first materialization, the phase a cancelled attempt stopped
in, revocation limited to unadmitted branches, and the summary shape of a prepared provider.


## 2026-09-22: Delivery discharges a propagated contribution

A consumer with a change of its own proceeds past a provider whose publication failed (section 19.3). Under the former
admission rule, "the target has not released past the unit's commit", such a consumer was never planned again once the
provider published, and stayed on the provider's old version without a diagnostic: the orphan of section 13.7a, reached
from the consumer's side. The bump axis now admits a contribution until the source has delivered it (section 13.4a): the
target has released at or after a release of that source carrying the commit. Every contribution admitted before remains
admitted; the only new admissions are targets that released past a commit before their source did, on a cause of their
own or while the source was held, and those now receive the release as an ordinary catch-up. The channel axis keeps the
window test. Section 19.5 reconciles a proceeding consumer to what its providers have published, never to a planned
version that did not publish. Vectors 80b, 80d and 82b1 pin the rule; sections 9.2, 13.7a, 13.7b and 13.7c are restated
in its terms. Plans change only in histories where a consumer got ahead of a provider, where the former rule lost a
release the guarantees of section 13.7c promised. The rule was completed the same day from the implementation: a window
per consumer over what its provider released after the consumer last saw it keeps the debt visible once the provider has
released (section 13.3), and a provider is released at the baseline commit of a consumer it still owes only in a run
that releases the consumer after it, `E201` otherwise (section 19.3), because two releases on one commit have no order.
A review of the text on 2026-09-23 against a technical report closed six more gaps: the owed window is taken per
reachable pair and is the whole history for a consumer older than its provider's first release; a source owes only
targets that depend on it within the unit's depth, in section 9.2 and the audit of section 13.7b alike; a consumer whose
every cause is owed by a provider that failed in the run is not republished, whatever the edge kind; an exact
`Release-As` is a cause of its own; G6 counts one catch-up per proceeded consumer; and the stable branch of section 13.9
takes a graduation's version from section 11.5. The conformance row written for the second of these found that the
reference engine had attributed a unit's whole source set to every dependent its walk reached, so a consumer of one of
two packages a unit was written over was owed by the other and could be released again on its account; the engine was
corrected in the same candidate, and the row stays as its fence. The dispat engine implements the delivery admission and
the reconciliation of a proceeding consumer; the owed windows and `E201` are not yet implemented, and a consumer that
sits out the run in which its provider releases the owed commit is therefore still stranded in that engine, which its
release notes list as a departure.


## 2026-09-23: Command sweeps on workers

The dispat engine now delegates a command sweep, its `run` command, to worker nodes under [section
28.10](./SPEC.md#2810-command-sweeps), so that its own release workflow can run the full test suite on a machine the
pipeline creates for the run. A sweep takes no release lock: its messages carry a generation derived from the run
identity alone, the plan digest is the whole plan with an empty release selection, equal to the one a read-only
invocation reports, and the refusals of a release with links (worker authority, a worker-role file, a lock bypass, a
missing secret) apply to it unchanged. The task carries the script's commands, the computed and the unresolved
environment and no hooks, on a coordination branch of kind `run`. Execution links may be given on the invocation and are
validated as the file's are; a read-only invocation with links reports the digest and neither probes nor assigns. Sweep
output roots are declared at the root of the entry configuration only and resolve against each package's own repository;
nothing merges until every task has answered, conflicts are found by comparing the manifests before any install, every
set that names a disputed path is withheld, each set installs all or nothing, an interrupted sweep merges nothing, and
an absent root is admitted as an empty set. Two departures remain: a sweep task placed on the orchestrator itself writes
the checkout directly and is not captured, so a conflict between it and a delegated task is not detected, which vector
32 requires; and a node that predates the `run` kind is not refused at preflight, as section 28.2 requires, but leaves
the assignment unclaimed until its deadline. A package restricted to workers now refuses a local sweep as it refuses a
local release. The same work found and corrected a defect in the build transport: a build placed again after a failed
attempt had its outputs checked against the first attempt's identity and refused. The workflow then ran end to end on
2026-09-23: every package's tests as one sweep of seventeen tasks from a clean checkout, on a c3-standard-8 the pipeline
created and deleted, seventeen ran and none failed, coverage of every task merged back, 16 minutes of wall time; no
single-node run of the same sweep was timed beside it, so this is a record of use, not a measurement.


## 2026-09-24: Delivery by tags and ancestry

The provider receipt of CCME 3.1.0-rc.4 is withdrawn. That candidate had every consumer release record name the
provider tags it observed, as `release <tag> dispat-seen-v1:<payload>` in the tag message, and read delivery from that
record. Delivery is again read from tags and ancestry alone ([section 13.4a](./SPEC.md#134a-source-packages)): a source
has delivered a commit to a target when some release of the source carries the commit and the target's baseline reaches
that release. The one state ancestry cannot order, two releases on one commit, is kept from arising unseen by `E201`
([section 19.3](./SPEC.md#193-partial-failure)) rather than recorded, and a debt that the source's later release left
in no ordinary window stays visible through the owed windows of [section 13.3](./SPEC.md#133-pending-window). A tag
written with a receipt remains an ordinary release tag: its message is text the engine does not read, whatever the
payload says and whether or not it decodes.

The dispat engine implements both, which closes the departure recorded on 2026-09-22. An owed window is taken for every
pair of a provider and a consumer the provider reaches over `propagation.kinds`, not only for the edges, and is read
only where it reaches further back than the union of the ordinary windows, so a history in which no consumer got ahead
of its provider reads nothing more. `E201` refuses, before anything is published, a run that would release a provider
at the baseline commit of a consumer it still owes without releasing that consumer after it, and names both remedies:
the consumer in the same run, or the provider after a new commit. A consumer that fails after its provider published on
its baseline commit is reported with the one remedy left, an exact `Release-As` at the version the run planned (section
8.6). The refusal is conservative whenever the consumer's baseline is the head the run starts from: in commit mode the
provider's tag usually lands on a release commit past that head, but a run cannot know before it publishes whether that
commit will be empty. The same work found that a member of a shared-version group that got ahead of its provider was
masked out of the group's freshness test, so the member caught up alone while the rest of its group stayed behind; the
group now moves as one.

## 2026-09-24: The repository is the mailbox

The dispat engine now states the choice section 28.4 leaves to an implementation: the mailbox is the repository's own
remote by default. A worker link with no endpoint, in `execution.workers` or as `--worker name` on the invocation,
reaches the push URL of the remote the release takes its lock on, which in a composed workspace is the entry
repository's, and the worker names that repository as its own endpoint. A snapshot that descends from the planned head
therefore carries no history the remote does not already hold, and cleanup that removes every ref of a run costs the
next run nothing. An endpoint a link states remains an override for coordination that has to live elsewhere, and the
relay between mailboxes is unchanged. The push URL is held to the endpoint rules of section 28.2 when a run that
dispatches starts, before any lock: one that carries a credential is refused under `execution-configuration`, and a
release checks it again at the destination its lock was taken on. With the repository as the mailbox, the transport
credentials of section 28.4 are credentials that can write the repository; the engine does not narrow them itself, and
its documentation requires the host's branch and tag rules to keep them from release branches, release tags and the lock
tag. The engine's own release gives its worker machine an installation token of a GitHub App minted for the run, an
identity those rules can tell apart from the release job's. The same candidate records the run identity on a `run` line
of the release lock tag, the place section 28.6 requires; verifies ownership before the first worker probe, as section
28.3 requires before dispatch; bounds each read of a lock and retries a failed one before a run stops for it; and reads
the lock back before every publication of a release that delegates nothing as well.

## 2026-10-25: The dispat theorem

Section 13.11 now gives a scoped theorem for the failure-free task makespan of a compiled directed acyclic graph.
A fixed history, configuration, inventory and execution policy determine one semantic plan, but not one schedule
or implementation. With identical interchangeable slots and zero scheduling and transfer time, the span `Lk` and
slot work `Wk / s` lower-bound every schedule. A work-conserving list schedule finishes within
`Wk / s + (1 - 1/s) Lk`: trace the latest-finishing predecessor chain backward from a last-finishing task;
when no task on that chain runs, all slots are busy. Thus the task makespan is asymptotically optimal under the
stated model. For disjoint stage pools the analogous chain argument gives `Lk + sum(W_j / m_j)`, including the
within-three-times build and publication case. The count of pools matters in that factor. Distributed execution
also has node compatibility, transfers, record-unit reservations and owner lanes; the theorem gives valid
resource-load lower bounds but no general matching factor for that full model.

For sufficient compatible capacity to run every ready task through completion, the earliest-start recurrence
`E_i = max(E_j + d_j)` over predecessors has one solution on any finite DAG. A topological induction proves
both its feasibility and that no task can start earlier in another feasible schedule. Dispatching every ready
task immediately therefore has a unique earliest-start vector and finishes at span `Lk`, regardless of how
large the finite task graph is. Other schedules can attain the same makespan by delaying tasks with slack;
this characterization does not imply a unique executor or remove finite-capacity placement choices.

The stronger online result assumes enough compatible capacity for immediate starts. Before a task starts,
its duration is unobserved and can independently be any finite nonnegative value without an upper bound.
If a deterministic nonanticipating policy delays ready task `i` from `R_i` to `S_i > R_i`, let
`P_i` and `Q_i` be longest weighted prefixes and suffixes around `i`. Change only its unseen duration to
`M` larger than the sum of all other durations. The policy has the same observations before `S_i`, while
the new span is `P_i + M + Q_i` and its finish is at least `S_i + M + Q_i`, strictly later. Therefore a
policy exactly fastest for every duration assignment has the unique ASAP start vector. This does not cover
known-zero tasks or known bounded durations. It also does not imply uniqueness at asymptotic order: starting
task `i` at `2E_i` is feasible online and finishes within `2Lk`, while delaying tasks with `E_i > 0`.

The new comparison corollary names its baselines. Indegree events take `O(n + e)` graph work, while an
algorithm rescanning every vertex and edge after every completion takes `Theta(n(n + e))` by definition.
Sequential execution takes `s` versus 1 for `s` independent unit tasks on `s` slots. With `s = k`, a
chain `z_1 -> ... -> z_k` of unit tasks and a duration-`k` leaf `b_i` after each `z_i` has only
`n = 2k`, `e = 2k - 1`, and span `2k`. Immediate dispatch finishes at `2k`; edge-depth barriers take
`1 + k²`, an unbounded ratio. For `s²` independent tasks statically assigned before durations are known,
some worker owns at least `s`; making those `s` tasks long gives static time at least `s`, while dynamic
work-conserving dispatch finishes before `2 + 1/s²` under the stated short-task durations. The latter
comparison does not cover duration-aware placement or stealing. The one-pool list bound holds on every
finite instance, so for fixed `s` it also gives
`E[T_list] <= (2 - 1/s) E[Topt]` under any distribution with finite expected optimum. It proves no
strict average advantage or unique scheduler. Planning work, placement, transfers and retry time stay
outside these ideal task-time comparisons.

The scaling claim now uses `h` identical machines, `c` compatible slots each, and measured uniform per-task
speedup `v`. It gives the lower bound `(1/v) max(Lk, Wk/(h c))` and the matching one-pool list-schedule order
`Theta((Wk/(h c) + Lk)/v)`. A chain finishes in exactly `Lk/v`, independent of machine and slot count. For `n`
independent equal-duration tasks of baseline duration `d`, the exact time is `(d/v) ceil(n/(h c))`: slot load
gives the lower bound and full batches attain it. A bigger machine may add faster tasks, more slots, or both;
more machines add slots. Faster tasks shorten both cases, while extra slots shorten the independent case.
The claim assumes that fixed task durations scale uniformly. Stage, record, owner, transfer and compatibility
limits retain their separate terms.

The explicit task graph occupies `Theta(Tk + Dk)` words if retained as adjacency lists. Its indegree and ready
state add `O(Tk)` words, and each task and edge can be processed once. That is a conditional graph-input and
readiness bound. Priority and placement add their own work, and no universal minimum auxiliary-memory bound is
claimed. A binary heap and a balanced search tree can implement the same deterministic ready priority with the
same asymptotic time and space bounds, so the proposed uniqueness of the executor is false even when semantic
output and priority agree. The ratio `Wk / Lk` marks when the aggregate work lower bound reaches the span, not
when runtime becomes exactly `Lk`; task packing can leave a gap.

The full `plan.Compute` cost is distinct from task graph compilation. Full validation of an explicit
workspace segment, relevant pending message bytes or a supplied tag inventory has an input-reading lower bound;
writing the required `Plan` entries and diagnostics has an output lower bound. Under opaque comparison names,
an unsorted independent workspace makes the least-name order a sort, giving `Omega(P log P)` comparisons;
a heap with one adjacency pass matches `O(P + E + P log P)` for that subproblem. These premises do not
make the full planner jointly optimal. Propagation traversals, ancestry markers, correction probes and
cache state have input-dependent costs with no matching universal auxiliary-memory lower bound.

A concrete family shows why the implementation at commit `74a7fdcd` is not fastest on every input. In a
linear history `c_0, ..., c_P`, give each independent package `p_i` a distinct baseline at `c_{i-1}` and
one direct unit at `c_i` scoped only to itself. Give all commits one author. With interned identifiers
in a word RAM of `w = Theta(log(P + 1))` bits, input and `Plan` output have `Theta(P)` primitive entries;
actual strings and commit metadata bytes are charged separately. Each pending window is a different suffix.
`services/dispat/internal/plan/windows.go` materializes a bitset per boundary through the `commitSet` of
`services/dispat/internal/plan/ancestry.go`, retaining `Theta(P²)` bits. Beyond the fixed marker budget,
the per-boundary fallback can repeat `Theta(P²)` history listing work. Independently,
`services/dispat/internal/plan/authors.go` scans each suffix for attribution, `Theta(P²)` visits although
each release emits one author. On this verified chain, boundary ranks and a shared author aggregate answer
these window and author questions in `O(P)` words, or `O(P log P)` bits, and `O(P)` work. Other planner
phases retain their costs.
This establishes a family-specific gap in the dated
planner, not a lower bound for arbitrary history DAGs or a claim about every future algorithm.

The planner comparison is likewise conditional. Scanning all `P` candidates at each least-ready extraction,
while processing edges once, costs `Theta(P² + E)`; a heap costs `O(E + P log(P + 1))`.
Sparse `E = O(P)` gives a strict order saving; dense `E = Theta(P²)` makes edge reads dominate both.
A proposed adaptive method could verify each repository's materialized pending union is a chain in
`O(C + Au)` time with constant-time indexed union membership, charging index construction separately.
`Au` counts all parent entries of union commits, including edges leaving the union.
It could use rank windows and a shared suffix author aggregate there, with a general DAG fallback.
This adds a linear pass when the fallback already scans those same parent lists and saves window and author
work on the one-author chain family. The engine at `0e495797` does not implement the rank fast path.
No input distribution is asserted, so these are worst-case or stated-family comparisons.

The current fixed-stage task compiler reads `Plan.Providers` and returns an explicit task scheduler graph.
Those retained input and output representations occupy `Theta(P + E + Tk + Dk)` words when both coexist;
their sizes do not give an auxiliary bookkeeping lower bound. Under indexed relations, constant-time map
access and policy calls, graph construction has an upper account
`O(P + E + Tk + Dk + Pr log(Pr + 1))` for `Pr` releasing packages, including changed-name and relay-list sorts.
The scheduler can sort newly ready batches when relay nodes exist, so its current readiness path is not
the table's achievable linear path. A restricted streaming readiness problem does have an exact `n`-bit
lower bound: after a gate completes, each of `n` targets is ready according to whether its independent
source finished earlier. The same read-only graph admits all subsets through durations learned only from
one-pass completion events. With no event replay or external query, and every mutable completion annotation
counted as scheduler RAM, the `2^n` subsets need `n` bits. A bitmap attains the
bound on that family. In `w`-bit words this is `Omega(n/w)`, not `Omega(n)`.

Durable recovery is separate from failure-free scheduling. Publication and its record are two writes against
independent systems, with no general atomicity or undo. A record written first can claim an absent effect.
If the effect happens first, a crash before the record leaves the same local durable state as a crash before
the effect. Blind retry can duplicate an effect or meet an immutable-version refusal; blind skip loses an
unperformed effect. An absence query is also insufficient while an earlier request may still arrive. A retry
needs that request settled or fenced, or destination deduplication. Section 19.4 permits identity-verified
adoption; its `E198` applies when an existing version cannot be identified, not to every uncertain outcome.

The informative executor checks crash readiness before dispatching an irreversible external task: stable effect
identity, a durable record path and a supported way to settle an uncertain attempt. Such a
capability check cannot prove future availability or arbitrary script behavior. Dispatch without the contract
can expose a recovery-ambiguity anomaly after a crash: surviving evidence may not distinguish effect from no
effect. Withholding new work can preserve safety but cannot promise completion or safe takeover before the old
attempt settles. This adds no diagnostic number and
claims no implementation already performs the check. Completed effects are retained, records become durable
before work that requires them or success reporting, and §19.3 applies its blocking and own-cause rules. The
next plan is recomputed from immutable records under §17.2. These are necessary observable invariants of a
saga-style forward-recovery class under the stated crash model, not a unique implementation or a minimum
auxiliary memory footprint. Conditional completion needs fair dispatch, settled attempts, stable inputs and an
eventual crash-free interval long enough for effects and records to succeed. Arbitrary failures have no elapsed
time or retry-work bound. This addition is informative; it changes no grammar, plan or conformance obligation.

An idempotent replay result now states separate premises. Fixed source, configuration, task inputs, toolchain and
environment identities plus deterministic tasks give the same artifact bytes and digest on rebuild; plan
determinism alone does not constrain clocks, randomness, mutable networks or other scripts. A fixed
owner/package/version key bound to source and digest is installed once, adopted only after identity verification,
or rejected on mismatch. The effective immutable-key transition satisfies
`F_{K,J}(F_{K,J}(x)) = F_{K,J}(x)` when the destination
offers atomic request deduplication or the previous attempt is settled and its content verified. An in-flight
request cannot be retried from an absence snapshot alone. The create-only canonical tag likewise accepts a
matching existing identity and never rewrites a different one. Crash cases before effect, between effect and
record, and after record therefore replay the same request, verify/adopt it, or skip it, respectively. New valid
records discharge completed work into a residual plan; they do not leave the whole plan unchanged. Repeated
builds and safe requests may cost extra work without changing the intended final state, while arbitrarily many
failures give no total-work bound. Other external effects need their own idempotency contracts.

The plan, task graph and durable-effect arguments compose within one fixed input snapshot: the semantic plan
determines allowed work, graph edges enforce its predecessors, and verified immutable records let a later
invocation discharge completed effects from an updated record inventory. Other inputs stay fixed and the
retry invariant of §13.7c applies when claiming the same intended final state. Fair retries and eventual
successful settlement give that state under the stated premises. Planning, compilation, scheduling and recovery retain
separate time and memory accounts; this composition proves no joint asymptotic minimum.

## 2026-10-25: Configured stages in the compiled plan

Section 13.11 now states the graph a configured stage joins. A package's stages are a function of its configuration and
of its publish target, fixed at `HEAD`: `version` and `build` always, `publish` unless the target is `none`, the other
built-in stages where configured, and the user stages its flow names. The login that runs once per space is no stage. A
stage a plan does not run stays in the graph at duration zero, so whether a configuration is accepted never depends on
the plan. An entry is a script name or an object whose `before`, `after` and `parallel` constraints each name a stage or
a list, an empty list stating nothing, and a nearer level replaces the whole entry. A level that states both `version`
and `propagate` stays refused when the configuration loads. A name of a built-in stage the package lacks is dropped, and
so is the whole entry of such a stage, so one configuration serves every target. A stage places itself only when a
`before` or `after` name survives that drop, and otherwise keeps its default place. Any other name that is no stage of
the package is refused, and so is a name of the stage that carries it.

A default edge runs into a stage that does not place itself, from the stages of the greatest rank below it, with every
user stage ranked with `tests`. The section gives the consequences as examples: `tests` stays after `build` when `build`
is constrained, `announce` stays after `publish` when `publish` is constrained, an `announce` placed after `build` gates
the publication, a stage that states only `parallel` keeps its place, and, where the package states no constraints, user
stages run beside `tests`. A stage placed after a stage that does not place itself closes a cycle with that stage's
default edges when it is or reaches one of their sources, so `tests` or a user stage placed after `publish` needs
`publish` to place itself; the section says so where it advises placing a stage after the publication. The preparing
stages precede every other stage, and the build precedes the publication. The publication then waits for every stage not
placed after it, and the record is a task of its own that follows the publication, with the stages after the publication
following the record. A package whose target is `none` is recorded after every stage but an `announce` that does not
place itself and what it reaches. A configuration that places such a package's build after that `announce` has it
recorded before it builds, and a failed build then only reports; it is acyclic only where the package has a stage ranked
with `tests` and the build reaches none of them. A lemma proves that these edges add no cycle. Section 19.1's bullet on
target `none` now says that the tag is written once the package's stages before its record have completed. That is a
patch under section 17.3, which defines one by the plan alone: it changes when a record is written and no plan. It does
change conformance for an implementation that runs a build or tests for such a package and tagged it after the manifest
write, since its tag now waits for them.

A stage's failure falls in one of three cases. A stage before the record fails the package, as a failed build does, and
a resumed run runs it again. A stage of the post-publication set starts only once the record is written, and the
next release plan does not resume it. A stage unordered with the record only reports, and it is run again only
when a run stops
before the record; the publication and record edges leave no stage of an accepted configuration in this case. A
configured stage is outside the unrecoverable set, since its effect is not declared irreversible, so a stage whose
effect cannot be repeated belongs in the publish stage or in the post-publication set. Within a package a stage relies
only on the exports of the stages that reach it, and reading any other export is a race the plan does not order. Between
packages every stage of a consumer other than `sign` and `version` counts as its build and waits for what the relation
of section 19.2a names, and a provider's record precedes the publication of every consumer that reaches it over
`publish.orderKinds`. Blocking stays section 19.3's: a consumer with no cause of its own is blocked, one with a cause of
its own proceeds, and a stage that reads a build that never happened fails at that stage for want of its input.

A configuration is realisable when every package meets three conditions, constraints that name stages of the package
other than the stage that carries them, an acyclic stage graph and no `parallel` pair the package graph orders, and the
workspace meets a fourth: the graph of packages over the edge kinds of section 13.1, `propagation.kinds` and
`publish.orderKinds`, and the build dependencies that are not `none` is acyclic. The second and third are necessary,
given the first, for an order that realises the configuration, the first and fourth are premises, and with all four the
closure of each package graph realises its configuration and the compiled plan is acyclic. The fourth refuses a cycle
through a build dependency whose kind is outside the edge kinds of section 13.1, which a task graph can survive; section
19.2a's bullet on cycles now says so. A cycle over the edge kinds of section 13.1 alone is already `E200`, so that is
the only cycle the fourth condition meets. Its known cost is the test fixture of section 13.1: a fixture that depends at
runtime on the library whose tests read the fixture's build is accepted by section 13.1 and refused by an implementation
that offers configured stages, even where no flow is configured. A `parallel` constraint between `publish` and a stage
not placed after it is refused, since the publication waits for that stage, and one between `publish` and a stage placed
after it is refused too, since the publication reaches that stage. A configuration that is not realisable is refused
before any effect, whether or not the package is planned, and alike for a release, read-only planning and a command
sweep. Step 2 of the dispat algorithm compiles the package graphs with their cross-package edges once it finds the
configuration realisable, and the compiled plan's unrecoverable set holds the publications and the records.

The compiled plan's task list names `syncLock`. The revised theorem distinguishes the one-pool idealization
from the distributed resource model. Its aggregate slot lower bound uses the summed node capacity, or the
smaller sum of run-wide stage limits when every positive-duration task draws on one such limit. Each stage's own
work over its slots is another lower bound. Where records use a separate capacity and owner publications and
records are serialized, record work and each owner's work also bound makespan. The specified protocol reserves
recording capacity for a whole publication-and-record unit, so its full duration over that capacity gives a
stronger lower bound. These overlapping constraints have no general upper factor in the theorem. Its stage
budget upper bound assumes disjoint pools, no other binding capacities and zero transfer and scheduler time.
The build readiness case has two such pools. The scaling threshold applies to the aggregate slot lower-bound
term only; stage, record and owner terms may dominate, and actual task packing may leave a gap.

An implementation that gives records a capacity of their own and serializes an owner's publications and records takes a
record unit's recording capacity, then its owner lanes, in the stable order of section 27.7 where there are several, and
only then offers its publication for a slot; a record unit is a publication with its record, or a record alone. Where
record units acquire capacity and owner lanes in one order, no wait cycle forms. Two corollaries follow. Among
configurations with the
same stages, scripts, durations, cross-package edges and stages after the publication, an edge no data dependency needs
never shortens the fastest execution and can lengthen it. The fastest states as constraints the transitive reduction, on
the package's stages, of the closure of the data dependencies, required stage edges, publication and record edges,
and, without a
publish stage, the order its `announce` puts on the stages after it. Once summed node capacity reaches slot work
over span, more nodes cannot lower that aggregate term. Configured parallelism can lower the span term only when
removing an actual chain constraint, and can lower the full lower bound only if no unchanged resource term remains
as large. The cost table gains a row for checking a configured order and the ledger a line for its
adjacency.

Section 19.1's clarification aside, the normative passages are in sections 19.2a and 28.5. Section 19.2a requires an
implementation that lets a configuration add or order stages to keep the publication order and the readiness relation,
to treat a failed stage before the record as a failure of the package, and to refuse a configuration that is not
realisable before any effect. The refusal emits one diagnostic for each carrying stage and stated name against the first
condition, one for each package naming every stage on a cycle, one for each `parallel` pair the package graph relates,
counted once whichever member states it, and one for the workspace naming the packages on a cycle, under the
`execution-configuration` identifier of section 28.9. The diagnostics of the first three conditions are ordered by
package name first, as section 17.2 requires, and the workspace's comes after them. No section 16 number is allocated,
since the categories of section 28.9 are outcome classes and not allocations of new numbers, and `W194` keeps its
condition. Section 28.5 makes the command task of every stage, a user stage and `announce` included, take node capacity
and a stage budget where one applies, as a build does, with the implementation documenting which budget a stage that
section 28.2 does not name draws on. It makes a user stage whose exports a later stage reads a producer of an admitted
output set, which can be empty. It states a stage's inputs: from its own package the admitted sets of the stages that
reach it in the package graph, an output set reaching no stage its producer does not reach, and from another package the
provider's build output under `build`, the published artefact under `publish` and nothing under `none`. No grammar,
release plan or diagnostic number changes, and outside the section 19.1 bullet nothing changes for an implementation
that does not offer configured stages. For an implementation that runs neither builds nor tests, those are stages of
duration zero, so its tag still follows the manifest write.

The section states the model the dispat engine is to implement; nothing of it is released. What runs today is this. A
`flow` entry is a list of script names, and an unknown `flow` key is refused. A package's stages run in the fixed chain
`sign`, `version`, `syncLock`, `build`, `tests` and `publish`, each where present, and `build` and `publish` run for
every releasing package. Provider waits attach to a package's first task, so a consumer whose first task is `sign` or
`version` waits longer than section 19.2a requires. The engine has no publish target of its own: a package with nothing
to publish still runs a publish task, which runs no publish script of its own but passes the space's login gate where a
login is configured. Where tags are written as each package publishes, the package is recorded in that task's tail after
its build and tests, and `announce` runs inside the publish task after the record, so a consumer waits through it. With
a release commit or a composed workspace the tail writes the release recorders and runs `announce`, and the tag is
written when the run finalizes, after every package. A provider that failed at any stage, or was skipped, blocks a
consumer with no cause of its own, and blocks one with a cause of its own too where the provider's space makes its
relation blocking.
