# Everything the world outside Terraform needs to know: the addresses for
# manual DNS, the nameservers to confirm at the registrar, and the values
# the CI environment carries as GitHub secrets and variables.

output "ipv4" {
  description = "The site's global IPv4 address, for an A record when DNS is managed elsewhere."
  value       = google_compute_global_address.ipv4.address
}

output "ipv6" {
  description = "The site's global IPv6 address, for an AAAA record when DNS is managed elsewhere."
  value       = google_compute_global_address.ipv6.address
}

output "name_servers" {
  description = "The zone's nameservers, to confirm the registrar points at them; empty when manage_dns is off."
  value       = var.manage_dns ? data.google_dns_managed_zone.site[0].name_servers : []
}

output "wif_provider" {
  description = "The Workload Identity provider resource name, the GCP_WIF_PROVIDER secret."
  value       = google_iam_workload_identity_pool_provider.github.name
}

output "releaser_sa" {
  description = "The releaser service account email, the GCP_RELEASER_SA secret."
  value       = google_service_account.releaser.email
}

output "bucket" {
  description = "The site bucket's name, the DOCS_BUCKET repository variable."
  value       = google_storage_bucket.site.name
}

output "url_map" {
  description = "The URL map's name, the DOCS_URL_MAP repository variable the deploy invalidates."
  value       = google_compute_url_map.site.name
}

output "certificate" {
  description = "The managed certificate's name; describe it with gcloud to watch provisioning reach ACTIVE."
  value       = google_compute_managed_ssl_certificate.site.name
}
