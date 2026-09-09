variable "context" {
  description = "Context for the local cache fixture."
  type        = object({ account_id = string, account_name = string, tenant = string, namespace = string, stage = string, region = string, region_code = string, tags = map(string) })
  nullable    = false
}

variable "network" {
  description = "Network for the local cache fixture."
  type        = object({ id = string, account_id = string, region = string, cidr = string, private_subnet_ids = list(string), private_subnet_cidrs = list(string) })
  nullable    = false
}

variable "data_config" {
  description = "Data config for the local cache fixture."
  type        = object({ multi_az = bool, backup_retention_days = number, cache_nodes = number })
  nullable    = false
}
