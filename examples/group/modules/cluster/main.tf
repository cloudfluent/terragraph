terraform {
  required_providers {
    random = {
      source  = "hashicorp/random"
      version = "~> 3.6"
    }
  }
}

resource "random_id" "cluster" {
  byte_length = 4
  keepers = {
    vpc_id = var.vpc_id
  }
}
