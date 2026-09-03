terraform {
  required_providers {
    local = {
      source  = "hashicorp/local"
      version = "~> 2.5"
    }
  }

  backend "local" {}
}

resource "local_file" "wired_value" {
  filename = "${path.module}/${var.cluster_id}.txt"
  content  = var.cluster_id
}
