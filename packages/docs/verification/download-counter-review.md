# Download counter review

## Feature review

The approved scope is a static snapshot on a 15-minute schedule. Eight rolling digits begin at zero during loading, settle on the fetched total and animate subsequent changes. Dot separators group thousands and millions, and larger mobile digits remain readable. Visible browser tabs refetch the snapshot every 15 minutes and refresh immediately on return. The complete value must remain readable by assistive technology, and reduced-motion preferences must disable spinning. Values longer than eight digits must expand rather than truncate.

The total combines repeat GitHub release-asset downloads with Docker Hub repository pulls. It is not a count of unique users or successful installations. Both stable releases and prereleases belong in the GitHub total. The four Docker image repositories are counted once each; summing tag totals would be a different metric.

Scheduled GitHub Actions runs can be delayed or disabled after inactivity. The collection time and stale state are part of the feature contract. A failed source must not turn a partial result into a new total. The browser must retain the last complete snapshot during transient failures.

## Independent checks before feature testing

The existing docs suite passed all 22 tests, TypeScript checking passed, and the production build passed before implementation.

A separate REST API check on 2026-09-11 at 05:05:48 UTC found 347 published releases over four pages, containing 247 assets, with no release exceeding eight assets. Their download total was 1,692. The Docker Hub totals were 4,957 for alpine, 4,627 for debian, 4,665 for ubuntu and 4,828 for dind: 19,077 pulls and a combined 20,769 events. These are point-in-time review measurements, not a committed fallback for the page.

GitHub's live GraphQL endpoint also accepted the proposed release and release-asset connections. Batching these connections avoids a separate request for each empty release. The collector still needs to traverse both connection cursors. The [GraphQL rate-limit reference](https://docs.github.com/en/graphql/overview/rate-limits-and-query-limits-for-the-graphql-api) documents a repository's Actions token budget and requires explicit error handling even for HTTP 200 responses.

The hosting review confirmed that deployment deletes objects missing from the static build. The dynamic snapshot therefore needs an exact exclusion in that synchronization. The [gcloud rsync reference](https://docs.cloud.google.com/sdk/gcloud/reference/storage/rsync) defines exclusions as regular expressions on relative paths.

## Implementation issues identified before testing

The first independent source review identified the following cases for correction and regression coverage before the final test run:

- Go's RFC3339 timestamp without milliseconds did not match the browser validator's exact ISO string comparison.
- Missing GraphQL connection fields could decode as an empty valid result, silently lowering the total.
- An aborted browser request's completion handler could clear a newer request after the page became visible again.
- A browser request needed a deadline so loading could not spin indefinitely.
- The initial animation moved entire digit boxes rather than rolling digits inside stationary windows.
- The collector needed explicit signal cancellation and request trace logging, with operational failures at suitable log levels.

## Follow-ups and limits

- Activation is deferred to a future authorized push and workflow run. No production object, release or remote Git state is written by this task.
- Reusing the established cloud identity avoids new infrastructure, but that identity has broader rights than a single-object updater needs. A dedicated identity restricted to the snapshot object is a possible later infrastructure change.
- GitHub and Docker do not offer a shared transactional timestamp. The snapshot records a bounded collection across sources rather than an instantaneous global count.
- Upstream deletions and provider corrections can lower a total. The UI should show the received total rather than silently enforce a monotonic counter.

## Final verification

The independently reviewed Sol implementation passes the collector integration and command suite with race detection
and `go vet`. Whole-package Go statement coverage is 95.4%; browser-model lines and functions reach 100%, with 98.46%
branch coverage. Both enforce a 95% floor. Docs tests, TypeScript checking, production build and the Docker typecheck
target all passed. The Docker target also runs the collector gate.

Browser checks covered loading reels, the live 20,769 value, expanded values, mobile digit size and dot grouping.
The first-release button now wraps at 280 pixels with 16 pixels of outer space on each side and retains its inner
padding; at 320 pixels its label fits on one line. Temporary viewport overrides were reset. Reduced-motion behavior
was checked in source and tests; native browser emulation was unavailable.

Post-test regressions and their added cases are listed separately in the
[verification plan](./download-counter-plan.md#bugs-found-after-testing): malformed GraphQL schemas, impossible dates,
visible assistive text and mobile button overflow. The Go timestamp and browser request-finalizer issues were caught
in implementation review before final testing.

The changes affect the docs landing page, its local development snapshot route, the docs test image and static-object
publication. CLI behavior and Go libraries remain outside scope. Current and version 1.10 documentation are mirrored;
version declarations are unchanged. One unscoped CCME commit contains implementation, tests and documentation and requests only a docs patch through file ownership, with no push or publication.
