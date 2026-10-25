# The worker command

Run `dispat worker` on a machine that should accept build and publish tasks from an orchestrator. It waits for work
addressed to its node name, runs the assigned commands, and reports the results. The orchestrator remains responsible
for planning the release and writing release records.

```
dispat worker [--state-dir <dir>] [--idle-timeout <seconds>]
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
| `execution.endpoint`   | the repository this node reads that work from: the repository being released, or the mailbox the orchestrator's link names. Without it, the push URL of the release remote of the checkout the worker runs in |

A worker started in a checkout of the repository being released needs no `execution.endpoint`: it reads its work from
that checkout's release remote, `commit.remote` or `origin`, at the push URL Git resolves for it, which is where an
orchestrator's link with no endpoint sends the work. The folder is `--root`, the current folder by default. A folder
that is not a Git repository, a remote that is not configured or pushes to more than one URL, and a push URL that
carries a credential or is otherwise no endpoint are refused with `E225`, naming both remedies: state
`execution.endpoint`, or start the worker in a checkout of that repository.

`execution.concurrency` (default `1`) is how many assigned command tasks this node takes on at once, counted across
every run that reaches it. `execution.transfer` is what this node accepts as one task's build outputs; a run whose
own ceilings are higher than a node's is refused before it dispatches anything.

```yaml title="dispat.worker.yaml"
execution:
  role: worker
  name: build-a
  endpoint: git@github.com:acme/project.git
  secretEnv: DISPAT_EXECUTION_SECRET
  concurrency: 2

logFormat: json
```

A worker configuration does not need spaces, packages, or release policy. Each assignment supplies the commands,
environment variable names, and exact source revision. When `execution.endpoint` is set, the worker can start outside
a Git repository and fetch the source it needs.

Set the signing secret and any credentials or variables the assigned commands need. Environment values are expanded
on the worker, using its own environment:

```sh
export DISPAT_EXECUTION_SECRET="$(cat /run/secrets/dispat-execution)"
export NPM_TOKEN="$(cat /run/secrets/npm)"
dispat worker --config dispat.worker.yaml
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
The worker checks the lock before every assignment and stops if another process has taken it.

### `--idle-timeout`

Stop the process after this many seconds with nothing claimed and nothing in flight, counted from the node's last
activity. `0`, the default, serves until the process is signalled. A task that outlives the window does not count as
idleness, so a long build never ends its own node.

This is how a node started for one release ends by itself: a CI job or a Kubernetes Job runs
`dispat worker --idle-timeout 900` and finishes some minutes after the release does.

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
bare invocation, `commit`, `github`, `changelog`, `autoversion`, `compute` and `worker` itself. They report `E226`.
Everything a build script legitimately calls stays available, including `exec`, `if`, `for`, `install`, `scanner`,
`writer`, `replacer`, `autowriter` and `trigger`, so a stage script behaves the same wherever it was placed.

A node with `execution.role: orchestrator` may run this command. While it serves a task it has that task's authority
and never consults its own worker list.

## Examples

A long-running service, started once and left serving:

```sh
dispat worker --config dispat.worker.yaml --state-dir /var/lib/dispat/worker
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
  dispat worker --config dispat.worker.yaml --state-dir /var/lib/dispat/worker
```

A CI job that serves one release and ends:

```yaml
jobs:
  worker:
    runs-on: ubuntu-latest
    steps:
      - uses: yohimik/dispat@v1
      - run: dispat worker --config dispat.worker.yaml --idle-timeout 900
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
