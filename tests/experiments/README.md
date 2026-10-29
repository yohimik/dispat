# Release experiments

These experiments exercise partial releases against disposable Git repositories and a real local npm registry.
The faults cover a rejected package upload, a concurrent branch update, and a consumer failure after its provider
publishes. Each protocol records the clone, remote Git refs, and registry state after every step, and counts what the
tool needed to finish a release its first run left partial. dispat runs as either a shipped binary or an explicitly
identified candidate; comparison tools use pinned versions.

A fourth experiment injects no fault: it times each tool's plan and release against the lower bound of the dispat
theorem, and sets each tool's plan beside dispat's.

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
| `propagation` | `core` publishes, then `cli` fails; the fault is removed without another source change.           | `build` (`cli`'s build script fails), `publish` (the registry refuses `cli`'s upload), `build-distributed`, `deselected`, `held`, `deferred` and `deferred-build`, described below; every scenario runs under every tool |
| `timing`      | none: the tool's plan command and its documented release, both timed.                              | `six` (the six-package fixture), `wide` (the wide fixture, `--wide`), both with the pending minor to `core`  |

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
response. For a failed build, dispat can run the release again. After an external publish request receives an error,
dispat retains its release lock because the request might still be in flight; a fresh release is fenced until an
operator proves and reconciles that exact destination outcome. The experiment records that safe non-convergence and
does not remove the lock. lerna's response is `lerna changed` and then `lerna publish from-package`, which
publishes whatever the registry is missing; when that refuses the working tree its own failed publish left rewritten,
the protocol checks the manifests out by hand and runs it again. nx's is `nx release --dry-run`, then
`nx release publish` for what the first run versioned or `nx release` again when it versioned nothing, and a push by
hand when the release left its commit on the clone alone, because nx 23.1.2 pushes before it commits in this fixture.
changesets' is `changeset status`, `changeset publish` again, and the push changesets never makes.

The further scenarios take the same catch-up through the other ways a consumer comes to be owed its provider. Every
tool runs each of them. dispat's protocol is described first; the other tools follow under the same intent, each
through its own nearest mechanism:

- `build-distributed` is the `build` fault with every build on a worker node, `build-a`, started in the container.
  The fixture sets `runOnly: [choreographer, orchestrator]`, names the signing secret's variable, and preauthorizes `build-a`
  in `execution.choreographers`. The choreographer reads the fixture's origin as its mailbox, while the first node serves an HTTPS
  return endpoint inside the isolated container. The log names `build-a` in the task outcome of `cli`'s build.
- `deselected` has no fault: the first run is `dispat release --package core,ui,api`, so `cli` sits the provider's
  release out, and the next full run must find what it is owed from the provider's release alone.
- `held` has no fault: an empty `release(cli): hold` commit with `Release-As: none` precedes the fix, so the first run
  ships `core` while `cli` is held (`W154`). The operator's resume, an empty `release(cli): resume` commit with
  `Release-As: auto`, is a manual step of the first phase, and the catch-up follows it.
- `deferred` gives `cli` a fix of its own and disables its build. When the registry refuses `core`, dispat retains the
  lock. Its version task waits for `core`'s record, so `cli` cannot publish against an unsettled provider.
- `deferred-build` keeps `cli`'s build configured. Its version task has the same provider-record dependency, so neither
  version nor build starts while `core`'s refused upload remains unsettled. The proxy answers the refused upload
  immediately; a gate waiting for `cli/dist/index.js` would be impossible under this graph.

lerna, nx and changesets run the same scenarios this way. Where a tool has no mechanism for the operator's intent, the
protocol runs its ordinary documented release and says so in the transcript.

| Scenario            | lerna                                                                                 | nx                                                                         | changesets                                                                          |
|---------------------|---------------------------------------------------------------------------------------|----------------------------------------------------------------------------|-------------------------------------------------------------------------------------|
| `build-distributed` | the `build` protocol: lerna has no worker of its own                                  | the `build` protocol: nx distributes tasks only through Nx Cloud, which the image disables | the `build` protocol: changesets has no worker                                      |
| `deselected`        | `lerna version` takes no package selection, so the first run is unselected            | `nx release --projects core,ui,api`                                        | `changeset version --ignore cli`, with `cli`'s entry in a changeset of its own      |
| `held`              | `cli` is marked `"private": true` and every version step passes `--no-private`        | `release.projects` is `["*", "!cli"]`                                      | the configuration's `ignore` is `["cli"]`, with `cli`'s entry in a changeset of its own |
| `deferred`          | the provider's run is `lerna publish from-package`, which takes no selection          | the provider's run selects `--projects core,ui,api`                        | the provider's run is `changeset publish`, which takes no selection                 |
| `deferred-build`    | as `deferred`, with `cli`'s build                                                     | as `deferred`, with `cli`'s build                                          | as `deferred`, with `cli`'s build                                                   |

A hold is a committed edit, `chore(release): hold cli`, and the resume is a manual step of the first phase that
reverses it as `chore(release): resume cli`. A changeset that names an ignored package beside others is refused, so in
`deselected` and `held` the changesets fixture asks for the same four patches in two files. In `deferred`, `cli` has a
fix of its own committed as `fix(cli): repair command` (and a changeset for changesets) and no build script, because
these tools run every build script they find; `deferred-build` keeps it. After the first run, a `deselected` or `held`
catch-up is the tool's own release again. In the `deferred` cells, dispat instead retains its lock after an uncertain
external upload and records a fenced retry; the other tools run their documented recovery commands. nx publishes
with `nx release publish` instead whenever a version it tagged is missing from the registry. The shared fault is
preserved, while each verdict states what that tool actually established.

### The timing cells

The timing protocol is the same under every tool: the fixture with its pending minor to `core`, the baseline
publication and an observation, then `query:plan`, the tool's own plan command, and the release, then an observation
and the figures.

| Tool         | `query:plan`                                                                          | The release (`run`, or `run-<part>` per command)                               | Relation  |
|--------------|---------------------------------------------------------------------------------------|--------------------------------------------------------------------------------|-----------|
| `dispat`     | `dispat status --log-format json`                                                     | `dispat release --log-format json`                                             | `build`   |
| `lerna`      | `lerna version --conventional-commits --yes --no-git-tag-version --no-push` in a scratch clone taken just before it, since lerna has no dry run | `lerna version --conventional-commits --yes`, `lerna publish from-git --yes` | `build` |
| `nx`         | `nx release --dry-run`, which also runs the pre-version command and so every build   | `nx release --yes`, and the push of the commit it leaves on the clone          | `none`    |
| `changesets` | `changeset status --verbose`                                                          | `changeset version`, the commit, `changeset publish`, the push                 | `publish` |

The planning time is the `plan` step's duration, and the makespan runs from the start of the first release step to
the end of the last. `lib/plan.py` reads the plan step's output into `plan.json` as `{packages: {package: next
version}, told}`, where `told` is what the tool read of the pending change: the pending changesets' front matter for
changesets, the subject of the commit at the clone's head for the others. dispat's own `releasing` count on its
`release plan ready` line is held against the packages read, and a mismatch is refused.

The tasks are builds and publications, the same two kinds under every tool, and none of them is read from a tool's
log. Every package's build script holds for `BUILD_HOLD_MS` and then appends `{package, task: "build", start, end}` in
wall-clock seconds to the file `EXPERIMENT_TIMING` names, which `lib/common.sh` sets to `timing.jsonl` in the cell's
folder. The proxy appends `{package, task: "upload", start, end, status}` for every upload it forwards, and that upload
is the publication's task for every tool: lerna publishes in-process through libnpmpublish, and the upload is the one
point every tool's publication passes through. `lib/npm-shim`, built like the git shim, stands first on `PATH` for
each release step and appends `{package, task: "publish", start, end, exit}` around every `npm publish` it forwards,
reading the package from the manifest of the folder the publish names, or of its working directory when it names none.
Those processes are reported beside the tasks, with their own package's build cut out (changesets' `prepack` runs
inside one), and are no part of Wk or Lk. The shim stands in `/work/node_modules/.bin` during the step: nx runs npm
under npm-run-path, which puts every `node_modules/.bin` above the working directory and then node's own folder ahead
of `PATH`. No tool's versioning, commit, tags or push is a task under any tool.

`lib/timing.py` computes the figures into `timing.json`, in the vocabulary of the dispat theorem (CCME SPEC.md
§13.11), from the records inside the release's span:

| Field              | What it is                                                                                         |
|--------------------|----------------------------------------------------------------------------------------------------|
| `planningSeconds`  | the `plan` step's duration; `planBuilds` counts and sums the builds that ran inside it             |
| `makespanSeconds`  | the release's span, first release step's start to last release step's end                         |
| `idleSeconds`      | the part of the makespan no task was running in                                                    |
| `work`             | Wk, every build's and upload's duration summed                                                     |
| `criticalPath`     | Lk, the longest precedence chain, with `criticalChain` naming its tasks                            |
| `parallelism`      | s as observed: the most tasks running at one instant, not the theorem's slot count                 |
| `bound`            | max(Lk, Wk / s)                                                                                    |
| `ratio`            | makespan over bound                                                                                |
| `relation`         | the build readiness relation the protocol declares, `publish`, `build` or `none` (§19.2a)          |
| `observedRelation` | the strongest relation every consumer build in the records honoured, from `consumerBuilds`         |
| `violations`       | every edge of the declared precedence whose later task started before the earlier one had ended     |
| `tasks`            | per kind, `build`, `publish` (the uploads) and `npmPublish`: count, total, median, max and failed  |
| `taskTimes`        | every build and upload: package, kind, start after the release's start, and duration               |
| `untimed`, `unknown`, `straddling` | shipped packages without a build or an accepted upload, record packages outside the graph, and records crossing the release's edge |

The precedence runs over the dependency graph the clone's manifests declare: every package builds before it
publishes, a provider publishes before its consumer, and a consumer's build waits for its providers' publications
under `publish`, their builds under `build` and nothing under `none`; a package that ran no task of a kind passes its
providers through. The relation is declared per tool in its protocol, from that tool's configuration: dispat's fixture
leaves `isBuildWaitingPublish` at its default, `false`; `lerna publish` packs every package, running `prepack`, in
dependency order before it publishes any; the nx fixture declares no `dependsOn` for the build target its
pre-version command runs; `changeset publish` publishes in chunks of the dependency graph and npm runs `prepack`
inside each `npm publish`. The records are held to it edge by edge, within the 50 ms the step and record clocks may
differ by.

The wide fixture (`fixture.py <root> <flavour> --wide`, with `fixture.py --packages wide` printing its names) is
`core`, a chain of `WIDE_CHAIN` packages `chain1`, `chain2`, ... each depending on the one before it, and `WIDE_LEAVES`
leaves `leaf01`, `leaf02`, ... depending on `core`, tilde ranges throughout, baselined and published like the six.
`common.sh` exports the wide names as `EXPERIMENT_PACKAGES` for the wide cells, and the fixture refuses a list that is
not its shape. Its pending change is `feat(core)^^` for dispat, every transitive consumer, which is what lerna and nx
release on their own; the six keeps `feat(core)^`, direct consumers only, and changesets gets `core: minor` alone in
both scenarios.

Every timing cell holds every tool to the same sentences: the plan named the packages it would release, the release
exited 0, it shipped every package the plan named at the version it named and nothing else, every package that
shipped is consistent, and the release honoured every edge of the precedence it was measured under. A cell whose
records hold no build or no upload of the release, miss a shipped package's build or accepted upload, name a package
outside the graph or straddle the release's span is not a record and fails. The report sets each tool's plan beside
the dispat cell's plan of the same scenario and records agreement or the differences.

The campaign runs the timing cells after every other cell, one at a time, so no other cell shares the host with one.

### The LLVM distributed build

Production docs import full two-VM LLVM and Linux kernel campaign artifacts selected by exact workflow run and attempt IDs. The release workflow checks successful runs from this repository’s separate `Build experiments` workflow and original checkout source, then calls `dispat exec import-build-campaigns --for pkg:experiments --in pkg:experiments` with `EXPERIMENTS_BUILD_VALIDATE_ONLY=1` before release effects. The docs hook calls the same command after the standard experiment campaign.

The importer requires `EXPERIMENTS_LLVM_CAMPAIGN_DIR`, `EXPERIMENTS_KERNEL_CAMPAIGN_DIR`, `EXPERIMENTS_BUILD_SOURCE_REVISION`, `EXPERIMENTS_BUILD_BINARY_SHA256`, and each workload's `EXPERIMENTS_{LLVM,KERNEL}_RUN_ID` and `EXPERIMENTS_{LLVM,KERNEL}_RUN_ATTEMPT`. Both artifacts must contain all six selected full-profile records. It verifies source, binary, version, trial identity, raw evidence hashes, observed durations and hardware/configuration before replacing previous build-comparison cells. It keeps the measured candidate identities intact; these comparisons do not assert that the later published alpine binary was measured. Missing inputs fail rather than selecting an older campaign or substituting a local run.

Each executed cell must also retain sanitized Docker store observations from preparation, the coordinator, and both workers. The importer requires classic `overlay2` store semantics, rejects containerd snapshotter observations, and retains the original files with SHA256 hashes. Each daemon's observed version remains independent; matching versions are not assumed or required.

`dispat exec llvm-experiments --for pkg:experiments --in pkg:experiments` remains an optional one-host diagnostic campaign. Its three Dispat modes and native Nx Cloud entries are independent of the required production cloud imports. Standalone `summary` includes whatever records were explicitly produced or imported.

The default `full` workload configures and builds LLVM core twice, once with X86 and once with AArch64. Each
configuration builds the default `all` target, including its tools and libraries. Clang and other subprojects,
runtimes, tests, examples, benchmarks, and documentation are disabled equally. The source is
[LLVM 20.1.8](https://github.com/llvm/llvm-project/commit/87f0227cb60147a26a1eeb4fb06e3b505e9c7261), pinned by full
commit and checked again before each task. Both configurations begin with a new build directory. This measures
two independent complete backend builds, not the subdivision of one Ninja graph into remote compiler invocations.

Two executor processes use independent checkouts on the same host. Each can run one task with two Ninja jobs by
default. The container's default CPU quota is four; `EXPERIMENTS_LLVM_CPUS` and `EXPERIMENTS_LLVM_JOBS` set those
budgets for every cell. `EXPERIMENTS_LLVM_TIMEOUT` defaults to four hours per cell. Source checkout and toolchain
installation happen outside the measurement. The image contains the compiler tools and source; Nx's Cloud client
is acquired and fingerprinted in all three workspaces before timing. Client versions and SHA-256 fingerprints
must match, and the verdict records both. Timing includes node startup, registration,
scheduling, CMake configuration, compilation, output transport, and coordinator verification. Returned manifests
must identify this run and two distinct executors, and the coordinator checks executable digests, LLVM versions,
and a demangling result. Full builds return five representative binaries from each configuration rather than
transferring every library; the work still builds the complete configured target.
Full campaigns can take hours; each verdict records its timeout. Production import requires all five runnable
cloud cells to complete and retains Changesets as unsupported, without a duration. The optional one-host
diagnostic command may retain blocked native-cloud entries, but those cannot replace a required cloud result.

Nx and Lerna use their documented Nx Cloud distribution with two manually started agents. They require
`NX_CLOUD_ACCESS_TOKEN` and `EXPERIMENTS_NX_CLOUD_ID` in the caller's environment. Without them, the result is
`blocked`, with no duration and no local substitute. Changesets has no distributed build runner, so it receives
`unsupported`, also without a duration. Every task's committed input contains a fresh run identity, preventing
remote-cache reuse between cells while retaining the cache transport Nx Cloud requires for distribution.
The token is passed through the environment and is not saved in a fixture or result.
For the optional one-host command, custom Cloud endpoints and encryption use `NX_CLOUD_API` and
`NX_CLOUD_ENCRYPTION_KEY`. Production GCE comparisons use the separately labeled manual split adapter for
Nx and Lerna and do not require native Nx Cloud credentials.

`EXPERIMENTS_LLVM_PROFILE=smoke` selects only `llvm-cxxfilt` and `llvm-size` in both configurations. This validates
the harness with real LLVM compilation but is explicitly labeled a subset in its result and never reported as a
full build. `tools/llvmexperiment` implements the protocol, and `Dockerfile.llvm` prepares its shared environment.
Each cell writes `verdict.json`, `observations.jsonl`, and `transcript.txt`; the verdict's `llvm` object records
availability, workload, source revision, resource budget, executor names, durations, and artifact digests.

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

The build-fault propagation cells ask whether the provider shipped, the consumer was withheld, and the next release
finished the consumer without republishing the provider. The registry-refusal cells ask a different safety question:
whether dispat reports `E228`, retains its remote release lock, and refuses a fresh release with `E336` before any
second upload or tag. Their `recovery.converged` value remains false. Compared tools still run their ordinary
commands under the same injected fault; their verdicts describe what happened rather than gating the campaign.

### The colleague's push

The colleague is a second clone of the origin. A shim placed first on `PATH` for the duration of the tool's release
command records every git call the tool makes, and fires the colleague's commit and push once, right before the first
`git push` that is neither the release lock's nor a commit. dispat's push of its release lock precedes the plan and is
excluded by name, and so are its pushes of publication receipts, which accompany each publish; a `git commit` never fires the injection, because the shim reads the git subcommand rather than the
whole command line.

The interleaving is therefore a point in each tool's own sequence rather than a timer, the same every time and the
same for every tool. Where in the release that point falls differs by tool, and that difference is part of the record:

- **lerna** commits and tags, then pushes the branch and the tags together, and publishes afterwards.
- **nx** pushes before it commits or tags, leaving the version bumps staged in the index when the push is refused.
- **changesets** makes no push of its own: it versions, commits, publishes and tags, and the push is the operator's,
  so the injection fires on the command the protocol runs rather than on anything the tool does.
- **dispat** publishes first and pushes the branch last. Each package's publication receipt reaches origin the moment
  the package publishes, so when the branch refuses the push the release exists on the registry, in this clone, and as
  one receipt per package on origin. The dispat cell asserts that order from the recorded git calls, and that no receipt
  is left once the tags are on origin.

After the tool's run, the experiment performs the recovery an operator is left with, `git pull --rebase` and
`git push --follow-tags` for the tools that leave a refused push behind (taking the release's side of a conflict), and
then asks each tool what it would release next: `lerna changed`, `nx release --dry-run`, `changeset status`,
`dispat status`. For dispat it then runs the release again from the same clone.

dispat's orphan fixture opts `cli` into `revertOnFail`, but that worktree rule cannot prove a refused registry upload
has ceased. The protocol verifies that earlier successful packages remain durable, `cli` has no new tag, `E228`
retains the remote lock, and a fresh release is fenced with `E336` without another upload. The fixture does not
remove the lock or claim automatic recovery from an uncertain external publication.

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

The origin's row lists dispat's publication receipts, `refs/dispat/receipts/<tag>`, beside its tags. A receipt stands in
for a release tag that waits for the release commit, and a finished release leaves none, which every dispat cell
asserts after its last release.

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
any missing, corrupt, partial or failed record. Only the release workflow writes the records the site is built from;
a run by hand, with `dispat exec experiments`, measures any released version.

The campaign is whatever `cells` prints and nothing else counts it. The sweep pipes that list into `xargs`, the
cache manifest stores it verbatim, and the report's headline counts the records
it found. A cell added to `cells` is therefore a cell every one of them runs, with no number to update anywhere.

New timing cells also retain `executionMeasurement` beside the existing `timing` figures. Before the first release
command, the producer captures package dependencies, the selected plan, readiness configuration and source inputs.
Its graph is explicitly an analysis projection of build and registry-upload operations, with unselected operations
retained as virtual dependency relays. It is not a native executor graph and lists omitted work.

The additional records use unrounded local monotonic durations. The shared `execution-measurement` command derives
and validates the formulas; the producer does not duplicate them. Nominal work arithmetic assumes compatible
reported seconds and records that relative clock-rate uncertainty is unknown. Unmapped task span, overlap and
unestablished resource capacity remain null with reasons. The older wall-clock `timing` fields keep their existing
meaning and rounding. Other experiment cells do not acquire reconstructed measurements.

Every new timing run has a unique directory under `execution-measurements/<trial UUID>/`, containing the captured
inputs, raw task and step records, environment, digests and derivation output. Set `EXPERIMENT_REPETITION` to a
positive repetition number when invoking a container. The campaign keeps these directories when it replaces the
current cell results, so reruns and failed trials do not overwrite earlier evidence.

Underneath, one cell is one container:

```sh
docker buildx build --file tests/experiments/Dockerfile \
  --build-arg DISPAT_VERSION=1.7.1 --build-context measurement-source=./tools \
  --load -t dispat-experiments tests/experiments
docker run --rm -v "$PWD/coverage/experiments:/results" dispat-experiments midrelease dispat conflict
```

The binary under test is copied out of the published `yohimik/dispat-alpine:<version>` image, so the experiments run
the exact bytes a release shipped and never a build of the checkout. The other tools are installed into the image from
a committed lockfile at their pinned versions; no phase installs anything over the experiment registry.

Each run leaves `coverage/experiments/<experiment>[-<scenario>]-<tool>/` with the transcript, every step's output and
`steps.jsonl`, one observation per step, every failed expectation's own output, the registry's and the proxy's logs,
the git calls the tool made (`git-calls.log`, one JSON object per line, with the injection marked), the worker's log
for the distributed cell, `recovery.json` when a catch-up phase ran, and `verdict.json`. A timing cell adds
`timing.jsonl` (the build, upload and `npm publish` records), `timing.json` (the figures, also the verdict's `timing`
object) and `plan.json` (the plan read from `step-plan.log` and what the tool was told, also the verdict's `plan`
object). Every step in `steps.jsonl` carries its wall-clock `started` and `ended` besides its `seconds`. Mount the folder over `/exp`
(`-v "$PWD/tests/experiments:/exp:ro"`) to iterate on the scripts without rebuilding.

## Layout

```
Dockerfile         the runtime: dispat from its image, the other tools, verdaccio, python
tools/             the compared tools' manifest and lockfile, installed with npm ci
run.sh             entrypoint: one experiment, one tool, one fresh registry
lib/common.sh      typed steps, observe, assert, verdict; the registry and the fault; the colleague and the shim
lib/recovery.jq    the catch-up summary a verdict carries
lib/fixture.py     the six-package and wide fixtures in one flavour per tool
lib/observe.py     the state reader, through a scratch observer repository
lib/git-shim       the recording, injecting git
lib/npm-shim       the npm that times every publish it forwards
lib/plan.py        a tool's plan, read from its plan command's output
lib/timing.py      a timing cell's figures
lib/failproxy.py   the 502-injecting reverse proxy in front of verdaccio, with gated refusals
lib/cache.sh       the validation of a persisted campaign
orphan/<tool>.sh   the orphan protocol per tool
midrelease/<tool>.sh
propagation/<tool>.sh
timing/<tool>.sh
```

The harness's own tests run in the `shellcheck` target of `Dockerfile.gotest` (`dispat exec shellcheck`). The shell
ones run beside shellcheck over every script here: `lib/recovery-test.sh` holds the catch-up summary to each way a
catch-up can go, `lib/cache-test.sh` holds the campaign cache to what it may reuse, and `lib/npm-shim-test.sh` holds
the npm shim to what it times. The Python ones run in the step after it, which gives git the identity the fixture
commits with: `lib/fixture-test.py` holds each flavour to what it is told and where it builds,
`lib/failproxy-test.py` holds the proxy to its decisions and its gate, and `lib/plan-test.py` and
`lib/timing-test.py` hold the plan readings and the figures to recorded outputs and hand-made records.
