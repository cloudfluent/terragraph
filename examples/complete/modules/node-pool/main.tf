# Built-in state stores model apply-time outputs without providers, provisioners, or cloud access.
resource "terraform_data" "this" {
  input = {
    pool = {
      name          = "${var.cluster.name}-${var.pool_config.name}"
      cluster_name  = var.cluster.name
      desired_size  = var.pool_config.desired_size
      capacity_type = var.pool_config.capacity_type
    }
    scaling        = { min = var.pool_config.min_size, max = var.pool_config.max_size }
    instance_types = ["m7i.large", "m7a.large"]
    tags           = merge(var.context.tags, { Name = "${var.cluster.name}-${var.pool_config.name}", Attributes = var.pool_config.name })
  }

  lifecycle {
    precondition {
      condition     = var.context.account_id == var.cluster.account_id
      error_message = "Cluster belongs to another account; connect this environment's cluster export."
    }
  }
}
