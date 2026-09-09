locals {
  prefix = "${var.context.tenant}-${var.context.region_code}"
}

# Built-in state stores model apply-time outputs without providers, provisioners, or cloud access.
resource "terraform_data" "this" {
  input = {
    telemetry = {
      archive_bucket_arn = "arn:aws:s3:::${local.prefix}-s3-common-audit-${var.context.account_id}"
      kms_key_arn        = "arn:aws:kms:${var.context.region}:${var.context.account_id}:key/00000000-0000-4000-8000-000000000001"
      endpoint           = "https://telemetry.example.invalid"
    }
    retention_days = 365
    tags           = merge(var.context.tags, { Name = "${local.prefix}-s3-common-audit-${var.context.account_id}", Attributes = "audit" })
  }

}
