variable "context" {
  description = "Context for the local node-pool fixture."
  type        = object({ account_id = string, account_name = string, tenant = string, namespace = string, stage = string, region = string, region_code = string, tags = map(string) })
  nullable    = false
}

variable "cluster" {
  description = "Cluster for the local node-pool fixture."
  type        = object({ name = string, arn = string, endpoint = string, oidc_provider_arn = string, account_id = string, vpc_id = string, version = string })
  nullable    = false
}

variable "pool_config" {
  description = "Pool config for the local node-pool fixture."
  type        = object({ name = string, capacity_type = string, min_size = number, desired_size = number, max_size = number })
  nullable    = false

  validation {
    condition     = contains(["ON_DEMAND", "SPOT"], var.pool_config.capacity_type) && var.pool_config.min_size >= 1 && var.pool_config.min_size <= var.pool_config.desired_size && var.pool_config.desired_size <= var.pool_config.max_size
    error_message = "Use ON_DEMAND or SPOT and require 1 <= min_size <= desired_size <= max_size."
  }
}
