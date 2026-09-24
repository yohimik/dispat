# Release experiments

These experiments exercise partial releases against disposable Git repositories and a real local npm registry.
The faults cover a rejected package upload, a concurrent branch update, and a consumer failure after its provider
publishes. Each protocol records the clone, remote Git refs, and registry state after every step, and counts what the
tool needed to finish a release its first run left partial. dispat runs as either a shipped binary or an explicitly
identified candidate; comparison tools use pinned versions.

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

| Experiment    | The fault                                                                                         | Scenarios                                                                                                    |
|---------------|---------------------------------------------------------------------------------------------------|--------------------------------------------------------------------------------------------------------------|
| `orphan`      | The registry answers 502 to `cli`'s upload while `core`, `ui` and `api` publish.                  | none                                                                                                         |
| `midrelease`  | A colleague's commit lands on `origin/main` right before the tool's own first push of the branch. | `clean` (touches `api`, no overlap); `conflict` (edits `core/package.json` next to the version line)         |
| `propagation` | `core` publishes, then `cli` fails; the fault is removed without another source change.           | `build` (`cli`'s build script fails) and `publish` (the registry refuses `cli`'s upload) under every tool; under dispat alone `build-distributed`, `deselected`, `held`, `deferred` and `deferred-build`, described below |

Tools: `lerna` 10.0.1, `nx` 23.1.2, `changesets` 3.0.1, `dispat` at the version the image was built from.

The fixture is six packages: `cli`, `ui` and `api` depend on `core`; `theme` and `docs` depend on `ui`. All start at
1.0.0, tagged `<name>@1.0.0`, published to a registry that starts inside the container, and pushed to a bare origin
beside the clone. The orphan and midrelease fixtures begin with one pending minor change to `core`, committed the way
each tool reads it: a conventional commit, or a changeset file for changesets. Dependencies are tilde ranges, so this
minor reaches `core`'s consumers under every tool. The propagation fixtures instead use a patch, described below. Every
commit is dated from a fixed clock, so two runs of one cell produce the same shas and two transcripts diff against
each other.

The baseline tags are annotated. `git describe` ignores a lightweight tag, and lerna reads its previous release
through `git describe`, so a lightly tagged baseline is a baseline lerna cannot see: it then reports no previous
release, assumes every package changed, and the fixture rather than the tool decides what a run releases.

Every package carries one `build` script, which writes its `dist` output from its source. The propagation fixtures run
it where each tool documents a build: dispat's build stage through the fixture's `dispat.yaml`, nx's
`release.version.preVersionCommand` (`npx nx run-many -t build`), and the `prepack` script that `lerna publish` and
`changeset publish` run for every package they pack. A build failure there is therefore one fault observed through four
tools rather than four faults that happen to share a name. The orphan and midrelease fixtures run no build under
lerna, nx or changesets. The nx fixture ignores `.nx`, where nx keeps its cache and workspace data, as `nx init` sets
a workspace up.

### The propagation cells

The `build` and `publish` cells use a patch to `core` that remains inside the consumers' `~1.0.0` range, so semver
alone asks for no consumer release at all. dispat receives `fix(core)^: correct reader`, whose caret is explicit
propagation intent; lerna and nx receive the plain `fix(core): correct reader` and decide for themselves. Nothing is
forced on their side. changesets receives explicit patch entries for `core`, `cli`, `ui` and `api`, which request the
same direct-consumer release set without pretending it reads the caret syntax.

The fault is armed before the first run, and the first run is each tool's own documented release: `dispat release`;
`lerna version --conventional-commits` and then `lerna publish from-git`; `nx release`; `changeset version`, a commit
of what it wrote and then `changeset publish`. Nothing is published by hand and no tool is handed an order: every
package a tool planned is attempted by that tool. The `build` scenario fails `cli`'s build script, which leaves
`/fault-fired` behind when it runs; the `publish` scenario lets the build pass and has the registry refuse `cli`'s
upload. A cell whose fault never fired is not a record, and the harness fails it rather than writing a verdict.

Where each tool builds decides what its first run publishes, and the records show it. dispat's fixture sets
`isBuildWaitingPublish: true` on `core`, which holds every consumer's build until the provider is published, and the
protocol reads that order back out of the run's log. lerna runs every package's `prepack` before it publishes any, so
one failed build publishes nothing. nx runs its pre-version command before it versions, so one failed build versions
and publishes nothing. changesets publishes the packages it finds missing side by side, so the provider and the other
consumers publish while `cli` fails. Under a refused upload, every tool's first run publishes the provider.

The release sets differ as well. dispat releases `core` and its three declared consumers, and writes each consumer's
range up to the provider it was released with. lerna and nx release all six: they bump every transitive dependent of a
changed package, `theme` and `docs` included, although `~1.0.0` already accepted `core@1.0.1`.

The fault is then removed with no commit and no new release intent, and the catch-up is each tool's documented
recovery. dispat's is the same command again. lerna's is `lerna changed` and then `lerna publish from-package`, which
publishes whatever the registry is missing; when that refuses the working tree its own failed publish left rewritten,
the protocol checks the manifests out by hand and runs it again. nx's is `nx release --dry-run`, then
`nx release publish` for what the first run versioned or `nx release` again when it versioned nothing, and a push by
hand when the release left its commit on the clone alone, because nx 23.1.2 pushes before it commits in this fixture.
changesets' is `changeset status`, `changeset publish` again, and the push changesets never makes.

The dispat-only cells take the same catch-up through the other ways a consumer comes to be owed its provider:

- `build-distributed` is the `build` fault with every build on a worker node, `build-a`, started in the container.
  The fixture sets `runOnly: [worker, orchestrator]` and names the signing secret's variable; every dispat command
  links the worker with `--worker build-a` and no endpoint, so the fixture's own origin is the mailbox, and the worker
  names that origin as its `execution.endpoint`. The outcomes are the local cell's, and the log names `build-a` in the
  task outcome of `cli`'s build.
- `deselected` has no fault: the first run is `dispat release --package core,ui,api`, so `cli` sits the provider's
  release out, and the next full run must find what it is owed from the provider's release alone.
- `held` has no fault: an empty `release(cli): hold` commit with `Release-As: none` precedes the fix, so the first run
  ships `core` while `cli` is held (`W154`). The operator's resume, an empty `release(cli): resume` commit with
  `Release-As: auto`, is a manual step of the first phase, and the catch-up follows it.
- `deferred` gives `cli` a fix of its own and no build. The registry refuses `core` while `cli` publishes its fix
  against the old provider; the manifests the failed run left are checked out by hand, one empty commit moves the head
  past `cli`'s release (a provider released alone on its owed consumer's own release commit is refused with `E201`),
  and `dispat release --package core,ui,api` ships the provider without `cli`. The catch-up must then find what `cli`
  is owed although its own tag already contains the propagation commit.
- `deferred-build` keeps `cli`'s build. The proxy holds `core`'s refused upload until `cli`'s build has written
  `dist/index.js`, so `cli` has always built against the provider's planned version when the provider fails, and
  dispat withholds it with `W194`. The catch-up builds and publishes it against the version that shipped.

### The catch-up

Every step a protocol runs is typed, `step <kind>:<name> <command...>`: `release` is one of the tool's own release
commands, `manual` is anything done by hand (a commit, a push, a checkout, a reset, a publish or build outside the
tool), and `query` is a read-only question such as `dispat status`, `lerna changed`, `nx release --dry-run` or
`changeset status`. A step without a kind ends the run before its command runs. `catch_up` moves the protocol from the
`initial` phase into the `catch-up` phase once the fault is gone, and every step is appended to `steps.jsonl` as
`{step, exit, kind, phase, seconds}`.

`lib/recovery.jq` summarises the catch-up phase into the verdict's `recovery` object:

| Field             | What it says                                                                                    |
|-------------------|-------------------------------------------------------------------------------------------------|
| `runs`            | uninterrupted passes of the tool's own commands; a manual step or a failed command ends one     |
| `releaseCommands` | the tool's own release commands                                                                 |
| `manualCommands`  | the steps done by hand                                                                          |
| `converged`       | whether the release ended settled                                                               |

A query is never counted. A release ended settled when the last observation has every package consistent or at its
baseline, the clone clean and level with its origin, and the tool's own next plan empty. The next plan is the tool's
answer to the last question of the catch-up: `dispat status --require-release` exits 3, `lerna changed` exits 1 and
lists no package, nx writes no new version, and changesets lists no package to bump. A protocol without a catch-up
phase writes no `recovery`, and the report reads it as not measured: the midrelease protocols are typed but measure
none, the orphan protocols measure the recovery after the refused upload, and every propagation protocol measures its
catch-up.

The propagation cells hold every tool to the same sentences. For the first run of a fault cell: the provider shipped
in the first run, the consumer did not ship in the first run, and the first run reported the failure. For the
catch-up: the plan before the catch-up named the owed consumer, the catch-up took one run, the catch-up took no manual
step, the consumer shipped at the version first planned, the provider was published once (exactly one accepted upload
of `core` after the baseline, read from the proxy's own log), and the release converged. dispat's cells add checks on
the run's structured log (the order of the provider's publication and the consumer's build, the stage that failed, a
catch-up plan of `cli` alone as `W193` at the version it is owed) and on the manifests (the consumer's range caught up
to `^1.0.1`, `theme` and `docs` untouched), and the per-cell facts above.

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

The registry is read through the fault proxy's own decision log as well: every request, the package it names, and the
status it ended in. The count of accepted uploads behind "the provider was published once" is read from it, counting
from the line the baseline publication ended at, and so is the refusal that proves a publish fault fired. A refusal
the protocol gated names its gate in the same line, `DENY PUT /core (package core) after <path> -> 502`, or says the
gate timed out.

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

Each run leaves `coverage/experiments/<experiment>[-<scenario>]-<tool>/` with the transcript, every step's output and
`steps.jsonl`, one observation per step, every failed expectation's own output, the registry's and the proxy's logs,
the git calls the tool made (`git-calls.log`, one JSON object per line, with the injection marked), the worker's log
for the distributed cell, `recovery.json` when a catch-up phase ran, and `verdict.json`. Mount the folder over `/exp`
(`-v "$PWD/tests/experiments:/exp:ro"`) to iterate on the scripts without rebuilding.

## Layout

```
Dockerfile         the runtime: dispat from its image, the other tools, verdaccio, python
tools/             the compared tools' manifest and lockfile, installed with npm ci
run.sh             entrypoint: one experiment, one tool, one fresh registry
lib/common.sh      typed steps, observe, assert, verdict; the registry and the fault; the colleague and the shim
lib/recovery.jq    the catch-up summary a verdict carries
lib/fixture.py     the six-package fixture in one flavour per tool
lib/observe.py     the state reader, through a scratch observer repository
lib/git-shim       the recording, injecting git
lib/failproxy.py   the 502-injecting reverse proxy in front of verdaccio, with gated refusals
lib/cache.sh       the validation of a persisted campaign
orphan/<tool>.sh   the orphan protocol per tool
midrelease/<tool>.sh
propagation/<tool>.sh
```

The harness's own tests run in the `shellcheck` target of `Dockerfile.gotest` (`dispat exec shellcheck`), beside
shellcheck over every script here: `lib/recovery-test.sh` holds the catch-up summary to each way a catch-up can go,
`lib/fixture-test.py` holds each flavour to what it is told and where it builds, `lib/failproxy-test.py` holds the
proxy to its decisions and its gate, and `lib/cache-test.sh` holds the campaign cache to what it may reuse.
