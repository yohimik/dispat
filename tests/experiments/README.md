# Release experiments

Two things can go wrong in the middle of a monorepo release that no unit test reaches: the registry refuses one
package after others have gone out, and a colleague's push lands on the branch while the release runs. Each experiment
here puts a released dispat binary and three other release tools through one of those, in the same fixture, with the
same fault, and records what every tool leaves behind in the three places a release writes: the clone, the origin and
the registry.

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

Tools: `lerna` 10.0.1, `nx` 23.1.2, `changesets` 3.0.1, `dispat` at the version the image was built from.

The fixture is six packages, `cli`, `ui`, `api` depending on `core` and `theme`, `docs` on `ui`, all at 1.0.0, tagged
`<name>@1.0.0`, published to a registry that starts inside the container, and pushed to a bare origin beside the clone.
One pending change, a minor to `core`, is committed the way each tool reads it: a conventional commit, or a changeset
file for changesets. Dependencies are tilde ranges, so the minor reaches `core`'s consumers under every tool. Every
commit is dated from a fixed clock, so two runs of one cell produce the same shas and two transcripts diff against
each other.

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
```

`build` takes the version from `EXPERIMENTS_DISPAT_VERSION`, or the newest `docker/dispat-alpine/v*` tag when it is
unset. `EXPERIMENTS_RESULTS` moves the results folder, `EXPERIMENTS_JOBS` sets how many cells run at once (four), and
`EXPERIMENTS_EXPECT=0` records a dispat cell without gating on its verdict.

Every release reruns every cell: the docs package's `beforeBuild` hook runs them against the image the run has just
published, and the site page is built from what they recorded. The `Experiments` workflow is the by-hand run, for any
released version, on demand.

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
```
