# LLVM and Linux kernel builds on two GCE workers

The `Release` workflow's `purpose=llvm-gce` or `purpose=kernel-gce` dispatch builds the selected checkout
once, retains its OCI archive, image identity and binary hashes, then runs five
independent cells. Every cell creates two fresh workers of one selected machine type in
one selected zone, verifies their exact boot image and matching CPU platform, and
loads the same image bytes. Workers have no service account or cloud scopes.
The preparer, coordinator and workers explicitly use Docker's classic `overlay2`
image store with `containerd-snapshotter` disabled. The driver rejects a different
store before provisioning or loading worker images and retains each daemon's
store/version evidence. This keeps the saved image's configuration digest
identical after loading; all image checks and execution use that exact immutable
ID. Tags are not an identity fallback.
The coordinator runs on the GitHub runner. Both supported machine types have
four vCPUs and 16 GiB memory. Each worker container has four CPUs,
14 GiB memory, one outer task slot and two inner Ninja or Make jobs. The workflow
defaults to `us-central1-b` and `Intel Cascade Lake`; these selections apply to
every competitor and every Dispat mode in the campaign.

The driver defaults to `--workload llvm`. Use `--workload linux-kernel` to select
the runner's separate Linux-kernel build profile with the same VM, container,
task-slot, inner-job and timeout limits. The selector is passed explicitly to
the coordinator and retained with the requested trial options; it does not
replace or relabel LLVM results. Both workloads use the same exact OCI archive
and image-identity contract.

Pass the same `--machine-type`, `--boot-image` and `--cpu-platform` to every `llvmcloud run` in a
campaign. The boot-image pin must be the full
`https://www.googleapis.com/compute/v1/projects/ubuntu-os-cloud/global/images/<image-name>`
selfLink; the driver uses it directly without resolving an image family again.
The driver defaults to `--machine-type n2-standard-4`, whose CPU pin accepts
`Intel Cascade Lake` or `Intel Ice Lake`. N2 supplies `--min-cpu-platform` at
creation. Selecting `--machine-type c3-standard-4` requires the explicit CPU pin
`Intel Sapphire Rapids`. C3's fixed CPU platform makes the minimum-platform
selector unnecessary; creation sets an NVMe boot-disk interface and gVNIC on
the default network, retaining the ephemeral public IP used by SSH. Both
profiles retain the 100 GB `pd-balanced` boot disk. The driver requires each
VM's actual platform to equal the pin exactly, including the first VM, and
checks C3's actual disk and NIC interfaces. A newer platform does not silently
satisfy an older pin. Invalid machine/CPU pairs fail before provisioning, and a mismatch
fails the cell without fallback. Requested pins remain in the private journal
and the retained trial/provenance evidence. Without these optional pins, the
standalone N2 driver resolves its image family once per cell and checks that the
two actual platforms match; that does not establish equality across cells.

The workflow's `cloud-machine-type` and `cloud-cpu-platform` inputs select this
same fixed profile for every cell. C3 uses the separate regional `C3_CPUS`
quota: a two-worker cell needs eight free vCPUs. CPU, memory, disk size, task
slots and inner build jobs remain equal across tools; changing machine series
starts a separate campaign. See Google's [machine specifications](https://docs.cloud.google.com/compute/docs/general-purpose-machines),
[Ubuntu interface support](https://docs.cloud.google.com/compute/docs/images/os-details),
and [CPU quotas](https://docs.cloud.google.com/compute/resource-usage).

The three Dispat cells use compilation, interpretation and hybrid modes. The Nx
and Lerna cells explicitly use `manual-gcp-split`: the experiment driver assigns
one LLVM backend or kernel architecture to each VM and invokes the tool there. That is custom
orchestration, not Nx Cloud or native cross-host scheduling. Changesets has no
build scheduler and has no measured build cell.

```sh
gh workflow run release.yml --ref <reviewed-source-ref> \
  -f purpose=llvm-gce -f llvm-profile=smoke
```

Use `llvm-profile=full` for both complete LLVM backend builds. Smoke is a partial
functional check and cannot be reported as a full LLVM build. For `kernel-gce`,
the full profile builds the large `allmodconfig` kernel-and-modules workload for
`x86_64` and `arm64`; smoke uses `defconfig` and omits modules. Fresh output trees
do not establish a cold operating-system page cache. Every cell has a
four-hour execution limit; setup has a separate 35-minute limit. The job and VM
lifetimes are bounded at five hours, leaving cleanup time. No machine/zone
fallback, warm build directory or silent competitor fallback is allowed.

The runner obtains existing repository-scoped GCP Workload Identity credentials.
The coordinator communicates with a host driver over a private Unix socket. The
driver uses a unique SSH key and guest-published host keys, starts only named
trial containers and forwards loopback Git/return ports. The fresh Git mailbox
uses TLS and separate per-role credentials; worker pushes are restricted to
their own pending/outcome refs. Production repository credentials and cloud
credentials never enter either worker or the coordinator container.

Authenticated Git request bodies are spooled to private temporary files before
the CGI backend receives their exact byte length. This supports Git's chunked
artifact pushes without changing `http.postBuffer`. Each request is limited to
2 GiB, with at most two files retained through backend consumption. Requests
that fail body validation are rejected before Git starts. Cancellation
unblocks the upload read and removes the temporary pathname. If the CGI child
already started, its existing lifecycle is unchanged and the quota remains held
until it returns; cancellation is not evidence that a Git write stopped. Files
are also removed after completed requests. Ref authorization and the exact
artifact/result admission checks remain unchanged.

Only `/cloud/results` is uploaded. `/cloud/private`, the control socket, Git
credentials, signing secrets and state workspaces are excluded. Physical node
IDs from `gcloud describe` are checked independently against each VM's metadata
service; task evidence must match those IDs. Resource probes, graph/attempt
evidence, returned artifact hashes, failed trials and cleanup results are kept.
The reporting layer applies the separate CCME measurement contract.

Cleanup rechecks each instance's name, numeric ID and ownership labels, deletes
only that trial's resources, and verifies that the exact IDs disappeared. An
API failure is an error, not proof of absence. A journal preserves uncertain
creation outcomes for the workflow's always-run cleanup step. The VM lifetime
is the fallback if the runner itself is lost. Cleanup records distinguish
`resourcesReleased` from evidence-collection errors; either failure still makes
the cell fail.
