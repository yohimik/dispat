# Distributed execution

A release can run its build and publish commands on other machines while one machine keeps the release locks, the
plan and the records. The machine a release is started on is the orchestrator. The machines that execute the work it
assigns are worker nodes. Everything travels over Git, through the repository being released: an orchestrator pushes
an assignment to a coordination branch on the remote it releases to, the worker answers on the same branch, and
verified build outputs move between dependent tasks the same way.

With no `execution` object, or with an empty `workers` list, a release plans and executes exactly as it does on one
machine. Distributed execution is an addition to a configuration rather than a different release engine. The same
pool also runs [`dispat run` sweeps](#running-scripts-on-workers), which release nothing.

## When to choose it

Choose it when the work of a release is larger than one machine is comfortable with and the packages can be built
independently: a workspace whose builds take tens of minutes, a graph wide enough that several packages are ready at
the same moment, or a release that needs artefacts for platforms this machine cannot build.

Leave it out when the work is small. Every delegated task pushes a prepared source state to a mailbox, materializes a
checkout on the node, and pushes its outputs back. For a build that takes seconds, that transfer and setup is the
larger half of the job.

|                                          | One machine                                     | Orchestrator and workers                                  |
|------------------------------------------|-------------------------------------------------|-----------------------------------------------------------|
| Where build commands run                 | the machine the release was started on          | a worker node, or this machine when no node has room      |
| Where publish commands run               | the machine the release was started on          | here, unless `runOnly` delegates the publish stage        |
| Where the locks, the plan and the records live | one machine                               | the orchestrator alone                                    |
| What a build product travels as          | a folder in the checkout                        | a verified manifest and its bytes, on a temporary branch  |
| What has to exist beforehand             | the checkout                                    | a shared signing secret and the nodes                     |
| What a lost machine costs                | the run                                         | the packages that node was working on                     |
| Extra failure to read                    | none                                            | a publication whose outcome the run cannot establish      |

dispat makes no performance claim for distributed execution. What it distributes is stated below; how much faster a
particular release becomes depends on the graph, the machines and the transfer, and no measured comparison is
published yet.

## Orchestrator and worker roles

The two roles are the same binary in different postures, and the difference is what each is allowed to decide.

**The orchestrator owns the release.** It acquires the release locks, fixes the planning input and computes the plan
once, names that plan with a digest, probes every configured worker, assigns tasks, admits or refuses the outputs
that come back, authorizes every publication, writes the tags, records and release commits, and runs the run-level
hooks. It is also a node of its own pool: a frame it cannot or should not delegate runs here, under the same rules.

**A worker executes what it was assigned and initiates nothing.** It polls the mailbox named in its own
configuration, claims work addressed to it when it has a free slot, materializes the exact source state the
assignment names, runs the frame, and reports the result. A node with `execution.role: worker` refuses to start a
release with `E226`. So does any process running under a task's authority, whatever its own role says, which is what
stops a build script from starting a second release of the repository it is building.

A node whose configuration says `role: orchestrator` may also run `dispat worker`. While it serves a task it has that
task's authority and never consults its own worker list.

## Configuring the nodes

Two objects configure a distributed release: the orchestrator's `execution` object and each worker's own. The
repository being released is the mailbox both reach, so a link needs no more than the node's name.

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

[`execution`](./configuration/execution.md) documents every key, its default and its validation. Three properties of
the object are worth knowing before the keys:

- **It is a node-startup setting.** The key exists in the root file only, and it is read from the configuration a run
  is started with. A space, a package, a package folder's file, an imported configuration and a linked peer never
  state it, and a peer that carries one of its own is legal and ignored, with one debug line saying so. A checkout
  that travels to another machine must not be able to tell that machine what role it plays.
- **A node's name is its identity in the transport.** The name is written into the branches addressed to it, and the
  worker's `execution.name` has to be the `name` of the orchestrator's link to it.
- **An endpoint is credential free.** An `https`, `ssh` or `file` URL, an absolute path, or the scp-like `host:path`
  form. `http` and `git` are refused because they authenticate nobody, and user information is refused because the
  configuration file is committed. Git's own credentials on each machine are what reach the mailbox. A link that
  states no endpoint reaches the push URL of the remote the release takes its lock on, which is held to the same
  rules, because an endpoint never carries a secret (CCME §28.2): a push URL carrying a token is refused with `E225`
  before any lock, so keep that credential in a credential helper or in `http.extraheader`.

### The mailbox repository

The mailbox is the repository being released. A worker link with no `endpoint` reaches the remote the release takes
its lock on, at the push URL Git resolves for it: `commit.remote`, or `origin` when that names none, and in a composed
workspace the entry repository's. Every worker names that same repository as its own `endpoint`. The
coordination branches live on it beside the release branches, under `refs/heads/dispat-worker-*`, and a run deletes
the ones it created when it ends. A remote that already holds the repository's objects receives only what a task
changed.

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

A link that states an `endpoint` reaches that repository instead, for coordination that has to live somewhere else
than the repository being released. Several links may share one endpoint, and a worker names the same one as its own.
When two nodes read different mailboxes, the orchestrator relays a result from one to the other, so a node is never
told about a machine it cannot reach, and an endpoint that holds none of the repository's objects receives the source's
reachable history on its first task ([what to watch for](#what-to-watch-for) has the cost).

### The signing secret

Every message a mailbox carries is signed with one shared secret, named by `execution.secretEnv` and read from the
environment of each node. The name goes in the configuration file and the secret never does, exactly as a webhook's
signing secret is named rather than written.

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

Nothing connects to a worker. It polls its mailbox over Git, every five seconds when it is idle and almost at once
after it has answered something, so it runs behind NAT and on hosted CI runners with no inbound network of any kind.
It needs no checkout of the repository beforehand, and the folder it starts in does not have to be a Git repository:
the assignment carries the commands, the environment names and the exact source state, and the node materializes a
checkout of its own.

`--idle-timeout` ends the process with exit code `0` after that many seconds with nothing claimed and nothing in
flight, counted from its last activity, which is how a node started for one release ends by itself. `SIGINT` and
`SIGTERM` stop it as soon as the work it has claimed is finished, also with exit code `0`.

## How a run distributes tasks

A distributed release performs the same steps in the same order as a local one. What changes is where each frame
runs.

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
| the version stage and the manifest and lock-file preparation   | they write the state every build of the run then consumes   |
| the space login                                                | authentication must not travel                              |
| release commits, tags, changelogs, GitHub releases, records     | a worker is never granted the right to write a release ref  |
| `postPublish`, `announce`, `onFail`, `onSkip` and every run-level hook | they observe the run, which exists here          |

What a worker runs: the build frame (`beforeBuild`, the build commands, `postBuild`) and, where `runOnly` delegates
it, the publish frame (`beforePublish`, the publish commands and the stage's after hook). A frame always runs as one
unit on one node, because a hook that observed a folder another machine wrote would be observing nothing.

**Budgets are the run's and capacity is the node's.** The root `concurrency` budget bounds how many builds and
publishes are in flight across the whole run and is never multiplied by the number of workers: a task holds its stage
slot while it waits for a node. `execution.concurrency` is how many assigned tasks one node accepts at once, counted
across every run that reaches it. Work a node has no slot for waits in its mailbox; an assignment nobody claims
within the task deadline is withdrawn and placed again, up to three times, and then fails its package.

## Build outputs as inputs

A build product is normally ignored by Git, so a checkout says nothing about it and a machine that did not run the
build cannot learn what to ask for. `buildOutputs` is how a package says what its build leaves behind:

```yaml
buildOutputs: [dist]

packages:
  assets:
    buildOutputs: [dist, generated/types]
```

The key rides the ordinary ladder (root, space, space folder file, package, package folder file) and replaces whole.
Entries are literal paths relative to the package folder, slash-separated on every platform, and ignored files travel
on purpose: a `dist` folder that no commit holds is exactly what a consumer's build needs.

**What travels, and how it is checked.** A successful build captures its declared roots into a Git tree, and the
manifest that describes them travels inside the signed result: every entry with its type, mode, size and SHA-256, and
a header binding the set to the run, the plan, the task attempt, the ownership, the source states the build consumed,
the package and version, the platform it ran on and the output sets that went into it. The orchestrator verifies the
manifest against the tree before it admits the set, and the consuming node verifies it again against the digest its
assignment names before it installs a single file.

**Installation is atomic at the task-input boundary.** Files are written into a staging folder and verified as they
are written, then each declared root replaces its destination by a rename. No command of a task starts before every
one of its inputs is installed.

The set is staged in the checkout's private Git storage. When the final rename from there into the checkout fails, as
it does for a linked worktree whose private Git storage is on another filesystem, the set is staged again in a private
(`0700`) folder beside the outermost checkout and renamed from there, so the install stays a rename and temporary files
stay outside Git status and release records. When that rename fails too, the install fails with its error. Normal
completion removes the folder. If a process is killed during installation, a `dispat-outputs-` folder can remain in Git
storage or as a hidden sibling of the checkout. After confirming that no release using that checkout is active, an
operator may remove that run's leftover folder; dispat does not guess that another run's staging is abandoned.

**What is refused.** The prerequisite fails with `E227`, and the consumers of that prerequisite are blocked, when a
declared root is absent, when the manifest and the tree disagree, when the totals do not match the entries, when an
entry is not a file or a symlink, when a path is absolute or holds `..`, a `.git` component, a backslash, a colon or
a NUL, when a path lies outside the declared roots, when a symlink target is absolute or leaves its root, when a mode
is neither `0644` nor `0755`, when two paths differ only by case, when the platform does not match, or when the set
is over `transfer.maxFiles`, `transfer.maxBytes` or `transfer.maxManifestBytes`. The reason is a stable word in the
log rather than an echo of anything the task produced.

**Only declared paths travel.** A build that changes tracked files outside its declared roots has those changes
counted and reported as `W244` stray writes, and they are not admitted anywhere. A build whose output a consumer
needs therefore declares it.

**A provider this run does not release is still built.** When a consumer's build needs the outputs of a provider that
is not in the plan, that provider's build frame runs once per run as a `prepare` task, told exactly what
`dispat run` tells it: the version it already carries, a bump of `none`, no tag, no changelog, no record, no plan
entry and no event. The run summary reports it with its computation completed, its outputs admitted and its
publication `none`. One consequence is worth expecting: a run can leave a non-releasing package's `dist` rebuilt in
the orchestrator's checkout.

**What a provider relation says about transport.** `isBuildWaitingPublish` decides what a consumer's build waits for,
and under `{build: none}` the consumer's build does not read the provider's output at all: no outputs travel across
that hop, and a provider reachable only across `none` hops is not prepared. See
[the provider relation](./configuration/spaces.md#the-provider-relation) and the worked numbers in
[the terraform example](./examples/terraform.md). An edge that waits for publication still waits: a consumer that
installs its provider from a registry is ordered after the provider's publication exactly as it is on one machine.

## Publishing from workers

Publication stays on the orchestrator unless a package's `runOnly` names `worker` for the publish stage, and a space
with a `flow.login` publishes here whatever the key says. Publication is serialized per repository anyway, so
delegating one buys a run nothing and spreads registry credentials over one more machine.

Where a publication is delegated, four things hold:

- **Credentials live in the worker's environment.** The `env` pairs of the configuration travel unresolved: a value
  written as `$NPM_TOKEN` travels as that reference and is expanded on the node that runs the command, from that
  node's own environment. The token a delegated publish needs must therefore exist on that worker.
- **The authorization is a separate step.** The node installs the package's own verified build outputs, runs
  `beforePublish`, reports itself ready and stops. The orchestrator then re-verifies that it still holds the release
  lock, that a composed workspace's snapshot is unchanged, and that nothing relevant to the package changed in the
  meantime, and writes one single-use authorization naming the exact state of the branch it authorizes. The
  authorization carries the instant it stops meaning anything, and the node re-reads the branch one last time
  immediately before the command, so a withdrawal written after the authorization still stops the publish.
- **Publication and recording are serialized per repository.** With workers configured, one repository's packages
  publish one after another and are recorded in the same order.
- **The hook's exports come home.** What `beforePublish` exported travels with the ready message and is merged onto
  the release at the point the local path would have merged it, so the values a hook exported are on the release
  whether or not the publication that follows succeeds.

One limit is worth stating. A delegated publication's `DISPAT_EXPORT_GITHUB` cannot carry file attachments: the paths
it exports name a checkout on the worker that is deleted when the task ends. Export attachments from a stage that
runs on the orchestrator. A publish script that writes tracked files on the node is a stray write, reported as
`W244` and carried nowhere.

## Running scripts on workers

`dispat run` sweeps a script across the pool the way a release executes its builds. With worker links, from
`execution.workers` or from [`--worker`](#naming-a-worker-on-the-command-line), each package's task of the sweep is
placed on a node: the commands the swept script binds for that package, run in the package folder with
`DISPAT_STAGE=run:<script>`, the computed `DISPAT_*` pairs, the configuration's own `env` pairs unresolved, and the
values the package's providers exported earlier in the same sweep. The node materializes the package's input closure
as it does for a build and returns what the commands exported, which reaches the package's consumers wherever they
run. No hook brackets a sweep task, and nothing another task produced is installed before it: a script that needs a
provider's `dist` is a build rather than a sweep.

```sh
dispat run tests --since all
```

**Placement follows `runOnly`.** A sweep task is placed where the package's build would be: under `both` on the least
loaded worker with room, and on this machine only when no worker has any; under `orchestrator` here; and under
`worker` on a worker only, so a sweep with no worker link refuses such a package with `E225` before any script runs.
A sweep with no worker link at all runs every task here, as it always did: it fixes no plan, takes no lock, prints no
summary and reaches no mailbox.

**A sweep takes no release lock.** It records nothing and authorizes no effect a lock would have to fence, so its
messages are bound to a generation drawn from its own run identity instead of from lock objects, and a release of the
same repositories may run beside it: a sweep neither waits for a release nor excludes one. The refusals a release is
held to still come first. A worker, or a process running under a task's authority, may not start a distributed sweep
(`E226`), and a lock bypass beside worker links is refused with `E225`, because the bypass states that the repository
has no remote to coordinate through. The plan is fixed and logged as `plan fixed`, every link is probed, and the sweep
ends with the summary a distributed release prints: one `task outcome` line per task naming its node, its
computation, its outputs and how many values it exported, then the run's `run finished` line.

**What a script writes can come back.** A delegated task's files stay on the node that ran it unless the script
declares where it writes. `runOutputs` in the root file names, per script, folders relative to the root of each
package's repository:

```yaml
runOutputs:
  tests: [coverage]
```

After a delegated task succeeds, its node captures those folders under the manifest, the validation and the transfer
ceilings every build output is held to, and the orchestrator verifies each set and merges it into its own checkout once
every task of the sweep has answered:

- **The merge keeps what it does not replace.** A file replaces the file of its own path and every other file of the
  folder stays, because every task of the sweep writes into the same folder: ten packages' `tests` tasks write ten
  profiles into one `coverage/`.
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
- **An interrupted sweep merges nothing.** A folder assembled from whichever tasks answered before the interrupt is a
  folder nobody asked for, so the sets already admitted are left where they are and the run says so.

If the merge fails, for example because the checkout cannot accept a file, `dispat run` fails and logs
`run outputs could not be merged` at error level with the underlying cause. The task summary still follows, so the
failed merge is visible alongside the tasks that produced those outputs.

A root may be neither a package folder nor a folder holding one, and may not overlap any package's `buildOutputs`
root, because the two keys install differently: a build output root is replaced whole and a sweep's root is merged
file by file. `dispat status` reports each of these with `E225`. The key is read from the configuration the sweep is
started with alone, as `execution` is. Declare folders Git ignores: a folder Git tracks, or does not ignore, also
travels to every task inside the prepared input state.

### Naming a worker on the command line

A pipeline that creates a worker machine a minute before the run cannot write it into the committed file.
`--worker name`, repeatable on `release`, `run` and `status`, adds a link beside the ones `execution.workers` lists,
and like a configured link with no endpoint it reaches the repository being released. `--worker name=endpoint` names
another mailbox instead:

```sh
dispat run tests --since all --worker ci-worker-1
```

The link is held to every rule a configured one is: a node name, a credential-free endpoint when it states one, a name
no other link folds to, and a `secretEnv` the file names. A value that is not a node name alone, or leaves either half
of `name=endpoint` empty, is a usage error. The link is refused with `E226` under a task's authority and on a node
whose file says `role: worker`, because a worker dispatches nothing. It is no part of the plan digest, and
`dispat status --worker` prints the digest a distributed run would carry, resolves no remote and reaches no node.

## Locks, failure and recovery

**The lock is not optional here.** `unsafeDisableLock`, a per-repository bypass and `DISPAT_UNSAFE_DISABLE_LOCK` are
all refused with `E225` when workers are configured. Every node writes through a remote, and the lock is the only
thing that stops a second run authorizing the same publication from somewhere else.

**Ownership is re-verified before every new effect.** The orchestrator reads every release lock back from its remote
before the first worker probe, before every assignment and before every publication, whether that publication is
delegated or kept here, and a successful answer is never reused for the next effect. A lock that is no longer on the
remote ends the run with `E336`: no new assignment is written, every attempt in flight is withdrawn, and a publication
that was authorized before the loss is still recorded, because a create-only record of an effect that already happened
is not a new effect. A read that fails is not a loss: each read is bounded at fifteen seconds, and a failed one is
read again, one and then two seconds later, with a warning each time. Only when three reads have failed does the run
stop as it stops for a loss, with the lost line naming the reason `unverified` instead of `lost`; the lock is still
this run's, so it is given back. A run that was interrupted during a read has lost nothing.

**A node that stops answering blocks its dependents.** A compute task that was never claimed is withdrawn by
revoking its branch and placed again. A claimed attempt that passes its deadline takes its node out of the pool, its
slot is not reused, and the packages downstream of it are not attempted. A task is never retried on another node
inside the same run: the next run builds it again.

**Capacity returns only on evidence.** A slot goes back when a result arrives, when a withdrawal is acknowledged, or
when an unclaimed assignment is fenced before any worker can claim it. Deleting the branch or writing cancellation
under an exact lease on that assignment provides this proof without a worker acknowledgement. An attempt that
simply stopped answering leaves its slot where it is, because the work may still be running on that machine.

**A publication whose outcome cannot be established is reported as such.** When a publication was authorized and no
result came back, the run withdraws it and waits `timeouts.cancel` for an acknowledgement. An acknowledgement saying
the publish command had not started is an ordinary publish failure. An acknowledgement from after the command
started, or no acknowledgement at all, is `E228`: the package failed at its publish stage, no second attempt is made
in this run, its dependents are blocked, and the run exits non-zero.

An authorization push that did not report success is settled by reading the branch, never by pushing it again.
Found on the branch, or under the result the node wrote on top of it, the authorization landed, and the run reads the
node's result as usual. Two failures prove that no node read it: a failure preparing it locally, which happens before
any push, and a push the remote refused, a rejected lease or a server rule, whose branch does not carry it. For either
one dispat withdraws the waiting attempt, waits for its acknowledgement, fails the package at the authorization and
gives the lock back. Any other outcome is unknown: the worker may already have received permission and started
publishing, and a missing branch, or one reset to its earlier state, cannot prove otherwise. The run withdraws the
attempt as it withdraws a publisher that never answered, and unless the node says its command never started it
reports `E228` and retains the repository's release lock and any coordination evidence still present.

If the publisher never acknowledged, the release lock of the repository it was publishing into is **retained**. The
uncertain publication's authorization ref is also retained, whether the node acknowledged after starting publish or
never answered. Other owned coordination refs are cleaned up when their current tips still belong to this run. The
error names the order of recovery, and it is the order to follow:

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

**Retry is the supported recovery.** dispat has no registry reconciliation step. Once the outcome is known, the next
ordinary run plans what is still owed and publishes it, exactly as it does after an interrupted local publish. Read
[recovering from a failed run](./reference/releasing/recovery.md) for the general shape of that.

**Leftover coordination branches need classification before deletion.** A completed run deletes its own refs before
it gives its release locks back, within two minutes, and reports `W244` when one survives, with exit code `0`, because
a coordination branch carries no release record. A mailbox that does not answer within that bound costs the refs, never
the locks. An
`E228` run retains the uncertain publication's authorization as evidence; follow the recovery order above before
deleting it. For other branches of a crashed run, confirm that no process still uses them and that their current tips
belong to that run's authenticated chain. Investigate a ref whose tip changed unexpectedly or cannot be authenticated
rather than deleting it as this run's residue.

## Security: what distributed execution exposes and how to contain it

**Adding worker nodes widens the set of machines and repositories that can affect what a release publishes.** On one
machine, the checkout, the credentials and the artefacts never leave it. With workers, source and command text
travel through a repository, a shared secret decides what a node will execute, and an artefact another machine built
becomes what gets published. Each of the seven items below is a real exposure of that arrangement and what contains
it.

**1. The repository holds everything that travels.** Full source snapshots, untracked files that are not ignored and
unpushed commits included, the command text of every delegated stage, the computed `DISPAT_*` values of each stage,
the values scripts exported through `DISPAT_OUTPUT` and the build outputs themselves are all pushed to the repository
being released, or to the endpoint a link names instead, and a host keeps an object fetchable by its id for a while
after its branch is deleted.

Contain it by treating whatever travels as readable by everyone who can read that repository: on a public repository,
delegate no work whose inputs or outputs must stay private, and keep the orchestrator's checkout free of untracked
files that are not ignored. Protect release branches, release tags and the lock tag from worker credentials with the
host's ref rules. Never export a secret as a script output, and never write one literally in `env` or in a command:
write `$NAME`, which travels as the reference and is expanded on the node that runs the command.

**2. The signing secret is shared and symmetric.** Every node holding the secret can sign any message. A compromised
worker, or anyone holding the secret with push access to the repository the messages travel through, can forge an
assignment, which is command execution on every other node sharing that secret, and can forge a result, which is a
build output the orchestrator would admit.

Contain it by treating every machine that holds one secret as one trust zone. Do not share a secret across trust
levels: use a separate secret per zone. Keep the secret only in the CI secret store or an
equivalent, never in a file in the repository, and rotate it whenever a worker is decommissioned or suspected.
Per-link secrets are not implemented, so the secret is as strong as the least trusted machine holding it.

**3. Workers run what they are told.** A worker executes the commands of any authentic assignment, with its own
environment and its own credentials. That is what a worker is for, and it means an assignment is as powerful as the
node it reaches.

Contain it by running workers on dedicated machines, preferably ephemeral ones, with least-privilege credentials:
Git credentials that write `dispat-worker-*` branches and read the sources, and no release or registry credentials
unless that worker must publish. Do not serve tasks on a shared machine or an untrusted runner.

**4. A worker's build output becomes a published artefact.** Outputs are verified for integrity and origin, through
the digest, the signature and the identity in the manifest, and not for honesty. A compromised worker can return a
poisoned artefact that verifies perfectly.

Contain it by keeping the builds that must be trusted on the trusted machine: `runOnly: orchestrator`, or the
`[build, publish]` pair form, for any package whose artefact is signed or shipped to users. Where builds are
reproducible, compare the output of two nodes. Sign artefacts on the orchestrator rather than on the node that built
them.

**5. Publication credentials.** A space with a login script always publishes on the orchestrator, so its credentials
never need to exist on a worker. A publish without a login script may be delegated by `runOnly`, and the worker must
then hold whatever that publish reads from its environment.

The recommendation is plain: keep publishing on the orchestrator, with a login script or with `runOnly: both` or
`[both, orchestrator]`, unless there is a specific reason not to.

**6. Logs.** A worker's log holds the output of the scripts it ran, which is whatever those scripts print. Treat
worker logs exactly like CI logs: same retention, same access control, same care about what a build script echoes.

**7. Leftovers.** The coordination branches of a crashed run hold the same source, command text and outputs as live
ones. Delete them once the run is over. A retained lock is deliberate and is cleared only through the recovery order
above.

A checklist an operator can follow:

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

## Where a stage runs

`runOnly` is where one package's delegable stages may be placed. It takes one value for both stages or a
`[build, publish]` pair, exactly as `concurrency` does:

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

The key rides the same ladder as `buildOutputs` and `buildPlatforms` (root, space, space folder file, package,
package folder file) and replaces the pair whole: a level that states one value has said something about both stages.

Five rules follow from what the two stages are:

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

**A push is never repeated to learn its outcome.** Git reports each ref of a push as applied, as refused because the
lease no longer held (`[rejected]`), as refused by a server rule or hook (`[remote rejected]`), or not at all; a
`[remote failure]` and a connection lost in the middle of a push leave the outcome unknown. A party that did not hear
success reads the branch instead: its message on the tip, or on the tip's first-parent chain, landed; a refused message
the branch does not carry did not; anything else is unknown, and is read three times over three seconds before it is
treated as such. The branches a run creates are recorded for cleanup before they are pushed, so a push with no known
outcome still leaves a branch the run closes. An assignment whose push stays unknown is revoked under a lease on
itself and offered again when the branch is gone, and goes on when the node has already claimed it. A node's claim
whose push stays unknown is run if it surfaces on the branch, and a withdrawal on top of it is acknowledged. A result a
server rule refuses is reported once more without what was refused: a build, a preparation or a sweep task reports a
failure with the reason `transfer-refused` and no outputs, and a publication keeps its status and drops its exports,
so the run hears an answer rather than waiting out `timeouts.task`. The node logs the server's reason, redacted.

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
outputs; a result that carries no outputs reports under a fixed 30 second bound.

## Diagnostics

Every failure of a distributed run carries dispat's numbered code and, beside it, the outcome class a reader
switches on.

| Code   | Category                    | Means                                                                            |
|--------|-----------------------------|----------------------------------------------------------------------------------|
| `E225` | `execution-configuration`   | a configuration no distributed run could be executed under: an unknown role, a capacity that is not one, a malformed or credential-carrying endpoint, a duplicated node name, a missing signing secret, a lock bypass beside workers, `commit.verify: false` beside workers on a release, a link with no endpoint whose release remote's push URL carries credentials or is not a Git remote a mailbox can use, an unsatisfiable `buildPlatforms`, `runOnly: worker` with no worker links, a node that failed preflight, overlapping `buildOutputs`, or a `runOutputs` root that is or holds a package folder or overlaps a build output root |
| `E226` | `execution-authority`       | work refused because of who asked: a release or a distributed sweep initiated on a worker or under a task's authority, a `--worker` link stated there, an assignment that is not authentically this run's, or a write the task's authority does not extend to |
| `E227` | `io-integrity`              | input or output data that is missing, changed, incomplete, incompatible or escaping its declared roots, or two tasks of one sweep writing one path with different bytes; it fails one prerequisite and blocks that prerequisite's consumers |
| `E228` | `publication-unknown`       | an authorized publication that never reported back; the run is incomplete, makes no second attempt, and retains the lock of an unfenced publisher |
| `E229` | `transport-cleanup`         | transport state the run could not leave in a safe place: an attempt that had to be fenced, or owned refs whose survival leaves an effect unresolved |
| `W244` | `transport-cleanup`         | the harmless half of the same subject: refs a completed run could not safely delete, and writes a build made outside what it declared. A run that is otherwise clean still exits `0`; inspect a retained ref before deleting it |

Three conditions dispat already had a code for keep it and join the specification's `native-recording-or-lock` class:
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
  process id is in `worker.lock`; a worker that finds the id of a process that is gone takes the folder over, so a
  crashed worker restarts without anybody deleting a file.
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
