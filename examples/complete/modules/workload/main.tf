# Built-in state stores model apply-time outputs without providers, provisioners, or cloud access.
resource "terraform_data" "this" {
  input = {
    service = {
      name         = var.service_config.name
      url          = "https://${var.service_config.name}.${var.context.stage}.${var.dns_zone}"
      image        = "${var.registry.repository_url}/${var.service_config.name}:${var.service_config.image_tag}"
      replicas     = var.service_config.replicas
      account_id   = var.context.account_id
      cluster_name = var.cluster.name
    }
    namespace            = var.identity.namespace
    service_account      = var.identity.service_account
    role_arn             = var.identity.role_arn
    database_endpoint    = var.database.endpoint
    database_credentials = var.credentials
    cache_endpoint       = var.cache.endpoint
    queue_url            = var.queue.url
    tags                 = var.context.tags
  }

  lifecycle {
    precondition {
      condition     = var.context.account_id == var.cluster.account_id
      error_message = "Cluster belongs to another account; connect this environment's cluster export."
    }
    precondition {
      condition     = var.addons.cluster_name == var.cluster.name && var.identity.cluster_name == var.cluster.name && var.identity.account_id == var.context.account_id
      error_message = "Add-ons and pod identity must target this cluster; correct the platform edges."
    }
    precondition {
      condition     = var.connectivity.account_id == var.context.account_id && var.connectivity.apps_vpc_id == var.cluster.vpc_id && var.connectivity.data_vpc_id == var.database.vpc_id && var.cache.vpc_id == var.database.vpc_id && var.database.account_id == var.context.account_id && var.cache.account_id == var.context.account_id && var.queue.account_id == var.context.account_id
      error_message = "Workload dependencies cross an account or VPC boundary; connect this environment's data and connectivity exports."
    }
  }
}
