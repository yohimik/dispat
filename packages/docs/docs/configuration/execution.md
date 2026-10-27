# `execution`

Use `execution` to configure a node for distributed releases: its identity, task capacity, and worker links.
For setup and a walkthrough, start with [distributed execution](../distributed-execution.md). This page
is the option reference.

```yaml
execution:
  concurrency: 2
  maxAttempts: 1
  retryAfterUncertainAttempt: false
  name: orchestrator
  endpoint: git@github.com:acme/project.git
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
  transfer:
    maxFiles: 20000
    maxBytes: 2147483648
    maxManifestBytes: 8388608
    maxPackBytes: 4294967296
    timeout: 1800
```

With the key absent, or with `workers` absent or empty, a release plans and executes on one
machine.

Distributed nodes currently require Linux or macOS for the persistent shared node-slot authority. Windows
distributed execution is refused before task effects. Local execution without worker links and without
explicit shared node capacity remains available.

`--distribution-mode=compilation` is the default for `release`, `run` and `status`. It compiles one task
descriptor per executable logical task before work starts. With `interpretation`, each admitted node independently
derives the same plan and graph from the pinned input, verifies their digests, then proposes locally ready work.
The first node validates each exact proposal and alone advances canonical task, ledger and release refs. A worker
must confirm its own signed pending branch before command effects. `hybrid` remains unavailable and is refused
before task effects. The mode is an invocation choice; there is no static node role field.
When remote workers participate, `release` and `run` require `--return-endpoint=https://host:port`.
`--return-listen=host:port` can set a different local bind address. Status accepts the mode but starts no listener.

## Root only, and read from the entry configuration alone

Each machine reads `execution` from its startup configuration. Two rules keep that authority local:

**It exists in the root file only.** A space, a package, a package's own folder file and a space configuration file
reject it as an unknown key. A checkout that travels to another machine must not be able to replace that machine's
execution authority.

**It is read from the configuration the run was started with.** An imported configuration and a linked peer may
carry an `execution` object of their own, because any peer may be another run's entry, and it is never consulted.
dispat writes one debug line per repository whose own object it ignored. On a worker node, the configuration inside
a transported checkout is never read for authority either.

## Keys

| Key           | Type                        | Default           | Description                                                                                          |
|---------------|-----------------------------|-------------------|------------------------------------------------------------------------------------------------------|
| `concurrency` | int                         | `1`               | How many claimed command tasks this node runs at once, across runs. Must be at least 1.              |
| `maxAttempts` | int | `1` in interpretation | Maximum accepted pickups per delegated logical task, counting the first; interpretation only, from 1 through 64. |
| `retryAfterUncertainAttempt` | boolean | `false` | Assert that delegated commands can safely repeat after an attempt whose external effects are uncertain. |
| `name`        | string                      | none              | This node's identity when it serves tasks. Required by `dispat worker`.                               |
| `endpoint`    | string                      | the release remote | The repository this node reads work from and writes its own pickup branches to when it serves tasks. Without it, `dispat worker` uses the release remote of the checkout it runs in. |
| `secretEnv`   | string                      | none              | The name of the environment variable holding the shared signing secret. Required with `workers`, and required by `dispat worker`. |
| `identityKeyEnv` | string | none | On a late worker, names the environment variable holding its base64url Ed25519 private seed. |
| `workers` | array of `{name, endpoint, lateJoin, identityPublicKey}` | empty | Preauthorized peer nodes. `lateJoin: true` requires a pinned independent public key and permits admission after tasks start. |
| `timeouts`    | object                      | see below         | The bounded waits of a distributed run, in seconds.                                                   |
| `transfer`    | object                      | see below         | What one task's declared build outputs may weigh, and how long moving them may take.                  |

### `concurrency`

The number of claimed command tasks this node runs at once, counted across every run that reaches it. It is this
node's capacity and not the run's budget: the root [`concurrency`](./README.md#top-level-options) budget bounds how
many builds and publishes the run has in flight across every machine, and it is never multiplied by the number of
workers.

On an orchestrator, this also limits the local node in the task pool. Stages pinned with `runOnly: orchestrator` wait
for that capacity. The value must be at least `1`; here, `0` does not mean the number of CPUs.

### `retryAfterUncertainAttempt`

This is an explicit assertion by the first node that every delegated command in the run is safe to execute again
after an earlier pickup whose external effects are uncertain. The default is `false`. In interpretation,
`maxAttempts` independently bounds sequential pickups after a settled failed Result; a value greater than one
does not imply this safety assertion. When enabled, the signed
bootstrap and logical task graph bind the choice; an admitted worker cannot change it. After uncertain contact
loss, a task can move to another authorized worker only after the first node has signed its Cancel, waited the
bounded reply grace and recorded contact loss. The old physical pickup remains charged until its exact signed
finality. A late old result cannot
replace the first verified winner. Do not enable this for a publish or build script that can make an unverified,
non-idempotent external change.

### `maxAttempts`

In interpretation, this is the total number of accepted physical pickups allowed for one delegated logical task.
Omission means one, including the initial pickup; `2` permits at most one successor. The value must be from 1
through 64. A settled failed Result, after its required package hooks have settled, can lead to a sequential
successor while attempts remain. A timeout or lost contact also needs `retryAfterUncertainAttempt: true` and
signed Cancel and reply-grace evidence. Neither setting repeats first-node publication or other native effects.
The resolved count is signed into the run bootstrap and task graph. Compiled mode retains its existing bounded
placement policy.

### `name`

This node's identity when it serves tasks, written from letters, digits, dots, underscores and hyphens, with no two
dots in a row. The name is written into the coordination branches addressed to the node, so a name git would refuse
in a ref is refused when the file loads.

A worker's `name` has to be the `name` of the orchestrator's link to it. It names an execution endpoint and never a
repository peer: a node name and a repository identity are separate vocabularies and are free to differ.

### `endpoint`

This is the credential-free Git URL of the repository holding the run's coordination refs and pinned Git inputs.
Only the orchestrator advances canonical coordination and release refs. A worker reads exact inputs, creates its
own pending/output/failed branches and returns claims and results through the authenticated non-Git channel.
By default, the coordination repository is the repository
being released; a worker link can name a separate one. dispat accepts an
`https`, `ssh`, or `file` URL, an absolute path, and the scp-like `[user@]host:path` form.

It refuses `http` and `git`, which authenticate nobody; a query or a fragment, which a mailbox address has no use
for; a leading `-`, which git reads as an option; the `transport::address` form, which names a helper program to
run; and user information, except a bare ssh account such as `git@host`, because the configuration file is
committed. A refused endpoint is redacted in the message.

A worker that states no `endpoint` resolves the coordination repository from the checkout it is started in: the push URL of its release
remote, `commit.remote` or `origin`, which is what an orchestrator's link with no `endpoint` reaches. The resolution is
strict. `dispat worker` is refused with `E225` when the folder `--root` names is not a Git repository, when the remote
is not configured or pushes to more than one URL, and when its push URL breaks a rule above, a credential in it above
all; each refusal names both remedies, stating `endpoint`, or starting the worker in a checkout of the repository the
orchestrator's link names.

### `secretEnv`

Name the environment variable that holds the shared signing secret. dispat signs every coordination message with it.
Store only the variable name in the configuration, just as you would for a webhook's `secretEnv`.

It is required with a non-empty `workers` list, and required by `dispat worker`. Whether the variable is actually
set is a question for the run: a release that would dispatch with the variable unset or empty is refused with
`E225`, and so is a `dispat worker` that cannot authenticate what it reads.

The secret is shared and symmetric: every node holding it can sign any message. See
[the security section](../distributed-execution.md#security-what-distributed-execution-exposes-and-how-to-contain-it)
before one secret spans machines of different trust levels.

### `identityKeyEnv`

For a worker authorized with `lateJoin: true`, name the environment variable holding its canonical unpadded
base64url Ed25519 private seed (32 bytes before encoding). The matching public key is pinned on the
orchestrator's `workers` entry. The seed value stays in the worker's secret store and is never written to
configuration, Git coordination objects or logs. A shared `secretEnv` value or matching node name alone cannot
authenticate a late join or add capacity. An initially required worker can omit this key and uses strict signed
preflight; such a keyless worker cannot later register new capacity through the join route.

### `workers`

The execution links of one run: each entry is a node `name`, optionally the `endpoint` it reads, and optionally
`lateJoin: true`. The list is the run's preauthorized membership policy. An entry without `lateJoin: true` must
answer preflight before any task effect. A late entry must pin its own canonical base64url Ed25519
`identityPublicKey`; the serving node reads the matching 32-byte private seed from the environment variable named
by `execution.identityKeyEnv`. A late worker may be absent then, but must pass identity-signed admission,
capability and capacity checks before receiving an executable claim. A run may start with only the orchestrator
admitted when every listed worker permits late joining.
The name is required and unique after case folding, and an endpoint a link states follows the rules above.

A link with no `endpoint` reaches the repository being released: the remote the release takes its lock on, which is
`commit.remote` or `origin`, at the push URL Git resolves for it, and in a composed workspace the entry repository's
remote. That push URL is held to the rules of an endpoint when a run that dispatches starts, before any lock: one that
carries a credential, a query or a fragment, or is not a Git remote a node could reach, is refused with `E225`, naming
the link and the remote and redacting the URL. Keep the credential in a credential helper or in `http.extraheader`
rather than in the URL. A release checks the push URL again at the destination its lock was taken on, and one debug
line per such link names the remote it reaches.

A link that states an `endpoint` names a coordination repository for this run. All links in one run must resolve
to the same exact canonical Git endpoint; a different endpoint is refused before locks or task effects. It is a
list of objects rather than a map so node names keep the case the file wrote them in. The orchestrator makes exact
predecessor result objects available to the consumer node and alone advances canonical task, ledger and release refs.

An empty or absent list preserves local execution, and a present but empty list means what an absent one means.

#### Links named on the command line

`--worker name` or `--worker name=endpoint`, repeatable on `dispat release`, `dispat run` and `dispat status`, adds one
required-at-start link to this list for one invocation, after the ones the file states. Configure `lateJoin: true`
in `execution.workers` for a node that may arrive after tasks begin. A name alone reaches the repository being
released, as a configured link with no `endpoint` does. The flag exists for the machine a pipeline creates a minute
before the run, which a committed file cannot know about:

```sh
dispat release --worker ci-worker-1
```

A file that states only `secretEnv` and the waits is enough for it. The link is added before the file is validated, so
it is held to every rule of this page: the name, an endpoint when it states one, a name no configured link folds to,
and the `secretEnv` the file has to name, each refused with `E225` and a message naming the flag and the node name it
was given, with any credential in the endpoint redacted. A value that is neither a node name alone nor `name=endpoint`
with both halves stated is a usage error, and it is never echoed. A process running under a task's authority
refuses the flag with `E226`; a task cannot expand the run's node set. The links are no
part of the plan digest, and `dispat status --worker` prints that digest, resolves no remote and reaches no node.

### `timeouts`

Every value is in seconds, and `0` or an absent key keeps the default.

| Key         | Default | Bounds                                                                                                     |
|-------------|---------|-------------------------------------------------------------------------------------------------------------|
| `preflight` | `60`    | one worker's answer to the probe sent after planning and before any dispatch                                 |
| `admission` | `600`   | the finite window, up to 604800 seconds (seven days), after successful global hooks and signed execution-start for a preauthorized late worker to join; expiry fails still-inert worker-only tasks without inventing a command result |
| `task`      | `3600`  | one claimed task. The signed task carries the deadline and the node enforces it too |
| `cancel`    | `60`    | the wait for a withdrawn task to acknowledge. Capacity is held until it does                                 |

Raise `preflight` when the nodes are started by the same pipeline as the release: it has to cover the time a machine
takes to exist and answer.

### `transfer`

These settings limit the size of one task's build outputs and the time allowed to transfer them. dispat checks the
limits both when capturing outputs and when installing them on another node. The value `0` or an absent key keeps the
default.

| Key                | Default      | Bounds                                                                                     |
|--------------------|--------------|---------------------------------------------------------------------------------------------|
| `maxFiles`         | `20000`      | the number of files one task's outputs may hold                                             |
| `maxBytes`         | `2147483648` | their total size in bytes (2 GiB)                                                           |
| `maxManifestBytes` | `8388608`    | the largest protocol document one task may produce, in bytes (8 MiB)                        |
| `maxPackBytes`     | `4294967296` | the Git pack returned for one result, including metadata (4 GiB)                            |
| `timeout`          | `1800`       | one transfer, in seconds                                                                    |

`maxManifestBytes` limits every coordination document, including compiled task templates. Setting it too low can prevent
ordinary tasks from running.

`maxPackBytes` is separate from `maxBytes`: Git pack framing and metadata add bytes beyond the output files.
The worker and first node enforce the same signed pack ceiling on the return channel.

`timeout` covers fetching and installing task inputs, and returning a result with build outputs. Allow enough time for
the largest output set your packages produce. A result with no outputs uses a fixed 30-second reporting limit.

A worker states its own ceilings. A run whose ceilings are higher than a node's is refused at preflight with `E225`,
rather than having a node silently truncate what it was asked to move.

## What a distributed run refuses

Every refusal below carries `E225` and the `execution-configuration` category, and none of them lets a command run.
The refusals of the object's own values happen when the configuration loads, and the refusals of a run that would
dispatch (a lock bypass, `commit.verify`, the signing secret, the release remote a link reaches) happen when it starts,
before any lock. The two that depend on the plan, a stage pinned to `worker` with no worker links and a
`buildPlatforms` no node satisfies, happen once the plan is fixed, which for a release is after it has taken its locks,
and still before any hook, stage or record; a release then gives its locks back. A node that fails preflight is
refused at the same point:

- a `concurrency` below 1, a negative timeout or ceiling;
- a `name` or a worker `name` that is not a node name, or two links whose names fold together;
- an `endpoint` git could not safely be pointed at, including the release remote's push URL that a link with no
  endpoint would reach;
- a link named with `--worker` that breaks any rule a configured link is held to;
- `workers` with no `secretEnv`, or a `secretEnv` naming a variable that is unset or empty in the environment of a
  run that would dispatch;
- `workers` beside any release-lock bypass: `unsafeDisableLock`, a per-repository bypass, or
  `DISPAT_UNSAFE_DISABLE_LOCK`;
- `workers` on a release where any participating repository sets `commit.verify: false`, because such a release
  reads its lock back before task activation and publication (a sweep, which takes no lock, is not refused for it);
- a package whose build is pinned to `worker` while the run has no worker links, once the plan is fixed;
- a package whose `buildPlatforms` no configured worker satisfies, once the nodes have answered;
- two packages whose `buildOutputs` claim one folder, or a declared root holding another package's folder, which
  `dispat status` reports as well;
- a `runOutputs` root that is or holds a package folder, or overlaps a package's `buildOutputs` root, which
  `dispat status` reports as well.

## The ladder keys beside it

Three related keys configure individual packages. They follow the usual override order (root, space, space folder
file, package, package folder file), and each value replaces the inherited value in full:

| Key              | Documented in                                                                                                 |
|------------------|----------------------------------------------------------------------------------------------------------------|
| `buildOutputs`   | [Space options](./spaces.md#space-options) and [package options](./packages.md#package-options)                  |
| `buildPlatforms` | [Space options](./spaces.md#space-options) and [package options](./packages.md#package-options)                  |
| `runOnly`        | [Space options](./spaces.md#space-options), [package options](./packages.md#package-options) and [where a stage runs](../distributed-execution.md#where-a-stage-runs) |

One key is neither: `runOutputs`, the folders a `dispat run` sweep of a script writes, which a sweep executed on the
pool carries back and merges into this checkout. It is root-only and read from the entry configuration alone, as this
object is. See [running scripts on workers](../distributed-execution.md#running-scripts-on-workers).

## See also

- [Distributed execution](../distributed-execution.md) for the arrangement, the transport and the recovery, and
  [the coordination repository](../distributed-execution.md#the-coordination-repository) for what travels through the repository
  being released and how to protect its release refs.
- [The worker command](../cli/worker.md) for the node that reads this object and serves tasks.
- Annotated example files:
  [`dispat.example.orchestrator.yaml`](https://github.com/yohimik/dispat/blob/main/services/dispat/dispat.example.orchestrator.yaml)
  and
  [`dispat.example.worker.yaml`](https://github.com/yohimik/dispat/blob/main/services/dispat/dispat.example.worker.yaml).
- [Configuration file reference](./README.md) for every other top-level key.
- [models](../go/models.md) for the Go shapes of this object.
