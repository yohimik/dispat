# The bucket that holds the built site. The name is not the domain: a
# domain-named bucket would demand Search Console verification and buy
# nothing behind a load balancer, which routes by URL map rather than by
# bucket name. Standard storage in a single region, no versioning and no
# access logs, because the site is a build artifact the release can always
# reproduce and every avoided feature is avoided cost.
#
# The website block matters behind the load balancer, with one asymmetry
# worth knowing: main_page_suffix and not_found_page both apply, but the
# redirect from /foo to /foo/ that the bucket's own website endpoint would
# answer does not happen here. Docusaurus builds with trailingSlash: true, so
# every generated link already carries the slash; a hand-typed slashless URL
# gets the site's own 404 page.
resource "google_storage_bucket" "site" {
  name                        = var.bucket_name
  location                    = var.region
  storage_class               = "STANDARD"
  uniform_bucket_level_access = true

  website {
    main_page_suffix = "index.html"
    not_found_page   = "404.html"
  }
}

resource "google_storage_bucket_iam_member" "public" {
  bucket = google_storage_bucket.site.name
  role   = "roles/storage.objectViewer"
  member = "allUsers"
}
