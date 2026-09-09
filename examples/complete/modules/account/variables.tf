variable "organization" {
  description = "Organization for the local account fixture."
  type        = object({ tenant = string, namespace = string, region = string, region_code = string, domain = string, tags = map(string) })
  nullable    = false
}

variable "account" {
  description = "Account for the local account fixture."
  type        = object({ account_id = string, name = string, stage = string })
  nullable    = false

  validation {
    condition     = can(regex("^[0-9]{12}$", var.account.account_id)) && contains(["common", "dev", "stg", "prd"], var.account.stage)
    error_message = "Use a fictional 12-digit account ID and stage common, dev, stg, or prd."
  }
}
