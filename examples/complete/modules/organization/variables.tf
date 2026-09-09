variable "organization" {
  description = "Organization for the local organization fixture."
  type        = object({ tenant = string, namespace = string, region = string, region_code = string, domain = string, tags = map(string) })
  nullable    = false
}
