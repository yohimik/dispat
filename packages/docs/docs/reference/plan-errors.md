# Diagnostic codes

Look up every code dispat reports and see what to do about each one. dispat starts everything from a plan: which
packages changed, what their next versions are, and in what order. A release writes irreversibly to registries, so
dispat refuses to start if it cannot compute a trustworthy plan.

This page covers failures that prevent a trustworthy plan and coded diagnostics that stop or qualify a release. If a
run started publishing before it failed, also see [Recovering from a failed run](./releasing/recovery.md) and
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

## Polyrepository snapshot and recording diagnostics

These codes apply only when [source-history mode](../control-repository.md#source-history-mode) or a
[choreographed fleet](../choreographed-repositories.md) is active. `E330` through `E334`, `E338` and `E339` prevent
publication from an untrustworthy combined snapshot. Initial failures stop the run; drift found in a
package's final pre-publish check fails that package before its publish command and gates its consumers. `E335` reports
a post-publication record failure, `E336` prevents uncoordinated mutation, and `E337` prevents a publish or record path
that needs an unavailable branch. Packages already published and durably recorded remain successful, and failed or
unrecorded providers continue to block dependent work. `W330`, `W332` and `W333` do not stop the run.

| Code | Means | What to do |
|------|-------|------------|
| `E330` | A linked source is missing, uninitialized, shallow, duplicated, outside the workspace, or checked out at a commit other than the control gitlink; or a relevant planned head, release tag, or pin changes before publication. | Initialize every listed submodule, fetch complete history, and check out the exact commit pinned by control `HEAD`. Remove unplanned Git writes from build and hook commands or use the native record step with its exact exported commit. After a partial recording failure, inspect the durable source result before changing the gitlink. |
| `E331` | A package or space crosses repository ownership, a source-local path escapes its source repository, or a package is inside an unlisted nested Git repository. | Keep the package path, `src`, manifests, and release writes inside one listed owner. Check symlinks and the closest Git worktree as well as the paths written in the config. |
| `E332` | Imported declarations conflict, or `repositoryOverrides` does not name one exact `.gitmodules` source identity. | Remove duplicate package/config ownership. Copy the source name exactly, including case, and do not apply a central override to an imported source that owns its own commit policy. |
| `E333` | A cross-repository consumer boundary is missing, ambiguous, conflicting, or unreachable; or an applicable control directive's own gitlink snapshot pins a source revision the active checkout of that source does not contain. | Preserve an ordinary control release checkpoint that identifies the exact consumer tag and matching gitlink transition, or add the required `repositoryBaselines` tuple. Never choose a boundary by date. For the projection case, synchronize the source to include the pin the directive was written against, or select a control revision the checkout already carries. |
| `E334` | Two incomparable source revisions require one semantic winner. | Add an explicit control directive at a commit whose gitlinks observe the source work it is intended to resolve. Dates, traversal order, repository names, and SHA spelling cannot order separate histories. |
| `E335` | A source record, source push, or control gitlink checkpoint failed after publication. | Inspect which source tags and revisions reached their remotes and preserve those successes. If the source is durable but its checkpoint failed, explicitly commit or reconcile the ordinary control gitlink to that revision, or restore the intended pin. Do not republish the source or wait for dispat to create the repair. |
| `E336` | A release lock cannot be acquired or returned, so dispat cannot prove exclusive release ownership is cleanly coordinated. | Stop competing runs. If cleanup failed, inspect the published results and every remote lock before retrying; remove a stranded lock only after confirming its owner has ended. For a fleet, restore the shared lock and local mutation-lock paths in every repository. |
| `E337` | A configured release branch is absent, or a detached source needs a branch push without `commit.branch`. | Create or select the configured existing branch. Set `commit.branch` when a detached checkout must push a release commit; a tag-only or local no-branch-push operation does not need it. |
| `W330` | An `external: true` provider is absent from this snapshot, so the edge is inactive. | Include the provider's source config when it should participate. Otherwise confirm the omission is intentional; the edge activates and receives full validation when the provider is present. |
| `E338` | A [choreographed fleet](../choreographed-repositories.md)'s links do not form a tree: a second route reaches a repository the walk already entered. | Remove one link so exactly one route joins every pair. `dispat compute` proposes the minimum set that connects the roster and never a second route; it reports an existing ring rather than deleting a link, because a link records what a release incorporated. |
| `E339` | A repository identity cannot be trusted to name one participant: `repository` is missing, reserved, malformed, repeated in a roster or self-naming, or a linked checkout contradicts the name it was linked as. | Spell the identity the same in the peer's own `repository` key, in every roster naming it, and as the submodule name linking it. Keep `control` for a central control repository. |
| `W331` | The release lock is switched off by an explicit unsafe setting. The warning names every repository releasing without a lock and which setting asked for it. | Confirm nothing else can release these repositories at the same time. Remove `unsafeDisableLock` and `DISPAT_UNSAFE_DISABLE_LOCK` for any repository that has a remote to coordinate through. In a choreographed fleet, a configuration setting speaks for the repository stating it alone. |
| `W332` | A fleet link is declared by only one of its two repositories. The fleet composes from the declaring end, and a release started at the other end would compose a smaller fleet. | Run `dispat compute --write` from either end. It proposes the missing half and writes the declaration inside the checkout the declaring repository already holds, without fetching anything; commit it in that repository. A declaring repository with no remote has the half withheld with a warning, because the pin has to be a revision the peer can fetch, so add that one by hand. |
| `W333` | A peer's roster does not name every member of the composed fleet, so a release started there plans without them. | Add the missing `repositories` entries to that peer's own configuration, with the url the fleet is fetched from. `dispat compute` writes them into the owning file once a url is known. |

Boundary lookup is lazy for each consumer tag and repository. A tag-only release remains usable when no applicable
control intent affects that package. If an explicit control directive does affect it and must be ordered across the
tag, dispat needs either the ordinary checkpoint association or an explicit tuple whose `repository` is `control`;
otherwise it reports `E333`.

`E333` also covers the opposite mismatch. A control directive is written against the exact source revisions its own
commit pinned, so applying it to a source checkout that does not contain that pin would project the intent onto code
which never carried it. A control repository whose pointer is rewound after the directive landed produces this, and so
does a source clone that has not fetched the pinned revision. dispat reports it only for a package the directive can
actually reach in this run: the commit must still be in that package's pending window, undischarged by its baseline,
and neither cancelled nor held. Differing control and source revisions are ordinary and are not an error by
themselves.

## Distributed execution diagnostics

These codes apply only when [distributed execution](../distributed-execution.md) is configured, which means an
`execution` object with at least one worker link, or a node running `dispat worker`. Each one carries an outcome
category beside the code, in the `category` field of the log line, because the class is what decides how a reader has
to react: `execution-configuration`, `execution-authority`, `io-integrity`, `publication-unknown` and
`transport-cleanup`. Three conditions dispat already reported keep their codes and join the sixth class,
`native-recording-or-lock`: `E220`, `E221` and `E222` for a tag or a record, `E335` and `E336` for a lock.

| Code | Means | What to do |
|------|-------|------------|
| `E225` | A configuration no distributed run could be executed under: an unknown `execution.role`, a capacity below 1, a malformed or credential-carrying `endpoint`, two worker links whose names fold together, a missing or empty signing secret, a release-lock bypass beside `execution.workers`, a `runOnly` that pins a stage to `worker` with no worker links, a package whose `buildPlatforms` no configured node satisfies, two packages whose `buildOutputs` claim one folder, a `runOutputs` root that is or holds a package folder or overlaps a `buildOutputs` root, a `--worker` link breaking any of these rules, or a node that failed preflight. | Fix what the message names. Every one of these is decided before a lock, a plan or a command, so nothing has been published and nothing has to be cleaned up. A node that failed preflight is named with the reason it gave: a protocol version, a capacity, or transfer ceilings below this run's. |
| `E226` | Work refused because of who asked for it: a release or a distributed `dispat run` sweep started on a node whose role is `worker`, a `--worker` link named there or under worker authority, a release or a native record command started by a task script under worker authority, or an assignment a node refused as not authentically this run's (signature, node name, branch, protocol, issue time, or an attempt it has already answered). | Start releases from the orchestrator. In a build script, use the commands a task may run: `exec`, `if`, `for`, `install`, `scanner`, `writer`, `replacer`, `autowriter` and `trigger`. A rejected assignment names the reason as one word; a signature rejection means the nodes do not share one secret. |
| `E227` | Input or output data that cannot be used as it stands: a declared root that is absent, a manifest that disagrees with its tree, a path or a symlink escaping its declared root, a forbidden entry type or mode, a set over `transfer.maxFiles`, `maxBytes` or `maxManifestBytes`, an incompatible platform, a package with nowhere left to run, or two tasks of one `dispat run` sweep writing one path under a `runOutputs` root with different bytes. | Read the reason word in the log. It fails one prerequisite and blocks that prerequisite's consumers, leaving unrelated work alone, so the run's other packages are unaffected. Declare what the build actually writes, keep outputs inside their roots, and raise the ceiling the set exceeded if it is legitimately that large. |
| `E228` | A publication this run authorized and cannot establish the outcome of: the node never reported back, and either acknowledged after its publish command had started or did not acknowledge at all. No second attempt is made in this run, the package's dependents are blocked, and the release lock of the repository it was publishing into is retained when the publisher never acknowledged. | Follow the order the message gives: list the run's `dispat-worker-*` refs in the mailbox, find the authorization with no result beside it, confirm on that node that the publisher has stopped, check the registry for the version, delete the run's refs, and only then delete the lock tag. See [the release lock](./releasing/release-lock.md#a-lock-a-distributed-run-retained). Then run the release again: the next run plans what is still owed. |
| `E229` | Transport state the run could not leave in a safe place: an attempt that had to be fenced, or owned refs whose survival leaves an effect unresolved. | Inspect the named refs in the mailbox before deleting them, then delete them. Nothing about a release record depends on them; what they can leave open is whether an attempt was stopped. |
| `W244` | The harmless half of the same subject: coordination refs a completed run could not delete, and tracked files a task wrote outside its declared `buildOutputs`. Neither erases a release record, so an otherwise clean run still exits `0`. | Delete leftover refs from the mailbox. For stray writes, declare the paths in `buildOutputs` if a consumer needs them, or stop the build writing them: they are carried nowhere. |

## Repository-scoped errors: no correct plan exists

Six diagnostics say the repository is in a state where **any** plan would be wrong. They abort the run regardless of
your `commitErrors` setting. The alternative is releasing something incorrect.

| Code | Means | What to do |
|------|-------|------------|
| `E196` | The clone is shallow or grafted. History is incomplete, and every window and baseline computed over it is silently wrong. Also reported by a release that pushes: the remote it records to holds a release tag on a commit this checkout's head reaches, and this checkout does not have that tag, so the version would be planned and published a second time. A clone made without tags, or made before another run recorded, is the usual cause. | Run `git fetch --unshallow`. For the missing records, run `git fetch --tags` and run again; dispat never fetches them for you, because a run that refreshed its records after reading them would plan from something it never checked. In CI, fetch the full history and its tags. `actions/checkout` needs `fetch-depth: 0`. This is the single most common error on a fresh pipeline. |
| `E191` | Two tags parse to the same version of one package but point at different commits. The baseline is ambiguous. A release that pushes reports it for the same version named at two commits across the two copies as well: the tag in this checkout and the one the remote it records to holds. | Decide which tag is the release and delete the other locally and on the remote. This is usually a hand-made tag colliding with a real one. Neither copy is moved for you: a tag that has been pushed is the record of a publication. |
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

Debug output also includes one `planning workload` event with elapsed time and counts of history reads, commits,
ancestry queries and fleet-link reads. Compare these counts when a larger workspace starts planning slowly.
`canonicalBytes` measures the retained commit payload, not the process's total memory use. These counters are enabled
only at debug or trace level.

Pass `--log-level trace` to add every git command with its arguments and duration. This prints every dependency edge,
baseline, window size, computed bump, and next version. It is verbose on purpose. Attach this level if you open an
issue.

Pass `--log-format json` to turn the diagnostics into machine-readable lines. Each line carries its `code`. Your CI can
act on a specific error rather than grepping prose.
