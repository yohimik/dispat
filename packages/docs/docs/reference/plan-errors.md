# Diagnostic codes

Look up every code dispat reports and see what to do about each one. dispat starts everything from a plan: which
packages changed, what their next versions are, and in what order. A release writes irreversibly to registries, so
dispat refuses to start if it cannot compute a trustworthy plan.

This page covers runs that never start. If a run *did* start and then failed, see
[Recovering from a failed run](./releasing/recovery.md) and
[What to do when something fails](./releasing/steps.md#what-to-do-when-something-fails).

**Start with `dispat status`.** Run this command to compute the exact same plan a release does and print the same
diagnostics. It touches nothing and exits `0` even where a release refuses. Every code below shows up there first, and
you can run it as many times as you like.

## Three ways a run stops

Runs fail at different moments. Knowing which failure you have narrows the search.

| | What happened | Exit |
|---|---|---|
| **Before the plan** | dispat could not read the repository or the config well enough to plan. | `1` |
| **A repository-scoped error** | The plan was computed and is *wrong*: no correct plan exists. | `1` |
| **A unit-scoped error** | One commit or one package is broken, but the rest of the plan is fine. | `0` by default |

The third failure type catches you out in both directions. By default those are **tolerated**, so a typo'd scope warns
and the release proceeds without that package. Set [`commitErrors: "error"`](../configuration/parser.md) to make any
bad commit message stop the run.

## Before the plan

These stop the run before dispat plans anything, or before it reads any history at all. None of them are about your
commits.

**`no dispat config file found in <dir> or any parent directory`**. dispat looked for `dispat.json`, `dispat.yaml`,
`dispat.yml` and `dispat.toml` in `--root` and then in every parent. The error names each file it tried. Change to the
correct directory, or run [`dispat init`](../cli/init.md). Pass an explicit `--config` to fail on a typo instead of
silently loading a different file. This flag never ascends.

**`<dir> is not a git repository root (no .git)`**. Put the config at the repository root. dispat plans from tags and
history. A folder that is not a repository gives dispat nothing to plan from.

**A config that does not load.** dispat rejects unknown keys rather than ignoring them. A typo'd key is otherwise
invisible until a script fails to run. The error names the key, and anything dispat should not validate goes under
[`custom`](../configuration/custom.md).

**`unable to create the release lock tag`**. Another release is running against this repository, or one died without
giving [the lock](./releasing/release-lock.md) back. Check that nothing else is releasing. If you are sure, run
`git push <remote> --delete dispat-release-lock` to fix it. dispat takes the lock before planning, on every release
and whatever flags it was given, because whether there is work to do is not known until after planning. Use
`dispat status --require-release` to ask whether a release would publish anything without touching the lock.

## After the plan, before any releasing

These run once the plan exists and before any hook, build, or publish does. The plan is already computed, so `dispat
status` still prints it where these stop a release.

**`branch "x" is not allowed to release (run.allowBranch: ...)`**. The
[branch guard](../configuration/run-hooks.md#the-branch-guard) is doing its job. A detached HEAD matches no pattern,
including `*`. CI that checks out a commit rather than a branch hits this too, but `dispat status` still works
anywhere.

**`the checkout is behind <remote>/<branch>; pull before releasing`**. Another clone pushed since you fetched. Your
tags are stale and the plan would use an outdated view. Run `git pull --rebase` and try again.

## Repository-scoped errors: no correct plan exists

Six diagnostics say the repository is in a state where **any** plan would be wrong. They abort the run regardless of
your `commitErrors` setting. The alternative is releasing something incorrect.

| Code | Means | What to do |
|------|-------|------------|
| `E196` | The clone is shallow or grafted. History is incomplete, and every window and baseline computed over it is silently wrong. | Run `git fetch --unshallow`. In CI, fetch the full history. `actions/checkout` needs `fetch-depth: 0`. This is the single most common error on a fresh pipeline. |
| `E191` | Two tags parse to the same version of one package but point at different commits. The baseline is ambiguous. | Decide which tag is the release and delete the other locally and on the remote. This is usually a hand-made tag colliding with a real one. |
| `E200` | The dependency graph has a cycle. The message names the edges. | Break the cycle in [`dependencies`](../configuration/dependencies.md). Split the packages if the manifests really depend on each other in a loop. No ordering exists for a cycle. |
| `E195` | A computed version is not greater than the baseline it must exceed. | This is almost always a hand-edited or hand-deleted tag, or a `Release-As` that shipped and left the baseline ahead. See below. |
| `E185` | Graduating a prerelease would go backwards from the baseline it is graduating from. | This happens with hand-edited tags, or when an exact `Release-As` raises a train above the pending window. The pin lives in the baseline tag instead of the window, and graduation computes without it. Pin the graduation with `Release-As` (`Release-As: auto` resumes holds, not this), or restore the removed tag. |
| `E182` | A prerelease baseline has no numeric counter. The train cannot continue. | The tag was written by hand in a shape dispat cannot count from, like `1.0.0-beta` rather than `1.0.0-beta.0`. Tag the next one explicitly with `Release-As`. |

### E195 in practice

This error looks alarming but is usually mundane. The plan wants to release a version that is not above what the
package already published. The baseline and the pending work disagree. Three usual causes:

- **A tag was deleted.** dispat reads baselines from tags. Remove one and the package appears further back than it is,
  and the pending window spans work that already released.
- **A tag was moved or hand-written** at a version above where the commits lead.
- **A prerelease train was interrupted** by a directive that voided the bump that started it. The train's target falls
  below the candidate already published.

The fix is a decision rather than a command. Choose the version this package should ship next and say so with an
explicit `Release-As:` footer on an empty commit. This is a normal, supported move. It discharges the conflict for good
because the tag it creates becomes the new baseline.

## Unit-scoped errors: one thing is broken

These errors name a commit or a package. The default `commitErrors: "warn"` means the offending unit contributes
nothing and **the rest of the run proceeds**. One malformed footer should not hold up four healthy packages. Under
`"error"`, dispat refuses the release and tags nothing. `dispat status` still exits `0` and shows you the plan.

| Code | Means |
|------|-------|
| `E130` | A commit scope *includes* a package that does not exist at HEAD, like `fix(cro):` when the package is `core`. This is an error rather than a warning because a typo silently drops a release. Fix the spelling, or list it in [`nonPackageScopes`](../configuration/parser.md) if the scope is deliberately not a package name. |
| `E140` | A commit type is not in the configured table, and `strictTypes` is on. |
| `E153` | `Release-As` does not move the package forward from its baseline. |
| `E154` | `Release-As` names an exact version, but the scope reaches more than one package. |
| `E156` | `Release-As` is below what the pending commits require. A breaking change cannot ship as a patch. |
| `E157` | `Release-As` raises the major more than one above the computed version. A typo'd major is irreversible, so dispat refuses this by default. |
| `E210` | An `Edits` or `Deletes` target names no commit, names more than one commit, or is not an earlier commit than the correction. |
| `E211` | A correction's unit position is past the end of the target commit, or a bare sha names a commit carrying several units. |
| `E212` | A correction targets a `cancel` or `release` unit. Neither carries a record to correct. |
| `E213` | A correction names a package its target's record does not. Narrowing a record is legal, but widening is not. |

Every rejected `Release-As` falls back to the ordinarily computed version rather than swallowing the commit. A sibling
`feat` in the same commit still releases. The four correction errors work the same way. The correction contributes
nothing and the record it aimed at stands untouched. See [Correcting a release record](./corrections.md).

Notice the asymmetry between including and excluding an unknown name. Including one triggers `E130`. **Excluding** one
triggers `W130`, a warning. A scope that excludes a deleted or renamed package is harmless and common. The include
changes what ships.

## A step command inside a run

[Step commands](./releasing/steps.md#four-rules-that-make-them-safe) invoked from a stage script replan. The replan
must align to the run that invoked them. Two codes belong to that alignment.

| Code | Means |
|------|-------|
| `E219` | The step cannot align to the run. The package is missing from its plan, or the run's version renders a different tag. dispat writes nothing. A failed leg re-runs, but a drifted record does not. |
| `W229` | A wired `dispat github` ran before the run's tag existed. GitHub would invent the tag at the default branch head. Run the commit step first. |

## A record that renders less than it was asked to

Records are written after the plan is settled and, for a GitHub release, after the package is already published. What a
record's own configuration could not do is therefore reported rather than refused, and the entry is published in the
shape dispat could actually render.

| Code | Means |
|------|-------|
| `W240` | [Commit references](../configuration/records.md#naming-the-commit-behind-a-line) are configured, and some of the entry's lines have no commit id to point at. Those lines render without a reference instead of with one that resolves nowhere. The warning names the package and how many lines it covers, once per release and per record. |
| `W241` | [`noChangesText`](../configuration/records.md#what-an-entry-with-no-sections-says) is configured, and it expanded to nothing or to whitespace alone. The entry carries the built-in line naming the release's cause instead. The substitution is invisible in the file itself, which is why it is reported once per release and per record. |
| `W242` | The release push was refused because commits landed on the branch while the run was working. dispat pulled them and merged them with its release commit, which it left exactly as it was, then pushed the merge. The release is still the one that was planned: the commits that arrived are outside the tag's ancestry, carry no record here, and are released by the next run. See [When somebody pushes while the release runs](./releasing/recovery.md#when-somebody-pushes-while-the-release-runs). |
| `W243` | The release push was refused because commits landed on the branch while the run was working, and those commits changed the same content the release did. The release still completes: this release's side of every conflicting file is what the branch keeps, the other side is pushed to a `release-conflicts/...` branch of its own, and the changelog and the GitHub release both name the files and that branch. Somebody has to reconcile the two sides. See [When somebody pushes while the release runs](./releasing/recovery.md#when-somebody-pushes-while-the-release-runs). |

An `auto` link that cannot be derived is the quieter member of the same family. It falls back to the plain unlinked
text and says so at `debug` level, because a repository with no GitHub coordinates configured is a normal thing to be
rather than something to warn about on every release.

## The plan is fine and nothing releases

These are not errors, but they read like errors.

- **`no pending changes`**. Nothing addressed any package since the last tag. `dispat status` shows every package as
  `unchanged`.
- **A commit that touched only ignored files** reports `W131` and releases nothing. See
  [what counts as a change](../configuration/change-scope.md).
- **A selection that reaches nothing.** Run `dispat release -p core` when core has no pending work, and dispat releases
  nothing and exits `0`. The filter narrows the window without replacing it. Pass `--since all` to reach an unchanged
  package.
- **`W230` / `W231`**. The selection is releasable but incomplete. A consumer is withheld because its provider was left
  out, or a versioning group is being split. Both release what they can and warn. Pass `--strict` to refuse the release
  before anything builds. See [Partial releases](./releasing/partial-releases.md).
- **A package of a `versioning: none` space.** This package never releases, regardless of what it has pending. The
  graph shows it as `script-only (versioning: none)`. A `Release-As` aimed at it warns `W238` and moves nothing. See
  [Packages that never release](./releasing/versioning.md#packages-that-never-release-none).

## Getting more out of the failure

Pass `--log-level debug` to see how the run decided. This shows which config file dispat read, which folder it treated
as the monorepo root, and which folder each package is scoped to. It also prints the plan's phases as dispat works
through them.

Pass `--log-level trace` to add every git command with its arguments and duration. This prints every dependency edge,
baseline, window size, computed bump, and next version. It is verbose on purpose. Attach this level if you open an
issue.

Pass `--log-format json` to turn the diagnostics into machine-readable lines. Each line carries its `code`. Your CI can
act on a specific error rather than grepping prose.
