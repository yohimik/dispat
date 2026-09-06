# CCME 3.0.0 VCS adapter protocol

**Status:** Proposed normative extension for CCME 3.0.0. **Implementation status:** Dispat 1.8 implements only its
built-in Git backend. It does not read the configuration in this document and MUST NOT claim external-adapter
conformance.

This protocol makes the version-control operations used by CCME explicit without changing the commit-message grammar
or the release algorithm. Git remains the default. An external adapter is a repository-trusted local command which
projects another version-control system onto the same immutable revision DAG and release-record model.

The key words **MUST**, **MUST NOT**, **REQUIRED**, **SHOULD**, **SHOULD NOT**, and **MAY** have the meanings in [RFC 2119](https://www.rfc-editor.org/rfc/rfc2119).

## 1. Configuration and invocation

The CCME 3.0.0 configuration surface is:

```yaml
vcs:
  driver: external
  command: ["/opt/acme/bin/ccme-vcs-fossil", "--repository", ".fslckout"]
```

An adapter MAY instead configure one command per operation:

```yaml
vcs:
  driver: external
  commands:
    capabilities: ["/opt/acme/bin/ccme-vcs-fossil", "capabilities"]
    snapshot: ["/opt/acme/bin/ccme-vcs-fossil", "snapshot"]
    revisions: ["/opt/acme/bin/ccme-vcs-fossil", "revisions"]
    isAncestor: ["/opt/acme/bin/ccme-vcs-fossil", "is-ancestor"]
    changedPaths: ["/opt/acme/bin/ccme-vcs-fossil", "changed-paths"]
    records: ["/opt/acme/bin/ccme-vcs-fossil", "records"]
    acquireLock: ["/opt/acme/bin/ccme-vcs-fossil", "acquire-lock"]
    createRecord: ["/opt/acme/bin/ccme-vcs-fossil", "create-record"]
    releaseLock: ["/opt/acme/bin/ccme-vcs-fossil", "release-lock"]
```

Omitting `vcs`, or setting `vcs.driver: git`, selects the built-in Git backend and forbids `command` and `commands`.
For `driver: external`, exactly one of `command` or `commands` is REQUIRED. `command` is either a non-empty array of
non-empty strings or an object:

```yaml
vcs:
  driver: external
  command:
    shell: "/opt/acme/bin/ccme-vcs-fossil --repository .fslckout"
```

Every value in `commands` has the same forms. A configured operation runs its own value; a single `command` is a
multiplexer and learns the operation from the request. The array form invokes the first element directly with the
remaining elements as its argument vector and is RECOMMENDED. The object form invokes the configured command using the
platform's normal command shell. An implementation MUST NOT
interpolate request fields, revision identifiers, paths, credentials, or environment values into either form. It MUST
NOT download, discover, or execute an adapter named by repository history, a manifest, or an adapter response.
Configuration is trusted executable code and MUST be reviewed under the same policy as release scripts.

The engine starts a new adapter process for each request. Its working directory MUST be the canonical workspace root.
It supplies `CCME_VCS_PROTOCOL=3.0.0`, `CCME_WORKSPACE` (the same canonical root), and a minimal implementation-defined
environment. Locale and time zone MUST NOT affect results. The engine MUST NOT place credentials in request JSON or add
them to the environment merely for this protocol; credentials needed by the VCS are supplied by the operator's normal
credential mechanism. Diagnostics and logs MUST redact credential-bearing environment values and command arguments.

The engine writes exactly one UTF-8 JSON object followed by LF to standard input, closes standard input, and reads
exactly one UTF-8 JSON object from standard output. A UTF-8 BOM, invalid UTF-8, duplicate object keys, trailing
non-whitespace output, non-finite numbers, or non-object top level is a protocol error. The adapter MAY write human
diagnostics to standard error; the engine MUST bound and redact captured stderr before displaying it.

Every request has `protocol`, `id`, `operation`, and `params`. Every response has the matching `protocol` and `id`,
exactly one of `result` or `error`, and no operation-dependent fields outside that member. Unknown fields MUST be
ignored. Names and string values are case-sensitive.

```json
{"protocol":"3.0.0","id":"0001","operation":"snapshot","params":{}}
```

```json
{"protocol":"3.0.0","id":"0001","result":{"snapshot":"s:7f8c","head":"r:91","shallow":false}}
```

The stable default limits are 30 seconds per read operation, 30 seconds per write operation, 256 MiB stdout, 1 MiB
stderr, JSON nesting depth 64, 1,000,000 revisions, 5,000,000 paths, 1,000,000 records, and 16 MiB per message. A
configuration MAY lower or raise them but MUST keep every limit finite. On timeout or cancellation the engine MUST
terminate the process and its descendants after at most a five-second graceful period. An implementation that cannot
reliably terminate the owned process tree MUST refuse mutating external adapters. A killed, signalled, timed-out,
oversized, or non-zero process is a failed operation even if stdout contains JSON. Exit zero with an `error` response
is also a failed operation. Exit zero with one valid `result` response is success.

An error has the following shape:

```json
{"protocol":"3.0.0","id":"0001","error":{"code":"snapshot-changed","message":"repository changed","retryable":true}}
```

`code` is a stable lower-case ASCII token, `message` is safe human text, and `retryable` is a boolean. Adapters MUST NOT
put secrets in errors. Protocol errors and unsupported required capabilities are run-fatal failures; the engine
MUST NOT continue with partial or invented data.

## 2. Capabilities and fixed snapshots

The first request MUST be `capabilities`:

```json
{"protocol":"3.0.0","id":"c1","operation":"capabilities","params":{}}
```

```json
{"protocol":"3.0.0","id":"c1","result":{"protocolVersion":"3.0.0","operations":["snapshot","revisions","isAncestor","changedPaths","records","acquireLock","createRecord","releaseLock"],"revisionOperands":"exact-token","atomicConditionalLock":true,"immutableRecords":true,"durableWrites":true,"stableOrdering":true}}
```

`protocolVersion` MUST be exactly `3.0.0`; negotiation by guessing or silently downgrading is forbidden. The engine MUST
validate the complete capability set needed by the requested CCME action before reading history or writing state. An
adapter MUST report only guarantees it implements. The engine MUST refuse an adapter which lacks a required operation
or guarantee. In particular it MUST NOT emulate compare-and-set locking with a read followed by an unconditional write.

`snapshot` returns an opaque `snapshot` token, opaque revision identifier `head`, and `shallow`. The snapshot MUST bind
the local revision graph and path state together with the authoritative remote record and lock state observed by the
adapter. All subsequent read and write requests carry that exact snapshot token. All reads MUST describe that one fixed
state at that head. If the adapter cannot honor the token because relevant state changed, it MUST return
`snapshot-changed`; it MUST NOT silently refresh. The engine then discards every derived value and restarts from
`snapshot`. A `shallow: true` snapshot is rejected as CCME `E196`.

Revision identifiers are opaque non-empty UTF-8 strings without ASCII whitespace or control characters. A literal `#`
is reserved for CCME's unit selector and MUST be JSON-quoted in a CCME 3.0 revision operand; the engine removes those
JSON quotes and escapes before lookup. Engines MUST otherwise compare identifiers only for byte equality, pass them
back unchanged, and never infer ancestry, age, or type from their spelling. `revisionOperands` is either `exact-token`
or `git-sha-abbreviation`. Under `exact-token`, `Edits`, `Deletes`, and `Reverts` operands MUST contain the adapter's
full canonical identifier as a bare non-whitespace token, or as a JSON string when quoting is needed; prefixes are never
accepted. For a bare operand, `#` is excluded from the revision token and introduces an optional one-based unit selector
without leading zeros. For a quoted operand, parse exactly one JSON string first, then the optional `#<n>` selector;
an escaped or literal `#` inside that string belongs to the revision identifier. A bare identifier beginning with `"`
must be quoted as a JSON string. `*` retains its wildcard correction meaning and a literal identifier `*` must therefore
be quoted. There is no optional whitespace inside an operand. `Reverts` does not accept a unit selector. Invalid or
ambiguous encoding is a directive error, never a fallback to prefix matching. The built-in Git backend reports `git-sha-abbreviation` and preserves CCME 2.0's SHA-shaped operands, unique
abbreviation, and ambiguity errors. An engine MUST NOT apply Git abbreviation rules to another backend.

## 3. Read operations

Every successful list is complete and ordered as stated. Pagination is deliberately absent: an engine chooses bounds
large enough for a run or fails without computing a partial plan.

### `revisions`

Request:

```json
{"protocol":"3.0.0","id":"r1","operation":"revisions","params":{"snapshot":"s:7f8c"}}
```

Result:

```json
{"protocol":"3.0.0","id":"r1","result":{"revisions":[{"id":"r:90","parents":[],"messageBase64":"ZmVhdChjb3JlKTogZmlyc3QK"},{"id":"r:91","parents":["r:90"],"messageBase64":"Zml4KGNvcmUpOiBndWFyZCBFT0YK"}]}}
```

The result MUST contain every revision reachable from `head`, each exactly once, in deterministic parent-before-child
topological order with identifier bytes as the tie-break between eligible revisions. `parents` MUST contain every direct
parent in the VCS's native order; the first entry is the first parent used by merge diff and scope rules and MUST NOT be
sorted. `messageBase64` is RFC 4648 base64 with required padding and encodes the raw, unmodified commit-message bytes.
The engine decodes it, reports SPEC.md `E001` for invalid UTF-8, and performs §4.1 normalization exactly once. The graph MUST be
acyclic, all parents MUST appear in the result, and the final graph MUST make `head` reachable. Commit dates and author
metadata are neither requested nor used.

### `isAncestor`

`params` contains `snapshot`, `ancestor`, and `descendant`; `result` is `{ "value": true }` or `{ "value": false }`.
The relation is ancestor-or-self. Both identifiers MUST belong to the snapshot. Results MUST agree with the parent graph
returned by `revisions`; disagreement is a protocol error.

### `changedPaths`

`params` contains `snapshot` and `revision`. The result is `{"paths":[...]}` with normalized repository-relative paths,
sorted by UTF-8 bytes and without duplicates. Paths use `/`, contain no empty, `.` or `..` segment, have no leading `/`,
NUL or backslash, and identify both old and new names for a rename. For a root revision, compare with an empty tree; for
a merge, compare with the first parent. This exactly supplies SPEC.md §6.2 and does not grant filesystem access outside
the workspace.

### `records`

Request:

```json
{"protocol":"3.0.0","id":"t1","operation":"records","params":{"snapshot":"s:7f8c","namespace":"release"}}
```

Result:

```json
{"protocol":"3.0.0","id":"t1","result":{"records":[{"namespace":"release","type":"version","key":"@acme/core@1.4.2","revision":"r:90","contentId":"sha256:e5e41029bf650b63c04709e5568d2009f727450f57438a9204f17e76ff66ea68","payload":{"package":"@acme/core","version":"1.4.2"},"recordId":"tag:@acme/core@1.4.2"}]}}
```

`namespace` is a required exact filter. Records MUST include every record in that namespace reachable from `head` and
be sorted by `type`, `key`, `revision`, then `recordId`, by UTF-8 bytes. `namespace` and `type` are non-empty printable ASCII strings. `key` is a non-empty UTF-8 string without control characters; a backend encodes it reversibly when its ref-name syntax is narrower. `revision` belongs to the snapshot and `recordId` is opaque and stable. `payload` is arbitrary
JSON. `contentId` is `sha256:` followed by the lower-case SHA-256 hex digest of the payload's [RFC 8785 JSON Canonicalization Scheme](https://www.rfc-editor.org/rfc/rfc8785) bytes; the adapter MUST verify it on reads and writes.

The `release` namespace and `version` type are baseline records. Their payload contains exactly `package` and
`version`; package is byte-identical to a workspace package and version is SemVer 2.0.0. Unknown packages and invalid
versions are handled exactly as tags are in SPEC.md §12. Duplicate package/version pairs on different revisions remain
`E191`. Reachability, baselines, pending windows, corrections, cancellation, propagation, and convergence retain
SPEC.md §§12–13 semantics.

Other namespaces and types are never baselines. CCME 3 rollback uses `rollback` namespace records whose type is one of
`intent`, `attempt`, `ack`, `cancel`, or `complete`; they are audit receipts anchored to a revision and MUST NOT participate in
baseline selection, pending windows, or tag-format diagnostics. Their payload is defined by the rollback specification.

## 4. Write operations

Writes require a lock obtained by `acquireLock`. The adapter MUST provide an atomic conditional create against the
snapshot and expected ownership; filesystem polling or read-then-write simulation is non-conforming.

```json
{"protocol":"3.0.0","id":"l1","operation":"acquireLock","params":{"snapshot":"s:7f8c","resource":"release","owner":"run:4bb2","expectedAbsent":true}}
```

A successful result is `{"lock":"opaque-lock-token","owner":"run:4bb2","snapshot":"s:7f8c-lock"}`. `expectedAbsent`
MUST be true and acquisition MUST be one atomic create against the supplied snapshot. A caller may not replace or steal
a live lock merely because it knows its owner. Locks MUST NOT expire automatically: expiry could admit a second owner
while the first is still publishing externally. Recovery from an abandoned lock is an explicit, authenticated,
out-of-band operator action. Lock tokens are credentials and MUST be redacted. Lock creation may be excluded from the
immutable read view, but the returned snapshot MUST in either case be valid for subsequent reads and writes by this
lock owner. Otherwise the adapter returns `snapshot-changed` or `lock-conflict`.

`createRecord` has `snapshot`, `lock`, `namespace`, `type`, `key`, `revision`, `contentId`, `payload`, and
`expectedAbsent: true`. `expectedAbsent` MUST be true. The adapter atomically creates one immutable record only when the
lock is live and owned, the snapshot still matches, and no record with that namespace/type/key exists. It returns the
stored record. Existing byte-identical canonical content anchored at the same revision MUST return success with
`existing: true`; different content or revision MUST return `record-conflict`. This identity rule makes retries after
an uncertain transport outcome safe. There is no update, move, overwrite, or delete operation. The engine writes a
`release/version` record only at the tag-after-publish point required by SPEC.md §19.1.

Success means the record and its referenced payload are durably acknowledged by the adapter's authoritative remote
store, not merely present in a local ref, cache, or outgoing queue. `durableWrites: true` asserts this property. A local
success followed by remote rejection is an operation failure. If acknowledgement is lost after the remote write, the
engine resolves the outcome by reading immutable records and retrying the identical identity; it never overwrites or
guesses. Partial release recovery remains the tag/record-driven procedure in SPEC.md §19.

The successful result includes the stored record and a new `snapshot` token reflecting that write. The engine MUST use
the returned token for its next write. A retry made with the preceding snapshot MAY return the already-created
byte-identical record and its resulting snapshot; it MUST NOT admit any unrelated intervening mutation. This forms a
conditional mutation chain for multi-package releases without weakening fixed-snapshot reads.

```json
{"protocol":"3.0.0","id":"w1","operation":"createRecord","params":{"snapshot":"s:7f8c-lock","lock":"opaque-lock-token","namespace":"release","type":"version","key":"@acme/core@1.4.3","revision":"r:91","contentId":"sha256:489bd2156872222b1cb823a3f0c8f8fbf8e8c56a27e6d84ce395e033827411a3","payload":{"package":"@acme/core","version":"1.4.3"},"expectedAbsent":true}}
```

`releaseLock` contains `lock` and `owner`. It MUST conditionally release only that exact live lock token owned by that
owner. Releasing an already released lock MAY succeed idempotently, but MUST NOT release a different or successor lock.
Failure to release a lock is reported after the release result; it never permits mutation without a lock.

## 5. Git default mapping

The built-in Git driver is the reference mapping:

| Protocol concept | Git meaning |
|---|---|
| snapshot/head | resolved `HEAD` commit plus the relevant ref-state identity |
| revision/parents/message | peeled commit object ID, native commit-parent order, raw commit message bytes (without log-output transcoding) |
| ancestry | Git ancestor-or-self reachability |
| changed paths | root-vs-empty or first-parent diff, with both rename paths |
| `release/version` record | reachable tag named `<package>@<version>`, peeled to a commit |
| other immutable record | an implementation-owned append-only Git ref/object representation that cannot collide with release tags |
| conditional lock | implementation-owned atomic release lock guarding snapshot validation and tag creation |

The mapping MUST preserve all behavior in SPEC.md §§12, 13, 17, 18, and 19. External adapters MUST provide the same
observable release plan and publication safety for a snapshot mapping that preserves revision identity, parent order and the specified total ordering. They MUST NOT linearize a DAG, omit parents,
use wall-clock order, return partial history, make release records mutable, weaken dependency-first publication, move a
record after publication, or claim publication/locking guarantees supplied only by some other process.

This protocol replaces only the Git-shaped inputs and immutable ledger writes required by CCME's release computation,
rollback receipts, and tag-after-publish rule. Package discovery and manifest reads use the workspace at the fixed head;
the engine MUST verify that it represents the adapter snapshot before planning. Editing manifests and changelogs,
running package scripts, forming a release commit, pushing source revisions, and registry publication are release-engine
operations outside this adapter protocol. Consequently implementing this document alone is not a complete replacement
for every Git command used by Dispat. A product offering a non-Git end-to-end release MUST separately define those
workspace and publication operations and MUST preserve the snapshot, dependency ordering, and partial-failure rules in
SPEC.md §19. Dispat 1.8 offers no such external path.

## 6. Conformance vectors

1. An adapter returns revisions `A`, `B(parent A)`, `M(parents A,B)` in a stable topological order, and all read calls
   under snapshot `S` agree. **Accept.** Each revision contributes once even though it is reachable by multiple paths.
2. The same request under `S` returns a new head or changed release record. **Reject** with `snapshot-changed`; discard
   the plan and restart.
3. `revisions` returns a child before its parent, omits a merge parent, duplicates a revision, or changes order between
   identical calls. **Protocol error; no plan.**
   Sorting a merge's native parent list is also a protocol error because it changes the first-parent diff.
4. `changedPaths` for a rename returns only the destination, or returns `../outside`. **Protocol error; no derived
   scope resolution.**
5. `records` returns `core@1.2.0` twice on distinct revisions. **CCME `E191`.** It MUST NOT choose by date or
   list order.
6. Capabilities omit `atomicConditionalLock`, and the engine could approximate it with `records` followed by
   `createRecord`. **Refuse before writes.** No simulated compare-and-set is permitted.
7. Two owners acquire `release` with `expectedAbsent: true`. Exactly one succeeds; the other receives `lock-conflict`.
8. Retrying an identical `createRecord` after an uncertain transport result returns the same record with
   `existing: true`; retrying different content returns `record-conflict`.
9. An adapter uses opaque IDs `r:90` and reports `revisionOperands: "exact-token"`. `Edits: r:9` does not resolve to `r:90`.
   **Fail the directive.** Prefix matching is specific to the Git backend.
10. The command exits zero with valid JSON plus a debug line on stdout, exits non-zero with a success object, exceeds a
    configured bound, or is cancelled. **Operation failure in every case; no partial result is consumed.**

## 7. Diagnostics

These codes extend the CCME 3.0 diagnostic registry. They are specification identifiers; Dispat 1.8 does not emit them.

| Code | Condition |
|---|---|
| `E320` | Invalid VCS configuration or command invocation failure. |
| `E321` | Protocol/version mismatch, malformed JSON, mismatched response ID, or output after the response. |
| `E322` | Required operation or claimed guarantee is absent; includes any attempted simulated conditional write. |
| `E323` | Adapter timeout, cancellation, signal, non-zero exit, or configured resource bound exceeded. |
| `E324` | Snapshot changed or responses disagree within one snapshot; the run cannot be restarted safely. |
| `E325` | Invalid, incomplete, cyclic, inconsistently ordered, or non-deterministic revision graph. |
| `E326` | Invalid path, record, content identity, revision operand, or ancestry result. |
| `E327` | Conditional lock conflict, ownership mismatch, or loss before a required write. |
| `E328` | Immutable record conflict or attempted mutation/deletion. |
| `E329` | Adapter error not represented by a more specific CCME diagnostic. |
| `W320` | Adapter stderr was truncated to its configured bound. |
| `W321` | Release completed but lock release failed; explicit operator recovery may be required. |

`snapshot-changed` detected before publication SHOULD cause a bounded restart from a new snapshot. Exhausting that
bound is `E324`. After any external publish side effect, it MUST be treated as `E324` and resumed from immutable records
under SPEC.md §19; the engine MUST NOT reuse the stale plan. All `E32x` diagnostics are run-scoped and prevent
further writes in that attempt.
