# Architecture

## Runtime steps

Steps 1-2 are the command-line controller (`internal/cli`, behind the thin `main.go` of the dispat binary); everything
from discovery on is the `app` package's `Status` (steps 3-6) and `Release` (all of them), so the same operations are
callable without a command line.

1. Parse the command line (pflag); dispatch `release`, `status`, `run <script> [package]`, `init`,
   `test <script> <package>` or `preview [package]`. An unknown command word is `run`'s shorthand (`dispat lint`).
   `init` writes a starter config and exits before anything else (there is no config to load yet), refusing a `--root`
   that is not a git repository root. The run command computes the plan, then executes the named space run script inside
   each changed package over the dependency graph (build concurrency budget; `--on-error` decides whether a failure
   skips the failed package's dependents) and stops; with an explicit `[package]` (or, for the shorthand, when invoked
   from inside a package's folder) the run narrows to that one package, changed or not, with no graph; `--since <rev>`
   (or `all`) instead selects what the commits since a revision address: scopes first, changed files for scopeless
   units (§6.2). Nothing below step 6 applies to it. `test` and `preview` compute the plan quietly (diagnostics, no
   graph), then run one top-level script in one package's folder with its full
   `DISPAT_*` environment / print the pending release notes (one package's, or every pending package's in publish
   order when none is named), and stop.
2. Resolve the config file (in `--root`, or ascending its parent directories, the config's own directory becoming the
   effective monorepo root; a file without `spaces` or `packages` — a package's in-folder override — does not end the
   ascent), then load and validate it (viper; unknown keys rejected; flag bindings applied).
3. Discover packages: every direct sub-folder of each space path not excluded by the space's `.dispatignore`, names
   unique across spaces, plus every standalone `packages` entry with a `path`. Per-package configuration resolves here
   — the top-level `packages` entry, then the folder's own dispat config file — each configured package getting a
   derived space value with the merged configuration (a standalone package a single-package space of its own), so
   everything downstream reads per-package behaviour without knowing the layers exist. Dependency declarations are
   collected from every source — the root list, the entries, the in-folder files — into one merged list.
4. Build the dependency graph from the merged relations; topologically sort it (cycles abort with the members named).
5. Plan (see below): resolve baselines, compute pending windows, parse the union of them, apply cancellation and holds,
   compute direct bumps, run the three propagation phases, then versions, with every versioning group — a
   `fixed`/`fixedSparse` space, or a declared `versionGroups` entry its members joined — versioned as one
   (see "Fixed versioning groups" below).
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

The planner is a pure function of (history, graph, configuration). It never consults wall-clock time, tag creation dates
or the outcome of any previous run, which is what makes re-running after a partial publish deterministic.

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

**Fixed versioning groups.** Packages with shared versioning are grouped by their resolved group key — the space's own
name for a `fixed`/`fixedSparse` space, or the declared `versionGroups` entry the space or package joined, so a group
may span spaces — and each group is versioned as one virtual package, after the per-package pipeline has produced
bumps, channels and pins. The group aggregates what the version computation reads: the baselines of *every* member
(held ones included, so the shared version can never fall below a position a member already published) and the bumps,
new work and channel movements of the members that would release. It runs that through the ordinary §13.9 computation,
pins, trains and guards included: one shared next version, one prerelease train, one pin per group (the newest member
pin wins; its scope breadth is one by construction, since the group is one version). Assignment is where the modes
differ, and the mode is each member's own (a joined group can mix them): `fixed` releases every non-held member at the
group version, marking members with no cause of their own as rides (`W210`, non-suppressible, with a "no changes"
changelog entry); `fixedSparse` assigns the group version only to members with a cause of their own. Scope resolution
is untouched: each member keeps its *own* units for changelog and release notes. Two convergence properties are
preserved: an aligned quiet group releases nothing, and a `fixed` member left behind the group's published baseline (a
failed ride, a mid-life adoption) is re-released at exactly that baseline on the next run.

## Module map

```
pkg/ccme                 the commit-message parser: units, headers, inline
                         directives, footers, scope terms, semver. Regex-free,
                         single pass, no backtracking; immutable and safe for
                         concurrent use. Knows nothing of git, workspaces or
                         versions; it parses messages.
pkg/models               the PUBLIC configuration model (its own module): the
                         structs a dispat.json/.yaml decodes into, with json
                         tags mirroring the mapstructure keys so external
                         tooling (and the black-box integration suite) can
                         author configs as typed values and marshal them to
                         loadable JSON. Models and pure helpers only;
                         loading, validation and discovery stay internal to
                         the CLI.
pkg/manifest             the SHARED manifest vocabulary (its own module):
                         dependency kinds spelled like the manifest fields,
                         the requirements-file name rule and PEP 503 name
                         normalisation: the definitions the scanner and the
                         writer must apply identically, held in one place so
                         the reading and writing halves cannot drift. Models
                         and pure functions only; no I/O, no dependencies.
pkg/scanner              manifest READER (its own module). Two manifest
                         shapes: structured documents (package.json, go.mod,
                         Cargo.toml, pyproject.toml (PEP 621 + Poetry, PEP
                         503 name normalisation), composer.json, pom.xml
                         (groupId:artifactId coordinates), *.csproj
                         (suffix-matched; ProjectReference = local path),
                         pubspec.yaml) and line-by-line "name specifier"
                         files, requirements*.txt (pattern-matched; a dev/
                         test file name maps to devDependencies). All parsed
                         into one ecosystem-neutral shape: declared identity
                         (name, version; line manifests have none) plus
                         declared dependencies with their ranges, fields and
                         local paths. Thin per-format parsers, a walking
                         Scan (skips node_modules/vendor/target/dot-folders)
                         and a root-only ScanRoot, both capped at 16 MiB per
                         file; NameIndex (manifest name -> owning package,
                         ambiguity reported, never guessed) and
                         ResolveLocalDir shared by compute and
                         auto-versioning. The walk skips dependency,
                         virtual-env and build-output folders. Feeds
                         `dispat compute` and auto-versioning. Code
                         DSL manifests (Gemfile, build.gradle, mix.exs,
                         Package.swift) are deliberately absent: parsing
                         code with regexes produces wrong graphs.
pkg/writer               manifest WRITER (its own module): format-preserving
                         in-place edits (package.json via byte-precise JSON
                         scalar replacement, go.mod via x/mod/modfile,
                         requirements*.txt via per-line specifier splicing)
                         replacing only the version text being changed.
                         Atomic same-folder rename; reports applied and
                         missing edits. Ecosystems without a writer are
                         read-only here; their reconciliation belongs to a
                         flow.version script. Shares pkg/manifest's kinds
                         and file-name rules with the scanner.
services/dispat/
  main.go                thin entry point: os.Exit(cli.Run(...))
  internal/
    cli      the command-line controller: pflag flags, command dispatch
             (release / status / run <script> / init / test / preview /
             compute, with an unknown word treated as a run script name),
             zerolog setup,
             config file resolution and loading; then delegates to app and
             maps its results onto exit codes
    app      the application: App with Status, Release, RunScript
             (the `dispat run` implementation: changed packages scheduled
             over the dependency graph within the build budget, --on-error
             skip/continue semantics, or one targeted package (named, or
             inferred from the invocation directory), TestScript, Preview,
             Compute (manifest scan -> derived edges -> suggestions diffed
             against the config, previewed / confirmed / applied with the
             config's dependencies array rewritten format-preservingly and
             the previous copy backed up) and the
             standalone InitConfig, holding recorder
             assembly (changelog + github), diagnostic reporting and the
             release-refusal policy, upfront remote/API verification,
             run-level hooks (gating beforeAll; warn-only postAll +
             commit/push hooks; all run in the repo root), finalize phase
             (release commit, tags, push, github releases), graph printout,
             summary; callable from any front end, not only the CLI
    config   viper loading (UnmarshalExact + weak typing for scalar-or-pair
             concurrency) over the public pkg/models structs (aliased, so the
             rest of the CLI imports only this package), config file
             resolution (the --config fallback names, in --root or ascending its
             parents, stopping only at a file that declares spaces), initials
             baseline versions, tag formats, commit-error policy,
             versioning-mode, versionGroups and runScripts validation, the
             parser-object resolution onto a ccme.Config (validated by
             constructing a parser at load time), package discovery on the
             filesystem (.dispatignore filtering; per-package override
             merging — packages entries and in-folder config files — each
             overridden package resolved onto a derived Space of its own)
    model    shared domain types: Space (incl. Versioning mode, VersionGroup
             + RunScripts), Package (incl. stage weights and the resolved
             changelog/github record policies), Dependency, DepKind
    globx    the one glob matcher scope terms, autoVersion range globs and
             .dispatignore patterns share: "*" matches any run of bytes,
             path separators included
    graph    the shared dependency machinery: Scheduler[N], the incremental
             core of Kahn's algorithm (nodes, edges, the ready frontier,
             done -> newly-ready), used by the executor to drive its task
             graph; and Graph, a validated string-node facade draining a
             Scheduler through a name-ordered min-heap: O((V+E) log V),
             deterministic output, cycle detection by leftover in-degree
    gitx     Git interface + CLI implementation shelling out to git: TagFormat
             (render/parse/glob, incl. {channel}/{counter} prerelease
             spelling), one tag listing per package with peeled
             target commits, from which Tags.Baseline and Tags.StableBaseline
             are selections rather than second queries; merge-base ancestry,
             commit log with
             SHAs, parents and first-parent file lists, tag -a, checkout +
             clean for revertOnFail rollbacks, add + commit for the release
             commit, ls-remote verification, remote tag listing, branch +
             tag push that skips tags already on the remote
    plan     the planner, split by concern:
               plan.go        windows, cancellation, holds, direct bumps,
                              versions, fixed versioning groups, emit,
                              diagnostics
               propagate.go   the three propagation phases and the shared
                              depth-bounded traversal
               channel.go     channel derivation, proposal resolution,
                              prerelease version computation, graduation
               scope.go       scope-set resolution: globs, exclusions, the
                              file-derived set by longest path prefix
               directives.go  the ccme -> plan adapters: unit accessors,
                              propagation knobs, version helpers
    release  the executor: task-graph construction (version/syncLock/build/
             publish nodes, dependency edges) over graph.Scheduler, with
             per-stage worker budgets (syncLock's own budget defaults to 1,
             serialising lock file regeneration), native auto-versioning at
             the version stage (autoversion.go: pkg/scanner + pkg/writer
             under the space's match/range/kinds/only policy, emitting
             W192/W197/W203), skip propagation, provider-update
             filtering, script sequences (fail-fast vs warn-only), per-stage
             hooks, the once-per-space login gate (sync.Once keyed by space),
             the warn-only announce frame after each publish, the warn-only
             onFail/onSkip outcome scripts, DISPAT_* script environments incl.
             the run-outcome and release-notes listings, the DISPAT_OUTPUT
             export file every per-package sequence (the login included)
             gets (parsed after the sequence, a failed one included; merged
             onto the Release, login exports space-wide at each publish; and
             carried into every later script's environment as DISPAT_OUTPUT_*
             with DISPAT_OUTPUT_SOURCE_* provenance, DISPAT_EXPORT_GITHUB
             passing through whole), line-buffered log streaming of script
             output
    script   Runner interface + ShellRunner (configurable shell, default
             sh -c; injected env, cwd = package)
    changelog entry rendering (Format with defaults) + FileWriter recorder that
             prepends entries to the per-package changelog file
    github   Releaser recorder: same changelog data delivered as a GitHub
             release via POST /repos/{owner}/{repo}/releases, only for
             packages that exported DISPAT_EXPORT_GITHUB, prereleases
             marked as such, the export's files uploaded as release assets
             through the created release's upload_url (invalid entries
             skipped with a warning)
```

Tag names are built in exactly one place, `plan.Release.TagName()`. They have to be: the name is what the *next* run
reads a package's baseline from, so a caller rendering one differently would silently give that package no history.

## Graph algorithms

A handful of small graph algorithms and one graph-shaped structure carry the whole tool; this section is the map of what runs where.

### Topological sort: the publish order

`internal/graph.TopoSort` is Kahn's algorithm over package names with one refinement: the zero-in-degree frontier is a
**min-heap**, so ties always break alphabetically and the same graph yields the same order on every machine (§17.2).

```
    a     b        frontier = nodes with no pending providers: {a, b}
     \   / \       the heap pops alphabetically -> a, b, then c, d, then e
      c    d       every provider precedes every consumer
       \  /
        e          a cycle leaves nodes stranded with the frontier empty:
                   TopoSort returns a typed *CycleError naming them, which
                   the planner reports as E200 with the edges' manifest kinds
```

Cost: O((V+E) log V) for the heap pops; run once per plan.

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

`graph.Drain` consumes a Scheduler (the incremental core of Kahn's algorithm) with bounded parallelism. One
coordinating goroutine owns all scheduler state; every ready node gets a goroutine of its own; completions come back
over one channel and cascade new ready nodes.

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
the budget simply runs alone). A node that does not fit waits at the head of its queue and nothing behind it overtakes —
the head-of-line discipline is what makes a heavy node's wait finite instead of starvable. syncLock keeps the ordinary
cost of one: its budget exists to serialise lock-file writers, not to price packages. There are no locks in the
scheduling hot path; a mutex guards only the shared result map that skip decisions, provider filtering and the syncLock
skip read.
Every task finishes exactly once, including no-op completions for packages already failed or skipped, which is what
lets the counting terminate without special cases. A cancelled context stops new launches (in-flight nodes are awaited,
the rest are reported cancelled), and a frontier that empties with nodes remaining is diagnosed as a cycle instead of
deadlocking.

### Propagation: bounded BFS, three phases

Propagation (§9.2) walks the dependency graph outward from each unit's source packages, along kind-filtered edges,
admitted against each **target's own** pending window. Every package is expanded at most once per phase, so the walk is
O(V+E) whatever the directive's depth says: `^` (one edge), `+N` and `^^`/`+*` (the closure) only change where the
expansion stops, never how often a node is visited.

```
   feat(core)^          feat(core)^^ / +*
   core ─► api          core ─► api ─► app        bumps merge by max();
     └──► cli             └──► cli                a package never re-propagates
        (depth 1:              (closure: api,     a bump it merely received
         api, cli)              cli, app)
```

The three phases (channel proposals, channel resolution, bump propagation) may not be merged: phase 3 reads what phase
2 settled. Phase 1 reads only units and baselines, which is what makes the channel axis converge.

### Ancestry: three sources, memoised

Cancellation (§10.4) and prerelease-train containment ask "is commit a an ancestor of commit b?". The answer comes from
the strongest available source: git itself (`merge-base --is-ancestor`), the commits' parent pointers (BFS), or plain
history order for linear fakes. Every (a, b) answer is memoised on the computation, because the same pair is asked
from several nested phases and each uncached git answer is a subprocess.

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

| Not implemented                                             | Delegated to                                                                 |
|-------------------------------------------------------------|------------------------------------------------------------------------------|
| Rewriting dependency ranges in manifests                    | native for `package.json`/`go.mod` under [`autoVersion`](./configuration/spaces.md#autoversion) (W197/W203); other ecosystems: `flow.version`, via the `DISPAT_WORKSPACE_*` variables |
| Manifest-vs-baseline version checks                         | native under `autoVersion` (W192); otherwise nothing                         |
| Publish targets, registries, adopting published versions    | `flow.publish`                                                               |
| `initialVersion` / `preserveMajorZero` remapping            | current behaviour: first release from `0.0.0`, ordinary bumps                |
| Per-run safety limits (max packages, majors, channel moves) | (nothing; the exact-pin major-jump guard *is* enforced, with a default of 1) |
| Post-run convergence verification                           | (nothing; re-run `status`)                                                   |

A consequence for the diagnostics registry: the specification codes that belong to these registry- and audit-aware
features are never emitted by dispat. `E197` (publish-order violation), `E198` (registry identity unverifiable) and
`E199` (convergence check failed) assume an engine that queries registries and audits its own runs; `W195` (staleness
audit) and `W196` (published version adopted from the registry) belong to the same features. Every other code of the
registry, the repository-scoped bucket included (`E182`, `E185`, `E191`, `E195`, `E196`, `E200`), is implemented and
emitted.

In the other direction, two codes are dispat's own, outside the specification's registry, attached to the
manifest-derived features the specification predates: `W220` (ambiguous manifest name) and `W221` (rewritten
dependency with no configured `dependencies` edge). They follow the registry's numbering conventions and blast-radius
rules, and are documented where their features are ([`compute`](./cli.md),
[`autoVersion`](./configuration/spaces.md#autoversion)). `W192`, `W197` and `W203`, the auto-versioning narrations,
are the specification's own §9.4/§12.4 codes.

## Testing

`go test ./...` runs testify-based unit tests with in-memory fakes:

| Package   | Coverage                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                             |
|-----------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| ccme      | Its own suite: header grammar, footers, units, diagnostics, semver, allocation and fuzz tests, plus the specification's conformance vectors.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                         |
| scanner   | Its own suite: per-ecosystem parsing (npm fields and local paths, go.mod requires/replaces with the lax fallback, Cargo tables/renames/workspace degradation, PEP 621/Poetry/groups, composer, Maven coordinates and scopes, csproj precedence, pubspec overrides folded onto declarations, requirements continuations/flags/editable installs), walk-and-skip behaviour, partial results on malformed manifests, context cancellation, NameIndex/ResolveLocalDir, plus two fuzz targets over every registered parser.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                       |
| writer    | Its own suite: byte-exact format preservation for all three formats (deliberately ugly fixtures compared byte-for-byte), no-op detection via mtime, JSON escaping and mode preservation, missing fields and composite values, the shared dispatch table, the Edit.Kind whitelist, Result.Path, ErrUnsupportedManifest, plus a fuzz target proving a rewrite either errors or leaves valid JSON.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                |
| graph     | Ordering constraints, alphabetical determinism, cycle errors, unknown nodes; Scheduler frontier/cascade semantics (diamond joins, one-time hand-out, blocked-as-cycle, generic node types); Drain's weighted slot accounting (budget never exceeded, weight-past-budget clamping and serialisation, sub-1 weights costing one, edges respected under weights).                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                          |
| globx     | The shared glob matcher's table: literals, `*` runs crossing separators, backtracking, empty patterns and inputs.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                    |
| config    | Defaults (incl. `commitErrors`, `nonPackageScopes`, `tagFormat`), model-authored JSON loading with per-format smoke tests (JSON/YAML/TOML), config file resolution (fallback order, explicit names, the not-found error), flag precedence, optional scripts, scalar-or-array script references, flow/login/run-hook resolution and their unknown- and empty-reference errors, changelog/github objects, initials parsing, per-space tag formats and their validation, versioning-mode normalization and rejection, runScripts validation and case-insensitive lookup, the parser object (defaults, every option mapped onto ccme.Config, invalid values rejected at load), discovery errors (and discovery carrying versioning + runScripts onto the Space), the autoVersion object (validation errors, the full resolution mapping incl. defaults and the inert disabled block), commit.include validation, versionGroups declarations and references (invalid modes, namespace collisions, unknown and independent-space references, the mutual exclusion with versioning), per-package overrides (the tri-state pointer scalars, entry-level flow merge incl. the empty-array clear, runScripts union, the wholesale autoVersion replacement, the forbidden flow.login and path keys, script-ref and concurrency validation, record-policy overlays, unmatched/ambiguous packages keys), in-folder config files (all four formats, precedence over the packages entry, unknown keys, the nested-root refusal), .dispatignore (patterns, comments, globs), the config-resolution ascent skipping override files, and the config editor (JSON byte-splice, YAML comment preservation, TOML refusal, backups, atomic no-op writes, override blocks surviving the dependencies splice).                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                         |
| plan      | Propagation is opt-in; caret/`^^`/`+N` depths; `^none`; `Propagate-Scope`; edge kinds; `max()` merging; scope resolution (derived, longest prefix, globs incl. the matcher's backtracking table, `*`, exclusions, unknown includes vs excludes); baselines and initials; catch-up (stale, never-released, discharge-once, no widening) and the staleness-audit duality; cancellation under all three ancestry sources (git-native, parent-pointer BFS, history rank); holds, resume and exact pins with every guard and the rejected-pin fallback; channels, trains, graduation, propagated-transition graduation, suppression and convergence; error blast radius; tag formats; the reporting accessors (Reason, counters, outputs, PossiblyBehind); fixed versioning groups (rides and W210, shared version over the max baseline, sparse members staying behind, the single shared train, space-wide pins and per-member holds, laggard alignment and its absence under fixedSparse, group isolation between two fixed spaces, propagation into a fixed space, declared groups spanning spaces, mixed-mode groups and their laggard semantics, group-named diagnostics, a package's clone opting out of its space's group).                                                                                                                                                                                                                                   |
| release   | Ordering under both `isBuildWaitingPublish` values, version-task placement, failed/skipped provider filtering, publish- and build-failure cascades, skip cascades and blocked reporting, held packages excluded, channel-only releases executed, per-stage budgets, script envs (incl. channel variables and workspace versions), the once-per-space login gate (single run, per-space not per-script, failure failing every space publish), hook ordering/environment/gating (incl. warn-only postPublish), the announce frame (order, warn-only failures, skipped on a failed publish), the onFail/onSkip outcome scripts (fired once with the failure/skip specifics, silent on success), the release-notes environment, fail-fast vs warn-only sequences, the run-outcome environment, script-less releases, recorder failures, revertOnFail at every stage, space tag formats, the DISPAT_OUTPUT exports (accumulation across scripts and hooks, both name spellings, re-export override incl. source, flow into onFail even from a failed sequence, empty listing, the NAME=value grammar with its reserved DISPAT_ names and gating-failure semantics, DISPAT_OUTPUT_SOURCE_* provenance, space-wide login exports incl. their gating parse failures, the DISPAT_EXPORT_GITHUB pass-through), and native auto-versioning (range/version rewriting against real manifests, every policy filter, failed-provider baseline fallback, rewrite-failure + revertOnFail, convergence across two runs, the W192/W197/W203/W221 diagnostics, syncLock placement, budgets, failure semantics and the skip-when-unchanged rule). |
| changelog | Section/entry rendering, custom formats, file prepend, custom file/title, the fixed-ride "no changes" entry (and content beating the placeholder), the prerelease notes windowing (a prerelease renders only its fresh changeset, a graduation the whole window), the per-package dispatcher (a disabled package records nothing; an enabled one writes through its own file, title and format).                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                    |
| github    | Request shape (path, auth, payload) via httptest, custom format, prerelease marking, the DISPAT_EXPORT_GITHUB gate (no export, no release, no API call), its asset uploads through the advertised upload_url (multi-file lists, names, content type, bytes, failure propagation, invalid entries skipped with a warning, empty creation bodies tolerated), API and connection errors.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                |
| script    | Default and custom shells, env injection, failure propagation.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                       |
| gitx      | Against a real temporary git repo: tag round trips, baseline vs stable baseline, peeled tag commits, ancestry, commit logs carrying SHAs/parents/multi-paragraph messages, tag format render/parse/glob and custom-format resolution, scoped RevertDir, CommitDirs (incl. empty-stage no-op), VerifyRemote, pushes to a bare remote and the push skipping tags that already exist on the remote.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                     |
| app       | Unit tests of its helpers: commit-message templating, github repository/token resolution and the per-target dispatch (one releaser per distinct resolved target, disabled and unresolvable packages recording nothing, the recorder assembly), the initials mapping (case-insensitive, unmatched keys warned), the --on-error validator, the init starter configs, the git-prerequisites guard (missing git binary / missing .git), and the compute command end to end against real mini-monorepos (detection incl. cross-ecosystem matching, the name-ambiguity drop, diffing with keep/kind/stale-endpoint semantics, preview/write/interactive/check modes, backups, the TOML snippet fallback, error paths). The composition claims (release flows, hooks, finalize, records) live in the black-box `tests/integration` suite.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                   |
| cli       | Unit tests of the controller itself: the --version flag, unknown flags printing usage, command-arity and --on-error usage errors (exit 2, before any config or git), the missing-config error, the init command (a loadable starter per format, never overwriting, only at a git repository root), the logger constructor's level fallback. Full command flows are covered black-box by `tests/integration`, which drives the compiled binary.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                       |

The planner's two formulations of staleness (walking down from a provider and up from a consumer) are asserted to agree,
which is a cheap and effective conformance check on the propagation rules.
