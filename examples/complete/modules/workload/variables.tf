variable "context" {
  description = "Context for the local workload fixture."
  type        = object({ account_id = string, account_name = string, tenant = string, namespace = string, stage = string, region = string, region_code = string, tags = map(string) })
  nullable    = false
}

variable "cluster" {
  description = "Cluster for the local workload fixture."
  type        = object({ name = string, arn = string, endpoint = string, oidc_provider_arn = string, account_id = string, vpc_id = string, version = string })
  nullable    = false
}

variable "addons" {
  description = "Addons for the local workload fixture."
  type        = object({ cluster_name = string, controllers = list(string) })
  nullable    = false
}

variable "connectivity" {
  description = "Connectivity for the local workload fixture."
  type        = object({ apps_vpc_id = string, data_vpc_id = string, account_id = string, route_domain = string, transit_gateway_id = string })
  nullable    = false
}

variable "database" {
  description = "Database for the local workload fixture."
  type        = object({ endpoint = string, port = number, name = string, account_id = string, vpc_id = string })
  nullable    = false
}

variable "credentials" {
  description = "Credentials for the local workload fixture."
  type        = object({ username = string, password = string })
  nullable    = false
  sensitive   = true
}

variable "cache" {
  description = "Cache for the local workload fixture."
  type        = object({ endpoint = string, port = number, account_id = string, vpc_id = string })
  nullable    = false
}

variable "queue" {
  description = "Queue for the local workload fixture."
  type        = object({ arn = string, url = string, account_id = string })
  nullable    = false
}

variable "identity" {
  description = "Identity for the local workload fixture."
  type        = object({ role_arn = string, namespace = string, service_account = string, account_id = string, cluster_name = string })
  nullable    = false
}

variable "registry" {
  description = "Registry for the local workload fixture."
  type        = object({ repository_url = string, owner_account_id = string })
  nullable    = false
}

variable "dns_zone" {
  description = "Dns zone for the local workload fixture."
  type        = string
  nullable    = false
}

variable "service_config" {
  description = "Service config for the local workload fixture."
  type        = object({ name = string, image_tag = string, replicas = number })
  nullable    = false

  validation {
    condition     = contains(["checkout", "payments"], var.service_config.name) && var.service_config.replicas >= 1 && length(var.service_config.image_tag) > 0
    error_message = "Use checkout or payments, at least one replica, and a nonempty immutable fixture image tag."
  }
}
