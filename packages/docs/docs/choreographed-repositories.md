# Linked repositories

Link repositories when each team should keep its own release configuration, but packages need to release in one
dependency order. Each repository declares an identity and records its own releases. Two-sided Git submodule links
connect the peers, and a release can start from any of them.

To set it up, [declare the peers](#the-identity-and-roster), [create their links](#linking-the-fleet-with-dispat-compute),
then configure the [CI checkout](#ci-checkout). Read the link-recording and recovery sections before the first release.

A non-empty repository identity enables this mode. The supported computed shapes are a chain or an entry-hub star; either way, each peer
still owns its policy. Choosing a star does not turn its centre into a control repository or change the behavior of
existing [control-repository configurations](./control-repository.md).

## When to choose it

Both arrangements give you one dependency graph, one publish order and propagation across repository boundaries. They
differ in where the fleet's configuration lives and in who starts a release.

|                                      | A control repository                       | A choreographed fleet                        |
|--------------------------------------|--------------------------------------------|----------------------------------------------|
| Where the fleet configuration lives  | one control file                           | one file per peer                            |
| Who can start a release              | the control repository                     | any peer                                     |
| What links the repositories          | submodules under the control checkout      | two-sided submodule links between peers      |
| What proves a cross-repository boundary | the control release checkpoint           | the links the release itself recorded        |
| Fleet-wide holds and directives      | a control commit reaches every package     | no unit reaches another repository's packages |
| Extra repository to own and review   | yes                                        | no                                           |

Use a control repository when one team owns release policy and needs fleet-wide directives. Use linked peers when
each repository owns its policy and any peer should be able to start the combined release. In the peer model, a commit
directly addresses only its own repository's packages; dependency propagation can still cross repositories.

## The identity and roster

Set `repository` in each peer and list the other peers under `repositories`. That list is the roster. It may be empty
or omitted for a one-member fleet, but a non-empty roster without the declaring repository's identity fails with
`E339`.

Here, `api` consumes `sdk`, and `web` consumes `api`:

```yaml title="sdk/dispat.yaml"
repository: sdk

repositories:
  - name: api
    url: git@github.com:acme/api.git
    branch: main
  - name: web
    url: git@github.com:acme/web.git
    branch: main

spaces:
  libraries:
    path: packages

commit:
  enabled: true
  push: true
  branch: main
```

```yaml title="api/dispat.yaml"
repository: api

repositories:
  - name: sdk
    url: git@github.com:acme/sdk.git
    branch: main
  - name: web
    url: git@github.com:acme/web.git
    branch: main

spaces:
  services:
    path: packages

dependencies:
  api: [sdk]

commit:
  enabled: true
  push: true
  branch: main
```

`web/dispat.yaml` is the same shape, with `repository: web`, a roster naming `sdk` and `api`, and `web: [api]` in its
`dependencies`.

- **`repository`** is this repository's identity in the fleet. It is written from letters, digits, dots, underscores
  and hyphens, and `control` is reserved for a central control repository. The same spelling is the submodule name every
  link to this repository uses, which is what lets one identity be read from either end of a link.
- **`repositories`** is the roster: every other peer by identity, with the `url` a link to it is cloned from, the
  `path` a link to it occupies inside this repository (default `.links/<name>`) and the `branch` the link follows.

Every peer's roster names the other members, even those it does not link to directly. Three repositories need only
two links; dispat follows those links to reach the rest.

Three settings belong to a control repository and are refused here, because a fleet with no control repository cannot
honour a policy written for one: `configs` and `--configs`, a `repositoryOverrides.<peer>.commit` object, and a
`repositoryBaselines` entry naming `control`.

## Linking the fleet with `dispat compute`

After writing the rosters, run [`dispat compute`](./cli/compute.md) in any peer to propose the missing links:

```console
$ dispat compute
+ link api sdk  connects sdk to the fleet

1 suggestion(s); apply all with --write, choose with --interactive
```

Alongside dependency edges and baselines, compute can propose three fleet changes:

| Suggestion | What it changes |
|---|---|
| `+ link <repository> <peer>` | Creates a missing link or supplies its missing reverse declaration. |
| `+ init <repository> <path>` | Creates the local checkout for a declared link. |
| `+ repository <repository> <peer>` | Adds a missing roster entry to that peer's own configuration. |

The default, `--topology minimal`, preserves existing links and adds the fewest needed to connect the roster without
creating a second route between peers. It requires a chain: every repository has at most two link neighbours,
and new links join free path endpoints. Three repositories get two links and a longest route of two hops. A
third link would create a second route and fail with `E338`. An existing branch with three link neighbours
cannot become a chain without deleting a link, so minimal refuses it. A chain is a supported shape, not a
claim of shortest hop count or physical latency; other tree shapes exist.

Preview shows at most one new joining edge in a repair round. In `minimal` mode, `--write` initializes
missing checkouts first, applies a writable endpoint link, recomposes the fleet, then continues. A peer
reached later may reveal more identities, missing roster entries, or an incompatible link. After applying
selected changes, dispat lists all remaining fleet suggestions. A newly discovered roster entry needs
another invocation for a safe configuration edit; while suggestions remain, `--write` reports incomplete
repair as an error and leaves staged edits for review. With `--interactive`, newly discovered suggestions
are displayed for another invocation; only suggestions explicitly accepted by the operator are applied.
For either topology, the chosen link owner's roster must name the peer before a new forward link is staged;
dispat proposes that roster repair first and rereads the fleet afterward.

A `+ link` can also repair a one-sided declaration reported by `W332`. The pair is already connected, so this adds no
route and fetches nothing. It writes the missing declaration inside the checkout already held by the declaring
repository.

Use `--topology star` when every peer should link directly to the repository where the command starts. Star mode
preserves existing links and errors if they cannot fit that shape; it never converts a tree by deleting links.
Known incompatible links are rejected before a write begins. A peer checked out during `--write` can expose a link
that was not previously visible; if it conflicts, the command fails and leaves its staged edits for review. It does
not roll back those edits or delete the conflicting link.

Apply them with `--write`, or `--interactive` to answer per suggestion. `--check` writes nothing and exits `1` while
anything is pending, which is the CI gate for a fleet whose links lag its rosters.

What `--write` does, in the part of its output that concerns the links:

```console
09:31:07 INF created fleet link path=.links/sdk peer=sdk repository=api
linked sdk from api at .links/sdk
09:31:07 INF declared the other half of the fleet link peer=api repository=sdk revision=edc0c8a0188cb2e4d033d06a4b47e6062fe6763e
declared api inside sdk at .links/api
09:31:08 INF created fleet link path=.links/web peer=web repository=api
linked web from api at .links/web
09:31:08 INF declared the other half of the fleet link peer=api repository=web revision=edc0c8a0188cb2e4d033d06a4b47e6062fe6763e
declared api inside web at .links/api
created 2 fleet link operation(s); review and commit them in each repository
```

The forward link is a checkout created by `git submodule add`. For the reverse link, dispat writes `.gitmodules`,
creates an empty folder and stages a gitlink without cloning again. The pin must name a revision already available
from the linking repository's remote. If that repository has no remote, dispat warns and leaves out the reverse
declaration because the peer would have no fetchable revision. A missing URL, unresolved checkout initialization,
or withheld reverse declaration leaves repair incomplete: `--write` exits nonzero after its warning and keeps
staged changes for review. It does not rewrite an existing reverse pin to mask the problem.

What `--write` does not do:

- **It never commits.** Every repository it touched is left with staged changes for you to read and commit yourself,
  in that repository, with whatever review that repository requires.
- **It never deletes a link.** A link is history's only record of what a release incorporated, so a link the fleet no
  longer needs is reported rather than removed.
- **It never recurses.** The back half of a pair is deliberately left as an empty folder. Never run
  `git submodule update --recursive` in a linked fleet: it fills that folder with a second copy of the repository you
  are standing in.
- **It writes no roster entry without a `url`.** Supply the missing URL in the declaring configuration, then run
  compute again. URLs containing a username and password are refused because `.gitmodules` is committed and pushed.

Commit the result in each repository and push. The fleet composes from then on.

## How a run composes the fleet

Start from any peer. dispat reads its configuration, follows fleet links breadth first, and loads each peer's
configuration as that repository's root. Each identity is visited once. The log lists the resulting workspace:

```console
$ dispat status
09:31:07 INF polyrepo workspace composed root=/w/api entry=api repositories=["api","sdk","web"]
```

Two rules keep the walk unambiguous. Only a submodule the roster names is a fleet link, so an ordinary vendored
submodule takes no part. And the links must form a tree: reaching a repository a second time through another link is
`E338`, because a second route would be a second answer to which repositories lie between two peers.

A linked checkout that calls itself something other than the name it was linked as is `E339`. A link whose folder
holds no repository is `E330`, and the message names the command
that initialises exactly that link. That is also what you get when you start a run inside another repository's linked
checkout: the back-link there is an empty folder by design.

Link pins are **advisory**. Two repositories cannot both record each other's current revision, so planning uses the
revision actually checked out in each peer. A mismatch with a link pin does not fail the run.

The snapshot is fixed once composition finishes. A successful native commit or link settlement in the same run can
advance an exact relevant head and any release-tag refs it wrote, including when another repository's package consults
that owner. Cross-owner admission requires the run's completed native-transition evidence, the exact live Git heads
and any claimed tag objects; another package's bare exported pin is insufficient. A package's own full-commit export
continues to admit only its own repository under the existing changed-tag checks. Neither path changes the plan or
declares the package published. A relevant head or tag changed by anything else before publication still fails with
`E330`. An unsettled native transition is waited on only for the bounded record interval; a timeout leaves its
outcome unknown and fails closed.

### Which settings combine

Each peer's configuration is an ordinary repository-local root. The table shows how far each kind of setting reaches
in the composed fleet.

| Setting | Boundary in an identity-linked fleet |
| --- | --- |
| Package names and `dependencies` | One package namespace and one dependency graph, including edges between peers. |
| `versionGroups` and implicit groups from shared spaces | Local to the peer that declares them. A same-named group in another peer is a separate group with its own version, and `versionGroup` names a group of the same repository. An unqualified `--group` selects the matching group of every peer. |
| Other space definitions and package configuration | Each peer owns its own paths and package declarations. A matching space name alone does not combine folders or scripts. |
| `scripts`, `env`, `flow`, parser, commit and lock policy | Resolve from the owning peer and its normal configuration layers. They do not merge across peers. |
| `execution`, `runOutputs` and repository participation | Read from the entry peer for this invocation. |
| Run concurrency, logging and root `webhooks` | Read from the entry peer, as described under [What to watch for](#what-to-watch-for). |

## How commits reach packages

A commit directly addresses packages owned by the repository that carries it. This is true for an explicit package
name or glob, `*`, `.` and the changed-file fallback, and it is true in every peer including the one the run started
in. There is no repository whose commits reach the whole fleet. Once dispat has the direct set, `^`, `^^`, channel
propagation and configured dependency propagation traverse the combined graph and reach consumers anywhere.

Because no commit can arbitrate between repositories, two incomparable directives that need one winner stop with
`E334`. Withdraw or restate the conflicting intent rather than expecting dispat to order two separate histories.

A commit that only moved a fleet link is never counted as a change to the package enclosing it. The pointer records
what a release incorporated; the source commit in the other repository supplies the release intent.

## Settling the links before a package publishes

A release must record which revisions of other repositories it included. With no control repository to hold a
checkpoint, peers record that evidence in their links **before** publication. This is called settling the links.

For a package whose plan used another repository's history, dispat follows the route to that repository. Starting
at the deepest hop, each repository commits the revision of its next hop. The consumer then records its first hop.
These are ordinary commits using the release message:

```console
$ git log --format=%s -3
chore(release): api-pkg@0.1.0
chore(release): api-pkg@0.1.0
feat(api-pkg): bootstrap api
$ git show --stat HEAD~1
 .links/sdk | 2 +-
```

The newest commit is the release commit named by the tag. The one below it moved the link. The tagged tree therefore
contains the pin the next plan will read.

The recording rules are:

- **Settlement publishes no artifact.** An interruption leaves ordinary commits. The next run skips matching pins
  and pushes a recorded head if the remote does not yet have it.
- **It is all or nothing per package.** If a repository on the route has release commits disabled while the consumer's
  repository has them enabled, dispat refuses that package with `E333` before it publishes, rather than publishing a
  release whose evidence stops halfway.
- **Overlapping routes cannot deadlock publication.** Settlement takes the required publish lanes in repository-name
  order and releases all but the consumer's own before publishing.

If the consumer's release commits are disabled, it makes a valid tag-only release and logs that fact at info level.
No link settlement is recorded, so a later cross-repository boundary needs the explicit tuple described below.
dispat also skips settlement when all pins already match; it never creates an empty commit to mark one.

Because a settlement commits and pushes in a repository, that repository's `beforeCommit`, `afterCommit`, `postCommit`,
`beforePush` and `afterPush` hooks bracket it, outside the settlement's Git transactions and only when a commit or a
push actually happens.

A settlement moves a repository's head before the package publishes, and dispat admits exactly the revision it wrote
as that repository's expected head. It checks the head the run expects before it writes, so a head that anything else
moved is still `E330`.

## Freshness, and a pin that never outruns its target

Before recording a peer revision, a repository that pushes checks whether the peer's remote already has it. It checks
the peer's `commit.branch`, falling back to the roster's `branch`. This keeps a recorded link from naming a commit
other checkouts cannot fetch, just as a control checkpoint must.

Before planning, every participating repository that pushes must have a reachable remote, and its checked-out branch
must not be behind. A detached checkout has no branch to compare at that point.

Once the plan identifies where links must be recorded, dispat also checks each destination branch for those commits.
If it differs from the checked-out branch, it must not be behind either.

Every linked checkout is detached, because that is what `git submodule update` leaves behind. A repository that must
push what it records therefore needs `commit.branch`, and `E337` stops the release before publication without one.
Give every peer a `commit.branch` in its own configuration.

## When a boundary needs help

Within one repository, a package's own tags establish its pending windows. Across repositories, dispat needs to know
which revision of the other repository a consumer release already incorporated, and it reads that out of the links:

1. the consumer's release tag must sit on an ordinary release commit whose message names that exact tag;
2. following the one route of links between the two repositories, hop by hop from that commit's tree, each hop must
   record a link to the next; and
3. the revision the last hop records must be present in that repository and reachable from its planned head.

A matching pointer proves nothing on its own. A tag attached later to a commit whose links were recorded for another
release would otherwise be read as evidence. When the chain cannot prove the boundary, dispat reports `E333` naming
the hop it stopped at, and you state the association:

```yaml title="api/dispat.yaml"
repositoryBaselines:
  - consumer: api-pkg
    releaseTag: api-pkg@2.4.0
    repository: sdk
    revision: 6f1a9f0d2b90c8f96a4d74dcb6568fd373b22c16
```

This says `api-pkg@2.4.0` included `sdk` through that revision. Put the entry in any peer that knows the association;
dispat merges `repositoryBaselines` from every composed peer before resolving it.

Two peers may declare the same consumer, release tag and repository if their revisions resolve to the same commit.
If they resolve to different commits, `E333` names both peers and revisions, regardless of where the run started.
Correct the conflicting entry before retrying. An explicit tuple takes precedence over link evidence. `control` is
not a valid repository here because this fleet has no control participant.

## Repository participation

`repositoryOverrides.<peer>.enabled: false` takes a peer out of the run, exactly as it does with a control repository:

```yaml
repositoryOverrides:
  legacy:
    enabled: false
```

Participation is read only from the peer where this run starts. Other peers' participation settings do not affect
the invocation. A key naming no member of the entry peer's roster is refused, as is an attempt to exclude the entry
peer itself.

## Releasing one peer alone

Use `--polyrepo=false` to override the mode enabled by repository identity and release only the current repository:

```console
$ dispat --polyrepo=false release
09:31:07 INF fleet links skipped by --polyrepo=false; releasing this repository alone repository=api
```

No fleet is composed, so nothing outside this repository is planned, locked, settled or recorded.

Check cross-repository dependencies before using this flag. Their providers are absent from a standalone run, so
discovery fails unless the edges are marked `external: true`.

An external edge becomes inactive and reports `W330`. The release can then proceed, but **it records no link
settlement**. The next fleet plan has no evidence for that tag's cross-repository boundary and reports `E333`; supply
the `repositoryBaselines` tuple above to resolve it. Use standalone release for an independent peer, or be prepared
to state the boundary by hand.

## Selecting work with `--since`

`--since` names a revision of the repository you are standing in. dispat projects it into one range per repository by
following the same link routes the boundary evidence uses, and evaluates each repository once:

```sh
dispat run tests --since origin/main --consumers
```

A repository that revision records no link for contributes its whole reachable history, which is what happens for a
peer added to the fleet after the selected revision. `--since all` keeps its usual meaning and selects every package.

## CI checkout

A fleet job checks out one peer and materialises its links. Do **not** ask for recursive submodules:

```yaml
- uses: actions/checkout@v4
  with:
    fetch-depth: 0
    submodules: true
    token: ${{ secrets.FLEET_TOKEN }}
- run: dispat status --require-release
- run: dispat release
```

Three details matter:

- **`submodules: true`, never `submodules: recursive`.** Each peer carries a back-link to the repository linking it,
  and recursion fills it with a second copy of that repository. dispat never walks into it and never stages it.
- **`fetch-depth: 0` in the entry repository and complete history in every peer.** dispat counts commits since a tag,
  and a shallow peer stops the run with `E330`.
- **A token that can read the peers.** The automatic `GITHUB_TOKEN` is scoped to the repository running the job, so
  private peers fail to clone with it.

The checkout the action produces puts each peer at the revision its link records, and that pin is advisory. Run
`git submodule update --init --remote -- <path>` for a link you want at its branch tip instead, one path at a time.

Every peer is detached under this checkout, so give each one a `commit.branch` in its own configuration if it will push
what it records.

## Diagnostics

| Code | Means |
|------|-------|
| `E330` | A link was never materialised, its folder holds no repository, a peer has incomplete history, or a head moved before publication. |
| `E332` | A key only a control repository can own was used, a package name is declared twice, or participation names no roster member. |
| `E333` | A cross-repository boundary cannot be proven from the links, or a repository on a settlement route cannot record its hop. |
| `E334` | Two incomparable revisions need one winner and no unit of this fleet can arbitrate. |
| `E337` | A repository must push what it records and is detached without `commit.branch`. |
| `E338` | The fleet links do not form a tree: a second route reaches a repository already entered. |
| `E339` | An identity cannot be trusted: it is missing, reserved, malformed, repeated in a roster, self-naming, or contradicted by the link it was reached through. |
| `W331` | The release lock is off for the repositories the warning names. |
| `W332` | A link only one of its two repositories declares. A release started at the other end would compose a smaller fleet. |
| `W333` | A peer's roster does not name every member of the fleet, so a release started there plans without them. |

[Diagnostic codes](./reference/plan-errors.md#polyrepository-snapshot-and-recording-diagnostics) gives the recovery for
each one. `W332` and `W333` are what `dispat compute` repairs.

## What to watch for

- **The links are the record.** A settlement commit looks like a pointer move and is exactly that, and it is the only
  place a choreographed fleet writes what a release incorporated. Do not rewrite, squash or drop one.
- **`git submodule update --recursive` breaks the shape.** The empty back-link folder is deliberate. Update one path
  at a time, without recursion.
- **A peer's lock setting is its own.** `unsafeDisableLock` in one repository releases that repository without a lock
  and no other. The environment kill switch applies to everything the invocation releases. Both warn with `W331`.
- **The fleet lock takes every peer in repository-name order** and releases in reverse, so two runs of the same fleet
  contend in the same order.
- **Run-scoped settings come from the entry.** Concurrency, logging and the root-level `webhooks` list are read from
  the configuration of the repository the run started in, so a release started in a different peer can behave
  differently. Per-package settings still come from the peer that owns the package.
- **A roster the fleet outgrew is a warning, not a repair.** `W333` tells you a peer cannot plan a boundary across a
  repository it has not heard of. Run `dispat compute` in that peer rather than assuming the next release will notice.
- **A centrally configured repository does not merge as a peer.** Linked peers own their own policy and records;
  centrally imported sources preserve the control repository's established ownership behavior.

## See also

- [A control repository](./control-repository.md) for central hub topology, and for the pointer-history pattern that
  needs no conventional commits in the linked repositories at all.
- [Distributed execution](./distributed-execution.md) for running the builds of one release on several machines.
  Execution links are not fleet links: a worker node runs commands and owns no history, so it never joins the roster,
  and a peer's own `execution` object is ignored because the run reads that object from the repository it was started
  in alone.
- [One repository or many](./monorepo.md) for the underlying decision.
- [The compute command](./cli/compute.md) for the dependency edges and baselines the same command proposes.
- [Configuration](./configuration/README.md) for every key, and [models](./go/models.md) for the Go shapes.
- [Diagnostic codes](./reference/plan-errors.md) for the recovery of each code above.
