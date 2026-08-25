# No state is stored anywhere. Each run regenerates terraform.tfstate
# beside this configuration by importing what actually exists (rebuild.sh,
# one import per declared resource), and the file is gitignored and
# disposable: in CI it dies with the release job, locally it feeds an
# inspection plan. It records every resource attribute, including the
# identifiers this repository deliberately does not carry, which is why it
# must never be committed.
terraform {
  required_version = ">= 1.9"

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 6.0"
    }
  }
}

provider "google" {
  project = var.project_id
}
