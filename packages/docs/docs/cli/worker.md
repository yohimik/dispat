# The worker command

Run `dispat worker` to serve the build and publish tasks another machine's release assigns to this one. A worker
plans nothing, starts no release and writes no release record: it reads the work addressed to it, runs what its
assignment authorized, and reports back.

```
dispat worker [--state-dir <dir>] [--idle-timeout <seconds>]
```

Read [distributed execution](../distributed-execution.md) for what an orchestrator and a worker are, and
[the `execution` object](../configuration/execution.md) for the keys below.

## What it needs

The node is described by the `execution` object of the configuration file this invocation reads. Three of its
settings are required here, and each is refused by name when it is missing:

| Setting                | Why it is required                                                                   |
|------------------------|--------------------------------------------------------------------------------------|
| `execution.name`       | it is how this node recognises the work addressed to it                              |
| `execution.endpoint`   | it is the repository this node reads that work from: the repository being released, or the mailbox the orchestrator's link names |
| `execution.secretEnv`  | it names the environment variable holding the secret every message is signed with    |

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

Nothing else about the repository is needed. A serving node declares no spaces, no packages and no release policy,
because the assignment carries the commands, the environment names and the exact source state; a file that declares
no space and no package is complete for this command alone. The folder the process starts in does not have to be a
Git repository, and the repository being released does not have to be checked out on the node beforehand.

The environment must hold the signing secret, and whatever the commands this node will run need. A value a stage
reads from the environment is expanded here, on the node, from this node's own environment.

```sh
export DISPAT_EXECUTION_SECRET="$(cat /run/secrets/dispat-execution)"
export NPM_TOKEN="$(cat /run/secrets/npm)"
dispat worker --config dispat.worker.yaml
```

## Flags

### `--state-dir`

The folder this node keeps its object cache, its task checkouts and its record of already-answered work in. Without
it, `dispat/worker` under the user cache directory.

The layout is `<state-dir>/<node name>/`, holding `cache/` (one bare repository per endpoint), `seen.json` (the
answered-work record) and `worker.lock`. Everything in it is reconstructible: a node whose folder was deleted makes
one again and pays a fetch, so a container may use an empty volume for it.

Two processes must not serve one node name from one state folder. The second one to start refuses, because two nodes
sharing a folder would each hold half the record of what has been answered. `worker.lock` holds the serving process's
id and is removed when that process stops. A worker that finds the id of a process that is gone takes the folder over,
so a crashed worker restarts without anybody deleting the file. A new claim is trusted after it has settled for a
second, which is how one of several workers started at once against one folder serves and the others refuse. A serving
worker reads the lock again before it claims each assignment, and stops when another process's id is written there.

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
| the state folder is already held by another process | non-zero, reported as `E225`                                             |
| another process's id appears in `worker.lock` while serving | non-zero, reported as `E225`, after the tasks already claimed have finished |

A signalled node stops claiming new work, withdraws nothing it has not been asked to withdraw, and lets what it has
claimed finish before the process ends. Give a supervisor a grace period as long as `execution.timeouts.task` if its
tasks are long, because a shorter one kills the task with the process.

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
