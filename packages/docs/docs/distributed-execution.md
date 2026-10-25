# Distributed execution

Use workers to run parts of a release on other machines. The machine where you start the release is the
**orchestrator**: it keeps the locks, computes the plan and writes the release records. **Workers** run assigned
commands and return their results. Assignments, responses and build outputs travel over temporary Git branches,
usually in the repository being released.

To try it, [configure the nodes](#configuring-the-nodes), [start a worker](#starting-a-worker), then choose
[where stages run](#where-a-stage-runs) and [which build outputs travel](#build-outputs-as-inputs). Read the
[security section](#security-what-distributed-execution-exposes-and-how-to-contain-it) before sharing a pool or using
a public repository as its mailbox. For a stalled or uncertain run, go to [recovery](#locks-failure-and-recovery).

With no `execution` object or an empty `workers` list, releases still run on one machine. The same worker pool can
also run [`dispat run` scripts](#running-scripts-on-workers) without releasing anything.

## When to choose it

Workers are useful when several long builds can run independently, or when a release needs platforms the current
machine cannot build for. Look at the dependency graph: more workers help only while several tasks are ready at once.

For short builds, start with one machine. Each delegated task transfers a source snapshot, prepares a checkout and
sends outputs back. That setup can take longer than the build itself.

|                                          | One machine                                     | Orchestrator and workers                                  |
|------------------------------------------|-------------------------------------------------|-----------------------------------------------------------|
| Where build commands run                 | the machine the release was started on          | a worker node, or this machine when no node has room      |
| Where publish commands run               | the machine the release was started on          | here, unless `runOnly` delegates the publish stage        |
| Where the locks, the plan and the records live | one machine                               | the orchestrator alone                                    |
| What a build product travels as          | a folder in the checkout                        | a verified manifest and its bytes, on a temporary branch  |
| What has to exist beforehand             | the checkout                                    | a shared signing secret and the nodes                     |
| What a lost machine costs                | the run                                         | the packages that node was working on                     |
| Extra failure to read                    | none                                            | a publication whose outcome the run cannot establish      |

There is no published performance comparison yet. Measure your own graph, machines and transfer time before assuming
that a larger pool will shorten the release.

## Orchestrator and worker roles

Both roles use the same binary, with different authority:

**The orchestrator owns the release.** It locks the repositories, captures the planning input and computes one plan
with a digest. It probes workers, assigns tasks, verifies returned outputs, authorizes publication, writes release
records and runs the run-level hooks. It can also execute tasks locally when they are not delegated.

**A worker executes assignments.** It polls its mailbox, claims work when a slot is free, checks out the exact assigned
source state, runs the commands and reports the result. A stage and its surrounding hooks form a **frame**, which
always runs together on one node.

A worker cannot start a release: `execution.role: worker` refuses it with `E226`. Processes running under a task's
authority are also refused, regardless of their configured role. A build script therefore cannot start a second
release of the repository it is building.

A node whose configuration says `role: orchestrator` may also run `dispat worker`. While it serves a task it has that
task's authority and never consults its own worker list.

## Configuring the nodes

Configure `execution` on the orchestrator and on each worker. By default, they exchange messages through the release
repository, so the orchestrator's worker list needs only the node names:

```yaml title="dispat.yaml on the orchestrator"
execution:
  role: orchestrator
  concurrency: 2
  secretEnv: DISPAT_EXECUTION_SECRET
  workers:
    - name: build-a
    - name: build-b
  timeouts:
    preflight: 60
    task: 3600
    cancel: 60
```

```yaml title="dispat.worker.yaml on a worker node"
execution:
  role: worker
  name: build-a
  endpoint: git@github.com:acme/project.git
  secretEnv: DISPAT_EXECUTION_SECRET
  concurrency: 2
```

The [`execution` reference](./configuration/execution.md) lists every key and default. For this setup, check three
details:

- **Only the startup root selects execution policy.** Space, package, package-folder and imported settings do not
  supply it. A linked peer may have its own `execution`, but the combined run ignores it and logs that at debug level.
  A transferred checkout cannot choose the receiving machine's role.
- **A node's name is its identity in the transport.** The name is written into the branches addressed to it, and the
  worker's `execution.name` has to be the `name` of the orchestrator's link to it.
- **Endpoints contain no credentials.** Supported forms are `https`, `ssh` and `file` URLs, absolute paths and
  scp-like `host:path`. Unauthenticated `http` and `git`, and URLs carrying user information, are refused. Use Git's
  credential helper or `http.extraheader` on each machine instead. The same rule applies when an omitted endpoint
  resolves to the release remote's push URL: a token in that URL fails with `E225` before locking (CCME §28.2).

### The mailbox repository

The mailbox is a Git repository used to exchange task messages. Without an explicit `endpoint`, dispat uses the push
URL of `commit.remote`, falling back to `origin`. In a composed workspace, this is the entry repository's remote.
Workers either name that repository as their endpoint or start in its checkout and use the same remote.

Coordination branches live under `refs/heads/dispat-worker-*`, beside the release branches. A run deletes its own
branches when it ends. Using the source repository avoids retransferring objects the remote already has; it receives
only the task's changes.

Using the repository itself has consequences worth knowing before the first run:

- **Whatever travels is readable by whoever can read the repository.** Source snapshots, build outputs, the values
  scripts export and the command text of every delegated stage are pushed to it, so on a public repository they are
  public. A snapshot is the working tree as the orchestrator had it, which includes files that are untracked and not
  ignored and commits that were never pushed, and a host keeps an object fetchable by its id for a while after the
  branch that carried it is deleted.
- **The host's ref rules keep workers away from releases.** The credentials a worker writes `dispat-worker-*` branches
  with can write any other ref their permissions reach. Protect the release branches, the release tags and the
  `dispat-release-lock` tag with the host's branch and tag rules, so that worker credentials create and delete
  coordination branches and nothing else, which is what CCME §28.4 requires of transport credentials.
- **The host's size limits apply.** GitHub warns about a file over 50 MB, rejects a file over 100 MB and rejects a push
  over 2 GB, and one task's output set travels in one push. `transfer.maxBytes` bounds what dispat sends; the host's
  limits bound what it accepts.
- **A composed workspace uses the entry repository.** The histories of the peers a task reads are pushed to the entry
  repository's remote, because that is where the node reads them from.

Set `endpoint` to use a different mailbox. Several workers can share it, but each must name the same mailbox as its
orchestrator link. If workers use different mailboxes, the orchestrator relays results between them.

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

Workers need outbound Git access, with no inbound connection. They poll every five seconds while idle and almost
immediately after answering work, so they can run behind NAT or on hosted CI runners.

With an explicit `endpoint`, a worker needs no existing source checkout or Git repository in its startup folder. The
assignment supplies commands, environment names and exact source states, and the worker creates its own checkout.
Without an endpoint, start it in the release repository's checkout. Otherwise, or if that checkout's push URL cannot
serve as a mailbox, startup fails with `E225`.

`--idle-timeout` exits with code `0` after the configured seconds with no claimed or in-flight work, measured from
the last activity. Use it for workers started for one release. `SIGINT` and `SIGTERM` wait for claimed work to finish,
then also exit with code `0`.

## Where a stage runs

Use `runOnly` to choose where a package's build and publish stages can run. Supply one value for both or a
`[build, publish]` pair:

```yaml
runOnly: orchestrator             # build and publish stay on the orchestrator

packages:
  ui:
    runOnly: [both, orchestrator] # build anywhere there is room, publish here
  signer:
    runOnly: orchestrator         # this build signs its artefact
  linux-image:
    runOnly: [worker, both]       # this build must not run on the orchestrator
```

| Value          | What it means                                                                                     |
|----------------|---------------------------------------------------------------------------------------------------|
| `both`         | the default: a build goes to a worker when one has room and to the orchestrator when none has; a publish stays on the orchestrator |
| `worker`       | the stage may only be delegated; a run with no worker links is refused with `E225` before any stage runs |
| `orchestrator` | the stage may only run on the machine the release was started on                                   |

The setting follows the same override order as `buildOutputs` and `buildPlatforms`: root, space, space-folder file,
package, then package-folder file. An override replaces the whole pair; a single value sets both stages.

Placement follows these rules:

- **A frame runs as one unit on one node.** There is one placement question per frame, not one per command.
- **The orchestrator is a node of last resort under `both`.** It joins its own pool with capacity
  `execution.concurrency` and is chosen only when no worker has room. A build it keeps still captures and admits its
  declared outputs through the same path a worker's result takes.
- **A publish is delegated only by an explicit `worker`.** `both` keeps publication here.
- **A login pins publication here.** A space with `flow.login` publishes on the orchestrator whatever the key says,
  and `publish: worker` beside a login is refused when the configuration loads.
- **Pinning costs parallelism.** A frame pinned to the orchestrator waits for one of this machine's own slots, and a
  frame pinned to a worker waits for a worker even when this machine is free. A package pinned to the orchestrator is
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
  provider outputs its build was given, exactly as for a delegated publication. A frame placed on the orchestrator
  installs nothing: the outputs are already in its checkout. A package that declares no `buildOutputs` hands a node
  its sources alone, so declare what the test scripts read.
- **It produces no output set.** The admitted set stays the one the build reported. What its scripts export travels
  back in the signed result and reaches the publish, as a build's exports do.

The test runs as a separate `<package>:test` task and shares the build budget. The assignment kind is `stage`; its `stage`
field supplies `DISPAT_STAGE` and the hook names. Build and publish assignments keep their existing `build` and
`publish` kinds.

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
3. **Every worker is probed.** One `probe` assignment per configured node, bounded by `timeouts.preflight`. The
   answer states the node's protocol version, dispat build, operating system, architecture, capacity, git version and
   transfer ceilings. A node that does not answer, speaks another protocol version, reports no capacity, or would
   refuse what this run transfers fails the release with `E225`, before a single task is dispatched. So does a
   package whose `buildPlatforms` no node satisfies.
4. **Each dispatch carries a prepared input state.** Before a task is assigned, the orchestrator captures the
   current working state of every repository in that package's input closure as a commit of its own, and the
   assignment names those exact object ids. A node builds from the state the orchestrator captured, never from a
   branch tip that may have moved.
5. **Tasks are placed as nodes become free.** A task asks the pool for a free slot on a node that satisfies its
   platforms and its `runOnly` value, and holds that slot until the attempt is terminal. Under the default `both` a
   build is offered to the least loaded worker and to the orchestrator only when no worker has room.

What stays on the orchestrator:

| Work                                                          | Why it does not travel                                      |
|---------------------------------------------------------------|-------------------------------------------------------------|
| the sign and version stages and the manifest and lock-file preparation | they write the state every build of the run then consumes |
| the space login                                                | authentication must not travel                              |
| release commits, tags, changelogs, GitHub releases, records     | a worker is never granted the right to write a release ref  |
| `postPublish`, every stage after the records (`announce` by default), `onFail`, `onSkip` and every run-level hook | they observe the run, which exists here          |

What a worker runs: the build frame (`beforeBuild`, the build commands, `postBuild`), the test frame (`beforeTest`,
the test commands, `postTest`) where the package has one, the frame of every [stage of your
own](#stages-of-your-own) before the records, and, where `runOnly` delegates it, the publish frame
(`beforePublish`, the publish commands and the stage's after hook). A frame always runs as one unit on one node,
because a hook that observed a folder another machine wrote would be observing nothing.

**Run budgets and node capacity are separate.** Root `concurrency` limits builds and publishes across the whole run;
adding workers does not multiply it. A task holds its stage slot while waiting for a node. `execution.concurrency`
limits the assigned tasks one node accepts across all runs using it.

Work waits in the mailbox when the node has no slot. An assignment unclaimed by its deadline is withdrawn and placed
again, up to three times, then fails its package.

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
again against its assignment's digest before installing any file.

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

## Publishing from workers

Publishing stays on the orchestrator unless `runOnly` explicitly assigns the publish stage to `worker`. A space with
`flow.login` always publishes on the orchestrator. Since publications are serialized per repository, delegation does
not add parallel publication within that repository, and the worker would need registry credentials.

Where a publication is delegated, four things hold:

- **Credentials live in the worker's environment.** The `env` pairs of the configuration travel unresolved: a value
  written as `$NPM_TOKEN` travels as that reference and is expanded on the node that runs the command, from that
  node's own environment. The token a delegated publish needs must therefore exist on that worker.
- **Publication needs separate authorization.** The worker installs verified build outputs, runs `beforePublish`,
  reports ready and waits. The orchestrator checks the release lock, composed-workspace snapshot and other state
  relevant to the package, then writes a single-use authorization for that exact branch state with an expiry time.
  The worker reads the branch again immediately before the command, so a later withdrawal still stops publication.
- **Publication and recording are serialized per repository.** With workers configured, one repository's packages
  publish one after another and are recorded in the same order.
- **Hook exports return with the ready message.** `beforePublish` values merge into the release at the same point as
  in a local run, whether or not the subsequent publish succeeds.

A delegated publish cannot attach files through `DISPAT_EXPORT_GITHUB`: its paths refer to the worker checkout, which
is deleted when the task ends. Export attachments from an orchestrator stage. Tracked files changed by a worker's
publish script are reported as `W244` stray writes and are not transferred.

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

Placement uses the package's build `runOnly` setting. `both` prefers the least-loaded available worker and uses the
orchestrator only when no worker has room. `orchestrator` stays local. `worker` requires a worker link; otherwise the
sweep fails with `E225` before running scripts.

Without worker links, the sweep runs locally as usual: no fixed plan, release lock, distributed summary or mailbox.

A distributed sweep also takes no release lock. It records no releases and authorizes no locked publication.
Messages use a generation derived from the sweep's run identity rather than lock objects. A release of the same
repositories may run alongside it; neither waits for nor excludes the other.

A worker or a process under task authority still cannot start a distributed sweep (`E226`). A lock bypass with worker
links is refused (`E225`) because it declares that no coordination remote is available. Before work starts, the sweep
fixes and logs its plan as `plan fixed` and probes every link. At the end, each `task outcome` line reports node,
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
of `name=endpoint` empty, is a usage error. The link is refused with `E226` under a task's authority and on a node
whose file says `role: worker`, because a worker dispatches nothing. It is no part of the plan digest, and
`dispat status --worker` prints the digest a distributed run would carry, resolves no remote and reaches no node.

## Locks, failure and recovery

With workers configured, `unsafeDisableLock`, per-repository bypasses and `DISPAT_UNSAFE_DISABLE_LOCK` are all refused
with `E225`. The remote lock prevents two orchestrators from authorizing the same release.

For a failed build, the next run can build again. For a publish with an unknown result (`E228`), first establish what
the worker and registry did, then follow the cleanup order below. Do not remove a retained lock to start a retry.

### Lock checks during the run

The orchestrator reads every release lock from its remote before the first worker probe, every assignment and every
publication, including local publishes. It never reuses a previous successful check.

If the lock is gone, the run stops with `E336`, writes no new assignments and withdraws attempts in flight. A publish
authorized before the loss is still recorded: a create-only record of an existing effect is not a new publication.

A failed read does not prove the lock was lost. Each read is limited to fifteen seconds. Failures are retried after
one second and then two seconds, with a warning each time. After three failed reads, the run stops as it would for a
lost lock, but reports `unverified` instead of `lost` and returns the lock it still owns. An interruption during a
read is not treated as lock loss.

### A worker stops answering

An unclaimed compute task can be withdrawn by revoking its branch and placed again. Once an attempt is claimed,
passing its deadline removes that node from the pool. Its slot is not reused and downstream packages are not
attempted. A claimed task is never retried on another node within the same run; the next run builds it again.

A slot becomes available again only after a result, an acknowledged withdrawal, or proof that an unclaimed
assignment can no longer be claimed. Deleting its branch or writing cancellation under an exact lease supplies that
proof without a worker acknowledgement. Silence alone does not: the task may still be running.

### A publication has no known result

If an authorized publish returns no result, the run withdraws it and waits `timeouts.cancel` for acknowledgement.
What happens next depends on the evidence:

| Evidence | Outcome |
|---|---|
| A result was already written where the withdrawal would go | Use that result: record success, or report an ordinary publish failure. This also applies to publication authorized before a lost lock. |
| The worker acknowledges that the command never started | Ordinary publish failure. |
| The command started but has no result, or the worker never acknowledges | `E228`: fail the publish stage, block dependents, make no second attempt and exit non-zero. |

The same rule applies when authorization was refused. If the worker says the command nevertheless started and supplies
no result, report `E228`, not a withheld publication.

If an authorization push returns no success receipt, dispat reads the branch; it never repeats the push to find out.
Finding the authorization at the tip or below the worker's result proves it arrived, and the result is handled
normally.

Only two failures prove that no worker received the authorization: local preparation failed before any push, or the
remote rejected the push through a lease or server rule and the branch does not contain it. In either case, dispat
withdraws the attempt, waits for acknowledgement, fails authorization and returns the lock.

Every other result is uncertain. A missing or reset branch does not prove the worker never received permission. The
run withdraws the attempt, and unless the worker confirms that its command never started, reports `E228` and retains
the repository's lock and remaining coordination evidence.

### Recovering an uncertain publication

If the publisher never acknowledges, its repository's release lock is **retained**. The authorization ref is also
retained when the worker acknowledges after starting publication or never answers. Other owned coordination refs
are cleaned up only when their current tips still belong to the run.

Follow the recovery order printed in the error:

1. read the run id from the `run` line of the retained lock tag, and list the coordination refs of the repository
   (`git ls-remote --heads origin 'dispat-worker-*'`); the run's own carry that id in the `run` field of their
   messages;
2. find the authorization that has no result beside it;
3. confirm on that node that the publisher has stopped;
4. check the registry for the version;
5. delete the run's refs;
6. and only then delete the lock tag on the remote.

Before deleting a ref, verify that its current tip still belongs to this run's authenticated chain. A changed or
unauthenticated tip needs investigation, not deletion under this run's cleanup procedure.

[The release lock](./reference/releasing/release-lock.md#a-lock-a-distributed-run-retained) has the commands. A run
may end with a lock retained and no release record at all, so the registry is the evidence, not the tags.

dispat does not reconcile the registry for you. Once the outcome is known and cleanup is complete, use an ordinary
run to plan the remaining work. See [Recovering from a failed run](./reference/releasing/recovery.md) for the same
process after a local publish interruption.

### Cleanup and leftover branches

Revoking an expired attempt or withdrawing one at shutdown waits at most `timeouts.cancel`. Closing coordination
branches uses that timeout clamped between 30 seconds and two minutes, and happens before release locks are returned.
An unresponsive mailbox can leave branches behind with `W244`; it does not prevent the run from returning locks that
are otherwise eligible for release. Local refs fetched by the run are removed regardless of remote cleanup success.

An internal coordination-branch failure is contained to that branch, node preflight or task. If publication was
already authorized, it is handled as a missing response, not assumed to have failed.

A completed run reports leftover coordination refs with `W244` and can still exit `0`; those refs are not release
records. An `E228` authorization is different: it is evidence, and must follow the recovery order above.

For other branches from a crashed run, confirm that no process uses them and that their tips belong to the run's
authenticated chain before deleting them. Investigate unexpected or unauthenticated tips instead of treating them as
routine leftovers.

## Security: what distributed execution exposes and how to contain it

Workers add machines and repositories that can affect a release. Source and commands pass through the mailbox,
the shared secret authorizes work, and files built elsewhere may become the published artifact. Check these seven
parts of the setup before assigning work.

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
worker, or anyone holding the secret with push access to the repository the messages travel through, can forge an
assignment, which is command execution on every other node sharing that secret, and can forge a result, which is a
build output the orchestrator would admit.

All machines holding one secret belong to one trust zone. Use separate secrets for different trust levels, store them
in the CI secret store or equivalent, and rotate them when a worker is removed or suspected of compromise. Never
commit the secret. Per-link secrets are not implemented, so every holder must be trusted to the same level.

**3. Workers run what they are told.** A worker executes the commands of any authentic assignment, with its own
environment and its own credentials. That is what a worker is for, and it means an assignment is as powerful as the
node it reaches.

Run workers on dedicated machines, preferably ephemeral ones, with least-privilege credentials:
Git credentials that write `dispat-worker-*` branches and read the sources, and no release or registry credentials
unless that worker must publish. Do not serve tasks on a shared machine or an untrusted runner.

**4. A worker's build output becomes a published artefact.** Outputs are verified for integrity and origin, through
the digest, the signature and the identity in the manifest, and not for honesty. A compromised worker can return a
poisoned artefact that verifies perfectly.

Keep builds that require that trust on the trusted machine: `runOnly: orchestrator`, or the
`[build, publish]` pair form, for any package whose artefact is signed or shipped to users. Where builds are
reproducible, compare the output of two nodes. Sign artefacts on the orchestrator rather than on the node that built
them.

**5. Publication credentials.** A space with a login script always publishes on the orchestrator, so its credentials
never need to exist on a worker. A publish without a login script may be delegated by `runOnly`, and the worker must
then hold whatever that publish reads from its environment.

Keep publishing on the orchestrator with a login script, `runOnly: both` or `[both, orchestrator]`, unless you have a
specific need for worker publication.

**6. Logs.** A worker's log holds the output of the scripts it ran, which is whatever those scripts print. Treat
worker logs exactly like CI logs: same retention, same access control, same care about what a build script echoes.

**7. Leftovers.** The coordination branches of a crashed run hold the same source, command text and outputs as live
ones. Delete them once the run is over. A retained lock is deliberate and is cleared only through the recovery order
above.

Before starting the pool, confirm:

- Everything that travels may be read by everyone who can read the repository being released, and the orchestrator's
  checkout holds no untracked file that is not ignored.
- Release branches, release tags and the lock tag are protected from worker credentials by the host's ref rules.
- The signing secret comes from a secret store, is shared only inside one trust zone, and is rotated when a worker
  leaves or is suspected.
- Workers run on dedicated, preferably ephemeral machines, with no credentials beyond the coordination branches, the
  sources and what their own builds need.
- No secret is written literally in `env` or in a command, and no script exports one.
- Packages whose artefacts are signed or shipped to users are pinned with `runOnly: orchestrator`.
- Publishing stays on the orchestrator except where a delegated publish is deliberate, and that worker holds only the
  credentials it needs.
- Worker logs are treated as CI logs.
- Coordination branches of failed runs are deleted, and a retained lock is cleared only by the documented order.

[Worker nodes on Kubernetes](./examples/kubernetes-workers.md#secrets-and-isolation) adds the cluster-specific half
of this: namespaces, service account tokens, network policy and which Secrets a pool mounts.

## The coordination branches

One branch carries one attempt of one task, as a first-parent chain of commits whose trees hold the message
`dispat/<kind>.json` and the detached signature `dispat/<kind>.sig` beside it. A build's result also carries its
captured outputs in the same commit.

Branches are named `dispat-worker-<node>-<YYYYMMDD>-<kind>-<32 hex characters>`, with the kind one of `probe`,
`build`, `publish`, `prepare`, `run` for a sweep task, `snapshot` or `relay`. The name is a routing hint and never an
authority: it lets a node list only `refs/heads/dispat-worker-<its name>-*`, and nothing ever parses the date or the
kind back out of it. The signed message inside decides what a node may act on. A worker named `build` also matches the
branches of `build-a`, which is harmless for the same reason, but a glob written by hand for a single node should
anchor on the date segment.

Every transition is one compare-and-swap push by one party:

| State      | Written by       | Means                                                                         |
|------------|------------------|-------------------------------------------------------------------------------|
| assignment | the orchestrator | the work, created only if the branch does not exist                           |
| claim      | the node         | the node has a free slot and has taken the work                               |
| ready      | the node         | a publication has done everything before the irreversible command             |
| go         | the orchestrator | the single-use publication authorization, naming the exact ready it answers   |
| result     | the node         | the terminal report of the attempt, with the captured outputs of a build      |
| cancel     | the orchestrator | the attempt is withdrawn                                                      |
| ack        | the node         | the withdrawn attempt has stopped, with the phase it was in                   |
| closed     | the orchestrator | the ref is deleted at the end of the run                                      |

**A push is never repeated to learn its outcome.** Git can report a ref as applied, rejected because its lease no
longer holds (`[rejected]`), or rejected by a server rule or hook (`[remote rejected]`). A `[remote failure]`, lost
connection or missing receipt leaves the result unknown.

The sender then reads the branch. Its message at the tip or in the first-parent chain proves the push landed. An
explicitly refused message absent from the branch did not land. Anything else remains unknown after three reads over
three seconds. Branches are registered for cleanup before the push, so an uncertain result does not escape cleanup.

Recovery depends on the message:

- An assignment with an unknown push result is revoked under an exact lease and offered again once its branch is
  gone. If the worker already claimed it, the attempt continues.
- A worker with an unknown claim result runs the task if its claim appears on the branch, or acknowledges a
  withdrawal written above it.
- A result rejected by a server rule is reported once more without the refused content. Build, preparation and sweep
  tasks report `transfer-refused` with no outputs. A publish retains its status but drops exports. The worker logs
  the server reason with redaction, letting the orchestrator receive an answer before `timeouts.task` expires.

**Transfers do not block mailbox control traffic.** While a worker pushes outputs, it can still poll, acknowledge
withdrawals and read publication authorizations. The orchestrator can push one input state to several nodes at once;
dispatches needing the same state on one node share a push.

In a shared mailbox, the orchestrator fetches only its own attempts' branches, not other runs' output trees. It
removes those local refs when their branches close and when the run ends.

**What an assignment carries:** the protocol version, the kind, the run id, the plan digest, the task and attempt,
the ownership generation, the node it is addressed to, the branch it may appear on and the instant it was issued;
the repositories of the input closure with the exact object id of each prepared state; the package, its version and
its folder; the frame's before hook, commands and after hook; the computed `DISPAT_*` pairs; the configuration's own
`env` pairs unresolved; the exports of the package's earlier stages; the shell; the platforms; the inputs it consumes
by object id and manifest digest; the declared output roots; what it permits; its deadline in seconds; and the
transfer ceilings.

**What it never carries:** a resolved secret, a login command, a login export, any login state, the right to write a
release ref, and any policy a node would have to rediscover. A node accepts a message only when the signature
verifies, the node name is its own, the branch is the ref it was found on, the protocol version matches exactly, the
issue time is within 24 hours, and the attempt is not in its own record of already-answered work.
The node retains an accepted attempt for 48 hours: an issue time 24 hours in the future is valid when first accepted
and can remain valid for another 24 hours. The record is pruned after that full acceptance horizon, including while a
node keeps serving without restarting.

The bounds come from `execution.transfer`: `maxFiles` (default 20000), `maxBytes` (default 2 GiB),
`maxManifestBytes` (default 8 MiB) and `timeout` (default 1800 seconds). `maxManifestBytes` bounds every document of
the protocol and not only an output manifest, so a value below the size of an ordinary assignment makes a profile
unusable. `transfer.timeout` bounds the fetch and install of a task's inputs and the push of a result that carries
outputs; a result that carries no outputs reports under a fixed 30 second bound. It also bounds each poll of a
worker's mailbox, because a poll fetches the input states and output sets addressed to the node: a poll that runs out
of time is reported as a warning, and the next poll asks again.

## Diagnostics

Each distributed failure has a numbered code and an outcome category. The category distinguishes configuration,
authority, data integrity, uncertain publication and cleanup problems.

| Code   | Category                    | Means                                                                            |
|--------|-----------------------------|----------------------------------------------------------------------------------|
| `E225` | `execution-configuration`   | a configuration no distributed run could be executed under: an unknown role, a capacity that is not one, a malformed or credential-carrying endpoint, a duplicated node name, a missing signing secret, a lock bypass beside workers, `commit.verify: false` beside workers on a release, a link with no endpoint whose release remote's push URL carries credentials or is not a Git remote a mailbox can use, a worker with no endpoint started outside a checkout whose release remote a mailbox can be, an unsatisfiable `buildPlatforms`, `runOnly: worker` with no worker links, a node that failed preflight, overlapping `buildOutputs`, or a `runOutputs` root that is or holds a package folder or overlaps a build output root |
| `E226` | `execution-authority`       | work refused because of who asked: a release or a distributed sweep initiated on a worker or under a task's authority, a `--worker` link stated there, an assignment that is not authentically this run's, or a write the task's authority does not extend to |
| `E227` | `io-integrity`              | input or output data that is missing, changed, incomplete, incompatible or escaping its declared roots, or two tasks of one sweep writing one path with different bytes; it fails one prerequisite and blocks that prerequisite's consumers |
| `E228` | `publication-unknown`       | an authorized publication that never reported back; the run is incomplete, makes no second attempt, and retains the lock of an unfenced publisher |
| `E229` | `transport-cleanup`         | transport state the run could not leave in a safe place: an attempt that had to be fenced, or owned refs whose survival leaves an effect unresolved |
| `W244` | `transport-cleanup`         | the harmless half of the same subject: refs a completed run could not safely delete, and writes a build made outside what it declared. A run that is otherwise clean still exits `0`; inspect a retained ref before deleting it |

Existing record and lock diagnostics keep their codes and use the `native-recording-or-lock` category:
`E220`, `E221` and `E222` for a tag or a record, and `E335` and `E336` for a lock.

[Diagnostic codes](./reference/plan-errors.md#distributed-execution-diagnostics) gives the recovery for each one.

## What to watch for

- **A mailbox elsewhere pays for the repository's history.** A prepared input state descends from the planned head, so
  the repository being released, which already holds that history, receives only what a task changed. An `endpoint`
  that names another repository shares no objects with the source, and its first push sends everything reachable
  from the planned head: for a very large repository, gigabytes and minutes. Normal completed-run cleanup deletes the
  run's owned coordination refs, so nothing keeps those objects referenced there and the next run pays it again.
- **A heterogeneous pool waits for its slowest node.** Placement is first free, not fastest: one machine much slower
  than the others will be given work and the run will wait for it. Prefer a homogeneous pool, or separate the classes
  of machine with `runOnly` and `buildPlatforms`.
- **A preempted or evicted worker holds its task until the deadline.** There is no retry on another node inside a
  run, so a spot machine that disappears mid-build costs that package this run, and `timeouts.task` is how long the
  run waits before saying so. Preemptible machines are a poor fit for publishing workers in particular, because a
  publication that was authorized and never reported back retains a lock.
- **`dispat status` is how a pool is sized.** More workers help only while more builds are ready at once than there
  are slots. No run is shorter than its longest chain, and one repository's publications happen one after another.
- **A worker's task deadline is enforced by the node itself.** The orchestrator's wait and the node's own are the
  same number, so a task that overruns is stopped on the machine rather than abandoned while it is still running.
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
