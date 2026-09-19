# A choreographed fleet of repositories

A [control repository](./control-repository.md) is one way to give dispat a graph across several Git repositories. A
choreographed fleet is the other. There is no control repository. Every repository is a peer that states its own
identity, keeps its own configuration and writes its own release records, the peers are joined to each other by
ordinary two-sided git submodule links, and a release can start in any of them.

dispat calls the two arrangements **sagas**. `saga: orchestration` is the control-repository protocol and the default.
`saga: choreography` is this page. A configuration that names no saga behaves exactly as it always has.

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

Choose a control repository when one team owns the fleet's release policy and you want fleet-wide directives. Choose a
choreographed fleet when each repository owns its own release policy and any of them should be able to release the
whole graph. The cost is that no commit can address the fleet as a whole: every unit is read against the repository
that carries it.

## The three keys

Each peer declares the saga, its own identity, and the roster of the fleet it belongs to. Here is a three-repository
fleet of `sdk`, `api` and `web`, where `api` consumes `sdk` and `web` consumes `api`.

```yaml title="sdk/dispat.yaml"
saga: choreography
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
saga: choreography
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

- **`saga: choreography`** selects the protocol. It also turns on polyrepository mode, so you do not write
  `polyrepo: true` beside it. You can select it for one invocation with the global `--saga choreography` flag.
- **`repository`** is this repository's identity in the fleet. It is written from letters, digits, dots, underscores
  and hyphens, and `control` is reserved for the orchestration saga. The same spelling is the submodule name every
  link to this repository uses, which is what lets one identity be read from either end of a link.
- **`repositories`** is the roster: every other peer by identity, with the `url` a link to it is cloned from, the
  `path` a link to it occupies inside this repository (default `.links/<name>`) and the `branch` the link follows.

The roster states membership, not the links. A fleet of three repositories needs only two links, so a peer names every
member of the fleet and is linked to some of them. dispat walks the links to reach the rest.

Three keys belong to a control repository and are refused here, because a fleet with no control repository cannot
honour a policy written for one: `configs` and `--configs`, a `repositoryOverrides.<peer>.commit` object, and a
`repositoryBaselines` entry naming `control`. Writing `repository` or `repositories` without `saga: choreography` is
refused too, so a fleet never believes it is linked while nothing reads the link.

## Linking the fleet with `dispat compute`

The roster is what you write. The links are what dispat creates from it. Run
[`dispat compute`](./cli/compute.md) in any peer:

```console
$ dispat compute
+ link api sdk  connects sdk to the fleet
+ link api web  connects web to the fleet

2 suggestion(s); apply all with --write, choose with --interactive
```

There are three kinds of fleet change beside the dependency edges and baselines the command already proposes:

- `+ link <repository> <peer>` creates a link that is missing. dispat proposes the minimum set of links that connects
  everything the rosters name, chosen so no two repositories are ever joined twice. Three repositories get two links,
  never three, because a third would be a second route between two of them and a run refuses that with `E338`. The
  same line also proposes the half of a link only one of its two repositories declares, which is the state `W332`
  reports: the pair is already joined, so the proposal adds no route, fetches nothing, and writes the missing
  declaration inside the checkout the declaring repository already holds.
- `+ init <repository> <path>` materialises a link the fleet declares and this checkout does not have.
- `+ repository <repository> <peer>` adds a roster entry a peer has not heard of. The entry is written into that
  peer's own configuration file, because a roster is that repository's own statement.

Apply them with `--write`, or `--interactive` to answer per suggestion. `--check` writes nothing and exits `1` while
anything is pending, which is the CI gate for a fleet whose links lag its rosters.

What `--write` does, in the part of its output that concerns the links:

```console
09:31:07 INF created fleet link path=.links/sdk peer=sdk repository=api
linked sdk from api at .links/sdk
09:31:07 INF declared the other half of the fleet link peer=api repository=sdk revision=edc0c8a0188cb2e4d033d06a4b47e6062fe6763e
declared api inside sdk at .links/api
09:31:07 INF created fleet link path=.links/web peer=web repository=api
linked web from api at .links/web
09:31:07 INF declared the other half of the fleet link peer=api repository=web revision=edc0c8a0188cb2e4d033d06a4b47e6062fe6763e
declared api inside web at .links/api
created 2 fleet link operation(s); review and commit them in each repository
```

The forward half is a real checkout, made with `git submodule add`. The other half is declared inside that checkout
without cloning anything: dispat writes the `.gitmodules` entry, creates the empty folder and stages the gitlink,
pinned at a revision the linking repository's own remote can already serve. A linking repository with no remote has
that half withheld with a warning, because there is no revision a peer could fetch to pin it at.

What `--write` does not do:

- **It never commits.** Every repository it touched is left with staged changes for you to read and commit yourself,
  in that repository, with whatever review that repository requires.
- **It never deletes a link.** A link is history's only record of what a release incorporated, so a link the fleet no
  longer needs is reported rather than removed.
- **It never recurses.** The back half of a pair is deliberately left as an empty folder. Never run
  `git submodule update --recursive` in a linked fleet: it fills that folder with a second copy of the repository you
  are standing in.
- **It writes no roster entry without a `url`.** An entry nothing can be fetched from would be a repair only halfway,
  so dispat reports it and you supply the url where the fleet declares it. A url carrying a username and password is
  refused outright, because `.gitmodules` is committed and pushed.

Commit the result in each repository and push. The fleet composes from then on.

## How a run composes the fleet

Any peer is a starting point. dispat reads the configuration of the repository you are standing in, follows its fleet
links breadth first, reads each peer's own configuration as that repository's root, and stops when every identity has
been entered once. The log says what it found:

```console
$ dispat status
09:31:07 INF polyrepo workspace composed root=/w/api entry=api repositories=["api","sdk","web"] saga=choreography
```

Two rules keep the walk unambiguous. Only a submodule the roster names is a fleet link, so an ordinary vendored
submodule takes no part. And the links must form a tree: reaching a repository a second time through another link is
`E338`, because a second route would be a second answer to which repositories lie between two peers.

A linked checkout that calls itself something other than the name it was linked as, or that does not itself state
`saga: choreography`, is `E339`. A link whose folder holds no repository is `E330`, and the message names the command
that initialises exactly that link. That is also what you get when you start a run inside another repository's linked
checkout: the back-link there is an empty folder by design.

The pin a link records is **advisory**. Two repositories that link each other can never both record the other's
current revision, so dispat composes at the revision each checkout actually holds and never fails because a pin
disagrees. Everything else about the fixed snapshot is unchanged: the heads read at composition are the heads the plan
is computed from, and a relevant head or tag that moves before publication is `E330`.

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

A control repository writes a checkpoint after a release. A choreographed fleet has no such repository, so the same
fact is written into the links themselves, and it is written **before** the package publishes so that the release
commit the tag sits on already carries it.

Before a package whose plan read history from another repository publishes, every repository on the route to that
repository records the revision of its next hop, deepest hop first, and the consumer records its own first hop. The
settlement is an ordinary commit carrying the release's own message:

```console
$ git log --format=%s -3
chore(release): api-pkg@0.1.0
chore(release): api-pkg@0.1.0
feat(api-pkg): bootstrap api
$ git show --stat HEAD~1
 .links/sdk | 2 +-
```

The first of the two is the release commit the tag names, and the one below it is the settlement that moved the link.
The tag's own commit therefore carries the pin in its tree, which is exactly what the next plan reads back.

Three properties make this safe to do before publication:

- **Nothing is published by it.** A settlement that is interrupted leaves ordinary commits and nothing external. The
  next run reads what each repository already records, does nothing where it matches, and pushes a head the remote
  does not hold yet.
- **It is all or nothing per package.** If a repository on the route has release commits disabled while the consumer's
  repository has them enabled, dispat refuses that package with `E333` before it publishes, rather than publishing a
  release whose evidence stops halfway.
- **It never holds two repositories at once.** The publish lanes a settlement needs are taken in repository-name
  order and all but the consumer's own are given back before publication, so two consumers with overlapping routes
  cannot wait on each other.

A consumer whose own release commits are disabled records nothing. That is a valid tag-only release, dispat says so at
info level, and a later boundary across it needs the explicit tuple described below. No settlement is created where the
recorded pins already equal the revisions to record, and no empty commit is ever made to mark one.

Because a settlement commits and pushes in a repository, that repository's `beforeCommit`, `afterCommit`,
`postCommit`, `beforePush` and `afterPush` hooks bracket it, outside the advisory lock and only when a commit or a push
actually happens.

## Freshness, and a pin that never outruns its target

Before it records a revision of a peer, a repository that pushes asks that peer's own remote whether it already holds
that revision. The branch it asks about is the peer's own `commit.branch` when it has one, and otherwise the `branch`
the fleet roster states for it. A recorded link therefore never names a commit nobody else can fetch, which is the same
rule a control checkpoint follows.

Every repository a release records a link in is checked the way a publishing repository always is. With pushing
enabled, its remote must be reachable and its branch must not be behind: a checkout that is behind stops the run
before anything is written.

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

This says `api-pkg@2.4.0` incorporated `sdk` through that revision. Write the entry in whichever peer knows it: dispat
merges the `repositoryBaselines` of every composed repository before it resolves a boundary, because there is no
central file to collect them in. An explicit tuple wins over the link evidence. `control` is not a valid `repository`
value here, because this saga has no participant by that name.

## Repository participation

`repositoryOverrides.<peer>.enabled: false` takes a peer out of the run, exactly as it does with a control repository:

```yaml
repositoryOverrides:
  legacy:
    enabled: false
```

The difference is where it is read. A peer owns its own release policy, but whether it takes part in this run is the
invocation's question, so participation is read from the configuration of the repository the run started in and from
nowhere else. A key naming no member of that repository's roster is refused, and so is excluding the repository the
run started in.

## Releasing one peer alone

`--polyrepo=false` is the escape hatch. It clears the polyrepository mode that `saga: choreography` turned on and
releases the repository you are standing in, by itself:

```console
$ dispat --polyrepo=false release
09:31:07 INF fleet links skipped by --polyrepo=false; releasing this repository alone repository=api saga=choreography
```

No fleet is composed, so nothing outside this repository is planned, locked, settled or recorded.

Know the hazard before you use it on a repository that consumes packages from its peers. A dependency on a package in
another repository has no provider in a standalone run, so package discovery fails with an unknown provider unless the
edge is marked `external: true`. With that marking the edge goes inactive, `W330` says so, and the release proceeds and
writes its release commit **without settling any link first**. The next fleet-wide plan then finds no evidence for that
release tag and reports `E333`, and the remedy is the `repositoryBaselines` tuple above. Use the flag for a peer whose
packages depend on nothing outside its own repository, or accept that you will state the boundary by hand.

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
- **A peer that is itself a control repository does not merge.** A fleet link may not cross sagas, and a linked
  checkout that does not state `saga: choreography` is `E339`.

## See also

- [A control repository](./control-repository.md) for the other saga, and for the pointer-history pattern that needs
  no conventional commits in the linked repositories at all.
- [One repository or many](./monorepo.md) for the underlying decision.
- [The compute command](./cli/compute.md) for the dependency edges and baselines the same command proposes.
- [Configuration](./configuration/README.md) for every key, and [models](./go/models.md) for the Go shapes.
- [Diagnostic codes](./reference/plan-errors.md) for the recovery of each code above.
