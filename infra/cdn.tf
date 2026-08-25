# The serving path: bucket -> backend bucket with Cloud CDN -> URL map ->
# HTTPS proxy -> four global forwarding rules, two addresses times two ports.
# The four rules together sit inside the flat first-five-rules charge, which
# is the dominant line of the monthly bill; everything else here is free or
# fractions of a dollar at documentation traffic.
#
# DDoS protection needs no resource here: a global external load balancer
# carries Google's always-on layer 3 and 4 volumetric defence by default,
# free, and the CDN answers cached traffic at the edge, so a flood's cost
# exposure is bounded by cache egress. The paid tier, Cloud Armor, offers
# backend buckets only its edge policies (IP and geography filters; the
# WAF and rate-limiting rules need backend services), at per-policy and
# per-request fees; for a public static site the default is the right
# amount of protection.
#
# USE_ORIGIN_HEADERS hands cache policy to the deploy: the publish stage
# uploads HTML with a five-minute TTL and the content-hashed assets as
# immutable, and the CDN obeys those headers instead of guessing. Negative
# caching keeps a burst of requests for a missing path from hammering the
# bucket, while a short TTL lets a page that appears in the next release show
# up promptly.
resource "google_compute_backend_bucket" "site" {
  name        = var.bucket_name
  bucket_name = google_storage_bucket.site.name
  enable_cdn  = true

  cdn_policy {
    cache_mode       = "USE_ORIGIN_HEADERS"
    negative_caching = true

    negative_caching_policy {
      code = 404
      ttl  = 60
    }
  }
}

resource "google_compute_managed_ssl_certificate" "site" {
  name = "${var.bucket_name}-cert"

  managed {
    domains = concat([var.domain], var.enable_www ? ["www.${var.domain}"] : [])
  }
}

resource "google_compute_global_address" "ipv4" {
  name = "${var.bucket_name}-ipv4"
}

resource "google_compute_global_address" "ipv6" {
  name       = "${var.bucket_name}-ipv6"
  ip_version = "IPV6"
}

# The main map serves the apex from the backend bucket; when www is enabled,
# its hostname gets a matcher whose only job is a permanent redirect to the
# apex, so the site has exactly one canonical origin.
resource "google_compute_url_map" "site" {
  name            = var.bucket_name
  default_service = google_compute_backend_bucket.site.id

  dynamic "host_rule" {
    for_each = var.enable_www ? [1] : []

    content {
      hosts        = ["www.${var.domain}"]
      path_matcher = "www-redirect"
    }
  }

  dynamic "path_matcher" {
    for_each = var.enable_www ? [1] : []

    content {
      name = "www-redirect"

      default_url_redirect {
        host_redirect          = var.domain
        https_redirect         = true
        redirect_response_code = "MOVED_PERMANENTLY_DEFAULT"
        strip_query            = false
      }
    }
  }
}

# Port 80 exists to send everyone to 443. The .dev TLD is HSTS-preloaded so
# browsers never actually ask, but scripts and health checks do, and the
# redirect rides the same flat forwarding-rule charge either way.
resource "google_compute_url_map" "http_redirect" {
  name = "${var.bucket_name}-http-redirect"

  default_url_redirect {
    https_redirect         = true
    redirect_response_code = "MOVED_PERMANENTLY_DEFAULT"
    strip_query            = false
  }
}

resource "google_compute_target_https_proxy" "site" {
  name             = var.bucket_name
  url_map          = google_compute_url_map.site.id
  ssl_certificates = [google_compute_managed_ssl_certificate.site.id]
}

resource "google_compute_target_http_proxy" "http_redirect" {
  name    = "${var.bucket_name}-http"
  url_map = google_compute_url_map.http_redirect.id
}

resource "google_compute_global_forwarding_rule" "https_ipv4" {
  name                  = "${var.bucket_name}-https-ipv4"
  ip_address            = google_compute_global_address.ipv4.address
  port_range            = "443"
  target                = google_compute_target_https_proxy.site.id
  load_balancing_scheme = "EXTERNAL_MANAGED"
}

resource "google_compute_global_forwarding_rule" "https_ipv6" {
  name                  = "${var.bucket_name}-https-ipv6"
  ip_address            = google_compute_global_address.ipv6.address
  port_range            = "443"
  target                = google_compute_target_https_proxy.site.id
  load_balancing_scheme = "EXTERNAL_MANAGED"
}

resource "google_compute_global_forwarding_rule" "http_ipv4" {
  name                  = "${var.bucket_name}-http-ipv4"
  ip_address            = google_compute_global_address.ipv4.address
  port_range            = "80"
  target                = google_compute_target_http_proxy.http_redirect.id
  load_balancing_scheme = "EXTERNAL_MANAGED"
}

resource "google_compute_global_forwarding_rule" "http_ipv6" {
  name                  = "${var.bucket_name}-http-ipv6"
  ip_address            = google_compute_global_address.ipv6.address
  port_range            = "80"
  target                = google_compute_target_http_proxy.http_redirect.id
  load_balancing_scheme = "EXTERNAL_MANAGED"
}
