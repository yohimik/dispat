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

The profile specifies capacity limits, publication fencing and reconciliation, output admission and conformance
cases. It is unimplemented and unmeasured: this draft does not establish a performance improvement, execute its
conformance vectors, change the message grammar or lift the parser hold. Implementation and experimental
validation require separate evidence. Version markers remain owned by the specification release process.


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
