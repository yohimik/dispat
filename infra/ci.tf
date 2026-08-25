# The release workflow's identity: Workload Identity Federation, no exported
# keys. GitHub's OIDC issuer vouches for the workflow run, the provider's
# attribute condition pins acceptance to this one repository, and the
# releaser service account is what those runs become.
#
# Infra is written only from CI: the release rebuilds an ephemeral state
# from the cloud (rebuild.sh), plans, applies, and discards it, so the
# releaser holds the roles an apply needs, self-managed by this very block.
# The identity itself is the one thing a CI apply cannot create, so it was
# created once at day zero with gcloud under these same names, and the first
# release's rebuild imports it into Terraform's map (see README.md).
# The repository pin above and the workflow_dispatch-only release trigger
# keep a human at the head of every run.
resource "google_iam_workload_identity_pool" "github" {
  workload_identity_pool_id = "github"
  display_name              = "GitHub Actions"
}

resource "google_iam_workload_identity_pool_provider" "github" {
  workload_identity_pool_id          = google_iam_workload_identity_pool.github.workload_identity_pool_id
  workload_identity_pool_provider_id = "github"
  display_name                       = "GitHub OIDC"
  attribute_condition                = "assertion.repository == \"${var.github_repository}\""

  attribute_mapping = {
    "google.subject"       = "assertion.sub"
    "attribute.repository" = "assertion.repository"
  }

  oidc {
    issuer_uri = "https://token.actions.githubusercontent.com"
  }
}

resource "google_service_account" "releaser" {
  account_id   = "releaser"
  display_name = "Release automation, impersonated by GitHub Actions through WIF"
}

resource "google_service_account_iam_member" "wif" {
  service_account_id = google_service_account.releaser.name
  role               = "roles/iam.workloadIdentityUser"
  member             = "principalSet://iam.googleapis.com/${google_iam_workload_identity_pool.github.name}/attribute.repository/${var.github_repository}"
}

# What an apply touches: the load balancer chain including CDN invalidation
# (which the docs deploy also uses), the site bucket and its objects, the
# DNS records when managed here, its own service account and WIF pool, and
# the project bindings this very block writes.
resource "google_project_iam_member" "releaser" {
  for_each = toset(compact([
    "roles/compute.loadBalancerAdmin",
    var.manage_dns ? "roles/dns.admin" : "",
    "roles/storage.admin",
    "roles/iam.workloadIdentityPoolAdmin",
    "roles/iam.serviceAccountAdmin",
    "roles/resourcemanager.projectIamAdmin",
  ]))

  project = var.project_id
  role    = each.value
  member  = "serviceAccount:${google_service_account.releaser.email}"
}
