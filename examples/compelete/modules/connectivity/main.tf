# Built-in state stores model apply-time outputs without providers, provisioners, or cloud access.
resource "terraform_data" "this" {
  input = {
    connectivity = {
      apps_vpc_id        = var.apps_network.id
      data_vpc_id        = var.data_network.id
      account_id         = var.context.account_id
      route_domain       = var.context.stage
      transit_gateway_id = var.transit.id
    }
    attachments = [var.apps_network.id, var.data_network.id]
    routes = {
      apps_to_data = var.data_network.cidr
      data_to_apps = var.apps_network.cidr
    }
    cross_environment_routes = []
    share_owner_account_id   = var.transit.owner_account_id
  }

  lifecycle {
    precondition {
      condition     = var.context.account_id == var.apps_network.account_id && var.context.account_id == var.data_network.account_id
      error_message = "Both VPCs must belong to this account; connect the local apps and data networks."
    }
    precondition {
      condition     = var.context.region == var.transit.region && var.apps_network.region == var.data_network.region
      error_message = "Transit and VPC regions must agree; select the regional transit export."
    }
    precondition {
      condition     = var.apps_network.id != var.data_network.id && var.apps_network.cidr != var.data_network.cidr
      error_message = "Use separate apps and data VPCs with non-overlapping CIDRs; correct the network inputs."
    }
  }
}
