variable "context" {
  description = "Context for the local vpc fixture."
  type        = object({ account_id = string, account_name = string, tenant = string, namespace = string, stage = string, region = string, region_code = string, tags = map(string) })
  nullable    = false
}

variable "network_config" {
  description = "Network config for the local vpc fixture."
  type        = object({ purpose = string, cidr = string, availability_zones = optional(list(string), []), nat_gateways = optional(number, 0) })
  nullable    = false

  validation {
    condition     = can(cidrsubnet(var.network_config.cidr, 8, 130)) && (length(var.network_config.availability_zones) == 0 || (length(var.network_config.availability_zones) >= 2 && length(var.network_config.availability_zones) <= 3)) && length(distinct(var.network_config.availability_zones)) == length(var.network_config.availability_zones) && contains([0, 1, length(var.network_config.availability_zones) == 0 ? 3 : length(var.network_config.availability_zones)], var.network_config.nat_gateways)
    error_message = "Provide an IPv4 CIDR with room for subnets, two or three distinct AZs, and zero, one, or one NAT gateway per AZ."
  }
}
