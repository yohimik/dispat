# infra

The Google Cloud footprint that serves [dispat.dev](https://dispat.dev): a Cloud Storage bucket behind a global HTTPS
load balancer with Cloud CDN, a Google-managed certificate, a Cloud DNS zone, and the Workload Identity Federation setup
the release workflow authenticates with.

This folder is a versioned dispat package, and the footprint is written only from CI, through the dispat release: a
release that contains commits touching `infra/` rebuilds the state on the runner, plans in its build stage, applies in
its publish stage, and cuts an `infra/v*` tag, so the tag history is the list of applied states. No changelog and no
GitHub release are written for this package, because the commits behind each tag already say what changed. A release
with no infra commits skips the package entirely. See the package entry in the root `dispat.yaml`.

## Identifiers stay out of the repository, state too

Nothing in this folder names the project or the bucket. Terraform reads them from the environment:

| Variable             | Meaning                                                                           |
|----------------------|-----------------------------------------------------------------------------------|
| `TF_VAR_project_id`  | The Google Cloud project id.                                                      |
| `TF_VAR_bucket_name` | The site bucket's globally unique name.                                           |
| `TF_VAR_dns_zone`    | The existing Cloud DNS zone's name, as `gcloud dns managed-zones list` prints it. |
| `TF_VAR_region`      | Optional; the bucket's region, `us-east1` by default.                             |

Put them in a gitignored `.env` and hand it to dispat with `--env-file`, or export them in the shell. In CI the same
values, and the identity the run authenticates as, arrive as workflow environment from GitHub secrets and variables, set
once at day zero.

The state follows the same rule: none is stored anywhere. Each `tf-plan` regenerates `terraform.tfstate` by reading the
cloud, one [`terraform import`](./rebuild.sh) per declared resource (Terraform cannot discover resources on its own; the
state is the map from configuration address to cloud id, so the script spells the mapping out). In CI the file dies with
the release job; locally it is gitignored and disposable. It records every resource attribute, including the identifiers
above, which is why it must never be committed.

## Day zero

The only thing a CI apply cannot create is the identity CI authenticates as. It was created once with `gcloud`, under
the exact names `ci.tf` declares — the Workload Identity pool and provider (pinned to this repository), the releaser
service account and its bindings — together with the GitHub secrets and variables above; the first release imported and
adopted all of it, and `ci.tf` has managed it since. Recreating it in a fresh project means repeating those gcloud
commands from `ci.tf`'s declarations, refilling the CI environment, and enabling the APIs: compute, dns, iam,
iamcredentials, sts, storage, and cloudresourcemanager, the last of which the releaser needs at plan time (the
project-number lookup in `rebuild.sh`) and at apply time (project IAM bindings).

A release applies the footprint and deploys the site in one run, infra first (the docs package declares the edge that
orders it). The domain's Cloud DNS zone predates this configuration, so Terraform expects it by name (`TF_VAR_dns_zone`)
and only writes the site's records into it; with `manage_dns = false` instead, create A and AAAA records wherever the
domain is served, from the `ipv4` and `ipv6` outputs. After the first apply, the managed certificate provisions once DNS
resolves, reaching `ACTIVE` typically within the hour:

```sh
gcloud compute ssl-certificates describe "$(terraform output -raw certificate)" --global
```

## Day to day

Change a `.tf` file, inspect the plan locally, then let the release write it:

```sh
dispat exec tf-plan --for pkg:infra --in pkg:infra
```

`tf-plan` rebuilds the state and saves the plan, and is read-only toward the infrastructure, so it runs anywhere;
`tf-apply` is the one write and refuses to run outside CI. Commit the change conventionally and release: dispat versions
the package from the commits, the build stage rebuilds and plans on the runner, the publish stage applies exactly that
plan, and the `infra/v*` tag records that it happened.

## Costs

The four forwarding rules dominate at
about $18 a month, the flat charge for the first five rules. Cloud CDN keeps egress pennies at documentation traffic, the certificate and the addresses in use are free, storage and the DNS zone round to cents. Roughly $
19 to $20 a month all in.

## Protection

A global external load balancer carries Google's always-on layer 3 and 4 volumetric DDoS defence by default, free, and
the CDN answers cached traffic at the edge, so a flood's cost exposure is bounded by cache egress. Cloud Armor's paid
policies are deliberately absent; the comment in `cdn.tf` records why.

## The old GitHub Pages site

The `gh-pages` branch holds two pages: every old
`yohimik.github.io/dispat` path redirects, path-preserving, to the same page on the domain. GitHub Pages serves
`404.html` for every missing path, which is what lets two files cover the whole old site. The branch never changes again
and nothing in the repository feeds it.
