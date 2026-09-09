variable "context" {
  description = "Context for the local addons fixture."
  type        = object({ account_id = string, account_name = string, tenant = string, namespace = string, stage = string, region = string, region_code = string, tags = map(string) })
  nullable    = false
}

variable "cluster" {
  description = "Cluster for the local addons fixture."
  type        = object({ name = string, arn = string, endpoint = string, oidc_provider_arn = string, account_id = string, vpc_id = string, version = string })
  nullable    = false
}

variable "telemetry" {
  description = "Telemetry for the local addons fixture."
  type        = object({ archive_bucket_arn = string, kms_key_arn = string, endpoint = string })
  nullable    = false
}
