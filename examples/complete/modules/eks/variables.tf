variable "context" {
  description = "Context for the local eks fixture."
  type        = object({ account_id = string, account_name = string, tenant = string, namespace = string, stage = string, region = string, region_code = string, tags = map(string) })
  nullable    = false
}

variable "network" {
  description = "Network for the local eks fixture."
  type        = object({ id = string, account_id = string, region = string, cidr = string, private_subnet_ids = list(string), private_subnet_cidrs = list(string) })
  nullable    = false
}

variable "telemetry" {
  description = "Telemetry for the local eks fixture."
  type        = object({ archive_bucket_arn = string, kms_key_arn = string, endpoint = string })
  nullable    = false
}

variable "cluster_config" {
  description = "Cluster config for the local eks fixture."
  type        = object({ version = string, private_endpoint = optional(bool, true) })
  nullable    = false
}
