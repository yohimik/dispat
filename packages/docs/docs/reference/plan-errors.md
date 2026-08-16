# Diagnostic codes

Every code dispat can report, and what to do about each one. Everything dispat does starts from a plan: which packages
changed, what their next versions are, and in what order. When the plan cannot be computed, or can be computed but is
not trustworthy, nothing runs at all. That is deliberate, because a release is a set of irreversible writes to
registries, so the moment to refuse is before the first one rather than halfway through.

This page is mostly about a run that never got started. For a run that *did* start and then something went wrong, see
[Recovering from a failed run](./releasing/recovery.md) and
[What to do when something fails](./releasing/steps.md#what-to-do-when-something-fails).

**Start with `dispat status`.** It computes exactly the same plan a release does and prints the same diagnostics, but
it touches nothing and exits `0` even where a release would refuse. Every code below shows up there first, and you can
run it as many times as you like.

## Three ways a run stops

They fail at different moments, and knowing which one you are looking at narrows the search a lot.

| | What happened | Exit |
|---|---|---|
| **Before the plan** | dispat could not read the repository or the config well enough to plan at all. | `1` |
| **A repository-scoped error** | The plan was computed and is *wrong*: no correct plan exists. | `1` |
| **A unit-scoped error** | One commit or one package is broken; the rest of the plan is fine. | `0` by default |

The third is the one to know about, and it catches you out in both directions: by default those are **tolerated**, so a typo'd scope
warns and the release goes ahead without that package. If you would rather any bad commit message stop the run, set
[`commitErrors: "error"`](../configuration/parser.md).

## Before the plan

These fail before any history is read, and none of them are about your commits.

**`no dispat config file found in <dir> or any parent directory`**. dispat looked for `dispat.json`, `dispat.yaml`,
`dispat.yml` and `dispat.toml` in `--root` and then in every parent, and the error names each one it tried. Either
you are in the wrong directory, or you want [`dispat init`](../cli/init.md). An explicit `--config` never ascends, so
a typo there fails instead of silently loading a different file.

**`<dir> is not a git repository root (no .git)`**. The config belongs at the repository root. dispat plans from
tags and history, so a folder that is not a repository has nothing to plan from.

**A config that does not load.** Unknown keys are rejected rather than ignored, because a typo'd key is otherwise
invisible until a script that should have run never does. The error names the key. Anything dispat should not
validate goes under [`custom`](../configuration/custom.md).

**`branch "x" is not allowed to release (run.allowBranch: ...)`**. The [branch guard](../configuration/run-hooks.md#the-branch-guard)
is doing its job. A detached HEAD matches no pattern, `*` included, so CI that checks out a commit rather than a
branch hits this too. `dispat status` still works anywhere.

**`the checkout is behind <remote>/<branch>; pull before releasing`**. Another clone has pushed since you fetched,
so your tags are stale and the plan would be computed from an outdated view. `git pull --rebase` and run again.

**`unable to create the release lock tag`**. Another release is running against this repository, or one died without
giving [the lock](./releasing/release-lock.md) back. The message carries the remedy: if you are sure nothing else is
releasing, `git push <remote> --delete dispat-release-lock`.

## Repository-scoped errors: no correct plan exists

Six diagnostics say the repository itself is in a state where **any** plan would be wrong. They abort the run
whatever `commitErrors` says, because the alternative is releasing something incorrect.

| Code | Means | What to do |
|------|-------|------------|
| `E196` | The clone is shallow or grafted, so history is incomplete and every window and baseline computed over it is silently wrong. | `git fetch --unshallow`. In CI, fetch the full history: `actions/checkout` needs `fetch-depth: 0`. This is the single most common one on a fresh pipeline. |
| `E191` | Two tags parse to the same version of one package but point at different commits, so the baseline is ambiguous. | Decide which one is the release and delete the other, locally and on the remote. Usually a hand-made tag colliding with a real one. |
| `E200` | The dependency graph has a cycle; the message names the edges. | Break the cycle in [`dependencies`](../configuration/dependencies.md). If the manifests really do depend on each other in a loop, the packages need splitting; no ordering exists for a cycle. |
| `E195` | A computed version is not greater than the baseline it must exceed. | Almost always a hand-edited or hand-deleted tag, or a `Release-As` that shipped and left the baseline ahead. See below. |
| `E185` | Graduating a prerelease would go backwards from the baseline it is graduating from. | Reachable only from hand-edited tags. Pin the intended version with `Release-As`, or restore the tag that was removed. |
| `E182` | A prerelease baseline has no numeric counter, so the train cannot be continued. | The tag was written by hand in a shape dispat cannot count from (`1.0.0-beta` rather than `1.0.0-beta.0`). Tag the next one explicitly with `Release-As`. |

### E195 in practice

This is the one that looks most alarming and is usually the most mundane. The plan wants to release a version that is
not above what the package has already published, which means the baseline and the pending work disagree. Three usual
causes:

- **A tag was deleted.** dispat reads baselines from tags; remove one and the package appears to be further back than
  it is, while the pending window now spans work that was already released.
- **A tag was moved or hand-written** at a version above where the commits lead.
- **A prerelease train was interrupted** by a directive that voided the bump that started it, so the train's target
  falls below the candidate already published.

The fix is a decision, not a command: choose the version this package should ship next and say so with an explicit
`Release-As:` footer on an empty commit. That is a normal, supported move, and it discharges the conflict for good
because the tag it creates becomes the new baseline.

## Unit-scoped errors: one thing is broken

These name a commit or a package. Under the default `commitErrors: "warn"` the offending unit contributes nothing and
**the rest of the run proceeds**, which is usually what you want: one malformed footer should not hold up four healthy
packages. Under `"error"` the release is refused with nothing tagged, while `dispat status` still exits `0` and shows
you the plan.

| Code | Means |
|------|-------|
| `E130` | A commit scope *includes* a package that does not exist at HEAD: `fix(cro):` when the package is `core`. An error rather than a warning because a typo here silently drops a release. Either fix the spelling, or, if the scope is deliberately not a package name, list it in [`nonPackageScopes`](../configuration/parser.md). |
| `E140` | A commit type is not in the configured table, and `strictTypes` is on. |
| `E153` | `Release-As` does not move the package forward from its baseline. |
| `E154` | `Release-As` names an exact version but the scope reaches more than one package. |
| `E156` | `Release-As` is below what the pending commits require: a breaking change cannot ship as a patch. |
| `E157` | `Release-As` raises the major more than one above the computed version. A typo'd major is not undoable, so this is refused by default. |
| `E210` | An `Edits` or `Deletes` target names no commit, names more than one, or is not an earlier commit than the correction. |
| `E211` | A correction's unit position is past the end of the target commit, or a bare sha names a commit carrying several units. |
| `E212` | A correction targets a `cancel` or `release` unit, neither of which carries a record to correct. |
| `E213` | A correction names a package its target's record does not. Narrowing a record is legal; widening is not. |

Every rejected `Release-As` falls back to the ordinarily computed version rather than swallowing the commit, so a
sibling `feat` in the same commit still releases. The four correction errors work the same way: the correction
contributes nothing and the record it aimed at stands untouched. See
[Correcting a release record](./corrections.md).

Note the asymmetry between including and excluding an unknown name. Including one is `E130` above; **excluding** one
is `W130`, a warning, because a scope that excludes a package somebody has since deleted or renamed is harmless and
common. The include is the one that changes what ships.

## A step command inside a run

[Step commands](./releasing/steps.md#two-rules-that-make-them-safe) invoked from a stage script replan, and the
replan must align to the run that invoked them. Two codes belong to that alignment.

| Code | Means |
|------|-------|
| `E219` | The step cannot align to the run: the package is missing from its plan, or the run's version renders a different tag. Nothing is written, because a failed leg re-runs where a drifted record does not. |
| `W229` | A wired `dispat github` ran before the run's tag existed. GitHub would invent the tag at the default branch head, so the commit step belongs first. |

## The plan is fine and nothing releases

Not an error, and worth naming because it reads like one.

- **`no pending changes`**. Nothing since the last tag addressed any package. `dispat status` shows every package as
  `unchanged`.
- **A commit that touched only ignored files** reports `W131` and releases nothing; see
  [what counts as a change](../configuration/change-scope.md).
- **A selection that reaches nothing.** `dispat release -p core` when core has no pending work releases nothing and
  exits `0`. The filter narrows the window, it does not replace it; `--since all` is what reaches an unchanged
  package.
- **`W230` / `W231`**. The selection is releasable but incomplete: a consumer is withheld because its provider was
  left out, or a versioning group is being split. Both release what they can and warn. `--strict` turns either into a
  refusal before anything is built; see [Partial releases](./releasing/partial-releases.md).
- **A package of a `versioning: none` space.** It is never released, whatever it has pending: the graph shows it as
  `script-only (versioning: none)`, and a `Release-As` aimed at it warns `W238` and moves nothing. See
  [Packages that never release](./releasing/versioning.md#packages-that-never-release-none).

## Getting more out of the failure

`--log-level debug` says how the run decided: which config file was read, which folder it treated as the monorepo
root, which folder each package is scoped to, and the plan's phases as it works through them.

`--log-level trace` adds every git command with its arguments and duration, every dependency edge, and every
package's baseline, window size, computed bump and next version, releasing or not. It is verbose on purpose. If you
are opening an issue, this is the level to attach.

`--log-format json` turns the diagnostics into machine-readable lines, each carrying its `code`, so CI can act on a
specific one rather than grepping prose.
