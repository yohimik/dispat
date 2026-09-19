# Release experiments

These experiments exercise partial releases against disposable Git repositories and a real local npm registry.
The faults cover a rejected package upload, a concurrent branch update, and a consumer failure after its provider
publishes. Each protocol records the clone, remote Git refs, and registry state before and after recovery.
Dispat runs as either a shipped binary or an explicitly identified candidate; comparison tools use pinned versions.

The runs are records first and verdicts second. For dispat the expectations decide the exit code; for lerna, nx and
changesets they are recorded either way, so a row that reads "3/5" is a description of that tool, not a failure of the
run.

The measured results are published with the documentation site at
[dispat.dev/internals/experiments/](https://dispat.dev/internals/experiments/), rerun by every release against the
image that release just published.

## What runs

```
run.sh <experiment> <tool> [scenario]
```

| Experiment   | The fault                                                                                                   | Scenarios                                                                  |
|--------------|-------------------------------------------------------------------------------------------------------------|----------------------------------------------------------------------------|
| `orphan`     | The registry answers 502 to `cli`'s upload while `core`, `ui` and `api` publish.                            | none                                                                       |
| `midrelease` | A colleague's commit lands on `origin/main` right before the tool's own first push of the branch.           | `clean` (touches `api`, no overlap); `conflict` (edits `core/package.json` next to the version line) |
| `propagation` | `core` publishes, then `cli` fails; the fault is removed without another source change before retry.       | `build` (`cli`'s build script fails); `publish` (the registry rejects `cli`'s upload)                           |

Tools: `lerna` 10.0.1, `nx` 23.1.2, `changesets` 3.0.1, `dispat` at the version the image was built from.

The fixture is six packages: `cli`, `ui` and `api` depend on `core`; `theme` and `docs` depend on `ui`. All start at
1.0.0, tagged
`<name>@1.0.0`, published to a registry that starts inside the container, and pushed to a bare origin beside the clone.
One pending change, a minor to `core`, is committed the way each tool reads it: a conventional commit, or a changeset
file for changesets. Dependencies are tilde ranges, so the minor reaches `core`'s consumers under every tool. Every
commit is dated from a fixed clock, so two runs of one cell produce the same shas and two transcripts diff against
each other.

The baseline tags are annotated. `git describe` ignores a lightweight tag, and lerna reads its previous release
through `git describe`, so a lightly tagged baseline is a baseline lerna cannot see: it then reports no previous
release, assumes every package changed, and the fixture rather than the tool decides what a run releases.

Every package carries one `build` script, which writes its `dist` output from its source. dispat's build stage runs it
through the fixture's `dispat.yaml`, and the propagation protocol runs the same script under Lerna with `lerna run
build`, so a build failure there is one fault observed through two tools rather than two faults that happen to share a
name. The nx and changesets protocols run no build of their own, and the script is inert for them.

### The propagation cells

The propagation cells use a patch to `core` that remains inside the consumers' `~1.0.0` range, so semver alone asks
for no consumer release at all. Dispat receives `fix(core)^: correct reader`, whose caret is explicit propagation
intent; Lerna receives the plain `fix(core): correct reader` and decides for itself. Nothing is forced on Lerna's
side, and what its version command selects is part of the record.

What the two record is different. Dispat releases `core` and its three declared consumers, and writes each consumer's
range up to the provider it was released with. Lerna releases all six: it bumps every transitive dependent of a
changed package, `theme` and `docs` included, although `~1.0.0` already accepted `core@1.0.1`.

The provider is published, tagged and observed before the fault is injected. Under Lerna the protocol does this
directly, publishing `core` through `lerna exec` and observing the registry before it arms the fault. Under dispat one
command runs the whole release, so the fixture sets `isBuildWaitingPublish: true` on `core`: that setting belongs to
the provider, and it holds every consumer's build until `core` has been published. The protocol then reads the
ordering back out of the run's own log rather than assuming it.

The `build` scenario fails `cli`'s build script, and the protocol records that `cli` never reached a publication
attempt at all. The `publish` scenario lets the build succeed and has the registry refuse `cli`'s upload. A fault in
an npm publish lifecycle hook would fail the publication under both names, which is why the fault lives in the build
script and the assertions name the stage.

After the fault is removed, the protocol adds no commit and supplies no new release intent. Dispat's next plan
contains the owed `cli` catch-up alone, its recovery publishes `cli` without republishing `core`, and the plan after
it is empty. Lerna's tag-based `lerna changed` is already empty, because its version command tagged every package
before any of them was published; `lerna publish from-package` then queries the registry and republishes the five
packages the registry is missing, not the one that failed.

### The colleague's push

The colleague is a second clone of the origin. A shim placed first on `PATH` for the duration of the tool's release
command records every git call the tool makes, and fires the colleague's commit and push once, right before the first
`git push` that is neither the release lock's nor a commit. dispat's push of its release lock precedes the plan and is
excluded by name; a `git commit` never fires the injection, because the shim reads the git subcommand rather than the
whole command line.

The interleaving is therefore a point in each tool's own sequence rather than a timer, the same every time and the
same for every tool. Where in the release that point falls differs by tool, and that difference is part of the record:

- **lerna** commits and tags, then pushes the branch and the tags together, and publishes afterwards.
- **nx** pushes before it commits or tags, leaving the version bumps staged in the index when the push is refused.
- **changesets** makes no push of its own: it versions, commits, publishes and tags, and the push is the operator's,
  so the injection fires on the command the protocol runs rather than on anything the tool does.
- **dispat** publishes first and pushes last, so the release exists on the registry and in this clone alone when the
  branch refuses it.

After the tool's run, the experiment performs the recovery an operator is left with, `git pull --rebase` and
`git push --follow-tags` for the tools that leave a refused push behind (taking the release's side of a conflict), and
then asks each tool what it would release next: `lerna changed`, `nx release --dry-run`, `changeset status`,
`dispat status`. For dispat it then runs the release again from the same clone.

dispat's orphan fixture opts `cli` into `revertOnFail`. The protocol verifies that the refused package's version
rewrite is rolled back and the worktree is clean before retrying. This exercises the configured automatic recovery
path; without that opt-in, dispat deliberately preserves the failed rewrite for inspection and its dirty-path safety
check requires an operator to resolve it before another release.

### What is observed

After every step, `lib/observe.py` reads the clone, the origin and the registry and joins them into one state per
package:

| State        | Meaning                                                                            |
|--------------|------------------------------------------------------------------------------------|
| `consistent` | the registry's version is tagged on origin and the tag is reachable from `main`    |
| `orphan`     | a tag names a version the registry does not hold                                   |
| `unpushed`   | the tag exists in the local clone only                                             |
| `dangling`   | the tag is on origin but outside `main`'s ancestry                                 |
| `unrecorded` | the registry serves a version no tag names                                         |
| `baseline`   | nothing beyond the fixture's 1.0.0                                                 |

The `dangling` state is what a rebase after a refused push produces: the release commit is rewritten, the tags stay on
the original, and the next plan of a tag-driven tool sees everything as changed again.

Each package's row also carries the manifest the clone's working tree holds at that moment, its version and its
dependency ranges. The registry says what shipped and the tags say what was recorded; neither says what the checkout
now claims about its providers, and a reconciled range or a rolled back version rewrite is visible nowhere else. That
reading is what the propagation cells assert their range claims from.

Nothing is written into the repositories under test. The observer builds a scratch bare repository beside the fixture
at each observation and fetches both sides into it under separate ref namespaces, so the origin's refs and the clone's
are distinguishable and neither the clone nor the origin is touched. An observer that fetched into the clone would
give the next `git pull --rebase` of a recovery a fetch it never performed.

## Running

The experiments are the `experiments` package's scripts in the root `dispat.yaml`, and the results land in
`coverage/experiments/<cell>/` at the repository root, beside the coverage profiles a test run leaves. Nothing under
this folder is written to: a run is reproduced, not kept.

```sh
# every cell, four at a time, into coverage/experiments/
EXPERIMENTS_DISPAT_VERSION=1.7.1 dispat exec experiments --for pkg:experiments --in pkg:experiments

# one cell, the by-hand form
EXPERIMENT=midrelease TOOL=dispat SCENARIO=conflict \
  dispat exec experiment --for pkg:experiments --in pkg:experiments

# the table, to the terminal and to a job summary
dispat exec summary --for pkg:experiments --in pkg:experiments

# every campaign cell, one `<experiment> <tool> [scenario]` per line
dispat exec cells --for pkg:experiments --in pkg:experiments
```

`build` takes the version from `EXPERIMENTS_DISPAT_VERSION`, or the newest `docker/dispat-alpine/v*` tag when it is
unset. To measure an unpublished candidate, set `EXPERIMENTS_DISPAT_ARTIFACT` to its executable and
`EXPERIMENTS_DISPAT_ID` to an explicit identity such as a full commit plus build target. Candidate mode records that
identity and the executable's computed SHA-256 in every verdict; shipped-image mode remains the default. `EXPERIMENTS_RESULTS` moves the
results folder, `EXPERIMENTS_JOBS` sets how many cells run at once (four), and `EXPERIMENTS_EXPECT=0` records a dispat
cell without gating on its verdict.

The release workflow restores the newest records for the current harness. The docs package's `beforeBuild` hook
reuses them only when the experiments image id is identical, the manifest names the exact current cell list, every
verdict and observation stream parses, and every dispat verdict passed. The image id covers the published native
binary, pinned comparison tools and harness. A version or protocol change therefore reruns every cell, as does
any missing, corrupt, partial or failed record. The `Experiments` workflow remains the by-hand run for any released
version; its matrix is `cells` read at the start of the run.

The campaign is whatever `cells` prints and nothing else counts it. The sweep pipes that list into `xargs`, the
workflow turns it into its matrix, the cache manifest stores it verbatim, and the report's headline counts the records
it found. A cell added to `cells` is therefore a cell every one of them runs, with no number to update anywhere.

Underneath, one cell is one container:

```sh
docker buildx build --file tests/experiments/Dockerfile \
  --build-arg DISPAT_VERSION=1.7.1 --load -t dispat-experiments tests/experiments
docker run --rm -v "$PWD/coverage/experiments:/results" dispat-experiments midrelease dispat conflict
```

The binary under test is copied out of the published `yohimik/dispat-alpine:<version>` image, so the experiments run
the exact bytes a release shipped and never a build of the checkout. The other tools are installed into the image from
a committed lockfile at their pinned versions; no phase installs anything over the experiment registry.

Each run leaves `coverage/experiments/<experiment>[-<scenario>]-<tool>/` with the transcript, every step's output, one
observation per step, every failed expectation's own output, the git calls the tool made (`git-calls.log`, one JSON
object per line, with the injection marked), and `verdict.json`. Mount the folder over `/exp`
(`-v "$PWD/tests/experiments:/exp:ro"`) to iterate on the scripts without rebuilding.

## Layout

```
Dockerfile         the runtime: dispat from its image, the other tools, verdaccio, python
tools/             the compared tools' manifest and lockfile, installed with npm ci
run.sh             entrypoint: one experiment, one tool, one fresh registry
lib/common.sh      step, observe, assert, verdict; the registry; the colleague and the shim
lib/fixture.py     the six-package fixture in one flavour per tool
lib/observe.py     the state reader, through a scratch observer repository
lib/git-shim       the recording, injecting git
lib/failproxy.py   the 502-injecting reverse proxy in front of verdaccio
orphan/<tool>.sh   the orphan protocol per tool
midrelease/<tool>.sh
propagation/{dispat,lerna}.sh
```
