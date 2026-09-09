# CCME specification

This package contains the Conventional Commits: Monorepo Extension specification under GPL-3.0-or-later. [SPEC.md](./SPEC.md) defines its grammar, release computation and conformance obligations.

## CCME 3.0.0 specification release

This revision adds two contracts for future release engines:

- [VCS adapters](./VCS-PROTOCOL.md): Git by default, or trusted shareable shell commands with defined JSON input/output, complete history snapshots, immutable records and conditional locks.
- [Explicit rollback](./ROLLBACK.md): an exact-version `rollback(scope)` request, package/space handlers, consumer-first withdrawal and durable receipts. A missing handler fails preflight. Published version identities and release tags are retained.

**Dispat 1.8.x and the CCME 2 parser do not implement these additions.** Existing users should read the immutable [CCME 2.0.0 specification](https://github.com/yohimik/dispat/blob/specs/ccme-spec/v2.0.0/specs/ccme-spec/SPEC.md). The [dated design history](./DESIGN-HISTORY.md) records this disclosure separately from implementation and experimental validation.

The specification and parser retain the `ccme` major/minor version group. An explicit `release(ccme)` / `Release-As: none` commit holds the parser while this specification releases at 3.0.0. That deliberate hold is not a claim of parser compatibility with CCME 3. Do not lift it without reviewing the resulting group plan and implemented behavior.

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

The verifier checks version declarations, required normative files, local links and license material, and refuses symlinked inputs. During the build it also checks `DISPAT_NEW_VERSION`. The lifecycle suite uses an executable `DISPAT_BIN` or Dispat on `PATH`, disposable repositories, and local-only release records. It verifies packaging and version substitution, not implementation of CCME 3's new protocols. Their conformance vectors state expected outcomes for future implementations; they are not passing Dispat tests.

The package uses the documented [replacing strategy](../../packages/docs/docs/editing/replacer.md#replacing-during-a-release): `autoVersion.manifests: none` disables manifest scanning, and four explicit rules update `VERSION` and the declarations in `SPEC.md`. Examples and unrelated files are preserved. [dispat.yaml](./dispat.yaml) contains the configuration.

Validation brackets replacement through `beforeVersion` and `build`. Multi-file writes are not one atomic transaction; a failed release retains local edits for inspection and must not publish an invalid specification. Version classification follows SPEC.md §17.3. CCME 3 is major because previously inert rollback units gain operational semantics under explicit activation, and full-engine conformance now includes the new contracts.
