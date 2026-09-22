# Worker nodes on Kubernetes

A cluster suits dispat's worker nodes as **batch capacity that exists for the length of one release**. It does not suit
them as a deployment that grows and shrinks on CPU load while a release is running. This page gives the reasons, the
arrangement that works, and the manifest for it. Read [distributed execution](../distributed-execution.md) first for
what an orchestrator and a worker are.

## What fits

A worker asks nothing of the network it runs in. It polls its mailbox repository over Git and pushes its answers back,
so a worker pod needs no Service, no Ingress and no inbound port. It runs behind NAT and in a private cluster.

A worker is also a well-behaved batch process:

- `--idle-timeout` makes it exit with code `0` after that many seconds with nothing claimed and nothing in flight,
  counted from its last activity, so a pod started for one release ends by itself and a long build never ends its own
  pod.
- A `SIGTERM` stops it as soon as the tasks it has claimed are finished, and that exit is also code `0`.
- Everything under `--state-dir` can be lost. The object cache is rebuilt, and an assignment is bound to the branch it
  arrived on, so an `emptyDir` volume is a correct state folder.

Releases come in bursts: minutes of work, a few times a day or a week. Machines kept warm for them are idle almost all
of the time. Starting the workers for a release and letting them end afterwards costs nothing between releases, and
that saving is the reason to run workers on a cluster.

The machines themselves follow from the pods. A worker pod requests the CPU and memory one build needs. When the
cluster has no room for it the pod stays pending, the cluster autoscaler or Karpenter adds a machine, and it removes
the machine again once the pod has ended. Horizontal scaling of machines is therefore driven by pending worker pods.
No metric has to be watched for it.

## What does not fit

A HorizontalPodAutoscaler that watches worker CPU during a release adds nothing, for four reasons.

- **The pool is fixed before any task is placed.** The orchestrator's configuration lists its workers by name. After
  planning it asks every one of them what it is, and a worker that does not answer within
  `execution.timeouts.preflight` refuses the run before anything is dispatched. A pod that appears later is in no
  list, was never asked, and receives no work.
- **CPU is the wrong signal.** A worker is at full CPU with one build running and at full CPU with ten more waiting.
  Waiting work is visible in the mailbox as unclaimed coordination branches, and the orchestrator knows the amount
  exactly before it dispatches anything, because it holds the plan.
- **The reaction is slower than the work.** A metric window, a pod start, an image pull and often a new machine take
  minutes. Most releases are over by then.
- **Scaling down is destructive.** An autoscaler removes whichever pod it chooses. A build lost that way fails its
  package for this run, and the run waits out `execution.timeouts.task` before saying so, because a task is never
  retried on another node. A publish lost that way has an unknown outcome (`E228`), and the run
  [retains that repository's release lock](../reference/releasing/release-lock.md#a-lock-a-distributed-run-retained)
  until a person has looked.

A demand signal exists for anyone who wants to start workers from zero without a pipeline step: the number of
branches under `refs/heads/dispat-worker-*` in the mailbox repository. It is never CPU.

## How large a pool is worth having

More workers help only while more builds are ready than there are slots. Three facts bound the useful size:

- No run is shorter than its longest chain of tasks, whatever the number of machines.
- The widest set of builds that can be ready at the same moment is the most slots a run can use. What a consumer's
  build waits for in its provider decides that width; see
  [the provider relation](../configuration/spaces.md#the-provider-relation).
- Publications of one repository run one after another. Workers shorten the build part of a release, and the sum of
  the publish durations stays.

Size the pool for the widest level of a typical release and no further. `dispat status` prints the plan without
running anything, which is where to read that width from.

## One Indexed Job per release

An Indexed Job gives every pod a stable number, which becomes the name the orchestrator addresses it by. The pods run
until they have been idle, then succeed, and the Job is finished.

The orchestrator lists the same names and waits long enough for pods and machines to appear:

```yaml
execution:
  role: orchestrator
  secretEnv: DISPAT_EXECUTION_SECRET
  timeouts:
    preflight: 600   # covers image pulls and new machines
    task: 3600
    cancel: 60
  workers:
    - {name: w-0, endpoint: git@github.com:acme/release-mailbox.git}
    - {name: w-1, endpoint: git@github.com:acme/release-mailbox.git}
    - {name: w-2, endpoint: git@github.com:acme/release-mailbox.git}
    - {name: w-3, endpoint: git@github.com:acme/release-mailbox.git}
```

A pipeline that creates its workers for the run can name them on the command line instead, with
`--worker name=endpoint` on the release, beside a file that states only the secret and the waits.

A worker's name is read from its configuration file, so each pod writes its own file from its index before it starts
serving:

```yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: dispat-workers
  namespace: release
spec:
  completionMode: Indexed
  completions: 4
  parallelism: 4
  backoffLimit: 0
  ttlSecondsAfterFinished: 600
  template:
    metadata:
      annotations:
        cluster-autoscaler.kubernetes.io/safe-to-evict: "false"
    spec:
      restartPolicy: Never
      automountServiceAccountToken: false
      terminationGracePeriodSeconds: 3600
      nodeSelector:
        kubernetes.io/arch: amd64
      securityContext:
        runAsNonRoot: true
        runAsUser: 1001
      containers:
        - name: worker
          image: registry.example.com/release/worker:1
          command: ["sh", "-c"]
          args:
            - |
              cat > /work/dispat.json <<EOF
              {
                "execution": {
                  "role": "worker",
                  "name": "w-${JOB_COMPLETION_INDEX}",
                  "endpoint": "git@github.com:acme/release-mailbox.git",
                  "secretEnv": "DISPAT_EXECUTION_SECRET",
                  "concurrency": 1
                },
                "logFormat": "json"
              }
              EOF
              exec dispat worker --root /work --state-dir /state --idle-timeout 900
          env:
            - name: DISPAT_EXECUTION_SECRET
              valueFrom:
                secretKeyRef: {name: dispat-execution, key: secret}
          resources:
            requests: {cpu: "7", memory: 24Gi}
            limits: {memory: 24Gi}
          volumeMounts:
            - {name: work, mountPath: /work}
            - {name: state, mountPath: /state}
            - {name: git-ssh, mountPath: /home/worker/.ssh, readOnly: true}
      volumes:
        - name: work
          emptyDir: {}
        - name: state
          emptyDir: {}
        - name: git-ssh
          secret: {secretName: mailbox-deploy-key, defaultMode: 0400}
```

The image carries `dispat`, `git` and whatever the build commands call. The release pipeline applies the Job, then
starts `dispat release` on the orchestrator. The preflight is what waits for the pool. When every worker has answered,
the run places its work, and fifteen idle minutes after the last task the pods succeed and the machines go away.

The choices in the manifest that matter:

| Setting | Why |
|---------|-----|
| `completions` equal to the number of listed workers | Every listed worker has to answer the preflight. A pool of another size needs another worker list. |
| `concurrency: 1` and a CPU request close to a whole machine | A build usually takes every core it finds. One slot per pod, one pod per machine, is what makes a pending pod mean a new machine. |
| `safe-to-evict: "false"` | The cluster autoscaler does not drain a machine from under a running task. |
| `terminationGracePeriodSeconds` as long as `timeouts.task` | On `SIGTERM` a worker finishes what it claimed. A shorter grace period kills the task with it. |
| `backoffLimit: 0`, `restartPolicy: Never` | A worker that failed is looked at, not restarted into the middle of a run. |
| `nodeSelector` on the architecture | It has to agree with the packages' `buildPlatforms`. Run one Job per platform when a release needs several. |

A StatefulSet with a volume claim per pod is the variation for large repositories. Its pods are named by ordinal in the
same way, they keep their fetched objects between releases, and the pipeline scales it up before a release and back to
zero after one. Use `--idle-timeout 0` there, because a StatefulSet restarts a container that exits.

## Secrets and isolation

A worker pod runs the commands an assignment carries, and an assignment is accepted when it verifies under the signing
secret. Whoever holds that secret, or can write to the mailbox repository with it, runs commands in these pods. Read
[what distributed execution exposes](../distributed-execution.md#security-what-distributed-execution-exposes-and-how-to-contain-it)
for the exposures that are not specific to a cluster, the trust-zone rule for the secret, and the operator checklist.
What a cluster adds to that:

- Give the workers a namespace of their own, no service account token, a non-root user, and a NetworkPolicy that
  allows egress to the Git host and the registries the builds read, and nothing else.
- Keep the signing secret and the mailbox deploy key in Secrets that only this namespace mounts, and do not mount the
  release job's credentials anywhere in it.
- A pool sharing one secret is one trust zone. Two namespaces that should not be able to run each other's commands
  need two mailbox repositories and two secrets.
- Keep publishing credentials off the workers. A space with a `login` script always publishes on the orchestrator, and
  `runOnly: orchestrator` keeps a package's build and publish there.
- Do not run a worker that publishes on preemptible machines. A publish that was authorized and never reported back
  leaves that repository's release lock held on purpose, and a preempted pod's task stays assigned until the task
  deadline rather than moving to another pod.

## What dispat does not do

- It does not admit a worker after the preflight. Capacity added during a run is not used by that run.
- It does not start a run with part of its pool. Every listed worker answers, or the run is refused with `E225`.
- It does not retry a task on another node within the same run. A lost pod fails its package, and the next run builds
  it again.
- It does not read a worker's name from the environment, which is why the pod writes its configuration file.
