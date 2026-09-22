# Contributing to dispat

Start with the [agent work guide](specs/agent-guide/README.md), the [architecture](packages/docs/docs/internals/architecture.md), and the configuration of the package you intend to change. Use explicit help commands to explore the CLI. Bare `dispat` runs a release.

Two documents are normative and are not repeated here. The [agent work guide](specs/agent-guide/README.md) owns the
working procedure: how to pick up a task, what to verify, and what to record. The
[CCME specification](specs/ccme-spec/SPEC.md) owns the commit-message grammar, the release units, the corrections, and the
diagnostic codes. Read them there and link to them rather than restating them.

Keep review findings, audit summaries and task plans in the conversation. Never create or commit standalone review reports in this repository. Put lasting requirements and user-facing explanations in the existing guides, specifications or documentation.

## Code style

### Common rules for all languages

Use small cohesive types, explicit dependencies, and abstractions at the boundary that needs them. Apply a pattern when it solves a concrete problem. Do not add factories, wrappers, or inheritance-like layers merely to give a pattern a name.

- Use explicit, descriptive names. The wider a name's scope, the more context its name must carry: package-level and exported names should be more descriptive than names used within a small local block. Avoid unexplained abbreviations and generic names such as `data` or `obj` when a domain name is available.
- Name variables, parameters, fields, and data types primarily with nouns or noun phrases that describe what they hold or represent, such as `repository`, `packageName`, or `releasePlan`. Use plural nouns for collections, such as `packages`.
- Reserve `i`, `j`, and `k` for numeric loop counters or indexes, such as `0, 1, 2, 3, ...`. Name iterated values and map keys by their meaning, such as `packageName` or `repository`.
- Name action functions and methods with verbs, such as `LoadConfig` in Go or `loadConfig` in JavaScript/TypeScript.
- Name functions after the specific work they perform: `CalculateChecksum`/`calculateChecksum`, `FormatReleaseLabel`/`formatReleaseLabel`, or `ResolveConfig`/`resolveConfig`. Using `Get`/`get` for a calculation, transformation, or decision is an antipattern: `getReleaseLabel` hides that the function formats a label. Reserve `Get`/`get` for retrieving an existing value, such as `getPackageById` fetching a package record from a database.
- Name boolean variables and predicates as questions: use `Is`/`is` for a singular subject and `Are`/`are` for a plural subject, following the language's casing rules. Examples include `IsEnabled`, `isEnabled`, `ArePackagesLocal`, and `arePackagesLocal`. A lookup returning a value and a presence boolean keeps its action name, such as `FindPackage`.
- Preserve published APIs. Introduce a preferred name with a documented deprecated alias or forwarding function when a rename would break users. Required library and framework methods keep their contract names.
- Keep functions focused on one operation. Split a long function along meaningful validation, planning, execution, or cleanup boundaries. Do not fragment a readable operation into trivial helpers just to lower its line count.
- When a decision needs branching, move it into a focused, named function that returns the result directly from each branch. Do not initialize a temporary variable and assign it separately in each branch.
- Prefer guard clauses and early `return` for invalid inputs, errors, and completed cases. Keep the main path flat, and omit `else` after a branch that returns.
- Use `continue` to skip irrelevant loop items and `break` as soon as the loop's work is complete. Prefer these exits over nested conditionals or flags that only track whether to stop. Preserve required cleanup on every exit path.
- Keep mutable state owned by one component. Bound concurrency, close response bodies and files, and stop timers and child processes on every exit path.

### Go

- Use ordinary Go composition and define interfaces at the boundary that consumes them.
- Name structs with nouns that describe their responsibility. Use Go's exported and unexported casing conventions.
- Start constructor names with `New` for exported functions or `new` for unexported functions, such as `NewUser` or `newUser`.
- Name methods that convert a value to a specific type after the target type, such as `User.Int()` for an integer conversion.
- Name interfaces with an adjective describing their capability or an `x` suffix, such as `Configurable` or `Gitx`. Name implementations logically, such as `LocalGitx`.
- Return errors with the operation and safe context. Preserve wrapped errors with `%w` when callers need to inspect them. Never ignore cleanup errors that could leave a release lock or published state ambiguous.
- Pass the caller's context through cancellable work. Use a bounded detached context only for documented finalization that must survive cancellation.
- Format Go with `gofmt`.

Three of those naming rules are checked. `go run ./tools/namingcheck .` reads `pkg`, `services/dispat`, `tools` and
`tests/integration` and reports an exported predicate that does not start with `Is`, an exported interface that is
neither an adjective nor an `x` name, and an exported implementation type wearing either vocabulary. Whether a noun is
the right noun is a review question and is deliberately not checked.

An exception is written down rather than argued. `tools/namingcheck/naming-exceptions.txt` carries one line per
excused declaration with the reason: an external contract such as `sort.Interface`, a method whose name a cross-package
interface fixes, and the deprecated forwarders in `pkg/*/compatibility.go` and
`services/dispat/internal/gitx/compatibility.go`. A reason beginning with `pending` marks a name that should still
change and names the review that owns the tree it lives in; `namingcheck -pending` lists that backlog. A single
declaration may instead carry `//namingcheck:exempt <reason>` in its own doc comment.

### JavaScript and TypeScript

- Use `const` for every variable declaration, including destructuring and loop bindings. Do not use `let` or `var`.
- When a value depends on branching, extract the decision into a function with early returns and bind its result with `const`. Do not replace reassignment with an object used only as a mutable box.
- Use `for (const item of items)` or `for (const [index, item] of items.entries())` when iteration needs early `continue` or `break`. Use collection operations such as `map`, `filter`, or `reduce` when they express the transformation clearly.
- A `const` binding does not make an object or array immutable. Keep any mutation explicit and within the owning component.
- Use `camelCase` for variables, parameters, functions, and methods, and `PascalCase` for classes, interfaces, and type aliases. Apply the common noun, action, and predicate naming rules.
- Follow the package's existing formatting, module, and TypeScript conventions.

For example, calculate a retry delay in a focused function, return directly from each branch, and bind the result with `const`. Name this operation `calculateRetryDelay`, not `getRetryDelay`:

```ts
function calculateRetryDelay(attempt: number, baseDelayMs: number, maxDelayMs: number): number {
  if (attempt <= 0) {
    return 0;
  }

  return Math.min(baseDelayMs * 2 ** (attempt - 1), maxDelayMs);
}

const retryDelayMs = calculateRetryDelay(attempt, baseDelayMs, maxDelayMs);
```

### Shell and documentation

Follow the local shell and documentation conventions. Shell scripts use POSIX `sh` and `set -eu`; quote paths and pass data separately from executable command text. Prefer a Go command under `tools/` or a dispat config script over a new shell file.

## Configuration is an API

Every setting must follow the existing root, space, package, and folder configuration ladder. Omitted values inherit. Explicit `false`, zero, or an empty value retain the meaning defined by the setting; do not accidentally turn omission into an override. Preserve each field's established map and list merge rules.

### The ladder

Four levels state a package's settings. From weakest to strongest they are the root configuration file, the space
(its entry in `spaces` and its own folder configuration file), the package (its entry in the space's `packages` map or
in the root `packages` map), and the package's own in-folder configuration file. The nearest statement to the package
wins. A level that says nothing inherits, which is what makes the boolean options three-state rather than plain: a
package writes an explicit `false` to contradict a `true` it would otherwise inherit.

How a level combines with the one below it is a property of the setting, and each rule has to be preserved:

- **Replaced.** Single values such as `tagFormat`, `versioning` and `src`.
- **Merged entry by entry.** `flow`, `scripts` and `env`. A level replaces the entries it names and keeps the rest.
- **Replaced whole.** `autoVersion`, `aliasTags`, `webhooks`, `buildOutputs`, `buildPlatforms`, `runOnly` and `manifestNames`. Their empty fields carry meaning
  against their siblings, so a partial overlay cannot express what they mean. An empty list is how a level opts out.
- **Overlaid field by field.** `changelog` and `github`, including their nested `authors` object. Their `include` and
  `exclude` lists still replace whole, because adding to an inherited list could never take a pattern away again.
- **Merged, never overridden.** `dependencies`. Every declaration at every level adds to one graph.
- **Concatenated.** `ignore`. Later levels add patterns, and a `!` pattern re-includes what an earlier level excluded.

Watch `concurrency`. At the root it is the budget, which is how many slots a stage may use at once. On a space or a
package it is a weight, which is how many slots one task occupies. They are two sides of one number and are not
interchangeable.

The repository-wide keys exist only at the root: `spaces`, `versionGroups`, `initials`, `commit`, `shell`, `run`,
`parser`, `commitErrors`, `nonPackageScopes`, `logLevel`, `logFormat`, `updateCheck`, `unsafeDisableLock`, `polyrepo`,
`repository`, `repositories`, `configs`, `repositoryOverrides`, `repositoryBaselines` and `execution`. `execution` is
narrower still: it is a node-startup setting, so it is read from the entry configuration alone and an imported or
linked repository's own object is validated and ignored. The full reference is
the [configuration documentation](packages/docs/docs/configuration/README.md).

### Repository participation

In a fleet, `repositoryOverrides.<source>.enabled: false` takes a repository out of the run. The key must match the
`.gitmodules` name exactly, including case, because that external name identifies the owning Git history. Exclusion is
a safety boundary rather than a filter: a disabled repository is removed before its checkout is inspected and before
its history is initialized, so it contributes no packages, no hooks, no baselines and no remote release lock, and a
source whose folder is not even present still composes. The paths of an excluded repository keep their ownership
boundaries, so nothing else may reach into them.

### Choreographed fleets

An identity-linked fleet has no control repository: every participant states its own `repository` identity, optionally
lists other peers in `repositories`, carries its own configuration and records, and is joined to its neighbours by two-sided submodule links. The
keys a control repository owns are refused rather than ignored there, because a fleet with no such repository cannot
honour a policy written for one, and a key nothing reads is how a fleet comes to believe it is linked. Participation is
still the invocation's question and is read from the entry repository alone, while commit policy, lock policy and
release records belong to each peer. The links must form a tree, so exactly one route joins any two repositories and
cross-repository evidence has one reading. `dispat compute --topology minimal` preserves existing links and proposes
the fewest additions that connect the roster. Every such proposal adds the same number of links, so it joins the
groups the existing links leave the fleet in at their centres, which keeps the longest route between two repositories
as short as those links allow: the work of reading link evidence and of settling a release across the fleet grows
with that route. `--topology star` proposes a direct link from the entry repository to every peer and errors when
existing links cannot fit that shape. Neither mode commits nor removes a link. The full contract is
[A choreographed fleet](packages/docs/docs/choreographed-repositories.md).

### Linked configuration

Linked configuration must use the same resolved settings in validation, planning, scripts, hooks, and release execution. Test inherited scripts and environment together, including root defaults, matching space/package overrides, multiple linked files, nested imports, and explicit disabling. Resolve relative paths against their documented owner. Never silently discover or adopt another repository's policy.

Keep shared policy at the lowest common scope. Put a short package-specific shell command in that package's `dispat.yaml`. Use a standalone script when it has independent callers or enough control flow to benefit from separate testing. Do not duplicate a tool pin or install policy between CI, Docker, and local scripts.

## Release safety

Treat publication as a saga of external writes. A successful upload cannot be rolled back by deleting a local file. Record known successful publications even if another package fails. A critical record or push failure must make the run fail without reporting the package as unpublished.

For a normal fleet release, acquire every participating repository's remote `dispat-release-lock` before synchronization or version planning. Acquire locks in deterministic order and clean up owned locks in reverse order if any acquisition fails. Never delete another run's lock. Keep disabled repositories out of planning and locking while retaining their ownership boundaries. The explicit unsafe lock bypass must produce a warning.

Protect existing work, reject ambiguous ownership and runtime revision drift, and verify publication outcomes before a retry when a response or record is missing. Destructive recovery needs explicit authorization. Production publication belongs in the repository's CI release workflow. A request for local commits does not authorize pushing or publishing.

### What recovery means

A release is recoverable because the next run can read what the last one left. Three rules make that true, and a change
that breaks any of them is a release-safety regression:

1. **A published package is never reported unpublished.** A tag, record, release-commit or push failure after
   publication is reported as a critical diagnostic and the package stays `published`. The run still fails: it exits
   non-zero and the completion webhook reports `release.finished` as `failed`, with the published count preserved.
   A published package with no record is the one state the next run cannot reason about, so the failure is loud.
2. **Durable work survives a neighbour's failure.** A source that committed, tagged and pushed keeps that publication
   when a consumer, a control checkpoint, a fleet link settlement or another repository fails. Dependent work is
   blocked until the recording it needs succeeds, and a retry reads the durable tag and finishes only what is
   unfinished rather than republishing. A settlement that was interrupted left ordinary commits and nothing published,
   so the retry reads what each repository already records and finishes the rest.
3. **A record never outruns the thing it records.** A control checkpoint is not created or pushed for a commit the
   remote does not have, a fleet link settlement is not created or pushed for a revision its target's remote does not
   have, and a tag sitting at a foreign commit is reported and left alone rather than force-moved.

Retry is the supported recovery. It needs no new intent: the same command runs again, reads the tags and records that
already exist, and converges. Destructive recovery, including removing generated state a failed run retained, is the
operator's explicit act and is never something a run does on its own.

The lock is on by default. `unsafeDisableLock` in the configuration and `DISPAT_UNSAFE_DISABLE_LOCK=true` in the
environment are the only ways to switch it off, they warn when they do, and they exist for a repository with no remote
to coordinate through. Neither may become an implicit default.

### Releasing across machines

A run that delegates work to worker nodes holds itself to four further rules, and a change that relaxes any of them is
a release-safety regression:

1. **The complete lock set precedes dispatch.** Every participating repository's lock is acquired before the plan is
   fixed, and ownership is re-verified against the remote before every new assignment and every publication
   authorization. A lock that is gone halts every attempt in flight and starts no new effect.
2. **A lock bypass and worker links are refused together.** `unsafeDisableLock`, a per-repository bypass and
   `DISPAT_UNSAFE_DISABLE_LOCK` each refuse a run that configures workers. The bypass exists for a repository with no
   remote to coordinate through, which is not a run that reaches other machines.
3. **A publication is authorized once.** The node reports itself ready, the run revalidates ownership and inputs, and
   the single-use authorization names the exact state of the branch it authorizes and the instant it expires. The node
   re-reads that branch immediately before the irreversible command. No second authorization is written for an attempt
   in the same run.
4. **An unknown outcome retains its lock.** An authorized publication that never reported back is reported as such,
   never as a failure or a success, and the release lock of the repository it was publishing into is retained when the
   publisher never acknowledged. The run fails, names the order of recovery, and never reports clean cleanup.

Publication happens in CI. The release workflow is the only place that pushes tags, publishes packages or creates
releases. Use `dispat status` to inspect the plan locally. Bare `dispat` and `dispat release` execute release stages;
they are not preview commands. A request for local commits does not authorize a push.

## Tests and evidence

Use a regression test that fails for the original bug and asserts externally meaningful behavior. Prefer integration fixtures for configuration resolution, Git history, locking, dependency propagation, partial releases, and retry behavior. Include cancellation and failure paths, not only successful examples.

### Which suite owns a claim

- **Unit tests** live beside the code, in each module and in `services/dispat/internal/*`. They own the decisions a
  package makes against in-memory fakes: parsing, folding, planning arithmetic, error classification, and the
  boundaries a black-box run cannot reach reliably, such as a corrupt checkout or a filesystem that refuses a write.
- **Integration tests** live in [`tests/integration`](tests/integration). They compile the real binary and drive it
  against disposable Git repositories. They own everything that is only true across a process boundary: exit codes,
  scheduling, real config files on disk, the JSON event stream, and the release protocol end to end.
- **Public-API integration tests** live in `tests/integration/publicapi`. They exercise the published modules the way
  a program outside this repository would, through their exported API rather than through the CLI.
- **Release experiments** live in [`tests/experiments`](tests/experiments). They run whole release protocols against a
  registry a container may break, and record what happened. A recorded failure stays a failure.

Every integration test has one clear goal in the [test plan](tests/integration/docs/test-plan.md), and the
[requirement matrix](tests/integration/docs/qa-requirements.md) maps released behavior to the assertions that
demonstrate it. Reuse fixtures, but do not duplicate scenarios just to increase test counts. Qualify an ambiguous test
name with its repository-relative file, as `path/to/file_test.go::TestName`. Keep requirement-to-test references valid
when moving or renaming tests. `sh scripts/check-test-plan.sh` checks all of that locally, and the `repo-checks` target
of `Dockerfile.gotest` is the same check in CI.

### The coverage gates

Two gates are measured separately and neither substitutes for the other:

- **Combined** statement coverage, the unit and integration profiles merged, is at least 95%.
- **Integration-only** statement coverage is at least 95% across the CLI and the six public Go modules: `pkg/ccme`,
  `pkg/config`, `pkg/manifest`, `pkg/models`, `pkg/scanner`, `pkg/writer` and `services/dispat`. A release may state
  another figure at dispatch (`coverage_minimum_integration`, read by the badge and report gates as
  `COVERAGE_MINIMUM_INTEGRATION`); that is a recorded decision for one release, not a change of the bar.

The integration denominator is a frozen inventory. Every one of those seven modules must appear in it, and a production
package or block missing from the instrumented build is a failure rather than a smaller denominator. Unit coverage is
not integration coverage.

Evidence is bound to a revision. Each profile carries the commit it was measured at, and the gate rejects a missing,
stale, mixed-commit, unexpected or orphan profile before it reports a number. Report the actual denominator, the
revision, and how the tests were run. A documented test is not evidence that it passed.

To measure locally, run the whole suite and then the gate:

```sh
dispat run tests --since all
go run ./tools/testreport coverage -coverage coverage -commit "$(git rev-parse HEAD)" \
  -minimum-total 95 -minimum-integration 95
```

Run targeted tests while editing, then the full required gates before declaring a candidate ready. The [script guide](scripts/README.md) describes the Docker-based test, race, formatting, vet, coverage, and report pipeline. The npm distribution and documentation have their own package tests. Do not publish stale reports or describe untested platform artifacts as verified.

### Performance evidence

A performance claim needs a measurement. Add a Go benchmark that reports the counter the claim is about, not only
nanoseconds: allocations, Git invocations, files read, or whatever the change is supposed to reduce. Record a before
and after table with the same benchmark, the same machine and the same iteration count, and say which revision each
column was taken at. A benchmark that ran in a test pass rather than through `testreport bench` is not a measurement.
An improvement nobody can reproduce from the table is a claim, not evidence.

## Logging

Use trace for detailed event flow, debug for decisions and diagnostic context, info for meaningful user-visible progress and outcomes, warn for recoverable conditions, and error for failures. Avoid duplicate reports of the same error. Include safe repository/package identifiers and stage names so concurrent work can be followed. Redact credentials in remotes, commands, headers, and errors. Never log an entire environment or token-bearing script.

The levels answer different questions, and choosing the wrong one makes a run unreadable:

| Level | What belongs there |
|-------|--------------------|
| `error` | Something failed: a package that could not build or publish, or a record that could not be written after a release was already out. |
| `warn` | Something recoverable that the reader wants to know about. Every `W` diagnostic lives here. |
| `info` | The default, and the story of the run: the plan, which package published at which tag, and how the run ended. Enough to read a CI log and know what shipped. |
| `debug` | How the run decided: which configuration file was read, which folder is the root, which folder each package is scoped to, and the plan's phases. |
| `trace` | Every operation, one line each: every Git command with its arguments and duration, every dependency edge, every package's baseline, window, bump and next version. Verbose on purpose. |

## Documentation and review

Explain the user outcome before implementation details. Use short direct sentences, runnable examples, and descriptive links. Keep README copy slightly formal and the CCME specification formal. Avoid decorative em dashes, unsupported superlatives, and promises of automatic recovery from ambiguous uploads. Any language is supported through configured commands; list built-in manifest support separately.

State repository conventions, design rules, and style requirements directly. Do not attribute them to a user's request, an author's preference, or a conversation. Preserve technical authorship credits and distinguish verified historical outcomes from reported ones.

Update current documentation and examples with behavior changes. Historical version snapshots describe their released binaries and must not be rewritten to advertise unreleased features. Report unresolved findings, incomplete validation, and optional cleanup in the conversation with their impact. A passing suite is evidence for the cases it exercises, not a guarantee that every future release will succeed.
