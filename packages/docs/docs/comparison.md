# dispat beside other release tools

When comparing release tools, look beyond the first successful run. Ask what happens when one package publishes and
the next one fails: how do you find the unfinished work, and what do you run to finish it?

This page describes the behavior observed in dispat's checked-in experiment campaign. It covers Lerna 10.0.1,
Nx 23.1.2, Changesets 3.0.1, and the dispat binary under test. A separate history replay examines repositories released
with release-please and Lerna. These are specific versions and configurations, not claims about every setup or future
release of those tools.

The [experiment report](./internals/experiments.mdx) links the commands, fixtures, and recorded results. Use it when you
want to reproduce a finding or check whether it applies to your project.

## Where the tag is written

A tag and an uploaded artifact are separate writes. Their order affects what a later run can infer from Git.

In the tested Lerna and Nx flows, versioning creates tags before publication. If an upload then fails, the tag alone
does not prove that the artifact exists. Recovery checks the npm registry for versions still missing. For example,
`lerna publish from-package` compares manifest versions with the registry. A release-please workflow similarly needs
its publishing job to track publication after the release PR is merged and tagged.

Changesets and dispat tag successful publications after uploading. In the failed-upload fixture, neither wrote a tag
for the failed package. Changesets uses the npm registry to find what remains to publish. dispat uses release tags,
commit history, and resolved configuration to plan the remaining work.

This does not make upload and tagging atomic. If an upload succeeds and the process stops before recording its tag,
dispat cannot infer the result from Git alone. Check the destination before retrying that package. Custom publish
commands must handle their own partial writes, whether they publish npm packages, images, or infrastructure changes.

## Blast radius

A dependency update does not always mean every consumer needs a new release. The tools in the fixture make that
choice differently:

| In the tested setup | Which consumers release? |
| --- | --- |
| Lerna independent mode and Nx release | All downstream consumers in the fixture were bumped. |
| Changesets | Consumers were bumped where needed to satisfy the configured dependency ranges. |
| dispat | The commit specified the propagation depth: `^`, `^^`, or `+N`. |

For dispat, `fix(x)` changes `x` alone. `fix(x)^` also reaches direct consumers, and `fix(x)^^` reaches transitive
consumers. This makes propagation a per-commit decision. Changesets expresses bump intent in changeset files instead.
Those files are consumed during versioning, so replaying its input requires the relevant workspace state, not only
commit-message parsing.

Choose the behavior that fits how your team describes changes. A broader automatic cascade can be useful; explicit
propagation gives reviewers a separate decision to make in the commit message.

## The experiments

The tools are pinned in
[`tests/experiments/tools/package.json`](https://github.com/yohimik/dispat/blob/main/tests/experiments/tools/package.json).
The harness gives each test case its own registry, clone, and origin. A proxy in front of Verdaccio can reject one
named package's upload with HTTP 502 while allowing the others through. Fixed commit times make repeated transcripts
comparable.

The release campaign runs against the dispat image it has published. See the
[recorded results](./internals/experiments.mdx) and [reproduction instructions](./internals/experiments.mdx#reproducing).
The harness, fixture, observer, and fault proxy live in
[`tests/experiments`](https://github.com/yohimik/dispat/blob/main/tests/experiments/README.md).

### One denied upload

The fixture has six npm packages. `cli`, `ui`, and `api` depend on `core`; `theme` and `docs` depend on `ui`. One
feature is pending in `core`.

The initial plans differed:

- Lerna and Nx selected `core` at `1.1.0` and all five consumers at `1.0.1`.
- Changesets selected `core` and its three direct consumers. Their `~1.0.0` ranges did not accept `core@1.1.0`.
  `theme` and `docs` stayed unchanged because their ranges still accepted the new patch of `ui`.
- With `feat(core)^`, dispat selected `core` and its three direct consumers.

When the proxy rejected `cli`'s upload, Lerna and Nx had already created its new tag. Changesets and dispat had not.
The registry still had the old `cli` version in every case.

All tested recovery paths eventually completed:

| Tool | What recovery did in this fixture |
| --- | --- |
| Lerna | `lerna publish from-package` first refused the dirty working tree. After the fixture restored the rewritten files, the registry comparison found and published the missing version. |
| Nx | `nx release publish` skipped versions already in the registry and uploaded the missing package. |
| Changesets | `changeset publish` found the missing publication, uploaded it, and tagged it. |
| dispat | A second release run published `cli` at its planned version. A third run had an empty plan. |

The cleanup described here is an experiment observation, not a recommendation to discard files from your own failed
run. Inspect those files and the registry before taking recovery action.

release-please was not included in this upload-failure test because it delegates publication to a separate CI job.
Its version decisions were examined through history replay instead.

### A patch that asks direct consumers to release

This test uses `fix(core)^`. The new `core@1.0.1` still fits each consumer's `~1.0.0` range, but dispat's caret explicitly
requests consumer releases. For the corresponding Changesets test, the fixture supplies explicit patch entries for
`core` and its three direct consumers. Lerna and Nx receive `fix(core)` and select all six packages in their tested
configurations.

The harness then fails a consumer build or upload, removes the fault, and runs each tool's recovery command without
adding a commit. Where the build sits in the pipeline matters:

- dispat's fixture builds `cli` after `core` publishes. A failed `cli` build leaves successful independent
  publications in place.
- Lerna's fixture runs `prepack` before publication, and Nx's runs the build before versioning. Their build-failure
  cases publish nothing. Their upload-failure cases can leave tags for missing publications.
- The Changesets upload flow leaves the successful packages published and tagged.

The recorded dispat catch-up selects `cli` alone with `W193` and does not republish `core`. The other tools recover
through their registry-aware publish paths, with the cleanup or Git pushes shown in the transcripts. Every tested
catch-up converged, and every tool published `core` once in these fixtures.

Additional cases cover a consumer omitted from a selected release, a held consumer that is later resumed, and a
consumer that releases its own fix before a failed provider becomes available. The last case checks two outcomes:
with no build artifact, the later plan can still find the missed provider delivery; with an artifact already built
against the unavailable version, dispat blocks it with `W194` and rebuilds on the next run.

A worker case runs dispat builds on a node reached through the release repository. The other tools are exercised
through the local build, selection, and hold mechanisms configured by the harness. Those mechanisms are not identical:
Nx uses `--projects`, Changesets uses `--ignore`, and the Lerna fixture uses private-package settings for a hold.
Read the [experiment record](./internals/experiments.mdx) before treating a result as a direct feature comparison.

### Replay against real history

The replay checks five recorded releases from each of two repositories. For each release, it checks out the release
commit's parent, generates a dispat configuration, and compares the resulting plan with the versions that shipped.

For `googleapis/google-cloud-node`, released with release-please in manifest mode, dispat reproduced 36 of 42 released
package-version pairs. The differences were three previously untagged packages managed through the manifest, two
initial-version policies, and one commit-type mapping. It also selected six to eight additional packages per release
whose pending work had not been included in that release PR.

For `conventional-changelog`, released with Lerna independent mode, every package dispat selected in the matching
replays had the released version. The replay did not configure dependency edges, so Lerna's dependent cascade accounted
for additional packages. The fifth replay diverged after a documented manual force release following a publication
error.

These results show how history, configuration, and release policy affect a replay. They do not establish that one
tool can reproduce another tool's releases without matching those inputs.

### Planning and release time

The timing cells tell every tool of one pending change, each the way it reads one, with no fault, run its own plan
command and then its documented release, and time both. Builds are timed by the fixture and publications by the
registry, as their uploads, the same two kinds of task for every tool, rather than read from any tool's log. Each
release is set against the bound of the dispat theorem, `max(Lk, Wk / s)`, under the build readiness relation its
tool's configuration declares, with s the concurrency the release was seen to use: no execution that honours that
precedence and runs at most that many tasks at once with those durations finishes sooner. Every edge of the
precedence is checked against the records, and the edges a release did not honour are listed beside its figures. Each
tool's plan is also read as a set of packages at their next versions and set beside dispat's plan of the same
scenario, with what each tool was told and the differences recorded as data. Report elapsed time for this recorded
workload and host alongside each tool's ratio. Ratios use different graphs, measured durations and observed
concurrency, so they do not rank cross-tool speed. The tables, each task's duration and the ratio's limits are on
[release experiments](./internals/experiments.mdx#planning-and-release-time).

### Scale and recovery growth

The recorded synthetic planning runs took:

| Workspace size | Observed planning time |
| --- | --- |
| 100 packages | 0.6 seconds |
| 1,000 packages | 2.8–6.7 seconds |
| 10,000 packages, roughly 20,000 edges | 110–149 seconds |

At the largest size, per-package Git subprocess costs dominated and growth was super-linear. A 1,000-package chain
had a similar cost to the layered graph at that size. These measurements describe the test environment; they are not
a timing guarantee for another repository or machine.

The recovery campaign ran 24 episodes with 20- and 100-package workspaces and per-publication failure probabilities
of 0.1 and 0.3. Each sequence reached an empty plan within five runs, starting with plans of up to 45 packages. Blocked
consumers returned at the versions planned for them under those unchanged inputs.

## Two runs at once

dispat also coordinates concurrent runs through Git. Before planning, it claims a reserved annotated lock tag on each
participating remote. If another run owns a required lock, the new run stops rather than planning a competing release.

Normal cleanup removes the locks the run owns. The orchestrator publishes and writes release records; an uncertain
destination outcome must be reconciled before a retry. See [The release lock](./reference/releasing/release-lock.md)
and [Recovery](./reference/releasing/recovery.md) for partially completed releases.
