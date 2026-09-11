# Landing-page download counter

## Scope

Keep the existing static hosting. Collect totals every 15 minutes and show the collection time on the landing page. Count downloads of every asset of every published GitHub release in `yohimik/dispat`, including prereleases, and repository pull totals for the four `yohimik/dispat-{alpine,debian,ubuntu,dind}` Docker Hub images. These are distribution events, not unique people or installations. Do not change the CLI or libraries.

## Execution plan

1. Read the docs package, release configuration, hosting and CI contracts. Record affected surfaces and foreseeable bugs before implementation.
2. Spawn the requested Sol agent to implement the collector, static JSON refresh workflow, landing-page component, integration tests and matching current/1.10 documentation. Keep the collector inside the docs package and use idiomatic Go structs, composition and small interfaces where they aid testing.
3. Independently review the agent's implementation and feature behavior. Verify aggregation, browser lifecycle, deployment preservation, logging, resource bounds and the last commit's CI selection. Add regression tests for every bug found after testing and record them separately below.
4. Run collector integration tests with race detection and measured coverage, targeting at least 95% of feature business logic. Run docs tests, typecheck and production build; inspect the rendered counter. Record evidence and any unavailable checks honestly.
5. Consolidate the local progress into one unscoped `fix:` commit containing the implementation, tests and documentation. File ownership selects the docs package; the same record describes the whole change. Validate diagnostics, the computed package/version plan and last-commit test selection. Do not push, publish, hand-create release records or change version declarations.

## Risks identified before implementation

- Paginated GitHub releases and assets can silently undercount. Include every page, prevent duplicate counting and reject pagination loops or unbounded responses.
- Missing, negative, fractional, malformed or overflowing counts must fail collection rather than become zero. Empty valid release lists and genuine zero counts remain valid.
- An upstream failure must preserve the last complete snapshot. Publish only after all sources succeed; replace a local output atomically and use an atomic object upload.
- Bound request duration, complete collection duration, response sizes and page traversal. Close response bodies and cancel requests on termination. Avoid uncontrolled retries or per-visitor upstream calls.
- Never forward the GitHub credential to Docker Hub or a pagination/redirect destination. Use fixed source identities and do not log credentials or response bodies.
- Site deployment uses deletion during rsync: exclude the independently refreshed JSON from deployment deletion. Do not embed that JSON into the site or PWA precache.
- Scheduled Actions runs can be delayed. Describe a 15-minute schedule rather than guaranteed real-time updates; show freshness and stale/unavailable states without presenting failures as zero.
- Browser polling must stop on unmount and avoid overlap, hidden-page work and stale responses. Preserve the last valid value during transient failures.
- Use the existing deployment identity and bucket variables; no new runtime service or infrastructure resources. Keep any new workflow narrowly permissioned and non-overlapping.

## Test cases

Use local HTTP servers for full collector-to-upstream integration: multiple release/asset pages, prereleases/drafts, duplicates, empty/zero data, four-image sum, missing fields, invalid JSON and integers, overflow, HTTP failures/rate limiting, timeout/cancellation, body/page limits, redirect/token isolation and preservation of prior output. Test the browser's schema validation, sum consistency, timestamps, stale state, failed refresh and lifecycle cleanup. Check workflow/deployment wiring and keep existing test modifications limited to the feature.

## Review and follow-ups

- The user selected static JSON refreshed every 15 minutes over a live Go service. Hosting changes are limited to preserving and uploading that object.
- GitHub download counts include repeat and automation downloads; Docker counts use Docker's own semantics. The page must identify the combined metric clearly.
- Activation requires a future authorized push and CI run. This task makes local commits only.
- Existing unrelated local changes (`go.work.sum` and untracked personal/output files) are outside this task.
- The collector uses GitHub GraphQL connections so both release and asset pagination stay complete without spending
  hundreds of REST requests every 15 minutes. Release and asset node IDs are deduplicated across moving cursor pages.
- The landing display has eight rolling places while loading and expands for larger values. Digit animation is disabled
  under `prefers-reduced-motion`, while a separate live status exposes the unformatted full value to assistive tools.

## Bugs found after testing

- Independent integration cases showed that absent or null GraphQL nodes, pagination fields and draft flags could
  publish an incomplete count. Presence checks now reject these responses; ten malformed-schema cases cover them.
- An impossible date such as February 30 passed JavaScript date parsing. Canonical calendar validation now rejects it,
  with a regression in the browser model tests.
- Browser inspection found that the global `sr-only` class did not exist, leaving duplicate status text visible.
  A scoped visually hidden class fixes it, with a wiring and clipping regression.
- The user found the first-release button reaching the edges of a narrow mobile viewport. Scoped wrapping and width
  constraints preserve the container padding. A regression guards the rule; browser measurements confirm 16-pixel
  outer margins and retained inner padding at 280 pixels, with a single-line label at 320 pixels.

The timestamp spelling mismatch, request-finalizer race, initial animation structure and reduced-motion selector were
identified during implementation review before their final tests. The Go race-test container and Docker build context
were corrected during test setup. These are distinct from the post-test product regressions above.

## Verification results

- A live collector run matched a separate REST calculation: 20,769 total, comprising 1,692 GitHub release-asset downloads
  and 19,077 pulls across the four Docker Hub repositories. The measurements were minutes apart on 2026-09-11.
- The Go integration and command suite passes with race detection and `go vet`: whole-package statement coverage is
  95.4%. Collection, release/asset pagination, integer/overflow checks and command execution reach 100%. The remaining
  uncovered statements primarily concern OS write failures and the process exit. Both CI entry points enforce 95%.
- Browser model coverage is 100% of lines and functions, and 98.46% of branches. The docs suite includes 36 tests.
- Mobile inspection verified eight larger rolling digits with dot grouping (`00.020.769`), loading from zero, a full
  accessible value, and a working development snapshot route. Generated local snapshots remain gitignored.
- Final gates include docs tests, TypeScript checking, production build and the containerised docs typecheck target,
  which also runs Go race tests, coverage enforcement and vet. The final unscoped commit selects docs tests through file ownership.

## Deferred follow-ups

Activation requires a future push and workflow run; cloud IAM and cache behaviour remain unverified in production.
A dedicated snapshot-only cloud identity is a possible later infrastructure improvement. See the
[feature review](./download-counter-review.md) for the operational limits and independent evidence.
