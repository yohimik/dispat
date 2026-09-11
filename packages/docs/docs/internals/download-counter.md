# Landing-page distribution counter

The landing page shows the sum of two public distribution measurements: downloads of every asset attached to every
published release in `yohimik/dispat`, including prereleases, and the repository pull totals reported by Docker Hub for
`yohimik/dispat-alpine`, `yohimik/dispat-debian`, `yohimik/dispat-ubuntu`, and `yohimik/dispat-dind`. Draft GitHub
releases are excluded. Repeated downloads, automated downloads and repeated image pulls all count, so this is a count
of distribution events rather than people or installations.

A scheduled GitHub Actions workflow collects a complete snapshot every 15 minutes, at minutes 11, 26, 41 and 56 of
each UTC hour. Changes to the workflow or collector on `main` also trigger an immediate refresh, and
`workflow_dispatch` remains available for manual recovery. GitHub release and asset
connections are both paginated, and the four Docker repositories are read separately. The snapshot is uploaded only
after all five sources succeed; a failed run leaves the last complete JSON object in place. GitHub Actions schedules
can be delayed, so the page shows the collection time and marks a snapshot delayed after 30 minutes.

The browser fetches `/downloads.json` directly from the static site and refetches every 15 minutes while the page is visible. Returning to a hidden tab triggers an immediate fetch. It keeps
the last valid value through a transient failure and stops requests when the page is hidden or unmounted. The eight
place counter rolls from zero while the first snapshot loads and rolls changed digits on later updates. Stationary
periods group thousands and millions, as in `00.020.769`. Digits remain large on narrow screens; larger totals add
places instead of being truncated, and reduced-motion preferences disable the animation. The JSON is uploaded
independently of site releases, excluded from the release deployment's deleting sync, and absent from the PWA
precache.

No credential reaches the browser. GitHub Actions supplies its repository token and authenticates the upload with the
site's existing Google Cloud Workload Identity Federation; Docker Hub's public repository endpoint needs no token. To
populate the counter during local development, run `pnpm --filter dispat-docs downloads:refresh` from the repository
root before `pnpm docs:start`. The helper uses an exported `GITHUB_TOKEN` or an existing authenticated `gh` CLI session,
runs the collector in Docker, and writes only a gitignored development snapshot.

The collector bounds each request to 20 seconds and the complete run to two minutes. It rejects incomplete responses,
pagination loops and values outside JavaScript’s safe integer range. JSON logs report collection and file publication
at info level, preserved snapshots at warning level and failed commands at error level. Use `-log-level debug` for
provider progress or `-log-level trace` for request diagnostics; neither level records credentials or response bodies.
