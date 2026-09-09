variable "context" {
  description = "Context for the local connectivity fixture."
  type        = object({ account_id = string, account_name = string, tenant = string, namespace = string, stage = string, region = string, region_code = string, tags = map(string) })
  nullable    = false
}

variable "apps_network" {
  description = "Apps network for the local connectivity fixture."
  type        = object({ id = string, account_id = string, region = string, cidr = string, private_subnet_ids = list(string), private_subnet_cidrs = list(string) })
  nullable    = false
}

variable "data_network" {
  description = "Data network for the local connectivity fixture."
  type        = object({ id = string, account_id = string, region = string, cidr = string, private_subnet_ids = list(string), private_subnet_cidrs = list(string) })
  nullable    = false
}

variable "transit" {
  description = "Transit for the local connectivity fixture."
  type        = object({ id = string, owner_account_id = string, region = string })
  nullable    = false
}
