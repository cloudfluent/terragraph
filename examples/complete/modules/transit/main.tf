# Built-in state stores model apply-time outputs without providers, provisioners, or cloud access.
resource "terraform_data" "this" {
  input = {
    transit = {
      id               = "tgw-${substr(sha256(var.network.id), 0, 17)}"
      owner_account_id = var.context.account_id
      region           = var.context.region
    }
    hub_vpc_id                      = var.network.id
    ram_principals                  = ["organization"]
    default_route_table_association = false
    default_route_table_propagation = false
    route_domains                   = ["dev", "stg", "prd"]
  }

  lifecycle {
    precondition {
      condition     = var.context.account_id == var.network.account_id && var.context.region == var.network.region
      error_message = "Network belongs to another account or region; connect this environment's network export."
    }
  }
}
