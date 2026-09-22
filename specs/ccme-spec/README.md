# CCME specification

This package contains the Conventional Commits: Monorepo Extension specification under GPL-3.0-or-later. [SPEC.md](./SPEC.md) defines its grammar, release computation and conformance obligations.

## Current specification release

The current source contains the following additional contracts:

- [VCS adapters](./VCS-PROTOCOL.md): Git by default, or trusted shareable shell commands with defined JSON input/output, complete history snapshots, immutable records and conditional locks.
- [Explicit rollback](./ROLLBACK.md): an exact-version `rollback(scope)` request, package/space handlers, consumer-first withdrawal and durable receipts. A missing handler fails preflight. Published version identities and release tags are retained.
- [Polyrepository Git profile](./SPEC.md#27-polyrepository-git-profile): optional planning across explicitly linked source histories, with repository-qualified revisions, gitlink checkpoints, explicit ambiguous-boundary recovery, and one combined dependency graph.
- [Linked peer topology](./SPEC.md#2711-linked-peer-topology): the same profile without a control repository. Identity and roster activate the fleet; peers carry their own configuration and records, two-sided links join them, a release may start in any of them, and a cross-repository boundary is proven from the links a release recorded rather than from a checkpoint.
- [Distributed task and release execution](./SPEC.md#28-distributed-task-and-release-execution): optional orchestration and worker roles across all three history modes, complete locking before planning, verified transfer and reuse of dependent build outputs, and command sweeps that run on workers without a release lock. Temporary Git branches carry task data; ordinary repository records retain release authority.
- [Build readiness](./SPEC.md#192a-build-readiness): what a consumer's build waits for in its provider, `none`, `build` or `publish`; the provider publishes first under all three.

External VCS adapters and rollback remain specification contracts rather than claims about the current dispat implementation. Existing CCME 2 users should read the immutable [CCME 2.0.0 specification](https://github.com/yohimik/dispat/blob/specs/ccme-spec/v2.0.0/specs/ccme-spec/SPEC.md). The [dated design history](./DESIGN-HISTORY.md) records disclosures separately from implementation and experimental validation.

The distributed profile is implemented by the dispat release engine over a Git mailbox transport; the design history's entry for 2026-09-22 states what is implemented, which vectors are met, where the engine departs from the profile and what was measured. The conformance vectors are required cases and are not evidence of speedup. Repository entry points remain independent of execution-node roles; an explicit worker cannot initiate a release.

The specification and parser retain the `ccme` major/minor version group. An explicit `release(ccme)` / `Release-As: none` commit holds the parser when a specification release adds engine behavior without changing message parsing. That deliberate hold is not a claim of parser compatibility with every optional CCME 3 profile. Do not lift it without reviewing the resulting group plan and implemented behavior.

## Optional authoring tools

[VCS-PROTOCOL.md §8](./VCS-PROTOCOL.md#8-commit-authoring-and-message-validation-informative) explains how an optional
commit-message validator relates to the adapter contract. A native source commit is different from a release record.
Git argument forwarding does not implement external adapters, and validating messages does not implement newer CCME
features or prove a release plan is correct. This clarification leaves the existing grammar and release algorithm unchanged.

## Distribution and verification

Each package keeps its own tags. This specification uses `specs/ccme-spec/v{version}`. The `VERSION` file and the three normative declarations in `SPEC.md` retain the published baseline until native `autoVersion` stamps the planned release. The additional protocol files are included in the same specification package, not independently versioned packages.

```sh
sh verify.sh
sh test.sh
```

The verifier checks version declarations, required normative sections and files, local links and license material, and refuses symlinked inputs. During the build it also checks `DISPAT_NEW_VERSION`. The lifecycle suite uses an executable `DISPAT_BIN` or dispat on `PATH`, disposable repositories, and local-only release records. It verifies a minor specification release with the parser held, packaging, and version substitution. It does not turn prose conformance vectors into implementation evidence for external adapters, rollback or distributed execution.

The package uses the documented [replacing strategy](../../packages/docs/docs/editing/replacer.md#replacing-during-a-release): `autoVersion.manifests: none` disables manifest scanning, and four explicit rules update `VERSION` and the declarations in `SPEC.md`. Examples and unrelated files are preserved. [dispat.yaml](./dispat.yaml) contains the configuration.

Validation brackets replacement through `beforeVersion` and `build`. Multi-file writes are not one atomic transaction; a failed release retains local edits for inspection and must not publish an invalid specification. Version classification follows SPEC.md §17.3. The optional polyrepository profile is a minor addition because omitting it preserves the existing single-repository plan exactly.

A previous release passed standalone version-stamping tests but created a specification tag whose files still
contained the old version. Its custom `flow.version` script had not run because the package had neither provider
updates nor native `autoVersion`. Keep the regression at the release boundary: run the actual package configuration
in a disposable repository and inspect the version declarations in the committed tag, not only the working files.
The build must reject a version different from `DISPAT_NEW_VERSION`, even when all declarations agree with each other.
