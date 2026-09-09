# CCME rollback protocol

This document is normative for CCME 3.0.0 and is incorporated by [SPEC.md §26](./SPEC.md#26-explicit-rollback). Requirement words have the meaning defined there. It specifies a future release-engine capability. **dispat 1.8.x and the CCME 2 parser do not implement this directive or the configuration below.**

## 1. Purpose and activation

A rollback withdraws one explicitly identified published package version from its configured destination. It is an external operation, not a Git revert, a `cancel`, a version decrement, or an automatic response to a failed release. It does not undo database migrations, external consumers, or arbitrary side effects unless the declared handler explicitly implements and verifies those effects.

A conforming engine MUST recognize rollback units and show them separately in its read-only plan. Execution MUST require trusted repository configuration enabling rollback and naming an activation revision. The activation revision is a full immutable revision identifier; a rollback directive must be a proper descendant of it. A directive at or before the activation revision MUST NOT execute and emits `W300`. This prevents an upgrade from executing previously inert `rollback` messages in old history.

The engine MUST reject a pending post-activation rollback when execution is disabled (`E300`). It MUST NOT silently turn it into an ordinary no-op release. An invocation that only previews MUST never call the mutating handler. Existing branch protection and release authorization remain applicable; a commit message does not grant credentials or authority to delete an artifact.

Conceptual configuration, not accepted by current dispat:

```json
{
  "rollback": {
    "enabled": true,
    "activationRevision": "0123456789abcdef0123456789abcdef01234567"
  },
  "spaces": {
    "services": {
      "rollback": {"command": "sh ./scripts/withdraw-artifact.sh"}
    }
  },
  "packages": {
    "api": {
      "rollback": {"command": "sh ./scripts/withdraw-api.sh"}
    }
  }
}
```

The revision above is illustrative, not a valid activation point in an arbitrary repository. Configuration resolves a handler from package first, then its containing space. There is no implicit root handler. An explicit package `rollback: null` disables inheritance. Shell text comes only from trusted configuration, never from a commit or protocol response. A handler runs from the fixed repository root; paths in its command are relative to that root. Package and space paths are supplied as data.

## 2. Directive and exact target

```text
rollback(api): withdraw the compromised artifact

Rollback-Version: 1.4.2
```

`rollback` is a control type with bump `none`. It MUST have an explicit scope-set containing one or more literal package names. File-derived scopes, `.`, wildcards, exclusions, and an empty scope-set are forbidden (`E301`). Spaces provide handler inheritance; their names do not implicitly expand a rollback scope. Use separate units when packages have different target versions.

Exactly one `Rollback-Version` footer is required. An optional `Rollback-Cancel` footer is defined below for cancelling an earlier, unstarted request; it is not a withdrawal. The `Rollback-Version` value is one complete SemVer string, including any prerelease or build metadata. `latest`, ranges, channels, and relative versions are invalid. `Rollback-Version` is valid only on a rollback unit. Duplicate or conflicting occurrences are errors, not newest-wins directives (`E301`).

A rollback unit MUST NOT carry `!`, `BREAKING CHANGE`, release/channel/propagation directives, `Release-As`, `Edits`, `Deletes`, or `Reverts` (`E301`). Authorship/review trailers and unknown non-directive footers retain their ordinary treatment. Rollback produces neither a version bump nor an ordinary release changelog entry; its reason belongs in the rollback report and receipt.

For every named package, the engine MUST resolve the requested version to one immutable release record reachable from the directive's revision, whose release revision is a proper ancestor of the directive. Absence, conflicting identities, an unknown package, or a record introduced only after the directive is `E302`. A target is the tuple:

```text
(repository identity, package, exact version, release-record identity,
 release revision, destination identity, artifact identity)
```

The plan MUST print the tuple, directive revision/unit index, withdrawal effect, handler identity, and affected local consumers. The target MUST NOT follow a moving alias or a new baseline on retry. If the current publish destination differs from the historical destination, the old destination is not silently replaced; preflight fails until trusted configuration explicitly addresses the recorded destination.

Rollback requests are discovered from the activated reachable history and discharged by rollback-completion records. They are not bounded by `W`, `Wfresh`, or the package's next release tag. Publishing another version, `cancel`, hold directives, and ordinary correction footers do not erase an already requested rollback. Only the explicit pre-intent cancellation below can discharge an unstarted request. A hold still controls ordinary version publication only.

### Cancelling an unstarted request

```text
rollback(api): cancel the mistaken withdrawal request

Rollback-Version: 1.4.2
Rollback-Cancel: 0123456789abcdef0123456789abcdef01234567#1
```

`Rollback-Cancel` names the full immutable revision and one-based unit index of an earlier rollback request. The
referenced revision must be a proper ancestor of this cancellation directive, and the target package/version must
match exactly. One cancellation unit targets one literal package and one earlier request. It contributes no destructive
operation. Unknown targets, wrong versions, cancellation-of-cancellation and duplicate footers are `E309`.

Cancellation is permitted only before any durable intent exists for the target request. Under the release lock, the
engine checks all relevant intent/attempt/completion records, appends a durable immutable `rollback/cancel` record
binding the original request and cancellation directive, and suppresses the original request thereafter. A failure to
persist cancellation is `E307`; a concurrent or existing intent makes cancellation `E309`. It never asserts that an
artifact was removed. After an intent exists, first reconcile whether removal occurred; cancellation cannot hide an
ambiguous external operation. A fresh, separately authorized rollback directive may request the same version later.

Read-only plans show cancellations. Execution still requires activation and authorization, but a cancellation-only
operation does not require a removal handler. All cancellation validations precede artifact mutation. Records are
written before executing the remaining withdrawals. A matching cancellation record makes reruns no-ops.

## 3. Preflight and consumer safety

Missing or disabled handlers are **fatal preflight errors** (`E303`). This includes missing inherited handlers and destinations that cannot remove the requested version. The error names every uncovered package. A warning-only default would report success without carrying out the requested withdrawal. This protocol provides no option that labels a skipped rollback as completed.

Before any artifact mutation or ordinary publication, the engine MUST:

1. Acquire the repository release lock under the VCS protocol and bind the plan to its immutable history/record snapshot.
2. Validate every directive, release identity, destination, handler, and receipt capability.
3. Identify available in-workspace consumer releases that rely on each target, using recorded release-time dependency identities. Merely inspecting today's graph is insufficient for historical versions.
4. Require every affected available local consumer version to be explicitly targeted too, or prove that its deployed/released configuration no longer relies on the withdrawn artifact. Missing evidence or an omitted consumer is `E304`. Never add destructive targets implicitly.
5. Inspect each target without mutation, establish its identity and the handler's supported removal effect, and verify that a durable intent record can be stored.
6. Reject overlap with ordinary publication of the same package in this run, or ordinary publications whose resolved inputs refer to a withdrawn target (`E304`).

An available local consumer release is a recorded package-version whose artifact remains available in its destination
and lacks a completed withdrawal record. This includes older versions, not only the latest tag or currently deployed
version. The inventory MUST enumerate all such local releases relevant to the target; it cannot omit an older consumer
merely because the current source no longer depends on that provider. Historical dependencies refer to exact released
identities, not package-name nodes. Current source edges are relevant only when proven to describe one of those exact
release identities. The operator may first release a supported migration or withdraw consumer versions in separate
runs, but a newer version alone does not remove an older available consumer's dependency.

A legacy release without sufficient recorded artifact/dependency identity requires a trusted, reviewable inventory supplied for this exact operation. It MUST be verified against the destination and included by digest in the intent record; lack of a trustworthy inventory is an error. Inference from an arbitrary tag name or a manifest version alone is not evidence of artifact identity.

No repository-local graph proves the absence of external consumers. The plan MUST disclose that limitation and the operator's declared impact policy. Deleting a registry version cannot revoke copies already downloaded. A destination that only supports deprecation or yanking MUST report `unsupported` for a requested removal; those different effects require separately specified operations and MUST NOT be represented as deletion.

The handler's read-only inspection can fail or time out; either fails preflight. If a target is already absent without a matching prior durable intent or completion record, the engine MUST fail with `E306`. Absence alone does not prove that this repository removed the intended artifact.

## 4. Ordering and execution

Build the rollback dependency graph on exact `(package, version, release-record identity)` nodes from the verified
available-release inventory. An edge links an exact consumer release to an exact provider release over
`publish.blockingKinds`. Include all relevant intermediate release identities, not only selected targets. Do not union
historical and current edges by package name: `A@1 -> B@1` and `B@2 -> A@2` do not form a cycle. A genuine cycle or an
unresolvable identity is `E304`. Compute dependency-first topological order, breaking ties by the UTF-8 byte tuple
`(package, version, release-record identity)`, reverse it, then filter to selected targets. Multiple versions of one
package are distinct nodes and require distinct explicitly versioned directive units. No destructive target is inferred
from a wildcard or automatically added by this ordering step.

Thus for `web -> api -> core`, withdrawal executes `web`, then `api`, then `core`, even if an intermediate package currently has no ordinary release pending. Handler lookup does not determine order.

All target preflight checks complete before the first destructive call. Before calling a handler, append and durably publish the exact target's intent record. After verified success, append and durably publish its completion record immediately. Never batch completion recording until the end of the run.

On handler failure, stop withdrawing its providers and their transitive providers; they may still be needed by that consumer. Stop scheduling all remaining withdrawals in this attempt; even independent branches remain pending. Any failed, blocked, unsupported, ambiguous, or unrecorded target makes the overall run exit nonzero. Ordinary publication MUST NOT start unless every requested rollback is complete. A run containing only completed rollback requests is a successful no-op.

Rollback MUST NOT be automatically invoked by ordinary publish/build failure. The forward-recovery rules in SPEC.md §19.3 remain the default. Explicit rollback is a separate user-authored operation, not a global transaction rollback.

## 5. Handler wire contract

The engine invokes the trusted command with a single UTF-8 JSON object on stdin. It MUST NOT interpolate package names, versions, paths, reasons, or record content into the shell command. The handler MUST read the request as data.

Protocol `ccme.rollback/1` supports two operations: `inspect` (read-only) and `remove` (mutating). Required request fields are:

| Field | Meaning |
| --- | --- |
| `protocol` | Exactly `ccme.rollback/1`. |
| `operation` | `inspect` or `remove`. |
| `requestId` | Stable identifier for the specific directive and exact target. |
| `repository` | Stable repository identity, not a credential-bearing URL. |
| `revision` | Full fixed plan revision identifier. |
| `directive` | Full directive revision and one-based unit index. |
| `package`, `version` | Literal package name and exact version. |
| `packagePath`, `space` | Repository-relative package path and containing space name, or `null` space. |
| `releaseRecord`, `releaseRevision` | Immutable release-record identity and the released revision. |
| `destination` | Configured historical destination identifier. |
| `artifact` | Verified artifact digest/identity and format, or `null` on the first inspection if an inventory must establish it. |
| `intent` | Previously persisted intent-record identity for `remove` or a retry; otherwise `null`. |

A request identifier is the lowercase SHA-256 hex digest of the UTF-8 RFC 8785 canonical JSON array
`["ccme.rollback/1", repository, directiveRevision, unitIndex, package, version, releaseRecord]`.
The unit index MUST be a positive integer no greater than 2^53−1. This defines identical bytes without depending
on object-key order and uses the same canonicalization contract as VCS records. It is an operation identity, not proof
of authorization.

Example response to a successful inspection (identifiers abbreviated for readability):

```json
{
  "protocol": "ccme.rollback/1",
  "requestId": "same-full-request-id-as-input",
  "result": "present",
  "artifact": {"algorithm": "sha256", "digest": "verified-full-digest"},
  "effect": "remove",
  "message": "Exact version and digest verified"
}
```

Every response MUST contain string fields `protocol`, `requestId`, and `result`, an `effect` string or `null`, an `artifact` object or
`null`, and an optional string `message`; other fields are ignored. `effect` is exactly `remove` for supported success
and `null` for unsupported/failed results. `result` is the outcome token listed here. Duplicate JSON keys, invalid UTF-8,
a BOM, a non-object value or trailing non-whitespace output are protocol errors. Responses MUST echo `protocol` and
`requestId`. `inspect` returns `present`, `absent`, or `unsupported`; `remove` returns `removed`, `already_absent`, or `unsupported`. Both can return `failed`. `present`, `removed`, and `already_absent` require an artifact object with `algorithm` and
`digest` strings identifying the verified content. An initial `absent` response carries `artifact: null`; only an
inspection bound to a prior durable intent may echo that intent's artifact identity while verifying its absence.
`unsupported` and `failed` use `artifact: null`, `effect: null`, and a diagnostic `message`. A response with either
outcome and exit zero is syntactically valid but still an operation failure. A successful removal response MUST carry
the exact verified artifact identity; `already_absent` MUST refer to the previously persisted intent's artifact identity and verify absence of that exact destination/version. A present artifact with a different identity MUST NOT be removed.

The handler MUST emit exactly one JSON response on stdout, with no progress text. Diagnostics go to stderr. Exit `0` means a valid successful operation response; nonzero, malformed/extra JSON, mismatched echoed protocol/request identifier, `unsupported`, or `failed` is failure regardless of accompanying text (`E305`). A mismatched target, destination or artifact identity is `E306`, not `E305`. An `unsupported` result discovered by inspection is the preflight error `E303`; `E305` applies if a previously supported remove operation becomes unsupported after preflight. There is no special shell exit code that implicitly means "already removed".

Request and response limits are 1 MiB each. The engine MUST bound captured stderr and redact credentials; overflowed protocol output is an error. The configured timeout MUST be positive and finite; default 300 seconds per operation. Cancellation or timeout MUST stop the handler and its owned child process tree before further mutation is scheduled. Mutation support requires a process-containment mechanism that lets the engine terminate and verify teardown of the
handler and its owned descendants. A platform/handler combination that can escape that containment is unsupported;
conformance does not claim detection of unrelated system processes. The engine MUST report an ambiguous result if it cannot establish whether a started `remove` completed. It MUST NOT infer rollback from a timeout or a killed process.

## 6. Durable intents, receipts, and retries

A rollback intent binds the request identifier, exact target tuple, directive, fixed plan revision, handler/configuration identity, verified inventory digest, supported effect, and the artifact identity established during preflight. Store it as an immutable record in namespace `rollback`, type `intent` in the repository ledger using create-if-absent semantics. Recreating an identical record is allowed; a different value at the same identity is `E307`.

The completion record has namespace `rollback` and type `complete`, references the intent, and includes the verified result `removed` or `already_absent`. It is written only after successful verification. For `intent` and `complete`, the record key is the stable request identifier. The record revision is the directive's
revision, so a branch containing the request can observe its disposition even if it does not contain a later execution
head. The execution head remains a payload field. Attempts use type `attempt` and key `<requestId>/<attemptId>`, where
`attemptId` is the SHA-256 digest of the canonical attempt payload; acknowledgements and cancellations use types `ack`
and `cancel` keyed by their own directive's request identifier and explicitly reference the target intent/request.
Records discovered under the lock are authoritative; a duplicate cancellation of an already cancelled request may
acknowledge the existing record but must not create a contradictory target disposition.

All record kinds are stored through the VCS protocol's durable immutable-record operations; they are not ordinary release tags and MUST NOT participate in baseline selection. An append-only local file that has not reached the configured durable remote is not a completed record.

On retry:

- A matching completion record suppresses the destructive call. A completion is a ledger fact, not continuous proof of absence. Before any plan relies on current absence, the engine MUST inspect for unexpected reappearance; reappearance is `E306`, never permission to remove a different artifact.
- A matching intent without completion permits read-only inspection of that same target. If still present with the same identity, repeat the idempotent remove request. If absent, verify the intent-bound destination/version and append completion. If identity differs or cannot be checked, fail `E306`.
- No intent and an absent target is not enough to invent a receipt. Report the ambiguous state for explicit reconciliation.
- A completed withdrawal requested again by a later directive MUST reuse the target's verified completion evidence, append an acknowledgement for the new request, and avoid another destructive operation.

A new handler implementation may be required to repair a failed attempt. It does not change the target or request identifier. The engine MUST disclose the changed handler/configuration identity and require authorization covering that change before retrying; it MUST preserve the original intent and record the new attempt as an immutable child record.

Lost responses and interruption between deletion and recording remain possible. The protocol guarantees durable recorded completion and identity-bound retries, not exactly-once external deletion. Failure to store a completion after deletion is `E307`, leaves the operation incomplete, and MUST be surfaced as "artifact removed; completion not durably recorded".

## 7. Baselines and mathematical boundary

Release tags and their historical payloads MUST NOT be moved or deleted. A completion receipt marks the target artifact as withdrawn in the ledger but preserves its published-version identity permanently. Its version remains part of baseline/high-water computation, so a later ordinary release must use a greater version. Rollback does not rewind manifests, revive old pending commits, re-admit discharged propagation, or make a version available for reuse.

Ordinary publication and adoption (§19.4) MUST reject any withdrawn package-version identity. Dependency reconciliation MUST NOT select a withdrawn artifact as an available provider. If the existing manifest/dependency resolution requires one, the run fails `E308` until an explicitly planned new version or supported alternative resolves it. Historical ledger authority and current artifact availability are separate facts. A receipt proves the completed operation, not perpetual absence in a destination controlled by other actors.

G1–G8 apply to the forward-publish projection under their existing hypotheses, with rollback activation, inventories, and completion state fixed. They do not assert availability of withdrawn artifacts or automatic restoration of consumers. For a fixed rollback target set, graph, configuration and external identities, deterministic ordering follows from the specified total order. Under successful-progress retries with idempotent handlers and durable records, each new completion removes one outstanding target, so at most `n` such progress runs complete `n` targets. Failed attempts have no finite bound. No convergence is promised if a registry forbids deletion, records cannot be persisted, or external actors keep changing identities.

## 8. Conformance vectors

These are protocol requirements for future implementations, not results of dispat tests.

| ID | Input or condition | Required outcome |
| --- | --- | --- |
| R1 | `rollback(api)` with exact version and verified record/handler | Separate rollback plan; no bump or ordinary changelog entry. |
| R2 | No explicit scope, wildcard scope, `!`, propagation, or missing version | `E301`; no handler mutation. |
| R3 | Directive at/before activation revision | `W300`; never execute it. |
| R4 | Post-activation rollback with execution disabled | `E300`; no artifact mutation. |
| R5 | Package has no handler, but its space has one | Resolve the space handler, running from repository root for the exact package target. |
| R6 | Neither package nor space supplies a handler, or package explicitly disables it | `E303` before all artifact mutations. |
| R7 | `web -> api -> core`, all affected versions explicitly requested | Remove `web`, `api`, `core`, in that order. |
| R8 | Same graph, only `core` requested and active consumers still require it | `E304`; do not implicitly withdraw consumers. |
| R9 | `api` withdrawal fails after `web` completes | Preserve web receipt; block core; report nonzero; retry never withdraws web again. |
| R10 | Remove succeeds remotely but response is lost | Preserve intent; retry inspects identical target, verifies absence, and records completion. |
| R11 | Target absent with no intent/receipt | `E306`; absence does not prove successful rollback. |
| R12 | Requested version exists with different digest | `E306`; do not invoke remove. |
| R13 | Completed rollback followed by ordinary release | Baseline never decreases, target version never reused; normal release must exceed high-water version. |
| R14 | Ordinary publication fails, no rollback directive | Existing forward recovery; do not invoke rollback handlers. |
| R15 | Changed current destination, missing historical dependency evidence, or omitted dependent | Preflight failure, no destructive call. |
| R16 | Invalid JSON, wrong echoed request identifier, excessive output, timeout or failed teardown of the defined owned process containment | Fail `E305`; preserve uncertainty and prevent further destructive work. |
| R17 | A later package tag advances past the rollback directive before rollback completes | The request remains pending; ordinary release tags cannot discharge it. |
| R18 | Explicit new directive repeats an already completed identical target | Record acknowledgement backed by the existing completion; no repeated remove. |
| R19 | A would-be forward release resolves a withdrawn provider version | `E308`; do not publish a consumer with an unavailable input. |
| R20 | Completion record push fails after verified removal | `E307`; report removed-but-unrecorded, retain intent, retry with verification. |
| R21 | Cancellation references an unstarted exact request | Persist cancellation under the lock; original request never calls remove. |
| R22 | Cancellation references a request with a durable intent or completion | `E309`; reconcile the external outcome, never hide it as cancelled. |
| R23 | Historical `A@1 -> B@1` and current `B@2 -> A@2` | Distinct release nodes; do not invent a cycle by collapsing package names. |
| R24 | A completed target reappears externally | Receipt remains historical; a plan relying on absence inspects and fails `E306`. |
