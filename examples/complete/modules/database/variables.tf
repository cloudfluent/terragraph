variable "context" {
  description = "Context for the local database fixture."
  type        = object({ account_id = string, account_name = string, tenant = string, namespace = string, stage = string, region = string, region_code = string, tags = map(string) })
  nullable    = false
}

variable "network" {
  description = "Network for the local database fixture."
  type        = object({ id = string, account_id = string, region = string, cidr = string, private_subnet_ids = list(string), private_subnet_cidrs = list(string) })
  nullable    = false
}

variable "data_config" {
  description = "Data config for the local database fixture."
  type        = object({ multi_az = bool, backup_retention_days = number, cache_nodes = number })
  nullable    = false

  validation {
    condition     = var.data_config.backup_retention_days >= 1 && var.data_config.backup_retention_days <= 35 && var.data_config.cache_nodes >= 1
    error_message = "Use 1-35 backup days and at least one cache node."
  }
}
