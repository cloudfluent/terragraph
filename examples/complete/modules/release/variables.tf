variable "context" {
  description = "Context for the local release fixture."
  type        = object({ account_id = string, account_name = string, tenant = string, namespace = string, stage = string, region = string, region_code = string, tags = map(string) })
  nullable    = false
}

variable "cluster" {
  description = "Cluster for the local release fixture."
  type        = object({ name = string, arn = string, endpoint = string, oidc_provider_arn = string, account_id = string, vpc_id = string, version = string })
  nullable    = false
}

variable "connectivity" {
  description = "Connectivity for the local release fixture."
  type        = object({ apps_vpc_id = string, data_vpc_id = string, account_id = string, route_domain = string, transit_gateway_id = string })
  nullable    = false
}

variable "checkout" {
  description = "Checkout for the local release fixture."
  type        = object({ name = string, url = string, image = string, replicas = number, account_id = string, cluster_name = string })
  nullable    = false
}

variable "payments" {
  description = "Payments for the local release fixture."
  type        = object({ name = string, url = string, image = string, replicas = number, account_id = string, cluster_name = string })
  nullable    = false
}
