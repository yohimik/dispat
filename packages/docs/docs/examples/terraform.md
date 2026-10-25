# Terraform before application deployment

This repository treats its Google Cloud infrastructure as the `infra` package. The build stage imports a known set of
resources into temporary Terraform state and saves a plan. The publish stage applies that same plan, after which
dependent applications can deploy.

The build commands are:

```sh
sh rebuild.sh
terraform plan -input=false -out=tfplan
```

Its publish stage applies that saved plan in CI:

```sh
terraform apply -input=false tfplan
```

The commands live in [`infra/dispat.yaml`](https://github.com/yohimik/dispat/blob/main/infra/dispat.yaml), and
[`rebuild.sh`](https://github.com/yohimik/dispat/blob/main/infra/rebuild.sh) maps each Terraform address to its cloud
resource. The temporary `terraform.tfstate` and `tfplan` disappear with the runner. dispat adds no progress database,
cache or state bucket; after a successful apply, the package's `infra/v*` Git tag records the release.

Use this approach only when the resources are known and can all be imported reliably. Terraform still needs state
during the run. If your team uses a persistent remote backend, keep it; create any bucket or table it needs before
running the stack that consumes it.

## How the site follows the infrastructure

[`packages/docs/dispat.yaml`](https://github.com/yohimik/dispat/blob/main/packages/docs/dispat.yaml) declares `infra` as a provider of `docs`. The real `infra` package uses the default `isBuildWaitingPublish: false`, so the docs build waits for `tf-plan` to finish and may overlap `tf-apply`. The docs publish always waits for the provider's publish, so deployment cannot begin until the saved Terraform plan applies successfully. A failed apply prevents the dependent site deployment.

That is the scheduler rule to choose deliberately:

- A consumer's **build waits for its provider's build** by default, then may overlap the provider's publish.
- A consumer's **publish always waits for its provider's publish**.
- Set `isBuildWaitingPublish: true` on the **provider** when a consumer's version and build must also wait for that provider to publish.
- Set `isBuildWaitingPublish: {build: none}` on the **provider** when the consumer's build reads nothing the provider's build or publication produces, and only the deployments must follow one another. See [The provider relation](../configuration/spaces.md#the-provider-relation).

## An adapted infrastructure, backend, and frontend release

The demo expands this pattern to infrastructure, a backend and a frontend. The configuration below shows their
ordering; supply the referenced scripts for your own infrastructure and applications.

```yaml
initials: {infra: 1.2.0, backend: 0.8.2, frontend: 2.1.0}

scripts:
  tf-plan: sh rebuild.sh && terraform plan -input=false -out=tfplan
  tf-apply: terraform apply -input=false tfplan
  build-service: ./build.sh
  deploy-service: ./deploy.sh

flow: {build: build-service, publish: deploy-service}

packages:
  infra:
    path: infra
    tagFormat: 'infra/v{version}'
    flow: {build: tf-plan, publish: tf-apply}
  backend:
    path: backend
    dependencies: [infra]
  frontend:
    path: frontend
    dependencies: [infra]
```

Commit the infrastructure change with propagation to all downstream consumers, preview it, then release:

```sh
git commit -m "feat(infra)^^: add application database"
dispat status
dispat
```

The verified fixture plans `infra` from `1.2.0` to `1.3.0`, applies its saved plan, and records `infra/v1.3.0`. With the default `isBuildWaitingPublish: false`, both application builds wait for the infrastructure plan and then overlap the apply. Backend and frontend are independent consumers, so their builds may run in parallel. Their deploys wait until the infrastructure apply succeeds, releasing `0.8.3` and `2.1.1` respectively. An unchanged rerun executes no stages and leaves the three tags unchanged.

### Building the applications beside the infrastructure

If neither application build reads the Terraform plan, the builds do not need to wait for it. They only need the
infrastructure applied before deployment. Set the provider's build relation to `none` to express that:

```yaml
packages:
  infra:
    path: infra
    tagFormat: 'infra/v{version}'
    isBuildWaitingPublish:
      build: none
    flow: {build: tf-plan, publish: tf-apply}
```

The three builds can now run at once. Both deployments still wait for the apply; their versions and publication order
are unchanged.

There is a failure-policy distinction to check: a failed apply stops a consumer releasing only through propagation,
but a consumer with its own change can still deploy. Set `isBlocking: true` on `infra` if every dependent deployment
must stop after a failed apply.

Use `none` only when the dependency affects deployment order alone. A consumer that resolves its provider from a
registry needs `publish`; one that reads local build output needs `build`. dispat cannot inspect what a build reads,
so it never infers `none` or uses it as the default.

The relation fixes what happens before what, not how long a run takes. Take the three packages with build and publish stages of 4 and 6 minutes for the infrastructure, 9 and 3 for the backend, and 12 and 2 for the frontend, with the frontend consuming both:

| Relation on the providers | Unlimited slots | 1 build slot | 2 build slots | 3 build slots |
|---------------------------|-----------------|--------------|---------------|---------------|
| `publish`                 | 36              | 36           | 36            | 36            |
| `build`                   | 27              | 27           | 27            | 27            |
| `none`                    | 15              | 27           | 18            | 15            |

Minutes, with one publish slot in the three budgeted columns. With every slot free the three builds run side by side and finish at 4, 9 and 12, and the apply and the two deploys run in order from 4 to 10, 10 to 13 and 13 to 15. With a build budget of one, `none` equals `build`: a weaker relation pays for itself only where there are build slots to spare.

With every slot free a weaker relation never lengthens a run, but under a budget it can, because a consumer that may start early takes a slot a longer build would otherwise have had. Two build slots, one publish slot, and packages of `(2, 3)`, `(1, 2)` consuming the first and an unrelated `(8, 3)` finish at 11 under `build` and at 12 under `none`. Measure the run you have rather than reading a relation as a speed-up.

The fixture at [`packages/docs/demo/fixtures/infra`](https://github.com/yohimik/dispat/tree/main/packages/docs/demo/fixtures/infra) uses local marker files, not Terraform or a cloud account, to verify that ordering. The repository's production Terraform is in [`infra/`](https://github.com/yohimik/dispat/tree/main/infra), where the CI guard on `tf-apply` prevents an accidental local apply.

See [From one package to many](./one-to-many.md) to add deliverables while preserving existing package identities and release history.
