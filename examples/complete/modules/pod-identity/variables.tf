variable "context" {
  description = "Context for the local pod-identity fixture."
  type        = object({ account_id = string, account_name = string, tenant = string, namespace = string, stage = string, region = string, region_code = string, tags = map(string) })
  nullable    = false
}

variable "cluster" {
  description = "Cluster for the local pod-identity fixture."
  type        = object({ name = string, arn = string, endpoint = string, oidc_provider_arn = string, account_id = string, vpc_id = string, version = string })
  nullable    = false
}

variable "queue" {
  description = "Queue for the local pod-identity fixture."
  type        = object({ arn = string, url = string, account_id = string })
  nullable    = false
}

variable "service_config" {
  description = "Service config for the local pod-identity fixture."
  type        = object({ name = string, image_tag = string, replicas = number })
  nullable    = false
}
