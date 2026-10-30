# CCME specification

This package contains the Conventional Commits: Monorepo Extension specification under GPL-3.0-or-later. [SPEC.md](./SPEC.md) defines its grammar, release computation and conformance obligations.

## Current specification release

The current source contains the following additional contracts:

- [VCS adapters](./VCS-PROTOCOL.md): Git by default, or trusted shareable shell commands with defined JSON input/output, complete history snapshots, immutable records and conditional locks.
- [Explicit rollback](./ROLLBACK.md): an exact-version `rollback(scope)` request, package/space handlers, consumer-first withdrawal and durable receipts. A missing handler fails preflight. Published version identities and release tags are retained.
- [Polyrepository Git profile](./SPEC.md#27-polyrepository-git-profile): optional planning across explicitly linked source histories, with repository-qualified revisions, gitlink checkpoints, explicit ambiguous-boundary recovery, and one combined dependency graph.
- [Linked peer topology](./SPEC.md#2711-linked-peer-topology): the same profile without a control repository. Identity and roster activate the fleet; peers carry their own configuration and records, two-sided links join them, a release may start in any of them, and a cross-repository boundary is proven from the links a release recorded rather than from a checkpoint.
- [Distributed task and release execution](./SPEC.md#28-distributed-task-and-release-execution): optional
  compilation, interpretation and hybrid modes across all three history modes. One first node owns engine-managed
  Git pushes and release effects; choreographers execute eligible tasks. Pinned planning inputs, a verified
  return channel and exact task outcomes preserve release authority in ordinary repository records.
- [Build readiness](./SPEC.md#192a-build-readiness): what a consumer's build waits for in its provider, `none`, `build` or `publish`; the provider publishes first under all three.

External VCS adapters and rollback remain specification contracts rather than claims about the current dispat implementation. Existing CCME 2 users should read the immutable [CCME 2.0.0 specification](https://github.com/yohimik/dispat/blob/specs/ccme-spec/v2.0.0/specs/ccme-spec/SPEC.md). The [dated design history](./DESIGN-HISTORY.md) records disclosures separately from implementation and experimental validation.

The dated [design history](./DESIGN-HISTORY.md) states which distribution-mode contracts the dispat binary
actually implements and which remain future work. The specification of all three modes does not claim that
the current binary implements them. Conformance vectors are required cases for advertised modes, not speedup
evidence. Repository entry points remain independent of a node's per-run first-node authority.

The parser at `pkg/ccme` has `versioning: none`: its unsuffixed Go module remains on the published v1.0.0
line and is not tagged or published by a specification release. Its scripts may still run. A specification
change does not imply that the parser or any pending engine package is released or conformant; inspect each
package's versioning policy and the dated design history separately.

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

The mathematical claims have explicit premises: [§13.7c](./SPEC.md#137c-guarantees) states the retry guarantees and
[§13.11](./SPEC.md#1311-complexity-and-scale) gives the complexity accounting and scheduling proof. The scheduling
bounds assume a failure-free task graph and the stated resource model; they do not prove unique recovery architecture,
minimum memory use or a duration bound across failures. The package verifier and lifecycle suite do not machine-check
these proofs.

The package uses the documented [replacing strategy](../../packages/docs/docs/editing/replacer.md#replacing-during-a-release): `autoVersion.manifests: none` disables manifest scanning, and four explicit rules update `VERSION` and the declarations in `SPEC.md`. Examples and unrelated files are preserved. [dispat.yaml](./dispat.yaml) contains the configuration.

Validation brackets replacement through `beforeVersion` and `build`. Multi-file writes are not one atomic transaction; a failed release retains local edits for inspection and must not publish an invalid specification. Version classification follows SPEC.md §17.3. The optional polyrepository profile is a minor addition because omitting it preserves the existing single-repository plan exactly.

A previous release passed standalone version-stamping tests but created a specification tag whose files still
contained the old version. Its custom `flow.version` script had not run because the package had neither provider
updates nor native `autoVersion`. Keep the regression at the release boundary: run the actual package configuration
in a disposable repository and inspect the version declarations in the committed tag, not only the working files.
The build must reject a version different from `DISPAT_NEW_VERSION`, even when all declarations agree with each other.
