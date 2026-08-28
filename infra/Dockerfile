# syntax=docker/dockerfile:1
#
# The infra package's static gate, containerised: format and validity of the
# Terraform configuration, with no cloud credentials and no state. This is
# what the vet sweep builds; the plan and apply stages talk to Google Cloud
# and therefore run as `docker run` against the tools image (tools.Dockerfile
# beside this file) — a side effect must never be a cacheable build step.
#
# The provider download layer is keyed on versions.tf alone, so editing a
# resource re-runs only the validation.
ARG TERRAFORM_VERSION=1.13
FROM hashicorp/terraform:${TERRAFORM_VERSION} AS init
WORKDIR /infra
COPY versions.tf ./
RUN terraform init -backend=false -input=false >/dev/null

FROM init AS validate
COPY . .
RUN terraform fmt -check -recursive
RUN terraform init -backend=false -input=false >/dev/null \
 && terraform validate
