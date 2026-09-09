# Built-in state stores model apply-time outputs without providers, provisioners, or cloud access.
resource "terraform_data" "this" {
  input = {
    context = {
      account_id   = var.account.account_id
      account_name = var.account.name
      tenant       = var.organization.tenant
      namespace    = var.organization.namespace
      stage        = var.account.stage
      region       = var.organization.region
      region_code  = var.organization.region_code
      tags = merge(var.organization.tags, {
        Namespace   = var.organization.namespace
        Tenant      = var.organization.tenant
        Environment = var.organization.region_code
        Stage       = var.account.stage
        Account     = var.account.name
        ManagedBy   = "terragraph-fixture"
      })
    }
    execution_role_arn = "arn:aws:iam::${var.account.account_id}:role/${var.organization.tenant}-gbl-role-${var.account.stage}-terraform"
  }

}
