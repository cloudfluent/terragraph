locals {
  prefix = "${var.context.tenant}-${var.context.region_code}"
  name   = "${local.prefix}-rds-${var.context.stage}-commerce"
}

# Built-in state stores model apply-time outputs without providers, provisioners, or cloud access.
resource "terraform_data" "this" {
  input = {
    database = {
      endpoint   = "${local.name}.rds.example.invalid"
      port       = 5432
      name       = "commerce"
      account_id = var.context.account_id
      vpc_id     = var.network.id
    }
    credentials = {
      username = "fixture_app"
      password = "fixture-only-${var.context.stage}-not-a-real-password"
    }
    private_subnet_ids    = var.network.private_subnet_ids
    multi_az              = var.data_config.multi_az
    backup_retention_days = var.data_config.backup_retention_days
    encrypted             = true
    publicly_accessible   = false
    tags                  = merge(var.context.tags, { Name = local.name, Attributes = "commerce" })
  }

  lifecycle {
    precondition {
      condition     = var.context.account_id == var.network.account_id && var.context.region == var.network.region
      error_message = "Network belongs to another account or region; connect this environment's network export."
    }
    precondition {
      condition     = var.context.stage != "prd" || (var.data_config.multi_az && var.data_config.backup_retention_days >= 7)
      error_message = "Production database requires multi-AZ and at least seven backup days; update data_config."
    }
  }
}
