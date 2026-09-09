# Validated commit authoring: implementation and review plan

Status: implementation and fresh independent verification complete; CI-gated release pending. The author subsequently requested shipping the feature and explicitly confirmed that docs and Docker images must release with it. Release follows successful verification; research reruns use the resulting published binary.

## Scope and compatibility

Add a Git-backed authoring entry point that validates the complete proposed commit message with the repository's existing CCME parser settings. Preserve parser code, grammar, release computation and unrelated commands. Dispat 1.8.2 already provides a native release-step `commit` command, used by this repository's publishing flow. The author selected natural message flags for authoring and rejected a Git-named mode flag. `-m`/`--message`, `-F`/`--file`, `--edit`, `--amend`, `-C`/`--reuse-message` and `-c`/`--reedit-message` select validated authoring; release-step options keep their meaning. Do not silently replace the release default or alter release configuration.

CCME 3 already specifies external VCS adapters. Clarify that its existing adapter operations concern release snapshots, records and locks, not Git CLI argument forwarding or source-commit creation. Describe optional authoring validation without pretending Dispat implements external adapters or CCME 3 rollback. Keep specification version markers at their baseline until normal release versioning.

The implementation agent is Sol, replacing the originally requested Opus. The coordinating agent independently reviews the implementation, reproduces the critical boundaries and owns final local commits.

## Before implementation: hazards and invariants

1. Validate the message that will actually become the commit, after editor and existing hook changes, accounting for Git cleanup. A precheck of `-m` alone is insufficient. An error in any CCME unit rejects the whole proposed commit; warnings stay visible and nonblocking.
2. Forward Git arguments as argv. Do not interpolate messages, paths or credentials into shell source or logs. Keep native editor, signing, stdin, hooks and Git exit behavior where supported.
3. Preserve existing hooks once each, including custom hook paths and worktrees. Reject bypass options before mutation when mandatory validation cannot be guaranteed. Cover short aliases, message reuse, fixup/squash, dry-run, and empty-message options.
4. Preserve HEAD and unrelated staged/worktree files on rejection. Do not implement automatic staging, release tags, publishing, rollback or release planning in authoring mode.
5. Bound message/config reads and diagnostic output. Remove private temporary state on every ordinary exit. Cancel the owned process tree, including editors/hooks, without killing unrelated processes. Examine Windows and TinyGo compatibility before claiming support.
6. Emit lifecycle trace/debug logs without message-bearing argv; warnings at warning level and errors at error level. Do not claim successful validation on a Git dry-run or a path that skipped the validator.

### Pre-implementation finding CV-01

An independent disposable-Git probe showed that `commit-msg` sees the raw message before final cleanup. With
`--cleanup=strip`, a leading comment can be present in the hook input but absent from the commit object. The authoring
integration must validate the effective final bytes, and regression tests must compare those bytes with the resulting
commit object. This was discovered during design, before implementation tests; it is not a post-test regression.

## Implementation and acceptance

- Sol owns the CLI/application implementation, focused unit tests and a dedicated black-box integration test file. The coordinator owns specification/docs/test-plan changes and final review.
- Give each integration invariant one primary goal in `test-plan.md`; reuse existing legacy-command tests rather than duplicate their entire coverage.
- Exercise real disposable Git repositories for input modes, configured/default parser behavior, multi-unit errors, warning handling, transformed messages, hooks, signing/editor failures where reproducible, invocation failures, cancellation, and cleanup.
- Measure statement coverage for the new implementation using the integration-built binary; aim for at least 95% of feature statements. Report the actual denominator and uncovered paths. A percentage is not proof of all business cases. Do not mix this with a stale combined-repository badge.
- Run focused tests first, then the affected module suite, instrumented integration race checks, test-reference validation, spec lifecycle checks, docs typecheck/build with broken-link checks, and relevant cross-builds. Broaden only for demonstrated dependencies or failures.
- Update upcoming and served 1.8 docs in their existing style, identifying availability correctly. No unsupported promise that old 1.8 binaries already provide the new option.
- Create short local commits using explicit paths. Put the final semantic release intent for the specification in the final release-intent commit; do not add parser changes or accidentally release the parser through its version group. Verify the resulting release plan and the final commit's CI selection. The subsequent shipping request authorizes the normal CI-gated release. Do not rewrite history or publish outside the release workflow.
- Never stage `paper/`, `patent/`, `pater/`, `.claude/`, `guides/`, `output/`, `.DS_Store`, or the pre-existing `go.work.sum` change.

## Findings after testing

Record each newly discovered bug separately here with its reproducer, regression-test owner and resolution. Do not silently relabel a post-test discovery as a planned case.

- **CV-DOC-01:** The first docs build rejected a new link from the historical 1.8 command page to `/next/cli/commit/`. Removed the cross-version link and retained a version-specific availability note. The existing historical-links build gate owns this regression; no duplicate test was added.

- **CV-02:** An initial black-box run exposed harness/global `--root` being forwarded to Git. The CLI now extracts supported Dispat global flags while preserving Git argument values.
- **CV-03:** The first no-config run checked the wrong error sentinel. The default parser is now used only for `pkg/config.ErrNoConfig`; malformed configuration still fails.
- **CV-04:** Independent review after the first test pass found that default cleanup guessed editor use. An invocation-private editor marker now selects cleanup from actual editor execution; explicit whitespace stays explicit. Goal 50 owns the editor regressions.
- **CV-05:** Review found bypass flags and `--` could be misread inside message arguments. Option-aware scanning now respects values and path boundaries, rejects bypass clusters and unsupported abbreviations, and is covered by argument-boundary tests plus native probes.
- **CV-06:** Copying a hook into the proxy directory broke hook-relative helper paths. Wrappers now invoke the original executable path; Goal 50 verifies adjacent helper access and exactly one invocation.
- **CV-07:** Configuration ascent changed relative Git file/pathspec interpretation. Authoring now retains the invocation directory while loading parser settings from the resolved configuration; Goal 50 owns the nested-directory regression.
- **CV-08:** Long Git option values and attached signing-key text exposed another routing ambiguity. The classifier now consumes long option values and attached signing-key/template text before looking for authoring selectors or global flags. `TestSplitGitCommitArgsDoesNotInterpretOtherGitOptionValues` owns these regressions.

- **CV-09:** Final static platform review found that Unix executable-bit checks would omit ordinary Git for Windows hooks and its `.exe` fallback. Hook discovery now follows the platform rules, keeps extensionless-hook precedence, and normalizes fallback proxies while invoking the original file. Platform-policy and fallback tests run on Linux; Windows packages also cross-compile. Native Windows execution remains unverified. The source reference is [Git's Windows access implementation](https://github.com/git/git/blob/master/compat/mingw.c) and its hook lookup in `hook.c`.

- **CV-10 (post-push, before release):** An independent disposable-repository probe found that `dispat --package commit -m "feat(core): x"` treated the package value as a command and created a source commit. The pending CI run was cancelled; no release had started. Command discovery now uses the actual pflag declarations, and authoring rejects release selectors before as well as after the command. `TestCommitValidationNeverUsesFlagValueAsCommand` and `TestCommitValidationRejectsPrefixedReleaseFlagsBeforeMutation` verify unchanged HEAD and staged contents. A focused correction follows the feature commit without rewriting history.

## Follow-ups and disposition

- Existing release-command name conflicts with authoring: resolved through natural authoring flags while preserving release-step invocations. Git is not named in a new mode flag.
- External adapter source-commit authoring is absent from CCME 3's current wire protocol: clarify the boundary; implementing a new adapter operation is out of scope unless separately requested.
- Local authoring checks can be bypassed by invoking Git directly. CI should validate incoming history; this feature alone is not a repository-wide enforcement boundary.
- Native Windows runtime availability and exact TinyGo compatibility must be verified or reported as limitations, not inferred from a Linux container.
- Windows cancellation retains the existing subprocess runner behavior: it kills the direct process and bounds pipe waits. Unix process-tree termination is tested; Windows descendant termination is not guaranteed by this existing helper. A broader Windows job-object lifecycle is outside this feature and requires native runtime testing.
- Three pre-existing test names occur in both unit and integration modules. Their layers exercise different boundaries; the reference validator reports them, and this feature introduces no additional naming collision.

## Verification evidence (9 September 2026)

After CV-10, the fresh Docker measurement of the production source passed the strict,
unrounded combined gate: **19,305 / 20,317 statements (95.01895%)**. The report
contains 2,996 tests and 37 fuzz targets. All 641 integration cases passed both
the ordinary run and the run with the separately built Dispat subprocess
instrumented for races. This is a local working-tree measurement; CI must
produce its own commit-stamped report before release.

The three new production files cover **303 / 317 statements (95.58%)** when
unit and real-CLI integration profiles are combined by source block. That
figure is not integration-only coverage or the denominator of every edited
function in existing files. The 27 authoring integration cases own the
end-to-end invariants; policy and filesystem failure tests cover narrower
boundaries. Remaining uncovered blocks are defensive metadata/input I/O and
private-file write/close failures, rather than untested normal authoring flows.

Independent macOS probes compared the resulting commit object's exact message
bytes against native Git for all five cleanup modes, with and without an
editor. All 18 probes passed, including three bypass spellings and five routing
rejections that preserve HEAD and staged contents. Application
and CLI vet, the full service tests, specification/guide lifecycle tests, docs
typecheck and production build, and test-plan reference checks passed. Windows
packages cross-compiled; a native Windows runtime was not available, so runtime
parity there is not established by this review.

After CV-10, the TinyGo 0.43.0-net.2 build passed all 823 integration tests and
subtests with zero failures and zero skips against its native ARM64 release
binary. Both Linux architectures compiled.

An early full race attempt with a ten-minute harness limit timed out waiting
for an install subprocess. It produced no race report; the canonical full
measurement subsequently passed every integration case under its normal
20-minute limit. Two early TinyGo assertions used a stale diagnostic substring
and an invalid fixture configuration; both test fixtures were corrected before
final acceptance. These were test defects, not evidence of product corruption.

The disposable release-plan check selected Dispat 1.9.0, models 1.9.0, the agent
guide 1.9.0, docs 1.9.0, all four Docker images 1.9.0, and specification 3.0.2.
The parser remains at 2.0.0 with its existing hold. The feature multi-unit commit
carries the feature's transitive-consumer intent and the specification's patch
intent. Changing `specs/ccme-spec/SPEC.md` selects all CI test modules through
`scripts/ci-base.sh`; the focused CV-10 correction carries only `fix(dispat)^^` and its tests. Its committed CI selection and release plan are checked before the follow-up push.
