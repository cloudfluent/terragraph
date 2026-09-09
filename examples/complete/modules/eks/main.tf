locals {
  prefix = "${var.context.tenant}-${var.context.region_code}"
  name   = "${local.prefix}-eks-${var.context.stage}-commerce"
}

# Built-in state stores model apply-time outputs without providers, provisioners, or cloud access.
resource "terraform_data" "this" {
  input = {
    cluster = {
      name              = local.name
      arn               = "arn:aws:eks:${var.context.region}:${var.context.account_id}:cluster/${local.name}"
      endpoint          = "https://${local.name}.eks.example.invalid"
      oidc_provider_arn = "arn:aws:iam::${var.context.account_id}:oidc-provider/oidc.eks.${var.context.region}.amazonaws.com/id/${upper(substr(sha256(local.name), 0, 32))}"
      account_id        = var.context.account_id
      vpc_id            = var.network.id
      version           = var.cluster_config.version
    }
    private_subnet_ids         = var.network.private_subnet_ids
    endpoint_private_access    = var.cluster_config.private_endpoint
    endpoint_public_access     = !var.cluster_config.private_endpoint
    secrets_encryption_key_arn = var.telemetry.kms_key_arn
    control_plane_logs         = ["api", "audit", "authenticator", "controllerManager", "scheduler"]
    tags                       = merge(var.context.tags, { Name = local.name, Attributes = "commerce" })
  }

  lifecycle {
    precondition {
      condition     = var.context.account_id == var.network.account_id && var.context.region == var.network.region
      error_message = "Network belongs to another account or region; connect this environment's network export."
    }
    precondition {
      condition     = var.context.stage != "prd" || var.cluster_config.private_endpoint
      error_message = "Production clusters require private endpoints; set cluster_config.private_endpoint to true."
    }
  }
}
