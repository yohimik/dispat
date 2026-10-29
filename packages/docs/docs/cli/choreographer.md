# The choreographer command

Run `dispat choreographer` on a peer node that can execute build, test and other eligible prepublication stages.
In compilation it reads the signed compiled graph; in interpretation it independently derives the same plan and
graph from pinned input, verifies both digests, then discovers ready tasks. It executes only after its authenticated claim is
accepted and its pending Git branch's exact remote OID is confirmed. It writes its own output or failed branch. The orchestrator also
executes ordinary work and alone advances canonical task, ledger and release refs, publishes artifacts and writes
release records.

```
dispat choreographer [--state-dir <dir>] [--idle-timeout <seconds>]
```

Read [distributed execution](../distributed-execution.md) for what an orchestrator and a worker are, and
[the `execution` object](../configuration/execution.md) for the keys below.

## What it needs

Configure the node in the file's `execution` object. Set `name` and `secretEnv`; `endpoint` can be omitted when the
worker runs in a checkout with a suitable release remote:

| Setting                | Why it is needed                                                                     |
|------------------------|--------------------------------------------------------------------------------------|
| `execution.name`       | required: it is how this node recognises the work addressed to it                    |
| `execution.secretEnv`  | required: it names the environment variable holding the secret every message is signed with |
| `execution.identityKeyEnv` | for a late worker, names the environment variable holding its independent base64url Ed25519 private seed |
| `execution.endpoint`   | the repository this node reads pinned Git inputs and coordination state from; without it, the release remote of the checkout it runs in |

A worker started in a checkout of the repository being released needs no `execution.endpoint`: it reads its work from
that checkout's release remote, `commit.remote` or `origin`, at the push URL Git resolves for it, which is where an
orchestrator's link with no endpoint sends the work. The folder is `--root`, the current folder by default. A folder
that is not a Git repository, a remote that is not configured or pushes to more than one URL, and a push URL that
carries a credential or is otherwise no endpoint are refused with `E225`, naming both remedies: state
`execution.endpoint`, or start the worker in a checkout of that repository.

`execution.concurrency` (default `1`) is how many claimed command tasks this node takes on at once, counted across
every run that reaches it. `execution.transfer` is what this node accepts as one task's build outputs; a run whose
own ceilings are higher than a node's is refused before it dispatches anything.

```yaml title="dispat.choreographer.yaml"
execution:
  name: build-a
  endpoint: git@github.com:acme/project.git
  secretEnv: DISPAT_EXECUTION_SECRET
  concurrency: 2

logFormat: json
```

For a late worker, also set `execution.identityKeyEnv: DISPAT_WORKER_JOIN_SEED` and provide that environment
variable from its secret store. It holds the canonical unpadded base64url encoding of a 32-byte Ed25519 private
seed; the orchestrator pins the matching `choreographers[].identityPublicKey`. The private seed never goes in the
configuration or logs. The shared message secret alone cannot authorize a late join.

A worker configuration does not need a separate release policy. The signed graph binds task identity and exact
inputs; compilation also binds command templates. The worker verifies the graph and each eligible task before
claiming. When `execution.endpoint` is set,
the worker can start outside a Git checkout. The orchestrator advertises its HTTPS return URL and exact
TLS leaf SHA-256 in signed per-run bootstrap data; the peer needs no independent return URL setting.
The worker sends signed claim and result requests through that authenticated non-Git channel. It reads Git objects
and creates only its own `dispat-pending-*`, `dispat-output-*` (compilation), `dispat-completed-*`
(interpretation), and `dispat-failed-*` refs. An interpretation pickup name includes its creation time and a
persisted random identity, so two pickups in the same clock tick remain distinct; time does not authorize a claim.
Configure Git host rules
to deny its credential writes to canonical task and ledger refs, release branches, tags and the lock tag.

Set the signing secret and any credentials or variables the assigned commands need. Environment values are expanded
on the worker, using its own environment:

```sh
export DISPAT_EXECUTION_SECRET="$(cat /run/secrets/dispat-execution)"
export NPM_TOKEN="$(cat /run/secrets/npm)"
dispat choreographer --config dispat.choreographer.yaml
```

## Flags

### `--state-dir`

Set the directory used for the object cache, task checkouts, and completed-task records. The default is
`dispat/worker` under the user cache directory.

Each node gets `<state-dir>/<node name>/`, containing `cache/`, `seen.json`, and `worker.lock`. The state can be
recreated, so a container may start with an empty volume; it will need to fetch the objects again.

**Cache cleanup.** On startup, the worker removes coordination branches left in the cache by an earlier process.
Git's automatic maintenance is disabled there. After one idle minute with no claimed or running tasks, the worker
compacts the cache and removes objects unreachable from open coordination branches. It does this once per idle
period and never while a task is running.

**Interrupted state writes.** If a crash truncates `seen.json`, the next worker recovers complete claims, drops any
partial last entry, and rewrites the file on its first claim. Older dispat versions refuse that truncated file until
it has been rewritten.

**One process per node.** Two workers cannot share a node name and state directory. `worker.lock` records the serving
process ID and is removed on shutdown. A restarted worker can recover a stale lock, including one with its own reused
process ID, as commonly happens in containers. A new claim settles for one second to resolve simultaneous starts.
The worker checks its own state lock before every claim and stops if another process has taken it.

### `--idle-timeout`

Stop the process after this many seconds with nothing claimed and nothing in flight, counted from the node's last
activity. `0`, the default, serves until the process is signalled. A task that outlives the window does not count as
idleness, so a long build never ends its own node.

This is how a node started for one release ends by itself: a CI job or a Kubernetes Job runs
`dispat choreographer --idle-timeout 900` and finishes some minutes after the release does.

### Global flags

`--config`, `--root`, `--log-level`, `--log-format`, `--env-file` and the rest of the
[global flags](./README.md#global-flags) work as they do everywhere. `--log-level trace` is what shows every poll
tick and every ref inspected.

## Exit behaviour and signals

| Situation                                          | Exit code                                                                |
|----------------------------------------------------|--------------------------------------------------------------------------|
| `SIGINT` or `SIGTERM`                              | `0`, after the tasks already claimed have finished                        |
| `--idle-timeout` elapsed                           | `0`                                                                       |
| a required setting is missing, or the secret is unset or empty | non-zero, reported as `E225`                                   |
| no `execution.endpoint`, and no release remote a mailbox can be found in `--root` | non-zero, reported as `E225`                |
| the state folder is already held by another process | non-zero, reported as `E225`                                             |
| another process's id appears in `worker.lock` while serving | non-zero, reported as `E225`, after the tasks already claimed have finished |

On a signal, the worker stops accepting new tasks and waits for claimed tasks to finish. It does not withdraw other
work unless instructed to do so. Set the supervisor's shutdown grace period to at least `execution.timeouts.task` if
long-running tasks need time to finish.

Every line the process writes carries `role=worker` and `node=<this node's name>`, so one log of a distributed
release can be read per machine.

## What a worker will not do

Under a task's authority, dispat refuses the commands that start a release or write a release ref: `release` and the
bare invocation, `commit`, `github`, `changelog`, `autoversion`, `compute` and `choreographer` itself. They report `E226`.
Everything a build script legitimately calls stays available, including `exec`, `if`, `for`, `install`, `scanner`,
`writer`, `replacer`, `autowriter` and `trigger`, so a stage script behaves the same wherever it was placed.

This command serves tasks and does not initiate a release. While it executes a task it has that task's authority;
the static configuration has no node-role key.

## Examples

A long-running service, started once and left serving:

```sh
dispat choreographer --config dispat.choreographer.yaml --state-dir /var/lib/dispat/worker
```

For a long-running Docker worker, use `--init` so the container reaps subprocesses
started by Git and by package commands. Give the worker a writable state volume
and use an image with the tools its assigned commands need:

```sh
docker run --rm --init \
  -e DISPAT_EXECUTION_SECRET \
  -v "$PWD:/workspace:ro" \
  -v dispat-worker-state:/var/lib/dispat/worker \
  -w /workspace \
  yohimik/dispat-alpine:1 \
  dispat choreographer --config dispat.choreographer.yaml --state-dir /var/lib/dispat/worker
```

A CI job that serves one release and ends:

```yaml
jobs:
  worker:
    runs-on: ubuntu-latest
    steps:
      - uses: yohimik/dispat@v1
      - run: dispat choreographer --config dispat.choreographer.yaml --idle-timeout 900
        env:
          DISPAT_EXECUTION_SECRET: ${{ secrets.DISPAT_EXECUTION_SECRET }}
```

## See also

- [Distributed execution](../distributed-execution.md) for the whole arrangement, the security section and the
  recovery of a run that lost a node.
- [The `execution` object](../configuration/execution.md) for every key this command reads.
- [Worker nodes on Kubernetes](../examples/kubernetes-workers.md) for a pool that exists for the length of one
  release.
- [dispat in CI](../reference/ci.md#worker-nodes-in-a-pipeline) for the worker job beside the release job.
