terraform {
  required_version = ">= 1.8.0"
}

resource "terraform_data" "application_database" {
  input = "application-database"
}
