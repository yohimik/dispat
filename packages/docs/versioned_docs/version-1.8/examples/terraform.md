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

The commands are defined in [`infra/dispat.yaml`](https://github.com/yohimik/dispat/blob/main/infra/dispat.yaml); the address-to-resource import mapping is explicit in [`rebuild.sh`](https://github.com/yohimik/dispat/blob/main/infra/rebuild.sh). The temporary `terraform.tfstate` and `tfplan` are Terraform files, not Dispat release state, and disappear with the runner. Dispat creates no separate progress database, cache, or state bucket. After a successful apply, the package's `infra/v*` Git tag is its durable release record.

This bounded approach fits an existing, known footprint whose resources can all be imported deterministically. It does not make Terraform stateless. A team using a persistent remote backend should keep doing so; if that backend needs a bucket or table, bootstrap it before the stack that consumes it.

## How the site follows the infrastructure

[`packages/docs/dispat.yaml`](https://github.com/yohimik/dispat/blob/main/packages/docs/dispat.yaml) declares `infra` as a provider of `docs`. The real `infra` package uses the default `isBuildWaitingPublish: false`, so the docs build waits for `tf-plan` to finish and may overlap `tf-apply`. The docs publish always waits for the provider's publish, so deployment cannot begin until the saved Terraform plan applies successfully. A failed apply prevents the dependent site deployment.

That is the scheduler rule to choose deliberately:

- A consumer's **build waits for its provider's build** by default, then may overlap the provider's publish.
- A consumer's **publish always waits for its provider's publish**.
- Set `isBuildWaitingPublish: true` on the **provider** when a consumer's version and build must also wait for that provider to publish.

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
    isBuildWaitingPublish: true
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

The verified fixture plans `infra` from `1.2.0` to `1.3.0`, applies its saved plan, and records `infra/v1.3.0`. Because `infra` sets `isBuildWaitingPublish`, both application builds wait for that apply. Backend and frontend are independent consumers, so they may build and deploy in parallel after infrastructure succeeds, releasing `0.8.3` and `2.1.1` respectively. An unchanged rerun executes no stages and leaves the three tags unchanged.

The fixture at [`packages/docs/demo/fixtures/infra`](https://github.com/yohimik/dispat/tree/main/packages/docs/demo/fixtures/infra) uses local marker files, not Terraform or a cloud account, to verify that ordering. The repository's production Terraform is in [`infra/`](https://github.com/yohimik/dispat/tree/main/infra), where the CI guard on `tf-apply` prevents an accidental local apply.
