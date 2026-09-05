# CCME separation review

Reviewed on 5 September 2026. Implementation: `1c96acda`; documentation: `4a6a789a`.

The specification now has a standalone release package, an initial baseline of 1.0.0, and tags of the form `specs/ccme-spec/v{version}`. Dispat's real planner selects a major release to 2.0.0. The message grammar is unchanged. The GPL license text is byte-for-byte identical to the previous `pkg/ccme/LICENSE-SPEC`; the Go parser retains its MIT license.

## Findings and disposition

| Finding | Resolution |
| --- | --- |
| Prerelease history could appear to admit delivered work again in the written algorithm | Separate fresh admission from train-wide version calculation. |
| Unconditional stability, narrowing, and convergence claims had counterexamples | State the retry invariant, surface changed inputs, and limit the convergence proof to that domain. |
| The upward stale-source algorithm omitted channel eligibility | Apply the same eligibility predicate as downward propagation. |
| Axis independence was stated symmetrically | State the one-way guarantee: a rejected bump does not suppress an admitted channel. |
| Inherited source channels can change between retries | Treat changed baseline channels as changed planning inputs. |
| Bucketed traversal could admit a unit's own source | Require per-unit source exclusion. |
| Tag-ledger discharge was confused with external exactly-once publication | Distinguish the ledger guarantee from publish/tag reconciliation and publisher idempotency. |
| Parser archives included the GPL specification beside MIT code | Move the specification and unchanged license outside the Go module; update links and test fixtures. |
| Specification versions had no independent release path | Add a package-local version hook using `dispat replacer`, staged validation, rollback, and CI tests. |

## Verification

- The complete `services/dispat/internal/plan` test package passed in Go 1.27. Five new regression tests exercise specification vectors 134–138. Existing tests cover fresh prerelease discharge, including vector 133.
- The version-hook regression suite passed against a real Dispat executable: version replacement, replay, build metadata, malformed declarations, symlink refusal, and rollback after the second installation write fails.
- The Docker shell gate passed, including the new specification scripts and the announcement regressions.
- CI baseline scenarios and the test-plan reference validator passed. The final report commit uses an all-package CCME scope so the normal affected-package selection reaches every module and the specification's own tests.
- Docusaurus typechecking and the production build passed with broken links, anchors, and Markdown links configured as errors. Current and archived concepts and architecture pages now qualify the proof summaries.
- English and Russian technical reports and ICSE variants compile. Final reference checks pass; the ICSE layout retains four overfull-box warnings. Paper edits remain local and are excluded from commits.

## Limits

The written proofs are reviewed arguments, not machine-checked proofs. Global convergence outside the stated retry invariant and exactly-once arbitrary external effects are not claimed. The version hook recovers ordinary write failures and catchable interruptions; its two-file installation is not atomic across a machine crash. Failed restoration preserves backups for recovery.

This change does not modify production Go behavior. Full combined coverage was not remeasured for this documentation and test change, and the existing coverage badge is not presented as a new measurement. Released-module build checks still depend on publishing the pending Aqua module versions; workspace planner tests passed.

The paper explicitly analyzes the historical 1.0.0 specification at `713f1a0b`. Its historical experiment counts were preserved. No paper, patent, or unrelated working files are part of these commits.
