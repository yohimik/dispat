# `execution`

The `execution` object says what part this machine plays when a release is executed across several machines: the
role it is in, how much work it takes on at once, the mailbox it is reached at, and the worker nodes it may delegate
to. Read [distributed execution](../distributed-execution.md) for what the object is for; this page is the keys.

```yaml
execution:
  role: orchestrator
  concurrency: 2
  name: build-a
  endpoint: git@github.com:acme/release-mailbox.git
  secretEnv: DISPAT_EXECUTION_SECRET
  workers:
    - name: build-a
      endpoint: git@github.com:acme/release-mailbox.git
  timeouts:
    preflight: 60
    task: 3600
    cancel: 60
  transfer:
    maxFiles: 20000
    maxBytes: 2147483648
    maxManifestBytes: 8388608
    timeout: 1800
```

With the key absent, or with `workers` absent or empty, a release plans and executes exactly as it does on one
machine.

## Root only, and read from the entry configuration alone

`execution` is a node-startup setting, and two rules follow from that.

**It exists in the root file only.** A space, a package, a package's own folder file and a space configuration file
reject it as an unknown key. A checkout that travels to another machine must not be able to tell that machine what
role it plays.

**It is read from the configuration the run was started with.** An imported configuration and a linked peer may
carry an `execution` object of their own, because any peer may be another run's entry, and it is never consulted.
dispat writes one debug line per repository whose own object it ignored. On a worker node, the configuration inside
a transported checkout is never read for authority either.

## Keys

| Key           | Type                        | Default           | Description                                                                                          |
|---------------|-----------------------------|-------------------|------------------------------------------------------------------------------------------------------|
| `role`        | string                      | `orchestrator`    | `orchestrator` or `worker`, matched exactly. A worker refuses to start a release with `E226`.        |
| `concurrency` | int                         | `1`               | How many assigned command tasks this node runs at once, across runs. Must be at least 1.              |
| `name`        | string                      | none              | This node's identity when it serves tasks. Required by `dispat worker`.                               |
| `endpoint`    | string                      | none              | This node's own mailbox repository when it serves tasks. Required by `dispat worker`.                 |
| `secretEnv`   | string                      | none              | The name of the environment variable holding the shared signing secret. Required with `workers`, and required by `dispat worker`. |
| `workers`     | array of `{name, endpoint}` | empty             | The worker nodes this orchestrator may delegate to. A worker states none.                             |
| `timeouts`    | object                      | see below         | The bounded waits of a distributed run, in seconds.                                                   |
| `transfer`    | object                      | see below         | What one task's declared build outputs may weigh, and how long moving them may take.                  |

### `role`

`orchestrator`, the default, is the node a release is started on: it owns the locks, the plan, the assignments, the
authorizations and the records. `worker` is a node that serves tasks and initiates nothing; it refuses to start a
release, and stating `workers` beside it is refused, because a worker delegates nothing.

The value is matched exactly, as `logLevel` and `commitErrors` are, so a misspelling is refused rather than guessed
at.

### `concurrency`

The number of assigned command tasks this node runs at once, counted across every run that reaches it. It is this
node's capacity and not the run's budget: the root [`concurrency`](./README.md#top-level-options) budget bounds how
many builds and publishes the run has in flight across every machine, and it is never multiplied by the number of
workers.

On an orchestrator it is also the capacity of the local node in the run's own pool, which is what a frame pinned
with `runOnly: orchestrator` waits for. A stated `0` is refused rather than read as "the number of CPUs", because a
node that accepts no task is a node to remove rather than to configure.

### `name`

This node's identity when it serves tasks, written from letters, digits, dots, underscores and hyphens, with no two
dots in a row. The name is written into the coordination branches addressed to the node, so a name git would refuse
in a ref is refused when the file loads.

A worker's `name` has to be the `name` of the orchestrator's link to it. It names an execution endpoint and never a
repository peer: a node name and a repository identity are separate vocabularies and are free to differ.

### `endpoint`

The credential-free Git URL of this node's mailbox repository, which is the whole transport. dispat accepts an
`https`, `ssh` or `file` URL, an absolute path, and the scp-like `[user@]host:path` form.

It refuses `http` and `git`, which authenticate nobody; a query or a fragment, which a mailbox address has no use
for; a leading `-`, which git reads as an option; the `transport::address` form, which names a helper program to
run; and user information, except a bare ssh account such as `git@host`, because the configuration file is
committed. A refused endpoint is redacted in the message.

### `secretEnv`

The name of the environment variable holding the secret every mailbox message is signed with. The name goes in the
file and the secret never does, exactly as a webhook's `secretEnv` names its signing secret.

It is required with a non-empty `workers` list, and required by `dispat worker`. Whether the variable is actually
set is a question for the run: a release that would dispatch with the variable unset or empty is refused with
`E225`, and so is a `dispat worker` that cannot authenticate what it reads.

The secret is shared and symmetric: every node holding it can sign any message. See
[the security section](../distributed-execution.md#security-what-distributed-execution-exposes-and-how-to-contain-it)
before one secret spans machines of different trust levels.

### `workers`

The execution links of an orchestrator: each entry is a node `name` and the `endpoint` it is reached at. Both fields
are required, names are unique after case folding, and each endpoint follows the rules above.

It is a list of objects rather than a map so that node names keep the case the file wrote them in. Several links may
name one mailbox repository, and a link may have a mailbox of its own: when a provider and its consumer answer on
different mailboxes, the orchestrator relays the provider's result onto the consumer's.

An empty or absent list preserves local execution, and a present but empty list means what an absent one means.

### `timeouts`

Every value is in seconds, and `0` or an absent key keeps the default.

| Key         | Default | Bounds                                                                                                     |
|-------------|---------|-------------------------------------------------------------------------------------------------------------|
| `preflight` | `60`    | one worker's answer to the probe sent after planning and before any dispatch                                 |
| `task`      | `3600`  | one assigned task. An unclaimed assignment is withdrawn and placed again, up to three times; a claimed attempt is measured from the claim, and every assignment carries the deadline so the node enforces it too |
| `cancel`    | `60`    | the wait for a withdrawn task to acknowledge. Capacity is held until it does                                 |

Raise `preflight` when the nodes are started by the same pipeline as the release: it has to cover the time a machine
takes to exist and answer.

### `transfer`

What one task's declared build outputs may weigh, and how long moving them may take. A set over a ceiling is refused
where it is captured and again where it is installed, so a runaway build is refused rather than discovered as a full
disk on the node that consumes it. `0` or an absent key keeps the default.

| Key                | Default      | Bounds                                                                                     |
|--------------------|--------------|---------------------------------------------------------------------------------------------|
| `maxFiles`         | `20000`      | the number of files one task's outputs may hold                                             |
| `maxBytes`         | `2147483648` | their total size in bytes (2 GiB)                                                           |
| `maxManifestBytes` | `8388608`    | the largest protocol document one task may produce, in bytes (8 MiB)                        |
| `timeout`          | `1800`       | one transfer, in seconds                                                                    |

Two of them are worth a sentence more. `maxManifestBytes` bounds every document of the coordination protocol and
not only an output manifest, so a value below the size of an ordinary assignment makes the profile unusable rather
than merely strict. `timeout` bounds the fetch and install of a task's inputs and the push of a result that carries
build outputs, so size it for the largest output set a package produces; a result that carries no outputs reports
under a fixed 30 second bound instead.

A worker states its own ceilings. A run whose ceilings are higher than a node's is refused at preflight with `E225`,
rather than having a node silently truncate what it was asked to move.

## What a distributed run refuses

Every refusal below carries `E225` and the `execution-configuration` category, and every one of them happens before
a lock, a plan or a command:

- an unknown `role`, a `concurrency` below 1, a negative timeout or ceiling;
- a `name` or a worker `name` that is not a node name, or two links whose names fold together;
- an `endpoint` git could not safely be pointed at;
- `workers` on a node whose role is `worker`;
- `workers` with no `secretEnv`, or a `secretEnv` naming a variable that is unset or empty in the environment of a
  run that would dispatch;
- `workers` beside any release-lock bypass: `unsafeDisableLock`, a per-repository bypass, or
  `DISPAT_UNSAFE_DISABLE_LOCK`;
- a package whose `runOnly` pins a stage to `worker` while the run has no worker links;
- a package whose `buildPlatforms` no configured worker satisfies;
- two packages whose `buildOutputs` claim one folder, or a declared root holding another package's folder, which
  `dispat status` reports as well.

## The ladder keys beside it

Three keys decide what one package's build produces and where its stages may run. They are not part of this object,
because they are per-package policy rather than node startup, and they ride the ordinary ladder (root, space, space
folder file, package, package folder file), replacing whole:

| Key              | Documented in                                                                                                 |
|------------------|----------------------------------------------------------------------------------------------------------------|
| `buildOutputs`   | [Space options](./spaces.md#space-options) and [package options](./packages.md#package-options)                  |
| `buildPlatforms` | [Space options](./spaces.md#space-options) and [package options](./packages.md#package-options)                  |
| `runOnly`        | [Space options](./spaces.md#space-options), [package options](./packages.md#package-options) and [where a stage runs](../distributed-execution.md#where-a-stage-runs) |

## See also

- [Distributed execution](../distributed-execution.md) for the arrangement, the transport and the recovery.
- [The worker command](../cli/worker.md) for the node that reads this object and serves tasks.
- Annotated example files:
  [`dispat.example.orchestrator.yaml`](https://github.com/yohimik/dispat/blob/main/services/dispat/dispat.example.orchestrator.yaml)
  and
  [`dispat.example.worker.yaml`](https://github.com/yohimik/dispat/blob/main/services/dispat/dispat.example.worker.yaml).
- [Configuration file reference](./README.md) for every other top-level key.
- [models](../go/models.md) for the Go shapes of this object.
