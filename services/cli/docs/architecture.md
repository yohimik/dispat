# Architecture

## Runtime steps

1. Parse the command line (pflag); dispatch `release` or `status`.
2. Load and validate `dispat.json` (viper; unknown keys rejected; flag bindings applied).
3. Discover packages: every direct sub-folder of each space path, names unique across spaces.
4. Build the dependency graph from configured relations; topologically sort it (cycles abort with the members named).
5. For each package: resolve the latest `pkg@*` tag (highest parseable version when the newest tag parses; otherwise the
   unparseable newest tag with the baseline taken from config `initials`, default 0.0.0), scan commit subjects since it,
   compute the own bump; then one topological pass propagates provider changes into consumer patch bumps — including
   catch-up bumps for consumers whose provider's latest tag is newer than their own (or who were never released while a
   provider was) — and fixes next versions.
6. Print the full graph, highlighting changed packages with `old -> new` versions. `status` stops here.
7. When `commit.push` or GitHub releases are enabled: verify remote/API access up front (`git ls-remote`,
   `GET /repos/{owner}/{repo}`) and fail fast before any release work.
8. Execute the task graph (version/build/publish per changed package) with per-stage concurrency budgets.
9. After each successful publish: run the release recorders (changelog file; GitHub release unless in release-commit
   mode), then create the annotated tag (deferred in release-commit mode).
10. Finalize phase (when `commit` is enabled): one release commit staging all published packages, tags on that commit,
    push (`--follow-tags`, when `commit.push` is enabled), then GitHub releases referencing the pushed tags.
11. Print a per-package summary plus totals; exit `1` if anything failed.

## Module map

```
main.go                  thin entry point: os.Exit(cli.Run(...))
internal/
  cli        wiring: pflag flags, command dispatch, zerolog setup, recorder
             assembly (changelog + github), upfront remote/API verification,
             finalize phase (release commit, tags, push, github releases),
             graph printout, summary, exit codes
  config     viper loading (UnmarshalExact + weak typing for scalar-or-pair
             concurrency), changelog/github option objects, initials baseline
             versions, validation, package discovery on the filesystem
  model      shared domain types: Space, Package, Dependency
  conventional  regex-free conventional-commits subject parser (single pass,
             byte-index scanning)
  semver     strict MAJOR.MINOR.PATCH parse/compare/bump, regex-free,
             overflow-checked
  graph      Kahn topological sort over an adjacency list with a name-ordered
             min-heap: O((V+E) log V), deterministic output, cycle detection
             by leftover in-degree
  gitx       Git interface + CLI implementation shelling out to git
             (tag --list per package, log --format=%s ranges, tag -a,
             checkout + clean for revertOnFail rollbacks, add + commit for the
             release commit, ls-remote verification, push --follow-tags)
  plan       planning: baseline resolution (parseable tag > initials > 0.0.0),
             per-package own bump from history, then one topological pass to
             propagate provider changes and fix next versions
  release    the executor: task-graph construction (version/build/publish
             nodes, dependency edges), dependency-counting scheduler with
             per-stage worker budgets, skip propagation, provider-update
             filtering, DISPAT_* script environments, line-buffered log
             streaming of script output
  script     Runner interface + ShellRunner (configurable shell, default
             sh -c; injected env, cwd = package)
  changelog  entry rendering (Format with defaults) + FileWriter recorder that
             prepends entries to the per-package changelog file
  github     Releaser recorder: same changelog data delivered as a GitHub
             release via POST /repos/{owner}/{repo}/releases
```

## Task graph

Each changed package contributes up to three task nodes:

- `version` — exists only when the package's bump is `DueTo` provider updates. Incoming edges: each changed provider's
  `build`, plus that provider's `publish` when the provider's space sets `isBuildWaitingPublish: true`. Outgoing edge:
  the package's own `build`.
- `build` — depends on `version` when present, otherwise carries the provider edges itself. Outgoing edge: `publish`.
- `publish` — depends on the package's own `build` and always on every changed provider's `publish` (publishing against
  an unpublished provider version would be broken regardless of the flag; `isBuildWaitingPublish` only controls whether
  the consumer may *build* before the provider publishes).

The scheduler is a dependency-counting loop (Kahn's idea at task granularity): tasks whose in-degree reaches zero enter
their stage's ready queue; a single coordinating goroutine launches workers within each stage budget (`build` and
`version` share the build budget, `publish` has its own) and collects completions over a channel. There are no locks in
the scheduling hot path — a mutex guards only the shared result map that skip decisions and provider filtering read.
Every task finishes exactly once, including no-op completions for packages already failed or skipped, which is what lets
the counting terminate without special cases.

## Failure semantics

A failed script (or release recorder) marks the package failed; nothing aborts the run. `Result.FailedStage` records
where it failed (informational, shown in the summary). At the start of every task the package re-evaluates the skip
rule: skip if some changed provider failed (at any stage) or was skipped AND the package has neither own commits nor a
successfully published changed provider. A consumer's terminal outcome is deterministic in both modes: its publish
always waits for its providers' publishes, so a provider's publish failure is guaranteed to be seen at the latest there.
With `isBuildWaitingPublish: true` provider outcomes are already final before the consumer's version stage; with `false`
the consumer may spend a version/build on a release that its publish then skips — the trade-off that flag opts into. The
version stage filters failed/skipped providers out of `DISPAT_UPDATED_PROVIDERS` and skips its script entirely when none
remain.

Failed or skipped consumers are not lost across runs. Tag creation times (`creatordate`, returned by `gitx.LatestTag`)
let the planner detect a consumer whose provider's latest release tag is newer than the consumer's own — the signature
of "provider published, consumer failed" — and schedule the missed patch release in the next run (`DueTo` then contains
an *unchanged* provider: it gets no task nodes, and the version stage passes its released version with
`oldVersion == newVersion`). A consumer that was never released while a provider has been is caught up the same way.
Same-second tag times from a single successful run compare as fresh (strict `>`), so normal runs never self-trigger.

For spaces with `revertOnFail: true`, a failing package (any stage, including a failing release recorder) has its folder
rolled back via the `Reverter` interface (`gitx.CLI`: `git checkout -- <dir>` + `git clean -fd <dir>` — tracked files
restored from HEAD, untracked files removed, scoped to the package folder). The same rollback runs when a package is
skipped after its version stage already modified files. A revert error is logged but the package keeps its original
failure status.

## Design decisions

Interfaces decouple every side effect, keeping the planner and executor unit-testable with in-memory fakes:

| Interface                 | Implementations                           | Used by |
|---------------------------|-------------------------------------------|---------|
| `gitx.Git`                | `gitx.CLI` (shells out to git)            | plan    |
| `script.Runner`           | `script.ShellRunner`                      | release |
| `release.Tagger`          | `gitx.CLI`                                | release |
| `release.Reverter`        | `gitx.CLI`                                | release |
| `release.ReleaseRecorder` | `changelog.FileWriter`, `github.Releaser` | release |

`ReleaseRecorder` is the extension point for publishing release data anywhere: both current implementations render the
same changelog sections (shared `changelog.Format`, zero-value fields fall back to defaults) and differ only in the
destination — a file prepend vs. a REST call. Recorders run in order after each successful publish; a recorder error
fails the package before tagging.

Other choices: versions in git tags only (no version files to commit); one `git tag`/`git log` pair per package rather
than a global log walk (simple and correct for per-package tag baselines); shelling out to the git binary to match CI
byte-for-byte; script output streamed line-by-line into the structured logger so parallel package logs stay
attributable; deterministic ordering everywhere (alphabetical tie-breaks in the toposort, sorted space iteration in
discovery).

Complexity: discovery O (packages), planning O (V+E) graph work plus one git query pair per package, execution O (V+E)
scheduling overhead on top of script runtime.

## Testing

`go test ./...` — testify-based unit tests with in-memory fakes:

| Package      | Coverage                                                                                                                                                                                                                                                                                                                                                                                                      |
|--------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| semver       | Parse/compare/bump tables, invalid inputs, overflow guard.                                                                                                                                                                                                                                                                                                                                                    |
| conventional | Subject classification incl. `!` marker, malformed headers, scope rules.                                                                                                                                                                                                                                                                                                                                      |
| graph        | Ordering constraints, alphabetical determinism, cycle errors, unknown nodes.                                                                                                                                                                                                                                                                                                                                  |
| config       | Defaults, JSON+YAML loading, flag precedence, optional scripts, changelog/github objects, initials parsing and validation, discovery errors.                                                                                                                                                                                                                                                                  |
| plan         | Own bumps, propagation, single-patch rule, breaking changes, first releases, initials baselines (untagged and unparseable-tag cases), catch-up of stale and never-released consumers, cycles.                                                                                                                                                                                                                 |
| release      | Ordering under both `isBuildWaitingPublish` values, version-task placement, failed/skipped provider filtering, publish-failure and build-failure cascades in both modes, skip cascades, per-stage budgets, script envs, script-less releases, recorder failures, revertOnFail at every stage.                                                                                                                 |
| changelog    | Section/entry rendering, custom formats, file prepend, custom file/title.                                                                                                                                                                                                                                                                                                                                     |
| github       | Request shape (path, auth, payload) via httptest, custom format, API and connection errors.                                                                                                                                                                                                                                                                                                                   |
| script       | Default and custom shells, env injection, failure propagation.                                                                                                                                                                                                                                                                                                                                                |
| gitx         | Against a real temporary git repo: tag round trips, unparseable-newest and backport tag resolution, scoped RevertDir, CommitDirs (incl. empty-stage no-op), VerifyRemote and pushes to a bare remote.                                                                                                                                                                                                         |
| cli          | End-to-end against a real temporary git repo: status/release commands, disabled changelog, revertOnFail restoration, initials with a broken tag, release commit + push to a bare remote (tag placement, templated message, clean worktree), fail-fast remote verification, three-run catch-up scenario (provider published, consumer failed, next run heals), github owner/repo/token resolution, exit codes. |
