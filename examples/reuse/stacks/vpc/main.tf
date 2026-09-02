terraform {
  required_providers {
    random = {
      source  = "hashicorp/random"
      version = "~> 3.6"
    }
  }

  # Empty on purpose: this is what makes the module reusable. Terraform's
  # partial backend configuration lets terragraph fill in `path` per node
  # via `terraform init -backend-config=...` at runtime — no code
  # generation, no hardcoded state location baked into the module.
  backend "local" {}
}

resource "random_id" "vpc" {
  byte_length = 4
}
