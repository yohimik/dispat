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
the transfer of dependent outputs that section 28.7 names as the profile's required gain. That fixture is the LLVM pair
(llvm, lld, polly and a bundle; two n2-custom-14 workers and one e2-standard-4 orchestrator; install trees compared with
the prefix masked), whose two attempts of 2026-09-23 were discarded and are no measurement: the first failed lld and
polly because the fork's recipe fingerprinted umask-dependent modes that the transport normalises to 0644 and 0755,
since corrected in the fork to the executable bit and link targets; the second built and published llvm, lld and polly
through the workers in 27 min 21 s and then failed the bundle on an orchestrator without the C++ compiler its identity
record needs. No speedup of the profile or of the engine is claimed from any of this. Sections 28.1, 28.2, 28.4, 28.5,
28.6, 28.8 and 28.9 gained the rules this implementation showed to be missing: local execution captured like a worker's,
unclaimed work outside capacity, an unreadable tip not counted as seen, outermost-first materialization, the phase a
cancelled attempt stopped in, revocation limited to unadmitted branches, and the summary shape of a prepared provider.


## 2026-09-22: Delivery discharges a propagated contribution

A consumer with a change of its own proceeds past a provider whose publication failed (section 19.3). Under the former
admission rule, "the target has not released past the unit's commit", such a consumer was never planned again once the
provider published, and stayed on the provider's old version without a diagnostic: the orphan of section 13.7a, reached
from the consumer's side. The bump axis now admits a contribution until the source has delivered it (section 13.4a): the
target has released at or after a release of that source carrying the commit. Every contribution admitted before remains
admitted; the only new admissions are targets that released past a commit before their source did, on a cause of their
own or while the source was held, and those now receive the release as an ordinary catch-up. The channel axis keeps the
window test. Section 19.5 reconciles a proceeding consumer to what its providers have published, never to a planned
version that did not publish. Vectors 80b, 80d and 82c pin the rule; sections 9.2, 13.7a, 13.7b and 13.7c are restated
in its terms. Plans change only in histories where a consumer got ahead of a provider, where the former rule lost a
release the guarantees of section 13.7c promised. The rule was completed the same day from the implementation: a window
per consumer over what its provider released after the consumer last saw it keeps the debt visible once the provider has
released (section 13.3), and a provider is released at the baseline commit of a consumer it still owes only in a run
that releases the consumer after it, `E201` otherwise (section 19.3), because two releases on one commit have no order.
