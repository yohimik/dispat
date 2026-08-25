# The domain's zone predates this configuration, so it is read, never
# created: Terraform expects the zone named by dns_zone and manages only the
# site's records inside it. A record that already exists at the apex (a
# parking page, a placeholder) must be deleted or imported first, because
# Cloud DNS holds one record set per name and type.
data "google_dns_managed_zone" "site" {
  count = var.manage_dns ? 1 : 0

  name = var.dns_zone
}

resource "google_dns_record_set" "apex_a" {
  count = var.manage_dns ? 1 : 0

  managed_zone = data.google_dns_managed_zone.site[0].name
  name         = "${var.domain}."
  type         = "A"
  ttl          = 300
  rrdatas      = [google_compute_global_address.ipv4.address]
}

resource "google_dns_record_set" "apex_aaaa" {
  count = var.manage_dns ? 1 : 0

  managed_zone = data.google_dns_managed_zone.site[0].name
  name         = "${var.domain}."
  type         = "AAAA"
  ttl          = 300
  rrdatas      = [google_compute_global_address.ipv6.address]
}

resource "google_dns_record_set" "www" {
  count = var.manage_dns && var.enable_www ? 1 : 0

  managed_zone = data.google_dns_managed_zone.site[0].name
  name         = "www.${var.domain}."
  type         = "CNAME"
  ttl          = 300
  rrdatas      = ["${var.domain}."]
}
