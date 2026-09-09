variable "context" {
  description = "Context for the local shared-services fixture."
  type        = object({ account_id = string, account_name = string, tenant = string, namespace = string, stage = string, region = string, region_code = string, tags = map(string) })
  nullable    = false
}

variable "telemetry" {
  description = "Telemetry for the local shared-services fixture."
  type        = object({ archive_bucket_arn = string, kms_key_arn = string, endpoint = string })
  nullable    = false
}

variable "domain" {
  description = "Domain for the local shared-services fixture."
  type        = string
  nullable    = false
}
