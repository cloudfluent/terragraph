# Built-in state stores model apply-time outputs without providers, provisioners, or cloud access.
resource "terraform_data" "this" {
  input = {
    registry = {
      repository_url   = "${var.context.account_id}.dkr.ecr.${var.context.region}.amazonaws.com/commerce"
      owner_account_id = var.context.account_id
    }
    dns_zone             = var.domain
    image_tag_mutability = "IMMUTABLE"
    cross_account_pull   = ["dev", "stg", "prd"]
    audit_archive_arn    = var.telemetry.archive_bucket_arn
    tags                 = var.context.tags
  }

}
