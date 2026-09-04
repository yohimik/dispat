# Release readiness review

This review covers the release engine, manifest tools, tests and CI, documentation, and live landing demonstrations. Sol agents implemented bounded changes; the coordinating agent inspected their diffs, reproduced critical regressions, and ran independent browser checks. No releases, remote tags, pushes, or history rewrites were performed.

## Findings and disposition

| Finding | Disposition and evidence |
| --- | --- |
| Shared-checkout lock acquisition could reuse another attempt's local tag object. | Fixed: unique attempt objects, immutable object IDs, non-overwriting remote acquisition, and expected-object leases on deletion. Ownership/interleaving and interruption regressions pass. |
| Cleanup could outlive cancellation indefinitely. | Fixed: unlock has a 30-second budget; detached release finalization has a five-minute budget. Incomplete records and cleanup failures remain visible. |
| Commit or rollback could capture/discard pre-existing work. | Fixed: refuse affected dirty paths when automatic commit or per-package rollback can touch them. Generated files remain valid rerun inputs when neither behavior is enabled. Path-limited standalone commits preserve unrelated staged work. |
| Symlink and partial multi-file writes risked unintended modification. | Fixed: writers refuse symlink targets, preserve modes, validate before atomic replacement, and remove temporary files. CLI regression verifies the first completed write is reported when a later target fails; the symlink and its referent remain unchanged. Multi-file rewrites are not one transaction. |
| A publisher can succeed before the client receives a response or records a tag. | Verified and documented: independent packages finish safely and the command reports failure for incomplete work. A lost-response GitHub experiment verifies recovery without another POST. Arbitrary publishers still require idempotency or destination inspection in this ambiguous interval. |
| Self-update version checking could accept an incorrect executable. | Fixed: require the exact version line while accepting the CLI's preceding logo. Real CLI self-update regression passes. Race-prone HTTP test fixtures were corrected. |
| HTTP work retained queued payloads after shutdown; oversized API responses could be silently truncated. | Fixed: timed-out webhook shutdown cancels active work and drains queued payloads without sending them. Bounded API reads reject oversized bodies. |
| Credentials could appear in diagnostic URLs or command logging. | Fixed: redact URL credentials/query/fragment and avoid logging raw shell commands or webhook URLs. Regression tests cover diagnostics and logging. Command output remains output from the user's own scripts. |
| Large histories allocated redundant package windows and author storage. | Fixed: share immutable membership sets for identical baseline windows, traverse each identical history listing once, and retain only distinct author identities. Performance figures below record the same synthetic fixture before and after. |
| Aqua project manifests were missing. | Implemented in scanner, compute, writer, autowriter, and autoVersion, including conventional filenames, local imports, registry-qualified identities, exact pins, and source ownership. Tools-only conformance uses upstream Aqua v2.50.0 configuration types. |
| Aqua inputs can contain imports, expressions, links, and unusual YAML. | Covered: deterministic bounded local reads, cancellation, cycles, canonical containment, alias ownership, inline/separate versions, comment/quote/newline preservation, and partial failures. Expressions and `go_version_file` remain unchanged. Unsafe flow/anchor/alias write forms are refused. No registry fetching, expression evaluation, installation, or checksum changes occur during scanning. |
| Integration races did not instrument the CLI subprocess. | Fixed: the integration race pass builds both ordinary and version-stamped subprocesses with `-race`, and race reports independently fail the gate even when a scenario expects a nonzero exit. |
| Coverage profiles could be incomplete or mixed across runs. | Fixed: invalidate a module stamp before testing, write it only after success, require every module profile and matching commit stamps, and gate the unrounded combined statement ratio at 95%. Badge and documentation use this same denominator and complete run. |
| Push and pull-request selection used an insufficient window. | Fixed: push uses the prior push revision; pull requests use the tested merge's base parent. Missing/untrustworthy baselines select all modules. Disposable single-commit, multi-commit, merge, shared-infrastructure, and integration-only scenarios are covered. |
| Test-plan references and counts had drifted. | Corrected both missing Linkr references and assigned all behavioral integration cases to explicit goals. Reference validation names modules explicitly. Similar cases across unit, CLI, and lifecycle boundaries were retained where they verify different invariants; three cross-module shared names are intentional. |
| A second consumer-window flag would duplicate existing behavior. | Not added: existing `--since` accepts Git revisions, `HEAD~1` through `HEAD~9`, tags, branches, and `all`, composed with `--consumers`. Regression tests cover the selection windows. |
| Landing copy and recovery claims overstated guarantees. | Rewritten: explain the outcome, installation, first plan, dependency terminology, and recovery limits in natural English. Corrected current and served 1.7 behavior; Aqua stays in unreleased documentation. Package READMEs retain a formal tone; CCME SPEC remains normative. |
| Landing videos were stale and loaded unnecessary media. | Replaced with one lazy Remotion Player and the selected shared composition. Stable feature IDs survive heading edits. Corrected Run's transitive web consumer, lock ownership, recovery claims, and Aqua story. Thirty unused landing MP4/WebM assets were removed after reference checks. Existing README GIF stories are unchanged, so their exports were retained. |
| Documentation navigation and mobile presentation were crowded. | Improved global typography, page width, sidebar grouping, tables/code, focus states, light/dark presentation, and mobile navigation/search. Removed the obsolete first-release announcement from current and 1.7 getting-started pages. Installation appears before the demo on the landing page. |
| Mobile live scenes were initially unreadable at full-scene scale. | Corrected after recordings: only the readable canvas scrolls horizontally, by touch or keyboard. Captions, guidance, transcript, and controls wrap within the viewport. Desktop and narrow viewport checks verify no document overflow. |

## Architecture and behavior review

The existing package boundaries, composed Go types, small interfaces, and dependency scheduler remain appropriate. There was no demonstrated need for a framework rewrite or inheritance-style abstractions. Validation returns and documented `Must` helpers are intentional; process termination remains at command boundaries. Release cancellation and failure paths now preserve required finalization.

Planning, version groups, prerelease trains, propagation, hooks, selection, records, installers, and self-update were reviewed against their tests and documented behavior. Deterministic plans, selections, records, and summaries matter; concurrent stage completion order remains naturally nondeterministic. Debug/trace events cover planning, scheduling, stages, recording, cleanup, and recovery, with actionable user events at info/warning/error levels.

A test count or statement percentage does not prove every possible business case. The integration goal inventory documents the tested invariants; the combined coverage gate includes unit and integration instrumentation and is not an assertion of 95% integration-only coverage.

## Verification

The complete measured build at `a431c7b3d67d2c04ec6e3e958c0425f65cc327e2` passed:

| Gate | Result |
| --- | --- |
| Combined statement coverage | **18,814 / 19,800 = 95.0202%**, above the unrounded 95% threshold; badge display 95.0%. |
| Tests | **2,918 passed**, no failures or skips. |
| Fuzz targets | **37** seed suites passed; scanner/writer also received bounded fuzz runs. |
| Integration | **608 passed**: 607 behavioral scenarios and one harness integrity check. |
| Integration race | **608 passed in 47.1 seconds**, with both harness and CLI subprocesses instrumented. |
| Benchmarks | **48 measurements** across three modules. |
| Test-goal references | **630 valid references; 608 assigned integration goals**. |

Coverage profiles, the badge, and `packages/docs/data/report.json` share the measurement commit above. Generated coverage and documentation data remain ignored build artifacts, as configured by the repository. The final commit contains only goal documentation and this review; it does not change measured production or test code. Unit coverage is 88.1%; CLI subprocess integration coverage is 86.2%; overlapping covered statements produce the 95.0202% combined result.

The 10,000-commit, 500-package chain fixture improved from a five-run median of 1.79 seconds and 889.1 MiB peak RSS to 1.46 seconds and 116.6 MiB: 18.4% faster and 86.9% lower peak memory. An independent coordinator run measured 1.48 seconds and 116.6 MiB. Final live Go heap was approximately 22 MiB. These local synthetic measurements establish the eliminated allocations; they are not a promise for every repository.

The race gate removes only the race runtime's per-process exit sleep, preserving instrumentation and the test timeout. The option is documented in [Go's race detector options](https://go.dev/doc/articles/race_detector#Options).

Completed checks include formatting, all nine Go vet targets, module tidiness, shell checks, pinned actionlint, test-reference validation, bounded scanner/writer fuzzing, repeated shuffled lock/interruption tests, unit race checks for affected safety paths, real CLI failure/recovery experiments, and release builds for six standard and two TinyGo targets. The instrumented race and coverage results above supersede earlier measurements. The original fully instrumented race attempt reached its 20-minute timeout because of the one-second process-exit sleep; after removing that delay, the complete suite passed. A deliberate racy-child experiment also proved that recorded races fail the gate even when the scenario accepts child failure.

Docusaurus type checking and production builds pass with broken links and anchors configured as errors. Playwright recordings cover the landing, installation, recovery, Aqua, and scanner pages at 1440×1000 and 390×844, plus light mode, search, mobile navigation, all 16 scenes, reduced motion, keyboard controls, manual pause across selection, offscreen pause, failed scene loading, and static installation text without JavaScript. The visibility handler was tested by dispatching a hidden-document signal; this is not an operating-system tab-switch test.

The final page tour reported no JavaScript errors or horizontal page overflow. Landing CLS was zero in both viewports. Observed initial resource transfer decreased from approximately 5.46 MB to 2.24 MB on desktop and 4.87 MB to 1.73 MB on mobile. These local browser observations are not a controlled network benchmark. Only the active scene mounts; inactive scenes perform no animation work.

Playwright recordings and screenshots are retained locally under `output/playwright/release-readiness/` and `output/playwright/live-demo-keyboard-final/`. They are deliberately uncommitted. The repeatable smoke runner is `packages/docs/demo/playwright-smoke.mjs`.

## Explicitly deferred or limited

- Historical commit diagnostics: 34 unit/message errors all predate the v1.7.2 release. Current `commitErrors: warn` policy leaves these units inert and `status` returns a valid plan with exit 0. History was preserved as requested; changing that policy to `error` would require an explicit treatment of those old commits.
- Persistent recovery journal: deferred as agreed. Git tags remain the completion record, with documented publisher idempotency requirements for ambiguous outcomes.
- Aqua registry authoring and installation: outside project-manifest support. Dynamic expressions, `go_version_file`, and checksums are not evaluated or rewritten.
- Duplicate release-window flag: unnecessary; existing `--since` and `--consumers` cover the requested workflow.
- Unrelated dependency upgrades and speculative architecture/performance rewrites: not performed. Remotion packages were aligned only as required for the live player.
- Real external publishing and every third-party registry combination: not executed. Local failure experiments use isolated repositories and test servers.
- Browser evidence uses Chromium desktop/mobile emulation. Other browser engines and physical devices were not certified by this review.
- `pater/`, `patent/`, `paper/`, unrelated `.DS_Store`, `.claude/`, `guides/`, and `output/` content are excluded from commits.

## Local commits

| Commit | Change |
| --- | --- |
| `b7434468` | Bound release HTTP work and webhook shutdown. |
| `c4e6c629` | Protect release lock ownership, cleanup, commits, and existing work. |
| `23cf03d3` | Harden self-update checks and diagnostic logging. |
| `2219aa84` | Refuse symlink writer targets. |
| `18f3e2c2` | Add Aqua project manifests. |
| `42ab5198` | Keep lock generation TinyGo compatible. |
| `41c62a3b` | Cover partial writes, recovery, dirty paths, and demo/selection boundaries. |
| `645e3c1a` | Validate CI windows and coverage integrity. |
| `73a4e86b` | Prefer canonical writable Aqua sources. |
| `3419488c` | Refresh Docusaurus, natural copy, and live demonstrations. |
| `78a79042` | Clarify workflow shell analysis. |
| `06e5890b` | Share immutable commit windows. |
| `d949e3de` | Retain only distinct author identities. |
| `a431c7b3` | Enforce subprocess race reports. |

The final validation commit uses `test(*): verify release gates`. Its parent window is checked against the real repository configuration to ensure every affected test module is selected. No narrower commit follows it.
