# LLVM on two GCE workers

The `Release` workflow's `purpose=llvm-gce` dispatch builds the selected checkout
once, retains its OCI archive, image identity and binary hashes, then runs five
independent cells. Every cell creates two fresh `n2-standard-4` workers in
`us-central1-a`, verifies their exact boot image and matching CPU platform, and
loads the same image bytes. Workers have no service account or cloud scopes.
The coordinator runs on the GitHub runner. Each worker container has four CPUs,
14 GiB memory, one outer task slot and two inner Ninja jobs.

The three Dispat cells use compilation, interpretation and hybrid modes. The Nx
and Lerna cells explicitly use `manual-gcp-split`: the experiment driver assigns
one LLVM backend to each VM and invokes the tool there. That is custom
orchestration, not Nx Cloud or native cross-host scheduling. Changesets has no
build scheduler and has no measured build cell.

```sh
gh workflow run release.yml --ref <reviewed-source-ref> \
  -f purpose=llvm-gce -f llvm-profile=smoke
```

Use `llvm-profile=full` for both complete LLVM backend builds. Smoke is a partial
functional check and cannot be reported as a full LLVM build. Every cell has a
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
is the fallback if the runner itself is lost.
