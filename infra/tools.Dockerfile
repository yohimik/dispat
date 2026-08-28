# syntax=docker/dockerfile:1
#
# The release's deployment tools, in one image: gcloud for the docs deploy
# and the state rebuild, terraform for the infra stages — together because
# rebuild.sh interleaves the two in one script. This is the one image a
# release materialises locally (`--load`), because its stages are side
# effects driven through `docker run`: a deploy or an apply must never be a
# cacheable build step, so unlike every gate it cannot be a Dockerfile
# target. CI runners are ephemeral, so the image dies with the job.
#
# The terraform binary is static; copying it out of the official image beats
# a curl-and-checksum dance and keeps both versions pinned in FROM lines.
ARG TERRAFORM_VERSION=1.13
ARG CLOUD_SDK_VERSION=582.0.0
FROM hashicorp/terraform:${TERRAFORM_VERSION} AS terraform

FROM google/cloud-sdk:${CLOUD_SDK_VERSION}-slim
COPY --from=terraform /bin/terraform /usr/local/bin/terraform
