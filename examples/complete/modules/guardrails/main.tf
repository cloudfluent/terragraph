# Built-in state stores model apply-time outputs without providers, provisioners, or cloud access.
resource "terraform_data" "this" {
  input = {
    context = var.context
    controls = {
      cloudtrail          = true
      config_recorder     = true
      guardduty           = true
      deny_public_buckets = true
      require_encryption  = true
      allowed_regions     = [var.context.region]
    }
  }

}
