# Architecture

`§N.N` references throughout this page point into the conventional-commits specification the parser implements,
[`pkg/ccme/SPEC.md`](https://github.com/yohimik/dispat/blob/main/pkg/ccme/SPEC.md).

## Runtime steps

Steps 1-2 are the command-line controller (`internal/cli`, behind the thin `main.go` of the dispat binary); everything
from discovery on is the `app` package's `Status` (steps 3-6) and `Release` (all of them), so the same operations are
callable without a command line.

`Release` brackets steps 3 onward with the [release lock](../cookbook/releasing/release-lock.md): an annotated `dispat-release-lock` tag
pushed, unforced, to `commit.remote` before discovery, and deleted from the remote and the clone on the way out, under
a context detached from cancellation so an interrupt still gives it back. A rejected push means a release is already
running against this repository, and the run stops there with exit `1`, before it has read a single tag.
`unsafeDisableLock: true` in the config, or `DISPAT_UNSAFE_DISABLE_LOCK=true` in the environment, skips the whole
bracket; either is enough. The name is reserved in `internal/gitx`, so no tag format, however broad, can read it back
as a release tag.

1. Parse the command line (pflag); dispatch `release`, `status`, `run <script>`, `init` or `preview`. An unknown
   command word is `run`'s shorthand (`dispat lint`).
   `init` writes a starter config and exits before anything else (there is no config to load yet), refusing a `--root`
   that is not a git repository root. The run command computes the plan, then runs the script inside each changed
   package that has one (looked up in the package's `scripts`, then its space's, then the file's) over the dependency
   graph (build concurrency budget; `--on-error` decides whether a failure skips the failed package's dependents) and
   stops. Which packages that covers is decided in three steps by the shared `internal/filter` resolver. First a
   window: the release window, or what `--since <rev>` addresses (scopes first, changed files for scopeless units per
   §6.2), or every package for `--since all`. Then the `--package` / `--space` / `--group` terms narrow it, with the
   invocation folder standing in for the terms nobody typed. Finally `--consumers` expands the result downstream. Nothing below step 6
   applies to it. `preview` computes the plan quietly (diagnostics, no graph), prints the pending release notes (every
   pending package's in publish order, under the same filter), and stops. `scanner` and `writer` also
   answer before any config is loaded, for the same reason as `init`. They are the `pkg/scanner` and `pkg/writer`
   libraries exposed directly (see [Manifest tools](../cookbook/editing/manifests.md)), reading nothing but the paths named on
   the command line. A monorepo root, a plan and a git history are all beside the point.
2. Resolve the config file (in `--root`, or ascending its parent directories, the config's own directory becoming the
   effective monorepo root; a folder's `.dispatexclude` chooses between the candidate names). A file declaring `spaces`
   ends the ascent. One declaring only `packages` is a candidate that yields to a root above claiming its folder as a
   space, which is how a space folder's file is told from a monorepo of standalone packages. A file declaring neither,
   meaning a package's in-folder override, does not end the ascent. Then load and validate it (viper; unknown keys rejected;
   flag bindings applied).
3. Discover packages: every direct sub-folder of each space path not excluded by the space's `.dispatexclude`, names
   unique across spaces, plus every standalone `packages` entry with a `path`. Per-package configuration resolves here,
   through the seven-layer ladder: the root file's own defaults, the space, the space folder's config file, then the
   top-level `packages` entry, the space's `packages` entry, the space file's `packages` entry and the package folder's
   own file. Each configured package gets a derived space value carrying the merged configuration, a standalone package
   becoming a single-package space of its own. Everything downstream then reads per-package behaviour without knowing
   the layers exist. Dependency declarations are collected from
   every source (the root object, the entries, the in-folder files) into one merged list.
4. Build the dependency graph from the merged relations; topologically sort it (cycles abort with the members named).
5. Plan (see below): resolve baselines, compute pending windows, parse the union of them, apply cancellation and holds,
   compute direct bumps, run the three propagation phases, then versions. Every versioning group is versioned as one,
   whether it is a space with its own shared mode or a declared `versionGroups` entry its members joined (see
   "Versioning groups" below).
6. Print the diagnostics, then narrow the plan to the invocation's `--package` / `--space` / `--group` selection.
   `plan.Narrow` does three things: every unselected releasing package is deselected, a selected one whose provider is
   releasing and unselected is withheld for the next run as `W230`, and a versioning group the selection splits is
   reported as `W231`. Then print the full graph with `old -> new` versions and channel transitions, in publish order,
   the deselected packages marked as such.
   `--strict` refuses a selection carrying either finding, after the graph and before any release work. `status` stops
   here.
7. Refuse to release when the plan has a repository-scoped error, or any error at all under `commitErrors: "error"`.
8. When `commit.push` or GitHub releases are enabled: verify remote/API access up front (`git ls-remote`,
   `GET /repos/{owner}/{repo}`) and fail fast before any release work. `commit.verify: false` skips the git check.
9. Run the gating run-level `beforeAll` hook; its failure aborts the run before any release work.
10. Execute the task graph (version/build/publish per *releasing* package; held packages are excluded) with per-stage
    concurrency budgets. Each stage is bracketed by the space's gating hooks (`beforeAll`,
    `beforeVersion`/`postVersion`, `beforeBuild`/`postBuild`, `beforePublish`), and a space's `flow.login` runs once
    before its first publish, every other publish of the space waiting on it.
11. After each successful publish, three things run in order. First the release recorders: the changelog file, and the
    GitHub release unless in release-commit mode. Then the annotated tag, which is deferred in release-commit mode, and
    which a `PACKAGE_<KEY>` script export pins to the exported commit instead of HEAD. Then the warn-only `postPublish`
    hook and the warn-only announce frame (`beforeAnnounce`, the announce stage, `postAnnounce`). The publish having succeeded, none of this can fail the
    package any more: a failure here is a [critical](#after-the-point-of-no-return).
12. Run the warn-only `postAll` hook with the run outcome (`DISPAT_RESULT_*`).
13. Finalize phase (when `commit` is enabled): one release commit staging all published packages, then tags on that
    commit, or on a package's exported `PACKAGE_<KEY>` commit. When `commit.push` is enabled the push follows, branch
    first, then the run's tags, with any tag already on the remote skipped (warned, not fatal). GitHub releases
    referencing the pushed tags come last. The warn-only commit/push hooks bracket these operations (`beforeCommit`/`afterCommit`,
    `postCommit` after tags,
    `beforePush`/`afterPush`). Every package here has published, so nothing in this phase aborts it either: each step
    runs, and each failure is a [critical](#after-the-point-of-no-return).
14. Print a per-package summary plus totals; exit `1` if anything failed or if any critical was recorded.

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

**Versioning groups.** Packages with shared versioning are grouped by their resolved group key: the space's own name
for a space with its own mode, or the declared `versionGroups` entry the space or package joined, so a group may span
spaces. A mode carries one number, its *shared depth*: how many leading version components the group holds equal, 3 for
`fixed`, 2 for `fixedMajorMinor`, 1 for `fixedMajor`, 0 for `independent`. The group's depth is the deepest any member
declares, since sharing more implies sharing the prefix, and a disagreement is reported as `W213`.

Each group is versioned as one virtual package, after the per-package pipeline has produced bumps, channels and pins.
The group aggregates what the version computation reads: the baselines of *every* member (held ones included, so the
shared version can never fall below a position a member already published) and the bumps, new work and channel
movements of the members that would release. It runs that through the ordinary §13.9 computation, pins, trains and
guards included: one shared next version, one prerelease train, one pin per group (the newest member pin wins; its
scope breadth is one by construction, since the group holds one shared prefix).

**The engagement rule** is what makes a partial depth work, and it is a single predicate evaluated after that
computation. The group takes its members over in two cases only: when the computed version leaves the group's prefix
behind, or when the group already sits on a prerelease train whose prefix left the stable line. In that second case the
train's later prereleases and its graduation belong to the whole group, even though neither moves the prefix again. At the full depth the predicate is
constantly true, so `fixed` and `fixedSparse` run the path they always did. A pin is admitted to the group computation
under the same rule: one naming the prefix the group already carries is left to its own package's `applyPin`, so a
member's local `Release-As` is never measured against the group's aggregate.

When the group does not engage, every member goes through the ordinary per-member `versionOne`, and an alignment pass
then keeps the invariant. A member releasing below the group's prefix adopts it, which is how a sparse member's first
change lands it on the shared part. And a non-sparse member with nothing pending whose baseline lags is released at the
prefix with `W210`.

Assignment is where sparseness shows, and the mode is each member's own, so a joined group can mix them. A plain mode
releases every non-held member at the group version, marking members with no cause of their own as rides: `W210`,
non-suppressible, with a "no changes" changelog entry naming the shared part. A sparse mode assigns the group version
only to members with a cause of their own. Scope resolution is untouched either way, so each member keeps its *own*
units for changelog and release notes.

Two convergence properties are preserved. A quiet group whose members agree on the prefix releases nothing. And a
non-sparse member left behind, by a failed ride or a mid-life adoption, is re-released on the next run: at exactly the
group's published version under `fixed`, and at the start of its own line under a partial mode.

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
| `internal/app`       | The application layer: `Status`, `Release`, `RunScript`, `AutoWriter`, `AutoSubstitute`, `Preview`, `Compute`, `ScanManifests`, `WriteManifests`, the step commands, the finalize phase and run-level hooks; wires every other package together. Its package *sweep* is the shared half of every command that covers a set of packages: the selection, the task graph, the concurrency budget and the skip cascade live there once, and each command supplies only what one package's work means. |
| `internal/config`    | Config resolution, loading, validation, package discovery, the space and per-package override merging, `.dispatexclude` over folder and config names, format-preserving config editing for `compute --write` (every key one run touches in a file written in a single pass, so one backup holds the file as it was). |
| `internal/plan`      | The planner: windows, scopes, directives, propagation, channels, versioning groups; a pure function of history, graph and configuration. Plus `Narrow`, which restricts a computed plan to part of the graph for a filtered release (publish order withholds, versioning-group splits reported).                                                                     |
| `internal/graph`     | Deterministic topological sort and the generic `Scheduler`/`Drain` pump described below.                                                                                                                     |
| `internal/release`   | The executor: the task graph, stage frames, hooks, login gates, native auto-versioning, `DISPAT_*` environment rendering, script outputs. Plus the release lock, the one-tag mutex a release takes on the remote before it plans.                                                                    |
| `internal/changelog` | Changelog rendering and the per-package record dispatcher.                                                                                                                                                   |
| `internal/github`    | The GitHub release recorder: REST calls, asset uploads, up-front verification, and the already-published probe that makes recording repeatable.                                                              |
| `internal/gitx`      | Git behind an interface: tags, baselines, commits, ancestry, tag formats; the CLI implementation shells out to `git`.                                                                                        |
| `internal/script`    | Shell script execution with process-group cancellation and bounded pipe waits.                                                                                                                               |
| `internal/model`     | Resolved domain types (`Space`, `Package`, `AutoVersion`, record specs) shared by config, plan and release.                                                                                                  |
| `internal/filter`    | The one selection resolver every package-selecting command shares, `release` and `status` included: `--package` / `--space` / `--group` terms, their globs, and the invocation folder that stands in for the first two.                              |
| `internal/globx`     | The one glob matcher scope terms, `autoVersion.match`, `.dispatexclude` and the selection terms share.                                                                                                        |
| `internal/ignore`    | The change-scope ignore rules: `ignore` patterns and `.dispatignore` files compiled into one chain per package (repository, space, package), asked once per changed file after ownership is decided. Built on `globx`, so there is still one glob dialect.                                                    |

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

Each releasing package contributes up to four task nodes. `version` exists when any provider of the package moved in
this run **or** its space auto-versions (§9.4 reconciles against every workspace dependency, so the stage cannot be
conditional on this run's updates). "Moved" is `Release.Updates`, which is deliberately wider than `DueTo`: a provider
releasing beside its consumer with no propagation between them still hands it a version to pick up, and gating on
`DueTo` made a scripted version stage and a native `autoVersion` block disagree about the same run. `syncLock` exists
when an autoVersion space configures the scripts. `build` and
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

Two things drain: the release executor, over `(package, stage)` nodes and the budgets above, and the *package sweep* in
`internal/app`, over package names and one budget. The sweep is what every command covering a set of packages runs on:
`run`, `autowriter`, `autosubstitute` and the four step commands. It decides who runs, in what order, under which
budget, and what a failure does to the dependents. A command supplies only a `packageWork`: given one package's release, hand back
the work to do, or nothing when there is none. `commit` and `github` declare themselves serial, since a repository has
one index and one HEAD; the rest ride the build budget. Adding a command that covers packages is therefore one small
file, not a copy of the scheduling.

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

**Once the release work starts, no error aborts the run.** Everything that can refuse a release happens before any of
it: the [release lock](../cookbook/releasing/release-lock.md), a blocked plan, the branch guard, the behind-remote check, the remote and
GitHub verification, and the `beforeAll` hook. Those refuse while nothing has happened yet, which is the only moment refusing costs nothing. From the first
build script onward the run always goes to the end: a package can fail, and its consumers can be skipped behind it, but
every other package still releases and the finalize phase still records whatever published. The only thing that stops a
run early is an interrupt, and even then what already published is still recorded.

Inside that, there is a second and stronger line: **once a package's publish succeeds, nothing can fail that package**.
See [After the point of no return](#after-the-point-of-no-return) below.

A failed script marks the package failed; nothing aborts the run. `Result.FailedStage` records
where it failed (informational, shown in the summary). At the start of every task the package re-evaluates the skip
rule: skip if some changed provider failed (at any stage) or was skipped AND the package has neither own commits nor a
successfully published changed provider. A consumer's terminal outcome is deterministic in both modes: its publish
always waits for its providers' publishes, so a provider's publish failure is guaranteed to be seen at the latest there.
With `isBuildWaitingPublish: true` provider outcomes are already final before the consumer's version stage; with `false`
the consumer may spend a version/build on a release that its publish then skips, the trade-off that flag opts into. The
version stage filters failed/skipped providers out of the `DISPAT_UPDATED_*` variables and skips its script entirely
when it had providers to pick up and none of them survive.

Failed or skipped consumers are not lost across runs, and nothing about that depends on tag creation times; those are
not stable under merges, rebases or equal timestamps, and are used for reporting only. A commit propagates to a
dependant while the *dependant's* window still contains it, so a consumer that missed a run is simply still owed its
release (`DueTo` then contains an *unchanged* provider: it gets no task nodes, and the version stage passes its released
version — the case that makes `Updates` a union of `DueTo` and this run's releasing providers rather than either
alone). A consumer that was never released while a provider has been is the same case, with the whole history as its
window. Such a release is labelled a catch-up and reported with the origin's published version.

Because admission is a window test, the guarantees are structural rather than heuristic. A contribution survives every
run until the dependant releases past it, and is not re-admitted afterwards. The version a package is caught up at is
the one it was planned at originally. And a later run's target set is always a subset of the first run's, so a failed
publish can never widen a commit's blast radius.

A skipped package is recorded as *blocked* with the dependency responsible, rather than being silently absent from the
summary: a package that was in the plan and produced nothing has to be accounted for.

For spaces with `revertOnFail: true`, a failing package has its folder
rolled back via the `Reverter` interface (`gitx.CLI`: `git checkout -- <dir>` + `git clean -fd <dir>`, so tracked files
are restored from HEAD and untracked files removed, scoped to the package folder). The same rollback runs when a package
is skipped after its version stage already modified files. A revert error is logged but the package keeps its original
failure status. Nothing is ever reverted after a successful publish, for the reason the next section gives.

### After the point of no return

A package's publish script succeeding is the point of no return. The artefact is on its registry and no later step can
take it back, so from there dispat has nothing left to decide and only things left to record: the tag, the changelog
entry, the GitHub release, the release commit, the push.

When one of those fails it is a **critical**: logged with its own code, counted in the summary, and otherwise ignored
by the run, which carries on to everything else it owed. The command still exits `1` at the end, once, so a release
that lost part of its record never looks green in CI.

| Code | What failed |
|-------|--------------------------------------------------|
| `E210` | A release tag could not be created |
| `E211` | A release tag already exists at a different commit |
| `E212` | A release record (changelog entry, GitHub release) could not be written |
| `E213` | The release commit could not be made |
| `E214` | The push failed |

The reason none of these fails the package is that failing it would be a lie with consequences. The package *is*
published, so marking it failed would do three wrong things. It would revert the package folder, throwing away the
version bump of something already on a registry. It would run the `onFail` script. And it would skip every consumer,
none of which has anything wrong with it, and all of which have a real published provider to build against. None of
that un-publishes anything. So the status
stays `published`, the summary line for that package turns into an error carrying what went missing, and the operator
gets told exactly what to go and repair.

For the same reason nothing stops at the first failure. A changelog that could not be written must not cost the tag
too; one package's tag failing says nothing about the next package's; a failed push must not withhold the GitHub
releases that document the same work. Each step runs, each failure is recorded, and the exit code adds them up.

`E211` is the one case that also declines to act. A tag already sitting at another commit is left exactly where it is
rather than moved onto this release, because it is a record some earlier run made. With
[force](../configuration/records.md#force) on, a tag moved here would be carried over the copy on the remote, turning
one local mistake into everyone's.

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
destination, a file prepend vs. a REST call. Recorders run in order after each successful publish; a recorder error is
a critical (`E212`) and the remaining recorders and the tag still follow.

Other choices:

- Versions in git tags only, so there are no version files to commit.
- One `git tag`/`git log` pair per package rather than a global log walk, which is simple and correct for per-package
  tag baselines.
- Shelling out to the git binary, to match CI byte-for-byte.
- Script output streamed line-by-line into the structured logger, so parallel package logs stay attributable.
- Deterministic ordering everywhere: alphabetical tie-breaks in the toposort, sorted space iteration in discovery.

Two diagnostics are deliberately not suppressible, because each explains a release outcome a reader of the commit log
alone cannot account for: a **catch-up** and a **channel-only** release explain a package's *presence* in a plan, and a
**blocked** package and a **suppressed propagation** explain an *absence*.

Complexity: discovery O (packages), planning O (V+E) graph work per propagation phase plus exactly one `git tag` and one
bounded `git log` per package, execution O (V+E) scheduling overhead on top of script runtime.

Two places where that bound is easy to lose, and both are guarded by construction rather than by care. A package needs
two baselines, and asking for them separately runs the same tag query twice, so the listing is the primitive and both
are selections over it. And the propagation phase split costs a second pass over the *unit list* and a second set of
graph traversals, but not a second pass over history. Commit walking, window computation, parsing and scope resolution
all happen once, before either phase runs, and the expensive part of a run is going to the object store. In a repository
running no prerelease train no unit carries a channel directive at all and the channel phase does nothing.

## Deliberately out of scope

dispat implements the release computation. Some parts of the specification assume an engine that also reads package
manifests and knows about registries; those are delegated to the version and publish scripts instead, which is what
keeps dispat language-agnostic:

| Not implemented                                             | Delegated to                                                                                                                                                                          |
|-------------------------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| Rewriting dependency ranges in manifests                    | native for `package.json`/`go.mod` under [`autoVersion`](../configuration/spaces.md#autoversion) (W197/W203); other ecosystems: `flow.version`, via the `DISPAT_WORKSPACE_*` variables |
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

In the other direction, eighteen codes are dispat's own, outside the specification's registry, attached to features the
specification predates or does not have. They are numbered from `W210` and `E210` upward, clear of the registry (which
ends at `W208` and `E200`) and of `W195`/`W196`, which the specification reserves for the audit features above.

| Codes | Feature |
|------------------------|--------------------------------------------------|
| `W210`-`W213`          | [Versioning groups](../cookbook/releasing/versioning.md): a ride, and the three conflicts a shared version can produce |
| `W220`-`W222`, `W225` | Manifest-derived: an ambiguous manifest name, a rewritten dependency with no configured edge, a replace rule that matched nothing, one package's manifests declaring different versions for it |
| `W223`, `W224`, `W226` | A record that is already there: a release tag, a GitHub release, a changelog entry. What makes the [step commands](../cookbook/releasing/steps.md) re-runnable |
| `W230`, `W231`         | [Releasing part of the graph](../cookbook/releasing/partial-releases.md): a package the publish order cannot reach yet, a selection splitting a versioning group |
| `W232`                 | An [alias tag](../configuration/alias-tags.md) that could not be written |
| `W233`                 | A [versioning group](../cookbook/releasing/versioning.md) whose members sit on different major versions, so the newest one decides where they all land |
| `E210`-`E214`          | [After the point of no return](#after-the-point-of-no-return): a tag, a record, the release commit or the push failing once a release is already out |

All of them follow the registry's numbering conventions and blast-radius rules, and are documented where their features
are. `W192`, `W197` and `W203`, the auto-versioning narrations, are the specification's own §9.4/§12.4 codes.

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
