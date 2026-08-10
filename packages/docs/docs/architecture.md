# Architecture

`§N.N` references throughout this page point into the conventional-commits specification the parser implements,
[`pkg/ccme/SPEC.md`](https://github.com/yohimik/dispat/blob/main/pkg/ccme/SPEC.md).

## Runtime steps

Steps 1-2 are the command-line controller (`internal/cli`, behind the thin `main.go` of the dispat binary); everything
from discovery on is the `app` package's `Status` (steps 3-6) and `Release` (all of them), so the same operations are
callable without a command line.

1. Parse the command line (pflag); dispatch `release`, `status`, `run <script> [package]`, `init` or
   `preview [package]`. An unknown command word is `run`'s shorthand (`dispat lint`).
   `init` writes a starter config and exits before anything else (there is no config to load yet), refusing a `--root`
   that is not a git repository root. The run command computes the plan, then runs the script inside each changed
   package that has one (looked up in the package's `scripts`, then its space's, then the file's) over the dependency
   graph (build concurrency budget; `--on-error` decides whether a failure skips the failed package's dependents) and
   stops; with an explicit `[package]` (or, for the shorthand, when invoked from inside a package's folder) the run
   narrows to that one package, changed or not, with no graph; `--since <rev>` (or `all`) instead selects what the
   commits since a revision address: scopes first, changed files for scopeless units (§6.2). Nothing below step 6
   applies to it. `preview` computes the plan quietly (diagnostics, no graph), prints the pending release notes (one
   package's, or every pending package's in publish order when none is named), and stops. `scanner` and `writer` also
   answer before any config is loaded, and for the same reason as `init`: they are the `pkg/scanner` and `pkg/writer`
   libraries exposed directly (see [Manifest tools](./manifests.md)), reading nothing but the paths named on the
   command line, so a monorepo root, a plan and a git history are all beside the point.
2. Resolve the config file (in `--root`, or ascending its parent directories, the config's own directory becoming the
   effective monorepo root; a file without `spaces` or `packages` (a package's in-folder override) does not end the
   ascent), then load and validate it (viper; unknown keys rejected; flag bindings applied).
3. Discover packages: every direct sub-folder of each space path not excluded by the space's `.dispatignore`, names
   unique across spaces, plus every standalone `packages` entry with a `path`. Per-package configuration resolves here
   (the top-level `packages` entry, then the folder's own dispat config file), each configured package getting a derived
   space value with the merged configuration (a standalone package a single-package space of its own), so everything
   downstream reads per-package behaviour without knowing the layers exist. Dependency declarations are collected from
   every source (the root list, the entries, the in-folder files) into one merged list.
4. Build the dependency graph from the merged relations; topologically sort it (cycles abort with the members named).
5. Plan (see below): resolve baselines, compute pending windows, parse the union of them, apply cancellation and holds,
   compute direct bumps, run the three propagation phases, then versions, with every versioning group (a
   `fixed`/`fixedSparse` space, or a declared `versionGroups` entry its members joined) versioned as one (see "Fixed
   versioning groups" below).
6. Print the diagnostics, then the full graph with `old -> new` versions and channel transitions, in publish order.
   `status` stops here.
7. Refuse to release when the plan has a repository-scoped error, or any error at all under `commitErrors: "error"`.
8. When `commit.push` or GitHub releases are enabled: verify remote/API access up front (`git ls-remote`,
   `GET /repos/{owner}/{repo}`) and fail fast before any release work. `commit.verify: false` skips the git check.
9. Run the gating run-level `beforeAll` hook; its failure aborts the run before any release work.
10. Execute the task graph (version/build/publish per *releasing* package; held packages are excluded) with per-stage
    concurrency budgets. Each stage is bracketed by the space's gating hooks (`beforeAll`,
    `beforeVersion`/`postVersion`, `beforeBuild`/`postBuild`, `beforePublish`), and a space's `flow.login` runs once
    before its first publish, every other publish of the space waiting on it.
11. After each successful publish: run the release recorders (changelog file; GitHub release unless in release-commit
    mode), then create the annotated tag (deferred in release-commit mode; a `PACKAGE_<KEY>` script export pins it to
    the exported commit instead of HEAD), then the warn-only `postPublish` hook and the warn-only announce frame
    (`beforeAnnounce`, the announce stage, `postAnnounce`).
12. Run the warn-only `postAll` hook with the run outcome (`DISPAT_RESULT_*`).
13. Finalize phase (when `commit` is enabled): one release commit staging all published packages, tags on that commit
    (or on a package's exported `PACKAGE_<KEY>` commit), then the push when `commit.push` is enabled: the branch first,
    then the run's tags with any tag already existing on the remote skipped (warned, not fatal), then GitHub releases
    referencing the pushed tags. The warn-only commit/push hooks bracket these operations (`beforeCommit`/`afterCommit`,
    `postCommit` after tags,
    `beforePush`/`afterPush`).
14. Print a per-package summary plus totals; exit `1` if anything failed.

## Planning

The planner is a pure function of (history, graph, configuration). It never consults wall-clock time or the outcome of
any previous run, which is what makes re-running after a partial publish deterministic. The one date-derived input is
git's newest-first tag ordering (which tag counts as a package's latest), and that is itself a fixed property of the
repository, the same on every re-run.

**Windows.** Every question of the form "does this commit still count?" is answered against a *pending window*: the
commits from a package's last **stable** tag to `HEAD`. Which package's window is consulted depends on the purpose, and
conflating the two is the bug that loses releases:

| Question                              | Window consulted |
|---------------------------------------|------------------|
| Does this unit bump `P` itself?       | `P`'s            |
| Does this unit set `P`'s own channel? | `P`'s            |
| Does this unit bump dependant `D`?    | **`D`'s**        |
| Does this unit re-channel `D`?        | **`D`'s**        |

Reading the last two against the *source's* window silently orphans consumers after a partial publish: the commit leaves
the provider's window when it releases, the unit loses its source packages, and the consumer is never released on this
or any future run. Reading them against the target's window is all that catch-up is; there is no repair pass, no second
traversal and no timestamp comparison anywhere in the package.

On a prerelease train the window deliberately spans commits the train's prereleases already published, which is what
lets §11.4 recompute the train's target (and a graduation's version) over the whole train. But published work is
published: a commit contained in the *baseline* tag (the newest tag of any kind) still counts toward the bump, and is
discharged for every question of the form "is this still pending?". It cannot re-release the train (otherwise every run
would emit `beta.1`, `beta.2`, ... from the same content), a `Release-As` it carries is consumed, and a `cancel`
cannot discard it (a prerelease tag is a published tag). The discharge extends to diagnostics: a contained channel
directive is not re-warned `W199` (it worked; the tag records it), contained propagated proposals are not re-judged
(`W200`/`W208`), and a cancel discharged for every package it names is spent, not misaimed, so `W170` stays for live
cancels only.

**Propagation runs in three phases, and they may not be merged**, because phase 3 reads what phase 2 produces:

1. **Channel axis**: each unit's `Propagate-Channel*` directives propose a channel per package, admitted against each
   target's window.
2. **Channel resolution**: settle `channel(P)` for every package. A direct directive beats every propagated one
   regardless of age; among equals the newest commit wins, then the last unit in it. Candidates are built by pushing
   from units to the packages they resolve to, rather than rescanning the window once per package.
3. **Bump axis**: each unit's `Propagate*` directives, admitted only where a source releases on a channel the target can
   resolve (`stable`, or the target's own). A stable consumer is not bumped by a provider's beta release: it would be a
   republish with no content, and a stable package would ship declaring a prerelease dependency.

There is no circularity in the other direction: phase 1 reads only the units and the packages' *baselines*, never a
value computed in this run. That is also what makes the channel axis converge: having arrived on `beta`, a package is
already there, so nothing further is proposed even though the commit is still in its window.

The traversal is shared by both axes: breadth-first from the unit's source packages, single-visit, shortest-path depth,
measured from the originating source set and never re-based on an intermediate. A package republishing as a catch-up
does not propagate onward, so a failed publish can never enlarge what a commit releases.

**Fixed versioning groups.** Packages with shared versioning are grouped by their resolved group key: the space's own
name for a `fixed`/`fixedSparse` space, or the declared `versionGroups` entry the space or package joined, so a group
may span spaces. Each group is versioned as one virtual package, after the per-package pipeline has produced bumps,
channels and pins. The group aggregates what the version computation reads: the baselines of *every* member (held ones
included, so the shared version can never fall below a position a member already published) and the bumps, new work and
channel movements of the members that would release. It runs that through the ordinary §13.9 computation, pins, trains
and guards included: one shared next version, one prerelease train, one pin per group (the newest member pin wins; its
scope breadth is one by construction, since the group is one version). Assignment is where the modes differ, and the
mode is each member's own (a joined group can mix them): `fixed` releases every non-held member at the group version,
marking members with no cause of their own as rides (`W210`, non-suppressible, with a "no changes"
changelog entry); `fixedSparse` assigns the group version only to members with a cause of their own. Scope resolution is
untouched: each member keeps its *own* units for changelog and release notes. Two convergence properties are preserved:
an aligned quiet group releases nothing, and a `fixed` member left behind the group's published baseline (a failed ride,
a mid-life adoption) is re-released at exactly that baseline on the next run.

## Module map

One line per package; each package's doc comment carries the full story, and the deeper design notes live in the
sections below.

| Package              | Role                                                                                                                                                                                                         |
|----------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `pkg/ccme`           | The commit-message parser: units, headers, directives, footers, scope terms, semver. Regex-free, single-pass, immutable; knows nothing of git or workspaces. Spec in [`SPEC.md`](https://github.com/yohimik/dispat/blob/main/pkg/ccme/SPEC.md). |
| `pkg/models`         | The public configuration model (own module): the structs a `dispat.json`/`.yaml` decodes into, so external tooling and the integration suite author configs as typed values.                                 |
| `pkg/manifest`       | Shared manifest vocabulary (own module): dependency kinds, the requirements-file name rule, PEP 503 normalisation; definitions the scanner and writer must apply identically.                                |
| `pkg/scanner`        | Deliberately lightweight manifest reader (own module): npm, Go, Cargo, Python, Composer, Maven, .NET, pub, RubyGems and the mobile formats parsed into one `Manifest` shape with declared names, versions, dependencies and local paths; bounded reads, partial results with joined errors. No lockfile resolution, no network. Exposed on its own as `dispat scanner`. |
| `pkg/writer`         | Format-preserving manifest writer (own module): byte-precise range and version rewrites for **every** manifest the scanner reads, atomic writes, and a result separating applied edits from ones deliberately left alone (a Maven property, a workspace inheritance). Exposed on its own as `dispat writer`. |
| `internal/cli`       | The command-line controller: flags, dispatch, exit-code mapping, logger construction.                                                                                                                        |
| `internal/app`       | The application layer: `Status`, `Release`, `RunScript`, `TestScript`, `Preview`, `Compute`, `ScanManifests`, `WriteManifests`, the finalize phase and run-level hooks; wires every other package together.  |
| `internal/config`    | Config resolution, loading, validation, package discovery, per-package override merging, `.dispatignore`, format-preserving config editing for `compute --write`.                                            |
| `internal/plan`      | The planner: windows, scopes, directives, propagation, channels, fixed groups; a pure function of history, graph and configuration.                                                                          |
| `internal/graph`     | Deterministic topological sort and the generic `Scheduler`/`Drain` pump described below.                                                                                                                     |
| `internal/release`   | The executor: the task graph, stage frames, hooks, login gates, native auto-versioning, `DISPAT_*` environment rendering, script outputs.                                                                    |
| `internal/changelog` | Changelog rendering and the per-package record dispatcher.                                                                                                                                                   |
| `internal/github`    | The GitHub release recorder: REST calls, asset uploads, up-front verification.                                                                                                                               |
| `internal/gitx`      | Git behind an interface: tags, baselines, commits, ancestry, tag formats; the CLI implementation shells out to `git`.                                                                                        |
| `internal/script`    | Shell script execution with process-group cancellation and bounded pipe waits.                                                                                                                               |
| `internal/model`     | Resolved domain types (`Space`, `Package`, `AutoVersion`, record specs) shared by config, plan and release.                                                                                                  |
| `internal/globx`     | The one glob matcher scope terms, `autoVersion.match` and `.dispatignore` share.                                                                                                                             |

## Graph algorithms

A handful of small graph algorithms and one graph-shaped structure carry the whole tool; this section is the map of what
runs where.

### Topological sort: the publish order

`internal/graph.TopoSort` is Kahn's algorithm over package names with one refinement: the zero-in-degree frontier is a
**min-heap**, so ties always break alphabetically and the same graph yields the same order on every machine (§17.2). The
heap makes the sort O ((V+E) log V), which with one bounded `git tag`/`git log` query pair per package is what keeps
planning cheap at monorepo scale.

```
    a     b        frontier = nodes with no pending providers: {a, b}
     \   / \       the heap pops alphabetically -> a, b, then c, d, then e
      c    d       every provider precedes every consumer
       \  /
        e          a cycle leaves nodes stranded with the frontier empty:
                   TopoSort returns a typed *CycleError naming them, which
                   the planner reports as E200 with the edges' manifest kinds
```

Cost: O ((V+E) log V) for the heap pops; run once per plan.

### The task graph: what a release actually schedules

Each releasing package contributes up to four task nodes. `version` exists when the package's bump is `DueTo` provider
updates **or** its space auto-versions (§9.4 reconciles against every workspace dependency, so the stage cannot be
conditional on this run's updates); `syncLock` exists when an autoVersion space configures the scripts. `build` and
`publish` always exist.

```
  core:   build ─────────────► publish
            │                    │ │
            │  (isBuildWaiting)──┘ │ (always)
            ▼                      ▼
  web:    version ─► syncLock ─► build ─► publish
```

(The two provider edges land on web's *first* task, its `version`, and on its `publish`; the bullets below are the
precise rule.)

- A consumer's **first** task (version when present, build otherwise) waits for each changed provider's `build`, and
  additionally for that provider's `publish` when the provider's space sets `isBuildWaitingPublish: true` (the Docker
  case: the consumer can only build after the base image is pushed).
- A consumer's `publish` **always** waits for its providers' publishes: publishing against an unpublished provider
  version would be broken regardless of the flag.
- `syncLock` sits between `version` and `build`, in a scheduling class of its own.

### Drain: the one scheduling pump

`graph.Drain` consumes a Scheduler (the incremental core of Kahn's algorithm) with bounded parallelism. One coordinating
goroutine owns all scheduler state; every ready node gets a goroutine of its own; completions come back over one channel
and cascade new ready nodes.

```
   ready queues, one per class          budgets
   build:    [web:version, ...]   ◄──   build/version share BuildConcurrency
   publish:  [core:publish]       ◄──   PublishConcurrency
   syncLock: [web:syncLock]       ◄──   min over the voting spaces (default 1)

   loop: launch while budgets allow ──► goroutine per node ──► done channel
         ▲                                                        │
         └── completion cascades newly-ready nodes ◄──────────────┘
```

Per-class queues mean a stalled class never blocks another class's budget. A node occupies as many of its class's slots
as its package's configured weight (the per-package `concurrency` override; clamped to `[1, budget]`, so a weight past
the budget simply runs alone). A node that does not fit waits at the head of its queue and nothing behind it overtakes
it; the head-of-line discipline is what makes a heavy node's wait finite instead of starvable. syncLock keeps the
ordinary cost of one: its budget exists to serialise lock-file writers, not to price packages. There are no locks in the
scheduling hot path; a mutex guards only the shared result map that skip decisions, provider filtering and the syncLock
skip read. Every task finishes exactly once, including no-op completions for packages already failed or skipped, which
is what lets the counting terminate without special cases. A cancelled context stops new launches (in-flight nodes are
awaited, the rest are reported cancelled), and a frontier that empties with nodes remaining is diagnosed as a cycle
instead of deadlocking.

### Propagation: bounded BFS, three phases

Propagation (§9.2) walks the dependency graph outward from each unit's source packages, along kind-filtered edges,
admitted against each **target's own** pending window. Every package is expanded at most once per phase, so the walk is
O (V+E) whatever the directive's depth says: `^` (one edge), `+N` and `^^`/`+*` (the closure) only change where the
expansion stops, never how often a node is visited.

```
   feat(core)^          feat(core)^^ / +*
   core ─► api          core ─► api ─► app        bumps merge by max();
     └──► cli             └──► cli                a package never re-propagates
        (depth 1:              (closure: api,     a bump it merely received
         api, cli)              cli, app)
```

The three phases (channel proposals, channel resolution, bump propagation) may not be merged: phase 3 reads what phase 2
settled. Phase 1 reads only units and baselines, which is what makes the channel axis converge.

### Ancestry: three sources, memoised

Cancellation (§10.4) and prerelease-train containment ask "is commit a an ancestor of commit b?". The answer comes from
the strongest available source: git itself (`merge-base --is-ancestor`), the commits' parent pointers (BFS), or plain
history order for linear fakes. Every (a, b) answer is memoised on the computation, because the same pair is asked from
several nested phases and each uncached git answer is a subprocess.

```
   c1 ── c2 ── c3 ── c4      ancestor(c2, c4)?  git first;
           \                 parent-pointer BFS when git has
            └─ c5            no answer (ErrNoAncestry); history
                             rank as the linear-case fallback
```

A real git failure aborts planning rather than silently degrading to the weaker fallbacks: a wrong ancestry answer
changes which releases get cancelled or contained.

## Failure semantics

A failed script (or release recorder) marks the package failed; nothing aborts the run. `Result.FailedStage` records
where it failed (informational, shown in the summary). At the start of every task the package re-evaluates the skip
rule: skip if some changed provider failed (at any stage) or was skipped AND the package has neither own commits nor a
successfully published changed provider. A consumer's terminal outcome is deterministic in both modes: its publish
always waits for its providers' publishes, so a provider's publish failure is guaranteed to be seen at the latest there.
With `isBuildWaitingPublish: true` provider outcomes are already final before the consumer's version stage; with `false`
the consumer may spend a version/build on a release that its publish then skips, the trade-off that flag opts into. The
version stage filters failed/skipped providers out of the `DISPAT_UPDATED_*` variables and skips its script entirely
when none remain.

Failed or skipped consumers are not lost across runs, and nothing about that depends on tag creation times; those are
not stable under merges, rebases or equal timestamps, and are used for reporting only. A commit propagates to a
dependant while the *dependant's* window still contains it, so a consumer that missed a run is simply still owed its
release (`DueTo` then contains an *unchanged* provider: it gets no task nodes, and the version stage passes its released
version). A consumer that was never released while a provider has been is the same case, with the whole history as its
window. Such a release is labelled a catch-up and reported with the origin's published version.

Because admission is a window test, the guarantees are structural rather than heuristic: a contribution survives every
run until the dependant releases past it and is not re-admitted afterwards; the version a package is caught up at is the
one it was planned at originally; and a later run's target set is always a subset of the first run's, so a failed
publish can never widen a commit's blast radius.

A skipped package is recorded as *blocked* with the dependency responsible, rather than being silently absent from the
summary: a package that was in the plan and produced nothing has to be accounted for.

For spaces with `revertOnFail: true`, a failing package (any stage, including a failing release recorder) has its folder
rolled back via the `Reverter` interface (`gitx.CLI`: `git checkout -- <dir>` + `git clean -fd <dir>`, so tracked files
are restored from HEAD and untracked files removed, scoped to the package folder). The same rollback runs when a package
is skipped after its version stage already modified files. A revert error is logged but the package keeps its original
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
destination, a file prepend vs. a REST call. Recorders run in order after each successful publish; a recorder error
fails the package before tagging.

Other choices: versions in git tags only (no version files to commit); one `git tag`/`git log` pair per package rather
than a global log walk (simple and correct for per-package tag baselines); shelling out to the git binary to match CI
byte-for-byte; script output streamed line-by-line into the structured logger so parallel package logs stay
attributable; deterministic ordering everywhere (alphabetical tie-breaks in the toposort, sorted space iteration in
discovery).

Two diagnostics are deliberately not suppressible, because each explains a release outcome a reader of the commit log
alone cannot account for: a **catch-up** and a **channel-only** release explain a package's *presence* in a plan, and a
**blocked** package and a **suppressed propagation** explain an *absence*.

Complexity: discovery O (packages), planning O (V+E) graph work per propagation phase plus exactly one `git tag` and one
bounded `git log` per package, execution O (V+E) scheduling overhead on top of script runtime.

Two places where that bound is easy to lose, and both are guarded by construction rather than by care. A package needs
two baselines, and asking for them separately runs the same tag query twice, so the listing is the primitive and both
are selections over it. And the propagation phase split costs a second pass over the *unit list* and a second set of
graph traversals, but not a second pass over history: commit walking, window computation, parsing and scope resolution
all happen once, before either phase runs, and the expensive part of a run is going to the object store. In a repository
running no prerelease train no unit carries a channel directive at all and the channel phase does nothing.

## Deliberately out of scope

dispat implements the release computation. Some parts of the specification assume an engine that also reads package
manifests and knows about registries; those are delegated to the version and publish scripts instead, which is what
keeps dispat language-agnostic:

| Not implemented                                             | Delegated to                                                                                                                                                                          |
|-------------------------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| Rewriting dependency ranges in manifests                    | native for `package.json`/`go.mod` under [`autoVersion`](./configuration/spaces.md#autoversion) (W197/W203); other ecosystems: `flow.version`, via the `DISPAT_WORKSPACE_*` variables |
| Manifest-vs-baseline version checks                         | native under `autoVersion` (W192); otherwise nothing                                                                                                                                  |
| Publish targets, registries, adopting published versions    | `flow.publish`                                                                                                                                                                        |
| `initialVersion` / `preserveMajorZero` remapping            | current behaviour: first release from `0.0.0`, ordinary bumps                                                                                                                         |
| Per-run safety limits (max packages, majors, channel moves) | (nothing; the exact-pin major-jump guard *is* enforced, with a default of 1)                                                                                                          |
| Post-run convergence verification                           | (nothing; re-run `status`)                                                                                                                                                            |

A consequence for the diagnostics registry: the specification codes that belong to these registry- and audit-aware
features are never emitted by dispat. `E197` (publish-order violation), `E198` (registry identity unverifiable) and
`E199` (convergence check failed) assume an engine that queries registries and audits its own runs; `W195` (staleness
audit) and `W196` (published version adopted from the registry) belong to the same features. Every other code of the
registry, the repository-scoped bucket included (`E182`, `E185`, `E191`, `E195`, `E196`, `E200`), is implemented and
emitted.

In the other direction, two codes are dispat's own, outside the specification's registry, attached to the
manifest-derived features the specification predates: `W220` (ambiguous manifest name) and `W221` (rewritten dependency
with no configured `dependencies` edge). They follow the registry's numbering conventions and blast-radius rules, and
are documented where their features are ([`compute`](./cli.md),
[`autoVersion`](./configuration/spaces.md#autoversion)). `W192`, `W197` and `W203`, the auto-versioning narrations, are
the specification's own §9.4/§12.4 codes.

## Testing

`go test ./...` runs testify-based unit tests with in-memory fakes; every internal package and every `pkg/` module has
its own suite. What each suite asserts, claim by claim, is catalogued in the
[integration test plan](https://github.com/yohimik/dispat/blob/main/tests/integration/docs/test-plan.md) and summarised per area in
[test results](https://github.com/yohimik/dispat/blob/main/tests/integration/docs/test-results.md); the statement-coverage snapshot per package is in
[coverage](./coverage.md).

The composition claims (the compiled binary, real git over a process boundary, exit codes, scheduling under real
concurrency, signal handling) live exclusively in the black-box
[integration suite](https://github.com/yohimik/dispat/blob/main/tests/integration/README.md), which builds the real binary and asserts on git state, JSON
log events and nanosecond execution timelines. `services/dispat` itself hosts no end-to-end tests; a test that could
only be satisfied by faking what the suite can witness for real belongs there instead.
