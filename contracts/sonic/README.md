# Dispat Solidity package (`sol`)

`sol` ports Dispat's normalized forward-release computation to Solidity for
[Sonic's EVM](https://docs.soniclabs.com/sonic/build-on-sonic). It also represents repository history as an
append-only local blockchain ledger and coordinates work across execution nodes. The existing Go CLI remains available.

The implementation follows the planning rules in
[CCME at `7bee84063815ad0e0018cfb9fccb5214ab3b1612`](https://github.com/yohimik/dispat/blob/7bee84063815ad0e0018cfb9fccb5214ab3b1612/specs/ccme-spec/SPEC.md).
That snapshot declares version `3.1.0-rc.12`. This is a bounded Solidity implementation of the normalized planning
and coordination rules, not a claim of complete CCME conformance. The boundaries below are part of its API.

## Build and test

The root Dispat configuration registers this directory as the independently versioned `sol` package. Its private
npm package, `@dispat/sol`, supplies the local toolchain and is separate from the root pnpm workspace.

```sh
cd contracts/sonic
npm ci
npm run build
npm test
npm run test:gas
npm run demo
```

The lockfile pins the [Foundry Forge](https://github.com/foundry-rs/foundry) executable to 1.7.1. No global Foundry
installation or `forge-std` checkout is required. The first build downloads the pinned native Solidity 0.8.28
compiler through Foundry's compiler manager. Later builds can use the local compiler cache.

Compilation enables the optimizer and IR pipeline and targets the Paris EVM. Tests execute locally in Forge and
require no RPC endpoint, wallet, private key, faucet funds, or deployment. `npm run format:check` checks Solidity
formatting; `npm run format` applies it. `npm run forge -- <arguments>` forwards arbitrary Forge arguments. The local
launcher executes the pinned platform binary directly so compiler and test failures return a nonzero exit status;
Foundry 1.7.1's npm wrapper does not preserve that status.

Use Node.js 20 or newer. An existing compatible Forge installation can also run `forge build` and `forge test`
directly from this directory with the same `foundry.toml` settings.

From the repository root, with a CLI built from this checkout:

```sh
dispat run tests --package sol --since all
dispat run build --package sol --since all
dispat run vet --package sol --since all
dispat run test:checks --package sol --since all
```

`test:checks` connects this package to the existing RC checks lane and runs formatting, the full Solidity suite, and
the local ledger demo. Package scripts install the pinned dependencies with lifecycle scripts disabled. The local Forge launcher preserves
the native executable's exit status, including failed tests. Package releases use `contracts/sonic/v{version}` tags,
update the package manifest and npm lockfile, and record source through the existing Dispat release flow. They do not
deploy contracts or publish an npm package. No root pnpm lockfile or CI workflow is required by this package.

## Components

| Contract or library | Responsibility |
| --- | --- |
| `DispatPlanner` | Pure release computation from a normalized snapshot, or a prepared history projection. |
| `DispatLedger` | Authorized, append-only commit DAG, normalized units, immutable release records, and hash-linked ledger events. |
| `DispatFederation` | Selects single, specified, or discovered repository histories and projects cross-repository boundaries. |
| `DispatExecution` | Pins a plan and task graph, accepts node claims and results, and retains uncertain work for reconciliation. |
| `DispatTaskGraph` | Validates command → publish → record prerequisites and provider ordering across the full package graph. |
| `CCMEHistory` | Ancestry, baseline selection, fresh/train windows, source delivery, corrections, and cancellation. |
| `CCMEChannels`, `CCMEVersion`, `CCMEGraph` | Channel precedence, version arithmetic, bounded propagation, and full-graph ordering. |

## A local blockchain as history

Deploy `DispatPlanner`, then `DispatLedger(planner, packages, edges)` on a local EVM. `npm run demo` executes this
lifecycle in Forge's local simulation; it uses neither an RPC endpoint nor transaction broadcasting.

The ledger's owner appends an entire commit with `appendCommit(id, parents, units)`. Commits and units receive stable
append-order indices. Parent bits identify earlier commits; branches and merges remain a DAG. The most recently
appended commit is the selected HEAD. `projectSnapshot()` projects only its reachable ancestry, remaps indices, and returns
the newest-first unit stream the planner consumes. `projectSnapshotAt(commitIndex)` selects another HEAD using the current
release inventory; it does not rewind Ethereum state to an earlier block.

Units supplied to `appendCommit` are in ascending, one-based order within the commit. Their `commitIndex` is the new
commit's index. `correctionTargets` uses permanent ledger unit indices; the ledger remaps them when producing a
snapshot. A commit's content cannot be amended by appending units later.

`appendRelease(packageIndex, commitIndex, version)` records an externally established publication. An identical receipt
is idempotent. The same package/version cannot point at another commit. No method deletes commits or release records.
Every mutation extends a hash chain containing the chain ID, ledger address, sequence, prior hash, record kind, and
payload hash. Ledger events expose those commitments. The owner supplies the facts; these hashes do not prove that a
Git server or external registry contains them.

`compute()` reads the ledger and returns a plan. Planning is read-only. Release receipts must be appended only after
the corresponding publication is known to have succeeded. A provider and consumer recorded on the same source commit
still require the external ordering/reconciliation discipline of CCME §19.3; a Git-style tag alone cannot establish
which external upload happened first.

## Normalized planner input

`CCMETypes.Snapshot` contains packages, commits, tags, resolved units, and dependency edges. Call `DispatPlanner.compute`
directly for one complete reachable history. `computePrepared` accepts the history facts produced by a multi-history
adapter; those facts are trusted numerical inputs rather than independently authenticated proofs.

The input contract is explicit:

- Package names are unique and sorted bytewise. Scope and provenance masks use this order.
- Commit IDs are nonzero, unique opaque `bytes32` values. Parents refer to lower indices, commits follow the
  normalizer's canonical topological order, and the final commit is HEAD. Every supplied commit must reach that HEAD.
  Commit dates do not determine ancestry or directive precedence.
- Units are ordered by descending commit index, then descending one-based unit index. Scope selection, type-to-bump
  mapping, footer parsing, and `inherit` on the bump axis have already been resolved. Direct scopes remain separate
  from bump and channel propagation scopes; zero target masks exclude every target. Depth zero disables an axis;
  `65535` traverses every reachable level within the package bound.
- Version fields use `uint64`. Stable has channel zero and counter zero. A prerelease channel is a left-aligned,
  zero-padded ASCII `bytes32`, with a numeric counter. Tags with another valid SemVer prerelease shape cannot be
  represented and must be rejected by the normalizer. Build metadata is stripped before normalization.
- Supply each package's initial stable version explicitly, normally `0.1.0`, and explicitly enable the default
  `preserveMajorZero` policy. Solidity's zero values do not insert configuration defaults.
- Corrections identify resolved unit indices and preserve whether scope was inherited or wildcard targets were used.
  Corrections apply before cancellation; published source work continues to support consumer catch-up.
- `propagates` and `ordersPublish` select the two edge sets. The union must be acyclic. Scope filters do not prune
  traversal; propagation does not restart at an intermediate consumer.

The kernel separates fresh admission from the train aggregate, derives delivery from provider records actually reached
by consumer release boundaries, resolves channels before bumps, reports held would-be versions, and computes publication
order over the whole graph before removing unchanged packages. Returned diagnostics use numeric CCME warning codes;
errors use `CCMEError` or an input/graph-specific custom error. Package-level diagnostics use unit index `65535`.

## History and execution modes

History selection and execution are independent axes.

| History mode | Selection |
| --- | --- |
| Single | Only the entry repository. |
| Specified | An explicit repository set, including the entry. |
| Discovered | The connected fleet reached from the entry through reciprocal peer links. |

The federation adapter consumes normalized repository inventories and explicit cross-repository boundary evidence.
Repository identities qualify history; identical commit IDs in different repositories are not the same revision.
Missing relevant boundaries and unresolved conflicting directives fail instead of being ordered by timestamps.
This is a blockchain adaptation of history selection, not a Git checkout/discovery client.

`DispatFederation.Input` carries each repository's identity, native snapshot, reciprocal peer mask, and inventory-wide
bump/channel target masks, plus cross-repository edges. `Boundary` rows bind a consumer's exact current baseline or
stable version to a qualified source commit. `Observation` rows identify the foreign history visible at a consumer's
cancellation commit. `MISSING_COMMIT_INDEX` explicitly means that no foreign revision was incorporated. These supplied facts are
trusted; a missing relevant row fails closed. Native direct scopes and correction targets stay repository-local.
Global directives from a control repository are not implemented.

Native wildcard corrections discard pending incoming bump and channel contributions per target without deleting the
provider's record or a published prerelease train contribution. A live wildcard whose scope also receives pending
foreign contributions fails with `InvalidInput("correction.foreignWildcard", unitIndex)`: this adapter does not
establish the cross-repository causal reach needed to apply that correction. Delivered foreign work, voided
corrections, and Single mode do not trigger this restriction. Cancellation observations do not supply a general
foreign-correction proof.

`compute` returns the selected repository mask and maps global package/unit indices back to their repository-local
identities. `prepare` returns the same merged snapshot and history facts for `DispatExecution.startPrepared`. The
tests exercise all nine combinations using these actual projections and complete command, publication, and recording
settlement. Discovered mode currently requires the selected reciprocal component to be a tree.

| Execution mode | Task acquisition |
| --- | --- |
| Compilation | All descriptors are stored up front; nodes claim a compiled ready task. |
| Interpretation | Nodes derive descriptors from the pinned input and submit them; the contract checks the committed descriptor and readiness. |
| Hybrid | Command descriptors are compiled; first-only publication and recording descriptors are supplied after their prerequisites settle. |

`DispatExecution.start` recomputes and pins the semantic plan. `startPrepared` accepts the same prepared history used
for federated computation and includes those facts in the input hash. All modes commit to the complete task graph and
produce the same semantic plan hash for identical inputs. They are distinct claim paths, not aliases for compilation.

Each planned package has one or more command tasks, exactly one publication task, and exactly one record task.
Commands precede publication; recording directly follows publication. The validator preserves provider command and
record prerequisites through unchanged intermediate packages. This implementation uses build readiness `build` and
requires a no-op publication descriptor for a package without an external artifact. Other build-readiness policies
and arbitrary CCME flow-stage semantics are not implemented by this validator.

Nodes register their command capacity and must be admitted to a run. Capacity applies across runs. Only the deploying
orchestrator can start runs, accept results, publish, record, retry failed commands, cancel, or reconcile uncertain
outcomes. Repository lock identities must be the canonical actual participant set supplied by that orchestrator.
Within this coordinator, a repository belongs to at most one unfinished run. Independent coordinator contracts do
not share these locks.

An executor proposes its output digest; the orchestrator verifies it off-chain before accepting it. Attempt numbers
fence stale results. A failed command can be retried up to the configured bound. Unknown results retain the repository
lock and command capacity until the executor has stopped and the orchestrator records explicit reconciliation.
Publication is never automatically retried. Cancellation stops new work but permits recording a known successful
publication. `finish` refuses to release locks while a known successful publication is unrecorded.

## Supported boundary and limits

The Solidity code does not parse commit messages, authenticate external Git history, run shells, transfer build
artifacts, rewrite manifests, or contact registries. It supplies on-chain computation and coordination for an
off-chain normalizer and executor. It does not implement the CCME external VCS adapter, explicit rollback protocol,
shared-version groups, range-aware suppression, configurable governance limits, or every textual diagnostic.
The exact-version major-jump limit is fixed at the specification default of one. The owner/orchestrator is a real
authority boundary; this is not a permissionless publication or consensus protocol.

A planner snapshot is bounded to 64 packages, 256 commits, 256 units, 512 tags, and 512 edges. An execution graph has
at most 256 tasks and 64 admitted nodes, with 1–64 command attempts. Federation accepts at most 16 repository
identities and 64 inventory packages; its selected totals must fit the planner bounds. It accepts at most 2,048
baseline/stable boundary rows and 4,096 cancellation observations. These are rejection limits, not throughput or
gas guarantees. Worst-case inputs can exceed a node's call/transaction gas budget. Do not truncate old history to fit:
that can erase release debt. Archival checkpoints would need a separately specified proof-preserving format.

Tests execute locally, including fuzzed history immutability and version properties. Public Sonic deployment,
network-load behavior, external executor integration, and a complete Appendix B conformance campaign are separate
work. Sonic's documented network settings are [mainnet chain 146 and testnet chain 14601](https://docs.soniclabs.com/sonic/build-on-sonic/getting-started);
this package never chooses a network or reads a private key automatically.

## License

This package's source is GPL-3.0-or-later, following the CCME specification-derived source policy in the repository's
root `LICENSE`. The full GPL version 3 text is included in [LICENSE](LICENSE).
