# Architecture

Read this page to understand the source code or why a behaviour works the way it does. It covers the modules dispat is
assembled from, the algorithms behind the plan, the execution model, and the design decisions that shape both.
References like `§N.N` point into the conventional-commits specification the parser implements, located at
[`pkg/ccme/SPEC.md`](https://github.com/yohimik/dispat/blob/main/pkg/ccme/SPEC.md). The specification is licensed under
GPL-3.0-or-later, and dispat uses GPL licensed materials from CCME wherever this documentation quotes or restates it.
The parser itself and the rest of dispat remain under MIT.

## Runtime steps

Steps 1 and 2 run in the command-line controller, located in `internal/cli` behind the thin `main.go` of the dispat
binary. Everything from discovery onward runs in the `app` package's `Status` (steps 3 to 6) and `Release` (all steps).
You can call these same operations without a command line.

The `Release` function brackets step 3 onward with the [release lock](../reference/releasing/release-lock.md). It
pushes an annotated `dispat-release-lock` tag unforced to `commit.remote` before discovery, and deletes it from the
remote and the clone on the way out. This deletion runs under a detached context, so an interrupt still gives the lock
back. If the push is rejected, a release is already running against this repository. The run stops there with exit `1`
before reading a single tag. Set `unsafeDisableLock: true` in the config or `DISPAT_UNSAFE_DISABLE_LOCK=true` in the
environment to skip this bracket entirely. The tag name is reserved in `internal/gitx`, so no tag format can read it
back as a release tag.

1. Parse the command line using pflag and dispatch one of the commands in the [CLI reference](../cli/README.md). An
   unknown command word acts as shorthand for `run`, like `dispat lint`.

   dispat reads your environment files (`./.env` or the file `--env-file` names) into the process environment before
   the phases start. It adds only what the environment does not already define. This happens first because dispat's own
   variables live there, including the update check's switch and the GitHub token.

   The controller runs its work in phases ordered by how much each needs to know. It refuses what the flags alone can
   refuse first, so a usage mistake never costs you a configuration error. Commands that read no config file run next,
   then the config loads, and finally everything that needs it runs. The commands that answer before loading any config
   are `init`, `self-update`, `install`, and the three manifest commands (`scanner`, `writer`, `replacer`). The `if`
   command joins them because a condition checks the environment rather than the repository.

   Run `init` to write a starter config and exit before anything else. It refuses a `--root` that is not a git
   repository root. The `run` command computes the plan and stops. It runs the script inside each changed package that
   has one over the dependency graph, using the build concurrency budget. dispat looks up the script in the package's
   `scripts`, then its space's, then the file's. The `--on-error` flag decides whether a failure skips the failed
   package's dependents.

   The shared `internal/filter` resolver decides which packages this covers in three steps. First it finds a window:
   the release window, what `--since <rev>` addresses (scopes first, then changed files for scopeless units per §6.2),
   or every package for `--since all`. Next, the `--package` / `--space` / `--group` terms narrow this window. The
   invocation folder stands in for any terms you did not type. Finally, `--consumers` expands the result downstream.

   Nothing below step 6 applies to the `run` command. The `preview` command computes the plan quietly with diagnostics
   but no graph. It prints the pending release notes for every pending package in publish order under the same filter,
   then stops. The `scanner` and `writer` commands also answer before any config loads. They expose the `pkg/scanner`
   and `pkg/writer` libraries directly (see [Manifest tools](../editing/manifests.md)). They read nothing but the paths
   named on the command line, making a monorepo root, a plan, and a git history beside the point.
2. Resolve the config file in `--root` or by ascending its parent directories. The config's own directory becomes the
   effective monorepo root. A folder's `.dispatexclude` chooses between candidate names. A file declaring `spaces` ends
   the ascent. A file declaring only `packages` yields to a root above claiming its folder as a space. This tells a
   space folder's file apart from a monorepo of standalone packages. A file declaring neither is a package's in-folder
   override and does not end the ascent.

   dispat parses the file using its own format's parser, so every key keeps the case you wrote it in. It replaces each
   `$ref` with the file it names relative to the file that wrote it, and refuses cycles with the chain. A copy of the
   result carries the flag bindings, and dispat's own decoder reads that copy into the model and validates it, folding
   a key to find the field it names, refusing any key the model has no field for, and refusing two keys of one object
   that differ only by case. Nothing is renamed on the way, so a map key reaches the model as its author wrote it.
3. Discover packages across every direct sub-folder of each space path not excluded by the space's `.dispatexclude`.
   Package names must be unique across spaces, case included, because every name in dispat is matched
   case-insensitively. Discovery includes every standalone `packages` entry with a `path`.
   Per-package configuration resolves here through a seven-layer ladder. It reads the root file's own defaults, the
   space, the space folder's config file, then the top-level `packages` entry. It continues through the space's
   `packages` entry, the space file's `packages` entry, and the package folder's own file.

   Each configured package gets a derived space value carrying the merged configuration. A standalone package becomes a
   single-package space of its own. Everything downstream reads per-package behaviour without knowing these layers
   exist. dispat collects dependency declarations from every source (the root object, the entries, the in-folder files)
   into one merged list.
4. Build the dependency graph from the merged relations. dispat topologically sorts this graph. Cycles abort the run
   and print the named members.
5. Build the plan by resolving baselines, computing pending windows, and parsing their union. dispat applies
   corrections, cancellations, and holds before computing direct bumps. It runs the three propagation phases, then
   computes versions. Corrections rewrite the parsed stream in place. Every phase after them reads a stream it cannot
   distinguish from one authored that way. Every versioning group is versioned as one. This applies whether it is a
   space with its own shared mode or a declared `versionGroups` entry its members joined.
6. Print the diagnostics, then narrow the plan to your `--package` / `--space` / `--group` selection. The `plan.Narrow`
   function deselects every unselected releasing package. It withholds a selected package whose provider is releasing
   but unselected for the next run as `W230`. It reports a versioning group split by the selection as `W231`. dispat
   then prints the full graph with `old -> new` versions and channel transitions in publish order. It marks the
   deselected packages as such.

   Pass `--strict` to refuse a selection carrying either finding. This refusal happens after printing the graph and
   before any release work. The `status` command stops here.
7. Refuse to release when the plan has a repository-scoped error. dispat also refuses any error at all under
   `commitErrors: "error"`.
8. Verify remote and API access up front when `commit.push` or GitHub releases are enabled. dispat runs `git ls-remote`
   and `GET /repos/{owner}/{repo}` to fail fast before any release work starts. Set `commit.verify: false` to skip the
   git check.
9. Run the gating run-level `beforeAll` hook. A failure here aborts the run before any release work begins.
10. Execute the task graph with per-stage concurrency budgets. This runs a version, build, and publish stage per
    *releasing* package, excluding held packages. The space's gating hooks bracket each stage (`beforeAll`,
    `beforeVersion`/`postVersion`, `beforeBuild`/`postBuild`, `beforePublish`). A space's `flow.login` runs once before
    its first publish. Every other publish in that space waits on it.
11. Three things run in order after each successful publish. The release recorders run first, writing the changelog
    file and the GitHub release unless you are in release-commit mode. The annotated tag runs next. dispat defers this
    tag in release-commit mode, and a `PACKAGE_<KEY>` script export pins it to the exported commit instead of HEAD. The
    warn-only `postPublish` hook and the warn-only announce frame (`beforeAnnounce`, the announce stage,
    `postAnnounce`) run last. Because the publish succeeded, none of this can fail the package anymore. A failure here
    is a [critical](#after-the-point-of-no-return).
12. Run the warn-only `postAll` hook. It receives the run outcome through the `DISPAT_RESULT_*` variables.
13. Run the finalize phase when `commit` is enabled. dispat makes one release commit staging all published packages,
    then places tags on that commit or on a package's exported `PACKAGE_<KEY>` commit. The push follows when
    `commit.push` is enabled. It pushes the branch first, then the run's tags, skipping and warning about any tag
    already on the remote. GitHub releases referencing the pushed tags come last. The warn-only commit and push hooks
    bracket these operations (`beforeCommit`/`afterCommit`, `postCommit` after tags, `beforePush`/`afterPush`). Every
    package here has published, so nothing in this phase aborts it either. Each step runs, and each failure is a
    [critical](#after-the-point-of-no-return).
14. Print a per-package summary and the totals. dispat exits `1` if anything failed or if it recorded any critical.

## Planning

The planner acts as a pure function of your history, graph, and configuration. It never consults wall-clock time or the
outcome of any previous run. This makes re-running after a partial publish deterministic. The one date-derived input is
git's newest-first tag ordering, which decides which tag counts as a package's latest. That ordering remains a fixed
property of the repository and stays the same on every re-run.

**Windows.** dispat answers every question of the form "does this commit still count?" against a *pending window*. This
window contains the commits from a package's last **stable** tag to `HEAD`. Which package's window it consults depends
on the purpose. Conflating the two is the bug that loses releases:

| Question                              | Window consulted |
|---------------------------------------|------------------|
| Does this unit bump `P` itself?       | `P`'s            |
| Does this unit set `P`'s own channel? | `P`'s            |
| Does this unit bump dependant `D`?    | **`D`'s**        |
| Does this unit re-channel `D`?        | **`D`'s**        |

Reading the last two questions against the *source's* window silently orphans consumers after a partial publish. The
commit leaves the provider's window when it releases, making the unit lose its source packages. The consumer then never
releases on this or any future run. Reading them against the target's window is all that catch-up is. dispat uses no
repair pass, no second traversal, and no timestamp comparison anywhere in the package.

On a prerelease train, the window deliberately spans commits the train's prereleases already published. This lets §11.4
recompute the train's target and a graduation's version over the whole train. But published work remains published. A
commit contained in the *baseline* tag (the newest tag of any kind) still counts toward the bump and is discharged for
every question asking "is this still pending?". It cannot re-release the train, which prevents every run from emitting
`beta.1`, `beta.2` from the same content. A `Release-As` it carries is consumed, and a `cancel` cannot discard it
because a prerelease tag is a published tag. This discharge extends to diagnostics. dispat does not re-warn a contained
channel directive as `W199` because the tag records that it worked. It does not re-judge contained propagated proposals
(`W200`/`W208`). A cancel discharged for every package it names is spent rather than misaimed, so `W170` stays for live
cancels only.

**Propagation runs in three phases, and they may not be merged**. Phase 3 reads what phase 2 produces:

1. **Channel axis**: Each unit's `Propagate-Channel*` directives propose a channel per package. dispat admits these
   against each target's window.
2. **Channel resolution**: Settle `channel(P)` for every package. A direct directive beats every propagated one
   regardless of age. Among equals, the newest commit wins, followed by the last unit in it. dispat builds candidates
   by pushing from units to the packages they resolve to, rather than rescanning the window once per package.
3. **Bump axis**: Each unit's `Propagate*` directives are admitted only where a source releases on a channel the target
   can resolve. This means `stable` or the target's own channel. A stable consumer is not bumped by a provider's beta
   release. That would be a republish with no content, and a stable package would ship declaring a prerelease
   dependency.

There is no circularity in the other direction. Phase 1 reads only the units and the packages' *baselines*, never a
value computed in this run. This makes the channel axis converge. Having arrived on `beta`, a package is already there,
so dispat proposes nothing further even though the commit remains in its window.

Both axes share the traversal. It runs breadth-first from the unit's source packages with a single-visit, shortest-path
depth. dispat measures this from the originating source set and never re-bases on an intermediate. A package
republishing as a catch-up does not propagate onward. A failed publish can never enlarge what a commit releases.

**Versioning groups.** Packages with shared versioning are grouped by their resolved group key. This key is the space's
own name for a space with its own mode, or the declared `versionGroups` entry the space or package joined. A group may
span spaces. A mode carries one number called its *shared depth*. This defines how many leading version components the
group holds equal: 3 for `fixed`, 2 for `fixedMajorMinor`, 1 for `fixedMajor`, and 0 for `independent`. The group's
depth is the deepest any member declares. Sharing more implies sharing the prefix, and dispat reports a disagreement as
`W237`.

dispat versions each group as one virtual package after the per-package pipeline produces bumps, channels, and pins.
The group aggregates what the version computation reads. It reads the baselines of *every* member, including held ones,
so the shared version can never fall below a position a member already published. It also reads the bumps, new work,
and channel movements of the members that would release. dispat runs that through the ordinary §13.9 computation,
including pins, trains, and guards. This produces one shared next version, one prerelease train, and one pin per group.
The newest member pin wins. Its scope breadth is one by construction because the group holds one shared prefix.

**The engagement rule** makes a partial depth work. It is a single predicate evaluated after that computation. The
group takes its members over in two cases only. It engages when the computed version leaves the group's prefix behind,
or when the group already sits on a prerelease train whose prefix left the stable line. In that second case, the
train's later prereleases and its graduation belong to the whole group, even though neither moves the prefix again. At
the full depth, the predicate is constantly true, so `fixed` and `fixedSparse` run the path they always did. dispat
admits a pin to the group computation under the same rule. A pin naming the prefix the group already carries is left to
its own package's `applyPin`. This ensures a member's local `Release-As` is never measured against the group's
aggregate.

When the group does not engage, every member goes through the ordinary per-member `versionOne`. An alignment pass then
keeps the invariant. A member releasing below the group's prefix adopts it. This is how a sparse member's first change
lands it on the shared part. A non-sparse member with nothing pending whose baseline lags is released at the prefix
with `W234`.

Assignment is where sparseness shows. The mode is each member's own, so a joined group can mix them. A plain mode
releases every non-held member at the group version. It marks members with no cause of their own as rides using `W234`.
This is non-suppressible and writes a "no changes" changelog entry naming the shared part. A sparse mode assigns the
group version only to members with a cause of their own. Scope resolution remains untouched either way, so each member
keeps its *own* units for changelog and release notes.

dispat preserves two convergence properties. A quiet group whose members agree on the prefix releases nothing. A
non-sparse member left behind by a failed ride or a mid-life adoption is re-released on the next run. It releases at
exactly the group's published version under `fixed`, and at the start of its own line under a partial mode.

The group's aggregate measures each member's pending work against the tag holding the group's baseline rather than the
member's own. Work that tag already contains never moves the shared prefix a second time: a member whose leg failed
after another member published their shared work catches up at the version that carries it, as its own release rather
than a ride, and the member that published is not dragged into an empty re-release.

## Module map

Read one line per package below. Each package's doc comment carries the full story, and the deeper design notes live in
the sections below.

| Package              | Role                                                                                                                                                                                                         |
|----------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `pkg/ccme`           | The commit-message parser: units, headers, directives, footers, scope terms, semver. Regex-free, single-pass, immutable; knows nothing of git or workspaces. Spec in [`SPEC.md`](https://github.com/yohimik/dispat/blob/main/pkg/ccme/SPEC.md). |
| `pkg/config`         | The generic configuration loader (own module): json/yaml/toml into one tree, `$ref` composition, the upward root ascent, a reflection-free setter-table decode that keeps a key's written case and refuses unknown keys and two spellings of one name, env layering and ref-aware format-preserving edits. Knows nothing of dispat's own model. |
| `pkg/models`         | The public configuration model (own module): the structs a `dispat.json`/`.yaml` decodes into, so external tooling and the integration suite author configs as typed values.                                 |
| `pkg/manifest`       | Shared manifest vocabulary (own module): dependency kinds, the requirements-file name rule, PEP 503 normalisation; definitions the scanner and writer must apply identically.                                |
| `pkg/scanner`        | Deliberately lightweight manifest reader (own module): npm, Go, Cargo, Python, Composer, Maven, .NET, pub, RubyGems and the mobile formats parsed into one `Manifest` shape with declared names, versions, dependencies and local paths; bounded reads, partial results with joined errors. No lockfile resolution, no network. Exposed on its own as `dispat scanner`. |
| `pkg/writer`         | Format-preserving manifest writer (own module): byte-precise range and version rewrites for **every** manifest the scanner reads, atomic writes, and a result separating applied edits from ones deliberately left alone (a Maven property, a workspace inheritance). Exposed on its own as `dispat writer`. |
| `internal/cli`       | The command-line controller: flags, dispatch, exit-code mapping, logger construction.                                                                                                                        |
| `internal/app`       | The application layer: `Status`, `Release`, `RunScript`, `AutoWriter`, `AutoReplacer`, `Preview`, `Compute`, `ScanManifests`, `WriteManifests`, the step commands, the finalize phase and run-level hooks; wires every other package together. Its package *sweep* is the shared half of every command that covers a set of packages: the selection, the task graph, the concurrency budget and the skip cascade live there once, and each command supplies only what one package's work means. |
| `internal/config`    | Everything about dispat's configuration that is dispat's rather than configuration's: the fields tables, validation, package discovery, the space and per-package override merging, `.dispatexclude` over folder and config names, and the domain half of `compute --write`. Reading a file, following its `$ref`s, the root ascent and the decode itself are `pkg/config`'s; this layer supplies the model and the wording. |
| `internal/plan`      | The planner: windows, scopes, directives, propagation, channels, versioning groups; a pure function of history, graph and configuration. Plus `Narrow`, which restricts a computed plan to part of the graph for a filtered release (publish order withholds, versioning-group splits reported).                                                                     |
| `internal/graph`     | Deterministic topological sort and the generic `Scheduler`/`Drain` pump described below.                                                                                                                     |
| `internal/release`   | The executor: the task graph, stage frames, hooks, login gates, native auto-versioning, `DISPAT_*` environment rendering, script outputs. Plus the release lock, the one-tag mutex a release takes on the remote before it plans.                                                                    |
| `internal/changelog` | Changelog rendering and the per-package record dispatcher.                                                                                                                                                   |
| `internal/github`    | The GitHub release recorder: REST calls, asset uploads, up-front verification, and the already-published probe that makes recording repeatable.                                                              |
| `internal/gitx`      | Git behind an interface: tags, baselines, commits, ancestry, tag formats; the CLI implementation shells out to `git`.                                                                                        |
| `internal/script`    | Shell script execution with process-group cancellation and bounded pipe waits. Traces the resolved shell, folder and command of everything it runs, which is the answer to "what did that stage actually execute". |
| `internal/selfupdate` | Replacing the running binary with one downloaded from a release: version resolution, checksum verification against the published sums, the kept backup and its expiry, and the background check that prints the notice on the way out. |
| `internal/model`     | Resolved domain types (`Space`, `Package`, `AutoVersion`, record specs) shared by config, plan and release.                                                                                                  |
| `internal/filter`    | The one selection resolver every package-selecting command shares, `release` and `status` included: `--package` / `--space` / `--group` terms, their globs, and the invocation folder that stands in for the first two.                              |
| `internal/globx`     | The one glob matcher scope terms, `autoVersion.match`, `.dispatexclude` and the selection terms share.                                                                                                        |
| `internal/ignore`    | The change-scope ignore rules: `ignore` patterns and `.dispatignore` files compiled into one chain per package (repository, space, package), asked once per changed file after ownership is decided. Built on `globx`, so there is still one glob dialect.                                                    |

## Graph algorithms

A handful of small graph algorithms and one graph-shaped structure carry the whole tool. This section maps what runs
where.

### Topological sort: the publish order

The `internal/graph.TopoSort` function uses Kahn's algorithm over package names with one refinement. The zero-in-degree
frontier is a **min-heap**. Ties always break alphabetically, so the same graph yields the same order on every machine
(§17.2). The heap makes the sort O ((V+E) log V). Combined with one bounded `git tag` and `git log` query pair per
package, this keeps planning cheap at monorepo scale.

```
    a     b        frontier = nodes with no pending providers: {a, b}
     \   / \       the heap pops alphabetically -> a, b, then c, d, then e
      c    d       every provider precedes every consumer
       \  /
        e          a cycle leaves nodes stranded with the frontier empty:
                   TopoSort returns a typed *CycleError naming them, which
                   the planner reports as E200 with the edges' manifest kinds
```

The cost is O ((V+E) log V) for the heap pops. dispat runs this once per plan.

### The task graph: what a release actually schedules

Each releasing package contributes up to four task nodes. The `version` node exists when any provider of the package
moved in this run **or** its space auto-versions. Section §9.4 reconciles against every workspace dependency, so the
stage cannot be conditional on this run's updates. "Moved" means `Release.Updates`, which is deliberately wider than
`DueTo`. A provider releasing beside its consumer with no propagation between them still hands it a version to pick up.
Gating on `DueTo` made a scripted version stage and a native `autoVersion` block disagree about the same run. The
`syncLock` node exists when an autoVersion space configures the scripts. The `build` and `publish` nodes always exist.

```
  core:   build ─────────────► publish
            │                    │ │
            │  (isBuildWaiting)──┘ │ (always)
            ▼                      ▼
  web:    version ─► syncLock ─► build ─► publish
```

(The two provider edges land on web's *first* task, its `version`, and on its `publish`. Read the bullets below for the
precise rule.)

- A consumer's **first** task (version when present, build otherwise) waits for each changed provider's `build`. It
  additionally waits for that provider's `publish` when the provider's space sets `isBuildWaitingPublish: true`. This
  covers the Docker case, where the consumer can only build after the base image is pushed.
- A consumer's `publish` **always** waits for its providers' publishes. Publishing against an unpublished provider
  version would be broken regardless of the flag.
- When a changed provider fails or is skipped, its consumers are skipped unless they have a fresh release reason of
  their own. A provider under `isBuildWaitingPublish: true` skips them unconditionally: their builds consume the
  publish that never happened, so a reason of their own cannot proceed them past the missing artifact.
- The `syncLock` node sits between `version` and `build` in a scheduling class of its own.

### Drain: the one scheduling pump

The `graph.Drain` function consumes a Scheduler with bounded parallelism. This is the incremental core of Kahn's
algorithm. One coordinating goroutine owns all scheduler state. Every ready node gets a goroutine of its own.
Completions come back over one channel and cascade new ready nodes.

```
   ready queues, one per class          budgets
   build:    [web:version, ...]   ◄──   build/version share BuildConcurrency
   publish:  [core:publish]       ◄──   PublishConcurrency
   syncLock: [web:syncLock]       ◄──   min over the voting spaces (default 1)

   loop: launch while budgets allow ──► goroutine per node ──► done channel
         ▲                                                        │
         └── completion cascades newly-ready nodes ◄──────────────┘
```

Per-class queues ensure a stalled class never blocks another class's budget. A node occupies as many of its class's
slots as its package's configured weight. dispat clamps this per-package `concurrency` override to `[1, budget]`, so a
weight past the budget simply runs alone. A node that does not fit waits at the head of its queue, and nothing behind
it overtakes it. This head-of-line discipline makes a heavy node's wait finite instead of starvable. The syncLock node
keeps the ordinary cost of one. Its budget exists to serialise lock-file writers, not to price packages.

There are no locks in the scheduling hot path. A mutex guards only the shared result map that handles skip decisions,
provider filtering, and the syncLock skip read. Every task finishes exactly once, including no-op completions for
packages already failed or skipped. This lets the counting terminate without special cases. A cancelled context stops
new launches, awaits in-flight nodes, and reports the rest as cancelled. dispat diagnoses a frontier that empties with
nodes remaining as a cycle instead of deadlocking.

Two things drain. The release executor drains over `(package, stage)` nodes and the budgets above. The *package sweep*
in `internal/app` drains over package names and one budget. Every command covering a set of packages runs on this
sweep, including `run`, `autowriter`, `autoreplacer`, and the four step commands. It decides who runs, in what order,
under which budget, and what a failure does to the dependents. A command supplies only a `packageWork`. Given one
package's release, it hands back the work to do or nothing when there is none. The `commit` and `github` commands
declare themselves serial because a repository has one index and one HEAD. The rest ride the build budget. Adding a
command that covers packages requires only one small file rather than a copy of the scheduling.

### Propagation: bounded BFS, three phases

Propagation (§9.2) walks the dependency graph outward from each unit's source packages. It moves along kind-filtered
edges and admits commits against each **target's own** pending window. dispat expands every package at most once per
phase. The walk remains O (V+E) whatever the directive's depth says. The symbols `^` (one edge), `+N`, and `^^`/`+*`
(the closure) only change where the expansion stops, never how often a node is visited.

```
   feat(core)^          feat(core)^^ / +*
   core ─► api          core ─► api ─► app        bumps merge by max();
     └──► cli             └──► cli                a package never re-propagates
        (depth 1:              (closure: api,     a bump it merely received
         api, cli)              cli, app)
```

The three phases (channel proposals, channel resolution, bump propagation) may not be merged. Phase 3 reads what phase
2 settled. Phase 1 reads only units and baselines, which makes the channel axis converge.

### Ancestry: three sources, memoised

Cancellation (§10.4) and prerelease-train containment ask "is commit a an ancestor of commit b?". The answer comes from
the strongest available source. dispat tries git itself (`merge-base --is-ancestor`), the commits' parent pointers
(BFS), or plain history order for linear fakes. It memoises every (a, b) answer on the computation. The same pair is
asked from several nested phases, and each uncached git answer spawns a subprocess.

```
   c1 ── c2 ── c3 ── c4      ancestor(c2, c4)?  git first;
           \                 parent-pointer BFS when git has
            └─ c5            no answer (ErrNoAncestry); history
                             rank as the linear-case fallback
```

A real git failure aborts planning rather than silently degrading to the weaker fallbacks. A wrong ancestry answer
changes which releases get cancelled or contained.

## Failure semantics

**Once the release work starts, no error aborts the run.** Everything that can refuse a release happens before any of
it. This includes the [release lock](../reference/releasing/release-lock.md), a blocked plan, the branch guard, the
behind-remote check, the remote and GitHub verification, and the `beforeAll` hook. Those refuse while nothing has
happened yet, which is the only moment refusing costs nothing. From the first build script onward, the run always goes
to the end. A package can fail, and dispat can skip its consumers behind it. Every other package still releases, and
the finalize phase still records whatever published. Only an interrupt stops a run early, and even then dispat records
what already published.

Inside that, there is a second and stronger line. **Once a package's publish succeeds, nothing can fail that package**.
See [After the point of no return](#after-the-point-of-no-return) below.

A failed script marks the package failed, but nothing aborts the run. The `Result.FailedStage` records where it failed
for the summary. At the start of every task, the package re-evaluates the skip rule. It skips if some changed provider
failed or was skipped AND the package has no fresh own commits, no channel change, and no successfully published
changed provider. A bump in the changeset its baseline has not published counts as fresh own commits, but own work an
earlier prerelease shipped on a prerelease train does not.

A consumer's terminal outcome is deterministic in both modes. Its publish always waits for its providers' publishes, so
a provider's publish failure is guaranteed to be seen at the latest there. With `isBuildWaitingPublish: true`, provider
outcomes are already final before the consumer's version stage. With `false`, the consumer may spend a version or build
on a release that its publish then skips. This is the trade-off that flag opts into. The version stage filters failed
or skipped providers out of the `DISPAT_UPDATED_*` variables. It skips its script entirely when it had providers to
pick up and none of them survive.

Failed or skipped consumers are not lost across runs. Nothing about this depends on tag creation times, because those
are not stable under merges, rebases, or equal timestamps. dispat uses them for reporting only. A commit propagates to
a dependant while the *dependant's* window still contains it. A consumer that missed a run is simply still owed its
release. The `DueTo` field then contains an *unchanged* provider. It gets no task nodes, and the version stage passes
its released version. This case makes `Updates` a union of `DueTo` and this run's releasing providers rather than
either alone. A consumer that was never released while a provider has been is the same case, using the whole history as
its window. dispat labels such a release a catch-up and reports it with the origin's published version.

Because admission is a window test, the guarantees are structural rather than heuristic. A contribution survives every
run until the dependant releases past it. dispat does not re-admit it afterwards. The version a package is caught up at
is the one dispat planned it at originally. A later run's target set is always a subset of the first run's, so a failed
publish can never widen a commit's blast radius.

dispat records a skipped package as *blocked* with the responsible dependency. It is never silently absent from the
summary. A package that was in the plan and produced nothing has to be accounted for.

For spaces with `revertOnFail: true`, a failing package has its folder rolled back via the `Reverter` interface. dispat
runs `git checkout -- <dir>` and `git clean -fd <dir>` using `gitx.CLI`. This restores tracked files from HEAD and
removes untracked files, scoped to the package folder. The same rollback runs when a package is skipped after its
version stage already modified files. dispat logs a revert error, but the package keeps its original failure status.
Nothing is ever reverted after a successful publish, for the reason the next section gives.

### After the point of no return

A package's publish script succeeding marks the point of no return. The artefact is on its registry, and no later step
can take it back. From there, dispat has nothing left to decide and only things left to record. It writes the tag, the
changelog entry, the GitHub release, the release commit, and the push.

When one of those fails, it is a **critical**. dispat logs it with its own code, counts it in the summary, and
otherwise ignores it. The run carries on to everything else it owed. The command still exits `1` at the end, so a
release that lost part of its record never looks green in CI.

| Code | What failed |
|-------|--------------------------------------------------|
| `E220` | A release tag could not be created |
| `E221` | A release tag already exists at a different commit |
| `E222` | A release record (changelog entry, GitHub release) could not be written |
| `E223` | The release commit could not be made |
| `E224` | The push failed |

None of these fails the package because failing it would be a lie with consequences. The package *is* published, so
marking it failed would do three wrong things. It would revert the package folder, throwing away the version bump of
something already on a registry. It would run the `onFail` script. It would also skip every consumer, none of which has
anything wrong with it, and all of which have a real published provider to build against. None of that un-publishes
anything. The status stays `published`, and the summary line for that package turns into an error carrying what went
missing. You get told exactly what to go and repair.

For the same reason, nothing stops at the first failure. A changelog that could not be written must not cost the tag
too. One package's tag failing says nothing about the next package's tag. A failed push must not withhold the GitHub
releases that document the same work. Each step runs, each failure is recorded, and the exit code adds them up.

The `E221` error is the one case that also declines to act. A tag already sitting at another commit is left exactly
where it is rather than moved onto this release. It is a record some earlier run made. With
[force](../configuration/records.md#force) on, a tag moved here would overwrite the copy on the remote, turning one
local mistake into everyone's.

## Design decisions

Interfaces decouple every side effect. This keeps the planner and executor unit-testable with in-memory fakes:

| Interface                 | Implementations                           | Used by |
|---------------------------|-------------------------------------------|---------|
| `gitx.Git`                | `gitx.CLI` (shells out to git)            | plan    |
| `script.Runner`           | `script.ShellRunner`                      | release |
| `release.Tagger`          | `gitx.CLI`                                | release |
| `release.Reverter`        | `gitx.CLI`                                | release |
| `release.ReleaseRecorder` | `changelog.FileWriter`, `github.Releaser` | release |

The `ReleaseRecorder` interface is the extension point for publishing release data anywhere. Both current
implementations render the same changelog sections using the shared `changelog.Format`. Zero-value fields fall back to
defaults. They differ only in the destination, choosing a file prepend versus a REST call. Recorders run in order after
each successful publish. A recorder error is a critical (`E222`), and the remaining recorders and the tag still follow.

Other choices:

- Versions live in git tags only, so you have no version files to commit.
- dispat uses one `git tag` and `git log` pair per package rather than a global log walk. This is simple and correct
  for per-package tag baselines.
- It shells out to the git binary to match CI byte-for-byte.
- Script output streams line-by-line into the structured logger, keeping parallel package logs attributable.
- Ordering is deterministic everywhere. dispat uses alphabetical tie-breaks in the toposort and sorted space iteration
  in discovery.

Two diagnostics are deliberately not suppressible. Each explains a release outcome a reader of the commit log alone
cannot account for. A **catch-up** and a **channel-only** release explain a package's *presence* in a plan. A
**blocked** package and a **suppressed propagation** explain an *absence*.

Discovery runs in O (packages) time. Planning takes O (V+E) graph work per propagation phase plus exactly one `git tag`
and one bounded `git log` per package. Execution adds O (V+E) scheduling overhead on top of script runtime.

That bound is easy to lose in two places, and both are guarded by construction rather than by care. A package needs two
baselines, and asking for them separately runs the same tag query twice. The listing is the primitive, and both
baselines are selections over it. The propagation phase split costs a second pass over the *unit list* and a second set
of graph traversals. It does not cost a second pass over history. Commit walking, window computation, parsing, and
scope resolution all happen once before either phase runs. Going to the object store is the expensive part of a run. In
a repository running no prerelease train, no unit carries a channel directive at all, and the channel phase does
nothing.

## Deliberately out of scope

dispat implements the release computation. Some parts of the specification assume an engine that also reads package
manifests and knows about registries. The manifest half is native when a space opts into
[`autoVersion`](../configuration/autoversion.md), whose parsing strategy covers every format the scanner reads; the
registry half is delegated to the version and publish scripts. This keeps dispat language-agnostic:

| Not implemented                                             | Delegated to                                                                                                                                                                          |
|-------------------------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| Rewriting dependency ranges in manifests                    | native under [`autoVersion`](../configuration/autoversion.md) for every scanner-read format, npm and Go through the game engines ([Supported formats](../editing/manifests.md#supported-formats)), reported as W197/W203; indirections and versions no manifest holds: `replace` rules or `flow.version`, via the `DISPAT_WORKSPACE_*` variables |
| Manifest-vs-baseline version checks                         | native under `autoVersion` (W192); otherwise nothing                                                                                                                                  |
| Publish targets, registries, adopting published versions    | `flow.publish`                                                                                                                                                                        |
| `initialVersion` / `preserveMajorZero` remapping            | current behaviour: first release from `0.0.0`, ordinary bumps                                                                                                                         |
| Per-run safety limits (max packages, majors, channel moves) | (nothing; the exact-pin major-jump guard *is* enforced, with a default of 1)                                                                                                          |
| Post-run convergence verification                           | (nothing; re-run `status`)                                                                                                                                                            |
| `requireCodeownerFor`, the CODEOWNER gate on directives     | (nothing; the specification's own configuration for it is unmodelled, so no directive can be gated on approval)                                                                       |

This has a consequence for the diagnostics registry. dispat never emits the specification codes that belong to these
registry-aware and audit-aware features. Errors like `E197` (publish-order violation), `E198` (registry identity
unverifiable), and `E199` (convergence check failed) assume an engine that queries registries and audits its own runs.
Warnings like `W195` (staleness audit) and `W196` (published version adopted from the registry) belong to the same
features. dispat implements and emits every other code of the registry, including the repository-scoped bucket (`E182`,
`E185`, `E191`, `E195`, `E196`, `E200`). This includes the codes the registry leaves to the engine rather than to the
parser. dispat raises `E210`-`E213` and `W209`-`W215` here. These resolve an `Edits` or `Deletes` target against
history and report what the correction did (see [Correcting a record](../reference/corrections.md)). Read
[Diagnostic codes](../reference/plan-errors.md) to see what each of those six means and what to do about it.

In the other direction, thirty-one codes are dispat's own. They sit outside the specification's registry, attached to
features the specification predates or does not have. They are numbered from `W220` and `E215` upward. This clears the
registry, which ends at `W215` and `E213`. It also clears `W195` and `W196`, which the specification reserves for the
audit features above.

| Codes | Feature |
|------------------------|--------------------------------------------------|
| `W234`-`W237`          | [Versioning groups](../reference/releasing/versioning.md): a ride, and the three conflicts a shared version can produce |
| `W220`-`W222`, `W225` | Manifest-derived: an ambiguous manifest name, a rewritten dependency with no configured edge, a replace rule that matched nothing, one package's manifests declaring different versions for it |
| `W223`, `W224`, `W226` | A record that is already there: a release tag, a GitHub release, a changelog entry. What makes the [step commands](../reference/releasing/steps.md) re-runnable |
| `W227` | A `commit.include` path that names nothing on disk: the path is not staged, and the miss is reported so a typo cannot silently cost the release commit its artifact |
| `W228` | A [step command](../reference/releasing/steps.md) invoked inside a run whose own replan drifted from the run's `DISPAT_*` environment: the record is aligned to the run and the correction is reported |
| `E219` | A [step command](../reference/releasing/steps.md) inside a run it cannot align to, because the package is missing from its plan or the run's version renders a different tag. Nothing is written: a refused leg is re-runnable, a drifted record is not |
| `W229` | A wired `dispat github` running before the run's tag exists: GitHub would invent the tag at the default branch head, so the ordering is reported. The commit step belongs first |
| `W230`, `W231`         | [Releasing part of the graph](../reference/releasing/partial-releases.md): a package the publish order cannot reach yet, a selection splitting a versioning group |
| `W232`                 | An [alias tag](../configuration/alias-tags.md) that could not be written |
| `W233`                 | A [versioning group](../reference/releasing/versioning.md) whose members sit on different major versions, so the newest one decides where they all land |
| `W238`                 | A `Release-As` footer naming a package whose [versioning is `none`](../reference/releasing/versioning.md#packages-that-never-release-none): the directive moves nothing |
| `W239`                 | A [webhook](../configuration/webhooks.md) delivery that did not get through: the endpoint kept refusing, never answered, or the queue was full. Warn-only by design, like `W232`: a listener that missed a notification is never a reason to fail the release it was watching |
| `W240`                 | [Commit references](../configuration/records.md#naming-the-commit-behind-a-line) are configured but some entry lines have no commit id to point at, so those lines render without one. The planner has a sha for every commit a Git implementation reports one for; a window key that stands in for a missing sha names nothing a reader could open |
| `W241`                 | A configured [`noChangesText`](../configuration/records.md#what-an-entry-with-no-sections-says) that expanded to nothing, or to whitespace alone, so the entry carries the built-in line instead. The record is written and what it says is true, which is exactly why the substitution has to be reported: nothing in the file shows it happened |
| `W242`                 | The release push was refused because commits landed on the branch while the run was working, and the release recovered: it pulled them, replayed its release commit on top, re-pointed its tags at the replayed commit and pushed again. Warned rather than logged plainly because the release went out on a tree that is not the one the run was planned against, and the commits that arrived sit underneath the release commit with no record of their own |
| `E220`-`E224`          | [After the point of no return](#after-the-point-of-no-return): a tag, a record, the release commit or the push failing once a release is already out |
| `E215`-`E218`          | The [scanner command's gates](../editing/manifests.md): a local link still present under `--verify-unlinked`, none present under `--verify-linked`, a range `--forbid-range` matched, a range `--require-range` did not find |

All of these codes follow the registry's numbering conventions and blast-radius rules. dispat documents them where
their features are. The auto-versioning narrations `W192`, `W197`, and `W203` are the specification's own §9.4/§12.4
codes.

## Testing

The workspace is a set of modules under `go.work` with no module at the root. You run tests per module using
`go test -C services/dispat ./...`, and the same for each `pkg/` module. Those are testify-based unit tests against
in-memory fakes. Every internal package and every `pkg/` module has its own suite.

The integration suite is catalogued claim by claim in the
[test plan](https://github.com/yohimik/dispat/blob/main/tests/integration/docs/test-plan.md). Its coverage matrix maps
each of its forty-one goals onto the tests that prove it. The results are summarised per area in
[test results](./test-results.mdx). The unit suites have no equivalent catalogue. What they assert is stated in each
test's own name and doc comment. You can see what the whole suite reaches per package in [coverage](./coverage.mdx).

The composition claims live exclusively in the black-box
[integration suite](https://github.com/yohimik/dispat/blob/main/tests/integration/README.md). This includes the
compiled binary, real git over a process boundary, exit codes, scheduling under real concurrency, and signal handling.
The suite builds the real binary and asserts on git state, JSON log events, and nanosecond execution timelines. The
`services/dispat` module itself hosts no end-to-end tests. A test that could only be satisfied by faking what the suite
can witness for real belongs in the integration suite instead.
