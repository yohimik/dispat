# dispat beside other release tools

Monorepo release tools compute versions from the same raw material: conventional commits, per-package git tags, and a
dependency graph. Version arithmetic does not separate them. The difference lies in how each tool handles a partially
successful release. A release is never a single operation. A run that publishes eleven packages performs eleven
independent, individually fallible writes to registries the tool does not control. It might finish having done only
seven. Published versions are immutable. Republishing an existing version is a hard error, so rolling back those
seven is impossible. Any recovery mechanism must therefore complete the remaining four.

dispat treats the run as a saga: a sequence of independently committed legs with recovery by completion rather than
rollback. The ledger of that saga is the release tags themselves. dispat writes a package's tag strictly after its
publish succeeds. The tags never claim more than was delivered, and whatever a run still owes remains visible to the
next run as pending work. Recovery means running the same command again.

This page describes how lerna, nx, release-please and changesets (Turborepo's documented release path) handle these
same situations. It also details the experiments that observed the differences on the tools themselves.

## Where the tag is written

The structural difference is the order of tag and artifact.

lerna, nx release and release-please write tags before artifacts exist. `lerna version` commits and tags before
`lerna publish` uploads anything. nx release's version step tags before its publish step runs. release-please tags
when the release PR merges, leaving publication to a CI job that runs afterwards. Once the tag exists, the tool
considers the version released regardless of upload success. The tag store cannot represent "planned but not yet
published". Delivery is at most once. A publish failure after tagging leaves consumers with nothing and the tool with
no memory of the debt.

Recovery for those three consults state outside the tags. `lerna publish from-package` diffs every manifest version
against the npm registry and publishes what is missing. This is a correct convergence procedure exactly where a
queryable registry exists, per ecosystem. No such query exists for a Docker tag, a GitHub release, or an applied
Terraform plan.

changesets sits on the other side of that line, alongside dispat. It tags strictly after each successful publish, so
a failed upload leaves no orphaned tag. `changeset publish` asks the npm registry what remains, making its recovery
idempotent where that query exists. Its plan, however, is a function of mutable workspace files rather than of the
repository's history. The bump intent lives in human-written changeset files that `changeset version` consumes and
deletes. A past release can therefore not be recomputed from history. Its propagation is range-gated: a dependent
bumps only when the released version leaves the dependent's declared semver range.

dispat tags after publish and reads its plan from commit messages and tags alone. The same plan can be recomputed at
any commit. Delivery is exactly once over arbitrary targets, and recovery requires no registry query of any kind.

## Blast radius

lerna in independent mode, nx release, and release-please with its node-workspace plugin patch-bump the dependents
of a released package transitively and unconditionally. A `fix` in one package releases everything downstream of it.
changesets bumps dependents only until their declared ranges hold again. dispat makes the radius part of the commit
itself. A plain `fix(x)` releases one package, `fix(x)^` reaches the direct consumers, and `fix(x)^^` reaches the
transitive ones. The `+N` modifier bounds the depth, so two changes in one window can carry two different radii.

| | lerna / nx / release-please | changesets | dispat |
|---|---|---|---|
| bump source | commit text | changeset files | commit text |
| tag written | before publish | after publish | after publish |
| blast radius | all dependents | until ranges hold | per commit: `^`, `^^`, `+N` |
| delivery | at most once | once, npm only | exactly once |
| recovery | npm query or CI re-run | npm query | re-run, any target |
| replayable from history | yes | no, files consumed | yes |
| prerelease channels | global switch | global mode | per commit |
| retractions | none | unreleased only | `cancel`, `Edits`, holds |

## The experiments

The readings above start as documentation. The following experiments executed them against the tools themselves, using
the latest published version of each: lerna 10.0.1, nx 23.1.2, changesets 3.0.1 under turbo 2.10.12, and dispat.
Fault injection requires a registry the experiment is allowed to break. Each tool ran against its own fresh local
registry (verdaccio 6) behind a proxy that fails the upload of one named package with a 502. Every phase was run
twice. The machine-recorded transcripts are identical between repetitions.

### One denied upload

The fixture is six npm packages: `core`; `cli`, `ui` and `api` depending on `core`; `theme` and `docs` depending on
`ui`. One change is pending on `core`.

Blast radius, observed. `lerna version --conventional-commits` and `nx release` both bumped `core` to 1.1.0 and all
five dependents to 1.0.1. This included `theme` and `docs`, whose only dependency `ui` did not change. changesets,
given a major changeset for `core`, bumped exactly until the ranges held again. It bumped `cli`, `ui` and `api` to
1.0.1, because `^1.0.0` no longer admits 2.0.0. It left `theme` and `docs` untouched, because `ui`'s patch stays
inside their ranges. dispat, under an explicit `feat(core)^`, planned four: `core` at 1.1.0 and its three direct
consumers.

The orphan. With `cli`'s upload denied, lerna and nx had already written all six tags at their version steps. After
the failed publish, the tag `cli@1.0.1` exists while the registry still serves 1.0.0. changesets and dispat, tagging
after publish, wrote no `cli` tag at all.

Recovery. lerna's documented recovery, `lerna publish from-package`, refused to run at first. Its own failed publish
had left the working tree dirty on the rewritten manifests. Only after a manual `git checkout` did the registry diff
publish `cli@1.0.1`. nx recovered without that block. Re-running `nx release publish` skipped the four published
packages by registry query and uploaded `cli`. `changeset publish` likewise reported five packages already published
and delivered the sixth. Both recoveries are npm queries, correct where such a query exists, and only there. dispat's
recovery was re-running the same command. The second run published `cli` at the same 1.0.1, and the third run planned
nothing. No flags, no cleanup, no registry query.

release-please was not part of this run. It has no publish step to deny, because publication is delegated to CI. Its
versioning decisions were instead checked against real history, below.

### Replay against real history

For the last five releases of two repositories that follow the commit convention, the experiment checked out each
release commit's parent. It planned with dispat under a generated configuration and compared the plan against what
actually shipped. This is expected data the repositories already contain.

On googleapis/google-cloud-node (released by release-please in manifest mode, 243 packages), dispat reproduced 36 of
the 42 released package-versions exactly. Every miss classifies: three releases of previously untagged packages
managed only by the manifest, two initial-version policies for new packages, and one commit-type mapping. dispat also
planned six to eight packages per release beyond each release PR. This was pending work release-please had not yet
cut, visible to a planner that reads the tag store.

On conventional-changelog (released by lerna in independent mode, replayed without dependency edges configured),
every package dispat planned matched the released version exactly. The remainder is precisely lerna's implicit
dependent cascade, now measured rather than asserted. The fifth replayed release diverges wholesale. The repository's
own history explains why: a documented manual force release after a publication error.

### Scale and recovery growth

On synthetic layered workspaces, planning takes 0.6 s at 100 packages, 2.8 to 6.7 s at 1,000, and 110 to 149 s at
10,000 packages with roughly 20,000 edges. The curve is super-linear at the top end, where per-package git subprocess
costs dominate. A 1,000-package chain, the worst dependency depth, costs the same as the layered shape at that size.

For recovery growth, the real executor ran against a local origin with failures injected in the publish script: 24
episodes over 20- and 100-package workspaces at per-publish failure probabilities of 0.1 and 0.3. Every sequence
converged to an empty plan in at most 5 runs against first plans of up to 45 packages. Blocked dependents reappeared
at their originally planned versions.

## Two runs at once

One more difference does not need an experiment to state. dispat's mutual exclusion between concurrent runs needs no
lock service. A run pushes a single reserved annotated tag to the git remote before planning and deletes it on the
way out. The remote's compare-and-swap on tag creation makes the loser's push fail before it reads a single tag. The
[release lock](./reference/releasing/release-lock.md) page describes it. The failure model behind all of this is the
subject of [Recovering from a failed run](./reference/releasing/recovery.md).
