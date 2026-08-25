# The identifiers of the Google Cloud footprint stay out of the repository:
# project_id and bucket_name have no defaults and arrive as TF_VAR_*
# environment variables from a gitignored .env on the operator's
# workstation, the only place terraform runs. Only the domain, the
# repository slug and the toggles are written here, because they are public
# by nature.

variable "project_id" {
  description = "The Google Cloud project that hosts the site."
  type        = string
}

variable "region" {
  description = <<-EOT
    The bucket's region. Cloud CDN serves cached content from its global edge
    regardless, so this choice affects only cache-fill latency and storage
    price. us-east1 sits in the lowest price tier and is the best compromise
    between a United States and a European audience.
  EOT
  type        = string
  default     = "us-east1"
}

variable "domain" {
  description = "The apex domain the site is served from."
  type        = string
  default     = "dispat.dev"
}

variable "bucket_name" {
  description = "The globally unique name of the bucket that holds the site."
  type        = string
}

variable "enable_www" {
  description = "Serve the www subdomain as a redirect to the apex."
  type        = bool
  default     = true
}

variable "manage_dns" {
  description = "Manage the site's records in the existing Cloud DNS zone named by dns_zone."
  type        = bool
  default     = true
}

variable "dns_zone" {
  description = <<-EOT
    The name of the Cloud DNS managed zone that already holds the domain,
    as `gcloud dns managed-zones list` prints it. The zone is expected, not
    created: the domain was bought with its zone in place, and Terraform
    only writes the site's records into it.
  EOT
  type        = string
  default     = ""

  validation {
    condition     = !var.manage_dns || var.dns_zone != ""
    error_message = "manage_dns needs dns_zone: name the existing managed zone, or set manage_dns = false."
  }
}

variable "github_repository" {
  description = "The owner/name slug of the repository whose workflows may impersonate the releaser."
  type        = string
  default     = "yohimik/dispat"
}
