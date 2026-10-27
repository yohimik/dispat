# Distributed execution

Use workers to run parts of a release on other machines. The first node is the **orchestrator**. It also
executes eligible tasks, and it alone advances canonical task and ledger refs, publishes artifacts, and writes
release records. Other **workers** execute eligible work, create their own pending and outcome Git branches,
and return claim and result requests to the orchestrator over the authenticated non-Git channel.

`--distribution-mode=compilation` is the default. The orchestrator computes the plan and graph,
creates an inert branch for every executable logical task before work starts, and publishes a signed readiness feed.
Workers select eligible ready tasks from that graph. In `interpretation`, every admitted node independently derives
the same semantic plan and graph from the pinned input, verifies both digests, and chooses locally ready work.
The orchestrator validates and persists each exact proposal in either mode. An accepted signed claim and a
confirmed remote pending ref permit the command to start. A missing input, different digest, or missing required
attestation refuses execution before any claim. `hybrid` remains unavailable and is refused before task effects.

To try it, [configure the nodes](#configuring-the-nodes), [start a worker](#starting-a-worker), then choose
[where stages run](#where-a-stage-runs) and [which build outputs travel](#build-outputs-as-inputs). Read the
[security section](#security-what-distributed-execution-exposes-and-how-to-contain-it) before sharing a pool or using
a public repository as its mailbox. For a stalled or uncertain run, go to [recovery](#locks-failure-and-recovery).

With no `execution` object or an empty `workers` list, releases still run on one machine. The same node pool can
also run [`dispat run` scripts](#running-scripts-on-workers) without releasing anything.

Distributed first-node and worker processes currently require Linux or macOS. Their shared weighted node-slot
journal needs verified owner-only storage and durable directory updates; Windows distributed execution is refused
before task effects. Local execution without worker links and without explicit shared node capacity remains
available on Windows.

## When to choose it

Workers are useful when several long builds can run independently, or when a release needs platforms the
current machine cannot build for. Look at the dependency graph: more nodes help only while several tasks are ready.

For short builds, start with one machine. Each delegated task transfers a source snapshot, prepares a checkout and
sends outputs back. That setup can take longer than the build itself.

|                                          | One machine                                     | Orchestrator and workers                           |
|------------------------------------------|-------------------------------------------------|-----------------------------------------------------------|
| Where build commands run                 | the machine the release was started on          | a worker, or the orchestrator when eligible        |
| Where publish commands run               | the machine the release was started on          | the orchestrator                                         |
| Where release locks and records live      | one machine                                     | the orchestrator alone                                    |
| Where the plan is derived                 | the first node                                  | the first node in compilation; every admitted node in interpretation, from identical pinned input |
| What a build product travels as          | a folder in the checkout                        | a verified manifest and bytes returned to the orchestrator |
| What has to exist beforehand             | the checkout                                    | a shared signing secret and a fixed authorization policy; late workers may join while tasks run |
| What a lost machine costs                | the run                                         | the packages that node was working on                     |
| Extra failure to read                    | none                                            | a claim, return-channel or Git coordination failure        |

There is no published performance comparison yet. Measure your own graph, machines and transfer time before assuming
that a larger pool will shorten the release.

### Recommended layout with one orchestrator

Run one release job as the orchestrator and start its workers beside it. Every node can run eligible build,
test and custom stages before the records. The orchestrator performs the publish stage and pushes canonical
coordination and release refs. Keep registry and release-write credentials there. Workers need a Git credential
that can create their own pending and outcome branches, read access to exact pinned inputs, the shared execution
signing secret, and access to the authenticated return channel; build commands may need separate credentials for
private inputs.

The orchestrator plans the run, compiles the graph in compilation mode, checks locks, validates and persists claims and results,
prepares native source state, authorizes publication, and writes native Git and release records. Run-level hooks
and stages after the records run there. Coordination and preparation consume time; adding nodes helps only when the graph
has ready work and transfer costs leave room for a gain.

## Orchestrator and worker responsibilities

All nodes use the same binary, with different authority for one run:

**The orchestrator owns publication.** It locks the repositories, commits the pinned planning input, verifies the
agreed plan and graph identity, persists task claims and results, runs publication, writes release records and runs
the run-level hooks. It executes eligible build and validation tasks.

**A worker executes work.** It verifies the signed compiled task graph and exact inputs, chooses compatible ready
work within its physical capacity, and requests an exact claim. It runs a task only after
the orchestrator confirms that the authoritative claim landed and the worker confirms its pending branch's
exact remote OID from that accepted task tip. It pushes a signed output or failed branch descended from that pending tip, then returns the
result for the orchestrator to verify and settle.
A stage and its surrounding hooks form a **frame**, which always runs together on one node.

The `dispat worker` command serves a node; it does not initiate a release. Processes running under a task's
authority cannot initiate one. A build script therefore cannot start a second release of the repository it builds.

## Configuring the nodes

Configure `execution` on the orchestrator and each worker. The orchestrator's peer list needs the node names:

```yaml title="dispat.yaml on the orchestrator"
runOnly: [worker, orchestrator]

execution:
  concurrency: 2
  secretEnv: DISPAT_EXECUTION_SECRET
  workers:
    - name: build-a
    - name: build-b
      lateJoin: true
      identityPublicKey: <base64url-ed25519-public-key>
  timeouts:
    preflight: 60
    admission: 600
    task: 3600
    cancel: 60
```

```yaml title="dispat.worker.yaml on a worker node"
execution:
  name: build-a
  endpoint: git@github.com:acme/project.git
  secretEnv: DISPAT_EXECUTION_SECRET
  concurrency: 2
```

The [`execution` reference](./configuration/execution.md) lists every key and default. For this setup, check three
details:

`build-a` must pass preflight before any task begins. `build-b` may join while tasks are already running.
Configure `build-b` with `identityKeyEnv` naming the secret store variable that holds its matching base64url
Ed25519 private seed. Its identity is authorized by the signed bootstrap and its independent signature;
its capabilities and capacity are verified before its
first claim. A worker-only task can wait for an eligible late worker until the signed admission deadline; unrelated
ready work continues. The deadline cannot release an accepted or uncertain claim.

Start a remote-node run with an HTTPS return endpoint reachable by every worker, for example:

```sh
dispat release --distribution-mode=compilation \
  --return-endpoint=https://orchestrator.example:8443
```

`--return-listen=host:port` overrides the local bind address when it differs from the advertised URL. Distributed runs
require the return endpoint with remote workers; a local-only run needs no listener. The Git `endpoint`
in the configuration is a repository URL and cannot substitute for this HTTPS address.
The orchestrator includes its advertised return URL and exact TLS leaf SHA-256 in signed per-run bootstrap data.
A worker reads and verifies that data before sending a claim or result; it needs no separate return URL
in its configuration.

- **Only the startup root selects execution policy.** Space, package, package-folder and imported settings do not
  supply it. A linked peer may have its own `execution`, but the combined run ignores it and logs that at debug level.
  A transferred checkout cannot choose the receiving machine's role.
- **A node's name is its identity in the transport.** The name is written into the branches addressed to it, and the
  worker's `execution.name` has to be the `name` of the orchestrator's link to it.
- **Endpoints contain no credentials.** Supported forms are `https`, `ssh` and `file` URLs, absolute paths and
  scp-like `host:path`. Unauthenticated `http` and `git`, and URLs carrying user information, are refused. Use Git's
  credential helper or `http.extraheader` on the orchestrator instead. The same rule applies when an omitted endpoint
  resolves to the release remote's push URL: a token in that URL fails with `E225` before locking (CCME §28.2).

### The coordination repository

The coordination repository holds authoritative task refs. The orchestrator alone advances those refs. Without an
explicit `endpoint`, it uses the push URL of `commit.remote`, falling back to `origin`; in a composed workspace,
this is the entry repository's remote. A worker fetches pinned inputs and coordination state, creates its own
pickup branches there, and returns claim and result requests through the authenticated non-Git channel.

Task reservations live under `refs/heads/dispat-task-*`, reusable worker notices under
`refs/heads/dispat-slot-*`, and preflight probes under `refs/heads/dispat-worker-*`. Completed task branches
remain as signed records of their finished work, including tasks that produced no files. A worker creates an
immutable `dispat-pending-*` branch for each physical pickup and a descendant `dispat-output-*` or
`dispat-failed-*` branch for its result. These branches have unique pickup IDs; a retry after failure or lost contact
uses a new pickup while an uncertain old lease stays charged until its exact finality is known. The first verified
success settles the logical task. Using the source
repository avoids retransferring objects the remote already has; it receives only the task's changes.

Using the repository itself has consequences worth knowing before the first run:

- **Whatever travels is readable by whoever can read the repository.** Source snapshots, build outputs, the values
  scripts export and the command text of every delegated stage are pushed to it, so on a public repository they are
  public. A snapshot is the working tree as the orchestrator had it, which includes files that are untracked and not
  ignored and commits that were never pushed, and a host keeps an object fetchable by its id for a while after the
  branch that carried it is deleted.
- **The host's ref rules keep publication under one authority.** Workers need Git write access for their own
  `dispat-pending-*`, `dispat-output-*` and `dispat-failed-*` refs. Protect canonical task and ledger refs,
  release branches, tags and the lock tag from worker credentials on the Git host. A repository-wide contents-write
  token does not by itself restrict which refs its holder can push. On GitHub, use
  [branch and tag rulesets](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-rulesets/available-rules-for-rulesets)
  to restrict creation and updates of the protected refs for the worker App identity.
- **The host's size limits apply.** GitHub warns about a file over 50 MB, rejects a file over 100 MB and rejects a push
  over 2 GB, and one task's output set travels in one push. `transfer.maxBytes` bounds what dispat sends; the host's
  limits bound what it accepts.
- **A composed workspace uses the entry repository.** The histories of the peers a task reads are pushed to the entry
  repository's remote, because that is where the node reads them from.

Set `endpoint` to use a different coordination repository. Several workers can share one, and the
orchestrator makes their exact predecessor objects available before granting work.

For a custom mailbox, a supported self-hosted Git server in the same data center as the orchestrator and workers can
reduce transport latency. Point each applicable worker link and node configuration at that `endpoint`; measure
transfer time with the actual repository and outputs. The mailbox is coordination storage, not a release record.

A separate mailbox initially has none of the source objects, so its first task transfers the source's reachable
history. See [transfer costs](#what-to-watch-for) before choosing that layout for a large repository.

### The signing secret

Every message is signed with a shared secret. `execution.secretEnv` names the environment variable each node reads.
Put only that variable name in the configuration; supply the secret through the node's environment.

The secret is symmetric. Every node holding it can sign any message, so it is the trust boundary of the whole pool:
see [the security section](#security-what-distributed-execution-exposes-and-how-to-contain-it) before sharing one
secret across machines of different trust levels.

## Starting a worker

A worker runs [`dispat worker`](./cli/worker.md). It needs its configuration file, the signing secret in its
environment, and whatever its build commands need:

```sh
export DISPAT_EXECUTION_SECRET="$(cat /run/secrets/dispat-execution)"
dispat worker --config dispat.worker.yaml --state-dir /var/lib/dispat/worker --idle-timeout 900
```

Workers need read access to exact pinned Git inputs and an authenticated return channel to the orchestrator.
Git polling does not carry claims or results.

With an explicit `endpoint`, a worker needs no existing source checkout or Git repository in its startup folder.
The signed compiled task template supplies commands, environment names and exact source states. The worker creates a
checkout for the work it accepts.
Without an endpoint, start it in the release repository's checkout. Otherwise, or if that checkout's push URL cannot
serve as a mailbox, startup fails with `E225`.

`--idle-timeout` exits with code `0` after the configured seconds with no claimed or in-flight work, measured from
the last activity. Use it for workers started for one release. `SIGINT` and `SIGTERM` wait for claimed work to finish,
then also exit with code `0`.

## Where a stage runs

Use `runOnly` to choose where a package's build stage can run. The publish stage always runs on the orchestrator.
Supply a value for both positions or a `[build, publish]` pair; the publish position accepts `both` or
`orchestrator`, which both mean orchestrator placement:

```yaml
runOnly: [worker, orchestrator]   # build on a worker, publish on the orchestrator

packages:
  ui:
    runOnly: [both, orchestrator] # build anywhere there is room, publish here
  signer:
    runOnly: orchestrator         # this build signs its artefact here
  linux-image:
    runOnly: [worker, both]       # worker build; publish still stays here
```

| Value          | What it means                                                                                     |
|----------------|---------------------------------------------------------------------------------------------------|
| `both`         | the default: a build is eligible on a compatible worker or the orchestrator; publish stays on the orchestrator |
| `worker`       | build may only be delegated; a run with no worker links is refused with `E225` before any stage runs |
| `orchestrator` | the stage may only run on the machine the release was started on                                   |

The setting follows the same override order as `buildOutputs` and `buildPlatforms`: root, space, space-folder file,
package, then package-folder file. An override replaces the whole pair. Scalar `runOnly: worker` is refused because
it also sets the publish position to `worker`; migrate it to `runOnly: [worker, orchestrator]`.

Placement follows these rules:

- **A frame runs as one unit on one node.** There is one placement question per frame, not one per command.
- **The orchestrator also has build capacity under `both`.** It joins the pool with capacity
  `execution.concurrency`; the first node and admitted peers can claim compatible ready tasks. A build it keeps
  still captures and admits its declared outputs.
- **Publication remains here.** `both` and `orchestrator` in the publish position have the same placement. A worker
  value in that position is refused when the configuration loads, whether or not the space has `flow.login`.
- **Pinning costs parallelism.** A frame pinned to the orchestrator waits for one of this machine's own slots, and a
  frame pinned to a worker waits for a compatible node even when this machine is free. A package pinned to the orchestrator is
  not held to the workers' platforms at preflight, which is what lets one machine sign what a pool of other
  architectures builds.

`buildPlatforms` is the other placement filter: the `os/arch` values in Go's spelling that a package's build may run
on, empty meaning any node. A preparation obeys the provider's own `runOnly` and `buildPlatforms`, not its
consumer's.

### The test stage

Configure [`flow.test`](./configuration/spaces.md#stages-and-hooks) or one of its hooks to run a test frame after
build and before publish. Tests check the build's output and use build placement rules:

- **It reads the build's `runOnly` value** with the build's meaning, and the same `buildPlatforms`. A space's login
  does not pin it, because a login is the publication's affair.
- **The node that built the package is asked for first.** Its cache already holds the objects the frame reads, and a
  busy one is passed over rather than waited for.
- **The package's own admitted outputs are installed first** on a node that did not build them, together with the
  provider outputs its build was given, under the same checked input contract. A frame placed on the orchestrator
  installs nothing: the outputs are already in its checkout. A package that declares no `buildOutputs` hands a node
  its sources alone, so declare what the test scripts read.
- **It produces no output set.** The admitted set stays the one the build reported. What its scripts export travels
  back in the signed result and reaches the publish, as a build's exports do.

The test runs as a separate `<package>:test` task and shares the build budget. Its task kind is `stage`; its `stage`
field supplies `DISPAT_STAGE` and the hook names. Build work keeps its existing `build` kind; publication is local
to the orchestrator.

Run the same dispat version on all nodes. An older worker that does not understand the generic `stage` kind leaves the
task unclaimed; it is placed again and eventually fails the package if no node claims it.

### Stages of your own

A [stage the flow defines](./configuration/spaces.md#stages-of-your-own) travels like the test stage, under the
generic `stage` kind naming it, and follows the build's placement rules:

- **Before the release records** it reads the build's `runOnly` value with the build's meaning and the same
  `buildPlatforms`, holds a slot of the build budget, and runs as a `<package>:<stage>` task of its own. Where the
  build reaches it in the [package graph](./configuration/spaces.md#the-order-of-the-stages), it is given the package's
  own admitted outputs, like the test stage; where it does not, it is given the providers' outputs, like the build.
  Output sets travel along the graph's edges only, so a stage never reads what a stage beside it produced. It
  produces no output set of its own: the admitted set stays the build's, and what its scripts export travels back and
  reaches the stages after it.
- **After the release records** it never withholds the record: it runs on the orchestrator and only warns, as the
  announce stage does.

Two stages the flow leaves unordered are two tasks: they take two slots and may land on two nodes, which is how a
fleet runs every independent stage of a package at once. A stage you define sits beside the test stage by default, so a
package with test and two stages of its own fans out to three nodes once its build is admitted.

## How a run distributes tasks

A distributed release follows the local release stages, with these additional coordination steps:

1. **The locks come first.** Every participating repository's release lock is acquired in name order, before
   anything is planned. A run that would release without the lock is refused: see
   [the release lock](./reference/releasing/release-lock.md).
2. **The plan is fixed and named.** The plan is computed under the locks and hashed into a plan digest, logged once
   as `plan fixed`. Every message of the run states that digest, so a node can refuse work belonging to another
   planning of the same repository. `dispat status` fixes and prints the same digest without starting anything.
3. **Required workers are probed.** Each configured worker without `lateJoin: true` must answer preflight before
   task effects. Its answer establishes protocol, platform, capacity and transfer limits. A preauthorized late
   worker may be absent; it must pass signed admission by the finite `timeouts.admission` deadline.
4. **The finite graph is published.** After preflight and graph compilation, the orchestrator creates one signed,
   inert task ref for every executable logical task, including first-node-only work, and publishes the signed graph
   root and node notices. Task identity does not change when a late worker joins or a claim is retried. A failed
   initialization batch starts no package work. Local preparation, version, sign and publication do not create
   executable task refs.
5. **Ready tasks are claimed.** Admitted nodes discover compatible ready tasks from the same graph. The first
   accepted authenticated claim owns a task; the broker checks current membership, capacity, placement, budgets,
   source and predecessor objects before accepting it. The worker builds from exact pinned inputs, never from a
   moving ref tip. A late worker can claim a task that already existed before it joined.

What stays on the orchestrator:

| Work                                                          | Why it does not travel                                      |
|---------------------------------------------------------------|-------------------------------------------------------------|
| the sign and version stages and the manifest and lock-file preparation | they write the state every build of the run then consumes |
| the space login                                                | authentication must not travel                              |
| release commits, tags, changelogs, GitHub releases, records     | a worker is never granted the right to write a release ref  |
| `postPublish`, every stage after the records (`announce` by default), `onFail`, `onSkip` and every run-level hook | they observe the run, which exists here          |

What a worker runs: the build frame (`beforeBuild`, the build commands, `postBuild`), the test frame (`beforeTest`,
the test commands, `postTest`) where the package has one, and the frame of every [stage of your
own](#stages-of-your-own) before the records. The publish frame (`beforePublish`, the publish commands and its after
hook) runs on the orchestrator. A frame runs as one unit on one node, so its hooks see that node's files.

**Run budgets and node capacity are separate.** Root `concurrency` limits builds and publishes across the whole run;
adding workers does not multiply it. A task holds its stage slot while waiting for a node. `execution.concurrency`
limits the claimed tasks one node accepts across all runs using it.

Ready work waits for a compatible free slot. A still-inert worker-only task with no admitted compatible worker at
the admission deadline ends with a signed no-claim outcome. That outcome is distinct from a command result, and
unrelated ready work continues while the task waits.

## Build outputs as inputs

Build products are usually ignored by Git, so another machine cannot get them from a source checkout. Declare the
folders a build produces with `buildOutputs`:

```yaml
buildOutputs: [dist]

packages:
  assets:
    buildOutputs: [dist, generated/types]
```

The setting follows the normal override order: root, space, space-folder file, package, then package-folder file.
Each override replaces the whole list. Paths are literal, relative to the package folder, and use `/` on every
platform. Ignored files are included; transferring an untracked `dist` is the purpose of this setting.
Before a package's preparation commands run, dispat resets its declared roots to the pinned source snapshot.
Tracked and other admitted source files remain; stale ignored files under those roots are removed. Preparation and
build commands can then produce ignored files for later stages. Keep source inputs that preparation does not create
outside ignored output roots, or track them in Git.

### What travels and how it is checked

A successful build captures its declared roots in a Git tree. Its signed result includes a manifest with each entry's
type, mode, size and SHA-256. The header binds the set to the run, plan, task attempt, ownership, source states,
package, version, platform and output sets used as inputs.

The orchestrator checks the manifest against the tree before accepting the outputs. The consumer node checks them
again against its signed task's digest before installing any file.

A worker first creates a signed pending commit parented by the exact accepted task tip. It then creates a signed
output or failed commit parented by that pending tip; declared output bytes live at
`outputs/<declared-root>/...` on the outcome branch. The orchestrator verifies the branch and result before
advancing the assigned canonical task branch. Its completed tip carries the verified bytes and a signed task
decision. A task with no output files still gets a signed finished tip. The task tip records command finality;
package outcome and release publication settle separately after dependent work and any failure hooks.

### Installing the outputs

Files are staged and verified, then each declared root replaces its destination by a rename. No task command starts
until all inputs are installed; installation is atomic at that task-input boundary.

Staging normally uses the checkout's private Git storage. If the rename into the checkout fails, as it can when a
linked worktree's Git storage is on another filesystem, dispat stages again in a private `0700` folder beside the
outermost checkout. This keeps the final operation a rename and keeps temporary files out of Git status and release
records. If the second rename fails, installation fails with that error.

Normal completion removes the staging folder. A killed process may leave a `dispat-outputs-` folder in Git storage or
as a hidden sibling of the checkout. Remove that run's folder only after confirming that no release using the
checkout is active. dispat does not assume another run's staging folder is abandoned.

### Rejected outputs

An invalid output set fails its prerequisite with `E227` and blocks that prerequisite's consumers. The following are
rejected:

- A missing declared root, a manifest/tree mismatch, or totals that do not match the entries.
- An entry other than a file or symlink, a mode other than `0644` or `0755`, or two paths differing only by case.
- An absolute path, a path outside the declared roots, or one containing `..`, a `.git` component, a backslash, colon
  or NUL.
- A symlink target that is absolute or leaves its root.
- A platform mismatch, or a set exceeding `transfer.maxFiles`, `transfer.maxBytes` or `transfer.maxManifestBytes`.

The log uses a stable reason word instead of echoing task-produced text.

Tracked changes outside declared roots are counted and reported as `W244` stray writes. They are not accepted as
outputs. Declare every path a consumer needs.

### Providers that are not releasing

If a consumer needs outputs from a provider outside the release plan, the provider builds once as a `prepare` task.
It receives its current version and a bump of `none`, just as with `dispat run`. It gets no tag, changelog, record,
plan entry or event. The summary shows completed computation, accepted outputs and publication `none`.

This can rebuild a non-releasing package's `dist` in the orchestrator's checkout.

`isBuildWaitingPublish` also controls output transfer. With `{build: none}`, the consumer reads no provider outputs,
so none travel across that edge. A provider reached only through `none` edges is not prepared. See
[the provider relation](./configuration/spaces.md#the-provider-relation) and the worked numbers in
[the terraform example](./examples/terraform.md). An edge that waits for publication still waits: a consumer that
installs its provider from a registry is ordered after the provider's publication exactly as it is on one machine.

## Publishing on the orchestrator

The orchestrator runs `beforePublish`, the publish commands and the stage's after hook in its checkout. It checks
the release lock and relevant inputs after `beforePublish` and before the irreversible command. Verified worker
outputs are admitted into that checkout before the publish frame starts. One repository's packages publish and
record in order; independent repositories retain their dependency-safe concurrency.

Keep registry credentials on the orchestrator. Configuration `env` values such as `$NPM_TOKEN` are expanded on the
machine that runs the command, so a publish secret need not reach a worker. A worker needs the shared
execution secret, read access to pinned Git inputs, Git write access for its own pickup branches and access to the
authenticated return channel, but no permission to update release refs. Custom stage scripts and their hooks are executable policy; keep publication commands out of stages
placed on workers when their credentials must exclude publication.

## Running scripts on workers

`dispat run` can run a script across the worker pool without releasing packages. Configure workers in
`execution.workers` or pass [`--worker`](#naming-a-worker-on-the-command-line). Each selected package becomes one task,
running its script commands in the package folder.

The task receives `DISPAT_STAGE=run:<script>`, computed `DISPAT_*` values, unresolved configuration `env` pairs and
values exported by providers earlier in the same sweep. The worker prepares the package's source input closure and
returns exported values to its consumers, wherever they run.

Sweep tasks have no surrounding hooks and install no output files from other tasks. If a script needs a provider's
`dist`, configure it as a build instead.

```sh
dispat run tests --since all
```

### Placement and release locks

Placement uses the package's build `runOnly` setting. An eligible node with free capacity picks compatible ready
work from the signed compiled frontier. `orchestrator` stays local. `worker` requires an admitted worker or a
preauthorized compatible late worker before the admission deadline; otherwise the
sweep fails with `E225` before running scripts.

Without worker links, the sweep runs locally as usual: no fixed plan, release lock, distributed summary or mailbox.

A distributed sweep also takes no release lock. It records no releases and authorizes no locked publication.
Messages use a generation derived from the sweep's run identity rather than lock objects. A release of the same
repositories may run alongside it; neither waits for nor excludes the other.

A worker or a process under task authority still cannot start a distributed sweep (`E226`). A lock bypass with worker
links is refused (`E225`) because it declares that no coordination remote is available. Before work starts, the sweep
fixes and logs its plan as `plan fixed` and probes each required-at-start link. At the end, each `task outcome` line reports node,
computation, outputs and exported-value count, followed by `run finished`.

### Returning files from a script

Files written by a delegated task stay on its worker unless `runOutputs` declares them. In the startup root
configuration, name output folders per script, relative to each package's repository root:

```yaml
runOutputs:
  tests: [coverage]
```

After a task succeeds, the worker captures those folders using the same manifest, validation and transfer limits as
build outputs. The orchestrator verifies the sets and merges them into its checkout after every task has answered:

- **The merge preserves other files.** Each file replaces only its own path. For example, ten package tasks can
  contribute ten profiles to one `coverage/` folder.
- **A set is merged whole or not at all.** Every file of one task's set is staged and verified before the first one is
  moved, and a move that fails puts back the files already moved.
- **Two tasks may not disagree about a path.** Two tasks writing one path with different bytes fail the sweep with
  `E227`, naming the path and both tasks, and neither task's set is merged, so the folder holds neither file at that
  path. Identical bytes are merged once.
- **A folder the script did not write is an empty set.** This is a deliberate difference from a build output root,
  whose absence fails the build: a script may have nothing to write for a package.
- **A task this machine ran itself captures nothing.** Under `both` the orchestrator takes a task when no worker has
  room, and that task writes into this checkout directly, as every task of a sweep without workers does. The rule
  about disagreeing paths is checked between the sets that travelled.
- **An interrupted sweep merges nothing.** Already accepted sets stay where they are, and the run reports that no
  merge occurred. It does not assemble a partial folder from only the tasks that answered.

If the merge fails, for example because the checkout cannot accept a file, `dispat run` fails and logs
`run outputs could not be merged` at error level with the underlying cause. The task summary still follows, so the
failed merge is visible alongside the tasks that produced those outputs.

A `runOutputs` root cannot be a package folder, contain a package folder or overlap any package's `buildOutputs`.
`dispat status` reports these conflicts with `E225`. The distinction matters because build output roots are replaced
whole, while sweep outputs are merged file by file.

Like `execution`, `runOutputs` is read only from the startup configuration. Use Git-ignored folders; tracked or
non-ignored files also travel to every task as part of its prepared source state.

### Naming a worker on the command line

For workers created by CI, use repeatable `--worker name` flags on `release`, `run` or `status`. They add links beside
`execution.workers` and use the release repository as their mailbox. Use `--worker name=endpoint` for a different
mailbox:

```sh
dispat run tests --since all --worker ci-worker-1
```

The link is held to every rule a configured one is: a node name, a credential-free endpoint when it states one, a name
no other link folds to, and a `secretEnv` the file names. A value that is not a node name alone, or leaves either half
of `name=endpoint` empty, is a usage error. The link is refused with `E226` under a task's authority because a task
cannot expand the run's node set. It is no part of the plan digest, and
`dispat status --worker` prints the digest a distributed run would carry, resolves no remote and reaches no node.

## Locks, failure and recovery

With workers configured, `unsafeDisableLock`, per-repository bypasses and `DISPAT_UNSAFE_DISABLE_LOCK` are all refused
with `E225`. One orchestrator owns the compiled plan and its lock set during the run. Another attempted release of an
overlapping repository is refused by those locks. For a failed build, the next run can build again. A local publish
script can return an error after its destination accepted the artifact. That error does not automatically retain the
lock or establish the external outcome. Before trying the same publication again, establish destination identity
under [recovery](./reference/releasing/recovery.md) or use a publisher whose retry is safe for that exact artifact.
An opaque command without either capability needs operator reconciliation.

### Lock checks during the run

The orchestrator reads every release lock from its remote before the first worker probe, task activation and every
publication, including local publishes. It never reuses a previous successful check.

If the lock is gone, the run stops with `E336`, admits no new claims and withdraws attempts in flight. A
publication that already completed is still recorded: a create-only record of an existing effect is not a new
publication.

A failed read does not prove the lock was lost. Each read is limited to fifteen seconds. Failures are retried after
one second and then two seconds, with a warning each time. After three failed reads, the run stops as it would for a
lost lock, but reports `unverified` instead of `lost` and returns the lock it still owns. An interruption during a
read is not treated as lock loss.

### A worker stops answering

An unclaimed compute task can be withdrawn through an exact task decision and made ready again. Once an attempt is claimed,
passing its deadline removes that node from the pool. Its slot is not reused and downstream packages are not
attempted. A claimed task is never retried on another node within the same run; the next run builds it again.

A slot becomes available again only after a result, an acknowledged withdrawal, or proof that an unclaimed
offer can no longer be claimed. Writing cancellation under an exact lease supplies that
proof without a worker acknowledgement. Silence alone does not: the task may still be running.

### An uncertain publication

Publication runs on the orchestrator. If its command returns an uncertain result, a transport task receipt cannot
settle it. Reconcile the destination's package, version and artifact identity using any durable publication
receipt before any retry, following [recovery](./reference/releasing/recovery.md). Worker task timeouts concern build
and validation work; they cannot leave an in-flight worker publisher.

### Cleanup and leftover branches

Revoking an expired attempt or withdrawing one at shutdown waits at most `timeouts.cancel`. Closing coordination
branches uses that timeout clamped between 30 seconds and two minutes, and happens before release locks are returned.
An unresponsive mailbox can leave branches behind with `W244`; it does not prevent the run from returning locks that
are otherwise eligible for release. Local refs fetched by the run are removed regardless of remote cleanup success.

An internal coordination-branch failure is contained to that branch, node preflight or task. A completed run can
report leftover coordination refs with `W244` and still exit `0`; those refs are not release records.

For other branches from a crashed run, confirm that no process uses them and that their tips belong to the run's
authenticated chain before deleting them. Investigate unexpected or unauthenticated tips instead of treating them as
routine leftovers.

## Security: what distributed execution exposes and how to contain it

Workers add machines and repositories that can affect a release. Source and commands pass through the mailbox,
the shared secret authorizes work, and files built elsewhere may become the published artifact. Check these seven
parts of the setup before activating work.

**1. The repository holds everything that travels.** Full source snapshots, untracked files that are not ignored and
unpushed commits included, the command text of every delegated stage, the computed `DISPAT_*` values of each stage,
the values scripts exported through `DISPAT_OUTPUT` and the build outputs themselves are all pushed to the repository
being released, or to the endpoint a link names instead, and a host keeps an object fetchable by its id for a while
after its branch is deleted.

Treat transferred data as readable by everyone with access to that repository. With a public mailbox, do not delegate
private inputs or outputs. Keep unintended untracked, non-ignored files out of the orchestrator checkout. Use host
ref rules to protect release branches, tags and the lock tag from worker credentials.

Never export secrets through script outputs or write them literally in `env` or commands. Use `$NAME`, which travels
as a reference and is expanded from the executing node's environment.

**2. The signing secret is shared and symmetric.** Every node holding the secret can sign any message. A compromised
worker, or anyone holding the secret with push access to the repository the messages travel through, can forge a
compiled task, which is command execution on every other node sharing that secret, and can forge a result, which is a
build output the orchestrator would admit.

All machines holding one secret belong to one trust zone. Use separate secrets for different trust levels, store them
in the CI secret store or equivalent, and rotate them when a worker is removed or suspected of compromise. Never
commit the secret. Per-link secrets are not implemented, so every holder must be trusted to the same level.

**3. Workers run what they are told.** A worker executes the commands of any authentic compatible task, with its own
environment and its own credentials. That is what a worker is for, and it means a signed task is as powerful as the
node it reaches.

Run workers on dedicated machines, preferably ephemeral ones, with least-privilege credentials:
Git credentials that read coordination and source refs and that dispat uses to create worker-owned pickup refs,
with host rules denying canonical and release writes. Give workers no release or registry credentials unless a
worker script itself needs another credential. Do not serve tasks on a shared machine or an untrusted runner.

**4. A worker's build output becomes a published artefact.** Outputs are verified for integrity and origin, through
the digest, the signature and the identity in the manifest, and not for honesty. A compromised worker can return a
poisoned artefact that verifies perfectly.

Keep builds that require that trust on the trusted machine: `runOnly: orchestrator`, or the
`[build, publish]` pair form, for any package whose artefact is signed or shipped to users. Where builds are
reproducible, compare the output of two nodes. Sign artefacts on the orchestrator rather than on the node that built
them.

**5. Publication credentials.** The publish stage runs on the orchestrator, with or without a space login. Registry
and release-write credentials need not exist on workers. Give workers only the Git mailbox access and execution
secret required for authenticated claims, plus any separate credentials their build or validation scripts actually need.
Custom stage scripts and hooks are arbitrary commands; keeping publication credentials off workers also requires
keeping publication actions out of worker-placed scripts.

**6. Logs.** A worker's log holds the output of the scripts it ran, which is whatever those scripts print. Treat
worker logs exactly like CI logs: same retention, same access control, same care about what a build script echoes.

**7. Leftovers.** The coordination branches of a crashed run hold the same source, command text and outputs as live
ones. Delete them once the run is over. A process that stops before cleanup can leave its lock; clear it through the
documented recovery order. A local publish script returning an error does not by itself retain the lock.

Before starting the pool, confirm:

- Everything that travels may be read by everyone who can read the repository being released, and the orchestrator's
  checkout holds no untracked file that is not ignored.
- Release branches, release tags and the lock tag are protected from worker credentials by the host's ref rules.
- The signing secret comes from a secret store, is shared only inside one trust zone, and is rotated when a worker
  leaves or is suspected.
- Workers run on dedicated, preferably ephemeral machines, with no release or registry credentials beyond what
  their own builds need; host rules deny their Git credential canonical and release ref writes.
- No secret is written literally in `env` or in a command, and no script exports one.
- Packages whose artefacts are signed or shipped to users are pinned with `runOnly: orchestrator`.
- Publishing and release Git writes stay on the orchestrator; worker credentials cannot write release refs.
- Worker logs are treated as CI logs.
- Coordination branches of failed runs are deleted, and a lock left by a stopped process is cleared only by the
  documented order.

[Worker nodes on Kubernetes](./examples/kubernetes-workers.md#secrets-and-isolation) adds the cluster-specific half
of this: namespaces, service account tokens, network policy and which Secrets a pool mounts.

## The coordination branches

A distributed compilation publishes one signed graph root and one inert `dispat-task-*` ref per executable logical
stage before any package frame starts. The root pins the run, ownership generation, plan and graph digests, algorithm,
compiled task templates, authorized worker identities, finite admission deadline, exact input objects and transfer
limits. A retry stays on the same logical task ref. Local preparation, signing, publication and release records remain
first-node operations. The signed `dispat-ledger-*` ref records membership, claims, results, decisions and capacity
changes; `dispat-slot-*` refs notify workers about the graph and ready work. These refs are retained after a sealed
run for exact receipt and finality recovery.

An authorized worker with `lateJoin: true` can start after the first task begins. Its admission window begins only
after successful global hooks and the signed execution-start event. It reads the graph notice from its
preauthorized slot, verifies the root, and sends a signed join proposal to the first node's pinned HTTPS return
endpoint. The first node verifies the identity, canonical coordination endpoint, platform, capability and capacity,
then signs admission in the ledger and initializes bounded node slots. A configured worker without `lateJoin: true`
must answer preflight before any task effect. A late join requires the entry's pinned Ed25519 public key and the
worker's matching private seed; the shared message secret or node name alone cannot add capacity. An eager
keyless worker cannot use the join route to replace its admitted capacity. A worker that has not been admitted
cannot claim work.

For this RC.9 cut, an admitted identity's endpoint, capacity and authority ID are fixed for the run. A change to
one of those values is signed-refused; there is no mutable-capacity administration command. New authorized late
workers add usable capacity. A worker shutdown can sign a drain, but an occupied lease remains charged until its
exact task finality is settled.

Each admitted node discovers compatible ready tasks from the same graph. The first node accepts a claim against
the exact task tip and ledger epoch, then records verified result and terminal decisions with its capacity changes.
A peer sends claims and results through the authenticated return channel and pushes only its own immutable pickup
branches; only the first node advances canonical coordination and release refs. Unknown push outcomes are resolved
by exact ref readback before any retry.
If the admission deadline passes while a worker-only task is still inert and no compatible peer has joined, the
first node records a signed no-claim terminal event. It does not invent a command result or expire an accepted claim.
Unrelated ready tasks continue using their available capacity.

Each message binds the protocol version, run, generation, plan and graph, task identity, exact source and predecessor
objects, permissions and deadline. It carries neither resolved secrets nor release write authority. Workers reject
messages with a bad signature, mismatched graph root, wrong node or run identity, stale ledger epoch or object closure,
or unsupported protocol. The worker's own state journal prevents replay after restart.

The bounds come from `execution.transfer`: `maxFiles` (default 20000), `maxBytes` (default 2 GiB),
`maxManifestBytes` (default 8 MiB), `maxPackBytes` (default 4 GiB) and `timeout` (default 1800 seconds).
`maxPackBytes` bounds returned Git packs separately from captured output bytes. `maxManifestBytes` bounds every
coordination document. `transfer.timeout` bounds input fetch and output transfer; `timeouts.task` bounds accepted
command execution, and `timeouts.admission` bounds entry of preauthorized late workers.

## Diagnostics

Each distributed failure has a numbered code and an outcome category. The category distinguishes configuration,
authority, data integrity and cleanup problems.

| Code   | Category                    | Means                                                                            |
|--------|-----------------------------|----------------------------------------------------------------------------------|
| `E225` | `execution-configuration`   | a configuration no distributed run could be executed under: an unknown role, a capacity that is not one, a malformed or credential-carrying endpoint, a duplicated node name, a missing signing secret, a lock bypass beside workers, `commit.verify: false` beside workers on a release, a link with no endpoint whose release remote's push URL carries credentials or is not a Git remote a mailbox can use, a worker with no endpoint started outside a checkout whose release remote a mailbox can be, an unsatisfiable `buildPlatforms`, a worker publish placement (including scalar `runOnly: worker`), a worker build with no worker links, a node that failed preflight, overlapping `buildOutputs`, or a `runOutputs` root that is or holds a package folder or overlaps a build output root |
| `E226` | `execution-authority`       | work refused because of who asked: a release or a distributed sweep initiated on a worker or under a task's authority, a `--worker` link stated there, a claim that is not authentically this run's, or a write the task's authority does not extend to |
| `E227` | `io-integrity`              | input or output data that is missing, changed, incomplete, incompatible or escaping its declared roots, or two tasks of one sweep writing one path with different bytes; it fails one prerequisite and blocks that prerequisite's consumers |
| `E229` | `transport-cleanup`         | transport state the run could not leave in a safe place: an attempt that had to be fenced, or owned refs whose survival leaves an effect unresolved |
| `W244` | `transport-cleanup`         | the harmless half of the same subject: refs a completed run could not safely delete, and writes a build made outside what it declared. A run that is otherwise clean still exits `0`; inspect a retained ref before deleting it |

Existing record and lock diagnostics keep their codes and use the `native-recording-or-lock` category:
`E220`, `E221` and `E222` for a tag or a record, and `E335` and `E336` for a lock.

[Diagnostic codes](./reference/plan-errors.md#distributed-execution-diagnostics) gives the recovery for each one.

## What to watch for

- **A mailbox elsewhere pays for the repository's history.** A prepared input state descends from the planned head, so
  the repository being released, which already holds that history, receives only what a task changed. An `endpoint`
  that names another repository shares no objects with the source. Its first input-bearing task can send
  everything reachable from the planned head: for a very large repository, gigabytes and minutes. Signed task and
  ledger refs remain after a sealed run for receipt recovery until the documented retention cleanup.
- **A heterogeneous pool can wait for a slow claimant.** Once a compatible node claims work, its capacity stays
  occupied until signed finality. Prefer a homogeneous pool, or separate machine classes with `runOnly` and
  `buildPlatforms`.
- **A preempted or evicted worker retains its physical charge.** A run may retry after signed Cancel and its bounded
  reply grace only when `execution.retryAfterUncertainAttempt: true` asserts that its delegated commands are safe to
  repeat after an uncertain external effect, and an allowed attempt remains. The default is `false`. The old pickup
  remains charged until its exact signed finality; a late old outcome cannot replace the accepted winner. No second
  pickup begins merely because another worker is idle.
- **Interpretation counts total pickups.** `execution.maxAttempts` defaults to `1` and allows at most `64` accepted
  pickups of one delegated logical task, including the first. A larger value permits sequential retry after a
  settled failed Result and required hooks; it does not assert that an uncertain external effect is safe to repeat.
  A first-node publication or native record is never repeated by this setting.
- **`dispat status` is how a pool is sized.** More workers help only while more builds are ready at once than there
  are slots. No run is shorter than its longest chain, and one repository's publications happen one after another.
- **A worker's task deadline is enforced by the node itself.** The orchestrator and worker use the same task bound.
  Before reporting completion or acknowledging cancellation, the worker stops and checks the process group it
  created for the command. If it cannot prove that group is gone, it retains the physical lease. This check cannot
  prove that a child which deliberately escaped the group or an external upload has stopped; task scripts must
  settle those effects before exiting.
- **Two workers must not share one state folder for one node name.** The second one to start refuses. Give each node
  its own `--state-dir`, and expect the folder to be disposable: everything in it is rebuilt. The owning worker's
  process id is in `worker.lock`; a worker that finds the id of a process that is gone, or its own id left by an
  earlier process, as a container's process 1 always is, takes the folder over, so a crashed worker restarts without
  anybody deleting a file.
- **Nested dispat commands on a worker are restricted.** Under a task's authority `release`, a bare `dispat`,
  `commit`, `github`, `changelog`, `autoversion`, `compute` and `worker` are refused with `E226`. Everything a build
  script legitimately uses stays allowed, including `exec`, `if`, `for`, `install`, `scanner`, `writer`, `replacer`,
  `autowriter` and `trigger`.
- **Stage events come from the orchestrator.** A delegated stage's events are raised on the orchestrator, which names
  the node that ran it in `worker`. Only a `dispat trigger` inside a delegated script is sent from the worker, so the
  endpoints such a script reaches, and the variables their headers and `secretEnv` read, have to exist on every node
  that script may run on.
- **The summary is the answer to "what happened where".** One line per task in plan order with its placement, and
  the four outcomes kept apart: computation, outputs, publication and recording. A completed task is not a released
  package.

## See also

- [The `execution` object](./configuration/execution.md) for every key, default and refusal.
- [The worker command](./cli/worker.md) for the flags, the state folder and the exit behaviour.
- [The run command](./cli/run.md#running-on-worker-nodes) for a sweep executed on the pool.
- [Worker nodes on Kubernetes](./examples/kubernetes-workers.md) for a pool that exists for the length of one
  release.
- [dispat in CI](./reference/ci.md#worker-nodes-in-a-pipeline) for a pipeline that starts workers beside the release
  job.
- [The release lock](./reference/releasing/release-lock.md) for the lock a distributed run may retain.
- [Spaces](./configuration/spaces.md) and [packages](./configuration/packages.md) for `buildOutputs`,
  `buildPlatforms`, `runOnly` and the provider relation.
- [Diagnostic codes](./reference/plan-errors.md#distributed-execution-diagnostics) for the recovery of each code
  above.
- [Architecture](./internals/architecture.md) for the stage seam, the coordinator and what is deliberately out of
  scope.
