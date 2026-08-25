# Regenerates the state by reading what actually exists, so no state is
# ever stored anywhere: each release's build stage rebuilds it on the
# runner, plans, applies, and the file dies with the job; locally the same
# rebuild feeds an inspection plan. Terraform cannot discover resources on
# its own; the state is the map from configuration address to cloud id, and
# refresh only rereads entries already mapped. So this script spells the
# mapping out, one import per resource the configuration declares, skipping
# whatever does not exist yet. A plan afterwards creates the gaps and
# updates the rest. Every name derives from the bucket's, which is what
# makes the mapping mechanical.
set -eu

: "${TF_VAR_project_id:?TF_VAR_project_id is required (the project id is not committed)}"
: "${TF_VAR_bucket_name:?TF_VAR_bucket_name is required (the site bucket name is not committed)}"

P="$TF_VAR_project_id"
B="$TF_VAR_bucket_name"
DOMAIN="${TF_VAR_domain:-dispat.dev}"
SA="releaser@${P}.iam.gserviceaccount.com"
# The WIF pool's resource name carries the project number, not the id.
PN="$(gcloud projects describe "$P" --format='value(projectNumber)')"

rm -f terraform.tfstate terraform.tfstate.backup
terraform init -input=false

imp() {
  if terraform import -input=false "$1" "$2" >/dev/null 2>&1; then
    echo "imported $1"
  else
    echo "skipped  $1 (does not exist yet, or not in this configuration)"
  fi
}

imp google_storage_bucket.site "$B"
imp google_storage_bucket_iam_member.public "$B roles/storage.objectViewer allUsers"
imp google_compute_backend_bucket.site "projects/$P/global/backendBuckets/$B"
imp google_compute_managed_ssl_certificate.site "projects/$P/global/sslCertificates/$B-cert"
imp google_compute_global_address.ipv4 "projects/$P/global/addresses/$B-ipv4"
imp google_compute_global_address.ipv6 "projects/$P/global/addresses/$B-ipv6"
imp google_compute_url_map.site "projects/$P/global/urlMaps/$B"
imp google_compute_url_map.http_redirect "projects/$P/global/urlMaps/$B-http-redirect"
imp google_compute_target_https_proxy.site "projects/$P/global/targetHttpsProxies/$B"
imp google_compute_target_http_proxy.http_redirect "projects/$P/global/targetHttpProxies/$B-http"
imp google_compute_global_forwarding_rule.https_ipv4 "projects/$P/global/forwardingRules/$B-https-ipv4"
imp google_compute_global_forwarding_rule.https_ipv6 "projects/$P/global/forwardingRules/$B-https-ipv6"
imp google_compute_global_forwarding_rule.http_ipv4 "projects/$P/global/forwardingRules/$B-http-ipv4"
imp google_compute_global_forwarding_rule.http_ipv6 "projects/$P/global/forwardingRules/$B-http-ipv6"
imp google_iam_workload_identity_pool.github "projects/$P/locations/global/workloadIdentityPools/github"
imp google_iam_workload_identity_pool_provider.github "projects/$P/locations/global/workloadIdentityPools/github/providers/github"
imp google_service_account.releaser "projects/$P/serviceAccounts/$SA"
imp google_service_account_iam_member.wif "projects/$P/serviceAccounts/$SA roles/iam.workloadIdentityUser principalSet://iam.googleapis.com/projects/$PN/locations/global/workloadIdentityPools/github/attribute.repository/${TF_VAR_github_repository:-yohimik/dispat}"
for R in compute.loadBalancerAdmin dns.admin storage.admin iam.workloadIdentityPoolAdmin iam.serviceAccountAdmin resourcemanager.projectIamAdmin; do
  imp "google_project_iam_member.releaser[\"roles/$R\"]" "$P roles/$R serviceAccount:$SA"
done

if [ -n "${TF_VAR_dns_zone:-}" ]; then
  Z="$TF_VAR_dns_zone"
  imp 'google_dns_record_set.apex_a[0]' "$P/$Z/$DOMAIN./A"
  imp 'google_dns_record_set.apex_aaaa[0]' "$P/$Z/$DOMAIN./AAAA"
  imp 'google_dns_record_set.www[0]' "$P/$Z/www.$DOMAIN./CNAME"
fi

echo "state rebuilt; run tf-plan next"
