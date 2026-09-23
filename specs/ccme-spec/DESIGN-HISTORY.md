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
