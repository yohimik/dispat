# Terraform before application deployment

This repository releases its Google Cloud footprint as the `infra` package. Its build stage reconstructs Terraform's working state by importing a fixed set of known cloud resources, then saves an exact plan:

```sh
sh rebuild.sh
terraform plan -input=false -out=tfplan
```

Its publish stage applies that saved plan in CI:

```sh
terraform apply -input=false tfplan
```

The commands are defined in [`infra/dispat.yaml`](https://github.com/yohimik/dispat/blob/main/infra/dispat.yaml); the address-to-resource import mapping is explicit in [`rebuild.sh`](https://github.com/yohimik/dispat/blob/main/infra/rebuild.sh). The temporary `terraform.tfstate` and `tfplan` are Terraform files, not dispat release state, and disappear with the runner. dispat creates no separate progress database, cache, or state bucket. After a successful apply, the package's `infra/v*` Git tag is its durable release record.

This bounded approach fits an existing, known footprint whose resources can all be imported deterministically. It does not make Terraform stateless. A team using a persistent remote backend should keep doing so; if that backend needs a bucket or table, bootstrap it before the stack that consumes it.

## How the site follows the infrastructure

[`packages/docs/dispat.yaml`](https://github.com/yohimik/dispat/blob/main/packages/docs/dispat.yaml) declares `infra` as a provider of `docs`. The real `infra` package uses the default `isBuildWaitingPublish: false`, so the docs build waits for `tf-plan` to finish and may overlap `tf-apply`. The docs publish always waits for the provider's publish, so deployment cannot begin until the saved Terraform plan applies successfully. A failed apply prevents the dependent site deployment.

That is the scheduler rule to choose deliberately:

- A consumer's **build waits for its provider's build** by default, then may overlap the provider's publish.
- A consumer's **publish always waits for its provider's publish**.
- Set `isBuildWaitingPublish: true` on the **provider** when a consumer's version and build must also wait for that provider to publish.
- Set `isBuildWaitingPublish: {build: none}` on the **provider** when the consumer's build reads nothing the provider's build or publication produces, and only the deployments must follow one another. See [The provider relation](../configuration/spaces.md#the-provider-relation).

## An adapted infrastructure, backend, and frontend release

The live demo turns the same pattern into three standalone packages. It is an illustrative configuration: supply the referenced scripts for your infrastructure and applications.

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

Neither application's build reads anything `tf-plan` writes. They read the repository, and what they need from the infrastructure is that it is *applied* before they deploy onto it. That is the `none` relation, declared on the provider:

```yaml
packages:
  infra:
    path: infra
    tagFormat: 'infra/v{version}'
    isBuildWaitingPublish:
      build: none
    flow: {build: tf-plan, publish: tf-apply}
```

The three builds may now run at once, and both deploys still wait for the apply. Nothing else about the run changes: the plan, the versions and the publication order are the same, and a failed apply still stops both deployments. Use `none` only for a relation that is genuinely a deploy order. A consumer whose lock file or build resolves the provider from a registry needs `publish`; one that reads the provider's local build output needs `build`. dispat cannot check the claim, so it never infers `none` and never makes it a default.

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
