terraform {
  required_providers {
    local = {
      source  = "hashicorp/local"
      version = "~> 2.5"
    }
  }
}

resource "local_file" "wired_value" {
  filename = "${path.module}/vpc_id.txt"
  content  = var.vpc_id
}
