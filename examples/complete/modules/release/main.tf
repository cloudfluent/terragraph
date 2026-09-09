# Built-in state stores model apply-time outputs without providers, provisioners, or cloud access.
resource "terraform_data" "this" {
  input = {
    release = {
      stage        = var.context.stage
      account_id   = var.context.account_id
      cluster_name = var.cluster.name
      apps_vpc_id  = var.connectivity.apps_vpc_id
      data_vpc_id  = var.connectivity.data_vpc_id
      services     = [var.checkout.url, var.payments.url]
    }
  }

  lifecycle {
    precondition {
      condition     = var.checkout.account_id == var.context.account_id && var.payments.account_id == var.context.account_id && var.checkout.cluster_name == var.cluster.name && var.payments.cluster_name == var.cluster.name
      error_message = "Both applications must be released into this account and cluster; correct the service exports."
    }
  }
}
