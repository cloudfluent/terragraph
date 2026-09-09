# Built-in state stores model apply-time outputs without providers, provisioners, or cloud access.
resource "terraform_data" "this" {
  input = {
    cache = {
      endpoint   = "${var.context.tenant}-${var.context.stage}.cache.example.invalid"
      port       = 6379
      account_id = var.context.account_id
      vpc_id     = var.network.id
    }
    nodes              = var.data_config.cache_nodes
    transit_encryption = true
    at_rest_encryption = true
    private_subnet_ids = var.network.private_subnet_ids
    tags               = var.context.tags
  }

  lifecycle {
    precondition {
      condition     = var.context.account_id == var.network.account_id && var.context.region == var.network.region
      error_message = "Network belongs to another account or region; connect this environment's network export."
    }
    precondition {
      condition     = var.context.stage != "prd" || var.data_config.cache_nodes >= 2
      error_message = "Production cache needs at least two nodes; increase data_config.cache_nodes."
    }
  }
}
