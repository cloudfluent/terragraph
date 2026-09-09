locals {
  prefix = "${var.context.tenant}-${var.context.region_code}"
}

# Built-in state stores model apply-time outputs without providers, provisioners, or cloud access.
resource "terraform_data" "this" {
  input = {
    identity = {
      role_arn        = "arn:aws:iam::${var.context.account_id}:role/${local.prefix}-role-${var.context.stage}-eks-${var.service_config.name}"
      namespace       = var.service_config.name
      service_account = var.service_config.name
      account_id      = var.context.account_id
      cluster_name    = var.cluster.name
    }
    trust_service   = "pods.eks.amazonaws.com"
    allowed_actions = var.service_config.name == "checkout" ? ["sqs:SendMessage"] : ["sqs:ReceiveMessage", "sqs:DeleteMessage", "sqs:GetQueueAttributes"]
    queue_arn       = var.queue.arn
    tags            = merge(var.context.tags, { Name = "${local.prefix}-role-${var.context.stage}-eks-${var.service_config.name}", Attributes = var.service_config.name })
  }

  lifecycle {
    precondition {
      condition     = var.context.account_id == var.cluster.account_id
      error_message = "Cluster belongs to another account; connect this environment's cluster export."
    }
    precondition {
      condition     = var.queue.account_id == var.context.account_id
      error_message = "Queue belongs to another account; connect the environment queue."
    }
  }
}
