locals {
  availability_zones = length(var.network_config.availability_zones) > 0 ? var.network_config.availability_zones : [for suffix in ["a", "b", "c"] : "${var.context.region}${suffix}"]
  prefix             = "${var.context.tenant}-${var.context.region_code}"
  name               = "${local.prefix}-vpc-${var.context.stage}-${var.network_config.purpose}"
  identity           = "${var.context.account_id}-${local.name}"
}

# Built-in state stores model apply-time outputs without providers, provisioners, or cloud access.
resource "terraform_data" "this" {
  input = {
    network = {
      id                   = "vpc-${substr(sha256(local.identity), 0, 17)}"
      account_id           = var.context.account_id
      region               = var.context.region
      cidr                 = var.network_config.cidr
      private_subnet_ids   = [for az in local.availability_zones : "subnet-${substr(sha256("${local.identity}-${az}"), 0, 17)}"]
      private_subnet_cidrs = [for index, az in local.availability_zones : cidrsubnet(var.network_config.cidr, 4, index)]
    }
    public_subnet_cidrs = var.network_config.nat_gateways > 0 ? [for index, az in local.availability_zones : cidrsubnet(var.network_config.cidr, 8, index + 128)] : []
    nat_gateways        = var.network_config.nat_gateways
    endpoints           = ["s3", "ecr.api", "ecr.dkr", "sts", "logs", "secretsmanager"]
    tags                = merge(var.context.tags, { Name = local.name, Attributes = var.network_config.purpose })
  }
  triggers_replace = [var.network_config.cidr]

  lifecycle {
    precondition {
      condition     = alltrue([for az in local.availability_zones : startswith(az, var.context.region)])
      error_message = "Availability zones must belong to the account context region; correct network_config.availability_zones."
    }
  }
}
