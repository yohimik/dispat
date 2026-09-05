# Infrastructure-order fixture

This disposable fixture represents Terraform as an ordinary versioned package. Like the repository's real infra flow,
its build stage reconstructs Terraform state by representing imports of known resources and then saves a local
`terraform plan`. Its publish stage records a local `terraform apply`. The temporary `infra/terraform.tfstate` stands
for Terraform's own working state and disappears with the fixture. Dispat adds no progress database, cache, or state
bucket; its durable completion record is the `infra/v1.3.0` Git tag.

The marker commands never invoke Terraform or a cloud API. They model rebuild/import, plan, and apply only to let
`verify-infra.py` prove that backend and frontend both wait for the infrastructure apply, then build independently.
