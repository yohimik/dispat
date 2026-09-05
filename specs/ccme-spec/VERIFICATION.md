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
- CI baseline scenarios and the test-plan reference validator passed during this review. The specification and parser
  are members of the shared `ccme` version group, so their major and minor release lines remain aligned while their
  package tests and patch versions remain independent.
- Docusaurus typechecking and the production build passed with broken links, anchors, and Markdown links configured as errors. Current and archived concepts and architecture pages now qualify the proof summaries.
- English and Russian technical reports and ICSE variants compile. Final reference checks pass; the ICSE layout retains four overfull-box warnings. Paper edits remain local and are excluded from commits.

## Limits

The written proofs are reviewed arguments, not machine-checked proofs. Global convergence outside the stated retry invariant and exactly-once arbitrary external effects are not claimed. The version hook recovers ordinary write failures and catchable interruptions; its two-file installation is not atomic across a machine crash. Failed restoration preserves backups for recovery.

The specification corrections do not change the commit-message grammar or the parser's executable behavior. They do
change normative release-planning rules, which is why the specification advances to 2.0.0. Full combined coverage was
not remeasured for this documentation and test change, and the existing coverage badge is not presented as a new
measurement. The CCME and models Go modules now use `/v2` import paths. Published-module checks remain release-time gates: they
must resolve the stable tags written by the same run, without workspace replacements.

The paper explicitly analyzes the historical 1.0.0 specification at `713f1a0b`. Its historical experiment counts were preserved. No paper, patent, or unrelated working files are part of these commits.


## Coordinated 1.8.0 release verification

The first coordinated run published CLI 1.8.0, the four images at 1.8.0, CCME and its specification at 2.0.0,
and manifest, scanner and writer at 1.2.0. The docs package stopped at its experiment gate. Models 2.0.0 was an
incorrect release: commit `3e8b5849` had introduced unnecessary `/v2` imports and separated models from the CLI
version group. At the owner's request, all artifacts and tags from that coordinated run were withdrawn and main was restored
to the pre-publication history before applying the repair.

Moving the specification did not change any parser production type or function. The recovery restores the original
models module path, its existing CCME v1 type identities, and membership in the CLI `fixedMajorMinor` group. CCME's
separately versioned module and specification remain in their own shared group. Models source matches its pre-migration contents
exactly apart from the license notice. The incorrect breaking release record is corrected with CCME metadata,
without rewriting commit history. The owner explicitly authorized replacing the withdrawn release with a corrected 1.8.0 run. GitHub and Docker
references can be removed, but copies already downloaded or cached by Go module proxies cannot be recalled.

Additional verification completed before CI dispatch:

- The exact `Dockerfile.gotest` `test-dispat` target passed all 1,520 CLI tests under its non-root Go 1.26 environment.
- The parser, planner, and Git adapter tests passed. Specification vectors 133–138 remain covered, including fresh
  prerelease admission, suppression changes, channel eligibility, and per-unit self-source exclusion.
- Bulk tag tests now include a divergent branch with an unreachable higher version. Tag visibility, alias handling,
  and release-lock exclusion are preserved.
- Histories are shared by peeled baseline commit identity, with a safe tag fallback when an adapter supplies no OID.
  Regressions distinguish shared commits, distinct commits, and unknown commit identities.
- The specification's performance discussion now correctly treats generation numbers as a way to prune ancestry
  searches, not as a constant-time proof of reachability. Distinct baseline commits need not correspond to release runs.
- The specification's version-hook suite, CI baseline scenarios, pre-release checksum checks, and single-call
  announcement regression passed. Docusaurus typechecking and production build passed.
- Full JSON status output was identical before and after bulk tag loading. Native median time changed from 1.452 to
  1.242 seconds in the coordinator's three-run comparison. These timings cover that optimization, before the additional
  baseline-identity cache change; they are not a benchmark of the published binary.

The final `test(*)` commit requests the complete affected-package test sweep. CI and the release workflow must still
pass on the pushed revision. The release workflow computes fresh combined coverage and requires an unrounded ratio
of at least 95%; this document does not substitute earlier coverage measurements for that gate.

## License boundary verification

Commit `da2695d2` distinguishes MIT grants for official compiled binaries from GPL-3.0-or-later source files that
reference the specification. The CCME parser remains MIT and has no changes in that commit. Other source files
remain MIT unless separately licensed. Earlier distributed copies retain their original grants.

The coordinator and an independent reviewer checked all 59 source and test files receiving SPDX notices. Removing
the newly added notices reproduces each file's previous contents exactly. The MIT license text is unchanged, and
the GPL copies in the published CLI and models modules match the specification's GPL text byte for byte. Module
notices, READMEs, the architecture page, and site metadata distinguish the source and binary grants consistently.

Docusaurus typechecking and production build, the specification verifier and version-hook tests, and test-plan
reference validation passed after the notice changes. The preceding CI revision passed Tests and Format and vet;
the new `test(*)` commit requests fresh CI for this complete revision before release dispatch.

## Corrected release plan after rollback

The corrected planner output selects models, Dispat, all four images and docs at 1.8.0. Models shares the CLI
`fixedMajorMinor` group. Only CCME and its specification advance to 2.0.0; manifest, scanner and writer advance to
1.2.0. The checked-in specification version and its Markdown declarations remain 1.0.0 until the release hook
stamps 2.0.0. No additional major-version policy gate was added.

All models and CLI Go tests passed after restoring the original module imports, and the integration harness
compiled against those same types. The specification verifier and version-hook suite passed. The repaired orphan
experiment passed all eight checks against the published 1.8.0 image before that image was withdrawn: the failed
package explicitly enables rollback, previously published packages are not republished, and the next plan is empty.

The rollback removed all eleven publications from the failed run, including the erroneous models 2.0.0 release.
Docker rollback run 33947918048 verified the four image repositories: stable aliases again resolve to 1.7.2 and the
1.8/1.8.0 tags are absent. Discord deletion was verified through its API; the owner confirmed LinkedIn and Instagram
cleanup. The corrected main revision requires fresh CI and the complete release gates before publication.

An isolated copy of the checked-in specification passed the real release hook from 1.0.0 to 2.0.0. The hook
updated `VERSION` and all three normative Markdown declarations, and the resulting specification passed validation.
The version-hook regression suite also passed. README wording distinguishes the unchanged 1.0.0 message grammar
from the separately versioned specification, so publishing 2.0.0 does not leave a stale current-version claim.
